package types

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/knadh/koanf/parsers/toml"
	"github.com/knadh/koanf/providers/env"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/providers/structs"
	"github.com/knadh/koanf/v2"
)

// LoadConfig loads configuration with the following precedence (highest wins):
//  1. Built-in defaults
//  2. Config file (explicit path or auto-discovered)
//  3. Environment variables (YANTRA__ prefix, double underscore as separator)
func LoadConfig(configPath string) (*YantraConfig, error) {
	k := koanf.New(".")

	// Layer 1: built-in defaults
	if err := k.Load(structs.Provider(DefaultConfig(), "koanf"), nil); err != nil {
		return nil, fmt.Errorf("loading defaults: %w", err)
	}

	// Layer 2: config file
	cfgPath := resolveConfigPath(configPath)
	if cfgPath != "" {
		if err := k.Load(file.Provider(cfgPath), toml.Parser()); err != nil {
			// Only error if the user explicitly provided a path
			if configPath != "" {
				return nil, fmt.Errorf("loading config %s: %w", cfgPath, err)
			}
			// Auto-discovered path not found is fine, continue with defaults
		}
	}

	// Layer 3: environment variables (YANTRA__SELECTION__PROVIDER → selection.provider)
	if err := k.Load(env.Provider("YANTRA__", ".", func(s string) string {
		s = strings.TrimPrefix(s, "YANTRA__")
		s = strings.ToLower(s)
		s = strings.ReplaceAll(s, "__", ".")
		return s
	}), nil); err != nil {
		return nil, fmt.Errorf("loading env vars: %w", err)
	}

	var cfg YantraConfig
	if err := k.Unmarshal("", &cfg); err != nil {
		return nil, fmt.Errorf("unmarshaling config: %w", err)
	}

	return &cfg, nil
}

// resolveConfigPath finds the config file to load.
func resolveConfigPath(explicit string) string {
	if explicit != "" {
		return explicit
	}

	// Search order: ./yantra.toml → ./.yantra/config.toml → ~/.config/yantra/config.toml
	candidates := []string{
		"yantra.toml",
		filepath.Join(".yantra", "config.toml"),
	}

	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, ".config", "yantra", "config.toml"))
	}

	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}

	return ""
}
