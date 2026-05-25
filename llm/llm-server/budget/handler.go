package budget

import (
	"log/slog"

	"nudgebee/llm/common"
)

// GinContext is an interface for gin.Context to allow mocking in tests
type GinContext interface {
	JSON(code int, obj any)
}

// CheckBudgetAndRespond is a helper function for gin handlers to check budget limits and send response if exceeded.
// Returns true if budget was exceeded (response already sent), false if OK to proceed.
// Usage:
//
//	if budget.CheckBudgetAndRespond(c, tenantId, accountId, budget.ModuleInvestigation, logger) {
//	    return // Response already sent
//	}
func CheckBudgetAndRespond(c GinContext, tenantId, accountId, module string, logger *slog.Logger) bool {
	exceeded, errorMsg := CheckBudgetLimits(tenantId, accountId, module, logger)
	if exceeded {
		c.JSON(429, buildApiResponse(nil, []error{
			common.Error{
				Message: errorMsg,
			},
		}))
		return true
	}
	return false
}

// buildApiResponse constructs the standard API response format
func buildApiResponse(data any, errs []error) map[string]any {
	response := make(map[string]any)

	if data != nil {
		response["data"] = data
	}

	if len(errs) > 0 {
		response["errors"] = errs
	}

	return response
}
