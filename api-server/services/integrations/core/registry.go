package core

import (
	"log/slog"
	"strings"
)

var nbSystemTools = map[string]Integration{}

func RegisterIntegration(integration Integration) {
	slog.Debug("registering integration", "integration", integration.Name())
	if _, ok := nbSystemTools[strings.ToLower(integration.Name())]; ok {
		slog.Warn("integration already registered", "tool", integration.Name())
	}
	nbSystemTools[strings.ToLower(integration.Name())] = integration
}

// RegisterIntegrationWithSource registers an integration under a source-qualified
// key (e.g. "es:user") so that integrations with the same provider name but
// different sources (agent vs user) can have distinct schemas and validation.
// providerName is the lookup name (which may differ from integration.Name() when
// agent and user variants share a provider name).
func RegisterIntegrationWithSource(providerName string, source string, integration Integration) {
	key := strings.ToLower(providerName) + ":" + strings.ToLower(source)
	slog.Debug("registering integration with source", "provider", providerName, "source", source, "integration", integration.Name())
	nbSystemTools[key] = integration
}

// RegisteredIntegrations returns every integration registered under its plain
// name. Source-qualified registrations ("name:source") are variants of those and
// are left out.
func RegisteredIntegrations() []Integration {
	integrations := make([]Integration, 0, len(nbSystemTools))
	for key, integration := range nbSystemTools {
		if strings.Contains(key, ":") {
			continue
		}
		integrations = append(integrations, integration)
	}
	return integrations
}

func GetIntegration(toolName string) (Integration, bool) {
	key := strings.ToLower(toolName)
	if toolFactory := nbSystemTools[key]; toolFactory != nil {
		return toolFactory, true
	}
	// Fall back to any source-qualified registration ("name:source") so that
	// integrations registered only via RegisterIntegrationWithSource (e.g. pinot:user)
	// still resolve when callers look them up by plain name.
	prefix := key + ":"
	for k, v := range nbSystemTools {
		if strings.HasPrefix(k, prefix) {
			return v, true
		}
	}
	return nil, false
}

// GetIntegrationBySource looks up an integration by name and source.
// It first tries the source-qualified key (e.g. "es:user"), then falls back
// to the plain name lookup.
func GetIntegrationBySource(toolName string, source string) (Integration, bool) {
	if source != "" {
		key := strings.ToLower(toolName) + ":" + strings.ToLower(source)
		if toolFactory := nbSystemTools[key]; toolFactory != nil {
			return toolFactory, true
		}
	}
	return GetIntegration(toolName)
}
