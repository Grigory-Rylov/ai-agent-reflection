package permission

import (
	"reflect"
	"testing"
)

func TestPrefix(t *testing.T) {
	tests := []struct {
		name   string
		tokens []string
		want   []string
	}{
		{
			name:   "arity 1 - unknown commands default to first token",
			tokens: []string{"unknown", "command", "subcommand"},
			want:   []string{"unknown"},
		},
		{
			name:   "arity 1 - explicitly listed command",
			tokens: []string{"touch", "foo.txt"},
			want:   []string{"touch"},
		},
		{
			name:   "arity 1 - echo",
			tokens: []string{"echo", "hello", "world"},
			want:   []string{"echo"},
		},
		{
			name:   "arity 2 - two token commands",
			tokens: []string{"git", "checkout", "main"},
			want:   []string{"git", "checkout"},
		},
		{
			name:   "arity 2 - docker",
			tokens: []string{"docker", "run", "nginx"},
			want:   []string{"docker", "run"},
		},
		{
			name:   "arity 2 - npm install",
			tokens: []string{"npm", "install", "lodash"},
			want:   []string{"npm", "install"},
		},
		{
			name:   "arity 3 - three token commands",
			tokens: []string{"aws", "s3", "ls", "my-bucket"},
			want:   []string{"aws", "s3", "ls"},
		},
		{
			name:   "arity 3 - npm run",
			tokens: []string{"npm", "run", "dev", "script"},
			want:   []string{"npm", "run", "dev"},
		},
		{
			name:   "arity 3 - gh pr",
			tokens: []string{"gh", "pr", "list", "--repo", "foo"},
			want:   []string{"gh", "pr", "list"},
		},
		{
			name:   "arity 3 - gcloud",
			tokens: []string{"gcloud", "compute", "instances", "list"},
			want:   []string{"gcloud", "compute", "instances"},
		},
		{
			name:   "longest match wins - nested prefixes",
			tokens: []string{"docker", "compose", "up", "service"},
			want:   []string{"docker", "compose", "up"},
		},
		{
			name:   "longest match wins - consul kv",
			tokens: []string{"consul", "kv", "get", "config"},
			want:   []string{"consul", "kv", "get"},
		},
		{
			name:   "longest match wins - ip addr",
			tokens: []string{"ip", "addr", "show"},
			want:   []string{"ip", "addr", "show"},
		},
		{
			name:   "longest match wins - kubectl rollout",
			tokens: []string{"kubectl", "rollout", "restart", "deploy/api"},
			want:   []string{"kubectl", "rollout", "restart"},
		},
		{
			name:   "longest match wins - terraform workspace",
			tokens: []string{"terraform", "workspace", "select", "prod"},
			want:   []string{"terraform", "workspace", "select"},
		},
		{
			name:   "exact length matches - git",
			tokens: []string{"git", "checkout"},
			want:   []string{"git", "checkout"},
		},
		{
			name:   "exact length matches - npm run",
			tokens: []string{"npm", "run", "dev"},
			want:   []string{"npm", "run", "dev"},
		},
		{
			name:   "edge - empty",
			tokens: nil,
			want:   []string{},
		},
		{
			name:   "edge - single unknown",
			tokens: []string{"single"},
			want:   []string{"single"},
		},
		{
			name:   "edge - git alone",
			tokens: []string{"git"},
			want:   []string{"git"},
		},
		{
			name:   "flags do not count - make",
			tokens: []string{"make", "build"},
			want:   []string{"make", "build"},
		},
		{
			name:   "python defaults to two tokens",
			tokens: []string{"python", "-m", "venv", "env"},
			want:   []string{"python", "-m"},
		},
		{
			name:   "go build",
			tokens: []string{"go", "build", "./..."},
			want:   []string{"go", "build"},
		},
		{
			name:   "kubectl get",
			tokens: []string{"kubectl", "get", "pods"},
			want:   []string{"kubectl", "get"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Prefix(tt.tokens)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Prefix(%v) = %v, want %v", tt.tokens, got, tt.want)
			}
		})
	}
}
