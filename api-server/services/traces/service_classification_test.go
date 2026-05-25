package traces

import (
	"testing"
)

func TestIsLikelyKubernetesService(t *testing.T) {
	extractor := &TraceToKnowledgeGraphExtractor{
		accountID: "test-account",
		tenantID:  "test-tenant",
	}

	tests := []struct {
		hostname    string
		expected    bool
		description string
	}{
		// Internal services (should return true)
		{
			hostname:    "relay-server",
			expected:    true,
			description: "Simple service name with -server suffix",
		},
		{
			hostname:    "services-server",
			expected:    true,
			description: "Service name with -server suffix",
		},
		{
			hostname:    "user-service",
			expected:    true,
			description: "Service name with -service suffix",
		},
		{
			hostname:    "payment-api",
			expected:    true,
			description: "Service name with -api suffix",
		},
		{
			hostname:    "worker-app",
			expected:    true,
			description: "Service name with -app suffix",
		},
		{
			hostname:    "simple-name",
			expected:    true,
			description: "Simple name without dots should be internal",
		},
		{
			hostname:    "my-service.default.svc.cluster.local",
			expected:    true,
			description: "Kubernetes FQDN",
		},

		// External services (should return false)
		{
			hostname:    "api.external.com",
			expected:    false,
			description: "External domain with .com",
		},
		{
			hostname:    "service.example.org",
			expected:    false,
			description: "External domain with .org",
		},
		{
			hostname:    "payment.stripe.com",
			expected:    false,
			description: "External payment service",
		},
		{
			hostname:    "analytics.google.com",
			expected:    false,
			description: "External analytics service",
		},
	}

	for _, tt := range tests {
		t.Run(tt.description, func(t *testing.T) {
			result := extractor.isLikelyKubernetesService(tt.hostname)
			if result != tt.expected {
				t.Errorf("isLikelyKubernetesService(%q) = %v, expected %v",
					tt.hostname, result, tt.expected)
			}
		})
	}
}

func TestIsInternalDomain(t *testing.T) {
	extractor := &TraceToKnowledgeGraphExtractor{
		accountID: "test-account",
		tenantID:  "test-tenant",
	}

	tests := []struct {
		hostname    string
		expected    bool
		description string
	}{
		// Should be internal
		{
			hostname:    "relay-server",
			expected:    true,
			description: "Kubernetes service name",
		},
		{
			hostname:    "service.svc.cluster.local",
			expected:    true,
			description: "Kubernetes cluster FQDN",
		},
		{
			hostname:    "localhost",
			expected:    true,
			description: "Localhost",
		},
		{
			hostname:    "10.0.0.1",
			expected:    true,
			description: "Internal IP range",
		},
		{
			hostname:    "app.internal",
			expected:    true,
			description: "Internal domain suffix",
		},

		// Should be external
		{
			hostname:    "api.external.com",
			expected:    false,
			description: "External domain",
		},
		{
			hostname:    "payment.stripe.com",
			expected:    false,
			description: "External payment service",
		},
	}

	for _, tt := range tests {
		t.Run(tt.description, func(t *testing.T) {
			result := extractor.isInternalDomain(tt.hostname)
			if result != tt.expected {
				t.Errorf("isInternalDomain(%q) = %v, expected %v",
					tt.hostname, result, tt.expected)
			}
		})
	}
}
