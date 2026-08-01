package agent

import (
	"encoding/json"
	"testing"
)

func TestTokenCounts(t *testing.T) {
	t.Run("from llama.cpp timings", func(t *testing.T) {
		event := &SSEEvent{}
		raw := `{"choices":[{"delta":{},"finish_reason":"stop"}],"timings":{"prompt_n":43,"predicted_n":20}}`
		if err := json.Unmarshal([]byte(raw), event); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		in, out := tokenCounts(event)
		if in != 43 || out != 20 {
			t.Errorf("expected in=43, out=20, got in=%d, out=%d", in, out)
		}
	})

	t.Run("from OpenAI usage", func(t *testing.T) {
		event := &SSEEvent{}
		raw := `{"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":100,"completion_tokens":25,"total_tokens":125}}`
		if err := json.Unmarshal([]byte(raw), event); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		in, out := tokenCounts(event)
		if in != 100 || out != 25 {
			t.Errorf("expected in=100, out=25, got in=%d, out=%d", in, out)
		}
	})

	t.Run("usage wins over timings", func(t *testing.T) {
		event := &SSEEvent{}
		raw := `{"choices":[],"usage":{"prompt_tokens":50,"completion_tokens":5,"total_tokens":55},"timings":{"prompt_n":1,"predicted_n":1}}`
		if err := json.Unmarshal([]byte(raw), event); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		in, out := tokenCounts(event)
		if in != 50 || out != 5 {
			t.Errorf("expected usage in=50, out=5, got in=%d, out=%d", in, out)
		}
	})

	t.Run("returns zeros when no token info", func(t *testing.T) {
		event := &SSEEvent{}
		raw := `{"choices":[{"delta":{"content":"hi"},"finish_reason":null}]}`
		if err := json.Unmarshal([]byte(raw), event); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		in, out := tokenCounts(event)
		if in != 0 || out != 0 {
			t.Errorf("expected zeros, got in=%d, out=%d", in, out)
		}
	})

	t.Run("nil event returns zeros", func(t *testing.T) {
		in, out := tokenCounts(nil)
		if in != 0 || out != 0 {
			t.Errorf("expected zeros, got in=%d, out=%d", in, out)
		}
	})
}

func TestSendThinkingTokens(t *testing.T) {
	t.Run("sends only when tokens present", func(t *testing.T) {
		a := &agentImpl{}
		var sent []string
		a.thinkingCallback = func(peerID int64, content string) error {
			sent = append(sent, content)
			return nil
		}

		a.sendThinkingTokens(1, 0, 0)
		if len(sent) != 0 {
			t.Errorf("expected no message when tokens are 0, got %v", sent)
		}

		a.sendThinkingTokens(1, 43, 20)
		if len(sent) != 1 {
			t.Fatalf("expected 1 message, got %v", sent)
		}
		if sent[0] != "[TOKENS] in: 43, out: 20" {
			t.Errorf("unexpected message: %q", sent[0])
		}
	})

	t.Run("skips when callback is nil", func(t *testing.T) {
		a := &agentImpl{}
		a.sendThinkingTokens(1, 43, 20) // must not panic
	})
}
