package tools

import (
	"context"
	"testing"
)

type MockTool struct {
	name        string
	description string
	schema      map[string]interface{}
	executeFunc func(ctx context.Context, inputs map[string]string) (ToolResult, error)
}

func (m *MockTool) Name() string {
	return m.name
}

func (m *MockTool) Description() string {
	return m.description
}

func (m *MockTool) Schema() map[string]interface{} {
	return m.schema
}

func (m *MockTool) Execute(ctx context.Context, inputs map[string]string) (ToolResult, error) {
	if m.executeFunc != nil {
		return m.executeFunc(ctx, inputs)
	}
	return ToolResult{Success: true, Data: "executed"}, nil
}

func TestNewRegistry(t *testing.T) {
	t.Run("creates empty registry", func(t *testing.T) {
		registry := NewRegistry()

		if registry == nil {
			t.Fatal("Registry should not be nil")
		}
		if len(registry.GetAll()) != 0 {
			t.Errorf("expected empty registry, got %d tools", len(registry.GetAll()))
		}
	})
}

func TestRegistryRegister(t *testing.T) {
	t.Run("registers tool successfully", func(t *testing.T) {
		registry := NewRegistry()
		tool := &MockTool{
			name:        "test_tool",
			description: "Test tool description",
			schema:      map[string]interface{}{},
		}

		registry.Register(tool)

		if !registry.IsRegistered("test_tool") {
			t.Error("expected tool to be registered")
		}
		if len(registry.GetAll()) != 1 {
			t.Errorf("expected 1 tool, got %d", len(registry.GetAll()))
		}
	})

	t.Run("overwrites existing tool", func(t *testing.T) {
		registry := NewRegistry()
		tool1 := &MockTool{name: "tool"}
		tool2 := &MockTool{name: "tool"}

		registry.Register(tool1)
		registry.Register(tool2)

		if len(registry.GetAll()) != 1 {
			t.Errorf("expected 1 tool (overwrite), got %d", len(registry.GetAll()))
		}
	})
}

func TestRegistryGet(t *testing.T) {
	t.Run("returns registered tool", func(t *testing.T) {
		registry := NewRegistry()
		tool := &MockTool{name: "test_tool"}
		registry.Register(tool)

		found, ok := registry.Get("test_tool")

		if !ok {
			t.Fatal("expected tool to be found")
		}
		if found != tool {
			t.Error("expected to get the same tool instance")
		}
	})

	t.Run("returns false for unregistered tool", func(t *testing.T) {
		registry := NewRegistry()

		_, ok := registry.Get("nonexistent")

		if ok {
			t.Error("expected tool not to be found")
		}
	})
}

func TestRegistryUnregister(t *testing.T) {
	t.Run("removes tool from registry", func(t *testing.T) {
		registry := NewRegistry()
		registry.Register(&MockTool{name: "test_tool"})
		if !registry.IsRegistered("test_tool") {
			t.Fatal("expected tool to be registered")
		}

		registry.Unregister("test_tool")

		if registry.IsRegistered("test_tool") {
			t.Error("expected tool to be unregistered")
		}
		if len(registry.GetAll()) != 0 {
			t.Errorf("expected 0 tools, got %d", len(registry.GetAll()))
		}
		if _, ok := registry.Get("test_tool"); ok {
			t.Error("expected Get to fail after unregister")
		}
	})

	t.Run("unregister missing tool is a no-op", func(t *testing.T) {
		registry := NewRegistry()
		registry.Register(&MockTool{name: "keep"})

		registry.Unregister("nonexistent")

		if !registry.IsRegistered("keep") {
			t.Error("expected other tools to remain")
		}
	})

	t.Run("re-register after unregister works", func(t *testing.T) {
		registry := NewRegistry()
		tool := &MockTool{name: "test_tool"}
		registry.Register(tool)
		registry.Unregister("test_tool")

		registry.Register(&MockTool{name: "test_tool"})
		if !registry.IsRegistered("test_tool") {
			t.Error("expected tool to be re-registered")
		}
	})
}

func TestRegistryGetAliases(t *testing.T) {
	t.Run("read_file alias finds read", func(t *testing.T) {
		registry := NewRegistry()
		registry.Register(&FileReadTool{})

		tool, ok := registry.Get("read_file")
		if !ok {
			t.Error("expected read_file alias to find read")
		}
		if tool.Name() != "read" {
			t.Errorf("expected read, got %q", tool.Name())
		}
	})

	t.Run("file_read alias finds read", func(t *testing.T) {
		registry := NewRegistry()
		registry.Register(&FileReadTool{})

		tool, ok := registry.Get("file_read")
		if !ok {
			t.Error("expected file_read alias to find read")
		}
		if tool.Name() != "read" {
			t.Errorf("expected read, got %q", tool.Name())
		}
	})

	t.Run("write_file alias finds write", func(t *testing.T) {
		registry := NewRegistry()
		registry.Register(&FileWriteTool{})

		tool, ok := registry.Get("write_file")
		if !ok {
			t.Error("expected write_file alias to find write")
		}
		if tool.Name() != "write" {
			t.Errorf("expected write, got %q", tool.Name())
		}
	})

	t.Run("write is canonical", func(t *testing.T) {
		registry := NewRegistry()
		registry.Register(&FileWriteTool{})

		tool, ok := registry.Get("write")
		if !ok {
			t.Error("expected write to be found")
		}
		if tool.Name() != "write" {
			t.Errorf("expected write, got %q", tool.Name())
		}
	})

	t.Run("read is canonical", func(t *testing.T) {
		registry := NewRegistry()
		registry.Register(&FileReadTool{})

		tool, ok := registry.Get("read")
		if !ok {
			t.Error("expected read to be found")
		}
		if tool.Name() != "read" {
			t.Errorf("expected read, got %q", tool.Name())
		}
	})

	t.Run("edit_file finds edit", func(t *testing.T) {
		registry := NewRegistry()
		registry.Register(&EditTool{})

		tool, ok := registry.Get("edit_file")
		if !ok {
			t.Error("expected edit_file alias to find edit")
		}
		if tool.Name() != "edit" {
			t.Errorf("expected edit, got %q", tool.Name())
		}
	})

	t.Run("list_dir finds file_list", func(t *testing.T) {
		registry := NewRegistry()
		registry.Register(&DirListTool{})

		tool, ok := registry.Get("list_dir")
		if !ok {
			t.Error("expected list_dir alias to find file_list")
		}
		if tool.Name() != "file_list" {
			t.Errorf("expected file_list, got %q", tool.Name())
		}
	})

	t.Run("file_list alias works", func(t *testing.T) {
		registry := NewRegistry()
		registry.Register(&DirListTool{})

		_, ok := registry.Get("file_list")
		if !ok {
			t.Error("expected file_list to be found")
		}
	})

	t.Run("shell alias finds bash", func(t *testing.T) {
		registry := NewRegistry()
		registry.Register(&ShellExecuteTool{})

		tool, ok := registry.Get("shell")
		if !ok {
			t.Fatal("expected shell alias to find bash")
		}
		if tool.Name() != "bash" {
			t.Errorf("expected bash, got %q", tool.Name())
		}
	})

	t.Run("shell_execute alias finds bash", func(t *testing.T) {
		registry := NewRegistry()
		registry.Register(&ShellExecuteTool{})

		tool, ok := registry.Get("shell_execute")
		if !ok {
			t.Fatal("expected shell_execute alias to find bash")
		}
		if tool.Name() != "bash" {
			t.Errorf("expected bash, got %q", tool.Name())
		}
	})

	t.Run("search_code alias finds grep", func(t *testing.T) {
		registry := NewRegistry()
		registry.Register(&GrepTool{})

		tool, ok := registry.Get("search_code")
		if !ok {
			t.Fatal("expected search_code alias to find grep")
		}
		if tool.Name() != "grep" {
			t.Errorf("expected grep, got %q", tool.Name())
		}
	})

	t.Run("question alias finds ask", func(t *testing.T) {
		registry := NewRegistry()
		registry.Register(&QuestionTool{})

		tool, ok := registry.Get("question")
		if !ok {
			t.Fatal("expected question alias to find ask")
		}
		if tool.Name() != "ask" {
			t.Errorf("expected ask, got %q", tool.Name())
		}
	})
}

func TestRegistryToOpenAISchema(t *testing.T) {
	t.Run("converts tools to OpenAI schema", func(t *testing.T) {
		registry := NewRegistry()
		tool := &MockTool{
			name:        "test_tool",
			description: "Test tool",
			schema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"input": map[string]interface{}{
						"type":        "string",
						"description": "Input parameter",
					},
				},
			},
		}
		registry.Register(tool)

		schema := registry.ToOpenAISchema()

		if len(schema) != 1 {
			t.Fatalf("expected 1 schema item, got %d", len(schema))
		}

		item := schema[0]
		if item["type"] != "function" {
			t.Errorf("expected type 'function', got '%v'", item["type"])
		}

		fn, ok := item["function"].(map[string]interface{})
		if !ok {
			t.Fatal("expected function key to be a map")
		}
		if fn["name"] != "test_tool" {
			t.Errorf("expected name 'test_tool', got '%v'", fn["name"])
		}
		if fn["description"] != "Test tool" {
			t.Errorf("expected description 'Test tool', got '%v'", fn["description"])
		}
	})
}

func TestCreateStringParameter(t *testing.T) {
	t.Run("creates string parameter", func(t *testing.T) {
		param := CreateStringParameter("input", "Input value", true)

		if param["type"] != "string" {
			t.Errorf("expected type 'string', got '%v'", param["type"])
		}
		if param["description"] != "Input value" {
			t.Errorf("expected description 'Input value', got '%v'", param["description"])
		}
		if param["required"] != true {
			t.Error("expected required to be true")
		}
	})
}

func TestCreateIntegerParameter(t *testing.T) {
	t.Run("creates integer parameter", func(t *testing.T) {
		param := CreateIntegerParameter("count", "Number of items", true)

		if param["type"] != "integer" {
			t.Errorf("expected type 'integer', got '%v'", param["type"])
		}
		if param["required"] != true {
			t.Error("expected required to be true")
		}
	})
}

func TestCreateBooleanParameter(t *testing.T) {
	t.Run("creates boolean parameter", func(t *testing.T) {
		param := CreateBooleanParameter("enabled", "Enable feature", false)

		if param["type"] != "boolean" {
			t.Errorf("expected type 'boolean', got '%v'", param["type"])
		}
		if param["required"] != nil {
			t.Error("expected required to be nil (optional)")
		}
	})
}

func TestCreateEnumParameter(t *testing.T) {
	t.Run("creates enum parameter", func(t *testing.T) {
		param := CreateEnumParameter("level", "Priority level", []string{"low", "medium", "high"}, true)

		if param["type"] != "string" {
			t.Errorf("expected type 'string', got '%v'", param["type"])
		}

		enum, ok := param["enum"].([]string)
		if !ok {
			t.Fatal("expected enum to be []string")
		}
		if len(enum) != 3 {
			t.Errorf("expected 3 enum values, got %d", len(enum))
		}
	})
}

func TestMarshalToolResult(t *testing.T) {
	t.Run("marshals successful result", func(t *testing.T) {
		result := ToolResult{
			Success: true,
			Data:    map[string]string{"key": "value"},
		}

		jsonStr := MarshalToolResult(result)

		if jsonStr == "" {
			t.Error("expected non-empty JSON string")
		}

		parsed, err := UnmarshalToolResult(jsonStr)
		if err != nil {
			t.Fatalf("failed to unmarshal: %v", err)
		}
		if !parsed.Success {
			t.Error("expected success to be true")
		}
	})

	t.Run("marshals error result", func(t *testing.T) {
		result := ToolResult{
			Success: false,
			Error:   "something went wrong",
		}

		jsonStr := MarshalToolResult(result)

		parsed, _ := UnmarshalToolResult(jsonStr)
		if parsed.Success {
			t.Error("expected success to be false")
		}
		if parsed.Error != "something went wrong" {
			t.Errorf("expected error message, got '%s'", parsed.Error)
		}
	})
}
