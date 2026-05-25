package scan_orchestrator

import (
	"sync"

	"nudgebee/services/security"
	"nudgebee/services/tenant"
)

// CronScanners is the set of Job-based scanners auto-triggered by the existing
// "K8s Recommendation refresh" + "Security recommendation refresh" crons.
// image_scanner is intentionally omitted here because it requires a target
// image parameter and runs per-image via processImageScanner — RunAllForAccount
// would have nothing to pass.
var CronScanners = []string{
	"popeye_scan",
	"trivy_cis_scan",
	"kube_bench_scan",
	"helm_chart_upgrade",
}

// DirectAPIScannerMeta is the schedule metadata for scanners that are NOT
// Job-based (no ScannerCatalog entry). Map keys are the legacy Robusta
// action_func_name values — kept as-is so the UI parser, which has read
// agent.connection_status -> 'schedule_jobs' since the Robusta era, matches
// these scanners against the labels it already knows.
type DirectAPIScannerMeta struct {
	RuleName       string
	CronExpression string
}

// DirectAPIScanners is the schedule-side companion to ScannerCatalog for the
// three direct-K8s-API scanners (no Job, no catalog entry). EvaluateHeartbeat
// uses this for missed-scan detection; the schedule-job state writer uses it
// to record state under the legacy action_func_name keys.
var DirectAPIScanners = map[string]DirectAPIScannerMeta{
	"certificate_scanner": {RuleName: CertificateExpiryRuleName, CronExpression: "0 12 * * *"},
	"k8s_version_upgrade": {RuleName: KubeProxyVersionRuleName, CronExpression: "0 0 1 * *"},
	"unused_pv":           {RuleName: UnusedPVCRuleName, CronExpression: "0 0 * * *"},
}

// MaxConcurrentScans bounds how many scanners we run in parallel for one
// account. Per-agent concurrency is bounded by the agent's own
// concurrent_job_limit cap (default 5); this is the api-server-side cap.
const MaxConcurrentScans = 4

// RunAllForAccount runs every scanner in CronScanners for one account in
// parallel (bounded by MaxConcurrentScans), persisting each scanner's output
// independently. Failures on one scanner don't block the others.
//
// Caller is the existing recommendation-refresh cron path
// (recommendation.GenerateRecommendation per-account loop). Caller MUST gate
// on tenant.IsFeatureExplicitlyEnabled(FEATURE_SERVER_ORCHESTRATED_SCANNERS)
// before calling RunAllForAccount; that gate stays at the call-site so the
// legacy agent-task path keeps running for tenants who haven't opted in.
func RunAllForAccount(ctx *security.RequestContext, account ScanAccount) {
	logger := ctx.GetLogger().With("account_id", account.AccountID, "tenant_id", account.TenantID)

	work := make(chan string)
	var wg sync.WaitGroup
	for i := 0; i < MaxConcurrentScans; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					logger.Error("scan_orchestrator: worker panicked", "panic", r)
				}
			}()
			for scanner := range work {
				runOne(ctx, account, scanner)
			}
		}()
	}
	for _, s := range CronScanners {
		work <- s
	}
	close(work)
	wg.Wait()

	// Direct-K8s-API scanners aren't Job-based (Robusta ran them in-process
	// via the Python K8s client). Each fetches via the agent's get_resource
	// primitive, parses, persists. Errors don't block the others — just
	// logged. Each call has its own recover so a panic in one direct-API
	// scanner doesn't stop the other from running.
	func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Error("scan_orchestrator: certificate_scanner panicked", "panic", r)
			}
		}()
		if err := RunCertificateScan(ctx, account); err != nil {
			logger.Error("scan_orchestrator: certificate_scanner failed", "error", err)
		}
	}()
	func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Error("scan_orchestrator: k8s_version_upgrade panicked", "panic", r)
			}
		}()
		if err := RunK8sVersionUpgrade(ctx, account); err != nil {
			logger.Error("scan_orchestrator: k8s_version_upgrade failed", "error", err)
		}
	}()
	func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Error("scan_orchestrator: unused_pvc panicked", "panic", r)
			}
		}()
		if err := RunUnusedPVCScan(ctx, account); err != nil {
			logger.Error("scan_orchestrator: unused_pvc failed", "error", err)
		}
	}()
	logger.Info("scan_orchestrator: RunAllForAccount complete")
}

// RunOne runs + persists a single scanner for one account. Used by the
// UI-triggered CreateRecommendationJob path (one button click = one scanner).
// Errors are returned so the caller can surface them in the API response.
func RunOne(ctx *security.RequestContext, account ScanAccount, scannerName string, params map[string]any) error {
	logger := ctx.GetLogger().With("scanner", scannerName, "account_id", account.AccountID, "tenant_id", account.TenantID)
	recs, err := RunScan(ctx, account, scannerName, params)
	if err != nil {
		logger.Error("scan_orchestrator: RunScan failed", "error", err)
		return err
	}
	return Persist(ctx, account, scannerName, recs)
}

// runOne is the unexported worker-pool body used by RunAllForAccount. It
// swallows errors (logs them) so one bad scanner doesn't kill the loop.
func runOne(ctx *security.RequestContext, account ScanAccount, scannerName string) {
	logger := ctx.GetLogger().With("scanner", scannerName, "account_id", account.AccountID, "tenant_id", account.TenantID)
	recs, err := RunScan(ctx, account, scannerName, nil)
	if err != nil {
		logger.Error("scan_orchestrator: RunScan failed", "error", err)
		return
	}
	if err := Persist(ctx, account, scannerName, recs); err != nil {
		logger.Error("scan_orchestrator: Persist failed", "error", err)
	}
}

// IsEnabledForTenant is a tiny re-export of the per-tenant gate, so callers
// in services/recommendation don't need to import services/tenant directly
// just to read the SERVER_ORCHESTRATED_SCANNERS feature flag.
func IsEnabledForTenant(ctx *security.RequestContext, tenantID string) bool {
	return tenant.IsFeatureExplicitlyEnabled(ctx, tenantID, FeatureName)
}
