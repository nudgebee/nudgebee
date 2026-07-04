package api

import (
	"net/http"

	"nudgebee/services/internal/database"
	"nudgebee/services/security"
	"nudgebee/services/triage"

	"github.com/gin-gonic/gin"
)

// inputString safely reads a string field from an action payload.
func inputString(in map[string]interface{}, key string) string {
	if v, ok := in[key].(string); ok {
		return v
	}
	return ""
}

// inputBool safely reads a bool field.
func inputBool(in map[string]interface{}, key string) bool {
	if v, ok := in[key].(bool); ok {
		return v
	}
	return false
}

// inputInt safely reads a numeric value from a dynamic map as an int. JSON decodes numbers as
// float64, but tests / non-JSON callers may pass other numeric types, so accept them all.
func inputInt(in map[string]interface{}, key string) int {
	switch v := in[key].(type) {
	case float64:
		return int(v)
	case float32:
		return int(v)
	case int:
		return v
	case int64:
		return int(v)
	case int32:
		return int(v)
	}
	return 0
}

// handleWorkloadListCriticality returns the full workload inventory for an account with resolved
// criticality (the Service Criticality review screen). Read-only.
func handleWorkloadListCriticality(h *ActionRequest, c *gin.Context, ctx *security.RequestContext) {
	account := inputString(h.Input, "cloud_account_id")
	if account == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cloud_account_id is required"})
		return
	}
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		ctx.GetLogger().Error("criticality list: db manager", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database connection failed"})
		return
	}
	req := triage.ListWorkloadCriticalityRequest{
		CloudAccountID: account,
		Namespace:      inputString(h.Input, "namespace"),
		TieredOnly:     inputBool(h.Input, "tiered_only"),
		Limit:          inputInt(h.Input, "limit"),
		Offset:         inputInt(h.Input, "offset"),
	}
	items, total, err := triage.ListWorkloadCriticality(ctx.GetContext(), dbms.Db, req)
	if err != nil {
		ctx.GetLogger().Error("criticality list: query failed", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list workload criticality: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "total": total})
}

// handleWorkloadUpsertCriticality records an operator's explicit criticality override for a workload.
func handleWorkloadUpsertCriticality(h *ActionRequest, c *gin.Context, ctx *security.RequestContext) {
	account := inputString(h.Input, "cloud_account_id")
	crid := inputString(h.Input, "cloud_resource_id")
	criticality := inputString(h.Input, "criticality")
	namespace := inputString(h.Input, "namespace")
	if account == "" || crid == "" || criticality == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cloud_account_id, cloud_resource_id and criticality are required"})
		return
	}
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		ctx.GetLogger().Error("criticality upsert: db manager", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database connection failed"})
		return
	}
	tenant := ctx.GetSecurityContext().GetTenantId()
	userID := ctx.GetSecurityContext().GetUserId()
	if err := triage.UpsertUserCriticality(ctx.GetContext(), dbms.Db, tenant, account, crid, namespace, criticality, userID); err != nil {
		ctx.GetLogger().Error("criticality upsert: failed", "error", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// handleWorkloadDeleteCriticality removes an operator override so the workload reverts to the derived tier.
func handleWorkloadDeleteCriticality(h *ActionRequest, c *gin.Context, ctx *security.RequestContext) {
	account := inputString(h.Input, "cloud_account_id")
	crid := inputString(h.Input, "cloud_resource_id")
	if account == "" || crid == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cloud_account_id and cloud_resource_id are required"})
		return
	}
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		ctx.GetLogger().Error("criticality reset: db manager", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database connection failed"})
		return
	}
	if err := triage.ResetWorkloadCriticality(ctx.GetContext(), dbms.Db, account, crid); err != nil {
		ctx.GetLogger().Error("criticality reset: failed", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}
