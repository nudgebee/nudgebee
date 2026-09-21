// Package bootstrap holds work that brings a fresh deployment up to a usable
// state at startup, before anything serves.
//
// Today that is provisioning the first admin and their tenant at install time,
// so a self-hosted deployment is complete when `helm install` returns rather
// than half-configured until somebody signs in. Other one-shot install-time
// setup belongs here too.
//
// Naming note: "bootstrap" already carries two meanings in this codebase. This
// package is the second one — one-shot startup work, as in
// api.RunEEBootstrapHooks. It is NOT the licence gate that decides whether a
// new user may onboard (license.BootstrapCheckImpl, ee/license/bootstrap.go),
// though it does consult the licence to decide whether it is entitled to
// provision at all.
//
// It does not create anything itself. It decides whether this deployment is
// entitled and which tenant to target, then calls user.OnboardUser — the same
// function the login path calls — so roles, tenant linkage, group membership,
// cache invalidation and the agent-playbook seeding all happen exactly once, in
// one place. The bundled agent's registration runs inside that call too, so a
// successful provision leaves the hosting cluster already connected.
//
// Inert unless an admin address is available, which is every install that does
// not opt in. Those keep today's behavior: everything is provisioned by the
// first login instead.
package bootstrap

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"nudgebee/services/config"
	"nudgebee/services/internal/database"
	"nudgebee/services/license"
	"nudgebee/services/security"
	"nudgebee/services/user"
)

const (
	// displayName matches what the first-login path passes, which keeps the
	// generated tenant name identical whichever path provisioned the install.
	displayName = "admin"
	adminRole   = "tenant_admin"
)

// Provision creates the first admin, their tenant and the bundled cluster if
// this deployment is entitled and an admin address is configured. Safe to call
// on every boot: it converges rather than duplicating.
func Provision(ctx context.Context, logger *slog.Logger) error {
	// Default a nil logger at the entry point, matching the convention used across
	// knowledge_graph/core and security.NewRequestContext*. Guarding here rather than
	// at each call site keeps the internal helpers free of repeated nil checks.
	if logger == nil {
		logger = slog.Default()
	}

	email := adminEmail(logger)
	if email == "" {
		logger.Debug("first run: no admin address configured, leaving provisioning to first login")
		return nil
	}

	// Entitlement gate. TierSaaS is what an unlicensed or malformed-licence
	// Enterprise image resolves to, and its bootstrap denial is a deliberate
	// licensing control — provisioning an admin here would route around it and
	// hand an unlicensed deployment a working install.
	lic := license.Get()
	if lic.Tier() == license.TierSaaS {
		logger.Warn("first run: deployment is not entitled to provision an admin, skipping",
			"tier", lic.Tier(), "status", lic.Status())
		return nil
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return fmt.Errorf("first run: database unavailable: %w", err)
	}

	existingTenantID, err := firstTenantID(ctx, dbms)
	if err != nil {
		return fmt.Errorf("first run: tenant lookup failed: %w", err)
	}

	licenceTenantID := ""
	if lic.Tier() == license.TierEE {
		licenceTenantID = lic.TenantID()
	}

	// The free-to-paid transition. A deployment that ran unlicensed already has
	// a tenant with a generated id; a licence bought later names a different
	// one. Creating the licence's tenant here would leave the admin in one and
	// the cluster in the other — an empty-looking product at exactly the moment
	// the customer paid. Reconciling the two is an operator decision.
	if existingTenantID != "" && licenceTenantID != "" && existingTenantID != licenceTenantID {
		logger.Error("first run: this deployment already has a tenant that is not the licence's; refusing to create a second",
			"existing_tenant", existingTenantID, "licence_tenant", licenceTenantID)
		return nil
	}

	request := user.UserOnboardRequest{
		Username:    email,
		DisplayName: displayName,
		Status:      "active",
		Role:        adminRole,
	}

	// Exactly one of TenantId / TenantName must be set. Leaving both empty
	// takes OnboardUser's final branch, which creates a brand new tenant every
	// time — on a restart loop that mints one per boot.
	switch {
	case existingTenantID != "":
		request.TenantId = existingTenantID
	case licenceTenantID != "":
		request.TenantId = licenceTenantID
	default:
		// Get-or-create by name, which is what makes repeated boots converge on
		// a Community install with no licence tenant to key on.
		request.TenantName = user.GeneratedOrgName(displayName, email)
	}

	// NewRequestContextForSuperAdmin hardcodes context.Background(), which would
	// silently drop the caller's deadline: OnboardUser and everything it calls --
	// including the bundled agent's reconcile -- read the context off the request
	// context, so the boot timeout guarding the probe budget would not reach any
	// of it. Build the equivalent with ctx instead.
	reqCtx := security.NewRequestContext(ctx, security.NewSecurityContextForSuperAdmin(), logger, nil, nil)
	if _, err := user.OnboardUser(reqCtx, request); err != nil {
		return fmt.Errorf("first run: failed to provision the admin: %w", err)
	}

	logger.Info("first run: provisioned the first admin",
		"admin", email, "tenant", firstNonEmpty(request.TenantId, request.TenantName))
	return nil
}

// adminEmail resolves the address to provision.
//
// A licence that names an address wins over admin.email, and that precedence is
// load-bearing rather than a preference. licensedBootstrapCheck enforces the
// licence's address as an allowlist: on a licensed deployment no other address
// may bootstrap. But a pre-provisioned user never reaches that check --
// getOrCreateBootstrapAdminUser short-circuits for users that already exist --
// so honouring a conflicting admin.email here would mint an admin the licence
// does not authorise and hand it a working login. Same shape as the tier bypass
// the entitlement gate closes, one layer down.
//
// Community licences carry no address, so admin.email is authoritative there,
// which is the case it exists for.
func adminEmail(logger *slog.Logger) string {
	configured := strings.TrimSpace(config.Config.AdminEmail)
	licenced := strings.TrimSpace(license.Get().Email())

	if licenced == "" {
		return configured
	}
	if configured != "" && !strings.EqualFold(configured, licenced) {
		logger.Warn("first run: admin.email does not match the licence and is being ignored; the licence's address is authoritative",
			"configured", configured, "licence", licenced)
	}
	return licenced
}

// firstTenantID mirrors license.SingleTenantBootstrap's resolution, which is
// what the login path uses. The two must agree, or an install would provision
// into a tenant the admin never lands on.
func firstTenantID(ctx context.Context, dbms *database.DatabaseManager) (string, error) {
	var id string
	err := dbms.Db.QueryRowContext(ctx, "SELECT id::text FROM tenant ORDER BY created_at LIMIT 1").Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return id, nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
