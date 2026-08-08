package vault

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func sampleDocument(t *testing.T, blob string) Document {
	t.Helper()
	e, err := NewEntry("Example", "safe metadata", []string{"Test"}, blob, nil, time.Date(2026, 8, 8, 12, 0, 0, 0, time.Local))
	if err != nil {
		t.Fatal(err)
	}
	return Document{Version: SchemaVersion, Entries: []Entry{e}}
}

func TestStoreCreatesSecureVaultAndRoundTripsBlob(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "vault.yaml")
	s := NewStore(path)
	doc, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if doc.Version != SchemaVersion || len(doc.Entries) != 0 {
		t.Fatalf("new document = %#v", doc)
	}
	want := "unvalidated text\n  spaces\n\x00 is text"
	doc = sampleDocument(t, want)
	if err := s.Save(doc, false); err != nil {
		t.Fatal(err)
	}
	got, err := NewStore(path).Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.Entries[0].Blob != want {
		t.Fatalf("blob = %q", got.Entries[0].Blob)
	}
	if runtime.GOOS != "windows" {
		di, _ := os.Stat(filepath.Dir(path))
		fi, _ := os.Stat(path)
		if di.Mode().Perm() != 0o700 {
			t.Fatalf("dir mode %o", di.Mode().Perm())
		}
		if fi.Mode().Perm() != 0o600 {
			t.Fatalf("file mode %o", fi.Mode().Perm())
		}
	}
}

func TestStoreReportsCurrentFileSize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vault.yaml")
	store := NewStore(path)
	if size, err := store.Size(); err != nil || size != 0 {
		t.Fatalf("missing vault size=%d err=%v", size, err)
	}
	if _, err := store.Load(); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(sampleDocument(t, "opaque placeholder"), false); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if size, err := store.Size(); err != nil || size != info.Size() {
		t.Fatalf("vault size=%d err=%v, want %d", size, err, info.Size())
	}
}

func TestStoreDoesNotChangeExistingParentDirectoryMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission semantics")
	}
	parent := t.TempDir()
	if err := os.Chmod(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(parent, "vault.yaml")
	store := NewStore(path)
	if _, err := store.Load(); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(sampleDocument(t, "opaque"), false); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(parent)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("existing parent mode changed to %o", info.Mode().Perm())
	}
}

func TestStoreBackupConflictAndForcedOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vault.yaml")
	s := NewStore(path)
	if _, err := s.Load(); err != nil {
		t.Fatal(err)
	}
	first := sampleDocument(t, "first")
	if err := s.Save(first, false); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	external := strings.ReplaceAll(string(mustRead(t, path)), "first", "external")
	if err := os.WriteFile(path, []byte(external), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded.Entries[0].Blob = "mine"
	if err := s.Save(loaded, false); !errors.Is(err, ErrConflict) {
		t.Fatalf("want conflict, got %v", err)
	}
	if !strings.Contains(string(mustRead(t, path)), "external") {
		t.Fatal("conflict overwrote file")
	}
	if err := s.Save(loaded, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mustRead(t, path)), "mine") {
		t.Fatal("force did not save")
	}
	if !strings.Contains(string(mustRead(t, path+".bak")), "external") {
		t.Fatal("backup does not contain prior file")
	}
}

func TestStoreRejectsMalformedUnsupportedSymlinkAndNonFile(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct{ name, content string }{
		{"malformed", "version: ["}, {"version", "version: 2\nentries: []\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(dir, tc.name)
			os.WriteFile(p, []byte(tc.content), 0o600)
			_, err := NewStore(p).Load()
			if err == nil {
				t.Fatal("expected error")
			}
		})
	}
	t.Run("directory", func(t *testing.T) {
		_, err := NewStore(dir).Load()
		if err == nil {
			t.Fatal("expected error")
		}
	})
	if runtime.GOOS != "windows" {
		t.Run("symlink", func(t *testing.T) {
			target := filepath.Join(dir, "target")
			os.WriteFile(target, []byte("version: 1\nentries: []\n"), 0o600)
			link := filepath.Join(dir, "link")
			os.Symlink(target, link)
			_, err := NewStore(link).Load()
			if err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestStoreCleansTemporaryFileAfterInterruptedWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vault.yaml")
	s := NewStore(path)
	if _, err := s.Load(); err != nil {
		t.Fatal(err)
	}
	s.beforeRename = func(string) error { return errors.New("simulated interruption") }
	if err := s.Save(sampleDocument(t, "x"), false); err == nil {
		t.Fatal("expected error")
	}
	matches, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".vault.yaml.tmp-*"))
	if len(matches) != 0 {
		t.Fatalf("temporary files remain: %v", matches)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("destination unexpectedly created")
	}
}

func TestStoreCanonicalizesTagsFromExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vault.yaml")
	content := "version: 1\nentries:\n  - id: 550e8400-e29b-41d4-a716-446655440000\n    name: One\n    tags: [Linux, linux]\n    created: '2026-08-08 12:00:00'\n    updated: '2026-08-08 12:00:00'\n    blob: ''\n  - id: 550e8400-e29b-41d4-a716-446655440001\n    name: Two\n    tags: [LINUX, Ops]\n    created: '2026-08-08 12:00:00'\n    updated: '2026-08-08 12:00:00'\n    blob: ''\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	doc, err := NewStore(path).Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(doc.Entries[0].Tags, ",") + ";" + strings.Join(doc.Entries[1].Tags, ","); got != "Linux;Linux,Ops" {
		t.Fatalf("tags = %q", got)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestStoreCanonicalizesTagsBeforePersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vault.yaml")
	doc := sampleDocument(t, "blob")
	doc.Entries[0].Tags = []string{" Linux ", "linux", "", "OPS"}
	doc.Entries = append(doc.Entries, Entry{
		ID: "11111111-1111-4111-8111-111111111111", Name: "Second",
		Tags:    []string{"ops", "Cloud", " cloud "},
		Created: "2026-08-08 12:00:00", Updated: "2026-08-08 12:00:00",
	})
	store := NewStore(path)
	if _, err := store.Load(); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(doc, false); err != nil {
		t.Fatal(err)
	}
	var loaded Document
	if err := yaml.Unmarshal(mustRead(t, path), &loaded); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(loaded.Entries[0].Tags, ","); got != "Linux,OPS" {
		t.Fatalf("first tags not canonical: %q", got)
	}
	if got := strings.Join(loaded.Entries[1].Tags, ","); got != "OPS,Cloud" {
		t.Fatalf("cross-entry tags not canonical: %q", got)
	}
}

func TestStoreDetectsExternalChangeImmediatelyBeforeReplace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vault.yaml")
	store := NewStore(path)
	if _, err := store.Load(); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(sampleDocument(t, "generation A"), false); err != nil {
		t.Fatal(err)
	}
	external := []byte("external bytes that must survive")
	store.beforeRename = func(string) error {
		return os.WriteFile(path, external, 0o600)
	}
	err := store.Save(sampleDocument(t, "generation B"), false)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("expected late conflict, got %v", err)
	}
	if got := mustRead(t, path); string(got) != string(external) {
		t.Fatalf("external update was lost: %q", got)
	}
	if _, err := os.Stat(path + ".bak"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("backup rotated during conflicted save: %v", err)
	}
}

func TestForcedOverwriteBacksUpLatestLateExternalBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vault.yaml")
	store := NewStore(path)
	if _, err := store.Load(); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(sampleDocument(t, "generation A"), false); err != nil {
		t.Fatal(err)
	}
	lateExternal := []byte("version: 1\nentries: []\n# latest external revision\n")
	store.beforeRename = func(string) error {
		return os.WriteFile(path, lateExternal, 0o600)
	}
	if err := store.Save(sampleDocument(t, "forced replacement"), true); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, path+".bak"); string(got) != string(lateExternal) {
		t.Fatalf("backup did not preserve latest external bytes:\n%s", got)
	}
}

func TestFailedSaveDoesNotRotateLastGoodBackup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vault.yaml")
	store := NewStore(path)
	if _, err := store.Load(); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(sampleDocument(t, "A"), false); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(sampleDocument(t, "B"), false); err != nil {
		t.Fatal(err)
	}
	backupA := mustRead(t, path+".bak")
	store.beforeRename = func(string) error { return errors.New("simulated final replace failure") }
	if err := store.Save(sampleDocument(t, "C"), false); err == nil {
		t.Fatal("expected save failure")
	}
	if got := mustRead(t, path+".bak"); string(got) != string(backupA) {
		t.Fatal("failed save rotated away the last good backup")
	}
}

func TestLoadHardensExistingVaultMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission semantics")
	}
	path := filepath.Join(t.TempDir(), "vault.yaml")
	data, err := yaml.Marshal(sampleDocument(t, "opaque"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewStore(path).Load(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("vault mode after load = %o", info.Mode().Perm())
	}
}

func TestPostCommitDurabilityErrorKeepsStoreStateSynchronized(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vault.yaml")
	store := NewStore(path)
	if _, err := store.Load(); err != nil {
		t.Fatal(err)
	}
	store.syncDir = func(string) error { return errors.New("simulated directory sync failure") }
	err := store.Save(sampleDocument(t, "committed"), false)
	var committed *CommittedError
	if !errors.As(err, &committed) {
		t.Fatalf("expected committed error, got %v", err)
	}
	if !strings.Contains(string(mustRead(t, path)), "committed") {
		t.Fatal("new vault was not committed")
	}
	store.syncDir = syncDirectory
	if err := store.Save(sampleDocument(t, "next"), false); err != nil {
		t.Fatalf("store fingerprint was stale after committed error: %v", err)
	}
}

func TestStoreRejectsSymlinkedPathComponent(t *testing.T) {
	realParent := t.TempDir()
	linkParent := filepath.Join(t.TempDir(), "linked")
	if err := os.Symlink(realParent, linkParent); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	store := NewStore(filepath.Join(linkParent, "vault.yaml"))
	if _, err := store.Load(); err == nil || !strings.Contains(strings.ToLower(err.Error()), "symbolic link") {
		t.Fatalf("symlinked parent was accepted: %v", err)
	}
}
