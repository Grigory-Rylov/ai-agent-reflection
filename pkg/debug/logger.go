package debug

import "fmt"


type Logger interface {
	Debug(format string, args ...interface{})
	Info(format string, args ...interface{})
	Warn(format string, args ...interface{})
	Error(format string, args ...interface{})
}


type silentLogger struct{}

func (l *silentLogger) Debug(format string, args ...interface{}) {}
func (l *silentLogger) Info(format string, args ...interface{})  {}
func (l *silentLogger) Warn(format string, args ...interface{})  {}
func (l *silentLogger) Error(format string, args ...interface{}) {}


type consoleLogger struct{}

func (l *consoleLogger) Debug(format string, args ...interface{}) {
	fmt.Printf("[DEBUG] "+format+"\n", args...)
}
func (l *consoleLogger) Info(format string, args ...interface{}) {
	fmt.Printf("[INFO] "+format+"\n", args...)
}
func (l *consoleLogger) Warn(format string, args ...interface{}) {
	fmt.Printf("[WARN] "+format+"\n", args...)
}
func (l *consoleLogger) Error(format string, args ...interface{}) {
	fmt.Printf("[ERROR] "+format+"\n", args...)
}


func NewLogger(debug bool) Logger {
	if debug {
		return &consoleLogger{}
	}
	return &silentLogger{}
}
