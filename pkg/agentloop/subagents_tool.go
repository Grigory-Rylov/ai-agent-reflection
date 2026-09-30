package agentloop

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/tools"
)

type SubagentsTool struct{}

func (t *SubagentsTool) Name() string {
	return "subagents"
}

func (t *SubagentsTool) Description() string {
	return "Manage background subagents launched by the task tool. action=list|jobs shows them; action=wait blocks for a job (or all) to finish and returns results; action=cancel aborts one by id."
}

func (t *SubagentsTool) Schema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action":          tools.CreateStringParameter("action", "One of: list, jobs, wait, cancel", true),
			"id":              tools.CreateStringParameter("id", "Job id (job-N) from a task result. For wait: omit to wait for all running jobs.", false),
			"timeout_seconds": tools.CreateStringParameter("timeout_seconds", "Max seconds to wait (default 600).", false),
		},
		"required": []string{"action"},
	}
}

func (t *SubagentsTool) Execute(ctx context.Context, inputs map[string]string) (tools.ToolResult, error) {
	action := strings.ToLower(strings.TrimSpace(inputs["action"]))
	switch action {
	case "list", "jobs":
		return t.jobs(), nil
	case "wait":
		return t.waitFor(inputs["id"], parseTimeout(inputs["timeout_seconds"]))
	case "cancel":
		return t.cancelJob(inputs["id"])
	case "":
		return tools.ToolResult{Success: false, Error: "action parameter is required (list, wait, cancel)"}, nil
	default:
		return tools.ToolResult{Success: false, Error: fmt.Sprintf("unknown action %q; use list, wait, or cancel", action)}, nil
	}
}

func parseTimeout(raw string) time.Duration {
	if n, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	return 600 * time.Second
}

func (t *SubagentsTool) jobs() tools.ToolResult {
	snap := subJobs.snapshot()
	items := make([]map[string]interface{}, 0, len(snap))
	for _, j := range snap {
		entry := map[string]interface{}{
			"id":          j.id,
			"agent":       j.name,
			"status":      string(j.status),
			"elapsed_sec": int(time.Since(j.createdAt).Seconds()),
		}
		if j.status == SubagentErrored {
			entry["error"] = j.result.Error
		} else if m, ok := j.result.Data.(map[string]interface{}); ok {
			if s, ok := m["summary"].(string); ok {
				entry["summary"] = s
			}
		}
		items = append(items, entry)
	}
	return tools.ToolResult{Success: true, Data: map[string]interface{}{
		"count": len(items),
		"jobs":  items,
	}}
}

func (t *SubagentsTool) waitFor(id string, timeout time.Duration) (tools.ToolResult, error) {
	if id = strings.TrimSpace(id); id != "" {
		res, ok := subJobs.wait(id, timeout)
		if !ok {
			return tools.ToolResult{Success: false, Error: fmt.Sprintf("timed out after %s waiting for job %s", timeout, id)}, nil
		}
		return res, nil
	}
	return t.waitAll(timeout)
}

func (t *SubagentsTool) waitAll(timeout time.Duration) (tools.ToolResult, error) {
	deadline := time.Now().Add(timeout)
	for {
		if !anyRunning() {
			break
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	return t.jobs(), nil
}

func anyRunning() bool {
	for _, j := range subJobs.snapshot() {
		if j.status == SubagentRunning {
			return true
		}
	}
	return false
}

func (t *SubagentsTool) cancelJob(id string) (tools.ToolResult, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return tools.ToolResult{Success: false, Error: "id parameter is required for cancel"}, nil
	}
	if subJobs.cancel(id) {
		return tools.ToolResult{Success: true, Data: map[string]interface{}{"cancelled": id}}, nil
	}
	return tools.ToolResult{Success: false, Error: fmt.Sprintf("no running job with id %q", id)}, nil
}
