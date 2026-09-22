package agent

import (
	"testing"
)

func TestToolCallSignature(t *testing.T) {
	tc := ToolCall{
		ID:   "call_1",
		Type: "function",
		Function: ToolCallFunction{
			Name:      "read",
			Arguments: []byte(`"{\"path\":\"/tmp/test.txt\"}"`),
		},
	}

	sig := toolCallSignature(tc)
	expected := `read:{"path":"/tmp/test.txt"}`
	if sig != expected {
		t.Errorf("expected %q, got %q", expected, sig)
	}
}

func TestXMLToolCallSignature(t *testing.T) {
	tc := XMLToolCall{
		Name: "read",
		Args: map[string]string{"path": "/tmp/test.txt"},
	}

	sig := xmlToolCallSignature(tc)
	expected := `read:{"path":"/tmp/test.txt"}`
	if sig != expected {
		t.Errorf("expected %q, got %q", expected, sig)
	}
}

func TestToolCallSignature_Matching(t *testing.T) {

	nativeTC := ToolCall{
		ID:   "call_1",
		Type: "function",
		Function: ToolCallFunction{
			Name:      "read",
			Arguments: []byte(`"{\"path\":\"/tmp/test.txt\"}"`),
		},
	}

	xmlTC := XMLToolCall{
		Name: "read",
		Args: map[string]string{"path": "/tmp/test.txt"},
	}

	nativeSig := toolCallSignature(nativeTC)
	xmlSig := xmlToolCallSignature(xmlTC)

	if nativeSig != xmlSig {
		t.Errorf("signatures should match: native=%q, xml=%q", nativeSig, xmlSig)
	}
}

func TestToolCallSignature_Different(t *testing.T) {

	nativeTC := ToolCall{
		ID:   "call_1",
		Type: "function",
		Function: ToolCallFunction{
			Name:      "read",
			Arguments: []byte(`"{\"path\":\"/tmp/test.txt\"}"`),
		},
	}

	xmlTC := XMLToolCall{
		Name: "read",
		Args: map[string]string{"path": "/tmp/other.txt"},
	}

	nativeSig := toolCallSignature(nativeTC)
	xmlSig := xmlToolCallSignature(xmlTC)

	if nativeSig == xmlSig {
		t.Errorf("signatures should NOT match: native=%q, xml=%q", nativeSig, xmlSig)
	}
}

func TestToolCallSignature_DifferentTools(t *testing.T) {

	nativeTC := ToolCall{
		ID:   "call_1",
		Type: "function",
		Function: ToolCallFunction{
			Name:      "read",
			Arguments: []byte(`"{\"path\":\"/tmp/test.txt\"}"`),
		},
	}

	xmlTC := XMLToolCall{
		Name: "write",
		Args: map[string]string{"path": "/tmp/test.txt"},
	}

	nativeSig := toolCallSignature(nativeTC)
	xmlSig := xmlToolCallSignature(xmlTC)

	if nativeSig == xmlSig {
		t.Errorf("signatures should NOT match for different tools: native=%q, xml=%q", nativeSig, xmlSig)
	}
}
