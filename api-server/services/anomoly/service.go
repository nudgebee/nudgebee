package anomoly

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand"
	"nudgebee/services/application"
	"nudgebee/services/common"
	"nudgebee/services/config"
	"nudgebee/services/event"
	"nudgebee/services/internal/database"
	"nudgebee/services/ml"
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
		func(data []byte) error {
			request := AnomalyProcessingMessage{}
			err := common.UnmarshalJson(data, &request)
			if err != nil {
				slog.Error("anomaly: unable to unmarshal message", "error", err, "message", string(data))
				return nil
			}
			ctx := security.NewRequestContextForTenantAdmin(request.TenantId, slog.Default(), nil, nil)

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
	rows, err := dbms.Db.Queryx(`select ca.id as cloud_account_id , ca.tenant as tenant from cloud_accounts ca inner join agent a on ca.id = a.cloud_account_id where ca.cloud_provider = 'K8s' and a.status = 'CONNECTED' group by ca.tenant, ca.id`)
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

	// Validate account exists, is K8s type, and has connected agent
	var tenantId string
	err = dbms.Db.Get(&tenantId, `
		SELECT ca.tenant
		FROM cloud_accounts ca
		INNER JOIN agent a ON ca.id = a.cloud_account_id
		WHERE ca.id = $1 AND ca.cloud_provider = 'K8s' AND a.status = 'CONNECTED'
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
			"anomalyType", item.AnomalyConfig.AnomalyType, "provider", item.AnomalyConfig.AnomalyProvider)

		var processingErr error
		if item.AnomalyConfig.AnomalyProvider == "prometheus" {
			processingErr = processSingleApplicationPrometheus(ctx, item.AnomalyConfig, item.TenantID, item.AccountID, item.Application)
		} else {
			var existingCount int
			if typeCounts, ok := existingAnomalyCounts[item.AccountID]; ok {
				existingCount = typeCounts[item.AnomalyConfig.AnomalyType]
			}
			processingErr = processSingleApplicationMlAsync(ctx, item.AnomalyConfig, item.TenantID, item.AccountID, item.Application, existingCount)
		}

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
	}, common.MqPublishWithExpiration(time.Hour*1))

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
		ctx.GetLogger().Info("anomaly: no anomalies found", "account", app.AccountId)
		return
	}

	// Process each anomaly
	for _, mlAnomaly := range mlAnomalies {
		if !mlAnomaly.HasAnomaly {
			continue
		}
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
	ctx.GetLogger().Info("anomaly: processing anomaly", "account", app.AccountId, "tenant", app.TenantId, "app", app.ApplicationName, "namespace", app.ApplicationNamespace, "type", app.AnomalyType, "time", time.Since(t0).Seconds())
}

func processSingleApplicationPrometheus(
	ctx *security.RequestContext,
	anomalyCfg AnomalyTemplate,
	tenantId string,
	accountId string,
	app application.Application,
) error {
	slog.Debug("anomaly: processing prometheus config for single application",
		"accountId", accountId, "tenantId", tenantId, "app", app.Name, "namespace", app.K8sNamespace, "anomalyType", anomalyCfg.AnomalyType)

	queryName, promQuery := getQuery(anomalyCfg)
	if promQuery == "" {
		slog.Warn("anomaly: Prometheus query not found for anomaly type", "type", anomalyCfg.AnomalyType, "app", app.Name)
		return nil
	}

	referencePeriods := []int{1, 7, 14}
	endTime := time.Now().UTC()

	// 1. Get current data for the specific application
	currentDataStartTime := endTime.Add(-time.Hour) // Example: last hour
	rCurrentStartTime := currentDataStartTime.Format("2006-01-02T15:04:05.000000Z")
	rCurrentEndTime := endTime.Format("2006-01-02T15:04:05.000000Z")

	applicationsFilterForCurrent := map[string]any{
		app.Name: map[string]any{"namespace": app.K8sNamespace},
	}
	currentAppStatsResponses, err := queryRelayServer(accountId, rCurrentStartTime, rCurrentEndTime, promQuery, queryName, applicationsFilterForCurrent)
	if err != nil {
		slog.Error("anomaly: failed to query current relay server data for app", "error", err, "accountId", accountId, "app", app.Name)
		return err
	}

	if len(currentAppStatsResponses) == 0 {
		slog.Info("anomaly: no current data from relay for app", "accountId", accountId, "app", app.Name, "queryName", queryName)
		return nil
	}
	currentAppMetricValue, foundCurrent := currentAppStatsResponses[0].OtherMetrics[queryName]
	if !foundCurrent {
		slog.Info("anomaly: current metric not found in relay response for app", "metric", queryName, "app", app.Name)
		return nil
	}

	// Fan the per-period relay fetches out concurrently — each period is an independent
	// network round-trip to the agent (no shared state), so running them in parallel
	// cuts wall time from sum(latencies) to ~max(latencies) per app.
	var g errgroup.Group
	for _, period := range referencePeriods {
		period := period
		g.Go(func() error {
			historicalDataStartTime := endTime.AddDate(0, 0, -period)
			rHistStartTime := historicalDataStartTime.Format("2006-01-02T15:04:05.000000Z")
			rHistEndTime := endTime.Format("2006-01-02T15:04:05.000000Z") // Using 'now' as end for historical window

			applicationsFilterForHist := map[string]any{
				app.Name: map[string]any{"namespace": app.K8sNamespace},
			}
			historicalAppStatsResponses, err := queryRelayServer(accountId, rHistStartTime, rHistEndTime, promQuery, queryName, applicationsFilterForHist)
			if err != nil {
				// Match prior behavior: skip this period on relay error, don't fail the whole app.
				slog.Error("anomaly: failed to query historical relay server data for app", "error", err, "accountId", accountId, "app", app.Name, "period", period)
				return nil
			}

			if len(historicalAppStatsResponses) == 0 {
				slog.Info("anomaly: no historical data from relay for app", "accountId", accountId, "app", app.Name, "period", period, "queryName", queryName)
				return nil
			}
			historicalAppMetricValue, foundHist := historicalAppStatsResponses[0].OtherMetrics[queryName]
			if !foundHist || historicalAppMetricValue == 0 {
				slog.Info("anomaly: historical metric not found or is zero for app", "metric", queryName, "app", app.Name, "period", period)
				return nil
			}

			calculatedChangePerc := ((currentAppMetricValue - historicalAppMetricValue) / historicalAppMetricValue) * 100.0

			isAnomaly := false
			switch anomalyCfg.ChangeOperator {
			case "GT":
				isAnomaly = currentAppMetricValue > historicalAppMetricValue && calculatedChangePerc > anomalyCfg.BufferPercenatge
			case "LT":
				isAnomaly = currentAppMetricValue < historicalAppMetricValue && (-calculatedChangePerc) > anomalyCfg.BufferPercenatge
			case "GTE":
				isAnomaly = currentAppMetricValue >= historicalAppMetricValue && calculatedChangePerc >= anomalyCfg.BufferPercenatge
			case "LTE":
				isAnomaly = currentAppMetricValue <= historicalAppMetricValue && (-calculatedChangePerc) >= anomalyCfg.BufferPercenatge
			default:
				slog.Warn("anomaly: unknown change operator", "operator", anomalyCfg.ChangeOperator, "app", app.Name)
			}

			if !isAnomaly {
				return nil
			}

			slog.Info("anomaly: Prometheus-based anomaly detected",
				"accountId", accountId, "app", app.Name, "namespace", app.K8sNamespace, "type", anomalyCfg.AnomalyType,
				"current", currentAppMetricValue, "historical", historicalAppMetricValue, "period", period)

			refValueMap := map[string]any{
				fmt.Sprintf("historical_%d_days_value", period): historicalAppMetricValue,
				"query_name": queryName,
			}
			evalTime := time.Now().UTC()
			anomalyToInsert := Anomaly{
				Id:           common.GenerateUUID(),
				AccountId:    accountId,
				Tenant:       tenantId,
				Name:         app.Name,
				Namespace:    app.K8sNamespace,
				OldValue:     refValueMap,
				CurrentValue: currentAppMetricValue,
				AnomalyType:  anomalyCfg.AnomalyType,
				IsAnomaly:    true,
				EvaluatedAt:  &evalTime,
			}
			if err := insertAnomaly([]Anomaly{anomalyToInsert}); err != nil {
				slog.Error("anomaly: failed to insert prometheus-based anomaly", "error", err, "accountId", accountId, "app", app.Name, "period", period)
				return err
			}
			return nil
		})
	}

	return g.Wait()
}

func queryRelayServer(accountId string, rStartTime string, rEndTime string, query string, queryName string, applicationsFilter map[string]any) ([]relay.ApplicationStatsResponse, error) {
	request :=
		relay.ActionExecuteBody{
			AccountID:  accountId,
			ActionName: "application_stats",
			ActionParams: map[string]any{
				"r_start_time": rStartTime,
				"r_end_time":   rEndTime,
				"queries": map[string]any{
					queryName: query,
				},
				"applications": applicationsFilter, // Use the provided filter
			},
		}

	resp, err := relay.Execute(relay.RelayExecuteRequest{
		NoSinks: true,
		Cache:   false,
		Body:    request,
	})

	if err != nil {
		slog.Error("Failed to execute relay task", "error", err, "accountId", accountId, "response_detail", resp["response"])
		return nil, err
	}

	if resp["status_code"] == 500 {
		slog.Error("Relay task returned 500", "error_response", resp["response"], "accountId", accountId)
		return nil, fmt.Errorf("anomaly: failed to execute relay task error %s accountId %s ", resp["response"], accountId)
	}

	reports := make([]relay.ApplicationStatsResponse, 0)
	sloReports := resp["data"].(map[string]any)["data"]
	for _, m := range sloReports.([]any) {
		jsonData, err := common.MarshalJson(m)
		if err != nil {
			slog.Warn("Failed to marshal relay report item to JSON", "error", err)
			continue
		}

		var sloReport relay.ApplicationStatsResponse
		if err := common.UnmarshalJson(jsonData, &sloReport); err != nil {
			slog.Warn("Failed to unmarshal relay report item from JSON", "error", err)
			continue
		}
		reports = append(reports, sloReport)
	}
	return reports, nil
}

func getQuery(config AnomalyTemplate) (string, string) {
	if config.AnomalyType == MetricAnomolyTypeCPU {
		return "max", "quantile(0.99, rate(container_cpu_usage_seconds_total{container!=\"\"}[5m])) by (pod, namespace, container) / quantile(0.99, kube_pod_container_resource_limits{resource=\"cpu\", container!=\"\"}) by (pod, namespace, container)"
	}
	if config.AnomalyType == MetricAnomolyTypeMemory {
		return "max", "quantile(0.99, container_memory_usage_bytes{container!=\"\"}) by (pod, namespace, container) / quantile(0.99, kube_pod_container_resource_limits{resource=\"memory\", container!=\"\"}) by (pod, namespace, container)"
	}
	if config.AnomalyType == MetricAnomolyTypeLatency {
		return "max", "histogram_quantile(0.99, sum(rate(container_http_requests_duration_seconds_total_bucket{destination_workload_name!~\"(kubernetes)\", destination_workload_namespace!~\"(external|node)\", destination_workload_name!=\"\"}[5m])) by (le, destination_workload_name, destination_workload_namespace))"
	}
	if config.AnomalyType == MetricAnomolyTypeErrorRate {
		return "max", "(sum by (destination_workload_name, destination_workload_namespace)( increase(container_http_requests_total{destination_workload_name!~\"(kubernetes)\", destination_workload_namespace!~\"(external|node)\", status=~\"5..|4..\"}[5m]) ) / sum by (destination_workload_name, destination_workload_namespace)( increase(container_http_requests_total{destination_workload_name!~\"(kubernetes)\", destination_workload_namespace!~\"(external|node)\"}[5m]) ) )"
	}
	if config.AnomalyType == MetricAnomolyTypeNetwork {
		return "max", "sum(rate(container_network_receive_bytes_total{}[5m])) by (pod, namespace, container) + sum(rate (container_network_transmit_bytes_total{}[5m])) by (pod, namespace, container)"
	}
	return "", ""
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
	evidences, err := collectEvidences(anomaly)
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
		FindingId:        anomaly.Id,
		AggregationKey:   "Anomaly",
		Description:      fmt.Sprintf("%s Anomaly detected for %s in namespace %s", anomaly.AnomalyType, anomaly.Name, anomaly.Namespace),
		SubjectType:      workloadKindMap[anomaly.Name],
		SubjectNode:      "",
		Status:           "FIRING",
		StartsAt:         anomaly.EvaluatedAt,
		Fingerprint:      fmt.Sprintf("anomaly-%s-%s-%s-%s", anomaly.AccountId, anomaly.AnomalyType, anomaly.Name, anomaly.Namespace),
		Cluster:          accountName,
	}
	_, err = event.InsertEvent(eventObj, "")
	if err != nil {
		slog.Error("anomaly: error inserting event", "error", err, "account_id", anomaly.AccountId, "anomaly_id", anomaly.Id, "anomaly_type", anomaly.AnomalyType, "name", anomaly.Name, "namespace", anomaly.Namespace)
	}
	return err
}

func collectEvidences(anomaly *Anomaly) ([]any, error) {
	evidences := make([]any, 5)
	// anomaly to map
	anomalyMap, err := common.MarshalStructToMap(anomaly)
	if err != nil {
		slog.Error("anomaly: unable to convert struct to map", "error", err)
		return nil, err
	}
	evidences = append(evidences, anomalyMap)

	// start time is 30 min before the evaluated time
	startTime := anomaly.EvaluatedAt.Add(-60 * time.Minute)
	endTime := *anomaly.EvaluatedAt

	// Step 2: Collect workload metrics (memory, cpu, latency, cpu_throttling)
	for _, metricName := range []string{"memory", "cpu", "latency", "cpu_throttling"} {
		ev, err := relay.WorkloadMetricsExecutor(
			anomaly.AccountId,
			anomaly.Name,
			anomaly.Namespace,
			metricName,
			startTime,
			endTime,
		)
		if err != nil {
			slog.Error("anomaly: error getting workload ", "error", err, "metric", metricName, "workload", anomaly.Name, "namespace", anomaly.Namespace)
			continue
		}
		res, err := relay.FormatEvidenceResponseFromAgent(fmt.Sprintf("%s Metric", strings.ToTitle(strings.ReplaceAll(metricName, "_", " "))), ev)
		if err != nil {
			slog.Error("anomaly: error formatting evidence response", "metric", metricName, "error", err)
			continue
		}
		evidences = append(evidences, res)
	}

	// get service map
	healthCheckParams := map[string]any{
		"workload_filter": map[string]string{
			"workload_name":      anomaly.Name,
			"workload_namespace": anomaly.Namespace,
		},
		"r_start_time": startTime.UTC().Format("2006-01-02T15:04:05.000000Z"),
		"r_end_time":   endTime.UTC().Format("2006-01-02T15:04:05.000000Z"),
	}
	evidence, err := relay.PodActionExecutor(anomaly.AccountId, anomaly.Name, anomaly.Namespace, relay.ServiceMapActionName, healthCheckParams)
	if err == nil {
		res, err := relay.FormatEvidenceResponseFromAgent("Service Map", evidence)
		if err != nil {
			slog.Error("anomaly: error formatting service map at anomaly evidence", "error", err)
		} else {
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
			evidences = append(evidences, res)
		}
	} else {
		slog.Error("anomaly: error getting service map at anomaly", "error", err)
	}

	// get workload traces
	evidence, err = relay.PodActionExecutor(anomaly.AccountId, anomaly.Name, anomaly.Namespace, relay.WorkloadTracesEnricherActionName, map[string]any{
		"destination_workload_name":      anomaly.Name,
		"destination_workload_namespace": anomaly.Namespace,
		"duration_minutes":               60,
	})
	if err == nil {
		res, err := relay.FormatEvidenceResponseFromAgent("Workload Traces", evidence)
		if err != nil {
			slog.Error("anomaly: error formatting evidence response in anomaly at traces", "error", err)
		} else {
			evidences = append(evidences, res)
		}
	} else {
		slog.Error("anomaly: error getting workload traces at anomaly", "error", err)
		return nil, err
	}
	return evidences, nil
}
