package parser

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)


type EventType string

const (
	EventDelta     EventType = "delta"     
	EventReasoning EventType = "reasoning" 
	EventStop      EventType = "stop"      
	EventDone      EventType = "done"      
)


type DeltaChunk struct {
	Role           string `json:"role"`
	Content        string `json:"content"`
	ReasoningContent string `json:"reasoning_content"`
}


type SSEChunk struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	Model   string `json:"model"`

	
	ChoiceIndex    int         `json:"choice_index"`
	FinishReason   *string     `json:"finish_reason"`
	Delta          DeltaChunk  `json:"delta"`
	Content        string      `json:"-"`
	ReasoningContent string   `json:"-"`

	
	Timings *Timings `json:"timings"`
}


type Timings struct {
	PromptN            int64   `json:"prompt_n"`
	PromptMS           float64 `json:"prompt_ms"`
	PromptPerTokenMS   float64 `json:"prompt_per_token_ms"`
	PromptPerSecond    float64 `json:"prompt_per_second"`
	PredictedN         int64   `json:"predicted_n"`
	PredictedMS        float64 `json:"predicted_ms"`
	PredictedPerTokenMS float64 `json:"predicted_per_token_ms"`
	PredictedPerSecond float64 `json:"predicted_per_second"`
}


func (c *SSEChunk) UnmarshalJSON(data []byte) error {
	
	var raw struct {
		ID           string     `json:"id"`
		Object       string     `json:"object"`
		Created      int64      `json:"created"`
		Model        string     `json:"model"`
		Choices      []struct {
			Index        int         `json:"index"`
			FinishReason *string     `json:"finish_reason"`
			Delta        DeltaChunk  `json:"delta"`
		} `json:"choices"`
		Timings *Timings `json:"timings"`
	}

	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	
	c.ID = raw.ID
	c.Object = raw.Object
	c.Created = raw.Created
	c.Model = raw.Model
	c.ChoiceIndex = 0
	c.FinishReason = raw.Choices[0].FinishReason
	c.Timings = raw.Timings

	if len(raw.Choices) > 0 {
		c.ChoiceIndex = raw.Choices[0].Index
		c.Delta = raw.Choices[0].Delta
		c.Content = raw.Choices[0].Delta.Content
		c.ReasoningContent = raw.Choices[0].Delta.ReasoningContent
	}

	return nil
}


func (c SSEChunk) EventType() EventType {
	if c.FinishReason != nil {
		return EventStop
	}
	if c.ReasoningContent != "" {
		return EventReasoning
	}
	if c.Content != "" {
		return EventDelta
	}
	return ""
}


func (c SSEChunk) IsCompletion() bool {
	return c.FinishReason != nil
}


func (c SSEChunk) IsStopReason() bool {
	if c.FinishReason == nil {
		return false
	}
	return *c.FinishReason == "stop" || *c.FinishReason == "length" || *c.FinishReason == "eoi" || *c.FinishReason == "eos"
}


type Parser struct {
	reader *bufio.Reader
}


func NewParser(r io.Reader) *Parser {
	return &Parser{
		reader: bufio.NewReader(r),
	}
}


func (p *Parser) ParseChunk() (EventType, string, error) {
	line, err := p.reader.ReadSlice('\n')
	if err != nil {
		if err == io.EOF {
			return "", "", io.EOF
		}
		return "", "", fmt.Errorf("failed to read line: %w", err)
	}

	
	lineStr := strings.TrimSpace(string(line))

	
	if lineStr == "" {
		return p.ParseChunk() 
	}

	
	if strings.Contains(lineStr, "[DONE]") {
		return EventDone, "", nil
	}

	
	if !strings.HasPrefix(lineStr, "data: ") {
		return "", "", fmt.Errorf("invalid SSE format: %s", lineStr)
	}

	
	jsonData := strings.TrimPrefix(lineStr, "data: ")
	return "", jsonData, nil
}


func (p *Parser) ParseStream() ([]SSEChunk, EventType, error) {
	var chunks []SSEChunk
	var lastEventType EventType

	for {
		eventType, jsonData, err := p.ParseChunk()
		if err != nil {
			return chunks, lastEventType, err
		}

		
		if eventType == EventDone {
			return chunks, EventDone, nil
		}

		
		var chunk SSEChunk
		if err := json.Unmarshal([]byte(jsonData), &chunk); err != nil {
			continue 
		}

		chunks = append(chunks, chunk)
		lastEventType = eventType
	}
}


func ParseSSELine(line string) (SSEChunk, error) {
	if !strings.HasPrefix(line, "data: ") {
		return SSEChunk{}, fmt.Errorf("invalid SSE format: %s", line)
	}

	jsonData := strings.TrimPrefix(line, "data: ")
	var chunk SSEChunk
	if err := json.Unmarshal([]byte(jsonData), &chunk); err != nil {
		return SSEChunk{}, fmt.Errorf("failed to parse JSON: %w", err)
	}

	return chunk, nil
}


func CountChunksByType(chunks []SSEChunk) (delta, reasoning, stop int) {
	for _, c := range chunks {
		switch c.EventType() {
		case EventDelta:
			delta++
		case EventReasoning:
			reasoning++
		case EventStop:
			stop++
		}
	}
	return
}


func ExtractContent(chunks []SSEChunk) string {
	var result strings.Builder
	for _, c := range chunks {
		if c.Content != "" {
			result.WriteString(c.Content)
		}
	}
	return result.String()
}


func ExtractReasoning(chunks []SSEChunk) string {
	var result strings.Builder
	for _, c := range chunks {
		if c.ReasoningContent != "" {
			result.WriteString(c.ReasoningContent)
		}
	}
	return result.String()
}
