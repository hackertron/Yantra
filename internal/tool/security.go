package tool

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/hackertron/Yantra/internal/types"
)

// SecurityPolicy determines whether a tool execution is permitted.
type SecurityPolicy interface {
	CheckExecution(t types.Tool, input json.RawMessage, execCtx types.ToolExecutionContext) error
}

// WorkspacePolicy enforces file-path containment and shell command restrictions.
type WorkspacePolicy struct {
	allowedCommands map[string]bool
	deniedCommands  map[string]bool
	allowOperators  bool
}

// defaultAllowlist is the set of commands permitted by default.
var defaultAllowlist = []string{
	"ls", "cat", "head", "tail", "wc", "find", "grep", "rg", "sed", "awk",
	"sort", "uniq", "cut", "tr", "tee", "diff", "patch",
	"git", "gh",
	"go", "gofmt", "goimports",
	"node", "npm", "npx", "yarn", "pnpm", "bun", "deno",
	"python", "python3", "pip", "pip3", "uv",
	"ruby", "gem", "bundle",
	"rustc", "cargo",
	"java", "javac", "mvn", "gradle",
	"make", "cmake",
	"docker", "docker-compose",
	"curl", "wget",
	"echo", "printf", "date", "env", "which", "whoami", "pwd",
	"mkdir", "cp", "mv", "touch", "chmod", "ln",
	"tar", "zip", "unzip", "gzip", "gunzip",
	"jq", "yq",
	"tree", "file", "stat", "du", "df",
}

// defaultDenylist is the set of commands that are always blocked.
var defaultDenylist = []string{
	"sudo", "su", "doas",
	"mkfs", "fdisk", "dd",
	"shutdown", "reboot", "halt", "poweroff", "init",
	"rm",
}

// shellOperators are the shell metacharacters blocked when AllowOperators is false.
var shellOperators = []string{"|", "&&", "||", ";", ">", ">>", "<", "$(", "`", "&"}

// NewWorkspacePolicy creates a WorkspacePolicy from a ShellConfig.
func NewWorkspacePolicy(cfg types.ShellConfig) *WorkspacePolicy {
	allowed := make(map[string]bool)
	denied := make(map[string]bool)

	if !cfg.ReplaceDefaults {
		for _, cmd := range defaultAllowlist {
			allowed[cmd] = true
		}
		for _, cmd := range defaultDenylist {
			denied[cmd] = true
		}
	}

	for _, cmd := range cfg.Allow {
		allowed[cmd] = true
	}
	for _, cmd := range cfg.Deny {
		denied[cmd] = true
	}

	return &WorkspacePolicy{
		allowedCommands: allowed,
		deniedCommands:  denied,
		allowOperators:  cfg.AllowOperators,
	}
}

// CheckExecution validates the tool invocation against workspace security rules.
func (p *WorkspacePolicy) CheckExecution(t types.Tool, input json.RawMessage, execCtx types.ToolExecutionContext) error {
	name := t.Name()

	switch name {
	case "read_file", "write_file", "list_files":
		return p.checkFilePath(input, execCtx.WorkspaceDir)
	case "shell_exec":
		return p.checkShellCommand(input)
	}

	return nil
}

// checkFilePath ensures any "path" field in the input resolves within the workspace.
func (p *WorkspacePolicy) checkFilePath(input json.RawMessage, workspace string) error {
	var args struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return fmt.Errorf("security: invalid input: %w", err)
	}
	if args.Path == "" {
		return fmt.Errorf("security: path is required")
	}
	_, err := ResolvePath(args.Path, workspace)
	return err
}

// ResolvePath resolves a tool path argument relative to the workspace and validates
// that the result stays within the workspace. Returns the resolved absolute path.
func ResolvePath(path, workspace string) (string, error) {
	if workspace == "" {
		return "", fmt.Errorf("security: workspace directory not set")
	}

	var resolved string
	if filepath.IsAbs(path) {
		resolved = filepath.Clean(path)
	} else {
		resolved = filepath.Clean(filepath.Join(workspace, path))
	}

	// The resolved path must be the workspace itself or a child of it.
	wsClean := filepath.Clean(workspace)
	if resolved != wsClean && !strings.HasPrefix(resolved, wsClean+string(filepath.Separator)) {
		return "", fmt.Errorf("security: path %q resolves outside workspace %q", path, workspace)
	}

	return resolved, nil
}

// checkShellCommand validates the command against allow/deny lists and operator restrictions.
func (p *WorkspacePolicy) checkShellCommand(input json.RawMessage) error {
	var args struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return fmt.Errorf("security: invalid input: %w", err)
	}
	if args.Command == "" {
		return fmt.Errorf("security: command is required")
	}

	// Check for shell operators first.
	if !p.allowOperators {
		for _, op := range shellOperators {
			if strings.Contains(args.Command, op) {
				return fmt.Errorf("security: shell operator %q is not allowed", op)
			}
		}
	}

	// Extract the base command name.
	base := extractBaseCommand(args.Command)

	// Deny overrides allow.
	if p.deniedCommands[base] {
		return fmt.Errorf("security: command %q is denied", base)
	}

	if !p.allowedCommands[base] {
		return fmt.Errorf("security: command %q is not in the allowlist", base)
	}

	return nil
}

// extractBaseCommand returns the first word of the command string,
// stripping any path prefix (e.g. "/usr/bin/git" → "git").
func extractBaseCommand(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	fields := strings.Fields(cmd)
	if len(fields) == 0 {
		return ""
	}
	return filepath.Base(fields[0])
}
