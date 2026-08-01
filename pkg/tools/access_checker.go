package tools

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/opencode/llama-client/pkg/access"
	"github.com/opencode/llama-client/pkg/permission"
)

// Global access controller, shared across all tool instances.
// Set at startup from main.go, checked by resolvePath.
var globalAccessController *access.Controller

// SetAccessController устанавливает глобальный контроллер доступа.
func SetAccessController(ctrl *access.Controller) {
	globalAccessController = ctrl
}

// GetAccessController возвращает текущий контроллер доступа.
func GetAccessController() *access.Controller {
	return globalAccessController
}

// CheckPathAllowed проверяет, разрешён ли доступ к указанному пути.
// Возвращает nil если доступ разрешён, или ошибку с описанием причины отказа.
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

// formatDirs форматирует список директорий для сообщения об ошибке.
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

// FileToolKind определяет тип файлового инструмента
type FileToolKind int

const (
	ToolRead  FileToolKind = iota // только чтение (file_read, file_list)
	ToolWrite                     // запись (file_write, edit)
)

// fileToolPaths возвращает список имён параметров, содержащих пути,
// для указанного инструмента.
func FileToolPaths(toolName string, args map[string]string) []string {
	switch toolName {
	case "file_read", "read_file":
		if p, ok := args["path"]; ok {
			return []string{p}
		}
	case "file_write", "write_file":
		if p, ok := args["path"]; ok {
			return []string{p}
		}
	case "edit", "edit_file":
		if p, ok := args["path"]; ok {
			return []string{p}
		}
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
		return nil // командная строка — проверяем отдельно
	}
	return nil
}

// fileCommands содержит команды, оперирующие файлами/директориями.
// Для этих команд ExtractShellPaths извлекает пути из аргументов.
var fileCommands = map[string]bool{
	"cat": true, "less": true, "more": true, "head": true, "tail": true,
	"ls": true, "find": true,
	"cp": true, "mv": true, "rm": true,
	"mkdir": true, "touch": true, "chmod": true, "chown": true,
	"grep": true, "sed": true, "awk": true,
	"diff": true, "patch": true,
	"git": true,
}

// isAbsolutePath возвращает true, если строка выглядит как абсолютный Unix-путь.
func isAbsolutePath(s string) bool {
	return len(s) > 0 && s[0] == '/'
}

// looksLikePath проверяет, похож ли токен на путь к файлу.
// Исключает флаги, IP-адреса, URL, хосты, цитируемые строки и опции.
func looksLikePath(token string) bool {
	if strings.HasPrefix(token, "-") {
		return false
	}
	if len(token) > 0 && (token[0] == '\'' || token[0] == '"' || token[0] == '`') {
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

// ExtractShellPaths извлекает пути к файлам из shell-команды.
// Возвращает пустой слайс, если команда не оперирует файлами.
func ExtractShellPaths(command string) []string {
	if command == "" {
		return nil
	}

	parts := strings.Fields(command)
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

// PathsAllAllowed проверяет, что все указанные пути находятся
// в разрешённых директориях access controller'а.
// Возвращает true если контроллера нет, путей нет, или все пути разрешены.
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

// ShellPathsAllAllowed проверяет, что все указанные пути находятся
// в разрешённых директориях access controller'а.
// Возвращает true если контроллера нет, путей нет, или все пути разрешены.
func ShellPathsAllAllowed(paths []string) bool {
	return PathsAllAllowed(paths)
}

// cwdShellCommands меняют рабочую директорию и не требуют отдельной проверки.
var cwdShellCommands = map[string]bool{
	"cd": true, "chdir": true, "popd": true, "pushd": true,
}

// ShellCommandPathsAllowed проверяет, что каждая подкоманда shell-команды
// работает только внутри разрешённых директорий (рабочая папка и allowed_dirs).
// Возвращает false, если какая-то подкоманда не оперирует файлами или
// трогает путь вне разрешённых директорий — такую команду нужно
// проверять паттернами и, возможно, спрашивать пользователя.
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

// shellSubcommandPathsAllowed проверяет одну подкоманду.
func shellSubcommandPathsAllowed(sub string, devPaths map[string]bool) bool {
	parts := strings.Fields(sub)
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

// commandParts пропускает ведущие env-присваивания вида VAR=... и возвращает
// оставшиеся токены и имя команды (basename). Если команда состоит только
// из env-присваиваний — возвращает исходные токены и пустое имя.
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

// isEnvAssignment возвращает true для токена вида VAR=value (имя без '/',
// чтобы не спутать с путём, содержащим '=').
func isEnvAssignment(token string) bool {
	eq := strings.IndexByte(token, '=')
	if eq <= 0 || strings.HasPrefix(token, "-") {
		return false
	}
	return !strings.Contains(token[:eq], "/")
}

// isExplicitPathToken возвращает true для токена, который однозначно
// ссылается на файловую систему: абсолютный путь, ~ или ...
// Точковые токены вроде com.avito.android или 1.2.3 путями не считаются.
func isExplicitPathToken(token string) bool {
	if strings.HasPrefix(token, "-") {
		return false
	}
	return isAbsolutePath(token) || strings.HasPrefix(token, "~") || token == ".."
}

// isDiscardPath возвращает true для псевдо-устройств, запись в которые
// ничего не сохраняет (например, /dev/null). Такие пути не считаются
// файловыми операциями и не проверяются против allowed_dirs.
func isDiscardPath(p string) bool {
	return p == "/dev/null"
}

// collectFilePaths собирает локальные (host) файловые пути подкоманды:
// пути из аргументов файловых команд, цели редиректов и явные пути
// (абсолютные, ~, ..) в любом месте подкоманды. Пути удалённого устройства
// из devPaths (см. collectDevicePaths) и discard-пути (/dev/null) не считаются
// хостовыми файловыми операциями.
func collectFilePaths(sub string, devPaths map[string]bool) []string {
	parts := strings.Fields(sub)
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
	var paths []string
	for _, p := range ExtractShellPaths(sub) {
		if !devPaths[p] && !isDiscardPath(p) {
			paths = append(paths, p)
		}
	}
	for _, p := range redirectionTargets(sub) {
		if !devPaths[p] && !isDiscardPath(p) {
			paths = append(paths, p)
		}
	}
	rest, _ := commandParts(parts)
	for _, tok := range rest[1:] {
		if isExplicitPathToken(tok) && !devPaths[tok] && !isDiscardPath(tok) {
			paths = append(paths, tok)
		}
	}
	return paths
}

// remoteHostPaths возвращает локальные пути для команд, работающих с удалённым
// устройством/хостом (adb, ssh, scp). Второе значение — true, если команда
// удалённая. Пути внутри adb shell, ssh host или scp host:... относятся к
// чужой файловой системе и против allowed_dirs хоста не проверяются.
func remoteHostPaths(sub string) ([]string, bool) {
	parts := strings.Fields(sub)
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

// adbVerbIndex возвращает индекс глагола adb и аргументы после него,
// пропуская опции устройства (-s serial, -H host, -P port, -t transport...).
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

// adbHostPaths возвращает хостовые файлы adb-команды: источник push,
// приёмник pull, пакет install/sideload. Прочие adb-команды (shell и др.)
// работают с устройством и хостовых путей не имеют.
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

// isPathArgToken возвращает true для токена-аргумента, похожего на путь,
// исключая операторы оболочки и fd-редиректы (2>&1, 1> и т.п.).
func isPathArgToken(tok string) bool {
	if isShellOperatorToken(tok) {
		return false
	}
	if idx := strings.IndexAny(tok, "><"); idx >= 0 && idx != len(tok)-1 {
		return false
	}
	return looksLikePath(tok) || isExplicitPathToken(tok)
}

// sshHostPaths возвращает хостовые файлы ssh-команды (-i key). Всё после
// host — удалённая команда и хостовых путей не содержит.
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

// scpHostPaths возвращает локальные пути scp: токены без префикса host:.
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

// isRemoteSCPToken возвращает true для токена вида [user@]host:path.
func isRemoteSCPToken(tok string) bool {
	idx := strings.IndexByte(tok, ':')
	if idx <= 0 {
		return false
	}
	slash := strings.IndexByte(tok, '/')
	return slash < 0 || idx < slash
}

// collectDevicePaths собирает пути, принадлежащие файловой системе удалённого
// устройства/хоста, из всех подкоманд. Такие пути, упомянутые в хостовых
// подкомандах цепочки (например cat /data/local/tmp/ui.xml после adb shell
// uiautomator dump ...), не проверяются против allowed_dirs.
func collectDevicePaths(subs []string) map[string]bool {
	dev := make(map[string]bool)
	for _, sub := range subs {
		for _, p := range devicePathsIn(sub) {
			dev[p] = true
		}
	}
	return dev
}

// devicePathsIn возвращает пути устройства/удалённого хоста в одной подкоманде.
func devicePathsIn(sub string) []string {
	parts := strings.Fields(sub)
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

// adbDevicePaths возвращает пути устройства для adb shell/exec-out/exec-in
// (всё после глагола), push (цель) и pull (источник).
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

// sshRemotePaths возвращает пути удалённой команды ssh (всё после host).
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

// scpRemotePaths возвращает пути вида host:path в scp-команде.
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

// pathLikeTokens возвращает токены, похожие на пути (абсолютные, ~, ..,
// точковые), отбрасывая флаги. Сбор останавливается на операторах оболочки
// (> < | && ;), после которых идёт хостовый контекст, а не устройство.
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

// isShellOperatorToken возвращает true для токена-оператора оболочки.
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

// cwdTargetAllowed проверяет, что цель cd/pushd находится внутри
// разрешённых директорий. popd и cd без аргумента считаем непроверяемыми.
func cwdTargetAllowed(parts []string) bool {
	if parts[0] == "popd" {
		return false
	}
	if len(parts) < 2 {
		return false
	}
	return PathsAllAllowed(parts[1:2])
}

// redirectionTargets извлекает цели редиректов (> >> < 2> 2>> ...) из подкоманды.
// Обрабатывает как отдельные токены оператора (> file), так и слитные (>/file).
// fd-редиректы вида 2>&1 пропускаются.
func redirectionTargets(sub string) []string {
	var targets []string
	tokens := strings.Fields(sub)
	for i := 0; i < len(tokens); i++ {
		idx := strings.LastIndexAny(tokens[i], "><")
		if idx < 0 {
			continue
		}
		target := strings.TrimLeft(tokens[i][idx+1:], "&0123456789")
		if target != "" && looksLikePath(target) {
			targets = append(targets, target)
			continue
		}
		if idx == len(tokens[i])-1 && i+1 < len(tokens) && looksLikePath(tokens[i+1]) {
			targets = append(targets, tokens[i+1])
			i++
		}
	}
	return targets
}

// ShellCommandHasFilePaths возвращает true, если shell-команда содержит явные
// хостовые файловые операции: пути в аргументах файловых команд, цели
// редиректов или абсолютные пути/.. /~ в любом месте команды. Пути удалённого
// устройства (adb shell, ssh, scp) не считаются хостовыми.
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

// shellSubcommandHasFilePaths проверяет одну подкоманду.
func shellSubcommandHasFilePaths(sub string, devPaths map[string]bool) bool {
	return len(collectFilePaths(sub, devPaths)) > 0
}

// ShellCommandFilesystemSafe возвращает true, если команда не трогает файлы
// вне разрешённых директорий: каждая подкоманда либо не имеет хостовых путей
// (устройство adb/ssh/scp, файловая команда в рабочей папке, команда без
// файловых операций), либо все её хостовые пути находятся в allowed_dirs.
// Используется флагом пропуска запроса для безопасных команд.
func ShellCommandFilesystemSafe(command string) bool {
	if command == "" {
		return true
	}
	subs := permission.SplitCommands(command)
	devPaths := collectDevicePaths(subs)
	for _, sub := range subs {
		paths := collectFilePaths(sub, devPaths)
		if len(paths) > 0 && !PathsAllAllowed(paths) {
			return false
		}
	}
	return true
}

// CheckToolArgs проверяет все пути в аргументах инструмента на доступ.
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
		// Для glob с паттерном — проверяем что результат останется внутри разрешённых
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
