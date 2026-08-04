package agentloop

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/opencode/llama-client/pkg/modelsconfig"
)

func newModelsJSON(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "models.json")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write models.json: %v", err)
	}
	return path
}

func TestModelContextResolver(t *testing.T) {
	t.Run("uses context from models.json when set", func(t *testing.T) {
		holder := modelsconfig.NewTestHolder(&modelsconfig.ModelsConfig{
			Default: "minimax",
			Models: map[string]modelsconfig.ModelEntry{
				"minimax": {Name: "MiniMax-M2.7", Host: "127.0.0.1:1", Context: 32768},
			},
		})
		r := NewModelContextResolver(holder, nil)
		ctx, err := r.Resolve()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ctx != 32768 {
			t.Errorf("got %d, want 32768", ctx)
		}
	})

	t.Run("uses actual server context when models.json has none", func(t *testing.T) {
		var calls int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v1/models" {
				http.NotFound(w, r)
				return
			}
			atomic.AddInt32(&calls, 1)
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"object":"list","data":[{"id":"MiniMax-M2.7","status":{"value":"loaded","args":["--ctx-size","148000"]}}]}`))
		}))
		defer server.Close()

		holder := modelsconfig.NewTestHolder(&modelsconfig.ModelsConfig{
			Default: "minimax",
			Models: map[string]modelsconfig.ModelEntry{
				"minimax": {Name: "MiniMax-M2.7", Host: server.URL},
			},
		})
		r := NewModelContextResolver(holder, nil)

		first, err := r.Resolve()
		if err != nil {
			t.Fatalf("first resolve: %v", err)
		}
		if first != 148000 {
			t.Errorf("got %d, want 148000", first)
		}

		second, err := r.Resolve()
		if err != nil {
			t.Fatalf("second resolve: %v", err)
		}
		if second != 148000 {
			t.Errorf("got %d, want 148000", second)
		}
		if atomic.LoadInt32(&calls) != 1 {
			t.Errorf("server queried %d times, want 1 (cached)", calls)
		}
	})

	t.Run("re-queries when model switched via /r", func(t *testing.T) {
		var calls int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v1/models" {
				http.NotFound(w, r)
				return
			}
			atomic.AddInt32(&calls, 1)
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"object":"list","data":[
				{"id":"model-a","status":{"value":"loaded","args":["--ctx-size","32768"]}},
				{"id":"model-b","status":{"value":"loaded","args":["--ctx-size","65536"]}}
			]}`))
		}))
		defer server.Close()

		path := newModelsJSON(t, `{
			"default": "a",
			"models": {
				"a": {"name": "model-a", "host": "`+server.URL+`"},
				"b": {"name": "model-b", "host": "`+server.URL+`"}
			}
		}`)
		holder, err := modelsconfig.NewHolder(path)
		if err != nil {
			t.Fatalf("NewHolder: %v", err)
		}
		r := NewModelContextResolver(holder, nil)

		ctxA, err := r.Resolve()
		if err != nil {
			t.Fatalf("resolve a: %v", err)
		}
		if ctxA != 32768 {
			t.Errorf("got %d, want 32768", ctxA)
		}

		if err := holder.Switch("b"); err != nil {
			t.Fatalf("Switch: %v", err)
		}
		ctxB, err := r.Resolve()
		if err != nil {
			t.Fatalf("resolve b: %v", err)
		}
		if ctxB != 65536 {
			t.Errorf("got %d, want 65536", ctxB)
		}
		if atomic.LoadInt32(&calls) != 2 {
			t.Errorf("server queried %d times, want 2 (one per model)", calls)
		}
	})

	t.Run("errors when server unavailable and models.json has none", func(t *testing.T) {
		holder := modelsconfig.NewTestHolder(&modelsconfig.ModelsConfig{
			Default: "minimax",
			Models: map[string]modelsconfig.ModelEntry{
				"minimax": {Name: "MiniMax-M2.7", Host: "127.0.0.1:1"},
			},
		})
		r := NewModelContextResolver(holder, nil)
		if _, err := r.Resolve(); err == nil {
			t.Error("expected error, got nil")
		}
	})

	t.Run("nil holder returns error", func(t *testing.T) {
		r := NewModelContextResolver(nil, nil)
		if _, err := r.Resolve(); err == nil {
			t.Error("expected error, got nil")
		}
	})
}
