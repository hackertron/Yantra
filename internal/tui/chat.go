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
	c.md.SetWidth(width - 6) // indent width
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
		if last.Content != "" {
			last.Rendered = c.md.Render(last.Content)
		}
		c.rerender()
	}
}

// ShowToolProgress adds or updates a tool progress line.
func (c *ChatModel) ShowToolProgress(tool, status string) {
	for i := len(c.messages) - 1; i >= 0; i-- {
		if c.messages[i].Role == "tool" && c.messages[i].ToolName == tool {
			c.messages[i].Content = status
			if strings.Contains(strings.ToLower(status), "complete") ||
				strings.Contains(strings.ToLower(status), "done") ||
				strings.Contains(strings.ToLower(status), "result") {
				c.messages[i].Streaming = false
			}
			c.rerender()
			return
		}
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
		if c.hasActiveSpinner() {
			c.rerender()
		}
	}

	prevPercent := c.viewport.ScrollPercent()
	var cmd tea.Cmd
	c.viewport, cmd = c.viewport.Update(msg)
	if cmd != nil {
		cmds = append(cmds, cmd)
	}

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

// indent prefixes every line of text with the given prefix.
func indent(text, prefix string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n")
}

// rerender rebuilds the viewport content from all messages.
func (c *ChatModel) rerender() {
	var b strings.Builder
	pad := "    " // 4-space indent for message body

	for i, msg := range c.messages {
		if i > 0 {
			b.WriteString("\n")
		}

		switch msg.Role {
		case "user":
			indicator := c.styles.UserIndicator.Render()
			b.WriteString(fmt.Sprintf("  %s %s\n", indicator, c.styles.UserMessage.Render(msg.Content)))

		case "assistant":
			indicator := c.styles.AssistantIndicator.Render()
			b.WriteString(fmt.Sprintf("  %s ", indicator))
			if msg.Streaming {
				if msg.Content == "" {
					b.WriteString(c.styles.Dimmed.Render("thinking..."))
				} else {
					// First line inline with indicator, rest indented
					lines := strings.Split(msg.Content, "\n")
					b.WriteString(lines[0])
					if len(lines) > 1 {
						b.WriteString("\n")
						b.WriteString(indent(strings.Join(lines[1:], "\n"), pad))
					}
					b.WriteString(c.styles.Accent.Render("▍"))
				}
				b.WriteString("\n")
			} else if msg.Rendered != "" {
				lines := strings.Split(msg.Rendered, "\n")
				b.WriteString(lines[0])
				if len(lines) > 1 {
					b.WriteString("\n")
					b.WriteString(indent(strings.Join(lines[1:], "\n"), pad))
				}
				b.WriteString("\n")
			} else if msg.Content != "" {
				lines := strings.Split(msg.Content, "\n")
				b.WriteString(lines[0])
				if len(lines) > 1 {
					b.WriteString("\n")
					b.WriteString(indent(strings.Join(lines[1:], "\n"), pad))
				}
				b.WriteString("\n")
			} else {
				b.WriteString("\n")
			}

		case "tool":
			var icon string
			if msg.Streaming {
				icon = c.spinner.View()
			} else {
				icon = c.styles.ToolDone.Render("✓")
			}
			name := c.styles.ToolName.Render(msg.ToolName)
			status := c.styles.ToolStatus.Render(msg.Content)
			b.WriteString(fmt.Sprintf("    %s %s %s\n", icon, name, status))

		case "error":
			label := c.styles.ErrorLabel.Render("  ✗ error:")
			b.WriteString(fmt.Sprintf("%s %s\n", label, c.styles.ErrorText.Render(msg.Content)))

		case "system":
			lines := strings.Split(msg.Content, "\n")
			for _, line := range lines {
				b.WriteString(fmt.Sprintf("    %s\n", c.styles.Dimmed.Render(line)))
			}
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
