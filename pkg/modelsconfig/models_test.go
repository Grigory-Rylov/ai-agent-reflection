package modelsconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadValidFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "models.json")
	content := `{
		"default": "gemma",
		"models": {
			"gemma": {
				"name": "gemma-4-12b-it.gguf",
				"host": "127.0.0.1:8081"
			},
			"llama": {
				"name": "llama-3-8b.gguf",
				"host": "192.168.1.1:8081"
			}
		}
	}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Default != "gemma" {
		t.Errorf("default: got %q, want 'gemma'", cfg.Default)
	}
	if len(cfg.Models) != 2 {
		t.Errorf("models count: got %d, want 2", len(cfg.Models))
	}

	m, ok := cfg.Models["gemma"]
	if !ok {
		t.Fatal("model 'gemma' not found")
	}
	if m.Name != "gemma-4-12b-it.gguf" {
		t.Errorf("name: got %q", m.Name)
	}
	if m.Host != "127.0.0.1:8081" {
		t.Errorf("host: got %q", m.Host)
	}
}

func TestLoadMissingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nonexistent.json")

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !strings.Contains(err.Error(), "models.json") {
		t.Errorf("error should mention models.json, got: %v", err)
	}
	if !strings.Contains(err.Error(), "default") ||
		!strings.Contains(err.Error(), "models") ||
		!strings.Contains(err.Error(), "name") ||
		!strings.Contains(err.Error(), "host") {
		t.Errorf("error should describe format, got: %v", err)
	}
}

func TestLoadInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "models.json")
	content := `{bad json}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestLoadMissingDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "models.json")
	content := `{"models": {}}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for missing default")
	}
}

func TestLoadMissingModels(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "models.json")
	content := `{"default": "x"}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for missing models")
	}
}

func TestLoadDefaultNotInModels(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "models.json")
	content := `{"default": "nonexistent", "models": {"other": {"name": "m", "host": "h"}}}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error when default not in models")
	}
}

func TestHolderGetCurrent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "models.json")
	content := `{
		"default": "gemma",
		"models": {
			"gemma": {"name": "gemma-4.gguf", "host": "127.0.0.1:8081", "context": 32768},
			"llama": {"name": "llama-3.gguf", "host": "192.168.1.1:8081"}
		}
	}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	holder, err := NewHolder(path)
	if err != nil {
		t.Fatal(err)
	}

	alias, name, host := holder.GetCurrent()
	if alias != "gemma" {
		t.Errorf("alias: got %q, want 'gemma'", alias)
	}
	if name != "gemma-4.gguf" {
		t.Errorf("name: got %q", name)
	}
	if host != "http://127.0.0.1:8081" {
		t.Errorf("host: got %q, want 'http://127.0.0.1:8081'", host)
	}
}

func TestHolderSwitch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "models.json")
	content := `{
		"default": "gemma",
		"models": {
			"gemma": {"name": "gemma-4.gguf", "host": "127.0.0.1:8081"},
			"llama": {"name": "llama-3.gguf", "host": "192.168.1.1:8081"}
		}
	}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	holder, err := NewHolder(path)
	if err != nil {
		t.Fatal(err)
	}

	if err := holder.Switch("llama"); err != nil {
		t.Fatal(err)
	}

	alias, name, host := holder.GetCurrent()
	if alias != "llama" {
		t.Errorf("alias: got %q, want 'llama'", alias)
	}
	if name != "llama-3.gguf" {
		t.Errorf("name: got %q", name)
	}
	if host != "http://192.168.1.1:8081" {
		t.Errorf("host: got %q", host)
	}

	cfg2, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg2.Default != "llama" {
		t.Errorf("persisted default: got %q, want 'llama'", cfg2.Default)
	}
}

func TestHolderContext(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "models.json")
	content := `{
		"default": "gemma",
		"models": {
			"gemma": {"name": "gemma-4.gguf", "host": "127.0.0.1:8081", "context": 32768},
			"llama": {"name": "llama-3.gguf", "host": "192.168.1.1:8081"}
		}
	}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	holder, err := NewHolder(path)
	if err != nil {
		t.Fatal(err)
	}

	if got := holder.GetCurrentContext(); got != 32768 {
		t.Errorf("GetCurrentContext: got %d, want 32768", got)
	}
	if got := holder.GetModelContext("llama"); got != 0 {
		t.Errorf("GetModelContext(llama): got %d, want 0 (not set)", got)
	}
	if got := holder.GetModelContext("gemma"); got != 32768 {
		t.Errorf("GetModelContext(gemma): got %d, want 32768", got)
	}
}

func TestHolderVision(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "models.json")
	content := `{
		"default": "gemma",
		"models": {
			"gemma": {"name": "gemma-4.gguf", "host": "127.0.0.1:8081", "vision": true},
			"llama": {"name": "llama-3.gguf", "host": "192.168.1.1:8081"}
		}
	}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	holder, err := NewHolder(path)
	if err != nil {
		t.Fatal(err)
	}

	if got := holder.GetCurrentVision(); got != true {
		t.Errorf("GetCurrentVision: got %v, want true", got)
	}
	if got := holder.GetModelVision("gemma"); got != true {
		t.Errorf("GetModelVision(gemma): got %v, want true", got)
	}
	if got := holder.GetModelVision("llama"); got != false {
		t.Errorf("GetModelVision(llama): got %v, want false (not set)", got)
	}

	if err := holder.Switch("llama"); err != nil {
		t.Fatal(err)
	}
	if got := holder.GetCurrentVision(); got != false {
		t.Errorf("GetCurrentVision after switch: got %v, want false", got)
	}
}

func TestHolderSwitchUnknown(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "models.json")
	content := `{"default": "gemma", "models": {"gemma": {"name": "m", "host": "h"}}}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	holder, err := NewHolder(path)
	if err != nil {
		t.Fatal(err)
	}

	err = holder.Switch("unknown")
	if err == nil {
		t.Fatal("expected error for unknown alias")
	}
}

func TestHolderList(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "models.json")
	content := `{
		"default": "gemma",
		"models": {
			"gemma": {"name": "m1", "host": "h1"},
			"llama": {"name": "m2", "host": "h2"}
		}
	}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	holder, err := NewHolder(path)
	if err != nil {
		t.Fatal(err)
	}

	models := holder.List()
	if len(models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(models))
	}
	if models["gemma"].Name != "m1" {
		t.Errorf("gemma name: got %q", models["gemma"].Name)
	}
}
