package vk

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/logger"
)

func newLogVKServer(t *testing.T, wantPeer int64, savedDocs *atomic.Int32) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/method/docs.getMessagesUploadServer":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"response": map[string]interface{}{
					"upload_url": "http://" + r.Host + "/upload",
				},
			})
		case "/upload":
			json.NewEncoder(w).Encode(map[string]interface{}{"file": "uploaded_file_data"})
		case "/method/docs.save":
			savedDocs.Add(1)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"response": map[string]interface{}{
					"doc": map[string]interface{}{"id": float64(1), "owner_id": float64(-1)},
				},
			})
		case "/method/messages.send":
			if err := r.ParseForm(); err == nil {
				if got := r.FormValue("peer_id"); got != fmt.Sprintf("%d", wantPeer) {
					t.Errorf("messages.send peer_id = %s, want %d", got, wantPeer)
				}
			}
			json.NewEncoder(w).Encode(map[string]interface{}{"response": float64(1)})
		default:
			t.Errorf("unexpected request path: %s", r.URL.Path)
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
}

func newLogHandler(t *testing.T, logPath string, mainPeerID, wantPeer int64) (*BotHandler, *mockAgentLoop) {
	t.Helper()

	logCfg := logger.DefaultConfig()
	if logPath != "" {
		logCfg.File = logPath
	}
	log, err := logger.New(logCfg)
	if err != nil {
		t.Fatal(err)
	}

	var savedDocs atomic.Int32
	server := newLogVKServer(t, wantPeer, &savedDocs)
	t.Cleanup(server.Close)

	client := NewBotClient("test_token")
	client.baseURL = server.URL + "/method/"

	mock := newMockAgentLoop()
	handler := NewBotHandlerWithPeerID(client, mock, log, mainPeerID, 0, nil, nil)
	return handler, mock
}

func TestLogCommandSendsFileWithoutLLM(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "debug.log")
	if err := os.WriteFile(tmp, []byte("test log content\n"), 0644); err != nil {
		t.Fatal(err)
	}

	handler, mock := newLogHandler(t, tmp, 0, 12345)

	response := handler.ProcessMessage("/log", 12345)
	if !strings.Contains(response, "Лог-файл отправлен") {
		t.Errorf("expected confirmation message, got %q", response)
	}
	if mock.lastMessage != "" {
		t.Errorf("/log should not reach LLM, got lastMessage=%q", mock.lastMessage)
	}
}

func TestLogCommandAlias(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "debug.log")
	if err := os.WriteFile(tmp, []byte("test\n"), 0644); err != nil {
		t.Fatal(err)
	}

	handler, mock := newLogHandler(t, tmp, 0, 12345)

	response := handler.ProcessMessage("/logs", 12345)
	if !strings.Contains(response, "Лог-файл отправлен") {
		t.Errorf("expected confirmation message for /logs, got %q", response)
	}
	if mock.lastMessage != "" {
		t.Errorf("/logs should not reach LLM, got lastMessage=%q", mock.lastMessage)
	}
}

func TestLogCommandRoutesToMainPeer(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "debug.log")
	if err := os.WriteFile(tmp, []byte("test\n"), 0644); err != nil {
		t.Fatal(err)
	}

	handler, _ := newLogHandler(t, tmp, 999, 999)

	response := handler.ProcessMessage("/log", 12345)
	if !strings.Contains(response, "Лог-файл отправлен") {
		t.Errorf("expected confirmation message, got %q", response)
	}
}

func TestLogCommandMissingFile(t *testing.T) {

	missing := filepath.Join(t.TempDir(), "nope.log")
	logCfg := logger.DefaultConfig()
	logCfg.File = missing
	log, err := logger.New(logCfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(missing); err != nil {
		t.Fatal(err)
	}

	mock := newMockAgentLoop()
	handler := NewBotHandler(nil, mock, log)

	response := handler.ProcessMessage("/log", 12345)
	if response == "" {
		t.Error("expected error response for missing log file")
	}
	if mock.lastMessage != "" {
		t.Errorf("/log with missing file should not reach LLM, got lastMessage=%q", mock.lastMessage)
	}
}
