package agentloop

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
)

type Scenario struct {
	Name          string
	Prompt        string
	Steps         []ScenarioStep
	AssertContent string
}

type ScenarioStep struct {
	Path    string
	Content string
}

func LoadScenarioDir(scenarioDir string) (*Scenario, error) {
	entries, err := os.ReadDir(scenarioDir)
	if err != nil {
		return nil, fmt.Errorf("read scenario dir %s: %w", scenarioDir, err)
	}

	s := &Scenario{
		Name: filepath.Base(scenarioDir),
	}

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		path := filepath.Join(scenarioDir, e.Name())

		switch e.Name() {
		case "prompt.txt":
			data, err := os.ReadFile(path)
			if err != nil {
				return nil, fmt.Errorf("read %s: %w", path, err)
			}
			s.Prompt = strings.TrimSpace(string(data))

		case "assert.txt":
			data, err := os.ReadFile(path)
			if err != nil {
				return nil, fmt.Errorf("read %s: %w", path, err)
			}
			s.AssertContent = strings.TrimSpace(string(data))

		default:
			if _, err := strconv.Atoi(e.Name()[:3]); err == nil {
				data, err := os.ReadFile(path)
				if err != nil {
					return nil, fmt.Errorf("read %s: %w", path, err)
				}
				s.Steps = append(s.Steps, ScenarioStep{
					Path:    e.Name(),
					Content: string(data),
				})
			}
		}
	}

	sort.Slice(s.Steps, func(i, j int) bool {
		return s.Steps[i].Path < s.Steps[j].Path
	})

	return s, nil
}

func (s *Scenario) MockServer() *httptest.Server {
	var mu sync.Mutex
	callIndex := 0

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		idx := callIndex
		callIndex++
		mu.Unlock()

		w.Header().Set("Content-Type", "text/event-stream")

		var content string
		if idx < len(s.Steps) {
			content = s.Steps[idx].Content
		} else {
			content = "Done."
		}

		escaped := jsonEscape(content)
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"%s\"}}]}\n\n", escaped)
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		fmt.Fprintf(w, "data: [DONE]\n")
	}))
}

// AssertResult проверяет результат по assert.txt:
// строки вида "contains: text" — result должен содержать text
// строки вида "not_contains: text" — result не должен содержать text
func (s *Scenario) AssertResult(t testing.TB, result string) {
	t.Helper()
	if s.AssertContent == "" {
		return
	}
	for _, line := range strings.Split(s.AssertContent, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		switch {
		case strings.HasPrefix(line, "contains: "):
			want := strings.TrimPrefix(line, "contains: ")
			if !strings.Contains(result, want) {
				t.Errorf("result should contain %q, got:\n%s", want, result)
			}
		case strings.HasPrefix(line, "not_contains: "):
			unwant := strings.TrimPrefix(line, "not_contains: ")
			if strings.Contains(result, unwant) {
				t.Errorf("result should NOT contain %q, got:\n%s", unwant, result)
			}
		}
	}
}

func jsonEscape(s string) string {
	data, _ := json.Marshal(s)
	return string(data[1 : len(data)-1])
}
