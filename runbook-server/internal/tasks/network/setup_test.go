package network

import "fmt"

// TestLogger implements log.Logger for testing.
// It is shared across all tests in the network package.
type TestLogger struct{}

func (l *TestLogger) Debug(msg string, keyvals ...interface{}) {
	fmt.Printf("DEBUG: %s %v\n", msg, keyvals)
}
func (l *TestLogger) Info(msg string, keyvals ...interface{}) {
	fmt.Printf("INFO: %s %v\n", msg, keyvals)
}
func (l *TestLogger) Warn(msg string, keyvals ...interface{}) {
	fmt.Printf("WARN: %s %v\n", msg, keyvals)
}
func (l *TestLogger) Error(msg string, keyvals ...interface{}) {
	fmt.Printf("ERROR: %s %v\n", msg, keyvals)
}
