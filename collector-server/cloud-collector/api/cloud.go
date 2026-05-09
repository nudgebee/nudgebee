package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"nudgebee/collector/cloud/account"
	"nudgebee/collector/cloud/common"
	"nudgebee/collector/cloud/config"
	"nudgebee/collector/cloud/providers"
	_ "nudgebee/collector/cloud/providers/aws"
	azureProvider "nudgebee/collector/cloud/providers/azure"
	_ "nudgebee/collector/cloud/providers/cloudfoundry"
	_ "nudgebee/collector/cloud/providers/gcloud"
	"nudgebee/collector/cloud/security"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

type getUsageRequest struct {
	AccountId string     `json:"account_id" validate:"required"`
	Month     time.Month `json:"month" validate:"required"`
	Year      int        `json:"year" validate:"required"`
}

type getRecommendationRequest struct {
	AccountId string                               `json:"account_id" validate:"required"`
	Query     providers.ListRecommendationsRequest `json:"query" validate:"required"`
}

type getMetricsRequest struct {
	AccountId string                        `json:"account_id" validate:"required"`
	Query     providers.QueryMetricsRequest `json:"query" validate:"required"`
}

type listMetricsApiRequest struct {
	AccountId string                       `json:"account_id" validate:"required"`
	Request   providers.ListMetricsRequest `json:"request" validate:"required"`
}

type getLogsRequest struct {
	AccountId string                     `json:"account_id" validate:"required"`
	Query     providers.QueryLogsRequest `json:"query" validate:"required"`
}

type getServiceMapRequest struct {
	AccountId string                           `json:"account_id" validate:"required"`
	Query     providers.QueryServiceMapRequest `json:"query" validate:"required"`
}

type storeMetricsRequest struct {
	AccountId string `json:"account_id" validate:"required"`
	Query     account.StoreMetricesRequest
}

type getResourcesRequest struct {
	AccountId   string            `json:"account_id" validate:"required"`
	ServiceName string            `json:"service_name" validate:"required"`
	ResourceIds []string          `json:"resource_ids"`
	Labels      map[string]string `json:"labels"`
	Regions     []string          `json:"regions"`
}

type getEventsRequest struct {
	AccountId string                     `json:"account_id" validate:"required"`
	Query     providers.ListEventRequest `json:"query" validate:"required"`
}

type executeCommand struct {
	AccountId string `json:"account_id" validate:"required"`
	Command   string `json:"command" validate:"required"`
}

type storeEventRulesRequest struct {
	AccountId string `json:"account_id" validate:"required"`
}

type getPerformanceInsightsRequest struct {
	AccountId            string `json:"account_id" validate:"required"`
	DBInstanceIdentifier string `json:"db_instance_identifier" validate:"required"`
	Region               string `json:"region" validate:"required"`
	StartTime            string `json:"start_time"`
	EndTime              string `json:"end_time"`
}

type getDatabasePerformanceRequest struct {
	AccountId          string `json:"account_id" validate:"required"`
	DatabaseIdentifier string `json:"database_identifier" validate:"required"`
	Region             string `json:"region" validate:"required"`
	StartTime          string `json:"start_time"`
	EndTime            string `json:"end_time"`
	GranularitySeconds int32  `json:"granularity_seconds"`
	IncludeTopQueries  bool   `json:"include_top_queries"`
	IncludeWaitEvents  bool   `json:"include_wait_events"`
	IncludeTopUsers    bool   `json:"include_top_users"`
	IncludeTopHosts    bool   `json:"include_top_hosts"`
	TopN               int    `json:"top_n"`
}

type applyRecommendationRequest struct {
	AccountId        string                 `json:"account_id" validate:"required"`
	RecommendationId string                 `json:"recommendation_id" validate:"required"`
	Data             map[string]interface{} `json:"data" validate:"required"`
	ServiceName      string                 `json:"service_name" validate:"required"`
	ResourceId       string                 `json:"resource_id"`
	RuleName         string                 `json:"rule_name" validate:"required"`
	ResourceRegion   string                 `json:"resource_region"`
}

type applyCommandRequest struct {
	AccountId   string                 `json:"account_id" validate:"required"`
	ServiceName string                 `json:"service_name" validate:"required"`
	Region      string                 `json:"region" validate:"required"`
	ResourceId  string                 `json:"resource_id" validate:"required"`
	Command     string                 `json:"command" validate:"required"`
	Args        map[string]interface{} `json:"args"`
}

type validateCredentialsRequest struct {
	CloudProvider string `json:"cloud_provider" validate:"required"`

	// Azure fields
	TenantID       string `json:"tenant_id,omitempty"`
	ClientID       string `json:"client_id,omitempty"`
	ClientSecret   string `json:"client_secret,omitempty"`
	SubscriptionID string `json:"subscription_id,omitempty"`

	// GCP fields
	CredentialsJSON string `json:"credentials_json,omitempty"`
	ProjectID       string `json:"project_id,omitempty"`

	// GCP billing data fields (optional — validated only when provided)
	BillingProjectID string `json:"billing_project_id,omitempty"`
	BillingDatasetID string `json:"billing_dataset_id,omitempty"`
	BillingTableID   string `json:"billing_table_id,omitempty"`
}

func buildContextFromGin(c *gin.Context, logger *slog.Logger, tracer *trace.Tracer, meter *metric.Meter, account string) (*security.RequestContext, context.CancelFunc, error) {
	return buildContextFromGinWithTimeout(c, logger, tracer, meter, account, time.Duration(config.Config.CloudCollectorRequestTimeoutSeconds)*time.Second)
}

func buildContextFromGinWithTimeout(c *gin.Context, logger *slog.Logger, tracer *trace.Tracer, meter *metric.Meter, account string, timeout time.Duration) (*security.RequestContext, context.CancelFunc, error) {
	userId := c.Request.Header.Get("X-Hasura-User-Id")
	if userId == "" {
		userId = "admin"
	}
	tenantId := c.Request.Header.Get("X-Hasura-User-Tenant-Id")
	if tenantId == "" {
		return nil, nil, errors.New("invalid request, tenantId not found")
	}
	securityCtx, err := security.NewSecurityContext(tenantId, userId)
	if err != nil {
		return nil, nil, err
	}

	logger2 := logger.With("tenantId", tenantId, "userId", userId, "accountId", account)
	ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
	return security.NewRequestContext(ctx, securityCtx, logger2, tracer, meter), cancel, nil
}

func queryMetrics(c *gin.Context, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	request := getMetricsRequest{}
	err := c.ShouldBindJSON(&request)
	if err != nil {
		c.JSON(400, buildApiResponse(nil, err))
		return
	}
	err = common.ValidateStruct(request)
	if err != nil {
		slog.Error("error validating get_metrics", "error", err)
		c.JSON(400, buildApiResponse(nil, err))
		return
	}

	ctx, cancel, err := buildContextFromGin(c, logger, tracer, meter, request.AccountId)
	if err != nil {
		c.JSON(400, buildApiResponse(nil, err))
		return
	}
	defer cancel()
	resp, err := account.QueryMetrics(ctx, request.AccountId, request.Query)
	if err != nil {
		ctx.GetLogger().Error("error getting metrics data", "error", err)
		c.JSON(500, buildApiResponse(nil, err))
		return
	}
	c.JSON(200, buildApiResponse(resp))
}

func listMetrics(c *gin.Context, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	request := listMetricsApiRequest{}
	err := c.ShouldBindJSON(&request)
	if err != nil {
		c.JSON(400, buildApiResponse(nil, err))
		return
	}
	err = common.ValidateStruct(request)
	if err != nil {
		slog.Error("error validating list_metrics", "error", err)
		c.JSON(400, buildApiResponse(nil, err))
		return
	}

	ctx, cancel, err := buildContextFromGin(c, logger, tracer, meter, request.AccountId)
	if err != nil {
		c.JSON(400, buildApiResponse(nil, err))
		return
	}
	defer cancel()
	resp, err := account.ListMetrics(ctx, request.AccountId, request.Request)
	if err != nil {
		ctx.GetLogger().Error("error listing metrics", "error", err)
		c.JSON(500, buildApiResponse(nil, err))
		return
	}
	c.JSON(200, buildApiResponse(resp))
}

func handleCloudProviderApis(r *gin.Engine, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	groupV2 := r.Group("/v1/cloud")

	groupV2.POST("/get_usage", func(c *gin.Context) {
		request := getUsageRequest{}
		err := c.ShouldBindJSON(&request)
		if err != nil {
			c.JSON(400, buildApiResponse(nil, err))
			return
		}

		err = common.ValidateStruct(request)
		if err != nil {
			slog.Error("error validating get_usage", "error", err)
			c.JSON(400, buildApiResponse(nil, err))
			return
		}

		// Parse date
		month, year := request.Month, request.Year
		ctx, cancel, err := buildContextFromGin(c, logger, tracer, meter, request.AccountId)
		if err != nil {
			c.JSON(400, buildApiResponse(nil, err))
			return
		}
		defer cancel()
		resp, err := account.GetUsageData(ctx, request.AccountId, month, year)
		if err != nil {
			ctx.GetLogger().Error("error getting usage data", "error", err, "account_id", request.AccountId, "request", slog.AnyValue(request))
			c.JSON(400, buildApiResponse(nil, err))
			return
		}
		c.JSON(200, buildApiResponse(resp))
	})

	groupV2.POST("/store_usage", func(c *gin.Context) {
		request := getUsageRequest{}
		err := c.ShouldBindJSON(&request)
		if err != nil {
			c.JSON(400, buildApiResponse(nil, err))
			return
		}

		err = common.ValidateStruct(request)
		if err != nil {
			slog.Error("error validating store_usage", "error", err)
			c.JSON(400, buildApiResponse(nil, err))
			return
		}

		// Parse date
		month, year := request.Month, request.Year
		ctx, cancel, err := buildContextFromGin(c, logger, tracer, meter, request.AccountId)
		if err != nil {
			c.JSON(400, buildApiResponse(nil, err))
			return
		}
		defer cancel()
		resp, err := account.StoreUsage(ctx, request.AccountId, month, year)
		if err != nil {
			ctx.GetLogger().Error("error storing usage data", "error", err, "account_id", request.AccountId, "request", slog.AnyValue(request))
			c.JSON(500, buildApiResponse(nil, err))
			return
		}
		c.JSON(200, buildApiResponse(resp))
	})

	groupV2.POST("/get_recommendation", func(c *gin.Context) {
		request := getRecommendationRequest{}
		err := c.ShouldBindJSON(&request)
		if err != nil {
			c.JSON(400, buildApiResponse(nil, err))
			return
		}

		err = common.ValidateStruct(request)
		if err != nil {
			slog.Error("error validating get_recommendation", "error", err)
			c.JSON(400, buildApiResponse(nil, err))
			return
		}

		ctx, cancel, err := buildContextFromGin(c, logger, tracer, meter, request.AccountId)
		if err != nil {
			c.JSON(400, buildApiResponse(nil, err))
			return
		}
		defer cancel()

		resp, err := account.ListRecommendations(ctx, request.AccountId, request.Query)
		if err != nil {
			ctx.GetLogger().Error("error getting recommendation data", "error", err)
			c.JSON(500, buildApiResponse(nil, err))
			return
		}
		c.JSON(200, buildApiResponse(resp))
	})

	groupV2.POST("/store_recommendation", func(c *gin.Context) {
		request := getRecommendationRequest{}
		err := c.ShouldBindJSON(&request)
		if err != nil {
			c.JSON(400, buildApiResponse(nil, err))
			return
		}

		err = common.ValidateStruct(request)
		if err != nil {
			slog.Error("error validating store_recommendation", "error", err)
			c.JSON(400, buildApiResponse(nil, err))
			return
		}

		ctx, cancel, err := buildContextFromGin(c, logger, tracer, meter, request.AccountId)
		if err != nil {
			c.JSON(400, buildApiResponse(nil, err))
			return
		}
		defer cancel()

		resp, err := account.StoreRecommendations(ctx, request.AccountId, request.Query)
		if err != nil {
			ctx.GetLogger().Error("error storing recommendation data", "error", err)
			c.JSON(500, buildApiResponse(nil, err))
			return
		}
		c.JSON(200, buildApiResponse(resp))
	})

	groupV2.POST("/get_metrics", func(c *gin.Context) {
		queryMetrics(c, tracer, meter, logger)
	})
	groupV2.POST("/query_metrics", func(c *gin.Context) {
		queryMetrics(c, tracer, meter, logger)
	})
	groupV2.POST("/list_metrics", func(c *gin.Context) {
		listMetrics(c, tracer, meter, logger)
	})

	groupV2.POST("/query_logs", func(c *gin.Context) {
		request := getLogsRequest{}
		err := c.ShouldBindJSON(&request)
		if err != nil {
			c.JSON(400, buildApiResponse(nil, err))
			return
		}
		err = common.ValidateStruct(request)
		if err != nil {
			slog.Error("error validating get_metrics", "error", err)
			c.JSON(400, buildApiResponse(nil, err))
			return
		}

		ctx, cancel, err := buildContextFromGin(c, logger, tracer, meter, request.AccountId)
		if err != nil {
			c.JSON(400, buildApiResponse(nil, err))
			return
		}
		defer cancel()
		resp, err := account.QueryLogs(ctx, request.AccountId, request.Query)
		if err != nil {
			ctx.GetLogger().Error("error querying logs", "error", err)
			c.JSON(500, buildApiResponse(nil, err))
			return
		}
		c.JSON(200, buildApiResponse(resp))
	})

	groupV2.POST("/query_service_map", func(c *gin.Context) {
		request := getServiceMapRequest{}
		err := c.ShouldBindJSON(&request)
		if err != nil {
			c.JSON(400, buildApiResponse(nil, err))
			return
		}
		err = common.ValidateStruct(request)
		if err != nil {
			slog.Error("error validating get_metrics", "error", err)
			c.JSON(400, buildApiResponse(nil, err))
			return
		}

		ctx, cancel, err := buildContextFromGin(c, logger, tracer, meter, request.AccountId)
		if err != nil {
			c.JSON(400, buildApiResponse(nil, err))
			return
		}
		defer cancel()
		resp, err := account.QueryServiceMap(ctx, request.AccountId, request.Query)
		if err != nil {
			ctx.GetLogger().Error("error getting logs data", "error", err)
			c.JSON(500, buildApiResponse(nil, err))
			return
		}
		c.JSON(200, buildApiResponse(resp))
	})

	groupV2.POST("/store_metrics", func(c *gin.Context) {
		request := storeMetricsRequest{}
		err := c.ShouldBindJSON(&request)
		if err != nil {
			c.JSON(400, buildApiResponse(nil, err))
			return
		}
		err = common.ValidateStruct(request)
		if err != nil {
			slog.Error("error validating store_metrics", "error", err)
			c.JSON(400, buildApiResponse(nil, err))
			return
		}

		// Use longer timeout for metrics queries (GCP Monitoring API can be slow)
		ctx, cancel, err := buildContextFromGinWithTimeout(c, logger, tracer, meter, request.AccountId, 5*time.Minute)
		if err != nil {
			c.JSON(400, buildApiResponse(nil, err))
			return
		}
		defer cancel()
		resp, err := account.StoreMetrices(ctx, request.AccountId, request.Query)
		if err != nil {
			ctx.GetLogger().Error("error storing metrics data", "error", err)
			c.JSON(500, buildApiResponse(nil, err))
			return
		}
		c.JSON(200, buildApiResponse(resp))
	})

	groupV2.POST("/get_resources", func(c *gin.Context) {
		request := getResourcesRequest{}
		err := c.ShouldBindJSON(&request)
		if err != nil {
			c.JSON(400, buildApiResponse(nil, err))
			return
		}
		err = common.ValidateStruct(request)
		if err != nil {
			slog.Error("error validating get_resources", "error", err)
			c.JSON(400, buildApiResponse(nil, err))
			return
		}

		ctx, cancel, err := buildContextFromGin(c, logger, tracer, meter, request.AccountId)
		if err != nil {
			c.JSON(400, buildApiResponse(nil, err))
			return
		}
		defer cancel()
		resp, err := account.ListResources(ctx, request.AccountId, providers.ListResourceRequest{
			ResourceIds: request.ResourceIds,
			Labels:      request.Labels,
			ServiceName: request.ServiceName,
			Regions:     request.Regions,
		})
		if err != nil {
			ctx.GetLogger().Error("error getting resources data", "error", err)
			c.JSON(500, buildApiResponse(nil, err))
			return
		}
		c.JSON(200, buildApiResponse(resp))
	})

	groupV2.POST("/store_resources", func(c *gin.Context) {
		request := getResourcesRequest{}
		err := c.ShouldBindJSON(&request)
		if err != nil {
			c.JSON(400, buildApiResponse(nil, err))
			return
		}
		err = common.ValidateStruct(request)
		if err != nil {
			slog.Error("error validating store_resources", "error", err)
			c.JSON(400, buildApiResponse(nil, err))
			return
		}

		ctx, cancel, err := buildContextFromGin(c, logger, tracer, meter, request.AccountId)
		if err != nil {
			c.JSON(400, buildApiResponse(nil, err))
			return
		}
		defer cancel()
		resp, err := account.StoreResources(ctx, request.AccountId, request.ServiceName, request.Regions...)
		if err != nil {
			ctx.GetLogger().Error("error storing resources data", "error", err)
			c.JSON(500, buildApiResponse(nil, err))
			return
		}
		c.JSON(200, buildApiResponse(resp))
	})

	groupV2.POST("/get_events", func(c *gin.Context) {
		request := getEventsRequest{}
		err := c.ShouldBindJSON(&request)
		if err != nil {
			c.JSON(400, buildApiResponse(nil, err))
			return
		}
		err = common.ValidateStruct(request)
		if err != nil {
			slog.Error("error validating get_events", "error", err)
			c.JSON(400, buildApiResponse(nil, err))
			return
		}

		ctx, cancel, err := buildContextFromGin(c, logger, tracer, meter, request.AccountId)
		if err != nil {
			c.JSON(400, buildApiResponse(nil, err))
			return
		}
		defer cancel()
		resp, err := account.ListEvents(ctx, request.AccountId, request.Query)
		if err != nil {
			ctx.GetLogger().Error("error getting events data", "error", err)
			c.JSON(500, buildApiResponse(nil, err))
			return
		}
		c.JSON(200, buildApiResponse(resp))
	})

	groupV2.POST("/store_events", func(c *gin.Context) {
		request := getEventsRequest{}
		err := c.ShouldBindJSON(&request)
		if err != nil {
			c.JSON(400, buildApiResponse(nil, err))
			return
		}
		err = common.ValidateStruct(request)
		if err != nil {
			slog.Error("error validating store_events", "error", err)
			c.JSON(400, buildApiResponse(nil, err))
			return
		}

		tenantId := c.Request.Header.Get("X-Hasura-User-Tenant-Id")
		if tenantId == "" {
			c.JSON(400, buildApiResponse(nil, errors.New("invalid request, tenantId not found")))
			return
		}

		// Publish job to RabbitMQ for async processing (same pattern as StoreEventsForAllAccounts)
		job := account.CloudAccountEventsJob{
			JobId:     uuid.New().String(),
			AccountId: request.AccountId,
			TenantId:  tenantId,
		}
		err = common.MqPublish(config.Config.RabbitMqCloudAccountEventsExchange, config.Config.RabbitMqCloudAccountEventsQueue, job)
		if err != nil {
			logger.Error("store_events: failed to publish job", "error", err, "accountId", request.AccountId, "job_id", job.JobId)
			c.JSON(500, buildApiResponse(nil, fmt.Errorf("failed to enqueue events job: %w", err)))
			return
		}

		logger.Info("store_events: published job to queue", "accountId", request.AccountId, "job_id", job.JobId, "tenantId", tenantId)
		c.JSON(200, buildApiResponse(map[string]string{
			"status":  "enqueued",
			"job_id":  job.JobId,
			"message": "Events sync job enqueued successfully. Processing will complete asynchronously.",
		}))
	})

	groupV2.POST("/store_event_rules", func(c *gin.Context) {
		request := storeEventRulesRequest{}
		err := c.ShouldBindJSON(&request)
		if err != nil {
			c.JSON(400, buildApiResponse(nil, err))
			return
		}
		err = common.ValidateStruct(request)
		if err != nil {
			slog.Error("error validating store_event_rules", "error", err)
			c.JSON(400, buildApiResponse(nil, err))
			return
		}

		ctx, cancel, err := buildContextFromGin(c, logger, tracer, meter, request.AccountId)
		if err != nil {
			c.JSON(400, buildApiResponse(nil, err))
			return
		}
		defer cancel()
		resp, err := account.StoreEventRules(ctx, request.AccountId)
		if err != nil {
			ctx.GetLogger().Error("error getting events data", "error", err)
			c.JSON(500, buildApiResponse(nil, err))
			return
		}
		c.JSON(200, buildApiResponse(resp))
	})

	groupV2.POST("/execute_cli", func(c *gin.Context) {
		request := executeCommand{}
		err := c.ShouldBindJSON(&request)
		if err != nil {
			c.JSON(400, buildApiResponse(nil, err))
			return
		}
		err = common.ValidateStruct(request)
		if err != nil {
			slog.Error("error validating execute_cli", "error", err)
			c.JSON(400, buildApiResponse(nil, err))
			return
		}

		ctx, cancel, err := buildContextFromGin(c, logger, tracer, meter, request.AccountId)
		if err != nil {
			c.JSON(400, buildApiResponse(nil, err))
			return
		}
		if cancel != nil {
			defer cancel()
		}
		resp, err := account.ExecuteCliCommand(ctx, request.AccountId, request.Command)
		if err != nil {
			c.JSON(500, buildApiResponse(nil, err))
			return
		}
		c.JSON(200, buildApiResponse(resp))
	})

	groupV2.POST("/performance_insights", func(c *gin.Context) {
		request := getPerformanceInsightsRequest{}
		err := c.ShouldBindJSON(&request)
		if err != nil {
			c.JSON(400, buildApiResponse(nil, err))
			return
		}
		err = common.ValidateStruct(request)
		if err != nil {
			slog.Error("error validating performance_insights", "error", err)
			c.JSON(400, buildApiResponse(nil, err))
			return
		}

		ctx, cancel, err := buildContextFromGin(c, logger, tracer, meter, request.AccountId)
		if err != nil {
			c.JSON(400, buildApiResponse(nil, err))
			return
		}
		defer cancel()

		resp, err := account.QueryPerformanceInsights(ctx, request.AccountId, request.DBInstanceIdentifier, request.Region, request.StartTime, request.EndTime)
		if err != nil {
			ctx.GetLogger().Error("error getting performance insights data", "error", err)
			c.JSON(500, buildApiResponse(nil, err))
			return
		}
		c.JSON(200, buildApiResponse(resp))
	})

	groupV2.POST("/database_performance", func(c *gin.Context) {
		request := getDatabasePerformanceRequest{}
		err := c.ShouldBindJSON(&request)
		if err != nil {
			c.JSON(400, buildApiResponse(nil, err))
			return
		}
		err = common.ValidateStruct(request)
		if err != nil {
			slog.Error("error validating database_performance", "error", err)
			c.JSON(400, buildApiResponse(nil, err))
			return
		}

		ctx, cancel, err := buildContextFromGin(c, logger, tracer, meter, request.AccountId)
		if err != nil {
			c.JSON(400, buildApiResponse(nil, err))
			return
		}
		defer cancel()

		// Parse time strings if provided
		var startTime, endTime *time.Time
		if request.StartTime != "" {
			parsed, err := time.Parse(time.RFC3339, request.StartTime)
			if err != nil {
				ctx.GetLogger().Error("invalid start_time format", "error", err)
				c.JSON(400, buildApiResponse(nil, fmt.Errorf("invalid start_time format: %w", err)))
				return
			}
			startTime = &parsed
		}
		if request.EndTime != "" {
			parsed, err := time.Parse(time.RFC3339, request.EndTime)
			if err != nil {
				ctx.GetLogger().Error("invalid end_time format", "error", err)
				c.JSON(400, buildApiResponse(nil, fmt.Errorf("invalid end_time format: %w", err)))
				return
			}
			endTime = &parsed
		}

		// Build the generic request
		perfRequest := providers.DatabasePerformanceRequest{
			DatabaseIdentifier: request.DatabaseIdentifier,
			Region:             request.Region,
			StartTime:          startTime,
			EndTime:            endTime,
			GranularitySeconds: request.GranularitySeconds,
			IncludeTopQueries:  request.IncludeTopQueries,
			IncludeWaitEvents:  request.IncludeWaitEvents,
			IncludeTopUsers:    request.IncludeTopUsers,
			IncludeTopHosts:    request.IncludeTopHosts,
			TopN:               request.TopN,
		}

		// Call the account layer
		resp, err := account.QueryDatabasePerformance(ctx, request.AccountId, perfRequest)
		if err != nil {
			ctx.GetLogger().Error("error getting database performance data", "error", err)
			c.JSON(500, buildApiResponse(nil, err))
			return
		}

		c.JSON(200, buildApiResponse(resp))
	})

	groupV2.POST("/apply_recommendation", func(c *gin.Context) {
		request := applyRecommendationRequest{}
		err := c.ShouldBindJSON(&request)
		if err != nil {
			c.JSON(400, buildApiResponse(nil, err))
			return
		}
		err = common.ValidateStruct(request)
		if err != nil {
			slog.Error("error validating apply_recommendation", "error", err)
			c.JSON(400, buildApiResponse(nil, err))
			return
		}

		ctx, cancel, err := buildContextFromGin(c, logger, tracer, meter, request.AccountId)
		if err != nil {
			c.JSON(400, buildApiResponse(nil, err))
			return
		}
		if cancel != nil {
			defer cancel()
		}
		resp, err := account.ApplyRecommendation(ctx, request.AccountId, providers.Recommendation{
			RuleName:            request.RuleName,
			ResourceServiceName: request.ServiceName,
			ResourceId:          request.ResourceId,
			ResourceRegion:      request.ResourceRegion,
			Data:                request.Data,
		})
		if err != nil {
			ctx.GetLogger().Error("error applying recommendation", "error", err)
			c.JSON(500, buildApiResponse(nil, err))
			return
		}
		c.JSON(200, buildApiResponse(resp))
	})

	groupV2.POST("/apply_command", func(c *gin.Context) {
		request := applyCommandRequest{}
		err := c.ShouldBindJSON(&request)
		if err != nil {
			c.JSON(400, buildApiResponse(nil, err))
			return
		}
		err = common.ValidateStruct(request)
		if err != nil {
			slog.Error("error validating apply_command", "error", err)
			c.JSON(400, buildApiResponse(nil, err))
			return
		}

		ctx, cancel, err := buildContextFromGin(c, logger, tracer, meter, request.AccountId)
		if err != nil {
			c.JSON(400, buildApiResponse(nil, err))
			return
		}
		if cancel != nil {
			defer cancel()
		}

		// Execute command via account package
		resp, err := account.ApplyCommand(ctx, request.AccountId, providers.ApplyCommandRequest{
			ServiceName: request.ServiceName,
			Region:      request.Region,
			ResourceId:  request.ResourceId,
			Command:     request.Command,
			Args:        request.Args,
		})
		if err != nil {
			ctx.GetLogger().Error("error executing command", "error", err)
			c.JSON(500, buildApiResponse(nil, err))
			return
		}

		c.JSON(200, buildApiResponse(map[string]interface{}{
			"success": resp.Success,
			"message": resp.Message,
		}))
	})

	groupV2.GET("/permission_errors", func(c *gin.Context) {
		tenantId := c.Request.Header.Get("X-Hasura-User-Tenant-Id")
		cloudAccountId := c.Query("cloud_account_id")
		serviceName := c.Query("service_name")
		cloudProvider := c.Query("cloud_provider")

		db, err := common.GetDatabaseManager(common.Metastore)
		if err != nil {
			slog.Error("error getting database manager for permission errors", "error", err)
			c.JSON(500, buildApiResponse(nil, errors.New("internal error")))
			return
		}

		query := `SELECT id, tenant_id, cloud_account_id, account_number, cloud_provider,
			service_name, api_operation, wrapper_method, error_code, error_message, region,
			first_seen_at, last_seen_at, occurrence_count, is_resolved, resolved_at
			FROM cloud_api_permission_errors
			WHERE ($1 = '' OR tenant_id::text = $1)
			AND ($2 = '' OR cloud_account_id::text = $2)
			AND ($3 = '' OR service_name = $3)
			AND ($4 = '' OR cloud_provider = $4)
			ORDER BY last_seen_at DESC
			LIMIT 500`

		rows, err := db.Query(query, tenantId, cloudAccountId, serviceName, cloudProvider)
		if err != nil {
			slog.Error("error querying permission errors", "error", err)
			c.JSON(500, buildApiResponse(nil, fmt.Errorf("failed to query permission errors: %w", err)))
			return
		}
		defer func() { _ = rows.Close() }()

		var results []map[string]any
		for rows.Next() {
			row := make(map[string]any)
			if err := rows.MapScan(row); err != nil {
				slog.Error("error scanning permission error row", "error", err)
				continue
			}
			results = append(results, row)
		}
		if results == nil {
			results = []map[string]any{}
		}

		c.JSON(200, buildApiResponse(results))
	})

	// GCP project discovery endpoint
	groupV2.POST("/gcp_list_projects", func(c *gin.Context) {
		var request struct {
			CredentialsJSON string `json:"credentials_json" validate:"required"`
		}
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(400, buildApiResponse(nil, err))
			return
		}
		if err := common.ValidateStruct(request); err != nil {
			c.JSON(400, buildApiResponse(nil, err))
			return
		}

		projects, err := common.ListGCPProjects(c.Request.Context(), request.CredentialsJSON)
		if err != nil {
			c.JSON(500, buildApiResponse(nil, err))
			return
		}

		c.JSON(200, buildApiResponse(map[string]any{"projects": projects}))
	})

	// GCP monitoring permission check endpoint
	groupV2.POST("/check_gcp_monitoring_permission", func(c *gin.Context) {
		var request struct {
			AccountId string `json:"account_id" validate:"required"`
		}
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(400, buildApiResponse(nil, err))
			return
		}
		if err := common.ValidateStruct(request); err != nil {
			c.JSON(400, buildApiResponse(nil, err))
			return
		}

		ctx, cancel, err := buildContextFromGin(c, logger, tracer, meter, request.AccountId)
		if err != nil {
			c.JSON(400, buildApiResponse(nil, err))
			return
		}
		defer cancel()

		hasPermission, errorDetail, err := account.CheckGCPMonitoringPermission(ctx, request.AccountId)
		if err != nil {
			ctx.GetLogger().Error("error checking GCP monitoring permission", "error", err)
			c.JSON(500, buildApiResponse(nil, err))
			return
		}
		c.JSON(200, buildApiResponse(map[string]any{
			"has_permission": hasPermission,
			"error_detail":   errorDetail,
		}))
	})

	// GCP monitoring webhook setup endpoint
	groupV2.POST("/setup_gcp_monitoring_webhook", func(c *gin.Context) {
		var request struct {
			AccountId  string `json:"account_id" validate:"required"`
			WebhookUrl string `json:"webhook_url" validate:"required,url"`
		}
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(400, buildApiResponse(nil, err))
			return
		}
		if err := common.ValidateStruct(request); err != nil {
			c.JSON(400, buildApiResponse(nil, err))
			return
		}
		if !strings.HasPrefix(request.WebhookUrl, "https://") {
			c.JSON(400, buildApiResponse(nil, fmt.Errorf("webhook_url must use HTTPS")))
			return
		}

		ctx, cancel, err := buildContextFromGin(c, logger, tracer, meter, request.AccountId)
		if err != nil {
			c.JSON(400, buildApiResponse(nil, err))
			return
		}
		defer cancel()

		channelName, err := account.SetupGCPMonitoringWebhook(ctx, request.AccountId, request.WebhookUrl)
		if err != nil {
			ctx.GetLogger().Error("error setting up GCP monitoring webhook", "error", err)
			c.JSON(500, buildApiResponse(nil, err))
			return
		}
		c.JSON(200, buildApiResponse(map[string]string{"channel_name": channelName}))
	})

	// Azure Event Grid webhook relay endpoint — receives events forwarded by api-server
	var (
		eventGridProcessorOnce sync.Once
		eventGridProcessor     azureProvider.EventGridEventProcessor
		eventGridHandler       providers.ProcessedEventHandler
		eventGridInitErr       error
	)
	groupV2.POST("/process_azure_eventgrid_events", func(c *gin.Context) {
		eventGridProcessorOnce.Do(func() {
			eventGridProcessor, eventGridInitErr = azureProvider.NewEventGridProcessor(config.Config.CloudCollectorAzureEventRulesPath)
			if eventGridInitErr != nil {
				logger.Error("Failed to initialize Azure Event Grid processor", "error", eventGridInitErr)
				return
			}
			eventGridHandler = account.NewAsyncEventHandler()
			logger.Info("Azure Event Grid webhook processor initialized")
		})
		if eventGridInitErr != nil {
			c.JSON(500, buildApiResponse(nil, fmt.Errorf("event grid processor not initialized: %w", eventGridInitErr)))
			return
		}

		token := c.Query("token")
		if token == "" {
			c.JSON(400, buildApiResponse(nil, errors.New("missing token query parameter")))
			return
		}

		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			c.JSON(400, buildApiResponse(nil, fmt.Errorf("failed to read request body: %w", err)))
			return
		}

		cloudCtx := providers.NewCloudProviderContext(c.Request.Context())

		processedEvent, originatingAccount, err := azureProvider.ProcessEventGridEventFromBytes(cloudCtx, body, token, eventGridProcessor, eventGridHandler)
		if err != nil {
			logger.Error("Failed to process Event Grid event", "error", err)
			c.JSON(400, buildApiResponse(nil, err))
			return
		}

		if processedEvent.EventId == "" {
			logger.Info("Event Grid event skipped by processor (no matching rule)")
			c.JSON(200, buildApiResponse(map[string]string{"status": "skipped"}))
			return
		}

		if err := eventGridHandler.ProcessEvent(cloudCtx, processedEvent, originatingAccount); err != nil {
			logger.Error("Failed to handle processed Event Grid event", "error", err, "eventId", processedEvent.EventId)
			c.JSON(500, buildApiResponse(nil, err))
			return
		}

		c.JSON(200, buildApiResponse(map[string]string{"status": "processed", "eventId": processedEvent.EventId}))
	})

	// Cloud credential validation endpoint
	groupV2.POST("/validate_credentials", func(c *gin.Context) {
		request := validateCredentialsRequest{}
		err := c.ShouldBindJSON(&request)
		if err != nil {
			c.JSON(400, buildApiResponse(nil, err))
			return
		}
		err = common.ValidateStruct(request)
		if err != nil {
			slog.Error("error validating validate_credentials", "error", err)
			c.JSON(400, buildApiResponse(nil, err))
			return
		}

		var result common.ValidationResult

		switch request.CloudProvider {
		case "Azure":
			creds := common.AzureCredentials{
				TenantID:       request.TenantID,
				ClientID:       request.ClientID,
				ClientSecret:   request.ClientSecret,
				SubscriptionID: request.SubscriptionID,
			}
			result = common.ValidateAzureCredentials(c.Request.Context(), creds)
		case "GCP":
			creds := common.GCPCredentials{
				CredentialsJSON:  request.CredentialsJSON,
				ProjectID:        request.ProjectID,
				BillingProjectID: request.BillingProjectID,
				BillingDatasetID: request.BillingDatasetID,
				BillingTableID:   request.BillingTableID,
			}
			result = common.ValidateGCPCredentials(c.Request.Context(), creds)
		default:
			c.JSON(400, buildApiResponse(nil, fmt.Errorf("unsupported cloud provider: %s", request.CloudProvider)))
			return
		}

		c.JSON(200, buildApiResponse(result))
	})

	groupV2.POST("/aws_cf_stack_info", func(c *gin.Context) {
		var request struct {
			AccountId string `json:"account_id" validate:"required"`
		}
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(400, buildApiResponse(nil, err))
			return
		}
		if request.AccountId == "" {
			c.JSON(400, buildApiResponse(nil, fmt.Errorf("account_id is required")))
			return
		}
		ctx, cancel, err := buildContextFromGin(c, logger, tracer, meter, request.AccountId)
		if err != nil {
			c.JSON(400, buildApiResponse(nil, err))
			return
		}
		defer cancel()
		resp, err := account.GetAwsStackInfo(ctx, request.AccountId)
		if err != nil {
			c.JSON(500, buildApiResponse(nil, err))
			return
		}
		c.JSON(200, resp)
	})

	// Azure subscription discovery endpoint
	groupV2.POST("/azure_list_subscriptions", func(c *gin.Context) {
		var request struct {
			TenantID     string `json:"tenant_id" validate:"required"`
			ClientID     string `json:"client_id" validate:"required"`
			ClientSecret string `json:"client_secret" validate:"required"`
		}
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(400, buildApiResponse(nil, err))
			return
		}
		if err := common.ValidateStruct(request); err != nil {
			c.JSON(400, buildApiResponse(nil, err))
			return
		}

		subscriptions, err := common.ListAzureSubscriptions(c.Request.Context(), request.TenantID, request.ClientID, request.ClientSecret)
		if err != nil {
			c.JSON(500, buildApiResponse(nil, err))
			return
		}

		c.JSON(200, buildApiResponse(map[string]any{"subscriptions": subscriptions}))
	})

}
