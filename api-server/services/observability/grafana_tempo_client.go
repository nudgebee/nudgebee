package observability

import (
	"fmt"
	"io"
	"nudgebee/services/common"
	"nudgebee/services/integrations"
	"nudgebee/services/security"
	"strconv"
	"strings"
	"time"
)

// Tempo 3.0 rejects a search window over 168h and a TraceQL-metrics window of 24h or
// more (probed against the dev Tempo, 2026-09-29). A minute is kept back so a window the
// UI computed to the second never lands exactly on the limit.
const (
	tempoSearchMaxWindow  = 168*time.Hour - time.Minute
	tempoMetricsMaxWindow = 24*time.Hour - time.Minute
	tempoRequestTimeout   = 30 * time.Second
	tempoDefaultPageSize  = 50
	// tempoSpanCap bounds how deep the listing pages: /api/search has no offset, so page N
	// is served by fetching the newest N*size spans and slicing. 1000 matches the largest
	// caller (the LLM trace path).
	tempoSpanCap = 1000
	// tempoMaxSpansPerSpanSet is Tempo's default max_spans_per_span_set: a search asking
	// for more spans per trace (spss) is rejected with a 400, so paging past 100 spans
	// raises only `limit` (traces), never spss beyond this.
	tempoMaxSpansPerSpanSet = 100
	tempoMaxBody            = 32 << 20
)

// tempoWindow is a query window in unix seconds, as Tempo's start/end params take it.
type tempoWindow struct {
	Start   int64
	End     int64
	Clamped bool
}

func (w tempoWindow) params() map[string]string {
	return map[string]string{
		"start": strconv.FormatInt(w.Start, 10),
		"end":   strconv.FormatInt(w.End, 10),
	}
}

// tempoTimeWindow normalises a request window (callers send milliseconds; seconds are
// tolerated) and keeps only its most recent maxWindow, so a long picker range still answers
// with the newest data instead of a Tempo 400.
func tempoTimeWindow(start, end int64, maxWindow time.Duration, now time.Time) tempoWindow {
	toSec := func(v int64) int64 {
		if v >= 1e12 {
			return v / 1000
		}
		return v
	}
	w := tempoWindow{Start: toSec(start), End: toSec(end)}
	if w.End <= 0 {
		w.End = now.Unix()
	}
	if w.Start <= 0 || w.Start >= w.End {
		w.Start = w.End - int64(time.Hour/time.Second)
	}
	if limit := int64(maxWindow / time.Second); w.End-w.Start > limit {
		w.Start = w.End - limit
		w.Clamped = true
	}
	return w
}

// logTempoClamp records a window shortened to Tempo's limit, naming the requested and
// the effective window, so the clamp is never silent.
func logTempoClamp(ctx *security.RequestContext, operation string, reqStart, reqEnd int64, w tempoWindow) {
	if ctx == nil || !w.Clamped {
		return
	}
	ctx.GetLogger().Info("grafana tempo: query window clamped to Tempo's limit",
		"operation", operation,
		"requested_start", reqStart, "requested_end", reqEnd,
		"effective_start", w.Start, "effective_end", w.End)
}

// tempoGet performs one authenticated GET against Tempo. common.HttpGet does not fail on
// HTTP errors, so a non-2xx is turned into an error carrying Tempo's own message, which
// is plain text and names the actual problem (window too long, bad TraceQL, no org id).
func tempoGet(cfg integrations.GrafanaTempoConfig, path string, params map[string]string) ([]byte, error) {
	opts := []common.HttpOption{
		common.HttpWithHeaders(cfg.Headers()),
		common.HttpWithQueryParams(params),
		common.HttpWithTimeout(tempoRequestTimeout),
	}
	if cfg.InsecureSkipVerify {
		opts = append(opts, common.HttpWithInsecureSkipVerify())
	}
	resp, err := common.HttpGet(cfg.URL+path, opts...)
	if err != nil {
		return nil, fmt.Errorf("grafana tempo: %s request failed: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	// One byte past the limit tells a truncated body apart from one that fits exactly,
	// so an oversized answer is named instead of failing later as a JSON decode error.
	body, err := io.ReadAll(io.LimitReader(resp.Body, tempoMaxBody+1))
	if err != nil {
		return nil, fmt.Errorf("grafana tempo: reading %s response: %w", path, err)
	}
	if len(body) > tempoMaxBody {
		return nil, fmt.Errorf("grafana tempo: %s response too large (>%dMB) — narrow the time range or filters", path, tempoMaxBody>>20)
	}
	if resp.StatusCode/100 != 2 {
		msg := strings.TrimSpace(string(body))
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return nil, fmt.Errorf("grafana tempo: %s returned HTTP %d: %s", path, resp.StatusCode, msg)
	}
	return body, nil
}
