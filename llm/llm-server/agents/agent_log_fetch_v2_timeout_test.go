package agents

import (
	"errors"
	"strings"
	"testing"
	"time"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
)

// TestRunFetchLogsWithTimeout_ReturnsOnDeadline guards the wall-clock bound
// added to fetch_logs Execute. Regression class: conv 8832d8f4 on 2026-08-05
// had a single fetch_logs call hang 6520s (~108 min) because PR #35570's
// timeouts covered the api-server RPC handlers (handle_actions_logs.go)
// but not this internal llm-server tool. This test stands up a fn that
// blocks past the deadline and asserts the helper returns promptly with
// a clean errorResponse envelope (not a nil crash, not a stalled
// conversation).
func TestRunFetchLogsWithTimeout_ReturnsOnDeadline(t *testing.T) {
	ctx := security.NewRequestContextForTenantAdmin("test-tenant")

	// Blocked fn: signal via a channel we own so the leaked goroutine can
	// exit at test teardown instead of outliving the binary.
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	start := time.Now()
	resp, err := runFetchLogsWithTimeout(ctx, 200*time.Millisecond, func(_ *security.RequestContext) (core.NBAgentResponse, error) {
		<-release
		return core.NBAgentResponse{}, nil
	})
	elapsed := time.Since(start)

	// Contract: no error is returned; the failure surfaces as an
	// errorResponse envelope so the planner sees an actionable tool result
	// (same shape as other fetch_logs error paths — kubectl fetch failed,
	// intent extraction failed, etc.).
	if err != nil {
		t.Fatalf("expected nil error on deadline (contract is errorResponse envelope), got %v", err)
	}
	if resp.Status != core.ConversationStatusFailed {
		t.Fatalf("expected Failed status envelope, got %v", resp.Status)
	}
	joined := strings.Join(resp.Response, " ")
	if !strings.Contains(joined, "wall-clock deadline") {
		t.Fatalf("errorResponse should name the wall-clock deadline; got %q", joined)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("helper took %s to return — 200ms deadline should have fired much sooner", elapsed)
	}
}

// TestRunFetchLogsWithTimeout_ReturnsFastPath: happy path forwards value and
// error unchanged, no artificial wait.
func TestRunFetchLogsWithTimeout_ReturnsFastPath(t *testing.T) {
	ctx := security.NewRequestContextForTenantAdmin("test-tenant")

	wantResp := core.NBAgentResponse{AgentName: FetchLogsAgentName, Status: core.ConversationStatusCompleted}
	wantErr := errors.New("upstream boom")

	// Success path.
	gotResp, gotErr := runFetchLogsWithTimeout(ctx, 5*time.Second, func(_ *security.RequestContext) (core.NBAgentResponse, error) {
		return wantResp, nil
	})
	if gotErr != nil {
		t.Fatalf("unexpected error on success path: %v", gotErr)
	}
	if gotResp.Status != wantResp.Status {
		t.Fatalf("expected %v, got %v", wantResp.Status, gotResp.Status)
	}

	// Error path — helper must forward downstream errors verbatim, NOT wrap
	// as deadline-exceeded.
	_, gotErr = runFetchLogsWithTimeout(ctx, 5*time.Second, func(_ *security.RequestContext) (core.NBAgentResponse, error) {
		return core.NBAgentResponse{}, wantErr
	})
	if !errors.Is(gotErr, wantErr) {
		t.Fatalf("expected forwarded error %v, got %v", wantErr, gotErr)
	}
}

// TestRunFetchLogsWithTimeout_PropagatesPanic guards the panic recovery
// contract: an unrecovered panic in a spawned goroutine crashes the whole
// llm-server process (Gin's recovery middleware only guards the request-
// serving goroutine, not workers). The helper must recover in the worker
// and re-panic on the caller's goroutine so upstream middleware can
// convert it into a 500.
func TestRunFetchLogsWithTimeout_PropagatesPanic(t *testing.T) {
	ctx := security.NewRequestContextForTenantAdmin("test-tenant")

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

	_, _ = runFetchLogsWithTimeout(ctx, 5*time.Second, func(_ *security.RequestContext) (core.NBAgentResponse, error) {
		panic(panicVal)
	})
	t.Fatal("unreachable: panic should have propagated")
}

// TestRunFetchLogsWithTimeout_PassesTimeoutBoundContext guards the active
// cancellation half of the dual-race: fn should receive a RequestContext
// whose Go context has a deadline near the wall-clock timeout, so downstream
// code that plumbs ctx.GetContext() into HttpWithContext / gRPC / DB calls
// gets actively cancelled at the deadline (instead of running to natural
// completion in the orphan goroutine after the caller returned).
func TestRunFetchLogsWithTimeout_PassesTimeoutBoundContext(t *testing.T) {
	ctx := security.NewRequestContextForTenantAdmin("test-tenant")

	var seenDeadline time.Time
	var haveDeadline bool
	_, _ = runFetchLogsWithTimeout(ctx, 2*time.Second, func(tctx *security.RequestContext) (core.NBAgentResponse, error) {
		if tctx == nil || tctx.GetContext() == nil {
			t.Fatal("fn should receive a non-nil RequestContext with a Go context")
		}
		seenDeadline, haveDeadline = tctx.GetContext().Deadline()
		return core.NBAgentResponse{}, nil
	})

	if !haveDeadline {
		t.Fatal("passed context must carry a deadline")
	}
	// Deadline should be within a reasonable window of 2s from now (the fn
	// runs synchronously so timing slop is small).
	untilDeadline := time.Until(seenDeadline)
	if untilDeadline < 1500*time.Millisecond || untilDeadline > 2500*time.Millisecond {
		t.Fatalf("deadline expected ~2s from now, was %s", untilDeadline)
	}
}

// TestResolveFetchLogsWallClockTimeout_Precedence guards the three-tier resolution:
// test-override var > config env var > compile-time default. A regression here
// would either break test isolation (default hard-wired) or break runtime tuning
// (config value ignored).
func TestResolveFetchLogsWallClockTimeout_Precedence(t *testing.T) {
	origOverride := fetchLogsWallClockTimeout
	origConfig := config.Config.LlmServerFetchLogsWallClockTimeoutSeconds
	t.Cleanup(func() {
		fetchLogsWallClockTimeout = origOverride
		config.Config.LlmServerFetchLogsWallClockTimeoutSeconds = origConfig
	})

	// (1) Both unset → compile-time default (300s).
	fetchLogsWallClockTimeout = 0
	config.Config.LlmServerFetchLogsWallClockTimeoutSeconds = 0
	if got := resolveFetchLogsWallClockTimeout(); got != defaultFetchLogsWallClockTimeout {
		t.Fatalf("both unset: expected default %s, got %s", defaultFetchLogsWallClockTimeout, got)
	}

	// (2) Config set, test override unset → config wins.
	config.Config.LlmServerFetchLogsWallClockTimeoutSeconds = 45
	if got := resolveFetchLogsWallClockTimeout(); got != 45*time.Second {
		t.Fatalf("config-only: expected 45s, got %s", got)
	}

	// (3) Both set → test override wins (so tests never accidentally read prod config).
	fetchLogsWallClockTimeout = 123 * time.Millisecond
	if got := resolveFetchLogsWallClockTimeout(); got != 123*time.Millisecond {
		t.Fatalf("override + config: expected override 123ms, got %s", got)
	}
}
