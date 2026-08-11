package vmpackage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"nudgebee/services/account"
	"nudgebee/services/common"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
	"nudgebee/services/vulnmatcher"
)

const scanTimeout = 15 * time.Minute

// ScanPackages validates access to the account and cloud resource, then
// kicks off a detached pull-parse-match-persist pipeline for the VM and
// returns immediately. Mirrors recommendation.ScanImage's manual-trigger
// shape: the RPC caller gets an ack, the real work runs detached with its
// own timeout.
func ScanPackages(ctx *security.RequestContext, req ScanRequest) (ScanResponse, error) {
	if !ctx.GetSecurityContext().HasAccountAccess(req.AccountId, security.SecurityAccessTypeCreate) {
		return ScanResponse{}, common.ErrorUnauthorized("unauthorized")
	}

	a, err := account.GetAccount(ctx, req.AccountId)
	if err != nil {
		ctx.GetLogger().Error("vmpackage: error getting account", "error", err)
		return ScanResponse{}, err
	}
	if a.Id == "" {
		return ScanResponse{}, fmt.Errorf("vmpackage: account not found - %s", req.AccountId)
	}
	if a.Tenant != ctx.GetSecurityContext().GetTenantId() {
		return ScanResponse{}, fmt.Errorf("vmpackage: account not found in tenant - %s", req.AccountId)
	}

	ip, err := verifyCloudResource(req.CloudResourceId, req.AccountId, a.Tenant)
	if err != nil {
		return ScanResponse{}, err
	}

	detachedCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx.GetContext()), scanTimeout)
	scanCtx := security.NewRequestContext(detachedCtx, ctx.GetSecurityContext(), ctx.GetLogger(), ctx.GetTracer(), ctx.GetMeter())
	go func() {
		defer cancel()
		defer func() {
			if r := recover(); r != nil {
				ctx.GetLogger().Error("vmpackage: scan panicked", "panic", r)
			}
		}()
		target, err := fetchDiscoveryInventory(scanCtx.GetLogger(), req.AccountId, a.Tenant, req.DatasourceId, ip)
		if err != nil {
			scanCtx.GetLogger().Error("vmpackage: discovery_inventory failed", "cloud_resource_id", req.CloudResourceId, "error", err)
			return
		}
		if err := runScan(scanCtx, req.AccountId, a.Tenant, req.CloudResourceId, target); err != nil {
			scanCtx.GetLogger().Error("vmpackage: scan failed", "cloud_resource_id", req.CloudResourceId, "error", err)
		}
	}()

	ctx.GetLogger().Info("vmpackage: scan started", "account_id", req.AccountId, "cloud_resource_id", req.CloudResourceId)
	return ScanResponse{Data: []map[string]any{{"cloud_resource_id": req.CloudResourceId, "status": "started"}}}, nil
}

// verifyCloudResource confirms the caller-supplied cloud_resource_id belongs
// to this account+tenant before any SSH command runs, and returns its IP.
// Legacy VM cloud_resourses rows are onboarded one-per-IP with resourse_id
// formatted "vm-<ip>", parsed directly; anything else (a resource_match.go
// Tier-0/1/2 match or identity-created row, or a genuine cloud-collector
// row) falls back to resolveResourceIP's meta-based extraction.
func verifyCloudResource(cloudResourceID, accountID, tenantID string) (string, error) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return "", err
	}
	var resourceID string
	err = dbms.Db.Get(&resourceID,
		`SELECT resourse_id FROM cloud_resourses WHERE id = $1 AND account = $2 AND tenant = $3`,
		cloudResourceID, accountID, tenantID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", common.ErrorBadRequest(fmt.Sprintf("vmpackage: cloud_resource_id %s not found for this account", cloudResourceID))
		}
		return "", fmt.Errorf("vmpackage: verify cloud resource: %w", err)
	}
	if ip, ok := strings.CutPrefix(resourceID, vmResourceIDPrefix); ok {
		return ip, nil
	}
	ip, err := resolveResourceIP(dbms, cloudResourceID, accountID, tenantID)
	if err != nil {
		return "", fmt.Errorf("vmpackage: cloud_resource_id %s has resourse_id %q, not a vm-<ip> resource, and no IP could be recovered from it: %w", cloudResourceID, resourceID, err)
	}
	return ip, nil
}

const vmResourceIDPrefix = "vm-"

// runScan runs the parse -> persist(packages) -> capability-check -> match ->
// persist(findings) pipeline against an already-fetched discovery target.
// Fetching is the caller's job: the manual on-demand path (ScanPackages)
// fetches one target per call, while the cron sweep path
// (vmpackage.ScanAllAccounts) fetches many targets in one batch relay call —
// this function is shared by both so that logic never duplicates.
func runScan(ctx *security.RequestContext, accountID, tenantID, cloudResourceID string, target discoveryTarget) error {
	logger := ctx.GetLogger().With("account_id", accountID, "cloud_resource_id", cloudResourceID)

	osReleaseRaw, ok := target.Collectors["os-release"]
	if !ok {
		return fmt.Errorf("discovery_inventory: target %q missing os-release collector", target.Host)
	}
	family, version, err := ParseOSRelease(osReleaseRaw)
	if err != nil {
		return fmt.Errorf("parse os-release: %w", err)
	}

	pkgs, err := parsePackages(target.Collectors)
	if err != nil {
		return err
	}
	logger.Info("vmpackage: fetched packages", "host", target.Host, "os_family", family, "os_version", version, "package_count", len(pkgs))

	if len(pkgs) == 0 {
		// Per suspect_zero's philosophy below: a running host reporting zero
		// packages is far more likely a collector/parse failure than a
		// genuinely package-free VM. Bail before archiving so a bad
		// discovery run can't wipe the last-known-good inventory.
		logger.Warn("vmpackage: discovery_inventory produced zero packages, skipping persist", "host", target.Host)
		return nil
	}

	if err := upsertPackages(tenantID, accountID, cloudResourceID, family, version, pkgs); err != nil {
		return fmt.Errorf("store packages: %w", err)
	}

	// Fail fast on an OS the loaded vulnerability database has no data for,
	// rather than getting a 422 (or worse, a confident empty result) from
	// Match. Packages are already stored at this point even if matching is
	// skipped.
	caps, err := vulnmatcher.Capabilities()
	if err != nil {
		return fmt.Errorf("vuln-matcher capabilities: %w", err)
	}
	if !caps.SupportsOS(family, version) {
		return fmt.Errorf("vuln-matcher has no data for %s %s (packages stored, matching skipped)", family, version)
	}

	matchReq, pkgsByKey := buildMatchRequest(family, version, pkgs)
	if len(matchReq.Packages) == 0 {
		logger.Info("vmpackage: no packages to match")
		return nil
	}

	resp, err := vulnmatcher.Match(matchReq)
	if err != nil {
		return fmt.Errorf("vuln-matcher match: %w", err)
	}
	if resp.SuspectZero {
		// Per vuln-matcher-server's contract: a non-empty package set with
		// zero findings must be alarmed on, not recorded as a clean host.
		logger.Warn("vmpackage: suspect_zero returned by vuln-matcher — non-empty package set produced no findings",
			"package_count", len(matchReq.Packages))
	}

	if err := persistFindings(tenantID, accountID, cloudResourceID, resp.Findings, pkgsByKey); err != nil {
		return fmt.Errorf("store findings: %w", err)
	}
	logger.Info("vmpackage: scan complete", "finding_count", len(resp.Findings))
	return nil
}
