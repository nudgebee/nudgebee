package api

import (
	"testing"
	"time"

	"nudgebee/llm/agents"
	"nudgebee/llm/services_server"

	"github.com/stretchr/testify/assert"
)

func TestParseChangePlanBlock(t *testing.T) {
	t.Run("absent block returns nil", func(t *testing.T) {
		assert.Nil(t, parseChangePlanBlock("investigation text with no plan"))
	})

	t.Run("multi-line change value is captured in full", func(t *testing.T) {
		plan := parseChangePlanBlock("prose\n<change_plan>\nkind: source\nfiles: a/b.go, c/d.go\nchange: line one\nline two\nline three\n</change_plan>\ntrailing")
		if assert.NotNil(t, plan) {
			assert.Equal(t, "source", plan.Kind)
			assert.Equal(t, []string{"a/b.go", "c/d.go"}, plan.Files)
			assert.Equal(t, "line one\nline two\nline three", plan.Change)
		}
	})

	t.Run("capitalised keys are accepted", func(t *testing.T) {
		plan := parseChangePlanBlock("<change_plan>\nKind: deployment\nFiles: deploy/values.yaml\nChange: bump memory\nTarget_Value: 512Mi\n</change_plan>")
		if assert.NotNil(t, plan) {
			assert.Equal(t, "deployment", plan.Kind)
			assert.Equal(t, []string{"deploy/values.yaml"}, plan.Files)
			assert.Equal(t, "bump memory", plan.Change)
			assert.Equal(t, "512Mi", plan.TargetValue)
		}
	})

	t.Run("files split on newlines and commas", func(t *testing.T) {
		plan := parseChangePlanBlock("<change_plan>\nkind: source\nfiles: a.go,\n b.go\n c.go\nchange: x\n</change_plan>")
		if assert.NotNil(t, plan) {
			assert.Equal(t, []string{"a.go", "b.go", "c.go"}, plan.Files)
		}
	})

	t.Run("files split on newlines and commas with list prefixes", func(t *testing.T) {
		plan := parseChangePlanBlock("<change_plan>\nkind: source\nfiles: - a.go,\n * b.go\n c.go\nchange: x\n</change_plan>")
		if assert.NotNil(t, plan) {
			assert.Equal(t, []string{"a.go", "b.go", "c.go"}, plan.Files)
		}
	})

	t.Run("unknown kind is preserved and rejected before fixing", func(t *testing.T) {
		plan := parseChangePlanBlock("<change_plan>\nkind: infra\nchange: x\n</change_plan>")
		if assert.NotNil(t, plan) {
			assert.Equal(t, "infra", plan.Kind)
			assert.NotEmpty(t, validateCodeChangePlan(plan))
		}
	})

	t.Run("neither change nor files returns nil", func(t *testing.T) {
		assert.Nil(t, parseChangePlanBlock("<change_plan>\nkind: source\n</change_plan>"))
	})

	// #38488: two vulnerability-server events produced diffs that were then
	// rejected for "not setting the value the investigation asked for" — the
	// value was a placeholder the agent never filled in.
	t.Run("placeholder target value is dropped", func(t *testing.T) {
		plan := parseChangePlanBlock("<change_plan>\nkind: deployment\nfiles: deploy/values-dev.yaml\nchange: use a real tag\ntarget_value: <valid-image-tag>\n</change_plan>")
		if assert.NotNil(t, plan) {
			assert.Empty(t, plan.TargetValue)
		}
	})
}

func TestCodeAgentOutcome(t *testing.T) {
	citations := []any{map[string]any{
		"file_path":  "svc/gateway/ratelimit/redis.go",
		"line_start": float64(71), "line_end": float64(88),
		"note":    "retry with backoff",
		"snippet": "common.RetryWithBackoff(...)",
	}}

	t.Run("no_op keeps the agent's answer and citations", func(t *testing.T) {
		outcome, explanation := codeAgentOutcome(
			map[string]any{
				"answer":                        "already implemented",
				"citations":                     citations,
				agents.CodeAnalysisNoOpBasisKey: agents.CodeAnalysisNoOpBasisSpecialist,
			},
			EventAnalysisResponse{ExecutionStatus: "no_op"},
		)
		assert.Equal(t, codeOutcomeNoOp, outcome)
		assert.Contains(t, explanation, "already implemented")
		assert.Contains(t, explanation, "redis.go`:71-88")
		assert.Contains(t, explanation, "RetryWithBackoff")
	})

	// An unstamped response must never be read as "the change is already there".
	t.Run("missing execution_status is failed, not no_op", func(t *testing.T) {
		outcome, explanation := codeAgentOutcome(
			map[string]any{"description": "ran out of steps"},
			EventAnalysisResponse{},
		)
		assert.Equal(t, codeOutcomeFailed, outcome)
		assert.Contains(t, explanation, "ran out of steps")
	})

	t.Run("empty response yields no explanation so the caller skips", func(t *testing.T) {
		outcome, explanation := codeAgentOutcome(
			map[string]any{agents.CodeAnalysisNoOpBasisKey: agents.CodeAnalysisNoOpBasisSpecialist},
			EventAnalysisResponse{ExecutionStatus: "no_op"},
		)
		assert.Equal(t, codeOutcomeNoOp, outcome)
		assert.Empty(t, explanation)
	})

	// The specialist saying so is what makes a no-op mean the code already does
	// this. A run that proposed nothing checked nothing, and the investigate page
	// must not show it as a clean result.
	t.Run("a no_op without a verdict is inconclusive, not already present", func(t *testing.T) {
		for _, basis := range []string{"no_implementation_instructions", "", "something_new"} {
			resp := map[string]any{"description": "produced no instructions"}
			if basis != "" {
				resp[agents.CodeAnalysisNoOpBasisKey] = basis
			}
			outcome, explanation := codeAgentOutcome(resp, EventAnalysisResponse{ExecutionStatus: "no_op"})
			assert.Equal(t, codeOutcomeFailed, outcome, "basis %q", basis)
			assert.Contains(t, explanation, "produced no instructions")
		}
	})
}

func TestCodeFixSummary(t *testing.T) {
	t.Run("no_op tells synthesis the change is already present", func(t *testing.T) {
		s := codeFixSummary(EventAnalysisResponse{SourceUpdates: map[string]any{
			"outcome": codeOutcomeNoOp, "explanation": "retry exists in redis.go",
		}})
		assert.Contains(t, s, "already present")
		assert.Contains(t, s, "retry exists in redis.go")
	})

	t.Run("failed tells synthesis the code was not verified", func(t *testing.T) {
		s := codeFixSummary(EventAnalysisResponse{SourceUpdates: map[string]any{
			"outcome": codeOutcomeFailed, "explanation": "planner exhausted iterations",
		}})
		assert.Contains(t, s, "could not be completed")
	})

	t.Run("success passes the analysis through unchanged", func(t *testing.T) {
		s := codeFixSummary(EventAnalysisResponse{
			Analysis:      "the diff explanation",
			SourceUpdates: map[string]any{"outcome": codeOutcomeSuccess},
		})
		assert.Equal(t, "the diff explanation", s)
	})
}

func TestFilterCitationsToFiles(t *testing.T) {
	list := []any{
		map[string]any{"file_path": "svc/gateway/ratelimit/redis.go"},
		map[string]any{"file_path": "some/other/file.go"},
		map[string]any{"file_path": "svc/worker/redis.go"},
	}

	t.Run("matches directory-qualified suffix without admitting a namesake", func(t *testing.T) {
		kept, ok := filterCitationsToFiles(list, []string{"gateway/ratelimit/redis.go"}).([]any)
		if assert.True(t, ok) && assert.Len(t, kept, 1) {
			assert.Equal(t, "svc/gateway/ratelimit/redis.go", kept[0].(map[string]any)["file_path"])
		}
	})

	t.Run("no plan files keeps everything", func(t *testing.T) {
		kept, ok := filterCitationsToFiles(list, nil).([]any)
		assert.True(t, ok)
		assert.Len(t, kept, 3)
	})
	t.Run("bare filename does not match arbitrary directories", func(t *testing.T) {
		assert.Empty(t, filterCitationsToFiles(list, []string{"redis.go"}))
	})
	t.Run("absolute workspace path retains target directories", func(t *testing.T) {
		citations := []any{map[string]any{"file_path": "/tmp/repo/svc/./gateway/redis.go"}}
		assert.Len(t, filterCitationsToFiles(citations, []string{"svc/gateway/redis.go"}), 1)
		assert.Empty(t, filterCitationsToFiles(citations, []string{"other/gateway/redis.go"}))
	})
}

func TestValidateCodeChangePlan(t *testing.T) {
	for _, tc := range []struct {
		name  string
		plan  *codeChangePlan
		valid bool
	}{
		{"absent", nil, false},
		{"instructions only", &codeChangePlan{Kind: "source", Change: "fix"}, false},
		{"files only", &codeChangePlan{Kind: "source", Files: []string{"a.go"}}, false},
		{"unknown kind", &codeChangePlan{Kind: "infra", Change: "fix", Files: []string{"a.go"}}, false},
		{"parent path", &codeChangePlan{Kind: "source", Change: "fix", Files: []string{"../a.go"}}, false},
		{"absolute path", &codeChangePlan{Kind: "source", Change: "fix", Files: []string{"/tmp/a.go"}}, false},
		{"empty path", &codeChangePlan{Kind: "source", Change: "fix", Files: []string{" "}}, false},
		{"wildcard path", &codeChangePlan{Kind: "source", Change: "fix", Files: []string{"src/*.go"}}, false},
		{"placeholder instruction", &codeChangePlan{Kind: "source", Change: "<describe change>", Files: []string{"a.go"}}, false},
		{"source", &codeChangePlan{Kind: "source", Change: "fix", Files: []string{"svc/a.go"}}, true},
		{"deployment without numeric target", &codeChangePlan{Kind: "deployment", Change: "remove stale option", Files: []string{"values.yaml"}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.valid, validateCodeChangePlan(tc.plan) == "")
			// Stored plans must pass the same gate as freshly parsed plans.
			assert.Equal(t, tc.valid, validateCodeChangePlan(unmarshalCodeChangePlan(marshalCodeChangePlan(tc.plan))) == "")
		})
	}
}

func TestBuildFixModeQuery(t *testing.T) {
	t.Run("source plan is change plus located files", func(t *testing.T) {
		q := buildFixModeQuery(&codeChangePlan{
			Kind:   "source",
			Change: "trim the id",
			Files:  []string{"a/b.go", "c/d.go"},
		}, &services_server.EventCodeCapabilities{})
		assert.Equal(t, "trim the id\n\nFiles located during the investigation: a/b.go, c/d.go", q)
	})

	t.Run("deployment plan appends the values file and target", func(t *testing.T) {
		q := buildFixModeQuery(&codeChangePlan{
			Kind:        "deployment",
			Change:      "raise the limit",
			TargetValue: "512Mi",
		}, &services_server.EventCodeCapabilities{
			Deployment: &services_server.DeploymentCapability{
				ValuesPath: "deploy/values-dev.yaml",
			},
		})
		assert.Contains(t, q, "raise the limit")
		assert.Contains(t, q, "Edit the deployment values file deploy/values-dev.yaml to 512Mi")
	})

	// #38488: the fix agent's only input is this query, so the investigation's
	// citations have to ride along or it re-explores and can contradict it.
	t.Run("plan evidence is handed to the fix agent with the already-present rule", func(t *testing.T) {
		q := buildFixModeQuery(&codeChangePlan{
			Kind:     "source",
			Change:   "add a non-fatal fallback",
			Files:    []string{"a/b.go"},
			Evidence: "**Code examined**\n- `a/b.go`:10-20",
		}, &services_server.EventCodeCapabilities{})
		assert.Contains(t, q, "The investigation already read this code:")
		assert.Contains(t, q, "- `a/b.go`:10-20")
		assert.Contains(t, q, "implement the part that is missing")
	})
}

func TestStaleEvidenceReason(t *testing.T) {
	now := time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC)
	ago := func(d time.Duration) *time.Time { ts := now.Add(-d); return &ts }
	day := 24 * time.Hour

	assert.Empty(t, staleEvidenceReason(ago(2*day), now, 7*day), "recent event")
	assert.Empty(t, staleEvidenceReason(ago(7*day), now, 7*day), "exactly at the cutoff")
	assert.Empty(t, staleEvidenceReason(nil, now, 7*day), "unknown start")
	assert.Empty(t, staleEvidenceReason(ago(180*day), now, 0), "cutoff disabled")
	// event eab45d8f: fired 2026-03-23, re-analysed 2026-09-22.
	assert.Contains(t, staleEvidenceReason(ago(183*day), now, 7*day), "183 days ago")
}
