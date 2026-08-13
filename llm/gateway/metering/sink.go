package metering

import (
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"nudgebee/llm-gateway/common"
	"nudgebee/llm-gateway/config"
)

// Sink receives usage events. Record must never block the request path.
type Sink interface {
	Record(ev UsageEvent)
	Close() error
}

// NewSink returns a DB-backed async sink when a sink DB is configured, else a
// no-op (so the gateway proxies fine without a database). It NEVER blocks: the
// sink DB is opened lazily in the background (with retry), so a slow/down metering
// DB can't stall startup or k8s readiness. Events buffer meanwhile; overflow drops.
func NewSink() Sink {
	if !configured() {
		slog.Warn("metering: no sink DB configured — usage will NOT be recorded (noop). Set gateway_db_url (or clickhouse_url) to enable.")
		return noopSink{}
	}
	s := &dbSink{
		ch:   make(chan UsageEvent, sinkBufferSize),
		done: make(chan struct{}),
	}
	s.wg.Add(1)
	go s.run()
	slog.Info("metering: async DB sink started (connecting in background)", "sink", config.Config.MeteringSink, "buffer", sinkBufferSize)
	return s
}

func configured() bool {
	if config.Config.MeteringSink == config.SinkClickhouse {
		return config.Config.ClickhouseURL != ""
	}
	return config.Config.GatewayDBURL != ""
}

const (
	sinkBufferSize      = 8192
	batchMaxRows        = 200
	batchMaxWait        = 2 * time.Second
	connectRetryInitial = 1 * time.Second
	connectRetryMax     = 30 * time.Second
)

// noopSink drops events (used when no sink DB is configured).
type noopSink struct{}

func (noopSink) Record(UsageEvent) {}
func (noopSink) Close() error      { return nil }

// dbSink buffers events and flushes them in batches on a background goroutine.
// Overflow is dropped (and counted) rather than blocking the request path —
// billing-grade durability (outbox/WAL) is a later concern.
type dbSink struct {
	db      *common.DatabaseManager
	ch      chan UsageEvent
	done    chan struct{}
	wg      sync.WaitGroup
	dropped atomic.Int64
}

func (s *dbSink) Record(ev UsageEvent) {
	select {
	case s.ch <- ev:
	default:
		if n := s.dropped.Add(1); n%1000 == 1 {
			slog.Warn("metering: sink buffer full, dropping usage events", "dropped_total", n)
		}
	}
}

func (s *dbSink) Close() error {
	close(s.done)
	s.wg.Wait()
	if n := s.dropped.Load(); n > 0 {
		slog.Warn("metering: usage events dropped over lifetime", "dropped_total", n)
	}
	return nil
}

// connect opens the sink DB, retrying with capped backoff. It returns nil if
// Close is called before a connection succeeds. Runs off the request path, so the
// DB being slow/down never affects serving — events buffer until it is ready.
func (s *dbSink) connect() *common.DatabaseManager {
	backoff := connectRetryInitial
	for {
		db, err := common.GetDatabaseManager(common.MeteringSink)
		if err == nil {
			slog.Info("metering: sink DB connected")
			return db
		}
		slog.Warn("metering: sink DB not ready, retrying in background", "error", err, "retry_in", backoff.String())
		timer := time.NewTimer(backoff)
		select {
		case <-s.done:
			timer.Stop()
			return nil
		case <-timer.C:
		}
		if backoff *= 2; backoff > connectRetryMax {
			backoff = connectRetryMax
		}
	}
}

func (s *dbSink) run() {
	defer s.wg.Done()

	// Connect lazily; until then events accumulate in the buffer (overflow drops).
	// A pre-set db (test injection) skips the connect step.
	if s.db == nil {
		db := s.connect()
		if db == nil {
			return // closed before the DB came up; buffered events best-effort dropped
		}
		s.db = db
	}

	batch := make([]UsageEvent, 0, batchMaxRows)
	ticker := time.NewTicker(batchMaxWait)
	defer ticker.Stop()

	flush := func() {
		if len(batch) == 0 {
			return
		}
		if err := s.insert(batch); err != nil {
			slog.Error("metering: batch insert failed", "rows", len(batch), "error", err)
		}
		batch = batch[:0]
	}

	for {
		select {
		case ev := <-s.ch:
			batch = append(batch, ev)
			if len(batch) >= batchMaxRows {
				flush()
			}
		case <-ticker.C:
			flush()
		case <-s.done:
			// Drain what's buffered, then flush and exit.
			for {
				select {
				case ev := <-s.ch:
					batch = append(batch, ev)
					if len(batch) >= batchMaxRows {
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

// usageColumns is the fixed column order for inserts (matches UsageEvent db tags).
var usageColumns = []string{
	"id", "created_at", "tenant_id", "account_id", "user_id", "token_id",
	"session_id", "attributes",
	"provider", "model", "requested_provider", "requested_model", "responded_model",
	"routing_reason", "routing_rule",
	"method", "path", "streaming",
	"status_code", "latency_ms", "request_id",
	"input_tokens", "output_tokens", "total_tokens",
	"cache_read_tokens", "cache_write_tokens", "service_tier",
	"cost_usd",
}

// insert writes a batch as a single multi-row INSERT. DatabaseManager.Exec
// rebinds $N placeholders to the active driver (Postgres or ClickHouse).
func (s *dbSink) insert(evs []UsageEvent) error {
	n := len(usageColumns)
	var sb strings.Builder
	sb.WriteString("INSERT INTO llm_gateway_usage (")
	sb.WriteString(strings.Join(usageColumns, ","))
	sb.WriteString(") VALUES ")

	args := make([]any, 0, len(evs)*n)
	for i, ev := range evs {
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
			ev.ID, ev.CreatedAt, ev.TenantID, ev.AccountID, ev.UserID, ev.TokenID,
			ev.SessionID, ev.Attributes,
			ev.Provider, ev.Model, ev.RequestedProvider, ev.RequestedModel, ev.RespondedModel,
			ev.RoutingReason, ev.RoutingRule,
			ev.Method, ev.Path, ev.Streaming,
			ev.StatusCode, ev.LatencyMS, ev.RequestID,
			ev.InputTokens, ev.OutputTokens, ev.TotalTokens,
			ev.CacheReadTokens, ev.CacheWriteTokens, ev.ServiceTier,
			ev.CostUsd,
		)
	}
	// Rebind $N -> the driver's placeholder style (no-op for Postgres, ? for
	// ClickHouse). Exec itself does not rebind, matching the llm-server convention.
	_, err := s.db.Exec(s.db.Db.Rebind(sb.String()), args...)
	return err
}
