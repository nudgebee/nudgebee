package handlers

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/nudgebee/e2e-dashboard/internal/models"
	"github.com/nudgebee/e2e-dashboard/internal/store"
)

// RunsHandler handles test run operations
type RunsHandler struct {
	store *store.Store
}

// NewRunsHandler creates a new runs handler
func NewRunsHandler(s *store.Store) *RunsHandler {
	return &RunsHandler{store: s}
}

// CreateRun creates a new test run
func (h *RunsHandler) CreateRun(c *gin.Context) {
	var req models.CreateRunRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	run := &models.TestRun{
		ID:           uuid.New().String(),
		Environment:  req.Environment,
		Status:       "running",
		StartedAt:    time.Now().UTC(),
		GithubRunURL: req.GithubRunURL,
	}

	if err := h.store.CreateRun(run); err != nil {
		slog.Error("Failed to create run", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create run"})
		return
	}

	slog.Info("Created new test run", "id", run.ID, "environment", run.Environment)
	c.JSON(http.StatusCreated, run)
}

// GetRun retrieves a single test run by ID
func (h *RunsHandler) GetRun(c *gin.Context) {
	id := c.Param("id")

	run, err := h.store.GetRun(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Run not found"})
		return
	}

	// Get results for this run
	results, err := h.store.GetResultsByRunID(id)
	if err != nil {
		slog.Error("Failed to get results", "error", err)
		results = []models.TestResult{}
	}

	response := models.RunWithResults{
		TestRun: *run,
		Results: results,
	}

	c.JSON(http.StatusOK, response)
}

// UpdateRun updates an existing test run
func (h *RunsHandler) UpdateRun(c *gin.Context) {
	id := c.Param("id")

	var req models.UpdateRunRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Set completed_at if status is being set to passed/failed
	if req.Status == "passed" || req.Status == "failed" {
		if req.CompletedAt == "" {
			req.CompletedAt = time.Now().UTC().Format(time.RFC3339)
		}
	}

	if err := h.store.UpdateRun(id, &req); err != nil {
		slog.Error("Failed to update run", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update run"})
		return
	}

	run, err := h.store.GetRun(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Run not found"})
		return
	}

	slog.Info("Updated test run", "id", id, "status", run.Status)
	c.JSON(http.StatusOK, run)
}

// ListRuns retrieves all test runs with optional filters
func (h *RunsHandler) ListRuns(c *gin.Context) {
	environment := c.Query("environment")
	limitStr := c.DefaultQuery("limit", "50")
	offsetStr := c.DefaultQuery("offset", "0")

	limit, _ := strconv.Atoi(limitStr)
	offset, _ := strconv.Atoi(offsetStr)

	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	runs, total, err := h.store.ListRuns(environment, limit, offset)
	if err != nil {
		slog.Error("Failed to list runs", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to list runs"})
		return
	}

	if runs == nil {
		runs = []models.TestRun{}
	}

	c.JSON(http.StatusOK, models.RunsListResponse{
		Runs:  runs,
		Total: total,
	})
}
