package anomoly

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"nudgebee/services/application"
	"nudgebee/services/common"
	"nudgebee/services/config"
	"nudgebee/services/event"
	"nudgebee/services/internal/database"
	"nudgebee/services/ml"
	"nudgebee/services/observability"
	"nudgebee/services/relay"
	"nudgebee/services/security"
	"nudgebee/services/tenant"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	"golang.org/x/sync/errgroup"
)

const dateTimeFormat = "2006-01-02 15:04:05"

type AnomalyProcessingMessage struct {
	TenantId                string      `json:"tenant_id"`
	AccountId               string      `json:"account_id"`
	ApplicationNamespace    string      `json:"application_namespace"`
	ApplicationName         string      `json:"application_name"`
	StartTime               time.Time   `json:"start_time"`
	EndTime                 time.Time   `json:"end_time"`
	EvaluationPeriodMinutes *int        `json:"evaluation_period_minutes"`
	AnomalyType             AnomalyType `json:"anomaly_type"`
}

func init() {
	err := common.MqConsume(
		config.Config.RabbitMqServicesExchange,
		config.Config.RabbitMqServicesAnomalyProcessingQueue,
		config.Config.RabbitMqServicesAnomalyProcessingQueue,
		config.Config.RabbitMqServicesAnomalyProcessingConcurrency, // Read concurrency from config
		func(msgCtx context.Context, data []byte) error {
			request := AnomalyProcessingMessage{}
			err := common.UnmarshalJson(data, &request)
			if err != nil {
				slog.Error("anomaly: unable to unmarshal message", "error", err, "message", string(data))
				return nil
			}
			// Build the request context directly on msgCtx (the trace context
			// extracted from the message headers) so anomaly processing logs /
			// downstream calls share the trace_id, without a throwaway allocation.
			ctx := security.NewRequestContext(msgCtx, security.NewSecurityContextForTenantAdmin(request.TenantId), slog.Default(), nil, nil)

			ProcessAnomaly(ctx, request)
			return nil
		},
	)
	if err != nil {
		slog.Error("anomaly: unable to consume message", "error", err)
	}
}

//go:embed anamoly_template.json
var embeddedJSONContent []byte

func readJSONFile() ([]AnomalyTemplate, error) {
	var config []AnomalyTemplate
	err := common.UnmarshalJson(embeddedJSONContent, &config)
	return config, err
}

// accountTenantPair holds an AccountID and its corresponding TenantID.
type accountTenantPair struct {
	AccountID string
	TenantID  string
}

// processingWorkItem defines a single unit of anomaly detection work.
type processingWorkItem struct {
	TenantID      string
	AccountID     string
	Application   application.Application
	AnomalyConfig AnomalyTemplate
}

func Execute(context *security.RequestContext) error {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return err
	}
	// The agent join MUST pin a.type = 'k8s'. An account can carry several agent
	// rows (k8s, proxy, and the cloud-sync AWS/Azure/GCP registrations); without
	// the type filter a CONNECTED *proxy* row makes an account whose in-cluster
	// agent is long dead look eligible, and every work item it generates then
	// fails at the relay with "agent not connected". ca.status keeps retired
	// accounts out for the same reason (insight/service.go filters the same way).
	rows, err := dbms.Db.Queryx(`select ca.id as cloud_account_id , ca.tenant as tenant from cloud_accounts ca inner join agent a on ca.id = a.cloud_account_id where ca.cloud_provider = 'K8s' and ca.status = 'active' and a.type = 'k8s' and a.status = 'CONNECTED' group by ca.tenant, ca.id`)
	if err != nil {
		slog.Error("anomaly: failed to query accounts for processing", "error", err)
		return err
	}
	defer func() {
		err := rows.Close()
		if err != nil {
			slog.Error("anomaly: failed to close rows", "error", err)
		}
	}()

	var pairs []accountTenantPair
	for rows.Next() {
		var cloudAccountId string
		var tenantId string
		err = rows.Scan(&cloudAccountId, &tenantId)
		if err != nil {
			slog.Error("anomaly: failed to scan account data", "error", err)
			return fmt.Errorf("anomaly: scanning account data: %w", err)
		}
		pairs = append(pairs, accountTenantPair{AccountID: cloudAccountId, TenantID: tenantId})
	}
	if err = rows.Err(); err != nil {
		slog.Error("anomaly: error iterating over account rows", "error", err)
		return fmt.Errorf("anomaly: iterating account rows: %w", err)
	}

	return executeForAccountPairs(context, pairs, true)
}

// ExecuteForAccount runs anomaly detection for a specific account.
// This is used for manual triggering via the trigger_anomaly_execute RPC action.
func ExecuteForAccount(context *security.RequestContext, accountId string) error {
	context.GetLogger().Info("anomaly: starting execution for specific account", "accountId", accountId)

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return err
	}

	// Validate account exists, is active, is K8s type, and has a connected
	// in-cluster agent. Same predicate as Execute above — a.type = 'k8s' keeps a
	// CONNECTED proxy row from vouching for a dead cluster agent.
	var tenantId string
	err = dbms.Db.Get(&tenantId, `
		SELECT ca.tenant
		FROM cloud_accounts ca
		INNER JOIN agent a ON ca.id = a.cloud_account_id
		WHERE ca.id = $1 AND ca.cloud_provider = 'K8s' AND ca.status = 'active'
			AND a.type = 'k8s' AND a.status = 'CONNECTED'
		LIMIT 1
	`, accountId)
	if err != nil {
		context.GetLogger().Error("anomaly: account not found or not eligible for anomaly detection", "error", err, "accountId", accountId)
		return fmt.Errorf("account %s not found or not eligible for anomaly detection: %w", accountId, err)
	}

	// Check if feature is enabled for tenant (fail fast for single account trigger)
	if !tenant.IsFeatureEnabled(context, tenantId, tenant.FEATURE_ANOMALY_DETECTION) {
		context.GetLogger().Info("anomaly: feature not enabled for tenant", "tenantId", tenantId, "accountId", accountId)
		return fmt.Errorf("anomaly detection feature is not enabled for tenant %s", tenantId)
	}

	return executeForAccountPairs(context, []accountTenantPair{{AccountID: accountId, TenantID: tenantId}}, false)
}

// executeForAccountPairs processes anomaly detection for the given account-tenant pairs.
// This is the shared implementation used by both Execute() and ExecuteForAccount().
// When checkFeatureEnabled is true, it skips accounts where the feature is not enabled (used by Execute for batch processing).
// When false, feature check should be done by the caller before calling this function.
func executeForAccountPairs(ctx *security.RequestContext, pairs []accountTenantPair, checkFeatureEnabled bool) error {
	allAnomalyConfigs, err := readJSONFile()
	if err != nil {
		slog.Error("anomaly: failed to read anomaly_template.json", "error", err)
		return err
	}

	var workItems []processingWorkItem
	eligibleAccountIds := make([]string, 0, len(pairs))
	slog.Info("anomaly: generating work items...", "accountCount", len(pairs))

	for _, pair := range pairs {
		if checkFeatureEnabled && !tenant.IsFeatureEnabled(ctx, pair.TenantID, tenant.FEATURE_ANOMALY_DETECTION) {
			continue
		}

		apps, err := application.ListApplications(ctx, pair.AccountID)
		if err != nil {
			slog.Error("anomaly: failed to list applications for account", "error", err, "accountId", pair.AccountID, "tenantId", pair.TenantID)
			continue
		}

		eligibleAccountIds = append(eligibleAccountIds, pair.AccountID)
		for _, app := range apps {
			for _, cfg := range allAnomalyConfigs {
				workItems = append(workItems, processingWorkItem{
					TenantID:      pair.TenantID,
					AccountID:     pair.AccountID,
					Application:   app,
					AnomalyConfig: cfg,
				})
			}
		}
	}

	closeOrphanedAnomalyEvents(eligibleAccountIds)

	if len(workItems) == 0 {
		slog.Info("anomaly: no work items generated for processing.")
		return nil
	}

	// Pre-fetch existing anomaly counts for all eligible accounts in a single query,
	// instead of running a COUNT(*) per (app × anomaly_type) inside the loop.
	existingAnomalyCounts := fetchExistingAnomalyCounts(eligibleAccountIds)

	// Shuffle the workItems slice to distribute load
	source := rand.NewSource(time.Now().UnixNano())
	randomizer := rand.New(source)
	randomizer.Shuffle(len(workItems), func(i, j int) {
		workItems[i], workItems[j] = workItems[j], workItems[i]
	})

	processingStartTime := time.Now()
	slog.Info("anomaly: starting processing for work items", "count", len(workItems))

	for _, item := range workItems {
		slog.Info("anomaly: processing work item",
			"accountId", item.AccountID, "tenantId", item.TenantID,
			"application", item.Application.Name, "namespace", item.Application.K8sNamespace,
			"anomalyType", item.AnomalyConfig.AnomalyType)

		var existingCount int
		if typeCounts, ok := existingAnomalyCounts[item.AccountID]; ok {
			existingCount = typeCounts[item.AnomalyConfig.AnomalyType]
		}
		processingErr := processSingleApplicationMlAsync(ctx, item.AnomalyConfig, item.TenantID, item.AccountID, item.Application, existingCount)

		if processingErr != nil {
			slog.Error("anomaly: failed to process work item", "error", processingErr,
				"accountId", item.AccountID, "application", item.Application.Name, "anomalyType", item.AnomalyConfig.AnomalyType)
		}
	}

	slog.Info("anomaly: execution completed for all work items", "total_time_taken_seconds", time.Since(processingStartTime).Seconds())
	return nil
}

// fetchExistingAnomalyCounts returns a nested map[accountId][anomalyType] -> count of
// existing rows in the `anomaly` table. A single grouped query replaces what used to be
// one COUNT(*) per (app × anomalyType) inside the work-item loop.
// Missing entries (no rows for an account/type) implicitly read back as 0.
func fetchExistingAnomalyCounts(accountIds []string) map[string]map[AnomalyType]int {
	result := make(map[string]map[AnomalyType]int, len(accountIds))

	// Drop empty IDs defensively — an empty account_id in IN (...) would scan rows
	// belonging to other tenants/accounts, which the surrounding code never intends.
	nonEmptyAccountIds := make([]string, 0, len(accountIds))
	for _, id := range accountIds {
		if id != "" {
			nonEmptyAccountIds = append(nonEmptyAccountIds, id)
		}
	}
	if len(nonEmptyAccountIds) == 0 {
		return result
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		slog.Error("anomaly: failed to get db manager for existing anomaly counts", "error", err)
		return result
	}

	query, args, err := sqlx.In(`SELECT account_id, anomaly_type, COUNT(*) FROM anomaly WHERE account_id IN (?) GROUP BY account_id, anomaly_type`, nonEmptyAccountIds)
	if err != nil {
		slog.Error("anomaly: failed to build existing-anomaly-counts query", "error", err)
		return result
	}
	query = dbms.Db.Rebind(query)

	rows, err := dbms.Db.Queryx(query, args...)
	if err != nil {
		slog.Error("anomaly: failed to query existing anomaly counts", "error", err)
		return result
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil {
			slog.Error("anomaly: failed to close existing-anomaly-counts rows", "error", cerr)
		}
	}()

	for rows.Next() {
		var accountID string
		var anomalyType AnomalyType
		var count int
		if err := rows.Scan(&accountID, &anomalyType, &count); err != nil {
			slog.Error("anomaly: failed to scan existing anomaly count row", "error", err)
			continue
		}
		if _, ok := result[accountID]; !ok {
			result[accountID] = make(map[AnomalyType]int)
		}
		result[accountID][anomalyType] = count
	}
	if err := rows.Err(); err != nil {
		slog.Error("anomaly: error iterating existing-anomaly-counts rows", "error", err)
	}
	return result
}

// anomalyFingerprint is the single source of truth for a K8s metric anomaly's
// dedup/close identity — the same instance (account, type, workload,
// namespace) always produces the same string, across every detection cycle,
// which is what lets InsertEvent's ON CONFLICT(tenant, cloud_account_id,
// finding_id) upsert collapse repeat detections into one row instead of
// inserting a new one every cycle.
//
// Uses "|" as the field separator, not "-": account is a UUID and name/
// namespace are Kubernetes DNS-1123 labels, both of which routinely contain
// "-" themselves. Joining with "-" is ambiguous — name="api",
// namespace="prod-payments" and name="api-prod", namespace="payments" both
// serialize to the identical string under the same account+type — so two
// unrelated workloads could silently share one event and incorrectly close
// each other's anomalies. "|" cannot appear in a UUID, this type's enum
// values, or a DNS-1123 label, so it can't collide.
func anomalyFingerprint(accountId string, anomalyType AnomalyType, name, namespace string) string {
	return fmt.Sprintf("anomaly|%s|%s|%s|%s", accountId, anomalyType, name, namespace)
}

// closeAnomalyEventIfOpen auto-resolves the FIRING Anomaly event for exactly
// this (account, type, name, namespace) instance, if one is open. Call this
// when a detection cycle EXPLICITLY evaluated this instance and found it
// clean — ml.AnomalyResponse.HasAnomaly == false from the ML path, or
// isAnomaly == false from the Prometheus comparison — not merely "no result
// came back" (e.g. the ML new-workload/insufficient-training-data guard),
// which carries no information about whether a prior anomaly recovered.
//
// This is a real per-cycle recovery signal, not an inferred one, so it closes
// immediately — no debounce/buffer needed, unlike closeOrphanedAnomalyEvents.
func closeAnomalyEventIfOpen(accountId, tenantId string, anomalyType AnomalyType, name, namespace string) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		slog.Error("anomaly: failed to get db manager for closing recovered anomaly event", "error", err)
		return
	}
	fingerprint := anomalyFingerprint(accountId, anomalyType, name, namespace)

	var id string
	err = dbms.Db.QueryRowx(
		// events.ends_at is `timestamp` with no timezone (see V175); binding an
		// explicit UTC value here — rather than the SQL now(), which would cast
		// through the session's timezone setting — keeps it consistent with
		// every other write in this codebase, which timestamps in Go as UTC.
		`UPDATE events SET status = 'RESOLVED', ends_at = $4
		 WHERE tenant = $1 AND cloud_account_id = $2 AND finding_id = $3 AND status = 'FIRING'
		 RETURNING id`,
		tenantId, accountId, fingerprint, time.Now().UTC(),
	).Scan(&id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return
		}
		slog.Error("anomaly: failed to close recovered anomaly event", "error", err, "fingerprint", fingerprint)
		return
	}

	publishAnomalyEventResolved(id)
}

// publishAnomalyEventResolved delivers the resolved notification the same
// way webhook-driven resolves do (integrations/core/integration_webhook.go
// resolveEvent) — status alone doesn't notify anyone, this does.
func publishAnomalyEventResolved(eventId string) {
	_ = common.MqPublish(
		config.Config.RabbitMqEventPostProcessExchange,
		config.Config.RabbitMqEventPostProcessQueue,
		map[string]any{"event_id": eventId, "notify_resolved": true},
		common.MqPublishWithExpiration(1*time.Hour),
		common.MqPublishWithBackgroundRetry(),
	)
}

// closeOrphanedAnomalyEvents is a backstop, not the primary close path (see
// closeAnomalyEventIfOpen for that). It only catches anomaly instances that
// stop being evaluated AT ALL — the workload was deleted, the app no longer
// shows up in application.ListApplications, or the tenant disabled anomaly
// detection — so no cycle, clean or dirty, ever runs again to report a
// recovery. A FIRING anomaly event whose updated_at hasn't moved in a long
// time (default: 7 days) almost certainly fits that description, since a
// live, still-evaluated instance is either re-confirmed as FIRING (bumping
// updated_at via the upsert) or actively closed by closeAnomalyEventIfOpen
// every single cycle.
//
// The window is deliberately much longer than the detection cadence — this
// is cleanup for abandoned events, not a substitute for prompt closing.
//
// category = 'Anomaly' scopes this to K8s metric anomalies only — spend
// anomalies also use finding_type = 'Anomaly' but category = 'CostAnomaly',
// and already have their own OPEN/RESOLVED lifecycle (see spend_anomaly.go).
func closeOrphanedAnomalyEvents(accountIds []string) {
	closeAnomalyEventsUpdatedBefore(accountIds, time.Now().UTC().Add(-7*24*time.Hour))
}

// closeAnomalyEventsUpdatedBefore is closeOrphanedAnomalyEvents with the
// staleness cutoff as a parameter instead of a hardcoded "now - 7 days", so
// the WHERE clause's account/finding_type/category/status scoping can be
// tested without needing to backdate a real events.updated_at value — the
// table's set_public_events_updated_at trigger forces updated_at to NOW() on
// every UPDATE, so a freshly inserted row can't be backdated by SQL from the
// application; passing a cutoff in the future makes a fresh row "stale"
// relative to it instead.
func closeAnomalyEventsUpdatedBefore(accountIds []string, staleBefore time.Time) {
	nonEmptyAccountIds := make([]string, 0, len(accountIds))
	for _, id := range accountIds {
		if id != "" {
			nonEmptyAccountIds = append(nonEmptyAccountIds, id)
		}
	}
	if len(nonEmptyAccountIds) == 0 {
		return
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		slog.Error("anomaly: failed to get db manager for closing orphaned anomaly events", "error", err)
		return
	}

	query, args, err := sqlx.In(`
		UPDATE events
		   SET status = 'RESOLVED', ends_at = updated_at
		 WHERE cloud_account_id IN (?) AND finding_type = 'Anomaly' AND category = 'Anomaly'
		   AND status = 'FIRING' AND updated_at < ?
		 RETURNING id`, nonEmptyAccountIds, staleBefore)
	if err != nil {
		slog.Error("anomaly: failed to build orphaned-anomaly-close query", "error", err)
		return
	}
	query = dbms.Db.Rebind(query)

	rows, err := dbms.Db.Queryx(query, args...)
	if err != nil {
		slog.Error("anomaly: failed to close orphaned anomaly events", "error", err)
		return
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil {
			slog.Error("anomaly: failed to close rows after closing orphaned anomaly events", "error", cerr)
		}
	}()

	var resolvedIds []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			slog.Error("anomaly: failed to scan resolved anomaly event id", "error", err)
			continue
		}
		resolvedIds = append(resolvedIds, id)
	}
	if err := rows.Err(); err != nil {
		slog.Error("anomaly: error iterating resolved anomaly event rows", "error", err)
	}

	for _, id := range resolvedIds {
		publishAnomalyEventResolved(id)
	}
	if len(resolvedIds) > 0 {
		slog.Info("anomaly: closed orphaned anomaly events", "count", len(resolvedIds))
	}
}

func processSingleApplicationMlAsync(
	ctx *security.RequestContext,
	anomalyConfig AnomalyTemplate,
	tenantId string,
	accountId string,
	app application.Application, // Single application
	existingAnomalyCount int, // pre-fetched count of existing anomalies for (accountId, anomalyType)
) error {
	slog.Debug("anomaly: processing ML config for single application",
		"accountId", accountId, "tenantId", tenantId, "app", app.Name, "namespace", app.K8sNamespace, "anomalyType", anomalyConfig.AnomalyType)

	// Validate config
	if anomalyConfig.AnomalyType != MetricAnomolyTypeCPU && anomalyConfig.AnomalyType != MetricAnomolyTypeMemory && anomalyConfig.AnomalyType != MetricAnomolyTypeErrorRate && anomalyConfig.AnomalyType != MetricAnomolyTypeReplicas && anomalyConfig.AnomalyType != MetricAnomolyTypeLatency {
		slog.Warn("anomaly: ML processing skipped, unsupported anomaly type for ML", "type", anomalyConfig.AnomalyType, "app", app.Name)
		return nil
	}
	// Error-rate anomaly detection is on by default (off only when a tenant explicitly disables
	// FEATURE_ANOMALY_DETECTION_ERROR_RATE). Re-enabled now that the error-rate noise fix
	// (Laplace-smoothed query + 5% floor + sustained gate) makes it trustworthy.
	if anomalyConfig.AnomalyType == MetricAnomolyTypeErrorRate && !tenant.IsFeatureEnabledByDefault(ctx, tenantId, tenant.FEATURE_ANOMALY_DETECTION_ERROR_RATE) {
		slog.Debug("anomaly: ML processing skipped, error rate anomaly disabled for tenant", "tenantId", tenantId, "app", app.Name)
		return nil
	}
	// Time range for anomaly detection
	endTime := time.Now().UTC().Truncate(time.Hour)
	startTime := endTime.AddDate(0, 0, -1*config.Config.NBAnomalyTrainingDays)

	// Set evaluation period minutes
	var evaluationPeriodMinutes = config.Config.NBAnomalyEvaluationHours * 60
	if existingAnomalyCount != 0 {
		scheduledJobMinutes := 60 // This was hardcoded
		evaluationPeriodMinutes = scheduledJobMinutes
	}

	// Skip non-relevant kinds for the given app
	if !strings.EqualFold(app.K8sKind, "Deployment") && !strings.EqualFold(app.K8sKind, "StatefulSet") {
		slog.Debug("anomaly: ML processing skipped for app, kind not supported", "app", app.Name, "kind", app.K8sKind)
		return nil
	}

	// do not scan if there are no ready pods for the given app
	if app.ReadyPods == nil || *app.ReadyPods == 0 {
		slog.Debug("anomaly: ML processing skipped for app, no ready pods", "app", app.Name)
		return nil
	}

	slog.Info("anomaly: publishing ML processing message for app",
		"accountId", accountId, "tenantId", tenantId, "app", app.Name, "namespace", app.K8sNamespace, "type", anomalyConfig.AnomalyType)

	err := common.MqPublish(config.Config.RabbitMqServicesExchange, config.Config.RabbitMqServicesAnomalyProcessingQueue, AnomalyProcessingMessage{
		TenantId:                tenantId,
		AccountId:               accountId,
		ApplicationName:         app.Name,
		ApplicationNamespace:    app.K8sNamespace,
		StartTime:               startTime,
		EndTime:                 endTime,
		EvaluationPeriodMinutes: &evaluationPeriodMinutes,
		AnomalyType:             anomalyConfig.AnomalyType,
	}, common.MqPublishWithExpiration(time.Hour*1), common.MqPublishWithContext(ctx.GetContext()))

	if err != nil {
		slog.Error("anomaly: unable to publish ML message to anomaly processing queue", "error", err, "app", app.Name, "accountId", accountId)
		return err
	}

	return nil
}

func ProcessAnomaly(ctx *security.RequestContext, app AnomalyProcessingMessage) {
	// make the anomaly request
	t0 := time.Now()
	if !tenant.IsFeatureEnabled(ctx, app.TenantId, tenant.FEATURE_ANOMALY_DETECTION) {
		return
	}
	ctx.GetLogger().Info("anomaly: processing anomaly", "account", app.AccountId, "tenant", app.TenantId, "app", app.ApplicationName, "namespace", app.ApplicationNamespace, "type", app.AnomalyType)
	mlAnomalies, err := ml.GetAnomaly(ctx, ml.AnomalyRequest{
		Namespace:               app.ApplicationNamespace,
		Deployment:              app.ApplicationName,
		Tenant:                  app.TenantId,
		Account:                 app.AccountId,
		Type:                    strings.ToLower(string(app.AnomalyType)),
		StartTime:               &app.StartTime,
		EndTime:                 &app.EndTime,
		EvaluationPeriodMinutes: app.EvaluationPeriodMinutes,
	})
	if err != nil {
		//avoid unnecessary logging, anomaly failures are common
		ctx.GetLogger().Warn("anomaly: unable to generate anomaly", "account", app.AccountId, "tenant", app.TenantId, "namespace", app.ApplicationNamespace, "deployment", app.ApplicationName, "type", app.AnomalyType, "msg", err.Error())
		return
	}

	if len(mlAnomalies) == 0 {
		// The ML server returns an empty list only when it couldn't evaluate at
		// all (e.g. CancelPrediction for a new workload with too little training
		// data yet) — that's "no information", not "recovered", so don't close.
		ctx.GetLogger().Info("anomaly: no anomalies found", "account", app.AccountId)
		return
	}

	// Process each anomaly
	anomalyFound := false
	for _, mlAnomaly := range mlAnomalies {
		if !mlAnomaly.HasAnomaly {
			continue
		}
		anomalyFound = true
		anomalyValue := 0.0
		for _, d := range mlAnomaly.Data {
			if d.Anomaly {
				anomalyValue = d.Data
				break
			}
		}

		// Prepare anomaly data
		oldValue, err := common.MarshalStructToMap(mlAnomaly)
		if err != nil {
			ctx.GetLogger().Error("anomaly: unable to convert to map", "error", err)
		}

		// Insights are filtered to anomaly points and deduplicated by the ML server
		insightsJSON := []byte("[]")
		if jsonBytes, err := json.Marshal(mlAnomaly.Insights); err != nil {
			ctx.GetLogger().Error("anomaly: unable to marshal insights", "error", err)
		} else if string(jsonBytes) != "null" {
			insightsJSON = jsonBytes
		}

		endTime := time.Now().UTC()

		// Parse training end time from ML response (handles multiple Python datetime string formats)
		var trainingEndTime *time.Time
		if mlAnomaly.TrainingEndTime != nil && *mlAnomaly.TrainingEndTime != "" {
			timeFormats := []string{
				time.RFC3339,
				dateTimeFormat + ".999999", // Python datetime with microseconds
				dateTimeFormat,             // Python datetime without microseconds
			}
			for _, format := range timeFormats {
				if t, parseErr := time.Parse(format, *mlAnomaly.TrainingEndTime); parseErr == nil {
					trainingEndTime = &t
					break
				}
			}
		}

		// Invoke callback
		slog.Info("anomaly: found anomaly", "account_id", app.AccountId, "tenant", app.TenantId, "name", app.ApplicationName, "namespace", app.ApplicationNamespace, "anomaly_type", app.AnomalyType, "pod", mlAnomaly.Pod)
		err = insertAnomaly([]Anomaly{{
			Id:              common.GenerateUUID(),
			Name:            app.ApplicationName,
			Namespace:       app.ApplicationNamespace,
			AccountId:       app.AccountId,
			Tenant:          app.TenantId,
			CurrentValue:    anomalyValue,
			OldValue:        oldValue,
			AnomalyType:     app.AnomalyType,
			IsAnomaly:       true,
			EvaluatedAt:     &endTime,
			PodName:         mlAnomaly.Pod,
			TrainingEndTime: trainingEndTime,
			Insights:        insightsJSON,
		}})
		if err != nil {
			ctx.GetLogger().Error("anomaly: unable to handle callback for anomaly", "app", app.ApplicationName, "namespace", app.ApplicationNamespace, "error", err)
		}
	}

	// The ML server returned a definitive result (len(mlAnomalies) > 0) and none
	// of them had an anomaly — a real "this instance is clean" signal for this
	// cycle, not just an absence of one. Close its event if it was open.
	if !anomalyFound {
		closeAnomalyEventIfOpen(app.AccountId, app.TenantId, app.AnomalyType, app.ApplicationName, app.ApplicationNamespace)
	}
	ctx.GetLogger().Info("anomaly: processing anomaly", "account", app.AccountId, "tenant", app.TenantId, "app", app.ApplicationName, "namespace", app.ApplicationNamespace, "type", app.AnomalyType, "time", time.Since(t0).Seconds())
}

func insertAnomaly(anomalies []Anomaly) error {

	if len(anomalies) == 0 {
		return nil
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return err
	}

	// Build parameterized query for bulk insert to prevent SQL injection
	query := `INSERT INTO anomaly (id, account_id, tenant, name, namespace, reference_value, current_value, anomaly_type, is_anomaly, evaluated_at, pod_name, training_end_time, insights) VALUES `

	// Build placeholders and collect values for parameterized query
	valuePlaceholders := make([]string, 0, len(anomalies))
	args := make([]interface{}, 0, len(anomalies)*13)

	for i, anomaly := range anomalies {
		err := GenerateAnomalyEvent(dbms, &anomaly)
		if err != nil {
			slog.Error("anomaly: failed to generate anomaly event", "error", err, "accountId", anomaly.AccountId, "tenant", anomaly.Tenant, "name", anomaly.Name, "namespace", anomaly.Namespace)
		}

		// Prepare values
		oldValueStr, _ := common.MarshalJson(anomaly.OldValue)

		var trainingEndTime interface{}
		if anomaly.TrainingEndTime != nil {
			trainingEndTime = anomaly.TrainingEndTime.Format(dateTimeFormat)
		} else {
			trainingEndTime = nil
		}

		// Default to empty JSON array if insights is nil or empty
		var insightsJSON interface{}
		if len(anomaly.Insights) > 0 && string(anomaly.Insights) != "[]" {
			insightsJSON = anomaly.Insights
		} else {
			insightsJSON = []byte("[]")
		}

		// Calculate parameter positions for this row
		offset := i * 13
		placeholders := fmt.Sprintf("($%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d::jsonb)",
			offset+1, offset+2, offset+3, offset+4, offset+5, offset+6, offset+7, offset+8, offset+9, offset+10, offset+11, offset+12, offset+13)
		valuePlaceholders = append(valuePlaceholders, placeholders)

		// Append values in the same order as placeholders
		args = append(args,
			anomaly.Id,
			anomaly.AccountId,
			anomaly.Tenant,
			anomaly.Name,
			anomaly.Namespace,
			string(oldValueStr),
			anomaly.CurrentValue,
			string(anomaly.AnomalyType),
			anomaly.IsAnomaly,
			anomaly.EvaluatedAt.Format(dateTimeFormat),
			anomaly.PodName,
			trainingEndTime,
			insightsJSON,
		)
	}

	// Combine query with placeholders
	query += strings.Join(valuePlaceholders, ", ")

	// Execute parameterized query
	_, err = dbms.Db.Exec(query, args...)
	if err != nil {
		return err
	}
	return nil
}

func ListAnomalyTemplate(context *security.RequestContext, request AnomalyListTemplateRequest) ([]AnomalyTemplate, error) {
	return readJSONFile()
}

func GenerateAnomalyEvent(dbms *database.DatabaseManager, anomaly *Anomaly) error {

	workloadKindMap := map[string]string{}

	rows, err := dbms.Db.Queryx(`select name,kind from k8s_workloads where cloud_account_id=$1 and name=$2 and namespace=$3`, anomaly.AccountId, anomaly.Name, anomaly.Namespace)
	if err != nil {
		slog.Error("anomaly: error getting workload kind", "error", err)
		return err
	}
	defer func() {
		err := rows.Close()
		if err != nil {
			slog.Error("anomaly: error closing rows", "error", err)
		}
	}()

	for rows.Next() {
		var name string
		var kind string
		err = rows.Scan(&name, &kind)
		if err != nil {
			slog.Error("anomaly: error getting workload kind", "error", err)
			return err
		}
		workloadKindMap[name] = kind
	}
	evidences, err := collectEvidences(anomaly, workloadKindMap[anomaly.Name])
	if err != nil {
		slog.Error("anomaly: error collecting evidences", "error", err, "account_id", anomaly.AccountId, "anomaly_id", anomaly.Id, "anomaly_type", anomaly.AnomalyType, "name", anomaly.Name, "namespace", anomaly.Namespace)
		evidences = []any{}
	}

	var accountName string
	query := `SELECT ca.account_name FROM cloud_accounts ca WHERE id = $1`
	err = dbms.Db.Get(&accountName, query, anomaly.AccountId)
	if err != nil {
		slog.Error("slo: error in fetching cloud account name", "error", err)
		return err
	}

	// FindingId is the dedup fingerprint, not anomaly.Id (a fresh UUID every
	// detection cycle) — otherwise InsertEvent's ON CONFLICT(tenant,
	// cloud_account_id, finding_id) upsert never matches a prior cycle's row,
	// and every cycle inserts a brand-new FIRING event instead of refreshing
	// the existing one. See closeAnomalyEventIfOpen for the corresponding close.
	fingerprint := anomalyFingerprint(anomaly.AccountId, anomaly.AnomalyType, anomaly.Name, anomaly.Namespace)
	eventObj := event.Event{
		AccountId:        anomaly.AccountId,
		Tenant:           anomaly.Tenant,
		Source:           "anomaly",
		Title:            fmt.Sprintf("%s Anomaly detected for %s in namespace %s", anomaly.AnomalyType, anomaly.Name, anomaly.Namespace),
		Failure:          "true",
		FindingType:      "Anomaly",
		Category:         "Anomaly",
		Priority:         "HIGH",
		SubjectName:      anomaly.Name,
		SubjectNamespace: anomaly.Namespace,
		Evidences:        evidences,
		FindingId:        fingerprint,
		AggregationKey:   "Anomaly",
		Description:      fmt.Sprintf("%s Anomaly detected for %s in namespace %s", anomaly.AnomalyType, anomaly.Name, anomaly.Namespace),
		SubjectType:      workloadKindMap[anomaly.Name],
		SubjectNode:      "",
		Status:           "FIRING",
		StartsAt:         anomaly.EvaluatedAt,
		Fingerprint:      fingerprint,
		Cluster:          accountName,
		Labels:           metricStatLabels(anomaly),
	}
	err = upsertAnomalyEvent(dbms, eventObj)
	if err != nil {
		slog.Error("anomaly: error inserting event", "error", err, "account_id", anomaly.AccountId, "anomaly_id", anomaly.Id, "anomaly_type", anomaly.AnomalyType, "name", anomaly.Name, "namespace", anomaly.Namespace)
	}
	return err
}

// upsertAnomalyEvent stores the anomaly's event. With
// FeatureAnomalyReturnedDigestEnabled on it also announces an anomaly that had
// cleared and fired again (issue #40007): InsertEvent's upsert reopens the
// stored row but, for a row that already exists, runs no notification, and it
// cannot tell a return from the hourly re-detection of an anomaly that is still
// open. The stored status, read before the upsert overwrites it, can.
func upsertAnomalyEvent(dbms *database.DatabaseManager, eventObj event.Event) error {
	returnedEventId := ""
	if config.Config.FeatureAnomalyReturnedDigestEnabled {
		returnedEventId = clearedAnomalyEventId(dbms, eventObj)
	}

	if _, err := event.InsertEvent(eventObj, ""); err != nil {
		return err
	}

	if returnedEventId != "" {
		// phase is queue.EventPostProcessPhaseReturned, spelled out the way
		// InsertEvent spells out workflow_only: this package does not import the
		// consumer. workflow_only is for a consumer that predates phase: it
		// ignores the unknown field, and without workflow_only would run the
		// whole pipeline (triage, investigation, event.created) again.
		publishEventPostProcess(map[string]any{"event_id": returnedEventId, "workflow_only": true, "phase": "returned"})
	}
	return nil
}

// clearedAnomalyEventId returns the id of the event stored for this finding if
// it is RESOLVED or CLOSED, and "" otherwise: no row (a first firing), a FIRING
// row (re-detection of an open anomaly), or a failed lookup, which must not
// cost the event its upsert. The id comes from the row because InsertEvent
// returns the one it generated, which an update does not store.
func clearedAnomalyEventId(dbms *database.DatabaseManager, eventObj event.Event) string {
	var id, status string
	err := dbms.Db.QueryRowx(
		`SELECT id, status FROM events WHERE tenant = $1 AND cloud_account_id = $2 AND finding_id = $3`,
		eventObj.Tenant, eventObj.AccountId, eventObj.FindingId,
	).Scan(&id, &status)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			slog.Error("anomaly: failed to read stored anomaly event status", "error", err, "fingerprint", eventObj.FindingId)
		}
		return ""
	}
	if strings.EqualFold(status, string(event.EventStatusResolved)) || strings.EqualFold(status, string(event.EventStatusClosed)) {
		return id
	}
	return ""
}

// publishEventPostProcess sends one message to the event post-process queue. A
// variable so tests can read the message without a broker.
var publishEventPostProcess = func(message map[string]any) {
	_ = common.MqPublish(
		config.Config.RabbitMqEventPostProcessExchange,
		config.Config.RabbitMqEventPostProcessQueue,
		message,
		common.MqPublishWithExpiration(1*time.Hour),
		common.MqPublishWithBackgroundRetry(),
	)
}

func collectEvidences(anomaly *Anomaly, workloadKind string) ([]any, error) {
	// anomaly to map
	anomalyMap, err := common.MarshalStructToMap(anomaly)
	if err != nil {
		slog.Error("anomaly: unable to convert struct to map", "error", err)
		return nil, err
	}

	// start time is 30 min before the evaluated time
	startTime := anomaly.EvaluatedAt.Add(-60 * time.Minute)
	endTime := *anomaly.EvaluatedAt

	// Each downstream call (4 workload metrics + service map + traces) is an
	// independent relay HTTP round-trip. Fan them out via errgroup and write into
	// fixed slots so the final order matches the previous sequential ordering.
	// Concurrent writes to distinct slice indices are safe; no mutex needed.
	metricNames := []string{"memory", "cpu", "latency", "cpu_throttling"}
	slots := make([]any, 1+len(metricNames)+2) // anomaly + metrics + serviceMap + traces
	slots[0] = anomalyMap
	slotServiceMap := 1 + len(metricNames)
	slotTraces := slotServiceMap + 1

	var g errgroup.Group

	// Workload metrics (memory, cpu, latency, cpu_throttling), through the
	// metrics layer so the account's default metrics provider answers.
	metricsCtx := security.NewRequestContextForTenantAdmin(anomaly.Tenant, slog.Default(), nil, nil)
	for i, metricName := range metricNames {
		i, metricName := i, metricName
		g.Go(func() error {
			title := fmt.Sprintf("%s Metric", strings.ToTitle(strings.ReplaceAll(metricName, "_", " ")))
			res, err := observability.WorkloadMetricEvidence(metricsCtx, anomaly.AccountId, anomaly.Name, anomaly.Namespace, workloadKind, metricName, title, startTime, endTime)
			if err != nil {
				slog.Error("anomaly: error getting workload ", "error", err, "metric", metricName, "workload", anomaly.Name, "namespace", anomaly.Namespace)
				return nil
			}
			slots[1+i] = res
			return nil
		})
	}

	// Service map
	g.Go(func() error {
		healthCheckParams := map[string]any{
			"workload_filter": map[string]string{
				"workload_name":      anomaly.Name,
				"workload_namespace": anomaly.Namespace,
			},
			"r_start_time": startTime.UTC().Format("2006-01-02T15:04:05.000000Z"),
			"r_end_time":   endTime.UTC().Format("2006-01-02T15:04:05.000000Z"),
		}
		evidence, err := relay.PodActionExecutor(anomaly.AccountId, anomaly.Name, anomaly.Namespace, relay.ServiceMapActionName, healthCheckParams)
		if err != nil {
			slog.Error("anomaly: error getting service map at anomaly", "error", err)
			return nil
		}
		res, err := relay.FormatEvidenceResponseFromAgent("Service Map", evidence)
		if err != nil {
			slog.Error("anomaly: error formatting service map at anomaly evidence", "error", err)
			return nil
		}
		insight, err := common.CheckNeighboringWorkloadHealth(res, map[string]string{
			"WorkloadName": anomaly.Name,
			"Namespace":    anomaly.Namespace,
		})
		if err != nil {
			slog.Error("anomaly: error checking neighboring workload health at anomaly", "error", err)
		} else if len(insight) > 0 {
			res["insight"] = insight
		}
		res["type"] = relay.ServiceMapActionName
		res["start_time"] = startTime.UTC().Format("2006-01-02T15:04:05.000000Z")
		res["end_time"] = endTime.UTC().Format("2006-01-02T15:04:05.000000Z")
		res["workload_name"] = anomaly.Name
		res["workload_namespace"] = anomaly.Namespace
		slots[slotServiceMap] = res
		return nil
	})

	// Workload traces — only call whose relay-level failure aborts collectEvidences,
	// matching the original sequential behavior.
	g.Go(func() error {
		evidence, err := relay.PodActionExecutor(anomaly.AccountId, anomaly.Name, anomaly.Namespace, relay.WorkloadTracesEnricherActionName, map[string]any{
			"destination_workload_name":      anomaly.Name,
			"destination_workload_namespace": anomaly.Namespace,
			"duration_minutes":               60,
		})
		if err != nil {
			slog.Error("anomaly: error getting workload traces at anomaly", "error", err)
			return err
		}
		res, err := relay.FormatEvidenceResponseFromAgent("Workload Traces", evidence)
		if err != nil {
			slog.Error("anomaly: error formatting evidence response in anomaly at traces", "error", err)
			return nil
		}
		slots[slotTraces] = res
		return nil
	})

	if err := g.Wait(); err != nil {
		return nil, err
	}

	// Compact: drop slots that weren't filled (failed sub-calls).
	evidences := make([]any, 0, len(slots))
	for _, s := range slots {
		if s != nil {
			evidences = append(evidences, s)
		}
	}
	return evidences, nil
}
