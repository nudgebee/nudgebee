package servicemap

import (
	"context"
	"fmt"
	"log/slog"
	"nudgebee/collector/cloud/providers"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
)

// QueryEngine coordinates querying multiple relationship sources in parallel
type QueryEngine struct {
	sources       []RelationshipSource
	mergeStrategy MergeStrategy
	timeout       time.Duration
	logger        *slog.Logger
}

// NewQueryEngine creates a new query engine with the given sources
func NewQueryEngine(sources []RelationshipSource, logger *slog.Logger) *QueryEngine {
	return &QueryEngine{
		sources:       sources,
		mergeStrategy: &DefaultMergeStrategy{},
		timeout:       60 * time.Second,
		logger:        logger,
	}
}

// SetTimeout sets the maximum time to wait for all sources
func (e *QueryEngine) SetTimeout(timeout time.Duration) {
	e.timeout = timeout
}

// SetMergeStrategy sets the strategy for merging results from multiple sources
func (e *QueryEngine) SetMergeStrategy(strategy MergeStrategy) {
	e.mergeStrategy = strategy
}

// Query executes parallel queries across all available sources
func (e *QueryEngine) Query(ctx context.Context, cfg aws.Config, account providers.Account, request QueryRequest) ([]providers.ServiceMapApplication, error) {
	// Create a context with timeout
	queryCtx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()

	// Determine which sources are available and support the requested resource types
	activeSources := e.selectActiveSources(queryCtx, cfg, account, request)

	if len(activeSources) == 0 {
		return nil, fmt.Errorf("no available sources for the requested resources")
	}

	e.logger.Info("executing service map query",
		"activeSources", len(activeSources),
		"totalSources", len(e.sources),
		"resources", len(request.Resources))

	// Execute queries in parallel
	responses := e.executeParallel(queryCtx, activeSources, request)

	// Merge results
	applications, err := e.mergeStrategy.Merge(responses)
	if err != nil {
		return nil, fmt.Errorf("failed to merge results: %w", err)
	}

	e.logger.Info("service map query completed",
		"applications", len(applications),
		"sources", len(responses))

	return applications, nil
}

// selectActiveSources filters sources based on availability and resource type support
func (e *QueryEngine) selectActiveSources(ctx context.Context, cfg aws.Config, account providers.Account, request QueryRequest) []RelationshipSource {
	var activeSources []RelationshipSource

	// Build set of unique resource types
	resourceTypes := make(map[string]bool)
	for _, res := range request.Resources {
		resourceTypes[res.ResourceType] = true
	}

	for _, source := range e.sources {
		// Check if source is available
		if !source.IsAvailable(ctx, cfg, account) {
			e.logger.Debug("source not available",
				"source", source.Name())
			continue
		}

		// Check if source supports at least one of the requested resource types
		supported := false
		for resourceType := range resourceTypes {
			if source.SupportsResourceType(resourceType) {
				supported = true
				break
			}
		}

		if supported {
			activeSources = append(activeSources, source)
		} else {
			e.logger.Debug("source does not support requested resource types",
				"source", source.Name())
		}
	}

	return activeSources
}

// executeParallel runs all sources concurrently with timeout and error handling
func (e *QueryEngine) executeParallel(ctx context.Context, sources []RelationshipSource, request QueryRequest) []QueryResponse {
	responses := make([]QueryResponse, 0, len(sources))
	responseChan := make(chan QueryResponse, len(sources))
	var wg sync.WaitGroup

	// Launch a goroutine for each source
	for _, source := range sources {
		wg.Add(1)
		go func(src RelationshipSource) {
			defer wg.Done()

			startTime := time.Now()
			e.logger.Debug("querying source", "source", src.Name())

			// Execute query with timeout
			resp, err := src.GetRelationships(ctx, request)

			executionTime := time.Since(startTime)

			if err != nil {
				e.logger.Warn("source query failed",
					"source", src.Name(),
					"error", err,
					"duration", executionTime)

				// Return error response
				responseChan <- QueryResponse{
					Applications: []providers.ServiceMapApplication{},
					Errors:       []error{err},
					Metadata: SourceMetadata{
						Source:        src.Name(),
						QueriedAt:     startTime,
						ExecutionTime: executionTime,
					},
				}
				return
			}

			// Enrich metadata
			resp.Metadata.Source = src.Name()
			resp.Metadata.QueriedAt = startTime
			resp.Metadata.ExecutionTime = executionTime

			e.logger.Info("source query completed",
				"source", src.Name(),
				"applications", len(resp.Applications),
				"duration", executionTime)

			responseChan <- resp
		}(source)
	}

	// Wait for all goroutines and close channel
	go func() {
		wg.Wait()
		close(responseChan)
	}()

	// Collect responses (with timeout)
	timeoutTimer := time.NewTimer(e.timeout)
	defer timeoutTimer.Stop()

	for {
		select {
		case resp, ok := <-responseChan:
			if !ok {
				// Channel closed, all responses received
				return responses
			}
			responses = append(responses, resp)

		case <-timeoutTimer.C:
			e.logger.Warn("query timeout reached",
				"timeout", e.timeout,
				"responsesReceived", len(responses),
				"expectedResponses", len(sources))
			return responses

		case <-ctx.Done():
			e.logger.Warn("query cancelled",
				"responsesReceived", len(responses),
				"expectedResponses", len(sources))
			return responses
		}
	}
}

// QueryPlanner optimizes query execution based on source characteristics
type QueryPlanner struct {
	logger *slog.Logger
}

// NewQueryPlanner creates a new query planner
func NewQueryPlanner(logger *slog.Logger) *QueryPlanner {
	return &QueryPlanner{logger: logger}
}

// OptimizeRequest analyzes the request and returns optimization hints
func (p *QueryPlanner) OptimizeRequest(request QueryRequest, sources []RelationshipSource) QueryPlan {
	plan := QueryPlan{
		OriginalRequest: request,
		SourcePlans:     make([]SourcePlan, 0, len(sources)),
	}

	// For each source, determine optimal execution parameters
	for _, source := range sources {
		sourcePlan := SourcePlan{
			Source:   source,
			Enabled:  true,
			Timeout:  30 * time.Second, // Default timeout
			Priority: source.Priority(),
		}

		// Customize timeout based on source characteristics
		switch source.Name() {
		case "aws-config":
			// Config queries can be slow
			sourcePlan.Timeout = 45 * time.Second
		case "vpc-flow-logs":
			// Flow log queries can be very slow for large time ranges
			if request.TimeRange != nil {
				duration := request.TimeRange.End.Sub(request.TimeRange.Start)
				if duration > 24*time.Hour {
					p.logger.Warn("large time range for flow logs, may be slow",
						"duration", duration)
					sourcePlan.Timeout = 60 * time.Second
				}
			}
		case "service-specific":
			// Fast, direct API calls
			sourcePlan.Timeout = 15 * time.Second
		}

		plan.SourcePlans = append(plan.SourcePlans, sourcePlan)
	}

	return plan
}

// QueryPlan contains optimized execution parameters for a query
type QueryPlan struct {
	OriginalRequest QueryRequest
	SourcePlans     []SourcePlan
}

// SourcePlan contains execution parameters for a specific source
type SourcePlan struct {
	Source   RelationshipSource
	Enabled  bool
	Timeout  time.Duration
	Priority int
}

// CircuitBreaker prevents cascading failures from slow/failing sources
type CircuitBreaker struct {
	mu           sync.RWMutex
	failures     map[string]int
	lastFailure  map[string]time.Time
	threshold    int           // Open circuit after N failures
	resetTimeout time.Duration // Try again after this duration
	logger       *slog.Logger
}

// NewCircuitBreaker creates a new circuit breaker
func NewCircuitBreaker(threshold int, resetTimeout time.Duration, logger *slog.Logger) *CircuitBreaker {
	return &CircuitBreaker{
		failures:     make(map[string]int),
		lastFailure:  make(map[string]time.Time),
		threshold:    threshold,
		resetTimeout: resetTimeout,
		logger:       logger,
	}
}

// AllowRequest checks if a source should be queried
func (cb *CircuitBreaker) AllowRequest(sourceName string) bool {
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	failures, exists := cb.failures[sourceName]
	if !exists {
		return true
	}

	// Check if circuit should reset
	if lastFail, ok := cb.lastFailure[sourceName]; ok {
		if time.Since(lastFail) > cb.resetTimeout {
			// Reset circuit
			return true
		}
	}

	// Circuit is open if failures >= threshold
	return failures < cb.threshold
}

// RecordFailure increments failure count for a source
func (cb *CircuitBreaker) RecordFailure(sourceName string) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.failures[sourceName]++
	cb.lastFailure[sourceName] = time.Now()

	if cb.failures[sourceName] >= cb.threshold {
		cb.logger.Warn("circuit breaker opened",
			"source", sourceName,
			"failures", cb.failures[sourceName])
	}
}

// RecordSuccess resets failure count for a source
func (cb *CircuitBreaker) RecordSuccess(sourceName string) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	delete(cb.failures, sourceName)
	delete(cb.lastFailure, sourceName)
}
