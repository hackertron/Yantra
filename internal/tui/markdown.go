package tui

import (
	"strings"

	"github.com/charmbracelet/glamour"
)

// MarkdownRenderer wraps glamour for width-aware markdown rendering.
type MarkdownRenderer struct {
	width int
}

// NewMarkdownRenderer creates a renderer with the given wrap width.
func NewMarkdownRenderer(width int) *MarkdownRenderer {
	if width < 40 {
		width = 40
	}
	return &MarkdownRenderer{width: width}
}

// SetWidth updates the word wrap width.
func (m *MarkdownRenderer) SetWidth(w int) {
	if w < 40 {
		w = 40
	}
	m.width = w
}

// Render converts markdown text to styled terminal output.
func (m *MarkdownRenderer) Render(text string) string {
	r, err := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(m.width),
	)
	if err != nil {
		return text
	}
	out, err := r.Render(text)
	if err != nil {
		return text
	}
	return strings.TrimRight(out, "\n")
}
