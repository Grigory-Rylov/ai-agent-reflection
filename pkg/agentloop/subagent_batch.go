package agentloop

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/tools"
)

type batchTask struct {
	SubagentType string `json:"subagent_type"`
	Name         string `json:"name"`
	Agent        string `json:"agent"`
	Prompt       string `json:"prompt"`
	Task         string `json:"task"`
	Description  string `json:"description"`
}

func (b batchTask) agentName() string {
	return firstNonEmpty(b.SubagentType, b.Name, b.Agent)
}

func (b batchTask) promptWithContext(globalContext string) string {
	p := firstNonEmpty(b.Prompt, b.Task, b.Description)
	if globalContext == "" {
		return p
	}
	return globalContext + "\n\n---\n" + p
}

func firstBadTask(tasks []batchTask) string {
	for i, it := range tasks {
		p := firstNonEmpty(it.Prompt, it.Task, it.Description)
		if p == "" {
			return fmt.Sprintf("tasks[%d]: empty prompt", i)
		}
		if looksLikePlaceholderTask(p) {
			return fmt.Sprintf("tasks[%d]: placeholder prompt", i)
		}
	}
	return ""
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func (t *SubAgentTool) maxConcurrency() int {
	if t.MaxParallel > 0 {
		return t.MaxParallel
	}
	return 4
}

func (t *SubAgentTool) cloneForSibling(index int) *SubAgentTool {
	sub := *t
	sub.Chain = append([]string(nil), t.Chain...)
	sub.AgentSessionID = ""
	sub.NoSlotSave = true
	sub.spawnNonce = fmt.Sprintf("b%d-%d", index, t.CurrentDepth)
	return &sub
}

func (t *SubAgentTool) runBatch(ctx context.Context, globalContext, rawTasks string) tools.ToolResult {
	var tasks []batchTask
	if err := json.Unmarshal([]byte(rawTasks), &tasks); err != nil {
		return tools.ToolResult{Success: false, Error: fmt.Sprintf("failed to parse tasks array: %v", err)}
	}
	if len(tasks) == 0 {
		return tools.ToolResult{Success: false, Error: "tasks array is empty"}
	}
	if bad := firstBadTask(tasks); bad != "" {
		return tools.ToolResult{Success: false, Error: bad}
	}
	results := fanOut(len(tasks), t.maxConcurrency(), func(i int) tools.ToolResult {
		item := tasks[i]
		spawn := t.cloneForSibling(i)
		return spawn.runSpawn(ctx, item.agentName(), item.promptWithContext(globalContext))
	})
	return aggregateBatchResults(results)
}

func fanOut(n, concurrency int, run func(int) tools.ToolResult) []tools.ToolResult {
	if concurrency < 1 {
		concurrency = 1
	}
	results := make([]tools.ToolResult, n)
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int) {
			defer wg.Done()
			defer func() { <-sem }()
			results[idx] = run(idx)
		}(i)
	}
	wg.Wait()
	return results
}

func aggregateBatchResults(results []tools.ToolResult) tools.ToolResult {
	items := make([]map[string]interface{}, 0, len(results))
	okCount := 0
	for i, r := range results {
		entry := map[string]interface{}{"index": i}
		if r.Success {
			okCount++
			entry["status"] = "success"
			if m, ok := r.Data.(map[string]interface{}); ok {
				for k, v := range m {
					entry[k] = v
				}
			}
		} else {
			entry["status"] = "error"
			entry["error"] = r.Error
		}
		items = append(items, entry)
	}
	return tools.ToolResult{Success: true, Data: map[string]interface{}{
		"count":   len(items),
		"ok":      okCount,
		"failed":  len(items) - okCount,
		"results": items,
	}}
}
