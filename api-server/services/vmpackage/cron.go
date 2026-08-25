package vmpackage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"nudgebee/services/account"
	"nudgebee/services/common"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
)

// DiscoveryLabels is the shape forager's own metadata reporting writes into
// integrations.labels for a discovery-type datasource — confirmed against
// forager's discovery.Proxy.CollectMetadata: actions lists what the
// datasource can currently serve (discovery_inventory only appears once SSH
// creds + a pack key are configured), allowed_cidrs is the segment scope
// discovery_sweep must stay within, pack_versions are the content-pack
// versions available to pin in discovery_inventory's content_pack_version.
type DiscoveryLabels struct {
	Actions      []string `json:"actions"`
	AllowedCIDRs []string `json:"allowed_cidrs"`
	PackVersions []int    `json:"pack_versions"`
}

// DiscoveryDatasource is one discovery-type integration eligible for the
// cron sweep, scoped to the tenant/account it belongs to. Exported so the
// vmpackage/queue consumer (a separate package, to avoid an import cycle
// with the publisher call in the cron handler) can re-resolve and scan one
// datasource by id.
type DiscoveryDatasource struct {
	IntegrationID string
	TenantID      string
	// AccountID is the account the forager agent itself runs in (usually a
	// Kubernetes/self-hosted account) — where a fallback vm-<ip> placeholder
	// is filed when nothing on TargetAccountID matches. Not the account
	// whose network is being scanned; see TargetAccountID for that.
	AccountID string
	// TargetAccountID is the cloud account (AWS/Azure/GCP) whose network
	// this datasource scans, set via integrations_upsert_discovery_target
	// (link_role='discovery_target' in integrations_cloud_accounts). Empty
	// when unassociated — resource matching is skipped entirely in that
	// case and behavior is identical to before this field existed.
	TargetAccountID string
	Labels          DiscoveryLabels
}

// eligibleDiscoveryLabels parses a raw integrations.labels JSON blob and
// reports whether the datasource it describes can currently serve
// discovery_sweep. Shared by ListDiscoveryDatasources (every eligible
// datasource) and GetDiscoveryDatasourceByID (one, re-checked at consume
// time in case labels changed since the message was published).
func eligibleDiscoveryLabels(raw string) (DiscoveryLabels, bool) {
	var labels DiscoveryLabels
	if raw != "" {
		if err := common.UnmarshalJson([]byte(raw), &labels); err != nil {
			return DiscoveryLabels{}, false
		}
	}
	if !slices.Contains(labels.Actions, "discovery_sweep") || len(labels.AllowedCIDRs) == 0 {
		return DiscoveryLabels{}, false
	}
	return labels, true
}

// ListDiscoveryDatasources returns every discovery-type integration across
// every tenant/account, parsed and filtered down to the ones that can
// actually serve discovery_sweep. There is no separate feature flag —
// forager reporting a discovery-proxy datasource at all is the opt-in.
//
// Joins through integrations_cloud_accounts rather than filtering on
// integrations.account_id: the relay's auto-registration
// (UpsertAgentDatasources) never sets that scalar column for agent-sourced
// integrations, only the tenant_id column and the integrations_cloud_accounts
// mapping row — unlike vm_agent-style integrations created through the UI
// config flow, which do set it. resolveDatasourceKey's i.account_id filter
// would silently match nothing here.
//
// The join is split by link_role rather than left unfiltered: an integration
// can carry two integrations_cloud_accounts rows (the agent's own account,
// link_role='own', and optionally a user-associated scan target,
// link_role='discovery_target' — see integrations_upsert_discovery_target).
// Joining unfiltered would return one row per (integration, account) pair
// and dispatch the same forager twice; the 'own' join drives one row per
// integration as before, with the target account folded in as an extra
// column instead of a second row.
//
// The target side is further deduplicated with a DISTINCT ON subquery:
// UpsertDiscoveryTarget enforces one target per datasource by clearing any
// prior row before inserting the new one, but that clear-then-insert isn't
// atomic against the table's unique constraint (which keys on
// cloud_account_id too, so two different target accounts don't conflict) —
// a plain LEFT JOIN could still return two rows for one integration if a
// stale row ever survives, and dispatch the same forager twice.
func ListDiscoveryDatasources(dbms *database.DatabaseManager) ([]DiscoveryDatasource, error) {
	var rows []struct {
		IntegrationID   string         `db:"integration_id"`
		TenantID        string         `db:"tenant_id"`
		AccountID       string         `db:"account_id"`
		TargetAccountID sql.NullString `db:"target_account_id"`
		Labels          string         `db:"labels"`
	}
	err := dbms.Db.Select(&rows, `
		SELECT i.id::text AS integration_id, i.tenant_id::varchar AS tenant_id,
		       own.cloud_account_id::varchar AS account_id,
		       target.cloud_account_id::varchar AS target_account_id,
		       i.labels::text AS labels
		FROM integrations i
		JOIN integrations_cloud_accounts own ON own.integration_id = i.id AND own.link_role = 'own'
		LEFT JOIN (
			SELECT DISTINCT ON (integration_id) integration_id, cloud_account_id
			FROM integrations_cloud_accounts
			WHERE link_role = 'discovery_target'
			ORDER BY integration_id, id DESC
		) target ON target.integration_id = i.id
		WHERE i.type = 'discovery' AND i.status = 'enabled'`)
	if err != nil {
		return nil, fmt.Errorf("vmpackage: list discovery datasources: %w", err)
	}

	datasources := make([]DiscoveryDatasource, 0, len(rows))
	for _, r := range rows {
		labels, ok := eligibleDiscoveryLabels(r.Labels)
		if !ok {
			// One malformed/ineligible labels blob shouldn't take down the
			// whole sweep — skip it and keep going.
			continue
		}
		datasources = append(datasources, DiscoveryDatasource{
			IntegrationID:   r.IntegrationID,
			TenantID:        r.TenantID,
			AccountID:       r.AccountID,
			TargetAccountID: r.TargetAccountID.String,
			Labels:          labels,
		})
	}
	return datasources, nil
}

// GetDiscoveryDatasourceByID re-resolves a single discovery datasource by
// integration+account id, for the queue consumer to pick up current labels
// right before scanning rather than trusting a possibly-stale copy carried
// in the RabbitMQ message. Returns ok=false (no error) when the integration
// no longer exists, was disabled, or no longer qualifies — the consumer
// treats that as "nothing to do", not a failure.
func GetDiscoveryDatasourceByID(dbms *database.DatabaseManager, integrationID, accountID string) (DiscoveryDatasource, bool, error) {
	var row struct {
		TenantID        string         `db:"tenant_id"`
		TargetAccountID sql.NullString `db:"target_account_id"`
		Labels          string         `db:"labels"`
	}
	err := dbms.Db.Get(&row, `
		SELECT i.tenant_id::varchar AS tenant_id,
		       target.cloud_account_id::varchar AS target_account_id,
		       i.labels::text AS labels
		FROM integrations i
		JOIN integrations_cloud_accounts own ON own.integration_id = i.id AND own.link_role = 'own'
		LEFT JOIN integrations_cloud_accounts target ON target.integration_id = i.id AND target.link_role = 'discovery_target'
		WHERE i.id = $1 AND own.cloud_account_id = $2 AND i.type = 'discovery' AND i.status = 'enabled'
		LIMIT 1`,
		integrationID, accountID)
	if errors.Is(err, sql.ErrNoRows) {
		return DiscoveryDatasource{}, false, nil
	}
	if err != nil {
		return DiscoveryDatasource{}, false, fmt.Errorf("vmpackage: get discovery datasource %s: %w", integrationID, err)
	}

	labels, ok := eligibleDiscoveryLabels(row.Labels)
	if !ok {
		return DiscoveryDatasource{}, false, nil
	}
	return DiscoveryDatasource{
		IntegrationID:   integrationID,
		TenantID:        row.TenantID,
		AccountID:       accountID,
		TargetAccountID: row.TargetAccountID.String,
		Labels:          labels,
	}, true, nil
}

// resolveDiscoveryDatasourceKey resolves a discovery integration's own id
// (not an agent access_key or a caller-supplied datasource_id) to its
// forager-side datasource_key — the same integration_config_values lookup
// resolveDatasourceKey uses, minus the account_id/tenant_id filter that
// column doesn't carry for these auto-registered rows (see
// listDiscoveryDatasources).
func resolveDiscoveryDatasourceKey(dbms *database.DatabaseManager, integrationID string) (string, error) {
	var datasourceKey string
	err := dbms.Db.Get(&datasourceKey,
		`SELECT value FROM integration_config_values WHERE integration_id = $1 AND name = 'datasource_key'`,
		integrationID)
	if err != nil {
		return "", fmt.Errorf("vmpackage: resolve datasource_key for integration %s: %w", integrationID, err)
	}
	return datasourceKey, nil
}

// findOrCreateVMResource returns the cloud_resourses id for a swept IP,
// creating a minimal row if none exists yet. VM resources are one-per-IP
// with resourse_id "vm-<ip>" (see verifyCloudResource) — this is the
// fallback used only when resolveTargets (resource_match.go) found no
// identity/MAC/IP match against an existing cloud-collector resource. Column
// set mirrors application/discovery.go's existing unattended cloud_resourses
// insert (~line 134), which already proves this reduced set (no
// created_by/business_unit/etc.) works against the live schema. meta carries
// PrivateIpAddress so resolveResourceIP (resource_match.go) has a uniform
// way to recover a resource's IP regardless of which path created it.
func findOrCreateVMResource(dbms *database.DatabaseManager, tenantID, accountID, ip string) (string, error) {
	resourceID := vmResourceIDPrefix + ip

	var id string
	err := dbms.Db.Get(&id,
		`SELECT id::text FROM cloud_resourses WHERE resourse_id = $1 AND account = $2 AND tenant = $3`,
		resourceID, accountID, tenantID)
	if err == nil {
		return id, nil
	}

	meta, err := json.Marshal(map[string]string{"PrivateIpAddress": ip})
	if err != nil {
		return "", fmt.Errorf("vmpackage: marshal meta for %s: %w", resourceID, err)
	}

	id = common.GenerateUUID()
	_, err = dbms.Db.Exec(`
		INSERT INTO public.cloud_resourses
		  (id, created_at, updated_at, resourse_id, "name", "type", status, account, cloud_provider, region, arn, tenant, tags, meta, external_resource_id, is_active)
		VALUES ($1, now(), now(), $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $2, true)
		ON CONFLICT DO NOTHING`,
		id, resourceID, ip, "VM", "Active", accountID, account.AccountProviderSelfHosted, "global", "", tenantID, "{}", string(meta))
	if err != nil {
		return "", fmt.Errorf("vmpackage: create cloud_resourse for %s: %w", resourceID, err)
	}

	// Someone else may have created it concurrently between the lookup and
	// the ON CONFLICT DO NOTHING insert (no unique constraint on
	// (account, tenant, resourse_id) to conflict against) — re-read rather
	// than trust the id we generated.
	if err := dbms.Db.Get(&id,
		`SELECT id::text FROM cloud_resourses WHERE resourse_id = $1 AND account = $2 AND tenant = $3`,
		resourceID, accountID, tenantID); err != nil {
		return "", fmt.Errorf("vmpackage: read back cloud_resourse for %s: %w", resourceID, err)
	}
	return id, nil
}

// maxInt returns the largest value in vs, or 0 for an empty slice.
func maxInt(vs []int) int {
	m := 0
	for _, v := range vs {
		if v > m {
			m = v
		}
	}
	return m
}

// ScanDiscoveryDatasource is the per-datasource work function: sweeps
// ds.Labels.AllowedCIDRs for live hosts, inventories whatever the sweep
// found (when the datasource can serve discovery_inventory too), and runs
// each reachable host through the existing parse -> match -> persist
// pipeline (runScan). It is the unit of work the RabbitMQ consumer
// (vmpackage/queue) invokes for one message — panic-recovered internally so
// one bad datasource can never take down the consumer goroutine.
func ScanDiscoveryDatasource(ctx *security.RequestContext, dbms *database.DatabaseManager, ds DiscoveryDatasource) {
	tenantCtx := security.NewRequestContext(
		ctx.GetContext(),
		security.NewSecurityContextForTenantAdmin(ds.TenantID),
		ctx.GetLogger().With("tenant_id", ds.TenantID, "account_id", ds.AccountID, "integration_id", ds.IntegrationID),
		ctx.GetTracer(),
		ctx.GetMeter(),
	)
	defer func() {
		if r := recover(); r != nil {
			tenantCtx.GetLogger().Error("vmpackage: panic scanning discovery datasource", "panic", r)
		}
	}()

	datasourceKey, err := resolveDiscoveryDatasourceKey(dbms, ds.IntegrationID)
	if err != nil {
		tenantCtx.GetLogger().Warn("vmpackage: resolve datasource_key failed, skipping", "error", err)
		return
	}

	hosts, err := fetchDiscoverySweep(ds.AccountID, datasourceKey, ds.Labels.AllowedCIDRs)
	if err != nil {
		tenantCtx.GetLogger().Error("vmpackage: discovery_sweep failed", "error", err)
		return
	}
	tenantCtx.GetLogger().Info("vmpackage: discovery_sweep complete", "hosts_found", len(hosts))
	if len(hosts) == 0 {
		return
	}

	// Resolved right off the sweep response — identity/MAC/IP matching
	// against cloud_resourses doesn't need (and isn't gated on) inventory
	// running at all; see resource_match.go's package doc. A no-op when
	// this datasource has no target account associated yet. Per-host
	// resolution errors are logged and skipped inside resolveTargets, not
	// propagated here — one bad host must not block the rest of the sweep.
	resolved := resolveTargets(tenantCtx.GetLogger(), dbms, ds.TargetAccountID, ds.AccountID, ds.TenantID, hosts)

	if !slices.Contains(ds.Labels.Actions, "discovery_inventory") || len(ds.Labels.PackVersions) == 0 {
		// Sweep-only datasource (no SSH creds/pack key configured yet) — there's
		// nothing to inventory. Presence (and, when associated, identity) on
		// the network is all we can report.
		return
	}

	ips := make([]string, 0, len(hosts))
	for _, h := range hosts {
		ips = append(ips, h.IP)
	}
	packVersion := maxInt(ds.Labels.PackVersions)

	targets, err := fetchDiscoveryInventoryBatch(ds.AccountID, datasourceKey, ips, packVersion)
	if err != nil {
		tenantCtx.GetLogger().Error("vmpackage: discovery_inventory batch failed", "error", err)
		return
	}

	for _, target := range targets {
		scanTargetSafely(tenantCtx, dbms, ds, target, resolved[target.Host])
	}
}

func scanTargetSafely(tenantCtx *security.RequestContext, dbms *database.DatabaseManager, ds DiscoveryDatasource, target discoveryTarget, res resolvedTarget) {
	defer func() {
		if r := recover(); r != nil {
			tenantCtx.GetLogger().Error("vmpackage: panic scanning target", "host", target.Host, "panic", r)
		}
	}()

	if target.Status != "ok" {
		tenantCtx.GetLogger().Debug("vmpackage: target not ok, skipping", "host", target.Host, "status", target.Status)
		return
	}

	cloudResourceID, resolvedAccountID := res.CloudResourceID, res.AccountID
	if cloudResourceID == "" {
		// Tiers 0-2 (resource_match.go) found nothing under the associated
		// target account, or the datasource has no target account
		// associated yet — fall back to the bare vm-<ip> placeholder under
		// the agent's own account, exactly as before this change.
		var err error
		cloudResourceID, err = findOrCreateVMResource(dbms, ds.TenantID, ds.AccountID, target.Host)
		if err != nil {
			tenantCtx.GetLogger().Error("vmpackage: find-or-create cloud_resourse failed", "host", target.Host, "error", err)
			return
		}
		resolvedAccountID = ds.AccountID
	}

	if err := runScan(tenantCtx, resolvedAccountID, ds.TenantID, cloudResourceID, target); err != nil {
		tenantCtx.GetLogger().Error("vmpackage: scan failed", "cloud_resource_id", cloudResourceID, "host", target.Host, "error", err)
	}
}
