package agent

import (
	"testing"
)


func TestProcessToolResults_DoesNotParseToolCallsFromReasoning(t *testing.T) {
	
	

	
	reasoningText := `I'll execute a command:
<invoke>
<function=shell_execute>
<parameter=command>echo test</parameter>
</function>
</invoke>`

	responseText := "Here's the response without tool calls"

	
	parsedReasoning := ParseXMLToolCalls(reasoningText)
	if len(parsedReasoning.ToolCalls) != 1 {
		t.Errorf("Expected 1 tool call in reasoning, got %d", len(parsedReasoning.ToolCalls))
	}

	
	parsedResponse := ParseXMLToolCalls(responseText)
	if len(parsedResponse.ToolCalls) != 0 {
		t.Errorf("Expected 0 tool calls in response, got %d", len(parsedResponse.ToolCalls))
	}

	
	

	t.Log("PASS: reasoning contains tool call, response doesn't")
}


func TestProcessToolResults_ParsesToolCallsOnlyFromResponseText(t *testing.T) {
	responseWithToolCall := `Here's my response:
<invoke>
<function=file_read>
<parameter=path>/tmp/test.txt</parameter>
</function>
</invoke>`

	parsed := ParseXMLToolCalls(responseWithToolCall)
	if len(parsed.ToolCalls) != 1 {
		t.Errorf("Expected 1 tool call, got %d", len(parsed.ToolCalls))
	}

	if parsed.ToolCalls[0].Name != "file_read" {
		t.Errorf("Expected tool name 'file_read', got '%s'", parsed.ToolCalls[0].Name)
	}

	t.Log("PASS: tool calls correctly parsed from responseText")
}
