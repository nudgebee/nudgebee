package eventlog

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"nudgebee/llm/common"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

// pgDuplicateTable is raised when two replicas run CREATE TABLE IF NOT EXISTS
// for the same partition concurrently — the existence check and the create are
// not atomic, so the loser sees duplicate_table even with IF NOT EXISTS.
const pgDuplicateTable = "42P07"

// pgCheckViolation is what Postgres returns for
// "no partition of relation ... found for row". Used by Append to recognise a
// missing-partition insert failure and self-heal.
const pgCheckViolation = "23514"

// maxPartitionMonthsAhead bounds how far ahead provisioning will reach.
const maxPartitionMonthsAhead = 12

// partitionSanityYears bounds how far from now a caller-supplied timestamp may
// sit before EnsurePartitionForTime treats it as corrupt. Generous: the memory
// module itself only dates from 2026, and provisioning never reaches beyond
// maxPartitionMonthsAhead.
const partitionSanityYears = 5

// provisionTimeout bounds Append's self-heal (provision + retry) so a wedged
// metastore cannot stall a turn on the Observe write path.
const provisionTimeout = 10 * time.Second

// BootProvisionTimeout bounds the boot-time provisioning run. Exported so the
// maintenance package can share one definition rather than restate the number.
const BootProvisionTimeout = 30 * time.Second

// EnsurePartitions creates the monthly llm_memory_events partitions for the
// current month and the next monthsAhead months.
//
// This is the `memory_partition_maint` job the V707 migration's comment names
// but which was never implemented. llm_memory_events is PARTITION BY RANGE
// (created_at) with no DEFAULT partition, so a month with no partition rejects
// every insert with 23514 — and because Observe returns early on an append
// failure, the typed-store projection never runs either. A missing partition
// silently drops all memory writes, it doesn't merely lose the audit row.
//
// Idempotent, so every replica can run it at boot with no coordination.
func EnsurePartitions(ctx context.Context, monthsAhead int) error {
	if monthsAhead < 0 {
		monthsAhead = 0
	}
	// Cap the horizon so a misconfigured LLM_MEMORY_EVENTS_PARTITION_MONTHS_AHEAD
	// can't emit hundreds of DDL statements on every boot.
	if monthsAhead > maxPartitionMonthsAhead {
		monthsAhead = maxPartitionMonthsAhead
	}

	db, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return fmt.Errorf("eventlog.EnsurePartitions: db: %w", err)
	}

	// Anchor on the first of the current UTC month: AddDate on a day-of-month
	// past 28 would skip months (Jan 31 + 1 month = Mar 3).
	now := time.Now().UTC()
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)

	for i := 0; i <= monthsAhead; i++ {
		if err := ensurePartitionForTime(ctx, db, start.AddDate(0, i, 0)); err != nil {
			return fmt.Errorf("eventlog.EnsurePartitions: %w", err)
		}
	}

	slog.Info("memory-partition-maint: partitions ensured",
		"from", start.Format("2006-01"),
		"through", start.AddDate(0, monthsAhead, 0).Format("2006-01"))
	return nil
}

// EnsurePartitionForTime creates the single partition covering t, whenever t
// falls. Append's self-heal needs this rather than EnsurePartitions: the
// window EnsurePartitions provisions is anchored on time.Now(), so an event
// dated outside that window — a replayed or backfilled row with a historical
// timestamp, or one further ahead than MemoryEventsPartitionMonthsAhead —
// would still have no partition after provisioning, and the retry would drop
// the very event the self-heal exists to save.
func EnsurePartitionForTime(ctx context.Context, t time.Time) error {
	// Refuse timestamps that can't belong to a real event. A zero time would
	// provision llm_memory_events_0001_01; a corrupted one could provision
	// 9999_12. Both are partitions no row will ever land in, and the future
	// case is worse than the past case — rotate only reaps partitions OLDER
	// than the cutoff, so a far-future one is permanent clutter. Surfacing the
	// caller's bad timestamp beats silently materialising it as schema.
	now := time.Now().UTC()
	if t.IsZero() || t.Before(now.AddDate(-partitionSanityYears, 0, 0)) || t.After(now.AddDate(partitionSanityYears, 0, 0)) {
		return fmt.Errorf("eventlog.EnsurePartitionForTime: timestamp %s outside ±%dy of now", t.UTC().Format(time.RFC3339), partitionSanityYears)
	}
	db, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return fmt.Errorf("eventlog.EnsurePartitionForTime: db: %w", err)
	}
	if err := ensurePartitionForTime(ctx, db, t); err != nil {
		return fmt.Errorf("eventlog.EnsurePartitionForTime: %w", err)
	}
	return nil
}

// ensurePartitionForTime creates the monthly partition containing target.
// Idempotent, and tolerant of the duplicate-table race between replicas.
func ensurePartitionForTime(ctx context.Context, db *common.DatabaseManager, target time.Time) error {
	target = target.UTC()
	from := time.Date(target.Year(), target.Month(), 1, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 1, 0)
	name := partitionName(from)

	// Fast path: skip the DDL when the partition is already there. Measured on
	// PG15 the no-op CREATE ... IF NOT EXISTS takes no lock on the parent, so
	// this is not a lock fix — it saves the BEGIN/COMMIT round-trip on every run
	// after the first.
	//
	// to_regclass rather than a pg_class relname scan: relname matches in ANY
	// schema, so a same-named relation elsewhere would skip creating the one the
	// current search_path needs, and every insert for that month would then fail
	// 23514 with the self-heal skipping for the same reason. to_regclass
	// resolves through search_path and yields NULL when the name is not visible.
	//
	// Advisory only: on query error we fall through to the DDL, and the create
	// path stays correct if the partition vanishes between this read and the
	// statement below (the 42P07 branch covers the reverse race).
	var exists bool
	if err := db.Db.GetContext(ctx, &exists,
		`SELECT to_regclass($1) IS NOT NULL`, name); err == nil && exists {
		return nil
	}

	// Interpolated rather than parameterised because Postgres allows no
	// placeholders in DDL. Every interpolated value is derived from the
	// time.Time above, never from caller input.
	//
	// Bound the lock wait: CREATE ... PARTITION OF takes ACCESS EXCLUSIVE on
	// the parent, so a long-running query on llm_memory_events would otherwise
	// queue this DDL behind it — and every subsequent reader behind us. Fail
	// fast instead; the next scheduled run or Append's self-heal retries.
	// SET LOCAL only applies inside a transaction block, so the tx wrapper is
	// what makes the timeout take effect rather than leak to the pooled
	// connection. Mirrors RunEventsRotate's handling of the DROP side.
	stmt := fmt.Sprintf(
		`SET LOCAL lock_timeout = '5s'; CREATE TABLE IF NOT EXISTS %s PARTITION OF llm_memory_events FOR VALUES FROM ('%s') TO ('%s')`,
		name, from.Format("2006-01-02"), to.Format("2006-01-02"),
	)
	_, err := db.DoInTransaction(func(tx *sqlx.Tx) (any, error) {
		_, execErr := tx.ExecContext(ctx, stmt)
		return nil, execErr
	})
	if err != nil && !isPgCode(err, pgDuplicateTable) {
		return fmt.Errorf("create %s: %w", name, err)
	}
	return nil
}

// partitionName is the monthly naming convention seeded by V707
// (llm_memory_events_YYYY_MM). RunEventsRotate matches the same shape when it
// looks for partitions past retention — the two must stay in step.
func partitionName(t time.Time) string {
	return "llm_memory_events_" + t.UTC().Format("2006_01")
}

// isPgCode reports whether err is a Postgres error carrying the given SQLSTATE.
func isPgCode(err error, code string) bool {
	var pgErr *pq.Error
	return errors.As(err, &pgErr) && string(pgErr.Code) == code
}
