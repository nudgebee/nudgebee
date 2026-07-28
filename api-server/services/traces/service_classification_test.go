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

		{
			hostname:    "127.0.0.1",
			expected:    true,
			description: "Loopback IP",
		},
		{
			hostname:    "172.16.0.1",
			expected:    true,
			description: "172.16/12 private IP",
		},
		{
			hostname:    "192.168.1.1",
			expected:    true,
			description: "192.168/16 private IP",
		},

		// Should be external
		{
			hostname:    "api.external.com",
			expected:    false,
			description: "External domain",
		},
		{
			hostname:    "app-172.prod.example.com",
			expected:    false,
			description: "External hostname containing 172.",
		},
		{
			hostname:    "172.217.16.142",
			expected:    false,
			description: "Public IP starting with 172 outside 172.16/12",
		},
		{
			hostname:    "10.example.com",
			expected:    false,
			description: "Hostname starting with 10.",
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

// TestDetectApplicationType_SpanNameFallbackRequiresInboundSpan guards against a
// service that merely calls (or consumes from) a message queue/db being
// misclassified as being that message queue/db itself — the span-name fallback
// in detectApplicationType must not trust a CLIENT/PRODUCER/CONSUMER span's
// name, since that name describes the destination/operation being invoked, not
// the calling service's own identity. CONSUMER is included because consuming
// from a queue is just as much an outbound interaction as producing to one —
// e.g. an ordinary webhook/backend service that also drains a RabbitMQ queue
// as one of several duties must not become a MessageQueue node just because
// one of its many spans is named "rabbitmq.consume".
func TestDetectApplicationType_SpanNameFallbackRequiresInboundSpan(t *testing.T) {
	builder := &TraceServiceMapBuilder{}

	tests := []struct {
		description  string
		workloadName string
		spanKind     string
		spanName     string
		expectedType string
	}{
		{
			description:  "CLIENT span calling rabbitmq must not reclassify the calling service as rabbitmq",
			workloadName: "cloud-collector-server",
			spanKind:     "CLIENT",
			spanName:     "rabbitmq.publish",
			expectedType: "",
		},
		{
			description:  "PRODUCER span calling kafka must not reclassify the calling service as kafka",
			workloadName: "services-server",
			spanKind:     "PRODUCER",
			spanName:     "kafka.send",
			expectedType: "",
		},
		{
			description:  "CONSUMER span calling rabbitmq must not reclassify the calling service as rabbitmq",
			workloadName: "services-server",
			spanKind:     "CONSUMER",
			spanName:     "rabbitmq.consume",
			expectedType: "",
		},
		{
			description:  "SERVER span still classifies via span name — this direction means the service itself is being addressed as the broker",
			workloadName: "worker-3",
			spanKind:     "SERVER",
			spanName:     "rabbitmq.process",
			expectedType: "rabbitmq",
		},
		{
			description:  "unrecognized/missing span kind falls back to trusting the span name, unchanged from pre-fix behavior",
			workloadName: "worker-4",
			spanKind:     "",
			spanName:     "kafka.process",
			expectedType: "kafka",
		},
		{
			description:  "a service self-named after the broker is still classified regardless of span kind",
			workloadName: "rabbitmq",
			spanKind:     "CLIENT",
			spanName:     "amqp.publish",
			expectedType: "rabbitmq",
		},
	}

	for _, tt := range tests {
		t.Run(tt.description, func(t *testing.T) {
			span := TraceSpan{WorkloadName: tt.workloadName, SpanName: tt.spanName}
			attrs := &SpanAttributes{SpanKind: tt.spanKind}

			appType, _ := builder.detectApplicationType(span, attrs, map[string]string{}, map[string]string{})
			if appType != tt.expectedType {
				t.Errorf("detectApplicationType(workload=%q, kind=%q, span=%q) = %q, expected %q",
					tt.workloadName, tt.spanKind, tt.spanName, appType, tt.expectedType)
			}
		})
	}
}
