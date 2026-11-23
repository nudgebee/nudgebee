package api

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/events"
)

// Flag flips: recovery neither restarts nor duplicates an investigation that is running, whichever way the
// remediation loop flag flipped since it started.
func TestFlagFlip_RecoveryLeavesALiveRunAlone(t *testing.T) {
	for _, loopOn := range []bool{true, false} {
		f := newSessionsFixture(t)
		stubLoopOn(t, loopOn)
		var submitted int
		swap(t, &submitStuckAnalysisRecovery, func(context.Context, func()) error { submitted++; return nil })
		f.conversation("event-fp-1", core.ConversationStatusCompleted) // between two stages
		run := f.runRow(f.event, 10*time.Minute)                       // its rows look old to recovery
		unlock, acquired, err := tryEventAnalysisLock(context.Background(), f.db, f.account, "fp-1")
		require.NoError(t, err)
		require.True(t, acquired)

		// The run started with the flag the other way, and keeps marking its rows live.
		require.NoError(t, events.HeartbeatEventRun(context.Background(), f.db, f.account, f.event))
		require.NoError(t, syncStuckEventAnalyses())
		assert.Zero(t, submitted, "loop on=%v: a live run is not restarted", loopOn)

		// Its worker died: the lock is gone and the rows age out; recovery restarts it once. (Recovery reads the
		// row's age against the server clock in UTC, as the database stores it in production.)
		unlock()
		_, err = f.db.Exec(`UPDATE event_log_analysis SET updated_at = (now() AT TIME ZONE 'UTC') - interval '10 minutes' WHERE id = $1`, run.Id)
		require.NoError(t, err)
		require.NoError(t, syncStuckEventAnalyses())
		assert.Equal(t, 1, submitted, "loop on=%v: a dead run is restarted once", loopOn)
	}
}

// A second worker for the same alert while one runs (any flag state) does nothing: the pipeline lock.
func TestFlagFlip_SecondWorkerDoesNotRunTwice(t *testing.T) {
	f := newSessionsFixture(t)
	unlock, acquired, err := tryEventAnalysisLock(context.Background(), f.db, f.account, "fp-1")
	require.NoError(t, err)
	require.True(t, acquired)
	defer unlock()
	for _, loopOn := range []bool{true, false} {
		stubLoopOn(t, loopOn)
		_, again, err := tryEventAnalysisLock(context.Background(), f.db, f.account, "fp-1")
		require.NoError(t, err)
		assert.False(t, again, "loop on=%v", loopOn)
	}
}
