package handlers

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"nudgebee/relay-server/pkg/db"
	"nudgebee/relay-server/pkg/utils"
)

// requireTenantOwnsAccount rejects a request whose X-TENANT-ID names a tenant
// that does not own accountID. It writes the error response and returns false
// on rejection.
//
// Callers that send no X-TENANT-ID (internal services acting for the platform,
// which pass only the shared secret) are let through: the check is defence in
// depth behind the app proxy's own access check, not the primary gate.
func requireTenantOwnsAccount(c *gin.Context, store db.AgentStore, logger *slog.Logger, accountID, agentType string) bool {
	tenantID := strings.TrimSpace(c.GetHeader("X-TENANT-ID"))
	if tenantID == "" {
		return true
	}
	// account ids are uuids; anything else would only fail in Postgres as a 500.
	if _, err := uuid.Parse(accountID); err != nil {
		c.JSON(http.StatusBadRequest, utils.BuildError(http.StatusBadRequest, "invalid account_id"))
		return false
	}
	owner, err := store.AccountTenant(c.Request.Context(), accountID, agentType)
	if err != nil {
		logger.Error("failed to look up account tenant", "account_id", accountID, "err", err)
		c.JSON(http.StatusInternalServerError, utils.BuildError(http.StatusInternalServerError, "internal server error"))
		return false
	}
	if !strings.EqualFold(owner, tenantID) {
		logger.Warn("account does not belong to tenant", "account_id", accountID, "tenant_id", tenantID)
		c.JSON(http.StatusForbidden, utils.BuildError(http.StatusForbidden, "account does not belong to tenant"))
		return false
	}
	return true
}
