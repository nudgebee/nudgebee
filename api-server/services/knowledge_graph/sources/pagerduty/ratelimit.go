package pagerduty

import (
	"context"
	"errors"
	"time"

	gopagerduty "github.com/PagerDuty/go-pagerduty"
)

// rateLimitBackoff is a fixed, conservative wait before a single retry on a 429.
// go-pagerduty's APIError doesn't capture the Retry-After header value (only the
// status code), so there's no exact wait time to honor — unlike GitHub's
// RateLimitError.Rate.Reset or AbuseRateLimitError.RetryAfter.
const rateLimitBackoff = 30 * time.Second

// isRateLimited reports whether err is a PagerDuty 429 response.
func isRateLimited(err error) bool {
	var apiErr gopagerduty.APIError
	return errors.As(err, &apiErr) && apiErr.RateLimited()
}

// retryOnRateLimit runs fn once; if it fails with a PagerDuty 429, it sleeps
// rateLimitBackoff (respecting ctx cancellation) and retries fn exactly once more.
// Any other error is returned as-is so the caller's existing warn-and-continue /
// hard-fail handling applies unchanged.
func retryOnRateLimit[T any](ctx context.Context, fn func() (T, error)) (T, error) {
	val, err := fn()
	if err == nil || !isRateLimited(err) {
		return val, err
	}
	select {
	case <-time.After(rateLimitBackoff):
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	}
	return fn()
}
