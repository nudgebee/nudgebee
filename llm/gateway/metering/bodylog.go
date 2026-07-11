package metering

import (
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"nudgebee/llm-gateway/common"
	"nudgebee/llm-gateway/config"
)

// BodyLog is one captured request/response. Written only when body logging is
// enabled (config.CaptureBody). Bodies are pre-capped by the caller.
type BodyLog struct {
	ID           string    `db:"id"`
	RequestID    string    `db:"request_id"` // links to llm_gateway_usage.id
	CreatedAt    time.Time `db:"created_at"`
	ExpiresAt    time.Time `db:"expires_at"`
	TenantID     string    `db:"tenant_id"`
	UserID       string    `db:"user_id"`
	SessionID    string    `db:"session_id"`
	Provider     string    `db:"provider"`
	Model        string    `db:"model"`
	Method       string    `db:"method"`
	Path         string    `db:"path"`
	StatusCode   int       `db:"status_code"`
	RequestBody  string    `db:"request_body"`
	ResponseBody string    `db:"response_body"`
}

// BodyLogSink stores captured bodies. Record must never block the request path.
type BodyLogSink interface {
	Record(b BodyLog)
	Close() error
}

// Enabled reports whether body logging is on (config flag + a sink DB configured).
func BodyLoggingEnabled() bool {
	return config.Config.CaptureBody && configured()
}

// CapBody truncates b to the configured max, appending a marker when it overflows.
// Returns "" for empty input.
func CapBody(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	max := int(config.Config.BodyMaxBytes)
	if max > 0 && len(b) > max {
		return string(b[:max]) + fmt.Sprintf("\n[truncated %d bytes]", len(b)-max)
	}
	return string(b)
}

// TTL returns the configured body-log expiry from now.
func TTL() time.Duration { return time.Duration(config.Config.BodyTTLHours) * time.Hour }

// NewBodyLogSink returns a DB-backed async body-log sink when body logging is
// enabled and a sink DB is configured; otherwise a no-op. Never blocks: it
// connects lazily and starts a background soft-cleanup of expired rows.
func NewBodyLogSink() BodyLogSink {
	if !config.Config.CaptureBody {
		return noopBodyLog{}
	}
	if !configured() {
		slog.Warn("bodylog: gateway_capture_body=true but no sink DB configured — body logging disabled (noop)")
		return noopBodyLog{}
	}
	s := &dbBodyLog{ch: make(chan BodyLog, bodyBufferSize), done: make(chan struct{})}
	s.wg.Add(2)
	go s.run()
	go s.cleanupLoop()
	slog.Warn("bodylog: ENABLED — full request/response bodies are being stored (data custody)", "ttl_hours", config.Config.BodyTTLHours, "max_bytes", config.Config.BodyMaxBytes)
	return s
}

const (
	bodyBufferSize   = 2048
	bodyBatchMaxRows = 50
	bodyBatchMaxWait = 2 * time.Second
)

type noopBodyLog struct{}

func (noopBodyLog) Record(BodyLog) {}
func (noopBodyLog) Close() error   { return nil }

type dbBodyLog struct {
	// db is set once by run() and read by the separate cleanupLoop() goroutine,
	// so it must be accessed atomically to avoid a data race.
	db      atomic.Pointer[common.DatabaseManager]
	ch      chan BodyLog
	done    chan struct{}
	wg      sync.WaitGroup
	dropped atomic.Int64
}

func (s *dbBodyLog) Record(b BodyLog) {
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

	batch := make([]BodyLog, 0, bodyBatchMaxRows)
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
			res, err := db.Exec(db.Db.Rebind(
				`UPDATE llm_gateway_request_log SET deleted_at = $1 WHERE expires_at < $2 AND deleted_at IS NULL`),
				time.Now().UTC(), time.Now().UTC())
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

func (s *dbBodyLog) insert(rows []BodyLog) error {
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
