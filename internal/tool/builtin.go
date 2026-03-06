package tool

import "github.com/hackertron/Yantra/internal/types"

// RegisterBuiltins registers all built-in tools into the given registry.
// If mem is non-nil, memory tools (memory_search, memory_save) are included.
func RegisterBuiltins(r *ToolRegistry, cfg types.ToolsConfig, mem types.MemoryRetrieval) error {
	tools := []types.Tool{
		NewReadFile(),
		NewWriteFile(),
		NewEditFile(),
		NewListFiles(),
		NewShellExec(),
		NewWebFetch(),
		NewWebSearch(cfg.WebSearch),
	}
	if mem != nil {
		tools = append(tools, NewMemorySearch(mem), NewMemorySave(mem))
	}
	for _, t := range tools {
		if err := r.Register(t); err != nil {
			return err
		}
	}
	return nil
}
