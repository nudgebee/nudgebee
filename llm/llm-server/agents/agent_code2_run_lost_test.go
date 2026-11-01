package agents

import (
	"errors"
	"fmt"
	"testing"

	"nudgebee/llm/workspace"

	"github.com/stretchr/testify/assert"
)

// shouldSkipRetryGuard decides whether a workspace failure is exempt from the
// message-scoped "may still be running" guard. Only a confirmed pod-loss
// (workspace.ErrWorkspaceRunLost, possibly wrapped by the poll loop's %w
// chain) should skip it; an ordinary/ambiguous failure must still set the
// guard so the planner does not dispatch a second /analyze next to one that
// may still be running (#38994).
func TestShouldSkipRetryGuard(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "bare ErrWorkspaceRunLost",
			err:  workspace.ErrWorkspaceRunLost,
			want: true,
		},
		{
			name: "ErrWorkspaceRunLost wrapped through the poll loop's error chain",
			err:  fmt.Errorf("analysis polling abandoned after %d consecutive errors: %w", 12, workspace.ErrWorkspaceRunLost),
			want: true,
		},
		{
			name: "ErrWorkspaceRunLost returned at once by the poll loop",
			err:  fmt.Errorf("analysis lost with its workspace pod: %w", fmt.Errorf("%w: workspace pod is not ready: pod has failed", workspace.ErrWorkspaceRunLost)),
			want: true,
		},
		{
			name: "an ordinary timeout is not exempt",
			err:  errors.New("context deadline exceeded"),
			want: false,
		},
		{
			name: "a real analysis-reported failure is not exempt",
			err:  errors.New("analysis failed: build failed: exit status 1"),
			want: false,
		},
		{
			name: "nil error",
			err:  nil,
			want: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, shouldSkipRetryGuard(tc.err))
		})
	}
}
