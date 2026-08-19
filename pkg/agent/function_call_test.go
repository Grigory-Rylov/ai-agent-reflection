package agent

import (
	"encoding/json"
	"testing"
)

func TestHasPartialToolCall_Empty(t *testing.T) {
	if hasPartialToolCall("") {
		t.Error("expected false for empty string")
	}
}

func TestHasPartialToolCall_PlainText(t *testing.T) {
	if hasPartialToolCall("just some text") {
		t.Error("expected false for plain text")
	}
}

func TestHasPartialToolCall_CloseToolCall(t *testing.T) {
	if !hasPartialToolCall("some text </tool_call> more text") {
		t.Error("expected true for </tool_call>")
	}
}

func TestHasPartialToolCall_CloseFunction(t *testing.T) {
	if !hasPartialToolCall("text </function> text") {
		t.Error("expected true for </function>")
	}
}

func TestHasPartialToolCall_ParameterTag(t *testing.T) {
	if !hasPartialToolCall("text <parameter=path>/tmp</parameter> text") {
		t.Error("expected true for <parameter=...>")
	}
}

func TestHasPartialToolCall_FunctionTag(t *testing.T) {
	if !hasPartialToolCall("text <function=write_file> text") {
		t.Error("expected true for <function=...>")
	}
}

func TestStripPartialToolCall_NoFragments(t *testing.T) {
	input := "plain text without fragments"
	result := stripPartialToolCall(input)
	if result != input {
		t.Errorf("expected %q, got %q", input, result)
	}
}

func TestStripPartialToolCall_CloseToolCall(t *testing.T) {
	input := "some text </tool_call> more text"
	expected := "some text  more text"
	result := stripPartialToolCall(input)
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}

func TestStripPartialToolCall_CloseFunction(t *testing.T) {
	input := "text </function> end"
	expected := "text  end"
	result := stripPartialToolCall(input)
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}

func TestStripPartialToolCall_ParameterTag(t *testing.T) {
	input := "text <parameter=path>/tmp</parameter> end"
	
	expected := "text /tmp end"
	result := stripPartialToolCall(input)
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}

func TestStripPartialToolCall_FunctionParameterTags(t *testing.T) {
	input := "let me write carefully.\n</parameter>\n<parameter=path>\n/home/test\n</parameter>\n</function>\n</tool_call>"
	expected := "let me write carefully.\n/home/test"
	result := stripPartialToolCall(input)
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}

func TestStripPartialToolCall_RealWorldScenario(t *testing.T) {
	input := `The user is asking me to write a complete HTML file with a parallax driving scene. I need to be very careful about string quotes.
</parameter>
<parameter=path>
/home/orangepi/projects/go/agent/car_parallax.html
</parameter>
</function>
</tool_call>`
	expected := "The user is asking me to write a complete HTML file with a parallax driving scene. I need to be very careful about string quotes.\n/home/orangepi/projects/go/agent/car_parallax.html"
	result := stripPartialToolCall(input)
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}

func TestStripPartialToolCall_OnlyFragments(t *testing.T) {
	input := "</tool_call>\n</function>\n<parameter=path>"
	result := stripPartialToolCall(input)
	if result != "" {
		t.Errorf("expected empty string, got %q", result)
	}
}

func TestHasPartialToolCall_ValidFullToolCall(t *testing.T) {
	
	input := `<tool_call>
<function=write_file>
<parameter=path>/tmp/test</parameter>
</function>
</tool_call>`
	if hasPartialToolCall(input) {
		t.Error("expected false for valid full tool call")
	}
}

func TestHasPartialToolCall_CloseWithoutOpen(t *testing.T) {
	
	input := "some text\n</tool_call>\nmore text"
	if !hasPartialToolCall(input) {
		t.Error("expected true for </tool_call> without <tool_call>")
	}
}

func TestHasPartialToolCall_FunctionParamOutsideContext(t *testing.T) {
	
	input := "let me write carefully.\n<parameter=path>\n/home/test\n</parameter>"
	if !hasPartialToolCall(input) {
		t.Error("expected true for <parameter=...> outside tool_call context")
	}
}

func TestHasPartialToolCall_UnclosedToolCall(t *testing.T) {
	
	input := "some thinking\n<tool_call>\n<function=write_file>\n<parameter=path>/tmp/test.txt</parameter>"
	if !hasPartialToolCall(input) {
		t.Error("expected true for <tool_call> without </tool_call>")
	}
}

func TestHasPartialToolCall_ValidWithUnclosed(t *testing.T) {
	
	input := "<tool_call>\n<function=read_file>\n<parameter=path>/tmp/x</parameter>\n</function>\n</tool_call>\n<tool_call>\n<function=write_file>"
	if !hasPartialToolCall(input) {
		t.Error("expected true for valid + unclosed <tool_call>")
	}
}


func TestParseToolArguments_ArrayOptions(t *testing.T) {
	
	
	raw := json.RawMessage(`"{"question":"Pick color","options":[{"label":"Red"},{"label":"Blue"}]}"`)
	tc := ToolCall{ID: "call_1", Function: ToolCallFunction{Name: "question", Arguments: raw}}
	args, err := parseToolArguments(tc)
	if err != nil {
		t.Fatalf("parseToolArguments failed: %v", err)
	}
	opts, ok := args["options"]
	if !ok {
		t.Fatal("expected options key")
	}
	var parsed []map[string]string
	if err := json.Unmarshal([]byte(opts), &parsed); err != nil {
		t.Fatalf("options should be valid JSON array, got: %s (error: %v)", opts, err)
	}
	if len(parsed) != 2 {
		t.Fatalf("expected 2 options, got %d: %v", len(parsed), parsed)
	}
	if parsed[0]["label"] != "Red" {
		t.Errorf("expected label 'Red', got '%s'", parsed[0]["label"])
	}
}
