package modelsconfig

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

func TestParseEngineFields(t *testing.T) {
	content := `{
		"default": "a",
		"models": {
			"a": {
				"name": "model-a",
				"host": "127.0.0.1:8081",
				"type": "llama",
				"start-script": "/opt/start-a.sh",
				"stop-script": "/opt/stop-a.sh"
			},
			"b": {
				"name": "model-b",
				"host": "127.0.0.1:8020",
				"context": 8192,
				"vision": true
			}
		}
	}`
	cfg, err := Load(writeFile(t, "models.json", content))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	a := cfg.Models["a"]
	if a.Type != "llama" {
		t.Errorf("a.type: got %q, want llama", a.Type)
	}
	if a.StartScript != "/opt/start-a.sh" {
		t.Errorf("a.start-script: got %q", a.StartScript)
	}
	if a.StopScript != "/opt/stop-a.sh" {
		t.Errorf("a.stop-script: got %q", a.StopScript)
	}

	b := cfg.Models["b"]
	if b.Type != "" || b.StartScript != "" || b.StopScript != "" {
		t.Errorf("b engine fields should be empty, got %+v", b)
	}
}

func TestBackwardCompatOldFormatLoadsPassthrough(t *testing.T) {
	content := `{
		"default": "legacy",
		"models": {
			"legacy": {"name": "legacy.gguf", "host": "127.0.0.1:8081", "context": 32768, "vision": true, "slot-save": true}
		}
	}`
	cfg, err := Load(writeFile(t, "models.json", content))
	if err != nil {
		t.Fatalf("Load old-format file: %v", err)
	}
	m := cfg.Models["legacy"]
	if m.Type != "" || m.StartScript != "" || m.StopScript != "" {
		t.Errorf("old format must leave engine fields empty, got %+v", m)
	}
	if m.Context != 32768 || !m.Vision || !m.SlotSave {
		t.Errorf("old fields lost: %+v", m)
	}
}

func TestHolderEngineGetters(t *testing.T) {
	holder := NewTestHolder(&ModelsConfig{
		Default: "a",
		Models: map[string]ModelEntry{
			"a": {Name: "ma", Host: "h1", Type: "llama", StartScript: "sa.sh", StopScript: "sta.sh"},
			"b": {Name: "mb", Host: "h2", Type: "vllm", StartScript: "sb.sh", StopScript: "stb.sh"},
		},
	})

	cases := []struct {
		name     string
		got      string
		want     string
	}{
		{"cur-type", holder.GetCurrentEngineType(), "llama"},
		{"cur-start", holder.GetCurrentStartScript(), "sa.sh"},
		{"cur-stop", holder.GetCurrentStopScript(), "sta.sh"},
		{"model-type", holder.GetModelEngineType("b"), "vllm"},
		{"model-start", holder.GetModelStartScript("b"), "sb.sh"},
		{"model-stop", holder.GetModelStopScript("b"), "stb.sh"},
		{"missing-type", holder.GetModelEngineType("zzz"), ""},
		{"missing-start", holder.GetModelStartScript("zzz"), ""},
		{"missing-stop", holder.GetModelStopScript("zzz"), ""},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, c.got, c.want)
		}
	}
}

func TestSwitchPersistsEngineFields(t *testing.T) {
	content := `{
		"default": "a",
		"models": {
			"a": {"name": "ma", "host": "h1", "type": "llama", "start-script": "sa.sh", "stop-script": "sta.sh", "context": 1024},
			"b": {"name": "mb", "host": "h2", "type": "vllm", "start-script": "sb.sh", "stop-script": "stb.sh", "vision": true}
		}
	}`
	path := writeFile(t, "models.json", content)
	holder, err := NewHolder(path)
	if err != nil {
		t.Fatalf("NewHolder: %v", err)
	}

	if err := holder.Switch("b"); err != nil {
		t.Fatalf("Switch: %v", err)
	}

	reloaded, err := Load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Default != "b" {
		t.Fatalf("default after switch: got %q, want b", reloaded.Default)
	}
	a := reloaded.Models["a"]
	if a.Type != "llama" || a.StartScript != "sa.sh" || a.StopScript != "sta.sh" || a.Context != 1024 {
		t.Errorf("fields stripped on rewrite for a: %+v", a)
	}
	b := reloaded.Models["b"]
	if b.Type != "vllm" || b.StartScript != "sb.sh" || b.StopScript != "stb.sh" || !b.Vision {
		t.Errorf("fields stripped on rewrite for b: %+v", b)
	}
}

func TestValidationUnchangedWithEngineFields(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantErr bool
	}{
		{"valid with engine", `{"default":"a","models":{"a":{"name":"m","host":"h","type":"llama","start-script":"s","stop-script":"x"}}}`, false},
		{"missing default", `{"models":{"a":{"name":"m","host":"h"}}}`, true},
		{"empty models", `{"default":"a","models":{}}`, true},
		{"default not listed", `{"default":"a","models":{"b":{"name":"m","host":"h"}}}`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(writeFile(t, "models.json", tt.content))
			if (err != nil) != tt.wantErr {
				t.Fatalf("wantErr=%v, got err=%v", tt.wantErr, err)
			}
		})
	}
}
