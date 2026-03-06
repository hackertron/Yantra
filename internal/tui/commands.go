package tui

import "strings"

// SlashCommand represents a parsed slash command.
type SlashCommand struct {
	Name string
	Args string
}

// ParseSlashCommand parses a "/command args" string.
// Returns nil if the input is not a slash command.
func ParseSlashCommand(input string) *SlashCommand {
	input = strings.TrimSpace(input)
	if !strings.HasPrefix(input, "/") {
		return nil
	}
	input = input[1:] // strip leading /
	parts := strings.SplitN(input, " ", 2)
	cmd := &SlashCommand{Name: strings.ToLower(parts[0])}
	if len(parts) > 1 {
		cmd.Args = strings.TrimSpace(parts[1])
	}
	return cmd
}

// IsValidCommand checks if a command name is recognized.
func IsValidCommand(name string) bool {
	switch name {
	case "new", "sessions", "switch", "cancel", "clear", "help", "quit":
		return true
	}
	return false
}

// HelpText returns formatted help for all slash commands.
func HelpText() string {
	return `Available commands:
  /new [name]     Create a new session
  /sessions       List all sessions
  /switch <id>    Switch to a session by ID
  /cancel         Cancel the current turn
  /clear          Clear the chat display
  /help           Show this help message
  /quit           Exit the TUI

Shortcuts:
  Enter           Send message
  Alt+Enter       Insert newline
  Ctrl+C          Cancel turn / quit
  Esc             Cancel current turn`
}
