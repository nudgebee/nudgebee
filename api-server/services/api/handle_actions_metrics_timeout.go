package api

import (
	"fmt"
	"time"

	"nudgebee/services/security"
)

// Per-action wall-clock ceilings for observability RPC actions
// (metrics_* and logs_*). Values are chosen to match observed p95 latency
// profiles (see #35564 investigation + 7d logs sweep 2026-08-04):
//
//   - metadata calls (label_values, list_labels, list_names, list_series,
//     log_group) are expected to complete in <1s median and <10s p95 across
//     all supported providers. 30s is 3× headroom.
//
//   - heavy metrics query calls (query, aggregate, utilisation, get_query)
//     can legitimately span 10s of seconds for wide time-ranges and 100+
//     series; on the relay path `relay.Execute` already retries up to
//     180s×3 (≈554s theoretical max) for transient errors, so 180s per call
//     attempt is the operational upper bound any legitimate direct-API
//     provider should respect too.
//
//   - heavy logs query calls (logs_query, logs_list, logs_get_query) get a
//     slightly wider budget (240s) because the observed distribution shows
//     more legitimate long-tail (11 invocations 300–1601s in 7d, mostly
//     multi-step pivots on multi-provider fallback). Still short enough to
//     catch the 27-min outlier.
//
// All bounds are shorter than the 5-min conversation TTL on the llm-server
// side so a hang surfaces to the client as an actionable RPC error, not a
// silent TTL reap (the exact prod symptom in issue #35564).
const (
	observabilityMetadataActionTimeout = 30 * time.Second
	observabilityMetricsQueryTimeout   = 180 * time.Second
	observabilityLogsQueryTimeout      = 240 * time.Second
)

// runObservabilityActionWithTimeout enforces a wall-clock bound on a single
// observability RPC action handler (metrics_* / logs_*) by racing the action
// against a deadline in a goroutine and returning to the caller as soon as
// ANY of three signals fires:
//
//   - the wrapped fn completes (happy path)
//   - the upstream gin request context is cancelled (client disconnect,
//     upstream load-balancer timeout, etc.). buildContextFromPayload wires
//     ctx.GetContext() to c.Request.Context() so cancellation propagates.
//   - the local wall-clock deadline expires
//
// The deadline branch fires regardless of whether downstream code honors ctx
// (most direct-API providers — Datadog, NewRelic, Dynatrace, Splunk — call
// common.HttpGet without HttpWithContext and ignore ctx cancellation).
//
// When the deadline or upstream cancellation fires while fn is still running,
// the in-flight goroutine is orphaned; services-server keeps processing the
// wedged request until the underlying HTTP client eventually gives up (30s
// dial timeout in the default client, or whatever the provider-specific
// transport uses), then discards the response. This is a deliberate trade:
// bounded response latency to the caller > perfectly clean resource
// accounting on a rare pathological path.
//
// Note: this does NOT plumb the deadline into the downstream RequestContext
// (api-server's security.RequestContext has no SetContext method today), so
// well-behaved downstream code that would otherwise honor ctx cancellation
// still runs to completion in the orphan goroutine. Adding SetContext to
// api-server's security package is a follow-up; the wall-clock + upstream-
// cancel dual-race is the correctness-critical piece and holds without it.
func runObservabilityActionWithTimeout[T any](ctx *security.RequestContext, action string, timeout time.Duration, fn func() (T, error)) (T, error) {
	// Top-level nil guard so the rest of the function can trust ctx unconditionally.
	// Callers should always pass a valid RequestContext (buildContextFromPayload
	// never returns nil), but degrading to an actionable error is better than a
	// mid-request nil-pointer panic — and it removes the need to sprinkle
	// `if ctx != nil` around each ctx.GetContext() / ctx.GetLogger() call site.
	if ctx == nil {
		var zero T
		return zero, fmt.Errorf("observability action %q: nil request context", action)
	}

	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	type result struct {
		val      T
		err      error
		panicVal any
	}
	done := make(chan result, 1)
	go func() {
		defer func() {
			// An unrecovered panic in ANY goroutine crashes the entire process;
			// Gin's recovery middleware only guards the request-serving
			// goroutine, not spawned workers like this one. Recover here, log
			// (ctx is guaranteed non-nil by the top-level guard, and
			// ctx.GetLogger() itself falls back to slog.Default() when the
			// logger field is nil — see security/context.go:30), and hand the
			// panic value back to the caller so it can re-panic on the
			// request goroutine where the middleware can convert it into a
			// proper 500. When the caller has already returned (deadline /
			// cancel branch fired first), the buffered `done` channel absorbs
			// the send without blocking; the panic is only logged, not
			// re-raised, since re-panicking on nobody's goroutine would crash
			// the process again.
			if r := recover(); r != nil {
				ctx.GetLogger().Error("observability action panicked; propagating to caller goroutine if still waiting",
					"action", action, "panic", fmt.Sprintf("%v", r))
				done <- result{panicVal: r}
			}
		}()
		val, err := fn()
		done <- result{val: val, err: err}
	}()

	select {
	case r := <-done:
		if r.panicVal != nil {
			panic(r.panicVal)
		}
		return r.val, r.err
	case <-ctx.GetContext().Done():
		ctx.GetLogger().Warn("observability action cancelled by upstream request context",
			"action", action, "reason", ctx.GetContext().Err())
		var zero T
		return zero, fmt.Errorf("observability action %q cancelled: %w", action, ctx.GetContext().Err())
	case <-deadline.C:
		ctx.GetLogger().Warn("observability action exceeded deadline; goroutine orphaned until downstream client gives up",
			"action", action, "deadline", timeout.String())
		var zero T
		return zero, fmt.Errorf("observability action %q exceeded %s deadline", action, timeout)
	}
}
