package spend

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"nudgebee/services/config"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
	"nudgebee/services/tenant"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// SyncOpenCostSpends drives per-cluster cost ingestion from the centrally-deployed
// OpenCost instead of a per-customer in-cluster OpenCost + agent. For each active
// K8s cloud account it asks the k8s-collector for the next ingestion window, pulls
// node- and pod-level allocations from central OpenCost (keyed by X-Scope-OrgID =
// cloud account id, which OpenCost resolves to that cluster's Prometheus via the
// relay), then POSTs them back to the collector's existing /v1/opencost/data so the
// unchanged process_spend mapping writes spends + cloud_resource_metrics.
//
// This is the server-side replacement for the legacy in-cluster runner's publish()
// loop. Per-account failures are logged and skipped so one bad cluster doesn't stall
// the rest.
func SyncOpenCostSpends(ctx *security.RequestContext, accountIds []string) error {
	t0 := time.Now()
	logger := ctx.GetLogger()
	defer func() { logger.Info("opencost spend sync completed", "duration", time.Since(t0)) }()

	accounts, err := listActiveK8sAccounts(ctx.GetContext(), accountIds)
	if err != nil {
		return fmt.Errorf("opencost spend sync: list accounts: %w", err)
	}
	logger.Info("opencost spend sync: processing accounts", "count", len(accounts))

	client := &http.Client{Timeout: 120 * time.Second}
	var synced, skipped, gated, failed int
	for _, acc := range accounts {
		// Bail if the cron's overall deadline fired (or it was canceled) rather
		// than grinding through the remaining clusters.
		if err := ctx.GetContext().Err(); err != nil {
			logger.Warn("opencost spend sync: context done, halting",
				"error", err, "synced", synced, "remaining", len(accounts)-synced-skipped-gated-failed)
			return err
		}
		accLogger := logger.With("account_id", acc.AccountId, "tenant_id", acc.TenantId)
		// Enrollment is automatic (agent OpenCost disabled, see the account query).
		// This flag is a default-ON kill-switch: ops can force a tenant back off by
		// setting feature_flag OPENCOST_SERVER_SIDE_SPEND = 'disabled'.
		if !tenant.IsFeatureEnabledByDefault(ctx, acc.TenantId, tenant.FEATURE_OPENCOST_SERVER_SIDE_SPEND) {
			gated++
			// Kill-switched: clear the marker so the UI no longer claims OpenCost is
			// handled server-side (with the agent's own OpenCost also off, this cluster
			// genuinely has no cost source — the UI should surface that).
			if err := setOpenCostServerSide(ctx, acc.AccountId, false); err != nil {
				accLogger.Warn("opencost spend sync: failed to clear server-side marker", "error", err)
			}
			continue
		}
		// Enrollment (passed the gate above) means the server now owns this cluster's
		// OpenCost. Stamp the marker regardless of this run's outcome — an empty window
		// or a transient sync failure doesn't change ownership — so the UI reports
		// "managed server-side" instead of "disconnected".
		if err := setOpenCostServerSide(ctx, acc.AccountId, true); err != nil {
			accLogger.Warn("opencost spend sync: failed to set server-side marker", "error", err)
		}
		didSync, err := syncAccountSpends(ctx, client, acc.AccountId)
		switch {
		case err != nil:
			failed++
			accLogger.Error("opencost spend sync: account failed", "error", err)
		case didSync:
			synced++
		default:
			skipped++
		}
	}
	logger.Info("opencost spend sync: done", "synced", synced, "skipped", skipped, "gated", gated, "failed", failed)
	return nil
}

// serverSideOnConnectInFlight tracks per-account on-connect triggers currently
// running, so overlapping connect events for the same cluster (e.g. a fast
// reconnect flap) don't spawn duplicate syncs. Mirrors the inFlightUpdates
// pattern in services/account/agent_service.go.
var serverSideOnConnectInFlight sync.Map // key: account uuid.UUID, value: struct{}

// serverSideOnConnectTimeout bounds the detached background context so a hung
// relay/opencost call can't keep the in-flight guard set forever. A single
// account runs a handful of HTTP calls (window + node/pod allocation + store),
// each capped by the 120s client timeout in syncAccountSpends.
const serverSideOnConnectTimeout = 5 * time.Minute

// needsServerSideStampQuery reports whether a cloud account is a server-side
// OpenCost candidate that has not yet been stamped. It mirrors the eligibility
// of activeK8sAccountsByIdQuery (K8s, non-proxy row, agent OpenCost off) minus
// the status/last_connected gate — the connect event is itself proof of a live
// agent — and adds `NOT (connection_status ? 'opencostServerSide')` so the
// trigger fires only until the marker key is first written. Once
// SyncOpenCostSpends stamps it (true on enroll, false when kill-switched), the
// key is present and later connects short-circuit here.
//
// COALESCE guards a NULL connection_status: `NULL ? 'x'` is NULL and `NOT NULL`
// is NULL, which would drop the row and never trigger for a cluster whose
// connection_status has not been written yet.
const needsServerSideStampQuery = `
	SELECT EXISTS (
		SELECT 1
		FROM cloud_accounts ca
		INNER JOIN agent a ON ca.id = a.cloud_account_id
		WHERE ca.id = $1
		  AND ca.cloud_provider = 'K8s'
		  AND a.type != 'proxy'
		  AND (a.connection_status->>'opencostConnection') IS DISTINCT FROM 'true'
		  AND NOT (COALESCE(a.connection_status, '{}'::jsonb) ? 'opencostServerSide')
	)`

// TriggerServerSideOnConnect kicks a one-off server-side OpenCost sync for a
// single cluster the moment its agent connects, instead of waiting up to the
// 6-hourly cron interval. This closes the gap that leaves a freshly-onboarded
// or re-onboarded K8s account showing OpenCost "disconnected" (opencostConnection
// off, opencostServerSide not yet stamped) until the next cron tick.
//
// Best-effort and idempotent. The in-flight guard drops overlapping connect
// events for the same cluster; the eligibility probe and sync then run on a
// detached, timeout-bounded goroutine so the caller (the /v1/audit ingest
// handler) returns immediately and a client disconnect can't cancel the probe.
// Once the marker is stamped (or the cluster runs its own OpenCost) the probe
// returns false and the worker exits without syncing.
func TriggerServerSideOnConnect(ctx *security.RequestContext, accountId, tenantId string) {
	if accountId == "" || tenantId == "" {
		return
	}
	// Parse up front: a valid uuid binds to the ca.id (uuid) column directly so
	// the lookup uses the primary-key index, and an invalid id fails fast without
	// touching the DB.
	accountUUID, err := uuid.Parse(accountId)
	if err != nil {
		return
	}
	logger := ctx.GetLogger().With("account_id", accountId, "tenant_id", tenantId)

	if _, loaded := serverSideOnConnectInFlight.LoadOrStore(accountUUID, struct{}{}); loaded {
		return
	}

	// Detach from the ingest request context (cancelled once /v1/audit responds)
	// and bound the run so a hung call can't leak the goroutine or hold the
	// in-flight guard indefinitely.
	detached, cancel := context.WithTimeout(
		context.WithoutCancel(ctx.GetContext()),
		serverSideOnConnectTimeout,
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
		defer serverSideOnConnectInFlight.Delete(accountUUID)
		defer func() {
			if r := recover(); r != nil {
				logger.Error("opencost on-connect trigger: worker panicked", "panic", r)
			}
		}()

		dbms, err := database.GetDatabaseManager(database.Metastore)
		if err != nil {
			logger.Warn("opencost on-connect trigger: db manager", "error", err)
			return
		}
		var needs bool
		if err := dbms.Db.GetContext(bgCtx.GetContext(), &needs, needsServerSideStampQuery, accountUUID); err != nil {
			logger.Warn("opencost on-connect trigger: eligibility probe failed", "error", err)
			return
		}
		if !needs {
			return
		}
		logger.Info("opencost on-connect trigger: syncing server-side spends for newly-connected cluster")
		if err := SyncOpenCostSpends(bgCtx, []string{accountId}); err != nil {
			logger.Error("opencost on-connect trigger: sync failed", "error", err)
		}
	}()
}

type k8sAccount struct {
	AccountId string `db:"cloud_account_id"`
	TenantId  string `db:"tenant"`
}

func listActiveK8sAccounts(ctx context.Context, accountIds []string) ([]k8sAccount, error) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, err
	}
	var accounts []k8sAccount
	if len(accountIds) == 0 {
		if err := dbms.Db.SelectContext(ctx, &accounts, activeK8sAccountsQuery); err != nil {
			return nil, err
		}
		return accounts, nil
	}
	if err := dbms.Db.SelectContext(ctx, &accounts, activeK8sAccountsByIdQuery, pq.Array(accountIds)); err != nil {
		return nil, err
	}
	return accounts, nil
}

// activeK8sAccountsQuery selects connected K8s clusters whose agent is NOT running
// its own in-cluster OpenCost (opencostConnection != true). That is the migration
// signal: disabling OpenCost at the agent turns off both its in-cluster OpenCost and
// its legacy publish() push, so the server-side sync automatically takes over with
// no double-write. Clusters still running agent OpenCost are excluded here.
//
// The `a.type != 'proxy'` guard is required, not cosmetic: a cloud account may carry
// more than one agent row (UNIQUE on tenant, cloud_account_id, type), and the
// authoritative connection_status — the one the agent's telemetry writes and the one
// setOpenCostServerSide stamps — lives on the non-proxy row. A `proxy` row has no
// opencostConnection key, so `->>'opencostConnection'` is NULL → IS DISTINCT FROM
// 'true' is TRUE → without this guard a connected proxy row force-selects the account
// for server-side sync even when the real agent is running in-cluster OpenCost,
// re-introducing the exact double-write this query exists to prevent.
const activeK8sAccountsQuery = `
	SELECT ca.id::varchar AS cloud_account_id, ca.tenant::varchar AS tenant
	FROM cloud_accounts ca
	INNER JOIN agent a ON ca.id = a.cloud_account_id
	WHERE ca.cloud_provider = 'K8s'
	  AND a.type != 'proxy'
	  AND a.status = 'CONNECTED'
	  AND a.last_connected_at > now() - interval '1 DAY'
	  AND (a.connection_status->>'opencostConnection') IS DISTINCT FROM 'true'
	GROUP BY ca.id, ca.tenant`

// activeK8sAccountsByIdQuery is the explicit-account variant (manual cron payload).
// It intentionally keeps the same opencost-not-active guard (including the non-proxy
// row filter — see activeK8sAccountsQuery) so a manual trigger can't double-write a
// cluster that still runs agent OpenCost.
const activeK8sAccountsByIdQuery = `
	SELECT ca.id::varchar AS cloud_account_id, ca.tenant::varchar AS tenant
	FROM cloud_accounts ca
	INNER JOIN agent a ON ca.id = a.cloud_account_id
	WHERE ca.id = ANY($1::uuid[])
	  AND ca.cloud_provider = 'K8s'
	  AND a.type != 'proxy'
	  AND a.status = 'CONNECTED'
	  AND a.last_connected_at > now() - interval '1 DAY'
	  AND (a.connection_status->>'opencostConnection') IS DISTINCT FROM 'true'
	GROUP BY ca.id, ca.tenant`

// setOpenCostServerSide stamps a server-managed `opencostServerSide` marker on the
// agent's connection_status so the UI can render OpenCost as "managed server-side"
// (and keep the cluster healthy) when the agent's own in-cluster OpenCost is off.
//
// This is safe to write here even though the agent owns connection_status: the k8s
// telemetry handler merges (`||`) each heartbeat into connection_status rather than
// replacing it, and the agent never sends this key, so the server-owned marker
// survives heartbeats — the same pattern scan_orchestrator uses for `schedule_jobs`.
//
// The IS DISTINCT FROM guard makes a steady-state run a no-op (no dead tuples) once
// the marker matches the desired value.
func setOpenCostServerSide(ctx *security.RequestContext, accountId string, enabled bool) error {
	if accountId == "" {
		return fmt.Errorf("setOpenCostServerSide: empty account id")
	}
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return err
	}
	const q = `
		UPDATE agent
		SET connection_status = COALESCE(connection_status, '{}'::jsonb)
		                        || jsonb_build_object('opencostServerSide', $2::boolean)
		WHERE cloud_account_id = $1 AND type != 'proxy'
		  AND (connection_status->>'opencostServerSide') IS DISTINCT FROM $2::text`
	_, err = dbms.Db.ExecContext(ctx.GetContext(), q, accountId, enabled)
	return err
}

// syncAccountSpends runs one account through the attributes → allocation → store
// pipeline. Returns (true, nil) when allocations were posted, (false, nil) when
// there was nothing to sync (empty window), and an error on failure.
func syncAccountSpends(ctx *security.RequestContext, client *http.Client, accountId string) (bool, error) {
	if accountId == "" {
		return false, fmt.Errorf("empty account id")
	}
	window, step, err := fetchOpenCostWindow(ctx, client, accountId)
	if err != nil {
		return false, fmt.Errorf("fetch window: %w", err)
	}
	startDate, endDate, ok := parseWindow(window)
	if !ok {
		// "0,0" (or empty) is the collector's "caught up" signal — end window in
		// the future, nothing to ingest this run. Any other unparseable window is a
		// real error worth surfacing rather than silently skipping the account.
		if w := strings.TrimSpace(window); w == "" || w == "0,0" {
			return false, nil
		}
		return false, fmt.Errorf("invalid window %q from collector", window)
	}

	nodeData, err := fetchAllocation(ctx, client, accountId, window, step, "node")
	if err != nil {
		return false, fmt.Errorf("fetch node allocation: %w", err)
	}
	serviceData, err := fetchAllocation(ctx, client, accountId, window, step, "namespace,pod")
	if err != nil {
		return false, fmt.Errorf("fetch pod allocation: %w", err)
	}
	if isEmptyAllocation(nodeData) && isEmptyAllocation(serviceData) {
		// OpenCost reads each cluster's metrics through relay-server's /prometheus
		// facade. A cluster whose agent has no prometheus_url configured — one that
		// ships to Elasticsearch instead — has nothing there to read, so OpenCost
		// returns empty for every window and the cluster shows no Kubernetes cost at
		// all. Fall back to computing allocation from the inventory we already hold.
		//
		// Keyed off an empty result rather than a per-account setting on purpose: an
		// account whose Prometheus works keeps the OpenCost numbers and never reaches
		// this branch, so the fallback cannot regress anything that works today. A
		// genuinely idle window computes to ~nothing here too, which is correct.
		invNode, invService, invErr := buildInventoryAllocation(ctx, accountId, startDate, endDate, step)
		if invErr != nil {
			ctx.GetLogger().Error("opencost spend sync: inventory allocation failed",
				"account_id", accountId, "window", window, "error", invErr)
			return false, nil
		}
		if isEmptyAllocation(invNode) && isEmptyAllocation(invService) {
			ctx.GetLogger().Info("opencost spend sync: no allocations for window",
				"account_id", accountId, "window", window)
			return false, nil
		}
		// Logged at WARN, not INFO: these numbers are requests-based and carry no
		// storage, network or GPU cost, so a cluster silently switching to this path
		// is something an operator should see rather than discover from a dip.
		ctx.GetLogger().Warn("opencost spend sync: OpenCost returned nothing, using inventory-based allocation",
			"account_id", accountId, "window", window,
			"note", "requests-based; excludes storage, network egress, GPU and load balancer cost")
		nodeData, serviceData = invNode, invService
	}

	if err := postOpenCostData(ctx, client, accountId, startDate, endDate, nodeData, serviceData); err != nil {
		return false, fmt.Errorf("store allocation: %w", err)
	}
	ctx.GetLogger().Info("opencost spend sync: stored allocations",
		"account_id", accountId, "window", window, "start_date", startDate, "end_date", endDate)
	return true, nil
}

// fetchOpenCostWindow asks the collector for the next ingestion window+step for an
// account, reusing the collector's last-synced bookkeeping (so the server-side cron
// and the legacy agent path share one window source during dual-run).
func fetchOpenCostWindow(ctx *security.RequestContext, client *http.Client, accountId string) (string, string, error) {
	endpoint := config.Config.K8sCollectorServerUrl + "/v1/opencost/attributes"
	req, err := http.NewRequestWithContext(ctx.GetContext(), http.MethodGet, endpoint, nil)
	if err != nil {
		return "", "", err
	}
	setCollectorAuth(req, accountId)
	resp, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("collector attributes returned %d: %s", resp.StatusCode, string(body))
	}
	var parsed struct {
		Data struct {
			Window string `json:"window"`
			Step   string `json:"step"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", "", fmt.Errorf("decode attributes: %w", err)
	}
	return parsed.Data.Window, parsed.Data.Step, nil
}

// fetchAllocation pulls one aggregation level from central OpenCost and returns the
// raw `data` array verbatim (passed straight through to the collector, which owns
// the allocation→rows mapping — no need to parse the multi-MB payload here).
func fetchAllocation(ctx *security.RequestContext, client *http.Client, accountId, window, step, aggregate string) (json.RawMessage, error) {
	q := url.Values{}
	q.Set("window", window)
	if step != "" {
		q.Set("step", step)
	}
	q.Set("aggregate", aggregate)
	endpoint := config.Config.CostServerUrl + "/allocation/compute?" + q.Encode()

	req, err := http.NewRequestWithContext(ctx.GetContext(), http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	// Central OpenCost (CLOUD_COST_PROVIDER=nudgebee) is multi-tenant: the cluster
	// is selected by X-Scope-OrgID = cloud account id.
	req.Header.Set("X-Scope-OrgID", accountId)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("opencost %s returned %d: %s", aggregate, resp.StatusCode, truncate(body, 512))
	}
	var parsed struct {
		Code int             `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("decode opencost %s: %w", aggregate, err)
	}
	if parsed.Code != http.StatusOK {
		return nil, fmt.Errorf("opencost %s code %d", aggregate, parsed.Code)
	}
	// Never hand `null`/absent data downstream: the collector's process_spend does
	// len(spend["node"]) and would crash on a JSON null. Normalize to an empty array.
	if isEmptyAllocation(parsed.Data) {
		return json.RawMessage("[]"), nil
	}
	return parsed.Data, nil
}

// isEmptyAllocation reports whether an OpenCost `data` payload carries no entries
// (null, absent, [] or {}), so callers can skip the store instead of posting an
// empty/`null` body that process_spend can't handle. Operates on the byte slice to
// avoid copying the (multi-MB) payload to a string on every non-empty call.
func isEmptyAllocation(raw json.RawMessage) bool {
	t := bytes.TrimSpace(raw)
	return len(t) == 0 ||
		bytes.Equal(t, []byte("null")) ||
		bytes.Equal(t, []byte("[]")) ||
		bytes.Equal(t, []byte("{}"))
}

// postOpenCostData ships node + pod allocations to the collector's existing
// ingestion endpoint in the shape process_spend expects: {code, data:{node,service}}.
// Body is gzipped to match the legacy producer (pod payloads run multi-MB).
func postOpenCostData(ctx *security.RequestContext, client *http.Client, accountId string, startDate, endDate int64, nodeData, serviceData json.RawMessage) error {
	payload := struct {
		Code int `json:"code"`
		Data struct {
			Node    json.RawMessage `json:"node"`
			Service json.RawMessage `json:"service"`
		} `json:"data"`
	}{Code: http.StatusOK}
	payload.Data.Node = nodeData
	payload.Data.Service = serviceData

	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(raw); err != nil {
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}

	q := url.Values{}
	q.Set("start_date", fmt.Sprintf("%d", startDate))
	q.Set("end_date", fmt.Sprintf("%d", endDate))
	endpoint := config.Config.K8sCollectorServerUrl + "/v1/opencost/data?" + q.Encode()

	req, err := http.NewRequestWithContext(ctx.GetContext(), http.MethodPost, endpoint, &buf)
	if err != nil {
		return err
	}
	setCollectorAuth(req, accountId)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "gzip")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("collector data returned %d: %s", resp.StatusCode, truncate(body, 512))
	}
	return nil
}

// setCollectorAuth stamps the shared internal token + account id so the collector's
// AuthTokenMiddleware authenticates the call without per-agent Basic credentials.
func setCollectorAuth(req *http.Request, accountId string) {
	req.Header.Set(config.Config.ServiceApiServerTokenHeader, config.Config.ServiceApiServerToken)
	req.Header.Set("X-NB-Account-Id", accountId)
}

// parseWindow splits the collector's "<start>,<end>" epoch-seconds window. The
// collector returns "0,0" when there is nothing to ingest.
func parseWindow(window string) (start, end int64, ok bool) {
	var s, e int64
	if _, err := fmt.Sscanf(window, "%d,%d", &s, &e); err != nil {
		return 0, 0, false
	}
	if s == 0 || e == 0 || e <= s {
		return 0, 0, false
	}
	return s, e, true
}

func truncate(b []byte, n int) string {
	runes := []rune(string(b))
	if len(runes) <= n {
		return string(runes)
	}
	return string(runes[:n]) + "…"
}
