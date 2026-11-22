package slo

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jmoiron/sqlx"
)

// executeLockKey names the Postgres advisory lock that keeps hourly SLO runs from
// overlapping. The rpc-cron "SLO Execute" call can land on any api-server pod, so an
// in-process mutex would let a run slower than its hour overlap on a second pod, and
// CPU forecasts' two-clean-runs close and level escalation assume a single writer.
const executeLockKey = "slo_execute"

// tryExecuteLock takes the run lock without waiting. It is a session-level lock on one
// dedicated connection rather than a transaction-scoped one, because a run can take
// most of an hour and must not hold a transaction open that long; if the pod dies, the
// connection closes and Postgres releases the lock. ok is false when another run holds
// it. unlock releases the lock and returns the connection to the pool.
func tryExecuteLock(ctx context.Context, db *sqlx.DB) (unlock func(), ok bool, err error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("slo: execute lock: get connection: %w", err)
	}
	var got bool
	if err := conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(hashtext($1))`, executeLockKey).Scan(&got); err != nil {
		_ = conn.Close()
		return nil, false, fmt.Errorf("slo: execute lock: %w", err)
	}
	if !got {
		_ = conn.Close()
		return nil, false, nil
	}
	return func() {
		if _, err := conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock(hashtext($1))`, executeLockKey); err != nil {
			slog.Error("slo: failed to release the execute lock; closing its connection releases it", "error", err)
		}
		_ = conn.Close()
	}, true, nil
}
