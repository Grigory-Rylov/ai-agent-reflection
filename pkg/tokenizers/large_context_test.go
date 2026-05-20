package tokenizers

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// ============================================================
// Semi-integration tests for large context handling
// ============================================================

// TestLargePromptTokenization tests tokenizer behavior with 100K+ token prompts
// This test requires a running llama-server
func TestLargePromptTokenization(t *testing.T) {
	// Skip if no llama-server available
	serverURL := os.Getenv("LLAMA_SERVER_URL")
	if serverURL == "" {
		serverURL = "http://localhost:8081"
	}

	// Check if server is available
	tokenizer := NewLlamaServerTokenizer(serverURL, "", 32000)
	tokenizer.SetDebug(true)

	// Try a simple request first to check connectivity
	_, err := tokenizer.CountTokens("test")
	if err != nil {
		t.Skipf("llama-server not available at %s: %v", serverURL, err)
	}

	t.Run("SmallPrompt", func(t *testing.T) {
		text := "Hello, world!"
		tokens, err := tokenizer.CountTokens(text)
		if err != nil {
			t.Fatalf("Failed to count tokens: %v", err)
		}
		t.Logf("Small prompt: %d tokens for %d chars", tokens, len(text))
	})

	t.Run("MediumPrompt_10K", func(t *testing.T) {
		// ~10K tokens = ~40K chars
		text := strings.Repeat("This is a test sentence for tokenization. ", 1000)

		start := time.Now()
		tokens, err := tokenizer.CountTokens(text)
		elapsed := time.Since(start)

		if err != nil {
			t.Fatalf("Failed to count tokens: %v", err)
		}
		t.Logf("Medium prompt: %d tokens for %d chars in %v", tokens, len(text), elapsed)
	})

	t.Run("LargePrompt_100K", func(t *testing.T) {
		// Read the debug_prompt.txt file
		data, err := os.ReadFile("debug_prompt.txt")
		if err != nil {
			t.Skipf("debug_prompt.txt not found: %v", err)
		}

		text := string(data)
		t.Logf("Loaded debug_prompt.txt: %d chars, ~%d estimated tokens", len(text), len(text)/4)

		start := time.Now()
		tokens, err := tokenizer.CountTokens(text)
		elapsed := time.Since(start)

		if err != nil {
			// THIS IS THE KEY TEST - what error do we get?
			t.Logf("ERROR tokenizing large prompt: %v", err)
			t.Logf("Error type: %T", err)

			// Check if error contains info about context limit
			errStr := err.Error()
			if strings.Contains(errStr, "context") ||
				strings.Contains(errStr, "length") ||
				strings.Contains(errStr, "token") ||
				strings.Contains(errStr, "limit") {
				t.Logf("Error appears to be context-related: %s", errStr)
			}

			// Don't fail - we want to observe the error
			return
		}

		t.Logf("Large prompt: %d tokens for %d chars in %v", tokens, len(text), elapsed)

		// Check if tokens exceed max context
		maxCtx := tokenizer.MaxContextLength()
		if tokens > maxCtx {
			t.Logf("WARNING: Tokens (%d) exceed max context (%d)", tokens, maxCtx)
		}
	})
}

// TestTokenizeWithProgress tests tokenization with progress reporting
func TestTokenizeWithProgress(t *testing.T) {
	serverURL := os.Getenv("LLAMA_SERVER_URL")
	if serverURL == "" {
		serverURL = "http://localhost:8081"
	}

	tokenizer := NewLlamaServerTokenizer(serverURL, "", 32000)

	// Check connectivity
	_, err := tokenizer.CountTokens("test")
	if err != nil {
		t.Skipf("llama-server not available: %v", err)
	}

	// Load large file
	data, err := os.ReadFile("debug_prompt.txt")
	if err != nil {
		t.Skipf("debug_prompt.txt not found: %v", err)
	}

	text := string(data)
	t.Logf("Testing with %d chars", len(text))

	// Test tokenizing in chunks
	chunkSize := 10000
	var totalTokens int
	var chunks []int

	for i := 0; i < len(text); i += chunkSize {
		end := i + chunkSize
		if end > len(text) {
			end = len(text)
		}

		chunk := text[i:end]
		tokens, err := tokenizer.CountTokens(chunk)
		if err != nil {
			t.Logf("Error in chunk %d-%d: %v", i, end, err)
			continue
		}

		chunks = append(chunks, tokens)
		totalTokens += tokens
	}

	t.Logf("Chunked tokenization: %d total tokens across %d chunks", totalTokens, len(chunks))
}

// TestMaxContextExceeded tests what happens when we try to send more tokens than model can handle
func TestMaxContextExceeded(t *testing.T) {
	serverURL := os.Getenv("LLAMA_SERVER_URL")
	if serverURL == "" {
		serverURL = "http://localhost:8081"
	}

	// Create tokenizer with small max context to force overflow
	smallMaxCtx := 1000
	tokenizer := NewLlamaServerTokenizer(serverURL, "", smallMaxCtx)

	_, err := tokenizer.CountTokens("test")
	if err != nil {
		t.Skipf("llama-server not available: %v", err)
	}

	// Create text that definitely exceeds 1000 tokens
	largeText := strings.Repeat("This is a test sentence. ", 1000) // ~5000 tokens

	tokens, err := tokenizer.CountTokens(largeText)
	if err != nil {
		t.Logf("Error as expected: %v", err)
		return
	}

	t.Logf("Tokenized %d tokens (max context: %d)", tokens, smallMaxCtx)
	if tokens > smallMaxCtx {
		t.Logf("Tokens exceed max context - but no error returned!")
		t.Logf("This indicates tokenizer does NOT check context limits")
	}
}

// TestMessagesTokenCount tests counting tokens across multiple messages
func TestMessagesTokenCount(t *testing.T) {
	serverURL := os.Getenv("LLAMA_SERVER_URL")
	if serverURL == "" {
		serverURL = "http://localhost:8081"
	}

	tokenizer := NewLlamaServerTokenizer(serverURL, "", 32000)

	_, err := tokenizer.CountTokens("test")
	if err != nil {
		t.Skipf("llama-server not available: %v", err)
	}

	// Load large file
	data, err := os.ReadFile("debug_prompt.txt")
	if err != nil {
		t.Skipf("debug_prompt.txt not found: %v", err)
	}

	largeContent := string(data)

	// Create messages that simulate a long conversation
	messages := []Message{
		{Role: "system", Content: "You are a helpful assistant."},
		{Role: "user", Content: largeContent[:50000]}, // ~12K tokens
		{Role: "assistant", Content: "I understand the context."},
		{Role: "user", Content: largeContent[50000:100000]}, // ~12K tokens
		{Role: "assistant", Content: "Processing..."},
		{Role: "user", Content: largeContent[100000:150000]}, // ~12K tokens
	}

	start := time.Now()
	tokens, err := tokenizer.CountMessagesTokens(messages)
	elapsed := time.Since(start)

	if err != nil {
		t.Logf("Error counting message tokens: %v", err)
		return
	}

	t.Logf("Messages tokenization: %d tokens in %v", tokens, elapsed)

	maxCtx := tokenizer.MaxContextLength()
	if tokens > maxCtx {
		t.Logf("WARNING: Total tokens (%d) exceed max context (%d)", tokens, maxCtx)
	}
}

// TestPromptDebug generates debug info about the test file
func TestPromptDebug(t *testing.T) {
	data, err := os.ReadFile("debug_prompt.txt")
	if err != nil {
		t.Skipf("debug_prompt.txt not found: %v", err)
	}

	text := string(data)

	t.Logf("=== Debug Prompt Stats ===")
	t.Logf("Total characters: %d", len(text))
	t.Logf("Estimated tokens: %d (at 4 chars/token)", len(text)/4)
	t.Logf("Estimated tokens: %d (at 3 chars/token)", len(text)/3)
	t.Logf("Lines: %d", strings.Count(text, "\n"))
	t.Logf("Words: %d", len(strings.Fields(text)))

	// Check for different context limits
	limits := []int{4096, 8192, 16384, 32768, 65536, 128000}
	for _, limit := range limits {
		estTokens := len(text) / 4
		if estTokens > limit {
			t.Logf("Context %d: EXCEEDS by %d tokens", limit, estTokens-limit)
		} else {
			t.Logf("Context %d: OK (room for %d more)", limit, limit-estTokens)
		}
	}
}

// RunDebug is a helper function to run the tests manually
func RunDebug() {
	serverURL := "http://localhost:8081"
	tokenizer := NewLlamaServerTokenizer(serverURL, "", 32000)
	tokenizer.SetDebug(true)

	data, err := os.ReadFile("debug_prompt.txt")
	if err != nil {
		fmt.Printf("Error reading file: %v\n", err)
		return
	}

	text := string(data)
	fmt.Printf("Loaded: %d chars, ~%d tokens\n", len(text), len(text)/4)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	_ = ctx // context is used in real implementation

	start := time.Now()
	tokens, err := tokenizer.CountTokens(text)
	elapsed := time.Since(start)

	if err != nil {
		fmt.Printf("ERROR: %v (took %v)\n", err, elapsed)
		return
	}

	fmt.Printf("SUCCESS: %d tokens (took %v)\n", tokens, elapsed)
}
