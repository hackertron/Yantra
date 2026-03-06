package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/bubbles/v2/textarea"
)

const (
	inputMinHeight = 1
	inputMaxHeight = 5
)

// InputModel wraps a textarea for the chat input area.
type InputModel struct {
	textarea textarea.Model
	styles   Styles
	width    int
}

// NewInputModel creates the input textarea.
func NewInputModel(styles Styles) InputModel {
	ta := textarea.New()
	ta.Placeholder = "Type a message... (Enter send, /help for commands)"
	ta.ShowLineNumbers = false
	ta.SetHeight(inputMinHeight)
	ta.CharLimit = 0 // no limit
	ta.Focus()

	return InputModel{
		textarea: ta,
		styles:   styles,
	}
}

// SetWidth updates the textarea width.
func (i *InputModel) SetWidth(w int) {
	i.width = w
	i.textarea.SetWidth(w)
}

// Value returns the current input text.
func (i *InputModel) Value() string {
	return i.textarea.Value()
}

// Reset clears the input and shrinks back to minimum height.
func (i *InputModel) Reset() {
	i.textarea.Reset()
	i.textarea.SetHeight(inputMinHeight)
}

// Focus gives focus to the textarea.
func (i *InputModel) Focus() tea.Cmd {
	return i.textarea.Focus()
}

// Blur removes focus from the textarea.
func (i *InputModel) Blur() {
	i.textarea.Blur()
}

// IsSlashCommand checks if the current input starts with /.
func (i *InputModel) IsSlashCommand() bool {
	return strings.HasPrefix(strings.TrimSpace(i.textarea.Value()), "/")
}

// Update passes messages to the textarea and adjusts height.
func (i *InputModel) Update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	i.textarea, cmd = i.textarea.Update(msg)
	i.adjustHeight()
	return cmd
}

// View returns the rendered textarea.
func (i *InputModel) View() string {
	return i.textarea.View()
}

// adjustHeight dynamically adjusts the textarea height based on content.
func (i *InputModel) adjustHeight() {
	val := i.textarea.Value()
	lines := strings.Count(val, "\n") + 1
	h := lines
	if h < inputMinHeight {
		h = inputMinHeight
	}
	if h > inputMaxHeight {
		h = inputMaxHeight
	}
	i.textarea.SetHeight(h)
}

// Height returns the current height of the input area including chrome.
func (i *InputModel) Height() int {
	val := i.textarea.Value()
	lines := strings.Count(val, "\n") + 1
	h := lines
	if h < inputMinHeight {
		h = inputMinHeight
	}
	if h > inputMaxHeight {
		h = inputMaxHeight
	}
	// textarea chrome adds ~2 lines (border)
	return h + 2
}
