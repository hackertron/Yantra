package tui

import (
	"os"

	"charm.land/lipgloss/v2"
)

// Styles holds all lipgloss styles used across TUI components.
type Styles struct {
	// Header
	HeaderTitle lipgloss.Style
	HeaderDim   lipgloss.Style
	HeaderBar   lipgloss.Style

	// Message indicators
	UserIndicator      lipgloss.Style
	AssistantIndicator lipgloss.Style
	SystemIndicator    lipgloss.Style

	// Message content
	UserMessage      lipgloss.Style
	AssistantMessage lipgloss.Style

	// Tool progress
	ToolName   lipgloss.Style
	ToolStatus lipgloss.Style
	ToolDone   lipgloss.Style

	// Errors
	ErrorLabel lipgloss.Style
	ErrorText  lipgloss.Style

	// Status bar
	StatusBar    lipgloss.Style
	StatusAccent lipgloss.Style

	// Connection
	ConnGreen  lipgloss.Style
	ConnRed    lipgloss.Style
	ConnYellow lipgloss.Style

	// Misc
	Dimmed   lipgloss.Style
	HelpKey  lipgloss.Style
	HelpDesc lipgloss.Style
	Border   lipgloss.Style
	Accent   lipgloss.Style
}

// NewStyles creates a Styles set that adapts to the terminal background.
func NewStyles() Styles {
	hasDark := lipgloss.HasDarkBackground(os.Stdin, os.Stdout)
	ld := lipgloss.LightDark(hasDark)

	accent := lipgloss.Color("#AB9DF2") // soft purple
	dimFg := ld(lipgloss.Color("#999999"), lipgloss.Color("#555555"))
	subtleFg := ld(lipgloss.Color("#666666"), lipgloss.Color("#888888"))
	userColor := lipgloss.Color("#78DCE8") // cyan
	errorColor := lipgloss.Color("#FF6188") // red-pink
	greenColor := lipgloss.Color("#A9DC76") // green
	yellowColor := lipgloss.Color("#FFD866") // yellow
	borderColor := ld(lipgloss.Color("#DDDDDD"), lipgloss.Color("#333333"))

	return Styles{
		HeaderTitle: lipgloss.NewStyle().
			Foreground(accent).
			Bold(true),

		HeaderDim: lipgloss.NewStyle().
			Foreground(dimFg),

		HeaderBar: lipgloss.NewStyle().
			BorderStyle(lipgloss.NormalBorder()).
			BorderBottom(true).
			BorderForeground(borderColor).
			Padding(0, 1),

		UserIndicator: lipgloss.NewStyle().
			Foreground(userColor).
			Bold(true).
			SetString("❯"),

		AssistantIndicator: lipgloss.NewStyle().
			Foreground(accent).
			Bold(true).
			SetString("◆"),

		SystemIndicator: lipgloss.NewStyle().
			Foreground(dimFg).
			SetString("─"),

		UserMessage: lipgloss.NewStyle().
			Foreground(ld(lipgloss.Color("#2D2D2D"), lipgloss.Color("#E1E1E1"))),

		AssistantMessage: lipgloss.NewStyle(),

		ToolName: lipgloss.NewStyle().
			Foreground(yellowColor),

		ToolStatus: lipgloss.NewStyle().
			Foreground(dimFg),

		ToolDone: lipgloss.NewStyle().
			Foreground(greenColor),

		ErrorLabel: lipgloss.NewStyle().
			Foreground(errorColor).
			Bold(true),

		ErrorText: lipgloss.NewStyle().
			Foreground(errorColor),

		StatusBar: lipgloss.NewStyle().
			Foreground(subtleFg).
			Padding(0, 1),

		StatusAccent: lipgloss.NewStyle().
			Foreground(accent),

		ConnGreen: lipgloss.NewStyle().
			Foreground(greenColor),

		ConnRed: lipgloss.NewStyle().
			Foreground(errorColor),

		ConnYellow: lipgloss.NewStyle().
			Foreground(yellowColor),

		Dimmed: lipgloss.NewStyle().
			Foreground(dimFg),

		HelpKey: lipgloss.NewStyle().
			Foreground(accent).
			Bold(true),

		HelpDesc: lipgloss.NewStyle().
			Foreground(dimFg),

		Border: lipgloss.NewStyle().
			Foreground(borderColor),

		Accent: lipgloss.NewStyle().
			Foreground(accent),
	}
}
