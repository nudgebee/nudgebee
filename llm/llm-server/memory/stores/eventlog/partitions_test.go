package eventlog

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"nudgebee/llm/common"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func partitionExists(t *testing.T, name string) bool {
	t.Helper()
	db, err := common.GetDatabaseManager(common.Metastore)
	require.NoError(t, err)
	var exists bool
	require.NoError(t, db.Db.Get(&exists,
		`SELECT EXISTS (SELECT 1 FROM pg_class WHERE relname = $1)`, name))
	return exists
}

// TestEnsurePartitions_Integration is the regression test for the outage this
// package caused: V707 seeded partitions only through 2026-08-01 and the
// creator job it named (memory_partition_maint) was never written, so every
// event insert from 2026-08-01 onward failed with 23514.
func TestEnsurePartitions_Integration(t *testing.T) {
	if os.Getenv("RUN_MEMORY_INTEGRATION") != "true" {
		t.Skip("set RUN_MEMORY_INTEGRATION=true to run")
	}
	if _, err := common.GetDatabaseManager(common.Metastore); err != nil {
		t.Skipf("metastore unreachable: %v", err)
	}

	const monthsAhead = 3
	require.NoError(t, EnsurePartitions(context.Background(), monthsAhead))

	start := time.Now().UTC()
	start = time.Date(start.Year(), start.Month(), 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i <= monthsAhead; i++ {
		name := partitionName(start.AddDate(0, i, 0))
		assert.True(t, partitionExists(t, name), "partition %s must exist after EnsurePartitions", name)
	}

	// Idempotent: a second run is a no-op, which is what lets every replica
	// call this at boot without coordination.
	assert.NoError(t, EnsurePartitions(context.Background(), monthsAhead), "second run must not error")
}

// TestAppend_SelfHealsMissingPartition_Integration drops the partition for a
// future month, writes an event dated into it, and asserts the write still
// lands. This is the backstop for a tenant enabled at runtime, which no
// boot-time provisioning can cover.
func TestAppend_SelfHealsMissingPartition_Integration(t *testing.T) {
	if os.Getenv("RUN_MEMORY_INTEGRATION") != "true" {
		t.Skip("set RUN_MEMORY_INTEGRATION=true to run")
	}
	db, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		t.Skipf("metastore unreachable: %v", err)
	}

	// A month far enough out that no real data lives there, so dropping and
	// recreating its partition can't disturb anything.
	target := time.Now().UTC().AddDate(0, 9, 0)
	target = time.Date(target.Year(), target.Month(), 15, 12, 0, 0, 0, time.UTC)
	name := partitionName(target)

	_, err = db.Db.Exec(fmt.Sprintf("DROP TABLE IF EXISTS %s", name))
	require.NoError(t, err, "clear target partition")
	require.False(t, partitionExists(t, name), "precondition: partition must be absent")

	// Deliberately no MemoryEventsPartitionMonthsAhead override: the target is
	// further out than the default window, so this only passes if the
	// self-heal provisions the partition for the event's own timestamp rather
	// than for a window anchored on now.
	tenant := "test-selfheal-" + target.Format("20060102150405")
	err = Append(Event{
		TenantID:  tenant,
		EventType: EventTypeFactExtracted,
		Payload:   MarshalPayload(map[string]any{"subject": "selfheal probe"}),
		ActorKind: "system",
		CreatedAt: target,
	})
	require.NoError(t, err, "Append must provision the missing partition and retry")
	assert.True(t, partitionExists(t, name), "partition %s must exist after self-heal", name)

	var n int
	require.NoError(t, db.Db.Get(&n,
		`SELECT count(*) FROM llm_memory_events WHERE tenant_id = $1`, tenant))
	assert.Equal(t, 1, n, "the event must actually be persisted, not just the partition created")

	// Remove only the row this test wrote; leave the partition in place since
	// memory_partition_maint would have created it anyway.
	_, err = db.Db.Exec(`DELETE FROM llm_memory_events WHERE tenant_id = $1`, tenant)
	require.NoError(t, err)
}

// TestEnsurePartitionForTime_RejectsImplausibleTimestamps covers the guard's
// reject paths, which return before any DB access — so unlike the rest of this
// file they need no metastore and run in CI. Without them the guard was only
// ever exercised on its accept path.
func TestEnsurePartitionForTime_RejectsImplausibleTimestamps(t *testing.T) {
	now := time.Now().UTC()
	for _, tc := range []struct {
		name string
		ts   time.Time
	}{
		{"zero", time.Time{}},
		{"far past", now.AddDate(-(partitionSanityYears + 1), 0, 0)},
		{"far future", now.AddDate(partitionSanityYears+1, 0, 0)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := EnsurePartitionForTime(context.Background(), tc.ts)
			require.Error(t, err, "implausible timestamp must be refused, not materialised as a partition")
			assert.Contains(t, err.Error(), "EnsurePartitionForTime")
		})
	}
}

// TestPartitionName_IsUTCAndMonthly pins the naming contract that
// RunEventsRotate's regex depends on: a non-UTC input must still yield the
// UTC month, or retention silently stops matching the partitions we create.
func TestPartitionName_IsUTCAndMonthly(t *testing.T) {
	// Both directions of the boundary shift, since the partition a row lands in
	// is decided by created_at in UTC, not by the caller's wall clock.
	plus530 := time.FixedZone("IST", 5*3600+1800)
	minus5 := time.FixedZone("EST", -5*3600)

	// Local 1 Sep 01:00 (+05:30) is 31 Aug 19:30 UTC — still August.
	assert.Equal(t, "llm_memory_events_2026_08",
		partitionName(time.Date(2026, 9, 1, 1, 0, 0, 0, plus530)))

	// Local 31 Aug 21:00 (-05:00) is 1 Sep 02:00 UTC — already September.
	assert.Equal(t, "llm_memory_events_2026_09",
		partitionName(time.Date(2026, 8, 31, 21, 0, 0, 0, minus5)))
}
