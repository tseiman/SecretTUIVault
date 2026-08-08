package vault

import (
	"crypto/rand"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

const SchemaVersion = 1
const TimestampLayout = "2006-01-02 15:04:05"

var uuidV4Pattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

type Document struct {
	Version int     `yaml:"version"`
	Entries []Entry `yaml:"entries"`
}

type Entry struct {
	ID          string   `yaml:"id"`
	Name        string   `yaml:"name"`
	Description string   `yaml:"description,omitempty"`
	Tags        []string `yaml:"tags,omitempty"`
	Created     string   `yaml:"created"`
	Updated     string   `yaml:"updated"`
	Blob        string   `yaml:"blob"`
}

func NewEntry(name, description string, tags []string, blob string, canonical []string, now time.Time) (Entry, error) {
	id, err := newUUIDv4()
	if err != nil {
		return Entry{}, err
	}
	stamp := now.Format(TimestampLayout)
	e := Entry{ID: id, Created: stamp}
	e.Apply(name, description, tags, blob, canonical, now)
	if strings.TrimSpace(e.Name) == "" {
		return Entry{}, errors.New("name must not be empty")
	}
	return e, nil
}

func (e *Entry) Apply(name, description string, tags []string, blob string, canonical []string, now time.Time) {
	e.Name = name
	e.Description = description
	e.Tags = CanonicalizeTags(tags, canonical)
	e.Blob = blob
	updated := now.Format(TimestampLayout)
	if e.Updated != "" {
		if previous, err := time.ParseInLocation(TimestampLayout, e.Updated, time.Local); err == nil && updated <= e.Updated {
			updated = previous.Add(time.Second).Format(TimestampLayout)
		}
	}
	e.Updated = updated
}

func (d Document) Validate() error {
	if d.Version != SchemaVersion {
		return fmt.Errorf("unsupported vault version %d", d.Version)
	}
	seen := make(map[string]struct{}, len(d.Entries))
	for i, entry := range d.Entries {
		if !uuidV4Pattern.MatchString(entry.ID) {
			return fmt.Errorf("entry %d has an invalid UUIDv4 id", i)
		}
		if _, ok := seen[entry.ID]; ok {
			return fmt.Errorf("duplicate entry id %q", entry.ID)
		}
		seen[entry.ID] = struct{}{}
		if strings.TrimSpace(entry.Name) == "" {
			return fmt.Errorf("entry %q has an empty name", entry.ID)
		}
		created, err := time.ParseInLocation(TimestampLayout, entry.Created, time.Local)
		if err != nil {
			return fmt.Errorf("entry %q has invalid created timestamp: %w", entry.ID, err)
		}
		updated, err := time.ParseInLocation(TimestampLayout, entry.Updated, time.Local)
		if err != nil {
			return fmt.Errorf("entry %q has invalid updated timestamp: %w", entry.ID, err)
		}
		if updated.Before(created) {
			return fmt.Errorf("entry %q has updated timestamp before created", entry.ID)
		}
	}
	return nil
}

func (d *Document) NormalizeTags() {
	canonical := []string{}
	for i := range d.Entries {
		d.Entries[i].Tags = CanonicalizeTags(d.Entries[i].Tags, canonical)
		canonical = append(canonical, d.Entries[i].Tags...)
	}
}

func (d Document) CanonicalTags() []string {
	var tags []string
	for _, e := range d.Entries {
		tags = CanonicalizeTags(append(tags, e.Tags...), tags)
	}
	return tags
}

func CanonicalizeTags(tags, existing []string) []string {
	canon := make(map[string]string, len(existing)+len(tags))
	for _, tag := range existing {
		tag = strings.TrimSpace(tag)
		if tag != "" {
			key := strings.ToLower(tag)
			if _, ok := canon[key]; !ok {
				canon[key] = tag
			}
		}
	}
	result := make([]string, 0, len(tags))
	used := make(map[string]struct{}, len(tags))
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		key := strings.ToLower(tag)
		if _, ok := used[key]; ok {
			continue
		}
		used[key] = struct{}{}
		if known, ok := canon[key]; ok {
			result = append(result, known)
		} else {
			canon[key] = tag
			result = append(result, tag)
		}
	}
	return result
}

func newUUIDv4() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate id: %w", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}
