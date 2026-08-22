package tools

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/access"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/permission"
)

var globalAccessController *access.Controller

func SetAccessController(ctrl *access.Controller) {
	globalAccessController = ctrl
}

func GetAccessController() *access.Controller {
	return globalAccessController
}

func CheckPathAllowed(resolvedPath string) error {
	if globalAccessController == nil {
		return nil
	}
	result := globalAccessController.CheckAccess(resolvedPath)
	if !result.Allowed {
		allowedDirs := globalAccessController.AllowedDirs()
		return fmt.Errorf(
			"access denied: you do not have permission to access %q. "+
				"Allowed directories: %v. "+
				"You can only work with files inside these directories. "+
				"If you need access to a different directory, ask the user to grant it.",
			resolvedPath, formatDirs(allowedDirs),
		)
	}
	return nil
}

func formatDirs(dirs []string) string {
	if len(dirs) == 0 {
		return "none"
	}
	quoted := make([]string, len(dirs))
	for i, d := range dirs {
		quoted[i] = fmt.Sprintf("%q", d)
	}
	return strings.Join(quoted, ", ")
}

type FileToolKind int

const (
	ToolRead  FileToolKind = iota
	ToolWrite
)

func FileToolPaths(toolName string, args map[string]string) []string {
	switch toolName {
	case "file_read", "read_file":
		if p, ok := args["path"]; ok && p != "" {
			return []string{p}
		}
		return []string{"."}
	case "file_write", "write_file":
		if p, ok := args["path"]; ok && p != "" {
			return []string{p}
		}
		return []string{"."}
	case "edit", "edit_file":
		if p, ok := args["path"]; ok && p != "" {
			return []string{p}
		}
		return []string{"."}
	case "file_list", "list_dir", "dir_list":
		if p, ok := args["path"]; ok && p != "" {
			return []string{p}
		}
		return []string{"."}
	case "glob", "find_files":
		var paths []string
		if p, ok := args["path"]; ok && p != "" {
			paths = append(paths, p)
		}
		return paths
	case "search_code", "grep", "grep_search":
		if p, ok := args["path"]; ok && p != "" {
			return []string{p}
		}
		return []string{"."}
	case "shell_execute", "shell":
		return nil
	}
	return nil
}

var fileCommands = map[string]bool{
	"cat": true, "less": true, "more": true, "head": true, "tail": true,
	"ls": true, "find": true,
	"cp": true, "mv": true, "rm": true,
	"mkdir": true, "touch": true, "chmod": true, "chown": true,
	"grep": true, "sed": true, "awk": true,
	"diff": true, "patch": true,
	"git": true,
}

func isAbsolutePath(s string) bool {
	return len(s) > 0 && s[0] == '/'
}

func looksLikePath(token string) bool {
	token = stripOuterQuotes(token)
	if strings.HasPrefix(token, "-") {
		return false
	}
	if len(token) > 0 && (token[0] == '\'' || token[0] == '"' || token[0] == '`') {
		return false
	}
	if strings.ContainsAny(token, " \t\r\n") {
		return false
	}
	if strings.ContainsAny(token, "?*[]!") {
		return false
	}
	if isAbsolutePath(token) {
		return true
	}
	if strings.HasPrefix(token, "http://") || strings.HasPrefix(token, "https://") {
		return false
	}
	if strings.HasPrefix(token, "~") {
		return true
	}
	if token == ".." {
		return true
	}
	if strings.Contains(token, ".") && !strings.Contains(token, "@") {
		parts := strings.Split(token, ".")
		allNumeric := true
		for _, p := range parts {
			isNum := true
			for _, c := range p {
				if c < '0' || c > '9' {
					isNum = false
					break
				}
			}
			if isNum {
				continue
			}
			allNumeric = false
		}
		if allNumeric {
			return false
		}
		return true
	}
	if strings.Contains(token, "@") {
		return false
	}
	return false
}

func ExtractShellPaths(command string) []string {
	if command == "" {
		return nil
	}

	parts := permission.Tokenize(command)
	if len(parts) == 0 {
		return nil
	}

	cmd := parts[0]
	if slashIdx := strings.LastIndex(cmd, "/"); slashIdx >= 0 {
		cmd = cmd[slashIdx+1:]
	}
	if !fileCommands[cmd] {
		return nil
	}

	var paths []string
	for _, token := range parts[1:] {
		if looksLikePath(token) {
			paths = append(paths, token)
		}
	}
	return paths
}

func PathsAllAllowed(paths []string) bool {
	ctrl := GetAccessController()
	if ctrl == nil {
		return true
	}
	for _, p := range paths {
		resolved, err := resolvePath(p)
		if err != nil {
			return false
		}
		if err := CheckPathAllowed(resolved); err != nil {
			return false
		}
	}
	return true
}

func ShellPathsAllAllowed(paths []string) bool {
	return PathsAllAllowed(paths)
}

var cwdShellCommands = map[string]bool{
	"cd": true, "chdir": true, "popd": true, "pushd": true,
}

func ShellCommandPathsAllowed(command string) bool {
	if command == "" {
		return true
	}
	subs := permission.SplitCommands(command)
	devPaths := collectDevicePaths(subs)
	for _, sub := range subs {
		if !shellSubcommandPathsAllowed(sub, devPaths) {
			return false
		}
	}
	return true
}

func shellSubcommandPathsAllowed(sub string, devPaths map[string]bool) bool {
	parts := permission.Tokenize(sub)
	if len(parts) == 0 {
		return true
	}
	rest, cmd := commandParts(parts)
	if cwdShellCommands[cmd] {
		return cwdTargetAllowed(rest)
	}
	paths := collectFilePaths(sub, devPaths)
	if len(paths) > 0 {
		return PathsAllAllowed(paths)
	}
	return fileCommands[cmd]
}

func commandParts(parts []string) ([]string, string) {
	i := 0
	for i < len(parts) && isEnvAssignment(parts[i]) {
		i++
	}
	rest := parts[i:]
	if len(rest) == 0 {
		return parts, ""
	}
	cmd := rest[0]
	if slashIdx := strings.LastIndex(cmd, "/"); slashIdx >= 0 {
		cmd = cmd[slashIdx+1:]
	}
	return rest, cmd
}

func isEnvAssignment(token string) bool {
	eq := strings.IndexByte(token, '=')
	if eq <= 0 || strings.HasPrefix(token, "-") {
		return false
	}
	return !strings.Contains(token[:eq], "/")
}

var wrapperCommands = map[string]bool{
	"nohup": true, "time": true, "env": true, "sudo": true,
	"xargs": true, "nice": true, "ionice": true, "stdbuf": true,
	"setsid": true, "timeout": true, "command": true, "exec": true,
}

func wrapperInterpreterToken(rest []string, cmd string) string {
	if !wrapperCommands[cmd] {
		return ""
	}
	i := 1
	for i < len(rest) && (isEnvAssignment(rest[i]) || strings.HasPrefix(rest[i], "-")) {
		i++
	}
	if i >= len(rest) {
		return ""
	}
	prog := rest[i]
	if isKnownInterpreter(prog) {
		return prog
	}
	return ""
}

var knownInterpreterBasenames = map[string]bool{
	"bash": true, "sh": true, "dash": true, "zsh": true, "ksh": true,
	"python": true, "python2": true, "python3": true, "perl": true,
	"ruby": true, "node": true, "php": true, "lua": true, "pwsh": true,
}

func isKnownInterpreter(token string) bool {
	name := token
	if idx := strings.LastIndex(name, "/"); idx >= 0 {
		name = name[idx+1:]
	}
	return knownInterpreterBasenames[name]
}

func stripOuterQuotes(token string) string {
	if len(token) >= 2 {
		if (token[0] == '"' && token[len(token)-1] == '"') || (token[0] == '\'' && token[len(token)-1] == '\'') {
			return token[1 : len(token)-1]
		}
	}
	return token
}

func isExplicitPathToken(token string) bool {
	token = stripOuterQuotes(token)
	if strings.HasPrefix(token, "-") {
		return false
	}
	return isAbsolutePath(token) || strings.HasPrefix(token, "~") || token == ".."
}

func isDiscardPath(p string) bool {
	return p == "/dev/null"
}

func maskHeredocBodies(command string) string {
	if command == "" {
		return command
	}
	lines := strings.SplitAfter(command, "\n")
	out := make([]string, 0, len(lines))
	openDelims := make(map[string]int)
	flushOpen := func() {}
	for _, raw := range lines {
		line := strings.TrimSuffix(raw, "\n")
		trimmed := strings.TrimLeft(line, " \t")
		matched := ""
		for delim := range openDelims {
			if trimmed == delim {
				delete(openDelims, delim)
				matched = delim
				break
			}
		}
		if matched != "" {
			out = append(out, line+"\n")
			continue
		}
		out = append(out, line+"\n")
		if opener := findLineHeredocOpener(trimmed); opener != "" {
			openDelims[opener] = len(out)
		}
	}
	result := strings.Join(out, "")
	for _, idx := range openDelims {
		if idx < len(out) {
			out[idx] = ""
		}
	}
	result = strings.Join(out, "")
	_ = flushOpen
	return result
}

func findLineHeredocOpener(line string) string {
	idx := strings.Index(line, "<<")
	if idx < 0 {
		return ""
	}
	rest := line[idx+2:]
	rest = strings.TrimPrefix(rest, "-")
	start := 0
	for start < len(rest) && (rest[start] == ' ' || rest[start] == '\t') {
		start++
	}
	end := start
	for end < len(rest) && rest[end] != ' ' && rest[end] != '\t' && rest[end] != '\n' {
		end++
	}
	if end == start {
		return ""
	}
	delim := rest[start:end]
	delim = strings.Trim(delim, "'\"")
	return delim
}

var nestedSubRe = regexp.MustCompile(`\$\(([^)]+)\)`)

func collectFilePaths(sub string, devPaths map[string]bool) []string {
	parts := permission.Tokenize(sub)
	if len(parts) == 0 {
		return nil
	}
	if hostPaths, remote := remoteHostPaths(sub); remote {
		for _, p := range redirectionTargets(sub) {
			if !devPaths[p] && !isDiscardPath(p) {
				hostPaths = append(hostPaths, p)
			}
		}
		return hostPaths
	}
	masked := maskHeredocBodies(sub)
	var paths []string
	for _, p := range ExtractShellPaths(masked) {
		if !devPaths[p] && !isDiscardPath(p) {
			paths = append(paths, p)
		}
	}
	for _, p := range redirectionTargets(masked) {
		if !devPaths[p] && !isDiscardPath(p) {
			paths = append(paths, p)
		}
	}
	rest, cmd := commandParts(parts)
	skipInterpreter := wrapperInterpreterToken(rest, cmd)
	for _, tok := range rest[1:] {
		if tok == skipInterpreter {
			continue
		}
		if isExplicitPathToken(tok) && !devPaths[tok] && !isDiscardPath(tok) {
			paths = append(paths, tok)
		}
	}

	for _, m := range nestedSubRe.FindAllStringSubmatch(masked, -1) {
		inner := m[1]
		innerDev := collectDevicePaths(permission.SplitCommands(inner))
		for _, p := range collectFilePaths(inner, innerDev) {
			if !devPaths[p] && !isDiscardPath(p) {
				paths = append(paths, p)
			}
		}
	}
	return paths
}

func remoteHostPaths(sub string) ([]string, bool) {
	parts := permission.Tokenize(sub)
	if len(parts) == 0 {
		return nil, false
	}
	cmd := parts[0]
	if slashIdx := strings.LastIndex(cmd, "/"); slashIdx >= 0 {
		cmd = cmd[slashIdx+1:]
	}
	switch cmd {
	case "adb":
		return adbHostPaths(parts), true
	case "ssh":
		return sshHostPaths(parts), true
	case "scp":
		return scpHostPaths(parts), true
	}
	return nil, false
}

func adbVerbIndex(parts []string) (int, []string) {
	for i := 1; i < len(parts); i++ {
		switch parts[i] {
		case "-s", "-H", "-P", "-t":
			i++
		default:
			if strings.HasPrefix(parts[i], "-") {
				continue
			}
			return i, parts[i+1:]
		}
	}
	return -1, nil
}

func adbHostPaths(parts []string) []string {
	verbIdx, rest := adbVerbIndex(parts)
	if verbIdx < 0 || len(rest) == 0 {
		return []string{}
	}
	switch parts[verbIdx] {
	case "push", "install", "sideload":
		return []string{rest[0]}
	case "pull":
		for i := len(rest) - 1; i >= 0; i-- {
			if isPathArgToken(rest[i]) {
				return []string{rest[i]}
			}
		}
		return []string{}
	}
	return []string{}
}

func isPathArgToken(tok string) bool {
	if isShellOperatorToken(tok) {
		return false
	}
	if idx := strings.IndexAny(tok, "><"); idx >= 0 && idx != len(tok)-1 {
		return false
	}
	return looksLikePath(tok) || isExplicitPathToken(tok)
}

func sshHostPaths(parts []string) []string {
	var host []string
	for i := 1; i < len(parts); i++ {
		if parts[i] == "-i" {
			if i+1 < len(parts) {
				host = append(host, parts[i+1])
			}
			i++
			continue
		}
		if strings.HasPrefix(parts[i], "-") {
			continue
		}
		break
	}
	return host
}

func scpHostPaths(parts []string) []string {
	var local []string
	for _, tok := range parts[1:] {
		if strings.HasPrefix(tok, "-") || isRemoteSCPToken(tok) {
			continue
		}
		if looksLikePath(tok) {
			local = append(local, tok)
		}
	}
	return local
}

func isRemoteSCPToken(tok string) bool {
	idx := strings.IndexByte(tok, ':')
	if idx <= 0 {
		return false
	}
	slash := strings.IndexByte(tok, '/')
	return slash < 0 || idx < slash
}

func collectDevicePaths(subs []string) map[string]bool {
	dev := make(map[string]bool)
	for _, sub := range subs {
		for _, p := range devicePathsIn(sub) {
			dev[p] = true
		}
	}
	return dev
}

func devicePathsIn(sub string) []string {
	parts := permission.Tokenize(sub)
	if len(parts) == 0 {
		return nil
	}
	cmd := parts[0]
	if slashIdx := strings.LastIndex(cmd, "/"); slashIdx >= 0 {
		cmd = cmd[slashIdx+1:]
	}
	switch cmd {
	case "adb":
		return adbDevicePaths(parts)
	case "ssh":
		return sshRemotePaths(parts)
	case "scp":
		return scpRemotePaths(parts)
	}
	return nil
}

func adbDevicePaths(parts []string) []string {
	verbIdx, rest := adbVerbIndex(parts)
	if verbIdx < 0 {
		return nil
	}
	switch parts[verbIdx] {
	case "shell", "exec-out", "exec-in":
		return pathLikeTokens(rest)
	case "push":
		if len(rest) > 1 {
			return []string{rest[len(rest)-1]}
		}
	case "pull":
		if len(rest) > 1 {
			return []string{rest[0]}
		}
	}
	return nil
}

func sshRemotePaths(parts []string) []string {
	var out []string
	for i := 1; i < len(parts); i++ {
		tok := parts[i]
		if tok == "-i" || tok == "-p" || tok == "-o" || tok == "-L" || tok == "-R" || tok == "-D" {
			i++
			continue
		}
		if strings.HasPrefix(tok, "-") {
			continue
		}
		out = append(out, pathLikeTokens(parts[i+1:])...)
		break
	}
	return out
}

func scpRemotePaths(parts []string) []string {
	var out []string
	for _, tok := range parts[1:] {
		if strings.HasPrefix(tok, "-") {
			continue
		}
		if isRemoteSCPToken(tok) {
			if idx := strings.IndexByte(tok, ':'); idx >= 0 {
				out = append(out, tok[idx+1:])
			}
		}
	}
	return out
}

func pathLikeTokens(tokens []string) []string {
	var out []string
	for _, tok := range tokens {
		if isShellOperatorToken(tok) {
			break
		}
		if strings.HasPrefix(tok, "-") {
			continue
		}
		if isExplicitPathToken(tok) || looksLikePath(tok) {
			out = append(out, tok)
		}
	}
	return out
}

func isShellOperatorToken(tok string) bool {
	if strings.HasPrefix(tok, ">") || strings.HasPrefix(tok, "<") {
		return true
	}
	switch tok {
	case "|", "||", "&&", ";", "&", "(", ")":
		return true
	}
	return false
}

func cwdTargetAllowed(parts []string) bool {
	if parts[0] == "popd" {
		return false
	}
	if len(parts) < 2 {
		return false
	}
	return PathsAllAllowed(parts[1:2])
}

func redirectionTargets(sub string) []string {
	var targets []string
	tokens := permission.Tokenize(sub)
	for i := 0; i < len(tokens); i++ {
		opPos := unquotedRedirOperatorAt(tokens[i])
		if opPos < 0 {
			continue
		}
		target := strings.TrimLeft(tokens[i][opPos+1:], "&0123456789")
		if target != "" && looksLikePath(target) {
			targets = append(targets, target)
			continue
		}
		if opPos == len(tokens[i])-1 && i+1 < len(tokens) && looksLikePath(tokens[i+1]) {
			targets = append(targets, tokens[i+1])
			i++
		}
	}
	return targets
}

func unquotedRedirOperatorAt(token string) int {
	inSingle, inDouble := false, false
	lastUnquoted := -1
	for i := 0; i < len(token); i++ {
		ch := token[i]
		switch {
		case inSingle:
			if ch == '\'' {
				inSingle = false
			}
		case inDouble:
			if ch == '"' {
				inDouble = false
			}
		case ch == '\'':
			inSingle = true
		case ch == '"':
			inDouble = true
		case ch == '>' || ch == '<':
			lastUnquoted = i
		}
	}
	return lastUnquoted
}

func ShellCommandHasFilePaths(command string) bool {
	if command == "" {
		return false
	}
	subs := permission.SplitCommands(command)
	devPaths := collectDevicePaths(subs)
	for _, sub := range subs {
		if shellSubcommandHasFilePaths(sub, devPaths) {
			return true
		}
	}
	return false
}

func shellSubcommandHasFilePaths(sub string, devPaths map[string]bool) bool {
	return len(collectFilePaths(sub, devPaths)) > 0
}

var readOnlyCommands = map[string]bool{
	"ls": true, "cat": true, "head": true, "tail": true,
	"less": true, "more": true, "wc": true,
	"which": true, "whereis": true, "type": true,
	"file": true, "stat": true, "readlink": true,
	"grep": true, "egrep": true, "fgrep": true, "rg": true, "ag": true,
	"diff": true, "cmp": true, "md5sum": true, "sha1sum": true, "sha256sum": true,
	"tr": true, "cut": true, "sort": true, "uniq": true, "xargs": true,
	"echo": true, "printf": true, "pwd": true, "date": true, "env": true,
	"export": true, "unset": true,
	"ps": true, "top": true, "free": true, "df": true, "du": true, "uptime": true, "uname": true,
	"find": true,
	"go": true,
}

var findMutatingFlags = map[string]bool{
	"-delete": true, "-exec": true, "-execdir": true,
	"-ok": true, "-okdir": true,
	"-fprint": true, "-fprintf": true, "-fls": true,
}

func isFindExecFlag(tok string) bool {
	return tok == "-exec" || tok == "-execdir"
}

func IsReadOnlySubcommand(sub string) bool {
	parts := permission.Tokenize(sub)
	if len(parts) == 0 {
		return false
	}
	_, cmd := commandParts(parts)
	if !readOnlyCommands[cmd] {
		return false
	}
	for _, tok := range parts[1:] {
		tok = strings.TrimRight(tok, ";")
		if findMutatingFlags[tok] {
			return false
		}
		if isFindExecFlag(tok) {
			return false
		}
	}
	if cmd == "head" || cmd == "tail" {
		return headTailSafe(parts)
	}
	return true
}

func headTailSafe(parts []string) bool {
	for _, tok := range parts[1:] {
		if strings.HasPrefix(tok, "-") {
			continue
		}
		if isAbsolutePath(tok) || strings.HasPrefix(tok, "~") || tok == ".." {
			return false
		}
	}
	return true
}

func ShellCommandFilesystemSafe(command string) bool {
	if command == "" {
		return true
	}
	subs := permission.SplitCommands(command)
	devPaths := collectDevicePaths(subs)
	for _, sub := range subs {
		if !isSubcommandSafe(sub, devPaths) {
			return false
		}
	}
	return true
}

func isSubcommandSafe(sub string, devPaths map[string]bool) bool {
	parts := permission.Tokenize(sub)
	if len(parts) == 0 {
		return true
	}
	rest, _ := commandParts(parts)
	if isAbsolutePath(rest[0]) {
		return absoluteProgramSafe(rest)
	}
	for _, target := range redirectionTargets(sub) {
		if !devPaths[target] && !isDiscardPath(target) {
			if !PathsAllAllowed([]string{target}) {
				return false
			}
		}
	}
	if !IsReadOnlySubcommand(sub) {
		paths := collectFilePaths(sub, devPaths)
		if len(paths) > 0 && !PathsAllAllowed(paths) {
			return false
		}
	}
	envPaths := envAssignmentPaths(parts)
	envPaths = append(envPaths, exportStatementPaths(rest)...)
	if len(envPaths) > 0 && !PathsAllAllowed(envPaths) {
		return false
	}
	return true
}

func absoluteProgramSafe(rest []string) bool {
	base := filepath.Base(rest[0])
	if !readOnlyCommands[base] {
		return false
	}
	sub := strings.Join(rest, " ")
	if !IsReadOnlySubcommand(sub) {
		return false
	}
	if base == "go" {
		for _, arg := range rest[1:] {
			if goWritingVerbs[arg] {
				return false
			}
		}
	}
	for _, target := range redirectionTargets(sub) {
		if !isDiscardPath(target) && !PathsAllAllowed([]string{target}) {
			return false
		}
	}
	for _, token := range rest[1:] {
		if isAbsolutePath(token) && !PathsAllAllowed([]string{token}) {
			return false
		}
	}
	return true
}

var goWritingVerbs = map[string]bool{
	"install": true, "publish": true, "get": true, "reset": true, "clean": true,
}

func exportStatementPaths(rest []string) []string {
	cmd := rest[0]
	base := cmd
	if idx := strings.LastIndex(base, "/"); idx >= 0 {
		base = base[idx+1:]
	}
	if base != "export" {
		return nil
	}
	var paths []string
	for _, tok := range rest[1:] {
		eq := strings.IndexByte(tok, '=')
		if eq <= 0 {
			continue
		}
		value := tok[eq+1:]
		if isAbsolutePath(value) || strings.HasPrefix(value, "~") {
			paths = append(paths, value)
		}
	}
	return paths
}

func envAssignmentPaths(rest []string) []string {
	cmdStart := 0
	for cmdStart < len(rest) && isEnvAssignment(rest[cmdStart]) {
		cmdStart++
	}
	var paths []string
	for i := 0; i < cmdStart; i++ {
		value := rest[i][strings.IndexByte(rest[i], '=')+1:]
		if isAbsolutePath(value) || strings.HasPrefix(value, "~") {
			paths = append(paths, value)
		}
	}
	return paths
}

func ProblematicShellSubCommands(command string) []string {
	if command == "" {
		return nil
	}
	subs := permission.SplitCommands(command)
	devPaths := collectDevicePaths(subs)
	var problematic []string
	for _, sub := range subs {
		if !isSubcommandSafe(sub, devPaths) {
			problematic = append(problematic, sub)
		}
	}
	return problematic
}

func CheckToolArgs(toolName string, args map[string]string) error {
	paths := FileToolPaths(toolName, args)
	for _, p := range paths {
		resolved, err := resolvePath(p)
		if err != nil {
			return fmt.Errorf("invalid path: %w", err)
		}
		if err := CheckPathAllowed(resolved); err != nil {
			return err
		}

		if toolName == "glob" || toolName == "find_files" {
			if pattern, ok := args["pattern"]; ok && pattern != "" {
				matchPath := filepath.Join(resolved, pattern)
				matchPath = filepath.Clean(matchPath)
				if err := CheckPathAllowed(matchPath); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
