package api

import (
	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"log/slog"
	"nudgebee/services/common"
	"nudgebee/services/marketplace"
)

func handleMarketplaceEvents(r *gin.Engine, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {

	r.POST("/marketplace/subscribe", func(c *gin.Context) {
		common.MetricsApiRequestsTotal(c.Request.Context(), "marketplace_subscribe")
		var customerPayload marketplace.CustomerSubscription
		err := c.ShouldBindJSON(&customerPayload)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "marketplace_subscribe", "invalid_json")
			c.JSON(400, common.ErrorHasuraActionBadRequest("invalid json - "+err.Error()))
			return
		}
		response, errs := marketplace.AddCustomerSubscription(customerPayload)
		if errs != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "marketplace_subscribe", "add_subscription_failed")
			logger.Error("Unable to add marketplace subscription", "error", errs)
			c.JSON(400, common.ErrorHasuraActionBadRequest("Unable to add marketplace subscription - "+errs.Error()))
		}
		c.JSON(200, response)
	})

	r.POST("/marketplace/create/tenant-user", func(c *gin.Context) {
		common.MetricsApiRequestsTotal(c.Request.Context(), "marketplace_create_tenant_user")
		var request marketplace.NewCustomerTenantRequest
		err := c.ShouldBindJSON(&request)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "marketplace_create_tenant_user", "invalid_json")
			c.JSON(400, common.ErrorHasuraActionBadRequest("invalid json - "+err.Error()))
			return
		}
		hr := HasuraActionRequest{
			SessionVariables: map[string]any{
				"x-hasura-role": "admin",
			},
		}
		ctx, _ := buildContextFromHasuraPayload(c, &hr, tracer, meter, logger)
		response, errs := marketplace.CreateUserAndTenant(ctx, request)
		if errs != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "marketplace_create_tenant_user", "create_user_tenant_failed")
			logger.Error("Unable to add marketplace subscription", "error", errs)
			c.JSON(400, common.ErrorHasuraActionBadRequest("Unable to add marketplace subscription - "+errs.Error()))
		}
		c.JSON(200, response)
	})

	r.POST("/marketplace/aws/webhook", func(c *gin.Context) {
		common.MetricsApiRequestsTotal(c.Request.Context(), "marketplace_aws_webhook")
		var customerPayload marketplace.CustomerSubscription
		err := c.ShouldBindJSON(&customerPayload)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "marketplace_aws_webhook", "invalid_json")
			c.JSON(400, common.ErrorHasuraActionBadRequest("invalid json - "+err.Error()))
			return
		}
		response, errs := marketplace.AddCustomerSubscription(customerPayload)
		if errs != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "marketplace_aws_webhook", "add_subscription_failed")
			logger.Error("Unable to add marketplace subscription", "error", errs)
			c.JSON(400, common.ErrorHasuraActionBadRequest("Unable to add marketplace subscription - "+errs.Error()))
		}
		c.JSON(200, response)
	})

	r.POST("/marketplace/azure/webhook", func(c *gin.Context) {
		common.MetricsApiRequestsTotal(c.Request.Context(), "marketplace_azure_webhook")
		var azurePayload marketplace.AzurePayload
		err := c.ShouldBindJSON(&azurePayload)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "marketplace_azure_webhook", "invalid_json")
			c.JSON(400, common.ErrorHasuraActionBadRequest("invalid json - "+err.Error()))
			return
		}
		response, errs := marketplace.UpdateAzureSubscriptionBasedOnAction(azurePayload)
		if errs != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "marketplace_azure_webhook", "update_subscription_failed")
			logger.Error("Unable to add marketplace subscription", "error", errs)
			c.JSON(400, common.ErrorHasuraActionBadRequest("Unable to add marketplace subscription - "+errs.Error()))
		}
		c.JSON(200, response)
	})

	r.POST("/marketplace/billing/charge-buyers", func(c *gin.Context) {
		common.MetricsApiRequestsTotal(c.Request.Context(), "marketplace_charge_buyers")
		errs := marketplace.SendUsageReportsToMarketplacesForBilling()
		if errs != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "marketplace_charge_buyers", "send_usage_reports_failed")
			logger.Error("Unable to send marketplace usage reports", "error", errs)
			c.JSON(400, common.ErrorHasuraActionBadRequest("Unable  send marketplace usage reports - "+errs.Error()))
		}
		c.JSON(200, gin.H{"message": "Billing generated successfully"})
	})

	r.POST("/marketplace/aws/send-test-usage", func(c *gin.Context) {
		common.MetricsApiRequestsTotal(c.Request.Context(), "marketplace_aws_send_test_usage")
		var request marketplace.TestMeteredUsageRequest
		err := c.ShouldBindJSON(&request)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "marketplace_aws_send_test_usage", "invalid_json")
			c.JSON(400, common.ErrorHasuraActionBadRequest("invalid json - "+err.Error()))
			return
		}
		errs := marketplace.SendTestMeteredUsageToAws(request)
		if errs != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "marketplace_aws_send_test_usage", "send_test_usage_failed")
			logger.Error("Unable to send test metered usage to AWS", "error", errs)
			c.JSON(400, common.ErrorHasuraActionBadRequest("Unable to send test metered usage - "+errs.Error()))
			return
		}
		c.JSON(200, gin.H{"message": "Test metered usage sent successfully"})
	})
}
