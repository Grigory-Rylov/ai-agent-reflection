package tools

import (
	"context"
	"encoding/json"
	"fmt"
)

// ============================================================
// Tool Interface — базовый интерфейс для всех инструментов
// ============================================================

// ToolResult представляет результат выполнения инструмента
type ToolResult struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
}

// Tool определяет интерфейс для всех инструментов
type Tool interface {
	// Name возвращает идентификатор инструмента
	Name() string

	// Description предоставляет описание инструмента для LLM
	Description() string

	// Schema возвращает JSON Schema для параметров
	Schema() map[string]interface{}

	// Execute выполняет инструмент с заданными параметрами
	Execute(ctx context.Context, inputs map[string]string) (ToolResult, error)
}

// ============================================================
// Tool Registry — реестр инструментов
// ============================================================

// Registry хранит и управляет доступными инструментами
type Registry struct {
	tools map[string]Tool
}

// NewRegistry создаёт новый реестр инструментов
func NewRegistry() *Registry {
	return &Registry{
		tools: make(map[string]Tool),
	}
}

// Register регистрирует инструмент в реестре
func (r *Registry) Register(tool Tool) {
	r.tools[tool.Name()] = tool
}

// Get возвращает инструмент по имени с поддержкой алиасов
func (r *Registry) Get(name string) (Tool, bool) {
	// Прямой поиск
	if tool, ok := r.tools[name]; ok {
		return tool, true
	}

	// Алиасы для совместимости с разными форматами вызова
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

// GetAll возвращает все зарегистрированные инструменты
func (r *Registry) GetAll() []Tool {
	result := make([]Tool, 0, len(r.tools))
	for _, tool := range r.tools {
		result = append(result, tool)
	}
	return result
}

// IsRegistered возвращает true если инструмент зарегистрирован
func (r *Registry) IsRegistered(name string) bool {
	_, ok := r.tools[name]
	return ok
}

// ToOpenAISchema конвертирует реестр в формат OpenAI function calling
func (r *Registry) ToOpenAISchema() []map[string]interface{} {
	schema := make([]map[string]interface{}, 0)

	for _, tool := range r.GetAll() {
		// Правильный формат OpenAI: {"type": "function", "function": {...}}
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

// ============================================================
// Утилиты для инструментов
// ============================================================

// CreateStringParameter создаёт параметр строкового типа
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

// CreateIntegerParameter создаёт параметр целочисленного типа
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

// CreateBooleanParameter создаёт параметр логического типа
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

// CreateEnumParameter создаёт параметр с ограниченным набором значений
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

// CreateObjectParameter создаёт параметр-объект (для вложенных данных)
func CreateObjectParameter(name, description string, properties map[string]interface{}, required bool) map[string]interface{} {
	param := map[string]interface{}{
		"type":        "object",
		"description": description,
		"properties":  properties,
	}
	if required {
		param["required"] = true
	}
	return param
}

// MarshalToolResult marshals ToolResult to JSON string
func MarshalToolResult(result ToolResult) string {
	data, err := json.Marshal(result)
	if err != nil {
		return fmt.Sprintf(`{"success": false, "error": "failed to marshal result: %v"}`, err)
	}
	return string(data)
}

// UnmarshalToolResult unmarshals JSON string to ToolResult
func UnmarshalToolResult(data string) (ToolResult, error) {
	var result ToolResult
	if err := json.Unmarshal([]byte(data), &result); err != nil {
		return ToolResult{}, fmt.Errorf("failed to unmarshal tool result: %w", err)
	}
	return result, nil
}
