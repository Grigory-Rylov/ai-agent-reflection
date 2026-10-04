package compress

import "testing"

func TestOutputCapForWindow(t *testing.T) {
	tests := []struct {
		name          string
		contextWindow int
		want          int
	}{
		{"large window keeps output max", 262_144, OUTPUT_TOKEN_MAX},
		{"window below output max loses input floor", 32_768, 28_672},
		{"tiny window clamps to floor", 1_000, MinOutputTokens},
		{"unknown window keeps output max", 0, OUTPUT_TOKEN_MAX},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := OutputCapForWindow(tt.contextWindow); got != tt.want {
				t.Errorf("OutputCapForWindow(%d) = %d, want %d", tt.contextWindow, got, tt.want)
			}
		})
	}
}

func TestClampOutputTokens(t *testing.T) {
	tests := []struct {
		name          string
		promptTokens  int
		contextWindow int
		want          int
	}{
		{"small prompt keeps full cap", 100_000, 262_144, OUTPUT_TOKEN_MAX},
		{"prompt near cap leaves room", 232_869, 262_144, 27_227},
		{"prompt below cap keeps full cap", 225_000, 262_144, OUTPUT_TOKEN_MAX},
		{"no room left falls to floor", 261_500, 262_144, MinOutputTokens},
		{"narrow window caps by input floor", 30_000, 32_768, MinOutputTokens},
		{"unknown window keeps full cap", 1_000, 0, OUTPUT_TOKEN_MAX},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClampOutputTokens(tt.promptTokens, tt.contextWindow)
			if got != tt.want {
				t.Errorf("ClampOutputTokens(%d, %d) = %d, want %d", tt.promptTokens, tt.contextWindow, got, tt.want)
			}
		})
	}
}

func TestClampOutputTokensKeepsRequestWithinWindow(t *testing.T) {
	const window = 262_144
	for _, promptTokens := range []int{100_000, 225_000, 229_376, 232_869, 260_500} {
		cap := ClampOutputTokens(promptTokens, window)
		if promptTokens+cap > window {
			t.Errorf("ClampOutputTokens(%d, %d) = %d leaves %d tokens over window",
				promptTokens, window, cap, promptTokens+cap-window)
		}
	}
}

func TestFitsContext(t *testing.T) {
	tests := []struct {
		name          string
		promptTokens  int
		outputCap     int
		contextWindow int
		want          bool
	}{
		{"rejects overflow request", 232_869, OUTPUT_TOKEN_MAX, 262_144, false},
		{"accepts request at limit", 229_376, OUTPUT_TOKEN_MAX, 262_144, true},
		{"unknown window accepts", 1_000_000, OUTPUT_TOKEN_MAX, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FitsContext(tt.promptTokens, tt.outputCap, tt.contextWindow); got != tt.want {
				t.Errorf("FitsContext(%d, %d, %d) = %v, want %v",
					tt.promptTokens, tt.outputCap, tt.contextWindow, got, tt.want)
			}
		})
	}
}

func TestIsContextOverflowMessage(t *testing.T) {
	tests := []struct {
		name string
		text string
		want bool
	}{
		{
			name: "llama cpp vllm overflow",
			text: `API error: status 400, body: {"error":{"type":"invalid_request_error","message":"prompt (232869 tokens) + max tokens (32768) exceeds the context (262144); requests are never truncated"}}`,
			want: true,
		},
		{
			name: "openai max context length",
			text: "This model's maximum context length is 8192 tokens. However, you requested 9200 tokens in the messages.",
			want: true,
		},
		{
			name: "llama server exceed_context_size_error",
			text: `request (100010 tokens) exceeds the available context size (64000 tokens), try increasing it`,
			want: true,
		},
		{
			name: "context length exceeded",
			text: "Bad Request: This model's context_length_exceeded, reduce your prompt",
			want: true,
		},
		{
			name: "vllm token limit",
			text: "This model's maximum context length is 4096 tokens, however you requested 5000 tokens. Please reduce the length of the input.",
			want: true,
		},
		{"model context window exceeded", `{"error":{"type":"model_context_window_exceeded","code":"model_context_window_exceeded"}}`, true},
		{"too many tokens", "too many tokens in prompt", true},
		{"auth error", "invalid api key", false},
		{"network error", "connection refused", false},
		{"empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsContextOverflowMessage(tt.text); got != tt.want {
				t.Errorf("IsContextOverflowMessage(%q) = %v, want %v", tt.text, got, tt.want)
			}
		})
	}
}

func TestParseOverflowTokenCounts(t *testing.T) {
	text := `API error: status 400, body: {"error":{"type":"invalid_request_error","message":"prompt (232869 tokens) + max tokens (32768) exceeds the context (262144); requests are never truncated"}}`

	promptTokens, maxTokens, contextWindow, ok := ParseOverflowTokenCounts(text)
	if !ok {
		t.Fatalf("ParseOverflowTokenCounts did not parse %q", text)
	}
	if promptTokens != 232_869 {
		t.Errorf("promptTokens = %d, want 232869", promptTokens)
	}
	if maxTokens != 32_768 {
		t.Errorf("maxTokens = %d, want 32768", maxTokens)
	}
	if contextWindow != 262_144 {
		t.Errorf("contextWindow = %d, want 262144", contextWindow)
	}

	if _, _, _, ok := ParseOverflowTokenCounts("invalid api key"); ok {
		t.Error("expected ok=false for unrelated message")
	}
}
