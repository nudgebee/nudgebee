package observability

import (
	"context"
	"fmt"
	"nudgebee/services/account"
	"nudgebee/services/common"
	"nudgebee/services/integrations/core"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
	"strings"
	"sync"
	"time"

	"github.com/lib/pq"
)

// ProviderConnectionStatus is one signal's (logs / metrics / traces) effective
// provider and, when that provider is a non-agent integration, the result of
// probing it. IntegrationId is empty for agent-served signals — the agent's own
// connection_status is authoritative there and nothing is probed.
type ProviderConnectionStatus struct {
	ProviderType    string `json:"provider_type"`
	Provider        string `json:"provider"`
	Source          string `json:"source"`
	IntegrationId   string `json:"integration_id,omitempty"`
	IntegrationName string `json:"integration_name,omitempty"`
	Connected       bool   `json:"connected"`
	Error           string `json:"error,omitempty"`
}

// ProviderStatusKey is the server-managed key on agent.connection_status that
// holds the result of the last provider check. Owned by this package, never sent
// by the agent — see stampProviderStatus.
const ProviderStatusKey = "providerStatus"

// Seams so the resolver and the probe can be swapped in tests. Production wiring
// is the same resolver the Logs / Metrics / Traces tabs use, and the same probe
// the Integrations page's "Test Connection" runs (ValidateConfig + TestConnection).
var (
	resolveProviderForStatus = getLogsMetricsTracesProviderWithIntegration
	probeIntegration         = core.TestIntegrationConnection
	agentDetailsForStatus    = account.GetAgentConnectionDetails
)

var providerTypesForStatus = []string{"logs", "metrics", "traces"}

// agentReportedProvider is the backend the agent last told us it was wired to for a
// signal, mirroring the mapping in getLogsMetricsTracesProviderWithIntegration.
//
// The resolver refuses to hand back these values once the agent disconnects, and that is
// right for routing a query — a stale backend must not silently receive one. But this
// status view answers a descriptive question ("where do these logs come from?"), and the
// answer is still Loki even while the agent reporting it is offline. Reporting nothing
// there contradicted the Agent tab, which shows the provider from this same field.
func agentReportedProvider(features account.AgentDetailsFeatures, providerType string) string {
	promIsChronosphere := features.PrometheusUrl != nil && strings.Contains(*features.PrometheusUrl, "chronosphere")
	switch providerType {
	case "logs":
		if features.LogsConnectionProvider != nil {
			return *features.LogsConnectionProvider
		}
	case "traces":
		// TraceProvider alone proves nothing: every agent reports "otel_clickhouse" whether
		// or not a traces backend exists (verified across the fleet — the field is constant
		// while TracesEnabled varies). Only TracesEnabled says traces are actually wired, so
		// without it this signal is reported as unconfigured rather than named after a
		// default the agent always sends.
		if features.TracesEnabled == nil || !*features.TracesEnabled {
			return ""
		}
		if promIsChronosphere {
			return "chronosphere"
		}
		if features.TraceProvider != nil {
			return *features.TraceProvider
		}
	case "metrics":
		if promIsChronosphere {
			return "chronosphere"
		}
		if features.PrometheusUrl != nil && *features.PrometheusUrl != "" {
			return "prometheus"
		}
	}
	return ""
}

// CheckProviderStatus reports, per signal, which provider actually serves the
// account and — for the ones served by a non-agent integration (a hosted Loki,
// Datadog, Elasticsearch, …) — whether that integration answers its own
// validation probe. Backs the Agent Details page, so it stops reading the
// agent's Loki/Prometheus flags for signals the agent does not serve.
//
// Probes run one after another on purpose: this package keeps DB access off
// concurrent goroutines (see GetIntegrationByConfigNameValues), and the probe
// itself queries the integration tables.
func CheckProviderStatus(ctx *security.RequestContext, accountId string) ([]ProviderConnectionStatus, error) {
	if accountId == "" {
		return nil, fmt.Errorf("observability: account_id is required")
	}

	// Fetched once and shared across the three signals; only consulted as a display
	// fallback below. A missing agent row is not an error here — an account can be served
	// entirely by integrations.
	agentFeatures, agentErr := agentDetailsForStatus(accountId)

	statuses := make([]ProviderConnectionStatus, 0, len(providerTypesForStatus))
	for _, providerType := range providerTypesForStatus {
		status := ProviderConnectionStatus{ProviderType: providerType}

		provider, source, integration, err := resolveProviderForStatus(ctx, accountId, "", providerType, "")
		if err != nil {
			// Nothing serves this signal (no default integration and no live agent).
			// Keep the reason, then fall through to the agent's last-reported backend so
			// the row still names where this signal comes from.
			status.Error = err.Error()
		} else {
			status.Provider = provider
			status.Source = source
		}

		// Nothing resolved — either the resolver errored, or it withheld a stale
		// agent-reported provider. Name the agent's last-known backend for display.
		if status.Provider == "" && agentErr == nil {
			if reported := agentReportedProvider(agentFeatures.Features, providerType); reported != "" {
				status.Provider = reported
				status.Source = "agent"
			}
		}

		// Agent-served (including the agent's own auto-flagged default row), or a
		// synthetic non-integration source such as GCP Cloud Trace: nothing to probe.
		if err != nil || integration == nil || source == "agent" {
			statuses = append(statuses, status)
			continue
		}

		status.IntegrationId = integration.Id
		status.IntegrationName = integration.Name
		if probeErr := probeIntegration(ctx, integration.Id); probeErr != nil {
			status.Error = probeErr.Error()
		} else {
			status.Connected = true
		}
		statuses = append(statuses, status)
	}
	return statuses, nil
}

// refreshInFlight drops overlapping refreshes of the same account, so a cron tick
// that overlaps an integration-save trigger probes each backend once rather than
// twice. Mirrors the inFlightUpdates pattern in services/account/agent_service.go.
var refreshInFlight sync.Map // key: account id string, value: struct{}

// providerStatusAccount is one candidate cluster and the tenant that owns it.
type providerStatusAccount struct {
	AccountId string `db:"cloud_account_id"`
	TenantId  string `db:"tenant"`
}

// providerStatusAccountsQuery selects the K8s clusters this check exists for: those with a
// non-agent integration standing as the default provider for at least one signal, plus any
// cluster already carrying a stamp. The second arm is what lets a stamp clear itself — once
// the operator deletes or disables that integration the account no longer matches the first
// arm, and this run rewrites the stamp with the agent-served answer instead of leaving the
// last probe result frozen on screen.
//
// It deliberately does NOT cover every K8s cluster. Widening it was tried and reverted: a
// 176-account sweep blew through the cron's 10-minute deadline and halted at 147, because
// resolution is several database round trips per signal. It also bought nothing — an
// agent-served signal needs no probe, and the page already holds the agent's own reported
// provider from the very same row it renders the Agent tab from, so it fills those in
// locally rather than waiting on a cron to copy them into a stamp.
//
// The `a.type != 'proxy'` guard picks the row the agent's own telemetry writes, which is
// the row the UI reads and the row stampProviderStatus updates.
const providerStatusAccountsQuery = `
	SELECT ca.id::varchar AS cloud_account_id, ca.tenant::varchar AS tenant
	FROM cloud_accounts ca
	INNER JOIN agent a ON ca.id = a.cloud_account_id AND a.type != 'proxy'
	WHERE ca.cloud_provider = 'K8s'
	  AND (
	    EXISTS (
	      SELECT 1
	      FROM integrations_cloud_accounts ica
	      INNER JOIN integrations i ON i.id = ica.integration_id
	      WHERE ica.cloud_account_id = ca.id
	        AND i.source::text != 'agent'
	        AND i.status::text != 'disabled'
	        AND (ica.default_log_provider OR ica.default_metrics_provider OR ica.default_traces_provider)
	    )
	    OR COALESCE(a.connection_status, '{}'::jsonb) ? '` + ProviderStatusKey + `'
	  )
	GROUP BY ca.id, ca.tenant`

// providerStatusAccountsByIdQuery is the explicit-account variant (manual cron
// payload, and the post-save trigger). It deliberately drops the eligibility arms:
// an account named explicitly has just had its integrations changed, so it must be
// re-checked even when that change was the removal of the last one.
const providerStatusAccountsByIdQuery = `
	SELECT ca.id::varchar AS cloud_account_id, ca.tenant::varchar AS tenant
	FROM cloud_accounts ca
	INNER JOIN agent a ON ca.id = a.cloud_account_id AND a.type != 'proxy'
	WHERE ca.id = ANY($1::uuid[])
	  AND ca.cloud_provider = 'K8s'
	GROUP BY ca.id, ca.tenant`

func listProviderStatusAccounts(ctx context.Context, accountIds []string) ([]providerStatusAccount, error) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, err
	}
	var accounts []providerStatusAccount
	if len(accountIds) == 0 {
		if err := dbms.Db.SelectContext(ctx, &accounts, providerStatusAccountsQuery); err != nil {
			return nil, err
		}
		return accounts, nil
	}
	if err := dbms.Db.SelectContext(ctx, &accounts, providerStatusAccountsByIdQuery, pq.Array(accountIds)); err != nil {
		return nil, err
	}
	return accounts, nil
}

// stampProviderStatus writes the check result onto the account's agent row under
// ProviderStatusKey.
//
// Writing to connection_status from the server is safe even though the agent owns
// the column: the k8s telemetry handler merges (`||`) each heartbeat into it rather
// than replacing it, and the agent never sends this key — the same pattern
// spend.setOpenCostServerSide and scan_orchestrator's schedule_jobs rely on.
func stampProviderStatus(ctx *security.RequestContext, accountId string, statuses []ProviderConnectionStatus) error {
	// Nothing here is served by an integration, so there is no probe result to publish and
	// the page fills the row from the agent's own fields. Drop the key rather than storing a
	// contentless stamp: it is what the "or already stamped" arm of the candidate query keys
	// off, so leaving it behind would keep re-checking an account forever after its last
	// integration was removed. This is what makes that arm self-limiting.
	if !hasIntegrationServedSignal(statuses) {
		return clearProviderStatus(ctx, accountId)
	}

	stamp := map[string]any{"checkedAt": time.Now().UTC().Format(time.RFC3339)}
	for _, status := range statuses {
		stamp[status.ProviderType] = status
	}
	encoded, err := common.MarshalJson(stamp)
	if err != nil {
		return err
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return err
	}
	const q = `
		UPDATE agent
		SET connection_status = COALESCE(connection_status, '{}'::jsonb)
		                        || jsonb_build_object('` + ProviderStatusKey + `', $2::jsonb)
		WHERE cloud_account_id = $1 AND type != 'proxy'`
	if _, err := dbms.Db.ExecContext(ctx.GetContext(), q, accountId, string(encoded)); err != nil {
		return err
	}
	// GetAgentConnectionDetails caches the row for 15 minutes, so without this the
	// stamp we just wrote would be invisible to server-side readers until it expired.
	if err := account.InvalidateAgentCache(accountId); err != nil {
		ctx.GetLogger().Warn("provider status: failed to invalidate agent cache", "account_id", accountId, "error", err)
	}
	return nil
}

// hasIntegrationServedSignal reports whether any signal is served by a non-agent
// integration — the only case this check produces information the page cannot derive
// on its own.
func hasIntegrationServedSignal(statuses []ProviderConnectionStatus) bool {
	for _, status := range statuses {
		if status.IntegrationId != "" {
			return true
		}
	}
	return false
}

// clearProviderStatus removes the stamp, returning the account to "the agent describes
// itself" and dropping it out of the candidate query on the next run.
func clearProviderStatus(ctx *security.RequestContext, accountId string) error {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return err
	}
	const q = `
		UPDATE agent
		SET connection_status = connection_status - '` + ProviderStatusKey + `'
		WHERE cloud_account_id = $1 AND type != 'proxy'
		  AND connection_status ? '` + ProviderStatusKey + `'`
	if _, err := dbms.Db.ExecContext(ctx.GetContext(), q, accountId); err != nil {
		return err
	}
	if err := account.InvalidateAgentCache(accountId); err != nil {
		ctx.GetLogger().Warn("provider status: failed to invalidate agent cache", "account_id", accountId, "error", err)
	}
	return nil
}

// RefreshProviderStatus re-checks each candidate cluster's log / metric / trace
// providers and stamps the result onto its agent row, so the Agent Details page can
// report a non-agent backend's real state instead of the agent's irrelevant flag.
// Empty accountIds means "every candidate" (the cron); a non-empty list re-checks
// exactly those accounts (manual cron payload, or an integration having just changed).
//
// Per-account failures are logged and skipped rather than aborting the run: one
// unreachable backend must not stop the rest of the fleet being checked.
func RefreshProviderStatus(ctx *security.RequestContext, accountIds []string) error {
	t0 := time.Now()
	logger := ctx.GetLogger()

	accounts, err := listProviderStatusAccounts(ctx.GetContext(), accountIds)
	if err != nil {
		return fmt.Errorf("provider status refresh: list accounts: %w", err)
	}
	logger.Info("provider status refresh: processing accounts", "count", len(accounts))

	var checked, skipped, failed int
	for _, acc := range accounts {
		// Bail if the caller's deadline fired rather than grinding through the rest.
		if err := ctx.GetContext().Err(); err != nil {
			logger.Warn("provider status refresh: context done, halting",
				"error", err, "checked", checked, "remaining", len(accounts)-checked-skipped-failed)
			return err
		}
		if _, loaded := refreshInFlight.LoadOrStore(acc.AccountId, struct{}{}); loaded {
			skipped++
			continue
		}
		// Per-account closure so the in-flight guard is released by defer on every exit
		// path — an early return or a panic in the probe must not leave this account
		// permanently marked in-flight and therefore never checked again.
		func() {
			defer refreshInFlight.Delete(acc.AccountId)

			accLogger := logger.With("account_id", acc.AccountId, "tenant_id", acc.TenantId)

			// The resolver and the probe both filter on tenant_id, so a super-admin cron
			// context would resolve nothing. Re-scope to the tenant that owns the account.
			accCtx := security.NewRequestContext(
				ctx.GetContext(),
				security.NewSecurityContextForTenantAdmin(acc.TenantId),
				accLogger,
				ctx.GetTracer(),
				ctx.GetMeter(),
			)

			statuses, err := CheckProviderStatus(accCtx, acc.AccountId)
			if err != nil {
				failed++
				accLogger.Error("provider status refresh: check failed", "error", err)
				return
			}
			if err := stampProviderStatus(accCtx, acc.AccountId, statuses); err != nil {
				failed++
				accLogger.Error("provider status refresh: failed to stamp status", "error", err)
				return
			}
			checked++
		}()
	}
	logger.Info("provider status refresh: done",
		"checked", checked, "skipped", skipped, "failed", failed, "duration", time.Since(t0))
	return nil
}
