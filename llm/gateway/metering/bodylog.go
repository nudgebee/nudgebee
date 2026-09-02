package metering

import (
	"fmt"
	"log/slog"
	"time"

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
	// FirstUserMessage is a short RAW preview of the request's opening user message
	// (whitespace-collapsed, capped) — precomputed at capture so the Sessions tab reads
	// a cheap column instead of parsing the body per query. Wrapper tokens (e.g.
	// <session>) are intentionally kept as-is.
	FirstUserMessage string `db:"first_user_message"`
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

// bodyLogHook is an optionally-registered body-log sink factory. When none is
// registered it is nil and body logging is a no-op. Kept as a factory so the sink is
// constructed lazily by NewBodyLogSink only when body logging is actually enabled.
var bodyLogHook func() BodyLogSink

// RegisterBodyLogSink registers a body-log sink factory. When none is registered,
// NewBodyLogSink returns the no-op sink even when body logging is configured on.
func RegisterBodyLogSink(fn func() BodyLogSink) { bodyLogHook = fn }

// NewBodyLogSink returns an async body-log sink when body logging is enabled, a sink
// DB is configured, AND a sink is registered; otherwise a no-op.
func NewBodyLogSink() BodyLogSink {
	if !config.Config.CaptureBody {
		return noopBodyLog{}
	}
	if !configured() {
		slog.Warn("bodylog: gateway_capture_body=true but no sink DB configured — body logging disabled (noop)")
		return noopBodyLog{}
	}
	if bodyLogHook == nil {
		slog.Warn("bodylog: gateway_capture_body=true but no body-log sink registered — body logging disabled (noop)")
		return noopBodyLog{}
	}
	return bodyLogHook()
}

type noopBodyLog struct{}

func (noopBodyLog) Record(BodyLog) {}
func (noopBodyLog) Close() error   { return nil }
