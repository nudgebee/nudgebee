package user

import (
	"context"
	core "nudgebee/services/integrations/core"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
	"nudgebee/services/tenant"
	"time"

	"github.com/jmoiron/sqlx"
)

// syncTypes is the fixed set of integration types we pull identities from. Each is
// expected to implement core.UserLister.
var syncTypes = []string{
	IdentityTypePagerDuty,
	IdentityTypeZenDuty,
	IdentityTypeSlack,
	IdentityTypeGithub,
	IdentityTypeServiceNow,
	IdentityTypeGitlab,
	IdentityTypeJira,
	IdentityTypeMsTeams,
}

// resyncInterval skips an integration whose accounts were synced more recently than
// this, so a manual re-trigger or an overlapping cron tick doesn't redo recent work.
const resyncInterval = 30 * time.Minute

// RunIdentitySync fetches external integration accounts for each enabled
// Slack/GitHub/PagerDuty/ZenDuty integration, upserts one row per (account,
// external user), and auto-matches by email. Invoked from the "Identity Sync"
// cron (api/cron.go). When tenantFilter is non-empty only that tenant is synced
// (pass `tenant_id` in the cron payload) — otherwise every tenant is walked. One
// provider/tenant failure is logged and skipped — it never aborts the whole run.
// Returns the number of account rows upserted.
func RunIdentitySync(ctx *security.RequestContext, tenantFilter string) (int, error) {
	t0 := time.Now()
	log := ctx.GetLogger()

	manager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return 0, err
	}

	tenants, err := tenant.ListTenants(ctx)
	if err != nil {
		log.Error("identity sync: error listing tenants", "error", err)
		return 0, err
	}

	totalUpserted := 0
	for _, t := range tenants {
		if tenantFilter != "" && t.Id != tenantFilter {
			continue
		}
		// Tenant-scoped admin context so ListIntegrationConfigsByTenant resolves
		// the right tenant and authorizes the read.
		tctx := security.NewRequestContext(
			ctx.GetContext(),
			security.NewSecurityContextForTenantAdmin(t.Id),
			log.With("tenant_id", t.Id),
			ctx.GetTracer(), ctx.GetMeter(),
		)

		tenantUpserted := syncTenant(ctx.GetContext(), tctx, manager.Db, t.Id)
		if tenantUpserted > 0 {
			matched, err := autoMatchByEmail(manager.Db, t.Id)
			if err != nil {
				tctx.GetLogger().Error("identity sync: auto-match failed", "error", err)
			} else if matched > 0 {
				tctx.GetLogger().Info("identity sync: auto-matched accounts", "count", matched)
			}
		}
		totalUpserted += tenantUpserted
	}

	log.Info("identity sync: done", "tenants", len(tenants), "accounts_upserted", totalUpserted, "duration", time.Since(t0))
	return totalUpserted, nil
}

// syncTenant syncs all supported integration types for one tenant and returns the
// number of account rows upserted.
func syncTenant(fetchCtx context.Context, tctx *security.RequestContext, db *sqlx.DB, tenantId string) int {
	log := tctx.GetLogger()
	log.Info("identity sync: syncing tenant", "tenant_id", tenantId)
	upserted := 0
	enabledIntegrationIds := []string{}
	enumerationOk := true
	for _, integType := range syncTypes {
		log.Info("identity sync: enumerating integrations", "integration_type", integType)
		configs, err := core.ListIntegrationConfigsByTenant(tctx, integType)
		if err != nil {
			// A genuine read failure (not "no integrations of this type", which
			// returns an empty list with no error). Skip the disabled-integration
			// sweep so a transient DB error can't wipe valid rows.
			log.Warn("identity sync: enumeration failed", "integration_type", integType, "error", err)
			enumerationOk = false
			continue
		}
		log.Info("identity sync: enumerated integrations", "integration_type", integType, "enabled_instances", len(configs))
		for _, intg := range configs {
			enabledIntegrationIds = append(enabledIntegrationIds, intg.Id)
			upserted += syncIntegration(fetchCtx, tctx, db, tenantId, integType, intg)
		}
	}

	// Tombstone/delete rows whose integration was disabled or removed. Only when the
	// enabled set is fully known, so a transient enumeration error is never
	// interpreted as "every integration is gone".
	if enumerationOk {
		if err := sweepDisabledIntegrations(db, tenantId, syncTypes, enabledIntegrationIds); err != nil {
			tctx.GetLogger().Warn("identity sync: disabled-integration sweep failed", "tenant_id", tenantId, "error", err)
		}
	}
	return upserted
}

// syncIntegration syncs one integration instance. Scope follows the integration's
// category: tenant-scoped types (messaging/ticketing) store one row with a NULL
// account_id; account-scoped types fan out one row per cloud account from
// integrations_cloud_accounts and are skipped if scoped to none.
func syncIntegration(fetchCtx context.Context, tctx *security.RequestContext, db *sqlx.DB, tenantId, integType string, intg core.IntegrationDto) int {
	integration, found := core.GetIntegration(integType)
	if !found {
		return 0
	}
	lister, ok := integration.(core.UserLister)
	if !ok {
		// Integration exposes no user concept — nothing to sync (same optional
		// capability pattern as TestableIntegration).
		return 0
	}

	log := tctx.GetLogger().With("integration_type", integType, "integration_id", intg.Id)

	// Skip integrations already refreshed within resyncInterval so a manual
	// re-trigger or overlapping cron tick doesn't redo recent work. The integration
	// stays in the caller's enabled set, so its rows are preserved (not swept).
	if recent, err := integrationRecentlySynced(db, tenantId, intg.Id, resyncInterval); err != nil {
		log.Warn("identity sync: last-sync lookup failed", "error", err)
	} else if recent {
		log.Info("identity sync: skipping, synced within window", "window", resyncInterval.String())
		return 0
	}

	accountIds, err := resolveAccountScopes(db, tenantId, integration, intg.Id)
	if err != nil {
		log.Warn("identity sync: account lookup failed", "error", err)
		return 0
	}
	if len(accountIds) == 0 {
		return 0
	}

	// Bound each fetch so one slow/unreachable integration (e.g. a PagerDuty key
	// pointing at an unresponsive host — the SDK client has no timeout of its own)
	// cannot stall the whole cross-tenant run. ListUsers implementations honor ctx.
	log.Info("identity sync: fetching users")
	fetchStart := time.Now()
	fetchTimeoutCtx, cancel := context.WithTimeout(fetchCtx, 25*time.Second)
	defer cancel()
	accounts, err := lister.ListUsers(fetchTimeoutCtx, intg.Configs)
	if err != nil {
		log.Warn("identity sync: fetch failed", "fetch_ms", time.Since(fetchStart).Milliseconds(), "error", err)
		return 0
	}
	log.Info("identity sync: fetched users", "users", len(accounts), "fetch_ms", time.Since(fetchStart).Milliseconds())

	upsertStart := time.Now()
	upserted, seen := upsertAccounts(tctx, db, tenantId, integType, intg.Id, accountIds, accounts)
	log.Info("identity sync: upserted accounts", "rows", upserted, "scoped_accounts", len(accountIds), "upsert_ms", time.Since(upsertStart).Milliseconds())

	// Prune accounts removed at source for this integration (manual -> tombstone,
	// others -> delete). Safe: reconcileIntegration no-ops on an empty fetch, and we
	// only reach here on a successful fetch.
	if err := reconcileIntegration(db, tenantId, intg.Id, seen); err != nil {
		log.Warn("identity sync: reconcile failed", "error", err)
	}
	return upserted
}

// upsertAccounts writes one row per (account, external user) in a single batched
// statement and returns how many rows were upserted plus the set of external user
// ids seen (for reconciliation).
func upsertAccounts(tctx *security.RequestContext, db *sqlx.DB, tenantId, integType, integrationId string, accountIds []string, accounts []core.ExternalUser) (int, []string) {
	seen := make([]string, 0, len(accounts))
	items := make([]syncedAccount, 0, len(accountIds)*len(accounts))
	for _, acc := range accounts {
		if acc.ID == "" {
			continue
		}
		seen = append(seen, acc.ID)
		for _, accountId := range accountIds {
			items = append(items, syncedAccount{accountId: accountId, acc: acc})
		}
	}
	if len(items) == 0 {
		return 0, seen
	}

	upserted, err := upsertSyncedAccounts(db, tenantId, integType, integrationId, items)
	if err != nil {
		tctx.GetLogger().Warn("identity sync: upsert failed",
			"integration_type", integType, "integration_id", integrationId, "error", err)
	}
	return upserted, seen
}

// resolveAccountScopes returns the account ids to fan an integration out across.
// Tenant-scoped types (messaging/ticketing) return a single empty string, which
// the upsert stores as a NULL account_id. Account-scoped types return their
// cloud accounts (empty -> not integrated -> caller skips).
func resolveAccountScopes(db *sqlx.DB, tenantId string, integration core.Integration, integrationId string) ([]string, error) {
	if integration.Category().IsTenantScoped() {
		return []string{""}, nil
	}
	return listIntegrationAccountIds(db, tenantId, integrationId)
}

// listIntegrationAccountIds returns the cloud accounts an integration instance is
// scoped to, from integrations_cloud_accounts.
func listIntegrationAccountIds(db *sqlx.DB, tenantId, integrationId string) ([]string, error) {
	var ids []string
	err := db.Select(&ids,
		`SELECT cloud_account_id::text FROM integrations_cloud_accounts
		 WHERE integration_id = $1::uuid AND tenant_id = $2::uuid`,
		integrationId, tenantId)
	return ids, err
}
