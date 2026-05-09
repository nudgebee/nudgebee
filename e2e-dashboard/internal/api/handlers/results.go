package handlers

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/nudgebee/e2e-dashboard/internal/models"
	"github.com/nudgebee/e2e-dashboard/internal/store"
)

// ResultsHandler handles test result operations
type ResultsHandler struct {
	store *store.Store
}

// NewResultsHandler creates a new results handler
func NewResultsHandler(s *store.Store) *ResultsHandler {
	return &ResultsHandler{store: s}
}

// CreateResults batch inserts test results for a run
func (h *ResultsHandler) CreateResults(c *gin.Context) {
	runID := c.Param("id")

	var req models.BatchResultsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if len(req.Results) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No results provided"})
		return
	}

	// Verify run exists
	_, err := h.store.GetRun(runID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Run not found"})
		return
	}

	if err := h.store.CreateResults(runID, req.Results); err != nil {
		slog.Error("Failed to create results", "error", err, "run_id", runID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create results"})
		return
	}

	slog.Info("Created test results", "run_id", runID, "count", len(req.Results))
	c.JSON(http.StatusCreated, gin.H{
		"message": "Results created successfully",
		"count":   len(req.Results),
	})
}

// GetResults retrieves all results for a run
func (h *ResultsHandler) GetResults(c *gin.Context) {
	runID := c.Param("id")

	results, err := h.store.GetResultsByRunID(runID)
	if err != nil {
		slog.Error("Failed to get results", "error", err, "run_id", runID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get results"})
		return
	}

	if results == nil {
		results = []models.TestResult{}
	}

	c.JSON(http.StatusOK, gin.H{
		"results": results,
		"count":   len(results),
	})
}
