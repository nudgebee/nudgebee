package account

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"nudgebee/collector/cloud/common"
	"nudgebee/collector/cloud/config"
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
		err = common.MqPublish(config.Config.RabbitMqCloudAccountEventsExchange, config.Config.RabbitMqCloudAccountEventsQueue, job)
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

	processor := func(data []byte) error {
		var job CloudAccountEventsJob
		err := common.UnmarshalJson(data, &job)
		if err != nil {
			// Permanent error - malformed message. ACK to prevent poison message loop.
			// TODO: Send to DLQ for inspection
			ctx.GetLogger().Error("events: failed to unmarshal job - dropping message", "error", err, "data", string(data))
			return nil // Return nil to ACK and drop the message
		}

		logger := ctx.GetLogger().With("accountId", job.AccountId, "job_id", job.JobId)
		logger.Info("events: processing events job")

		// Create a new request context for this specific account
		jobCtx := security.NewRequestContext(context.Background(), security.NewSecurityContextForSuperAdminWithTenant(job.TenantId), logger, ctx.GetTracer(), ctx.GetMeter())

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
	matches := httpStatusCodePattern.FindStringSubmatch(err.Error())
	if len(matches) < 2 {
		return false
	}
	// HTTP 4xx status codes are permanent client errors
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
	subjectName := event.ResourceId
	if subjectName == "" {
		subjectName = event.EventName
	}

	return map[string]any{
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
		"cluster":           originatingAccount.AccountNumber,
		"cloud_resource_id": nilString,
		"cloud_account_id":  internalDBAccountID,
		"account_id":        internalDBAccountID,
		"category":          category, // finding_id is now handled in storeMultipleEventsInDB
		"aggregation_key":   event.EventName,
		"principal":         event.Username,
		"labels":            event.Labels,
	}, nil
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
	go func() {
		err := updateOrCreateAgentStatus(ctx, accountId, AgentStatusConnected, "", true, map[string]any{
			"account_number": acc.AccountNumber,
			"events": map[string]any{
				"count":    len(events.Items),
				"duration": time.Since(t0).String(),
				"start":    startDate.Format(time.RFC3339),
				"end":      endDate.Format(time.RFC3339),
				"err":      "",
			},
		})
		if err != nil {
			ctx.GetLogger().Error("Failed to update agent status", "error", err.Error())
		}
	}()

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
		if event.FindingId != "" {
			return event.FindingId
		}
		return fmt.Sprintf("%s-%d", event.EventId, event.Date.Unix())
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
	sources := lo.Keys(distinctSources)
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
		for _, summary := range events.Summary {
			if summary.ServiceName != "" && summary.Region != "" && (summary.ResourcesCreated > 0 || summary.ResourceDeleted > 0) {
				ctx.GetLogger().Info("discovering resources post events sync", "serviceName", summary.ServiceName, "region", summary.Region)
				_, err := StoreResources(ctx, accountId, summary.ServiceName, summary.Region)
				if err != nil {
					ctx.GetLogger().Error("unable to discover resources for service post events sync", "error", err, "serviceName", summary.ServiceName, "region", summary.Region)
				}
			}
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
		if event.FindingId != "" {
			return event.FindingId
		}
		return fmt.Sprintf("%s-%d", event.EventId, event.Date.Unix())
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
		if event.FindingId != "" {
			eventMap["finding_id"] = event.FindingId
		} else {
			eventMap["finding_id"] = fmt.Sprintf("%s-%d", event.EventId, event.Date.Unix())
		}

		eventMap["created_at"] = currentTimeForBatch
		if eventMap["id"] == nil {
			eventMap["id"] = uuid.New().String()
		}
		eventsToStoreInDB = append(eventsToStoreInDB, eventMap)
	}

	// Link events to cloud_resourses by matching subject_name to resourse_id or name
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
	type resolvedRow struct {
		ID             string `db:"id"`
		Tenant         string `db:"tenant"`
		CloudAccountID string `db:"cloud_account_id"`
		Fingerprint    string `db:"fingerprint"`
	}
	var allResolved []resolvedRow

	for _, fp := range fps {
		re := fpToEvent[fp]
		resolvedAt := re.Date.UTC()

		var rows []resolvedRow
		err := dbms.QueryAndScan(&rows,
			`UPDATE events SET status = 'RESOLVED', updated_at = $3, ends_at = $3, priority = 'INFO'
			 WHERE cloud_account_id = $1 AND fingerprint = $2
			 AND status NOT IN ('CLOSED', 'RESOLVED')
			 RETURNING id::text, tenant::text, cloud_account_id::text, fingerprint`,
			accountID, fp, resolvedAt)
		if err != nil {
			ctx.GetLogger().Error("failed to resolve event by fingerprint", "error", err, "component", funcName, "fingerprint", fp)
			return err
		}
		allResolved = append(allResolved, rows...)
	}

	if len(allResolved) > 0 {
		ctx.GetLogger().Info("resolved existing events by fingerprint", "component", funcName, "count", len(allResolved))
	}

	// Record event_history for each resolved event
	for _, row := range allResolved {
		re, ok := fpToEvent[row.Fingerprint]
		if !ok {
			continue
		}
		metadata := map[string]any{
			"resolved_title":       re.Title,
			"resolved_description": re.Description,
			"resolved_labels":      re.Labels,
			"resolved_date":        re.Date.Format(time.RFC3339),
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

	return nil
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

// linkCloudResourceIds performs a batch lookup of cloud_resourses to set cloud_resource_id
// on events that have a subject_name matching a known resource.
func linkCloudResourceIds(ctx *security.RequestContext, dbms *common.DatabaseManager, events []map[string]any, accountID string) {
	// Collect unique subject names that don't already have a cloud_resource_id
	subjectNames := map[string]bool{}
	for _, e := range events {
		if hasCloudResourceId(e) {
			continue
		}
		if name, ok := e["subject_name"].(string); ok && name != "" {
			subjectNames[name] = true
		}
	}
	if len(subjectNames) == 0 {
		return
	}

	names := make([]string, 0, len(subjectNames))
	for name := range subjectNames {
		names = append(names, name)
	}

	type resourceMatch struct {
		ID         string `db:"id"`
		ResourceId string `db:"resourse_id"`
		Name       string `db:"name"`
	}

	var matches []resourceMatch
	err := dbms.QueryAndScan(&matches,
		`SELECT id, resourse_id, name FROM cloud_resourses
		 WHERE account = $1 AND is_active = true
		   AND (resourse_id = ANY($2) OR name = ANY($2))`,
		accountID, pq.Array(names))
	if err != nil {
		ctx.GetLogger().Warn("linkCloudResourceIds: failed to query cloud_resourses", "error", err)
		return
	}

	if len(matches) == 0 {
		return
	}

	// Build lookup maps: resourse_id → UUID, name → UUID
	resourceIdMap := map[string]string{}
	nameMap := map[string]string{}
	for _, m := range matches {
		resourceIdMap[m.ResourceId] = m.ID
		if m.Name != "" {
			nameMap[m.Name] = m.ID
		}
	}

	linked := 0
	for _, e := range events {
		if hasCloudResourceId(e) {
			continue
		}
		name, _ := e["subject_name"].(string)
		if name == "" {
			continue
		}
		// Prefer resourse_id match over name match
		if id, ok := resourceIdMap[name]; ok {
			e["cloud_resource_id"] = &id
			linked++
		} else if id, ok := nameMap[name]; ok {
			e["cloud_resource_id"] = &id
			linked++
		}
	}

	if linked > 0 {
		ctx.GetLogger().Info("linkCloudResourceIds: linked events to cloud resources", "linked", linked, "total", len(events))
	}
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

	rules, err := ListEventRules(ctx, accountId)
	if err != nil {
		ctx.GetLogger().Error("unable to list event rules", "error", err, "component", funcName, "accountId", accountId)
		return rules, err
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

	errorList := []error{}
	currentTime := time.Now().UTC().Format(time.RFC3339)
	args := []map[string]any{}

	// Use a map to deduplicate rules by (account_id, tenant_id, alert) to avoid
	// "ON CONFLICT DO UPDATE command cannot affect row a second time" error
	tenantId := ctx.GetSecurityContext().GetTenantId()
	seenRules := make(map[string]bool)

	for _, rule := range rules.Items {
		// Create a unique key for deduplication based on the conflict clause
		ruleKey := fmt.Sprintf("%s:%s:%s", accountId, tenantId, rule.Name)
		if seenRules[ruleKey] {
			ctx.GetLogger().Warn("skipping duplicate rule in batch", "accountId", accountId, "tenantId", tenantId, "alert", rule.Name)
			continue
		}
		seenRules[ruleKey] = true

		annotations := map[string]string{
			"summary":     rule.Summary,
			"description": rule.Description,
		}

		annotationsStr, err := common.MarshalJson(annotations)
		if err != nil {
			ctx.GetLogger().Error("unable to marshal annotations for DB", "error", err)
			errorList = append(errorList, err)
			continue
		}

		labelsStr, err := common.MarshalJson(rule.Labels)
		if err != nil {
			ctx.GetLogger().Error("unable to marshal labels for DB", "error", err)
			errorList = append(errorList, err)
			continue
		}

		args = append(args, map[string]any{
			"id":          uuid.NewString(),
			"created_at":  currentTime,
			"updated_at":  currentTime,
			"tenant_id":   tenantId,
			"account_id":  accountId,
			"alert":       rule.Name,
			"annotations": string(annotationsStr),
			"expr":        rule.Expr,
			"duration":    fmt.Sprintf("%vs", int(rule.Duration.Seconds())),
			"labels":      string(labelsStr),
			"source":      rule.Source,
			"category":    rule.Category,
			"severity":    rule.Severity,
			"enabled":     true,
		})
	}

	if len(errorList) > 0 {
		return providers.ListEventRules{}, errors.Join(errorList...)
	}

	result, err := dbms.NamedExec(`insert into event_rules (id, created_at, updated_at, tenant_id, account_id, alert, annotations, expr, duration, labels, source, category, severity, enabled)
		values (:id, :created_at, :updated_at, :tenant_id, :account_id, :alert, :annotations, :expr, :duration, :labels, :source, :category, :severity, :enabled)
		on conflict (account_id, tenant_id, alert)
		do update set updated_at = excluded.updated_at, alert = excluded.alert, annotations = excluded.annotations, expr = excluded.expr, duration = excluded.duration, labels = excluded.labels, source = excluded.source, category = excluded.category, severity = excluded.severity`,
		args,
	)

	if err != nil {
		ctx.GetLogger().Error("StoreEventRules: failed to insert/update event rules", "error", err, "component", funcName)
	} else {
		rowsAffected, _ := result.RowsAffected()
		ctx.GetLogger().Info("StoreEventRules: successfully stored event rules", "component", funcName, "rowsAffected", rowsAffected, "rulesCount", len(rules.Items))
	}

	return rules, err

}
