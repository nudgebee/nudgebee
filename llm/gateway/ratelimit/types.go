// Package ratelimit enforces per-tenant/user quotas. Limits (metric × period ×
// value × scope) are configured in the metastore; live counters are tracked by a
// CounterStore. Windows are calendar-aligned in UTC — the reset is implicit in the
// bucket key, no cron.
package ratelimit

import "time"

// Metric is what a limit counts.
type Metric string

const (
	MetricRequests Metric = "requests"
	MetricTokens   Metric = "tokens"
	MetricCost     Metric = "cost" // USD; tracked internally as micro-dollars (int)
)

// Period is the calendar window a limit applies to.
type Period string

const (
	PeriodMinute Period = "minute"
	PeriodHour   Period = "hour"
	PeriodDay    Period = "day"
	PeriodMonth  Period = "month"
)

// costScale converts USD to the integer unit counters use (micro-dollars), so all
// metrics are integer and share one atomic path.
const costScale = 1_000_000

// Limit is one configured quota.
type Limit struct {
	TenantID string  `db:"tenant_id"`
	UserID   string  `db:"user_id"` // "" = tenant-wide
	Metric   Metric  `db:"metric"`
	Period   Period  `db:"period"`
	Value    float64 `db:"limit_value"` // cap: request count / token count / USD
	Enabled  bool    `db:"enabled"`
}

// intCap returns the limit value in the counter's integer unit.
func (l Limit) intCap() int64 {
	if l.Metric == MetricCost {
		return int64(l.Value * costScale)
	}
	return int64(l.Value)
}

// Scope identifies the caller a request is limited against.
type Scope struct {
	TenantID string
	UserID   string
}

// Exceeded describes a tripped limit (returned on rejection, for the 429 body).
type Exceeded struct {
	Metric Metric
	Period Period
	Limit  float64
	Scope  string // "tenant" | "user"
}

// bucket returns the calendar-aligned bucket token for a period at t (UTC).
func bucket(p Period, t time.Time) string {
	t = t.UTC()
	switch p {
	case PeriodMinute:
		return t.Format("2006-01-02T15:04")
	case PeriodHour:
		return t.Format("2006-01-02T15")
	case PeriodDay:
		return t.Format("2006-01-02")
	case PeriodMonth:
		return t.Format("2006-01")
	default:
		return t.Format("2006-01-02T15:04")
	}
}

// ResetAt returns when the calendar-aligned window for period p (the one containing
// t) rolls over, in UTC. Surfaced on a 429 so a throttled client knows when to retry.
func ResetAt(p Period, t time.Time) time.Time {
	t = t.UTC()
	switch p {
	case PeriodMinute:
		return t.Truncate(time.Minute).Add(time.Minute)
	case PeriodHour:
		return t.Truncate(time.Hour).Add(time.Hour)
	case PeriodDay:
		y, m, d := t.Date()
		return time.Date(y, m, d+1, 0, 0, 0, 0, time.UTC)
	case PeriodMonth:
		y, m, _ := t.Date()
		return time.Date(y, m+1, 1, 0, 0, 0, 0, time.UTC)
	default:
		return t.Truncate(time.Minute).Add(time.Minute)
	}
}

// ttl returns how long a bucket key lives (period length + slack) so stale buckets
// auto-expire.
func ttl(p Period) time.Duration {
	switch p {
	case PeriodMinute:
		return 2 * time.Minute
	case PeriodHour:
		return 2 * time.Hour
	case PeriodDay:
		return 48 * time.Hour
	case PeriodMonth:
		return 32 * 24 * time.Hour
	default:
		return 2 * time.Minute
	}
}

// key builds the counter key for a limit and scope at time t.
// rl:{tenant}:{user|-}:{metric}:{period}:{bucket}
func key(tenant, user string, m Metric, p Period, t time.Time) string {
	if user == "" {
		user = "-"
	}
	return "rl:" + tenant + ":" + user + ":" + string(m) + ":" + string(p) + ":" + bucket(p, t)
}
