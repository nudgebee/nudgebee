package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/nudgebee/e2e-dashboard/internal/api/handlers"
	"github.com/nudgebee/e2e-dashboard/internal/api/middleware"
	"github.com/nudgebee/e2e-dashboard/internal/store"
)

// SetupRouter configures all API routes
func SetupRouter(s *store.Store) *gin.Engine {
	router := gin.Default()

	// Middleware
	router.Use(middleware.CORS())

	// Handlers
	runsHandler := handlers.NewRunsHandler(s)
	resultsHandler := handlers.NewResultsHandler(s)
	metricsHandler := handlers.NewMetricsHandler(s)

	// Health check
	router.GET("/health", metricsHandler.Health)

	// API v1 routes
	v1 := router.Group("/api/v1")
	{
		// Test runs
		runs := v1.Group("/runs")
		{
			runs.POST("", runsHandler.CreateRun)
			runs.GET("", runsHandler.ListRuns)
			runs.GET("/:id", runsHandler.GetRun)
			runs.PUT("/:id", runsHandler.UpdateRun)

			// Results for a run
			runs.POST("/:id/results", resultsHandler.CreateResults)
			runs.GET("/:id/results", resultsHandler.GetResults)
		}

		// Metrics
		metrics := v1.Group("/metrics")
		{
			metrics.GET("/trends", metricsHandler.GetTrends)
			metrics.GET("/summary", metricsHandler.GetSummary)
		}
	}

	// Serve static files for dashboard
	router.StaticFS("/static", http.Dir("./web/dist"))

	// Serve index.html for root path
	router.GET("/", func(c *gin.Context) {
		c.File("./web/dist/index.html")
	})

	return router
}
