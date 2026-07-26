package tools

type Hunk struct {
	OldStart int
	OldCount int
	NewStart int
	NewCount int
	Lines    []string
}

type PatchFile struct {
	OldPath  string
	NewPath  string
	Hunks    []Hunk
	IsNew    bool
	IsDelete bool
}

func pickTargetPath(pf PatchFile) string {
	if pf.NewPath != "" && pf.NewPath != "/dev/null" {
		return pf.NewPath
	}
	if pf.OldPath != "" && pf.OldPath != "/dev/null" {
		return pf.OldPath
	}
	return ""
}

func errorResult(path string, err error) map[string]interface{} {
	return map[string]interface{}{
		"file":   path,
		"status": "error",
		"error":  err.Error(),
	}
}
