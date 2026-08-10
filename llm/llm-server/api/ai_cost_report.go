package api

import (
	"log/slog"
	"net/http"
	"time"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"

	"github.com/gin-gonic/gin"
)

// handleAiCostDailyReportApi registers the cron webhook that computes and
// publishes the daily AI-cost digest for every tenant (runbook-server's
// cron_triggers.yaml hits this directly, the same way it hits
// /v1/conversation/sync — no request body, X-ACTION-TOKEN gated at the
// gateway like every other route here).
//
// Day boundaries are UTC end-to-end (see GetAiCostAccountReport), not the
// tenant's local timezone — an IST tenant's "daily" window is 05:30-05:30
// IST, and this cron (fires ~06:00 UTC) lands at ~11:30 IST. A reasonable v1
// choice, but worth knowing since account owners will compare these figures
// against their own cloud/billing dashboards, which typically report local time.
func handleAiCostDailyReportApi(r *gin.Engine) {
	r.POST("/v1/cost/ai-cost-daily-report", func(c *gin.Context) {
		common.MetricsApiRequestsTotal("ai_cost_daily_report_cron")
		// Report on the day that just ended, not the partial day in progress —
		// the cron fires early morning UTC, so "today" would undercount.
		referenceDate := time.Now().UTC().AddDate(0, 0, -1)
		if err := core.RunAiCostDailyDigest(referenceDate); err != nil {
			slog.Error("ai_cost_daily_report: digest run failed", "error", err)
			c.JSON(http.StatusInternalServerError, buildApiResponse(nil, []error{
				common.Error{Message: err.Error()},
			}))
			return
		}
		c.JSON(http.StatusOK, buildApiResponse(map[string]any{"status": "ok"}, nil))
	})
}
