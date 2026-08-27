package agentloop

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/modelsconfig"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/store"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/tools"
	"github.com/Grigory-Rylov/ai-agent-reflection/session"
)

func TestClearPeerSessionPersistsLiveWorkingDir(t *testing.T) {
	const peerID = int64(555001)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("{}"))
	}))
	defer server.Close()

	td := t.TempDir()
	dirA := filepath.Join(td, "workspace-a")
	if err := os.MkdirAll(dirA, 0o755); err != nil {
		t.Fatal(err)
	}

	st, err := store.NewStore(filepath.Join(td, "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer st.Close()

	holder := modelsconfig.NewTestHolder(&modelsconfig.ModelsConfig{
		Default: "mx",
		Models:  map[string]modelsconfig.ModelEntry{"mx": {Name: "mx", Host: server.URL, Context: 4096}},
	})

	config := DefaultLoopConfig()
	config.ModelHolder = holder
	config.SessionConfig = session.Config{
		Store:      st,
		WorkingDir: td,
	}

	loop, err := NewAgentLoop(config, &mockVKClient{}, tools.NewRegistry())
	if err != nil {
		t.Fatalf("NewAgentLoop: %v", err)
	}

	if err := st.SaveSession(&store.SessionData{PeerID: peerID, WorkingDir: dirA}); err != nil {
		t.Fatal(err)
	}

	sess := loop.EnsureSession(peerID)
	if sess == nil || sess.GetWorkingDir() != dirA {
		t.Fatalf("seed wd = %v, want %q", sess, dirA)
	}

	loop.ClearPeerSession(peerID)

	if sd, _ := st.GetSession(peerID); sd != nil && sd.WorkingDir != dirA {
		t.Fatalf("regression: ClearPeerSession stomped live working dir: got %q want %q", sd.WorkingDir, dirA)
	}
}
