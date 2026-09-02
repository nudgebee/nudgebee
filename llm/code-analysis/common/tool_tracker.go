package common

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// TrackedToolInvocation represents a real tool execution with full details
type TrackedToolInvocation struct {
	ID         string         `json:"id"`
	ToolName   string         `json:"tool_name"`
	Input      map[string]any `json:"input"`
	Output     any            `json:"output"`
	Status     string         `json:"status"`
	Duration   string         `json:"duration"`
	Timestamp  string         `json:"timestamp"`
	StartTime  time.Time      `json:"-"`
	Error      string         `json:"error,omitempty"`
	StepNumber int            `json:"step_number,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

// ToolInvocationTracker tracks all tool invocations for an analysis session
type ToolInvocationTracker struct {
	mu          sync.RWMutex
	analysisID  string
	invocations []TrackedToolInvocation
	stepCounter int
}

// NewToolInvocationTracker creates a new tracker for an analysis session
func NewToolInvocationTracker(analysisID string) *ToolInvocationTracker {
	return &ToolInvocationTracker{
		analysisID:  analysisID,
		invocations: make([]TrackedToolInvocation, 0),
		stepCounter: 0,
	}
}

// StartInvocation begins tracking a new tool invocation
func (t *ToolInvocationTracker) StartInvocation(toolName string, input map[string]any) string {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.stepCounter++
	invocationID := generateInvocationID(t.analysisID, toolName, t.stepCounter)

	// Sanitize input before storing
	sanitizedInput := t.sanitizeInput(input)

	invocation := TrackedToolInvocation{
		ID:         invocationID,
		ToolName:   toolName,
		Input:      sanitizedInput,
		Status:     "running",
		StartTime:  time.Now(),
		Timestamp:  time.Now().Format(time.RFC3339),
		StepNumber: t.stepCounter,
		Metadata:   make(map[string]any),
	}

	t.invocations = append(t.invocations, invocation)

	return invocationID
}

// StartInvocationWithIntention begins tracking a new tool invocation with intention logging
func (t *ToolInvocationTracker) StartInvocationWithIntention(toolName string, input map[string]any, intention string) string {
	invocationID := t.StartInvocation(toolName, input)

	// Add intention to metadata
	t.AddMetadata(invocationID, "intention", intention)

	return invocationID
}

// CompleteInvocation finishes tracking a tool invocation with results
func (t *ToolInvocationTracker) CompleteInvocation(invocationID string, output any, status string, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	for i := range t.invocations {
		if t.invocations[i].ID == invocationID {
			duration := time.Since(t.invocations[i].StartTime)
			t.invocations[i].Duration = duration.String()
			t.invocations[i].Output = output
			t.invocations[i].Status = status

			if err != nil {
				t.invocations[i].Error = err.Error()
				t.invocations[i].Status = "error"
			}
			break
		}
	}
}

// AddMetadata adds metadata to a specific invocation
func (t *ToolInvocationTracker) AddMetadata(invocationID string, key string, value any) {
	t.mu.Lock()
	defer t.mu.Unlock()

	for i := range t.invocations {
		if t.invocations[i].ID == invocationID {
			t.invocations[i].Metadata[key] = value
			break
		}
	}
}

// GetInvocations returns all tracked invocations (thread-safe copy)
func (t *ToolInvocationTracker) GetInvocations() []TrackedToolInvocation {
	t.mu.RLock()
	defer t.mu.RUnlock()

	// Return a deep copy to prevent race conditions
	invocations := make([]TrackedToolInvocation, len(t.invocations))
	copy(invocations, t.invocations)

	return invocations
}

// FileEvidence is one file the run actually opened, with a short extract of
// what was returned.
type FileEvidence struct {
	Path    string
	Snippet string
}

// evidenceSnippetLines bounds the extract kept per file. Enough to show a reader
// what was looked at, short enough that an answer's evidence does not become a
// second copy of the repository.
const evidenceSnippetLines = 3

// ReadFileEvidence returns the distinct files this session actually looked at,
// in first-seen order, from successful file_view reads and ripgrep hits.
//
// This is the run's evidence trail as recorded by the tools themselves, which is
// a stronger source than the model's own account of what it looked at: a model
// can under-report (weaker ones routinely omit citations entirely) or
// over-report (cite a file it never opened). Explore-mode submission uses it to
// back an answer whose citations are missing, so the contract is satisfied by
// what happened rather than by what the model remembered to declare.
func (t *ToolInvocationTracker) ReadFileEvidence() []FileEvidence {
	t.mu.RLock()
	defer t.mu.RUnlock()

	seen := make(map[string]bool)
	var evidence []FileEvidence
	add := func(path, snippet string) {
		path = strings.TrimSpace(path)
		if path == "" || seen[path] {
			return
		}
		seen[path] = true
		evidence = append(evidence, FileEvidence{Path: path, Snippet: snippet})
	}

	for _, inv := range t.invocations {
		if inv.Status != "success" {
			continue
		}
		result := invocationResultText(inv.Output)
		switch {
		case strings.EqualFold(inv.ToolName, "file_view"):
			path, _ := inv.Input["file_path"].(string)
			add(path, firstLines(result, evidenceSnippetLines))
		case strings.EqualFold(inv.ToolName, "rg"):
			// Search hits are evidence too, and in practice the stronger source:
			// a run can locate its answer entirely through rg without ever
			// opening a file — the production failure this fix targets used rg
			// three times and file_view not at all. Counting only file_view
			// would have left that exact run with no evidence to cite.
			for _, hit := range ripgrepHits(result) {
				add(hit.Path, hit.Snippet)
			}
		}
	}
	return evidence
}

// ripgrepHits extracts the files named by ripgrep output, handling both shapes
// the tool emits: bare paths (files_only mode) and "path:line:match".
func ripgrepHits(result string) []FileEvidence {
	var hits []FileEvidence
	for _, line := range strings.Split(result, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		path, snippet := line, ""
		// "path:line:match" — require the middle field to be a number so a bare
		// path containing a colon is not mistaken for a match line.
		if i := strings.Index(line, ":"); i > 0 {
			rest := line[i+1:]
			if j := strings.Index(rest, ":"); j > 0 {
				if _, err := strconv.Atoi(rest[:j]); err == nil {
					path, snippet = line[:i], strings.TrimSpace(rest[j+1:])
				}
			}
		}
		if !looksLikePath(path) {
			continue
		}
		hits = append(hits, FileEvidence{Path: path, Snippet: snippet})
	}
	return hits
}

// looksLikePath screens out prose lines ripgrep may print around its results
// (counts, warnings) without trying to validate that the file exists.
func looksLikePath(s string) bool {
	if s == "" || strings.ContainsAny(s, " \t") {
		return false
	}
	return strings.Contains(s, "/") || strings.Contains(s, ".")
}

// invocationResultText digs the human-readable payload out of a recorded
// invocation output, which the tracked wrapper stores as a sanitized map.
func invocationResultText(output any) string {
	m, ok := output.(map[string]any)
	if !ok {
		return ""
	}
	for _, key := range []string{"result", "observation"} {
		if s, ok := m[key].(string); ok && strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

// firstLines returns the first n non-blank lines of s.
func firstLines(s string, n int) string {
	var kept []string
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		kept = append(kept, line)
		if len(kept) >= n {
			break
		}
	}
	return strings.Join(kept, "\n")
}

// GetInvocationCount returns the total number of invocations
func (t *ToolInvocationTracker) GetInvocationCount() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.invocations)
}

// GetLastInvocation returns the most recent invocation
func (t *ToolInvocationTracker) GetLastInvocation() *TrackedToolInvocation {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if len(t.invocations) == 0 {
		return nil
	}

	// Return a copy
	last := t.invocations[len(t.invocations)-1]
	return &last
}

// GetInvocationsSince returns invocations with StepNumber > sinceStep (thread-safe copy)
func (t *ToolInvocationTracker) GetInvocationsSince(sinceStep int) []TrackedToolInvocation {
	t.mu.RLock()
	defer t.mu.RUnlock()

	var result []TrackedToolInvocation
	for _, inv := range t.invocations {
		if inv.StepNumber > sinceStep {
			result = append(result, inv)
		}
	}
	return result
}

// GetCurrentStepCount returns the current step counter value
func (t *ToolInvocationTracker) GetCurrentStepCount() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.stepCounter
}

// sanitizeInput removes sensitive information from input data
func (t *ToolInvocationTracker) sanitizeInput(input map[string]any) map[string]any {
	sanitized := make(map[string]any)

	for key, value := range input {
		if key == "credentials" || key == "password" || key == "token" || key == "github_token" {
			sanitized[key] = "***REDACTED***"
		} else {
			// Deep sanitize string values for credential patterns
			if strValue, ok := value.(string); ok {
				sanitized[key] = t.sanitizeString(strValue)
			} else {
				sanitized[key] = value
			}
		}
	}

	return sanitized
}

// sanitizeString removes credential patterns from strings
func (t *ToolInvocationTracker) sanitizeString(s string) string {
	// GitHub token pattern
	ghpPattern := regexp.MustCompile(`ghp_[a-zA-Z0-9]{36}`)
	s = ghpPattern.ReplaceAllString(s, "***REDACTED***")

	// Generic token patterns
	tokenPattern := regexp.MustCompile(`[a-zA-Z0-9]{40,}`)
	if tokenPattern.MatchString(s) && len(s) > 20 {
		return "***REDACTED***"
	}

	return s
}

// generateInvocationID creates a unique ID for tool invocations
func generateInvocationID(analysisID, toolName string, step int) string {
	timestamp := time.Now().Unix()
	return fmt.Sprintf("%s_%s_%d_%d", analysisID, toolName, step, timestamp)
}

// ToJSON converts invocations to JSON format
func (t *ToolInvocationTracker) ToJSON() ([]byte, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	return json.MarshalIndent(t.invocations, "", "  ")
}
