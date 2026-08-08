package ui

import "github.com/charmbracelet/lipgloss"

var (
	titleStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	parameterStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#F2F2F2"))
	detailNameStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("81"))
	actionStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#000000")).Background(lipgloss.Color("#5FD7FF"))
	selectedStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
	mutedStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	borderStyle     = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("240")).Padding(0, 1)
	errorStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	modalStyle      = lipgloss.NewStyle().Border(lipgloss.DoubleBorder()).BorderForeground(lipgloss.Color("214")).Padding(1, 2)
)
