package recommendation

import (
	"fmt"
	"nudgebee/services/internal/database"
	"nudgebee/services/observability"
	"nudgebee/services/security"
	"time"
)

const (
	datadogNetworkTxMetric = "kubernetes.network.tx_bytes"
	datadogNetworkRxMetric = "kubernetes.network.rx_bytes"
)

// syncDatadogNetworkMetrics fetches network TX/RX metrics from Datadog and inserts
// them into cloud_resource_metrics so that processAbandonedRecommendations can
// use them without any query changes.
func syncDatadogNetworkMetrics(ctx *security.RequestContext, dbms *database.DatabaseManager, accountId string, observationDays int) error {
	now := time.Now()
	from := now.AddDate(0, 0, -observationDays)
	fromMs := from.UnixMilli()
	toMs := now.UnixMilli()

	// Fetch tenant_id for this account
	var tenantId string
	err := dbms.Db.Get(&tenantId, `SELECT tenant::varchar FROM cloud_accounts WHERE id::varchar = $1`, accountId)
	if err != nil {
		return fmt.Errorf("failed to get tenant for account %s: %w", accountId, err)
	}

	// Fetch workloads we care about (same filters as the abandoned resource query)
	type workloadInfo struct {
		CloudResourceId string `db:"cloud_resource_id"`
		Name            string `db:"name"`
		Namespace       string `db:"namespace"`
		Kind            string `db:"kind"`
	}
	var workloads []workloadInfo
	err = dbms.Db.Select(&workloads, `
		SELECT cloud_resource_id::varchar, name, namespace, kind
		FROM k8s_workloads
		WHERE cloud_account_id = $1
		  AND is_active IS NOT FALSE
		  AND kind NOT IN ('Job', 'Pod', 'DaemonSet')
		  AND namespace NOT IN ('kube-system', 'nudgebee-agent')
	`, accountId)
	if err != nil {
		return fmt.Errorf("failed to list workloads: %w", err)
	}
	if len(workloads) == 0 {
		ctx.GetLogger().Info("datadog network sync: no eligible workloads", "account_id", accountId)
		return nil
	}

	// Build Datadog tag key mapping
	kindToTag := map[string]string{
		"Deployment":  "kube_deployment",
		"StatefulSet": "kube_stateful_set",
		"ReplicaSet":  "kube_replica_set",
		"CronJob":     "kube_cron_job",
	}

	ddSource := observability.DatadogMetricSource{}
	metrics := make([]map[string]any, 0)

	for _, wl := range workloads {
		tagKey, ok := kindToTag[wl.Kind]
		if !ok {
			continue
		}

		// Build both TX and RX queries in a single request
		queries := map[string]string{
			"networkTransferBytes": fmt.Sprintf("avg:%s{kube_namespace:%s,%s:%s}", datadogNetworkTxMetric, wl.Namespace, tagKey, wl.Name),
			"networkReceiveBytes":  fmt.Sprintf("avg:%s{kube_namespace:%s,%s:%s}", datadogNetworkRxMetric, wl.Namespace, tagKey, wl.Name),
		}

		output, err := ddSource.FetchMetricsQuery(ctx, observability.FetchMetricsRequest{
			AccountId: accountId,
			Queries:   queries,
			StartTime: fromMs,
			EndTime:   toMs,
		})
		if err != nil {
			ctx.GetLogger().Warn("datadog network sync: query failed",
				"workload", wl.Name, "namespace", wl.Namespace, "error", err)
			continue
		}

		for _, qr := range output.Results {
			for _, result := range qr.Payload {
				avg, lastTs := computeAvgAndLastTs(result.Values, result.Timestamps)
				if avg <= 0 {
					continue
				}

				tags := fmt.Sprintf(`{"namespace":"%s","controller":"%s","controllerKind":"%s"}`, wl.Namespace, wl.Name, wl.Kind)
				metrics = append(metrics, map[string]any{
					"cloud_resource_id": wl.CloudResourceId,
					"timestamp":         time.Unix(lastTs, 0).UTC(),
					"metric":            qr.QueryKey,
					"metric_type":       "g",
					"tags":              tags,
					"value":             avg,
					"cloud_account_id":  accountId,
					"tenant_id":         tenantId,
				})
			}
		}
	}

	if len(metrics) == 0 {
		ctx.GetLogger().Info("datadog network sync: no metrics to insert", "account_id", accountId)
		return nil
	}

	ctx.GetLogger().Info("datadog network sync: inserting metrics", "account_id", accountId, "count", len(metrics))

	_, err = dbms.Db.NamedExec(`
		INSERT INTO cloud_resource_metrics
			(cloud_resource_id, timestamp, metric, metric_type, tags, value, cloud_account_id, tenant_id)
		VALUES
			(:cloud_resource_id, :timestamp, :metric, :metric_type, :tags, :value, :cloud_account_id, :tenant_id)
		ON CONFLICT (metric, cloud_resource_id, timestamp)
		DO UPDATE SET value = EXCLUDED.value
	`, metrics)
	if err != nil {
		return fmt.Errorf("failed to insert datadog network metrics: %w", err)
	}

	return nil
}

// computeAvgAndLastTs returns the average of positive values and the latest timestamp (epoch seconds).
func computeAvgAndLastTs(values []float64, timestamps []int64) (float64, int64) {
	var sum, count float64
	var lastTs int64
	for i, v := range values {
		if v > 0 {
			sum += v
			count++
			if i < len(timestamps) && timestamps[i] > lastTs {
				lastTs = timestamps[i]
			}
		}
	}
	if count == 0 {
		return 0, 0
	}
	// Timestamps from FetchMetricsQuery are in milliseconds
	return sum / count, lastTs / 1000
}
