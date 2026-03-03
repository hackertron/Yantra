package tui

import (
	"os"

	"charm.land/lipgloss/v2"
)

// Styles holds all lipgloss styles used across TUI components.
type Styles struct {
	// Header bar
	Header lipgloss.Style

	// Message labels
	UserLabel      lipgloss.Style
	AssistantLabel lipgloss.Style
	SystemLabel    lipgloss.Style

	// Message content
	UserMessage lipgloss.Style

	// Tool progress
	ToolLine lipgloss.Style

	// Errors
	ErrorText lipgloss.Style

	// Status bar
	StatusBar lipgloss.Style

	// Connection indicator colors
	ConnGreen  lipgloss.Style
	ConnRed    lipgloss.Style
	ConnYellow lipgloss.Style

	// Misc
	Dimmed   lipgloss.Style
	HelpKey  lipgloss.Style
	HelpDesc lipgloss.Style
}

// NewStyles creates a Styles set that adapts to the terminal background.
func NewStyles() Styles {
	hasDark := lipgloss.HasDarkBackground(os.Stdin, os.Stdout)
	ld := lipgloss.LightDark(hasDark)

	purple := lipgloss.Color("#7D56F4")
	dimFg := ld(lipgloss.Color("#888888"), lipgloss.Color("#666666"))

	return Styles{
		Header: lipgloss.NewStyle().
			Background(purple).
			Foreground(lipgloss.Color("#FFFFFF")).
			Bold(true).
			Padding(0, 1),

		UserLabel: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#61AFEF")).
			Bold(true),

		AssistantLabel: lipgloss.NewStyle().
			Foreground(purple).
			Bold(true),

		SystemLabel: lipgloss.NewStyle().
			Foreground(dimFg).
			Bold(true),

		UserMessage: lipgloss.NewStyle().
			Foreground(ld(lipgloss.Color("#333333"), lipgloss.Color("#E5E5E5"))),

		ToolLine: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#E5C07B")),

		ErrorText: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#E06C75")).
			Bold(true),

		StatusBar: lipgloss.NewStyle().
			Background(ld(lipgloss.Color("#E8E8E8"), lipgloss.Color("#2C2C2C"))).
			Foreground(dimFg).
			Padding(0, 1),

		ConnGreen: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#98C379")),

		ConnRed: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#E06C75")),

		ConnYellow: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#E5C07B")),

		Dimmed: lipgloss.NewStyle().
			Foreground(dimFg),

		HelpKey: lipgloss.NewStyle().
			Foreground(purple).
			Bold(true),

		HelpDesc: lipgloss.NewStyle().
			Foreground(dimFg),
	}
}
