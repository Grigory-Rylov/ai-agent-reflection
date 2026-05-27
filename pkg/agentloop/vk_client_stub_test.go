package agentloop

import (
	"os"
	"testing"
)

func TestStubVKClient_RemovesLogOnCreate(t *testing.T) {
	logPath := "/tmp/stub_vk_test.log"
	os.WriteFile(logPath, []byte("old content"), 0644)

	client := NewStubVKClient(logPath)
	defer os.Remove(logPath)

	if _, err := os.Stat(logPath); err == nil {
		data, _ := os.ReadFile(logPath)
		if len(data) > 0 {
			t.Errorf("log file should be empty after creation, got: %s", string(data))
		}
	}
	_ = client
}

func TestStubVKClient_LogsMessages(t *testing.T) {
	logPath := "/tmp/stub_vk_test.log"
	client := NewStubVKClient(logPath)
	defer os.Remove(logPath)

	_, err := client.SendMessage(12345, "Hello, world!")
	if err != nil {
		t.Fatalf("SendMessage failed: %v", err)
	}

	_, err = client.SendThinking(12345, "Thinking...")
	if err != nil {
		t.Fatalf("SendThinking failed: %v", err)
	}

	if !client.Contains("Hello, world!") {
		t.Error("expected log to contain message text")
	}
	if !client.Contains("Thinking...") {
		t.Error("expected log to contain thinking text")
	}
	if !client.Contains("[SendMessage]") {
		t.Error("expected log to contain [SendMessage] marker")
	}
	if !client.Contains("[SendThinking]") {
		t.Error("expected log to contain [SendThinking] marker")
	}

	lines := client.ReadLog()
	if len(lines) != 2 {
		t.Errorf("expected 2 log lines, got %d", len(lines))
	}
}

func TestStubVKClient_SatisfiesInterface(t *testing.T) {
	logPath := "/tmp/stub_vk_test.log"
	defer os.Remove(logPath)

	var client VKClient = NewStubVKClient(logPath)
	_ = client
}
