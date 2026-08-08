package main

import (
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/tseiman/SecretTUIVault/internal/app"
	"github.com/tseiman/SecretTUIVault/internal/ui"
)

func main() {
	code := app.Execute(os.Args[1:], os.Stdout, os.Stderr, os.UserHomeDir, func(model ui.Model) error {
		_, err := tea.NewProgram(model, tea.WithAltScreen()).Run()
		return err
	})
	os.Exit(code)
}
