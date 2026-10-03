package common

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// RetryWithBackoff retries fn up to maxAttempts times with exponential backoff.
func RetryWithBackoff(ctx context.Context, name string, maxAttempts int, initialInterval, maxInterval time.Duration, fn func(ctx context.Context) error) error {
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	var err error
	interval := initialInterval

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("%s: cancelled before attempt %d: %w", name, attempt, err)
		}
		err = fn(ctx)
		if err == nil {
			return nil
		}

		if attempt == maxAttempts {
			break
		}

		slog.Warn("retry: operation failed, retrying",
			"operation", name,
			"attempt", attempt,
			"max_attempts", maxAttempts,
			"backoff", interval.String(),
			"error", err,
		)

		select {
		case <-ctx.Done():
			return fmt.Errorf("%s: cancelled during retry: %w", name, ctx.Err())
		case <-time.After(interval):
		}

		interval *= 2
		if interval > maxInterval {
			interval = maxInterval
		}
	}

	return fmt.Errorf("%s failed after %d attempts: %w", name, maxAttempts, err)
}
