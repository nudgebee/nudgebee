package slo

import (
	"fmt"
	"log/slog"
	"math"
	"nudgebee/services/common"
	"nudgebee/services/config"
	"nudgebee/services/event"
	"nudgebee/services/internal/database"
	"nudgebee/services/relay"
	"nudgebee/services/security"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
)

func Execute() error {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return err
	}
	rows, err := dbms.Db.Queryx(`SELECT distinct s.cloud_account_id FROM public.slo_config s inner join agent a on s.cloud_account_id = a.cloud_account_id  where s.enabled = true and a.status = 'CONNECTED'`)
	if err != nil {
		return err
	}
	defer func() {
		err := rows.Close()
		if err != nil {
			slog.Error("slo: error closing rows", "error", err)
		}
	}()

	sloConfigs := make([]string, 0)
	for rows.Next() {
		accountIds := map[string]any{}
		err = rows.MapScan(accountIds)
		if err != nil {
			slog.Error("slo: error fetching slo config", "error", err)
			return err
		}
		id := fmt.Sprintf("%s", accountIds["cloud_account_id"])
		sloConfigs = append(sloConfigs, id)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("slo: error iterating account rows: %w", err)
	}
	for _, accountId := range sloConfigs {
		err := ExecuteSLO(accountId)
		if err != nil {
			slog.Error("slo: error running slo config", "error", err, "cloud account id", accountId)
		}
	}
	return nil
}
func ExecuteSLO(accountId string) error {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return err
	}

	rows, err := dbms.Db.Queryx(`SELECT id, name, description, window, goal, schedule, created_by, updated_by, method,
			histogram_query, filter_good_query, filter_bad_query, filter_valid_query, start_time,
			end_time, threshold, created_at, updated_at, cloud_account_id, tenant_id, enabled,
			workload_name, workload_namespace, workload_id
		FROM public.slo_config WHERE cloud_account_id=$1 AND enabled = true`, accountId)

	if err != nil {
		return err
	}
	defer func() {
		err := rows.Close()
		if err != nil {
			slog.Error("slo: error closing rows", "error", err)
		}
	}()

	sloConfigs := make([]DBSLOConfig, 0)
	for rows.Next() {
		config := DBSLOConfig{}
		err = rows.StructScan(&config)
		if err != nil {
			slog.Error("slo: error fetching slo config", "error", err, "accountId", accountId)
			return err
		}
		sloConfigs = append(sloConfigs, config)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("slo: error iterating config rows: %w", err)
	}
	for _, config := range sloConfigs {
		err := executeSLOConfig(config, accountId, dbms)
		if err != nil {
			slog.Error("slo: error running slo config", "error", err, "configId", config.Id, "accountId", accountId)
		}
	}
	return nil
}

func executeSLOConfig(config DBSLOConfig, accountId string, dbms *database.DatabaseManager) error {
	reports, err := executeSlo(config, accountId)
	if err != nil {
		slog.Error("slo: error executing slo", "error", err, "configId", config.Id, "accountId", accountId)
		return err
	}

	var accountName string
	query := `SELECT ca.account_name FROM cloud_accounts ca WHERE id = $1`
	err = dbms.Db.Get(&accountName, query, accountId)
	if err != nil {
		slog.Error("slo: error in fetching cloud account name", "error", err)
		return err
	}

	for _, report := range reports {
		status := "OK"
		if report.Alert {
			status = "FIRING"
		}
		tx, err := dbms.Db.Begin()
		if err != nil {
			slog.Error("slo: error starting transaction", "error", err, "configId", config.Id, "accountId", accountId)
			return err
		}
		timestamp := timestampToPostgresFormat(report.EndTime)
		query := `INSERT INTO slo_report (config_id, gap, error_budget_target, error_budget_measurement, error_budget_burn_rate,
				error_budget_burn_rate_threshold, error_budget_minutes, error_budget_remaining_minutes, error_minutes, error_budget_consumed_ratio, 
				status, bad_events_count, good_events_count, events_count, sli_measurement, tenant_id, cloud_account_id, workload_name, workload_namespace, timestamp) 
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20) 
				ON CONFLICT (tenant_id, cloud_account_id, config_id, workload_name, workload_namespace, timestamp) 
				DO UPDATE SET gap = EXCLUDED.gap, status = EXCLUDED.status, error_budget_burn_rate = EXCLUDED.error_budget_burn_rate`
		_, err = tx.Exec(query, config.Id, report.Gap, report.ErrorBudgetTarget, report.ErrorBudgetMeasurement, report.ErrorBudgetBurnRate, report.ErrorBudgetBurnRateThreshold,
			report.ErrorBudgetMinutes, report.ErrorBudgetRemainingMinutes, report.ErrorMinutes, report.ErrorBudgetConsumedRatio, status, report.BadEventsCount, report.GoodEventsCount, report.EventsCount,
			report.SLIMeasurement, config.TenantId, config.CloudAccountId, report.Workload, report.Namespace, timestamp)
		if err != nil {
			return err
		}
		err = tx.Commit()
		if err != nil {
			slog.Error("slo: error committing transaction", "error", err, "configId", config.Id, "accountId", accountId)
		}
		if report.Alert {
			err = triggerNotification(config, report, accountName)
			if err != nil {
				slog.Error("slo: error triggering notification", "error", err, "configId", config.Id, "accountId", accountId)
			}
			err = GenerateSLOEvent(dbms, config, accountName)
			if err != nil {
				slog.Error("slo: error generating event", "error", err, "configId", config.Id, "accountId", accountId)
			}
		}
	}
	return nil
}

func timestampToPostgresFormat(timestamp float64) string {
	// Convert timestamp to a time.Time object
	t := time.Unix(int64(timestamp), int64((timestamp-float64(int64(timestamp)))*1e9))
	// Round to nearest hour
	roundedTime := t.Round(time.Hour)
	// Format to PostgreSQL datetime format
	postgresFormat := roundedTime.Format("2006-01-02 15:04:05")

	return postgresFormat
}

func executeSlo(config DBSLOConfig, accountId string) ([]SLOReport, error) {
	sloConfig := SLOConfig{
		Name:            config.Name,
		Window:          config.Window,
		Goal:            config.Goal,
		Expression:      config.Expression,
		FilterGood:      config.FilterGood,
		FilterBad:       config.FilterBad,
		FilterValid:     config.FilterValid,
		Method:          config.Method,
		ThresholdBucket: config.ThresholdBucket / 1000,
	}
	resp, err := relay.Execute(relay.RelayExecuteRequest{
		NoSinks: true,
		Cache:   false,
		Body: relay.ActionExecuteBody{
			ActionName: "slo_generator",
			AccountID:  accountId,
			ActionParams: map[string]any{
				"slo_config": sloConfig,
			},
		},
	})
	if err != nil {
		slog.Error("slo: failed to execute slo task", "error", resp["response"], "accountId", accountId)
		return nil, err
	}
	if resp["status_code"] == 500 {
		slog.Error("slo: failed to execute slo task", "error", resp["response"], "accountId", accountId)
		return nil, fmt.Errorf("slo: failed to execute slo task error %s accountId %s ", resp["response"], accountId)
	}
	if resp["data"] == nil {
		return nil, nil
	}
	dataMap, ok := resp["data"].(map[string]any)
	if !ok || dataMap["data"] == nil {
		return nil, nil
	}
	reports := make([]SLOReport, 0)
	sloReports := dataMap["data"]
	for _, m := range sloReports.([]any) {
		jsonData, err := common.MarshalJson(m)
		if err != nil {
			continue
		}

		var sloReport SLOReport
		if err := common.UnmarshalJson(jsonData, &sloReport); err != nil {
			continue
		}
		reports = append(reports, sloReport)
	}
	return reports, nil
}

func CreateOrUpdateSLOConfig(context *security.RequestContext, sloConfigRequest SLORequest) (map[string]bool, error) {
	data := make(map[string]bool)
	if context.GetSecurityContext().GetUserId() == "" || context.GetSecurityContext().GetTenantId() == "" {
		return data, fmt.Errorf("unauthorized")
	}

	if !context.GetSecurityContext().HasAccountAccess(sloConfigRequest.AccountId, security.SecurityAccessTypeCreate) {
		return data, fmt.Errorf("unauthorized")
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return data, err
	}
	context.GetLogger().Info("CreateSLOConfig", "data", sloConfigRequest)
	for _, config := range sloConfigRequest.Config {
		filterGoodQuery := ""
		filterBadQuery := ""
		method := ""
		histogramQuery := ""
		window := 60 * 60
		if strings.ToLower(config.Name) == "latency" {
			histogramQuery = fmt.Sprintf("container_http_requests_duration_seconds_total_bucket{destination_workload_namespace=\"%s\", destination_workload_name=\"%s\", destination_workload_namespace!=\"external\"}", sloConfigRequest.Namespace, sloConfigRequest.WorkloadName)
			method = "distribution_cut"
		} else if strings.ToLower(config.Name) == "availability" {
			filterGoodQuery = fmt.Sprintf("container_http_requests_total{destination_workload_namespace=\"%s\",destination_workload_name=\"%s\" , status=~\"2..\"}", sloConfigRequest.Namespace, sloConfigRequest.WorkloadName)
			filterBadQuery = fmt.Sprintf("container_http_requests_total{destination_workload_namespace=\"%s\", destination_workload_name=\"%s\", status=~\"5..\"}", sloConfigRequest.Namespace, sloConfigRequest.WorkloadName)
			method = "good_bad_ratio"
		} else {
			return data, fmt.Errorf("invalid config provided %s", config.Name)
		}
		query := `INSERT INTO slo_config ("name", schedule, description, created_by, updated_by, filter_good_query, filter_bad_query, threshold, "method", histogram_query, cloud_account_id, tenant_id, "window", workload_name, workload_namespace, enabled, goal) 
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17) 
			ON CONFLICT (tenant_id, cloud_account_id, workload_name, workload_namespace, name) 
			DO UPDATE SET threshold = EXCLUDED.threshold, goal = EXCLUDED.goal`
		_, err = dbms.Db.Exec(query, config.Name, "", "", context.GetSecurityContext().GetUserId(), context.GetSecurityContext().GetUserId(), filterGoodQuery, filterBadQuery, config.Threshold, method, histogramQuery, sloConfigRequest.AccountId, context.GetSecurityContext().GetTenantId(), window, sloConfigRequest.WorkloadName, sloConfigRequest.Namespace, config.Enabled, config.Goal)
		if err != nil {
			return data, err
		}
		data["success"] = true
	}
	return data, nil
}

func GetSLOConfig(context *security.RequestContext, sloListRequest SLOListRequest) ([]SLOResponse, error) {
	if context.GetSecurityContext().GetUserId() == "" || context.GetSecurityContext().GetTenantId() == "" {
		return nil, fmt.Errorf("unauthorized")
	}

	if !context.GetSecurityContext().HasAccountAccess(sloListRequest.AccountId, security.SecurityAccessTypeRead) {
		return nil, fmt.Errorf("unauthorized")
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, err
	}

	whereClause, args, err := configListFiltersToWhereClause(context.GetSecurityContext().GetTenantId(), sloListRequest)
	if err != nil {
		return nil, err
	}

	query := fmt.Sprintf(`SELECT id, "name", description, schedule, created_by, updated_by, filter_good_query, filter_bad_query, threshold, created_at, updated_at, "method", histogram_query, cloud_account_id, tenant_id, "window", workload_name, workload_namespace, goal, enabled FROM public.slo_config where %s`, whereClause)
	// Use sqlx.In to handle slice arguments and prevent SQL injection
	query, args, err = sqlx.In(query, args...)
	if err != nil {
		return nil, err
	}
	query = dbms.Db.Rebind(query)

	rows, err := dbms.Db.Queryx(query, args...)

	if err != nil {
		return nil, err
	}
	defer func() {
		err := rows.Close()
		if err != nil {
			slog.Error("slo: error closing rows", "error", err)
		}
	}()

	sloConfigs := make([]DBSLOConfig, 0)
	for rows.Next() {
		config := DBSLOConfig{}
		err = rows.StructScan(&config)
		if err != nil {
			slog.Error("slo: error fetching slo config", "error", err, "accountId", sloListRequest.AccountId)
			return nil, err
		}
		sloConfigs = append(sloConfigs, config)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("slo: error iterating config rows: %w", err)
	}

	sloResponseMap := make(map[string]SLOResponse)
	for _, config := range sloConfigs {
		configKey := fmt.Sprintf("%s-%s-%s", config.Name, config.WorkloadName, config.Namespace)
		if response, ok := sloResponseMap[configKey]; ok {
			configResponse := SLOConfigResponse{Id: config.Id, Name: config.Name, Goal: config.Goal, Threshold: config.ThresholdBucket, Enabled: config.Enabled}
			response.Config = append(response.Config, configResponse)
			sloResponseMap[configKey] = response
		} else {
			response := SLOResponse{Id: config.Id, AccountId: config.CloudAccountId, WorkloadName: config.WorkloadName, Namespace: config.Namespace, Config: make([]SLOConfigResponse, 0)}
			configResponse := SLOConfigResponse{Id: config.Id, Name: config.Name, Goal: config.Goal, Threshold: config.ThresholdBucket, Enabled: config.Enabled}
			response.Config = append(response.Config, configResponse)
			sloResponseMap[configKey] = response
		}
	}
	values := make([]SLOResponse, 0, len(sloResponseMap))
	for _, v := range sloResponseMap {
		values = append(values, v)
	}
	return values, nil
}

func configListFiltersToWhereClause(tenantId string, filters SLOListRequest) (string, []interface{}, error) {
	conditions := make([]string, 0)
	args := make([]interface{}, 0)

	conditions = append(conditions, "tenant_id = ?")
	args = append(args, tenantId)

	if filters.AccountId != "" {
		conditions = append(conditions, "cloud_account_id = ?")
		args = append(args, filters.AccountId)
	}
	if len(filters.Namespace) > 0 {
		conditions = append(conditions, "workload_namespace in (?)")
		args = append(args, filters.Namespace)
	}
	if len(filters.WorkloadName) > 0 {
		conditions = append(conditions, "workload_name in (?)")
		args = append(args, filters.WorkloadName)
	}
	return strings.Join(conditions, " AND "), args, nil
}

func triggerNotification(sloConfig DBSLOConfig, sloReport SLOReport, accountName string) (err error) {
	slog.Info(fmt.Sprintf("Publishing slo notification - %s", sloConfig.Id))
	//gather previous slo report data and find out failing since
	previousReport, err := getPreviousSLOReport(sloConfig.Id, sloReport.Workload, sloReport.Namespace)
	if err != nil {
		slog.Error("slo: error getting previous slo report", "error", err)
		return err
	}

	failingSince := timestampToPostgresFormat(sloReport.EndTime)
	if previousReport.Id != "" {
		failingSince = previousReport.Timestamp.Format("2006-01-02 15:04:05")
	}

	currentGap := fmt.Sprintf("%.2f", sloReport.Gap)
	message := map[string]any{
		"kind":      "notification",
		"source":    "slo",
		"type":      "slo_alert",
		"tenant_id": sloConfig.TenantId,

		"parameters": map[string]any{
			"account_id":             sloConfig.CloudAccountId,
			"namespace":              sloReport.Namespace,
			"workload":               sloReport.Workload,
			"slo_name":               sloConfig.Name,
			"slo_type":               sloConfig.Name,
			"status":                 "FIRING",
			"slo_target":             sloConfig.Goal,
			"current_value":          currentGap,
			"firing_since":           failingSince,
			"burn_rate":              math.Round(sloReport.ErrorBudgetBurnRate),
			"error_budget_remaining": math.Round(sloReport.ErrorBudgetRemainingMinutes),
			"account_name":           accountName,
			"threshold":              sloConfig.ThresholdBucket,
			"bad_event_count":        sloReport.BadEventsCount,
			"good_event_count":       sloReport.GoodEventsCount,
		},
	}

	//log the message in json format
	messageJson, err := common.MarshalJson(message)
	if err != nil {
		slog.Error("slo: error marshalling message", "error", err)
		return err
	}
	slog.Info(fmt.Sprintf("SLO Notification - %s", string(messageJson)))

	err = common.MqPublish(config.Config.RabbitMqNotificationsExchange, config.Config.RabbitMqNotificationsQueue, message)
	if err != nil {
		slog.Error("slo: error publishing message to queue", "error", err)
		return err
	}

	return nil
}

func getPreviousSLOReport(configId string, workload string, namespace string) (DBSLOReport, error) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		slog.Error("slo: error getting database manager", "error", err)
		return DBSLOReport{}, err
	}

	rows, err := dbms.Db.Queryx(`SELECT id, config_id, gap, error_budget_target, error_budget_measurement, error_budget_burn_rate,
			error_budget_burn_rate_threshold, error_budget_minutes, error_budget_remaining_minutes,
			error_minutes, error_budget_consumed_ratio, status, bad_events_count, good_events_count,
			events_count, sli_measurement, tenant_id, cloud_account_id, workload_name,
			workload_namespace, timestamp, created_at, updated_at
		FROM slo_report
		WHERE config_id=$1 AND workload_name=$2 AND workload_namespace=$3 AND status='FIRING'
		ORDER BY timestamp DESC LIMIT 1`, configId, workload, namespace)

	if err != nil {
		slog.Error("slo: error in fetching previous slo report", "error", err)
		return DBSLOReport{}, err
	}
	defer func() {
		err := rows.Close()
		if err != nil {
			slog.Error("Failed to close rows", "error", err)
		}
	}()

	report := DBSLOReport{}
	for rows.Next() {
		err = rows.StructScan(&report)
		if err != nil {
			slog.Error("slo: error getting previous slo report", "error", err)
			return DBSLOReport{}, err
		}
	}
	if err := rows.Err(); err != nil {
		return DBSLOReport{}, fmt.Errorf("slo: error iterating previous report rows: %w", err)
	}

	// find out if there was any non firing status after firing status if yes then return none else return report
	if report.Id != "" {
		rows, err := dbms.Db.Queryx(`SELECT count(*) FROM slo_report where config_id=$1 and workload_name=$2 and workload_namespace=$3 and status!='FIRING' and timestamp > $4`, configId, workload, namespace, timestampToPostgresFormat(float64(report.Timestamp.Unix())))
		if err != nil {
			slog.Error("slo: error in fetching previous slo report", "error", err)
			return DBSLOReport{}, err
		}
		defer func() {
			err := rows.Close()
			if err != nil {
				slog.Error("Failed to close rows", "error", err)
			}
		}()
		count := 0
		if rows.Next() {
			err = rows.Scan(&count)
			if err != nil {
				slog.Error("slo: error getting previous slo report", "error", err)
				return DBSLOReport{}, err
			}
		}
		if err := rows.Err(); err != nil {
			return DBSLOReport{}, fmt.Errorf("slo: error iterating count rows: %w", err)
		}
		if count > 0 {
			return DBSLOReport{}, nil
		} else {
			return report, nil
		}
	}
	return DBSLOReport{}, nil
}

func GenerateSLOEvent(dbms *database.DatabaseManager, sloConfig DBSLOConfig, accountName string) error {
	slo := DBSLOReport{}
	rows, err := dbms.Db.Queryx(`SELECT id, config_id, gap, error_budget_target, error_budget_measurement, error_budget_burn_rate,
			error_budget_burn_rate_threshold, error_budget_minutes, error_budget_remaining_minutes,
			error_minutes, error_budget_consumed_ratio, status, bad_events_count, good_events_count,
			events_count, sli_measurement, tenant_id, cloud_account_id, workload_name,
			workload_namespace, timestamp, created_at, updated_at
		FROM slo_report
		WHERE config_id=$1 AND workload_name=$2 AND workload_namespace=$3 AND status='FIRING'
		ORDER BY timestamp DESC LIMIT 1`, sloConfig.Id, sloConfig.WorkloadName, sloConfig.Namespace)

	if err != nil {
		slog.Error("slo: error in fetching previous slo report", "error", err)
		return err
	}
	defer func() {
		err := rows.Close()
		if err != nil {
			slog.Error("Failed to close rows", "error", err)
		}
	}()
	for rows.Next() {
		err = rows.StructScan(&slo)
		if err != nil {
			slog.Error("slo: error getting previous slo report", "error", err)
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("slo: error iterating slo event rows: %w", err)
	}
	if slo.Id == "" {
		slog.Info("No SLO report found for config", "sloConfigId", sloConfig.Id, "accountName", accountName)
		return nil
	}
	evidences, err := collectEvidences(slo, sloConfig)
	if err != nil {
		slog.Error("slo: error collecting evidences", "error", err)
		evidences = []any{}
	}
	eventObj := event.Event{
		AccountId:        slo.CloudAccountId,
		Tenant:           slo.TenantId,
		Source:           "slo",
		Title:            fmt.Sprintf("%s SLO violation for %s in namespace %s", sloConfig.Name, slo.WorkloadName, slo.WorkloadNamespace),
		Failure:          "true",
		FindingType:      "SLO",
		Category:         "SLO",
		Priority:         "HIGH",
		SubjectName:      slo.WorkloadName,
		SubjectNamespace: slo.WorkloadNamespace,
		Evidences:        evidences,
		FindingId:        slo.Id,
		AggregationKey:   "SLOViolation",
		Description:      fmt.Sprintf("%s SLO violation for %s in namespace %s", sloConfig.Name, slo.WorkloadName, slo.WorkloadNamespace),
		SubjectType:      "deployment",
		SubjectNode:      "",
		Status:           "FIRING",
		StartsAt:         slo.Timestamp,
		Fingerprint:      fmt.Sprintf("slo-%s-%s-%s-%s", slo.CloudAccountId, sloConfig.Name, slo.WorkloadName, slo.WorkloadNamespace),
		Cluster:          accountName,
	}
	_, err = event.InsertEvent(eventObj, "")
	if err != nil {
		slog.Error("slo: error inserting event", "error", err)
		return err
	}
	return err
}

func collectEvidences(slo DBSLOReport, sloConfig DBSLOConfig) ([]any, error) {
	// 1 hour back time in utc
	startTime := time.Now().UTC().Add(-time.Hour)
	endTime := time.Now().UTC()
	evidences := []any{}

	logStep := func(step string, tStart time.Time) {
		slog.Info("Step completed", "step", step, "duration", time.Since(tStart).String())
	}
	// Step 1: Add SLO report/config as evidence
	t0 := time.Now()
	sloMap, err := common.MarshalStructToMap(slo)
	if err != nil {
		return nil, fmt.Errorf("slo: convert SLOReport to map: %w", err)
	}
	sloConfigMap, err := common.MarshalStructToMap(sloConfig)
	if err != nil {
		return nil, fmt.Errorf("slo: convert SLOConfig to map: %w", err)
	}
	evidences = append(evidences, map[string]any{
		"name":      "SLO",
		"SLOReport": sloMap,
		"SLOConfig": sloConfigMap,
	})
	logStep("add SLO report/config evidence", t0)

	// Step 2: Collect workload metrics (memory, cpu, latency, cpu_throttling)
	for _, metricName := range []string{"memory", "cpu", "latency", "cpu_throttling"} {
		t := time.Now()
		ev, err := relay.WorkloadMetricsExecutor(
			sloConfig.CloudAccountId,
			sloConfig.WorkloadName,
			sloConfig.Namespace,
			metricName,
			startTime,
			endTime,
		)
		if err != nil {
			slog.Error("slo: error getting workload ", "error", err, "metric", metricName, "workload", sloConfig.WorkloadName, "namespace", sloConfig.Namespace)
			continue
		}
		res, err := relay.FormatEvidenceResponseFromAgent(fmt.Sprintf("%s Metric", strings.ToTitle(strings.ReplaceAll(metricName, "_", " "))), ev)
		if err != nil {
			slog.Error("slo: error formatting evidence response", "metric", metricName, "error", err)
			continue
		}
		evidences = append(evidences, res)
		logStep(fmt.Sprintf("collect workload %s", metricName), t)
	}

	// Step 3: Collect workload traces
	t1 := time.Now()
	traceParams := map[string]any{
		"destination_workload_name":      sloConfig.WorkloadName,
		"destination_workload_namespace": sloConfig.Namespace,
		"duration_minutes":               sloConfig.Window / 60 * 2,
	}
	switch sloConfig.Name {
	case "latency":
		traceParams["order_by"] = "Duration desc"
	case "availability":
		traceParams["status_code"] = []string{"STATUS_CODE_ERROR"}
	}
	evTrace, err := relay.PodActionExecutor(
		sloConfig.CloudAccountId,
		sloConfig.WorkloadName,
		sloConfig.Namespace,
		relay.WorkloadTracesEnricherActionName,
		traceParams,
	)
	if err != nil {
		slog.Error("slo: error getting workload traces", "error", err)
	} else {
		resTrace, err := relay.FormatEvidenceResponseFromAgent("Workload Traces", evTrace)
		if err != nil {
			slog.Error("slo: error formatting workload trace evidence", "error", err)
		} else {
			insight, err := CheckTracesEvidence(resTrace, sloConfig)
			if err != nil {
				slog.Error("slo: error checking traces evidence", "error", err)
			} else if len(insight) > 0 {
				resTrace["insight"] = insight
			}
			evidences = append(evidences, resTrace)
			logStep("collect workload traces & latency insight", t1)
		}
	}

	// Step 4: Check neighboring workload health via service‐map
	t2 := time.Now()
	healthParams := map[string]interface{}{
		"workload_filter": map[string]string{
			"workload_name":      sloConfig.WorkloadName,
			"workload_namespace": sloConfig.Namespace,
		},
		"r_start_time": startTime.Format("2006-01-02T15:04:05.000000Z"),
		"r_end_time":   endTime.Format("2006-01-02T15:04:05.000000Z"),
	}
	evHealth, err := relay.PodActionExecutor(
		sloConfig.CloudAccountId,
		sloConfig.WorkloadName,
		sloConfig.Namespace,
		relay.ServiceMapActionName,
		healthParams,
	)
	if err != nil {
		slog.Error("slo: error getting neighboring workload health", "error", err)
	} else {
		resHealth, err := relay.FormatEvidenceResponseFromAgent("Service Map", evHealth)
		if err != nil {
			slog.Error("slo: error formatting health evidence", "error", err)
		} else {
			insight, err := common.CheckNeighboringWorkloadHealth(resHealth, map[string]string{
				"WorkloadName": sloConfig.WorkloadName,
				"Namespace":    sloConfig.Namespace,
			})
			if err != nil {
				slog.Error("slo: error checking neighboring workload health", "error", err)
			} else if len(insight) > 0 {
				resHealth["insight"] = insight
			}
			// enrich result with context
			resHealth["type"] = relay.ServiceMapActionName
			resHealth["start_time"] = startTime.Format(time.RFC3339Nano)
			resHealth["end_time"] = endTime.Format(time.RFC3339Nano)
			resHealth["workload_name"] = sloConfig.WorkloadName
			resHealth["workload_namespace"] = sloConfig.Namespace

			evidences = append(evidences, resHealth)
			logStep("collect neighboring workload health", t2)
		}
	}

	return evidences, nil
}

func CheckTracesEvidence(evidence map[string]any, sloConfig DBSLOConfig) ([]map[string]string, error) {
	// Step 1: Extract data map
	raw := evidence["data"].(string)
	dataMap := make(map[string]any)
	if err := common.UnmarshalJson([]byte(raw), &dataMap); err != nil {
		return nil, fmt.Errorf("slo: failed to unmarshal data: %v", err)
	}

	rawData, ok := dataMap["data"].([]any)
	if !ok {
		return nil, fmt.Errorf("slo: failed to fetch metrics from server: response data is empty or not an array")
	}

	traces := make([]map[string]any, 0, len(rawData))
	for _, item := range rawData {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("slo: invalid item in data array: not a map")
		}
		traces = append(traces, m)
	}
	insights := make([]map[string]string, 0)
	if sloConfig.Name == "latency" {

		// Step 4: Filter
		invalid := FilterLongTracesByWorkload(traces, sloConfig.ThresholdBucket, sloConfig.WorkloadName, sloConfig.Namespace)
		if len(invalid) == 0 {
			return nil, nil
		}

		// Step 5: Return insight
		insight := map[string]string{
			"message":  fmt.Sprintf("Found %d traces breaking the threshold of %.2f miliseconds", len(invalid), sloConfig.ThresholdBucket),
			"severity": "Critical",
		}
		insights = append(insights, insight)
	} else {
		// Step 4: Filter
		invalid := FilterErrorTracesByWorkload(traces, sloConfig.WorkloadName, sloConfig.Namespace)
		if len(invalid) == 0 {
			return nil, nil
		}

		// Step 5: Return insight
		insight := map[string]string{
			"message":  fmt.Sprintf("Found %d traces with error status", len(invalid)),
			"severity": "Critical",
		}
		insights = append(insights, insight)
	}
	return insights, nil
}

func FilterLongTracesByWorkload(traces []map[string]any, threshold float64, workloadName string, workloadNamespace string) []map[string]any {
	result := make([]map[string]any, 0)

	for _, trace := range traces {
		// Type assertion for duration_ns
		duration, ok := trace["Duration"].(float64)
		if !ok {
			continue // skip if duration is not a float64
		}
		// convert nanoseconds to seconds
		duration /= 1e+9
		if trace["workload_name"] == workloadName && trace["workload_namespace"] == workloadNamespace && duration > (threshold) {
			result = append(result, trace)
		}
	}

	return result
}

func FilterErrorTracesByWorkload(traces []map[string]any, workloadName string, workloadNamespace string) []map[string]any {
	result := make([]map[string]any, 0)

	for _, trace := range traces {
		// Type assertion for status code
		status_code, ok := trace["status_code"].(string)
		if !ok {
			continue // skip if duration is not a float64
		}

		if trace["workload_name"] == workloadName && trace["workload_namespace"] == workloadNamespace && status_code == "STATUS_CODE_ERROR" {
			result = append(result, trace)
		}
	}

	return result
}
