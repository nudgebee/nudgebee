package flow_sources

import (
	"fmt"
	"log/slog"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/security"
	"sync"
)

// FlowSourceFactory is a function type that creates a flow source instance
type FlowSourceFactory func(logger *slog.Logger) (core.FlowSourceInterface, error)

// FlowSourceRegistryEntry holds metadata about a registered flow source
type FlowSourceRegistryEntry struct {
	Name        string
	Factory     FlowSourceFactory
	Description string
	Category    string
}

var (
	// Global registry of flow source factories
	flowSourceRegistry = make(map[string]*FlowSourceRegistryEntry)
	flowRegistryMutex  sync.RWMutex
)

// RegisterFlowSourceFactory registers a flow source factory with the global registry
// This should be called in the init() function of each flow source implementation
func RegisterFlowSourceFactory(name string, factory FlowSourceFactory, description string, category string) {
	flowRegistryMutex.Lock()
	defer flowRegistryMutex.Unlock()

	if name == "" {
		panic("flow source name cannot be empty")
	}

	if factory == nil {
		panic(fmt.Sprintf("flow source factory for '%s' cannot be nil", name))
	}

	if _, exists := flowSourceRegistry[name]; exists {
		panic(fmt.Sprintf("flow source '%s' is already registered", name))
	}

	flowSourceRegistry[name] = &FlowSourceRegistryEntry{
		Name:        name,
		Factory:     factory,
		Description: description,
		Category:    category,
	}

	slog.Info("registered knowledge graph flow source factory",
		"flow_source", name,
		"description", description,
		"category", category)
}

// GetFlowSourceFactory retrieves a flow source factory by name
func GetFlowSourceFactory(name string) (FlowSourceFactory, error) {
	flowRegistryMutex.RLock()
	defer flowRegistryMutex.RUnlock()

	entry, exists := flowSourceRegistry[name]
	if !exists {
		return nil, fmt.Errorf("flow source '%s' not found in registry", name)
	}

	return entry.Factory, nil
}

// CreateFlowSource creates a new flow source instance using the registered factory
func CreateFlowSource(name string, logger *slog.Logger) (core.FlowSourceInterface, error) {
	factory, err := GetFlowSourceFactory(name)
	if err != nil {
		return nil, err
	}

	if logger == nil {
		logger = slog.Default()
	}

	flowSource, err := factory(logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create flow source '%s': %w", name, err)
	}

	return flowSource, nil
}

// ListRegisteredFlowSources returns a list of all registered flow source names
func ListRegisteredFlowSources() []string {
	flowRegistryMutex.RLock()
	defer flowRegistryMutex.RUnlock()

	flowSources := make([]string, 0, len(flowSourceRegistry))
	for name := range flowSourceRegistry {
		flowSources = append(flowSources, name)
	}

	return flowSources
}

// GetFlowSourceRegistryEntries returns all registered flow source entries with metadata
func GetFlowSourceRegistryEntries() []*FlowSourceRegistryEntry {
	flowRegistryMutex.RLock()
	defer flowRegistryMutex.RUnlock()

	entries := make([]*FlowSourceRegistryEntry, 0, len(flowSourceRegistry))
	for _, entry := range flowSourceRegistry {
		entries = append(entries, entry)
	}

	return entries
}

// IsFlowSourceRegistered checks if a flow source is registered
func IsFlowSourceRegistered(name string) bool {
	flowRegistryMutex.RLock()
	defer flowRegistryMutex.RUnlock()

	_, exists := flowSourceRegistry[name]
	return exists
}

// KnowledgeGraphService defines the interface for registering flow sources and enrichers
type KnowledgeGraphService interface {
	RegisterFlowSource(core.FlowSourceInterface) error
	RegisterExternalServiceEnricher(core.ExternalServiceEnricherInterface) error
}

// RegisterAllFlowSourcesToService registers all available flow sources to the provided service
// This also registers the centralized external service enricher
// This is a convenience function that simplifies service initialization
func RegisterAllFlowSourcesToService(service KnowledgeGraphService, ctx *security.RequestContext) error {
	logger := ctx.GetLogger()

	registeredFlowSources := ListRegisteredFlowSources()
	logger.Info("registering all flow sources from global registry",
		"available_flow_sources", registeredFlowSources)

	successCount := 0
	for _, flowSourceName := range registeredFlowSources {
		flowSource, err := CreateFlowSource(flowSourceName, logger)

		if err != nil {
			logger.Warn("failed to create flow source from registry",
				"flow_source", flowSourceName,
				"error", err)
			continue
		}

		if err := service.RegisterFlowSource(flowSource); err != nil {
			logger.Warn("failed to register flow source",
				"flow_source", flowSourceName,
				"error", err)
			continue
		}

		successCount++
	}

	logger.Info("flow source registration complete",
		"total_flow_sources", len(registeredFlowSources),
		"registered", successCount)

	// Register the centralized external service enricher
	// This enricher runs after all flow sources complete, allowing cross-source matching
	enricher := NewCentralizedExternalServiceEnricher(logger)
	if err := service.RegisterExternalServiceEnricher(enricher); err != nil {
		logger.Warn("failed to register external service enricher", "error", err)
	} else {
		logger.Info("registered centralized external service enricher")
	}

	return nil
}
