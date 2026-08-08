package app

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tseiman/SecretTUIVault/internal/ui"
)

func TestDefaultPathAndVaultOverride(t *testing.T) {
	home := func() (string, error) { return filepath.Join("tmp", "home"), nil }
	cfg, err := ParseArgs(nil, home)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.VaultPath != filepath.Join("tmp", "home", ".secrets", "vault.yaml") {
		t.Fatalf("path %q", cfg.VaultPath)
	}
	cfg, err = ParseArgs([]string{"--vault", "custom.yaml"}, home)
	if err != nil || cfg.VaultPath != "custom.yaml" {
		t.Fatalf("override %#v %v", cfg, err)
	}
}

func TestHelpVersionAndExplicitVaultDoNotRequireHomeDirectory(t *testing.T) {
	failingHome := func() (string, error) { return "", errors.New("home unavailable") }
	for _, args := range [][]string{{"--version"}, {"--vault", filepath.Join("tmp", "explicit.yaml")}} {
		if _, err := ParseArgs(args, failingHome); err != nil {
			t.Fatalf("ParseArgs(%v) required home: %v", args, err)
		}
	}
	var out, errOut bytes.Buffer
	if code := Execute([]string{"--help"}, &out, &errOut, failingHome, nil); code != 0 {
		t.Fatalf("help failed without home: code=%d stderr=%q", code, errOut.String())
	}
}

func TestExecuteHelpVersionBootstrapAndStartupError(t *testing.T) {
	homeDir := t.TempDir()
	home := func() (string, error) { return homeDir, nil }
	for _, tc := range []struct {
		args     []string
		contains string
	}{
		{[]string{"--help"}, "Usage: secretvault"}, {[]string{"--version"}, Version},
	} {
		var out, errOut bytes.Buffer
		if code := Execute(tc.args, &out, &errOut, home, nil); code != 0 {
			t.Fatalf("%v code %d stderr=%s", tc.args, code, errOut.String())
		}
		if !strings.Contains(out.String()+errOut.String(), tc.contains) {
			t.Fatalf("output missing %q", tc.contains)
		}
	}
	called := false
	code := Execute(nil, &bytes.Buffer{}, &bytes.Buffer{}, home, func(m ui.Model) error { called = true; return nil })
	if code != 0 || !called {
		t.Fatalf("missing vault bootstrap code=%d called=%v", code, called)
	}
	bad := filepath.Join(homeDir, "bad.yaml")
	os.WriteFile(bad, []byte("version: ["), 0o600)
	var errOut bytes.Buffer
	if code := Execute([]string{"--vault", bad}, &bytes.Buffer{}, &errOut, home, func(ui.Model) error { return nil }); code != 1 || !strings.Contains(errOut.String(), "decode vault") {
		t.Fatalf("startup error code=%d err=%s", code, errOut.String())
	}
}
