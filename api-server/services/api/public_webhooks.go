package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"nudgebee/services/common"
	"nudgebee/services/config"
	"nudgebee/services/integrations/core"
	webhook_queue "nudgebee/services/integrations/core/webhook_queue"
	"nudgebee/services/security"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// genericWebhookHandler creates a generic webhook handler for different webhook types
func genericWebhookHandler(webhookName string, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) gin.HandlerFunc {
	metricName := "webhook_" + webhookName

	return func(c *gin.Context) {
		common.MetricsApiRequestsTotal(c.Request.Context(), metricName)
		requestUrl := c.Request.URL.String()

		// Extract headers
		headers := map[string]string{}
		for k, v := range c.Request.Header {
			headers[k] = v[0]
		}

		// Defer body close for proper resource cleanup
		defer func() {
			if err := c.Request.Body.Close(); err != nil {
				logger.Error("Error closing request body", "webhook", webhookName, "error", err)
			}
		}()

		// Read request body
		bodybytes, err := io.ReadAll(c.Request.Body)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), metricName, "read_body_failed")
			logger.Error("integration: failed to read webhook request body", "webhook", webhookName, "error", err)
			c.JSON(400, common.ErrorHasuraActionBadRequest("failed to read request body"))
			return
		}

		payload := string(bodybytes)
		sc := security.NewRequestContextForSuperAdmin(logger, tracer, meter)

		webhookRowID, err := core.ValidateAndStoreWebhook(sc, requestUrl, headers, payload)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), metricName, "validate_webhook_failed")
			logger.Warn("integration: webhook validation failed",
				"webhook", webhookName, "url", requestUrl, "error", err)
			c.JSON(400, common.ErrorHasuraActionBadRequest("webhook validation failed"))
			return
		}

		if pubErr := webhook_queue.PublishWebhookProcess(webhookRowID); pubErr != nil {
			// RabbitMQ unavailable — fall back to sync processing
			logger.Warn("webhook: RabbitMQ unavailable, falling back to sync processing", "webhook", webhookName, "error", pubErr)
			if procErr := core.ProcessStoredWebhook(sc, webhookRowID); procErr != nil {
				common.MetricsApiRequestsFailedTotal(c.Request.Context(), metricName, "process_webhook_failed")
				logger.Error("integration: error processing webhook request",
					"webhook", webhookName, "url", requestUrl, "error", procErr)
			}
		}

		c.JSON(200, map[string]string{"message": "success"})
	}
}

// azureEventGridWebhookHandler handles Azure Event Grid webhook deliveries.
// It performs the Event Grid validation handshake and forwards events to cloud-collector.
func azureEventGridWebhookHandler(tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		common.MetricsApiRequestsTotal(c.Request.Context(), "webhook_azure_eventgrid")

		token := c.Query("token")
		if token == "" {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "webhook_azure_eventgrid", "missing_token")
			c.JSON(400, map[string]string{"error": "missing token query parameter"})
			return
		}

		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "webhook_azure_eventgrid", "read_body_failed")
			c.JSON(400, map[string]string{"error": "failed to read request body"})
			return
		}

		// Event Grid sends events as a JSON array
		var events []json.RawMessage
		if err := json.Unmarshal(body, &events); err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "webhook_azure_eventgrid", "parse_failed")
			logger.Error("azure-eventgrid: failed to parse event array", "error", err)
			c.JSON(400, map[string]string{"error": "invalid event grid payload"})
			return
		}

		// Check for validation handshake event
		for _, rawEvent := range events {
			var event struct {
				EventType string          `json:"eventType"`
				Data      json.RawMessage `json:"data"`
			}
			if err := json.Unmarshal(rawEvent, &event); err != nil {
				continue
			}
			if event.EventType == "Microsoft.EventGrid.SubscriptionValidationEvent" {
				var validationData struct {
					ValidationCode string `json:"validationCode"`
				}
				if err := json.Unmarshal(event.Data, &validationData); err != nil {
					logger.Error("azure-eventgrid: failed to parse validation data", "error", err)
					c.JSON(400, map[string]string{"error": "invalid validation event"})
					return
				}
				logger.Info("azure-eventgrid: responding to subscription validation handshake")
				c.JSON(200, map[string]string{"validationResponse": validationData.ValidationCode})
				return
			}
		}

		// Forward each event to cloud-collector
		relayFailed := false
		for _, rawEvent := range events {
			relayURL := fmt.Sprintf("%s/v1/cloud/process_azure_eventgrid_events?token=%s",
				config.Config.CloudCollectorServerUrl, token)

			resp, err := common.HttpPost(relayURL,
				common.HttpWithHeaders(map[string]string{
					config.Config.CloudCollectorServerTokenHeader: config.Config.CloudCollectorServerToken,
				}),
				common.HttpWithBody(io.NopCloser(bytes.NewReader(rawEvent))),
			)
			if err != nil {
				logger.Error("azure-eventgrid: failed to relay event to cloud-collector", "error", err)
				relayFailed = true
				continue
			}
			if resp.Body != nil {
				_ = resp.Body.Close()
			}
			if resp.StatusCode >= 400 {
				logger.Error("azure-eventgrid: cloud-collector returned error", "status", resp.StatusCode)
				relayFailed = true
			}
		}

		if relayFailed {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "webhook_azure_eventgrid", "relay_failed")
			c.JSON(502, map[string]string{"error": "failed to relay one or more events"})
			return
		}
		c.JSON(200, map[string]string{"message": "success"})
	}
}

func handlePublicWebhooksApis(r *gin.Engine, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	webhookGroup := r.Group("/api/webhooks")

	// Register webhook handlers using the generic handler
	webhookGroup.POST("/pagerduty", genericWebhookHandler("pagerduty", tracer, meter, logger))
	webhookGroup.POST("/newrelic", genericWebhookHandler("newrelic", tracer, meter, logger))
	webhookGroup.POST("/zenduty", genericWebhookHandler("zenduty", tracer, meter, logger))
	webhookGroup.POST("/prometheus-alertmanager", genericWebhookHandler("prometheus_alertmanager", tracer, meter, logger))
	webhookGroup.POST("/datadog", genericWebhookHandler("datadog", tracer, meter, logger))
	webhookGroup.POST("/azure-monitor", genericWebhookHandler("azure_monitor", tracer, meter, logger))
	webhookGroup.POST("/servicenow", genericWebhookHandler("servicenow-webhook", tracer, meter, logger))
	webhookGroup.POST("/grafana", genericWebhookHandler("grafana", tracer, meter, logger))
	webhookGroup.POST("/splunk", genericWebhookHandler("splunk_webhook", tracer, meter, logger))
	webhookGroup.POST("/gcp-monitoring", genericWebhookHandler("gcp_monitoring_webhook", tracer, meter, logger))
	webhookGroup.POST("/dynatrace", genericWebhookHandler("dynatrace_webhook", tracer, meter, logger))
	webhookGroup.POST("/solarwinds", genericWebhookHandler("solarwinds_webhook", tracer, meter, logger))
	webhookGroup.POST("/workflow", genericWebhookHandler("workflow_webhook", tracer, meter, logger))

	// Azure Event Grid webhook — custom handler for validation handshake + relay to cloud-collector
	webhookGroup.POST("/azure-eventgrid", azureEventGridWebhookHandler(tracer, meter, logger))
}
