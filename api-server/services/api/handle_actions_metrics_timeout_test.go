package api

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"nudgebee/services/security"
)

// TestRunMetricsActionWithTimeout_ReturnsOnDeadline guards the wall-clock
// backstop that stops the observed prod hang class (#35564) where a wedged
// metrics provider could stall a /rpc/metrics call for ~85 minutes until the
// conversation TTL reaped it. Passes if the helper returns within the
// deadline even though the wrapped fn is still blocked.
func TestRunMetricsActionWithTimeout_ReturnsOnDeadline(t *testing.T) {
	ctx := security.NewRequestContextForTenantAdmin("test-tenant", nil, nil, nil)

	// Wrapped fn blocks past the deadline. Signal on a channel we own so the
	// goroutine can exit cleanly at test teardown instead of leaking beyond
	// the test binary's lifetime.
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	start := time.Now()
	_, err := runObservabilityActionWithTimeout(ctx, "metrics_list_label_values", 200*time.Millisecond, func() (string, error) {
		<-release
		return "should never surface", nil
	})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("expected deadline-exceeded error, got nil (elapsed %s)", elapsed)
	}
	if !strings.Contains(err.Error(), "exceeded") || !strings.Contains(err.Error(), "metrics_list_label_values") {
		t.Fatalf("error should identify the action and note the deadline; got %q", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("helper took %s to return — backstop is not firing near the 200ms deadline", elapsed)
	}
}

// TestRunMetricsActionWithTimeout_ReturnsFastPath guards the happy path:
// when the wrapped fn completes before the deadline, its value and error
// bubble up unchanged (no artificial wait, no deadline-error wrapping).
func TestRunMetricsActionWithTimeout_ReturnsFastPath(t *testing.T) {
	ctx := security.NewRequestContextForTenantAdmin("test-tenant", nil, nil, nil)

	wantVal := "fast-path-result"
	wantErr := errors.New("fast-path-error")

	// Success path.
	gotVal, gotErr := runObservabilityActionWithTimeout(ctx, "metrics_query", 5*time.Second, func() (string, error) {
		return wantVal, nil
	})
	if gotErr != nil {
		t.Fatalf("expected nil error on fast success, got %v", gotErr)
	}
	if gotVal != wantVal {
		t.Fatalf("expected value %q, got %q", wantVal, gotVal)
	}

	// Error path — helper must forward downstream errors verbatim, NOT wrap
	// them as deadline-exceeded.
	_, gotErr = runObservabilityActionWithTimeout(ctx, "metrics_query", 5*time.Second, func() (string, error) {
		return "", wantErr
	})
	if !errors.Is(gotErr, wantErr) {
		t.Fatalf("expected forwarded error %v, got %v", wantErr, gotErr)
	}
}

// TestRunObservabilityActionWithTimeout_ReturnsOnUpstreamCancel guards the
// third select branch: when the upstream RequestContext is cancelled (client
// disconnect, upstream LB timeout) BEFORE the local deadline or the wrapped fn
// completes, the helper returns immediately with the ctx error rather than
// waiting the full timeout. gin's c.Request.Context() cancellation is the real-
// world trigger; here we simulate by constructing a RequestContext around a
// cancellable context and cancelling it mid-flight.
func TestRunObservabilityActionWithTimeout_ReturnsOnUpstreamCancel(t *testing.T) {
	upstream, cancelUpstream := context.WithCancel(context.Background())
	ctx := security.NewRequestContext(upstream, security.NewSecurityContextForTenantAdmin("test-tenant"), slog.Default(), nil, nil)

	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	// Cancel upstream in 100ms — well before the 10s local deadline.
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancelUpstream()
	}()

	start := time.Now()
	_, err := runObservabilityActionWithTimeout(ctx, "logs_query", 10*time.Second, func() (string, error) {
		<-release
		return "should never surface", nil
	})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("expected cancellation error, got nil (elapsed %s)", elapsed)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled wrapped in error, got %v", err)
	}
	if !strings.Contains(err.Error(), "logs_query") {
		t.Fatalf("error should name the action, got %q", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("helper took %s to return on upstream cancel — 10s deadline should not have fired first", elapsed)
	}
}

// TestRunObservabilityActionWithTimeout_PropagatesPanic guards the panic
// recovery + propagation contract: an unrecovered panic in a spawned goroutine
// crashes the whole process (Gin's middleware only guards the request
// goroutine). The helper must recover in the worker and re-panic on the
// caller's goroutine so upstream middleware can convert it into a 500.
//
// A regression here would look like the test binary itself crashing (SIGSEGV
// or "panic: intentional panic" without the recovery block firing).
func TestRunObservabilityActionWithTimeout_PropagatesPanic(t *testing.T) {
	ctx := security.NewRequestContextForTenantAdmin("test-tenant", nil, nil, nil)

	const panicVal = "intentional panic for test"
	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("expected panic to propagate to caller goroutine, but helper returned normally")
		}
		if r != panicVal {
			t.Fatalf("expected propagated panic value %q, got %v", panicVal, r)
		}
	}()

	_, _ = runObservabilityActionWithTimeout(ctx, "metrics_query", 5*time.Second, func() (string, error) {
		panic(panicVal)
	})
	t.Fatal("unreachable: panic should have propagated")
}

// TestRunObservabilityActionWithTimeout_NilContext guards the top-level nil
// guard. Callers should never pass a nil *security.RequestContext (production
// buildContextFromPayload never returns nil), but if one slips through we
// want a clean action-named error, not a nil-pointer panic mid-select that
// only Gin's outer recovery can salvage.
func TestRunObservabilityActionWithTimeout_NilContext(t *testing.T) {
	_, err := runObservabilityActionWithTimeout[string](nil, "logs_list_labels", 5*time.Second, func() (string, error) {
		t.Fatal("fn should not be invoked when ctx is nil — the guard must short-circuit first")
		return "", nil
	})
	if err == nil {
		t.Fatal("expected non-nil error when ctx is nil, got nil")
	}
	if !strings.Contains(err.Error(), "logs_list_labels") || !strings.Contains(err.Error(), "nil request context") {
		t.Fatalf("error should name the action and mention the nil ctx; got %q", err)
	}
}
