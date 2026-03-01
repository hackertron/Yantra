package types

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfig_FromFile(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "yantra.toml")
	content := `
[selection]
provider = "anthropic"
model = "claude-sonnet-4-20250514"

[runtime]
max_turns = 10
`
	if err := os.WriteFile(cfgFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(cfgFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Selection.Provider != "anthropic" {
		t.Fatalf("expected provider 'anthropic', got %q", cfg.Selection.Provider)
	}
	if cfg.Runtime.MaxTurns != 10 {
		t.Fatalf("expected max_turns 10, got %d", cfg.Runtime.MaxTurns)
	}
	// Defaults should still be present for unset fields
	if cfg.Gateway.Listen != "127.0.0.1:7700" {
		t.Fatalf("expected default gateway listen, got %q", cfg.Gateway.Listen)
	}
}

func TestLoadConfig_EnvOverride(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "yantra.toml")
	content := `
[selection]
provider = "openai"
model = "gpt-4o-mini"
`
	if err := os.WriteFile(cfgFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("YANTRA__SELECTION__PROVIDER", "gemini")

	cfg, err := LoadConfig(cfgFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Selection.Provider != "gemini" {
		t.Fatalf("expected env override to 'gemini', got %q", cfg.Selection.Provider)
	}
}
