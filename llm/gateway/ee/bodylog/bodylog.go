// Package bodylog is the EE full-body/PHI log sink: it stores full request/response
// bodies (data custody) in the metering sink DB, batched and TTL-expired. It is the
// DB-backed implementation of the metering.BodyLogSink seam; the OSS build keeps a
// no-op default in metering and never links this package. Registered via
// metering.RegisterBodyLogSink in init().
package bodylog

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"nudgebee/llm-gateway/common"
	"nudgebee/llm-gateway/config"
	"nudgebee/llm-gateway/metering"
)

func init() {
	metering.RegisterBodyLogSink(func() metering.BodyLogSink { return New() })
}

const (
	bodyBufferSize   = 2048
	bodyBatchMaxRows = 50
	bodyBatchMaxWait = 2 * time.Second
)

// New returns a DB-backed async body-log sink. Never blocks: it connects lazily and
// starts a background soft-cleanup of expired rows. The caller (metering.NewBodyLogSink)
// gates on config, so this is only constructed when body logging is enabled + a sink
// DB is configured.
func New() metering.BodyLogSink {
	s := &dbBodyLog{ch: make(chan metering.BodyLog, bodyBufferSize), done: make(chan struct{})}
	s.wg.Add(2)
	go s.run()
	go s.cleanupLoop()
	slog.Warn("bodylog: ENABLED — full request/response bodies are being stored (data custody)", "ttl_hours", config.Config.BodyTTLHours, "max_bytes", config.Config.BodyMaxBytes)
	return s
}

type dbBodyLog struct {
	// db is set once by run() and read by the separate cleanupLoop() goroutine,
	// so it must be accessed atomically to avoid a data race.
	db      atomic.Pointer[common.DatabaseManager]
	ch      chan metering.BodyLog
	done    chan struct{}
	wg      sync.WaitGroup
	dropped atomic.Int64
}

func (s *dbBodyLog) Record(b metering.BodyLog) {
	select {
	case s.ch <- b:
	default:
		if n := s.dropped.Add(1); n%100 == 1 {
			slog.Warn("bodylog: buffer full, dropping captured bodies", "dropped_total", n)
		}
	}
}

func (s *dbBodyLog) Close() error {
	close(s.done)
	s.wg.Wait()
	return nil
}

func (s *dbBodyLog) run() {
	defer s.wg.Done()
	if s.db.Load() == nil {
		db, err := common.GetDatabaseManager(common.MeteringSink)
		if err != nil {
			slog.Error("bodylog: sink DB unavailable — body logging disabled", "error", err)
			// Drain until close so Record never blocks.
			for {
				select {
				case <-s.ch:
				case <-s.done:
					return
				}
			}
		}
		s.db.Store(db)
	}

	batch := make([]metering.BodyLog, 0, bodyBatchMaxRows)
	ticker := time.NewTicker(bodyBatchMaxWait)
	defer ticker.Stop()
	flush := func() {
		if len(batch) == 0 {
			return
		}
		if err := s.insert(batch); err != nil {
			slog.Error("bodylog: batch insert failed", "rows", len(batch), "error", err)
		}
		batch = batch[:0]
	}
	for {
		select {
		case b := <-s.ch:
			batch = append(batch, b)
			if len(batch) >= bodyBatchMaxRows {
				flush()
			}
		case <-ticker.C:
			flush()
		case <-s.done:
			for {
				select {
				case b := <-s.ch:
					batch = append(batch, b)
					if len(batch) >= bodyBatchMaxRows {
						flush()
					}
				default:
					flush()
					return
				}
			}
		}
	}
}

// cleanupLoop periodically SOFT-deletes expired body-log rows (sets deleted_at).
// A separate hard-purge of soft-deleted rows can be added later.
func (s *dbBodyLog) cleanupLoop() {
	defer s.wg.Done()
	interval := time.Duration(config.Config.BodyCleanupIntervalMins) * time.Minute
	if interval <= 0 {
		interval = time.Hour
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			db := s.db.Load()
			if db == nil {
				continue
			}
			// Use the DB clock (NOW()) for both the expiry comparison and the
			// delete stamp — avoids clock skew on the replica running this janitor —
			// and bound the statement with a context so a slow DB can't wedge the loop.
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			res, err := db.Db.ExecContext(ctx, db.Db.Rebind(
				`UPDATE llm_gateway_request_log SET deleted_at = NOW() WHERE expires_at < NOW() AND deleted_at IS NULL`))
			cancel()
			if err != nil {
				slog.Error("bodylog: cleanup failed", "error", err)
				continue
			}
			if n, _ := res.RowsAffected(); n > 0 {
				slog.Info("bodylog: soft-deleted expired rows", "count", n)
			}
		case <-s.done:
			return
		}
	}
}

var bodyLogColumns = []string{
	"id", "request_id", "created_at", "expires_at",
	"tenant_id", "user_id", "session_id",
	"provider", "model", "method", "path", "status_code",
	"request_body", "response_body",
}

func (s *dbBodyLog) insert(rows []metering.BodyLog) error {
	n := len(bodyLogColumns)
	var sb strings.Builder
	sb.WriteString("INSERT INTO llm_gateway_request_log (")
	sb.WriteString(strings.Join(bodyLogColumns, ","))
	sb.WriteString(") VALUES ")
	args := make([]any, 0, len(rows)*n)
	for i, r := range rows {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteByte('(')
		for j := range n {
			if j > 0 {
				sb.WriteByte(',')
			}
			sb.WriteByte('$')
			sb.WriteString(strconv.Itoa(i*n + j + 1))
		}
		sb.WriteByte(')')
		args = append(args,
			r.ID, r.RequestID, r.CreatedAt, r.ExpiresAt,
			r.TenantID, r.UserID, r.SessionID,
			r.Provider, r.Model, r.Method, r.Path, r.StatusCode,
			r.RequestBody, r.ResponseBody,
		)
	}
	db := s.db.Load()
	_, err := db.Exec(db.Db.Rebind(sb.String()), args...)
	return err
}
