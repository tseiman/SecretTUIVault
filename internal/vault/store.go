package vault

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"gopkg.in/yaml.v3"
)

var ErrConflict = errors.New("vault changed since it was loaded")

type CommittedError struct{ Err error }

func (e *CommittedError) Error() string { return e.Err.Error() }
func (e *CommittedError) Unwrap() error { return e.Err }

type Store struct {
	path         string
	loaded       bool
	existed      bool
	fingerprint  [sha256.Size]byte
	beforeRename func(string) error
	syncDir      func(string) error
}

func NewStore(path string) *Store { return &Store{path: path, syncDir: syncDirectory} }
func (s *Store) Path() string     { return s.path }

func (s *Store) Load() (Document, error) {
	if err := rejectSymlinkComponents(s.path); err != nil {
		return Document{}, err
	}
	data, exists, err := readRegular(s.path)
	if err != nil {
		return Document{}, err
	}
	s.loaded = true
	s.existed = exists
	if !exists {
		s.fingerprint = [sha256.Size]byte{}
		return Document{Version: SchemaVersion, Entries: []Entry{}}, nil
	}
	s.fingerprint = sha256.Sum256(data)
	var doc Document
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&doc); err != nil {
		return Document{}, fmt.Errorf("decode vault: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return Document{}, errors.New("decode vault: multiple YAML documents are not supported")
		}
		return Document{}, fmt.Errorf("decode vault: %w", err)
	}
	if doc.Entries == nil {
		doc.Entries = []Entry{}
	}
	if err := doc.Validate(); err != nil {
		return Document{}, err
	}
	doc.NormalizeTags()
	return doc, nil
}

func (s *Store) Save(doc Document, overwrite bool) (err error) {
	if err := rejectSymlinkComponents(s.path); err != nil {
		return err
	}
	doc.NormalizeTags()
	if err := doc.Validate(); err != nil {
		return err
	}
	current, exists, err := readRegular(s.path)
	if err != nil {
		return err
	}
	if !overwrite && s.changed(exists, current) {
		return ErrConflict
	}
	data, err := yaml.Marshal(doc)
	if err != nil {
		return fmt.Errorf("encode vault: %w", err)
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create vault directory: %w", err)
	}

	var backupTemp string
	defer func() { err = joinCleanupError(err, backupTemp) }()
	if exists {
		backupTemp, err = prepareAtomicFile(s.path+".bak", current, 0o600)
		if err != nil {
			return fmt.Errorf("prepare backup: %w", err)
		}
	}
	vaultTemp, err := prepareAtomicFile(s.path, data, 0o600)
	if err != nil {
		return fmt.Errorf("prepare vault: %w", err)
	}
	defer func() { err = joinCleanupError(err, vaultTemp) }()

	if s.beforeRename != nil {
		if err := s.beforeRename(vaultTemp); err != nil {
			return fmt.Errorf("before vault replace: %w", err)
		}
	}
	lateCurrent, lateExists, err := readRegular(s.path)
	if err != nil {
		return err
	}
	if !overwrite && s.changed(lateExists, lateCurrent) {
		return ErrConflict
	}
	lateChanged := exists != lateExists || (exists && !bytes.Equal(current, lateCurrent))
	if overwrite && lateChanged {
		if cleanupErr := joinCleanupError(nil, backupTemp); cleanupErr != nil {
			return fmt.Errorf("discard stale prepared backup: %w", cleanupErr)
		}
		backupTemp = ""
		if lateExists {
			backupTemp, err = prepareAtomicFile(s.path+".bak", lateCurrent, 0o600)
			if err != nil {
				return fmt.Errorf("prepare latest external backup: %w", err)
			}
		}
	}
	if err := replaceFile(vaultTemp, s.path); err != nil {
		return fmt.Errorf("replace vault: %w", err)
	}
	vaultTemp = ""

	// The vault replacement is now committed. Synchronize Store state before
	// performing post-commit backup and directory durability work.
	s.loaded = true
	s.existed = true
	s.fingerprint = sha256.Sum256(data)

	if backupTemp != "" {
		if err := replaceFile(backupTemp, s.path+".bak"); err != nil {
			return &CommittedError{Err: fmt.Errorf("vault committed but backup replacement failed: %w", err)}
		}
		backupTemp = ""
	}
	if err := s.syncDir(dir); err != nil {
		return &CommittedError{Err: fmt.Errorf("vault committed but directory sync failed: %w", err)}
	}
	return nil
}

func (s *Store) changed(exists bool, current []byte) bool {
	if !s.loaded {
		return exists
	}
	if exists != s.existed {
		return true
	}
	return exists && sha256.Sum256(current) != s.fingerprint
}

const maxVaultSize = 64 << 20

func rejectSymlinkComponents(path string) error {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve vault path: %w", err)
	}
	volume := filepath.VolumeName(absPath)
	rest := strings.TrimPrefix(absPath, volume)
	current := volume
	if filepath.IsAbs(absPath) {
		current += string(os.PathSeparator)
	}
	parts := strings.FieldsFunc(rest, func(r rune) bool { return r == '/' || r == '\\' })
	for i, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("inspect vault path component: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("vault path component %q must not be a symbolic link", current)
		}
		if i < len(parts)-1 && !info.IsDir() {
			return fmt.Errorf("vault parent component %q is not a directory", current)
		}
	}
	return nil
}

func readRegular(path string) ([]byte, bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("inspect vault: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, false, errors.New("vault path must not be a symbolic link")
	}
	if !info.Mode().IsRegular() {
		return nil, false, errors.New("vault path must be a regular file")
	}
	if info.Size() > maxVaultSize {
		return nil, false, fmt.Errorf("vault exceeds maximum size of %d bytes", maxVaultSize)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		if err := os.Chmod(path, 0o600); err != nil {
			return nil, false, fmt.Errorf("secure vault permissions: %w", err)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false, fmt.Errorf("read vault: %w", err)
	}
	if len(data) > maxVaultSize {
		return nil, false, fmt.Errorf("vault exceeds maximum size of %d bytes", maxVaultSize)
	}
	return data, true, nil
}

func prepareAtomicFile(path string, data []byte, mode os.FileMode) (name string, err error) {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	tmp, err := os.CreateTemp(dir, "."+base+".tmp-*")
	if err != nil {
		return "", err
	}
	name = tmp.Name()
	completed := false
	defer func() {
		if completed {
			return
		}
		if closeErr := tmp.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close temporary file: %w", closeErr))
		}
		err = joinCleanupError(err, name)
		name = ""
	}()
	if err = tmp.Chmod(mode); err != nil {
		return "", err
	}
	if _, err = tmp.Write(data); err != nil {
		return "", err
	}
	if err = tmp.Sync(); err != nil {
		return "", err
	}
	if err = tmp.Close(); err != nil {
		return "", err
	}
	completed = true
	return name, nil
}

func joinCleanupError(prior error, name string) error {
	if name == "" {
		return prior
	}
	if err := os.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.Join(prior, fmt.Errorf("remove secret-bearing temporary file %q: %w", name, err))
	}
	return prior
}

func syncDirectory(dir string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
