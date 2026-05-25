package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"nudgebee/services/account"
	"nudgebee/services/common"
	"nudgebee/services/config"
	_ "nudgebee/services/event/queue"
	"nudgebee/services/integrations/core"
	webhook_queue "nudgebee/services/integrations/core/webhook_queue"
	"nudgebee/services/security"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/go-github/v61/github"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// githubWebhookDedupNamespace stores X-GitHub-Delivery UUIDs of webhooks
// we've already processed. Backed by the shared cache (Redis in prod), so
// dedup works across api-server replicas and survives pod restarts.
// 24h TTL covers GitHub's full retry window (up to 8 retries / 24h).
const (
	githubWebhookDedupNamespace = "github_webhook_delivery"
	githubWebhookDedupTTL       = 24 * time.Hour
	// Bound the outer dispatch goroutine. processResolution spawns its own
	// 35-min child for the LLM call; this only needs to cover the DB
	// lookup + UPDATE + child-goroutine launch.
	githubWebhookDispatchTimeout = 2 * time.Minute
)

func init() {
	common.CacheCreateNamespace(githubWebhookDedupNamespace,
		common.CacheNamespaceWithExpiration(githubWebhookDedupTTL))
}

// githubWebhookDeliverySeen returns true if the delivery ID has already been
// processed within githubWebhookDedupTTL. Cache failures fall back to
// "process it" — the addressing-state guard in ProcessOpenPRResolution
// remains the correctness backstop, so the worst case is one duplicate LLM
// run, not lost work.
func githubWebhookDeliverySeen(deliveryID string) bool {
	if deliveryID == "" {
		// No delivery header — better to process than to drop. GitHub always
		// sends X-GitHub-Delivery, so this only fires for manual / forged
		// requests, which HMAC verification will have already vetted.
		return false
	}
	if _, ok := common.CacheGet(githubWebhookDedupNamespace, deliveryID); ok {
		return true
	}
	if err := common.CacheSet(githubWebhookDedupNamespace, deliveryID, []byte("1"),
		common.CacheSetWithExpiration(githubWebhookDedupTTL)); err != nil {
		slog.Warn("github webhook: dedup cache set failed",
			"delivery_id", deliveryID, "error", err)
	}
	return false
}

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
			c.JSON(400, common.ErrorActionBadRequest("failed to read request body"))
			return
		}

		payload := string(bodybytes)
		sc := security.NewRequestContextForSuperAdmin(logger, tracer, meter)

		webhookRowID, err := core.ValidateAndStoreWebhook(sc, requestUrl, headers, payload)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), metricName, "validate_webhook_failed")
			logger.Warn("integration: webhook validation failed",
				"webhook", webhookName, "url", requestUrl, "error", err)
			c.JSON(400, common.ErrorActionBadRequest("webhook validation failed"))
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

// githubWebhookHandler accepts GitHub App webhook deliveries and dispatches
// followup work for any PR our code agent has open. HMAC-verified via
// X-Hub-Signature-256 using config.Config.GithubWebhookSecret. Idempotent on
// X-GitHub-Delivery so retries are safe. Only reacts to events that indicate
// new work to address (CI failure, review submitted, review comment) or
// terminal state (PR closed). Unknown / uninteresting events return 200 fast
// without DB I/O.
//
// Authorization model: HMAC proves the payload came from GitHub. Whether the
// PR belongs to a tenant we own is decided by FindOpenPRResolutionByURL —
// no match means "not our PR", and we 200-and-drop. No installation_id ↔
// tenant_id lookup is required because the resolution row already carries
// the tenant.
func githubWebhookHandler(tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		common.MetricsApiRequestsTotal(c.Request.Context(), "webhook_github")

		secret := config.Config.GithubWebhookSecret
		if secret == "" {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "webhook_github", "secret_not_configured")
			logger.Error("github webhook: GITHUB_WEBHOOK_SECRET is not configured; rejecting delivery")
			c.JSON(503, map[string]string{"error": "webhook secret not configured"})
			return
		}

		// github.ValidatePayload reads and verifies the body, returning the
		// raw bytes for ParseWebHook. It is constant-time on the signature
		// compare. Failures here mean either bad signature or unreadable
		// body — both 401 from our perspective.
		payload, err := github.ValidatePayload(c.Request, []byte(secret))
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "webhook_github", "invalid_signature")
			logger.Warn("github webhook: signature verification failed", "error", err)
			c.JSON(401, map[string]string{"error": "invalid signature"})
			return
		}

		deliveryID := c.GetHeader("X-GitHub-Delivery")
		if githubWebhookDeliverySeen(deliveryID) {
			logger.Info("github webhook: duplicate delivery, skipping", "delivery_id", deliveryID)
			c.JSON(200, map[string]string{"status": "duplicate"})
			return
		}

		eventType := github.WebHookType(c.Request)
		event, err := github.ParseWebHook(eventType, payload)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "webhook_github", "parse_failed")
			logger.Warn("github webhook: failed to parse event", "event_type", eventType, "error", err)
			c.JSON(400, map[string]string{"error": "failed to parse event"})
			return
		}

		prURL, action, terminal, interesting := extractGithubPRSignal(event)
		if !interesting {
			c.JSON(200, map[string]string{"status": "ignored"})
			return
		}

		if prURL == "" {
			logger.Info("github webhook: event matched but no PR URL extracted",
				"event_type", eventType, "action", action)
			c.JSON(200, map[string]string{"status": "no_pr_url"})
			return
		}

		resolutionID, tableName, err := account.FindOpenPRResolutionByURL(prURL)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "webhook_github", "lookup_failed")
			logger.Error("github webhook: failed to look up resolution", "pr_url", prURL, "error", err)
			c.JSON(500, map[string]string{"error": "resolution lookup failed"})
			return
		}
		if resolutionID == "" {
			logger.Info("github webhook: no open resolution row for PR", "pr_url", prURL, "event_type", eventType)
			c.JSON(200, map[string]string{"status": "no_match"})
			return
		}

		logger.Info("github webhook: dispatching followup",
			"pr_url", prURL,
			"event_type", eventType,
			"action", action,
			"terminal", terminal,
			"resolution_id", resolutionID,
			"table", tableName)

		// processResolution (called by ProcessOpenPRResolution) already spawns
		// the followup work in a recovered goroutine, so we don't need to
		// double-wrap. We still launch a goroutine here so the webhook
		// returns 200 fast (<10s budget) even if the row update / lookup
		// stalls on the database. The outer timeout caps DB lookup +
		// claim + child-goroutine launch; the inner LLM call has its own
		// 35-min bound. Panics in the dispatch path can never take down
		// the api-server.
		sc := security.NewRequestContextForSuperAdmin(logger, tracer, meter)
		go func() {
			defer func() {
				if r := recover(); r != nil {
					logger.Error("github webhook: panic in dispatch goroutine",
						"resolution_id", resolutionID, "recover", r)
				}
			}()
			bgCtx, cancel := context.WithTimeout(context.Background(), githubWebhookDispatchTimeout)
			defer cancel()
			boundedSc := security.NewRequestContext(bgCtx, sc.GetSecurityContext(),
				sc.GetLogger(), sc.GetTracer(), sc.GetMeter())
			if err := account.ProcessOpenPRResolution(boundedSc, resolutionID, tableName); err != nil {
				logger.Error("github webhook: ProcessOpenPRResolution failed",
					"resolution_id", resolutionID, "table", tableName, "error", err)
			}
		}()

		c.JSON(200, map[string]any{
			"status":        "queued",
			"resolution_id": resolutionID,
		})
	}
}

// extractGithubPRSignal inspects a parsed GitHub webhook event and decides
// whether it should fire a followup. Returns:
//   - prURL: the canonical https://github.com/<owner>/<repo>/pull/<number>
//     URL stored in our resolution rows, empty if the event has no PR
//     attachment.
//   - action: the event's "action" field, for logging.
//   - terminal: true for PR-closed events. We still pass these through so
//     downstream logic can mark the resolution row terminal (future work).
//   - interesting: true if we should attempt to dispatch.
//
// Events we ignore (return interesting=false) include push, PR open/edit/
// label, ping, and any unknown type. The cron remains the backstop.
func extractGithubPRSignal(event any) (prURL, action string, terminal, interesting bool) {
	switch e := event.(type) {
	case *github.CheckRunEvent:
		if e.GetAction() != "completed" {
			return "", e.GetAction(), false, false
		}
		conclusion := e.GetCheckRun().GetConclusion()
		if conclusion != "failure" && conclusion != "timed_out" && conclusion != "action_required" {
			return "", e.GetAction(), false, false
		}
		for _, pr := range e.GetCheckRun().PullRequests {
			if url := buildPRURL(e.GetRepo().GetHTMLURL(), pr.GetNumber()); url != "" {
				return url, e.GetAction(), false, true
			}
		}
		return "", e.GetAction(), false, true

	case *github.WorkflowRunEvent:
		if e.GetAction() != "completed" {
			return "", e.GetAction(), false, false
		}
		conclusion := e.GetWorkflowRun().GetConclusion()
		if conclusion != "failure" && conclusion != "timed_out" {
			return "", e.GetAction(), false, false
		}
		for _, pr := range e.GetWorkflowRun().PullRequests {
			if url := buildPRURL(e.GetRepo().GetHTMLURL(), pr.GetNumber()); url != "" {
				return url, e.GetAction(), false, true
			}
		}
		return "", e.GetAction(), false, true

	case *github.PullRequestReviewEvent:
		if e.GetAction() != "submitted" {
			return "", e.GetAction(), false, false
		}
		state := e.GetReview().GetState()
		// "approved" reviews don't need a code fix; skip them. We respond to
		// changes_requested and commented (the latter often contains actionable
		// inline feedback even without a formal request-changes).
		if state != "changes_requested" && state != "commented" {
			return "", e.GetAction(), false, false
		}
		return e.GetPullRequest().GetHTMLURL(), e.GetAction(), false, true

	case *github.PullRequestReviewCommentEvent:
		if e.GetAction() != "created" {
			return "", e.GetAction(), false, false
		}
		return e.GetPullRequest().GetHTMLURL(), e.GetAction(), false, true

	case *github.PullRequestEvent:
		if e.GetAction() != "closed" {
			return "", e.GetAction(), false, false
		}
		// Terminal: PR was merged or closed. We surface the signal so a
		// future change can transition the resolution row out of
		// 'created'/'needs_followup'. For now, mark interesting=true so the
		// dispatch path runs and its no-op gate logs the terminal state.
		return e.GetPullRequest().GetHTMLURL(), e.GetAction(), true, true
	}

	return "", "", false, false
}

func buildPRURL(repoHTMLURL string, prNumber int) string {
	if repoHTMLURL == "" || prNumber <= 0 {
		return ""
	}
	return fmt.Sprintf("%s/pull/%d", repoHTMLURL, prNumber)
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

	// GitHub App webhook — drives near-real-time PR followup. Fast path
	// alongside the pr-lifecycle-check cron (which now runs as a backstop).
	webhookGroup.POST("/github", githubWebhookHandler(tracer, meter, logger))
}
