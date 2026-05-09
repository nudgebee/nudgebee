package handlers

import (
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/nudgebee/e2e-dashboard/internal/models"
	"github.com/nudgebee/e2e-dashboard/internal/store"
)

// MetricsHandler handles metrics and analytics operations
type MetricsHandler struct {
	store *store.Store
}

// NewMetricsHandler creates a new metrics handler
func NewMetricsHandler(s *store.Store) *MetricsHandler {
	return &MetricsHandler{store: s}
}

// GetTrends retrieves pass rate trends
func (h *MetricsHandler) GetTrends(c *gin.Context) {
	environment := c.Query("environment")
	daysStr := c.DefaultQuery("days", "30")

	days, err := strconv.Atoi(daysStr)
	if err != nil || days <= 0 || days > 90 {
		days = 30
	}

	trends, err := h.store.GetTrends(days, environment)
	if err != nil {
		slog.Error("Failed to get trends", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get trends"})
		return
	}

	if trends == nil {
		trends = []models.TrendDataPoint{}
	}

	c.JSON(http.StatusOK, models.TrendsResponse{
		Data: trends,
	})
}

// GetSummary retrieves dashboard summary stats
func (h *MetricsHandler) GetSummary(c *gin.Context) {
	environment := c.Query("environment")

	summary, err := h.store.GetSummary(environment)
	if err != nil {
		slog.Error("Failed to get summary", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get summary"})
		return
	}

	c.JSON(http.StatusOK, summary)
}

// Health returns a health check response
func (h *MetricsHandler) Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":  "healthy",
		"service": "e2e-dashboard",
	})
}
