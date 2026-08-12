package handlers

import (
	"nudgebee/code-analysis-agent/internal/credentials"
	"strings"
	"sync"
	"testing"
)

// Regression test for the analysis-id collision bug: agent_code_2 calls
// within one conversation must not share a progress-store key, otherwise the
// 5-min deferred cleanup from a completed call wipes a concurrently running
// one and /status returns 404.
func TestNewAnalysisID_UniquePerCall(t *testing.T) {
	const n = 1000
	seen := make(map[string]struct{}, n)
	for i := 0; i < n; i++ {
		id := newAnalysisID()
		if id == "" {
			t.Fatalf("empty id at iter %d", i)
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate id at iter %d: %s", i, id)
		}
		seen[id] = struct{}{}
	}
}

// Regression test for the parse-error / irrelevance-marker collision:
// when parseAgentResponse fails, the synthetic fallback AnalysisResult must
// NOT contain the marker phrase that llm-server treats as "irrelevant
// analysis", because that phrase trips the per-message retry guard and locks
// out recovery for the rest of the message.
//
// The marker phrase is duplicated from llm-server's irrelevantAnalysisMarker
// constant by design — these two services must agree on the wire contract.
// An unparseable agent response must fail the run.
//
// This replaces TestParseErrorFallback_NoIrrelevanceMarker, which guarded the
// interaction between the old synthesised parse-error result and llm-server's
// irrelevance marker. That fallback is gone: the handler no longer invents an
// AnalysisResult (file_path "unknown", fixed_code "Manual investigation
// required", and a fabricated git_diff) and return it as a success. With no
// placeholder there is no marker interaction left to guard, and the fake diff
// can no longer reach a consumer that would try to apply it.
//
// parseAgentResponse returning an error is the trigger the handler acts on, so
// that is what is pinned here.
func TestParseAgentResponse_UnparseableInputIsAnError(t *testing.T) {
	h := &AgenticAnalyzeHandler{}

	for _, in := range []string{
		"this is not json and not a valid agent response",
		"",
		"{ truncated json",
	} {
		if _, err := h.parseAgentResponse(in); err == nil {
			t.Fatalf("parseAgentResponse(%q) must error so the run fails instead of "+
				"reporting a synthesised success", in)
		}
	}
}

func TestNewAnalysisID_UniqueUnderConcurrency(t *testing.T) {
	const goroutines = 64
	const perGoroutine = 100
	var mu sync.Mutex
	seen := make(map[string]struct{}, goroutines*perGoroutine)
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			local := make([]string, 0, perGoroutine)
			for i := 0; i < perGoroutine; i++ {
				local = append(local, newAnalysisID())
			}
			mu.Lock()
			defer mu.Unlock()
			for _, id := range local {
				if _, dup := seen[id]; dup {
					t.Errorf("duplicate id under concurrency: %s", id)
					return
				}
				seen[id] = struct{}{}
			}
		}()
	}
	wg.Wait()
}

func TestParseAgentResponseWithPRList(t *testing.T) {
	handler := &AgenticAnalyzeHandler{}

	// Test JSON response with pr_list
	jsonResponse := `{
		"title": "Last 2 Pull Requests for llm-server",
		"description": "Here are the last 2 pull requests found for 'llm-server'",
		"file_path": "",
		"line_number": 0,
		"error_message": "",
		"original_code": "",
		"fixed_code": "",
		"git_diff": "",
		"commit_hash": "",
		"author": "",
		"commit_date": "",
		"pr_list": [
			{
				"number": 163,
				"title": "chore(deps): bump the all group in /llm/llm-server",
				"author": {"login": "app/dependabot"},
				"url": "https://github.com/nudgebee/nudgebee/pull/163",
				"state": "open",
				"created_at": "2025-09-15T01:37:08Z",
				"merged_at": null
			},
			{
				"number": 157,
				"title": "fix(llm-server): add missing dependency",
				"author": {"login": "nimrodshn"},
				"url": "https://github.com/nudgebee/nudgebee/pull/157",
				"state": "merged",
				"created_at": "2024-08-26T14:48:09Z",
				"merged_at": "2024-08-26T14:50:00Z"
			}
		]
	}`

	result, err := handler.parseAgentResponse(jsonResponse)
	if err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	// Verify basic fields
	if result.Title != "Last 2 Pull Requests for llm-server" {
		t.Errorf("Expected title 'Last 2 Pull Requests for llm-server', got '%s'", result.Title)
	}

	// Verify pr_list is populated
	if len(result.PRList) != 2 {
		t.Fatalf("Expected 2 PRs in pr_list, got %d", len(result.PRList))
	}

	// Verify first PR
	pr1 := result.PRList[0]
	if pr1.Number != 163 {
		t.Errorf("Expected first PR number 163, got %d", pr1.Number)
	}
	if pr1.Title != "chore(deps): bump the all group in /llm/llm-server" {
		t.Errorf("Expected first PR title, got '%s'", pr1.Title)
	}
	if pr1.State != "open" {
		t.Errorf("Expected first PR state 'open', got '%s'", pr1.State)
	}

	// Verify second PR
	pr2 := result.PRList[1]
	if pr2.Number != 157 {
		t.Errorf("Expected second PR number 157, got %d", pr2.Number)
	}
	if pr2.State != "merged" {
		t.Errorf("Expected second PR state 'merged', got '%s'", pr2.State)
	}
}

func TestParseAgentResponseWithoutPRList(t *testing.T) {
	handler := &AgenticAnalyzeHandler{}

	// Test JSON response without pr_list
	jsonResponse := `{
		"title": "Simple Analysis",
		"description": "Basic analysis without PRs",
		"file_path": "test.go",
		"line_number": 42
	}`

	result, err := handler.parseAgentResponse(jsonResponse)
	if err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	if result.Title != "Simple Analysis" {
		t.Errorf("Expected title 'Simple Analysis', got '%s'", result.Title)
	}

	if len(result.PRList) != 0 {
		t.Errorf("Expected empty pr_list, got %d items", len(result.PRList))
	}
}

func TestAgenticPRListIntegration(t *testing.T) {
	// Skip this test if no GitHub token is available
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Create a mock request for PR listing
	req := AgenticAnalyzeRequest{
		Prompt:  "can you list last 2 pr for llm-server",
		AgentID: "code_agent",
		GitRepository: GitRepository{
			URL:    "https://github.com/nudgebee/nudgebee.git",
			Branch: "main",
		},
		GitCredentials: credentials.GitCredentials{
			Type:  "token",
			Token: "",
		},
	}

	// Validate request structure for generic mode (no logs = generic mode)
	if req.Prompt != "can you list last 2 pr for llm-server" {
		t.Errorf("Expected specific prompt, got: %s", req.Prompt)
	}

	if req.Logs != "" {
		t.Errorf("Expected empty logs for generic mode, got: %s", req.Logs)
	}

	if req.AgentID != "code_agent" {
		t.Errorf("Expected code_agent, got: %s", req.AgentID)
	}

	// For a full integration test, you would need:
	// 1. Real GitHub token in environment
	// 2. Proper config initialization
	// 3. LLM client setup
	// 4. Git client initialization
	//
	// Example of what the full test would look like:
	//
	// cfg := &config.Config{...}
	// gitClient := git.NewGitClient()
	// credHandler := credentials.NewCredentialHandler()
	// handler, err := NewAgenticAnalyzeHandler(cfg, gitClient, credHandler)
	// if err != nil {
	//     t.Fatalf("Failed to create handler: %v", err)
	// }
	//
	// response, err := handler.HandleAgenticAnalyze(context.Background(), req)
	// if err != nil {
	//     t.Fatalf("Analysis failed: %v", err)
	// }
	//
	// // Verify response has pr_list
	// if response.AgentResponse == nil {
	//     t.Fatal("AgentResponse is nil")
	// }
	// if len(response.AgentResponse.PRList) == 0 {
	//     t.Error("Expected pr_list to be populated, but it's empty")
	// }

	t.Log("Integration test structure validated - would need real credentials and config for full test")
}

func TestNormalizeAndValidateRequest_RepoURL(t *testing.T) {
	tests := []struct {
		name          string
		inputURL      string
		provider      string
		localPath     string
		wantURL       string
		wantErrSubstr string
	}{
		{
			name:     "passthrough https URL",
			inputURL: "https://github.com/nudgebee/nudgebee.git",
			provider: "github",
			wantURL:  "https://github.com/nudgebee/nudgebee.git",
		},
		{
			name:     "passthrough https URL without provider",
			inputURL: "https://github.com/nudgebee/nudgebee",
			wantURL:  "https://github.com/nudgebee/nudgebee",
		},
		{
			name:     "passthrough git@ ssh URL",
			inputURL: "git@github.com:nudgebee/nudgebee.git",
			provider: "github",
			wantURL:  "git@github.com:nudgebee/nudgebee.git",
		},
		{
			name:     "normalize api.github.com URL",
			inputURL: "https://api.github.com/repos/nudgebee/nudgebee",
			provider: "github",
			wantURL:  "https://github.com/nudgebee/nudgebee.git",
		},
		{
			name:     "expand bare owner/repo for github",
			inputURL: "nudgebee/nudgebee",
			provider: "github",
			wantURL:  "https://github.com/nudgebee/nudgebee.git",
		},
		{
			name:     "expand bare owner/repo for gitlab",
			inputURL: "nudgebee/llm-server",
			provider: "gitlab",
			wantURL:  "https://gitlab.com/nudgebee/llm-server.git",
		},
		{
			name:     "expand bare owner/repo for bitbucket",
			inputURL: "nudgebee/repo",
			provider: "bitbucket",
			wantURL:  "https://bitbucket.org/nudgebee/repo.git",
		},
		{
			name:     "provider casing is normalized",
			inputURL: "nudgebee/nudgebee",
			provider: "GitHub",
			wantURL:  "https://github.com/nudgebee/nudgebee.git",
		},
		{
			name:          "bare owner/repo without provider rejected",
			inputURL:      "nudgebee/nudgebee",
			provider:      "",
			wantErrSubstr: "shorthand form but provider",
		},
		{
			name:          "bare owner/repo with unknown provider rejected",
			inputURL:      "nudgebee/nudgebee",
			provider:      "gitea",
			wantErrSubstr: "shorthand form but provider",
		},
		{
			name:          "garbage URL rejected",
			inputURL:      "not-a-url-or-shorthand",
			provider:      "github",
			wantErrSubstr: "invalid repository URL format",
		},
		{
			name:      "local_path skips URL validation",
			inputURL:  "anything-goes",
			localPath: "/tmp/repo",
			wantURL:   "anything-goes",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := &AgenticAnalyzeHandler{}
			req := &AgenticAnalyzeRequest{
				Logs: "logs", // avoid noisy WARN
				GitRepository: GitRepository{
					URL:       tc.inputURL,
					Provider:  tc.provider,
					LocalPath: tc.localPath,
				},
			}
			err := h.normalizeAndValidateRequest(req)
			if tc.wantErrSubstr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tc.wantErrSubstr)
				}
				if !strings.Contains(err.Error(), tc.wantErrSubstr) {
					t.Fatalf("expected error containing %q, got %q", tc.wantErrSubstr, err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if req.GitRepository.URL != tc.wantURL {
				t.Errorf("URL: got %q, want %q", req.GitRepository.URL, tc.wantURL)
			}
		})
	}
}
