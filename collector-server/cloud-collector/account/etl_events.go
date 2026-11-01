package account

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"nudgebee/collector/cloud/common"
	"nudgebee/collector/cloud/config"
	"nudgebee/collector/cloud/kgnotify"
	"nudgebee/collector/cloud/providers"
	"nudgebee/collector/cloud/security"
	"nudgebee/collector/cloud/services"
	"regexp"
	"strings"
	"time"

	"github.com/lib/pq"

	"github.com/google/uuid"
	"github.com/samber/lo"
)

// httpStatusCodePattern matches error messages containing HTTP status codes (e.g. "returned 404:")
var httpStatusCodePattern = regexp.MustCompile(`returned\s+(\d{3})[:\s]`)

type CloudAccountEventsJob struct {
	JobId     string `json:"job_id"`
	AccountId string `json:"account_id"`
	TenantId  string `json:"tenant_id"`
}

// eventSourcesSupportingRules defines event sources that support event rules synchronization.
// This map centralizes the list of sources that require event rule management.
var eventSourcesSupportingRules = map[string]bool{
	"AWS_CloudWatch_Alarm": true,
	"Azure_Monitor_Alert":  true,
	"GCP_Metric_Alert":     true,
	"cloudfoundry":         true,
}

func StoreEventsForAllAccounts(ctx *security.RequestContext) {
	t0 := time.Now()
	ctx.GetLogger().Info("events: starting events job enqueuing for all accounts")

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		ctx.GetLogger().Error("events: unable to get database manager", "error", err)
		return
	}

	accountTenantIds := map[string]string{}
	queryResponse := []map[string]any{}
	err = dbms.QueryAndScan(&queryResponse, "select id::text, tenant::text from cloud_accounts where status = 'active' and lower(cloud_provider) IN ('aws', 'azure', 'gcp', 'cloudfoundry')")
	if err != nil {
		ctx.GetLogger().Error("events: unable to fetch active accounts", "error", err)
		return
	}
	for _, qr := range queryResponse {
		accountTenantIds[qr["id"].(string)] = qr["tenant"].(string)
	}

	if len(accountTenantIds) == 0 {
		ctx.GetLogger().Info("events: no active accounts found")
		return
	}
	ctx.GetLogger().Info("events: fetched active accounts", "count", len(accountTenantIds), "time", time.Since(t0).String())

	// Publish jobs to RabbitMQ
	publishedCount := 0
	failedCount := 0
	for accountId, tenantId := range accountTenantIds {
		job := CloudAccountEventsJob{
			JobId:     uuid.New().String(),
			AccountId: accountId,
			TenantId:  tenantId,
		}
		err = common.MqPublish(config.Config.RabbitMqCloudAccountEventsExchange, config.Config.RabbitMqCloudAccountEventsQueue, job, common.MqPublishWithContext(ctx.GetContext()))
		if err != nil {
			ctx.GetLogger().Error("events: failed to publish job", "error", err, "accountId", accountId, "job_id", job.JobId)
			failedCount++
		} else {
			ctx.GetLogger().Debug("events: published job", "accountId", accountId, "job_id", job.JobId)
			publishedCount++
		}
	}

	ctx.GetLogger().Info("events: finished enqueuing events jobs", "total_time", time.Since(t0).String(), "published", publishedCount, "failed", failedCount)
}

// ConsumeCloudAccountEventsJobs starts a RabbitMQ consumer that processes cloud account events jobs
func ConsumeCloudAccountEventsJobs(ctx *security.RequestContext, concurrency int) error {
	if concurrency <= 0 {
		concurrency = config.Config.CloudCollectorServerEventsWorkersMax
		if concurrency <= 0 {
			concurrency = 1 // fallback default
		}
	}

	ctx.GetLogger().Info("events: starting cloud account events consumer", "concurrency", concurrency, "queue", config.Config.RabbitMqCloudAccountEventsQueue, "exchange", config.Config.RabbitMqCloudAccountEventsExchange)

	// Declare and bind the DLQ so malformed messages routed via sendToDLQWithConfig
	// are retained for inspection instead of being silently discarded by the broker.
	if err := common.MqDeclareDLQ(config.Config.RabbitMqCloudAccountEventsDLQExchange, config.Config.RabbitMqCloudAccountEventsDLQQueue); err != nil {
		ctx.GetLogger().Error("events: failed to declare DLQ", "error", err)
	}

	processor := func(msgCtx context.Context, data []byte) error {
		var job CloudAccountEventsJob
		err := common.UnmarshalJson(data, &job)
		if err != nil {
			// Permanent error - malformed message. Route to DLQ for inspection, then
			// ACK to prevent a poison-message loop.
			ctx.GetLogger().Error("events: failed to unmarshal job - sending to DLQ", "error", err, "data", string(data))
			sendToDLQWithConfig(ctx, data, "unmarshal_error", err,
				config.Config.RabbitMqCloudAccountEventsDLQExchange,
				config.Config.RabbitMqCloudAccountEventsDLQQueue,
			)
			return nil // Return nil to ACK and drop the message
		}

		// Create a new request context for this specific account. msgCtx carries
		// the trace context extracted from the consumed message's headers, so the
		// trace continues from the publisher into event ETL. Build it before any
		// job-level logging so those lines carry the stamped trace_id too.
		jobCtx := security.NewRequestContext(msgCtx, security.NewSecurityContextForSuperAdminWithTenant(job.TenantId), ctx.GetLogger().With("accountId", job.AccountId, "job_id", job.JobId), ctx.GetTracer(), ctx.GetMeter())
		logger := jobCtx.GetLogger()
		logger.Info("events: processing events job")

		// Execute StoreEvents logic
		_, err = StoreEvents(jobCtx, job.AccountId)
		if err != nil {
			// Check if the error indicates a permanent failure (e.g. HTTP 4xx from provider API, or account not found)
			if isPermanentProviderError(err) || strings.Contains(err.Error(), "not found") {
				logger.Error("events: permanent error, dropping message", "error", err)
				return common.NewPermanentError(err)
			}
			// Transient error - may succeed on retry. NACK and requeue.
			logger.Error("events: failed to store events - will retry", "error", err)
			return err
		}

		logger.Info("events: successfully processed events job")
		return nil
	}

	return common.MqConsume(
		config.Config.RabbitMqCloudAccountEventsExchange,
		config.Config.RabbitMqCloudAccountEventsQueue,
		config.Config.RabbitMqCloudAccountEventsQueue,
		concurrency,
		processor,
	)
}

// isPermanentProviderError checks if an error from a cloud provider API indicates
// a permanent failure that will not succeed on retry. This includes HTTP 4xx errors
// (e.g. 400 Bad Request, 401 Unauthorized, 403 Forbidden, 404 Not Found).
func isPermanentProviderError(err error) bool {
	if err == nil {
		return false
	}
	// Type-based detection first: cloud SDK auth failures (expired/revoked
	// credentials, missing permissions) are wrapped errors whose textual form
	// varies by provider — e.g. the Azure SDK renders 401 as "RESPONSE 401:",
	// which the string regex below does not match. These never succeed on retry
	// until an operator rotates the credential, so discard rather than loop.
	if isAuthFailure(err) {
		return true
	}
	matches := httpStatusCodePattern.FindStringSubmatch(err.Error())
	if len(matches) < 2 {
		return false
	}
	// 429 (Too Many Requests) and 408 (Request Timeout) are 4xx but transient —
	// retrying after backoff can succeed, so don't classify them as permanent.
	if matches[1] == "429" || matches[1] == "408" {
		return false
	}
	// Remaining HTTP 4xx status codes are permanent client errors
	return matches[1][0] == '4'
}

// prepareEventForDB takes a providers.Event and related account information,
// and prepares a map suitable for insertion into the 'events' database table.
func prepareEventForDB(ctx *security.RequestContext, event providers.Event, originatingAccount providers.Account, internalDBAccountID string) (map[string]any, error) {
	ctxLogger := ctx.GetLogger().With("originatingAccount", originatingAccount.AccountNumber, "eventId", event.EventId, "component", "prepareEventForDB")

	currentTime := time.Now().UTC().Format(time.RFC3339)
	var nilString *string
	findingType := "issue"
	category := "issue"

	eventDateFormatted := event.Date.Format(time.RFC3339)
	rawJson, err := common.MarshalJson(event.Raw)
	if err != nil {
		ctxLogger.Error("unable to marshal raw event for DB", "error", err)
		rawJson = []byte("{}")
	}

	// Prepare evidences for DB (combine raw and additional context)
	dbEvidences := []map[string]any{
		{
			"type": string(providers.EventEvidenceTypeJson),
			"data": string(rawJson),
			"additional_info": map[string]string{
				"source":      "Event.Raw",
				"action_name": "Raw Event",
				"action_type": "event_detail",
			},
		},
	}
	for _, ev := range event.AdditionalContext {
		// Convert []string insights to structured format for UI compatibility.
		// The UI expects [{message, severity}] objects, not plain strings.
		structuredInsights := make([]map[string]string, 0, len(ev.Insight))
		for _, s := range ev.Insight {
			severity := "Info"
			if strings.HasPrefix(s, "WARNING:") {
				severity = "High"
			} else if strings.HasPrefix(s, "ERROR:") || strings.HasPrefix(s, "CRITICAL:") {
				severity = "Critical"
			}
			structuredInsights = append(structuredInsights, map[string]string{
				"message":  s,
				"severity": severity,
			})
		}
		dbEvidences = append(dbEvidences, map[string]any{
			"type":            string(ev.Type),
			"data":            ev.Data,
			"insight":         structuredInsights,
			"additional_info": ev.AdditionalInfo,
		})
	}

	var description *string
	if event.Description != "" {
		description = &event.Description
	}
	// The events "cluster" field is a generic scoping key; the other cloud
	// producers (k8s-collector cloud findings, spend anomalies) fill it with
	// the account NAME, so using the number here made the same account show
	// up twice in the Cluster facet.
	cluster := originatingAccount.AccountName
	if cluster == "" {
		cluster = originatingAccount.AccountNumber
	}

	subjectName := event.ResourceId
	if subjectName == "" {
		subjectName = event.EventName
	}

	eventMap := map[string]any{
		"updated_at":        eventDateFormatted,
		"title":             event.Title,
		"tenant":            ctx.GetSecurityContext().GetTenantId(),
		"subject_type":      event.ResourceType,
		"subject_node":      event.ResourceRegion,
		"subject_namespace": event.ResourceServiceName,
		"subject_name":      subjectName,
		"status":            string(event.EventStatus),
		"starts_at":         eventDateFormatted,
		"source":            event.EventSource,
		"service_key":       buildExternalResourceId(originatingAccount.CloudProvider, originatingAccount.AccountNumber, event.ResourceRegion, event.ResourceServiceName, event.ResourceType, event.ResourceId, ""),
		"priority":          string(event.EventSeverity),
		"id":                uuid.New().String(),
		"fingerprint":       event.EventId,
		"finding_type":      findingType,
		"finding_id":        event.EventId,
		"failure":           "False",
		"evidences":         dbEvidences,
		"ends_at":           eventDateFormatted,
		"description":       description,
		"created_at":        currentTime,
		"cluster":           cluster,
		"cloud_resource_id": nilString,
		"cloud_account_id":  internalDBAccountID,
		"account_id":        internalDBAccountID,
		"category":          category, // finding_id is now handled in storeMultipleEventsInDB
		"aggregation_key":   event.EventName,
		"principal":         event.Username,
		"labels":            event.Labels,
	}
	// Every event is sent with ends_at = starts_at, which the event service
	// stores as a moment that has already passed. A condition a poll can end is
	// sent with no end time instead, so it is stored firing until it is resolved.
	if event.OngoingScope != "" && event.EventStatus == providers.EventStatusFiring {
		delete(eventMap, "ends_at")
	}
	if len(event.ResourceLookupIds) > 0 {
		eventMap[resourceLookupIdsKey] = event.ResourceLookupIds
	}
	if event.EndsAt != nil {
		eventMap["ends_at"] = event.EndsAt.Format(time.RFC3339)
	}
	return eventMap, nil
}

// processEventsInternal is a centralized function to handle event preparation,
// database storage, and MQ publishing for a batch of events.
func processEventsInternal(ctx *security.RequestContext, dbms *common.DatabaseManager, events []providers.Event, originatingAccount providers.Account, internalDBAccountID string) error {
	funcName := "etl.events.processEventsInternal"
	if len(events) == 0 {
		ctx.GetLogger().Info("no events to process", "component", funcName, "originatingAccount", originatingAccount.AccountNumber, "internalDBAccountID", internalDBAccountID)
		return nil
	}
	ctxLogger := ctx.GetLogger().With("originatingAccount", originatingAccount.AccountNumber, "internalDBAccountID", internalDBAccountID, "component", "processEventsInternal")
	err := storeMultipleEventsInDB(ctx, dbms, originatingAccount, internalDBAccountID, events)
	if err != nil {
		ctxLogger.Error("failed to store events in DB", "error", err)
		return err
	}
	ctxLogger.Info("successfully stored events in DB", "eventCount", len(events))
	return nil
}

func StoreEvents(ctx *security.RequestContext, accountId string) (StoreEventResponse, error) {
	t0 := time.Now()
	// get serviceNames and regions of active resources
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		ctx.GetLogger().Error("unable to get dbms", "error", err)
		return StoreEventResponse{
			Count:    0,
			Duration: time.Since(t0),
		}, err
	}

	jobStatus, err := getAgentJobStatus(ctx, dbms, accountId)
	if err != nil {
		ctx.GetLogger().Error("unable to fetch agent job status", "error", err)
		return StoreEventResponse{
			Count:    0,
			Duration: time.Since(t0),
		}, err
	}
	eventJobStatus := map[string]any{}
	if jobStatus != nil && jobStatus["events"] != nil {
		eventJobStatus = jobStatus["events"].(map[string]any)
	}

	// get startDate and endDate
	startDate := time.Now().Add(-1 * time.Hour).UTC()
	if v, ok := eventJobStatus["end"]; ok {
		startDate, err = time.Parse(time.RFC3339, v.(string))
		startDate = startDate.Add(time.Second)
		if err != nil {
			ctx.GetLogger().Error("unable to parse lastRun", "error", err, "endDate", v)
		}
	}

	events, acc, err := getEventsInternal(ctx, accountId, providers.ListEventRequest{
		StartDate: &startDate,
	})

	if err != nil {
		ctx.GetLogger().Error("unable to fetch events", "error", err)
		_ = updateOrCreateAgentStatus(ctx, accountId, AgentStatusConnected, err.Error(), true, map[string]any{
			"events": map[string]any{
				"err": err.Error(),
			},
		})
		return StoreEventResponse{
			Count:    0,
			Duration: time.Since(t0),
		}, err
	}

	ctx.GetLogger().Info("StoreEvents: fetched events from provider", "accountId", accountId, "cloudProvider", acc.CloudProvider, "eventCount", len(events.Items), "startDate", startDate.Format(time.RFC3339))

	// update agent status
	endDate := time.Now().UTC()
	// What this poll listed is recorded with the rest of the events status: the
	// push path reads it to decide whether a pushed firing may be stored open.
	// The write is not awaited. If it fails, the recorded listing goes stale and
	// the push path falls back to storing moments.
	coverage := ongoingCoverageStatus(events.Ongoing)
	// The recorded end is this poll's time. endDate is reassigned below to the
	// newest event time, which for an alarm is when it last changed state and can
	// be hours old; read through the closure, a late write could record that
	// instead and pull the next poll's window (and its history read) back.
	recordedEnd := endDate
	go func() {
		eventsStatus := map[string]any{
			"count":    len(events.Items),
			"duration": time.Since(t0).String(),
			"start":    startDate.Format(time.RFC3339),
			"end":      recordedEnd.Format(time.RFC3339),
			"err":      "",
		}
		for key, value := range coverage {
			eventsStatus[key] = value
		}
		err := updateOrCreateAgentStatus(ctx, accountId, AgentStatusConnected, "", true, map[string]any{
			"account_number": acc.AccountNumber,
			"events":         eventsStatus,
		})
		if err != nil {
			ctx.GetLogger().Error("Failed to update agent status", "error", err.Error())
		}
	}()

	// Before the empty-batch return: a poll that finds nothing on is exactly the
	// one that has to end what was on.
	keepOngoingWhereListed(events.Items, listedScopes(events.Ongoing))
	if events.Ongoing != nil {
		if err := endFiringsNoLongerOn(ctx, dbms, accountId, *events.Ongoing); err != nil {
			ctx.GetLogger().Error("unable to end firings that are no longer on", "error", err, "accountId", accountId)
		}
	}

	if len(events.Items) == 0 {
		ctx.GetLogger().Info("no events to process")
		return StoreEventResponse{
			Count:    0,
			Duration: time.Since(t0),
		}, nil
	}

	distinctSources := map[string]bool{}
	for _, event := range events.Items {
		distinctSources[event.EventSource] = true
	}

	ctx.GetLogger().Info("StoreEvents: distinct event sources", "sources", lo.Keys(distinctSources), "accountId", accountId)

	// Compute finding_ids for the current batch so we can exclude still-active
	// events from the close step. Without this, long-lived alarms get closed
	// and then filtered out by the dedup check (finding_id already exists),
	// leaving them stuck as CLOSED.
	currentFindingIds := lo.Map(events.Items, func(event providers.Event, _ int) string {
		return event.FiringFindingID()
	})

	// Collect fingerprints of RESOLVED events so their existing FIRING counterparts
	// are not prematurely closed before we can update them to RESOLVED.
	resolvedFingerprints := lo.FilterMap(events.Items, func(event providers.Event, _ int) (string, bool) {
		if event.EventStatus == providers.EventStatusResolved {
			return event.EventId, true
		}
		return "", false
	})

	// Mark previous events as closed, but skip events still present in the current batch
	// and events whose fingerprint matches a RESOLVED event (they'll be resolved, not closed).
	sources := batchClosableSources(events)
	if len(sources) > 0 {
		closeQuery := `UPDATE events SET status = 'CLOSED'
			 WHERE cloud_account_id = $1 AND source = ANY($2) AND status != 'CLOSED'
			 AND finding_id != ALL($3)`
		closeArgs := []any{accountId, pq.Array(sources), pq.Array(currentFindingIds)}

		if len(resolvedFingerprints) > 0 {
			closeQuery += ` AND fingerprint != ALL($4)`
			closeArgs = append(closeArgs, pq.Array(resolvedFingerprints))
		}

		result, err := dbms.Exec(closeQuery, closeArgs...)
		if err != nil {
			ctx.GetLogger().Error("unable to mark previous events as closed", "error", err)
		} else {
			rowsAffected, err := result.RowsAffected()
			if err != nil {
				ctx.GetLogger().Error("unable to get rows affected", "error", err)
			}
			ctx.GetLogger().Info("StoreEvents: marked previous events as closed", "rowsAffected", rowsAffected, "accountId", accountId)
		}
	}

	// Use the centralized processing function
	err = processEventsInternal(ctx, dbms, events.Items, acc, accountId)
	if err != nil {
		// Error already logged by processEventsInternal or its sub-functions
		return StoreEventResponse{Count: 0, Duration: time.Since(t0)}, err
	}

	// unique values from DB
	eventRulesInDb := []string{}
	err = dbms.QueryAndScan(&eventRulesInDb, `select distinct alert 
		from event_rules
		where account_id = $1`, accountId)
	if err != nil {
		ctx.GetLogger().Error("unable to fetch event rules", "error", err)
	} else {
		eventRulesInDbMap := map[string]bool{}
		for _, rule := range eventRulesInDb {
			eventRulesInDbMap[rule] = true
		}

		ctx.GetLogger().Info("StoreEvents: checking for missing event rules", "existingRulesCount", len(eventRulesInDbMap), "accountId", accountId)

		missingRuleInDb := false
		for _, event := range events.Items {
			// Check for missing event rules for supported event sources
			ctx.GetLogger().Debug("StoreEvents: checking event rule", "eventName", event.EventName, "eventSource", event.EventSource, "inDB", eventRulesInDbMap[event.EventName])

			if !eventRulesInDbMap[event.EventName] && eventSourcesSupportingRules[event.EventSource] {
				missingRuleInDb = true
				ctx.GetLogger().Info("StoreEvents: found missing event rule", "eventName", event.EventName, "eventSource", event.EventSource)
				break
			}
		}

		if missingRuleInDb {
			_, err = StoreEventRules(ctx, accountId)
			if err != nil {
				ctx.GetLogger().Error("events: unable to store event rules", "error", err)
			}
		}
	}

	// Find the latest timestamp from all fetched events to ensure the window always moves forward.
	// The API might return events from different sources (CloudTrail, CloudWatch) that are not perfectly sorted together.
	endDate = events.Items[0].Date // Start with the first
	for _, event := range events.Items {
		if event.Date.After(endDate) {
			endDate = event.Date
		}
	}

	// Trigger targeted resource updates for services with created/deleted resources.
	// Full account-wide resource discovery is handled by the post-report consumer.
	if len(events.Summary) > 0 {
		ctx.GetLogger().Info("discovering resources post events sync", "count", slog.AnyValue(events.Summary))
		relisted := false
		for _, summary := range events.Summary {
			if summary.ServiceName != "" && summary.Region != "" && (summary.ResourcesCreated > 0 || summary.ResourceDeleted > 0) {
				ctx.GetLogger().Info("discovering resources post events sync", "serviceName", summary.ServiceName, "region", summary.Region)
				_, err := StoreResources(ctx, accountId, summary.ServiceName, summary.Region)
				if err != nil {
					ctx.GetLogger().Error("unable to discover resources for service post events sync", "error", err, "serviceName", summary.ServiceName, "region", summary.Region)
				}
				relisted = true
			}
		}
		// Resources were created or deleted: ask for this account's graph to be
		// rebuilt once, however many services were re-listed.
		if relisted {
			kgnotify.PublishAccountUpdate(ctx.GetContext(), ctx.GetSecurityContext().GetTenantId(), accountId)
		}
	}

	return StoreEventResponse{
		Count:    len(events.Items),
		Duration: time.Since(t0),
		Start:    startDate,
		End:      endDate,
	}, nil
}

// storeMultipleEventsInDB handles batch insertion of events.
// RESOLVED events update existing FIRING events by fingerprint instead of creating new rows.
func storeMultipleEventsInDB(ctx *security.RequestContext, dbms *common.DatabaseManager, originatingAccount providers.Account, internalDBAccountID string, events []providers.Event) error {
	funcName := "etl.events.storeMultipleEventsInDB"
	if len(events) == 0 {
		ctx.GetLogger().Info("no events to store in DB", "component", funcName, "originatingAccount", originatingAccount.AccountNumber, "internalDBAccountID", internalDBAccountID)
		return nil
	}

	// Separate RESOLVED events from insertable events.
	// RESOLVED events update existing FIRING events; they don't create new rows.
	var resolvedEvents, insertableEvents []providers.Event
	for _, event := range events {
		if event.EventStatus == providers.EventStatusResolved {
			resolvedEvents = append(resolvedEvents, event)
		} else {
			insertableEvents = append(insertableEvents, event)
		}
	}

	// Process insertable (non-RESOLVED) events first using the normal dedup + insert flow.
	if len(insertableEvents) > 0 {
		if err := insertNewEvents(ctx, dbms, originatingAccount, internalDBAccountID, insertableEvents); err != nil {
			return err
		}
	}

	// Resolve existing FIRING events by fingerprint and record history.
	if len(resolvedEvents) > 0 {
		if err := resolveExistingEvents(ctx, dbms, internalDBAccountID, resolvedEvents); err != nil {
			return err
		}
	}

	return nil
}

// insertNewEvents handles the dedup and insertion of non-RESOLVED events.
func insertNewEvents(ctx *security.RequestContext, dbms *common.DatabaseManager, originatingAccount providers.Account, internalDBAccountID string, events []providers.Event) error {
	funcName := "etl.events.insertNewEvents"

	// 1. Compute per-firing finding_ids for incoming events.
	findingIds := lo.Map(events, func(event providers.Event, _ int) string {
		return event.FiringFindingID()
	})

	// 2. Find which of these finding_ids already exist (processed firings)
	existingFindingIds := []string{}
	query := `SELECT finding_id FROM events WHERE cloud_account_id = $1 AND finding_id = ANY($2)`
	err := dbms.QueryAndScan(&existingFindingIds, query, internalDBAccountID, pq.Array(findingIds))
	if err != nil {
		ctx.GetLogger().Error("failed to query for existing events by finding_id", "error", err)
		return err
	}

	existingFindingIdsMap := lo.SliceToMap(existingFindingIds, func(fid string) (string, bool) {
		return fid, true
	})

	// 3. Filter out events whose per-firing finding_id already exists
	eventsToProcess := lo.Filter(events, func(event providers.Event, idx int) bool {
		candidateFindingId := findingIds[idx]
		return !existingFindingIdsMap[candidateFindingId]
	})

	if len(eventsToProcess) == 0 {
		ctx.GetLogger().Info("all incoming events are already active and being tracked; no new events to process", "component", funcName, "totalIncoming", len(events))
		return nil
	}

	eventsToStoreInDB := make([]map[string]any, 0, len(eventsToProcess))
	currentTimeForBatch := time.Now().UTC().Format(time.RFC3339) // Consistent created_at for the batch

	for _, event := range eventsToProcess {
		eventMap, err := prepareEventForDB(ctx, event, originatingAccount, internalDBAccountID)
		if err != nil {
			ctx.GetLogger().Error("failed to prepare event for DB storage", "error", err, "eventId", event.EventId)
			continue
		}
		// finding_id must be unique per firing. Use source-native ID if provided,
		// otherwise combine fingerprint (EventId) with timestamp.
		eventMap["finding_id"] = event.FiringFindingID()

		eventMap["created_at"] = currentTimeForBatch
		if eventMap["id"] == nil {
			eventMap["id"] = uuid.New().String()
		}
		eventsToStoreInDB = append(eventsToStoreInDB, eventMap)
	}

	// Link events to the cloud_resourses row of their resource
	linkCloudResourceIds(ctx, dbms, eventsToStoreInDB, internalDBAccountID)

	_, err = services.InvestigateEvent(ctx.GetSecurityContext().GetTenantId(), eventsToStoreInDB)
	if err != nil {
		ctx.GetLogger().Error("unable to investigate event", "error", err)
		return err
	}

	ctx.GetLogger().Info("insertNewEvents: successfully called InvestigateEvent", "component", funcName, "eventCount", len(eventsToStoreInDB))
	return nil
}

// resolveExistingEvents updates existing FIRING events to RESOLVED by fingerprint
// and records the resolution in event_history.
func resolveExistingEvents(ctx *security.RequestContext, dbms *common.DatabaseManager, accountID string, resolvedEvents []providers.Event) error {
	funcName := "etl.events.resolveExistingEvents"

	// Build fingerprint → resolved event map (last one wins if duplicates)
	fpToEvent := map[string]providers.Event{}
	for _, e := range resolvedEvents {
		fpToEvent[e.EventId] = e
	}
	fps := lo.Keys(fpToEvent)

	// Update existing FIRING events to RESOLVED per fingerprint (each may have a different resolution timestamp)
	var allResolved []resolvedRow

	// A failed fingerprint must not abandon the ones already updated: their rows
	// are committed as RESOLVED, so the next sync's UPDATE skips them and their
	// notification and history would be lost for good. Collect and continue.
	var updateErrs []error

	for _, fp := range fps {
		re := fpToEvent[fp]
		resolvedAt := re.Date.UTC()

		var rows []resolvedRow
		// priority is deliberately NOT written here. Severity describes what fired,
		// not what the row's current state is — `status = 'RESOLVED'` already carries
		// "it's over". This UPDATE used to also set priority = 'INFO', a leftover from
		// when the OK transition inserted its own row (whose severity template renders
		// Info for an OK state, correctly, because that row WAS the recovery notice).
		// Once this path switched to updating the firing row in place, that Info landed
		// on the incident itself and destroyed the source severity — irrecoverably, since
		// nothing else records it. A HIGH alarm that self-recovered then read as INFO,
		// which hid it from the Triage Inbox (it excludes DEBUG/INFO) and made the
		// briefing's Nubi-vs-source severity comparison measure this UPDATE rather than
		// the source.
		// starts_at <= the resolve's own time: a resolve cannot end a firing that
		// began after it. Pushes arrive unordered and more than once, so a resolve
		// from an earlier cycle can land after the next firing is stored; without
		// the bound it would close that firing while it still breaches, and
		// nothing stores it again because its finding_id already exists.
		err := dbms.QueryAndScan(&rows,
			`UPDATE events SET status = 'RESOLVED', updated_at = $3, ends_at = $3
			 WHERE cloud_account_id = $1 AND fingerprint = $2
			 AND status NOT IN ('CLOSED', 'RESOLVED')
			 AND starts_at <= $3
			 RETURNING id::text, tenant::text, cloud_account_id::text, fingerprint`,
			accountID, fp, resolvedAt)
		if err != nil {
			ctx.GetLogger().Error("failed to resolve event by fingerprint", "error", err, "component", funcName, "fingerprint", fp)
			updateErrs = append(updateErrs, err)
			continue
		}
		allResolved = append(allResolved, rows...)
	}

	if len(allResolved) > 0 {
		ctx.GetLogger().Info("resolved existing events by fingerprint", "component", funcName, "count", len(allResolved))
	}

	announceResolved(ctx, dbms, allResolved, func(row resolvedRow) map[string]any {
		re, ok := fpToEvent[row.Fingerprint]
		if !ok {
			return nil
		}
		return map[string]any{
			"resolved_title":       re.Title,
			"resolved_description": re.Description,
			"resolved_labels":      re.Labels,
			"resolved_date":        re.Date.Format(time.RFC3339),
		}
	})

	return errors.Join(updateErrs...)
}

// resolvedRow is an event a resolve has just moved from open to RESOLVED.
type resolvedRow struct {
	ID             string `db:"id"`
	Tenant         string `db:"tenant"`
	CloudAccountID string `db:"cloud_account_id"`
	Fingerprint    string `db:"fingerprint"`
}

// announceResolved publishes the resolved notification and records the history
// row for each event a resolve has just closed. Every resolve in this package
// ends here, so an alarm is announced and recorded the same way whether a
// pushed resolve or a poll saw it clear. metadataFor supplies what the history
// row says about the resolve; a nil map skips the history row.
func announceResolved(ctx *security.RequestContext, dbms *common.DatabaseManager, resolved []resolvedRow, metadataFor func(resolvedRow) map[string]any) {
	funcName := "etl.events.announceResolved"

	// Only a genuine open->resolved transition reaches here (the UPDATEs exclude
	// rows already CLOSED/RESOLVED), so repeat syncs of the same resolved alarm
	// publish nothing. api-server routes this to the resolved reply, threaded
	// under the original alert message.
	for _, row := range resolved {
		if err := common.MqPublish(
			config.Config.RabbitMqEventPostProcessExchange,
			config.Config.RabbitMqEventPostProcessQueue,
			map[string]any{"event_id": row.ID, "notify_resolved": true},
			common.MqPublishWithExpiration(1*time.Hour),
			common.MqPublishWithContext(ctx.GetContext()),
		); err != nil {
			ctx.GetLogger().Error("failed to publish resolved notification", "error", err, "component", funcName, "eventId", row.ID)
		}
	}

	// Record event_history for each resolved event
	for _, row := range resolved {
		metadata := metadataFor(row)
		if metadata == nil {
			continue
		}
		metaJSON, err := json.Marshal(metadata)
		if err != nil {
			ctx.GetLogger().Error("failed to marshal resolution metadata", "error", err, "eventId", row.ID)
			continue
		}

		_, err = dbms.Exec(
			`INSERT INTO event_history
			 (id, event_id, tenant_id, cloud_account_id, change_type, old_value, new_value, change_reason, metadata)
			 VALUES (gen_random_uuid(), $1, $2, $3, 'status',
			         '{"status":"FIRING"}'::jsonb, '{"status":"RESOLVED"}'::jsonb,
			         'cloud_alarm_resolved', $4::jsonb)`,
			row.ID, row.Tenant, row.CloudAccountID, string(metaJSON))
		if err != nil {
			ctx.GetLogger().Error("failed to insert event history for resolution", "error", err, "eventId", row.ID)
		}
	}
}

// hasCloudResourceId checks whether an event already has a non-nil cloud_resource_id.
// A plain nil check (v != nil) is insufficient because prepareEventForDB initialises the
// field with a typed nil (*string)(nil), which Go wraps in a non-nil interface value.
func hasCloudResourceId(e map[string]any) bool {
	v, ok := e["cloud_resource_id"]
	if !ok || v == nil {
		return false
	}
	if ptr, ok := v.(*string); ok {
		return ptr != nil
	}
	return true
}

// resourceLookupIdsKey carries providers.Event.ResourceLookupIds from
// prepareEventForDB to linkCloudResourceIds inside the event map. It is not an
// events column: linkCloudResourceIds removes it before the map leaves the
// collector.
const resourceLookupIdsKey = "_resource_lookup_ids"

// linkCloudResourceIds sets cloud_resource_id on each event to the
// cloud_resourses row that is the event's resource, or leaves it empty.
//
// One resource id is often stored on several rows: an EC2 instance id is the id
// of its EC2 row, its Systems Manager and Compute Optimizer rows, and of
// cost-report rows per usage type; a Lambda function can have a row per region.
// Matching the id alone and keeping whichever row came last attached alarms to
// those other rows. The row is now chosen by chooseEventResource. The link is
// decided here, once, before the event is sent for investigation: what is
// correlated and analysed is computed from it and is not recomputed later.
func linkCloudResourceIds(ctx *security.RequestContext, dbms *common.DatabaseManager, events []map[string]any, accountID string) {
	keys := make([]eventResourceKey, len(events))
	for i, e := range events {
		keys[i] = eventResourceKeyFromMap(e)
		delete(e, resourceLookupIdsKey)
	}

	externalIds := map[string]bool{}
	ids := map[string]bool{}
	for i, e := range events {
		if hasCloudResourceId(e) {
			continue
		}
		for _, id := range keys[i].ids {
			ids[id] = true
		}
		if len(keys[i].ids) > 0 && keys[i].externalId != "" {
			externalIds[keys[i].externalId] = true
		}
	}
	if len(ids) == 0 {
		return
	}

	var candidates []resourceCandidate
	err := dbms.QueryAndScan(&candidates,
		`SELECT id, resourse_id, COALESCE(name, '') AS name,
		        COALESCE(external_resource_id, '') AS external_resource_id,
		        COALESCE(service_name, '') AS service_name, COALESCE(type, '') AS type,
		        COALESCE(region, '') AS region, COALESCE(meta->>'nb_source', '') AS nb_source
		 FROM cloud_resourses
		 WHERE account = $1 AND is_active = true
		   AND (external_resource_id = ANY($2) OR resourse_id = ANY($3) OR name = ANY($3))`,
		accountID, pq.Array(lo.Keys(externalIds)), pq.Array(lo.Keys(ids)))
	if err != nil {
		ctx.GetLogger().Warn("linkCloudResourceIds: failed to query cloud_resourses", "error", err)
		return
	}
	if len(candidates) == 0 {
		return
	}

	linked, tied := 0, 0
	for i, e := range events {
		if hasCloudResourceId(e) || len(keys[i].ids) == 0 {
			continue
		}
		id, ok, isTie := chooseEventResource(keys[i], candidates)
		if isTie {
			tied++
			ctx.GetLogger().Info("linkCloudResourceIds: several rows fit the event equally well, leaving it unlinked",
				"subject", keys[i].ids[0], "serviceName", keys[i].serviceName, "subjectType", keys[i].resourceType)
		}
		if ok {
			e["cloud_resource_id"] = &id
			linked++
		}
	}

	if linked > 0 || tied > 0 {
		ctx.GetLogger().Info("linkCloudResourceIds: linked events to cloud resources", "linked", linked, "tied", tied, "total", len(events))
	}
}

// eventResourceKey is what an event says about its resource.
type eventResourceKey struct {
	externalId   string   // service_key, built like cloud_resourses.external_resource_id
	ids          []string // subject_name, then the provider's lookup ids
	serviceName  string   // subject_namespace
	resourceType string   // subject_type
	region       string   // subject_node
}

func eventResourceKeyFromMap(e map[string]any) eventResourceKey {
	str := func(k string) string { v, _ := e[k].(string); return v }
	key := eventResourceKey{
		externalId:   str("service_key"),
		serviceName:  str("subject_namespace"),
		resourceType: str("subject_type"),
		region:       str("subject_node"),
	}
	if name := str("subject_name"); name != "" {
		key.ids = append(key.ids, name)
	}
	if extra, ok := e[resourceLookupIdsKey].([]string); ok {
		for _, id := range extra {
			if id != "" && !lo.Contains(key.ids, id) {
				key.ids = append(key.ids, id)
			}
		}
	}
	return key
}

// resourceCandidate is a cloud_resourses row that may be an event's resource.
type resourceCandidate struct {
	ID          string `db:"id"`
	ResourceId  string `db:"resourse_id"`
	Name        string `db:"name"`
	ExternalId  string `db:"external_resource_id"`
	ServiceName string `db:"service_name"`
	Type        string `db:"type"`
	Region      string `db:"region"`
	Source      string `db:"nb_source"`
}

// Match tiers, best first. A row of another service is used only when no row of
// the event's own service matches: alarms on custom metrics, and events whose
// service name the inventory spells differently, have nothing better, and
// linking them by id is what they got before.
const (
	tierExactKey = iota
	tierServiceId
	tierServiceName
	tierOtherId
	tierOtherName
	tierNone
)

func candidateTier(key eventResourceKey, c resourceCandidate) int {
	if key.externalId != "" && c.ExternalId == key.externalId {
		return tierExactKey
	}
	sameService := key.serviceName != "" && c.ServiceName == key.serviceName
	switch {
	case sameService && lo.Contains(key.ids, c.ResourceId):
		return tierServiceId
	case sameService && c.Name != "" && lo.Contains(key.ids, c.Name):
		return tierServiceName
	case lo.Contains(key.ids, c.ResourceId):
		return tierOtherId
	case c.Name != "" && lo.Contains(key.ids, c.Name):
		return tierOtherName
	}
	return tierNone
}

// candidateScore breaks ties inside a tier: the event's region first (a
// function has a row per region it was ever seen in), then its resource type
// (an instance's EC2 row over its cost rows per usage type), then a row read
// from the provider's API over one derived from the bill.
func candidateScore(key eventResourceKey, c resourceCandidate) int {
	score := 0
	if key.region != "" && c.Region == key.region {
		score += 4
	}
	if key.resourceType != "" && c.Type == key.resourceType {
		score += 2
	}
	if c.Source != "billing" {
		score++
	}
	return score
}

// chooseEventResource returns the id of the row that is the event's resource.
// When the best rows of the best tier are different rows with the same score,
// it returns none and reports the tie: no link is better than a wrong one.
func chooseEventResource(key eventResourceKey, candidates []resourceCandidate) (id string, ok bool, tie bool) {
	bestTier, bestScore := tierNone, -1
	var best []string
	for _, c := range candidates {
		tier := candidateTier(key, c)
		if tier == tierNone {
			continue
		}
		score := candidateScore(key, c)
		switch {
		case tier < bestTier || (tier == bestTier && score > bestScore):
			bestTier, bestScore, best = tier, score, []string{c.ID}
		case tier == bestTier && score == bestScore && !lo.Contains(best, c.ID):
			best = append(best, c.ID)
		}
	}
	switch len(best) {
	case 0:
		return "", false, false
	case 1:
		return best[0], true, false
	}
	return "", false, true
}

func getEventsInternal(ctx *security.RequestContext, accountId string, filter providers.ListEventRequest) (providers.ListEventResponse, providers.Account, error) {
	account, provider, err := getAccount(ctx, accountId)
	if err != nil {
		ctx.GetLogger().Error("unable to fetch account", "error", err, "accountId", accountId)
		return providers.ListEventResponse{}, providers.Account{}, err
	}
	cloudProvider, ok := providers.GetProvider(provider)
	if !ok {
		return providers.ListEventResponse{}, providers.Account{}, fmt.Errorf("provider not found")
	}
	resources, err := cloudProvider.ListEvents(ctx, account, filter)
	return resources, account, err
}

func StoreEventRules(ctx *security.RequestContext, accountId string) (providers.ListEventRules, error) {
	funcName := "etl.events.StoreEventRules"
	if accountId == "" {
		return providers.ListEventRules{}, errors.New("accountId is required")
	}

	if ctx.GetSecurityContext().GetTenantId() == "" {
		return providers.ListEventRules{}, errors.New("tenantId is required")
	}

	rules, listErr := ListEventRules(ctx, accountId)
	if listErr != nil {
		ctx.GetLogger().Error("unable to list event rules", "error", listErr, "component", funcName, "accountId", accountId, "rulesCount", len(rules.Items))
		// Providers return the rules from the regions that succeeded alongside the
		// error. Storing them is safe — this only upserts, it never removes a rule
		// missing from the list — and dropping them let one failing region keep
		// every rule in the account stale.
		if len(rules.Items) == 0 {
			return rules, listErr
		}
	}

	ctx.GetLogger().Info("StoreEventRules: fetched event rules from provider", "component", funcName, "accountId", accountId, "rulesCount", len(rules.Items))

	if len(rules.Items) == 0 {
		ctx.GetLogger().Info("no event rules to store", "component", funcName, "accountId", accountId)
		return rules, nil
	}

	// get serviceNames and regions of active resources
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		ctx.GetLogger().Error("unable to get dbms", "error", err)
		return providers.ListEventRules{}, err
	}

	tenantId := ctx.GetSecurityContext().GetTenantId()
	args, errorList := buildEventRuleRows(rules.Items, accountId, tenantId, time.Now().UTC().Format(time.RFC3339))
	for _, row := range args {
		if row["ambiguous"] == true {
			ctx.GetLogger().Warn("StoreEventRules: several upstream rules share one name; stored as ambiguous",
				"component", funcName, "accountId", accountId, "alert", row["alert"], "source", row["source"])
		}
	}

	if len(errorList) > 0 {
		return providers.ListEventRules{}, errors.Join(errorList...)
	}

	result, err := upsertEventRuleRows(dbms, args)
	if err != nil {
		ctx.GetLogger().Error("StoreEventRules: failed to insert/update event rules", "error", err, "component", funcName)
	} else {
		rowsAffected, _ := result.RowsAffected()
		ctx.GetLogger().Info("StoreEventRules: successfully stored event rules", "component", funcName, "rowsAffected", rowsAffected, "rulesCount", len(rules.Items))
	}

	// Still surface a partial listing so the caller logs the failed regions.
	return rules, errors.Join(err, listErr)

}

// eventRuleRowKey is the event_rules conflict key within one account and tenant.
func eventRuleRowKey(rule providers.EventRule) string {
	return rule.Source + "\x00" + rule.Name
}

// buildEventRuleRows turns listed rules into event_rules rows, one per
// (account, tenant, source, alert) — the table's unique key; a batch naming a key
// twice fails with "ON CONFLICT DO UPDATE command cannot affect row a second
// time". When several upstream rules collapse into one row (the same alarm name
// in two AWS regions, two Azure resource groups, ...) the row is marked ambiguous
// and carries no external id, so no write lands on an arbitrary one of them.
func buildEventRuleRows(rules []providers.EventRule, accountId, tenantId, now string) ([]map[string]any, []error) {
	errorList := []error{}
	rows := []map[string]any{}
	rowByKey := map[string]map[string]any{}

	for _, rule := range rules {
		key := eventRuleRowKey(rule)
		if row, seen := rowByKey[key]; seen {
			row["ambiguous"] = true
			row["external_rule_id"] = nil
			// Live if any of the collapsed rules is live.
			row["enabled"] = row["enabled"].(bool) || !rule.Disabled
			continue
		}

		annotationsStr, err := common.MarshalJson(map[string]string{
			"summary":     rule.Summary,
			"description": rule.Description,
		})
		if err != nil {
			errorList = append(errorList, fmt.Errorf("marshal annotations for %q: %w", rule.Name, err))
			continue
		}
		labelsStr, err := common.MarshalJson(rule.Labels)
		if err != nil {
			errorList = append(errorList, fmt.Errorf("marshal labels for %q: %w", rule.Name, err))
			continue
		}

		var externalID any
		if rule.ExternalID != "" {
			externalID = rule.ExternalID
		}
		row := map[string]any{
			"id":               uuid.NewString(),
			"created_at":       now,
			"updated_at":       now,
			"tenant_id":        tenantId,
			"account_id":       accountId,
			"alert":            rule.Name,
			"annotations":      string(annotationsStr),
			"expr":             rule.Expr,
			"duration":         fmt.Sprintf("%vs", int(rule.Duration.Seconds())),
			"labels":           string(labelsStr),
			"source":           rule.Source,
			"category":         rule.Category,
			"severity":         rule.Severity,
			"enabled":          !rule.Disabled,
			"external_rule_id": externalID,
			"ambiguous":        false,
			"origin":           eventRuleOriginProvider,
		}
		rowByKey[key] = row
		rows = append(rows, row)
	}
	return rows, errorList
}

// eventRuleOriginProvider marks a rule whose definition the provider owns.
const eventRuleOriginProvider = "provider"

// The sync owns enabled and external_rule_id for these rows: they mirror the
// provider on every run, so a rule turned off in the cloud console reads as off.
const upsertEventRulesBase = `insert into event_rules (id, created_at, updated_at, tenant_id, account_id, alert, annotations, expr, duration, labels, source, category, severity, enabled, external_rule_id%s)
		values (:id, :created_at, :updated_at, :tenant_id, :account_id, :alert, :annotations, :expr, :duration, :labels, :source, :category, :severity, :enabled, :external_rule_id%s)
		-- Must name exactly the columns of event_rules_account_tenant_source_alert_key
		-- (migration V873). Postgres infers the arbiter index by exact column match, so a
		-- stale target raises 42P10 at plan time and fails the whole batch.
		on conflict (account_id, tenant_id, source, alert)
		do update set updated_at = excluded.updated_at, alert = excluded.alert, annotations = excluded.annotations, expr = excluded.expr, duration = excluded.duration, labels = excluded.labels, source = excluded.source, category = excluded.category, severity = excluded.severity,
			enabled = excluded.enabled, external_rule_id = excluded.external_rule_id%s`

var (
	upsertEventRulesSQL = fmt.Sprintf(upsertEventRulesBase, ", ambiguous, origin", ", :ambiguous, :origin",
		", ambiguous = excluded.ambiguous, origin = excluded.origin")
	// upsertEventRulesLegacySQL runs while the ambiguous/origin columns (migration
	// V941) do not exist yet, so the collector can roll out before the migration.
	upsertEventRulesLegacySQL = fmt.Sprintf(upsertEventRulesBase, "", "", "")
)

func upsertEventRuleRows(dbms *common.DatabaseManager, rows []map[string]any) (sql.Result, error) {
	result, err := dbms.NamedExec(upsertEventRulesSQL, rows)
	var pqErr *pq.Error
	if err != nil && errors.As(err, &pqErr) && pqErr.Code == "42703" {
		return dbms.NamedExec(upsertEventRulesLegacySQL, rows)
	}
	return result, err
}
