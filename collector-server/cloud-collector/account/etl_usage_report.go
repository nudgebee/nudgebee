package account

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"nudgebee/collector/cloud/common"
	"nudgebee/collector/cloud/config"
	"nudgebee/collector/cloud/kgnotify"
	"nudgebee/collector/cloud/providers"
	"nudgebee/collector/cloud/security"
	"runtime/debug"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/samber/lo"
)

type CloudAccountCostReportJob struct {
	JobId     string `json:"job_id"`
	AccountId string `json:"account_id"`
	TenantId  string `json:"tenant_id"`
	Month     int    `json:"month"`
	Year      int    `json:"year"`
	// Backfill, when true, signals the consumer to also discover and enqueue jobs
	// for the available historical billing periods after processing this month.
	// Only set at new-account onboarding; fanned-out historical jobs have it false.
	Backfill bool `json:"backfill"`
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

	publishErr := common.MqPublish(dlqExchange, dlqQueue, dlqMessage, common.MqPublishWithContext(ctx.GetContext()))
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
		err = common.MqPublish(config.Config.RabbitMqCloudAccountCostReportExchange, config.Config.RabbitMqCloudAccountCostReportQueue, currentMonthJob, common.MqPublishWithContext(ctx.GetContext()))
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
			err = common.MqPublish(config.Config.RabbitMqCloudAccountCostReportExchange, config.Config.RabbitMqCloudAccountCostReportQueue, previousMonthJob, common.MqPublishWithContext(ctx.GetContext()))
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

	processor := func(msgCtx context.Context, data []byte) error {
		var job CloudAccountCostReportJob
		err := common.UnmarshalJson(data, &job)
		if err != nil {
			// Permanent error - malformed message. Send to DLQ instead of retrying.
			ctx.GetLogger().Error("usagereport: failed to unmarshal job - sending to DLQ", "error", err, "data", string(data))
			sendToDLQ(ctx, data, "unmarshal_error", err)
			return nil // Return nil to ACK and remove from main queue
		}

		// Create a new request context for this specific account. msgCtx carries
		// the trace context extracted from the consumed message's headers, so the
		// trace continues from the publisher into cost-report ETL. Build it before
		// any job-level logging so those lines carry the stamped trace_id too.
		jobCtx := security.NewRequestContext(msgCtx, security.NewSecurityContextForSuperAdminWithTenant(job.TenantId), ctx.GetLogger().With("accountId", job.AccountId, "month", job.Month, "year", job.Year, "job_id", job.JobId), ctx.GetTracer(), ctx.GetMeter())
		logger := jobCtx.GetLogger()
		logger.Info("usagereport: processing cost report job")

		// Execute StoreUsage logic
		_, err = StoreUsage(jobCtx, job.AccountId, time.Month(job.Month), job.Year)
		if err != nil {
			// Send to DLQ instead of retrying indefinitely
			logger.Error("usagereport: failed to store usage report - sending to DLQ", "error", err)
			sendToDLQ(jobCtx, data, "processing_error", err)
			return nil // Return nil to ACK and remove from main queue
		}

		// For onboarding jobs, also backfill the historical billing periods that
		// already exist in the provider's usage-report storage. This runs in-process
		// and sequentially (see runHistoricalBackfill) on a bounded context so a
		// hung downstream call can't leak the worker. Best-effort: failures here
		// must not fail the job (the requested month was already processed).
		if job.Backfill {
			backfillCtx, cancel := context.WithTimeout(context.Background(), costBackfillTimeout)
			backfillJobCtx := security.NewRequestContext(backfillCtx, security.NewSecurityContextForSuperAdminWithTenant(job.TenantId), logger, ctx.GetTracer(), ctx.GetMeter())
			runHistoricalBackfill(backfillJobCtx, job.AccountId, job.TenantId, time.Month(job.Month), job.Year)
			cancel()
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

// costBackfillTimeout bounds the in-process historical backfill so a hung
// downstream S3/DB call can't leak the consumer worker indefinitely.
const costBackfillTimeout = 30 * time.Minute

// StartHistoricalBackfill runs the historical backfill detached from the caller,
// on its own bounded context.
//
// The onboarding consumer can afford to run runHistoricalBackfill inline because
// it is a queue worker. The HTTP onboarding path cannot: discovery plus a month
// at a time of report processing routinely outlasts any sane request timeout, so
// holding the request open would either time out the caller or tie up the
// handler for up to costBackfillTimeout.
//
// Best-effort by the same rules as the consumer's call: the requested month has
// already been stored by the time this runs, so a failure here costs history,
// not the current month.
func StartHistoricalBackfill(ctx *security.RequestContext, accountId, tenantId string, processedMonth time.Month, processedYear int) {
	logger := ctx.GetLogger()
	if logger == nil {
		logger = slog.Default()
	}
	tracer, meter := ctx.GetTracer(), ctx.GetMeter()
	go func() {
		// The consumer's inline call to runHistoricalBackfill is covered by the
		// MQ consumer's recover; a bare goroutine has no such umbrella, so a
		// panic anywhere downstream would take the whole collector process down
		// rather than costing one account its history.
		defer func() {
			if r := recover(); r != nil {
				logger.Error("usagereport: historical backfill panicked", "panic", r, "accountId", accountId, "stack", string(debug.Stack()))
			}
		}()
		backfillCtx, cancel := context.WithTimeout(context.Background(), costBackfillTimeout)
		defer cancel()
		backfillJobCtx := security.NewRequestContext(backfillCtx, security.NewSecurityContextForSuperAdminWithTenant(tenantId), logger, tracer, meter)
		runHistoricalBackfill(backfillJobCtx, accountId, tenantId, processedMonth, processedYear)
	}()
}

// postReportProcessingTimeout bounds a single post-report job safely under the
// RabbitMQ consumer_timeout (30m default). Staying under it means a slow account
// returns a retriable error through the normal retry path, instead of running
// long enough for the broker to force-close the channel and redeliver the
// message — where the redelivery is indistinguishable from a pod crash and gets
// misclassified as a poison message and dropped to the DLQ.
const postReportProcessingTimeout = 25 * time.Minute

// runHistoricalBackfill discovers the historical billing periods available for an
// account and processes each one in-process, sequentially (skipping the month the
// onboarding job already handled). Processing serially rather than fanning jobs
// back onto the queue avoids per-account sync-lock contention: StoreUsage takes a
// non-blocking lock and silently skips when it can't acquire it, so concurrent
// same-account jobs would otherwise be dropped. It is best-effort: providers that
// don't support period discovery (Azure, GCP) or any discovery error simply result
// in no backfill, and a single month's failure is logged without aborting the rest.
func runHistoricalBackfill(ctx *security.RequestContext, accountId, tenantId string, processedMonth time.Month, processedYear int) {
	if accountId == "" || tenantId == "" {
		ctx.GetLogger().Warn("usagereport: backfill skipped, empty accountId or tenantId", "accountId", accountId, "tenantId", tenantId)
		return
	}

	maxMonths := config.Config.CloudCollectorServerCostBackfillMonths
	if maxMonths <= 0 {
		return
	}

	account, _, err := getAccount(ctx, accountId)
	if err != nil {
		ctx.GetLogger().Warn("usagereport: backfill skipped, unable to fetch account", "error", err, "accountId", accountId)
		return
	}
	cloudProvider, ok := providers.GetProvider(strings.ToLower(account.CloudProvider))
	if !ok {
		return
	}
	discoverer, ok := cloudProvider.(providers.UsageReportPeriodDiscoverer)
	if !ok {
		ctx.GetLogger().Debug("usagereport: provider does not support backfill discovery", "provider", account.CloudProvider)
		return
	}

	periods, err := discoverer.GetAvailableUsageReportPeriods(ctx, account, maxMonths)
	if err != nil {
		ctx.GetLogger().Warn("usagereport: backfill discovery failed", "error", err, "accountId", accountId)
		return
	}

	processed := 0
	for _, p := range periods {
		// Skip the month already processed by the onboarding job.
		if p.Month == processedMonth && p.Year == processedYear {
			continue
		}
		if err := ctx.GetContext().Err(); err != nil {
			ctx.GetLogger().Warn("usagereport: backfill stopped before completion", "error", err, "accountId", accountId, "processed", processed)
			return
		}
		if _, err := StoreUsage(ctx, accountId, p.Month, p.Year); err != nil {
			ctx.GetLogger().Error("usagereport: backfill month failed", "error", err, "accountId", accountId, "month", p.Month, "year", p.Year)
			continue
		}
		processed++
	}
	ctx.GetLogger().Info("usagereport: historical backfill complete", "accountId", accountId, "discovered", len(periods), "processed", processed, "maxMonths", maxMonths)
}

// isBenignCostOutcome reports whether a spend-sync error is a steady state
// rather than a fault:
//
//   - ErrCostNotConfigured — no Cost & Usage Report or billing export attached.
//     Onboarding accepts this, so the account can sit here indefinitely.
//   - ErrUnsupported — the provider has no billing concept at all.
//
// Neither can be fixed by retrying, so the queue must ACK rather than
// dead-letter, and the agent must not be reported disconnected for it.
//
// A spend sync answers two different questions, and this function and
// agentStatusForUsageSync below are the only places that answer them:
//
//   - isBenignCostOutcome: is this a steady state the queue should ACK without
//     a dead-letter entry?
//   - agentStatusForUsageSync: does the account's credential still work?
//
// Every benign outcome also keeps the account connected. The reverse is not
// true: ErrCostFetchFailedCredentialOK is a real failure for the queue, but the
// credential was confirmed to work, so the account stays connected. Keep both
// answers here, side by side — two copies of the same answer is how the
// sts:ExternalId drift happened (#37513).
func isBenignCostOutcome(err error) bool {
	return errors.Is(err, errors.ErrUnsupported) || errors.Is(err, providers.ErrCostNotConfigured)
}

// agentStatusForUsageSync decides whether a spend-sync outcome should mark the
// whole agent disconnected.
//
// A genuine failure does. A benign outcome does not: onboarding accepts an
// account with no Cost & Usage Report, so that account is working as intended
// and everything except spend syncs normally. Reporting it as disconnected puts
// a red "The Agent is not connected" banner above a feature table where every
// row reads Connected, and trips agent-health alerting for a supported
// configuration.
//
// A failure after which the provider confirmed the credential still works
// (ErrCostFetchFailedCredentialOK) does not disconnect either: the account's
// other syncs keep working, and only its spend is missing this run.
//
// The error is still recorded either way, so the reason reaches the Spends
// error column.
func agentStatusForUsageSync(err error) AgentStatus {
	if err != nil && !isBenignCostOutcome(err) && !errors.Is(err, providers.ErrCostFetchFailedCredentialOK) {
		return AgentStatusDisconnected
	}
	return AgentStatusConnected
}

func StoreUsage(ctx *security.RequestContext, accountId string, month time.Month, year int) (StoreUsageReportResponse, error) {
	// Capture month-of-call before any defers so the post-report publish is
	// stable across mid-run month rollovers.
	callTime := time.Now()
	currentMonth := callTime.Month()
	currentYear := callTime.Year()

	// Defer the post-report publish so it runs AFTER the sync lock is released
	// (defers fire LIFO; this is registered before the release defer below).
	// Publishing while still holding the lock guaranteed contention with the
	// post-report consumer, which would race-acquire-fail the moment we
	// published — the original cause of recommendations going stale.
	var shouldPublishPostReport bool
	defer func() {
		if shouldPublishPostReport {
			publishPostReportJob(ctx, accountId, month, year, currentMonth, currentYear)
		}
	}()

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
	var usageReportResponse StoreUsageReportResponse
	var account providers.Account
	defer func() {
		ctx.GetLogger().Info("usagereport: stored usage report completed", "time", time.Since(t0).String())
		msg := ""
		if err != nil {
			msg = err.Error()
		}
		agentStatus := agentStatusForUsageSync(err)
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

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		ctx.GetLogger().Error("usagereport: unable to get dbms", "error", err)
		usageReportResponse = StoreUsageReportResponse{
			Count:    0,
			Duration: time.Since(t0),
		}
		return usageReportResponse, err
	}

	// Stream the report into cloud_account_usage_report a batch at a time; the
	// rest of the sync reads it back in SQL (see usage_report_derive.go).
	var sink *usageReportSink
	account, cloudProvider, err := resolveUsageProvider(ctx, accountId)
	if err == nil {
		sink = newUsageReportSink(ctx, dbms, accountId, account, month, year)
		err = streamUsageReport(ctx, cloudProvider, account, month, year, sink.write)
		if err != nil {
			sink.discard()
		}
	}
	if err != nil && sink != nil && sink.err != nil {
		ctx.GetLogger().Error("usagereport: unable to insert usage report", "error", err)
		usageReportResponse = StoreUsageReportResponse{
			Count:    0,
			Duration: time.Since(t0),
		}
		return usageReportResponse, err
	}
	if err != nil {
		// Two benign cases the queue must not retry or dead-letter:
		//   ErrUnsupported       — the provider has no billing concept at all.
		//   ErrCostNotConfigured — the account has no CUR / billing export
		//                          attached. Cost is optional at onboarding, so
		//                          an account can sit here indefinitely.
		// Both are steady states, so retrying cannot help and dead-lettering
		// would poison one message per account per day, forever. Everything
		// else (revoked permissions, an unreadable bucket, a BigQuery error) is
		// a real fault that should still surface in the DLQ.
		switch {
		case errors.Is(err, errors.ErrUnsupported):
			ctx.GetLogger().Debug("usagereport: service does not support usage reports", "accountId", accountId)
		case errors.Is(err, providers.ErrCostNotConfigured):
			ctx.GetLogger().Info("usagereport: no cost reporting configured for account", "accountId", accountId)
		default:
			ctx.GetLogger().Error("usagereport: unable to fetch usage report", "error", err)
		}
		// A spend/billing fetch failure must NOT zero out the rest of the account.
		// Resource discovery, recommendations, metrics, and event-rule sync run in
		// the post-report job and do not depend on billing data — so publish it here
		// the same way the no-billing-data path below does. Otherwise one misconfigured
		// or unreadable billing export (wrong table, bad schema, revoked BigQuery
		// access) silently blocks resource inventory, recommendations, and
		// event-to-resource linkage for the whole account.
		shouldPublishPostReport = true
		usageReportResponse = StoreUsageReportResponse{
			Count:    0,
			Duration: time.Since(t0),
		}
		// `err` stays set either way so the agent-status defer above still
		// records spends as disconnected with the reason — only the value
		// handed back to the queue differs.
		if isBenignCostOutcome(err) {
			return usageReportResponse, nil
		}
		return usageReportResponse, err
	}

	// If no billing data found, publish post-report job and return early.
	// Post-report (resource discovery, recommendations, metrics) still runs for accounts
	// with no billing (free tier, new accounts, disabled billing APIs).
	if sink.count() == 0 {
		shouldPublishPostReport = true
		return StoreUsageReportResponse{}, nil
	}
	ctx.GetLogger().Info("usagereport: stored usage report", "time", time.Since(t0).String(), "count", sink.count())

	err = sink.replacePreviousRuns()
	if err != nil {
		usageReportResponse = StoreUsageReportResponse{
			Count:    0,
			Duration: time.Since(t0),
		}
		return usageReportResponse, err
	}

	// update resources
	resourceCnt := 0
	if month == currentMonth && year == currentYear {
		resourceCnt, err = deriveBillingResources(ctx, dbms, accountId, account, sink)
		if err != nil {
			usageReportResponse = StoreUsageReportResponse{
				Count:    0,
				Duration: time.Since(t0),
			}
			return usageReportResponse, err
		}
		ctx.GetLogger().Info("usagereport: updated resources from usage report", "time", time.Since(t0).String(), "count", resourceCnt)
	}

	// trigger notifications
	err = sendDailySpendNotification(ctx, dbms, accountId, account.AccountName, sink)
	if err != nil {
		ctx.GetLogger().Error("usagereport: unable to send notification", "error", err)
	}

	// update spends
	spendCount, err := deriveSpends(ctx, dbms, accountId, sink)
	if err != nil {
		usageReportResponse = StoreUsageReportResponse{
			Count:    0,
			Duration: time.Since(t0),
		}
		return usageReportResponse, err
	}
	ctx.GetLogger().Info("usagereport: updated spends from usage report", "time", time.Since(t0).String(), "count", spendCount)

	// Publish post-report job AFTER all DB writes (resources + spends) are complete.
	// The post-report job UPSERTs cloud_resourses, and the spends INSERT takes KEY SHARE
	// FK locks on cloud_resourses, so concurrent execution can deadlock.
	// In-job ordering alone is not sufficient because a different StoreUsage invocation
	// for the same account (e.g. previous-month backfill) can run between this job's
	// release of the sync lock and the post-report consumer picking up the message.
	// The post-report consumer therefore acquires the same per-account sync lock; see
	// ConsumeCloudAccountPostReportJobs.
	//
	// The actual publish is deferred until after the lock release defer fires, so the
	// post-report consumer doesn't race-acquire-fail against this invocation's lock.
	shouldPublishPostReport = true

	usageReportResponse = StoreUsageReportResponse{
		Count:         sink.count(),
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
		common.MqPublishWithContext(ctx.GetContext()),
	)
	if publishErr != nil {
		ctx.GetLogger().Error("usagereport: failed to publish post-report job", "error", publishErr, "accountId", accountId)
	} else {
		ctx.GetLogger().Info("usagereport: published post-report job", "accountId", accountId, "job_id", postReportJob.JobId)
	}
}

// dailySpendSummary holds the aggregated figures sendDailySpendNotification reports.
func sendDailySpendNotification(ctx *security.RequestContext, dbms *common.DatabaseManager, accountId, accountName string, sink *usageReportSink) error {
	// its for previous day as we get data for last date.
	// Anchor in UTC to match the UTC end dates stored for billing lines.
	yesterdayDate := time.Now().UTC().AddDate(0, 0, -1)
	summary, err := summarizeDailySpend(dbms, accountId, sink, yesterdayDate)
	if err != nil {
		return err
	}
	totalSpendOfDay := summary.totalDay
	totalSpendOfMonth := summary.totalMonth
	costCurrency := summary.currency
	if costCurrency == "" {
		costCurrency = "USD"
	}
	if summary.mixedCurrencies {
		ctx.GetLogger().Warn("usagereport: mixed currencies detected in usage report, cost totals may be inaccurate",
			"account_id", accountId, "primary_currency", costCurrency)
	}
	dailyItemMap := summary.topDailyItems
	serviceItems := summary.topMonthlyServices

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
				"top_5_monthly_services": serviceItems,
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

	err = common.MqPublish(config.Config.RabbitMqNotificationsExchange, config.Config.RabbitMqNotificationsQueue, message, common.MqPublishWithContext(ctx.GetContext()))
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
	// StoreRecommendations matches service names case-insensitively, so two
	// spellings of one service ("awswaf", "AWSWAF") would sync it twice.
	services = lo.UniqBy(services, strings.ToLower)

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

	ctx.GetLogger().Info("usagereport: synced recommendations after usage report", "time", time.Since(t0).String())
	return data, nil
}

// storeNativeAccountRecommendations syncs the cloud provider's own recommendations
// (account-level, not in cloud_resourses). The post-report job runs it before
// resource discovery: it needs only a few account-level calls, and run after a
// slow discovery it was the step left with no time budget.
// AWS: costoptimizationhub, costexplorer, computeoptimizer, trustedadvisor
// GCP: recommender
// Azure: advisor
func storeNativeAccountRecommendations(ctx *security.RequestContext, accountId string) map[string]StoreUsageRecommendationResponse {
	t0 := time.Now()
	data := map[string]StoreUsageRecommendationResponse{}
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

	ctx.GetLogger().Info("usagereport: synced native recommendations", "time", time.Since(t0).String())
	return data
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

	// Ensure provider default services are always synced, even if they have no
	// rows in cloud_resourses yet. This covers services like GCP Persistent Disks
	// and NICs that never appear in billing data under their own service name.
	var defaultServices []string
	account, _, accErr := getAccount(ctx, accountId)
	if accErr != nil {
		ctx.GetLogger().Error("usagereport: unable to fetch account for default services check", "error", accErr, "accountId", accountId)
	} else {
		defaultServices = providerDefaultServices[strings.ToLower(account.CloudProvider)]
	}

	serviceRegionsMaps, addedDefaults := groupServiceRegions(serviceAndRegions, defaultServices)
	for _, defaultSvc := range addedDefaults {
		ctx.GetLogger().Info("usagereport: adding missing default service for discovery", "service", defaultSvc, "provider", account.CloudProvider, "accountId", accountId)
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

// groupServiceRegions groups an account's stored (service_name, region) rows into
// the region list to scan per service, and adds the default services that have no
// rows. Names are matched case-insensitively: the cost report stores a service under
// one spelling ("awswaf") and its lister under another ("AWSWAF"), and grouped
// apart each spelling ran its own full scan of the same service. StoreResources
// archives by lower(service_name), so one scan under either spelling covers the rows
// of both. A spelling that matches a default is preferred, so the service is scanned
// under the name its lister stores. addedDefaults lists the defaults that had no rows.
func groupServiceRegions(rows []map[string]any, defaults []string) (serviceRegions map[string][]string, addedDefaults []string) {
	defaultByFold := make(map[string]string, len(defaults))
	for _, d := range defaults {
		defaultByFold[strings.ToLower(d)] = d
	}

	serviceRegions = map[string][]string{}
	nameByFold := map[string]string{}
	for _, r := range rows {
		serviceName := r["service_name"].(string)
		// Normalize Azure service names - some resources may have full type paths
		// For microsoft.insights/actiongroups, microsoft.insights/metricalerts etc, use base service name
		if strings.HasPrefix(strings.ToLower(serviceName), "microsoft.insights/") {
			serviceName = "microsoft.insights"
		}

		fold := strings.ToLower(serviceName)
		name, ok := nameByFold[fold]
		if !ok {
			name = serviceName
			if d, isDefault := defaultByFold[fold]; isDefault {
				name = d
			}
			nameByFold[fold] = name
			serviceRegions[name] = []string{}
		}
		if region := r["region"].(string); !slices.Contains(serviceRegions[name], region) {
			serviceRegions[name] = append(serviceRegions[name], region)
		}
	}

	for _, d := range defaults {
		fold := strings.ToLower(d)
		if _, exists := nameByFold[fold]; !exists {
			nameByFold[fold] = d
			serviceRegions[d] = []string{}
			addedDefaults = append(addedDefaults, d)
		}
	}
	return serviceRegions, addedDefaults
}

// CloudAccountPostReportJob represents an async job for post-report processing
// (resource discovery, recommendations, metrics sync) that runs after the cost report is stored.
type CloudAccountPostReportJob struct {
	JobId     string `json:"job_id"`
	AccountId string `json:"account_id"`
	TenantId  string `json:"tenant_id"`
}

// postReportLockWait bounds one attempt's wait for a cost sync of the same
// account. It is part of postReportProcessingTimeout, so it has to leave room
// for the job's own steps; a longer sync is waited out across MQ retries.
const postReportLockWait = 8 * time.Minute

// ConsumeCloudAccountPostReportJobs starts a RabbitMQ consumer that processes post-report jobs.
// These jobs handle resource discovery, recommendations, and metrics sync asynchronously
// after the cost report has been stored, to avoid holding large report data in memory
// during these heavyweight operations.
func ConsumeCloudAccountPostReportJobs(ctx *security.RequestContext, concurrency int) error {
	if concurrency <= 0 {
		concurrency = 1
	}

	ctx.GetLogger().Info("postreport: starting cloud account post-report consumer", "concurrency", concurrency, "queue", config.Config.RabbitMqCloudAccountPostReportQueue, "exchange", config.Config.RabbitMqCloudAccountPostReportExchange)

	processor := func(msgCtx context.Context, data []byte) error {
		var job CloudAccountPostReportJob
		err := common.UnmarshalJson(data, &job)
		if err != nil {
			ctx.GetLogger().Error("postreport: failed to unmarshal job - dropping message", "error", err, "data", string(data))
			return nil // ACK to prevent poison message loop
		}

		// Bound the whole job (lock wait + all steps) safely under the RabbitMQ
		// consumer_timeout. The steps below are I/O-bound and honour this context, so
		// a slow account is cut off and the job returns a retriable error instead of
		// overrunning the broker timeout — see postReportProcessingTimeout.
		// WithoutCancel detaches from cancellation (so a shutdown mid-job doesn't
		// abort it) while retaining trace/baggage values; the timeout then bounds
		// it. Based on msgCtx — the trace context extracted from the consumed
		// message's headers — so the distributed trace continues into post-report
		// ETL (and, as before, the job survives consumer shutdown).
		procCtx, cancel := context.WithTimeout(context.WithoutCancel(msgCtx), postReportProcessingTimeout)
		defer cancel()

		// procCtx is derived from msgCtx (above), so the trace extracted from the
		// consumed message's headers continues from the publisher into post-report
		// ETL. Build the job context before any job-level logging so those lines
		// carry the stamped trace_id too.
		jobCtx := security.NewRequestContext(procCtx, security.NewSecurityContextForSuperAdminWithTenant(job.TenantId), ctx.GetLogger().With("accountId", job.AccountId, "job_id", job.JobId), ctx.GetTracer(), ctx.GetMeter())
		logger := jobCtx.GetLogger()

		// Acquire the per-account sync lock before doing any DB writes. This serializes
		// post-report (cloud_resourses UPSERT) against StoreUsage (spends INSERT, which
		// takes KEY SHARE on cloud_resourses via FK). Without this, two transactions
		// touch overlapping cloud_resourses rows in different order and Postgres
		// detects a deadlock — observed under prod for account 6c008cf8 on 2026-05-01.
		//
		// Block-wait for the lock: bouncing the message back through MQ on contention
		// burned the retry budget in microseconds (republishWithDelay's per-message
		// TTL doesn't actually delay redelivery), causing post-report to be discarded
		// after 3 retries — leaving recommendations stuck for days. The blocking
		// acquire keeps the AMQP delivery in-flight while we wait. The holder
		// renews the lock while it runs and a dead holder's lock expires within
		// minutes (common.TryAcquireSyncLock), so the wait is always on live
		// work; a sync longer than one wait is waited out across MQ retries.
		release, lockErr := common.AcquireSyncLockBlocking(procCtx, job.AccountId, postReportLockWait)
		if errors.Is(lockErr, common.ErrLockTimeout) {
			logger.Warn("postreport: gave up waiting for sync lock, will retry via MQ", "timeout", postReportLockWait.String())
			return lockErr
		}
		if lockErr != nil {
			// Redis unavailable: graceful degradation. Deadlock risk is
			// reintroduced but the alternative is dropping post-report entirely.
			logger.Warn("postreport: redis unavailable, proceeding without sync lock", "error", lockErr)
		} else {
			defer release(context.Background())
		}

		logger.Info("postreport: processing post-report job")

		// Step 1: Sync the cloud provider's own recommendations. They don't depend on
		// this run's resource discovery, so they go first and a slow discovery can't
		// use up their share of the time budget.
		storeNativeAccountRecommendations(jobCtx, job.AccountId)

		// Step 2: Discover and store resources from cloud provider APIs
		_, err = discoverAndStoreAccountResources(jobCtx, job.AccountId)
		if err != nil {
			logger.Error("postreport: failed to discover resources", "error", err)
			// Continue - don't fail the entire job for one step
		}

		// Step 3: Sync resource-based recommendations over the refreshed inventory
		_, err = storeAccountRecommendations(jobCtx, job.AccountId)
		if err != nil {
			logger.Error("postreport: failed to sync recommendations", "error", err)
		}

		// Step 4: Trigger metrics sync (publishes to metrics queue)
		StoreMetricesForAllAccounts(jobCtx, job.AccountId)

		// Step 5: Sync event rules from cloud provider
		_, err = StoreEventRules(jobCtx, job.AccountId)
		if err != nil {
			logger.Error("postreport: failed to sync event rules", "error", err)
		}

		// The steps above swallow their own errors to stay best-effort, so an expired
		// budget would otherwise be ACKed as success with partial work. Detect it and
		// return a retriable error so the job is retried cleanly rather than dropped.
		if procCtx.Err() != nil {
			logger.Warn("postreport: processing exceeded time budget, will retry via MQ", "timeout", postReportProcessingTimeout.String())
			return fmt.Errorf("postreport: processing exceeded %s budget: %w", postReportProcessingTimeout, procCtx.Err())
		}

		logger.Info("postreport: successfully processed post-report job")

		// Ask the knowledge graph to rebuild this account from the refreshed inventory.
		kgnotify.PublishAccountUpdate(procCtx, job.TenantId, job.AccountId)

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
