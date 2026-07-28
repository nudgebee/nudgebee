package api

import (
	"fmt"
	"net/http"
	"time"

	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/services/security"

	"github.com/gin-gonic/gin"
)

// Handlers backing the cross-automation execution dashboard. The three actions
// are separate because they refresh on different triggers: the list changes on
// every filter/page change, the aggregate only on filter change, and upcoming
// runs are independent of the filters entirely.

func (s *Server) handleListAccountExecutions(c *gin.Context, sc *security.RequestContext, args map[string]any) {
	filter, err := parseExecutionDashboardFilter(args)
	if err != nil {
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{err}))
		return
	}

	req := model.ListAccountExecutionsRequest{
		ExecutionDashboardFilter: filter,
		// Default on: the "Details" column is meant to explain a failure
		// without a second click. Callers that only need rows can opt out.
		IncludeFailureReason: true,
	}
	if limit, ok := args["limit"].(float64); ok {
		req.Limit = int(limit)
	}
	if page, ok := args["page"].(float64); ok {
		req.Page = int(page)
	}
	if nextPageToken, ok := args["next_page_token"].(string); ok {
		req.NextPageToken = nextPageToken
	}
	if include, ok := args["include_failure_reason"].(bool); ok {
		req.IncludeFailureReason = include
	}

	response, err := s.workflowService.ListAccountExecutions(sc, req)
	if err != nil {
		s.logger.Error("failed to list account executions via RPC", "error", err)
		handleServiceError(c, err, "failed to list executions")
		return
	}

	c.JSON(http.StatusOK, response)
}

func (s *Server) handleAggregateExecutions(c *gin.Context, sc *security.RequestContext, args map[string]any) {
	filter, err := parseExecutionDashboardFilter(args)
	if err != nil {
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{err}))
		return
	}

	req := model.AggregateExecutionsRequest{ExecutionDashboardFilter: filter}
	if topFailedLimit, ok := args["top_failed_limit"].(float64); ok {
		req.TopFailedLimit = int(topFailedLimit)
	}

	response, err := s.workflowService.AggregateExecutions(sc, req)
	if err != nil {
		s.logger.Error("failed to aggregate executions via RPC", "error", err)
		handleServiceError(c, err, "failed to aggregate executions")
		return
	}

	c.JSON(http.StatusOK, response)
}

// parseExecutionDashboardFilter reads the filter set shared by the list and the
// aggregate, so the two can never diverge on how an argument is interpreted.
func parseExecutionDashboardFilter(args map[string]any) (model.ExecutionDashboardFilter, error) {
	// Tenant-level like the Automations listing (#35113): no account means
	// every account this caller can read. parseAccountFilter also accepts the
	// legacy single `account_id`.
	filter := model.ExecutionDashboardFilter{
		AccountIDs:   parseAccountFilter(args),
		WorkflowIDs:  parseStringSlice(args["workflow_ids"]),
		TriggeredBy:  parseStringSlice(args["triggered_by"]),
		TriggerTypes: parseStringSlice(args["trigger_types"]),
	}

	for _, status := range parseStringSlice(args["statuses"]) {
		filter.Statuses = append(filter.Statuses, model.WorkflowExecutionStatus(status))
	}

	if startDate, err := parseOptionalTimestamp(args, "start_date"); err != nil {
		return model.ExecutionDashboardFilter{}, err
	} else if startDate != nil {
		filter.StartDate = startDate
	}
	if endDate, err := parseOptionalTimestamp(args, "end_date"); err != nil {
		return model.ExecutionDashboardFilter{}, err
	} else if endDate != nil {
		filter.EndDate = endDate
	}

	return filter, nil
}

func parseOptionalTimestamp(args map[string]any, key string) (*time.Time, error) {
	raw, ok := args[key].(string)
	if !ok || raw == "" {
		return nil, nil
	}
	parsed, err := parseTimestamp(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid %s format: %v", key, err)
	}
	return &parsed, nil
}

// parseStringSlice accepts a JSON array of strings, tolerating a bare string
// for single-value callers. Non-string entries are dropped rather than failing
// the whole request.
func parseStringSlice(raw any) []string {
	switch value := raw.(type) {
	case string:
		if value == "" {
			return nil
		}
		return []string{value}
	case []any:
		out := make([]string, 0, len(value))
		for _, item := range value {
			if str, ok := item.(string); ok && str != "" {
				out = append(out, str)
			}
		}
		return out
	case []string:
		return value
	default:
		return nil
	}
}
