package sources

import (
	"fmt"
	"log/slog"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/security"
	"sort"
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
	// RunsAfter names the enrichers whose output this one reads, so they must run
	// first in Phase 2.1. See ListRegisteredEnrichers.
	RunsAfter []string
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

	slog.Debug("registered knowledge graph source factory", "source", name, "description", description)
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
// This should be called in the init() function of each enricher implementation.
//
// runsAfter names enrichers whose output this one reads in the same build —
// edges or nodes they add to the slice it is handed. Declare it whenever that is
// true: without it the order is only alphabetical, and nothing fails when it is
// wrong, the enricher just finds less.
func RegisterCrossSourceEnricherFactory(name string, factory CrossSourceEnricherFactory, description string, runsAfter ...string) {
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
		RunsAfter:   runsAfter,
	}

	slog.Debug("registered cross-source enricher factory", "enricher", name, "description", description)
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

// ListRegisteredEnrichers returns every registered enricher name in the order
// Phase 2.1 should run them: each after the enrichers it declares in RunsAfter,
// ties broken by name.
//
// The order used to come straight from ranging over the registry map, which Go
// randomises on every call — and RegisterAllEnrichersToService runs per build,
// so two builds of the same inputs could run the enrichers in different orders
// and emit different graphs. That made the build impossible to replay
// deterministically, and silently broke aws_lb_k8s whenever it happened to run
// before k8s_ec2.
func ListRegisteredEnrichers() []string {
	// Copy under the lock, order outside it. Entries are never modified after
	// init, so the pointers stay valid once the lock is released.
	registryMutex.RLock()
	entries := make([]*EnricherRegistryEntry, 0, len(enricherRegistry))
	for _, entry := range enricherRegistry {
		entries = append(entries, entry)
	}
	registryMutex.RUnlock()
	return orderEnrichers(entries)
}

// orderEnrichers sorts entries so every enricher comes after the ones it names
// in RunsAfter, choosing the alphabetically first ready enricher at each step.
// It never fails at runtime: a dependency that is not registered is ignored,
// and enrichers caught in a cycle are appended in name order. Both are bugs in
// a registration, caught by TestRegisteredEnricherDependencies rather than here.
func orderEnrichers(entries []*EnricherRegistryEntry) []string {
	registered := make(map[string]bool, len(entries))
	for _, e := range entries {
		registered[e.Name] = true
	}

	// remaining[name] = how many registered dependencies have not been placed yet.
	remaining := make(map[string]int, len(entries))
	dependents := make(map[string][]string, len(entries))
	for _, e := range entries {
		remaining[e.Name] = 0
		for _, dep := range e.RunsAfter {
			if registered[dep] && dep != e.Name {
				remaining[e.Name]++
				dependents[dep] = append(dependents[dep], e.Name)
			}
		}
	}

	ready := make([]string, 0, len(entries))
	for name, n := range remaining {
		if n == 0 {
			ready = append(ready, name)
		}
	}

	ordered := make([]string, 0, len(entries))
	placed := make(map[string]bool, len(entries))
	for len(ready) > 0 {
		sort.Strings(ready)
		next := ready[0]
		ready = ready[1:]
		ordered = append(ordered, next)
		placed[next] = true
		for _, d := range dependents[next] {
			remaining[d]--
			if remaining[d] == 0 {
				ready = append(ready, d)
			}
		}
	}

	// Anything left is in a cycle.
	if len(ordered) < len(entries) {
		stuck := make([]string, 0, len(entries)-len(ordered))
		for _, e := range entries {
			if !placed[e.Name] {
				stuck = append(stuck, e.Name)
			}
		}
		sort.Strings(stuck)
		slog.Error("cross-source enricher dependencies form a cycle; running them in name order", "enrichers", stuck)
		ordered = append(ordered, stuck...)
	}
	return ordered
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
