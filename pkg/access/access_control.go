package access

import (
	"os"
	"path/filepath"
	"strings"
)

// ============================================================
// Access Control — контроль доступа к файловой системе
// ============================================================

// AccessLevel определяет уровень доступа
type AccessLevel int

const (
	AccessRead AccessLevel = iota
	AccessWrite
	AccessExecute
	AccessAll
)

// AccessResult результат проверки доступа
type AccessResult struct {
	Allowed   bool
	Reason    string
	RequestID string // Для отслеживания запросов на разрешение
}

// Controller управляет правилами доступа
type Controller struct {
	allowedDirs []string
	configured  bool
}

// ============================================================
// Инициализация
// ============================================================

// NewController создаёт новый контроллер доступа
func NewController(allowedDirs []string) *Controller {
	c := &Controller{
		allowedDirs: make([]string, 0),
	}

	for _, dir := range allowedDirs {
		c.addAllowedDir(dir)
	}

	c.configured = true
	return c
}

// addAllowedDir добавляет разрешённую директорию с разрешением переменных окружения
func (c *Controller) addAllowedDir(dir string) {
	// Расширяем переменные окружения (например, $HOME)
	dir = os.ExpandEnv(dir)

	// Преобразуем в абсолютный путь
	absPath, err := filepath.Abs(dir)
	if err != nil {
		return
	}

	// Канонизируем путь (разрешаем симлинки)
	canonical, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		canonical = absPath
	}

	c.allowedDirs = append(c.allowedDirs, canonical)
}

// ============================================================
// Проверка доступа
// ============================================================

// CheckAccess проверяет, находится ли путь в разрешённых директориях
func (c *Controller) CheckAccess(path string) AccessResult {
	if !c.configured {
		return AccessResult{
			Allowed: false,
			Reason:  "Access control not configured",
		}
	}

	// Расширяем переменные окружения
	path = os.ExpandEnv(path)

	// Преобразуем в абсолютный путь
	absPath, err := filepath.Abs(path)
	if err != nil {
		return AccessResult{
			Allowed: false,
			Reason:  "Cannot resolve path: " + err.Error(),
		}
	}

	// Канонизируем путь (разрешаем симлинки)
	canonical, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		// Если файл не существует, канонизируем родительскую директорию
		parentDir := filepath.Dir(absPath)
		canonicalParent, parentErr := filepath.EvalSymlinks(parentDir)
		if parentErr != nil {
			// Если родитель тоже не существует, используем абсолютный путь
			canonical = absPath
		} else {
			// Собираем путь из канонизированного родителя и имени файла
			canonical = filepath.Join(canonicalParent, filepath.Base(absPath))
		}
	}

	// Проверяем, что путь находится внутри разрешённых директорий
	for _, allowedDir := range c.allowedDirs {
		// Проверяем, начинается ли путь с разрешённой директории
		if strings.HasPrefix(canonical, allowedDir) {
			return AccessResult{
				Allowed: true,
				Reason:  "Path is within allowed directory",
			}
		}
	}

	return AccessResult{
		Allowed: false,
		Reason:  "Path is outside allowed directories: " + canonical,
	}
}

// CheckReadAccess проверяет доступ на чтение
func (c *Controller) CheckReadAccess(path string) AccessResult {
	return c.CheckAccess(path)
}

// CheckWriteAccess проверяет доступ на запись
func (c *Controller) CheckWriteAccess(path string) AccessResult {
	result := c.CheckAccess(path)
	if !result.Allowed {
		return result
	}

	// Дополнительно проверяем, что родительская директория существует и доступна
	parentDir := filepath.Dir(path)
	info, err := os.Stat(parentDir)
	if err != nil {
		if os.IsNotExist(err) {
			return AccessResult{
				Allowed: false,
				Reason:  "Parent directory does not exist: " + parentDir,
			}
		}
		return AccessResult{
			Allowed: false,
			Reason:  "Cannot stat parent directory: " + err.Error(),
		}
	}

	if !info.IsDir() {
		return AccessResult{
			Allowed: false,
			Reason:  "Parent path is not a directory",
		}
	}

	// Проверяем права на запись в родительскую директорию
	writable := false
	if info.Mode()&0222 != 0 {
		writable = true
	} else {
		// Проверяем через попытку создания временного файла
		tmpFile, err := os.CreateTemp(parentDir, ".access_check_*")
		if err == nil {
			writable = true
			tmpFile.Close()
			os.Remove(tmpFile.Name())
		}
	}

	if !writable {
		return AccessResult{
			Allowed: false,
			Reason:  "Parent directory is not writable",
		}
	}

	return AccessResult{
		Allowed: true,
		Reason:  "Path is within allowed directory and parent is writable",
	}
}

// ============================================================
// Безопасное преобразование путей
// ============================================================

// SanitizePath удаляет потенциально опасные последовательности из пути
func SanitizePath(path string) string {
	// Убираем переменные окружения для безопасности
	path = os.ExpandEnv(path)

	// Заменяем .. на пустые сегменты для предотвращения traversal
	cleaned := filepath.Clean(path)

	// Если путь содержит .. после очистки — это попытка traversal
	if strings.Contains(cleaned, "..") {
		return ""
	}

	// Убедимся, что путь абсолютный
	if !filepath.IsAbs(cleaned) {
		absPath, err := filepath.Abs(cleaned)
		if err == nil {
			cleaned = absPath
		}
	}

	return cleaned
}

// IsPathSafe проверяет, что путь не содержит опасных паттернов
func IsPathSafe(path string) bool {
	// Проверяем на наличие символов инъекции
	dangerousChars := []string{";", "|", "&", "`", "$", "(", ")", "{", "}", "<", ">", "\\", "\n", "\r"}
	for _, ch := range dangerousChars {
		if strings.Contains(path, ch) {
			return false
		}
	}

	// Проверяем на наличие .. (path traversal)
	cleaned := filepath.Clean(path)
	if strings.Contains(cleaned, "..") {
		return false
	}

	return true
}

// ============================================================
// Безопасное чтение файлов
// ============================================================

// SafeReadFile читает файл с проверкой доступа
func (c *Controller) SafeReadFile(path string) ([]byte, AccessResult) {
	result := c.CheckAccess(path)
	if !result.Allowed {
		return nil, result
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, AccessResult{
			Allowed: false,
			Reason:  "Failed to read file: " + err.Error(),
		}
	}

	return data, AccessResult{
		Allowed: true,
		Reason:  "File read successfully",
	}
}

// ============================================================
// Безопасная запись файлов
// ============================================================

// SafeWriteFile записывает файл с проверкой доступа
func (c *Controller) SafeWriteFile(path string, data []byte) AccessResult {
	result := c.CheckWriteAccess(path)
	if !result.Allowed {
		return result
	}

	// Создаём временный файл в той же директории (атомарная запись)
	dir := filepath.Dir(path)
	tmpFile, err := os.CreateTemp(dir, ".write_temp_*")
	if err != nil {
		return AccessResult{
			Allowed: false,
			Reason:  "Failed to create temp file: " + err.Error(),
		}
	}
	tmpName := tmpFile.Name()

	// Записываем данные
	if _, err := tmpFile.Write(data); err != nil {
		tmpFile.Close()
		os.Remove(tmpName)
		return AccessResult{
			Allowed: false,
			Reason:  "Failed to write to temp file: " + err.Error(),
		}
	}

	// Закрываем и переименовываем (атомарная операция)
	if err := tmpFile.Close(); err != nil {
		os.Remove(tmpName)
		return AccessResult{
			Allowed: false,
			Reason:  "Failed to close temp file: " + err.Error(),
		}
	}

	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return AccessResult{
			Allowed: false,
			Reason:  "Failed to rename temp file: " + err.Error(),
		}
	}

	return AccessResult{
		Allowed: true,
		Reason:  "File written successfully",
	}
}
