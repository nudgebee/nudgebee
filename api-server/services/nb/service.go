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
	cleanupBatchSize  = 10000
	cleanupMaxPerRun  = 100000
	cleanupMaxBatches = cleanupMaxPerRun / cleanupBatchSize // 10 batches
)

type dataCleanupJob struct {
	Name      string
	Metastore database.DatabaseManagerType
	Query     string
	Batched   bool // when true, execute in 10K batches up to 100K per cron run
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
				Name:      "events_normal",
				Metastore: database.Metastore,
				Batched:   true,
				Query: fmt.Sprintf(`WITH to_del AS (
					SELECT e.id FROM events e
					WHERE e.created_at < now() - interval '%d days'
						AND e.priority IN ('DEBUG', 'INFO', 'LOW', 'MEDIUM')
						AND NOT EXISTS (SELECT 1 FROM event_log_analysis ela WHERE ela.event_id = e.id)
						AND NOT EXISTS (SELECT 1 FROM llm_conversation_feedback lcf WHERE lcf.session_id = e.id::text AND lcf.module = 'investigate')
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
					LIMIT %d
				) DELETE FROM events WHERE id IN (SELECT id FROM to_del)`, config.Config.NBRetentionDaysEventsCritical, cleanupBatchSize),
			},
			{
				Name:      "events_high",
				Metastore: database.Warehouse,
				Query:     fmt.Sprintf(`alter table nudgebee.events_shard on cluster 'default' delete WHERE created_at < now() - interval %d day and priority in ('HIGH')`, config.Config.NBRetentionDaysEventsCritical),
			},
			{
				Name:      "notifications_sent",
				Metastore: database.Metastore,
				Query:     fmt.Sprintf(`DELETE FROM sent_notifications WHERE created_at < now() - interval '%d days'`, config.Config.NBRetentionDaysCronEvents),
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
				Query: fmt.Sprintf(`WITH to_del AS (
					SELECT ctid FROM k8s_workloads
					WHERE is_active = false AND creation_time < now() - interval '%d days'
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
				err = executeBatchedMetastoreJob(ctx, cj.Name, cj.Query)
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

func executeBatchedMetastoreJob(ctx *security.RequestContext, jobName, query string) error {
	databaseManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		ctx.GetLogger().Error("nb: failed to get database manager", "error", err, "job", jobName)
		return err
	}

	var totalDeleted int64
	for batch := 1; batch <= cleanupMaxBatches; batch++ {
		r, err := databaseManager.Db.Exec(query)
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
