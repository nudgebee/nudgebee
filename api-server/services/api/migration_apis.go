package api

import (
	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"log/slog"
	"nudgebee/services/billing"
	"nudgebee/services/common"
)

func handleMigrationApis(r *gin.Engine, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	groupV2 := r.Group("/v1/migration")
	groupV2.POST("/billing/charge-tenants", func(c *gin.Context) {
		common.MetricsApiRequestsTotal(c.Request.Context(), "migration_charge_tenants")
		var payload billing.GenerateChargePayload
		err := c.ShouldBindJSON(&payload)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "migration_charge_tenants", "invalid_json")
			logger.Error("error binding request", "error", err)
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		err = billing.GenerateBillingDataForTenantForGivenDuration(payload)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "migration_charge_tenants", "generate_billing_failed")
			logger.Error("authz: error creating access", "error", err)
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, gin.H{"message": "success"})
	})
}
