package logger

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ============================================================
// Уровни логирования
// ============================================================

// Level определяет уровень логирования
type Level int

const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
	LevelFatal
)

// String преобразует уровень в строковое представление
func (l Level) String() string {
	switch l {
	case LevelDebug:
		return "DEBUG"
	case LevelInfo:
		return "INFO"
	case LevelWarn:
		return "WARN"
	case LevelError:
		return "ERROR"
	case LevelFatal:
		return "FATAL"
	default:
		return "UNKNOWN"
	}
}

// ============================================================
// Logger — кастомный логгер
// ============================================================

// Config содержит настройки логгера
type Config struct {
	// Level — минимальный уровень логирования
	Level Level
	// File — путь к файлу логов (пусто для логирования только в консоль)
	File string
	// MaxSizeMB — максимальный размер лог-файла в МБ перед ротацией
	MaxSizeMB int
	// MaxAgeDays — максимальный возраст лог-файла в днях
	MaxAgeDays int
	// Compress — сжимать старые логи (gzip)
	Compress bool
}

// DefaultConfig возвращает конфигурацию по умолчанию
func DefaultConfig() Config {
	return Config{
		Level:      LevelInfo,
		File:       "",
		MaxSizeMB:  10,
		MaxAgeDays: 7,
		Compress:   false,
	}
}

// Logger — основной логгер приложения
type Logger struct {
	config  Config
	mu      sync.Mutex
	file    *os.File
	slog    *slog.Logger
	started time.Time
}

// ============================================================
// Инициализация
// ============================================================

// New создаёт новый логгер
func New(config Config) (*Logger, error) {
	l := &Logger{
		config:  config,
		started: time.Now(),
	}

	// Настраиваем форматирование
	handlerOptions := &slog.HandlerOptions{
		Level:     slog.LevelInfo,
		AddSource: false,
	}

	// Консольный логгер (всегда активен)
	consoleHandler := slog.NewTextHandler(os.Stderr, handlerOptions)
	l.slog = slog.New(consoleHandler)

	// Если указан файл — открываем и настраиваем файловый логгер
	if config.File != "" {
		// Создаём директорию если не существует
		dir := filepath.Dir(config.File)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create log directory: %w", err)
		}

		// Открываем файл (TRUNC очищает файл при старте)
		logFile, err := os.OpenFile(config.File, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
		if err != nil {
			return nil, fmt.Errorf("failed to open log file: %w", err)
		}
		l.file = logFile
	}

	return l, nil
}

// ============================================================
// Публичные методы логирования
// ============================================================

// DebugLog записывает дебаг-сообщение
func (l *Logger) DebugLog(msg string, args ...interface{}) {
	l.log(LevelDebug, msg, args...)
}

// InfoLog записывает информационное сообщение
func (l *Logger) InfoLog(msg string, args ...interface{}) {
	l.log(LevelInfo, msg, args...)
}

// WarnLog записывает предупреждение
func (l *Logger) WarnLog(msg string, args ...interface{}) {
	l.log(LevelWarn, msg, args...)
}

// ErrorLog записывает ошибку
func (l *Logger) ErrorLog(msg string, args ...interface{}) {
	l.log(LevelError, msg, args...)
}

// FatalLog записывает фатальную ошибку и завершает программу
func (l *Logger) FatalLog(msg string, args ...interface{}) {
	l.log(LevelFatal, msg, args...)
	os.Exit(1)
}

// log записывает сообщение со всеми уровнями
func (l *Logger) log(level Level, msg string, args ...interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()

	// Форматируем сообщение
	formattedMsg := fmt.Sprintf(msg, args...)

	// Добавляем метаданные
	timestamp := time.Now().Format("2006-01-02 15:04:05")
	levelStr := level.String()

	// Формируем полное сообщение
	fullMsg := fmt.Sprintf("[%s] [%s] %s", timestamp, levelStr, formattedMsg)

	// Пишем в консоль и/или файл
	if level >= l.config.Level {
		if l.file != nil {
			fmt.Fprintln(os.Stderr, fullMsg)
			fmt.Fprintln(l.file, fullMsg)
		} else {
			fmt.Fprintln(os.Stderr, fullMsg)
		}
	}
}

// ============================================================
// Методы для структурированного логирования
// ============================================================

// DebugLogf записывает дебаг-сообщение с форматированием
func (l *Logger) DebugLogf(format string, args ...interface{}) {
	l.DebugLog(format, args...)
}

// InfoLogf записывает информационное сообщение с форматированием
func (l *Logger) InfoLogf(format string, args ...interface{}) {
	l.InfoLog(format, args...)
}

// WarnLogf записывает предупреждение с форматированием
func (l *Logger) WarnLogf(format string, args ...interface{}) {
	l.WarnLog(format, args...)
}

// ErrorLogf записывает ошибку с форматированием
func (l *Logger) ErrorLogf(format string, args ...interface{}) {
	l.ErrorLog(format, args...)
}

// LogWithFields записывает сообщение с дополнительными полями
func (l *Logger) LogWithFields(level Level, msg string, fields map[string]interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()

	timestamp := time.Now().Format("2006-01-02 15:04:05")
	levelStr := level.String()

	// Формируем сообщение с полями
	fieldStr := ""
	for k, v := range fields {
		fieldStr += fmt.Sprintf(" %s=%v", k, v)
	}

	fullMsg := fmt.Sprintf("[%s] [%s] %s%s", timestamp, levelStr, msg, fieldStr)

	if level >= l.config.Level {
		if l.file != nil {
			l.slog.Info(fullMsg)
			fmt.Fprintln(l.file, fullMsg)
		} else {
			l.slog.Info(fullMsg)
		}
	}
}

// ============================================================
// Управление
// ============================================================

// SetLevel меняет минимальный уровень логирования
func (l *Logger) SetLevel(level Level) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.config.Level = level
}

// Close закрывает все ресурсы
func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.file != nil {
		return l.file.Close()
	}
	return nil
}

// IsFileLogging возвращает true если логирование в файл активно
func (l *Logger) IsFileLogging() bool {
	return l.file != nil
}

// GetStartTime возвращает время запуска логгера
func (l *Logger) GetStartTime() time.Time {
	return l.started
}

// ============================================================
// Утилиты
// ============================================================

// ParseLogLevel парсит уровень из строки
func ParseLogLevel(level string) Level {
	switch strings.ToLower(level) {
	case "debug", "dbg":
		return LevelDebug
	case "info", "inf":
		return LevelInfo
	case "warn", "warning":
		return LevelWarn
	case "error", "err":
		return LevelError
	case "fatal":
		return LevelFatal
	default:
		return LevelInfo
	}
}

// RotateLogFile выполняет ротацию лог-файла
func RotateLogFile(path string, maxSizeMB int, maxAgeDays int) error {
	if maxSizeMB <= 0 {
		maxSizeMB = 10
	}

	// Проверяем размер файла
	info, err := os.Stat(path)
	if err != nil {
		return err
	}

	maxSize := int64(maxSizeMB) * 1024 * 1024
	if info.Size() < maxSize {
		return nil
	}

	// Ротируем файл
	timestamp := time.Now().Format("20060102-150405")
	archivePath := fmt.Sprintf("%s.%s", path, timestamp)

	if err := os.Rename(path, archivePath); err != nil {
		return fmt.Errorf("failed to rename log file: %w", err)
	}

	// Удаляем старые логи
	cleanOldLogs(path, maxAgeDays)

	return nil
}

func cleanOldLogs(basePath string, maxAgeDays int) {
	if maxAgeDays <= 0 {
		maxAgeDays = 7
	}

	prefix := basePath + "."
	dir := filepath.Dir(basePath)

	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	cutoff := time.Now().AddDate(0, 0, -maxAgeDays)
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), filepath.Base(prefix)) {
			continue
		}
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			os.Remove(filepath.Join(dir, entry.Name()))
		}
	}
}

// ============================================================
// Глобальный логгер (singleton)
// ============================================================

var globalLogger *Logger
var globalOnce sync.Once

// InitGlobalLogger инициализирует глобальный логгер
func InitGlobalLogger(config Config) {
	globalOnce.Do(func() {
		globalLogger, _ = New(config)
	})
}

// GetGlobalLogger возвращает глобальный логгер
func GetGlobalLogger() *Logger {
	return globalLogger
}

// DebugLogfGlobal — глобальная функция для дебаг-логирования
func DebugLogfGlobal(format string, args ...interface{}) {
	if globalLogger != nil {
		globalLogger.DebugLogf(format, args...)
	}
}

// InfoLogfGlobal — глобальная функция для информационного логирования
func InfoLogfGlobal(format string, args ...interface{}) {
	if globalLogger != nil {
		globalLogger.InfoLogf(format, args...)
	}
}

// WarnLogfGlobal — глобальная функция для предупреждений
func WarnLogfGlobal(format string, args ...interface{}) {
	if globalLogger != nil {
		globalLogger.WarnLogf(format, args...)
	}
}

// ErrorLogfGlobal — глобальная функция для ошибок
func ErrorLogfGlobal(format string, args ...interface{}) {
	if globalLogger != nil {
		globalLogger.ErrorLogf(format, args...)
	}
}

// DebugToFile пишет дебаг-сообщение в файл и консоль
// Используется для детального логирования в debug режиме
func DebugToFile(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	// Всегда пишем в консоль
	fmt.Printf("%s\n", msg)
	// Если есть глобальный логгер с файлом - пишем в файл
	if globalLogger != nil && globalLogger.IsFileLogging() {
		timestamp := time.Now().Format("2006-01-02 15:04:05")
		fullMsg := fmt.Sprintf("[%s] [DEBUG] %s", timestamp, msg)
		globalLogger.WriteToFile(fullMsg)
	}
}

// WriteToFile пишет сообщение в лог-файл
func (l *Logger) WriteToFile(msg string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		fmt.Fprintln(l.file, msg)
	}
}
