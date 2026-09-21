package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// mergeAccountIds feeds invalidateIntegrationCaches. The case that matters is the
// third one: an update that shrinks an integration's account list must invalidate
// the accounts it dropped, which are exactly the ones NOT in the request.
func TestMergeAccountIds(t *testing.T) {
	cases := []struct {
		name      string
		requested []string
		affected  []string
		want      []string
	}{
		{
			name: "both empty yields empty, not nil",
			want: []string{},
		},
		{
			name:      "nothing unlinked passes the request through",
			requested: []string{"acc-1", "acc-2"},
			want:      []string{"acc-1", "acc-2"},
		},
		{
			// The bug this function exists for: the save keeps acc-1 and drops acc-2,
			// so acc-2 is absent from the request and must still be invalidated.
			name:      "unlinked account absent from the request is included",
			requested: []string{"acc-1"},
			affected:  []string{"acc-1", "acc-2"},
			want:      []string{"acc-1", "acc-2"},
		},
		{
			name:      "duplicates collapse, first-seen order wins",
			requested: []string{"acc-2", "acc-1"},
			affected:  []string{"acc-1", "acc-2", "acc-1"},
			want:      []string{"acc-2", "acc-1"},
		},
		{
			name:      "empty strings are dropped",
			requested: []string{"", "acc-1"},
			affected:  []string{""},
			want:      []string{"acc-1"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Never nil: the result is ranged over by the caller.
			assert.Equal(t, tc.want, mergeAccountIds(tc.requested, tc.affected))
		})
	}
}

// invalidateIntegrationCaches must no-op on an empty set rather than reach for
// RabbitMQ — a tenant-scoped integration (llm_gateway, ticketing) has no accounts.
func TestInvalidateIntegrationCachesEmptyIsNoOp(t *testing.T) {
	assert.NotPanics(t, func() {
		invalidateIntegrationCaches(nil, nil)
		invalidateIntegrationCaches(nil, []string{})
	}, "empty account set must return before touching ctx or any publisher")
}
