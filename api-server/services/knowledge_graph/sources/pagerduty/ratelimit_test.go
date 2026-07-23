package pagerduty

import (
	"context"
	"errors"
	"net/http"
	"testing"

	gopagerduty "github.com/PagerDuty/go-pagerduty"
)

func TestIsRateLimited(t *testing.T) {
	t.Run("plain error is not rate limited", func(t *testing.T) {
		if isRateLimited(errors.New("boom")) {
			t.Error("expected false for a non-APIError")
		}
	})

	t.Run("APIError with non-429 status is not rate limited", func(t *testing.T) {
		err := gopagerduty.APIError{StatusCode: http.StatusForbidden}
		if isRateLimited(err) {
			t.Error("expected false for a 403 APIError")
		}
	})

	t.Run("APIError with 429 status is rate limited", func(t *testing.T) {
		err := gopagerduty.APIError{StatusCode: http.StatusTooManyRequests}
		if !isRateLimited(err) {
			t.Error("expected true for a 429 APIError")
		}
	})
}

func TestRetryOnRateLimit(t *testing.T) {
	t.Run("no error returns immediately", func(t *testing.T) {
		calls := 0
		val, err := retryOnRateLimit(context.Background(), func() (int, error) {
			calls++
			return 42, nil
		})
		if err != nil || val != 42 || calls != 1 {
			t.Errorf("val=%v err=%v calls=%d, want 42 nil 1", val, err, calls)
		}
	})

	t.Run("non-rate-limit error is not retried", func(t *testing.T) {
		calls := 0
		_, err := retryOnRateLimit(context.Background(), func() (int, error) {
			calls++
			return 0, errors.New("boom")
		})
		if err == nil || calls != 1 {
			t.Errorf("err=%v calls=%d, want error and 1 call (no retry)", err, calls)
		}
	})

	t.Run("context cancellation during backoff aborts", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := retryOnRateLimit(ctx, func() (int, error) {
			return 0, gopagerduty.APIError{StatusCode: http.StatusTooManyRequests}
		})
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want context.Canceled", err)
		}
	})
}
