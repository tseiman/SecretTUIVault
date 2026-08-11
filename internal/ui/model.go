package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	internalgpg "github.com/tseiman/SecretTUIVault/internal/gpg"
	"github.com/tseiman/SecretTUIVault/internal/search"
	"github.com/tseiman/SecretTUIVault/internal/vault"
)

type saver interface {
	Save(vault.Document, bool) error
}

type vaultMetadata interface {
	Path() string
	Size() (int64, error)
}

type mode uint8

type clipboardResultMsg struct {
	value string
	err   error
}

type decryptResultMsg struct {
	id     uint64
	result internalgpg.Result
	err    error
}

const (
	modeList mode = iota
	modeView
	modeBlobCopy
	modeForm
	modeDelete
	modeConflict
	modeTags
	modeNewTag
	modeQuitConfirm
	modeDecrypting
	modeDecrypted
)

type formModel struct {
	name            textinput.Model
	tags            textinput.Model
	description     textarea.Model
	blob            textarea.Model
	blobOriginal    string
	blobInitial     string
	blobDirty       bool
	blobPasted      string
	blobPasteActive bool
	focus           int
	editID          string
}

type Model struct {
	doc                vault.Document
	store              saver
	metadata           vaultMetadata
	homeDir            string
	visible            []vault.Entry
	selected           int
	listOffset         int
	ascending          bool
	query              textinput.Model
	detail             viewport.Model
	decryptedView      viewport.Model
	form               formModel
	tagOptions         []string
	tagOrder           []string
	tagSelected        map[string]bool
	tagCursor          int
	tagOffset          int
	tagAscending       bool
	newTag             textinput.Model
	mode               mode
	returnMode         mode
	escapePrefix       bool
	pending            *vault.Document
	status             string
	width              int
	height             int
	gitVersion         string
	writeClipboard     func(string) error
	decrypt            func(context.Context, []byte) (internalgpg.Result, error)
	decryptCancel      context.CancelFunc
	decryptID          uint64
	decryptSignature   internalgpg.SignatureStatus
	decryptedPlaintext string
	manualBlob         string
	now                func() time.Time
}

func New(doc vault.Document, store saver) Model {
	q := textinput.New()
	q.KeyMap.Paste.SetEnabled(false)
	q.Prompt = "Search: "
	q.Placeholder = `text, TAG:value, NAME:"words", AND`
	d := viewport.New(40, 10)
	decrypted := viewport.New(40, 10)
	homeDir, _ := os.UserHomeDir()
	gpgRunner := internalgpg.NewRunner()
	m := Model{doc: doc, store: store, homeDir: homeDir, ascending: true, tagAscending: true, query: q, detail: d, decryptedView: decrypted, width: 80, height: 24, gitVersion: detectGitVersion(), writeClipboard: clipboard.WriteAll, decrypt: gpgRunner.Decrypt, now: time.Now}
	if metadata, ok := store.(vaultMetadata); ok {
		m.metadata = metadata
	}
	m.refresh("")
	return m
}

func (m Model) Init() tea.Cmd { return nil }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if result, ok := msg.(clipboardResultMsg); ok {
		if result.err == nil {
			m.status = "In Zwischenablage kopiert"
			return m, nil
		}
		m.status = fmt.Sprintf("Zwischenablage nicht verfügbar: %v; manueller Kopiermodus geöffnet", result.err)
		m.manualBlob = result.value
		m.mode = modeBlobCopy
		m.resizeWidgets()
		return m, nil
	}
	if result, ok := msg.(decryptResultMsg); ok {
		if result.id != m.decryptID {
			return m, nil
		}
		m.decryptCancel = nil
		if result.err != nil {
			m.mode = m.returnMode
			m.decryptedPlaintext = ""
			m.status = "GPG decryption failed: " + result.err.Error()
			return m, nil
		}
		m.decryptedPlaintext = result.result.Plaintext
		m.decryptSignature = result.result.Signature
		m.decryptedView.SetContent(safeMultiline(result.result.Plaintext))
		m.decryptedView.GotoTop()
		m.mode, m.status = modeDecrypted, ""
		m.resizeWidgets()
		return m, nil
	}
	if size, ok := msg.(tea.WindowSizeMsg); ok {
		m.width, m.height = max(20, size.Width), max(8, size.Height)
		m.resizeWidgets()
		return m, nil
	}
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	if m.mode == modeList && !m.query.Focused() {
		if key.Alt {
			if alias, ok := escapeFunctionKey(key); ok {
				key = alias
			}
		}
		if m.escapePrefix {
			m.escapePrefix = false
			if alias, ok := escapeFunctionKey(key); ok {
				key = alias
			}
		}
		if key.Type == tea.KeyEsc {
			m.escapePrefix = true
			return m, nil
		}
	}
	if key.Type == tea.KeyF10 {
		if m.mode == modeDecrypting && m.decryptCancel != nil {
			m.decryptCancel()
			m.decryptCancel = nil
		}
		if m.mode == modeForm || m.mode == modeConflict || m.mode == modeTags || m.mode == modeNewTag {
			m.returnMode, m.mode = m.mode, modeQuitConfirm
			return m, nil
		}
		return m, tea.Quit
	}
	switch m.mode {
	case modeForm:
		return m.updateForm(key)
	case modeDelete:
		if key.Type == tea.KeyEsc || strings.EqualFold(key.String(), "n") {
			m.mode, m.status = modeList, "Deletion cancelled"
		} else if strings.EqualFold(key.String(), "y") {
			m.deleteSelected()
		}
		return m, nil
	case modeConflict:
		if key.Type == tea.KeyEnter || key.Type == tea.KeyEsc || strings.EqualFold(key.String(), "c") {
			m.mode, m.pending, m.status = modeList, nil, "Save cancelled; external changes preserved"
		} else if strings.EqualFold(key.String(), "o") {
			m.forcePending()
		}
		return m, nil
	case modeTags:
		return m.updateTags(key)
	case modeNewTag:
		return m.updateNewTag(key)
	case modeQuitConfirm:
		if strings.EqualFold(key.String(), "q") || strings.EqualFold(key.String(), "y") {
			return m, tea.Quit
		}
		if key.Type == tea.KeyEnter || key.Type == tea.KeyEsc || strings.EqualFold(key.String(), "c") {
			m.mode = m.returnMode
		}
		return m, nil
	case modeDecrypting:
		if key.Type == tea.KeyEsc {
			if m.decryptCancel != nil {
				m.decryptCancel()
			}
			m.decryptCancel = nil
			m.decryptID++
			m.mode, m.status = m.returnMode, "GPG decryption cancelled"
		}
		return m, nil
	case modeDecrypted:
		if key.Type == tea.KeyEsc || key.Type == tea.KeyEnter {
			m.mode = m.returnMode
			m.decryptedPlaintext = ""
			m.decryptedView.SetContent("")
			m.resizeWidgets()
			return m, nil
		}
		var cmd tea.Cmd
		m.decryptedView, cmd = m.decryptedView.Update(key)
		return m, cmd
	case modeView:
		if key.Type == tea.KeyEsc || key.Type == tea.KeyEnter || key.Type == tea.KeyF3 {
			m.mode = modeList
			m.resizeWidgets()
			return m, nil
		}
		if strings.EqualFold(key.String(), "b") {
			m.returnMode = modeView
			return m, m.copyBlobCmd()
		}
		if strings.EqualFold(key.String(), "d") {
			return m.startDecrypt(modeView)
		}
		var cmd tea.Cmd
		m.detail, cmd = m.detail.Update(key)
		return m, cmd
	case modeBlobCopy:
		if key.Type == tea.KeyEsc || key.Type == tea.KeyEnter || strings.EqualFold(key.String(), "b") {
			m.mode = m.returnMode
			m.manualBlob = ""
			m.resizeWidgets()
		}
		return m, nil
	}
	if m.query.Focused() {
		switch key.Type {
		case tea.KeyEsc, tea.KeyEnter:
			m.query.Blur()
			return m, nil
		case tea.KeyUp:
			m.move(-1)
			return m, nil
		case tea.KeyDown:
			m.move(1)
			return m, nil
		}
		var cmd tea.Cmd
		m.query, cmd = m.query.Update(key)
		m.refresh(m.selectedID())
		return m, cmd
	}
	switch key.Type {
	case tea.KeyUp:
		m.move(-1)
	case tea.KeyDown:
		m.move(1)
	case tea.KeyF3, tea.KeyEnter:
		if len(m.visible) > 0 {
			m.mode = modeView
			m.resizeWidgets()
		}
	case tea.KeyF4:
		if len(m.visible) > 0 {
			m.openForm(m.selectedEntry())
		}
	case tea.KeyF5:
		m.openForm(vault.Entry{})
	case tea.KeyF8:
		if len(m.visible) > 0 {
			m.mode = modeDelete
		}
	case tea.KeyRunes:
		switch key.String() {
		case "/":
			m.query.Focus()
		case "b", "B":
			if len(m.visible) > 0 {
				m.returnMode = modeList
				return m, m.copyBlobCmd()
			}
		case "d", "D":
			if len(m.visible) > 0 {
				return m.startDecrypt(modeList)
			}
		case "s", "S":
			id := m.selectedID()
			m.ascending = !m.ascending
			m.refresh(id)
		}
	}
	return m, nil
}

func escapeFunctionKey(key tea.KeyMsg) (tea.KeyMsg, bool) {
	if key.Type != tea.KeyRunes || len(key.Runes) != 1 {
		return tea.KeyMsg{}, false
	}
	aliases := map[rune]tea.KeyType{
		'3': tea.KeyF3,
		'4': tea.KeyF4,
		'5': tea.KeyF5,
		'8': tea.KeyF8,
		'0': tea.KeyF10,
	}
	keyType, ok := aliases[key.Runes[0]]
	return tea.KeyMsg{Type: keyType}, ok
}

func (m Model) copyBlobCmd() tea.Cmd {
	value := m.selectedEntry().Blob
	write := m.writeClipboard
	return func() tea.Msg {
		return clipboardResultMsg{value: value, err: write(value)}
	}
}

func (m Model) startDecrypt(returnMode mode) (tea.Model, tea.Cmd) {
	ctx, cancel := context.WithCancel(context.Background())
	m.decryptID++
	id := m.decryptID
	input := append([]byte(nil), []byte(m.selectedEntry().Blob)...)
	decrypt := m.decrypt
	m.returnMode = returnMode
	m.decryptCancel = cancel
	m.decryptedPlaintext = ""
	m.mode, m.status = modeDecrypting, "Waiting for GPG Pinentry"
	return m, func() tea.Msg {
		result, err := decrypt(ctx, input)
		return decryptResultMsg{id: id, result: result, err: err}
	}
}

func (m *Model) updateForm(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if key.Type == tea.KeyEsc {
		m.mode, m.status = modeList, "Edit cancelled"
		return *m, nil
	}
	if key.Type == tea.KeyCtrlS {
		m.saveForm()
		return *m, nil
	}
	if key.Type == tea.KeyCtrlT {
		m.openTagPicker()
		return *m, nil
	}
	if key.Type == tea.KeyTab || key.Type == tea.KeyShiftTab {
		delta := 1
		if key.Type == tea.KeyShiftTab {
			delta = -1
		}
		m.form.focus = (m.form.focus + delta + 4) % 4
		m.focusForm()
		return *m, nil
	}
	var cmd tea.Cmd
	switch m.form.focus {
	case 0:
		m.form.name, cmd = m.form.name.Update(key)
	case 1:
		m.form.tags, cmd = m.form.tags.Update(key)
	case 2:
		m.form.description, cmd = m.form.description.Update(key)
	case 3:
		if key.Paste {
			m.form.blobPasted = string(key.Runes)
			m.form.blobPasteActive = true
			m.form.blobDirty = true
			m.form.blob.SetValue(m.form.blobPasted)
			return *m, nil
		}
		before := m.form.blob.Value()
		m.form.blob, cmd = m.form.blob.Update(key)
		if m.form.blob.Value() != before {
			m.form.blobDirty = true
			m.form.blobPasteActive = false
		}
	}
	return *m, cmd
}

func (m *Model) openTagPicker() {
	m.tagOptions = append([]string(nil), m.doc.CanonicalTags()...)
	current := vault.CanonicalizeTags(splitTags(m.form.tags.Value()), m.tagOptions)
	m.tagSelected = make(map[string]bool, len(current))
	for _, tag := range current {
		key := strings.ToLower(tag)
		m.tagSelected[key] = true
		found := false
		for _, option := range m.tagOptions {
			if strings.EqualFold(option, tag) {
				found = true
				break
			}
		}
		if !found {
			m.tagOptions = append(m.tagOptions, tag)
		}
	}
	m.tagOrder = append([]string(nil), m.tagOptions...)
	m.sortTagOptions("")
	m.mode, m.status = modeTags, ""
}

func (m *Model) sortTagOptions(preserve string) {
	sort.SliceStable(m.tagOptions, func(i, j int) bool {
		left, right := strings.ToLower(m.tagOptions[i]), strings.ToLower(m.tagOptions[j])
		if left == right {
			left, right = m.tagOptions[i], m.tagOptions[j]
		}
		if m.tagAscending {
			return left < right
		}
		return left > right
	})
	m.tagCursor, m.tagOffset = 0, 0
	for i, tag := range m.tagOptions {
		if preserve != "" && strings.EqualFold(tag, preserve) {
			m.tagCursor = i
			break
		}
	}
	m.ensureTagVisible()
}

func (m *Model) updateTags(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.Type {
	case tea.KeyEsc:
		m.mode = modeForm
		m.focusForm()
	case tea.KeyUp:
		if len(m.tagOptions) > 0 {
			m.tagCursor = (m.tagCursor - 1 + len(m.tagOptions)) % len(m.tagOptions)
			m.ensureTagVisible()
		}
	case tea.KeyDown:
		if len(m.tagOptions) > 0 {
			m.tagCursor = (m.tagCursor + 1) % len(m.tagOptions)
			m.ensureTagVisible()
		}
	case tea.KeySpace:
		if len(m.tagOptions) > 0 {
			key := strings.ToLower(m.tagOptions[m.tagCursor])
			m.tagSelected[key] = !m.tagSelected[key]
		}
	case tea.KeyEnter:
		selected := make([]string, 0, len(m.tagOrder))
		for _, tag := range m.tagOrder {
			if m.tagSelected[strings.ToLower(tag)] {
				selected = append(selected, tag)
			}
		}
		m.form.tags.SetValue(strings.Join(selected, ", "))
		m.form.focus = 1
		m.mode = modeForm
		m.focusForm()
	case tea.KeyRunes:
		if strings.EqualFold(key.String(), "s") {
			preserve := ""
			if len(m.tagOptions) > 0 {
				preserve = m.tagOptions[m.tagCursor]
			}
			m.tagAscending = !m.tagAscending
			m.sortTagOptions(preserve)
		} else if strings.EqualFold(key.String(), "n") {
			input := textinput.New()
			input.KeyMap.Paste.SetEnabled(false)
			input.Prompt = "New tag: "
			input.PromptStyle = parameterStyle
			input.Focus()
			m.newTag = input
			m.mode, m.status = modeNewTag, ""
		}
	}
	return *m, nil
}

func (m Model) tagPageSize() int { return max(1, m.height-10) }

func (m *Model) ensureTagVisible() {
	page := m.tagPageSize()
	if m.tagCursor < m.tagOffset {
		m.tagOffset = m.tagCursor
	}
	if m.tagCursor >= m.tagOffset+page {
		m.tagOffset = m.tagCursor - page + 1
	}
	if maxOffset := max(0, len(m.tagOptions)-page); m.tagOffset > maxOffset {
		m.tagOffset = maxOffset
	}
}

func (m *Model) updateNewTag(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if key.Type == tea.KeyEsc {
		m.mode = modeTags
		return *m, nil
	}
	if key.Type == tea.KeyEnter {
		value := strings.TrimSpace(m.newTag.Value())
		if value == "" {
			m.status = "Tag must not be empty"
			return *m, nil
		}
		canonical := vault.CanonicalizeTags([]string{value}, m.tagOptions)[0]
		found := -1
		for i, option := range m.tagOptions {
			if strings.EqualFold(option, canonical) {
				found = i
				break
			}
		}
		if found < 0 {
			m.tagOptions = append(m.tagOptions, canonical)
			m.tagOrder = append(m.tagOrder, canonical)
		} else {
			canonical = m.tagOptions[found]
		}
		m.tagSelected[strings.ToLower(canonical)] = true
		m.sortTagOptions(canonical)
		m.mode, m.status = modeTags, ""
		return *m, nil
	}
	var cmd tea.Cmd
	m.newTag, cmd = m.newTag.Update(key)
	return *m, cmd
}

func (m *Model) openForm(entry vault.Entry) {
	name, tags := textinput.New(), textinput.New()
	name.KeyMap.Paste.SetEnabled(false)
	tags.KeyMap.Paste.SetEnabled(false)
	name.Prompt, tags.Prompt = "Name: ", "Tags: "
	name.PromptStyle, tags.PromptStyle = parameterStyle, parameterStyle
	name.SetValue(entry.Name)
	tags.SetValue(strings.Join(entry.Tags, ", "))
	desc, blob := textarea.New(), textarea.New()
	desc.KeyMap.Paste.SetEnabled(false)
	blob.KeyMap.Paste.SetEnabled(false)
	desc.Placeholder, blob.Placeholder = "Description (metadata only)", "Opaque text blob"
	desc.SetValue(entry.Description)
	blob.SetValue(entry.Blob)
	m.form = formModel{
		name: name, tags: tags, description: desc, blob: blob,
		blobOriginal: entry.Blob, blobInitial: blob.Value(), editID: entry.ID,
	}
	m.mode, m.status = modeForm, ""
	m.focusForm()
	m.resizeWidgets()
}

func (m *Model) focusForm() {
	m.form.name.Blur()
	m.form.tags.Blur()
	m.form.description.Blur()
	m.form.blob.Blur()
	switch m.form.focus {
	case 0:
		m.form.name.Focus()
	case 1:
		m.form.tags.Focus()
	case 2:
		m.form.description.Focus()
	case 3:
		m.form.blob.Focus()
	}
}

func (m *Model) saveForm() {
	name := m.form.name.Value()
	if strings.TrimSpace(name) == "" {
		m.status = "Name must not be empty"
		return
	}
	tags := splitTags(m.form.tags.Value())
	blob := m.form.blobOriginal
	if m.form.blobPasteActive {
		blob = m.form.blobPasted
	} else if m.form.editID == "" || m.form.blobDirty || m.form.blob.Value() != m.form.blobInitial {
		blob = m.form.blob.Value()
	}
	candidate := cloneDocument(m.doc)
	if m.form.editID == "" {
		entry, err := vault.NewEntry(name, m.form.description.Value(), tags, blob, m.doc.CanonicalTags(), m.now())
		if err != nil {
			m.status = err.Error()
			return
		}
		candidate.Entries = append(candidate.Entries, entry)
	} else {
		for i := range candidate.Entries {
			if candidate.Entries[i].ID == m.form.editID {
				candidate.Entries[i].Apply(name, m.form.description.Value(), tags, blob, m.doc.CanonicalTags(), m.now())
			}
		}
	}
	m.saveCandidate(candidate, "Saved")
}

func (m *Model) deleteSelected() {
	id := m.selectedID()
	candidate := cloneDocument(m.doc)
	candidate.Entries = candidate.Entries[:0]
	for _, entry := range m.doc.Entries {
		if entry.ID != id {
			candidate.Entries = append(candidate.Entries, entry)
		}
	}
	m.saveCandidate(candidate, "Entry deleted")
}

func (m *Model) saveCandidate(candidate vault.Document, message string) {
	if err := m.store.Save(candidate, false); err != nil {
		var committed *vault.CommittedError
		if errors.As(err, &committed) {
			m.commit(candidate, "Saved with warning: "+err.Error())
			return
		}
		if errors.Is(err, vault.ErrConflict) {
			m.pending = &candidate
			m.mode, m.status = modeConflict, "Vault changed on disk"
			return
		}
		m.status = err.Error()
		return
	}
	m.commit(candidate, message)
}

func (m *Model) forcePending() {
	if m.pending == nil {
		m.mode = modeList
		return
	}
	candidate := *m.pending
	if err := m.store.Save(candidate, true); err != nil {
		var committed *vault.CommittedError
		if errors.As(err, &committed) {
			m.commit(candidate, "Overwritten with warning: "+err.Error())
			return
		}
		m.status = err.Error()
		return
	}
	m.commit(candidate, "External changes overwritten")
}

func (m *Model) commit(candidate vault.Document, message string) {
	id := m.selectedID()
	if len(candidate.Entries) > len(m.doc.Entries) {
		id = candidate.Entries[len(candidate.Entries)-1].ID
	}
	m.doc, m.pending, m.mode, m.status = candidate, nil, modeList, message
	m.refresh(id)
}

func (m *Model) refresh(selectedID string) {
	q, err := search.Parse(m.query.Value())
	if err != nil {
		m.status = "Query: " + err.Error()
		return
	}
	if strings.HasPrefix(m.status, "Query: ") {
		m.status = ""
	}
	results := q.Filter(m.doc.Entries)
	m.visible = make([]vault.Entry, len(results))
	for i := range results {
		m.visible[i] = results[i].Entry
	}
	sort.SliceStable(m.visible, func(i, j int) bool {
		a, b := strings.ToLower(m.visible[i].Name), strings.ToLower(m.visible[j].Name)
		if m.ascending {
			return a < b || (a == b && m.visible[i].ID < m.visible[j].ID)
		}
		return a > b || (a == b && m.visible[i].ID > m.visible[j].ID)
	})
	m.selected = 0
	for i := range m.visible {
		if m.visible[i].ID == selectedID {
			m.selected = i
			break
		}
	}
	m.ensureSelectedVisible()
	m.updateDetail()
}

func (m *Model) move(delta int) {
	if len(m.visible) == 0 {
		return
	}
	m.selected = (m.selected + delta + len(m.visible)) % len(m.visible)
	m.ensureSelectedVisible()
	m.updateDetail()
}

func (m *Model) listPageSize() int { return max(1, m.height-13) }

func (m *Model) ensureSelectedVisible() {
	if len(m.visible) == 0 {
		m.listOffset = 0
		return
	}
	page := m.listPageSize()
	if m.selected < m.listOffset {
		m.listOffset = m.selected
	}
	if m.selected >= m.listOffset+page {
		m.listOffset = m.selected - page + 1
	}
	if maxOffset := max(0, len(m.visible)-page); m.listOffset > maxOffset {
		m.listOffset = maxOffset
	}
}

func (m *Model) updateDetail() {
	if len(m.visible) == 0 {
		m.detail.SetContent("No matching entries")
		return
	}
	m.detail.SetContent(entryDetails(m.selectedEntry()))
}

func (m *Model) resizeWidgets() {
	left := max(18, m.width/3)
	right := max(18, m.width-left-5)
	m.query.Width = max(8, m.width-10)
	if m.mode == modeView || m.mode == modeBlobCopy || m.mode == modeDecrypted {
		outerWidth := min(80, m.width)
		m.detail.Width, m.detail.Height = max(8, outerWidth-6), max(3, m.height-9)
		m.decryptedView.Width, m.decryptedView.Height = max(8, outerWidth-6), max(3, m.height-11)
	} else {
		m.detail.Width, m.detail.Height = right-4, max(3, m.height-9)
	}
	m.ensureSelectedVisible()
	if m.mode == modeForm {
		m.form.name.Width, m.form.tags.Width = max(10, m.width-12), max(10, m.width-12)
		m.form.description.SetWidth(max(10, m.width-8))
		m.form.blob.SetWidth(max(10, m.width-8))
		m.form.description.SetHeight(max(2, (m.height-12)/3))
		m.form.blob.SetHeight(max(3, (m.height-12)/2))
	}
}

func renderActionBar(width int, actions ...string) string {
	blocks := make([]string, len(actions))
	for i, action := range actions {
		blocks[i] = actionStyle.Render(action)
	}
	return ansi.Truncate(strings.Join(blocks, " "), max(1, width), "…")
}

func humanFileSize(size int64) string {
	if size < 0 {
		size = 0
	}
	if size < 1_000 {
		return fmt.Sprintf("%dB", size)
	}
	value, unit := float64(size)/1_000, "kB"
	if size >= 1_000_000 {
		value, unit = float64(size)/1_000_000, "MB"
	}
	formatted := strconv.FormatFloat(value, 'f', 1, 64)
	return strings.TrimSuffix(formatted, ".0") + unit
}

func shortenHomePath(path, homeDir string) string {
	path = filepath.Clean(path)
	if homeDir == "" {
		return path
	}
	homeDir = filepath.Clean(homeDir)
	relative, err := filepath.Rel(homeDir, path)
	if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		if relative == "." {
			return "~"
		}
		return filepath.Join("~", relative)
	}
	return path
}

func (m Model) statusLine(message string) string {
	parts := make([]string, 0, 2)
	if m.metadata != nil {
		path := safeInline(shortenHomePath(m.metadata.Path(), m.homeDir))
		size, err := m.metadata.Size()
		if err != nil {
			parts = append(parts, path+" (size unavailable)")
		} else {
			parts = append(parts, path+" ("+humanFileSize(size)+")")
		}
	}
	if message != "" {
		parts = append(parts, message)
	}
	return strings.Join(parts, "  ")
}

func (m Model) View() string {
	header := m.renderHeader()
	searchLine := mutedStyle.Render("Sort: "+sortLabel(m.ascending)+"  ") + m.query.View()
	searchLine = ansi.Truncate(searchLine, max(1, m.width), "…")
	footer := renderActionBar(m.width, "↑↓ Nav", "/ Search", "S Sort", "B Copy", "D Decrypt", "F3 View", "F4 Edit", "F5 New", "F8 Del", "F10 Quit")
	statusMessage := safeInline(m.status)
	if strings.Contains(strings.ToLower(statusMessage), "error") || strings.Contains(strings.ToLower(statusMessage), "must") || strings.Contains(strings.ToLower(statusMessage), "disk") {
		statusMessage = errorStyle.Render(statusMessage)
	}
	status := statusMessage
	statusLine := m.statusLine(statusMessage)
	modalActionWidth := max(1, min(74, m.width-6))
	switch m.mode {
	case modeForm:
		formTitle := "Edit entry"
		if m.form.editID == "" {
			formTitle = "New entry"
		}
		if m.width < 60 || m.height < 18 {
			labels := []string{"Name", "Tags", "Description", "Blob (stored unchanged)"}
			widgets := []string{m.form.name.View(), m.form.tags.View(), m.form.description.View(), m.form.blob.View()}
			lines := []string{header, formTitle, parameterStyle.Render(labels[m.form.focus] + ":"), widgets[m.form.focus]}
			if statusLine != "" {
				lines = append(lines, statusLine)
			}
			lines = append(lines,
				renderActionBar(m.width, "Tab Next", "Ctrl+S Save"),
				renderActionBar(m.width, "Ctrl+T Tags", "Esc Cancel", "F10 Quit"),
			)
			return m.fitView(strings.Join(lines, "\n"))
		}
		formActions := renderActionBar(m.width, "Tab/Shift+Tab Next", "Ctrl+T Select tags", "Ctrl+S Save", "Esc Cancel")
		return m.fitView(strings.Join([]string{header, formTitle, m.form.name.View(), m.form.tags.View(), parameterStyle.Render("Description:"), m.form.description.View(), parameterStyle.Render("Blob (stored unchanged):"), m.form.blob.View(), statusLine, formActions}, "\n"))
	case modeDelete:
		deleteActions := renderActionBar(modalActionWidth, "Y Delete", "N Cancel", "Esc Cancel")
		return m.fitView(header + "\n" + m.renderModal(fmt.Sprintf("Delete %q?\n%s\n%s", safeInline(m.selectedEntry().Name), deleteActions, status)))
	case modeConflict:
		conflictActions := renderActionBar(modalActionWidth, "O Overwrite", "Enter/Esc/C Cancel")
		return m.fitView(header + "\n" + m.renderModal("The vault changed after it was loaded.\n"+conflictActions+"\n"+status))
	case modeTags:
		pageEnd := min(len(m.tagOptions), m.tagOffset+m.tagPageSize())
		rows := make([]string, 0, pageEnd-m.tagOffset)
		for i := m.tagOffset; i < pageEnd; i++ {
			tag := m.tagOptions[i]
			mark := " "
			if m.tagSelected[strings.ToLower(tag)] {
				mark = "x"
			}
			prefix := "  "
			if i == m.tagCursor {
				prefix = "› "
			}
			rows = append(rows, fmt.Sprintf("%s[%s] %s", prefix, mark, safeInline(tag)))
		}
		if len(rows) == 0 {
			rows = append(rows, "No existing tags")
		}
		tagSort := "A-Z"
		if !m.tagAscending {
			tagSort = "Z-A"
		}
		tagActions := renderActionBar(modalActionWidth, "↑↓ Move", "Space Toggle", "S Sort", "N New", "Enter Apply", "Esc Cancel")
		return m.fitView(header + "\n" + m.renderModal("Select tags (Sort: "+tagSort+")\n\n"+strings.Join(rows, "\n")+"\n\n"+tagActions))
	case modeNewTag:
		newTagActions := renderActionBar(modalActionWidth, "Enter Add", "Esc Back")
		return m.fitView(header + "\n" + m.renderModal("Create tag\n"+m.newTag.View()+"\n"+newTagActions+"\n"+status))
	case modeQuitConfirm:
		quitActions := renderActionBar(modalActionWidth, "Q/Y Quit", "Enter/Esc/C Continue editing")
		return m.fitView(header + "\n" + m.renderModal("Discard unsaved changes and quit?\n"+quitActions))
	case modeDecrypting:
		decryptActions := renderActionBar(modalActionWidth, "Esc Cancel", "F10 Quit")
		return m.fitView(header + "\n" + m.renderModal("Decrypting with GPG\n\nWaiting for GPG Agent / Pinentry…\n\n"+decryptActions))
	case modeDecrypted:
		signature := "Signature: " + string(m.decryptSignature)
		if m.decryptSignature == internalgpg.SignatureInvalid || m.decryptSignature == internalgpg.SignatureUnverified {
			signature = errorStyle.Render(signature)
		}
		decryptActions := renderActionBar(modalActionWidth, "↑↓ Scroll", "Esc/Enter Close", "F10 Quit")
		return m.fitView(header + "\n" + m.renderModal("Decrypted text\n"+signature+"\n\n"+m.decryptedView.View()+"\n\n"+decryptActions))
	case modeView:
		viewFooter := renderActionBar(modalActionWidth, "↑↓ Scroll", "B Copy blob", "D Decrypt", "Esc/Enter Close")
		if status != "" {
			viewFooter = status + "\n\n" + viewFooter
		}
		return m.fitView(header + "\n" + m.renderModal("View entry\n\n"+m.detail.View()+"\n\n"+viewFooter))
	case modeBlobCopy:
		return safeMultiline(m.manualBlob)
	}
	narrow := m.width < 60
	listWidth := max(16, m.width/3)
	detailWidth := max(16, m.width-listWidth-1)
	if narrow {
		listWidth = max(12, m.width)
	}
	page := m.listPageSize()
	var rows []string
	if len(m.visible) == 0 {
		rows = []string{"No matching entries"}
	} else {
		end := min(len(m.visible), m.listOffset+page)
		for i := m.listOffset; i < end; i++ {
			entry := m.visible[i]
			prefix := "  "
			line := ansi.Truncate(fmt.Sprintf("%s%s [%s]", prefix, safeInline(entry.Name), safeInline(strings.Join(entry.Tags, ", "))), max(6, listWidth-4), "…")
			if i == m.selected {
				line = selectedStyle.Render(ansi.Truncate("› "+safeInline(entry.Name)+" ["+safeInline(strings.Join(entry.Tags, ", "))+"]", max(6, listWidth-4), "…"))
			}
			rows = append(rows, line)
		}
	}
	upStyle := scrollInactiveStyle
	if m.listOffset > 0 {
		upStyle = scrollActiveStyle
	}
	downStyle := scrollInactiveStyle
	if m.listOffset+page < len(m.visible) {
		downStyle = scrollActiveStyle
	}
	listContent := []string{fmt.Sprintf("Entries (%d)", len(m.visible)), upStyle.Render("↑")}
	listContent = append(listContent, rows...)
	listContent = append(listContent, downStyle.Render("↓"))
	left := borderStyle.Width(max(8, listWidth-2)).Height(max(3, m.height-9)).Render(strings.Join(listContent, "\n"))
	right := borderStyle.Width(max(8, detailWidth-2)).Height(max(3, m.height-9)).Render("Details\n" + m.detail.View())
	body := lipgloss.JoinHorizontal(lipgloss.Top, left, " ", right)
	if narrow {
		body = left
	}
	return m.fitView(strings.Join([]string{header, searchLine, body, statusLine, footer}, "\n"))
}

func (m Model) renderHeader() string {
	width := max(1, m.width)
	left := titleStyle.Render("SecretTUIVault")
	version := m.gitVersion
	if version == "" {
		version = "dev"
	}
	right := "Git version: " + version
	rightWidth := lipgloss.Width(right)
	if rightWidth >= width {
		return ansi.Truncate(right, width, "…")
	}
	left = ansi.Truncate(left, max(1, width-rightWidth-1), "…")
	gap := max(1, width-lipgloss.Width(left)-rightWidth)
	return left + strings.Repeat(" ", gap) + right
}

func (m Model) fitView(content string) string {
	return lipgloss.NewStyle().MaxWidth(m.width).MaxHeight(m.height).Render(content)
}

func (m Model) renderModal(content string) string {
	return modalStyle.Width(max(18, min(78, m.width-2))).Render(content)
}

func (m Model) selectedEntry() vault.Entry {
	if len(m.visible) == 0 {
		return vault.Entry{}
	}
	return m.visible[min(max(0, m.selected), len(m.visible)-1)]
}
func (m Model) selectedID() string { return m.selectedEntry().ID }

func entryDetails(e vault.Entry) string {
	return strings.Join([]string{
		parameterStyle.Render("Name:") + " " + detailNameStyle.Render(safeInline(e.Name)),
		parameterStyle.Render("ID:") + " " + safeInline(e.ID),
		parameterStyle.Render("Tags:") + " " + safeInline(strings.Join(e.Tags, ", ")),
		parameterStyle.Render("Created:") + " " + safeInline(e.Created),
		parameterStyle.Render("Updated:") + " " + safeInline(e.Updated),
		"",
		parameterStyle.Render("Description:"),
		safeMultiline(e.Description),
		"",
		parameterStyle.Render("Blob:"),
		safeMultiline(e.Blob),
	}, "\n")
}

func safeInline(value string) string { return escapeControls(value, false) }

func safeMultiline(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	return escapeControls(value, true)
}

func escapeControls(value string, preserveNewlines bool) string {
	var escaped strings.Builder
	for _, r := range value {
		if r == '\n' && preserveNewlines {
			escaped.WriteRune(r)
			continue
		}
		if r < 0x20 || (r >= 0x7f && r <= 0x9f) {
			if r <= 0xff {
				fmt.Fprintf(&escaped, "\\x%02x", r)
			} else {
				fmt.Fprintf(&escaped, "\\u%04x", r)
			}
			continue
		}
		escaped.WriteRune(r)
	}
	return escaped.String()
}
func splitTags(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == '\n' })
}
func cloneDocument(d vault.Document) vault.Document {
	c := d
	c.Entries = append([]vault.Entry(nil), d.Entries...)
	for i := range c.Entries {
		c.Entries[i].Tags = append([]string(nil), c.Entries[i].Tags...)
	}
	return c
}
func sortLabel(ascending bool) string {
	if ascending {
		return "Name ↑"
	}
	return "Name ↓"
}
