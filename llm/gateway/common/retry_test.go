package common

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRetryWithBackoff_SuccessFirstAttempt(t *testing.T) {
	calls := 0
	err := RetryWithBackoff(context.Background(), "test-op", 3, 10*time.Millisecond, 50*time.Millisecond, func(ctx context.Context) error {
		calls++
		return nil
	})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected 1 call, got %d", calls)
	}
}

func TestRetryWithBackoff_TransientSuccess(t *testing.T) {
	calls := 0
	err := RetryWithBackoff(context.Background(), "test-op", 5, 5*time.Millisecond, 20*time.Millisecond, func(ctx context.Context) error {
		calls++
		if calls < 3 {
			return errors.New("transient failure")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("expected success after retries, got %v", err)
	}
	if calls != 3 {
		t.Fatalf("expected 3 calls, got %d", calls)
	}
}

func TestRetryWithBackoff_ExhaustedRetries(t *testing.T) {
	calls := 0
	testErr := errors.New("persistent failure")
	err := RetryWithBackoff(context.Background(), "test-op", 3, 5*time.Millisecond, 20*time.Millisecond, func(ctx context.Context) error {
		calls++
		return testErr
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if calls != 3 {
		t.Fatalf("expected 3 calls, got %d", calls)
	}
}

func TestRetryWithBackoff_ContextCancelledBeforeAttempt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	err := RetryWithBackoff(ctx, "test-op", 3, 10*time.Millisecond, 50*time.Millisecond, func(ctx context.Context) error {
		calls++
		return nil
	})
	if err == nil {
		t.Fatal("expected error due to cancelled context, got nil")
	}
	if calls != 0 {
		t.Fatalf("expected 0 calls, got %d", calls)
	}
}

func TestRetryWithBackoff_ContextCancelledDuringBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	err := RetryWithBackoff(ctx, "test-op", 5, 50*time.Millisecond, 100*time.Millisecond, func(ctx context.Context) error {
		calls++
		if calls == 1 {
			cancel()
			return errors.New("fail attempt 1")
		}
		return nil
	})
	if err == nil {
		t.Fatal("expected error due to context cancellation during backoff, got nil")
	}
	if calls != 1 {
		t.Fatalf("expected 1 call, got %d", calls)
	}
}
