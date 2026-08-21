package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/tools"
)


type ServerConfig struct {
	Type     string            `json:"type"`              
	Command  string            `json:"command,omitempty"` 
	Args     []string          `json:"args,omitempty"`    
	Env      []string          `json:"env,omitempty"`     
	URL      string            `json:"url,omitempty"`     
	Headers  map[string]string `json:"headers,omitempty"` 
	Enabled  bool              `json:"enabled"`
	Timeout  int               `json:"timeout,omitempty"` 
}


type Settings struct {
	InitTimeout   int `json:"initTimeout,omitempty"`   
	ToolTimeout   int `json:"toolTimeout,omitempty"`   
	MaxConcurrent int `json:"maxConcurrent,omitempty"` 
}


type Config struct {
	Servers  map[string]ServerConfig `json:"mcpServers"`
	Settings Settings                `json:"settings,omitempty"`
}


type MCPTool struct {
	tool       mcp.Tool
	serverName string
	client     *client.Client
}


func NewMCPTool(tool mcp.Tool, serverName string, client *client.Client) *MCPTool {
	return &MCPTool{
		tool:       tool,
		serverName: serverName,
		client:     client,
	}
}


func (t *MCPTool) Name() string {
	return t.serverName + "_" + t.tool.Name
}


func (t *MCPTool) Description() string {
	desc := t.tool.Description
	if desc == "" {
		desc = fmt.Sprintf("Tool from MCP server '%s'", t.serverName)
		return desc
	}
	return fmt.Sprintf("%s [via MCP server: %s]", desc, t.serverName)
}


func (t *MCPTool) Schema() map[string]interface{} {
	
	data, err := t.tool.InputSchema.MarshalJSON()
	if err != nil {
		return map[string]interface{}{"type": "object", "additionalProperties": false}
	}
	var schema map[string]interface{}
	if err := json.Unmarshal(data, &schema); err != nil {
		return map[string]interface{}{"type": "object", "additionalProperties": false}
	}

	schema["type"] = "object"
	schema["additionalProperties"] = false
	if _, ok := schema["properties"]; !ok {
		schema["properties"] = map[string]interface{}{}
	}
	return schema
}


func (t *MCPTool) Execute(ctx context.Context, inputs map[string]string) (tools.ToolResult, error) {
	
	args := make(map[string]interface{})
	for k, v := range inputs {
		args[k] = v
	}

	result, err := t.client.CallTool(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      t.tool.Name,
			Arguments: args,
		},
	})
	if err != nil {
		return tools.ToolResult{Success: false, Error: err.Error()}, nil
	}

	if result.IsError {
		return tools.ToolResult{Success: false, Error: extractContent(result.Content)}, nil
	}

	return tools.ToolResult{Success: true, Data: extractContent(result.Content)}, nil
}


func extractContent(content []mcp.Content) string {
	var texts []string
	for _, c := range content {
		if textContent, ok := c.(mcp.TextContent); ok {
			texts = append(texts, textContent.Text)
		}
	}
	return fmt.Sprintf("%s", texts)
}


type Logger interface {
	InfoLogf(format string, args ...interface{})
	WarnLogf(format string, args ...interface{})
	DebugLogf(format string, args ...interface{})
}


type Manager struct {
	clients  map[string]*client.Client
	registry *tools.Registry
	config   *Config
	logger   Logger
}


func NewManager(registry *tools.Registry, logger Logger) *Manager {
	return &Manager{
		clients:  make(map[string]*client.Client),
		registry: registry,
		logger:   logger,
	}
}


func (m *Manager) LoadConfig(ctx context.Context, config *Config) error {
	m.config = config

	for name, serverConfig := range config.Servers {
		if !serverConfig.Enabled {
			if m.logger != nil {
				m.logger.InfoLogf("[MCP] Server '%s' is disabled, skipping", name)
			}
			continue
		}

		if err := m.AddServer(ctx, name, serverConfig); err != nil {
			if m.logger != nil {
				m.logger.WarnLogf("[MCP] Failed to initialize server '%s': %v", name, err)
			}
			
		}
	}

	return nil
}


func (m *Manager) AddServer(ctx context.Context, name string, config ServerConfig) error {
	var c *client.Client
	var err error

	switch config.Type {
	case "stdio":
		c, err = client.NewStdioMCPClient(config.Command, config.Env, config.Args...)
	case "sse":
		c, err = client.NewSSEMCPClient(config.URL)
	default:
		return fmt.Errorf("unknown transport type: %s", config.Type)
	}

	if err != nil {
		return fmt.Errorf("create client: %w", err)
	}

	
	initRequest := mcp.InitializeRequest{}
	initRequest.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initRequest.Params.ClientInfo = mcp.Implementation{
		Name:    "ai-agent",
		Version: "1.0.0",
	}

	if _, err := c.Initialize(ctx, initRequest); err != nil {
		c.Close()
		return fmt.Errorf("initialize: %w", err)
	}

	m.clients[name] = c

	
	if err := m.registerTools(name, c); err != nil {
		if m.logger != nil {
			m.logger.WarnLogf("[MCP] Failed to register tools for '%s': %v", name, err)
		}
	}

	if m.logger != nil {
		m.logger.InfoLogf("[MCP] Connected to server '%s'", name)
	}

	return nil
}


func (m *Manager) registerTools(serverName string, c *client.Client) error {
	result, err := c.ListTools(context.Background(), mcp.ListToolsRequest{})
	if err != nil {
		return fmt.Errorf("list tools: %w", err)
	}

	for _, tool := range result.Tools {
		mcpTool := NewMCPTool(tool, serverName, c)
		m.registry.Register(mcpTool)

		if m.logger != nil {
			m.logger.DebugLogf("[MCP] Registered tool '%s' from server '%s'", mcpTool.Name(), serverName)
		}
	}

	return nil
}


func (m *Manager) Close() error {
	for name, c := range m.clients {
		if err := c.Close(); err != nil {
			if m.logger != nil {
				m.logger.WarnLogf("[MCP] Error closing server '%s': %v", name, err)
			}
		}
	}
	m.clients = make(map[string]*client.Client)
	return nil
}


func (m *Manager) Stats() string {
	return fmt.Sprintf("MCP Servers: %d", len(m.clients))
}
