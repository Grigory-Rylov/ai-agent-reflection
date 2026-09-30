package agentloop

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/tools"
)

func TestFanOutCapAndOrder(t *testing.T) {
	var cur, maxSeen int32
	results := fanOut(6, 2, func(i int) tools.ToolResult {
		c := atomic.AddInt32(&cur, 1)
		for {
			m := atomic.LoadInt32(&maxSeen)
			if c <= m || atomic.CompareAndSwapInt32(&maxSeen, m, c) {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
		atomic.AddInt32(&cur, -1)
		return tools.ToolResult{Success: true, Data: map[string]interface{}{"i": i}}
	})
	if len(results) != 6 {
		t.Fatalf("len = %d, want 6", len(results))
	}
	if atomic.LoadInt32(&maxSeen) > 2 {
		t.Errorf("max concurrency = %d, want <= 2", maxSeen)
	}
	for i, r := range results {
		if !r.Success {
			t.Fatalf("result %d not success", i)
		}
		m, ok := r.Data.(map[string]interface{})
		if !ok || m["i"] != i {
			t.Errorf("result %d has index %v (order not stable)", i, r.Data)
		}
	}
}

func TestAggregateBatchResults(t *testing.T) {
	res := []tools.ToolResult{
		{Success: true, Data: map[string]interface{}{"status": "success", "summary": "done"}},
		{Success: false, Error: "boom"},
		{Success: true, Data: map[string]interface{}{"summary": "ok2"}},
	}
	out := aggregateBatchResults(res)
	if !out.Success {
		t.Fatalf("aggregate should be success")
	}
	data := out.Data.(map[string]interface{})
	if data["ok"] != 2 || data["failed"] != 1 {
		t.Errorf("ok/failed = %v/%v, want 2/1", data["ok"], data["failed"])
	}
	items, _ := data["results"].([]map[string]interface{})
	if len(items) != 3 {
		t.Fatalf("items = %d, want 3", len(items))
	}
	if items[1]["status"] != "error" || items[1]["error"] != "boom" {
		t.Errorf("error item mapping wrong: %+v", items[1])
	}
	if items[2]["summary"] != "ok2" {
		t.Errorf("success data not merged: %+v", items[2])
	}
}

func TestCloneForSiblingIsolation(t *testing.T) {
	base := &SubAgentTool{Chain: []string{"root"}, CurrentDepth: 1}
	a := base.cloneForSibling(0)
	b := base.cloneForSibling(1)
	if !a.NoSlotSave || !b.NoSlotSave {
		t.Errorf("siblings must disable slot save")
	}
	if a.spawnNonce == b.spawnNonce {
		t.Errorf("siblings must have unique spawnNonce")
	}
	a.Chain = append(a.Chain, "a-only")
	if len(b.Chain) != 1 {
		t.Errorf("clones must not share Chain backing array")
	}
	if len(base.Chain) != 1 {
		t.Errorf("base Chain must be untouched")
	}
}

func TestFirstNonEmpty(t *testing.T) {
	if firstNonEmpty("", "b", "c") != "b" {
		t.Errorf("firstNonEmpty should return first non-empty")
	}
	if firstNonEmpty("", "") != "" {
		t.Errorf("all-empty should return empty string")
	}
}

func TestBatchTaskPromptWithContext(t *testing.T) {
	b := batchTask{Prompt: "do it"}
	if got := b.promptWithContext(""); got != "do it" {
		t.Errorf("no-context prompt = %q", got)
	}
	if got := b.promptWithContext("shared"); got != "shared\n\n---\ndo it" {
		t.Errorf("with-context prompt = %q", got)
	}
}
