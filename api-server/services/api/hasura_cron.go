package api

import (
	"context"
	"log/slog"
	"nudgebee/services/account"
	"nudgebee/services/anomoly"
	"nudgebee/services/application"
	"nudgebee/services/common"
	"nudgebee/services/crawl"
	"nudgebee/services/event"
	"nudgebee/services/eventrule"
	"nudgebee/services/insight"
	"nudgebee/services/integrations"
	"nudgebee/services/internal/database"
	"nudgebee/services/knowledge_graph/core"
	kgmodels "nudgebee/services/knowledge_graph/models"
	kgqueue "nudgebee/services/knowledge_graph/queue"
	"nudgebee/services/ml"
	"nudgebee/services/nb"
	"nudgebee/services/observability"
	"nudgebee/services/pr_raise"
	"nudgebee/services/recommendation"
	"nudgebee/services/reports"
	"nudgebee/services/security"
	"nudgebee/services/slo"
	"nudgebee/services/tenant"
	"nudgebee/services/traces"
	"nudgebee/services/triage"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

type HasuraCronRequest struct {
	Comment       string         `json:"comment"`
	Id            string         `json:"id"`
	Name          string         `json:"name"`
	Payload       map[string]any `json:"payload"`
	ScheduledTime string         `json:"scheduled_time"`
}

func buildContextFromHasuraCronPayload(c *gin.Context, h *HasuraCronRequest, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) *security.RequestContext {
	var ctx context.Context
	if c != nil && c.Request != nil && c.Request.Context() != nil {
		// Detach from HTTP request lifecycle — all cron handlers run in background
		// goroutines after returning 200, so the request context would be canceled.
		ctx = context.WithoutCancel(c.Request.Context())
	} else {
		ctx = context.Background()
	}
	span := trace.SpanFromContext(ctx)
	childLogger := logger.With("cron_job", h.Name, "cron_id", h.Id, "trace_id", span.SpanContext().TraceID().String())
	return security.NewRequestContext(ctx, security.NewSecurityContextForSuperAdmin(), childLogger, tracer, meter)
}

func handleHasuraCrons(r *gin.Engine, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	r.POST("/hasura-cron", func(c *gin.Context) {
		common.MetricsApiRequestsTotal(c.Request.Context(), "hasura_cron")
		var hasuraPayload HasuraCronRequest
		err := c.ShouldBindJSON(&hasuraPayload)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "hasura_cron", "invalid_json")
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		ctx := buildContextFromHasuraCronPayload(c, &hasuraPayload, tracer, meter, logger)
		//TODO as this takes time, we should consider moving this to a go routine
		switch hasuraPayload.Name {
		case "finops-score-recompute":
			go func() {
				t0 := time.Now()
				ctx.GetLogger().Info("cron: recomputing finops scores")
				err := recommendation.RecomputeAllFinOpsScores(ctx)
				if err != nil {
					ctx.GetLogger().Error("cron: error recomputing finops scores", "error", err)
				}
				ctx.GetLogger().Info("cron: finops score recompute done", "duration", time.Since(t0))
			}()
			c.JSON(200, gin.H{"status": "ok"})
		case "recommendation-nudge-digest":
			go func() {
				t0 := time.Now()
				ctx.GetLogger().Info("cron: sending recommendation nudge digest")
				err := reports.SendRecommendationNudgeDigest(ctx)
				if err != nil {
					ctx.GetLogger().Error("cron: error sending recommendation nudge digest", "error", err)
				}
				ctx.GetLogger().Info("cron: recommendation nudge digest done", "duration", time.Since(t0))
			}()
			c.JSON(200, gin.H{"status": "ok"})
		case "recommendation-proactive-nudge":
			go func() {
				t0 := time.Now()
				ctx.GetLogger().Info("cron: processing proactive nudges")
				err := recommendation.ProcessProactiveNudges(ctx)
				if err != nil {
					ctx.GetLogger().Error("cron: error processing proactive nudges", "error", err)
				}
				ctx.GetLogger().Info("cron: proactive nudges done", "duration", time.Since(t0))
			}()
			c.JSON(200, gin.H{"status": "ok"})
		case "daily-highlight-report":
			reportRequest := reports.TenantReportRequest{}
			err := common.UnmarshalMapToStruct(hasuraPayload.Payload, &reportRequest)
			if err != nil {
				common.MetricsApiRequestsFailedTotal(c.Request.Context(), "hasura_cron", "unmarshal_payload_failed")
				c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
				return
			}

			go func() {
				t0 := time.Now()
				err = reports.SendDailyHighlightEmailReport(ctx, reportRequest)
				if err != nil {
					ctx.GetLogger().Info("cron: Daily highlight report failed", "error", err)
				}
				ctx.GetLogger().Info("cron: Daily highlight report sent", "duration", time.Since(t0))
				err = reports.SendDailyAgentStatusEmail(ctx, reportRequest)
				if err != nil {
					logger.Info("Daily agent status report failed", "error", err)
				}
				ctx.GetLogger().Info("cron: Daily agent status report sent", "duration", time.Since(t0))
				err = reports.SendDailyEventsSummaryReport(ctx, reportRequest)
				if err != nil {
					logger.Info("Daily event summary report failed", "error", err)
				}
				ctx.GetLogger().Info("cron: Daily event summary report sent", "duration", time.Since(t0))
			}()

			c.JSON(200, gin.H{"status": "ok"})
		case "Insight refresh":
			go func() {
				// log the start time
				startTime := time.Now()
				ctx.GetLogger().Info("cron: Generating insight As async")
				err = insight.Process(ctx)
				if err != nil {
					ctx.GetLogger().Error("cron: error generating insight", "error", err)
				}
				// log the end time
				endTime := time.Now()
				ctx.GetLogger().Info("cron: Insight analysis completed in", "duration", endTime.Sub(startTime))
			}()
			c.JSON(200, gin.H{"status": "ok"})
		case "Resource Meta refresh":
			go func() {
				err = crawl.AWsResourceMeta(ctx)
				if err != nil {
					ctx.GetLogger().Error("cron: unable to crawl aws resource meta", "error", err)
				}

				err = crawl.AzureResourceMeta(ctx)
				if err != nil {
					ctx.GetLogger().Error("cron: unable to crawl azure resource meta", "error", err)
				}

				err = crawl.CivoResourceMeta(ctx)
				if err != nil {
					ctx.GetLogger().Error("cron: unable to crawl civo resource meta", "error", err)
				}

				err = crawl.GCPResourceMeta(ctx)
				if err != nil {
					ctx.GetLogger().Error("cron: unable to crawl gcp resource meta", "error", err)
				}
			}()
			c.JSON(200, gin.H{"status": "ok"})
		case "Agent Status Check":
			go func() {
				err = account.AgentCheckAndUpdateStatus(ctx)
				if err != nil {
					ctx.GetLogger().Error("cron: error checking agent status", "error", err)
				}
			}()
			c.JSON(200, gin.H{"status": "ok"})
		case "K8s Recommendation refresh":
			accountId := []string{}
			if hasuraPayload.Payload != nil && hasuraPayload.Payload["account_id"] != nil {
				switch v := hasuraPayload.Payload["account_id"].(type) {
				case []any:
					for _, val := range v {
						accountId = append(accountId, val.(string))
					}
				case []string:
					accountId = v
				case string:
					accountId = []string{v}
				default:
					c.JSON(400, common.ErrorHasuraActionBadRequest("Invalid account_id type"))
					return
				}
			}

			// run as async, eventually move to rabbitmq
			go func() {
				ctx.GetLogger().Info("cron: generating recommendations As async")
				_, err := recommendation.GenerateRecommendation(ctx, recommendation.GenerateRecommendationRequest{
					AccountId: accountId,
				})
				if err != nil {
					ctx.GetLogger().Error("cron: error generating recommendations", "error", err)
				}
			}()

			c.JSON(200, gin.H{"status": "ok"})
		case "Security recommendation refresh":
			var accountId []string
			// run as async, eventually move to rabbitmq
			go func() {
				ctx.GetLogger().Info("cron: generating recommendations As async")
				_, err := recommendation.GenerateSecurityRecommendation(ctx, recommendation.GenerateRecommendationRequest{
					AccountId: accountId,
				})
				if err != nil {
					ctx.GetLogger().Error("cron: error generating recommendations", "error", err)
				}
			}()

			c.JSON(200, gin.H{"status": "ok"})
		case "Hasura Event Data Cleanup", "NB Data Cleanup":
			go func() {
				defer func() {
					if r := recover(); r != nil {
						ctx.GetLogger().Error("cron: panic in event-data cleanup goroutine", "recovered", r)
					}
				}()
				// Mark stale KG edges before the deletion cleanup so freshly-tombstoned
				// rows still get the full retention window before nb.CleanupData purges them.
				if dbManager, err := database.GetDatabaseManager(database.Metastore); err != nil {
					ctx.GetLogger().Error("cron: failed to get db manager for kg edge sweep", "error", err)
				} else {
					kgService := core.NewService(ctx, ctx.GetLogger(), dbManager)
					if _, err := kgService.MarkStaleEdgesInactive(); err != nil {
						ctx.GetLogger().Error("cron: kg stale edge sweep failed", "error", err)
					}
				}
				nb.CleanupData(ctx)
			}()
			c.JSON(200, gin.H{"status": "ok"})
		case "Recommendation Resolution Update":
			go func() {
				err = recommendation.UpdateResolutionStatus(ctx)
				if err != nil {
					ctx.GetLogger().Error("cron: error updating resolution status", "error", err)
				}
				err = event.UpdateResolutionStatus(ctx)
				err = pr_raise.UpdateResolutionStatus(ctx)
			}()

			c.JSON(200, gin.H{"status": "ok"})
		case "SLO Execute":
			go func() {
				ctx.GetLogger().Info("cron: calculating slo As async")
				err := slo.Execute()
				if err != nil {
					ctx.GetLogger().Error("cron: error calculating slo", "error", err)
				}
			}()
			c.JSON(200, gin.H{"status": "ok"})
		case "Agent Task Cleanup":
			go func() {
				ctx.GetLogger().Info("cron: cleaning up agent tasks As async")
				err := account.CleanUpAgentTask(ctx)
				if err != nil {
					ctx.GetLogger().Error("cron: error cleaning up agent tasks", "error", err)
				}
			}()
			c.JSON(200, gin.H{"status": "ok"})
		case "Bill Tenants":
			ctx.GetLogger().Info("cron: billing calculations are disabled")
			c.JSON(200, gin.H{"status": "ok"})
		case "Anomaly Execute":
			go func() {
				ctx.GetLogger().Info("cron: processing anomaly")
				err = anomoly.Execute(ctx)
				if err != nil {
					ctx.GetLogger().Error("cron: failed to process anomaly", "error", err)
				}
			}()
			c.JSON(200, gin.H{"status": "ok"})
		case "Spend Anomaly Execute":
			go func() {
				ctx.GetLogger().Info("cron: processing spend anomaly detection")
				err = anomoly.ExecuteSpendAnomaly(ctx)
				if err != nil {
					ctx.GetLogger().Error("cron: failed to process spend anomaly", "error", err)
				}
			}()
			c.JSON(200, gin.H{"status": "ok"})
		case "Load Agent Playbook":
			go func() {
				ctx.GetLogger().Info("cron: Loading Agent Playbook")
				err = eventrule.LoadEventActions(ctx)
				if err != nil {
					ctx.GetLogger().Error("cron: failed to Load Agent Playbook", "error", err)
				}
			}()
			c.JSON(200, gin.H{"status": "ok"})
		case "Application Discovery Refresh":
			go func() {
				ctx.GetLogger().Info("cron: loading Discovery")
				application.Discover(ctx)
			}()
			c.JSON(200, gin.H{"status": "ok"})
		case "Batched Notifications":
			go func() {
				ctx.GetLogger().Info("cron: processing hourly non-prod events batch")
				err := reports.ProcessHourlyEventsBatchNotification(ctx)
				if err != nil {
					ctx.GetLogger().Error("cron: error processing hourly non-prod events batch", "error", err)
				}
			}()
			c.JSON(200, gin.H{"status": "ok"})
		case "Knowledge Graph Refresh":
			go func() {
				ctx.GetLogger().Info("cron: refreshing knowledge graph")

				// Create knowledge graph service
				service, err := traces.NewKnowledgeGraphService()
				if err != nil {
					ctx.GetLogger().Error("cron: failed to create knowledge graph service", "error", err)
					return
				}

				// Refresh knowledge graph for all tenants/accounts
				// You can customize this to refresh specific tenants/accounts from the payload
				request := traces.RefreshKnowledgeGraphRequest{
					ForceRefresh: true,
				}

				// Get tenant and account info from payload if available
				if hasuraPayload.Payload != nil {
					if tenantID, exists := hasuraPayload.Payload["tenant_id"]; exists {
						if tenantStr, ok := tenantID.(string); ok {
							request.TenantID = tenantStr
						}
					}
					if accountID, exists := hasuraPayload.Payload["cloud_account_id"]; exists {
						if accountStr, ok := accountID.(string); ok {
							request.CloudAccountID = accountStr
						}
					}
				}

				_, err = service.RefreshKnowledgeGraph(ctx.GetContext(), request)
				if err != nil {
					ctx.GetLogger().Error("cron: error refreshing knowledge graph", "error", err)
				}
			}()
			c.JSON(200, gin.H{"status": "ok"})
		case "build_knowledge_graph":
			ctx.GetLogger().Info("cron: starting build_knowledge_graph - publishing to queue")

			// Get database manager
			dbManager, err := database.GetDatabaseManager(database.Metastore)
			if err != nil {
				ctx.GetLogger().Error("cron: failed to get database manager", "error", err)
				c.JSON(500, gin.H{"status": "error", "message": "Failed to initialize database"})
				return
			}

			// Get all enabled filters to find unique tenant IDs
			filterRepo := core.NewFilterRepository(dbManager, ctx.GetLogger())
			filters, err := filterRepo.GetAllEnabledFilters(ctx)
			if err != nil {
				ctx.GetLogger().Error("cron: failed to get filters", "error", err)
				c.JSON(500, gin.H{"status": "error", "message": "Failed to retrieve filters"})
				return
			}

			// Also check feature flags for tenants with TRACES_SERVICE_MAP_KNOWLEDGE_GRAPH enabled
			featureTenants, featureErr := tenant.ListTenantWithFeature(ctx, tenant.FEATURE_TRACES_KNOWLEDGE_GRAPH)
			if featureErr != nil {
				ctx.GetLogger().Warn("cron: failed to get tenants with knowledge graph feature", "error", featureErr)
			} else if len(featureTenants) > 0 {
				// Build set of existing tenant IDs from filters
				existingTenants := make(map[string]bool)
				for _, f := range filters {
					existingTenants[f.TenantID.String()] = true
				}

				// Create default filters for tenants with feature enabled but no filter entry
				for _, featureTenantID := range featureTenants {
					if !existingTenants[featureTenantID] {
						newFilter := &kgmodels.KnowledgeGraphTenantFilter{
							TenantID:    uuid.MustParse(featureTenantID),
							FilterName:  "default",
							AccountIDs:  []string{},
							Sources:     []string{},
							FlowSources: []string{},
							Filters:     map[string]interface{}{},
							IsDefault:   true,
							Enabled:     true,
						}
						createErr := filterRepo.CreateFilter(ctx, newFilter)
						if createErr != nil {
							ctx.GetLogger().Warn("cron: failed to create default filter for tenant with feature flag",
								"tenant_id", featureTenantID, "error", createErr)
							continue
						}
						filters = append(filters, newFilter)
						ctx.GetLogger().Info("cron: created default knowledge graph filter for tenant with feature flag",
							"tenant_id", featureTenantID)
					}
				}
			}

			if len(filters) == 0 {
				ctx.GetLogger().Warn("cron: no filters found for build_knowledge_graph")
				c.JSON(200, gin.H{"status": "ok", "message": "no tenants"})
				return
			}

			// Collect unique tenant IDs
			tenantIDSet := make(map[string]struct{})
			for _, f := range filters {
				tenantIDSet[f.TenantID.String()] = struct{}{}
			}

			// Publish message for each unique tenant
			queuedCount := 0
			failedCount := 0
			for tenantID := range tenantIDSet {
				if err := kgqueue.PublishKGUpdate(tenantID, "cron"); err != nil {
					ctx.GetLogger().Error("cron: failed to publish KG update message", "tenant_id", tenantID, "error", err)
					failedCount++
				} else {
					queuedCount++
				}
			}

			ctx.GetLogger().Info("cron: queued KG update messages",
				"total_tenants", len(tenantIDSet),
				"queued", queuedCount,
				"failed", failedCount)

			c.JSON(200, gin.H{"status": "ok", "queued": queuedCount, "failed": failedCount})
		case "Vertical Rightsizing Refresh":
			// Parse optional payload parameters
			var accountIds []string
			var namespace string
			persistRecommendation := true // Default to true for cron jobs
			batchByNamespace := true      // Default to true
			var maxRecommendations *int

			if hasuraPayload.Payload != nil {
				// Parse account_id filter
				if hasuraPayload.Payload["account_id"] != nil {
					switch v := hasuraPayload.Payload["account_id"].(type) {
					case []any:
						for _, val := range v {
							accountIds = append(accountIds, val.(string))
						}
					case []string:
						accountIds = v
					case string:
						accountIds = []string{v}
					}
				}

				// Parse namespace filter
				if ns, ok := hasuraPayload.Payload["namespace"].(string); ok {
					namespace = ns
				}

				// Parse persist_recommendation flag
				if pr, ok := hasuraPayload.Payload["persist_recommendation"].(bool); ok {
					persistRecommendation = pr
				}

				// Parse batch_by_namespace flag
				if bbn, ok := hasuraPayload.Payload["batch_by_namespace"].(bool); ok {
					batchByNamespace = bbn
				}

				// Parse max_recommendations
				if mr, ok := hasuraPayload.Payload["max_recommendations"].(float64); ok {
					mrInt := int(mr)
					maxRecommendations = &mrInt
				}
			}

			go func() {
				ctx.GetLogger().Info("cron: triggering vertical rightsizing")
				err := triggerVerticalRightsizing(ctx, accountIds, namespace, persistRecommendation, batchByNamespace, maxRecommendations)
				if err != nil {
					ctx.GetLogger().Error("cron: error triggering vertical rightsizing", "error", err)
				}
			}()

			c.JSON(200, gin.H{"status": "ok"})
		case "Volume Rightsizing Refresh":
			var accountIds []string
			var namespace string
			persistRecommendation := true
			var maxRecommendations *int

			if hasuraPayload.Payload != nil {
				if hasuraPayload.Payload["account_id"] != nil {
					switch v := hasuraPayload.Payload["account_id"].(type) {
					case []any:
						for _, val := range v {
							accountIds = append(accountIds, val.(string))
						}
					case []string:
						accountIds = v
					case string:
						accountIds = []string{v}
					}
				}
				if ns, ok := hasuraPayload.Payload["namespace"].(string); ok {
					namespace = ns
				}
				if pr, ok := hasuraPayload.Payload["persist_recommendation"].(bool); ok {
					persistRecommendation = pr
				}
				if mr, ok := hasuraPayload.Payload["max_recommendations"].(float64); ok {
					mrInt := int(mr)
					maxRecommendations = &mrInt
				}
			}

			go func() {
				ctx.GetLogger().Info("cron: triggering volume rightsizing")
				err := triggerVolumeRightsizing(ctx, accountIds, namespace, persistRecommendation, maxRecommendations)
				if err != nil {
					ctx.GetLogger().Error("cron: error triggering volume rightsizing", "error", err)
				}
			}()

			c.JSON(200, gin.H{"status": "ok"})
		case "Threshold Suggestion Refresh":
			go func() {
				defer func() {
					if r := recover(); r != nil {
						ctx.GetLogger().Error("cron: panic in threshold suggestion analysis", "panic", r)
					}
				}()
				ctx.GetLogger().Info("cron: analyzing noisy alerts for threshold suggestions")
				if err := triage.AnalyzeNoisyAlerts(ctx); err != nil {
					ctx.GetLogger().Error("cron: failed to analyze noisy alerts", "error", err)
				}
			}()
			c.JSON(200, gin.H{"status": "ok"})
		case "Snooze Expiry Check":
			go func() {
				defer func() {
					if r := recover(); r != nil {
						ctx.GetLogger().Error("cron: panic in snooze expiry check", "panic", r)
					}
				}()
				ctx.GetLogger().Info("cron: processing expired snoozes")
				if err := triage.ProcessExpiredSnoozes(ctx); err != nil {
					ctx.GetLogger().Error("cron: failed to process expired snoozes", "error", err)
				}
			}()
			c.JSON(200, gin.H{"status": "ok"})
		case "pr-lifecycle-check":
			go func() {
				if err := account.CheckAndFollowupOpenPRs(ctx); err != nil {
					ctx.GetLogger().Error("cron: failed to check PR lifecycle", "error", err)
				}
			}()
			c.JSON(200, gin.H{"status": "ok"})
		default:
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "hasura_cron", "invalid_cron_job")
			c.JSON(400, common.ErrorHasuraActionBadRequest("Invalid cron job"))
			return
		}
	})
}

// triggerVerticalRightsizing iterates over eligible K8s accounts and triggers vertical rightsizing
func triggerVerticalRightsizing(ctx *security.RequestContext, accountIds []string, namespace string, persistRecommendation bool, batchByNamespace bool, maxRecommendations *int) error {
	t0 := time.Now()
	defer func() {
		ctx.GetLogger().Info("triggerVerticalRightsizing completed", "duration", time.Since(t0))
	}()

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return err
	}

	// Get list of accounts to process
	var accounts []struct {
		AccountId string `db:"cloud_account_id"`
		TenantId  string `db:"tenant"`
	}

	if len(accountIds) == 0 {
		// Get all K8s accounts with active agents
		query := `
			SELECT ca.id::varchar as cloud_account_id, ca.tenant::varchar as tenant
			FROM cloud_accounts ca
			INNER JOIN agent a ON ca.id = a.cloud_account_id
			WHERE ca.cloud_provider = 'K8s'
			AND a.status = 'CONNECTED'
			AND a.last_connected_at > now() - interval '1 DAY'
			GROUP BY ca.id, ca.tenant
		`
		err = dbms.Db.Select(&accounts, query)
		if err != nil {
			ctx.GetLogger().Error("vertical rightsizing: error fetching accounts", "error", err)
			return err
		}
	} else {
		// Filter by provided account IDs
		query := `
    SELECT ca.id::varchar as cloud_account_id, ca.tenant::varchar as tenant
    FROM cloud_accounts ca
    INNER JOIN agent a ON ca.id = a.cloud_account_id
    WHERE ca.id = ANY($1::uuid[]) -- Cast the input array, not the column
    AND ca.cloud_provider = 'K8s'
    AND a.status = 'CONNECTED'
    AND a.last_connected_at > now() - interval '1 DAY'
    GROUP BY ca.id, ca.tenant
`
		// Use pq.Array or similar driver helper if your DB driver requires it
		err = dbms.Db.Select(&accounts, query, pq.Array(accountIds))
		if err != nil {
			ctx.GetLogger().Error("vertical rightsizing: error fetching filtered accounts", "error", err)
			return err
		}
	}

	ctx.GetLogger().Info("vertical rightsizing: processing accounts", "count", len(accounts))

	// Process each account
	for _, acc := range accounts {
		// Create a tenant-scoped context for each account so that downstream
		// queries (e.g. GetIntegrationByConfigNameValues) have a valid tenant ID.
		tenantCtx := security.NewRequestContext(
			ctx.GetContext(),
			security.NewSecurityContextForSuperAdminAndTenant(acc.TenantId),
			ctx.GetLogger(),
			ctx.GetTracer(),
			ctx.GetMeter(),
		)

		// Check feature flag for tenant
		if !tenant.IsFeatureEnabledByDefault(tenantCtx, acc.TenantId, tenant.FEATURE_VERTICAL_RIGHTSIZING) {
			tenantCtx.GetLogger().Debug("vertical rightsizing: feature not enabled for tenant", "tenant_id", acc.TenantId)
			continue
		}

		tenantCtx.GetLogger().Info("vertical rightsizing: triggering for account",
			"account_id", acc.AccountId,
			"tenant_id", acc.TenantId,
			"namespace", namespace)

		// Determine metrics provider for this account
		metricsProvider, _, _ := observability.GetLogsMetricsTracesProvider(tenantCtx, acc.AccountId, "", "metrics", "")

		request := ml.VerticalRightsizingRequest{
			AccountId:             acc.AccountId,
			TenantId:              acc.TenantId,
			Namespace:             namespace,
			PersistRecommendation: persistRecommendation,
			BatchByNamespace:      batchByNamespace,
			MaxRecommendations:    maxRecommendations,
			MetricsProvider:       metricsProvider,
		}

		if metricsProvider == "datadog" {
			apiKey, appKey, site, err := integrations.GetDatadogConfigs(tenantCtx, acc.AccountId)
			if err != nil {
				tenantCtx.GetLogger().Error("vertical rightsizing: error getting datadog configs",
					"account_id", acc.AccountId,
					"tenant_id", acc.TenantId,
					"error", err)
				continue
			}
			request.DatadogApiKey = apiKey
			request.DatadogAppKey = appKey
			request.DatadogSite = site
		}

		_, err := ml.TriggerVerticalRightsizing(tenantCtx, request)
		if err != nil {
			tenantCtx.GetLogger().Error("vertical rightsizing: error triggering for account",
				"account_id", acc.AccountId,
				"tenant_id", acc.TenantId,
				"error", err)
		}
	}

	return nil
}

// triggerVolumeRightsizing iterates over eligible K8s accounts and triggers volume rightsizing
func triggerVolumeRightsizing(ctx *security.RequestContext, accountIds []string, namespace string, persistRecommendation bool, maxRecommendations *int) error {
	t0 := time.Now()
	defer func() {
		ctx.GetLogger().Info("triggerVolumeRightsizing completed", "duration", time.Since(t0))
	}()

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return err
	}

	var accounts []struct {
		AccountId string `db:"cloud_account_id"`
		TenantId  string `db:"tenant"`
	}

	if len(accountIds) == 0 {
		query := `
			SELECT ca.id::varchar as cloud_account_id, ca.tenant::varchar as tenant
			FROM cloud_accounts ca
			INNER JOIN agent a ON ca.id = a.cloud_account_id
			WHERE ca.cloud_provider = 'K8s'
			AND a.status = 'CONNECTED'
			AND a.last_connected_at > now() - interval '1 DAY'
			GROUP BY ca.id, ca.tenant
		`
		err = dbms.Db.Select(&accounts, query)
		if err != nil {
			ctx.GetLogger().Error("volume rightsizing: error fetching accounts", "error", err)
			return err
		}
	} else {
		query := `
			SELECT ca.id::varchar as cloud_account_id, ca.tenant::varchar as tenant
			FROM cloud_accounts ca
			INNER JOIN agent a ON ca.id = a.cloud_account_id
			WHERE ca.cloud_provider = 'K8s'
			AND a.status = 'CONNECTED'
			AND a.last_connected_at > now() - interval '1 DAY'
			AND ca.id::varchar = ANY($1)
			GROUP BY ca.id, ca.tenant
		`
		err = dbms.Db.Select(&accounts, query, pq.Array(accountIds))
		if err != nil {
			ctx.GetLogger().Error("volume rightsizing: error fetching filtered accounts", "error", err)
			return err
		}
	}

	ctx.GetLogger().Info("volume rightsizing: processing accounts", "count", len(accounts))

	for _, acc := range accounts {
		// Check feature flag for tenant (default enabled, can be explicitly disabled)
		if !tenant.IsFeatureEnabledByDefault(ctx, acc.TenantId, tenant.FEATURE_VERTICAL_RIGHTSIZING) {
			ctx.GetLogger().Debug("volume rightsizing: feature disabled for tenant", "tenant_id", acc.TenantId)
			continue
		}

		// Create a tenant-scoped context for integration lookups
		tenantCtx := security.NewRequestContextForTenantAdmin(acc.TenantId, ctx.GetLogger(), nil, nil)

		tenantCtx.GetLogger().Info("volume rightsizing: triggering for account",
			"account_id", acc.AccountId,
			"tenant_id", acc.TenantId,
			"namespace", namespace)

		metricsProvider, _, _ := observability.GetLogsMetricsTracesProvider(tenantCtx, acc.AccountId, "", "metrics", "")

		request := ml.VolumeRightsizingRequest{
			AccountId:             acc.AccountId,
			TenantId:              acc.TenantId,
			Namespace:             namespace,
			PersistRecommendation: persistRecommendation,
			MaxRecommendations:    maxRecommendations,
			MetricsProvider:       metricsProvider,
		}

		if metricsProvider == "datadog" {
			apiKey, appKey, site, err := integrations.GetDatadogConfigs(tenantCtx, acc.AccountId)
			if err != nil {
				tenantCtx.GetLogger().Error("volume rightsizing: error getting datadog configs",
					"account_id", acc.AccountId,
					"tenant_id", acc.TenantId,
					"error", err)
				continue
			}
			request.DatadogApiKey = apiKey
			request.DatadogAppKey = appKey
			request.DatadogSite = site
		}

		_, err := ml.TriggerVolumeRightsizing(tenantCtx, request)
		if err != nil {
			tenantCtx.GetLogger().Error("volume rightsizing: error triggering for account",
				"account_id", acc.AccountId,
				"tenant_id", acc.TenantId,
				"error", err)
		}
	}

	return nil
}
