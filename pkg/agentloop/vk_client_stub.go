package agentloop

import (
	"fmt"
	"os"
	"strings"
	"sync"
)

// StubVKClient — заглушка для VKClient, пишет отправку сообщений в лог-файл
// вместо реальной отправки в VK API.
type StubVKClient struct {
	LogPath string
	mu      sync.Mutex
}

// NewStubVKClient создаёт StubVKClient и очищает лог-файл.
func NewStubVKClient(logPath string) *StubVKClient {
	os.Remove(logPath)
	return &StubVKClient{LogPath: logPath}
}

// SendMessage логирует отправку сообщения и возвращает success.
func (c *StubVKClient) SendMessage(peerID int64, text string) (int64, error) {
	c.writeLog("[SendMessage] peer=%d: %s", peerID, text)
	return 1, nil
}

// SendThinking логирует отправку thinking-сообщения и возвращает success.
func (c *StubVKClient) SendThinking(peerID int64, content string) (int64, error) {
	c.writeLog("[SendThinking] peer=%d: %s", peerID, content)
	return 1, nil
}

func (c *StubVKClient) writeLog(format string, args ...interface{}) {
	c.mu.Lock()
	defer c.mu.Unlock()

	line := fmt.Sprintf(format, args...)
	f, err := os.OpenFile(c.LogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintln(f, line)
}

// ReadLog возвращает все строки из лог-файла.
func (c *StubVKClient) ReadLog() []string {
	c.mu.Lock()
	defer c.mu.Unlock()

	data, err := os.ReadFile(c.LogPath)
	if err != nil {
		return nil
	}
	content := strings.TrimRight(string(data), "\n")
	if content == "" {
		return nil
	}
	return strings.Split(content, "\n")
}

// Contains проверяет, содержит ли лог-файл строку с указанным текстом.
func (c *StubVKClient) Contains(substr string) bool {
	for _, line := range c.ReadLog() {
		if strings.Contains(line, substr) {
			return true
		}
	}
	return false
}
