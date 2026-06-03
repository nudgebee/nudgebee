package core

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"

	"nudgebee/services/common"
	"nudgebee/services/config"
	eventtypes "nudgebee/services/event/types"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
	"nudgebee/services/tenant"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/noirbizarre/gonja"
	"github.com/noirbizarre/gonja/exec"
)

type EventIncomingWebhook struct {
	WebhookId             string                            `json:"webhook_id" validate:"required"`
	EventType             string                            `json:"event_type" validate:"required"`
	EventId               string                            `json:"event_id" validate:"required"`
	EventUrl              string                            `json:"event_url" validate:"required"`
	EventStatus           string                            `json:"event_status" validate:"required"`
	EventPriority         string                            `json:"event_priority" validate:"required"`
	EventCreatedAt        time.Time                         `json:"event_created_at" validate:"required"`
	EventEndsAt           time.Time                         `json:"event_ends_at"`
	EventTitle            string                            `json:"event_title" validate:"required"`
	EventDescription      string                            `json:"event_description"`
	EventTags             []string                          `json:"event_tags"`
	Investigation         EventIncomingWebhookInvestigation `json:"investigation"`
	EventSubjectName      string                            `json:"event_subject_name"`
	EventSubjectNamespace string                            `json:"event_subject_namespace"`
	EventSubjectKind      string                            `json:"event_subject_kind"`
	AccountId             string                            `json:"account_id"`
	EventSubjectOwner     string                            `json:"event_subject_owner"`
	EventSubjectOwnerKind string                            `json:"event_subject_owner_kind"`
	CloudResourceId       string                            `json:"cloud_resource_id"`
	ServiceKey            string                            `json:"service_key"`
}

type EventIncomingWebhookInvestigationSeverity string

var ErrEventNotSupported = errors.New("event not supported")

// eventAnalysisSources defines webhook sources that trigger LLM event analysis
var eventAnalysisSources = map[string]bool{
	"datadog_webhook":                 true,
	"pagerduty_webhook":               true,
	"azure_monitor_webhook":           true,
	"servicenow_webhook":              true,
	"zenduty_webhook":                 true,
	"newrelic_webhook":                true,
	"grafana_webhook":                 true,
	"gcp_monitoring_webhook":          true,
	"dynatrace_webhook":               true,
	"solarwinds_webhook":              true,
	"splunk_webhook":                  true,
	"prometheus_alertmanager_webhook": true,
	"workflow_webhook":                true,
}

const (
	EventIncomingWebhookInvestigationSeverityCritical EventIncomingWebhookInvestigationSeverity = "critical"
	EventIncomingWebhookInvestigationSeverityHigh     EventIncomingWebhookInvestigationSeverity = "high"
	EventIncomingWebhookInvestigationSeverityMedium   EventIncomingWebhookInvestigationSeverity = "medium"
	EventIncomingWebhookInvestigationSeverityLow      EventIncomingWebhookInvestigationSeverity = "low"

	// Alert label keys
	AlertLabelReason = "reason"
)

var webhookProcessorWorkerPool *common.WorkerPool

// InvestigateEventFn is a callback for event investigation, registered by the event package to break circular imports.
var InvestigateEventFn func(sc *security.RequestContext, webhookEvent eventtypes.Event, id string) (string, error)

func init() {
	webhookProcessorWorkerPool = common.NewWorkerPool("webhook_processor", 5)
}

type EventIncomingWebhookInvestigation struct {
	RuleName    string                     `json:"rule_name" validate:"required"`
	Labels      map[string]string          `json:"labels"`
	Annotations map[string]string          `json:"annotations"`
	RuleType    string                     `json:"rule_type" validate:"required"`
	RuleId      string                     `json:"rule_id" validate:"required"`
	Fingerprint string                     `json:"fingerprint" validate:"required"`
	Status      eventtypes.EventStatus     `json:"status" validate:"required"`
	Severity    eventtypes.EventPriortiy   `json:"severity" validate:"required"`
	SourceUrl   string                     `json:"source_url" validate:"required"`
	Evidences   []eventtypes.EventEvidence `json:"evidences"`
}

type EventIncomingTroubleshootWebhookIntegration interface {
	ProcessEventWebook(sc *security.RequestContext, settings []IntegrationConfigValue, accountId, webhookPayload string) ([]EventIncomingWebhook, error)
	MergeEventWebhooks(sc *security.RequestContext, previous EventIncomingWebhook, new EventIncomingWebhook) (EventIncomingWebhook, error)
}

// StoredWebhookEventData represents webhook event data stored in the event_incoming_webhooks table
type StoredWebhookEventData struct {
	ID               string    `db:"id"`
	TenantID         string    `db:"tenant_id"`
	AccountID        string    `db:"account_id"`
	IntegrationID    string    `db:"integration_id"`
	IntegrationType  string    `db:"integration_type"`
	WebhookID        string    `db:"webhook_id"`
	EventType        string    `db:"event_type"`
	EventID          string    `db:"event_id"`
	EventURL         string    `db:"event_url"`
	EventStatus      string    `db:"event_status"`
	EventPriority    string    `db:"event_priority"`
	EventCreatedAt   time.Time `db:"event_created_at"`
	EventTitle       string    `db:"event_title"`
	EventDescription string    `db:"event_description"`
	Raw              string    `db:"raw"`
	RequestURL       string    `db:"request_url"`
}

func storeUnprocessedWebhook(integrationId, tenantId, accountId, integrationType, raw string) error {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		slog.Error("failed to get database manager for unprocessed webhook", "error", err)
		return fmt.Errorf("failed to get database manager: %w", err)
	}

	_, err = dbms.Db.Exec(`INSERT INTO event_incoming_webhooks(id, tenant_id, account_id, integration_id, integration_type, webhook_id, event_type, event_id, event_url, event_status, event_priority, event_created_at, event_title, event_description, raw)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)`,
		uuid.NewString(), tenantId, nullableAccountID(accountId), integrationId, integrationType,
		"unprocessed", "unprocessed", uuid.NewString(), "",
		"unprocessed", "", time.Now(),
		"Unprocessed webhook", "Raw webhook data that failed to parse", raw)

	if err != nil {
		slog.Error("failed to insert unprocessed webhook", "integration_id", integrationId, "error", err)
		return fmt.Errorf("failed to insert unprocessed webhook: %w", err)
	}

	slog.Info("successfully stored unprocessed webhook", "integration_id", integrationId)
	return nil
}

// webhookIntegrationInfo holds the result of looking up an integration by token.
type webhookIntegrationInfo struct {
	IntegrationID   string
	TenantID        string
	AccountID       string
	IntegrationType string
}

// nullableAccountID returns nil when accountId is empty so Postgres receives
// NULL instead of an invalid empty-string UUID.
func nullableAccountID(accountId string) any {
	if accountId == "" {
		return nil
	}
	return accountId
}

// extractWebhookQueryLabels parses the query string of an incoming webhook URL
// and returns its parameters (minus credential keys) as labels. Lets webhook
// senders attach context — e.g. `&env=prod`, `&cluster=us-east-1` — that the
// receiving integration handler doesn't natively model. Applies to every
// webhook integration since it's resolved in the central router.
//
// Uses url.Parse so URL fragments and malformed inputs cannot bleed into the
// label map; sensitive-key match is case-insensitive so `Token` / `TOKEN` /
// `Authorization` are stripped consistently.
func extractWebhookQueryLabels(requestUrl string) map[string]string {
	out := map[string]string{}
	if requestUrl == "" {
		return out
	}
	u, err := url.Parse(requestUrl)
	if err != nil {
		return out
	}
	for k, v := range u.Query() {
		switch strings.ToLower(k) {
		case "token", "authorization":
			continue
		}
		if len(v) > 0 && v[0] != "" {
			out[k] = v[0]
		}
	}
	return out
}

// mergeWebhookQueryLabels applies extractWebhookQueryLabels output to the
// Investigation.Labels of every event. Existing labels set by the integration
// handler win on collision so a sender's `?service=foo` cannot overwrite
// integration-derived labels. Shared by the live, stored, and replay paths.
func mergeWebhookQueryLabels(events []EventIncomingWebhook, requestUrl string) {
	queryLabels := extractWebhookQueryLabels(requestUrl)
	if len(queryLabels) == 0 {
		return
	}
	for i := range events {
		if events[i].Investigation.Labels == nil {
			events[i].Investigation.Labels = map[string]string{}
		}
		for k, v := range queryLabels {
			if _, exists := events[i].Investigation.Labels[k]; !exists {
				events[i].Investigation.Labels[k] = v
			}
		}
	}
}

// extractWebhookToken extracts the authentication token from the request URL or headers.
//
// URL: parses the request URI and reads the `token` query parameter exactly,
// so unrelated keys that happen to contain the substring `token=`
// (e.g. `csrf_token`, `my_token`) do not collide and fragments / path
// segments do not bleed into the value.
//
// Headers: matches `Authorization` (or any casing) and reads the `Bearer ...`
// scheme via strings.CutPrefix so a malformed header (`Bearer` without
// trailing space, an unrelated scheme, or a raw value) no longer panics on
// `Split(...)[1]`.
func extractWebhookToken(requestUrl string, requestHeaders map[string]string) string {
	if u, err := url.Parse(requestUrl); err == nil {
		if t := u.Query().Get("token"); t != "" {
			return t
		}
	}
	for k, v := range requestHeaders {
		if !strings.EqualFold(k, "Authorization") || v == "" {
			continue
		}
		if rest, ok := strings.CutPrefix(v, "Bearer "); ok {
			if rest = strings.TrimSpace(rest); rest != "" {
				return rest
			}
		}
	}
	return ""
}

// lookupIntegrationByToken finds the enabled integration matching the given webhook token.
func lookupIntegrationByToken(sc *security.RequestContext, token string) (*webhookIntegrationInfo, error) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		sc.GetLogger().Error("integrations: unable to get database manager", "error", err)
		return nil, err
	}

	integrations, err := dbms.Db.Queryx(`
		select i.id::text, i.tenant_id::text, COALESCE(ica.cloud_account_id::text, '') as cloud_account_id, i.type
		from integrations i
		left join integrations_cloud_accounts ica on i.id = ica.integration_id
		where i.id in (
			select integration_id
			from integration_config_values
			where value = $1 and name = 'token'
		) and i.status = 'enabled'
	`, token)
	if err != nil {
		sc.GetLogger().Error("integrations: unable to query integrations", "error", err)
		return nil, err
	}
	defer func() {
		if err := integrations.Close(); err != nil {
			sc.GetLogger().Error("integrations: unable to close rows", "error", err)
		}
	}()

	var info webhookIntegrationInfo
	integrationCount := 0
	for integrations.Next() {
		if integrationCount > 0 {
			sc.GetLogger().Warn("integrations: multiple cloud accounts linked to integration, using first account as default",
				"integration_id", info.IntegrationID, "default_account_id", info.AccountID)
			break
		}
		err = integrations.Scan(&info.IntegrationID, &info.TenantID, &info.AccountID, &info.IntegrationType)
		if err != nil {
			sc.GetLogger().Error("integrations: unable to scan integration row", "error", err)
			return nil, err
		}
		integrationCount++
	}

	if info.IntegrationID == "" {
		return nil, errors.New("integrations: integration not found for given token")
	}

	return &info, nil
}

// GetLinkedCloudAccountIds returns every cloud_account_id linked to enabled integrations
// of the given type that also include the provided accountId, scoped to the caller's tenant.
// This lets webhook handlers expand single-account lookups across all cloud accounts a
// shared integration (e.g. one Datadog token wired to multiple AWS accounts) covers, instead
// of arbitrarily picking the first row from the integrations_cloud_accounts join.
// The provided accountId is always included in the returned slice (even if empty results
// come back from the DB) so callers can use it as a safe drop-in for the prior single-id query.
func GetLinkedCloudAccountIds(sc *security.RequestContext, accountId, integrationType string) ([]string, error) {
	if accountId == "" {
		return nil, nil
	}
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		sc.GetLogger().Error("integrations: unable to get database manager", "error", err)
		return []string{accountId}, err
	}
	var ids []string
	err = dbms.Db.Select(&ids, `
		SELECT DISTINCT ica2.cloud_account_id::text
		FROM integrations_cloud_accounts ica1
		JOIN integrations_cloud_accounts ica2 ON ica1.integration_id = ica2.integration_id
		JOIN integrations i ON i.id = ica1.integration_id
		WHERE ica1.cloud_account_id = $1
		  AND i.type = $2
		  AND i.tenant_id = $3
		  AND i.status = 'enabled'
	`, accountId, integrationType, sc.GetSecurityContext().GetTenantId())
	if err != nil {
		sc.GetLogger().Error("integrations: unable to query linked cloud account ids",
			"error", err, "account_id", accountId, "integration_type", integrationType)
		return []string{accountId}, err
	}
	if len(ids) == 0 {
		return []string{accountId}, nil
	}
	// Make sure the caller-provided accountId is always present so callers can rely on
	// the list as a strict superset of the original behavior.
	for _, id := range ids {
		if id == accountId {
			return ids, nil
		}
	}
	return append(ids, accountId), nil
}

// ValidateAndStoreWebhook validates the webhook token, looks up the integration,
// and stores the raw payload in event_incoming_webhooks with processing_status='pending'.
// Returns the row UUID for async processing.
func ValidateAndStoreWebhook(sc *security.RequestContext, requestUrl string, requestHeaders map[string]string, webhookPayload string) (string, error) {
	token := extractWebhookToken(requestUrl, requestHeaders)
	if token == "" {
		return "", errors.New("integrations: token not found")
	}

	info, err := lookupIntegrationByToken(sc, token)
	if err != nil {
		return "", err
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return "", fmt.Errorf("integrations: failed to get database manager: %w", err)
	}

	rowID := uuid.NewString()
	_, err = dbms.Db.Exec(`INSERT INTO event_incoming_webhooks(id, tenant_id, account_id, integration_id, integration_type, webhook_id, event_type, event_id, event_url, event_status, event_priority, event_created_at, event_title, event_description, raw, processing_status, request_url)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)`,
		rowID, info.TenantID, nullableAccountID(info.AccountID), info.IntegrationID, info.IntegrationType,
		"", "", "", "", "", "", time.Now(), "", "", webhookPayload, "pending", requestUrl)
	if err != nil {
		return "", fmt.Errorf("integrations: failed to store webhook for async processing: %w", err)
	}

	return rowID, nil
}

// ProcessStoredWebhook fetches a pending webhook row, runs the integration-specific parser,
// applies label mappings, stores parsed data, and routes events for investigation/resolution.
func ProcessStoredWebhook(sc *security.RequestContext, webhookRowID string) error {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return fmt.Errorf("integrations: failed to get database manager: %w", err)
	}

	// Atomically claim the job by transitioning pending → processing.
	// Prevents duplicate processing if RabbitMQ redelivers the message.
	res, err := dbms.Db.Exec(`UPDATE event_incoming_webhooks SET processing_status = 'processing' WHERE id = $1 AND processing_status = 'pending'`, webhookRowID)
	if err != nil {
		return fmt.Errorf("integrations: failed to claim webhook for processing: %w", err)
	}
	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("integrations: failed to check affected rows: %w", err)
	}
	if rowsAffected == 0 {
		sc.GetLogger().Info("integrations: webhook not in pending state, skipping", "webhook_row_id", webhookRowID)
		return nil
	}

	var row StoredWebhookEventData
	err = dbms.Db.Get(&row, `SELECT id, tenant_id, COALESCE(account_id::text, '') as account_id, integration_id, integration_type, raw, COALESCE(request_url, '') as request_url FROM event_incoming_webhooks WHERE id = $1`, webhookRowID)
	if err != nil {
		updateWebhookStatus(dbms, webhookRowID, "failed")
		return fmt.Errorf("integrations: webhook row not found: %w", err)
	}

	sc = security.NewRequestContextForTenantAdmin(row.TenantID, sc.GetLogger().With("integration_id", row.IntegrationID, "webhook_row_id", webhookRowID), sc.GetTracer(), sc.GetMeter())

	integration, found := GetIntegration(row.IntegrationType)
	if !found {
		updateWebhookStatus(dbms, webhookRowID, "failed")
		return fmt.Errorf("integrations: integration type %s not found", row.IntegrationType)
	}

	webhookIntegration, ok := integration.(EventIncomingTroubleshootWebhookIntegration)
	if !ok {
		updateWebhookStatus(dbms, webhookRowID, "failed")
		return fmt.Errorf("integrations: integration type %s does not support webhook events", row.IntegrationType)
	}

	webhookSettings, err := getIntegrationSettings(dbms, row.IntegrationID)
	if err != nil {
		sc.GetLogger().Error("unable to get settings", "error", err)
	}

	webhookEvents, err := webhookIntegration.ProcessEventWebook(sc, webhookSettings, row.AccountID, row.Raw)
	if err != nil {
		updateWebhookStatus(dbms, webhookRowID, "failed")
		if !errors.Is(err, ErrEventNotSupported) {
			sc.GetLogger().Error("integrations: unable to process webhook payload", "error", err)
		}
		return nil
	}

	mergeWebhookQueryLabels(webhookEvents, row.RequestURL)

	tenantLabelMapping := loadTenantLabelMapping(sc)
	if tenantLabelMapping != nil {
		cache := newLabelExtractorCache(tenantLabelMapping, sc.GetLogger())
		for i := range webhookEvents {
			applyTenantLabelMapping(&webhookEvents[i], tenantLabelMapping, cache)
		}
	}

	applyAccountMappingToEvents(webhookEvents, webhookSettings, row.AccountID, sc.GetLogger())
	enrichEventsWithSubjectResolution(sc, webhookEvents)

	for i, e := range webhookEvents {
		eventAccountId := row.AccountID
		if e.AccountId != "" {
			eventAccountId = e.AccountId
		}
		if e.EventCreatedAt.IsZero() {
			e.EventCreatedAt = time.Now()
		}
		if e.WebhookId == "" {
			e.WebhookId = uuid.NewString()
		}

		if i == 0 {
			// Update the pending row with parsed data from the first event
			_, updateErr := dbms.Exec(`UPDATE event_incoming_webhooks SET
				webhook_id = $2, event_type = $3, event_id = $4, event_url = $5,
				event_status = $6, event_priority = $7, event_created_at = $8,
				event_title = $9, event_description = $10, account_id = $11, processing_status = 'processed'
				WHERE id = $1`,
				webhookRowID, e.WebhookId, e.EventType, e.EventId, e.EventUrl,
				e.EventStatus, e.EventPriority, e.EventCreatedAt, e.EventTitle, e.EventDescription, nullableAccountID(eventAccountId))
			if updateErr != nil {
				sc.GetLogger().Error("integrations: failed to update webhook row", "error", updateErr)
			}
		} else {
			// Insert additional rows for extra events from the same payload
			_, insertErr := dbms.Exec(`INSERT INTO event_incoming_webhooks(id, tenant_id, account_id, integration_id, integration_type, webhook_id, event_type, event_id, event_url, event_status, event_priority, event_created_at, event_title, event_description, raw, processing_status, request_url)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, 'processed', $16)`,
				uuid.NewString(), row.TenantID, nullableAccountID(eventAccountId), row.IntegrationID, row.IntegrationType,
				e.WebhookId, e.EventType, e.EventId, e.EventUrl, e.EventStatus, e.EventPriority,
				e.EventCreatedAt, e.EventTitle, e.EventDescription, row.Raw, row.RequestURL)
			if insertErr != nil {
				sc.GetLogger().Error("integrations: failed to insert additional webhook event row", "error", insertErr)
			}
		}

		routeWebhookEvent(sc, row.TenantID, eventAccountId, integration.Name(), e)
	}

	if len(webhookEvents) == 0 {
		updateWebhookStatus(dbms, webhookRowID, "processed")
	}

	return nil
}

// routeWebhookEvent routes a parsed webhook event to investigation or resolution.
func routeWebhookEvent(sc *security.RequestContext, tenantId, accountId, source string, e EventIncomingWebhook) {
	switch strings.ToLower(e.EventStatus) {
	case "resolved":
		if err := resolveEvent(sc, tenantId, accountId, source, e); err != nil {
			sc.GetLogger().Error("integrations: unable to resolve request", "error", err)
		}
	case "triggered", "firing", "warning":
		if err := investigateWebhookEvent(sc, tenantId, accountId, source, e); err != nil {
			sc.GetLogger().Error("integrations: unable to investigate request", "error", err)
		}
	default:
		sc.GetLogger().Warn("integrations: unknown event status", "event_status", e.EventStatus, "event_id", e.EventId)
	}
}

// updateWebhookStatus updates the processing_status of a webhook row.
func updateWebhookStatus(dbms *database.DatabaseManager, webhookRowID, status string) {
	_, err := dbms.Exec(`UPDATE event_incoming_webhooks SET processing_status = $2 WHERE id = $1`, webhookRowID, status)
	if err != nil {
		slog.Error("integrations: failed to update webhook processing status", "webhook_row_id", webhookRowID, "status", status, "error", err)
	}
}

func ProcessEventWebook(sc *security.RequestContext, requestUrl string, requestHeaders map[string]string, webhookPayload string) error {
	token := extractWebhookToken(requestUrl, requestHeaders)
	if token == "" {
		return errors.New("integrations: token not found")
	}

	info, err := lookupIntegrationByToken(sc, token)
	if err != nil {
		return err
	}

	integrationId := info.IntegrationID
	tenantId := info.TenantID
	accountId := info.AccountID
	integrationType := info.IntegrationType

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		sc.GetLogger().Error("integrations: unable to get database manager", "error", err)
		return err
	}

	// set tenant context
	sc = security.NewRequestContextForTenantAdmin(tenantId, sc.GetLogger().With("integration_id", integrationId), sc.GetTracer(), sc.GetMeter())
	integration, found := GetIntegration(integrationType)
	if !found {
		return errors.New("integrations: integration not found")
	}
	webhookIntegration, ok := integration.(EventIncomingTroubleshootWebhookIntegration)
	if !ok {
		return errors.New("integrations: integration not supported")
	}

	webhookSettings, err := getIntegrationSettings(dbms, integrationId)
	if err != nil {
		sc.GetLogger().Error("unable to get settings", "error", err)
	}

	//store webhookEvents data
	webhookEvents, err := webhookIntegration.ProcessEventWebook(sc, webhookSettings, accountId, webhookPayload)
	if err != nil {
		if saveErr := storeUnprocessedWebhook(integrationId, tenantId, accountId, integrationType, webhookPayload); saveErr != nil {
			sc.GetLogger().Error("integrations: failed to save unprocessed webhook", "error", saveErr)
		}
		if !errors.Is(err, ErrEventNotSupported) {
			sc.GetLogger().Error("integrations: unable to process request", "error", err)
		}
		// return nil as we want handle it silently
		return nil
	}

	// Merge URL query params (e.g. ?env=prod&cluster=us-east-1) into every
	// event's investigation labels. Available to every webhook integration
	// without per-handler changes. Existing labels from the integration win.
	mergeWebhookQueryLabels(webhookEvents, requestUrl)

	// Apply tenant-configured label mappings before processing events
	tenantLabelMapping := loadTenantLabelMapping(sc)
	if tenantLabelMapping != nil {
		cache := newLabelExtractorCache(tenantLabelMapping, sc.GetLogger())
		for i := range webhookEvents {
			applyTenantLabelMapping(&webhookEvents[i], tenantLabelMapping, cache)
		}
	}

	applyAccountMappingToEvents(webhookEvents, webhookSettings, accountId, sc.GetLogger())
	enrichEventsWithSubjectResolution(sc, webhookEvents)

	eventAccountId := accountId
	for _, e := range webhookEvents {
		if e.AccountId != "" {
			eventAccountId = e.AccountId
		}
		if e.EventCreatedAt.IsZero() {
			e.EventCreatedAt = time.Now()
		}
		if e.WebhookId == "" {
			e.WebhookId = uuid.NewString()
		}

		_, err = dbms.Exec(`insert into event_incoming_webhooks(id, tenant_id, account_id, integration_id, integration_type, webhook_id, event_type, event_id, event_url, event_status, event_priority, event_created_at, event_title, event_description, raw, request_url)
								values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)`,
			uuid.NewString(), tenantId, nullableAccountID(eventAccountId), integrationId, integrationType, e.WebhookId, e.EventType, e.EventId, e.EventUrl, e.EventStatus, e.EventPriority, e.EventCreatedAt, e.EventTitle, e.EventDescription, webhookPayload, requestUrl,
		)
		if err != nil {
			if saveErr := storeUnprocessedWebhook(integrationId, tenantId, eventAccountId, integrationType, webhookPayload); saveErr != nil {
				sc.GetLogger().Error("integrations: failed to save unprocessed webhook after insert failure", "error", saveErr)
			}
			return common.ErrorInternal("integrations: unable to insert values for tool config")
		}

		routeWebhookEvent(sc, tenantId, eventAccountId, integration.Name(), e)
	}
	return nil
}

func convertWebhookEventToEvent(e EventIncomingWebhook, tenantId, accountId, source string) eventtypes.Event {
	labels := make(map[string]string, len(e.Investigation.Labels)+9)
	labelsArray := make([][]any, 0, len(e.Investigation.Labels)+9)
	for k, v := range e.Investigation.Labels {
		labelsArray = append(labelsArray, []any{k, v})
		labels[k] = v
	}

	// Add additional tracking labels
	if labels["rule_id"] == "" {
		labelsArray = append(labelsArray, []any{"rule_id", e.Investigation.RuleId})
		labels["rule_id"] = e.Investigation.RuleId
	}
	if labels["alertname"] == "" {
		labelsArray = append(labelsArray, []any{"alertname", e.Investigation.RuleName})
		labels["alertname"] = e.Investigation.RuleName
	}
	if labels["rule_type"] == "" {
		labelsArray = append(labelsArray, []any{"rule_type", e.Investigation.RuleType})
		labels["rule_type"] = e.Investigation.RuleType
	}
	if labels["severity"] == "" {
		labelsArray = append(labelsArray, []any{"severity", e.Investigation.Severity})
		labels["severity"] = string(e.Investigation.Severity)
	}
	if labels["nb_webhook_id"] == "" {
		labelsArray = append(labelsArray, []any{"nb_webhook_id", e.WebhookId})
		labels["nb_webhook_id"] = e.WebhookId
	}
	if labels["nb_webhook_event_id"] == "" {
		labelsArray = append(labelsArray, []any{"nb_webhook_event_id", e.EventId})
		labels["nb_webhook_event_id"] = e.EventId
	}
	if labels["nb_webhook_source"] == "" {
		labelsArray = append(labelsArray, []any{"nb_webhook_source", source})
		labels["nb_webhook_source"] = source
	}
	if labels["nb_webhook_url"] == "" {
		labelsArray = append(labelsArray, []any{"nb_webhook_url", e.EventUrl})
		labels["nb_webhook_url"] = e.EventUrl
	}
	if labels["pattern_hash"] == "" {
		labelsArray = append(labelsArray, []any{"pattern_hash", e.EventId})
		labels["pattern_hash"] = e.EventId
	}

	evidences := make([]eventtypes.EventEvidence, 0, 2)

	// Check if alert has a reason label and create insight
	labelInsights := []eventtypes.EventEvidenceInsight{}
	if reasonValue, hasReason := e.Investigation.Labels[AlertLabelReason]; hasReason {
		labelInsights = append(labelInsights, eventtypes.EventEvidenceInsight{
			Message:  fmt.Sprintf("Alert contains reason: %s", reasonValue),
			Severity: "high",
		})
	}

	evidences = append(evidences, eventtypes.EventEvidence{
		Type:    "table",
		Insight: labelInsights,
		Data: map[string]any{
			"column_renderers": map[string]any{},
			"headers":          []string{"label", "value"},
			"rows":             labelsArray,
			"table_name":       "*Alert labels*",
		},
		AdditionalInfo: map[string]any{
			"action_name":            "webhook_event",
			"actual_action_name":     "webhook_event",
			"conditional_expression": "",
		},
	})
	evidences = append(evidences, eventtypes.EventEvidence{
		Type:    "markdown",
		Insight: []eventtypes.EventEvidenceInsight{},
		Data: map[string]any{
			"data": e.EventDescription,
			"name": "Event Description",
		},
		AdditionalInfo: map[string]any{
			"action_name":            "webhook_event",
			"actual_action_name":     "webhook_event",
			"conditional_expression": "",
		},
	})
	if len(e.Investigation.Evidences) > 0 {
		evidences = append(evidences, e.Investigation.Evidences...)
	}

	var createdAt *time.Time
	if !e.EventCreatedAt.IsZero() {
		createdAt = &e.EventCreatedAt
	} else {
		createdAt1 := time.Now()
		createdAt = &createdAt1
	}

	var endsAt *time.Time
	if !e.EventEndsAt.IsZero() {
		endsAt = &e.EventEndsAt
	}

	status := e.Investigation.Status
	if status == "" {
		status = eventtypes.EventStatusFiring
	}

	priortiy := e.Investigation.Severity
	if priortiy == "" {
		priortiy = eventtypes.EventPriortiyLow
	}

	return eventtypes.Event{
		AccountId:        accountId,
		Tenant:           tenantId,
		FindingId:        e.EventId,
		Title:            e.EventTitle,
		Description:      e.EventDescription,
		Source:           source,
		AggregationKey:   e.Investigation.RuleId,
		Failure:          "",
		FindingType:      "issue",
		Category:         "issue",
		Priority:         priortiy,
		SubjectType:      e.EventSubjectKind,
		SubjectName:      e.EventSubjectName,
		SubjectNamespace: e.EventSubjectNamespace,
		SubjectNode:      "",
		ServiceKey:       e.ServiceKey,
		Cluster:          "",
		EndsAt:           endsAt,
		StartsAt:         createdAt,
		CreatedAt:        createdAt,
		Fingerprint:      e.Investigation.Fingerprint,
		Evidences:        convertSliceAnyToSliceInterface(evidences),
		CloudResourceId:  e.CloudResourceId,
		Status:           status,
		Principal:        "",
		SubjectOwner:     e.EventSubjectOwner,
		SubjectOwnerKind: e.EventSubjectOwnerKind,
		Labels:           labels,
	}
}

func convertSliceAnyToSliceInterface(evidences []eventtypes.EventEvidence) []any {
	s := make([]any, len(evidences))
	for i, v := range evidences {
		s[i] = v
	}
	return s
}

func resolveEvent(sc *security.RequestContext, tenantId, accountId string, source string, event EventIncomingWebhook) error {
	if accountId == "" || accountId == uuid.Nil.String() {
		sc.GetLogger().Info("integrations: skipping event resolution — no cloud account linked", "source", source, "event_id", event.EventId)
		return nil
	}
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		sc.GetLogger().Error("integrations: unable to process request", "error", err)
		return err
	}
	fingerPrint := event.EventId
	if source == "datadog_webhook" || source == "gcp_monitoring_webhook" {
		fingerPrint = event.Investigation.Fingerprint
	}
	_, err = dbms.Exec(`update events set status = $3 where fingerprint = $2 and cloud_account_id = $1`,
		accountId, fingerPrint, "CLOSED",
	)
	return err
}

func investigateWebhookEvent(sc *security.RequestContext, tenantId, accountId string, source string, event EventIncomingWebhook) error {
	if event.Investigation.RuleId == "" {
		sc.GetLogger().Error("webhook: invalid webhook payload: missing or empty event client URL")
		return errors.New("webhook: invalid payload, missing URL")
	}

	if !eventAnalysisSources[source] {
		sc.GetLogger().Error("integrations: webhook source not whitelisted for analysis", "source", source, "event_id", event.EventId)
		return fmt.Errorf("integrations: webhook source %q not configured for analysis", source)
	}

	if !config.Config.WebhookAsyncExecution {
		return triggerEventAnalysisUsingLLM(sc, event, tenantId, accountId, sc.GetSecurityContext().GetUserId(), source)
	}
	webhookProcessorWorkerPool.Submit(func() {
		err := triggerEventAnalysisUsingLLM(sc, event, tenantId, accountId, sc.GetSecurityContext().GetUserId(), source)
		if err != nil {
			sc.GetLogger().Error("integrations: failed to trigger event analysis", "error", err)
		}
	})
	return nil
}

func triggerEventAnalysisUsingLLM(sc *security.RequestContext, webhookEvent EventIncomingWebhook, tenantId, accountId, userId string, source string) error {
	eventToInsert := convertWebhookEventToEvent(webhookEvent, tenantId, accountId, source)

	if InvestigateEventFn == nil {
		return errors.New("integrations: InvestigateEventFn not registered")
	}

	sc1 := security.NewRequestContextForTenantAdmin(tenantId, sc.GetLogger(), sc.GetTracer(), sc.GetMeter())
	_, err := InvestigateEventFn(sc1, eventToInsert, "")
	if err != nil {
		sc.GetLogger().Error("integrations: failed to do invent investigation using llm", "error", err, "accountId", accountId, "eventId", webhookEvent.EventId)
		return fmt.Errorf("failed to insert event for analysis: %w", err)
	}

	return nil
}

func ReplayWebhookEvent(sc *security.RequestContext, webhookEventId string) error {

	if sc == nil {
		return errors.New("integrations: security context cannot be nil")
	}
	if webhookEventId == "" {
		return errors.New("integrations: webhook_event_id is required")
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		sc.GetLogger().Error("integrations: unable to get database manager", "error", err)
		return err
	}

	// Fetch webhook event from event_incoming_webhooks table
	var webhookData StoredWebhookEventData
	query := `
		SELECT id, tenant_id, COALESCE(account_id::text, '') as account_id, integration_id, integration_type,
			   webhook_id, event_type, event_id, event_url, event_status,
			   event_priority, event_created_at, event_title, event_description, raw,
			   COALESCE(request_url, '') as request_url
		FROM event_incoming_webhooks
		WHERE id = $1
	`

	err = dbms.Db.Get(&webhookData, query, webhookEventId)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			sc.GetLogger().Warn("integrations: webhook event not found", "webhook_event_id", webhookEventId)
			return fmt.Errorf("integrations: webhook event not found with id %s", webhookEventId)
		}
		sc.GetLogger().Error("integrations: database error while fetching webhook event", "error", err, "webhook_event_id", webhookEventId)
		return fmt.Errorf("integrations: unable to fetch webhook event: %w", err)
	}

	// Get the integration
	integration, found := GetIntegration(webhookData.IntegrationType)
	if !found {
		return fmt.Errorf("integrations: integration type %s not found", webhookData.IntegrationType)
	}

	webhookIntegration, ok := integration.(EventIncomingTroubleshootWebhookIntegration)
	if !ok {
		return fmt.Errorf("integrations: integration type %s does not support webhook events", webhookData.IntegrationType)
	}

	// Get the actual integration settings
	webhookSettings, err := getIntegrationSettings(dbms, webhookData.IntegrationID)
	if err != nil {
		sc.GetLogger().Warn("integrations: unable to fetch integration settings, using empty settings", "error", err, "integration_id", webhookData.IntegrationID)
		webhookSettings = []IntegrationConfigValue{}
	}
	// set tenant context for replay
	sc = security.NewRequestContextForTenantAdmin(webhookData.TenantID, sc.GetLogger().With("integration_id", webhookData.IntegrationID), sc.GetTracer(), sc.GetMeter())

	webhookEvents, err := webhookIntegration.ProcessEventWebook(sc, webhookSettings, webhookData.AccountID, webhookData.Raw)
	if err != nil {
		sc.GetLogger().Error("integrations: unable to process webhook payload for replay", "error", err, "webhook_event_id", webhookEventId)
		return fmt.Errorf("integrations: unable to process webhook payload for replay: %w", err)
	}

	if webhookData.EventType == "workflow_webhook" {
		return nil
	}

	mergeWebhookQueryLabels(webhookEvents, webhookData.RequestURL)

	tenantLabelMapping := loadTenantLabelMapping(sc)
	if tenantLabelMapping != nil {
		cache := newLabelExtractorCache(tenantLabelMapping, sc.GetLogger())
		for i := range webhookEvents {
			applyTenantLabelMapping(&webhookEvents[i], tenantLabelMapping, cache)
		}
	}

	applyAccountMappingToEvents(webhookEvents, webhookSettings, webhookData.AccountID, sc.GetLogger())
	enrichEventsWithSubjectResolution(sc, webhookEvents)

	// Find the matching event from the processed events
	var targetEvent *EventIncomingWebhook
	for i := range webhookEvents {
		if webhookEvents[i].EventId == webhookData.EventID {
			targetEvent = &webhookEvents[i]
			break
		}
	}

	if targetEvent == nil {
		return fmt.Errorf("integrations: could not find matching event with id %s in processed webhook events", webhookData.EventID)
	}

	// Force the event status to "triggered" to replay the investigation
	targetEvent.EventStatus = "triggered"

	accountId := webhookData.AccountID
	if targetEvent.AccountId != "" {
		accountId = targetEvent.AccountId
	}
	// Trigger investigation for the replayed event
	err = investigateWebhookEvent(sc, webhookData.TenantID, accountId, integration.Name(), *targetEvent)
	if err != nil {
		sc.GetLogger().Error("integrations: unable to investigate replayed webhook event", "error", err, "webhook_event_id", webhookEventId)
		return fmt.Errorf("integrations: unable to investigate replayed webhook event: %w", err)
	}

	sc.GetLogger().Info("integrations: successfully replayed webhook event", "webhook_event_id", webhookEventId, "event_id", targetEvent.EventId)
	return nil
}

// SyntheticIntegrationNameKey is the settings key under which getIntegrationSettings
// injects the integration's display name. It is not stored in
// integration_config_values; it's read from integrations.name and surfaced
// alongside the real config values so per-integration handlers can route by
// name (e.g. workflow_webhook's fan-out endpoint) without taking a separate
// DB hit. The double-underscore prefix signals "synthetic, not on disk" and
// avoids collision with any user-defined schema property name.
const SyntheticIntegrationNameKey = "__integration_name"

func getIntegrationSettings(dbms *database.DatabaseManager, integrationId string) ([]IntegrationConfigValue, error) {
	var settings []IntegrationConfigValue
	// Single round trip: pull every integration_config_values row PLUS a
	// synthetic row carrying the integration's display name (read from
	// integrations.name). Per-integration handlers that need to route by name
	// (workflow_webhook's fan-out endpoint) can pick it up via GetSettingValue
	// without taking a second DB hit per webhook event.
	query := `
		SELECT name, value, is_encrypted
		FROM integration_config_values
		WHERE integration_id = $1
		UNION ALL
		SELECT $2, name, false
		FROM integrations
		WHERE id = $1 AND name <> ''`

	err := dbms.Db.Select(&settings, query, integrationId, SyntheticIntegrationNameKey)
	if err != nil {
		return nil, err
	}

	return settings, nil
}

// TenantLabelMapping defines per-tenant label key mappings for webhook event fields.
// Each field is an ordered list of label keys; the first non-empty match wins.
type TenantLabelMapping struct {
	SubjectNameLabels []string `json:"subject_name_labels"`
	NamespaceLabels   []string `json:"namespace_labels"`
	SeverityLabels    []string `json:"severity_labels"`
}

// loadTenantLabelMapping reads the webhook_label_mapping tenant attribute.
// Returns nil if not configured or on error (callers treat nil as no-op).
func loadTenantLabelMapping(sc *security.RequestContext) *TenantLabelMapping {
	attrs, err := tenant.GetTenantAttributesByName(sc, "webhook_label_mapping")
	if err != nil || len(attrs) == 0 {
		return nil
	}

	var mapping TenantLabelMapping
	if err := json.Unmarshal([]byte(attrs[0].Value), &mapping); err != nil {
		sc.GetLogger().Warn("integrations: failed to parse webhook_label_mapping", "error", err)
		return nil
	}

	if len(mapping.SubjectNameLabels) == 0 && len(mapping.NamespaceLabels) == 0 && len(mapping.SeverityLabels) == 0 {
		return nil
	}

	return &mapping
}

// labelExtractorCache pre-compiles regex patterns and gonja templates so they
// are compiled once per request rather than once per event × per key spec.
type labelExtractorCache struct {
	regexes   map[string]*regexp.Regexp
	templates map[string]*exec.Template
	logger    *slog.Logger
}

func newLabelExtractorCache(mapping *TenantLabelMapping, logger *slog.Logger) *labelExtractorCache {
	cache := &labelExtractorCache{
		regexes:   make(map[string]*regexp.Regexp),
		templates: make(map[string]*exec.Template),
		logger:    logger,
	}
	allSpecs := make([]string, 0, len(mapping.SubjectNameLabels)+len(mapping.NamespaceLabels)+len(mapping.SeverityLabels))
	allSpecs = append(allSpecs, mapping.SubjectNameLabels...)
	allSpecs = append(allSpecs, mapping.NamespaceLabels...)
	allSpecs = append(allSpecs, mapping.SeverityLabels...)

	for _, keySpec := range allSpecs {
		if strings.Contains(keySpec, "{{") {
			tpl, err := gonja.FromString(keySpec)
			if err != nil {
				logger.Warn("integrations: invalid gonja template in webhook_label_mapping", "template", keySpec, "error", err)
				continue
			}
			cache.templates[keySpec] = tpl
		} else if _, pattern, hasPattern := strings.Cut(keySpec, "|"); hasPattern && pattern != "" {
			re, err := regexp.Compile(pattern)
			if err != nil {
				logger.Warn("integrations: invalid regex in webhook_label_mapping", "pattern", pattern, "error", err)
				continue
			}
			cache.regexes[keySpec] = re
		}
	}
	return cache
}

// applyTenantLabelMapping fills empty event fields using the tenant's configured label key mappings.
// Per-integration logic (e.g. PagerDuty's resolveSubjectFromLabels) runs first and has priority.
// Tenant mapping only fills fields the integration processor couldn't resolve.
//
// Each entry can be a plain label key ("service_name"), a key with regex extraction
// ("app_id|/k8s/[^/]+/(.+)"), or a gonja/jinja2 template ("{{ labels.app_id | split(sep='/') | last }}").
func applyTenantLabelMapping(event *EventIncomingWebhook, mapping *TenantLabelMapping, cache *labelExtractorCache) {
	if mapping == nil || event.Investigation.Labels == nil {
		return
	}

	if event.EventSubjectName == "" {
		for _, keySpec := range mapping.SubjectNameLabels {
			if v := extractLabelValue(event.Investigation.Labels, keySpec, cache); v != "" {
				event.EventSubjectName = v
				break
			}
		}
	}

	if event.EventSubjectNamespace == "" {
		for _, keySpec := range mapping.NamespaceLabels {
			if v := extractLabelValue(event.Investigation.Labels, keySpec, cache); v != "" {
				event.EventSubjectNamespace = v
				break
			}
		}
	}

	if event.Investigation.Severity == "" {
		for _, keySpec := range mapping.SeverityLabels {
			if v := extractLabelValue(event.Investigation.Labels, keySpec, cache); v != "" {
				event.Investigation.Severity = normalizeSeverity(v)
				break
			}
		}
	}
}

// extractLabelValue resolves a label key spec to a value from the labels map.
// Supports three formats:
//   - "label_key"                                              → returns labels[label_key] as-is
//   - "label_key|regex_with_capture"                           → applies regex, returns first capture group
//   - "{{ labels.app_id | split(sep='/') | last }}"            → gonja (jinja2) template with labels context
func extractLabelValue(labels map[string]string, keySpec string, cache *labelExtractorCache) string {
	// Gonja template
	if strings.Contains(keySpec, "{{") {
		tpl := cache.templates[keySpec]
		if tpl == nil {
			return ""
		}
		result, err := tpl.Execute(gonja.Context{"labels": labels})
		if err != nil {
			return ""
		}
		return strings.TrimSpace(result)
	}

	// Plain key or regex extraction
	labelKey, _, hasPattern := strings.Cut(keySpec, "|")
	v := labels[labelKey]
	if v == "" {
		return ""
	}
	if !hasPattern {
		return v
	}
	re := cache.regexes[keySpec]
	if re == nil {
		return ""
	}
	matches := re.FindStringSubmatch(v)
	if len(matches) < 2 {
		return ""
	}
	return matches[1]
}

// normalizeSeverity maps a raw severity string to a known EventPriority constant.
func normalizeSeverity(raw string) eventtypes.EventPriortiy {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "critical", "fatal", "emergency", "p1", "high", "error", "major", "p2":
		return eventtypes.EventPriortiyHigh
	case "medium", "moderate", "warning", "warn", "p3":
		return eventtypes.EventPriortiyMedium
	case "low", "info", "informational", "minor", "p4", "p5":
		return eventtypes.EventPriortiyLow
	default:
		return eventtypes.EventPriortiyLow
	}
}

// GetSettingValue returns the value of a named setting from the integration config slice.
// It returns the first match found. The bool indicates whether the key was present.
func GetSettingValue(settings []IntegrationConfigValue, name string) (string, bool) {
	for _, s := range settings {
		if s.Name == name {
			return s.Value, true
		}
	}
	return "", false
}

// AccountMappingRule is one rule of the rule-based account_mapping shape.
// Match is AND across keys (every entry must hit) and OR within values
// (any of the listed values matches that key). AccountId is the routing
// target when the rule matches.
type AccountMappingRule struct {
	Match     map[string][]string
	AccountId string
}

// AccountMapping holds the parsed account_mapping configuration. Exactly one
// of Rules / Legacy is populated for a given config value:
//   - Rules:  new rule-based shape   {"rules":[{"match":{...},"accountId":"..."}]}
//   - Legacy: original flat shape    {"labelName":"env","dev":"acc-1",...}
//
// The legacy branch stays alive because existing rows on disk still use it.
// New configs written by the UI use the rules branch.
type AccountMapping struct {
	Rules  []AccountMappingRule
	Legacy map[string]string
}

// IsEmpty reports whether the mapping carries no usable routing info.
func (m *AccountMapping) IsEmpty() bool {
	return m == nil || (len(m.Rules) == 0 && len(m.Legacy) == 0)
}

// ParseAccountMapping extracts the account_mapping JSON from settings and
// returns a parsed AccountMapping. Returns nil when the setting is missing,
// empty, or unparseable.
//
// Tolerates three on-disk shapes:
//
//  1. Rules (canonical, written by current UI):
//     {"rules":[{"match":{"env":"prod","region":"us"},"accountId":"<id>"}, ...]}
//     match values may also be string arrays for value-OR semantics:
//     {"rules":[{"match":{"env":["na","eu"]},"accountId":"<id>"}]}
//  2. Legacy flat:  {"labelName":"env","dev":"<acc-id>","prod":"<acc-id>"}
//  3. Legacy nested (older UI): {"labelName":"env","dev":{"label":"k8s-dev","value":"<acc-id>"}}
//
// Shapes 2 and 3 are normalized into AccountMapping.Legacy so callers stay
// agnostic. Shape 3 was written by an older UI build that round-tripped a
// FilterDropdownButton option object straight into the payload.
func ParseAccountMapping(settings []IntegrationConfigValue, logger *slog.Logger) *AccountMapping {
	raw, found := GetSettingValue(settings, "account_mapping")
	if !found || raw == "" {
		return nil
	}
	var top map[string]any
	if err := common.UnmarshalJson([]byte(raw), &top); err != nil {
		if logger != nil {
			logger.Error("failed to unmarshal account_mapping", "error", err)
		}
		return nil
	}
	// Top is nil when the JSON value is literally `null`; treat as no mapping.
	if top == nil {
		return nil
	}

	if rawRules, ok := top["rules"].([]any); ok {
		return &AccountMapping{Rules: parseRuleList(rawRules, logger)}
	}

	legacy := make(map[string]string, len(top))
	for k, v := range top {
		switch val := v.(type) {
		case string:
			legacy[k] = val
		case map[string]any:
			if s, ok := val["value"].(string); ok {
				legacy[k] = s
			} else if logger != nil {
				logger.Warn("account_mapping nested entry missing string value", "key", k)
			}
		default:
			if logger != nil {
				logger.Warn("account_mapping entry has unexpected type", "key", k, "type", fmt.Sprintf("%T", v))
			}
		}
	}
	return &AccountMapping{Legacy: legacy}
}

func parseRuleList(rawRules []any, logger *slog.Logger) []AccountMappingRule {
	rules := make([]AccountMappingRule, 0, len(rawRules))
	for _, rawRule := range rawRules {
		ruleMap, ok := rawRule.(map[string]any)
		if !ok {
			if logger != nil {
				logger.Warn("account_mapping rule is not an object", "type", fmt.Sprintf("%T", rawRule))
			}
			continue
		}
		accountId, _ := ruleMap["accountId"].(string)
		matchAny, ok := ruleMap["match"].(map[string]any)
		if !ok {
			if logger != nil && ruleMap["match"] != nil {
				logger.Warn("account_mapping rule match is not an object", "type", fmt.Sprintf("%T", ruleMap["match"]))
			}
			continue
		}
		match := make(map[string][]string, len(matchAny))
		for k, v := range matchAny {
			switch val := v.(type) {
			case string:
				if val != "" {
					match[k] = []string{val}
				}
			case []any:
				list := make([]string, 0, len(val))
				for _, item := range val {
					if s, ok := item.(string); ok && s != "" {
						list = append(list, s)
					}
				}
				if len(list) > 0 {
					match[k] = list
				}
			default:
				if logger != nil {
					logger.Warn("account_mapping rule match has unexpected value type", "key", k, "type", fmt.Sprintf("%T", v))
				}
			}
		}
		if accountId == "" || len(match) == 0 {
			if logger != nil {
				logger.Warn("account_mapping rule dropped: missing accountId or match", "accountId", accountId, "matchKeys", len(match))
			}
			continue
		}
		rules = append(rules, AccountMappingRule{Match: match, AccountId: accountId})
	}
	return rules
}

// ApplyAccountMapping resolves the accountId using the account_mapping configuration.
//
// When mapping has Rules, walks them top-to-bottom and returns the first rule's
// AccountId whose Match keys all hit corresponding labels (AND across keys,
// OR within each key's value list).
//
// When mapping has only Legacy data, looks up the label specified by
// "labelName" (default "env") and returns the value-keyed account ID.
//
// Returns the original accountId when nothing matches.
func ApplyAccountMapping(accountId string, labels map[string]string, mapping *AccountMapping) string {
	if mapping.IsEmpty() || len(labels) == 0 {
		return accountId
	}
	if len(mapping.Rules) > 0 {
		for _, rule := range mapping.Rules {
			if ruleMatches(rule.Match, labels) {
				return rule.AccountId
			}
		}
		return accountId
	}
	return applyLegacyAccountMapping(accountId, labels, mapping.Legacy)
}

func ruleMatches(match map[string][]string, labels map[string]string) bool {
	for k, allowed := range match {
		got, ok := labels[k]
		if !ok {
			return false
		}
		found := false
		for _, v := range allowed {
			if v == got {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func applyLegacyAccountMapping(accountId string, labels map[string]string, legacy map[string]string) string {
	if len(legacy) == 0 {
		return accountId
	}
	labelName := legacy["labelName"]
	if labelName == "" {
		labelName = "env"
	}
	if labelValue, ok := labels[labelName]; ok {
		if mappedAccountId, ok := legacy[labelValue]; ok && labelValue != "labelName" {
			return mappedAccountId
		}
	}
	return accountId
}

// applyAccountMappingToEvents resolves each event's AccountId. When account_mapping
// is configured and a label matches, the mapped account ID wins. Otherwise the event
// keeps its own AccountId (set by webhook-specific logic) or falls back to the
// integration's configured account. ApplyAccountMapping is a no-op when the mapping
// is empty, so the common "only account_id configured" case still results in the
// fallback being applied here instead of relying on downstream logic.
func applyAccountMappingToEvents(events []EventIncomingWebhook, settings []IntegrationConfigValue, fallbackAccountId string, logger *slog.Logger) {
	mapping := ParseAccountMapping(settings, logger)
	for i := range events {
		base := events[i].AccountId
		if base == "" {
			base = fallbackAccountId
		}
		events[i].AccountId = ApplyAccountMapping(base, events[i].Investigation.Labels, mapping)
	}
}
