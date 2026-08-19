//go:build integration

package agent

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)


func TestContextOverflowIntegration(t *testing.T) {
	serverURL := os.Getenv("LLAMA_SERVER_URL")
	if serverURL == "" {
		serverURL = "http://localhost:8081"
	}

	
	config := Config{
		LlamaServerURL: serverURL,
		Model:          "",
		MaxTokens:      100,
		Temperature:    0.7,
		EnableTools:    false,
	}

	agent := NewAgent(config)

	
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err := agent.ProcessMessage(ctx, "Hello", 1)
	if err != nil {
		t.Skipf("llama-server not available: %v", err)
	}

	t.Run("DetectContextOverflow", func(t *testing.T) {
		
		
		largeContent := strings.Repeat("This is a test sentence for context overflow. ", 3000)

		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()

		_, err := agent.ProcessMessage(ctx, largeContent, 2)

		if err == nil {
			t.Log("WARNING: No error returned for large context - model might have large context")
			return
		}

		t.Logf("Error returned: %v", err)

		
		overflow := ParseContextOverflowError(err)
		if overflow == nil {
			t.Logf("Error is not context overflow: %v", err)
			return
		}

		t.Logf("Context overflow detected: %d tokens, max %d",
			overflow.PromptTokens, overflow.MaxContext)

		if overflow.PromptTokens > 0 {
			t.Logf("Successfully parsed overflow info: prompt=%d, max=%d",
				overflow.PromptTokens, overflow.MaxContext)
		}
	})

	t.Run("IsContextOverflowErrorWorks", func(t *testing.T) {
		
		testErr := fmt.Errorf(`API error: status 400, body: {"error":{"message":"request exceeds context"}}`)

		if !IsContextOverflowError(testErr) {
			t.Error("IsContextOverflowError should return true for context error")
		}

		if IsContextOverflowError(fmt.Errorf("some other error")) {
			t.Error("IsContextOverflowError should return false for other error")
		}
	})
}


func TestContextOverflowStatsFunction(t *testing.T) {
	
	err := fmt.Errorf(`API error: status 400, body: {"error":{"n_prompt_tokens":100000,"n_ctx":64000,"message":"request exceeds context size"}}`)

	promptTokens, maxContext, isOverflow := ContextOverflowStats(err)

	if !isOverflow {
		t.Error("Expected isOverflow to be true")
	}
	if promptTokens != 100000 {
		t.Errorf("Expected promptTokens=100000, got %d", promptTokens)
	}
	if maxContext != 64000 {
		t.Errorf("Expected maxContext=64000, got %d", maxContext)
	}

	
	_, _, isOverflow = ContextOverflowStats(fmt.Errorf("some error"))
	if isOverflow {
		t.Error("Expected isOverflow to be false for non-overflow error")
	}
}


func TestContextOverflowWithRealServer(t *testing.T) {
	serverURL := os.Getenv("LLAMA_SERVER_URL")
	if serverURL == "" {
		serverURL = "http://localhost:8081"
	}

	
	config := Config{
		LlamaServerURL: serverURL,
		Model:          "",
		MaxTokens:      10, 
		Temperature:    0.7,
		EnableTools:    false,
	}

	agent := NewAgent(config)

	
	ctx := context.Background()
	_, err := agent.ProcessMessage(ctx, "test", 100)
	if err != nil {
		t.Skipf("llama-server not available: %v", err)
	}

	
	largeMessage := strings.Repeat("x ", 100000) 

	start := time.Now()
	_, err = agent.ProcessMessage(ctx, largeMessage, 101)
	elapsed := time.Since(start)

	if err != nil {
		t.Logf("Error after %v: %v", elapsed, err)

		
		if IsContextOverflowError(err) {
			t.Log("SUCCESS: Context overflow error correctly identified")

			overflow := ParseContextOverflowError(err)
			if overflow != nil {
				t.Logf("Overflow details: prompt=%d, max=%d",
					overflow.PromptTokens, overflow.MaxContext)
			}
		} else {
			t.Logf("Error is not context overflow: %v", err)
		}
	} else {
		t.Logf("No error returned after %v - model context might be large enough", elapsed)
	}
}
