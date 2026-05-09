package common

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

// resetInMemoryPendingTokens clears the package-level fallback map.
// Each test that exercises the in-memory path should call this so it
// doesn't observe state leaked by a sibling test.
func resetInMemoryPendingTokens() {
	inMemoryPendingTokensMu.Lock()
	defer inMemoryPendingTokensMu.Unlock()
	inMemoryPendingTokens = make(map[string][]string)
}

func TestPendingTokens_RegisterAndDrain(t *testing.T) {
	resetInMemoryPendingTokens()
	ctx := context.Background()

	// Empty event-id / token return errors.
	assert.Error(t, RegisterPendingToken(ctx, "", "tok"))
	assert.Error(t, RegisterPendingToken(ctx, "evt", ""))
	if _, err := DrainPendingTokens(ctx, ""); err == nil {
		t.Fatalf("expected error draining empty event-id")
	}

	// Drain on never-registered event returns empty slice + no error.
	got, err := DrainPendingTokens(ctx, "no-such-event")
	assert.NoError(t, err)
	assert.Empty(t, got)

	// Single register → single drain returns the token.
	assert.NoError(t, RegisterPendingToken(ctx, "evt-1", "tok-a"))
	got, err = DrainPendingTokens(ctx, "evt-1")
	assert.NoError(t, err)
	assert.Equal(t, []string{"tok-a"}, got)

	// After drain, a fresh drain returns empty.
	got, err = DrainPendingTokens(ctx, "evt-1")
	assert.NoError(t, err)
	assert.Empty(t, got)
}

func TestPendingTokens_MultipleTokensSameEvent(t *testing.T) {
	resetInMemoryPendingTokens()
	ctx := context.Background()

	for _, tok := range []string{"a", "b", "c"} {
		assert.NoError(t, RegisterPendingToken(ctx, "evt-2", tok))
	}
	got, err := DrainPendingTokens(ctx, "evt-2")
	assert.NoError(t, err)
	assert.ElementsMatch(t, []string{"a", "b", "c"}, got)

	got, err = DrainPendingTokens(ctx, "evt-2")
	assert.NoError(t, err)
	assert.Empty(t, got)
}

func TestPendingTokens_RemoveSpecific(t *testing.T) {
	resetInMemoryPendingTokens()
	ctx := context.Background()

	for _, tok := range []string{"a", "b", "c"} {
		assert.NoError(t, RegisterPendingToken(ctx, "evt-3", tok))
	}

	// Remove the middle token; the other two stay.
	popped, err := RemovePendingToken(ctx, "evt-3", "b")
	assert.NoError(t, err)
	assert.True(t, popped)

	got, _ := DrainPendingTokens(ctx, "evt-3")
	assert.ElementsMatch(t, []string{"a", "c"}, got)

	// Removing a token that's already gone returns false, no error.
	popped, err = RemovePendingToken(ctx, "evt-3", "b")
	assert.NoError(t, err)
	assert.False(t, popped)

	// Argument validation.
	if _, err := RemovePendingToken(ctx, "", "tok"); err == nil {
		t.Fatalf("expected error for empty event-id")
	}
	if _, err := RemovePendingToken(ctx, "evt", ""); err == nil {
		t.Fatalf("expected error for empty token")
	}
}

func TestPendingTokens_RemoveLeavesMapEmptyOnLastRemoval(t *testing.T) {
	// Confirms in-memory bookkeeping doesn't leak per-event slices.
	resetInMemoryPendingTokens()
	ctx := context.Background()

	assert.NoError(t, RegisterPendingToken(ctx, "evt-4", "only"))
	popped, err := RemovePendingToken(ctx, "evt-4", "only")
	assert.NoError(t, err)
	assert.True(t, popped)

	inMemoryPendingTokensMu.Lock()
	_, present := inMemoryPendingTokens["evt-4"]
	inMemoryPendingTokensMu.Unlock()
	assert.False(t, present, "expected map entry to be deleted after last token removed")
}

func TestPendingTokens_ConcurrentRegisters(t *testing.T) {
	// Mostly confirms the in-memory mutex guards correctly. Redis path
	// is RPUSH which is atomic on the server side.
	resetInMemoryPendingTokens()
	ctx := context.Background()

	const writers = 10
	const perWriter = 50
	var wg sync.WaitGroup
	wg.Add(writers)
	for i := 0; i < writers; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < perWriter; j++ {
				_ = RegisterPendingToken(ctx, "evt-concurrent", "writer-x")
			}
		}(i)
	}
	wg.Wait()

	got, err := DrainPendingTokens(ctx, "evt-concurrent")
	assert.NoError(t, err)
	assert.Equal(t, writers*perWriter, len(got))
}

func TestPendingTokens_DrainIsolatesByEventID(t *testing.T) {
	resetInMemoryPendingTokens()
	ctx := context.Background()

	assert.NoError(t, RegisterPendingToken(ctx, "evt-A", "a1"))
	assert.NoError(t, RegisterPendingToken(ctx, "evt-A", "a2"))
	assert.NoError(t, RegisterPendingToken(ctx, "evt-B", "b1"))

	gotA, _ := DrainPendingTokens(ctx, "evt-A")
	assert.ElementsMatch(t, []string{"a1", "a2"}, gotA)

	// evt-B's tokens untouched.
	gotB, _ := DrainPendingTokens(ctx, "evt-B")
	assert.Equal(t, []string{"b1"}, gotB)
}
