package core

import (
	"log/slog"
	"strings"
)

var nbSystemTools = map[string]Integration{}

func RegisterIntegration(integration Integration) {
	slog.Info("registering integration", "integration", integration.Name())
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
	slog.Info("registering integration with source", "provider", providerName, "source", source, "integration", integration.Name())
	nbSystemTools[key] = integration
}

func GetIntegration(toolName string) (Integration, bool) {
	toolFactory := nbSystemTools[strings.ToLower(toolName)]
	if toolFactory == nil {
		return nil, false
	}
	return toolFactory, true
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
