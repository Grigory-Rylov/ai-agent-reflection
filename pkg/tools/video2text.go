package tools

import (
	"context"
	"fmt"
	"os"
)

type Video2TextTool struct{}

func (t *Video2TextTool) Name() string {
	return "video2text"
}

func (t *Video2TextTool) Description() string {
	return "Load a video file and use the multimodal model to recognize and describe its content. Returns a description of what happens in the video."
}

func (t *Video2TextTool) Schema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"path":   CreateStringParameter("path", "The video file path to analyze (absolute or relative)", true),
			"prompt": CreateStringParameter("prompt", "Optional instruction for what to look for in the video (default: describe the video content in detail)", false),
		},
		"required": []string{"path"},
	}
}

func (t *Video2TextTool) Execute(ctx context.Context, inputs map[string]string) (ToolResult, error) {
	path, ok := inputs["path"]
	if !ok || path == "" {
		return ToolResult{Success: false, Error: "path parameter is required"}, nil
	}

	resolvedPath, err := resolveReadPath(path)
	if err != nil {
		return ToolResult{Success: false, Error: fmt.Sprintf("Invalid path: %v", err)}, nil
	}

	data, err := os.ReadFile(resolvedPath)
	if err != nil {
		return ToolResult{Success: false, Error: fmt.Sprintf("Failed to read video: %v", err)}, nil
	}

	prompt := inputs["prompt"]
	if prompt == "" {
		prompt = "Describe the video content in detail."
	}

	text, err := recognizeMedia(ctx, resolvedPath, data, videoMimeType(resolvedPath), prompt)
	if err != nil {
		return ToolResult{Success: false, Error: err.Error()}, nil
	}

	return ToolResult{
		Success: true,
		Data: map[string]interface{}{
			"path":    resolvedPath,
			"content": text,
		},
	}, nil
}
