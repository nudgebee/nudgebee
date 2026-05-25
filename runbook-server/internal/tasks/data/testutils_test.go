package data

import (
	// "nudgebee/runbook/internal/tasks/types" // Removed unused import
	"nudgebee/runbook/internal/tasks/testutils" // Updated import path
	// Removed unused import: log "go.temporal.io/sdk/log"
	"nudgebee/runbook/internal/tasks/types" // Import types package for types.TaskContext
)

// TemporalLogger interface mirrors go.temporal.io/sdk/log.Logger for testing purposes
// (This specific definition is not strictly necessary if using slog.Default directly,
// but it represents the interface that the context expects)
type TemporalLogger interface {
	Debug(msg string, keyvals ...interface{})
	Info(msg string, keyvals ...interface{})
	Warn(msg string, keyvals ...interface{})
	Error(msg string, keyvals ...interface{})
}

// MockLogger implements TemporalLogger for testing purposes
type MockLogger struct{}

func (m *MockLogger) Debug(msg string, keyvals ...interface{}) {}
func (m *MockLogger) Info(msg string, keyvals ...interface{})  {}
func (m *MockLogger) Warn(msg string, keyvals ...interface{})  {}
func (m *MockLogger) Error(msg string, keyvals ...interface{}) {}

// GetTestTaskContext creates a TaskContext suitable for testing
func GetTestTaskContext() types.TaskContext {
	// Use slog.Default() for now, which matches the user's previous preference.
	// If go.temporal.io/sdk/log is required explicitly, then we need to adapt.
	return testutils.NewTestTaskContext("test-tenant", "test-account", "test-user", &MockLogger{})
}
