package agent

import (
	"fmt"
	"testing"
)

func TestParseContextOverflowError(t *testing.T) {
	tests := []struct {
		name          string
		err           error
		wantOverflow  bool
		wantTokens    int
		wantMaxCtx    int
	}{
		{
			name:         "nil error",
			err:          nil,
			wantOverflow: false,
		},
		{
			name:         "unrelated error",
			err:          fmt.Errorf("some other error"),
			wantOverflow: false,
		},
		{
			name: "real llama-server error",
			err: fmt.Errorf(`API error: status 400, body: {"error":{"code":400,"message":"request (100010 tokens) exceeds the available context size (64000 tokens), try increasing it","type":"exceed_context_size_error","n_prompt_tokens":100010,"n_ctx":64000}}`),
			wantOverflow:  true,
			wantTokens:    100010,
			wantMaxCtx:    64000,
		},
		{
			name:         "context exceed without JSON",
			err:          fmt.Errorf("request exceeds context size"),
			wantOverflow: true,
			wantTokens:   0, // не распарсились
			wantMaxCtx:   0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			overflow := ParseContextOverflowError(tt.err)

			if tt.wantOverflow {
				if overflow == nil {
					t.Fatal("expected overflow error, got nil")
				}
				if tt.wantTokens > 0 && overflow.PromptTokens != tt.wantTokens {
					t.Errorf("PromptTokens = %d, want %d", overflow.PromptTokens, tt.wantTokens)
				}
				if tt.wantMaxCtx > 0 && overflow.MaxContext != tt.wantMaxCtx {
					t.Errorf("MaxContext = %d, want %d", overflow.MaxContext, tt.wantMaxCtx)
				}
			} else {
				if overflow != nil {
					t.Fatalf("expected nil, got overflow error: %v", overflow)
				}
			}
		})
	}
}

func TestIsContextOverflowError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "nil error",
			err:  nil,
			want: false,
		},
		{
			name: "unrelated error",
			err:  fmt.Errorf("connection refused"),
			want: false,
		},
		{
			name: "context overflow error type",
			err: &ContextOverflowError{
				PromptTokens: 1000,
				MaxContext:   500,
			},
			want: true,
		},
		{
			name: "context overflow in message",
			err:  fmt.Errorf("request exceeds context size limit"),
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsContextOverflowError(tt.err)
			if got != tt.want {
				t.Errorf("IsContextOverflowError() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestContextOverflowError_Error(t *testing.T) {
	overflow := &ContextOverflowError{
		PromptTokens: 100000,
		MaxContext:   64000,
		Message:      "request exceeds context",
	}

	errStr := overflow.Error()
	if !containsAll(errStr, "100000", "64000", "exceed") {
		t.Errorf("Error() = %q, missing expected content", errStr)
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !contains(s, sub) {
			return false
		}
	}
	return true
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		(len(s) > 0 && len(sub) > 0 && findSubstring(s, sub)))
}

func findSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
