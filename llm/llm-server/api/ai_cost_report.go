package api

import (
	"log/slog"
	"net/http"
	"time"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"

	"github.com/gin-gonic/gin"
)

// handleAiCostDailyReportApi registers the hourly cron webhook that
// dispatches the AI-cost digest to every tenant whose configured send hour
// (configuration_store-backed, ai_cost_report_schedule.go; defaults to 06:00
// UTC) matches the current fire.
func handleAiCostDailyReportApi(r *gin.Engine) {
	r.POST("/v1/cost/ai-cost-daily-report", func(c *gin.Context) {
		common.MetricsApiRequestsTotal("ai_cost_daily_report_cron")
		if err := core.RunAiCostReportDispatch(time.Now().UTC()); err != nil {
			slog.Error("ai_cost_daily_report: dispatch run failed", "error", err)
			c.JSON(http.StatusInternalServerError, buildApiResponse(nil, []error{
				common.Error{Message: err.Error()},
			}))
			return
		}
		c.JSON(http.StatusOK, buildApiResponse(map[string]any{"status": "ok"}, nil))
	})
}
