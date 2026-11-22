package opencostengine

import (
	"context"
	"database/sql"
	"sync"
	"time"

	"github.com/opencost/opencost/core/pkg/log"
)

// metricsRouter decides, per cluster, whether the shim answers OpenCost's queries
// from services-server's metrics_query or proxies them to the agent's Prometheus.
//
// Neither path works for every cluster. A cluster whose metrics come from a
// directly connected integration (Azure Managed Prometheus, a hosted Prometheus)
// has no Prometheus behind the agent, so the passthrough answers "Prometheus not
// configured" and the cluster gets no cost at all. A cluster served by the agent
// loses OpenCost's pod-lifetime subqueries on the metrics_query path — measured on
// a live cluster, 0 series against 7,413 through the passthrough — and its cost
// drops to zero.
// So the passthrough stays the default, and only clusters the agent cannot serve
// move to metrics_query.
type metricsRouter struct {
	// lookup reports whether the account should use metrics_query.
	lookup func(ctx context.Context, accountID string) (bool, error)
	ttl    time.Duration
	now    func() time.Time

	mu    sync.Mutex
	cache map[string]routeDecision
}

type routeDecision struct {
	useObservability bool
	expires          time.Time
}

// routeCacheTTL bounds how stale a decision can be. OpenCost issues dozens of
// queries per allocation request, so the lookup must not run per query; a few
// minutes is well inside the 6-hourly sync cadence and short enough that an
// integration change is picked up by the next run.
const routeCacheTTL = 5 * time.Minute

func newMetricsRouter(lookup func(ctx context.Context, accountID string) (bool, error)) *metricsRouter {
	return &metricsRouter{
		lookup: lookup,
		ttl:    routeCacheTTL,
		now:    time.Now,
		cache:  map[string]routeDecision{},
	}
}

// useObservability returns the cached decision for the account, looking it up
// when absent or expired. A failed lookup keeps the passthrough — the path every
// cluster used before this router existed — and is not cached, so the next query
// tries again.
func (r *metricsRouter) useObservability(ctx context.Context, accountID string) bool {
	r.mu.Lock()
	d, ok := r.cache[accountID]
	r.mu.Unlock()
	if ok && r.now().Before(d.expires) {
		return d.useObservability
	}

	use, err := r.lookup(ctx, accountID)
	if err != nil {
		log.Warnf("PrometheusShim: metrics route lookup failed for cluster %s, using the agent passthrough: %s", accountID, err)
		return false
	}
	r.mu.Lock()
	r.cache[accountID] = routeDecision{useObservability: use, expires: r.now().Add(r.ttl)}
	r.mu.Unlock()
	return use
}

// metricsRouteQuery reads the provider check services-server stamps on the agent
// row (observability.ProviderStatusKey). The stamp exists only while some signal
// is served by a non-agent integration, and its metrics entry names who serves
// metrics, from the same resolver metrics_query routes by. An account with no
// stamp, or a stamp naming the agent, is agent-served. An account with no agent
// row at all has nothing behind the passthrough.
//
// Only a Prometheus integration qualifies. The shim pins metric_provider to
// prometheus because these are OpenCost's own PromQL strings, so a cluster whose
// metrics integration is Datadog, New Relic, Elasticsearch, … gains nothing from
// metrics_query — and most of those still run a Prometheus the agent can reach.
const metricsRouteQuery = `
	SELECT count(*),
	       COALESCE(bool_or(
	           COALESCE(a.connection_status->'providerStatus'->'metrics'->>'source', 'agent') <> 'agent'
	           AND a.connection_status->'providerStatus'->'metrics'->>'provider' = 'prometheus'
	       ), false)
	FROM agent a
	WHERE a.cloud_account_id = $1::uuid AND a.type IS DISTINCT FROM 'proxy'`

// lookupMetricsRoute is the production lookup, against the shared metastore pool.
func lookupMetricsRoute(ctx context.Context, accountID string) (bool, error) {
	if sharedDB == nil {
		return false, sql.ErrConnDone
	}
	var agents int
	var integrationServed bool
	if err := sharedDB.Connection().QueryRowContext(ctx, metricsRouteQuery, accountID).Scan(&agents, &integrationServed); err != nil {
		return false, err
	}
	return agents == 0 || integrationServed, nil
}
