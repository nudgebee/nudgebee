package nb

import (
	"fmt"
	"io"
	"nudgebee/services/common"
	"nudgebee/services/config"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
	"strings"
	"time"
)

var versionSyncedAt *time.Time
var versionInformation map[string]string

func GetVersions(ctx *security.RequestContext) (map[string]string, error) {

	if versionSyncedAt == nil || time.Since(*versionSyncedAt) > time.Minute*30 {
		resp, err := common.HttpGet("https://api.github.com/repos/nudgebee/k8s-agent/releases")
		if err != nil {
			ctx.GetLogger().Error("nb: failed to fetch latest version", "error", err)
			return nil, err
		}
		defer func() {
			err := resp.Body.Close()
			if err != nil {
				ctx.GetLogger().Error("nb: failed to close response body", "error", err)
			}
		}()

		if resp.StatusCode != 200 {
			ctx.GetLogger().Error("nb: failed to fetch latest version", "status", resp.StatusCode)
			return nil, err
		}

		data, err := io.ReadAll(resp.Body)
		if err != nil {
			ctx.GetLogger().Error("nb: unable to read response body", "error", err)
			return nil, err
		}

		appVersions := []map[string]any{}
		err = common.UnmarshalJson(data, &appVersions)
		if err != nil {
			ctx.GetLogger().Error("nb: unable to parse JSON response", "error", err)
			return nil, err
		}

		versionsMap := map[string]string{}
		versionsMap["agent_version_latest"] = ""

		for _, release := range appVersions {
			// Filter out draft and prerelease
			if draft, ok := release["draft"].(bool); ok && draft {
				continue
			}
			if prerelease, ok := release["prerelease"].(bool); ok && prerelease {
				continue
			}

			tagName, ok := release["tag_name"].(string)
			if !ok {
				continue
			}
			if strings.Contains(tagName, "nudgebee-agent-") {
				version := strings.Replace(tagName, "nudgebee-agent-", "", 1)
				versionsMap["agent_version_latest"] = version
				break // use the latest valid version only
			}
		}

		versionSyncedAt1 := time.Now()
		versionSyncedAt = &versionSyncedAt1
		versionInformation = versionsMap
	}

	return versionInformation, nil
}

const (
	// How long a Microsoft Teams card's buttons stay valid, and so how long its
	// sent_notifications row is kept.
	teamsCardRetentionDays = 7
	cleanupBatchSize       = 10000
	cleanupMaxPerRun       = 100000
	cleanupMaxBatches      = cleanupMaxPerRun / cleanupBatchSize // 10 batches
)

type dataCleanupJob struct {
	Name      string
	Metastore database.DatabaseManagerType
	Query     string
	Batched   bool // when true, execute in 10K batches up to 100K per cron run
	// MaxBatches overrides cleanupMaxBatches for batched jobs whose daily inflow
	// is close to 100K, so a backlog would otherwise never drain.
	MaxBatches int
}

func CleanupData(ctx *security.RequestContext, job ...string) {
	cleanupJobs :=
		[]dataCleanupJob{
			{
				Name:      "hdb_cron_events",
				Metastore: database.Metastore,
				Query:     fmt.Sprintf(`DELETE FROM hdb_catalog.hdb_cron_events WHERE scheduled_time < now() - interval '%d days'`, config.Config.NBRetentionDaysCronEvents),
			},
			{
				Name:      "hdb_scheduled_events",
				Metastore: database.Metastore,
				Query:     fmt.Sprintf(`DELETE FROM hdb_catalog.hdb_scheduled_events WHERE scheduled_time < now() - interval '%d days'`, config.Config.NBRetentionDaysCronEvents),
			},
			{
				Name:      "hdb_event_invocation_logs",
				Metastore: database.Metastore,
				Query:     fmt.Sprintf(`DELETE FROM hdb_catalog.event_invocation_logs WHERE created_at < now() - interval '%d days'`, config.Config.NBRetentionDaysCronEvents),
			},
			{
				Name:      "hdb_event_log",
				Metastore: database.Metastore,
				Query:     fmt.Sprintf(`DELETE FROM hdb_catalog.event_log WHERE created_at < now() - interval '%d days'`, config.Config.NBRetentionDaysCronEvents),
			},
			{
				Name:      "agent_audit_log",
				Metastore: database.Warehouse,
				Query:     fmt.Sprintf(`alter table nudgebee.agent_audit_log_shard on cluster 'default' delete WHERE created_at < now() - interval %d day`, config.Config.NBRetentionDaysAgentConnectLogs),
			},
			{
				// Events cascade-delete to event_classification, but
				// event_bulk_operations(classification_id) has no ON DELETE, so one
				// event behind a bulk operation fails the whole batch every run.
				// Skip those events (applies to events_high too).
				Name:      "events_normal",
				Metastore: database.Metastore,
				Batched:   true,
				Query: fmt.Sprintf(`WITH to_del AS (
					SELECT e.id FROM events e
					WHERE e.created_at < now() - interval '%d days'
						AND e.priority IN ('DEBUG', 'INFO', 'LOW', 'MEDIUM')
						AND NOT EXISTS (SELECT 1 FROM event_log_analysis ela WHERE ela.event_id = e.id)
						AND NOT EXISTS (SELECT 1 FROM llm_conversation_feedback lcf WHERE lcf.session_id = e.id::text AND lcf.module = 'investigate')
						AND NOT EXISTS (
							SELECT 1 FROM event_classification ec
							JOIN event_bulk_operations ebo ON ebo.classification_id = ec.id
							WHERE ec.event_id = e.id
						)
					LIMIT %d
				) DELETE FROM events WHERE id IN (SELECT id FROM to_del)`, config.Config.NBRetentionDaysEventsNormal, cleanupBatchSize),
			},
			{
				Name:      "events_normal",
				Metastore: database.Warehouse,
				Query:     fmt.Sprintf(`alter table nudgebee.events_shard on cluster 'default' delete WHERE created_at < now() - interval %d day and priority in ('DEBUG', 'INFO', 'LOW', 'MEDIUM')`, config.Config.NBRetentionDaysEventsNormal),
			},
			{
				Name:      "events_high",
				Metastore: database.Metastore,
				Batched:   true,
				Query: fmt.Sprintf(`WITH to_del AS (
					SELECT e.id FROM events e
					WHERE e.created_at < now() - interval '%d days'
						AND e.priority IN ('HIGH')
						AND NOT EXISTS (SELECT 1 FROM event_log_analysis ela WHERE ela.event_id = e.id)
						AND NOT EXISTS (SELECT 1 FROM llm_conversation_feedback lcf WHERE lcf.session_id = e.id::text AND lcf.module = 'investigate')
						AND NOT EXISTS (
							SELECT 1 FROM event_classification ec
							JOIN event_bulk_operations ebo ON ebo.classification_id = ec.id
							WHERE ec.event_id = e.id
						)
					LIMIT %d
				) DELETE FROM events WHERE id IN (SELECT id FROM to_del)`, config.Config.NBRetentionDaysEventsCritical, cleanupBatchSize),
			},
			{
				Name:      "events_high",
				Metastore: database.Warehouse,
				Query:     fmt.Sprintf(`alter table nudgebee.events_shard on cluster 'default' delete WHERE created_at < now() - interval %d day and priority in ('HIGH')`, config.Config.NBRetentionDaysEventsCritical),
			},
			{
				// A row holding a Microsoft Teams card with buttons is what a click on
				// the card updates, so it is kept while the buttons are valid: 7 days
				// (MAX_AGE_SECONDS in notifications-server's ms_teams/card_actions.py).
				Name:      "notifications_sent",
				Metastore: database.Metastore,
				Query: fmt.Sprintf(`DELETE FROM sent_notifications WHERE created_at < now() - interval '%d days'
					AND (teams_message_id IS NULL OR created_at < now() - interval '%d days')`,
					config.Config.NBRetentionDaysCronEvents, teamsCardRetentionDays),
			},
			{
				// Rows carry the full candidate set per search, so this table
				// grows with question volume. Batched: a busy environment can
				// accumulate tens of thousands of rows a day.
				Name:      "llm_retrieval_trace",
				Metastore: database.Metastore,
				Batched:   true,
				Query: fmt.Sprintf(`WITH to_del AS (
					SELECT id FROM llm_retrieval_trace
					WHERE created_at < now() - interval '%d days'
					LIMIT %d
				) DELETE FROM llm_retrieval_trace WHERE id IN (SELECT id FROM to_del)`,
					config.Config.NBRetentionDaysRagRetrievalTrace, cleanupBatchSize),
			},
			{
				Name:      "cloud_account_usage_report",
				Metastore: database.Metastore,
				Query:     fmt.Sprintf(`DELETE FROM cloud_account_usage_report WHERE report_date < now() - interval '%d days'`, config.Config.NBRetentionDaysCloudAccountUsageReport),
			},
			{
				Name:      "k8s_pods",
				Metastore: database.Metastore,
				Query:     fmt.Sprintf(`DELETE FROM k8s_pods WHERE is_active = false and creation_time < now() - interval '%d days'`, config.Config.NBRetentionDaysK8sResources),
			},
			{
				Name:      "k8s_workloads",
				Metastore: database.Metastore,
				Batched:   true,
				// Skip workloads still referenced by application_group_mapping: the FK
				// (workload_name, workload_kind, namespace_name, account_id) → k8s_workloads
				// is ON DELETE RESTRICT, so one referenced row fails the entire 10K batch and
				// aborts the run. application_group_mapping is small (hundreds of rows) so the
				// NOT EXISTS anti-join is cheap. Mapped workloads represent user app-group
				// membership and are intentionally retained (not cascade-deleted).
				Query: fmt.Sprintf(`WITH to_del AS (
					SELECT w.ctid FROM k8s_workloads w
					WHERE w.is_active = false AND w.creation_time < now() - interval '%d days'
					  AND NOT EXISTS (
						SELECT 1 FROM application_group_mapping m
						WHERE m.workload_name = w.name
						  AND m.workload_kind = w.kind
						  AND m.namespace_name = w.namespace
						  AND m.account_id = w.cloud_account_id
					  )
					LIMIT %d
				) DELETE FROM k8s_workloads WHERE ctid IN (SELECT ctid FROM to_del)`, config.Config.NBRetentionDaysK8sResources, cleanupBatchSize),
			},
			{
				Name:      "k8s_nodes",
				Metastore: database.Metastore,
				Query:     fmt.Sprintf(`DELETE FROM k8s_nodes WHERE is_active = false and node_creation_time < now() - interval '%d days'`, config.Config.NBRetentionDaysK8sResources),
			},
			{
				Name:      "knowledge_graph_edges",
				Metastore: database.Metastore,
				Batched:   true,
				Query: fmt.Sprintf(`WITH to_del AS (
					SELECT id FROM knowledge_graph_edge
					WHERE is_active = false
					  AND updated_at < now() - interval '%d days'
					LIMIT %d
				) DELETE FROM knowledge_graph_edge WHERE id IN (SELECT id FROM to_del)`,
					config.Config.NBRetentionDaysKGInactiveEdges, cleanupBatchSize),
			},
			{
				// event_log_analysis grew unbounded once the unique constraint on
				// (fingerprint, account, aggregation key, analysis type) was dropped
				// in V850 to allow historical runs per event. Nothing reclaims it,
				// so age out whole event identities that have gone quiet.
				//
				// The trigger is the age of the *newest* run for an identity, not each
				// row's own age: trimming old rows out of a still-active identity would
				// cascade-delete the event_analysis_mapping rows pointing at them and
				// silently drop live events back to the fingerprint fallback, where they
				// would surface a different event's analysis. Deleting only identities
				// where nothing is recent avoids that entirely.
				//
				// Expressed as "old row with no recent sibling" rather than a GROUP BY
				// with HAVING max(...): this query re-runs once per batch, and measured
				// on 300k rows across 30k identities the aggregate form costs 133ms per
				// batch against 29ms for this one, because it re-aggregates the whole
				// table every time.
				//
				// COALESCE(updated_at, recorded_at) on both sides: updated_at is nullable
				// with no default (V443), and a bare `updated_at < cutoff` is NULL for
				// legacy rows, so they would never age out and would accumulate silently.
				Name:      "event_log_analysis",
				Metastore: database.Metastore,
				Batched:   true,
				Query: fmt.Sprintf(`WITH to_del AS (
					SELECT e.id FROM event_log_analysis e
					WHERE COALESCE(e.updated_at, e.recorded_at) < now() - interval '%d days'
						AND NOT EXISTS (
							SELECT 1 FROM event_log_analysis n
							WHERE n.cloud_account_id = e.cloud_account_id
								AND n.event_fingerprint = e.event_fingerprint
								AND n.event_aggregation_key = e.event_aggregation_key
								AND n.analysis_type = e.analysis_type
								AND COALESCE(n.updated_at, n.recorded_at) >= now() - interval '%d days'
						)
					LIMIT %d
				) DELETE FROM event_log_analysis WHERE id IN (SELECT id FROM to_del)`,
					config.Config.NBRetentionDaysEventAnalysis, config.Config.NBRetentionDaysEventAnalysis, cleanupBatchSize),
			},
			{
				// Node retention is shorter than edge retention, so a tombstoned edge
				// can outlive the node it points at. The two NOT EXISTS guards keep
				// any node with a still-*active* edge alive; they are separate
				// subqueries so each can use its own index (source_node_id /
				// destination_node_id) — an OR inside one would force a seq scan.
				// Edges left dangling by this delete are reclaimed by the
				// knowledge_graph_orphan_edges job immediately below.
				Name:      "knowledge_graph_nodes",
				Metastore: database.Metastore,
				Batched:   true,
				Query: fmt.Sprintf(`WITH to_del AS (
					SELECT n.id FROM knowledge_graph_node n
					WHERE n.is_active = false
					  AND n.updated_at < now() - interval '%d days'
					  AND NOT EXISTS (
						SELECT 1 FROM knowledge_graph_edge e
						WHERE e.source_node_id = n.id AND e.is_active = true
					  )
					  AND NOT EXISTS (
						SELECT 1 FROM knowledge_graph_edge e
						WHERE e.destination_node_id = n.id AND e.is_active = true
					  )
					LIMIT %d
				) DELETE FROM knowledge_graph_node WHERE id IN (SELECT id FROM to_del)`,
					config.Config.NBRetentionDaysKGInactiveNodes, cleanupBatchSize),
			},
			{
				// Reclaims edges whose endpoint node has been deleted, so the shorter
				// node retention never leaves a dangling row behind. Must run after
				// knowledge_graph_nodes to catch the ones that job just orphaned.
				// Restricted to is_active = false on purpose: an *active* edge can
				// only reach a missing node through a bug, and the node job's guards
				// exist precisely to prevent that — silently deleting such an edge
				// here would hide it. Complements knowledge_graph_edges, which
				// reclaims by age rather than by orphanhood.
				Name:      "knowledge_graph_orphan_edges",
				Metastore: database.Metastore,
				Batched:   true,
				Query: fmt.Sprintf(`WITH to_del AS (
					SELECT e.id FROM knowledge_graph_edge e
					WHERE e.is_active = false
					  AND (
						NOT EXISTS (
							SELECT 1 FROM knowledge_graph_node n WHERE n.id = e.source_node_id
						)
						OR NOT EXISTS (
							SELECT 1 FROM knowledge_graph_node n WHERE n.id = e.destination_node_id
						)
					  )
					LIMIT %d
				) DELETE FROM knowledge_graph_edge WHERE id IN (SELECT id FROM to_del)`,
					cleanupBatchSize),
			},
			{
				// The FK recommendation_resolution(recommendation_id) → recommendation is
				// ON DELETE RESTRICT, so one referenced row fails the entire batch and
				// aborts the run. Recommendations with a resolution are the PR / ticket
				// history behind the Resolutions view, so they are intentionally retained
				// (not cascade-deleted). idx_rr_recommendation_id keeps the anti-join cheap.
				Name:      "recommendations_archive",
				Metastore: database.Metastore,
				Batched:   true,
				Query: fmt.Sprintf(`WITH to_del AS (
					SELECT r.id FROM recommendation r
					WHERE r.status = 'Archive'
					  AND r.updated_at < now() - interval '%d days'
					  AND NOT EXISTS (
						SELECT 1 FROM recommendation_resolution rr WHERE rr.recommendation_id = r.id
					  )
					LIMIT %d
				) DELETE FROM recommendation WHERE id IN (SELECT id FROM to_del)`,
					config.Config.NBRetentionDaysRecommendationsArchive, cleanupBatchSize),
			},
			{
				Name:      "ai_cost_report_dispatch_log",
				Metastore: database.Metastore,
				Query:     fmt.Sprintf(`DELETE FROM ai_cost_report_dispatch_log WHERE report_date < (now() - interval '%d days')::date`, config.Config.NBRetentionDaysAiCostReportDispatchLog),
			},
			{
				// One row per delivered event, carrying the full raw payload, so it
				// grows with alert volume. event_created_at is UTC wall clock in a
				// timestamp-without-time-zone column (see webhook_recovery.go), hence
				// the AT TIME ZONE 'utc' on the cutoff. Rows still pending or
				// processing are kept: they are deliveries that never completed and
				// the only record of the alert, re-driven by webhook recovery.
				Name:      "event_incoming_webhooks",
				Metastore: database.Metastore,
				Batched:   true,
				Query: fmt.Sprintf(`WITH to_del AS (
					SELECT id FROM event_incoming_webhooks
					WHERE event_created_at < (now() AT TIME ZONE 'utc') - interval '%d days'
					  AND processing_status NOT IN ('pending', 'processing')
					LIMIT %d
				) DELETE FROM event_incoming_webhooks WHERE id IN (SELECT id FROM to_del)`,
					config.Config.NBRetentionDaysEventIncomingWebhooks, cleanupBatchSize),
			},
			{
				// Event resolutions whose parent event was purged by event retention.
				// event_resolution lacks an FK constraint to events, so this sweep
				// reclaims orphaned records.
				Name:      "event_resolutions_orphaned",
				Metastore: database.Metastore,
				Batched:   true,
				Query: fmt.Sprintf(`WITH to_del AS (
					SELECT er.id FROM event_resolution er
					WHERE NOT EXISTS (SELECT 1 FROM events e WHERE e.id = er.event_id)
					LIMIT %d
				) DELETE FROM event_resolution WHERE id IN (SELECT id FROM to_del)`,
					cleanupBatchSize),
			},
			{
				// Granular telemetry metric points accumulated by collectors, OpenCost,
				// and ETL. Dashboards and anomaly rules only query recent windows (24h-30d).
				// ~50K rows/day age past retention, so the default 100K cap drains a large backlog
				// only ~50K/day net; 50 batches (500K) clears it in days instead of months.
				Name:       "cloud_resource_metrics",
				Metastore:  database.Metastore,
				Batched:    true,
				MaxBatches: 50,
				Query: fmt.Sprintf(`WITH to_del AS (
					SELECT id FROM cloud_resource_metrics
					WHERE timestamp < now() - interval '%d days'
					LIMIT %d
				) DELETE FROM cloud_resource_metrics WHERE id IN (SELECT id FROM to_del)`,
					config.Config.NBRetentionDaysCloudResourceMetrics, cleanupBatchSize),
			},
			{
				// Inactive or deleted resources that are past retention and have no
				// references in spends, events, metrics, application mappings, attributes,
				// recommendations, relationships, or query logs.
				//
				// Guards:
				// - spends, events, metrics, mappings: preserve historical telemetry and app groups.
				// - recommendation: prevents cascade-deleting recommendations on inactive resources,
				//   which would otherwise hit RESTRICT on recommendation_resolution(recommendation_id)
				//   and abort the batch.
				// - cloud_resource_relationships (source_id & destination_id) and dw_queries:
				//   guards against NO ACTION / RESTRICT foreign key violations.
				// - k8s_pods, k8s_workloads, k8s_nodes, insight, workload_criticality: no FK, so
				//   deleting would leave dangling resource id links. k8s rows age out via the
				//   k8s_* sweeps above, after which the resource becomes eligible on a later run.
				// 50 batches (500K) clears a multi-million-row backlog in days instead of weeks.
				Name:       "cloud_resources_inactive",
				Metastore:  database.Metastore,
				Batched:    true,
				MaxBatches: 50,
				Query: fmt.Sprintf(`WITH to_del AS (
					SELECT cr.id FROM cloud_resourses cr
					WHERE (cr.is_active = false OR cr.status IN ('Deleted', 'Inactive'))
					  AND cr.updated_at < now() - interval '%d days'
					  AND NOT EXISTS (SELECT 1 FROM spends s WHERE s.cloud_resource_id = cr.id)
					  AND NOT EXISTS (SELECT 1 FROM events e WHERE e.cloud_resource_id = cr.id)
					  AND NOT EXISTS (SELECT 1 FROM cloud_resource_metrics crm WHERE crm.cloud_resource_id = cr.id)
					  AND NOT EXISTS (SELECT 1 FROM application_group_mapping agm WHERE agm.cloud_resource_id = cr.id)
					  AND NOT EXISTS (SELECT 1 FROM cloud_resource_attributes cra WHERE cra.resource_id = cr.id)
					  AND NOT EXISTS (SELECT 1 FROM recommendation r WHERE r.resource_id = cr.id)
					  AND NOT EXISTS (SELECT 1 FROM cloud_resource_relationships crr WHERE crr.source_id = cr.id)
					  AND NOT EXISTS (SELECT 1 FROM cloud_resource_relationships crr WHERE crr.destination_id = cr.id)
					  AND NOT EXISTS (SELECT 1 FROM dw_queries dwq WHERE dwq.resource_id = cr.id)
					  AND NOT EXISTS (SELECT 1 FROM k8s_pods kp WHERE kp.cloud_resource_id = cr.id)
					  AND NOT EXISTS (SELECT 1 FROM k8s_workloads kw WHERE kw.cloud_resource_id = cr.id)
					  AND NOT EXISTS (SELECT 1 FROM k8s_nodes kn WHERE kn.cloud_resource_id = cr.id)
					  AND NOT EXISTS (SELECT 1 FROM insight i WHERE i.resource_id = cr.id)
					  AND NOT EXISTS (SELECT 1 FROM workload_criticality wc WHERE wc.cloud_resource_id = cr.id)
					LIMIT %d
				) DELETE FROM cloud_resourses WHERE id IN (SELECT id FROM to_del)`,
					config.Config.NBRetentionDaysInactiveCloudResources, cleanupBatchSize),
			},
			{
				// Pod/workload CPU and memory profiling dumps (JSONB profile payloads).
				Name:      "application_profile",
				Metastore: database.Metastore,
				Batched:   true,
				Query: fmt.Sprintf(`WITH to_del AS (
					SELECT id FROM application_profile
					WHERE created_at < now() - interval '%d days'
					LIMIT %d
				) DELETE FROM application_profile WHERE id IN (SELECT id FROM to_del)`,
					config.Config.NBRetentionDaysApplicationProfiles, cleanupBatchSize),
			},
			{
				// One row per anomaly detection; no table references anomaly, and readers
				// (UI list, anomaly_execute tool) look back at most a month. Metric anomalies
				// link to their event by fingerprint and have a NULL anomaly_status.
				// Cloud-spend anomalies stay OPEN while the spend stays high and their event's
				// finding_id is the row id, so an OPEN row is kept regardless of age: deleting
				// it would leave its event FIRING forever and re-open a duplicate.
				Name:      "anomaly",
				Metastore: database.Metastore,
				Batched:   true,
				Query: fmt.Sprintf(`WITH to_del AS (
					SELECT id FROM anomaly
					WHERE created_at < now() - interval '%d days'
					  AND anomaly_status IS DISTINCT FROM 'OPEN'
					LIMIT %d
				) DELETE FROM anomaly WHERE id IN (SELECT id FROM to_del)`,
					config.Config.NBRetentionDaysAnomalies, cleanupBatchSize),
			},
			{
				// Periodic SLO measurements. slo_report only references slo_config, and
				// nothing references slo_report.
				Name:      "slo_report",
				Metastore: database.Metastore,
				Batched:   true,
				Query: fmt.Sprintf(`WITH to_del AS (
					SELECT id FROM slo_report
					WHERE created_at < now() - interval '%d days'
					LIMIT %d
				) DELETE FROM slo_report WHERE id IN (SELECT id FROM to_del)`,
					config.Config.NBRetentionDaysSloReports, cleanupBatchSize),
			},
		}

	jobsToRemove := []dataCleanupJob{}
	if len(job) > 0 {
		for _, j := range job {
			for _, cj := range cleanupJobs {
				if cj.Name == j {
					jobsToRemove = append(jobsToRemove, cj)
					break
				}
			}
		}
	} else {
		jobsToRemove = cleanupJobs
	}

	for _, cj := range jobsToRemove {
		ctx.GetLogger().Info("nb: cleaning up job", "job", cj.Name, "store", cj.Metastore)
		switch cj.Metastore {
		case database.Metastore:
			var err error
			if cj.Batched {
				err = executeBatchedMetastoreJob(ctx, cj.Name, cj.Query, cj.MaxBatches)
			} else {
				err = executeMetastoreJob(ctx, cj.Name, cj.Query)
			}
			if err != nil {
				ctx.GetLogger().Error("nb: failed to clean up job", "error", err, "job", cj.Name, "store", cj.Metastore)
			}
		case database.Warehouse:
			err := executeWarehouseJob(ctx, cj.Name, cj.Query)
			if err != nil {
				ctx.GetLogger().Error("nb: failed to clean up job", "error", err, "job", cj.Name, "store", cj.Metastore)
			}
		}
	}
}

func executeMetastoreJob(ctx *security.RequestContext, jobName, query string) error {
	databaseManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		ctx.GetLogger().Error("nb: failed to get database manager", "error", err, "job", jobName)
		return err
	}
	r, err := databaseManager.Db.Exec(query)
	if err != nil {
		ctx.GetLogger().Error("nb: failed to execute query", "error", err, "query", query, "job", jobName)
		return err
	}
	c, err := r.RowsAffected()
	if err != nil {
		ctx.GetLogger().Error("nb: failed to get rows affected", "error", err, "job", jobName)
		return err
	}
	ctx.GetLogger().Info("nb: cleaned up job", "rows_affected", c, "job", jobName)
	return nil
}

func executeBatchedMetastoreJob(ctx *security.RequestContext, jobName, query string, maxBatches int) error {
	databaseManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		ctx.GetLogger().Error("nb: failed to get database manager", "error", err, "job", jobName)
		return err
	}

	if maxBatches <= 0 {
		maxBatches = cleanupMaxBatches
	}
	var totalDeleted int64
	for batch := 1; batch <= maxBatches; batch++ {
		// Honor context cancellation/deadline between batches so a caller-imposed
		// timeout actually bounds the run (e.g. the detached cron goroutines wrap
		// this in a context.WithTimeout). Harmless for callers whose context never
		// cancels.
		if err := ctx.GetContext().Err(); err != nil {
			ctx.GetLogger().Warn("nb: stopping batched cleanup early", "error", err, "job", jobName, "batch", batch, "total_deleted", totalDeleted)
			return err
		}
		r, err := databaseManager.Db.ExecContext(ctx.GetContext(), query)
		if err != nil {
			ctx.GetLogger().Error("nb: failed to execute batch", "error", err, "query", query, "job", jobName, "batch", batch)
			return err
		}
		c, err := r.RowsAffected()
		if err != nil {
			ctx.GetLogger().Error("nb: failed to get rows affected", "error", err, "job", jobName, "batch", batch)
			return err
		}
		totalDeleted += c
		ctx.GetLogger().Info("nb: batch cleanup progress", "batch", batch, "rows_deleted", c, "total_deleted", totalDeleted, "job", jobName)
		if c == 0 {
			break
		}
	}

	ctx.GetLogger().Info("nb: cleaned up job", "rows_affected", totalDeleted, "job", jobName)
	return nil
}

func executeWarehouseJob(ctx *security.RequestContext, jobName, query string) error {
	if config.Config.ClickhouseEnabled {
		databaseManager, err := database.GetDatabaseManager(database.Warehouse)
		if err != nil {
			ctx.GetLogger().Error("nb: failed to get warehouse database manager", "error", err, "job", jobName)
			return err
		}
		r, err := databaseManager.Db.Exec(query)
		if err != nil {
			ctx.GetLogger().Error("nb: failed to execute query", "error", err, "query", query, "job", jobName)
			return err
		}
		c, err := r.RowsAffected()
		if err != nil {
			ctx.GetLogger().Error("nb: failed to get rows affected", "error", err, "job", jobName)
			return err
		}
		ctx.GetLogger().Info("nb: cleaned up old events", "rows_affected", c, "job", jobName)
	} else {
		ctx.GetLogger().Info("nb: clickhouse is not enabled", "job", jobName, "job", jobName)
	}

	return nil
}
