package app

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"

	"github.com/tseiman/SecretTUIVault/internal/ui"
	"github.com/tseiman/SecretTUIVault/internal/vault"
)

const Version = "1.0.0"

type Config struct {
	VaultPath   string
	ShowVersion bool
}

type HomeDirFunc func() (string, error)
type Runner func(ui.Model) error

func ParseArgs(args []string, home HomeDirFunc) (Config, error) {
	return parseArgs(args, home, io.Discard)
}

func parseArgs(args []string, home HomeDirFunc, output io.Writer) (Config, error) {
	cfg := Config{}
	flags := flag.NewFlagSet("secretvault", flag.ContinueOnError)
	flags.SetOutput(output)
	flags.StringVar(&cfg.VaultPath, "vault", "", "path to the YAML vault (default ~/.secrets/vault.yaml)")
	flags.BoolVar(&cfg.ShowVersion, "version", false, "print version and exit")
	flags.Usage = func() {
		fmt.Fprintln(output, "Usage: secretvault [--vault PATH] [--version]")
		fmt.Fprintln(output, "Offline TUI for metadata and opaque text blobs.")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		return Config{}, err
	}
	if flags.NArg() != 0 {
		return Config{}, fmt.Errorf("unexpected argument %q", flags.Arg(0))
	}
	vaultProvided := false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "vault" {
			vaultProvided = true
		}
	})
	if vaultProvided && cfg.VaultPath == "" {
		return Config{}, errors.New("vault path must not be empty")
	}
	if cfg.VaultPath == "" && !cfg.ShowVersion {
		homeDir, err := home()
		if err != nil {
			return Config{}, fmt.Errorf("find home directory: %w", err)
		}
		cfg.VaultPath = filepath.Join(homeDir, ".secrets", "vault.yaml")
	}
	return cfg, nil
}

func Execute(args []string, stdout, stderr io.Writer, home HomeDirFunc, runner Runner) int {
	cfg, err := parseArgs(args, home, stdout)
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err != nil {
		fmt.Fprintln(stderr, "secretvault:", err)
		return 2
	}
	if cfg.ShowVersion {
		fmt.Fprintf(stdout, "secretvault %s\n", Version)
		return 0
	}
	store := vault.NewStore(cfg.VaultPath)
	doc, err := store.Load()
	if err != nil {
		fmt.Fprintln(stderr, "secretvault:", err)
		return 1
	}
	if runner == nil {
		fmt.Fprintln(stderr, "secretvault: no terminal runner configured")
		return 1
	}
	if err := runner(ui.New(doc, store)); err != nil {
		fmt.Fprintln(stderr, "secretvault:", err)
		return 1
	}
	return 0
}
