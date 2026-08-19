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


type Level int

const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
	LevelFatal
)


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


type Config struct {
	
	Level Level
	
	File string
	
	MaxSizeMB int
	
	MaxAgeDays int
	
	Compress bool
}


func DefaultConfig() Config {
	return Config{
		Level:      LevelInfo,
		File:       "",
		MaxSizeMB:  10,
		MaxAgeDays: 7,
		Compress:   false,
	}
}


type Logger struct {
	config  Config
	mu      sync.Mutex
	file    *os.File
	slog    *slog.Logger
	started time.Time
}


func New(config Config) (*Logger, error) {
	l := &Logger{
		config:  config,
		started: time.Now(),
	}

	
	handlerOptions := &slog.HandlerOptions{
		Level:     slog.LevelInfo,
		AddSource: false,
	}

	
	consoleHandler := slog.NewTextHandler(os.Stderr, handlerOptions)
	l.slog = slog.New(consoleHandler)

	
	if config.File != "" {
		
		dir := filepath.Dir(config.File)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create log directory: %w", err)
		}

		
		if err := RotateLogFile(config.File, config.MaxSizeMB, config.MaxAgeDays); err != nil {
			return nil, fmt.Errorf("failed to rotate log file: %w", err)
		}

		
		logFile, err := os.OpenFile(config.File, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			return nil, fmt.Errorf("failed to open log file: %w", err)
		}
		l.file = logFile
	}

	return l, nil
}


func (l *Logger) DebugLog(msg string, args ...interface{}) {
	l.log(LevelDebug, msg, args...)
}


func (l *Logger) InfoLog(msg string, args ...interface{}) {
	l.log(LevelInfo, msg, args...)
}


func (l *Logger) WarnLog(msg string, args ...interface{}) {
	l.log(LevelWarn, msg, args...)
}


func (l *Logger) ErrorLog(msg string, args ...interface{}) {
	l.log(LevelError, msg, args...)
}


func (l *Logger) FatalLog(msg string, args ...interface{}) {
	l.log(LevelFatal, msg, args...)
}


func FatalLogExit(msg string, args ...interface{}) {
	if globalLogger != nil {
		globalLogger.FatalLog(msg, args...)
	} else {
		fmt.Printf("[FATAL] "+msg+"\n", args...)
	}
	os.Exit(1)
}


func (l *Logger) log(level Level, msg string, args ...interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()

	
	formattedMsg := fmt.Sprintf(msg, args...)

	
	timestamp := time.Now().Format("2006-01-02 15:04:05")
	levelStr := level.String()

	
	fullMsg := fmt.Sprintf("[%s] [%s] %s", timestamp, levelStr, formattedMsg)

	
	if level >= l.config.Level {
		if l.file != nil {
			fmt.Fprintln(os.Stderr, fullMsg)
			fmt.Fprintln(l.file, fullMsg)
		} else {
			fmt.Fprintln(os.Stderr, fullMsg)
		}
	}
}


func (l *Logger) DebugLogf(format string, args ...interface{}) {
	l.DebugLog(format, args...)
}


func (l *Logger) InfoLogf(format string, args ...interface{}) {
	l.InfoLog(format, args...)
}


func (l *Logger) WarnLogf(format string, args ...interface{}) {
	l.WarnLog(format, args...)
}


func (l *Logger) ErrorLogf(format string, args ...interface{}) {
	l.ErrorLog(format, args...)
}


func (l *Logger) LogWithFields(level Level, msg string, fields map[string]interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()

	timestamp := time.Now().Format("2006-01-02 15:04:05")
	levelStr := level.String()

	
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


func (l *Logger) SetLevel(level Level) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.config.Level = level
}


func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.file != nil {
		return l.file.Close()
	}
	return nil
}


func (l *Logger) IsFileLogging() bool {
	return l.file != nil
}


func (l *Logger) GetStartTime() time.Time {
	return l.started
}


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


func RotateLogFile(path string, maxSizeMB int, maxAgeDays int) error {
	if maxSizeMB <= 0 {
		maxSizeMB = 10
	}

	
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	maxSize := int64(maxSizeMB) * 1024 * 1024
	if info.Size() < maxSize {
		return nil
	}

	
	timestamp := time.Now().Format("20060102-150405")
	archivePath := fmt.Sprintf("%s.%s", path, timestamp)

	if err := os.Rename(path, archivePath); err != nil {
		return fmt.Errorf("failed to rename log file: %w", err)
	}

	
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


var globalLogger *Logger
var globalOnce sync.Once


func InitGlobalLogger(config Config) {
	globalOnce.Do(func() {
		globalLogger, _ = New(config)
	})
}


func GetGlobalLogger() *Logger {
	return globalLogger
}


func DebugLogfGlobal(format string, args ...interface{}) {
	if globalLogger != nil {
		globalLogger.DebugLogf(format, args...)
	}
}


func InfoLogfGlobal(format string, args ...interface{}) {
	if globalLogger != nil {
		globalLogger.InfoLogf(format, args...)
	}
}


func WarnLogfGlobal(format string, args ...interface{}) {
	if globalLogger != nil {
		globalLogger.WarnLogf(format, args...)
	}
}


func ErrorLogfGlobal(format string, args ...interface{}) {
	if globalLogger != nil {
		globalLogger.ErrorLogf(format, args...)
	}
}


func DebugToFile(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	if globalLogger != nil && globalLogger.IsFileLogging() {
		fmt.Printf("%s\n", msg)
		timestamp := time.Now().Format("2006-01-02 15:04:05")
		fullMsg := fmt.Sprintf("[%s] [DEBUG] %s", timestamp, msg)
		globalLogger.WriteToFile(fullMsg)
	}
}


func (l *Logger) WriteToFile(msg string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		fmt.Fprintln(l.file, msg)
	}
}
