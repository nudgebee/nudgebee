package scan_orchestrator

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"nudgebee/services/security"
	"nudgebee/services/tenant"
)

// Tunables — exported so tests can shrink the loop. Production stays at defaults.
var (
	OverallTimeout      = 15 * time.Minute
	InitialPollInterval = 5 * time.Second
	MaxPollInterval     = 60 * time.Second
	ScheduleRetryWait   = 30 * time.Second // for concurrent_job_limit responses
	MaxScheduleRetries  = 3
)

// RunScan drives one scanner end-to-end for one account: schedule the Job,
// poll until it's done (or the deadline is hit), fetch logs, parse, return
// recommendations.
//
// Persistence is the caller's responsibility — keeps RunScan testable in
// isolation without a database. The cron handler is what UPSERTs into the
// recommendation table (so the existing path stays the source of truth for
// schema + archive semantics).
//
// Caller MUST gate on tenant.IsFeatureExplicitlyEnabled(FEATURE_SERVER_ORCHESTRATED_SCANNERS)
// before calling RunScan.
func RunScan(ctx *security.RequestContext, account ScanAccount, scannerName string, params map[string]any) ([]Recommendation, error) {
	scanner, ok := ScannerCatalog[scannerName]
	if !ok {
		return nil, fmt.Errorf("scan_orchestrator: unknown scanner %q", scannerName)
	}
	if account.AccountID == "" {
		return nil, errors.New("scan_orchestrator: AccountID is required")
	}

	logger := ctx.GetLogger().With("scanner", scannerName, "account_id", account.AccountID, "tenant_id", account.TenantID)

	spec := scanner.BuildSpec(account, params)
	resolveServiceAccountPlaceholder(&spec, account.ServiceAccount)

	deadline := time.Now().Add(OverallTimeout)

	// Schedule (with bounded retry on concurrent_job_limit).
	jobName, jobUUID, err := scheduleWithBackoff(account.AccountID, spec, deadline, logger)
	if err != nil {
		return nil, fmt.Errorf("schedule: %w", err)
	}
	logger = logger.With("job_name", jobName, "job_uuid", jobUUID)
	logger.Info("scan_orchestrator: scheduled")

	// Poll.
	final, err := pollUntilDone(account.AccountID, jobName, deadline, logger)
	if err != nil {
		return nil, fmt.Errorf("wait: %w", err)
	}
	if final.Status != "Complete" {
		return nil, fmt.Errorf("scan_orchestrator: job %s ended in %s: %s", jobName, final.Status, final.FailureReason)
	}

	// Fetch + parse.
	logs, err := getJobLogs(account.AccountID, jobName)
	if err != nil {
		return nil, fmt.Errorf("logs: %w", err)
	}
	if logs.Truncated {
		logger.Warn("scan_orchestrator: stdout truncated", "bytes_dropped", logs.StdoutBytesDropped)
	}
	if strings.TrimSpace(logs.Stdout) == "" {
		return nil, fmt.Errorf("scan_orchestrator: job %s produced no output", jobName)
	}

	recs, err := scanner.Parse(logs.Stdout, account)
	if err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	logger.Info("scan_orchestrator: completed", "recommendations", len(recs))
	return recs, nil
}

func scheduleWithBackoff(accountID string, spec JobSpec, deadline time.Time, logger *slog.Logger) (jobName, jobUUID string, err error) {
	for attempt := 0; attempt < MaxScheduleRetries; attempt++ {
		if time.Now().After(deadline) {
			return "", "", errors.New("schedule deadline exceeded before agent accepted the Job")
		}
		jobName, jobUUID, scheduled, err := scheduleJob(accountID, spec)
		if err != nil {
			return "", "", err
		}
		if scheduled {
			return jobName, jobUUID, nil
		}
		// concurrent_job_limit — wait and retry.
		logger.Info("scan_orchestrator: agent at concurrency cap, backing off", "attempt", attempt+1)
		time.Sleep(ScheduleRetryWait)
	}
	return "", "", errors.New("agent stayed at concurrency cap; abandoning")
}

func pollUntilDone(accountID, jobName string, deadline time.Time, logger *slog.Logger) (jobStatus, error) {
	interval := InitialPollInterval
	for {
		if time.Now().After(deadline) {
			return jobStatus{}, fmt.Errorf("scan_orchestrator: deadline %s exceeded for job %s", OverallTimeout, jobName)
		}
		s, err := waitForJob(accountID, jobName)
		if err != nil {
			return s, err
		}
		switch s.Status {
		case "Complete", "Failed", "NotFound":
			return s, nil
		}
		// Running. Sleep with exponential backoff up to MaxPollInterval.
		time.Sleep(interval)
		interval *= 2
		if interval > MaxPollInterval {
			interval = MaxPollInterval
		}
	}
}

// FeatureName is exported so callers (cron handler, tests) reference a single
// constant rather than literal strings. Must match the value in
// migrations/.../V723... and tenant.FEATURE_SERVER_ORCHESTRATED_SCANNERS.
const FeatureName = tenant.FEATURE_SERVER_ORCHESTRATED_SCANNERS
