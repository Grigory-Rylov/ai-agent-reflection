package compress

import (
	"testing"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/tokenizers"
)

func TestEstimateTokensSimple(t *testing.T) {
	tests := []struct {
		name    string
		text    string
		minimum int
		maximum int
	}{
		{"empty", "", 0, 0},
		{"short", "Hello world", 2, 5},
		{"medium", "This is a test sentence with multiple words.", 8, 15},
		{"code", "func main() { fmt.Println(\"hello\") }", 8, 20},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tokens := EstimateTokensSimple(tt.text)

			if tokens < tt.minimum {
				t.Errorf("Tokens %d < minimum %d", tokens, tt.minimum)
			}
			if tokens > tt.maximum*2 {
				t.Errorf("Tokens %d > maximum %d*2", tokens, tt.maximum)
			}
		})
	}
}

func TestEstimateMessagesTokensSimple(t *testing.T) {
	messages := []tokenizers.Message{
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi there!"},
	}

	tokens := EstimateMessagesTokensSimple(messages)

	if tokens <= 0 {
		t.Error("Token estimate should be positive")
	}
}


func TestEstimateMessagesTokensSimple_ToolCallsIncluded(t *testing.T) {
	
	content := "result" + `{"path":"src/main.go","content":"package main\nfunc main() { fmt.Println(\"hello\") }"}`
	messagesWithToolCalls := []tokenizers.Message{
		{Role: "user", Content: "read file"},
		{Role: "assistant", Content: content},
	}

	
	messagesWithoutToolCalls := []tokenizers.Message{
		{Role: "user", Content: "read file"},
		{Role: "assistant", Content: "result"},
	}

	tokensWith := EstimateMessagesTokensSimple(messagesWithToolCalls)
	tokensWithout := EstimateMessagesTokensSimple(messagesWithoutToolCalls)

	if tokensWith <= tokensWithout {
		t.Errorf("tokens with tool calls (%d) should exceed tokens without (%d)", tokensWith, tokensWithout)
	}
}
