package tools

import (
	"encoding/json"
	"strings"
	"testing"

	"nudgebee/llm/config"
	"nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
)

func TestGithubErrorHint(t *testing.T) {
	tests := []struct {
		name      string
		rawError  string
		wantEmpty bool
		contains  []string
	}{
		{
			name:     "unknown json field steers to graphql timelineItems",
			rawError: "Unknown JSON field: \"pullRequests\"\nAvailable fields:\n  assignees\n  author\n  labels\n  number",
			contains: []string{"Available fields", "timelineItems", "gh <subcommand> --help", "pullRequests"},
		},
		{
			name:     "unknown flag steers to jq and help",
			rawError: "unknown flag: --sort\n\nUsage:  gh issue list [flags]",
			contains: []string{"--sort", "--jq", "gh <subcommand> --help"},
		},
		{
			name:     "unknown shorthand flag is also handled",
			rawError: "unknown shorthand flag: 'x' in -x",
			contains: []string{"gh <subcommand> --help"},
		},
		{
			name:      "unrecognized error preserves raw output (no hint)",
			rawError:  "HTTP 404: Not Found (https://api.github.com/repos/x/y)",
			wantEmpty: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := githubErrorHint(tt.rawError)
			if tt.wantEmpty {
				assert.Empty(t, got)
				return
			}
			assert.NotEmpty(t, got)
			for _, want := range tt.contains {
				assert.Contains(t, got, want)
			}
		})
	}
}

func TestWrapCliError(t *testing.T) {
	t.Run("empty hint returns raw error unchanged", func(t *testing.T) {
		raw := "unknown flag: --sort"
		assert.Equal(t, raw, wrapCliError(raw, ""))
		assert.Equal(t, raw, wrapCliError(raw, "   "))
	})

	t.Run("non-empty hint wraps into json envelope", func(t *testing.T) {
		raw := "Unknown JSON field: \"pullRequests\""
		hint := "use graphql timelineItems"
		out := wrapCliError(raw, hint)

		assert.True(t, strings.HasPrefix(out, "{"), "expected JSON envelope, got %q", out)
		var env map[string]string
		assert.NoError(t, json.Unmarshal([]byte(out), &env))
		assert.Equal(t, hint, env["error_hint"])
		assert.Equal(t, raw, env["original_error"])
	})
}

func TestGithubReply(t *testing.T) {
	graphqlErr := `{"errors":[{"message":"Could not resolve to a Repository"}],"data":null}`
	t.Run("log mode keeps success and does not change the reply", func(t *testing.T) {
		got := githubReply(core.NbToolContext{}, graphqlErr)
		assert.Equal(t, core.NBToolResponseStatusSuccess, got.Status)
		assert.Equal(t, graphqlErr, got.Data)
	})
	t.Run("enforced tool fails a reply with a top-level errors field", func(t *testing.T) {
		prev := config.Config.LlmServerToolReportedFailureEnforceTools
		config.Config.LlmServerToolReportedFailureEnforceTools = ToolExecuteGithubCliCommand
		t.Cleanup(func() { config.Config.LlmServerToolReportedFailureEnforceTools = prev })

		got := githubReply(core.NbToolContext{}, graphqlErr)
		assert.Equal(t, core.NBToolResponseStatusError, got.Status)
		assert.Equal(t, core.FailureReasonReportedFailed, got.Metadata.FailureReason)

		for name, body := range map[string]string{
			"json array":           `[{"number":1,"title":"error handling"}]`,
			"clean object":         `{"data":{"repository":{"name":"x"}},"errors":null}`,
			"workflow run":         `{"status":"completed","conclusion":"failure"}`,
			"plain text with word": "Error: this is log text, judged by exit code only",
		} {
			got := githubReply(core.NbToolContext{}, body)
			assert.Equal(t, core.NBToolResponseStatusSuccess, got.Status, name)
		}
	})
}
