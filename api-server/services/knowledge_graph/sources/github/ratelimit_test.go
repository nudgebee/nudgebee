package github

import (
	"context"
	"errors"
	"testing"
	"time"

	gogithub "github.com/google/go-github/v61/github"
)

func TestGithubRateLimitWait(t *testing.T) {
	t.Run("plain error is not a rate limit", func(t *testing.T) {
		wait, ok := githubRateLimitWait(errors.New("boom"))
		if ok {
			t.Errorf("expected ok=false, got wait=%v", wait)
		}
	})

	t.Run("RateLimitError reports time until reset", func(t *testing.T) {
		reset := time.Now().Add(45 * time.Second)
		err := &gogithub.RateLimitError{Rate: gogithub.Rate{Reset: gogithub.Timestamp{Time: reset}}}
		wait, ok := githubRateLimitWait(err)
		if !ok {
			t.Fatal("expected ok=true")
		}
		if wait <= 0 || wait > 46*time.Second {
			t.Errorf("wait = %v, want ~45s", wait)
		}
	})

	t.Run("AbuseRateLimitError with explicit RetryAfter", func(t *testing.T) {
		retryAfter := 10 * time.Second
		err := &gogithub.AbuseRateLimitError{RetryAfter: &retryAfter}
		wait, ok := githubRateLimitWait(err)
		if !ok || wait != retryAfter {
			t.Errorf("wait = %v, ok = %v, want %v, true", wait, ok, retryAfter)
		}
	})

	t.Run("AbuseRateLimitError with no RetryAfter falls back to capped wait", func(t *testing.T) {
		err := &gogithub.AbuseRateLimitError{}
		wait, ok := githubRateLimitWait(err)
		if !ok || wait != maxRateLimitWait {
			t.Errorf("wait = %v, ok = %v, want %v, true", wait, ok, maxRateLimitWait)
		}
	})
}

func TestRetryOnRateLimit(t *testing.T) {
	t.Run("no error returns immediately", func(t *testing.T) {
		calls := 0
		val, _, err := retryOnRateLimit(context.Background(), func() (int, *gogithub.Response, error) {
			calls++
			return 42, nil, nil
		})
		if err != nil || val != 42 || calls != 1 {
			t.Errorf("val=%v err=%v calls=%d, want 42 nil 1", val, err, calls)
		}
	})

	t.Run("non-rate-limit error is not retried", func(t *testing.T) {
		calls := 0
		_, _, err := retryOnRateLimit(context.Background(), func() (int, *gogithub.Response, error) {
			calls++
			return 0, nil, errors.New("boom")
		})
		if err == nil || calls != 1 {
			t.Errorf("err=%v calls=%d, want error and 1 call (no retry)", err, calls)
		}
	})

	t.Run("rate limit within cap retries once and succeeds", func(t *testing.T) {
		calls := 0
		reset := time.Now().Add(10 * time.Millisecond)
		val, _, err := retryOnRateLimit(context.Background(), func() (int, *gogithub.Response, error) {
			calls++
			if calls == 1 {
				return 0, nil, &gogithub.RateLimitError{Rate: gogithub.Rate{Reset: gogithub.Timestamp{Time: reset}}}
			}
			return 99, nil, nil
		})
		if err != nil || val != 99 || calls != 2 {
			t.Errorf("val=%v err=%v calls=%d, want 99 nil 2", val, err, calls)
		}
	})

	t.Run("rate limit beyond cap is returned without retrying", func(t *testing.T) {
		calls := 0
		reset := time.Now().Add(maxRateLimitWait + time.Hour)
		_, _, err := retryOnRateLimit(context.Background(), func() (int, *gogithub.Response, error) {
			calls++
			return 0, nil, &gogithub.RateLimitError{Rate: gogithub.Rate{Reset: gogithub.Timestamp{Time: reset}}}
		})
		if err == nil || calls != 1 {
			t.Errorf("err=%v calls=%d, want error and 1 call (no retry, wait exceeds cap)", err, calls)
		}
	})

	t.Run("context cancellation during wait aborts", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		reset := time.Now().Add(time.Second)
		_, _, err := retryOnRateLimit(ctx, func() (int, *gogithub.Response, error) {
			return 0, nil, &gogithub.RateLimitError{Rate: gogithub.Rate{Reset: gogithub.Timestamp{Time: reset}}}
		})
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want context.Canceled", err)
		}
	})
}
