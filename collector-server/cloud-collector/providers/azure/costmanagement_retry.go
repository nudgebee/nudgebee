package azure

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	// costManagementMaxRetryAfter caps how long a 429 can park a cost-report
	// worker. Cost report jobs are consumed at
	// cloud_collector_server_cost_processing_workers_max, which defaults to 1, so
	// a worker asleep on one subscription blocks every other account's spend sync
	// behind it. Past this bound, give up and let the next nightly run retry.
	costManagementMaxRetryAfter = 5 * time.Minute

	// costManagementBaseBackoff is the fallback schedule (30s, 60s, 120s) used
	// when the response carries no usable retry hint.
	costManagementBaseBackoff = 30 * time.Second
)

// azureRetryAfterHeaders lists the headers Azure uses to say how long to wait,
// most specific first. Cost Management answers with the service-specific
// x-ms-ratelimit-* forms far more often than with plain Retry-After.
var azureRetryAfterHeaders = []string{
	"x-ms-ratelimit-microsoft.costmanagement-entity-retry-after",
	"x-ms-ratelimit-microsoft.costmanagement-tenant-retry-after",
	"x-ms-ratelimit-microsoft.costmanagement-client-retry-after",
	"x-ms-ratelimit-microsoft.consumption-retry-after",
	"Retry-After",
}

// retryAfterFromResponse reads Azure's own "wait this long" hint. It returns
// false when the response carries none, or carries one that is unusable.
//
// Retry-After is either delta-seconds or an HTTP-date (RFC 7231); the
// x-ms-ratelimit-* variants are always delta-seconds. A hint of 0 or less is
// treated as absent so a malformed header cannot turn the backoff into a spin.
func retryAfterFromResponse(resp *http.Response) (time.Duration, bool) {
	if resp == nil {
		return 0, false
	}

	for _, name := range azureRetryAfterHeaders {
		value := strings.TrimSpace(resp.Header.Get(name))
		if value == "" {
			continue
		}

		if seconds, err := strconv.Atoi(value); err == nil {
			if seconds <= 0 {
				continue
			}
			return time.Duration(seconds) * time.Second, true
		}

		// Only plain Retry-After is ever an HTTP-date. An absolute time is only
		// meaningful against the clock that produced it, so measure from the
		// response's own Date header when it has one and fall back to local time
		// otherwise. A local clock running ahead of Azure's would otherwise
		// shorten the wait and retry into the same closed window this change
		// exists to avoid.
		if when, err := http.ParseTime(value); err == nil {
			reference := time.Now().UTC()
			if date, derr := http.ParseTime(resp.Header.Get("Date")); derr == nil {
				reference = date
			}
			if delay := when.Sub(reference); delay > 0 {
				return delay, true
			}
		}
	}

	return 0, false
}

// costManagementBackoff decides how long to wait before retrying a throttled
// Cost Management call: Azure's own hint when it gives one, otherwise the
// doubling fallback. The result is always capped at costManagementMaxRetryAfter.
//
// Honouring the hint matters because the fixed schedule frequently retried
// earlier than Azure was willing to serve, burning all three attempts against a
// quota that had not reset. The quota is tenant-wide, not per-subscription —
// five different subscriptions were throttled inside one 25-minute window on
// 2026-08-21 while cost jobs were running strictly one at a time.
func costManagementBackoff(resp *http.Response, attempt int) (time.Duration, bool) {
	if delay, ok := retryAfterFromResponse(resp); ok {
		if delay > costManagementMaxRetryAfter {
			return costManagementMaxRetryAfter, true
		}
		return delay, true
	}

	// Every attempt from 4 up already exceeds the cap, and shifting far enough
	// (30s << 33) overflows time.Duration's int64 into a negative value, which
	// would make the retry timer fire immediately instead of waiting. Callers
	// currently cap at maxRetries=3, so this is a guard rather than a live bug.
	if attempt < 0 || attempt >= 4 {
		return costManagementMaxRetryAfter, false
	}

	delay := costManagementBaseBackoff << uint(attempt)
	if delay > costManagementMaxRetryAfter {
		delay = costManagementMaxRetryAfter
	}
	return delay, false
}

// waitBeforeRetry sleeps for delay unless the context is cancelled first, in
// which case it returns the context error. The first-page retry loop previously
// used a bare time.Sleep, so a cancelled sync could stay parked for the full
// backoff after its deadline had already passed.
func waitBeforeRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
