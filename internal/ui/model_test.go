package ui

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/tseiman/SecretTUIVault/internal/vault"
)

type fakeSaver struct {
	saved  []vault.Document
	forces []bool
	err    error
}

func (f *fakeSaver) Save(d vault.Document, force bool) error {
	f.saved = append(f.saved, d)
	f.forces = append(f.forces, force)
	return f.err
}

func uiDocument(t *testing.T) vault.Document {
	t.Helper()
	a, _ := vault.NewEntry("Alpha", "first description", []string{"Linux"}, "alpha blob\n", nil, time.Now())
	b, _ := vault.NewEntry("Beta", "second description", []string{"Windows"}, "beta blob\n", nil, time.Now())
	return vault.Document{Version: vault.SchemaVersion, Entries: []vault.Entry{a, b}}
}

func update(m Model, msg tea.Msg) (Model, tea.Cmd) {
	next, cmd := m.Update(msg)
	return next.(Model), cmd
}

func TestHeaderShowsGitVersionAtRightEdge(t *testing.T) {
	m := New(uiDocument(t), &fakeSaver{})
	m.gitVersion = "0ea17c7"
	m, _ = update(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	header := strings.SplitN(m.View(), "\n", 2)[0]
	if width := lipgloss.Width(header); width != 80 {
		t.Fatalf("header width=%d, want 80: %q", width, header)
	}
	if !strings.HasSuffix(header, "Git version: 0ea17c7") {
		t.Fatalf("Git version is not labeled and right-aligned: %q", header)
	}
	if strings.Contains(header, "Sort:") || strings.Contains(header, "Name ↑") {
		t.Fatalf("sort indicator must not be in header: %q", header)
	}
	lines := strings.Split(m.View(), "\n")
	if len(lines) < 2 || !strings.Contains(lines[1], "Sort: Name ↑  Search:") {
		t.Fatalf("sort indicator is not before Search: %q", m.View())
	}
}

func TestEntriesHeadingShowsVisibleCount(t *testing.T) {
	m := New(uiDocument(t), &fakeSaver{})
	if got := m.View(); !strings.Contains(got, "Entries (2)") {
		t.Fatalf("initial entry count missing: %q", got)
	}
	m.visible = m.visible[:1]
	if got := m.View(); !strings.Contains(got, "Entries (1)") {
		t.Fatalf("visible entry count was not updated: %q", got)
	}
}

func TestDetailNameStyleHasForegroundColor(t *testing.T) {
	if _, uncolored := detailNameStyle.GetForeground().(lipgloss.NoColor); uncolored {
		t.Fatal("detail name has no foreground color")
	}
}

func TestSplitViewResizeNavigationSortAndSearch(t *testing.T) {
	m := New(uiDocument(t), &fakeSaver{})
	m, _ = update(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	view := m.View()
	for _, want := range []string{"SecretTUIVault", "Alpha", "Details", "alpha blob", "F3 View", "F10 Quit"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	if m.selectedEntry().Name != "Beta" {
		t.Fatal("down did not navigate")
	}
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	if m.selectedEntry().Name != "Beta" || m.ascending {
		t.Fatal("sort toggle failed or lost selection")
	}
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	for _, r := range "TAG:Linux" {
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if len(m.visible) != 1 || m.visible[0].Name != "Alpha" {
		t.Fatalf("filter = %#v status=%s", m.visible, m.status)
	}
	m, _ = update(m, tea.WindowSizeMsg{Width: 35, Height: 12})
	if !strings.Contains(m.View(), "Alpha") {
		t.Fatal("narrow view lost content")
	}
}

func TestCreateEditViewDeleteAndCancellation(t *testing.T) {
	store := &fakeSaver{}
	m := New(uiDocument(t), store)
	m.now = func() time.Time { return time.Date(2026, 8, 8, 12, 0, 0, 0, time.Local) }
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyF5})
	if m.mode != modeForm {
		t.Fatal("F5 did not open form")
	}
	m.form.name.SetValue("Gamma")
	m.form.description.SetValue("metadata")
	m.form.tags.SetValue("linux, New")
	m.form.blob.SetValue("opaque\n")
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlS})
	if len(m.doc.Entries) != 3 || len(store.saved) != 1 {
		t.Fatal("create did not save")
	}
	if got := strings.Join(m.doc.Entries[2].Tags, ","); got != "Linux,New" {
		t.Fatalf("tags %q", got)
	}
	m.selected = 2
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyF4})
	m.form.name.SetValue("Gamma edited")
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.doc.Entries[2].Name != "Gamma" {
		t.Fatal("cancelled edit mutated document")
	}
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyF3})
	if m.mode != modeView || !strings.Contains(m.View(), "opaque") {
		t.Fatal("F3 view failed")
	}
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEsc})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyF8})
	if m.mode != modeDelete {
		t.Fatal("F8 did not confirm")
	}
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEsc})
	if len(m.doc.Entries) != 3 {
		t.Fatal("cancelled delete mutated document")
	}
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyF8})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if len(m.doc.Entries) != 3 || m.mode != modeDelete {
		t.Fatal("Enter must not confirm deletion")
	}
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if len(m.doc.Entries) != 3 || m.mode != modeList {
		t.Fatal("N did not cancel deletion")
	}
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyF8})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if len(m.doc.Entries) != 2 {
		t.Fatal("Y did not confirm deletion")
	}
}

func TestConflictModalOverwriteCancelAndErrors(t *testing.T) {
	store := &fakeSaver{err: vault.ErrConflict}
	m := New(uiDocument(t), store)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyF8})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if m.mode != modeConflict || len(m.doc.Entries) != 2 {
		t.Fatal("conflict should preserve current document")
	}
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.mode != modeList || len(m.doc.Entries) != 2 {
		t.Fatal("safe default conflict cancellation was destructive")
	}
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyF8})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	store.err = nil
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	if len(m.doc.Entries) != 1 || !store.forces[len(store.forces)-1] {
		t.Fatal("overwrite did not force pending save")
	}
	store.err = errors.New("disk full")
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyF5})
	m.form.name.SetValue("X")
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlS})
	if m.mode != modeForm || !strings.Contains(m.status, "disk full") {
		t.Fatal("save error not shown")
	}
}

func TestF10Quits(t *testing.T) {
	m := New(uiDocument(t), &fakeSaver{})
	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyF10})
	if cmd == nil || cmd() != tea.Quit() {
		t.Fatal("F10 did not quit")
	}
}

func TestEscapeDigitAliasesFunctionKeys(t *testing.T) {
	press := func(m Model, digit rune) (Model, tea.Cmd) {
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyEsc})
		return update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{digit}})
	}
	for _, tc := range []struct {
		digit rune
		mode  mode
	}{{'3', modeView}, {'4', modeForm}, {'5', modeForm}, {'8', modeDelete}} {
		m, _ := press(New(uiDocument(t), &fakeSaver{}), tc.digit)
		if m.mode != tc.mode {
			t.Fatalf("Esc+%c mode=%v, want %v", tc.digit, m.mode, tc.mode)
		}
	}
	_, cmd := press(New(uiDocument(t), &fakeSaver{}), '0')
	if cmd == nil || cmd() != tea.Quit() {
		t.Fatal("Esc+0 did not act as F10")
	}
	m, _ := update(New(uiDocument(t), &fakeSaver{}), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}, Alt: true})
	if m.mode != modeView {
		t.Fatalf("terminal-decoded Alt+3 mode=%v, want view", m.mode)
	}
}

func TestF10RequiresConfirmationWhenFormIsUnsaved(t *testing.T) {
	m := New(uiDocument(t), &fakeSaver{})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyF5})
	m.form.name.SetValue("Unsaved")
	var cmd tea.Cmd
	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyF10})
	if cmd != nil || m.mode != modeQuitConfirm {
		t.Fatalf("unsaved form quit without confirmation: mode=%v", m.mode)
	}
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.mode != modeForm {
		t.Fatal("Enter did not safely cancel quit")
	}
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyF10})
	_, cmd = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if cmd == nil || cmd() != tea.Quit() {
		t.Fatal("explicit quit confirmation did not quit")
	}
}

func TestLongListScrollsSelectedEntryIntoView(t *testing.T) {
	doc := vault.Document{Version: vault.SchemaVersion}
	for i := 0; i < 20; i++ {
		entry, err := vault.NewEntry(
			fmt.Sprintf("Entry %02d", i), "", nil, "", nil,
			time.Date(2026, 8, 8, 12, 0, i, 0, time.Local),
		)
		if err != nil {
			t.Fatal(err)
		}
		doc.Entries = append(doc.Entries, entry)
	}
	m := New(doc, &fakeSaver{})
	m, _ = update(m, tea.WindowSizeMsg{Width: 80, Height: 12})
	for i := 0; i < 10; i++ {
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	}
	view := m.View()
	if !strings.Contains(view, "Entry 10") {
		t.Fatalf("selected entry was scrolled out of the rendered list:\n%s", view)
	}
	if strings.Contains(view, "Entry 00") {
		t.Fatalf("list rendered every row instead of a scrolling window:\n%s", view)
	}
}

func TestNarrowAndLongListViewFitsTerminal(t *testing.T) {
	doc := vault.Document{Version: vault.SchemaVersion}
	for i := 0; i < 40; i++ {
		entry, err := vault.NewEntry(fmt.Sprintf("Entry %02d with a very long display name", i), "description", []string{"VeryLongTag"}, "blob", nil, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		doc.Entries = append(doc.Entries, entry)
	}
	m := New(doc, &fakeSaver{})
	m, _ = update(m, tea.WindowSizeMsg{Width: 35, Height: 20})
	for i := 0; i < 30; i++ {
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	}
	view := m.View()
	if width := lipgloss.Width(view); width > 35 {
		t.Fatalf("render width=%d exceeds terminal width=35", width)
	}
	if height := lipgloss.Height(view); height > 20 {
		t.Fatalf("render height=%d exceeds terminal height=20", height)
	}
	if !strings.Contains(view, "Entry 30") {
		t.Fatal("selected item was not visible")
	}
}

func TestEveryModeFitsNarrowTerminal(t *testing.T) {
	for _, size := range []tea.WindowSizeMsg{{Width: 20, Height: 8}, {Width: 35, Height: 12}, {Width: 59, Height: 16}} {
		m := New(uiDocument(t), &fakeSaver{})
		m, _ = update(m, size)
		assertFits := func(label string, model Model) {
			t.Helper()
			view := model.View()
			if width := lipgloss.Width(view); width > size.Width {
				t.Fatalf("%s view width=%d exceeds terminal width=%d:\n%s", label, width, size.Width, view)
			}
			if height := lipgloss.Height(view); height > size.Height {
				t.Fatalf("%s view height=%d exceeds terminal height=%d:\n%s", label, height, size.Height, view)
			}
		}
		assertFits("list", m)
		for focus := 0; focus < 4; focus++ {
			form := m
			form.openForm(vault.Entry{})
			form.form.focus = focus
			form.focusForm()
			assertFits(fmt.Sprintf("form-%d", focus), form)
		}
		for label, mode := range map[string]mode{
			"delete": modeDelete, "conflict": modeConflict, "tags": modeTags,
			"new-tag": modeNewTag, "quit": modeQuitConfirm, "view": modeView,
		} {
			modal := m
			modal.mode = mode
			modal.tagOptions = []string{"Linux", "Windows", "Recovery"}
			assertFits(label, modal)
		}
	}
}

func TestTagPickerSelectsExistingAndCreatesCanonicalTag(t *testing.T) {
	m := New(uiDocument(t), &fakeSaver{})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyF5})
	m.form.name.SetValue("Gamma")
	m.form.focus = 1
	m.focusForm()
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlT})
	if m.mode != modeTags || len(m.tagOptions) != 2 {
		t.Fatalf("tag picker did not expose existing tags: mode=%v options=%v", m.mode, m.tagOptions)
	}
	m, _ = update(m, tea.KeyMsg{Type: tea.KeySpace})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if m.mode != modeNewTag {
		t.Fatal("new tag input did not open")
	}
	for _, r := range "linux" {
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.mode != modeTags || len(m.tagOptions) != 2 {
		t.Fatalf("case-insensitive duplicate tag was added: %v", m.tagOptions)
	}
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	for _, r := range "Cloud" {
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.mode != modeForm || m.form.tags.Value() != "Linux, Cloud" {
		t.Fatalf("selected tags not applied to form: mode=%v tags=%q", m.mode, m.form.tags.Value())
	}
}

func TestEditWithoutTouchingBlobPreservesOpaqueBytes(t *testing.T) {
	values := []string{
		"line with trailing newline\n",
		"two trailing lines\n\n",
		"tab	and CRLF\r\n",
		"control \x00 byte",
	}
	for _, value := range values {
		t.Run(fmt.Sprintf("%q", value), func(t *testing.T) {
			doc := uiDocument(t)
			doc.Entries[0].Blob = value
			store := &fakeSaver{}
			m := New(doc, store)
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyF4})
			m.form.name.SetValue("Alpha edited")
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlS})
			if len(store.saved) != 1 || store.saved[0].Entries[0].Blob != value {
				t.Fatalf("blob changed from %q to %q", value, store.saved[0].Entries[0].Blob)
			}
		})
	}
}

func TestBracketedPasteStoresBlobBytesExactly(t *testing.T) {
	raw := "-----BEGIN-----\r\nline\tvalue\x00\r\n-----END-----\r\n"
	store := &fakeSaver{}
	m := New(uiDocument(t), store)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyF5})
	m.form.name.SetValue("Pasted")
	m.form.focus = 3
	m.focusForm()
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(raw), Paste: true})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlS})
	if len(store.saved) != 1 || store.saved[0].Entries[2].Blob != raw {
		t.Fatalf("pasted blob changed from %q to %q", raw, store.saved[0].Entries[2].Blob)
	}
}

func TestCommittedWarningStillUpdatesInMemoryDocument(t *testing.T) {
	store := &fakeSaver{err: &vault.CommittedError{Err: errors.New("directory sync failed")}}
	m := New(uiDocument(t), store)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyF5})
	m.form.name.SetValue("Committed")
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlS})
	if len(m.doc.Entries) != 3 || m.mode != modeList {
		t.Fatalf("committed save not reflected in memory: entries=%d mode=%v", len(m.doc.Entries), m.mode)
	}
	if !strings.Contains(strings.ToLower(m.status), "warning") {
		t.Fatalf("committed durability warning not shown: %q", m.status)
	}
}

func TestF3ViewFitsEightyColumnsAndKeepsArmorLinesUnwrapped(t *testing.T) {
	doc := uiDocument(t)
	armorLine := strings.Repeat("A", 65)
	doc.Entries[0].Blob = armorLine + "\n"
	m := New(doc, &fakeSaver{})
	m, _ = update(m, tea.WindowSizeMsg{Width: 120, Height: 35})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyF3})
	view := m.View()
	lines := strings.Split(view, "\n")
	modalWidth := 0
	for _, line := range lines {
		start, end := strings.Index(line, "╔"), strings.LastIndex(line, "╗")
		if start >= 0 && end >= start {
			modalWidth = lipgloss.Width(line[start : end+len("╗")])
			break
		}
	}
	if modalWidth == 0 || modalWidth > 80 {
		t.Fatalf("F3 modal width=%d, want 1..80 columns", modalWidth)
	}
	if !strings.Contains(view, armorLine) {
		t.Fatalf("65-character armor line was wrapped:\n%s", view)
	}
}

func TestF3BlobCopyViewContainsOnlyDisplaySafeBlob(t *testing.T) {
	doc := uiDocument(t)
	doc.Entries[0].Blob = "-----BEGIN PGP MESSAGE-----\rComment: placeholder\r\rbody\n"
	m := New(doc, &fakeSaver{})
	m.writeClipboard = func(string) error { return errors.New("clipboard unavailable") }
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyF3})
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
	m, _ = update(m, cmd())
	want := safeMultiline(doc.Entries[0].Blob)
	if got := m.View(); got != want {
		t.Fatalf("copy view contains chrome or changed display text:\ngot  %q\nwant %q", got, want)
	}
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.mode != modeView {
		t.Fatalf("Esc from copy view mode=%v, want F3 view", m.mode)
	}
}

func TestBOpensBlobCopyViewDirectlyFromMainView(t *testing.T) {
	doc := uiDocument(t)
	doc.Entries[0].Blob = "copy-only blob\n"
	m := New(doc, &fakeSaver{})
	m.writeClipboard = func(string) error { return errors.New("clipboard unavailable") }
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
	m, _ = update(m, cmd())
	if got := m.View(); got != doc.Entries[0].Blob {
		t.Fatalf("B did not open direct blob copy view: %q", got)
	}
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.mode != modeList {
		t.Fatalf("Esc returned to mode=%v, want main view", m.mode)
	}
	if !strings.Contains(m.View(), "Zwischenablage nicht verfügbar") {
		t.Fatalf("clipboard failure missing from status line: %q", m.View())
	}
}

func TestBCopiesExactBlobWithoutOpeningManualView(t *testing.T) {
	doc := uiDocument(t)
	doc.Entries[0].Blob = "opaque\r\nblob\rbytes"
	m := New(doc, &fakeSaver{})
	copied := ""
	m.writeClipboard = func(value string) error {
		copied = value
		return nil
	}
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
	if cmd == nil {
		t.Fatal("B did not start clipboard write")
	}
	m, _ = update(m, cmd())
	if copied != doc.Entries[0].Blob {
		t.Fatalf("clipboard value changed: got %q want %q", copied, doc.Entries[0].Blob)
	}
	if m.mode != modeList || m.status != "In Zwischenablage kopiert" {
		t.Fatalf("successful copy mode=%v status=%q", m.mode, m.status)
	}
}

func TestClipboardFallbackKeepsOriginallySelectedBlob(t *testing.T) {
	doc := uiDocument(t)
	doc.Entries[0].Blob = "first blob"
	doc.Entries[1].Blob = "second blob"
	m := New(doc, &fakeSaver{})
	m.writeClipboard = func(string) error { return errors.New("clipboard unavailable") }
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
	m.selected = 1
	m, _ = update(m, cmd())
	if got := m.View(); got != "first blob" {
		t.Fatalf("fallback blob changed with selection: %q", got)
	}
}

func TestF3ClipboardSuccessStaysInViewAndShowsStatus(t *testing.T) {
	m := New(uiDocument(t), &fakeSaver{})
	m.writeClipboard = func(string) error { return nil }
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyF3})
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
	m, _ = update(m, cmd())
	if m.mode != modeView || !strings.Contains(m.View(), "In Zwischenablage kopiert") {
		t.Fatalf("F3 clipboard success mode=%v view=%q", m.mode, m.View())
	}
}

func TestCarriageReturnsRenderAsLineBreaksInSplitAndF3Views(t *testing.T) {
	doc := uiDocument(t)
	doc.Entries[0].Blob = "\r-----BEGIN PGP MESSAGE-----\rComment: placeholder\r\rbody"
	m := New(doc, &fakeSaver{})
	m, _ = update(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	want := "\n-----BEGIN PGP MESSAGE-----\nComment: placeholder\n\nbody"
	if got := entryDetails(doc.Entries[0]); !strings.Contains(got, want) || strings.Contains(got, `\x0d`) {
		t.Fatalf("detail content did not normalize carriage returns:\n%q", got)
	}
	if got := m.detail.View(); strings.Contains(got, `\x0d`) || !strings.Contains(got, "-----BEGIN PGP MESSAGE-----") || !strings.Contains(got, "Comment: placeholder") {
		t.Fatalf("split detail did not render normalized lines:\n%q", got)
	}
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyF3})
	if got := m.View(); strings.Contains(got, `\x0d`) || !strings.Contains(got, "-----BEGIN PGP MESSAGE-----") || !strings.Contains(got, "Comment: placeholder") {
		t.Fatalf("F3 view did not render normalized lines:\n%q", got)
	}
}

func TestRenderedVaultTextCannotEmitTerminalControls(t *testing.T) {
	unsafe := "safe\x1b]52;c;ZXhmaWx0cmF0ZQ==\aend\x00"
	doc := uiDocument(t)
	doc.Entries[0].Name = unsafe
	doc.Entries[0].Description = unsafe
	doc.Entries[0].Tags = []string{unsafe}
	doc.Entries[0].Blob = unsafe
	m := New(doc, &fakeSaver{})
	for _, view := range []string{m.View(), entryDetails(doc.Entries[0])} {
		if strings.ContainsAny(view, "\x00\x07\x1b") {
			t.Fatalf("terminal control byte rendered verbatim: %q", view)
		}
		if !strings.Contains(view, "\\x1b") {
			t.Fatalf("escaped control representation missing: %q", view)
		}
	}
}
