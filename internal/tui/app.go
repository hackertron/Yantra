package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/hackertron/Yantra/internal/types"
)

// App is the root Bubble Tea model that composes all TUI components.
type App struct {
	// Components
	chat   ChatModel
	input  InputModel
	client *Client

	// State
	width, height int
	ready         bool   // after first WindowSizeMsg
	turning       bool   // turn in progress
	connected     bool
	connStatus    string // "connected", "disconnected", "reconnecting"
	sessionID     string
	tokenStatus   string // "turns=2 tokens=1543"
	provider      string // provider/model label
	version       string
	styles        Styles
	err           error // fatal startup error
}

// NewApp creates the root TUI application model.
func NewApp(client *Client, providerLabel, version string, hasDark bool) App {
	styles := NewStyles(hasDark)
	return App{
		chat:       NewChatModel(styles),
		input:      NewInputModel(styles),
		client:     client,
		connStatus: "connecting",
		provider:   providerLabel,
		version:    version,
		styles:     styles,
	}
}

// Init returns the initial commands (spinner tick).
func (a App) Init() tea.Cmd {
	return a.chat.Init()
}

// Update handles all incoming messages.
func (a App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		a.width = msg.Width
		a.height = msg.Height
		a.recalcLayout()

		if !a.ready {
			a.ready = true
			cmds = append(cmds, a.client.Connect())
		}
		return a, tea.Batch(cmds...)

	case tea.KeyPressMsg:
		return a.handleKey(msg)

	// Connection lifecycle
	case ConnectedMsg:
		a.connected = true
		a.connStatus = "connected"
		if msg.SessionID != "" {
			a.sessionID = msg.SessionID
		}
		return a, nil

	case DisconnectedMsg:
		a.connected = false
		if msg.Err != nil {
			a.connStatus = "reconnecting"
			a.chat.AppendError(fmt.Sprintf("Disconnected: %v", msg.Err))
			return a, a.client.Reconnect()
		}
		return a, nil

	case ReconnectingMsg:
		a.connStatus = fmt.Sprintf("reconnecting (%d)", msg.Attempt)
		return a, nil

	// Server frame messages
	case WelcomeMsg:
		a.sessionID = msg.SessionID
		a.connected = true
		a.connStatus = "connected"
		return a, nil

	case TextDeltaMsg:
		if !a.turning {
			a.turning = true
			a.chat.StartStreaming()
		}
		a.chat.AppendDelta(msg.Text)
		return a, nil

	case ToolProgressMsg:
		if !a.turning {
			a.turning = true
		}
		a.chat.ShowToolProgress(msg.Tool, msg.Status)
		return a, nil

	case TurnCompleteMsg:
		a.chat.FinishAllTools()
		a.chat.FinishStreaming(msg.Text)
		a.turning = false
		a.tokenStatus = msg.Status
		return a, nil

	case ErrorMsg:
		a.chat.AppendError(msg.Error)
		if a.turning {
			a.chat.FinishAllTools()
			a.chat.FinishStreaming("")
			a.turning = false
		}
		return a, nil

	case SessionListMsg:
		a.renderSessionList(msg.Sessions)
		return a, nil

	case SessionCreatedMsg:
		a.sessionID = msg.SessionID
		a.chat.Clear()
		a.chat.AppendSystem(fmt.Sprintf("New session: %s", truncateID(msg.SessionID)))
		return a, nil

	case SessionSwitchedMsg:
		a.sessionID = msg.SessionID
		a.chat.Clear()
		a.chat.AppendSystem(fmt.Sprintf("Switched to session: %s", truncateID(msg.SessionID)))
		return a, nil

	case GatewayReadyMsg:
		return a, nil

	case GatewayFailedMsg:
		a.err = msg.Err
		return a, tea.Quit
	}

	// Forward remaining messages to components.
	chatCmd := a.chat.Update(msg)
	if chatCmd != nil {
		cmds = append(cmds, chatCmd)
	}

	return a, tea.Batch(cmds...)
}

// View renders the full TUI layout.
func (a App) View() tea.View {
	if a.err != nil {
		return tea.NewView(fmt.Sprintf("\n  Fatal error: %v\n\n  Press any key to exit.\n", a.err))
	}

	if !a.ready {
		return tea.NewView("  Initializing...")
	}

	header := a.renderHeader()
	chatView := a.chat.View()
	inputView := a.input.View()
	status := a.renderStatusBar()

	content := lipgloss.JoinVertical(lipgloss.Left,
		header,
		chatView,
		inputView,
		status,
	)

	v := tea.NewView(content)
	v.AltScreen = true
	return v
}

// handleKey processes key press events.
func (a App) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	switch key {
	case "ctrl+c":
		if a.turning {
			a.client.SendCancel()
			a.turning = false
			a.chat.FinishAllTools()
			a.chat.FinishStreaming("")
			a.chat.AppendSystem("Cancelled.")
			return a, nil
		}
		return a, tea.Quit

	case "esc":
		if a.turning {
			a.client.SendCancel()
			a.turning = false
			a.chat.FinishAllTools()
			a.chat.FinishStreaming("")
			a.chat.AppendSystem("Cancelled.")
			return a, nil
		}
		return a, nil

	case "enter":
		if a.turning {
			return a, nil
		}
		return a.handleSend()

	case "alt+enter":
		cmd := a.input.Update(msg)
		return a, cmd
	}

	cmd := a.input.Update(msg)
	return a, cmd
}

// handleSend processes the current input (slash command or user message).
func (a App) handleSend() (tea.Model, tea.Cmd) {
	text := strings.TrimSpace(a.input.Value())
	if text == "" {
		return a, nil
	}

	a.input.Reset()

	if cmd := ParseSlashCommand(text); cmd != nil {
		return a.dispatchSlashCommand(cmd)
	}

	a.chat.AppendUserMessage(text)
	a.turning = true
	a.chat.StartStreaming()

	if err := a.client.SendMessage(text); err != nil {
		a.chat.FinishStreaming("")
		a.chat.AppendError(fmt.Sprintf("Send failed: %v", err))
		a.turning = false
	}

	return a, nil
}

// dispatchSlashCommand routes slash commands to the appropriate handler.
func (a App) dispatchSlashCommand(cmd *SlashCommand) (tea.Model, tea.Cmd) {
	switch cmd.Name {
	case "help":
		a.chat.AppendSystem(HelpText())
	case "clear":
		a.chat.Clear()
	case "quit":
		return a, tea.Quit
	case "new":
		name := cmd.Args
		if name == "" {
			name = "tui-session"
		}
		if err := a.client.SendSessionCmd("new", name); err != nil {
			a.chat.AppendError(fmt.Sprintf("Failed: %v", err))
		}
	case "sessions":
		if err := a.client.SendSessionCmd("list", ""); err != nil {
			a.chat.AppendError(fmt.Sprintf("Failed: %v", err))
		}
	case "switch":
		if cmd.Args == "" {
			a.chat.AppendError("Usage: /switch <session_id>")
		} else if err := a.client.SendSessionCmd("switch", cmd.Args); err != nil {
			a.chat.AppendError(fmt.Sprintf("Failed: %v", err))
		}
	case "cancel":
		if a.turning {
			a.client.SendCancel()
			a.turning = false
			a.chat.FinishAllTools()
			a.chat.FinishStreaming("")
			a.chat.AppendSystem("Cancelled.")
		} else {
			a.chat.AppendSystem("No active turn to cancel.")
		}
	default:
		a.chat.AppendError(fmt.Sprintf("Unknown command: /%s — type /help", cmd.Name))
	}
	return a, nil
}

// renderHeader builds the top header bar.
func (a App) renderHeader() string {
	title := a.styles.HeaderTitle.Render("yantra")

	var connDot string
	switch a.connStatus {
	case "connected":
		connDot = a.styles.ConnGreen.Render("●")
	case "disconnected":
		connDot = a.styles.ConnRed.Render("●")
	default:
		connDot = a.styles.ConnYellow.Render("●")
	}

	provider := a.styles.HeaderDim.Render(a.provider)
	conn := a.styles.HeaderDim.Render(a.connStatus)

	header := fmt.Sprintf("%s  %s  %s %s", title, provider, connDot, conn)
	return a.styles.HeaderBar.Width(a.width).Render(header)
}

// renderStatusBar builds the bottom status bar.
func (a App) renderStatusBar() string {
	var parts []string

	sid := truncateID(a.sessionID)
	if sid != "" {
		parts = append(parts, a.styles.StatusAccent.Render(sid))
	}
	if a.tokenStatus != "" {
		parts = append(parts, a.tokenStatus)
	}
	if a.turning {
		parts = append(parts, a.styles.Accent.Render("thinking..."))
	}

	if len(parts) == 0 {
		return ""
	}

	content := strings.Join(parts, a.styles.Dimmed.Render(" │ "))
	return a.styles.StatusBar.Width(a.width).Render(content)
}

// renderSessionList formats session records for display in chat.
func (a *App) renderSessionList(sessions []types.SessionRecord) {
	if len(sessions) == 0 {
		a.chat.AppendSystem("No sessions found.")
		return
	}

	var b strings.Builder
	b.WriteString("Sessions:\n")
	for _, s := range sessions {
		marker := "  "
		if s.ID == a.sessionID {
			marker = "▸ "
		}
		name := s.Name
		if name == "" {
			name = "(unnamed)"
		}
		b.WriteString(fmt.Sprintf("%s%s  %s  msgs=%d  %s\n",
			marker,
			truncateID(s.ID),
			name,
			s.MessageCount,
			s.UpdatedAt.Format("Jan 02 15:04"),
		))
	}
	a.chat.AppendSystem(b.String())
}

// recalcLayout distributes vertical space between components.
func (a *App) recalcLayout() {
	headerHeight := 2 // title + border
	statusHeight := 1
	inputHeight := a.input.Height()

	chatHeight := a.height - headerHeight - statusHeight - inputHeight
	if chatHeight < 1 {
		chatHeight = 1
	}

	a.chat.SetSize(a.width, chatHeight)
	a.input.SetWidth(a.width)
}

// truncateID returns the first 8 chars of an ID.
func truncateID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
