package session

import (
	"nudgebee/code-analysis-agent/internal/credentials"
	"nudgebee/code-analysis-agent/planners"
)

// BuildConfig holds optional custom build/lint/test commands for verification.
// When provided, these commands override auto-detection by the agent.
type BuildConfig struct {
	SetupCommand string `json:"setup_command,omitempty"` // e.g., "npm ci --legacy-peer-deps"
	LintCommand  string `json:"lint_command,omitempty"`  // e.g., "npm run lint"
	BuildCommand string `json:"build_command,omitempty"` // e.g., "npm run build"
	TestCommand  string `json:"test_command,omitempty"`  // e.g., "npm test"
}

// SessionContext holds all the shared information for a single analysis session.
// It acts as a "case file" that is passed between agents.
type SessionContext struct {
	AnalysisID       string
	OriginalQuery    string
	InitialLogs      string
	RepoContext      *planners.RepositoryContext
	Credentials      *credentials.ResolvedCredentials // Secure credentials for tools
	WorkingDirectory string                           // Current working directory for tools (set after repo clone)
	Scratchpad       string                           // Accumulates thoughts, tool calls, and discoveries from each agent
	EventID          string                           // Event ID for linking back to NudgeBee event investigation
	RecommendationID string                           // Recommendation ID for linking back to NudgeBee recommendation
	AccountID        string                           // Account ID for constructing recommendation URLs
	BuildConfig      *BuildConfig                     // Optional custom build/lint/test commands for verification
	Mode             string                           // "explore" (read-only) or "fix"; templates render mode-specific instructions
}

// AddToScratchpad appends investigation notes to the shared context
func (sc *SessionContext) AddToScratchpad(agentName, entry string) {
	if sc.Scratchpad != "" {
		sc.Scratchpad += "\n\n"
	}
	sc.Scratchpad += "=== " + agentName + " ===" + "\n" + entry
}

// GetScratchpad returns the accumulated investigation history
func (sc *SessionContext) GetScratchpad() string {
	return sc.Scratchpad
}
