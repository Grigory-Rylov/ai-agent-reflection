package vk

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/logger"
)

// newLogVKServer mocks the VK API endpoints used by UploadAndSendDocument:
// docs.getMessagesUploadServer -> upload -> docs.save -> messages.send.
func newLogVKServer(t *testing.T, wantPeer int64) *httptest.Server {
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

// newLogHandler builds a BotHandler with a mock VK client that verifies the
// file is delivered to wantPeer. mainPeerID configures the reply redirection.
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

	server := newLogVKServer(t, wantPeer)
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

	// mainPeerID=999: the file must be delivered there, not to the source peer.
	handler, _ := newLogHandler(t, tmp, 999, 999)

	response := handler.ProcessMessage("/log", 12345)
	if !strings.Contains(response, "Лог-файл отправлен") {
		t.Errorf("expected confirmation message, got %q", response)
	}
}

func TestLogCommandMissingFile(t *testing.T) {
	// logger.New creates the file (O_CREATE); remove it so handleLogCommand
	// sees a missing file and returns an error instead of trying to send.
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
