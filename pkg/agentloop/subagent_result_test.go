package agentloop

import (
	"testing"
)

func TestParseSubAgentResultFull(t *testing.T) {
	text := `I implemented the feature.

RESULT:
status: success
summary: Added file read tool with offset/limit support.
files: pkg/tools/impl_tools.go, pkg/tools/impl_tools_test.go
next: done`

	res := ParseSubAgentResult(text)
	if res.Status != "success" {
		t.Errorf("status = %q, want success", res.Status)
	}
	if res.Summary != "Added file read tool with offset/limit support." {
		t.Errorf("summary = %q", res.Summary)
	}
	if len(res.Files) != 2 || res.Files[0] != "pkg/tools/impl_tools.go" || res.Files[1] != "pkg/tools/impl_tools_test.go" {
		t.Errorf("files = %v", res.Files)
	}
	if res.Next != "done" {
		t.Errorf("next = %q, want done", res.Next)
	}
	if res.Raw != text {
		t.Errorf("raw should preserve original text")
	}
}

func TestParseSubAgentResultMissing(t *testing.T) {
	text := "I did a lot of work on the module.\n\nThe final paragraph describes everything that was changed in detail."

	res := ParseSubAgentResult(text)
	if res.Status != "partial" {
		t.Errorf("status = %q, want partial", res.Status)
	}
	if res.Summary == "" {
		t.Error("summary should fall back to last paragraph")
	}
	if res.Raw != text {
		t.Error("raw should preserve original text")
	}
}

func TestParseSubAgentResultMalformed(t *testing.T) {
	text := `RESULT:
summary: no status here
files: a.go`

	res := ParseSubAgentResult(text)
	if res.Status != "partial" {
		t.Errorf("status = %q, want partial when status field missing", res.Status)
	}
	if res.Summary != "no status here" {
		t.Errorf("summary = %q", res.Summary)
	}
}

func TestParseSubAgentResultFailure(t *testing.T) {
	text := `RESULT:
status: failure
summary: Build failed: undefined symbol X.
files: none
next: fix the import`

	res := ParseSubAgentResult(text)
	if res.Status != "failure" {
		t.Errorf("status = %q, want failure", res.Status)
	}
	if len(res.Files) != 0 {
		t.Errorf("files = %v, want empty for 'none'", res.Files)
	}
}

func TestParseSubAgentResultUnknownStatus(t *testing.T) {
	text := `RESULT:
status: maybe
summary: weird`

	res := ParseSubAgentResult(text)
	if res.Status != "partial" {
		t.Errorf("status = %q, want partial for unknown value", res.Status)
	}
}
func TestSubAgentToolReturnsStructured(t *testing.T) {
	tool := &SubAgentTool{}
	text := "Did the work.\n\nRESULT:\nstatus: success\nsummary: Implemented X.\nfiles: a.go, b.go\nnext: done"

	res := tool.buildResult("worker", text)
	if !res.Success {
		t.Fatalf("expected success, got error: %s", res.Error)
	}
	data, ok := res.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("data is %T", res.Data)
	}
	if data["status"] != "success" {
		t.Errorf("status = %v", data["status"])
	}
	if data["summary"] != "Implemented X." {
		t.Errorf("summary = %v", data["summary"])
	}
	if data["response"] != text {
		t.Error("response should contain full text")
	}
}

func TestSubAgentToolFailurePropagates(t *testing.T) {
	tool := &SubAgentTool{}
	text := "RESULT:\nstatus: failure\nsummary: Build broke.\nfiles: none\nnext: fix imports"

	res := tool.buildResult("worker", text)
	if res.Success {
		t.Fatal("expected failure result")
	}
	if res.Error == "" {
		t.Error("expected error message with summary")
	}
	data, ok := res.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("data is %T", res.Data)
	}
	if data["status"] != "failure" {
		t.Errorf("status = %v", data["status"])
	}
}
