package agent

import (
	"testing"
)

func TestToolCallSignature(t *testing.T) {
	tc := ToolCall{
		ID:   "call_1",
		Type: "function",
		Function: ToolCallFunction{
			Name:      "file_read",
			Arguments: []byte(`"{\"path\":\"/tmp/test.txt\"}"`),
		},
	}

	sig := toolCallSignature(tc)
	expected := `file_read:{"path":"/tmp/test.txt"}`
	if sig != expected {
		t.Errorf("expected %q, got %q", expected, sig)
	}
}

func TestXMLToolCallSignature(t *testing.T) {
	tc := XMLToolCall{
		Name: "file_read",
		Args: map[string]string{"path": "/tmp/test.txt"},
	}

	sig := xmlToolCallSignature(tc)
	expected := `file_read:{"path":"/tmp/test.txt"}`
	if sig != expected {
		t.Errorf("expected %q, got %q", expected, sig)
	}
}

func TestToolCallSignature_Matching(t *testing.T) {
	// NATIVE tool call
	nativeTC := ToolCall{
		ID:   "call_1",
		Type: "function",
		Function: ToolCallFunction{
			Name:      "file_read",
			Arguments: []byte(`"{\"path\":\"/tmp/test.txt\"}"`),
		},
	}

	// XML tool call с теми же аргументами
	xmlTC := XMLToolCall{
		Name: "file_read",
		Args: map[string]string{"path": "/tmp/test.txt"},
	}

	nativeSig := toolCallSignature(nativeTC)
	xmlSig := xmlToolCallSignature(xmlTC)

	if nativeSig != xmlSig {
		t.Errorf("signatures should match: native=%q, xml=%q", nativeSig, xmlSig)
	}
}

func TestToolCallSignature_Different(t *testing.T) {
	// NATIVE tool call
	nativeTC := ToolCall{
		ID:   "call_1",
		Type: "function",
		Function: ToolCallFunction{
			Name:      "file_read",
			Arguments: []byte(`"{\"path\":\"/tmp/test.txt\"}"`),
		},
	}

	// XML tool call с другими аргументами
	xmlTC := XMLToolCall{
		Name: "file_read",
		Args: map[string]string{"path": "/tmp/other.txt"},
	}

	nativeSig := toolCallSignature(nativeTC)
	xmlSig := xmlToolCallSignature(xmlTC)

	if nativeSig == xmlSig {
		t.Errorf("signatures should NOT match: native=%q, xml=%q", nativeSig, xmlSig)
	}
}

func TestToolCallSignature_DifferentTools(t *testing.T) {
	// NATIVE tool call - file_read
	nativeTC := ToolCall{
		ID:   "call_1",
		Type: "function",
		Function: ToolCallFunction{
			Name:      "file_read",
			Arguments: []byte(`"{\"path\":\"/tmp/test.txt\"}"`),
		},
	}

	// XML tool call - file_write (другой инструмент)
	xmlTC := XMLToolCall{
		Name: "file_write",
		Args: map[string]string{"path": "/tmp/test.txt"},
	}

	nativeSig := toolCallSignature(nativeTC)
	xmlSig := xmlToolCallSignature(xmlTC)

	if nativeSig == xmlSig {
		t.Errorf("signatures should NOT match for different tools: native=%q, xml=%q", nativeSig, xmlSig)
	}
}
