package license

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"nudgebee/services/internal/database"

	"github.com/gin-gonic/gin"
)

// BootstrapCheckDecision is the response shape for /v1/license/bootstrap-check.
// `Allowed` is the gate; `TenantID` and `Role` are passed through to the
// frontend's onboarding flow when the caller is permitted to bootstrap.
type BootstrapCheckDecision struct {
	Allowed  bool   `json:"allowed"`
	TenantID string `json:"tenant_id"`
	Role     string `json:"role"`
}

// BootstrapCheckImpl is the active bootstrap-check implementation. The
// default `SingleTenantBootstrap` is suitable for singleton-tenant
// deployments — any email is accepted as the bootstrap admin and joined
// to the first tenant in the metastore. Deployments that need additional
// gating reassign this from init().
var BootstrapCheckImpl = SingleTenantBootstrap

// SingleTenantBootstrap accepts any caller and resolves them to the first
// tenant in the metastore (creating one downstream if none exists). Exported
// so reassigned implementations can delegate to it for the unlicensed /
// singleton-tenant case.
func SingleTenantBootstrap(_ context.Context, _ string) BootstrapCheckDecision {
	var existingTenantID string
	if dbm, dbErr := database.GetDatabaseManager(database.Metastore); dbErr == nil {
		err := dbm.Db.QueryRow("SELECT id::text FROM tenant ORDER BY created_at LIMIT 1").Scan(&existingTenantID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			// Don't fail sign-in — log and fall through to "no tenant"
			// behavior, which is correct for first sign-in anyway.
			slog.Error("bootstrap-check: tenant lookup failed", "error", err)
		}
	}
	return BootstrapCheckDecision{
		Allowed:  true,
		TenantID: existingTenantID,
		Role:     "tenant_admin",
	}
}

// RegisterRoutes wires the /v1/license/me + /v1/license/bootstrap-check
// endpoints. Always available; OSS deployments report tier=oss with no
// features. The frontend reads from these instead of parsing the JWT locally.
func RegisterRoutes(r *gin.Engine) {
	r.GET("/v1/license/me", func(c *gin.Context) {
		lic := Get()
		var expiry int64
		if !lic.Expiry().IsZero() {
			expiry = lic.Expiry().Unix()
		}
		c.JSON(http.StatusOK, gin.H{
			"tier":        string(lic.Tier()),
			"status":      string(lic.Status()),
			"features":    lic.AllFeatures(),
			"expiry":      expiry,
			"tenant_id":   lic.TenantID(),
			"email":       lic.Email(),
			"server_time": time.Now().Unix(),
		})
	})

	// bootstrap-check is consulted by NextAuth's Credentials authorize()
	// callback before creating the first admin user. The decision logic
	// lives in BootstrapCheckImpl so deployments can layer in their own
	// gating (e.g. email allowlists) without OSS source carrying the policy.
	r.GET("/v1/license/bootstrap-check", func(c *gin.Context) {
		email := strings.TrimSpace(c.Query("email"))
		c.JSON(http.StatusOK, BootstrapCheckImpl(c.Request.Context(), email))
	})
}
