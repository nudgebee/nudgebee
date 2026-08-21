package github

import (
	"context"
	"errors"
	"time"

	gogithub "github.com/google/go-github/v61/github"
)

// maxRateLimitWait bounds how long a single retry will sleep for. GitHub's primary
// rate limit can reset up to an hour out; blocking a KG sync worker that long would
// stall every other tenant queued behind it, so a wait beyond this cap is treated the
// same as any other error (warn-and-continue / hard-fail, per the existing call site).
const maxRateLimitWait = 2 * time.Minute

// githubRateLimitWait reports how long to wait before retrying, and whether err was a
// GitHub rate-limit error at all. Primary rate limits carry an exact Reset time;
// secondary/abuse limits carry an explicit RetryAfter when GitHub provides one, else a
// conservative capped guess.
func githubRateLimitWait(err error) (time.Duration, bool) {
	var rl *gogithub.RateLimitError
	if errors.As(err, &rl) {
		return time.Until(rl.Rate.Reset.Time), true
	}
	var arl *gogithub.AbuseRateLimitError
	if errors.As(err, &arl) {
		if arl.RetryAfter != nil {
			return *arl.RetryAfter, true
		}
		return maxRateLimitWait, true
	}
	return 0, false
}

// retryOnRateLimit runs fn once; if it fails with a GitHub rate-limit error whose wait
// is within maxRateLimitWait, it sleeps (respecting ctx cancellation) and retries fn
// exactly once more. Any other error, or a wait beyond the cap, is returned as-is so
// the caller's existing warn-and-continue / hard-fail handling applies unchanged.
func retryOnRateLimit[T any](ctx context.Context, fn func() (T, *gogithub.Response, error)) (T, *gogithub.Response, error) {
	val, resp, err := fn()
	if err == nil {
		return val, resp, nil
	}
	wait, isRateLimit := githubRateLimitWait(err)
	if !isRateLimit || wait <= 0 || wait > maxRateLimitWait {
		return val, resp, err
	}
	select {
	case <-time.After(wait):
	case <-ctx.Done():
		var zero T
		return zero, resp, ctx.Err()
	}
	return fn()
}
