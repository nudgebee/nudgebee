package account

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"nudgebee/collector/cloud/common"
	"nudgebee/collector/cloud/config"
	"nudgebee/collector/cloud/providers"
	"nudgebee/collector/cloud/security"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/samber/lo"
)

type CloudAccountCostReportJob struct {
	JobId     string `json:"job_id"`
	AccountId string `json:"account_id"`
	TenantId  string `json:"tenant_id"`
	Month     int    `json:"month"`
	Year      int    `json:"year"`
}

type CloudAccountCostReportDLQMessage struct {
	OriginalMessage []byte `json:"original_message"`
	ErrorType       string `json:"error_type"`
	ErrorMessage    string `json:"error_message"`
	Timestamp       string `json:"timestamp"`
}

// sendToDLQ sends a failed message to the Dead Letter Queue
func sendToDLQ(ctx *security.RequestContext, originalMessage []byte, errorType string, err error) {
	sendToDLQWithConfig(ctx, originalMessage, errorType, err,
		config.Config.RabbitMqCloudAccountCostReportDLQExchange,
		config.Config.RabbitMqCloudAccountCostReportDLQQueue,
	)
}

// sendToDLQWithConfig sends a failed message to the specified Dead Letter Queue
func sendToDLQWithConfig(ctx *security.RequestContext, originalMessage []byte, errorType string, err error, dlqExchange string, dlqQueue string) {
	if dlqExchange == "" || dlqQueue == "" {
		ctx.GetLogger().Warn("DLQ not configured, skipping DLQ publish", "errorType", errorType)
		return
	}

	dlqMessage := CloudAccountCostReportDLQMessage{
		OriginalMessage: originalMessage,
		ErrorType:       errorType,
		ErrorMessage:    err.Error(),
		Timestamp:       time.Now().UTC().Format(time.RFC3339),
	}

	publishErr := common.MqPublish(dlqExchange, dlqQueue, dlqMessage)
	if publishErr != nil {
		ctx.GetLogger().Error("failed to publish to DLQ", "error", publishErr, "originalError", err)
	} else {
		ctx.GetLogger().Info("message sent to DLQ", "errorType", errorType)
	}
}

func StoreDailyUsageReportForAllAccounts(ctx *security.RequestContext) {
	t0 := time.Now()
	ctx.GetLogger().Info("usagereport: starting daily usage report job enqueuing for all accounts")
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		ctx.GetLogger().Error("unable to get database manager", "error", err)
		return
	}

	accountTenantIds := map[string]string{}
	queryResponse := []map[string]any{}

	err = dbms.QueryAndScan(&queryResponse, "select id::text, tenant::text from cloud_accounts where status = 'active' and lower(cloud_provider) IN ('aws', 'azure', 'gcp', 'cloudfoundry')")
	if err != nil {
		ctx.GetLogger().Error("usagereport: unable to fetch active accounts", "error", err)
		return
	}
	for _, qr := range queryResponse {
		accountTenantIds[qr["id"].(string)] = qr["tenant"].(string)
	}

	if len(accountTenantIds) == 0 {
		ctx.GetLogger().Info("usagereport: no active AWS/Azure/GCP accounts found to process for usage reports")
		return
	}
	ctx.GetLogger().Info("usagereport: fetched active accounts", "count", len(accountTenantIds), "time", time.Since(t0).String())

	// AWS/Azure settles bill of last month by 7th of current month, so till then also populate data for last month as well
	publishPreviousMonth := t0.Day() < 9

	// Publish jobs to RabbitMQ
	publishedCount := 0
	failedCount := 0
	for accountId, tenantId := range accountTenantIds {
		// Publish current month job
		currentMonthJob := CloudAccountCostReportJob{
			JobId:     uuid.New().String(),
			AccountId: accountId,
			TenantId:  tenantId,
			Month:     int(t0.Month()),
			Year:      t0.Year(),
		}
		err = common.MqPublish(config.Config.RabbitMqCloudAccountCostReportExchange, config.Config.RabbitMqCloudAccountCostReportQueue, currentMonthJob)
		if err != nil {
			ctx.GetLogger().Error("usagereport: failed to publish current month job", "error", err, "accountId", accountId, "job_id", currentMonthJob.JobId)
			failedCount++
		} else {
			ctx.GetLogger().Debug("usagereport: published current month job", "accountId", accountId, "job_id", currentMonthJob.JobId)
			publishedCount++
		}

		// Publish previous month job if needed
		if publishPreviousMonth {
			previousMonthTime := t0.AddDate(0, -1, 0)
			previousMonthJob := CloudAccountCostReportJob{
				JobId:     uuid.New().String(),
				AccountId: accountId,
				TenantId:  tenantId,
				Month:     int(previousMonthTime.Month()),
				Year:      previousMonthTime.Year(),
			}
			err = common.MqPublish(config.Config.RabbitMqCloudAccountCostReportExchange, config.Config.RabbitMqCloudAccountCostReportQueue, previousMonthJob)
			if err != nil {
				ctx.GetLogger().Error("usagereport: failed to publish previous month job", "error", err, "accountId", accountId, "job_id", previousMonthJob.JobId)
				failedCount++
			} else {
				ctx.GetLogger().Debug("usagereport: published previous month job", "accountId", accountId, "job_id", previousMonthJob.JobId)
				publishedCount++
			}
		}
	}

	ctx.GetLogger().Info("usagereport: finished enqueuing daily usage report jobs", "total_time", time.Since(t0).String(), "published", publishedCount, "failed", failedCount)
}

// ConsumeCloudAccountCostReportJobs starts a RabbitMQ consumer that processes cloud account cost report jobs
func ConsumeCloudAccountCostReportJobs(ctx *security.RequestContext, concurrency int) error {
	if concurrency <= 0 {
		concurrency = config.Config.CloudCollectorServerCostProcessingWorkersMax
		if concurrency <= 0 {
			concurrency = 1 // fallback default
		}
	}

	ctx.GetLogger().Info("usagereport: starting cloud account cost report consumer", "concurrency", concurrency, "queue", config.Config.RabbitMqCloudAccountCostReportQueue, "exchange", config.Config.RabbitMqCloudAccountCostReportExchange)

	processor := func(data []byte) error {
		var job CloudAccountCostReportJob
		err := common.UnmarshalJson(data, &job)
		if err != nil {
			// Permanent error - malformed message. Send to DLQ instead of retrying.
			ctx.GetLogger().Error("usagereport: failed to unmarshal job - sending to DLQ", "error", err, "data", string(data))
			sendToDLQ(ctx, data, "unmarshal_error", err)
			return nil // Return nil to ACK and remove from main queue
		}

		logger := ctx.GetLogger().With("accountId", job.AccountId, "month", job.Month, "year", job.Year, "job_id", job.JobId)
		logger.Info("usagereport: processing cost report job")

		// Create a new request context for this specific account
		jobCtx := security.NewRequestContext(context.Background(), security.NewSecurityContextForSuperAdminWithTenant(job.TenantId), logger, ctx.GetTracer(), ctx.GetMeter())

		// Execute StoreUsage logic
		_, err = StoreUsage(jobCtx, job.AccountId, time.Month(job.Month), job.Year)
		if err != nil {
			// Send to DLQ instead of retrying indefinitely
			logger.Error("usagereport: failed to store usage report - sending to DLQ", "error", err)
			sendToDLQ(jobCtx, data, "processing_error", err)
			return nil // Return nil to ACK and remove from main queue
		}

		logger.Info("usagereport: successfully processed cost report job")
		return nil
	}

	return common.MqConsume(
		config.Config.RabbitMqCloudAccountCostReportExchange,
		config.Config.RabbitMqCloudAccountCostReportQueue,
		config.Config.RabbitMqCloudAccountCostReportQueue,
		concurrency,
		processor,
	)
}

func StoreUsage(ctx *security.RequestContext, accountId string, month time.Month, year int) (StoreUsageReportResponse, error) {
	acquired, release, lockErr := common.TryAcquireSyncLock(ctx.GetContext(), accountId)
	if lockErr != nil {
		ctx.GetLogger().Error("usagereport: failed to acquire sync lock", "accountId", accountId, "error", lockErr)
		return StoreUsageReportResponse{}, lockErr
	}
	if !acquired {
		ctx.GetLogger().Info("usagereport: sync already in progress, skipping", "accountId", accountId)
		return StoreUsageReportResponse{}, nil
	}
	defer release(context.Background())

	t0 := time.Now()
	var err error
	currentMonth := t0.Month()
	currentYear := t0.Year()
	var usageReportResponse StoreUsageReportResponse
	var account providers.Account
	defer func() {
		ctx.GetLogger().Info("usagereport: stored usage report completed", "time", time.Since(t0).String())
		msg := ""
		if err != nil {
			msg = err.Error()
		}
		agentStatus := AgentStatusConnected
		if err != nil {
			agentStatus = AgentStatusDisconnected
		}
		connectionStatus := map[string]any{
			"account_number": account.AccountNumber,
			"spends": map[string]any{
				"updated_at": time.Now().UTC().Format(time.RFC3339),
				"last_job": map[string]any{
					"date": t0.Format("2006-01-02"),
				},
				"data": usageReportResponse,
				"err":  msg,
			},
		}

		// Best-effort: fetch CF stack info for AWS accounts during daily spends sync
		if strings.ToLower(account.CloudProvider) == "aws" && account.AssumeRole != nil && *account.AssumeRole != "" {
			stackInfo, stackErr := GetAwsStackInfo(ctx, accountId)
			if stackErr != nil {
				ctx.GetLogger().Warn("usagereport: failed to fetch CF stack info", "error", stackErr, "accountId", accountId)
			} else {
				connectionStatus["cf_stack"] = map[string]any{
					"template_version": stackInfo.TemplateVersion,
					"stack_name":       stackInfo.StackName,
					"stack_region":     stackInfo.StackRegion,
					"stack_status":     stackInfo.StackStatus,
					"updated_at":       time.Now().UTC().Format(time.RFC3339),
				}
			}
		}

		err := updateOrCreateAgentStatus(ctx, accountId, agentStatus, msg, true, connectionStatus)
		if err != nil {
			ctx.GetLogger().Error("usagereport: failed to update agent status", "error", err.Error())
		}
	}()

	usageReport, account1, err := getUsageDataInternal(ctx, accountId, month, year)
	account = account1
	if err != nil {
		if errors.Is(err, errors.ErrUnsupported) {
			ctx.GetLogger().Debug("usagereport: service does not support usage reports", "accountId", accountId)
		} else {
			ctx.GetLogger().Error("usagereport: unable to fetch usage report", "error", err)
		}
		usageReportResponse = StoreUsageReportResponse{
			Count:    0,
			Duration: time.Since(t0),
		}
		return usageReportResponse, err
	}

	// If no billing data found, publish post-report job and return early.
	// Post-report (resource discovery, recommendations, metrics) still runs for accounts
	// with no billing (free tier, new accounts, disabled billing APIs).
	if len(usageReport.Items) == 0 {
		publishPostReportJob(ctx, accountId, month, year, currentMonth, currentYear)
		return StoreUsageReportResponse{}, nil
	}

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		ctx.GetLogger().Error("usagereport: unable to get dbms", "error", err)
		usageReportResponse = StoreUsageReportResponse{
			Count:    0,
			Duration: time.Since(t0),
		}
		return usageReportResponse, err
	}
	ctx.GetLogger().Info("usagereport: pulled usage report", "time", time.Since(t0).String(), "count", len(usageReport.Items))

	err = storeUsageReport(ctx, dbms, accountId, usageReport, month, year)
	if err != nil {
		ctx.GetLogger().Error("usagereport: unable to insert usage report", "error", err)
		usageReportResponse = StoreUsageReportResponse{
			Count:    0,
			Duration: time.Since(t0),
		}
		return usageReportResponse, err
	}
	ctx.GetLogger().Info("usagereport: stored usage report", "time", time.Since(t0).String(), "count", len(usageReport.Items))

	// update resources
	resourceCnt := 0
	if month == currentMonth && year == currentYear {
		resourceCnt, err = updateResourcesFromUsageReport(ctx, dbms, accountId, account, usageReport)
		if err != nil {
			ctx.GetLogger().Error("usagereport: unable to update resources", "error", err)
			usageReportResponse = StoreUsageReportResponse{
				Count:    0,
				Duration: time.Since(t0),
			}
			return usageReportResponse, err
		}
		ctx.GetLogger().Info("usagereport: updated resources from usage report", "time", time.Since(t0).String(), "count", len(usageReport.Items))
	}

	// trigger notifications
	err = sendDailySpendNotification(ctx, accountId, account.AccountName, usageReport)
	if err != nil {
		ctx.GetLogger().Error("usagereport: unable to send notification", "error", err)
	}

	// update spends
	spendCount, err := updateSpendsFromUsageReport(ctx, dbms, accountId, account, usageReport)
	if err != nil {
		ctx.GetLogger().Error("usagereport: unable to update spends", "error", err)
		usageReportResponse = StoreUsageReportResponse{
			Count:    0,
			Duration: time.Since(t0),
		}
		return usageReportResponse, err
	}
	ctx.GetLogger().Info("usagereport: updated spends from usage report", "time", time.Since(t0).String(), "count", len(usageReport.Items))

	// Publish post-report job AFTER all DB writes (resources + spends) are complete.
	// The post-report job UPSERTs cloud_resourses, and the spends INSERT takes KEY SHARE
	// FK locks on cloud_resourses, so concurrent execution can deadlock.
	// In-job ordering alone is not sufficient because a different StoreUsage invocation
	// for the same account (e.g. previous-month backfill) can run between this job's
	// release of the sync lock and the post-report consumer picking up the message.
	// The post-report consumer therefore acquires the same per-account sync lock; see
	// ConsumeCloudAccountPostReportJobs.
	publishPostReportJob(ctx, accountId, month, year, currentMonth, currentYear)

	usageReportResponse = StoreUsageReportResponse{
		Count:         len(usageReport.Items),
		ResourceCount: resourceCnt,
		SpendCount:    spendCount,
		Duration:      time.Since(t0),
	}

	return usageReportResponse, err
}

func publishPostReportJob(ctx *security.RequestContext, accountId string, month time.Month, year int, currentMonth time.Month, currentYear int) {
	if month != currentMonth || year != currentYear {
		return
	}
	postReportJob := CloudAccountPostReportJob{
		JobId:     uuid.New().String(),
		AccountId: accountId,
		TenantId:  ctx.GetSecurityContext().GetTenantId(),
	}
	publishErr := common.MqPublish(
		config.Config.RabbitMqCloudAccountPostReportExchange,
		config.Config.RabbitMqCloudAccountPostReportQueue,
		postReportJob,
	)
	if publishErr != nil {
		ctx.GetLogger().Error("usagereport: failed to publish post-report job", "error", publishErr, "accountId", accountId)
	} else {
		ctx.GetLogger().Info("usagereport: published post-report job", "accountId", accountId, "job_id", postReportJob.JobId)
	}
}

func sendDailySpendNotification(ctx *security.RequestContext, accountId, accountName string, usageReportResponse providers.GetUsageReportResponse) error {
	// get data for current data
	todayItem := make([]providers.UsageReportItem, 0, 100)
	monthlyServices := map[string]float64{}
	// its for previous day as we get data for last date
	yesterdayDate := time.Now().AddDate(0, 0, -1)
	totalSpendOfDay := 0.0
	totalSpendOfMonth := 0.0
	// Determine the account's currency from the report items.
	// A single cloud account uses one currency; detect mismatches defensively.
	costCurrency := ""
	mixedCurrencies := false
	for _, r := range usageReportResponse.Items {
		if r.CostCurrency != "" {
			if costCurrency == "" {
				costCurrency = r.CostCurrency
			} else if r.CostCurrency != costCurrency {
				mixedCurrencies = true
			}
		}
		if r.EndDate.Day() == yesterdayDate.Day() && r.EndDate.Month() == yesterdayDate.Month() && r.EndDate.Year() == yesterdayDate.Year() {
			todayItem = append(todayItem, r)
			totalSpendOfDay += r.Cost
		}
		if r.EndDate.Month() == yesterdayDate.Month() && r.EndDate.Year() == yesterdayDate.Year() {
			totalSpendOfMonth += r.Cost
			monthlyServices[r.ProductCode] += r.Cost
		}
	}
	if costCurrency == "" {
		costCurrency = "USD"
	}
	if mixedCurrencies {
		ctx.GetLogger().Warn("usagereport: mixed currencies detected in usage report, cost totals may be inaccurate",
			"account_id", accountId, "primary_currency", costCurrency)
	}
	slices.SortFunc(todayItem, func(a, b providers.UsageReportItem) int {
		return cmp.Compare(b.Cost, a.Cost)
	})

	serviceItems := make([]map[string]any, 0, len(monthlyServices))
	for k, v := range monthlyServices {
		serviceItems = append(serviceItems, map[string]any{
			"service": k,
			"cost":    v,
		})
	}
	slices.SortFunc(serviceItems, func(a, b map[string]any) int {
		return cmp.Compare(b["cost"].(float64), a["cost"].(float64))
	})

	dailyItemMap := lo.Map(lo.Slice(todayItem, 0, 5), func(item providers.UsageReportItem, i int) map[string]any {
		return map[string]any{
			"cost":         item.Cost,
			"product_code": item.ProductCode,
			"resource_arn": item.ResourceArn,
		}
	})

	defaultChannels := map[string][]map[string]any{}
	if len(config.Config.CloudCollectorNotificationSlackChannel) > 0 {
		defaultChannels = map[string][]map[string]any{
			"slack": {
				{
					"name": "nudgebee-cloud-alerts",
					"id":   config.Config.CloudCollectorNotificationSlackChannel,
				},
			},
		}
	}

	message := map[string]any{
		"kind":      "notification",
		"source":    "cloud",
		"type":      "cloud_cost_summary",
		"tenant_id": ctx.GetSecurityContext().GetTenantId(),
		"channels":  defaultChannels,
		"parameters": map[string]any{
			"title":        fmt.Sprintf("Today's (%s) Cloud Spend For Account (%s) - %f and Cost of Month (%f)", yesterdayDate.Format("02-01-2006"), accountName, totalSpendOfDay, totalSpendOfMonth),
			"account_id":   accountId,
			"account_name": accountName,
			"summary": map[string]any{
				"top_5_daily_items":      dailyItemMap,
				"top_5_monthly_services": lo.Slice(serviceItems, 0, 5),
			},
			"total_daily_cost":   totalSpendOfDay,
			"total_monthly_cost": totalSpendOfMonth,
			"cost_currency":      costCurrency,
			"period": map[string]any{
				"start": yesterdayDate.AddDate(0, -1, 0).Format("2006-01-02"),
				"end":   yesterdayDate.Format("2006-01-02"),
			},
		},
	}

	err := common.MqPublish(config.Config.RabbitMqNotificationsExchange, config.Config.RabbitMqNotificationsQueue, message)
	if err != nil {
		ctx.GetLogger().Error("usagereport: error publishing message to queue", "error", err)
		return err
	}

	return nil
}

func GetUsageData(ctx *security.RequestContext, accountId string, month time.Month, year int) (providers.GetUsageReportResponse, error) {
	report, _, err := getUsageDataInternal(ctx, accountId, month, year)
	return report, err
}

func storeUsageReport(ctx *security.RequestContext, dbms *common.DatabaseManager, accountId string, usageReport providers.GetUsageReportResponse, month time.Month, year int) error {
	reportInsertedDate := time.Now().UTC()
	reportDate := time.Date(year, month, 1, 0, 0, 0, 0, time.UTC)

	// Delete existing records for this month first
	_, err := dbms.DoInTransaction(func(tx common.DatabaseManagerTx) (any, error) {
		impactedRecords, err := dbms.Exec("DELETE FROM cloud_account_usage_report WHERE account_id = $1 AND TO_CHAR(report_date::date, 'yyyy-mm') = $2", accountId, reportDate.Format("2006-01"))
		if err != nil {
			ctx.GetLogger().Error("usagereport: unable to delete existing usage report", "error", err)
			return nil, err
		}
		if c, err := impactedRecords.RowsAffected(); err == nil {
			ctx.GetLogger().Info("usagereport: deleted existing usage report", "count", c)
		}
		return nil, nil
	})
	if err != nil {
		return err
	}

	// Insert in chunks to avoid building all maps in memory at once.
	// Each chunk builds maps, inserts, then lets GC reclaim before next chunk.
	const chunkSize = 5000
	insertQuery := `INSERT INTO cloud_account_usage_report (id, tenant_id, account_id, report_date, product_code, product_service_code, resource_region_code, resource_id, resource_type, resource_arn, cost_category, cost_sub_category, resource_operation, cost, cost_currency, resource_tags, start_date, end_date)
				  VALUES (:id, :tenant_id, :account_id, :report_date, :product_code, :product_service_code, :resource_region_code, :resource_id, :resource_type, :resource_arn, :cost_category, :cost_sub_category, :resource_operation, :cost, :cost_currency, :resource_tags, :start_date, :end_date)
				`

	for chunkStart := 0; chunkStart < len(usageReport.Items); chunkStart += chunkSize {
		chunkEnd := chunkStart + chunkSize
		if chunkEnd > len(usageReport.Items) {
			chunkEnd = len(usageReport.Items)
		}
		chunk := usageReport.Items[chunkStart:chunkEnd]

		chunkMaps := make([]map[string]any, 0, len(chunk))
		for _, item := range chunk {
			if err := common.ValidateStruct(item); err != nil {
				ctx.GetLogger().Error("usagereport: invalid usage report item", "error", err, "data", item)
				return err
			}

			data := map[string]any{}
			data["id"] = uuid.New().String()
			tenantId := ctx.GetSecurityContext().GetTenantId()
			if tenantId == "" {
				data["tenant_id"] = nil
			} else {
				data["tenant_id"] = tenantId
			}
			data["account_id"] = accountId
			data["report_date"] = reportDate.Format("2006-01-02")
			data["report_inserted_date"] = reportInsertedDate
			data["product_code"] = item.ProductCode
			data["product_service_code"] = item.ProductServiceCode
			data["resource_region_code"] = lo.Ternary(item.ResourceRegionCode == "", "global", item.ResourceRegionCode)
			data["resource_id"] = item.ResourceId
			data["resource_type"] = item.ResourceType
			data["resource_arn"] = item.ResourceArn
			data["resource_operation"] = item.ResourceOperation
			data["cost_category"] = item.CostCategory
			data["cost_sub_category"] = item.CostSubCategory
			data["cost"] = item.Cost
			data["cost_currency"] = lo.Ternary(item.CostCurrency == "", "USD", item.CostCurrency)
			if len(item.ResourceTags) != 0 {
				tagsJson, err := common.MarshalJson(item.ResourceTags)
				if err != nil {
					ctx.GetLogger().Error("usagereport: unable to marshal tags", "error", err)
					return err
				}
				data["resource_tags"] = string(tagsJson)
			} else {
				data["resource_tags"] = "{}"
			}
			data["start_date"] = item.StartDate
			data["end_date"] = item.EndDate
			chunkMaps = append(chunkMaps, data)
		}

		_, err := dbms.DoInTransaction(func(tx common.DatabaseManagerTx) (any, error) {
			_, err := dbms.NamedExec(insertQuery, chunkMaps)
			return nil, err
		})
		if err != nil {
			ctx.GetLogger().Error("usagereport: unable to insert usage report chunk", "error", err, "chunkStart", chunkStart, "chunkEnd", chunkEnd)
			return err
		}
	}

	return nil
}

func updateResourcesFromUsageReport(ctx *security.RequestContext, dbms *common.DatabaseManager, accountId string, account providers.Account, usageReport providers.GetUsageReportResponse) (int, error) {
	if len(usageReport.Items) == 0 {
		return 0, nil
	}

	resourceMap := map[string]map[string]any{}
	t := time.Now().UTC().Format(time.RFC3339)
	var err error
	var nilString *string
	for _, item := range usageReport.Items {
		if item.ResourceId == "" {
			continue
		}
		resourceMapKey := buildExternalResourceId(account.CloudProvider, account.AccountNumber, item.ResourceRegionCode, item.ProductCode, item.ResourceType, item.ResourceId, "")
		if _, ok := resourceMap[resourceMapKey]; ok {
			continue
		}
		tagsStr := []byte("{}")
		if len(item.ResourceTags) > 0 {
			tagsStr, err = common.MarshalJson(item.ResourceTags)
			if err != nil {
				ctx.GetLogger().Error("unable to marshal tags", "error", err)
				return 0, err
			}
		}
		tenantId := ctx.GetSecurityContext().GetTenantId()
		resourceDbData := map[string]any{
			"id":                   uuid.New().String(),
			"created_at":           t,
			"created_by":           nilString,
			"updated_at":           t,
			"updated_by":           nilString,
			"resourse_id":          item.ResourceId,
			"name":                 lo.Ternary(item.ResourceName != "", item.ResourceName, item.ResourceId),
			"type":                 item.ResourceType,
			"status":               providers.ResourceStatusActive,
			"resourse_created_on":  t,
			"account":              accountId,
			"cloud_provider":       account.CloudProvider,
			"region":               lo.Ternary(item.ResourceRegionCode == "", "global", item.ResourceRegionCode),
			"arn":                  item.ResourceArn,
			"tenant":               lo.Ternary(tenantId == "", nilString, &tenantId),
			"tags":                 string(tagsStr),
			"meta":                 `{"nb_source":"billing"}`,
			"service_name":         item.ProductCode,
			"first_seen":           t,
			"last_seen":            t,
			"is_active":            true,
			"external_resource_id": resourceMapKey,
		}

		resourceMap[resourceMapKey] = resourceDbData
	}

	// update resources as inactive and upsert new ones in a single transaction to prevent deadlocks
	resourceMapKeys := slices.Collect(maps.Keys(resourceMap))

	// insert resources from usage report which dont exists and update existing ones last-seen
	// Choose conflict strategy based on cloud provider
	// Azure: Use (account, external_resource_id) because Azure external_resource_id = resource_id (full path)
	// AWS/GCP: Use 5-column constraint to handle cases where external_resource_id varies (e.g., :data-transfer suffix)
	baseInsertQuery := `INSERT INTO cloud_resourses (id, created_at, created_by, updated_at, updated_by, resourse_id, name, type, status, resourse_created_on, account, cloud_provider, region, arn, tenant, tags, meta, service_name, first_seen, last_seen, is_active, external_resource_id)
				values (:id, :created_at, :created_by, :updated_at, :updated_by, :resourse_id, :name, :type, :status, :resourse_created_on, :account, :cloud_provider, :region, :arn, :tenant, :tags, :meta, :service_name, :first_seen, :last_seen, :is_active, :external_resource_id)`
	var conflictClause string
	if strings.EqualFold(account.CloudProvider, "azure") {
		conflictClause = `
				 on conflict (account, external_resource_id)
					do update set
						last_seen = EXCLUDED.last_seen,
						tags = cloud_resourses.tags || EXCLUDED.tags,
						meta = CASE
							WHEN COALESCE(cloud_resourses.meta->>'nb_source', '') = ''
								THEN COALESCE(cloud_resourses.meta, CAST('{}' AS jsonb)) || EXCLUDED.meta
							ELSE
								COALESCE(cloud_resourses.meta, CAST('{}' AS jsonb)) || (EXCLUDED.meta - 'nb_source')
						END,
						arn = EXCLUDED.arn,
						resourse_id = EXCLUDED.resourse_id,
						name = EXCLUDED.name,
						type = EXCLUDED.type,
						region = EXCLUDED.region,
						service_name = EXCLUDED.service_name,
						is_active = EXCLUDED.is_active,
						status = EXCLUDED.status`
	} else {
		conflictClause = `
				 on conflict (account, resourse_id, type, region, service_name)
					do update set
						last_seen = EXCLUDED.last_seen,
						tags = cloud_resourses.tags || EXCLUDED.tags,
						meta = CASE
							WHEN COALESCE(cloud_resourses.meta->>'nb_source', '') = ''
								THEN COALESCE(cloud_resourses.meta, CAST('{}' AS jsonb)) || EXCLUDED.meta
							ELSE
								COALESCE(cloud_resourses.meta, CAST('{}' AS jsonb)) || (EXCLUDED.meta - 'nb_source')
						END,
						arn = EXCLUDED.arn,
						name = EXCLUDED.name,
						external_resource_id = EXCLUDED.external_resource_id,
						is_active = EXCLUDED.is_active,
						status = EXCLUDED.status`
	}
	insertQuery := baseInsertQuery + conflictClause

	_, err = dbms.DoInTransaction(func(tx common.DatabaseManagerTx) (any, error) {
		if len(resourceMapKeys) > 0 {
			updateQuery := `update cloud_resourses set is_active = false, last_seen = $2, status = $3 where account = $1 and status != 'Deleted' and external_resource_id != ALL($4::text[]) and meta->>'nb_source' = 'billing'`
			_, err := tx.Exec(updateQuery, accountId, time.Now().UTC().Format(time.RFC3339), providers.ResourceStatusDeleted, pq.Array(resourceMapKeys))
			if err != nil {
				return nil, fmt.Errorf("update resources inactive: %w", err)
			}
		}
		_, err := tx.NamedExec(insertQuery, slices.Collect(maps.Values(resourceMap)))
		if err != nil {
			return nil, fmt.Errorf("upsert resources: %w", err)
		}
		return nil, nil
	})
	if err != nil {
		ctx.GetLogger().Error("usagereport: unable to update resources", "error", err)
		return 0, err
	}
	return len(resourceMap), nil
}

// buildSpendMap aggregates usage report items into spend records keyed for DB upsert.
// The returned map keys align with the DB unique constraint (tenant, cloud_account, cloud_resource_id, date).
// externalResourceIdMap maps externalResourceId → cloud_resourses.id (UUID).
func buildSpendMap(account providers.Account, usageReport providers.GetUsageReportResponse, externalResourceIdMap map[string]string, tenantId string, accountId string) (map[string]map[string]any, error) {
	spendMap := map[string]map[string]any{}
	var nilString *string

	for _, item := range usageReport.Items {
		externalResourceId := ""
		if item.ResourceId != "" {
			externalResourceId = buildExternalResourceId(account.CloudProvider, account.AccountNumber, item.ResourceRegionCode, item.ProductCode, item.ResourceType, item.ResourceId, "")
		}
		resourceId := externalResourceIdMap[externalResourceId]
		// spendKey must align with unique constraint (tenant, cloud_account, cloud_resource_id, date).
		// Credit/Refund items have ResourceId="" (NULL in DB) so they won't conflict with non-credit rows.
		// Use "credit" bucket to prevent credit costs merging with non-credit empty-resource rows.
		creditBucket := ""
		if item.CostCategory == "Credit" || item.CostCategory == "Refund" {
			creditBucket = "credit"
		}
		// Build spendKey to match DB unique constraint granularity.
		// For non-credit items: use externalResourceId which includes (provider, account, region,
		// service, resourceType, resourceId) — prevents cross-service collision when multiple
		// services share the same ResourceId (e.g., GCP services with NULL resource.name
		// all fall back to project ID).
		// For credit items: ResourceId is cleared to "", so use ProductCode+region to preserve
		// per-service credit attribution instead of collapsing all credits into one row per day.
		var spendKey string
		if creditBucket == "credit" {
			spendKey = fmt.Sprintf("credit:%s:%s:%s", item.ProductCode, item.ResourceRegionCode, item.StartDate.UTC().Format("2006-01-02"))
		} else {
			keyPart := externalResourceId
			if keyPart == "" {
				keyPart = fmt.Sprintf("%s:%s", item.ProductCode, item.ResourceRegionCode)
			}
			spendKey = fmt.Sprintf(":%s:%s", keyPart, item.StartDate.UTC().Format("2006-01-02"))
		}
		if _, ok := spendMap[spendKey]; !ok {
			tagsStr := []byte("{}")
			tags := item.ResourceTags
			tags["nb_service_name"] = []string{item.ProductCode}
			tags["nb_resource_type"] = []string{item.ResourceType}
			tags["nb_resource_region_code"] = []string{item.ResourceRegionCode}
			tags["nb_resource_id"] = []string{resourceId}
			if len(item.ResourceTags) > 0 {
				var err error
				tagsStr, err = common.MarshalJson(item.ResourceTags)
				if err != nil {
					return nil, fmt.Errorf("unable to marshal tags: %w", err)
				}
			}
			spendDbData := map[string]any{
				"id":                uuid.New().String(),
				"date":              item.StartDate.UTC().Format("2006-01-02"),
				"amount":            item.Cost,
				"unit":              item.CostCurrency,
				"business_unit":     nilString,
				"tenant":            lo.Ternary(tenantId == "", nilString, &tenantId),
				"cloud_account":     accountId,
				"cloud_resource_id": lo.Ternary(resourceId == "", nilString, &resourceId),
				"exclude_aggregate": creditBucket == "credit",
				"tags":              string(tagsStr),
			}
			spendMap[spendKey] = spendDbData
		} else {
			spendMap[spendKey]["amount"] = spendMap[spendKey]["amount"].(float64) + item.Cost
		}
	}
	return spendMap, nil
}

func updateSpendsFromUsageReport(ctx *security.RequestContext, dbms *common.DatabaseManager, accountId string, account providers.Account, usageReport providers.GetUsageReportResponse) (int, error) {

	externalResourceIds := []string{}
	for _, item := range usageReport.Items {
		if item.ResourceId != "" {
			externalResourceIds = append(externalResourceIds, buildExternalResourceId(account.CloudProvider, account.AccountNumber, item.ResourceRegionCode, item.ProductCode, item.ResourceType, item.ResourceId, ""))
		}
	}

	//fetch data from db
	externalResourceIdMap, err := getExternalIdAndResourceIdMap(ctx, dbms, accountId, externalResourceIds)
	if err != nil {
		ctx.GetLogger().Error("usagereport: unable to fetch resources", "error", err)
		return 0, err
	}

	tenantId := ctx.GetSecurityContext().GetTenantId()
	spendMap, err := buildSpendMap(account, usageReport, externalResourceIdMap, tenantId, accountId)
	if err != nil {
		ctx.GetLogger().Error("usagereport: unable to build spend map", "error", err)
		return 0, err
	}

	// Separate spends by whether they have a cloud_resource_id.
	// NULL cloud_resource_id rows (credits/refunds) can't use UPSERT because
	// PostgreSQL treats NULLs as distinct in unique indexes, so ON CONFLICT won't match them.
	// Note: tenant is always non-NULL in this flow (set from job.TenantId), so the
	// ON CONFLICT (tenant, cloud_account, cloud_resource_id, date) clause works correctly.
	var spendsWithResource, spendsWithoutResource []map[string]any
	for _, s := range spendMap {
		if s["cloud_resource_id"] == nil {
			spendsWithoutResource = append(spendsWithoutResource, s)
		} else {
			spendsWithResource = append(spendsWithResource, s)
		}
	}

	dateStrArr := lo.Map(usageReport.Items, func(item providers.UsageReportItem, _ int) string { return item.StartDate.UTC().Format("2006-01-02") })
	dateStrArr = lo.Uniq(dateStrArr)

	_, err = dbms.DoInTransaction(func(tx common.DatabaseManagerTx) (any, error) {
		// UPSERT spends with non-NULL cloud_resource_id.
		// Only genuinely new rows trigger FK checks (KEY SHARE locks) on cloud_resourses.
		// Existing rows just get amount/tags updated — no FK recheck, no lock contention
		// with the post-report worker that UPSERTs cloud_resourses concurrently.
		if len(spendsWithResource) > 0 {
			upsertQuery := `INSERT INTO spends (id, date, amount, unit, business_unit, tenant, cloud_account, cloud_resource_id, exclude_aggregate, tags)
				VALUES (:id, :date, :amount, :unit, :business_unit, :tenant, :cloud_account, :cloud_resource_id, :exclude_aggregate, :tags)
				ON CONFLICT (tenant, cloud_account, cloud_resource_id, date)
				DO UPDATE SET amount = EXCLUDED.amount, unit = EXCLUDED.unit, tags = EXCLUDED.tags, exclude_aggregate = EXCLUDED.exclude_aggregate`
			_, err := tx.NamedExec(upsertQuery, spendsWithResource)
			if err != nil {
				return nil, fmt.Errorf("upsert spends: %w", err)
			}
		}

		// DELETE + INSERT for NULL cloud_resource_id rows (credits/refunds).
		// No FK contention — NULL cloud_resource_id means no FK check on cloud_resourses.
		_, err := tx.Exec(`DELETE FROM spends WHERE cloud_account = $1 AND date IN ($2) AND cloud_resource_id IS NULL`, accountId, dateStrArr)
		if err != nil {
			return nil, fmt.Errorf("delete null-resource spends: %w", err)
		}
		if len(spendsWithoutResource) > 0 {
			insertQuery := `INSERT INTO spends (id, date, amount, unit, business_unit, tenant, cloud_account, cloud_resource_id, exclude_aggregate, tags)
				VALUES (:id, :date, :amount, :unit, :business_unit, :tenant, :cloud_account, :cloud_resource_id, :exclude_aggregate, :tags)`
			_, err = tx.NamedExec(insertQuery, spendsWithoutResource)
			if err != nil {
				return nil, fmt.Errorf("insert null-resource spends: %w", err)
			}
		}

		return nil, nil
	})
	if err != nil {
		ctx.GetLogger().Error("usagereport: unable to update spends", "error", err)
		return 0, err
	}

	return len(spendMap), nil
}

func storeAccountRecommendations(ctx *security.RequestContext, accountId string) (data map[string]StoreUsageRecommendationResponse, err error) {
	t0 := time.Now()
	data = map[string]StoreUsageRecommendationResponse{}
	// get serviceNames and regions of active resources
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		ctx.GetLogger().Error("usagereport: unable to get dbms", "error", err)
		return data, err
	}

	query := `select distinct service_name from cloud_resourses where account = $1 and is_active = true`
	services := []string{}
	err = dbms.QueryAndScan(&services, query, accountId)
	if err != nil {
		ctx.GetLogger().Error("usagereport: unable to fetch resources", "error", err)
		return data, err
	}

	// sync resource-based recommendations
	for _, serviceName := range services {
		r, err := StoreRecommendations(ctx, accountId, providers.ListRecommendationsRequest{
			ServiceName: serviceName,
		})
		if err != nil && !errors.Is(err, errors.ErrUnsupported) {
			ctx.GetLogger().Error("usagereport: unable to sync recommendations", "error", err, "serviceName", serviceName, "accountId", accountId)
		}
		msg := ""
		if err != nil && !errors.Is(err, errors.ErrUnsupported) {
			msg = err.Error()
		}
		data[serviceName] = StoreUsageRecommendationResponse{
			Data:  r,
			Error: msg,
		}
	}

	// sync native cloud provider recommendations (account-level, not in cloud_resourses)
	// AWS: costoptimizationhub, costexplorer, computeoptimizer, trustedadvisor
	// GCP: recommender
	// Azure: advisor
	nativeServices := []string{"costoptimizationhub", "costexplorer", "computeoptimizer", "trustedadvisor", "recommender", "advisor"}
	for _, svc := range nativeServices {
		r, err := StoreRecommendations(ctx, accountId, providers.ListRecommendationsRequest{
			ServiceName: svc,
		})
		if err != nil {
			ctx.GetLogger().Error("usagereport: unable to sync native recommendations", "error", err, "service", svc, "accountId", accountId)
		}
		msg := ""
		if err != nil {
			msg = err.Error()
		}
		data[svc] = StoreUsageRecommendationResponse{
			Data:  r,
			Error: msg,
		}
	}

	ctx.GetLogger().Info("usagereport: synced recommendations after usage report", "time", time.Since(t0).String())
	return data, nil
}

func discoverAndStoreAccountResources(ctx *security.RequestContext, accountId string) (data map[string]StoreUsageResourceResponse, err error) {
	t0 := time.Now()
	data = map[string]StoreUsageResourceResponse{}
	// get serviceNames and regions of active resources
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		ctx.GetLogger().Error("usagereport: unable to get dbms", "error", err)
		return data, err
	}

	query := `select distinct service_name, region from cloud_resourses where account = $1`

	serviceAndRegions := []map[string]any{}
	err = dbms.QueryAndScan(&serviceAndRegions, query, accountId)
	if err != nil {
		ctx.GetLogger().Error("usagereport: unable to fetch resources", "error", err)
		return data, err
	}

	// If no resources exist yet (initial onboarding), discover from provider default services.
	// This handles providers like CloudFoundry that have no billing/usage data to seed resources.
	if len(serviceAndRegions) == 0 {
		ctx.GetLogger().Info("usagereport: no existing resources found, running initial discovery from default services", "accountId", accountId)
		account, _, accErr := getAccount(ctx, accountId)
		if accErr != nil {
			ctx.GetLogger().Error("usagereport: unable to fetch account for initial discovery", "error", accErr, "accountId", accountId)
			return data, accErr
		}
		discoverAndStoreResources(ctx, account, accountId)
		ctx.GetLogger().Info("usagereport: initial resource discovery completed", "accountId", accountId, "time", time.Since(t0).String())
		return data, nil
	}

	serviceRegionsMaps := map[string][]string{}
	for _, r := range serviceAndRegions {
		serviceName := r["service_name"].(string)
		// Normalize Azure service names - some resources may have full type paths
		// For microsoft.insights/actiongroups, microsoft.insights/metricalerts etc, use base service name
		if strings.HasPrefix(strings.ToLower(serviceName), "microsoft.insights/") {
			serviceName = "microsoft.insights"
		}

		if _, ok := serviceRegionsMaps[serviceName]; !ok {
			serviceRegionsMaps[serviceName] = []string{}
		}
		serviceRegionsMaps[serviceName] = append(serviceRegionsMaps[serviceName], r["region"].(string))
	}

	// Ensure provider default services are always synced, even if they have no
	// rows in cloud_resourses yet. This covers services like GCP Persistent Disks
	// and NICs that never appear in billing data under their own service name.
	account, _, accErr := getAccount(ctx, accountId)
	if accErr != nil {
		ctx.GetLogger().Error("usagereport: unable to fetch account for default services check", "error", accErr, "accountId", accountId)
	} else {
		providerKey := strings.ToLower(account.CloudProvider)
		for _, defaultSvc := range providerDefaultServices[providerKey] {
			if _, exists := serviceRegionsMaps[defaultSvc]; !exists {
				ctx.GetLogger().Info("usagereport: adding missing default service for discovery", "service", defaultSvc, "provider", account.CloudProvider, "accountId", accountId)
				serviceRegionsMaps[defaultSvc] = []string{}
			}
		}
	}

	// sync resources
	for serviceName, regions := range serviceRegionsMaps {
		r, err := StoreResources(ctx, accountId, serviceName, regions...)
		msg := ""
		if err != nil && !errors.Is(err, errors.ErrUnsupported) {
			ctx.GetLogger().Error("usagereport: unable to sync resources", "error", err, "serviceName", serviceName, "regions", regions)
			msg = err.Error()
		}
		data[serviceName] = StoreUsageResourceResponse{
			Data:    r,
			Regions: regions,
			Error:   msg,
		}
	}
	ctx.GetLogger().Info("usagereport: synced resources after usage report", "time", time.Since(t0).String())

	return data, nil
}

// CloudAccountPostReportJob represents an async job for post-report processing
// (resource discovery, recommendations, metrics sync) that runs after the cost report is stored.
type CloudAccountPostReportJob struct {
	JobId     string `json:"job_id"`
	AccountId string `json:"account_id"`
	TenantId  string `json:"tenant_id"`
}

// ConsumeCloudAccountPostReportJobs starts a RabbitMQ consumer that processes post-report jobs.
// These jobs handle resource discovery, recommendations, and metrics sync asynchronously
// after the cost report has been stored, to avoid holding large report data in memory
// during these heavyweight operations.
func ConsumeCloudAccountPostReportJobs(ctx *security.RequestContext, concurrency int) error {
	if concurrency <= 0 {
		concurrency = 1
	}

	ctx.GetLogger().Info("postreport: starting cloud account post-report consumer", "concurrency", concurrency, "queue", config.Config.RabbitMqCloudAccountPostReportQueue, "exchange", config.Config.RabbitMqCloudAccountPostReportExchange)

	processor := func(data []byte) error {
		var job CloudAccountPostReportJob
		err := common.UnmarshalJson(data, &job)
		if err != nil {
			ctx.GetLogger().Error("postreport: failed to unmarshal job - dropping message", "error", err, "data", string(data))
			return nil // ACK to prevent poison message loop
		}

		logger := ctx.GetLogger().With("accountId", job.AccountId, "job_id", job.JobId)

		// Acquire the per-account sync lock before doing any DB writes. This serializes
		// post-report (cloud_resourses UPSERT) against StoreUsage (spends INSERT, which
		// takes KEY SHARE on cloud_resourses via FK). Without this, two transactions
		// touch overlapping cloud_resourses rows in different order and Postgres
		// detects a deadlock — observed under prod for account 6c008cf8 on 2026-05-01.
		// On contention we return an error so the MQ layer retries with backoff;
		// on Redis failure we log and proceed (graceful degradation, deadlock risk
		// is reintroduced but the alternative is dropping post-report entirely).
		acquired, release, lockErr := common.TryAcquireSyncLock(context.Background(), job.AccountId)
		if lockErr != nil {
			logger.Warn("postreport: redis unavailable, proceeding without sync lock", "error", lockErr)
		} else if !acquired {
			logger.Info("postreport: sync lock held by another job, retrying via MQ")
			return fmt.Errorf("postreport: sync lock held for account %s", job.AccountId)
		} else {
			defer release(context.Background())
		}

		logger.Info("postreport: processing post-report job")

		jobCtx := security.NewRequestContext(context.Background(), security.NewSecurityContextForSuperAdminWithTenant(job.TenantId), logger, ctx.GetTracer(), ctx.GetMeter())

		// Step 1: Discover and store resources from cloud provider APIs
		_, err = discoverAndStoreAccountResources(jobCtx, job.AccountId)
		if err != nil {
			logger.Error("postreport: failed to discover resources", "error", err)
			// Continue - don't fail the entire job for one step
		}

		// Step 2: Sync recommendations
		_, err = storeAccountRecommendations(jobCtx, job.AccountId)
		if err != nil {
			logger.Error("postreport: failed to sync recommendations", "error", err)
		}

		// Step 3: Trigger metrics sync (publishes to metrics queue)
		StoreMetricesForAllAccounts(jobCtx, job.AccountId)

		// Step 4: Sync event rules from cloud provider
		_, err = StoreEventRules(jobCtx, job.AccountId)
		if err != nil {
			logger.Error("postreport: failed to sync event rules", "error", err)
		}

		logger.Info("postreport: successfully processed post-report job")

		// Notify KG system that data has been updated for this tenant
		publishKGUpdate(job.TenantId)

		return nil
	}

	return common.MqConsume(
		config.Config.RabbitMqCloudAccountPostReportExchange,
		config.Config.RabbitMqCloudAccountPostReportQueue,
		config.Config.RabbitMqCloudAccountPostReportQueue,
		concurrency,
		processor,
	)
}
