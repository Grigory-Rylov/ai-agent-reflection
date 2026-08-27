package vk

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/agentloop"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/modelsconfig"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/store"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/tools"
)

func TestSlashNewSessionIntegrationWorkingDir(t *testing.T) {
	const peerID = int64(770201)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("{}"))
	}))
	defer server.Close()

	td := t.TempDir()
	projDir := filepath.Join(td, "proj-old")
	newDir := filepath.Join(td, "proj-new")
	for _, d := range []string{projDir, newDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	st, err := store.NewStore(filepath.Join(td, "agent.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer st.Close()

	holder := modelsconfig.NewTestHolder(&modelsconfig.ModelsConfig{
		Default: "mx",
		Models:  map[string]modelsconfig.ModelEntry{"mx": {Name: "mx", Host: server.URL, Context: 4096}},
	})

	lc := agentloop.DefaultLoopConfig()
	lc.ModelHolder = holder
	lc.SessionConfig.Store = st
	lc.SessionConfig.WorkingDir = td

	loop, err := agentloop.NewAgentLoop(lc, nil, tools.NewRegistry())
	if err != nil {
		t.Fatalf("NewAgentLoop: %v", err)
	}

	if err := st.SaveSession(&store.SessionData{PeerID: peerID, WorkingDir: projDir}); err != nil {
		t.Fatal(err)
	}

	prevGW := tools.WorkingDir
	defer func() { tools.WorkingDir = prevGW }()
	tools.WorkingDir = projDir

	sess0 := loop.EnsureSession(peerID)
	s0 := "<zero-value>"
	if sess0 != nil {
		s0 = sess0.GetWorkingDir()
	}
	t.Logf("BOOT in-mem wd=%q (want %q)", s0, projDir)

	h := NewBotHandlerWithPeerID(nil, loop, nil, peerID, 0, nil, nil)
	reply := h.handleCommand("/n "+newDir, peerID)
	t.Logf("CMD REPLY: %q", strings.ReplaceAll(reply, "\n", "\\n"))

	sess1 := loop.GetSession(peerID)
	mem := "<gone>"
	if sess1 != nil {
		mem = sess1.GetWorkingDir()
	}
	rows := storeRowsWD(t, st, peerID)

	t.Logf("POST store-wd=%q inmem-wd=%q global-wd=%q", rows, mem, tools.WorkingDir)

	if mem != newDir {
		t.Errorf("BAD-INVMEMMARY in-mem: want %q got %q", newDir, mem)
	}
	if tools.WorkingDir != newDir {
		t.Errorf("BAD-INVMEMMARY global: want %q got %q", newDir, tools.WorkingDir)
	}
	if rows != newDir {
		t.Errorf("BAD-INVMEMMARY store: want %q got %q", newDir, rows)
	}
}

func storeRowsWD(t *testing.T, st store.Store, peerID int64) string {
	t.Helper()
	sd, err := st.GetSession(peerID)
	if err != nil || sd == nil {
		return "<none>"
	}
	return sd.WorkingDir
}
