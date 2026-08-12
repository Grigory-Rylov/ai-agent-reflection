package agentloop

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/modelsconfig"
)

// slotAndLLMServer имитирует llama-server с поддержкой slot-save:
// GET /slots, POST /slots/{id}?action=save|restore и /v1/chat/completions.
// Записывает последовательность обработанных путей запросов.
func slotAndLLMServer(t *testing.T) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var paths []string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if action := r.URL.Query().Get("action"); action != "" {
			path = fmt.Sprintf("%s?action=%s", path, action)
		}
		mu.Lock()
		paths = append(paths, path)
		mu.Unlock()

		switch {
		case r.URL.Path == "/slots" && r.Method == http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`[{"id":0,"is_processing":false}]`))
		case strings.HasPrefix(r.URL.Path, "/slots/") && r.Method == http.MethodPost:
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"id_slot":0,"filename":"x.bin","n_saved":1}`))
		case r.URL.Path == "/v1/chat/completions" && r.Method == http.MethodPost:
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Hello\"}}]}\n\n")
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
			fmt.Fprint(w, "[DONE]\n")
		default:
			http.NotFound(w, r)
		}
	}))

	record := func() []string {
		mu.Lock()
		defer mu.Unlock()
		out := make([]string, len(paths))
		copy(out, paths)
		return out
	}
	return server, record
}

func newSlotLoopConfig(serverURL string, slotSave bool) LoopConfig {
	config := DefaultLoopConfig()
	config.ModelHolder = modelsconfig.NewTestHolder(&modelsconfig.ModelsConfig{
		Default: "slot-model",
		Models: map[string]modelsconfig.ModelEntry{
			"slot-model": {Name: "model-a.gguf", Host: serverURL, Context: 8192, SlotSave: slotSave},
		},
	})
	config.EnableCompression = false
	config.EnableTools = false
	config.EnablePruning = false
	return config
}

func TestProcessPromptSlotSave(t *testing.T) {
	server, record := slotAndLLMServer(t)

	loop, err := NewAgentLoop(newSlotLoopConfig(server.URL, true), &mockVKClient{}, nil)
	if err != nil {
		t.Fatalf("NewAgentLoop: %v", err)
	}
	al := loop.(*agentLoop)

	if _, err := al.ProcessPrompt(context.Background(), "hi", 777); err != nil {
		t.Fatalf("ProcessPrompt: %v", err)
	}

	paths := record()
	want := []string{"/slots", "/slots/0?action=restore", "/v1/chat/completions", "/slots/0?action=save"}
	if len(paths) != len(want) {
		t.Fatalf("expected %d requests %v, got %d: %v", len(want), want, len(paths), paths)
	}
	for i, w := range want {
		if paths[i] != w {
			t.Errorf("request %d: got %q, want %q", i, paths[i], w)
		}
	}
}

func TestProcessPromptSlotSaveSecondCallSkipsSlotsProbe(t *testing.T) {
	server, record := slotAndLLMServer(t)

	loop, err := NewAgentLoop(newSlotLoopConfig(server.URL, true), &mockVKClient{}, nil)
	if err != nil {
		t.Fatalf("NewAgentLoop: %v", err)
	}
	al := loop.(*agentLoop)

	if _, err := al.ProcessPrompt(context.Background(), "one", 778); err != nil {
		t.Fatalf("first ProcessPrompt: %v", err)
	}
	if _, err := al.ProcessPrompt(context.Background(), "two", 778); err != nil {
		t.Fatalf("second ProcessPrompt: %v", err)
	}

	paths := record()
	slotsProbes := 0
	for _, p := range paths {
		if p == "/slots" {
			slotsProbes++
		}
	}
	if slotsProbes != 1 {
		t.Errorf("expected exactly 1 /slots probe (cached), got %d in %v", slotsProbes, paths)
	}
}

func TestProcessPromptSlotSaveDisabled(t *testing.T) {
	server, record := slotAndLLMServer(t)

	loop, err := NewAgentLoop(newSlotLoopConfig(server.URL, false), &mockVKClient{}, nil)
	if err != nil {
		t.Fatalf("NewAgentLoop: %v", err)
	}
	al := loop.(*agentLoop)

	if _, err := al.ProcessPrompt(context.Background(), "hi", 779); err != nil {
		t.Fatalf("ProcessPrompt: %v", err)
	}

	for _, p := range record() {
		if strings.Contains(p, "/slots") {
			t.Errorf("slot request made when slot-save disabled: %s", p)
		}
	}
}

func TestProcessPromptSlotSaveRestoreFailureDoesNotFailRequest(t *testing.T) {
	server, record := slotAndLLMServer(t)

	loop, err := NewAgentLoop(newSlotLoopConfig(server.URL, true), &mockVKClient{}, nil)
	if err != nil {
		t.Fatalf("NewAgentLoop: %v", err)
	}
	al := loop.(*agentLoop)

	// Первый запрос: restore упадёт (файла ещё нет), но запрос должен пройти.
	if _, err := al.ProcessPrompt(context.Background(), "hi", 780); err != nil {
		t.Fatalf("ProcessPrompt should not fail on restore error: %v", err)
	}

	paths := record()
	if len(paths) == 0 {
		t.Fatal("no requests recorded")
	}
	if paths[len(paths)-1] != "/slots/0?action=save" {
		t.Errorf("expected last request to be save, got %v", paths)
	}
}
