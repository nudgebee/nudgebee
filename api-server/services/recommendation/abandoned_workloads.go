package recommendation

import (
	"context"
	"fmt"
	"math"
	"nudgebee/services/internal/database"
	"nudgebee/services/observability"
	"nudgebee/services/security"
	"strings"
	"sync"
	"time"
)

// maxAbandonedFetchConcurrency bounds concurrent live metric fetches so a many-workload account
// completes within the background-task timeout without overwhelming the relay/agent.
const maxAbandonedFetchConcurrency = 10

// abandonedWorkload holds the DB-sourced facts about an eligible workload. Network traffic is
// NOT read from the DB — it is fetched live from the account's metrics provider (see below).
type abandonedWorkload struct {
	TenantId        string  `db:"tenant_id"`
	CloudResourceId string  `db:"cloud_resource_id"`
	CloudAccountId  string  `db:"cloud_account_id"`
	Name            string  `db:"name"`
	Namespace       string  `db:"namespace"`
	Kind            string  `db:"kind"`
	Amount          float64 `db:"amount"`
}

// findAbandonedWorkloads detects workloads whose average network traffic over the observation
// window is below networkThreshold.
//
// Unlike the legacy implementation, it does NOT read network metrics from the cloud_resource_metrics
// table (which is populated by a separate ETL that can silently go stale). Instead it fetches the
// per-workload network rate LIVE from whatever metrics provider the account is configured with —
// Prometheus, Datadog, Chronosphere, NewRelic, Dynatrace, etc. — via the provider-agnostic
// observability.FetchMetricUtilisation entry point. Only spend amount (for savings) is read from the DB.
//
// Returned rows match the shape the recommendation builder in processAbandonedRecommendations
// expects: tenant_id, cloud_resource_id, cloud_account_id, name, namespace, kind, avg_rate,
// date_diff, amount.
func findAbandonedWorkloads(ctx *security.RequestContext, dbms *database.DatabaseManager, accountId string, observationDays, networkThreshold int) ([]map[string]any, error) {
	if accountId == "" {
		return nil, fmt.Errorf("findAbandonedWorkloads: accountId is required")
	}
	tenantId := ctx.GetSecurityContext().GetTenantId()
	if tenantId == "" {
		return nil, fmt.Errorf("findAbandonedWorkloads: tenantId is required")
	}

	// Eligible workloads + their average spend amount, scoped by both tenant_id and the
	// globally-unique cloud_account_id for strict tenant isolation. INNER JOIN to spends preserves
	// the legacy behaviour of only considering workloads that have spend data (savings would be 0).
	var workloads []abandonedWorkload
	err := dbms.Db.Select(&workloads, `
		SELECT ksw.tenant_id::varchar        AS tenant_id,
		       ksw.cloud_resource_id::varchar AS cloud_resource_id,
		       ksw.cloud_account_id::varchar  AS cloud_account_id,
		       ksw.name, ksw.namespace, ksw.kind,
		       COALESCE(avg(s.amount), 0)     AS amount
		FROM k8s_workloads ksw
		INNER JOIN spends s ON s.cloud_account = ksw.cloud_account_id
		  AND s.tags->>'controllerKind' = ksw.kind
		  AND s.tags->>'controller'     = ksw.name
		  AND s.tags->>'namespace'      = ksw.namespace
		WHERE ksw.is_active IS NOT FALSE
		  AND ksw.cloud_account_id = $1
		  AND ksw.tenant_id = $2
		  AND ksw.kind NOT IN ('Job', 'Pod', 'DaemonSet')
		  AND ksw.namespace NOT IN ('kube-system', 'nudgebee-agent')
		GROUP BY ksw.tenant_id, ksw.cloud_resource_id, ksw.cloud_account_id, ksw.name, ksw.namespace, ksw.kind
	`, accountId, tenantId)
	if err != nil {
		return nil, fmt.Errorf("failed to list eligible workloads: %w", err)
	}
	if len(workloads) == 0 {
		ctx.GetLogger().Info("abandoned scan: no eligible workloads", "account_id", accountId)
		return nil, nil
	}

	now := time.Now()
	fromMs := now.AddDate(0, 0, -observationDays).UnixMilli()
	toMs := now.UnixMilli()

	// Fetch each workload's network rate live, with bounded concurrency: a many-workload account
	// would exceed the background-task timeout if fetched sequentially. Each fetch is independently
	// timeout-bounded, and scheduling stops early if the parent context is cancelled.
	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		abandoned = make([]map[string]any, 0)
		sem       = make(chan struct{}, maxAbandonedFetchConcurrency)
	)

	for _, wl := range workloads {
		if ctx.GetContext().Err() != nil {
			break
		}
		sem <- struct{}{}
		wg.Add(1)
		go func(wl abandonedWorkload) {
			defer wg.Done()
			defer func() { <-sem }()

			// One provider-agnostic call. FetchMetricUtilisation resolves the account's metrics
			// provider and builds the right query for the canonical network metric names; the pod->
			// workload label mapping is handled internally per provider. Builders lower-case the kind.
			// Bound each fetch so one slow/hung provider call can't stall the scan.
			fetchCtx, cancel := context.WithTimeout(ctx.GetContext(), 30*time.Second)
			defer cancel()
			reqCtx := security.NewRequestContext(fetchCtx, ctx.GetSecurityContext(), ctx.GetLogger(), ctx.GetTracer(), ctx.GetMeter())
			out, fetchErr := observability.FetchMetricUtilisation(reqCtx, observability.GetUtilisationTrendRequest{
				AccountId: accountId,
				StartTime: fromMs,
				EndTime:   toMs,
				Request: map[string]any{
					"workload_namespace": wl.Namespace,
					"workload_name":      wl.Name,
					"kind":               strings.ToLower(wl.Kind),
					"metrics":            []interface{}{"network_receive_packet", "network_transmit_packets"},
				},
			})
			if fetchErr != nil {
				ctx.GetLogger().Warn("abandoned scan: network metrics fetch failed",
					"workload", wl.Name, "namespace", wl.Namespace, "account_id", accountId, "error", fetchErr)
				return
			}

			avgRate, daySpan, ok := computeWorkloadNetworkRate(out)
			if !ok {
				return
			}
			// date_diff > 5 (legacy gate): require enough observed history so brand-new workloads are
			// not flagged on a thin window. avg_rate < threshold: the actual "abandoned" condition.
			if daySpan <= 5 || avgRate >= float64(networkThreshold) {
				return
			}

			mu.Lock()
			abandoned = append(abandoned, map[string]any{
				"tenant_id":         wl.TenantId,
				"cloud_resource_id": wl.CloudResourceId,
				"cloud_account_id":  wl.CloudAccountId,
				"name":              wl.Name,
				"namespace":         wl.Namespace,
				"kind":              wl.Kind,
				"avg_rate":          avgRate,
				"date_diff":         float64(daySpan),
				"amount":            wl.Amount,
			})
			mu.Unlock()
		}(wl)
	}
	wg.Wait()

	return abandoned, nil
}

// computeWorkloadNetworkRate aggregates the receive + transmit byte-rate series returned by
// FetchMetricUtilisation into a single average rate (bytes/sec) and the number of days the data
// spans. The transmit canonical query is returned negated by the metric builders (charting
// convention), so magnitudes are combined via abs(). Returns ok=false when there are no usable
// data points.
func computeWorkloadNetworkRate(out observability.OutputMetricQuery) (avgRate float64, daySpan int, ok bool) {
	var sum float64
	var count int
	var minTs, maxTs int64
	haveTs := false

	for _, qr := range out.Results {
		for _, res := range qr.Payload {
			for i, v := range res.Values {
				sum += math.Abs(v)
				count++
				if i < len(res.Timestamps) {
					ts := normalizeToSeconds(res.Timestamps[i])
					if !haveTs || ts < minTs {
						minTs = ts
					}
					if !haveTs || ts > maxTs {
						maxTs = ts
					}
					haveTs = true
				}
			}
		}
	}
	if count == 0 || !haveTs {
		return 0, 0, false
	}
	const secondsPerDay = 86400
	return sum / float64(count), int((maxTs - minTs) / secondsPerDay), true
}

// normalizeToSeconds returns an epoch timestamp in seconds, accepting either seconds or
// milliseconds. Providers are inconsistent: the Prometheus path returns seconds while the Datadog
// path returns milliseconds. Any real epoch-seconds value is well below 1e11 (year ~5138), whereas
// epoch-milliseconds today is ~1.7e12, so the threshold cleanly distinguishes the two.
func normalizeToSeconds(ts int64) int64 {
	if ts > 1e11 {
		return ts / 1000
	}
	return ts
}
