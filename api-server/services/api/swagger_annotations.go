package api

// This file exists solely so `swag init` picks up godoc-style annotations for
// public-facing endpoints whose handlers are registered as inline closures
// (closures aren't reachable to swag's AST walker). The functions below are
// never called at runtime — only their leading comment blocks matter. When
// you change a public route, update the matching stub here and rerun
// `make swag`.
//
// `swaggerStubs` references each stub so the `unused` linter sees them as
// used. swag's AST parser reads the comments regardless of references, so
// this sink does not affect spec generation.
var swaggerStubs = []func(){
	swaggerWebhookPagerduty,
	swaggerWebhookNewrelic,
	swaggerWebhookZenduty,
	swaggerWebhookPrometheus,
	swaggerWebhookDatadog,
	swaggerWebhookAzureMonitor,
	swaggerWebhookServicenow,
	swaggerWebhookGrafana,
	swaggerWebhookSplunk,
	swaggerWebhookGcpMonitoring,
	swaggerWebhookDynatrace,
	swaggerWebhookSolarwinds,
	swaggerWebhookWorkflow,
	swaggerWebhookAzureEventGrid,
	swaggerCron,
	swaggerMarketplaceSubscribe,
	swaggerMarketplaceCreateTenantUser,
	swaggerMarketplaceAwsWebhook,
	swaggerMarketplaceAzureWebhook,
	swaggerMarketplaceChargeBuyers,
	swaggerMarketplaceAwsSendTestUsage,
	swaggerExportRecommendations,
}

var _ = swaggerStubs

// ---------------------------------------------------------------------------
// Shared envelopes
// ---------------------------------------------------------------------------

// SuccessMessage is the {"message": "..."} envelope returned by most write endpoints.
type SuccessMessage struct {
	Message string `json:"message" example:"success"`
}

// ErrorMessage is the {"error": "..."} envelope returned on validation/processing failure.
type ErrorMessage struct {
	Error string `json:"error" example:"failed to read request body"`
}

// ActionError mirrors common.ErrorActionBadRequest output —
// {"message": "...", "extensions": {"code": "..."}} — the standard Hasura
// action error envelope.
type ActionError struct {
	Message    string `json:"message" example:"invalid json"`
	Extensions struct {
		Code string `json:"code" example:"bad_request"`
	} `json:"extensions"`
}

// WebhookPayload is a deliberately untyped passthrough — each provider
// (PagerDuty, Datadog, …) ships a different shape and the handler only
// validates + forwards. Swagger UI will render it as a free-form JSON editor.
type WebhookPayload map[string]interface{}

// ---------------------------------------------------------------------------
// Provider webhooks (POST /api/webhooks/*) — public, unauthenticated.
// ---------------------------------------------------------------------------

// swaggerWebhookPagerduty godoc
// @Summary      PagerDuty webhook
// @Description  Receives PagerDuty incident events. Validated and queued for async processing.
// @Tags         webhooks
// @Accept       json
// @Produce      json
// @Param        payload body WebhookPayload true "PagerDuty event payload (provider-defined)"
// @Success      200 {object} SuccessMessage
// @Failure      400 {object} ActionError
// @Router       /api/webhooks/pagerduty [post]
func swaggerWebhookPagerduty() {}

// swaggerWebhookNewrelic godoc
// @Summary      New Relic webhook
// @Tags         webhooks
// @Accept       json
// @Produce      json
// @Param        payload body WebhookPayload true "New Relic event payload"
// @Success      200 {object} SuccessMessage
// @Failure      400 {object} ActionError
// @Router       /api/webhooks/newrelic [post]
func swaggerWebhookNewrelic() {}

// swaggerWebhookZenduty godoc
// @Summary      Zenduty webhook
// @Tags         webhooks
// @Accept       json
// @Produce      json
// @Param        payload body WebhookPayload true "Zenduty event payload"
// @Success      200 {object} SuccessMessage
// @Failure      400 {object} ActionError
// @Router       /api/webhooks/zenduty [post]
func swaggerWebhookZenduty() {}

// swaggerWebhookPrometheus godoc
// @Summary      Prometheus Alertmanager webhook
// @Tags         webhooks
// @Accept       json
// @Produce      json
// @Param        payload body WebhookPayload true "Alertmanager v4 webhook payload"
// @Success      200 {object} SuccessMessage
// @Failure      400 {object} ActionError
// @Router       /api/webhooks/prometheus-alertmanager [post]
func swaggerWebhookPrometheus() {}

// swaggerWebhookDatadog godoc
// @Summary      Datadog webhook
// @Tags         webhooks
// @Accept       json
// @Produce      json
// @Param        payload body WebhookPayload true "Datadog event payload"
// @Success      200 {object} SuccessMessage
// @Failure      400 {object} ActionError
// @Router       /api/webhooks/datadog [post]
func swaggerWebhookDatadog() {}

// swaggerWebhookAzureMonitor godoc
// @Summary      Azure Monitor webhook
// @Tags         webhooks
// @Accept       json
// @Produce      json
// @Param        payload body WebhookPayload true "Azure Monitor common-alert-schema payload"
// @Success      200 {object} SuccessMessage
// @Failure      400 {object} ActionError
// @Router       /api/webhooks/azure-monitor [post]
func swaggerWebhookAzureMonitor() {}

// swaggerWebhookServicenow godoc
// @Summary      ServiceNow webhook
// @Tags         webhooks
// @Accept       json
// @Produce      json
// @Param        payload body WebhookPayload true "ServiceNow event payload"
// @Success      200 {object} SuccessMessage
// @Failure      400 {object} ActionError
// @Router       /api/webhooks/servicenow [post]
func swaggerWebhookServicenow() {}

// swaggerWebhookGrafana godoc
// @Summary      Grafana webhook
// @Tags         webhooks
// @Accept       json
// @Produce      json
// @Param        payload body WebhookPayload true "Grafana unified-alerting payload"
// @Success      200 {object} SuccessMessage
// @Failure      400 {object} ActionError
// @Router       /api/webhooks/grafana [post]
func swaggerWebhookGrafana() {}

// swaggerWebhookSplunk godoc
// @Summary      Splunk webhook
// @Tags         webhooks
// @Accept       json
// @Produce      json
// @Param        payload body WebhookPayload true "Splunk alert payload"
// @Success      200 {object} SuccessMessage
// @Failure      400 {object} ActionError
// @Router       /api/webhooks/splunk [post]
func swaggerWebhookSplunk() {}

// swaggerWebhookGcpMonitoring godoc
// @Summary      GCP Monitoring webhook
// @Tags         webhooks
// @Accept       json
// @Produce      json
// @Param        payload body WebhookPayload true "GCP Cloud Monitoring notification payload"
// @Success      200 {object} SuccessMessage
// @Failure      400 {object} ActionError
// @Router       /api/webhooks/gcp-monitoring [post]
func swaggerWebhookGcpMonitoring() {}

// swaggerWebhookDynatrace godoc
// @Summary      Dynatrace webhook
// @Tags         webhooks
// @Accept       json
// @Produce      json
// @Param        payload body WebhookPayload true "Dynatrace problem notification payload"
// @Success      200 {object} SuccessMessage
// @Failure      400 {object} ActionError
// @Router       /api/webhooks/dynatrace [post]
func swaggerWebhookDynatrace() {}

// swaggerWebhookSolarwinds godoc
// @Summary      SolarWinds webhook
// @Tags         webhooks
// @Accept       json
// @Produce      json
// @Param        payload body WebhookPayload true "SolarWinds event payload"
// @Success      200 {object} SuccessMessage
// @Failure      400 {object} ActionError
// @Router       /api/webhooks/solarwinds [post]
func swaggerWebhookSolarwinds() {}

// swaggerWebhookWorkflow godoc
// @Summary      Inbound workflow trigger webhook
// @Description  Receives the JSON payload that triggers a Nudgebee workflow integration. Validated against the workflow's webhook schema, then queued.
// @Tags         webhooks
// @Accept       json
// @Produce      json
// @Param        payload body WebhookPayload true "Workflow trigger payload (matches the workflow integration's declared schema)"
// @Success      200 {object} SuccessMessage
// @Failure      400 {object} ActionError
// @Router       /api/webhooks/workflow [post]
func swaggerWebhookWorkflow() {}

// swaggerWebhookAzureEventGrid godoc
// @Summary      Azure Event Grid webhook
// @Description  Handles the Event Grid validation handshake (`SubscriptionValidationEvent`) and forwards `Microsoft.*` events to the cloud-collector.
// @Tags         webhooks
// @Accept       json
// @Produce      json
// @Param        payload body WebhookPayload true "Event Grid event array"
// @Success      200 {object} SuccessMessage "Validation response or relay-success message"
// @Failure      400 {object} ErrorMessage
// @Failure      502 {object} ErrorMessage "Relay to cloud-collector failed"
// @Router       /api/webhooks/azure-eventgrid [post]
func swaggerWebhookAzureEventGrid() {}

// ---------------------------------------------------------------------------
// Cron entry point (POST /rpc-cron)
// ---------------------------------------------------------------------------

// swaggerCron godoc
// @Summary      Scheduled-trigger entry point
// @Description  Invoked by the runbook-server cron scheduler. Branches on `payload.name` to dispatch the matching cron job (e.g. `finops-score-recompute`). Background work runs detached from the request.
// @Tags         rpc
// @Accept       json
// @Produce      json
// @Param        payload body map[string]interface{} true "Scheduled-event payload (`{id, name, payload, scheduled_time}`)"
// @Success      200 {object} SuccessMessage
// @Failure      400 {object} ActionError
// @Security     ActionToken
// @Router       /rpc-cron [post]
func swaggerCron() {}

// ---------------------------------------------------------------------------
// Marketplace endpoints (POST /marketplace/*)
// Mostly invoked by AWS / Azure marketplace systems and internal billing.
// ---------------------------------------------------------------------------

// swaggerMarketplaceSubscribe godoc
// @Summary      Add a marketplace customer subscription
// @Tags         marketplace
// @Accept       json
// @Produce      json
// @Param        payload body map[string]interface{} true "marketplace.CustomerSubscription"
// @Success      200 {object} map[string]interface{} "Created subscription record"
// @Failure      400 {object} ActionError
// @Router       /marketplace/subscribe [post]
func swaggerMarketplaceSubscribe() {}

// swaggerMarketplaceCreateTenantUser godoc
// @Summary      Create tenant + initial user from a marketplace signup
// @Tags         marketplace
// @Accept       json
// @Produce      json
// @Param        payload body map[string]interface{} true "marketplace.NewCustomerTenantRequest"
// @Success      200 {object} map[string]interface{}
// @Failure      400 {object} ActionError
// @Router       /marketplace/create/tenant-user [post]
func swaggerMarketplaceCreateTenantUser() {}

// swaggerMarketplaceAwsWebhook godoc
// @Summary      AWS Marketplace subscription webhook
// @Tags         marketplace
// @Accept       json
// @Produce      json
// @Param        payload body map[string]interface{} true "marketplace.CustomerSubscription"
// @Success      200 {object} map[string]interface{}
// @Failure      400 {object} ActionError
// @Router       /marketplace/aws/webhook [post]
func swaggerMarketplaceAwsWebhook() {}

// swaggerMarketplaceAzureWebhook godoc
// @Summary      Azure Marketplace subscription webhook
// @Tags         marketplace
// @Accept       json
// @Produce      json
// @Param        payload body map[string]interface{} true "marketplace.AzurePayload"
// @Success      200 {object} map[string]interface{}
// @Failure      400 {object} ActionError
// @Router       /marketplace/azure/webhook [post]
func swaggerMarketplaceAzureWebhook() {}

// swaggerMarketplaceChargeBuyers godoc
// @Summary      Send marketplace usage reports for billing
// @Description  Triggers SendUsageReportsToMarketplacesForBilling. Internal/admin only.
// @Tags         marketplace
// @Produce      json
// @Success      200 {object} SuccessMessage
// @Failure      400 {object} ActionError
// @Security     ActionToken
// @Router       /marketplace/billing/charge-buyers [post]
func swaggerMarketplaceChargeBuyers() {}

// swaggerMarketplaceAwsSendTestUsage godoc
// @Summary      Send a test metered-usage record to AWS
// @Tags         marketplace
// @Accept       json
// @Produce      json
// @Param        payload body map[string]interface{} true "marketplace.TestMeteredUsageRequest"
// @Success      200 {object} SuccessMessage
// @Failure      400 {object} ActionError
// @Security     ActionToken
// @Router       /marketplace/aws/send-test-usage [post]
func swaggerMarketplaceAwsSendTestUsage() {}

// ---------------------------------------------------------------------------
// Recommendations export (POST /v1/export/recommendations) — Hasura action.
// ---------------------------------------------------------------------------

// ExportRecommendationsResult is the shape returned to Hasura on successful export.
type ExportRecommendationsResult struct {
	Format      string `json:"format" example:"csv"`
	RecordCount int    `json:"record_count" example:"42"`
	Filename    string `json:"filename" example:"recommendations-2026-05-07.csv"`
	Content     string `json:"content" example:"<base64-encoded payload>"`
}

// swaggerExportRecommendations godoc
// @Summary      Export recommendations as CSV/XLSX
// @Description  Hasura action endpoint. Wraps an ExportRecommendationsRequest in the standard Hasura action envelope (`{action, input, session_variables}`).
// @Tags         export
// @Accept       json
// @Produce      json
// @Param        payload body map[string]interface{} true "Hasura action envelope wrapping ExportRecommendationsRequest in `input.request`"
// @Success      200 {object} ExportRecommendationsResult
// @Failure      400 {object} ActionError
// @Security     ActionToken
// @Router       /v1/export/recommendations [post]
func swaggerExportRecommendations() {}
