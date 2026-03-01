package tool

import "github.com/hackertron/Yantra/internal/types"

// RegisterBuiltins registers all built-in tools into the given registry.
func RegisterBuiltins(r *ToolRegistry, cfg types.ToolsConfig) error {
	tools := []types.Tool{
		NewReadFile(),
		NewWriteFile(),
		NewListFiles(),
		NewShellExec(),
		NewWebFetch(),
	}
	for _, t := range tools {
		if err := r.Register(t); err != nil {
			return err
		}
	}
	return nil
}
