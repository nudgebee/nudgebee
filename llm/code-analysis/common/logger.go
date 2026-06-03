package common

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// LogType represents the type of log entry
type LogType string

const (
	LogTypeInfo   LogType = "LOG"    // General information, progress updates
	LogTypeError  LogType = "ERROR"  // Errors, failures, warnings
	LogTypeResult LogType = "RESULT" // Final results, outputs, structured data
)

// LogEvent represents different types of events in the analysis process
type LogEvent string

const (
	// Analysis lifecycle events
	EventAnalysisStart    LogEvent = "analysis_start"
	EventAnalysisComplete LogEvent = "analysis_complete"
	EventAnalysisFailure  LogEvent = "analysis_failure"

	// Tool execution events
	EventToolStart    LogEvent = "tool_start"
	EventToolComplete LogEvent = "tool_complete"
	EventToolFailure  LogEvent = "tool_failure"

	// Agent planning events
	EventPlanningStart    LogEvent = "planning_start"
	EventPlanningComplete LogEvent = "planning_complete"
	EventPlanningFailure  LogEvent = "planning_failure"
	EventPlanningProgress LogEvent = "planning_progress"

	// Step execution events
	EventStepStart    LogEvent = "step_start"
	EventStepComplete LogEvent = "step_complete"
	EventStepFailure  LogEvent = "step_failure"

	// Final result events
	EventFinalAnswer         LogEvent = "final_answer"
	EventFinalAnswerFallback LogEvent = "final_answer_fallback"
	EventResultExtracted     LogEvent = "result_extracted"
	EventResultParsed        LogEvent = "result_parsed"

	// Repository events
	EventRepoCloneStart    LogEvent = "repo_clone_start"
	EventRepoCloneComplete LogEvent = "repo_clone_complete"
	EventRepoCloneFailure  LogEvent = "repo_clone_failure"
)

// StructuredLog represents a structured log entry
type StructuredLog struct {
	Timestamp  string         `json:"timestamp"`
	LogType    LogType        `json:"log_type"`
	Event      LogEvent       `json:"event"`
	AnalysisID string         `json:"analysis_id,omitempty"`
	Message    string         `json:"message"`
	Data       map[string]any `json:"data,omitempty"`
	Duration   string         `json:"duration,omitempty"`
	Error      string         `json:"error,omitempty"`
	Success    bool           `json:"success"`
}

// AnalysisContext holds context for the current analysis
type AnalysisContext struct {
	AnalysisID string
	StartTime  time.Time
	Repository string
	UserID     string
	Request    any
}

// Logger provides structured logging for the code analysis agent
type Logger struct {
	context *AnalysisContext
}

// NewLogger creates a new structured logger with analysis context
func NewLogger(analysisID, repository, userID string, request any) *Logger {
	return &Logger{
		context: &AnalysisContext{
			AnalysisID: analysisID,
			StartTime:  time.Now(),
			Repository: repository,
			UserID:     userID,
			Request:    request,
		},
	}
}

// LogEvent logs a structured event
func (l *Logger) LogEvent(event LogEvent, logType LogType, message string, data map[string]any, err error, success bool) {
	logEntry := StructuredLog{
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
		LogType:    logType,
		Event:      event,
		AnalysisID: l.context.AnalysisID,
		Message:    message,
		Data:       data,
		Success:    success,
	}

	if err != nil {
		logEntry.Error = err.Error()
		logEntry.Success = false // Force success to false if there's an error
	}

	// Add duration for completion events
	if isCompletionEvent(event) {
		logEntry.Duration = time.Since(l.context.StartTime).String()
	}

	// Output as JSON only - no more mixed logging
	jsonBytes, _ := json.Marshal(logEntry)
	fmt.Println(string(jsonBytes))
}

// Log is a convenience method for LOG type entries
func (l *Logger) Log(event LogEvent, message string, data map[string]any) {
	l.LogEvent(event, LogTypeInfo, message, data, nil, true)
}

// Error is a convenience method for ERROR type entries
func (l *Logger) Error(event LogEvent, message string, err error, data map[string]any) {
	l.LogEvent(event, LogTypeError, message, data, err, false)
}

// Result is a convenience method for RESULT type entries
func (l *Logger) Result(event LogEvent, message string, result any) {
	data := map[string]any{"result": result}
	l.LogEvent(event, LogTypeResult, message, data, nil, true)
}

// GetAnalysisID returns the analysis ID from the logger's context.
func (l *Logger) GetAnalysisID() string {
	if l.context != nil {
		return l.context.AnalysisID
	}
	return ""
}

// Helper methods for common log patterns

func (l *Logger) AnalysisStart(repository string, logsLength int) {
	l.Log(EventAnalysisStart, "Starting code analysis", map[string]any{
		"repository":  repository,
		"logs_length": logsLength,
		"user_id":     l.context.UserID,
	})
}

func (l *Logger) AnalysisComplete(result any, toolCount int) {
	l.Result(EventAnalysisComplete, "Analysis completed successfully", map[string]any{
		"analysis_result": result,
		"tool_count":      toolCount,
	})
}

func (l *Logger) AnalysisFailure(err error, step string) {
	l.Error(EventAnalysisFailure, "Analysis failed", err, map[string]any{
		"failed_step": step,
	})
}

func (l *Logger) ToolStart(toolName string, input any) {
	l.Log(EventToolStart, fmt.Sprintf("Starting tool: %s", toolName), map[string]any{
		"tool_name": toolName,
		"input":     input,
	})
}

func (l *Logger) ToolComplete(toolName string, output any, duration time.Duration) {
	l.Log(EventToolComplete, fmt.Sprintf("Tool completed: %s", toolName), map[string]any{
		"tool_name": toolName,
		"output":    output,
		"duration":  duration.String(),
	})
}

func (l *Logger) ToolFailure(toolName string, err error) {
	l.Error(EventToolFailure, fmt.Sprintf("Tool failed: %s", toolName), err, map[string]any{
		"tool_name": toolName,
	})
}

func (l *Logger) StepStart(stepNumber int, action string, thought string) {
	l.Log(EventStepStart, fmt.Sprintf("Starting step %d: %s", stepNumber, action), map[string]any{
		"step_number": stepNumber,
		"action":      action,
		"thought":     thought,
	})
}

func (l *Logger) StepComplete(stepNumber int, action string, observation string) {
	l.Log(EventStepComplete, fmt.Sprintf("Step %d completed: %s", stepNumber, action), map[string]any{
		"step_number": stepNumber,
		"action":      action,
		"observation": observation,
	})
}

func (l *Logger) StepFailure(stepNumber int, action string, err error) {
	l.Error(EventStepFailure, fmt.Sprintf("Step %d failed: %s", stepNumber, action), err, map[string]any{
		"step_number": stepNumber,
		"action":      action,
	})
}

func (l *Logger) FinalAnswer(result any, source string) {
	l.Result(EventFinalAnswer, "Final answer generated", map[string]any{
		"analysis_result": result,
		"source":          source, // "agent" or "fallback"
	})
}

func (l *Logger) FinalAnswerFallback(reason string, intelligentAnalysis any) {
	l.Result(EventFinalAnswerFallback, "Using fallback analysis", map[string]any{
		"reason":               reason,
		"intelligent_analysis": intelligentAnalysis,
	})
}

func (l *Logger) ResultExtracted(result any, method string) {
	l.Result(EventResultExtracted, "Result extracted successfully", map[string]any{
		"extracted_result": result,
		"method":           method, // "structured_parsing", "regex_extraction", etc.
	})
}

func (l *Logger) ResultParsed(result any, valid bool) {
	l.Result(EventResultParsed, "Result parsing completed", map[string]any{
		"parsed_result": result,
		"valid":         valid,
	})
}

func (l *Logger) RepoCloneStart(repoURL string) {
	l.Log(EventRepoCloneStart, "Starting repository clone", map[string]any{
		"repo_url": repoURL,
	})
}

func (l *Logger) RepoCloneComplete(repoURL string, localPath string, branch string) {
	l.Log(EventRepoCloneComplete, "Repository clone completed", map[string]any{
		"repo_url":   repoURL,
		"local_path": localPath,
		"branch":     branch,
	})
}

func (l *Logger) RepoCloneFailure(repoURL string, err error) {
	l.Error(EventRepoCloneFailure, "Repository clone failed", err, map[string]any{
		"repo_url": repoURL,
	})
}

// isCompletionEvent checks if an event is a completion event for duration calculation
func isCompletionEvent(event LogEvent) bool {
	completionEvents := []LogEvent{
		EventAnalysisComplete,
		EventAnalysisFailure,
		EventToolComplete,
		EventToolFailure,
		EventPlanningComplete,
		EventPlanningFailure,
		EventStepComplete,
		EventStepFailure,
		EventFinalAnswer,
		EventFinalAnswerFallback,
		EventRepoCloneComplete,
		EventRepoCloneFailure,
	}

	for _, ce := range completionEvents {
		if event == ce {
			return true
		}
	}
	return false
}

// ParseStructuredLogs parses structured logs from output and extracts key information
func ParseStructuredLogs(output string) ([]StructuredLog, error) {
	lines := strings.Split(output, "\n")
	var logs []StructuredLog

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Try to parse as JSON
		var logEntry StructuredLog
		if err := json.Unmarshal([]byte(line), &logEntry); err == nil {
			logs = append(logs, logEntry)
		}
	}

	return logs, nil
}

// ExtractFinalResult extracts the final result from structured logs
func ExtractFinalResult(logs []StructuredLog) (any, bool) {
	for i := len(logs) - 1; i >= 0; i-- {
		log := logs[i]
		if log.Event == EventFinalAnswer || log.Event == EventFinalAnswerFallback {
			if result, exists := log.Data["result"]; exists {
				return result, log.Success
			}
		}
	}
	return nil, false
}

// GetAnalysisStatus returns the overall status of the analysis from logs
func GetAnalysisStatus(logs []StructuredLog) (status string, success bool) {
	for i := len(logs) - 1; i >= 0; i-- {
		log := logs[i]
		switch log.Event {
		case EventAnalysisComplete:
			return "completed", true
		case EventAnalysisFailure:
			return "failed", false
		}
	}
	return "in_progress", false
}
