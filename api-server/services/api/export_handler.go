package api

import (
	"log/slog"
	"nudgebee/services/common"
	"nudgebee/services/recommendation"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// handleExportApis configures export-related API routes
func handleExportApis(r *gin.Engine, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	r.POST("/v1/export/recommendations", func(c *gin.Context) {
		handleExportRecommendations(c, tracer, meter, logger)
	})
}

// ExportRecommendationsRequest defines the request structure for exporting recommendations
type ExportRecommendationsRequest struct {
	AccountID    string   `json:"account_id" mapstructure:"account_id"`
	Category     string   `json:"category" mapstructure:"category"`
	RuleName     string   `json:"rule_name" mapstructure:"rule_name"`
	Namespace    *string  `json:"namespace" mapstructure:"namespace"`
	WorkloadType *string  `json:"workload_type" mapstructure:"workload_type"`
	WorkloadName *string  `json:"workload_name" mapstructure:"workload_name"`
	Status       []string `json:"status" mapstructure:"status"`
	Format       string   `json:"format" mapstructure:"format"`
}

// handleExportRecommendations handles the export recommendations HTTP endpoint (RPC action)
func handleExportRecommendations(c *gin.Context, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	var actionPayload ActionRequest

	err := c.ShouldBindJSON(&actionPayload)
	if err != nil {
		c.JSON(400, common.ErrorActionBadRequest(err.Error()))
		return
	}

	// Parse request from RPC input
	mlRequest := actionPayload.Input
	var request ExportRecommendationsRequest

	if mlRequest["request"] == nil {
		err = common.UnmarshalMapToStruct(mlRequest, &request)
	} else {
		// Safe type assertion with comma, ok idiom
		if obj, ok := mlRequest["request"].(map[string]interface{}); ok {
			err = common.UnmarshalMapToStruct(obj, &request)
		} else {
			c.JSON(400, common.ErrorActionBadRequest("Invalid request: 'request' field must be a valid JSON object"))
			return
		}
	}

	if err != nil {
		c.JSON(400, common.ErrorActionBadRequest(err.Error()))
		return
	}

	// Validate format
	if request.Format != "csv" && request.Format != "xlsx" {
		c.JSON(400, common.ErrorActionBadRequest("Format must be csv or xlsx"))
		return
	}

	// Build context from RPC payload (includes user/tenant info)
	ctx, err := buildContextFromPayload(c, &actionPayload, tracer, meter, logger)
	if err != nil {
		c.JSON(400, common.ErrorActionBadRequest(err.Error()))
		return
	}

	// Build filters
	filters := recommendation.ExportFilters{
		AccountID:    request.AccountID,
		Category:     request.Category,
		RuleName:     request.RuleName,
		Namespace:    request.Namespace,
		WorkloadType: request.WorkloadType,
		WorkloadName: request.WorkloadName,
		Status:       request.Status,
	}

	// Generate export
	result, err := recommendation.GenerateRecommendationExport(ctx, filters, request.Format)
	if err != nil {
		logger.Error("Failed to generate export", "error", err, "filters", filters)
		c.JSON(400, common.ErrorActionBadRequest(err.Error()))
		return
	}

	logger.Info("Export generated successfully",
		"format", request.Format,
		"record_count", result.RecordCount,
		"account_id", request.AccountID,
		"rule_name", request.RuleName,
	)

	// Return result in RPC action format
	c.JSON(200, result)
}
