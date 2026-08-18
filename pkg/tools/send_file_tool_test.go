package tools

import (
	"context"
	"errors"
	"testing"
)

type mockFileSender struct {
	lastPath   string
	lastPeer   int64
	lastMsg    string
	returnID   int64
	returnErr  error
}

func (m *mockFileSender) UploadAndSendDocument(filePath string, peerID int64, message string) (int64, error) {
	m.lastPath = filePath
	m.lastPeer = peerID
	m.lastMsg = message
	return m.returnID, m.returnErr
}

func newSendFileTestEnv(t *testing.T) *mockFileSender {
	t.Helper()
	m := &mockFileSender{returnID: 42}
	SetSendFileDependencies(m, 100)
	t.Cleanup(func() { SetSendFileDependencies(nil, 0) })
	return m
}

func TestSendFileToolMetadata(t *testing.T) {
	tt := &SendFileTool{}
	if tt.Name() != "send-files" {
		t.Errorf("Name() = %q, want %q", tt.Name(), "send-files")
	}
	schema := tt.Schema()
	if schema["type"] != "object" {
		t.Errorf("Schema type = %v, want object", schema["type"])
	}
	props, _ := schema["properties"].(map[string]interface{})
	if _, ok := props["file_path"]; !ok {
		t.Error("Schema missing file_path property")
	}
}

func TestSendFileToolSuccess(t *testing.T) {
	m := newSendFileTestEnv(t)
	tt := &SendFileTool{}

	res, err := tt.Execute(context.Background(), map[string]string{
		"file_path": "/tmp/a.html",
		"caption":   "hello",
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected success, got error: %s", res.Error)
	}
	if m.lastPath != "/tmp/a.html" {
		t.Errorf("lastPath = %q, want /tmp/a.html", m.lastPath)
	}
	if m.lastPeer != 100 {
		t.Errorf("lastPeer = %d, want 100 (default)", m.lastPeer)
	}
	if m.lastMsg != "hello" {
		t.Errorf("lastMsg = %q, want hello", m.lastMsg)
	}
}

func TestSendFileToolCustomPeer(t *testing.T) {
	m := newSendFileTestEnv(t)
	tt := &SendFileTool{}

	res, _ := tt.Execute(context.Background(), map[string]string{
		"file_path": "/tmp/a.txt",
		"peer_id":   "2000000001",
	})
	if !res.Success {
		t.Fatalf("expected success, got error: %s", res.Error)
	}
	if m.lastPeer != 2000000001 {
		t.Errorf("lastPeer = %d, want 2000000001", m.lastPeer)
	}
}

func TestSendFileToolMissingPath(t *testing.T) {
	newSendFileTestEnv(t)
	tt := &SendFileTool{}

	res, _ := tt.Execute(context.Background(), map[string]string{})
	if res.Success {
		t.Error("expected failure when file_path missing")
	}
	if res.Error == "" {
		t.Error("expected non-empty error")
	}
}

func TestSendFileToolSenderError(t *testing.T) {
	m := newSendFileTestEnv(t)
	m.returnErr = errors.New("vk api error 100")
	tt := &SendFileTool{}

	res, _ := tt.Execute(context.Background(), map[string]string{"file_path": "/tmp/a.txt"})
	if res.Success {
		t.Error("expected failure when sender errors")
	}
	if res.Error == "" {
		t.Error("expected non-empty error")
	}
}

func TestSendFileToolNotConfigured(t *testing.T) {
	SetSendFileDependencies(nil, 0)
	t.Cleanup(func() { SetSendFileDependencies(nil, 0) })
	tt := &SendFileTool{}

	res, _ := tt.Execute(context.Background(), map[string]string{"file_path": "/tmp/a.txt"})
	if res.Success {
		t.Error("expected failure when not configured")
	}
}
