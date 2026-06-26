// Package opencostengine computes per-cluster OpenCost allocations in-process
// inside services-server, over UNMODIFIED upstream github.com/opencost/opencost.
//
// It is a thin adapter, not a fork: the nudgebee-specific pieces (cluster info,
// cluster cache over the relay, DB-backed pricing provider) implement upstream's
// public interfaces, and the one behavioural change OpenCost needs to run without
// an in-cluster exporter — querying standard kube-state-metrics instead of the
// exporter's own metrics — is injected via a custom MetricsQuerier wrapped in a
// DataSource (see querier.go / datasource.go). Per cluster we build an upstream
// costmodel.Accesses and call its public ComputeAllocationHandler in-process, so
// the JSON we return is byte-identical to the old standalone opencost server.
package opencostengine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sort"
	"sync"

	"github.com/julienschmidt/httprouter"
	promenv "github.com/opencost/opencost/modules/prometheus-source/pkg/env"
	"github.com/opencost/opencost/modules/prometheus-source/pkg/prom"
	occonfig "github.com/opencost/opencost/pkg/config"
	"github.com/opencost/opencost/pkg/costmodel"
	gocache "github.com/patrickmn/go-cache"
)

var (
	initOnce sync.Once
	initErr  error
	sharedDB *DB

	accessMu sync.RWMutex
	accesses = map[string]*costmodel.Accesses{}
)

// Init configures the engine once. The ported nudgebee components read config
// straight from process env: NUDGEBEE_DB (the metastore holding cloud_accounts /
// cloud_resource_details the provider reads), RELAY_SERVER_ENDPOINT and
// RELAY_SERVER_SECRET_KEY (the relay this server reaches each cluster's Prometheus
// + K8s API through) — all supplied by the cost-server pod's shared `nudgebee`
// secret. The two nudgebee-mode constants below are defaulted here but left
// overridable by env.
func Init() error {
	initOnce.Do(func() {
		for _, kv := range [][2]string{
			{"CLOUD_COST_PROVIDER", "nudgebee"},
			{"PROM_CLUSTER_ID_LABEL", "cluster_id"},
		} {
			// Default only when the env doesn't already set it (ops override wins).
			if os.Getenv(kv[0]) != "" {
				continue
			}
			if err := os.Setenv(kv[0], kv[1]); err != nil {
				initErr = fmt.Errorf("opencostengine: setenv %s: %w", kv[0], err)
				return
			}
		}
		db, err := NewDB(GetNudgebeeDbConnectionString())
		if err != nil {
			initErr = fmt.Errorf("opencostengine: db: %w", err)
			return
		}
		sharedDB = db
	})
	return initErr
}

// AllocationHandler serves GET /allocation/compute for a single cluster, selected
// by the X-Scope-OrgID header (= cloud_accounts.id). It delegates to the upstream
// ComputeAllocationHandler so the response body is the native {code,data} envelope
// — byte-identical to the old standalone opencost server. window/step/aggregate
// come from the query string and are read by the upstream handler.
func AllocationHandler(w http.ResponseWriter, r *http.Request) {
	if err := Init(); err != nil {
		http.Error(w, fmt.Sprintf("init: %v", err), http.StatusInternalServerError)
		return
	}
	orgID := r.Header.Get("X-Scope-OrgID")
	if orgID == "" {
		http.Error(w, "missing X-Scope-OrgID header", http.StatusBadRequest)
		return
	}
	access, err := getOrCreateAccess(orgID)
	if err != nil {
		http.Error(w, fmt.Sprintf("cluster %s: %v", orgID, err), http.StatusInternalServerError)
		return
	}
	access.ComputeAllocationHandler(w, r, httprouter.Params{})
}

// getOrCreateAccess builds (and caches) the per-cluster upstream cost engine.
// Mirrors the fork's multitenant getOrCreateAccess, except the data source is our
// kube-state-metrics-backed wrapper. clusterID == cloud_accounts.id.
func getOrCreateAccess(clusterID string) (*costmodel.Accesses, error) {
	if clusterID == "" {
		return nil, fmt.Errorf("clusterID cannot be empty")
	}
	accessMu.RLock()
	if a, ok := accesses[clusterID]; ok {
		accessMu.RUnlock()
		return a, nil
	}
	accessMu.RUnlock()

	accessMu.Lock()
	defer accessMu.Unlock()
	if a, ok := accesses[clusterID]; ok {
		return a, nil
	}

	clusterInfoProvider := NewNudgebeeClusterInfoProvider(sharedDB, clusterID)
	clusterCache, err := NewNudgebeeClusterCache(sharedDB, clusterID, GetNudgebeeRelayEndpoint(), GetNudgebeeRelayAuthToken())
	if err != nil {
		return nil, fmt.Errorf("cluster cache: %w", err)
	}
	confManager := occonfig.NewConfigFileManager(nil)

	promEndpoint := GetNudgebeeRelayEndpoint() + "/prometheus"
	_ = os.Setenv(promenv.PrometheusServerEndpointEnvVar, promEndpoint)
	prometheusConfig, err := prom.NewOpenCostPrometheusConfigFromEnv()
	if err != nil {
		return nil, fmt.Errorf("prometheus config: %w", err)
	}
	prometheusConfig.ServerEndpoint = promEndpoint
	prometheusConfig.ClientConfig.HeaderXScopeOrgId = clusterID
	prometheusConfig.ClientConfig.Auth.BearerToken = GetNudgebeeRelayAuthToken()
	// Reset cluster filter; set from the agent's reported prometheus labels if any.
	prometheusConfig.ClusterID = ""
	prometheusConfig.ClusterFilter = ""
	applyClusterLabel(clusterID, prometheusConfig)

	dataSource, err := prom.NewPrometheusDataSource(clusterInfoProvider, prometheusConfig)
	if err != nil {
		return nil, fmt.Errorf("prometheus data source: %w", err)
	}
	nbDS := newNudgebeeDataSource(dataSource)

	cloudProvider, err := NewNudgebeeProvider(sharedDB, clusterID)
	if err != nil {
		return nil, fmt.Errorf("provider: %w", err)
	}
	if err := cloudProvider.DownloadPricingData(); err != nil {
		// Non-fatal: matches the fork (pricing falls back to defaults).
		_ = err
	}

	clusterMap := dataSource.ClusterMap()
	costModel := costmodel.NewCostModel(nbDS, cloudProvider, clusterCache, clusterMap, dataSource.BatchDuration())
	metricsEmitter := costmodel.NewCostModelMetricsEmitter(clusterCache, cloudProvider, clusterInfoProvider, costModel)
	settingsCache := gocache.New(gocache.NoExpiration, gocache.NoExpiration)

	a := &costmodel.Accesses{
		DataSource:          nbDS,
		ClusterCache:        clusterCache,
		ClusterMap:          clusterMap,
		CloudProvider:       cloudProvider,
		ConfigFileManager:   confManager,
		ClusterInfoProvider: clusterInfoProvider,
		Model:               costModel,
		MetricsEmitter:      metricsEmitter,
		SettingsCache:       settingsCache,
	}
	a.InitializeSettingsPubSub()

	accesses[clusterID] = a
	return a, nil
}

// applyClusterLabel reads the agent's reported prometheusAdditionalLabels from
// connection_status and, if present, sets the cluster label + filter so the
// allocation queries are scoped to the right cluster's series.
func applyClusterLabel(clusterID string, cfg *prom.OpenCostPrometheusConfig) {
	if clusterID == "" {
		return
	}
	rows, err := sharedDB.Connection().Query("select connection_status::text from agent a where a.cloud_account_id = $1", clusterID)
	if err != nil {
		return
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var status string
		if err := rows.Scan(&status); err != nil {
			continue
		}
		m := map[string]any{}
		if err := json.Unmarshal([]byte(status), &m); err != nil {
			continue
		}
		labels, ok := m["prometheusAdditionalLabels"].(map[string]any)
		if !ok {
			continue
		}
		// Map iteration is randomized; sort keys so a cluster reporting multiple
		// additional labels yields a deterministic ClusterLabel/ClusterID pick.
		keys := make([]string, 0, len(labels))
		for k := range labels {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if s, ok := labels[k].(string); ok {
				cfg.ClusterLabel = k
				cfg.ClusterID = s
				break
			}
		}
	}
	if cfg.ClusterID == "" {
		cfg.ClusterFilter = ""
	} else {
		cfg.ClusterFilter = fmt.Sprintf("%s=\"%s\"", cfg.ClusterLabel, cfg.ClusterID)
	}
}

// ComputeAllocation runs one in-process allocation query for a cluster and returns
// the raw `data` payload of the `{code, data}` response — identical bytes to the
// old HTTP call to the standalone opencost server. `aggregate` is "node" or
// "namespace,pod"; `accountId` (= cloud account id) selects the cluster.
func ComputeAllocation(ctx context.Context, accountId, window, step, aggregate string) (json.RawMessage, error) {
	if err := Init(); err != nil {
		return nil, err
	}
	access, err := getOrCreateAccess(accountId)
	if err != nil {
		return nil, err
	}

	q := url.Values{}
	q.Set("window", window)
	if step != "" {
		q.Set("step", step)
	}
	q.Set("aggregate", aggregate)
	req := httptest.NewRequest(http.MethodGet, "/allocation/compute?"+q.Encode(), nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	access.ComputeAllocationHandler(rec, req, httprouter.Params{})

	if rec.Code != http.StatusOK {
		return nil, fmt.Errorf("opencost %s returned %d: %s", aggregate, rec.Code, truncate(rec.Body.Bytes(), 512))
	}
	var parsed struct {
		Code int             `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &parsed); err != nil {
		return nil, fmt.Errorf("decode opencost %s: %w", aggregate, err)
	}
	if parsed.Code != http.StatusOK {
		return nil, fmt.Errorf("opencost %s code %d", aggregate, parsed.Code)
	}
	return parsed.Data, nil
}

func truncate(b []byte, n int) string {
	runes := []rune(string(b))
	if len(runes) <= n {
		return string(runes)
	}
	return string(runes[:n]) + "…"
}
