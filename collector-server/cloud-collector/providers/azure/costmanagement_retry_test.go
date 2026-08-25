package azure

import (
	"context"
	"net/http"
	"testing"
	"time"
)

// These tests build synthetic 429s as composite literals rather than via a
// helper: bodyclose flags every call to a function returning *http.Response,
// and these responses have no body to close.
func headerWith(header, value string) http.Header {
	h := http.Header{}
	if header != "" {
		h.Set(header, value)
	}
	return h
}

// Cost Management answers 429 with service-specific x-ms-ratelimit-* headers far
// more often than with a plain Retry-After, so all of them have to be read.
func TestRetryAfterFromResponse_ReadsAzureHeaders(t *testing.T) {
	for _, header := range azureRetryAfterHeaders {
		t.Run(header, func(t *testing.T) {
			got, ok := retryAfterFromResponse(&http.Response{StatusCode: http.StatusTooManyRequests, Header: headerWith(header, "45")})
			if !ok || got != 45*time.Second {
				t.Fatalf("got (%v, %v), want (45s, true)", got, ok)
			}
		})
	}
}

// Retry-After may be an HTTP-date rather than delta-seconds (RFC 7231).
func TestRetryAfterFromResponse_ParsesHTTPDate(t *testing.T) {
	// Measured against the response's own Date header, so the result is exact
	// rather than dependent on how long the test itself took to run.
	now := time.Now().UTC()
	resp := &http.Response{StatusCode: http.StatusTooManyRequests, Header: headerWith("Retry-After", now.Add(90*time.Second).Format(http.TimeFormat))}
	resp.Header.Set("Date", now.Format(http.TimeFormat))

	got, ok := retryAfterFromResponse(resp)
	if !ok {
		t.Fatal("HTTP-date Retry-After was not honored")
	}
	if got != 90*time.Second {
		t.Fatalf("got %v, want 90s", got)
	}
}

// An absolute Retry-After is only meaningful against the clock that produced it.
// If our clock runs ahead of Azure's, measuring locally shortens the wait and
// retries into the window we were told to stay out of.
func TestRetryAfterFromResponse_HTTPDateUsesServerClock(t *testing.T) {
	// Server believes it is an hour earlier than we do.
	serverNow := time.Now().UTC().Add(-time.Hour)
	resp := &http.Response{StatusCode: http.StatusTooManyRequests, Header: headerWith("Retry-After", serverNow.Add(60*time.Second).Format(http.TimeFormat))}
	resp.Header.Set("Date", serverNow.Format(http.TimeFormat))

	got, ok := retryAfterFromResponse(resp)
	if !ok {
		t.Fatal("skewed HTTP-date Retry-After was discarded")
	}
	if got != 60*time.Second {
		t.Fatalf("got %v, want 60s measured from the server clock", got)
	}
}

// http.Header.Get is nil-safe, so a response with no header map at all must be
// treated as carrying no hint rather than panicking.
func TestRetryAfterFromResponse_NilHeaderMap(t *testing.T) {
	if _, ok := retryAfterFromResponse(&http.Response{StatusCode: http.StatusTooManyRequests}); ok {
		t.Fatal("response with nil Header reported a retry hint")
	}
}

// A malformed, zero, negative or past hint must not collapse the backoff into a
// spin — those responses have to fall through to the doubling schedule.
func TestRetryAfterFromResponse_IgnoresUnusableHints(t *testing.T) {
	cases := []struct{ name, header, value string }{
		{"no header", "", ""},
		{"zero", "Retry-After", "0"},
		{"negative", "Retry-After", "-30"},
		{"garbage", "Retry-After", "soon"},
		{"date in the past", "Retry-After", time.Now().UTC().Add(-time.Hour).Format(http.TimeFormat)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got, ok := retryAfterFromResponse(&http.Response{StatusCode: http.StatusTooManyRequests, Header: headerWith(tc.header, tc.value)}); ok {
				t.Fatalf("unusable hint was honored as %v", got)
			}
		})
	}
}

func TestRetryAfterFromResponse_NilResponse(t *testing.T) {
	if _, ok := retryAfterFromResponse(nil); ok {
		t.Fatal("nil response reported a retry hint")
	}
}

// Azure's hint wins over the fallback schedule — that is the entire point of the
// change, since the fixed schedule retried before the quota had reset.
func TestCostManagementBackoff_PrefersAzureHint(t *testing.T) {
	got, fromAzure := costManagementBackoff(&http.Response{StatusCode: http.StatusTooManyRequests, Header: headerWith("Retry-After", "17")}, 0)
	if !fromAzure || got != 17*time.Second {
		t.Fatalf("got (%v, %v), want (17s, true)", got, fromAzure)
	}
}

func TestCostManagementBackoff_FallsBackToDoubling(t *testing.T) {
	want := []time.Duration{30 * time.Second, 60 * time.Second, 120 * time.Second}

	for attempt, expected := range want {
		got, fromAzure := costManagementBackoff(&http.Response{StatusCode: http.StatusTooManyRequests, Header: headerWith("", "")}, attempt)
		if fromAzure {
			t.Fatalf("attempt %d reported an Azure hint where there was none", attempt)
		}
		if got != expected {
			t.Fatalf("attempt %d: got %v, want %v", attempt, got, expected)
		}
	}
}

// Cost jobs run at concurrency 1, so an unbounded wait parks every other
// account's spend sync behind one throttled subscription.
func TestCostManagementBackoff_CapsLongWaits(t *testing.T) {
	got, fromAzure := costManagementBackoff(&http.Response{StatusCode: http.StatusTooManyRequests, Header: headerWith("Retry-After", "3600")}, 0)
	if !fromAzure || got != costManagementMaxRetryAfter {
		t.Fatalf("got (%v, %v), want (%v, true)", got, fromAzure, costManagementMaxRetryAfter)
	}

	// The fallback schedule must be capped too, not just Azure's hint.
	if got, _ := costManagementBackoff(&http.Response{StatusCode: http.StatusTooManyRequests, Header: headerWith("", "")}, 10); got != costManagementMaxRetryAfter {
		t.Fatalf("fallback at attempt 10 = %v, want %v", got, costManagementMaxRetryAfter)
	}
}

// 30s << 33 overflows time.Duration's int64 into a negative value, and a
// negative delay makes the retry timer fire immediately — turning the backoff
// into a spin. No caller reaches that today (maxRetries is 3), so this pins the
// guard rather than a live failure.
func TestCostManagementBackoff_NeverNegative(t *testing.T) {
	for _, attempt := range []int{-1, 0, 3, 4, 33, 63, 64} {
		got, _ := costManagementBackoff(&http.Response{StatusCode: http.StatusTooManyRequests, Header: headerWith("", "")}, attempt)
		if got <= 0 {
			t.Fatalf("attempt %d produced a non-positive backoff: %v", attempt, got)
		}
		if got > costManagementMaxRetryAfter {
			t.Fatalf("attempt %d exceeded the cap: %v", attempt, got)
		}
	}
}

// The first-page loop used a bare time.Sleep, so a cancelled sync stayed parked
// for the full backoff after its deadline had passed.
func TestWaitBeforeRetry_ReturnsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	if err := waitBeforeRetry(ctx, time.Hour); err == nil {
		t.Fatal("waitBeforeRetry ignored a cancelled context")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("waitBeforeRetry blocked for %v after cancellation", elapsed)
	}
}

func TestWaitBeforeRetry_SleepsWhenLive(t *testing.T) {
	if err := waitBeforeRetry(context.Background(), 10*time.Millisecond); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
