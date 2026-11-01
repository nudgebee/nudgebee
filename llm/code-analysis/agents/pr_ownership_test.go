package agents

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"nudgebee/code-analysis-agent/internal/gitprovider"
	"nudgebee/code-analysis-agent/internal/ownership"
	"nudgebee/code-analysis-agent/tools/core"
)

func TestChangedOwnershipPaths(t *testing.T) {
	require.Equal(t, []string{"a.go", "old name.go", "new name.go", "deleted.go", "new.go"}, changedOwnershipPaths("M\x00a.go\x00R100\x00old name.go\x00new name.go\x00D\x00deleted.go\x00A\x00new.go\x00M\x00a.go\x00"))
	require.Empty(t, changedOwnershipPaths(""))
}

type fakePRFinder struct {
	url string
	err error
}

func (f fakePRFinder) Find(context.Context, string, string) (string, error) { return f.url, f.err }

func TestCreateOwnedPRRecovery(t *testing.T) {
	for _, tc := range []struct {
		name    string
		finder  prFinder
		message string
		calls   int
		success bool
	}{
		{"partial create", fakePRFinder{url: "https://github.com/acme/repo/pull/1"}, "could not add assignee", 1, true},

		{"ambiguous transport failure", fakePRFinder{err: ownership.ErrNotFound}, "network failure", 1, false},
		{"recovery unavailable", fakePRFinder{err: fmt.Errorf("unavailable")}, "assignee failed", 1, false},
		{"nil client", (*ownership.Client)(nil), "assignee failed", 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			args := []any{"pr", "create", "--head", "fix"}
			r := createOwnedPR(context.Background(), tc.finder, "fix", "main", "/repo", args, func(_ context.Context, input map[string]any) core.NBToolResponse {
				calls++
				require.Equal(t, "/repo", input["working_directory"])
				if calls == 1 {
					require.Equal(t, args, input["args"])
					return core.CreateErrorResponse(tc.message, tc.message)
				}
				require.Equal(t, []any{"pr", "create", "--head", "fix"}, input["args"])
				return core.CreateSuccessResponse("url", "url", nil)
			})
			require.Equal(t, tc.calls, calls)
			require.Equal(t, tc.success, r.Status == "success")
		})
	}
}

func TestUnresolvedOwnerPublishesWithoutAssignee(t *testing.T) {
	for _, command := range []string{"pr", "mr"} {
		calls := 0
		r := createOwnedPR(context.Background(), nil, "fix", "main", "/repo", []any{command, "create"}, func(_ context.Context, input map[string]any) core.NBToolResponse {
			calls++
			require.Equal(t, []any{command, "create"}, input["args"])
			return core.CreateSuccessResponse("url", "url", nil)
		})
		require.Equal(t, "success", r.Status)
		require.Equal(t, 1, calls)
	}
}

func TestMissingOwnershipSessionIsBestEffort(t *testing.T) {
	a := &OrchestratorAgent{}
	c, r := a.resolvePROwnership(context.Background(), nil, "/unused", "base", gitprovider.GitProviderGitHub)
	require.Nil(t, c)
	require.Equal(t, "unresolved", r.Status)
	require.NotEmpty(t, r.Warning)
}
