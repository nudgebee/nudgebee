package sources

import (
	"fmt"
	"log/slog"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/security"
	"sync"
)

// SourceFactory is a function type that creates a source instance
// Note: SourceConfig is already defined in interface.go
type SourceFactory func(config SourceConfig, logger *slog.Logger) (core.SourceInterface, error)

// CrossSourceEnricherFactory is a function type that creates a cross-source enricher instance
type CrossSourceEnricherFactory func(logger *slog.Logger) core.CrossSourceEnricherInterface

// SourceRegistryEntry holds metadata about a registered source
type SourceRegistryEntry struct {
	Name        string
	Factory     SourceFactory
	Description string
}

// EnricherRegistryEntry holds metadata about a registered cross-source enricher
type EnricherRegistryEntry struct {
	Name        string
	Factory     CrossSourceEnricherFactory
	Description string
}

var (
	// Global registry of source factories
	sourceRegistry = make(map[string]*SourceRegistryEntry)
	// Global registry of cross-source enricher factories
	enricherRegistry = make(map[string]*EnricherRegistryEntry)
	registryMutex    sync.RWMutex
)

// RegisterSourceFactory registers a source factory with the global registry
// This should be called in the init() function of each source implementation
func RegisterSourceFactory(name string, factory SourceFactory, description string) {
	registryMutex.Lock()
	defer registryMutex.Unlock()

	if name == "" {
		panic("source name cannot be empty")
	}

	if factory == nil {
		panic(fmt.Sprintf("source factory for '%s' cannot be nil", name))
	}

	if _, exists := sourceRegistry[name]; exists {
		panic(fmt.Sprintf("source '%s' is already registered", name))
	}

	sourceRegistry[name] = &SourceRegistryEntry{
		Name:        name,
		Factory:     factory,
		Description: description,
	}

	slog.Info("registered knowledge graph source factory", "source", name, "description", description)
}

// GetSourceFactory retrieves a source factory by name
func GetSourceFactory(name string) (SourceFactory, error) {
	registryMutex.RLock()
	defer registryMutex.RUnlock()

	entry, exists := sourceRegistry[name]
	if !exists {
		return nil, fmt.Errorf("source '%s' not found in registry", name)
	}

	return entry.Factory, nil
}

// CreateSource creates a new source instance using the registered factory
func CreateSource(name string, config SourceConfig, logger *slog.Logger) (core.SourceInterface, error) {
	factory, err := GetSourceFactory(name)
	if err != nil {
		return nil, err
	}

	if logger == nil {
		logger = slog.Default()
	}

	source, err := factory(config, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create source '%s': %w", name, err)
	}

	return source, nil
}

// ListRegisteredSources returns a list of all registered source names
func ListRegisteredSources() []string {
	registryMutex.RLock()
	defer registryMutex.RUnlock()

	sources := make([]string, 0, len(sourceRegistry))
	for name := range sourceRegistry {
		sources = append(sources, name)
	}

	return sources
}

// GetRegistryEntries returns all registered source entries with metadata
func GetRegistryEntries() []*SourceRegistryEntry {
	registryMutex.RLock()
	defer registryMutex.RUnlock()

	entries := make([]*SourceRegistryEntry, 0, len(sourceRegistry))
	for _, entry := range sourceRegistry {
		entries = append(entries, entry)
	}

	return entries
}

// IsSourceRegistered checks if a source is registered
func IsSourceRegistered(name string) bool {
	registryMutex.RLock()
	defer registryMutex.RUnlock()

	_, exists := sourceRegistry[name]
	return exists
}

// RegisterAllSourcesToService registers all available sources to the provided service
// TenantID and CloudAccountID can be empty - sources will use values from BuildRequest instead
// This is a convenience function that simplifies service initialization
func RegisterAllSourcesToService(service interface {
	RegisterSource(core.SourceInterface) error
}, tenantID, cloudAccountID string, ctx *security.RequestContext) error {
	logger := ctx.GetLogger()

	registeredSources := ListRegisteredSources()
	logger.Info("registering all sources from global registry",
		"tenant_id", tenantID,
		"cloud_account_id", cloudAccountID,
		"available_sources", registeredSources)

	successCount := 0
	for _, sourceName := range registeredSources {
		source, err := CreateSource(sourceName, SourceConfig{
			TenantID:       tenantID,
			CloudAccountID: cloudAccountID,
		}, logger)

		if err != nil {
			logger.Warn("failed to create source from registry",
				"source", sourceName,
				"error", err)
			continue
		}

		if err := service.RegisterSource(source); err != nil {
			logger.Warn("failed to register source",
				"source", sourceName,
				"error", err)
			continue
		}

		successCount++
	}

	logger.Info("source registration complete",
		"total_sources", len(registeredSources),
		"registered", successCount)

	return nil
}

// =============================================================================
// Cross-Source Enricher Registry Functions
// =============================================================================

// RegisterCrossSourceEnricherFactory registers a cross-source enricher factory with the global registry
// This should be called in the init() function of each enricher implementation
func RegisterCrossSourceEnricherFactory(name string, factory CrossSourceEnricherFactory, description string) {
	registryMutex.Lock()
	defer registryMutex.Unlock()

	if name == "" {
		panic("enricher name cannot be empty")
	}

	if factory == nil {
		panic(fmt.Sprintf("enricher factory for '%s' cannot be nil", name))
	}

	if _, exists := enricherRegistry[name]; exists {
		panic(fmt.Sprintf("enricher '%s' is already registered", name))
	}

	enricherRegistry[name] = &EnricherRegistryEntry{
		Name:        name,
		Factory:     factory,
		Description: description,
	}

	slog.Info("registered cross-source enricher factory", "enricher", name, "description", description)
}

// GetCrossSourceEnricherFactory retrieves an enricher factory by name
func GetCrossSourceEnricherFactory(name string) (CrossSourceEnricherFactory, error) {
	registryMutex.RLock()
	defer registryMutex.RUnlock()

	entry, exists := enricherRegistry[name]
	if !exists {
		return nil, fmt.Errorf("enricher '%s' not found in registry", name)
	}

	return entry.Factory, nil
}

// CreateCrossSourceEnricher creates a new enricher instance using the registered factory
func CreateCrossSourceEnricher(name string, logger *slog.Logger) (core.CrossSourceEnricherInterface, error) {
	factory, err := GetCrossSourceEnricherFactory(name)
	if err != nil {
		return nil, err
	}

	if logger == nil {
		logger = slog.Default()
	}

	return factory(logger), nil
}

// ListRegisteredEnrichers returns a list of all registered enricher names
func ListRegisteredEnrichers() []string {
	registryMutex.RLock()
	defer registryMutex.RUnlock()

	enrichers := make([]string, 0, len(enricherRegistry))
	for name := range enricherRegistry {
		enrichers = append(enrichers, name)
	}

	return enrichers
}

// RegisterAllEnrichersToService registers all available enrichers to the provided service
func RegisterAllEnrichersToService(service interface {
	RegisterCrossSourceEnricher(core.CrossSourceEnricherInterface) error
}, ctx *security.RequestContext) error {
	logger := ctx.GetLogger()

	registeredEnrichers := ListRegisteredEnrichers()
	logger.Info("registering all cross-source enrichers from global registry",
		"available_enrichers", registeredEnrichers)

	successCount := 0
	for _, enricherName := range registeredEnrichers {
		enricher, err := CreateCrossSourceEnricher(enricherName, logger)
		if err != nil {
			logger.Warn("failed to create enricher from registry",
				"enricher", enricherName,
				"error", err)
			continue
		}

		if err := service.RegisterCrossSourceEnricher(enricher); err != nil {
			logger.Warn("failed to register enricher",
				"enricher", enricherName,
				"error", err)
			continue
		}

		successCount++
	}

	logger.Info("enricher registration complete",
		"total_enrichers", len(registeredEnrichers),
		"registered", successCount)

	return nil
}
