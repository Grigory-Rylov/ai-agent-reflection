package logger

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)


func TestNewLogger(t *testing.T) {
	t.Run("creates logger with default config", func(t *testing.T) {
		logger, err := New(DefaultConfig())
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if logger == nil {
			t.Fatal("logger should not be nil")
		}
		if logger.config.Level != LevelInfo {
			t.Errorf("expected default level Info, got %v", logger.config.Level)
		}
	})

	t.Run("creates logger with debug level", func(t *testing.T) {
		config := DefaultConfig()
		config.Level = LevelDebug

		logger, err := New(config)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if logger.config.Level != LevelDebug {
			t.Errorf("expected debug level, got %v", logger.config.Level)
		}
	})

	t.Run("creates logger with file output", func(t *testing.T) {
		dir := setupTempDir(t)
		defer cleanupTempDir(t, dir)

		config := DefaultConfig()
		config.File = filepath.Join(dir, "app.log")

		logger, err := New(config)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if !logger.IsFileLogging() {
			t.Error("expected file logging to be enabled")
		}
	})

	t.Run("returns error for invalid file path", func(t *testing.T) {
		config := DefaultConfig()
		config.File = "/nonexistent/dir/nonexistent/file.log"

		_, err := New(config)
		if err == nil {
			t.Fatal("expected error for invalid file path")
		}
	})
}

func TestLoggerMethods(t *testing.T) {
	t.Run("logs info message", func(t *testing.T) {
		logger, _ := New(DefaultConfig())
		defer logger.Close()

		
		logger.InfoLog("test info message")
	})

	t.Run("logs debug message", func(t *testing.T) {
		config := DefaultConfig()
		config.Level = LevelDebug

		logger, _ := New(config)
		defer logger.Close()

		logger.DebugLog("test debug message")
	})

	t.Run("logs with formatting", func(t *testing.T) {
		logger, _ := New(DefaultConfig())
		defer logger.Close()

		logger.InfoLogf("test %s %d", "message", 42)
	})

	t.Run("sets custom level", func(t *testing.T) {
		config := DefaultConfig()
		logger, _ := New(config)
		defer logger.Close()

		logger.SetLevel(LevelDebug)
		if logger.config.Level != LevelDebug {
			t.Error("expected level to be updated")
		}
	})
}

func TestParseLogLevel(t *testing.T) {
	tests := []struct {
		input    string
		expected Level
	}{
		{"debug", LevelDebug},
		{"Debug", LevelDebug},
		{"dbg", LevelDebug},
		{"info", LevelInfo},
		{"Info", LevelInfo},
		{"warn", LevelWarn},
		{"warning", LevelWarn},
		{"error", LevelError},
		{"Error", LevelError},
		{"fatal", LevelFatal},
		{"unknown", LevelInfo},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := ParseLogLevel(tt.input)
			if result != tt.expected {
				t.Errorf("expected %v for input '%s', got %v", tt.expected, tt.input, result)
			}
		})
	}
}

func TestRotateLogFile(t *testing.T) {
	t.Run("rotates file when size exceeds limit", func(t *testing.T) {
		dir := setupTempDir(t)
		defer cleanupTempDir(t, dir)

		logFile := filepath.Join(dir, "app.log")

		
		f, _ := os.Create(logFile)
		f.Write(make([]byte, 1100*1024)) 
		f.Close()

		err := RotateLogFile(logFile, 1, 7)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		
		_, err = os.Stat(logFile)
		if err == nil {
			t.Error("expected original file to be renamed")
		}
	})

	t.Run("does not rotate when file is small", func(t *testing.T) {
		dir := setupTempDir(t)
		defer cleanupTempDir(t, dir)

		logFile := filepath.Join(dir, "app.log")

		
		f, _ := os.Create(logFile)
		f.WriteString("small")
		f.Close()

		err := RotateLogFile(logFile, 10, 7) 
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		
		_, err = os.Stat(logFile)
		if err != nil {
			t.Error("expected original file to remain")
		}
	})
}

func TestNewLoggerRotatesInsteadOfTruncating(t *testing.T) {
	t.Run("rotates oversized log on startup", func(t *testing.T) {
		dir := setupTempDir(t)
		defer cleanupTempDir(t, dir)

		logFile := filepath.Join(dir, "debug.log")
		f, _ := os.Create(logFile)
		f.Write(make([]byte, 6*1024*1024)) 
		f.Close()

		config := DefaultConfig()
		config.File = logFile
		config.MaxSizeMB = 5

		l, err := New(config)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		defer l.Close()

		if _, err := os.Stat(logFile); err != nil {
			t.Error("expected fresh log file to exist")
		}
		entries, _ := os.ReadDir(dir)
		archives := 0
		for _, e := range entries {
			if e.Name() != "debug.log" {
				archives++
			}
		}
		if archives != 1 {
			t.Errorf("expected 1 rotated archive, got %d", archives)
		}
	})

	t.Run("appends to existing log without clearing", func(t *testing.T) {
		dir := setupTempDir(t)
		defer cleanupTempDir(t, dir)

		logFile := filepath.Join(dir, "debug.log")
		if err := os.WriteFile(logFile, []byte("old content\n"), 0644); err != nil {
			t.Fatalf("failed to write file: %v", err)
		}

		config := DefaultConfig()
		config.File = logFile
		config.MaxSizeMB = 5

		l, err := New(config)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		defer l.Close()

		l.InfoLogf("new line")

		data, _ := os.ReadFile(logFile)
		if !strings.HasPrefix(string(data), "old content\n") {
			t.Errorf("expected old content to be preserved, got %q", data)
		}
	})
}

func TestLevelString(t *testing.T) {
	tests := []struct {
		level    Level
		expected string
	}{
		{LevelDebug, "DEBUG"},
		{LevelInfo, "INFO"},
		{LevelWarn, "WARN"},
		{LevelError, "ERROR"},
		{LevelFatal, "FATAL"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if tt.level.String() != tt.expected {
				t.Errorf("expected '%s', got '%s'", tt.expected, tt.level.String())
			}
		})
	}
}

func TestGetStartTime(t *testing.T) {
	logger, _ := New(DefaultConfig())
	defer logger.Close()

	startTime := logger.GetStartTime()
	if startTime.IsZero() {
		t.Error("start time should not be zero")
	}
}


func setupTempDir(t *testing.T) string {
	dir, err := os.MkdirTemp("", "logger_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	return dir
}

func cleanupTempDir(t *testing.T, dir string) {
	os.RemoveAll(dir)
}
