package reports

import (
	"database/sql"
	"errors"
	"log/slog"
	"math/big"
	"nudgebee/services/account"
	"nudgebee/services/common"
	"nudgebee/services/config"
	"nudgebee/services/internal/database"
	"nudgebee/services/internal/database/models"
	"nudgebee/services/query"
	"nudgebee/services/security"
	"nudgebee/services/tenant"
	"nudgebee/services/user"
	"sort"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	"github.com/samber/lo"
	"golang.org/x/sync/errgroup"
)

func getEmailsFromUsers(users []models.User) []string {
	emails := lo.Map(users, func(u models.User, i int) string {
		return u.Username
	})
	return lo.Uniq(emails)
}

func prepareEmailMessage(emails []string, totalPotentialSavings float64, totalOpportunityLost float64, dayMinus1Highlights any, dayMinus2Highlights any, insights any, tenant models.Tenant) map[string]interface{} {
	today := time.Now().Format("2 January 2006")
	return map[string]interface{}{
		"kind":      "email",
		"email":     emails,
		"source":    "daily_recap",
		"tenant_id": tenant.Id,
		"type":      "daily_highlight_report",
		"subject":   config.Config.BrandingName + " Daily Insights - " + today,
		"parameters": map[string]interface{}{
			"title":                   "Daily highlight report",
			"total_potential_savings": totalPotentialSavings,
			"total_opportunity_lost":  totalOpportunityLost,
			"highlight1":              dayMinus1Highlights,
			"highlight2":              dayMinus2Highlights,
			"insights":                insights,
			"organization_name":       tenant.Name,
			"base_url":                config.Config.BaseUrl,
			"links": map[string]interface{}{
				"base_url": config.Config.BaseUrl,
			},
		},
	}
}

func prepareCombinedHighlightQuery(tenant, startDate, endDate string, accountIds []string) query.QueryRequest {
	whereClauses := []query.QueryWhereClause{
		{Binary: query.BinaryWhereClause{"tenant_id": map[query.BinaryWhereClauseType]any{query.Eq: tenant}}},
		{Binary: query.BinaryWhereClause{"created_at": map[query.BinaryWhereClauseType]any{query.Lte: any(endDate)}}},
		{Binary: query.BinaryWhereClause{"created_at": map[query.BinaryWhereClauseType]any{query.Gt: any(startDate)}}},
		{Binary: query.BinaryWhereClause{"subject_type": map[query.BinaryWhereClauseType]any{query.In: []string{"node", "pod", "HighErrorCriticalLogs"}}}},
	}

	if len(accountIds) > 0 {
		whereClauses = append(whereClauses, query.QueryWhereClause{
			Binary: query.BinaryWhereClause{"account_id": map[query.BinaryWhereClauseType]any{query.In: accountIds}},
		})
	}

	return query.QueryRequest{
		Table: "event_groupings_v2",
		Where: query.QueryWhereClause{
			And: whereClauses,
		},
		Columns: []query.QueryColumn{
			{Name: "event_count", Expr: "distinct", Args: []string{"fingerprint"}},
			{Name: "account_id"},
			{Name: "subject_type"},
		},
		GroupBy: []string{"account_id", "subject_type"},
	}
}

func prepareRecommendationQuery(tenant string, accountIds []string) query.QueryRequest {
	whereClauses := []query.QueryWhereClause{
		{Binary: query.BinaryWhereClause{"tenant_id": map[query.BinaryWhereClauseType]any{query.Eq: tenant}}},
		{Binary: query.BinaryWhereClause{"status": map[query.BinaryWhereClauseType]any{query.In: []string{"Open", "InProgress"}}}},
		{Binary: query.BinaryWhereClause{"rule_name": map[query.BinaryWhereClauseType]any{query.In: []string{"pod_right_sizing", "unused_pvc",
			"pv_rightsize", "replica-rightsizing", "abandoned-resources", "eks_cluster_upgrade"}}}},
	}

	if len(accountIds) > 0 {
		whereClauses = append(whereClauses, query.QueryWhereClause{
			Binary: query.BinaryWhereClause{"account_id": map[query.BinaryWhereClauseType]any{query.In: accountIds}},
		})
	}

	return query.QueryRequest{
		Table: "recommendation_groupings_v2",
		Where: query.QueryWhereClause{
			And: whereClauses,
		},
		Columns: []query.QueryColumn{
			{Name: "category"},
			{Name: "rule_name"},
			{Name: "count"},
			{Name: "account_id"},
			{Name: "sum_estimated_savings"},
		},
		GroupBy: []string{"category", "rule_name"},
	}
}

var optimizationRuleNames = []string{"pod_right_sizing", "unused_pvc", "pv_rightsize", "replica-rightsizing", "abandoned-resources", "eks_cluster_upgrade"}

// calculateOpportunityLost calculates the total opportunity lost by not applying recommendations
// over the last 30 days. Formula: sum(estimated_savings * min(days_open, 30) / 30)
func calculateOpportunityLost(context *security.RequestContext, tenantId string, accountIds []string) (float64, error) {
	dbManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return 0, err
	}

	// Calculate opportunity lost using DISTINCT ON to pick the primary recommendation
	// per (resource_id, category), avoiding double-counting
	const opportunityLostQuery = `
		SELECT COALESCE(SUM(
			estimated_savings * LEAST(EXTRACT(EPOCH FROM (NOW() - created_at)) / 86400.0, 30) / 30.0
		), 0) as opportunity_lost
		FROM (
			SELECT DISTINCT ON (r.resource_id, r.category)
				r.estimated_savings, r.created_at
			FROM recommendation r
			INNER JOIN cloud_accounts ca ON ca.id = r.cloud_account_id
			WHERE r.tenant_id = $1
			  AND r.status IN ('Open', 'InProgress')
			  AND r.rule_name = ANY($2)
			  AND (CARDINALITY($3::uuid[]) = 0 OR r.cloud_account_id = ANY($3))
			  AND ca.status = 'active'
			  AND r.estimated_savings > 0
			ORDER BY r.resource_id, r.category, r.estimated_savings DESC, r.updated_at DESC, r.id
		) ranked
	`

	var opportunityLost float64
	err = dbManager.QueryRowAndScan(&opportunityLost, opportunityLostQuery, tenantId, pq.Array(optimizationRuleNames), pq.Array(accountIds))
	if err != nil {
		context.GetLogger().Error("Error calculating opportunity lost", "error", err)
		return 0, err
	}

	return opportunityLost, nil
}

func getTroubleshootHighlights(context *security.RequestContext, tenantId string, startDate, endDate string, accountIds []string) (map[string]interface{}, error) {
	subjectTypeToKey := map[string]string{
		"node":                  "node_events",
		"HighErrorCriticalLogs": "app_events",
		"pod":                   "pod_events",
	}

	// Single combined query for all event types grouped by subject_type
	combinedQuery := prepareCombinedHighlightQuery(tenantId, startDate, endDate, accountIds)
	resp, err := query.ExecuteQuery(context, combinedQuery)
	if err != nil {
		return nil, err
	}

	// Split rows by subject_type
	data := map[string]interface{}{
		"node_events": map[string]interface{}{"rows": []query.QueryRow{}},
		"app_events":  map[string]interface{}{"rows": []query.QueryRow{}},
		"pod_events":  map[string]interface{}{"rows": []query.QueryRow{}},
	}
	for _, row := range resp.Rows {
		subjectType, _ := row["subject_type"].(string)
		if key, ok := subjectTypeToKey[subjectType]; ok {
			bucket := data[key].(map[string]interface{})
			bucket["rows"] = append(bucket["rows"].([]query.QueryRow), row)
		}
	}

	queryRequest := prepareRecommendationQuery(tenantId, accountIds)
	recommendationData, err := query.ExecuteQuery(context, queryRequest)
	if err != nil {
		context.GetLogger().Error("Error executing query", "error", err)
		return nil, err
	}
	data["recommendation_groupings"] = map[string]interface{}{
		"rows": recommendationData.Rows,
	}

	return map[string]interface{}{"data": data}, nil
}

func SendDailyHighlightEmailReport(context *security.RequestContext, request TenantReportRequest) error {
	startTime := time.Now()

	tenants, err := tenant.ListTenantsWithActiveAccounts(context)
	if err != nil {
		context.GetLogger().Error("error sending daily_highlight_report slack notification", "error", err)
		return err
	}

	for _, tenant := range tenants {

		accounts, err := account.ListActiveAccountsWithConnectedAgents(context, tenant.Id)
		if err != nil {
			context.GetLogger().Error("error getting agent status by tenant", "error", err)
			continue
		}
		if len(accounts) == 0 {
			context.GetLogger().Warn("no active account found for tenant", "tenant", tenant.Id)
			continue
		}

		accountIds := make([]string, len(accounts))
		for i, acc := range accounts {
			accountIds[i] = acc.Id
		}

		users, err := user.GetUserByTenant(context, tenant.Id)
		if err != nil || len(users) == 0 {
			context.GetLogger().Error("error getting users by tenant or no users found in tenant", "error", err)
			continue
		}

		emails := getEmailsFromUsers(users)

		dateNow := time.Now().Truncate(time.Minute)
		yesterday := dateNow.AddDate(0, 0, -1)

		dateNowFormatted := dateNow.Format("2006-01-02 15:04:05-07:00")
		yesterdayFormatted := yesterday.Format("2006-01-02 15:04:05-07:00")
		dayBeforeFormatted := yesterday.AddDate(0, 0, -1).Format("2006-01-02 15:04:05-07:00")

		// Run independent queries concurrently
		var (
			highlight1           map[string]interface{}
			highlight2           map[string]interface{}
			insights             common.GqlResponse
			totalOpportunityLost float64
		)

		g, _ := errgroup.WithContext(context.GetContext())
		g.Go(func() error {
			var err error
			highlight1, err = getTroubleshootHighlights(context, tenant.Id, yesterdayFormatted, dateNowFormatted, accountIds)
			return err
		})
		g.Go(func() error {
			var err error
			highlight2, err = getTroubleshootHighlights(context, tenant.Id, dayBeforeFormatted, yesterdayFormatted, accountIds)
			return err
		})
		g.Go(func() error {
			var err error
			insights, err = fetchDailyK8sInsights(tenant.Id)
			return err
		})
		g.Go(func() error {
			val, err := calculateOpportunityLost(context, tenant.Id, accountIds)
			if err != nil {
				context.GetLogger().Warn("error calculating opportunity lost, defaulting to 0", "error", err)
				return nil // non-fatal, default to 0
			}
			totalOpportunityLost = val
			return nil
		})

		if err := g.Wait(); err != nil {
			context.GetLogger().Error("error running parallel queries", "error", err)
			continue
		}

		// Filter cloud_accounts to only include accounts with connected agents
		if cloudAccounts, ok := insights.Data["cloud_accounts"].([]interface{}); ok {
			accountIdSet := make(map[string]struct{}, len(accountIds))
			for _, id := range accountIds {
				accountIdSet[id] = struct{}{}
			}
			filtered := make([]interface{}, 0, len(cloudAccounts))
			for _, acc := range cloudAccounts {
				if accMap, ok := acc.(map[string]interface{}); ok {
					if id, ok := accMap["id"].(string); ok {
						if _, exists := accountIdSet[id]; exists {
							filtered = append(filtered, acc)
						}
					}
				}
			}
			insights.Data["cloud_accounts"] = filtered
		}

		if isPayloadEmpty(insights) {
			context.GetLogger().Info("Skipping message publish due to empty payload", "tenant", tenant.Id)
			continue
		}

		totalPotentialSavings := calculateTotalPotentialSavings(highlight1["data"].(map[string]interface{})["recommendation_groupings"].(map[string]interface{}))

		context.GetLogger().Info("preparing daily_highlight_report email to ", "users", slog.AnyValue(emails), "tenant", tenant.Id)
		insights.Data["recommendation_security_groupings_v2"] = map[string]interface{}{"rows": []interface{}{}}
		message := prepareEmailMessage(emails, totalPotentialSavings, totalOpportunityLost, highlight1, highlight2, insights, tenant)
		context.GetLogger().Debug("Payload", "Payload", message)
		err = common.MqPublish(config.Config.RabbitMqNotificationsExchange, config.Config.RabbitMqNotificationsQueue, message)
		if err != nil {
			context.GetLogger().Error("error sending daily_highlight_report email", "error", err)
			continue
		}
	}

	context.GetLogger().Info("time taken to send report email", "time", time.Since(startTime).String())
	return nil
}

func isPayloadEmpty(insights common.GqlResponse) bool {
	insightData, ok := insights.Data["insight"]
	if !ok || insightData == nil {
		return true
	}
	insightSlice, ok := insightData.([]interface{})
	return !ok || len(insightSlice) == 0
}

func calculateTotalPotentialSavings(data map[string]interface{}) float64 {
	totalSavings := big.NewFloat(0)
	rows := data["rows"].([]query.QueryRow)
	for _, row := range rows {
		if sumEstimatedSavings, ok := row["sum_estimated_savings"].(float64); ok {
			totalSavings = new(big.Float).Add(totalSavings, big.NewFloat(sumEstimatedSavings))
		}
	}

	totalSavingsFloat, _ := totalSavings.Float64()
	return totalSavingsFloat
}

func SendDailyAgentStatusEmail(context *security.RequestContext, query TenantReportRequest) error {
	startTime := time.Now()

	tenants, err := tenant.ListTenantsWithActiveAccounts(context)
	if err != nil {
		context.GetLogger().Error("Error listing tenants", "error", err)
		return err
	}

	for _, tenant := range tenants {

		accounts, err := account.ListActiveAccountsWithConnectedAgents(context, tenant.Id)
		if err != nil {
			context.GetLogger().Error("Error listing active accounts with connected agents for tenant", "tenant", tenant.Id, "error", err)
			continue
		}
		if len(accounts) == 0 {
			context.GetLogger().Warn("No active accounts found for tenant", "tenant", tenant.Id)
			continue
		}

		users, err := user.GetUserByTenant(context, tenant.Id)
		if err != nil {
			context.GetLogger().Error("Error getting users for tenant", "tenant", tenant.Id, "error", err)
			continue
		}
		if len(users) == 0 {
			context.GetLogger().Warn("No users found for tenant", "tenant", tenant.Id)
			continue
		}

		emails := getEmailsFromUsers(users)

		accountIds := make([]string, len(accounts))
		for i, acc := range accounts {
			accountIds[i] = acc.Id
		}

		// Batch query: fetch all agents for all accounts in one call
		batchedAgentStatus, err := fetchBatchedAgentStatus(accountIds, "k8s")
		if err != nil {
			context.GetLogger().Error("Error executing batched DB query for agent status", "tenant", tenant.Id, "error", err)
			continue
		}

		// Group agent results by cloud_account_id
		agentsByAccount := make(map[string][]interface{})
		if agents, ok := batchedAgentStatus.Data["agent"].([]interface{}); ok {
			for _, agent := range agents {
				if agentMap, ok := agent.(map[string]interface{}); ok {
					if accID, ok := agentMap["cloud_account_id"].(string); ok {
						agentsByAccount[accID] = append(agentsByAccount[accID], agent)
					}
				}
			}
		}

		var accountAgentStatus []map[string]interface{}
		for _, act := range accounts {
			agentDetails := agentsByAccount[act.Id]
			accountAgentDetails := map[string]interface{}{
				"account_name": act.AccountName,
				"account_id":   act.Id,
				"agent_details": common.GqlResponse{
					Data: map[string]any{"agent": agentDetails},
				},
			}
			accountAgentStatus = append(accountAgentStatus, accountAgentDetails)
		}

		context.GetLogger().Info("Preparing daily_agent_status report email", "users", emails, "tenant", tenant.Id)

		message := prepareAgentEmailMessage(emails, accountAgentStatus, tenant)
		context.GetLogger().Debug("Message payload prepared", "payload", message)
		err = common.MqPublish(config.Config.RabbitMqNotificationsExchange, config.Config.RabbitMqNotificationsQueue, message)
		if err != nil {
			context.GetLogger().Error("Error publishing message to RabbitMQ", "error", err)
			continue
		}
	}

	context.GetLogger().Info("Time taken to send daily agent status email", "duration", time.Since(startTime).String())
	return nil
}

func prepareAgentEmailMessage(emails []string, accountAgentStatus any, tenant models.Tenant) map[string]interface{} {
	today := time.Now().Format("2 January 2006")
	return map[string]interface{}{
		"kind":      "email",
		"email":     emails,
		"source":    "daily_recap",
		"tenant_id": tenant.Id,
		"type":      "agent_status_report",
		"subject":   config.Config.BrandingName + " Agent Status - " + today,
		"parameters": map[string]interface{}{
			"title":             "Agent Status Report",
			"accounts":          accountAgentStatus,
			"organization_name": tenant.Name,
			"base_url":          config.Config.BaseUrl,
			"links": map[string]interface{}{
				"base_url": config.Config.BaseUrl,
			},
		},
	}
}

func prepareDailyTroubleshootCountsQuery(tenant, startDate, endDate string, accountIds []string) query.QueryRequest {
	whereClauses := []query.QueryWhereClause{
		{Binary: query.BinaryWhereClause{"tenant_id": map[query.BinaryWhereClauseType]any{query.Eq: tenant}}},
		{Binary: query.BinaryWhereClause{"created_at": map[query.BinaryWhereClauseType]any{query.Lte: endDate}}},
		{Binary: query.BinaryWhereClause{"created_at": map[query.BinaryWhereClauseType]any{query.Gt: startDate}}},
	}

	if len(accountIds) > 0 {
		whereClauses = append(whereClauses, query.QueryWhereClause{
			Binary: query.BinaryWhereClause{"account_id": map[query.BinaryWhereClauseType]any{query.In: accountIds}},
		})
	}

	return query.QueryRequest{
		Table: "event_groupings_v2",
		Where: query.QueryWhereClause{
			And: whereClauses,
		},
		Columns: []query.QueryColumn{
			{Name: "created_at", Expr: "date_unit", Args: []string{"day"}},
			{Name: "account_id"},
			{Name: "aggregation_key"},
			{Name: "event_count"},
			{Name: "count_priority_high"},
			{Name: "count_priority_medium"},
			{Name: "count_priority_low"},
			{Name: "count_priority_debug"},
			{Name: "count_priority_info"},
			{Name: "count_application_issues"},
			{Name: "count_node_issues"},
			{Name: "count_pod_issues"},
		},
		GroupBy: []string{"account_id"},
	}
}

func prepareTroubleshootSummaryQuery(tenant, startDate, endDate string, accountIds []string) query.QueryRequest {
	whereClauses := []query.QueryWhereClause{
		{Binary: query.BinaryWhereClause{"tenant_id": map[query.BinaryWhereClauseType]any{query.Eq: tenant}}},
		{Binary: query.BinaryWhereClause{"priority": map[query.BinaryWhereClauseType]any{query.Eq: "HIGH"}}},
		{Binary: query.BinaryWhereClause{"created_at": map[query.BinaryWhereClauseType]any{query.Lte: endDate}}},
		{Binary: query.BinaryWhereClause{"created_at": map[query.BinaryWhereClauseType]any{query.Gt: startDate}}},
	}

	if len(accountIds) > 0 {
		whereClauses = append(whereClauses, query.QueryWhereClause{
			Binary: query.BinaryWhereClause{"account_id": map[query.BinaryWhereClauseType]any{query.In: accountIds}},
		})
	}

	return query.QueryRequest{
		Table: "event_groupings_v2",
		Where: query.QueryWhereClause{
			And: whereClauses,
		},
		Columns: []query.QueryColumn{
			{Name: "created_at", Expr: "date_unit", Args: []string{"day"}},
			{Name: "account_id"},
			{Name: "max_created_at"},
			{Name: "event_count"},
			{Name: "subject_namespace"},
			{Name: "subject_name"},
			{Name: "aggregation_key", Expr: "distinct"},
		},
		GroupBy: []string{"account_id", "aggregation_key"},
		OrderBy: []query.QueryOrderBy{
			{Column: "event_count", Order: "desc"},
		},
	}
}

func SendDailyEventsSummaryReport(context *security.RequestContext, request TenantReportRequest) error {
	startTime := time.Now()

	tenants, err := tenant.ListTenantsWithActiveAccounts(context)
	if err != nil {
		context.GetLogger().Error("error sending events summary notification", "error", err)
		return err
	}

	for _, tenant := range tenants {

		accounts, err := account.ListActiveAccountsWithConnectedAgents(context, tenant.Id)
		if err != nil {
			context.GetLogger().Error("error getting agent status by tenant", "error", err)
			continue
		}
		if len(accounts) == 0 {
			context.GetLogger().Warn("no active account found for tenant", "tenant", tenant.Id)
			continue
		}

		accountIds := make([]string, len(accounts))
		for i, acc := range accounts {
			accountIds[i] = acc.Id
		}

		users, err := user.GetUserByTenant(context, tenant.Id)
		if err != nil || len(users) == 0 {
			context.GetLogger().Error("error getting users by tenant or no users found in tenant", "error", err)
			continue
		}

		emails := getEmailsFromUsers(users)

		dateNow := time.Now().Truncate(time.Minute)
		yesterday := dateNow.AddDate(0, 0, -1)

		dateNowFormatted := dateNow.Format("2006-01-02 15:04:05-07:00")
		yesterdayFormatted := yesterday.Format("2006-01-02 15:04:05-07:00")

		queryRequest := prepareDailyTroubleshootCountsQuery(tenant.Id, yesterdayFormatted, dateNowFormatted, accountIds)
		response, err := query.ExecuteQuery(context, queryRequest)
		if err != nil {
			context.GetLogger().Error("Error executing query", "error", err)
			continue
		}

		dailyEventCounts := map[string]interface{}{
			"data": map[string]interface{}{
				"event_groupings": map[string]interface{}{
					"rows": response.Rows,
				},
			},
		}

		queryRequest = prepareTroubleshootSummaryQuery(tenant.Id, yesterdayFormatted, dateNowFormatted, accountIds)
		response, err = query.ExecuteQuery(context, queryRequest)
		if err != nil {
			context.GetLogger().Error("Error executing query", "error", err)
			continue
		}
		dailyEventSummarised := map[string]interface{}{
			"data": map[string]interface{}{
				"event_groupings": map[string]interface{}{
					"rows": response.Rows,
				},
			},
		}

		accountList, err := fetchK8sAccountList(tenant.Id)
		if err != nil {
			context.GetLogger().Error("Error executing query", "error", err)
			continue
		}

		message := prepareEventsSummaryEmailMessage(emails, dailyEventCounts, dailyEventSummarised, accountList, tenant)
		eventCountRows, err := ExtractEventsSummarisedRows(message, "parameters")
		if err != nil {
			context.GetLogger().Warn("could not extract summarized rows", "error", err)
			continue
		}
		eventSummarizedRows, err := ExtractEventCountRows(message, "parameters")
		if err != nil {
			context.GetLogger().Warn("could not extract rows", "error", err)
			continue
		}
		if len(eventCountRows) == 0 || len(eventSummarizedRows) == 0 {
			context.GetLogger().Warn("no events to send, skipping publish")
			continue
		}
		context.GetLogger().Debug("Message payload prepared", "payload", message)
		err = common.MqPublish(config.Config.RabbitMqNotificationsExchange, config.Config.RabbitMqNotificationsQueue, message)
		if err != nil {
			context.GetLogger().Error("Error publishing message to RabbitMQ", "error", err)
			continue
		}
	}

	context.GetLogger().Info("time taken to send report email", "time", time.Since(startTime).String())
	return nil
}

func prepareEventsSummaryEmailMessage(emails []string, dailyEventCounts any, dailyEventSummarised any, accountList any, tenant models.Tenant) map[string]interface{} {
	today := time.Now().Format("2 January 2006")
	return map[string]interface{}{
		"kind":      "email",
		"email":     emails,
		"source":    "daily_recap",
		"tenant_id": tenant.Id,
		"type":      "events_summary",
		"subject":   config.Config.BrandingName + " Troubleshoot Summary - " + today,
		"parameters": map[string]interface{}{
			"title":             "Events Summary",
			"accounts":          accountList,
			"event_counts":      dailyEventCounts,
			"events_summarised": dailyEventSummarised,
			"organization_name": tenant.Name,
			"base_url":          config.Config.BaseUrl,
			"links": map[string]interface{}{
				"base_url": config.Config.BaseUrl,
			},
		},
	}
}

func ExtractEventCountRows(root map[string]interface{}, parameter string) ([]interface{}, error) {
	paramsRaw, ok := root[parameter]
	if !ok {
		return nil, errors.New(`ExtractEventCountRows: missing "parameters"`)
	}
	params, ok := paramsRaw.(map[string]interface{})
	if !ok {
		return nil, errors.New("ExtractEventCountRows: missing parameters")
	}

	ecRaw, ok := params["event_counts"]
	if !ok {
		return nil, errors.New(`ExtractEventCountRows: missing "event_counts" under parameters`)
	}
	ecMap, ok := ecRaw.(map[string]interface{})
	if !ok {
		return nil, errors.New(`ExtractEventCountRows: "event_counts", want map[string]interface{}`)
	}

	dataRaw, ok := ecMap["data"]
	if !ok {
		return nil, errors.New(`ExtractEventCountRows: missing "data" under event_counts`)
	}
	data, ok := dataRaw.(map[string]interface{})
	if !ok {
		return nil, errors.New(`ExtractEventCountRows: "data", want map[string]interface{}`)
	}

	egRaw, ok := data["event_groupings"]
	if !ok {
		return nil, errors.New(`ExtractEventCountRows: missing "event_groupings" under data`)
	}
	egMap, ok := egRaw.(map[string]interface{})
	if !ok {
		return nil, errors.New(`ExtractEventCountRows: "event_groupings", want map[string]interface{}`)
	}

	rowsRaw, ok := egMap["rows"]
	if !ok {
		return nil, errors.New(`ExtractEventCountRows: missing "rows" under event_groupings`)
	}
	if typedRows, ok := rowsRaw.([]query.QueryRow); ok {
		row := make([]interface{}, len(typedRows))
		for i, r := range typedRows {
			row[i] = r
		}
		return row, nil

	}
	return nil, nil
}

func ExtractEventsSummarisedRows(root map[string]interface{}, parameter string) ([]interface{}, error) {
	paramsRaw, ok := root[parameter]
	if !ok {
		return nil, errors.New(`ExtractEventsSummarisedRows: missing "parameters"`)
	}
	params, ok := paramsRaw.(map[string]interface{})
	if !ok {
		return nil, errors.New(`ExtractEventsSummarisedRows: "parameters", want map[string]interface{}`)
	}

	esRaw, ok := params["events_summarised"]
	if !ok {
		return nil, errors.New(`ExtractEventsSummarisedRows: missing "events_summarised" under parameters`)
	}
	esMap, ok := esRaw.(map[string]interface{})
	if !ok {
		return nil, errors.New(`ExtractEventsSummarisedRows: "events_summarised", want map[string]interface{}`)
	}

	dataRaw, ok := esMap["data"]
	if !ok {
		return nil, errors.New(`ExtractEventsSummarisedRows: missing "data" under events_summarised`)
	}
	data, ok := dataRaw.(map[string]interface{})
	if !ok {
		return nil, errors.New(`ExtractEventsSummarisedRows: "data", want map[string]interface{}`)
	}

	egRaw, ok := data["event_groupings"]
	if !ok {
		return nil, errors.New(`ExtractEventsSummarisedRows: missing "event_groupings" under data`)
	}
	egMap, ok := egRaw.(map[string]interface{})
	if !ok {
		return nil, errors.New(`ExtractEventsSummarisedRows: "event_groupings", want map[string]interface{}`)
	}

	rowsRaw, ok := egMap["rows"]
	if !ok {
		return nil, errors.New(`ExtractEventsSummarisedRows: missing "rows" under event_groupings`)
	}
	if typedRows, ok := rowsRaw.([]query.QueryRow); ok {
		row := make([]interface{}, len(typedRows))
		for i, r := range typedRows {
			row[i] = r
		}
		return row, nil
	}

	return nil, nil
}

type Account struct {
	ID          string `json:"id"`
	AccountName string `json:"account_name"`
}

type AccountsData struct {
	Accounts []Account `json:"accounts"`
}

type AccountsDataD struct {
	Data AccountsData `json:"data"`
}

type BatchedFinding struct {
	ID               string    `json:"id"`
	Title            string    `json:"title"`
	AggregationKey   string    `json:"aggregation_key"`
	Severity         string    `json:"severity"`
	Count            int       `json:"count"`
	Cluster          string    `json:"cluster"`
	SubjectName      string    `json:"subject_name"`
	SubjectNamespace string    `json:"subject_namespace"`
	AccountID        string    `json:"account_id"`
	CreatedAt        time.Time `json:"created_at"`
}

type BatchedFindingsPayload struct {
	OrganizationID     string                    `json:"organization_id"`
	OrganizationName   string                    `json:"organization_name"`
	Accounts           AccountsDataD             `json:"accounts"`
	CriticalFindings   []BatchedFinding          `json:"critical_findings"`
	AggregatedFindings map[string]map[string]int `json:"aggregated_findings"` // account_id -> {aggregation_key -> count}
	TotalFindingsCount int                       `json:"total_findings_count"`
	BatchStartTime     time.Time                 `json:"batch_start_time"`
	BatchEndTime       time.Time                 `json:"batch_end_time"`
}

func ProcessHourlyEventsBatchNotification(ctx *security.RequestContext) error {
	startTime := time.Now()
	end := startTime
	start := end.Add(-12 * time.Hour)

	dbManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		ctx.GetLogger().Error("error getting database manager", "error", err)
		return err
	}

	const rulesQuery = `
		SELECT id, name, account_id, cluster, frequency, severity, tenant_id
		FROM notification_rules 
		WHERE delivery_mode = 'batch' AND is_active = true AND is_suppressed = false
	`
	rulesRows, err := dbManager.Query(rulesQuery)
	if err != nil {
		ctx.GetLogger().Error("error querying notification rules", "error", err)
		return err
	}
	defer func(rulesRows *sqlx.Rows) {
		err := rulesRows.Close()
		if err != nil {
			ctx.GetLogger().Error("error closing rows", "error", err)
		}
	}(rulesRows)

	type AccountRule struct {
		TenantID string
		Cluster  string
		Severity string
	}

	accountRules := make(map[string]AccountRule, 64)
	allowedAccountIDs := make([]string, 0, 64)

	for rulesRows.Next() {
		var ruleID, name, accountID, cluster, frequency, severity, tenantID string
		if err := rulesRows.Scan(&ruleID, &name, &accountID, &cluster, &frequency, &severity, &tenantID); err != nil {
			ctx.GetLogger().Error("error scanning notification rule row", "error", err)
			continue
		}
		accountRules[accountID] = AccountRule{
			TenantID: tenantID,
			Cluster:  cluster,
			Severity: severity,
		}
		allowedAccountIDs = append(allowedAccountIDs, accountID)
	}
	if err := rulesRows.Err(); err != nil {
		return err
	}
	if len(allowedAccountIDs) == 0 {
		ctx.GetLogger().Info("No active notification rules found")
		return nil
	}

	const eventQuery = `
		SELECT 
			id, cloud_account_id, title, cluster, subject_name, 
			subject_namespace, aggregation_key, created_at, priority
		FROM events
		WHERE cloud_account_id = ANY($1)
		  AND created_at >= $2
		  AND created_at < $3
		  AND priority NOT IN ('DEBUG', 'INFO')
	`
	eventRows, err := dbManager.Query(eventQuery, pq.Array(allowedAccountIDs), start, end)
	if err != nil {
		return err
	}
	defer func(eventRows *sqlx.Rows) {
		err := eventRows.Close()
		if err != nil {
			ctx.GetLogger().Error("error closing event rows", "error", err)
		}
	}(eventRows)

	tenantCriticalMap := make(map[string]map[string]*BatchedFinding)
	tenantAgg := make(map[string]map[string]map[string]int)
	tenantCount := make(map[string]int)

	for eventRows.Next() {
		var (
			id, accID, title, cluster, subjectName, aggregationKey, priority string
			subjectNamespace                                                 sql.NullString
			createdAt                                                        time.Time
		)
		if err := eventRows.Scan(&id, &accID, &title, &cluster, &subjectName,
			&subjectNamespace, &aggregationKey, &createdAt, &priority); err != nil {
			ctx.GetLogger().Error("error scanning event row", "error", err)
			continue
		}

		rule, ok := accountRules[accID]
		if !ok {
			continue
		}

		if rule.Severity != "" && rule.Severity != priority {
			continue
		}

		tenantID := rule.TenantID
		if tenantAgg[tenantID] == nil {
			tenantAgg[tenantID] = make(map[string]map[string]int)
		}
		if tenantAgg[tenantID][accID] == nil {
			tenantAgg[tenantID][accID] = make(map[string]int)
		}
		tenantAgg[tenantID][accID][aggregationKey]++
		tenantCount[tenantID]++

		if priority == "HIGH" {
			if tenantCriticalMap[tenantID] == nil {
				tenantCriticalMap[tenantID] = make(map[string]*BatchedFinding)
			}
			existing, exists := tenantCriticalMap[tenantID][aggregationKey]
			if exists {
				existing.Count++
				if createdAt.After(existing.CreatedAt) {
					existing.ID, existing.Title, existing.Cluster = id, title, cluster
					existing.SubjectName, existing.AccountID = subjectName, accID
					if subjectNamespace.Valid {
						existing.SubjectNamespace = subjectNamespace.String
					} else {
						existing.SubjectNamespace = ""
					}
					existing.CreatedAt = createdAt
				}
			} else {
				ns := ""
				if subjectNamespace.Valid {
					ns = subjectNamespace.String
				}
				tenantCriticalMap[tenantID][aggregationKey] = &BatchedFinding{
					ID:               id,
					Title:            title,
					AggregationKey:   aggregationKey,
					Severity:         priority,
					Count:            1,
					Cluster:          cluster,
					SubjectName:      subjectName,
					SubjectNamespace: ns,
					AccountID:        accID,
					CreatedAt:        createdAt,
				}
			}
		}
	}
	if err := eventRows.Err(); err != nil {
		return err
	}

	accountsMap := make(map[string][]Account, len(accountRules))
	for accountID, rule := range accountRules {
		accountsMap[rule.TenantID] = append(accountsMap[rule.TenantID], Account{
			ID:          accountID,
			AccountName: rule.Cluster,
		})
	}

	for tenantID, accounts := range accountsMap {
		if tenantCount[tenantID] == 0 {
			continue
		}

		tenantInfo, err := tenant.GetTenant(ctx, tenantID)
		if err != nil {
			ctx.GetLogger().Error("error getting tenant info", "tenant", tenantID, "error", err)
			continue
		}

		var topCriticalFindings []BatchedFinding
		if criticalMap := tenantCriticalMap[tenantID]; criticalMap != nil {
			allCritical := make([]*BatchedFinding, 0, len(criticalMap))
			for _, f := range criticalMap {
				allCritical = append(allCritical, f)
			}
			sort.Slice(allCritical, func(i, j int) bool {
				if allCritical[i].Count != allCritical[j].Count {
					return allCritical[i].Count > allCritical[j].Count
				}
				return allCritical[i].CreatedAt.After(allCritical[j].CreatedAt)
			})
			if len(allCritical) > 5 {
				allCritical = allCritical[:5]
			}
			topCriticalFindings = make([]BatchedFinding, len(allCritical))
			for i, f := range allCritical {
				topCriticalFindings[i] = *f
			}
		}

		payload := BatchedFindingsPayload{
			OrganizationID:     tenantID,
			OrganizationName:   tenantInfo.Name,
			Accounts:           AccountsDataD{Data: AccountsData{Accounts: accounts}},
			CriticalFindings:   topCriticalFindings,
			AggregatedFindings: tenantAgg[tenantID],
			TotalFindingsCount: tenantCount[tenantID],
			BatchStartTime:     start,
			BatchEndTime:       end,
		}

		message := map[string]any{
			"kind":       "notification",
			"type":       "batched_findings",
			"tenant_id":  tenantID,
			"source":     "hourly_batch",
			"parameters": payload,
		}

		ctx.GetLogger().Debug("Message payload prepared", "tenant", tenantID, "total", tenantCount[tenantID])
		if err := common.MqPublish(config.Config.RabbitMqNotificationsExchange, config.Config.RabbitMqNotificationsQueue, message); err != nil {
			ctx.GetLogger().Error("error publishing batched findings message", "tenant", tenantID, "error", err)
		}
	}

	ctx.GetLogger().Info("Completed hourly events batch processing", "duration", time.Since(startTime))
	return nil
}
