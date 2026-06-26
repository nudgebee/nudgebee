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
	"time"

	"nudgebee/services/config"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
	"nudgebee/services/tenant"

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
			continue
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
const activeK8sAccountsQuery = `
	SELECT ca.id::varchar AS cloud_account_id, ca.tenant::varchar AS tenant
	FROM cloud_accounts ca
	INNER JOIN agent a ON ca.id = a.cloud_account_id
	WHERE ca.cloud_provider = 'K8s'
	  AND a.status = 'CONNECTED'
	  AND a.last_connected_at > now() - interval '1 DAY'
	  AND (a.connection_status->>'opencostConnection') IS DISTINCT FROM 'true'
	GROUP BY ca.id, ca.tenant`

// activeK8sAccountsByIdQuery is the explicit-account variant (manual cron payload).
// It intentionally keeps the same opencost-not-active guard so a manual trigger
// can't double-write a cluster that still runs agent OpenCost.
const activeK8sAccountsByIdQuery = `
	SELECT ca.id::varchar AS cloud_account_id, ca.tenant::varchar AS tenant
	FROM cloud_accounts ca
	INNER JOIN agent a ON ca.id = a.cloud_account_id
	WHERE ca.id = ANY($1::uuid[])
	  AND ca.cloud_provider = 'K8s'
	  AND a.status = 'CONNECTED'
	  AND a.last_connected_at > now() - interval '1 DAY'
	  AND (a.connection_status->>'opencostConnection') IS DISTINCT FROM 'true'
	GROUP BY ca.id, ca.tenant`

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
		ctx.GetLogger().Info("opencost spend sync: no allocations for window",
			"account_id", accountId, "window", window)
		return false, nil
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
