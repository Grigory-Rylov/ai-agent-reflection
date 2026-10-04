package agent

import (
	"fmt"
	"testing"
)

func TestParseContextOverflowErrorFromServerText(t *testing.T) {
	err := fmt.Errorf("API error: status 400, body: %s", serverOverflowBody)

	if !IsContextOverflowError(err) {
		t.Fatal("llama.cpp/vLLM overflow text must be recognized as context overflow")
	}

	promptTokens, maxContext, isOverflow := ContextOverflowStats(err)
	if !isOverflow {
		t.Fatal("ContextOverflowStats must report overflow")
	}
	if promptTokens != 232_869 {
		t.Errorf("promptTokens = %d, want 232869", promptTokens)
	}
	if maxContext != 262_144 {
		t.Errorf("maxContext = %d, want 262144", maxContext)
	}
}

func TestParseContextOverflowErrorKeepsJSONFields(t *testing.T) {
	err := fmt.Errorf(`API error: status 400, body: {"error":{"code":400,"message":"request (100010 tokens) exceeds the available context size (64000 tokens), try increasing it","type":"exceed_context_size_error","n_prompt_tokens":100010,"n_ctx":64000}}`)

	promptTokens, maxContext, isOverflow := ContextOverflowStats(err)
	if !isOverflow {
		t.Fatal("expected overflow")
	}
	if promptTokens != 100_010 {
		t.Errorf("promptTokens = %d, want 100010", promptTokens)
	}
	if maxContext != 64_000 {
		t.Errorf("maxContext = %d, want 64000", maxContext)
	}
}
