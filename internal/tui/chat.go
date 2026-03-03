package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/viewport"
)

// ChatMessage represents a single message in the chat history.
type ChatMessage struct {
	Role      string // "user", "assistant", "tool", "error", "system"
	Content   string // raw text
	Rendered  string // glamour output (cached, set on turn_complete)
	ToolName  string
	Streaming bool
}

// ChatModel manages the chat viewport and message history.
type ChatModel struct {
	viewport   viewport.Model
	messages   []ChatMessage
	spinner    spinner.Model
	md         *MarkdownRenderer
	styles     Styles
	autoScroll bool
	width      int
	height     int
}

// NewChatModel creates a new chat viewport.
func NewChatModel(styles Styles) ChatModel {
	sp := spinner.New(
		spinner.WithSpinner(spinner.Dot),
	)

	vp := viewport.New()
	vp.MouseWheelEnabled = true
	vp.SoftWrap = true

	return ChatModel{
		viewport:   vp,
		spinner:    sp,
		md:         NewMarkdownRenderer(80),
		styles:     styles,
		autoScroll: true,
	}
}

// SetSize updates the viewport dimensions.
func (c *ChatModel) SetSize(width, height int) {
	c.width = width
	c.height = height
	c.viewport.SetWidth(width)
	c.viewport.SetHeight(height)
	c.md.SetWidth(width - 4)
	c.rerender()
}

// AppendUserMessage adds a user message and re-renders.
func (c *ChatModel) AppendUserMessage(text string) {
	c.messages = append(c.messages, ChatMessage{
		Role:    "user",
		Content: text,
	})
	c.rerender()
}

// StartStreaming creates a new empty assistant message for streaming.
func (c *ChatModel) StartStreaming() {
	c.messages = append(c.messages, ChatMessage{
		Role:      "assistant",
		Streaming: true,
	})
	c.rerender()
}

// AppendDelta appends text to the current streaming message.
func (c *ChatModel) AppendDelta(text string) {
	if len(c.messages) == 0 {
		return
	}
	last := &c.messages[len(c.messages)-1]
	if last.Role == "assistant" && last.Streaming {
		last.Content += text
		c.rerender()
	}
}

// FinishStreaming marks streaming done and renders markdown.
func (c *ChatModel) FinishStreaming(fullText string) {
	if len(c.messages) == 0 {
		return
	}
	last := &c.messages[len(c.messages)-1]
	if last.Role == "assistant" && last.Streaming {
		last.Streaming = false
		if fullText != "" {
			last.Content = fullText
		}
		last.Rendered = c.md.Render(last.Content)
		c.rerender()
	}
}

// ShowToolProgress adds or updates a tool progress line.
func (c *ChatModel) ShowToolProgress(tool, status string) {
	// Check if there's already a tool line for this tool.
	for i := len(c.messages) - 1; i >= 0; i-- {
		if c.messages[i].Role == "tool" && c.messages[i].ToolName == tool {
			c.messages[i].Content = status
			// Mark streaming=false when done (status contains "complete" or "done").
			if strings.Contains(strings.ToLower(status), "complete") ||
				strings.Contains(strings.ToLower(status), "done") ||
				strings.Contains(strings.ToLower(status), "result") {
				c.messages[i].Streaming = false
			}
			c.rerender()
			return
		}
		// Stop searching once we hit a non-tool message.
		if c.messages[i].Role != "tool" && c.messages[i].Role != "assistant" {
			break
		}
	}

	c.messages = append(c.messages, ChatMessage{
		Role:      "tool",
		ToolName:  tool,
		Content:   status,
		Streaming: true,
	})
	c.rerender()
}

// AppendError adds an error message.
func (c *ChatModel) AppendError(text string) {
	c.messages = append(c.messages, ChatMessage{
		Role:    "error",
		Content: text,
	})
	c.rerender()
}

// AppendSystem adds a system/info message.
func (c *ChatModel) AppendSystem(text string) {
	c.messages = append(c.messages, ChatMessage{
		Role:    "system",
		Content: text,
	})
	c.rerender()
}

// Clear removes all messages.
func (c *ChatModel) Clear() {
	c.messages = nil
	c.viewport.SetContent("")
	c.viewport.GotoTop()
}

// Update handles viewport and spinner updates.
func (c *ChatModel) Update(msg tea.Msg) tea.Cmd {
	var cmds []tea.Cmd

	switch msg.(type) {
	case spinner.TickMsg:
		var cmd tea.Cmd
		c.spinner, cmd = c.spinner.Update(msg)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
		// Re-render to update spinner frames.
		if c.hasActiveSpinner() {
			c.rerender()
		}
	}

	// Track if user has scrolled up.
	prevPercent := c.viewport.ScrollPercent()
	var cmd tea.Cmd
	c.viewport, cmd = c.viewport.Update(msg)
	if cmd != nil {
		cmds = append(cmds, cmd)
	}

	// Re-enable auto-scroll when user scrolls back to bottom.
	newPercent := c.viewport.ScrollPercent()
	if prevPercent < 1.0 && newPercent >= 1.0 {
		c.autoScroll = true
	} else if newPercent < prevPercent {
		c.autoScroll = false
	}

	return tea.Batch(cmds...)
}

// View returns the rendered viewport string.
func (c *ChatModel) View() string {
	return c.viewport.View()
}

// Init returns the spinner tick command.
func (c *ChatModel) Init() tea.Cmd {
	return c.spinner.Tick
}

// hasActiveSpinner returns true if any tool line is still in progress.
func (c *ChatModel) hasActiveSpinner() bool {
	for _, m := range c.messages {
		if m.Role == "tool" && m.Streaming {
			return true
		}
	}
	return false
}

// rerender rebuilds the viewport content from all messages.
func (c *ChatModel) rerender() {
	var b strings.Builder

	for _, msg := range c.messages {
		switch msg.Role {
		case "user":
			label := c.styles.UserLabel.Render("You")
			b.WriteString(label + "\n")
			b.WriteString(c.styles.UserMessage.Render(msg.Content))
			b.WriteString("\n\n")

		case "assistant":
			label := c.styles.AssistantLabel.Render("Yantra")
			b.WriteString(label + "\n")
			if msg.Streaming {
				b.WriteString(msg.Content)
				b.WriteString(c.styles.AssistantLabel.Render("\u258c")) // ▌ cursor
				b.WriteString("\n\n")
			} else if msg.Rendered != "" {
				b.WriteString(msg.Rendered)
				b.WriteString("\n\n")
			} else {
				b.WriteString(msg.Content)
				b.WriteString("\n\n")
			}

		case "tool":
			if msg.Streaming {
				frame := c.spinner.View()
				line := fmt.Sprintf("%s %s: %s",
					c.styles.ToolLine.Render(frame),
					c.styles.ToolLine.Render(msg.ToolName),
					c.styles.Dimmed.Render(msg.Content),
				)
				b.WriteString(line + "\n")
			} else {
				line := fmt.Sprintf("%s %s: %s",
					c.styles.ToolLine.Render("\u2713"), // ✓
					c.styles.ToolLine.Render(msg.ToolName),
					c.styles.Dimmed.Render(msg.Content),
				)
				b.WriteString(line + "\n")
			}

		case "error":
			b.WriteString(c.styles.ErrorText.Render("Error: "+msg.Content) + "\n\n")

		case "system":
			b.WriteString(c.styles.Dimmed.Render(msg.Content) + "\n\n")
		}
	}

	content := b.String()
	if len(content) > 0 && content[len(content)-1] == '\n' {
		content = content[:len(content)-1]
	}

	c.viewport.SetContent(content)
	if c.autoScroll {
		c.viewport.GotoBottom()
	}
}

// FinishAllTools marks all in-progress tool messages as done.
func (c *ChatModel) FinishAllTools() {
	for i := range c.messages {
		if c.messages[i].Role == "tool" && c.messages[i].Streaming {
			c.messages[i].Streaming = false
		}
	}
}

