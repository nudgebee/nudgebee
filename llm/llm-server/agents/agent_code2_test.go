package agents

import (
	"bufio"
	"encoding/json"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMain loads .env into OS environment so os.Getenv() works in tests.
func TestMain(m *testing.M) {
	if f, err := os.Open(".env"); err == nil {
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			if k, v, ok := strings.Cut(line, "="); ok {
				if os.Getenv(k) == "" { // don't override explicit env vars
					if err := os.Setenv(k, v); err != nil {
						panic(err)
					}
				}
			}
		}
		if err := scanner.Err(); err != nil {
			panic(err)
		}
		if err := f.Close(); err != nil {
			panic(err)
		}
	}
	os.Exit(m.Run())
}

// TODO mock DBs
// TODO mock Tool Execution

func TestCodeAgent2_WorkspaceResponseParsing(t *testing.T) {
	tests := []struct {
		name           string
		analyzeResp    map[string]any
		expectKey      string
		expectNotEmpty bool
	}{
		{
			name: "agent_response_with_title_and_description",
			analyzeResp: map[string]any{
				"success":     true,
				"analysis_id": "test-123",
				"agent_response": map[string]any{
					"title":               "Database Insertion Error",
					"description":         "ON CONFLICT DO UPDATE command cannot affect row a second time",
					"root_cause_analysis": "Duplicate rows in batch insert",
					"fixed_code":          "-- deduplicate before insert",
				},
				"processing_time": "5.2s",
			},
			expectKey:      "title",
			expectNotEmpty: true,
		},
		{
			name: "agent_response_with_pr_info",
			analyzeResp: map[string]any{
				"success":     true,
				"analysis_id": "test-456",
				"agent_response": map[string]any{
					"title":       "Fix duplicate insert",
					"description": "Deduplicate rows before batch insert",
					"automated_fix_pr_info": map[string]any{
						"number": 42,
						"url":    "https://github.com/org/repo/pull/42",
						"title":  "Fix: deduplicate before insert",
					},
				},
			},
			expectKey:      "automated_fix_pr_info",
			expectNotEmpty: true,
		},
		{
			name: "nil_agent_response_returns_full_body",
			analyzeResp: map[string]any{
				"success":        false,
				"error":          "analysis failed",
				"agent_response": nil,
			},
			expectKey:      "error",
			expectNotEmpty: true,
		},
		{
			name: "missing_agent_response_returns_full_body",
			analyzeResp: map[string]any{
				"success": false,
				"error":   "workspace pod not ready",
			},
			expectKey:      "error",
			expectNotEmpty: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			respBytes, err := json.Marshal(tt.analyzeResp)
			require.NoError(t, err)

			// Simulate the parsing logic from evaluateCodeUsingWorkspace
			var parsed map[string]any
			err = json.Unmarshal(respBytes, &parsed)
			require.NoError(t, err)

			var output string
			if agentResponse, ok := parsed["agent_response"]; ok && agentResponse != nil {
				responseBytes, err := json.Marshal(agentResponse)
				require.NoError(t, err)
				output = string(responseBytes)
			} else {
				output = string(respBytes)
			}

			// Verify the output is valid JSON
			var result map[string]any
			err = json.Unmarshal([]byte(output), &result)
			require.NoError(t, err, "output should be valid JSON: %s", output)

			// Verify expected key exists
			assert.Contains(t, result, tt.expectKey)

			// Verify source_details can be added (simulating the enrichment step)
			result["source_details"] = map[string]any{
				"workloads.nudgebee.com/git.hash": "abc123",
				"workloads.nudgebee.com/git.repo": "https://github.com/org/repo",
			}
			enriched, err := json.Marshal(result)
			require.NoError(t, err)
			assert.Contains(t, string(enriched), "source_details")
		})
	}
}

// TestCodeAgent2_WorkspaceAnalyzeRequestBuilding verifies the analyze request
// is correctly constructed from CodeAgent2Request and NBAgentRequest.

func TestCodeAgent2_WorkspaceAnalyzeRequestBuilding(t *testing.T) {
	tests := []struct {
		name           string
		request        CodeAgent2Request
		creds          []GitCredentials
		provider       string
		expectedBranch string
	}{
		{
			name: "github_token_auth",
			request: CodeAgent2Request{
				Query:            "Analyze the error",
				Errors:           []string{"error log 1", "error log 2"},
				GitRepo:          "https://github.com/org/repo",
				GitCommit:        "abc123",
				RaisePr:          true,
				EventId:          "evt-1",
				RecommendationId: "rec-1",
				AccountId:        "acc-1",
			},
			creds: []GitCredentials{
				{AuthType: "token", Password: "ghp_test_token", Provider: "github"},
			},
			provider:       "github",
			expectedBranch: "abc123", // falls back to GitCommit when TargetBranch is empty
		},
		{
			name: "gitlab_token_auth",
			request: CodeAgent2Request{
				Query:   "Analyze the crash",
				Errors:  []string{"traceback line 1"},
				GitRepo: "https://gitlab.com/group/project",
				RaisePr: false,
			},
			creds: []GitCredentials{
				{AuthType: "token", Password: "glpat-test_token", Provider: "gitlab"},
			},
			provider:       "gitlab",
			expectedBranch: "",
		},
		{
			name: "empty_errors",
			request: CodeAgent2Request{
				Query:   "Review this code",
				GitRepo: "https://github.com/org/repo",
				Agent:   "code_review",
			},
			creds: []GitCredentials{
				{AuthType: "token", Password: "ghp_token", Provider: "github"},
			},
			provider:       "github",
			expectedBranch: "",
		},
		{
			name: "target_branch_overrides_git_commit",
			request: CodeAgent2Request{
				Query:        "Fix migration",
				GitRepo:      "https://github.com/org/repo",
				GitCommit:    "abc123",
				TargetBranch: "prod",
				RaisePr:      true,
			},
			creds: []GitCredentials{
				{AuthType: "token", Password: "ghp_token", Provider: "github"},
			},
			provider:       "github",
			expectedBranch: "prod",
		},
		{
			name: "target_branch_only",
			request: CodeAgent2Request{
				Query:        "Fix migration on release branch",
				GitRepo:      "https://github.com/org/repo",
				TargetBranch: "release/1.x",
				RaisePr:      true,
			},
			creds: []GitCredentials{
				{AuthType: "token", Password: "ghp_token", Provider: "github"},
			},
			provider:       "github",
			expectedBranch: "release/1.x",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Mirror the branch-resolution logic in evaluateCodeUsingWorkspace.
			branch := tt.request.TargetBranch
			if branch == "" {
				branch = tt.request.GitCommit
			}

			analyzeRequest := map[string]any{
				"cloud_account_id":   tt.request.AccountId,
				"workload_name":      "unknown",
				"workload_namespace": "unknown",
				"workload_kind":      "Deployment",
				"logs":               joinErrors(tt.request.Errors),
				"prompt":             tt.request.Query,
				"git_repository": map[string]any{
					"url":      tt.request.GitRepo,
					"branch":   branch,
					"provider": tt.provider,
				},
				"raise_pr":          tt.request.RaisePr,
				"event_id":          tt.request.EventId,
				"recommendation_id": tt.request.RecommendationId,
				"account_id":        tt.request.AccountId,
			}

			if tt.request.Agent != "" {
				analyzeRequest["agent_id"] = tt.request.Agent
			}

			// Resolve git token
			gitToken := ""
			if len(tt.creds) > 0 && tt.creds[0].AuthType == "token" {
				gitToken = tt.creds[0].Password
			}
			if gitToken != "" {
				analyzeRequest["git_credentials"] = map[string]any{
					"type":  "token",
					"token": gitToken,
				}
			}

			// Verify serialization
			data, err := json.Marshal(analyzeRequest)
			require.NoError(t, err)

			var result map[string]any
			err = json.Unmarshal(data, &result)
			require.NoError(t, err)

			assert.Equal(t, tt.request.Query, result["prompt"])
			assert.Equal(t, tt.request.RaisePr, result["raise_pr"])

			gitRepo := result["git_repository"].(map[string]any)
			assert.Equal(t, tt.request.GitRepo, gitRepo["url"])
			assert.Equal(t, tt.provider, gitRepo["provider"])
			assert.Equal(t, tt.expectedBranch, gitRepo["branch"])

			if gitToken != "" {
				gitCreds := result["git_credentials"].(map[string]any)
				assert.Equal(t, "token", gitCreds["type"])
				assert.Equal(t, gitToken, gitCreds["token"])
			}

			if tt.request.Agent != "" {
				assert.Equal(t, tt.request.Agent, result["agent_id"])
			} else {
				_, hasAgent := result["agent_id"]
				assert.False(t, hasAgent)
			}
		})
	}
}

// TestCodeAgent2Request_TargetBranchJSON verifies the target_branch field is
// parsed from the standard JSON input the planner produces.

func TestCodeAgent2Request_TargetBranchJSON(t *testing.T) {
	raw := `{
      "query": "Fix the migration and raise a PR",
      "git_repo": "https://github.com/org/repo",
      "target_branch": "prod",
      "raise_pr": true
    }`

	var req CodeAgent2Request
	err := json.Unmarshal([]byte(raw), &req)
	require.NoError(t, err)
	assert.Equal(t, "prod", req.TargetBranch)
	assert.True(t, req.RaisePr)
	assert.Equal(t, "https://github.com/org/repo", req.GitRepo)
}

// TestExtractAgentResponseWithTokenUsage validates token usage extraction from
// code-analysis responses. No infrastructure needed.

func TestExtractAgentResponseWithTokenUsage(t *testing.T) {
	tests := []struct {
		name             string
		response         map[string]any
		expectAgentResp  string // substring expected in AgentResponse
		expectTokenUsage bool
		expectModel      string
		expectProvider   string
		expectPrompt     int
		expectCompletion int
		expectCached     int
	}{
		{
			name: "full_response_with_token_usage",
			response: map[string]any{
				"agent_response": map[string]any{
					"title":       "Bug Fix",
					"description": "Fixed null pointer",
				},
				"token_usage": map[string]any{
					"prompt_tokens":         float64(50000),
					"completion_tokens":     float64(3000),
					"total_tokens":          float64(53000),
					"cached_content_tokens": float64(12000),
					"model":                 "gemini-2.5-pro",
					"provider":              "googleai",
				},
			},
			expectAgentResp:  "Bug Fix",
			expectTokenUsage: true,
			expectModel:      "gemini-2.5-pro",
			expectProvider:   "googleai",
			expectPrompt:     50000,
			expectCompletion: 3000,
			expectCached:     12000,
		},
		{
			name: "response_without_token_usage",
			response: map[string]any{
				"agent_response": map[string]any{
					"title": "Analysis Result",
				},
			},
			expectAgentResp:  "Analysis Result",
			expectTokenUsage: false,
		},
		{
			name: "response_with_zero_tokens",
			response: map[string]any{
				"agent_response": map[string]any{"title": "test"},
				"token_usage": map[string]any{
					"prompt_tokens":     float64(0),
					"completion_tokens": float64(0),
					"total_tokens":      float64(0),
					"model":             "gemini-2.5-pro",
					"provider":          "googleai",
				},
			},
			expectAgentResp:  "test",
			expectTokenUsage: true,
			expectModel:      "gemini-2.5-pro",
			expectProvider:   "googleai",
			expectPrompt:     0,
		},
		{
			name: "nil_agent_response_returns_full_body",
			response: map[string]any{
				"success":        false,
				"error":          "analysis failed",
				"agent_response": nil,
				"token_usage": map[string]any{
					"prompt_tokens": float64(1000),
					"total_tokens":  float64(1000),
					"model":         "gemini-2.5-pro",
					"provider":      "googleai",
				},
			},
			expectAgentResp:  "analysis failed",
			expectTokenUsage: true,
			expectModel:      "gemini-2.5-pro",
			expectProvider:   "googleai",
			expectPrompt:     1000,
		},
		{
			name: "missing_agent_response_returns_full_body",
			response: map[string]any{
				"success": false,
				"error":   "timeout",
			},
			expectAgentResp:  "timeout",
			expectTokenUsage: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			respBytes, err := json.Marshal(tt.response)
			require.NoError(t, err)

			result := extractAgentResponseWithTokenUsage(respBytes)

			// Verify agent response
			assert.Contains(t, result.AgentResponse, tt.expectAgentResp)

			// Verify token usage
			if tt.expectTokenUsage {
				require.NotNil(t, result.TokenUsage, "expected token usage to be present")
				assert.Equal(t, tt.expectModel, result.TokenUsage.Model)
				assert.Equal(t, tt.expectProvider, result.TokenUsage.Provider)
				assert.Equal(t, tt.expectPrompt, result.TokenUsage.PromptTokens)
				assert.Equal(t, tt.expectCompletion, result.TokenUsage.CompletionTokens)
				assert.Equal(t, tt.expectCached, result.TokenUsage.CachedContentTokens)
			} else {
				assert.Nil(t, result.TokenUsage, "expected no token usage")
			}
		})
	}
}

// TestFuzzyMatchRepo verifies workload-name-to-repo fuzzy matching logic.

func TestFuzzyMatchRepo(t *testing.T) {
	tests := []struct {
		name         string
		workloadName string
		projectURLs  []string
		expected     string
	}{
		{
			name:         "exact repo name match",
			workloadName: "ticket-server",
			projectURLs:  []string{"https://github.com/org/ticket-server", "https://github.com/org/nudgebee-infra"},
			expected:     "https://github.com/org/ticket-server",
		},
		{
			name:         "single non-infra repo after filtering - no name match",
			workloadName: "llm-server",
			projectURLs:  []string{"https://github.com/org/nudgebee", "https://github.com/org/nudgebee-infra"},
			expected:     "https://github.com/org/nudgebee", // infra filtered, only one non-infra left
		},
		{
			name:         "filters out infra repos",
			workloadName: "api-server",
			projectURLs:  []string{"https://github.com/org/nudgebee-infra", "https://github.com/org/nudgebee-infrastructure", "https://github.com/org/nudgebee"},
			expected:     "https://github.com/org/nudgebee",
		},
		{
			name:         "empty workload name returns first non-infra",
			workloadName: "",
			projectURLs:  []string{"https://github.com/org/repo1", "https://github.com/org/repo2"},
			expected:     "",
		},
		{
			name:         "single non-infra repo returned directly",
			workloadName: "anything",
			projectURLs:  []string{"https://github.com/org/nudgebee-infra", "https://github.com/org/nudgebee"},
			expected:     "https://github.com/org/nudgebee",
		},
		{
			name:         "repo name contains workload name",
			workloadName: "collector",
			projectURLs:  []string{"https://github.com/org/cloud-collector-service", "https://github.com/org/nudgebee"},
			expected:     "https://github.com/org/cloud-collector-service",
		},
		{
			name:         "all infra repos returns empty",
			workloadName: "my-service",
			projectURLs:  []string{"https://github.com/org/infra", "https://github.com/org/helm-charts"},
			expected:     "",
		},
		{
			name:         "handles .git suffix in URLs",
			workloadName: "ticket-server",
			projectURLs:  []string{"https://github.com/org/ticket-server.git", "https://github.com/org/other.git"},
			expected:     "https://github.com/org/ticket-server.git",
		},
		{
			name:         "filters devops repos",
			workloadName: "api-server",
			projectURLs:  []string{"https://github.com/org/devops", "https://github.com/org/nudgebee"},
			expected:     "https://github.com/org/nudgebee",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := fuzzyMatchRepo(tt.workloadName, tt.projectURLs)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestValidateRepoSelection verifies LLM-response normalization and candidate matching.

func TestValidateRepoSelection(t *testing.T) {
	candidates := []string{
		"https://github.com/nudgebee/nudgebee",
		"https://github.com/nudgebee/k8s-agent",
		"https://gitlab.com/group/project",
	}
	tests := []struct {
		name     string
		response string
		expected string
	}{
		{"exact match", "https://github.com/nudgebee/nudgebee", "https://github.com/nudgebee/nudgebee"},
		{"trims whitespace", "  https://github.com/nudgebee/k8s-agent  ", "https://github.com/nudgebee/k8s-agent"},
		{"strips quotes", `"https://github.com/nudgebee/nudgebee"`, "https://github.com/nudgebee/nudgebee"},
		{"strips backticks", "`https://github.com/nudgebee/nudgebee`", "https://github.com/nudgebee/nudgebee"},
		{"trailing slash tolerated", "https://github.com/nudgebee/nudgebee/", "https://github.com/nudgebee/nudgebee"},
		{"uncertain returns empty", "UNCERTAIN", ""},
		{"uncertain mixed case", "Uncertain", ""},
		{"none returns empty", "none", ""},
		{"empty returns empty", "", ""},
		{"non-candidate returns empty", "https://github.com/other/repo", ""},
		{"explanation prefix not tolerated (LLM must reply only URL)", "I think it's https://github.com/nudgebee/nudgebee", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, validateRepoSelection(tt.response, candidates))
		})
	}
}

// TestTruncateForPrompt verifies bounded truncation with a marker.

func TestTruncateForPrompt(t *testing.T) {
	assert.Equal(t, "short", truncateForPrompt("short", 100))
	assert.Equal(t, "short", truncateForPrompt("short", 5))
	assert.Equal(t, "abc", truncateForPrompt("abcdefgh", 3)) // below marker overhead
	long := strings.Repeat("a", 50)
	out := truncateForPrompt(long, 20)
	assert.Equal(t, 20, len(out))
	assert.True(t, strings.HasSuffix(out, " [...]"))
	assert.Equal(t, "", truncateForPrompt("", 100))
	assert.Equal(t, "abc", truncateForPrompt("abc", 0)) // 0 = no limit
	// UTF-8 safety: multi-byte runes must not be split mid-byte.
	utf8Input := strings.Repeat("é", 50) // each 'é' is 2 bytes in UTF-8
	utf8Out := truncateForPrompt(utf8Input, 20)
	assert.Equal(t, 20, len([]rune(utf8Out)))
	assert.True(t, strings.HasSuffix(utf8Out, " [...]"))
	// Result must be valid UTF-8 — round-trip via []rune confirms no broken sequences.
	assert.Equal(t, utf8Out, string([]rune(utf8Out)))
}

// TestIsIrrelevantAnalysis verifies detection of off-topic analysis responses.

func TestIsIrrelevantAnalysis(t *testing.T) {
	tests := []struct {
		name     string
		response string
		expected bool
	}{
		{
			name:     "standard relevance check message",
			response: `{"description":"The automated analysis may not be directly addressing your specific issue. The agent failed to..."}`,
			expected: true,
		},
		{
			name:     "genuine analysis",
			response: `{"title":"Bug Fix","description":"Fixed null pointer in config parsing","root_cause_analysis":"..."}`,
			expected: false,
		},
		{
			name:     "empty response",
			response: "",
			expected: false,
		},
		{
			name:     "pr info response",
			response: `{"automated_fix_pr_info":{"branch":"fix/something","number":42}}`,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, isIrrelevantAnalysis(tt.response))
		})
	}
}

// TestConversationRetryGuard verifies the cache-based retry prevention is message-scoped.

func TestConversationRetryGuard(t *testing.T) {
	conversationId := "test-conv-retry-guard"
	messageId1 := "msg-1"
	messageId2 := "msg-2"
	guardKey1 := conversationId + ":" + messageId1
	guardKey2 := conversationId + ":" + messageId2

	// Ensure cleanup even if assertions fail mid-test
	t.Cleanup(func() {
		_ = common.CacheDelete(codeAgentFailuresCacheNS, guardKey1)
		_ = common.CacheDelete(codeAgentFailuresCacheNS, guardKey2)
	})

	// Clean state
	_ = common.CacheDelete(codeAgentFailuresCacheNS, guardKey1)
	_ = common.CacheDelete(codeAgentFailuresCacheNS, guardKey2)

	// First call — no guard, should not be blocked
	_, ok := common.CacheGet(codeAgentFailuresCacheNS, guardKey1)
	assert.False(t, ok, "should not have a failure entry before first call")

	// Simulate a failure for message 1
	_ = common.CacheSet(codeAgentFailuresCacheNS, guardKey1, []byte("analysis was not relevant"))

	// Same message — should be blocked
	reason, ok := common.CacheGet(codeAgentFailuresCacheNS, guardKey1)
	assert.True(t, ok, "should have a failure entry for message 1")
	assert.Equal(t, "analysis was not relevant", string(reason))

	// Different message in same conversation — should NOT be blocked
	_, ok = common.CacheGet(codeAgentFailuresCacheNS, guardKey2)
	assert.False(t, ok, "should not block a different message in the same conversation")

	// Simulate success on message 1 — clears guard for that message only
	_ = common.CacheDelete(codeAgentFailuresCacheNS, guardKey1)
	_, ok = common.CacheGet(codeAgentFailuresCacheNS, guardKey1)
	assert.False(t, ok, "should be cleared after success")
}

// TestExtractAgentResponseWithTokenUsage_InvalidJSON verifies graceful fallback on bad input.

func TestExtractAgentResponseWithTokenUsage_InvalidJSON(t *testing.T) {
	result := extractAgentResponseWithTokenUsage([]byte("not json at all"))
	assert.Equal(t, "not json at all", result.AgentResponse)
	assert.Nil(t, result.TokenUsage)
}

// TestRecordCodeAnalysisTokenUsage_NilAndZero verifies no-op for nil/zero token usage.

func TestRecordCodeAnalysisTokenUsage_NilAndZero(t *testing.T) {
	query := core.NBAgentRequest{AccountId: "test"}

	// Should not panic on nil
	recordCodeAnalysisTokenUsage(query, nil, 1.0)

	// Should not panic on zero tokens
	recordCodeAnalysisTokenUsage(query, &codeAnalysisTokenUsage{}, 1.0)
}

// joinErrors is a test helper that joins error strings.

func joinErrors(errors []string) string {
	result := ""
	for i, e := range errors {
		if i > 0 {
			result += "\n"
		}
		result += e
	}
	return result
}
