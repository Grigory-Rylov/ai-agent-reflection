package tools

import (
	"context"
	"encoding/json"
	"fmt"
)


type ToolResult struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
}


type Tool interface {
	
	Name() string

	
	Description() string

	
	Schema() map[string]interface{}

	
	Execute(ctx context.Context, inputs map[string]string) (ToolResult, error)
}


type Registry struct {
	tools map[string]Tool
}


func NewRegistry() *Registry {
	return &Registry{
		tools: make(map[string]Tool),
	}
}


func (r *Registry) Register(tool Tool) {
	r.tools[tool.Name()] = tool
}


func (r *Registry) Unregister(name string) {
	delete(r.tools, name)
}


func (r *Registry) Get(name string) (Tool, bool) {
	
	if tool, ok := r.tools[name]; ok {
		return tool, true
	}

	
	aliases := map[string]string{
		"read_file":    "file_read",
		"write_file":   "file_write",
		"list_dir":     "file_list",
		"list_files":   "file_list",
		"dir_list":     "file_list",
		"shell":        "shell_execute",
		"execute":      "shell_execute",
		"web_fetch":    "web_fetch",
		"fetch":        "web_fetch",
		"web_search":   "web_search",
		"search":       "web_search",
		"grep_search":  "grep",
		"find_files":   "glob",
		"calculate":    "calc",
		"edit_file":    "edit",
	}

	if alias, ok := aliases[name]; ok {
		tool, ok := r.tools[alias]
		return tool, ok
	}

	return nil, false
}


func (r *Registry) GetAll() []Tool {
	result := make([]Tool, 0, len(r.tools))
	for _, tool := range r.tools {
		result = append(result, tool)
	}
	return result
}


func (r *Registry) IsRegistered(name string) bool {
	_, ok := r.tools[name]
	return ok
}


func (r *Registry) ToOpenAISchema() []map[string]interface{} {
	schema := make([]map[string]interface{}, 0)

	for _, tool := range r.GetAll() {
		
		item := map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name":        tool.Name(),
				"description": tool.Description(),
				"parameters":  tool.Schema(),
			},
		}
		schema = append(schema, item)
	}

	return schema
}


func CreateStringParameter(name, description string, required bool) map[string]interface{} {
	param := map[string]interface{}{
		"type":        "string",
		"description": description,
	}
	if required {
		param["required"] = true
	}
	return param
}


func CreateIntegerParameter(name, description string, required bool) map[string]interface{} {
	param := map[string]interface{}{
		"type":        "integer",
		"description": description,
	}
	if required {
		param["required"] = true
	}
	return param
}


func CreateBooleanParameter(name, description string, required bool) map[string]interface{} {
	param := map[string]interface{}{
		"type":        "boolean",
		"description": description,
	}
	if required {
		param["required"] = true
	}
	return param
}


func CreateEnumParameter(name, description string, values []string, required bool) map[string]interface{} {
	param := map[string]interface{}{
		"type":        "string",
		"description": description,
		"enum":        values,
	}
	if required {
		param["required"] = true
	}
	return param
}


func MarshalToolResult(result ToolResult) string {
	data, err := json.Marshal(result)
	if err != nil {
		return fmt.Sprintf(`{"success": false, "error": "failed to marshal result: %v"}`, err)
	}
	return string(data)
}


func UnmarshalToolResult(data string) (ToolResult, error) {
	var result ToolResult
	if err := json.Unmarshal([]byte(data), &result); err != nil {
		return ToolResult{}, fmt.Errorf("failed to unmarshal tool result: %w", err)
	}
	return result, nil
}
