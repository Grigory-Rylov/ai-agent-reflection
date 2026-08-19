package access

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)


type AccessLevel int

const (
	AccessRead AccessLevel = iota
	AccessWrite
	AccessExecute
	AccessAll
)


type AccessResult struct {
	Allowed bool
	Reason  string
}


type Controller struct {
	mu          sync.RWMutex
	allowedDirs []string 
	sessionDirs []string 
}


func NewController(allowedDirs []string) *Controller {
	c := &Controller{
		allowedDirs: make([]string, 0),
		sessionDirs: make([]string, 0),
	}
	for _, dir := range allowedDirs {
		c.addAllowedDir(dir)
	}
	return c
}


func (c *Controller) addAllowedDir(dir string) {
	dir = os.ExpandEnv(dir)
	absPath, err := filepath.Abs(dir)
	if err != nil {
		return
	}
	canonical, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		canonical = absPath
	}
	c.allowedDirs = append(c.allowedDirs, canonical)
}


func (c *Controller) AddAllowedDir(dir string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.addAllowedDir(dir)
}


func (c *Controller) GrantPath(path string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	path = os.ExpandEnv(path)
	absPath, err := filepath.Abs(path)
	if err != nil {
		return
	}
	canonical, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		canonical = absPath
	}
	c.sessionDirs = append(c.sessionDirs, canonical)
}


func (c *Controller) RevokePath(path string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	absPath, err := filepath.Abs(path)
	if err != nil {
		return
	}
	canonical, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		canonical = absPath
	}
	for i, d := range c.sessionDirs {
		if d == canonical {
			c.sessionDirs = append(c.sessionDirs[:i], c.sessionDirs[i+1:]...)
			return
		}
	}
}


func (c *Controller) AllowedDirs() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	result := make([]string, 0, len(c.allowedDirs)+len(c.sessionDirs))
	result = append(result, c.allowedDirs...)
	result = append(result, c.sessionDirs...)
	return result
}


func isPathInAllowed(canonical string, dirs []string) bool {
	for _, allowedDir := range dirs {
		if strings.HasPrefix(canonical, allowedDir) {
			afterPrefix := canonical[len(allowedDir):]
			if afterPrefix == "" || strings.HasPrefix(afterPrefix, string(filepath.Separator)) {
				return true
			}
		}
	}
	return false
}


func resolveCanonical(path string) (string, error) {
	path = os.ExpandEnv(path)
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("cannot resolve path: %w", err)
	}
	absPath = filepath.Clean(absPath)

	volumeName := filepath.VolumeName(absPath)
	dirPart := absPath[len(volumeName):]

	var resolved string
	if volumeName != "" {
		resolved = volumeName + string(filepath.Separator)
	}

	parts := strings.Split(dirPart, string(filepath.Separator))
	for _, part := range parts {
		if part == "" {
			if resolved == "" {
				resolved = string(filepath.Separator)
			}
			continue
		}
		resolved = filepath.Join(resolved, part)
		if eval, err := filepath.EvalSymlinks(resolved); err == nil {
			resolved = eval
		}
	}

	return filepath.Clean(resolved), nil
}


func (c *Controller) CheckAccess(path string) AccessResult {
	c.mu.RLock()
	defer c.mu.RUnlock()

	canonical, err := resolveCanonical(path)
	if err != nil {
		return AccessResult{
			Allowed: false,
			Reason:  err.Error(),
		}
	}

	if isPathInAllowed(canonical, c.allowedDirs) {
		return AccessResult{
			Allowed: true,
			Reason:  "path is within allowed directories",
		}
	}

	if isPathInAllowed(canonical, c.sessionDirs) {
		return AccessResult{
			Allowed: true,
			Reason:  "path is within session-granted directories",
		}
	}

	allowedDirs := append([]string{}, c.allowedDirs...)
	allowedDirs = append(allowedDirs, c.sessionDirs...)

	return AccessResult{
		Allowed: false,
		Reason:  fmt.Sprintf("access denied: path %q is outside allowed directories %v", canonical, allowedDirs),
	}
}


func (c *Controller) CheckReadAccess(path string) AccessResult {
	return c.CheckAccess(path)
}


func (c *Controller) CheckWriteAccess(path string) AccessResult {
	result := c.CheckAccess(path)
	if !result.Allowed {
		return result
	}

	parentDir := filepath.Dir(path)
	info, err := os.Stat(parentDir)
	if err != nil {
		if os.IsNotExist(err) {
			return AccessResult{
				Allowed: false,
				Reason:  fmt.Sprintf("parent directory does not exist: %s", parentDir),
			}
		}
		return AccessResult{
			Allowed: false,
			Reason:  fmt.Sprintf("cannot stat parent directory: %v", err),
		}
	}

	if !info.IsDir() {
		return AccessResult{
			Allowed: false,
			Reason:  "parent path is not a directory",
		}
	}

	return AccessResult{
		Allowed: true,
		Reason:  "path is within allowed directory",
	}
}


func (c *Controller) SafeReadFile(path string) ([]byte, AccessResult) {
	result := c.CheckAccess(path)
	if !result.Allowed {
		return nil, result
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, AccessResult{
			Allowed: false,
			Reason:  fmt.Sprintf("failed to read file: %v", err),
		}
	}

	return data, AccessResult{
		Allowed: true,
		Reason:  "file read successfully",
	}
}


func (c *Controller) SafeWriteFile(path string, data []byte) AccessResult {
	result := c.CheckWriteAccess(path)
	if !result.Allowed {
		return result
	}

	dir := filepath.Dir(path)
	tmpFile, err := os.CreateTemp(dir, ".write_temp_*")
	if err != nil {
		return AccessResult{
			Allowed: false,
			Reason:  fmt.Sprintf("failed to create temp file: %v", err),
		}
	}
	tmpName := tmpFile.Name()

	if _, err := tmpFile.Write(data); err != nil {
		tmpFile.Close()
		os.Remove(tmpName)
		return AccessResult{
			Allowed: false,
			Reason:  fmt.Sprintf("failed to write to temp file: %v", err),
		}
	}

	if err := tmpFile.Close(); err != nil {
		os.Remove(tmpName)
		return AccessResult{
			Allowed: false,
			Reason:  fmt.Sprintf("failed to close temp file: %v", err),
		}
	}

	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return AccessResult{
			Allowed: false,
			Reason:  fmt.Sprintf("failed to rename temp file: %v", err),
		}
	}

	return AccessResult{
		Allowed: true,
		Reason:  "file written successfully",
	}
}


func SanitizePath(path string) string {
	path = os.ExpandEnv(path)
	cleaned := filepath.Clean(path)
	if strings.Contains(cleaned, "..") {
		return ""
	}
	if !filepath.IsAbs(cleaned) {
		absPath, err := filepath.Abs(cleaned)
		if err == nil {
			cleaned = absPath
		}
	}
	return cleaned
}


func IsPathSafe(path string) bool {
	dangerousChars := []string{";", "|", "&", "`", "$", "(", ")", "{", "}", "<", ">", "\\", "\n", "\r"}
	for _, ch := range dangerousChars {
		if strings.Contains(path, ch) {
			return false
		}
	}
	cleaned := filepath.Clean(path)
	if strings.Contains(cleaned, "..") {
		return false
	}
	return true
}
