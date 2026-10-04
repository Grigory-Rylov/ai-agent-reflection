package compress

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func captureCompressionRequestBody(window int, userPrompt string, requestedTokens int) (map[string]interface{}, error) {
	var body map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer srv.Close()

	compressor := NewLLMCompressor(srv.URL, "m", 0)
	compressor.SetContextWindow(window)
	_, err := compressor.Complete(context.Background(), "sys", userPrompt, requestedTokens)
	return body, err
}

func TestCompressionRequestClampsMaxTokensToWindow(t *testing.T) {
	body, err := captureCompressionRequestBody(8_192, strings.Repeat("u", 4_000), 32_768)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got := body["max_tokens"]; got != float64(4_096) {
		t.Errorf("max_tokens = %v, want 4096", got)
	}
}

func TestCompressionRequestKeepsMaxTokensWithoutWindow(t *testing.T) {
	body, err := captureCompressionRequestBody(0, strings.Repeat("u", 4_000), 32_768)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got := body["max_tokens"]; got != float64(32_768) {
		t.Errorf("max_tokens = %v, want 32768", got)
	}
}

func TestCompleteSurfacesOverflowMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":{"type":"invalid_request_error","message":"prompt (232869 tokens) + max tokens (32768) exceeds the context (262144); requests are never truncated"}}`)
	}))
	defer srv.Close()

	_, err := NewLLMCompressor(srv.URL, "m", 0).Complete(context.Background(), "sys", "usr", 100)
	if err == nil {
		t.Fatal("expected error for status 400")
	}
	if !IsContextOverflowMessage(err.Error()) {
		t.Errorf("error must carry the server overflow message, got %v", err)
	}
}
