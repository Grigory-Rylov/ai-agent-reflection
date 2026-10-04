package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/compress"
)

func budgetAgent(window int, engineType string) *agentImpl {
	return NewAgent(Config{
		LlamaServerURL: "http://127.0.0.1:8080",
		Model:          "test-model",
		MaxTokens:      window,
		EngineType:     engineType,
		SlotID:         -1,
	})
}

func decodeRequestBody(t *testing.T, data []byte) map[string]interface{} {
	t.Helper()
	var body map[string]interface{}
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	return body
}

func TestBuildRequestJSONClampsMaxTokensForHugePrompt(t *testing.T) {
	a := budgetAgent(262_144, "")
	messages := []Message{{Role: "user", Content: strings.Repeat("x", (262_144-1_000)*4)}}

	body := decodeRequestBody(t, a.buildRequestJSON(StreamingConfig{Model: "test-model"}, messages))

	if got := body["max_tokens"]; got != float64(compress.MinOutputTokens) {
		t.Errorf("max_tokens = %v, want %d", got, compress.MinOutputTokens)
	}
}

func TestBuildRequestJSONKeepsFullOutputCapForSmallPrompt(t *testing.T) {
	a := budgetAgent(262_144, "")

	body := decodeRequestBody(t, a.buildRequestJSON(StreamingConfig{Model: "test-model"},
		[]Message{{Role: "user", Content: "hello"}}))

	if got := body["max_tokens"]; got != float64(compress.OUTPUT_TOKEN_MAX) {
		t.Errorf("max_tokens = %v, want %d", got, compress.OUTPUT_TOKEN_MAX)
	}
}

func TestBuildRequestJSONCountsToolSchemasInPromptBudget(t *testing.T) {
	const window = 20_000
	a := budgetAgent(window, "")
	toolSchemas := []map[string]interface{}{{
		"type":     "function",
		"function": map[string]interface{}{"name": "big", "description": strings.Repeat("d", 60_000)},
	}}

	request := a.buildRequestJSON(StreamingConfig{Model: "test-model", Tools: toolSchemas},
		[]Message{{Role: "user", Content: "hello"}})
	withTools := decodeRequestBody(t, request)
	withoutTools := decodeRequestBody(t, a.buildRequestJSON(StreamingConfig{Model: "test-model"},
		[]Message{{Role: "user", Content: "hello"}}))

	if withTools["max_tokens"] == withoutTools["max_tokens"] {
		t.Fatalf("60k chars of tool schema ignored by prompt estimate, max_tokens = %v", withTools["max_tokens"])
	}
	used := len(request)/4 + int(withTools["max_tokens"].(float64))
	if used > window {
		t.Errorf("request needs %d tokens (prompt + output) against window %d", used, window)
	}
}

func TestBuildBaseRequestJSONStreamOptions(t *testing.T) {
	tests := []struct {
		name       string
		engineType string
		stream     bool
		wantUsage  bool
	}{
		{"llama cpp streaming", "", true, true},
		{"ninfer streaming", "ninfer", true, false},
		{"non streaming", "", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := budgetAgent(262_144, tt.engineType).
				buildBaseRequestJSON("test-model", []Message{{Role: "user", Content: "hi"}}, tt.stream)

			options, present := req["stream_options"]
			if present != tt.wantUsage {
				t.Fatalf("stream_options present = %v, want %v", present, tt.wantUsage)
			}
			if !tt.wantUsage {
				return
			}
			usage, ok := options.(map[string]interface{})["include_usage"]
			if !ok || usage != true {
				t.Errorf("include_usage = %v, want true", usage)
			}
		})
	}
}
