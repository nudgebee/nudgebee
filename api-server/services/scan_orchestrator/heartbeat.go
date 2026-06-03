package scan_orchestrator

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"nudgebee/services/security"

	"github.com/robfig/cron/v3"
)

// HeartbeatResult is the response shape returned by EvaluateHeartbeat. JSON
// keys match what the Python telemetry handler logs — keep them stable.
type HeartbeatResult struct {
	Evaluated bool     `json:"evaluated"`
	Queued    []string `json:"queued,omitempty"`
	Reason    string   `json:"reason,omitempty"`
}

// heartbeatEvaluationInFlight tracks per-account heartbeats whose missed-scan
// evaluation is currently running, so back-to-back heartbeats don't double-
// fire the same scanners. Mirrors the inFlightUpdates pattern in
// services/account/agent_service.go.
var heartbeatEvaluationInFlight sync.Map // key: accountID, value: struct{}

// heartbeatEvaluationTimeout caps the detached background context so a hung
// scanner can't lock the account out of future heartbeat evaluations
// indefinitely. Sized for the worst case: 7 scanners, MaxConcurrentScans=4
// workers, OverallTimeout=15min per scanner = ceil(7/4) * 15min = 30min.
const heartbeatEvaluationTimeout = 30 * time.Minute

// EvaluateHeartbeat is invoked by the Python telemetry handler after the agent
// connection_status has been updated. It inspects schedule_jobs on the
// just-updated connection_status to determine which scanners are "missed" —
// either never-run for this account, or whose last_exec_time_sec is older
// than one cron interval. Each missed scanner is queued for an async run.
//
// Returns immediately after queuing — the caller (telemetry POST) is treated
// as a notification, not a blocking RPC. Errors during the async runs land in
// the api-server log under "scan_orchestrator: RunOne failed", same as the
// existing "K8s Recommendation refresh" cron path.
func EvaluateHeartbeat(ctx *security.RequestContext, accountID, tenantID string) HeartbeatResult {
	logger := ctx.GetLogger().With("account_id", accountID, "tenant_id", tenantID)

	// Fail fast on empty identifiers — `SELECT ... WHERE cloud_account_id=''`
	// or a tenant flag check with `tenant_id=''` would otherwise touch every
	// row and is never a valid runtime state for this endpoint.
	if accountID == "" || tenantID == "" {
		logger.Warn("scan_orchestrator: heartbeat evaluation rejected, empty account_id or tenant_id")
		return HeartbeatResult{Evaluated: false, Reason: "missing account_id or tenant_id"}
	}

	jobs, err := fetchScheduleJobs(ScanAccount{AccountID: accountID, TenantID: tenantID})
	if err != nil {
		logger.Error("scan_orchestrator: heartbeat evaluation: read schedule_jobs", "error", err)
		return HeartbeatResult{Evaluated: false, Reason: "schedule_jobs read failed"}
	}

	now := time.Now()
	missed := missedScanners(jobs, now)
	if len(missed) == 0 {
		return HeartbeatResult{Evaluated: true}
	}

	if _, loaded := heartbeatEvaluationInFlight.LoadOrStore(accountID, struct{}{}); loaded {
		logger.Info("scan_orchestrator: heartbeat evaluation skipped, prior evaluation in-flight",
			"missed", missed)
		return HeartbeatResult{Evaluated: true, Reason: "in-flight"}
	}

	// The HTTP request that triggered this evaluation returns immediately
	// (HTTP 202) while the goroutine keeps running for minutes — detach the
	// request-scoped context so downstream DB / relay calls aren't cut off
	// when the response is sent. WithTimeout caps the detached context so a
	// hung scanner can't keep the in-flight guard set forever.
	detached, cancel := context.WithTimeout(
		context.WithoutCancel(ctx.GetContext()),
		heartbeatEvaluationTimeout,
	)
	bgCtx := security.NewRequestContext(
		detached,
		ctx.GetSecurityContext(),
		ctx.GetLogger(),
		ctx.GetTracer(),
		ctx.GetMeter(),
	)

	go func() {
		defer cancel()
		defer heartbeatEvaluationInFlight.Delete(accountID)
		defer func() {
			if r := recover(); r != nil {
				logger.Error("scan_orchestrator: heartbeat evaluation worker panicked", "panic", r)
			}
		}()
		runMissedScanners(bgCtx, ScanAccount{AccountID: accountID, TenantID: tenantID}, missed, logger)
	}()

	logger.Info("scan_orchestrator: heartbeat evaluation queued", "missed", missed)
	return HeartbeatResult{Evaluated: true, Queued: missed}
}

// missedScanners returns the scanner names that need to run based on
// schedule_jobs state. Two trigger rules apply:
//
//  1. "Never run" — scanner is not present in schedule_jobs, or
//     last_exec_time_sec is 0.
//  2. "Missed cron" — last_exec_time_sec exists, but the next cron tick after
//     that timestamp has already passed (i.e. nextRun <= now).
//
// Both rules together restore the legacy Robusta "INITIAL_SCHEDULE_DELAY_SEC
// fires NEW jobs ~120s after agent start" behaviour for the first case, and
// recover from agent downtime through a scheduled tick for the second.
func missedScanners(jobs []any, now time.Time) []string {
	all := scheduledScanners()
	missed := make([]string, 0, len(all))
	for _, name := range all {
		cronExpr := cronExpressionForScanner(name)
		if cronExpr == "" {
			continue
		}
		lastExec := lastExecForScanner(jobs, name)
		if lastExec == 0 {
			missed = append(missed, name)
			continue
		}
		nextRun, err := cronNextAfter(cronExpr, time.Unix(lastExec, 0))
		if err != nil {
			// Defensive — every cron in the catalog is hard-coded and unit-tested,
			// but if a future entry has a bad expression, treat as missed so the
			// scan fires (and a parse failure shows up in scan logs).
			missed = append(missed, name)
			continue
		}
		if !nextRun.After(now) {
			missed = append(missed, name)
		}
	}
	return missed
}

// scheduledScanners returns every scanner name that participates in scheduled
// runs: Job-based catalog scanners with a non-empty CronExpression, plus the
// direct-API scanners.
func scheduledScanners() []string {
	out := make([]string, 0, len(ScannerCatalog)+len(DirectAPIScanners))
	for name, s := range ScannerCatalog {
		if s.CronExpression == "" {
			continue
		}
		out = append(out, name)
	}
	for name := range DirectAPIScanners {
		out = append(out, name)
	}
	return out
}

// cronNextAfter parses a 5-field cron expression and returns the next tick
// strictly after the given baseline. Wraps robfig/cron/v3's ParseStandard so
// callers don't have to deal with the parser object.
func cronNextAfter(expr string, baseline time.Time) (time.Time, error) {
	sched, err := cron.ParseStandard(expr)
	if err != nil {
		return time.Time{}, err
	}
	return sched.Next(baseline), nil
}

// runMissedScanners invokes RunOne / direct-API runners for each missed
// scanner, bounded to MaxConcurrentScans. Mirrors RunAllForAccount's worker-
// pool shape but filters to a specified subset.
func runMissedScanners(ctx *security.RequestContext, account ScanAccount, scanners []string, logger *slog.Logger) {
	work := make(chan string)
	var wg sync.WaitGroup
	for i := 0; i < MaxConcurrentScans; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					logger.Error("scan_orchestrator: heartbeat worker panicked", "panic", r)
				}
			}()
			for scanner := range work {
				runMissedOne(ctx, account, scanner, logger)
			}
		}()
	}
	for _, s := range scanners {
		work <- s
	}
	close(work)
	wg.Wait()
	logger.Info("scan_orchestrator: heartbeat evaluation complete")
}

// runMissedOne dispatches a single missed scanner to the right run path:
// Job-based scanners go through RunOne; direct-API scanners call their
// dedicated runners directly. Errors are logged, not returned.
func runMissedOne(ctx *security.RequestContext, account ScanAccount, scanner string, logger *slog.Logger) {
	scopedLogger := logger.With("scanner", scanner)
	if _, ok := ScannerCatalog[scanner]; ok {
		if err := RunOne(ctx, account, scanner, nil); err != nil {
			scopedLogger.Error("scan_orchestrator: heartbeat RunOne failed", "error", err)
		}
		return
	}
	if _, ok := DirectAPIScanners[scanner]; !ok {
		scopedLogger.Error("scan_orchestrator: heartbeat unknown scanner")
		return
	}
	var err error
	switch scanner {
	case "certificate_scanner":
		err = RunCertificateScan(ctx, account)
	case "k8s_version_upgrade":
		err = RunK8sVersionUpgrade(ctx, account)
	case "unused_pv":
		err = RunUnusedPVCScan(ctx, account)
	default:
		err = errors.New("scan_orchestrator: heartbeat scanner has no run path")
	}
	if err != nil {
		scopedLogger.Error("scan_orchestrator: heartbeat direct-API scan failed", "error", err)
	}
}
