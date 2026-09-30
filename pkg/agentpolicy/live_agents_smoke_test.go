package agentpolicy

import (
	"path/filepath"
	"testing"
)

func TestDiscoverLiveAgentStore(t *testing.T) {
	got, err := DiscoverAgentFiles([]string{filepath.Join("..", "..", "agents")})
	if err != nil {
		t.Fatalf("discover live agents: %v", err)
	}
	am := NewAgentManager()
	am.LoadFromConfig(got)

	for _, name := range []string{"lead", "worker", "qa", "reviewer"} {
		if _, err := am.GetAgent(name); err != nil {
			t.Fatalf("agent %q not registered from files", name)
		}
	}

	qa, _ := am.GetAgent("qa")
	if !qa.Review || !qa.Leaf {
		t.Errorf("qa: review=%v leaf=%v, want true/true", qa.Review, qa.Leaf)
	}
	if len(qa.Prompt) == 0 {
		t.Errorf("qa prompt empty after frontmatter parse")
	}

	reviewer, _ := am.GetAgent("reviewer")
	if !reviewer.Review || !reviewer.Leaf {
		t.Errorf("reviewer: review=%v leaf=%v, want true/true", reviewer.Review, reviewer.Leaf)
	}
	if reviewer.Permission.GetAction("read") != "allow" {
		t.Errorf("reviewer read should be allowed")
	}
	if reviewer.Permission.GetAction("bash") != "deny" {
		t.Errorf("reviewer should be read-only; bash not denied")
	}
	if reviewer.Permission.GetAction("write") != "deny" {
		t.Errorf("reviewer should be read-only; write not denied")
	}

	lead, _ := am.GetAgent("lead")
	if !lead.Coordinator {
		t.Errorf("lead should be coordinator")
	}
	if len(lead.SubagentTypes) == 0 {
		t.Errorf("lead should declare subagentTypes")
	}
}
