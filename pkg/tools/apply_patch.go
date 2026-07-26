package tools

import "context"

type ApplyPatchTool struct{}

func (t *ApplyPatchTool) Name() string {
	return "apply_patch"
}

func (t *ApplyPatchTool) Description() string {
	return "Apply a unified diff patch to files. " +
		"Parses standard diff format (---/+++/@@ hunks) and applies changes. " +
		"Supports creating new files (--- /dev/null) and deleting files (+++ /dev/null)."
}

func (t *ApplyPatchTool) Schema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"patch": CreateStringParameter("patch",
				"The unified diff patch text to apply. "+
					"Format: --- old/file +++ new/file @@ -start,count +start,count @@ context -old +new", true),
		},
		"required": []string{"patch"},
	}
}

func (t *ApplyPatchTool) Execute(ctx context.Context, inputs map[string]string) (ToolResult, error) {
	patchText, ok := inputs["patch"]
	if !ok || patchText == "" {
		return ToolResult{Success: false, Error: "patch parameter is required"}, nil
	}

	files := ParsePatch(patchText)
	if len(files) == 0 {
		return ToolResult{
			Success: false,
			Error:   "No valid hunks found in patch",
		}, nil
	}

	var applied []map[string]interface{}
	for _, pf := range files {
		targetPath := pickTargetPath(pf)
		if targetPath == "" {
			continue
		}

		resolvedPath, err := resolvePath(targetPath)
		if err != nil {
			applied = append(applied, errorResult(targetPath, err))
			continue
		}

		result := applyFilePatch(resolvedPath, pf)
		applied = append(applied, result)
	}

	return ToolResult{
		Success: true,
		Data: map[string]interface{}{
			"applied": applied,
			"count":   len(applied),
		},
	}, nil
}
