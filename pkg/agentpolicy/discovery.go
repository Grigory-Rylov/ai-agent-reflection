package agentpolicy

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type agentFrontmatter struct {
	Name          string  `yaml:"name"`
	Description   string  `yaml:"description"`
	Mode          string  `yaml:"mode"`
	Model         string  `yaml:"model"`
	ThinkingLevel string  `yaml:"thinkingLevel"`
	Tools         StrList `yaml:"tools"`
	SubagentTypes StrList `yaml:"subagentTypes"`
	Spawns        StrList `yaml:"spawns"`
	Hidden        bool    `yaml:"hidden"`
	Internal      bool    `yaml:"internal"`
	Leaf          bool    `yaml:"leaf"`
	Review        bool    `yaml:"review"`
	Coordinator   bool    `yaml:"coordinator"`
	Blocking      bool    `yaml:"blocking"`
	OutputSchema  string  `yaml:"outputSchema"`
	RequestBudget int     `yaml:"requestBudget"`
	MaxRuntimeSec int     `yaml:"maxRuntimeSec"`
}
type StrList []string

func (s *StrList) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.SequenceNode:
		var items []string
		if err := node.Decode(&items); err != nil {
			return err
		}
		*s = items
	case yaml.ScalarNode:
		for _, part := range strings.Split(node.Value, ",") {
			if trimmed := strings.TrimSpace(part); trimmed != "" {
				*s = append(*s, trimmed)
			}
		}
	default:
		return fmt.Errorf("unexpected list node kind %d", node.Kind)
	}
	return nil
}

func splitFrontmatter(data string) (string, string, bool) {
	normalized := strings.ReplaceAll(data, "\r\n", "\n")
	if !strings.HasPrefix(normalized, "---\n") {
		return "", normalized, false
	}
	rest := normalized[4:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return "", normalized, false
	}
	body := rest[end+1:]
	if nl := strings.Index(body, "\n"); nl >= 0 {
		body = body[nl+1:]
	} else {
		body = ""
	}
	return rest[:end], body, true
}

func toolsToPermission(tools []string) Permission {
	if len(tools) == 0 {
		return nil
	}
	perm := Permission{"*": "deny"}
	for _, tool := range tools {
		perm[tool] = "allow"
	}
	return perm
}

func parseAgentMarkdown(path string) (string, AgentCfg, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", AgentCfg{}, fmt.Errorf("read agent file: %w", err)
	}
	stem := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	fmText, body, hasFM := splitFrontmatter(string(raw))
	if !hasFM {
		return stem, AgentCfg{Prompt: body}, nil
	}
	var fm agentFrontmatter
	if err := yaml.Unmarshal([]byte(fmText), &fm); err != nil {
		return "", AgentCfg{}, fmt.Errorf("parse frontmatter %s: %w", path, err)
	}
	name := stem
	if fm.Name != "" {
		name = fm.Name
	}
	subagentTypes := fm.SubagentTypes
	if len(subagentTypes) == 0 {
		subagentTypes = fm.Spawns
	}
	cfg := AgentCfg{
		Mode:          fm.Mode,
		Description:   fm.Description,
		Prompt:        body,
		Hidden:        fm.Hidden,
		Internal:      fm.Internal,
		Leaf:          fm.Leaf,
		Review:        fm.Review,
		Coordinator:   fm.Coordinator,
		SubagentTypes: subagentTypes,
		Tools:         fm.Tools,
		ThinkingLevel: fm.ThinkingLevel,
		Blocking:      fm.Blocking,
		OutputSchema:  fm.OutputSchema,
		RequestBudget: fm.RequestBudget,
		MaxRuntimeSec: fm.MaxRuntimeSec,
	}
	if perm := toolsToPermission(fm.Tools); perm != nil {
		cfg.Permission = perm
	}
	return name, cfg, nil
}

func DiscoverAgentFiles(dirs []string) (map[string]AgentCfg, error) {
	out := make(map[string]AgentCfg)
	var errs error
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".md" {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			name, cfg, err := parseAgentMarkdown(path)
			if err != nil {
				errs = errors.Join(errs, err)
				continue
			}
			out[name] = cfg
		}
	}
	return out, errs
}
