package vault

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestEntryLifecycleAndYAMLRoundTrip(t *testing.T) {
	now := time.Date(2026, 8, 8, 14, 3, 2, 0, time.Local)
	e, err := NewEntry("Demo", "metadata only", []string{"Linux", "linux", "Ops"}, "opaque\nblob\n", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(e.ID) {
		t.Fatalf("bad UUIDv4: %q", e.ID)
	}
	if e.Created != "2026-08-08 14:03:02" || e.Updated != e.Created {
		t.Fatalf("timestamps: %#v", e)
	}
	if got := strings.Join(e.Tags, ","); got != "Linux,Ops" {
		t.Fatalf("tags = %q", got)
	}
	v := Document{Version: SchemaVersion, Entries: []Entry{e}}
	data, err := yaml.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var round Document
	if err := yaml.Unmarshal(data, &round); err != nil {
		t.Fatal(err)
	}
	if round.Entries[0].Blob != "opaque\nblob\n" {
		t.Fatalf("blob changed: %q", round.Entries[0].Blob)
	}
	later := now.Add(time.Hour)
	round.Entries[0].Apply("Renamed", "description", []string{"linux", "New"}, "any text", v.CanonicalTags(), later)
	if round.Entries[0].Created != e.Created || round.Entries[0].Updated != "2026-08-08 15:03:02" {
		t.Fatal("timestamp lifecycle violated")
	}
	if got := strings.Join(round.Entries[0].Tags, ","); got != "Linux,New" {
		t.Fatalf("canonical tags = %q", got)
	}
}

func TestNewEntryIDsAreUnique(t *testing.T) {
	a, _ := NewEntry("a", "", nil, "", nil, time.Now())
	b, _ := NewEntry("b", "", nil, "", nil, time.Now())
	if a.ID == b.ID {
		t.Fatal("duplicate generated IDs")
	}
}

func TestDocumentValidate(t *testing.T) {
	base := Entry{ID: "550e8400-e29b-41d4-a716-446655440000", Name: "x", Created: "2026-08-08 00:00:00", Updated: "2026-08-08 00:00:00"}
	for _, tc := range []struct {
		name string
		doc  Document
	}{
		{"unsupported version", Document{Version: 2}},
		{"duplicate id", Document{Version: 1, Entries: []Entry{base, base}}},
		{"empty name", Document{Version: 1, Entries: []Entry{{ID: base.ID, Created: base.Created, Updated: base.Updated}}}},
		{"invalid id", Document{Version: 1, Entries: []Entry{{ID: "not-a-uuid", Name: "x", Created: base.Created, Updated: base.Updated}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.doc.Validate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
	if err := (Document{Version: 1, Entries: []Entry{base}}).Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestApplyAdvancesUpdatedAtSecondPrecisionAndValidationRejectsReversedTime(t *testing.T) {
	now := time.Date(2026, 8, 8, 12, 0, 0, 500, time.Local)
	entry, err := NewEntry("Demo", "", nil, "blob", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	before := entry.Updated
	entry.Apply("Changed", "", nil, "blob", nil, now)
	if entry.Updated == before {
		t.Fatalf("updated timestamp did not advance: %q", entry.Updated)
	}
	entry.Created = "2026-08-08 12:00:02"
	entry.Updated = "2026-08-08 12:00:01"
	if err := (Document{Version: SchemaVersion, Entries: []Entry{entry}}).Validate(); err == nil {
		t.Fatal("validation accepted updated before created")
	}
}
