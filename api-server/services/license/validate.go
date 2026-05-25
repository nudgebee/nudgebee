package license

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// CheckLimits exposes the /rpc/license response shape consumed by
// the frontend License page. Internally it delegates to the active License
// impl (OSS or EE JWT). The OSS impl reports tier=oss with no limits, which
// renders as "License not found" — preserving today's UX for OSS deploys.
func CheckLimits(c *gin.Context, _ *trace.Tracer, _ *metric.Meter, logger *slog.Logger) {
	lic := Get()
	maxAccounts := lic.MaxAccounts()
	maxNodes := lic.MaxNodesPerCluster()

	switch lic.Status() {
	case StatusMissing:
		logger.Warn("License not found")
		c.JSON(http.StatusOK, gin.H{"license": gin.H{
			"max_accounts": maxAccounts, "max_nodes_per_cluster": maxNodes,
			"message": "License not found", "status": "not_found",
		}})
	case StatusExpired:
		logger.Warn("License expired")
		c.JSON(http.StatusOK, gin.H{"license": gin.H{
			"type": string(lic.Tier()), "max_accounts": maxAccounts, "max_nodes_per_cluster": maxNodes,
			"message": "Your license is expired on " + lic.Expiry().Format("2006-01-02"),
			"status":  "expired",
		}})
	case StatusGrace, StatusActive:
		msg := "Your license is valid till " + lic.Expiry().Format("2006-01-02")
		if lic.Status() == StatusGrace {
			msg = "Your license has expired and is in grace period until restart"
		}
		c.JSON(http.StatusOK, gin.H{"license": gin.H{
			"type": string(lic.Tier()), "max_accounts": maxAccounts, "max_nodes_per_cluster": maxNodes,
			"message": msg, "status": "valid",
		}})
	default:
		// Defensive default — if Status() ever returns an unknown value,
		// respond with the same shape as StatusMissing rather than 200/empty.
		logger.Warn("license: unknown status", "status", string(lic.Status()))
		c.JSON(http.StatusOK, gin.H{"license": gin.H{
			"max_accounts": maxAccounts, "max_nodes_per_cluster": maxNodes,
			"message": "License status unknown", "status": "unknown",
		}})
	}
}

// GetMaxAccounts returns the licensed cluster cap, or -1 when unlimited
// (OSS / no license). Returns 0 when the license is expired (legacy callers
// in account/service.go gate enrollment on this).
func GetMaxAccounts() int {
	lic := Get()
	if lic.Status() == StatusExpired {
		return 0
	}
	return lic.MaxAccounts()
}
