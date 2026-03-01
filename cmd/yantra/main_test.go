package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInitCmd_WritesToConfigFlag(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "custom.toml")

	// Set the global configPath (simulates --config flag)
	configPath = target
	defer func() { configPath = "" }()

	if err := runInit(nil, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("failed to read config: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("expected non-empty config file")
	}
}

func TestInitCmd_DefaultPath(t *testing.T) {
	dir := t.TempDir()
	orig, _ := os.Getwd()
	defer os.Chdir(orig)
	os.Chdir(dir)

	configPath = ""

	if err := runInit(nil, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "yantra.toml")); err != nil {
		t.Fatalf("expected yantra.toml to exist: %v", err)
	}
}

func TestInitCmd_RefusesOverwrite(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "existing.toml")
	os.WriteFile(target, []byte("existing"), 0644)

	configPath = target
	defer func() { configPath = "" }()

	err := runInit(nil, nil)
	if err == nil {
		t.Fatal("expected error when file already exists")
	}
}
