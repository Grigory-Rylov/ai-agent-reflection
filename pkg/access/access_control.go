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
	mu           sync.RWMutex
	allowedDirs  []string
	globalDirs   []string
	sessionPeers map[int64][]string
}


func NewController(allowedDirs []string) *Controller {
	c := &Controller{
		allowedDirs:  make([]string, 0),
		globalDirs:   make([]string, 0),
		sessionPeers: make(map[int64][]string),
	}
	for _, dir := range allowedDirs {
		c.addAllowedDir(dir)
	}
	return c
}


func (c *Controller) addAllowedDir(dir string) {
	canonical, err := resolveCanonical(dir)
	if err != nil {
		return
	}
	c.allowedDirs = appendUnique(c.allowedDirs, canonical)
}


func (c *Controller) AddAllowedDir(dir string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.addAllowedDir(dir)
}


func (c *Controller) GrantPath(path string) {
	c.grantTo(&c.globalDirs, path)
}


func (c *Controller) RevokePath(path string) {
	c.revokeFrom(&c.globalDirs, path)
}


func (c *Controller) grantTo(store *[]string, path string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	canonical, err := resolveCanonical(path)
	if err != nil {
		return
	}
	*store = appendUnique(*store, deepestExistingAncestor(canonical))
}


func (c *Controller) revokeFrom(store *[]string, path string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	canonical, err := resolveCanonical(path)
	if err != nil {
		return
	}
	target := deepestExistingAncestor(canonical)
	for i, d := range *store {
		if d == target {
			*store = append((*store)[:i], (*store)[i+1:]...)
			return
		}
	}
}


func (c *Controller) GrantPathForPeer(peerID int64, path string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	canonical, err := resolveCanonical(path)
	if err != nil {
		return
	}
	dir := deepestExistingAncestor(canonical)
	existing, ok := c.sessionPeers[peerID]
	if !ok {
		c.sessionPeers[peerID] = []string{dir}
		return
	}
	c.sessionPeers[peerID] = appendUnique(existing, dir)
}


func (c *Controller) RevokePathForPeer(peerID int64, path string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	canonical, err := resolveCanonical(path)
	if err != nil {
		return
	}
	target := deepestExistingAncestor(canonical)
	existing, ok := c.sessionPeers[peerID]
	if !ok {
		return
	}
	var kept []string
	for _, d := range existing {
		if d != target {
			kept = append(kept, d)
		}
	}
	c.sessionPeers[peerID] = kept
}


func (c *Controller) ClearPeer(peerID int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.sessionPeers, peerID)
}


func (c *Controller) CheckAccessForPeer(peerID int64, path string) AccessResult {
	c.mu.RLock()
	defer c.mu.RUnlock()

	canonical, err := resolveCanonical(path)
	if err != nil {
		return AccessResult{Allowed: false, Reason: err.Error()}
	}

	if isPathInAllowed(canonical, c.allowedDirs) {
		return AccessResult{Allowed: true, Reason: "path is within allowed directories"}
	}

	if isPathInAllowed(canonical, c.sessionPeers[peerID]) {
		return AccessResult{Allowed: true, Reason: "path is within peer-granted directories"}
	}

	all := c.allEffectiveDirs()
	return AccessResult{
		Allowed: false,
		Reason:  fmt.Sprintf("access denied: path %q is outside allowed directories %v", canonical, all),
	}
}


func (c *Controller) CheckAccess(path string) AccessResult {
	c.mu.RLock()
	defer c.mu.RUnlock()

	canonical, err := resolveCanonical(path)
	if err != nil {
		return AccessResult{Allowed: false, Reason: err.Error()}
	}

	if isPathInAllowed(canonical, c.allowedDirs) {
		return AccessResult{Allowed: true, Reason: "path is within allowed directories"}
	}

	if isPathInAllowed(canonical, c.globalDirs) {
		return AccessResult{Allowed: true, Reason: "path is within globally-granted directories"}
	}

	all := c.allEffectiveDirs()
	return AccessResult{
		Allowed: false,
		Reason:  fmt.Sprintf("access denied: path %q is outside allowed directories %v", canonical, all),
	}
}


func (c *Controller) allEffectiveDirs() []string {
	result := make([]string, 0, len(c.allowedDirs)+len(c.globalDirs))
	result = append(result, c.allowedDirs...)
	result = append(result, c.globalDirs...)
	return result
}


func (c *Controller) AllowedDirs() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.allEffectiveDirs()
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


func appendUnique(list []string, val string) []string {
	for _, v := range list {
		if v == val {
			return list
		}
	}
	return append(list, val)
}


func deepestExistingAncestor(p string) string {
	cur := p
	for {
		info, err := os.Stat(cur)
		if err == nil && info.IsDir() {
			return cur
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return cur
		}
		cur = parent
	}
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


func CanonicalPath(path string) (string, error) {
	return resolveCanonical(path)
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
