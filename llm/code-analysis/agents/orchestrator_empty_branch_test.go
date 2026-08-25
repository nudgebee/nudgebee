package agents

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A branch with no commits must be reported as a terminal no-op, distinguishable
// from a real failure.
//
// Pushing an empty branch used to produce
// "GraphQL: Head sha can't be blank, Base sha can't be blank, No commits" from
// the provider — an opaque API error for what is really "the change is already
// present". Recurring since at least April. The caller keys off this sentinel to
// set execution_status no_op instead of pr_creation_status failed, so a wrapped
// error must still be recognisable.
func TestErrNoCommitsToPublish_IsDistinguishable(t *testing.T) {
	require.Error(t, errNoCommitsToPublish)

	assert.True(t, errors.Is(errNoCommitsToPublish, errNoCommitsToPublish))
	assert.True(t, errors.Is(fmt.Errorf("createPullRequest: %w", errNoCommitsToPublish), errNoCommitsToPublish),
		"the sentinel must survive wrapping — the caller matches on it to report a no-op")

	assert.False(t, errors.Is(errors.New("failed to push branch"), errNoCommitsToPublish),
		"an unrelated failure must not be mistaken for a no-op")
}
