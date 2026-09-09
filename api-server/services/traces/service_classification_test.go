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
//
// It also covers the case where span.kind isn't populated at all (observed in
// live data for a real service's "rabbitmq.consume" spans): an operation-verb
// suffix (consume/process/receive/send/produce/publish) in the span name is
// still treated as describing an interaction rather than an identity, unless
// span.kind is definitively SERVER.
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
			description:  "missing span kind with a bare technology name (no operation verb) still classifies",
			workloadName: "worker-4",
			spanKind:     "",
			spanName:     "kafka",
			expectedType: "kafka",
		},
		{
			description:  "missing span kind with an operation-verb span name must not reclassify — the real recurring case (services-server's rabbitmq.consume spans carry no span.kind)",
			workloadName: "services-server",
			spanKind:     "",
			spanName:     "rabbitmq.consume",
			expectedType: "",
		},
		{
			description:  "missing span kind with a producer-verb span name must not reclassify either",
			workloadName: "services-server",
			spanKind:     "",
			spanName:     "kafka.produce",
			expectedType: "",
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

			appType, _, _ := builder.detectApplicationType(span, attrs, map[string]string{}, map[string]string{})
			if appType != tt.expectedType {
				t.Errorf("detectApplicationType(workload=%q, kind=%q, span=%q) = %q, expected %q",
					tt.workloadName, tt.spanKind, tt.spanName, appType, tt.expectedType)
			}
		})
	}
}

// TestDetectApplicationType_UncertainMatchCapturesNearMiss verifies the third
// return value: a confidence guard that excludes a pattern match from
// overriding the service's type still surfaces the near-miss (for later
// review — see core.RecordUncertainClassification), but only when nothing
// more confident is found anywhere else in the function, and never for a
// span that genuinely matches nothing.
func TestDetectApplicationType_UncertainMatchCapturesNearMiss(t *testing.T) {
	builder := &TraceServiceMapBuilder{}

	tests := []struct {
		description      string
		workloadName     string
		spanKind         string
		spanName         string
		messagingSystem  string
		telemetryLang    string
		expectUncertain  bool
		expectReasonCode string
	}{
		{
			description:     "CLIENT span matching rabbitmq surfaces an uncertain match with the outbound reason",
			workloadName:    "services-server",
			spanKind:        "CLIENT",
			spanName:        "rabbitmq.publish",
			expectUncertain: true, expectReasonCode: "outbound_span_kind",
		},
		{
			description:     "CONSUMER span matching rabbitmq also surfaces an uncertain match",
			workloadName:    "services-server",
			spanKind:        "CONSUMER",
			spanName:        "rabbitmq.consume",
			expectUncertain: true, expectReasonCode: "outbound_span_kind",
		},
		{
			// The span.kind-absent guard: a "rabbitmq.consume" span carrying no
			// span.kind at all is excluded by looksLikeOperation, not by
			// isOutboundKind — so the near-miss must be recorded under its own
			// reason code, or a review row would claim a span kind the span
			// never had.
			description:     "operation-verb span name with no span.kind surfaces the operation-verb reason, not the outbound one",
			workloadName:    "services-server",
			spanKind:        "",
			spanName:        "rabbitmq.consume",
			expectUncertain: true, expectReasonCode: "operation_verb_span_name",
		},
		{
			description:     "SERVER span matching rabbitmq is trusted as an identity, so no near-miss is recorded",
			workloadName:    "services-server",
			spanKind:        "SERVER",
			spanName:        "rabbitmq.consume",
			expectUncertain: false,
		},
		{
			description:      "messaging.system present but service name doesn't contain it surfaces the name-mismatch reason",
			workloadName:     "services-server",
			messagingSystem:  "kafka",
			expectUncertain:  true,
			expectReasonCode: "messaging_system_name_mismatch",
		},
		{
			description:     "a span matching no pattern at all must not produce noise",
			workloadName:    "ordinary-http-service",
			spanKind:        "CLIENT",
			spanName:        "GET /health",
			expectUncertain: false,
		},
		{
			description:     "a confident language match must not carry a stray uncertain value",
			workloadName:    "ordinary-service",
			telemetryLang:   "python",
			expectUncertain: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.description, func(t *testing.T) {
			span := TraceSpan{WorkloadName: tt.workloadName, SpanName: tt.spanName}
			attrs := &SpanAttributes{SpanKind: tt.spanKind, MessagingSystem: tt.messagingSystem}
			labels := map[string]string{}
			if tt.telemetryLang != "" {
				labels["telemetry.sdk.language"] = tt.telemetryLang
			}

			_, _, uncertain := builder.detectApplicationType(span, attrs, map[string]string{}, labels)
			if tt.expectUncertain && uncertain == nil {
				t.Fatalf("expected an uncertain match, got nil")
			}
			if !tt.expectUncertain && uncertain != nil {
				t.Fatalf("expected no uncertain match, got %+v", uncertain)
			}
			if tt.expectUncertain && uncertain.ReasonCode != tt.expectReasonCode {
				t.Errorf("ReasonCode = %q, expected %q", uncertain.ReasonCode, tt.expectReasonCode)
			}
		})
	}
}

// TestBuildServiceMap_UncertainMatchAggregation verifies the aggregation
// behavior in service_map.go across multiple spans for the same service:
// the first near-miss wins (a later near-miss for a different reason doesn't
// overwrite it), and it coexists with — is not cleared by — a confident type
// detected from a different span for the same service.
func TestBuildServiceMap_UncertainMatchAggregation(t *testing.T) {
	builder := NewTraceServiceMapBuilder()
	builder.AddSpans([]TraceSpan{
		{
			WorkloadName:      "services-server",
			WorkloadNamespace: "example-ns",
			SpanID:            "span1",
			TraceID:           "trace1",
			SpanName:          "rabbitmq.consume",
			Timestamp:         "2026-01-01T00:00:00Z",
			SpanAttributes:    map[string]string{"span.kind": "CONSUMER"},
		},
		{
			// A second, different near-miss reason for the same service —
			// must NOT overwrite the first one captured above.
			WorkloadName:      "services-server",
			WorkloadNamespace: "example-ns",
			SpanID:            "span2",
			TraceID:           "trace2",
			SpanName:          "kafka.publish",
			Timestamp:         "2026-01-01T00:01:00Z",
			SpanAttributes:    map[string]string{"span.kind": "PRODUCER"},
		},
		{
			// A confident classification from a different span — coexists
			// with the uncertain match rather than clearing it.
			WorkloadName:      "services-server",
			WorkloadNamespace: "example-ns",
			SpanID:            "span3",
			TraceID:           "trace3",
			SpanName:          "GET /health",
			Timestamp:         "2026-01-01T00:02:00Z",
			SpanAttributes:    map[string]string{"telemetry.sdk.language": "python"},
		},
	})

	serviceMap, err := builder.BuildServiceMap()
	if err != nil {
		t.Fatalf("BuildServiceMap: %v", err)
	}

	var app *ServiceApplication
	for i := range serviceMap.Applications {
		if serviceMap.Applications[i].Id.Name == "services-server" {
			app = &serviceMap.Applications[i]
			break
		}
	}
	if app == nil {
		t.Fatalf("expected an application named services-server in the service map")
	}

	if app.UncertainMatch == nil {
		t.Fatalf("expected UncertainMatch to be set")
	}
	if app.UncertainMatch.SpanID != "span1" {
		t.Errorf("UncertainMatch.SpanID = %q, expected the FIRST near-miss span (span1), not a later one overwriting it", app.UncertainMatch.SpanID)
	}
	if app.UncertainMatch.MatchedValue != "rabbitmq" {
		t.Errorf("UncertainMatch.MatchedValue = %q, expected %q", app.UncertainMatch.MatchedValue, "rabbitmq")
	}

	foundPython := false
	for _, typ := range app.Type {
		if typ == "python" {
			foundPython = true
		}
	}
	if !foundPython {
		t.Errorf("Type = %v, expected it to include the confident \"python\" classification from span3", app.Type)
	}
}

// TestDetectApplicationType_ConfidentHTTPFallbackClearsUncertainMatch is a
// regression test: the HTTP-fallback branch calls detectHTTPServiceType,
// which can itself confidently classify a span (e.g. via http.server_name
// matching nginx/envoy — a signal not checked anywhere earlier in
// detectApplicationType). When it does, any near-miss captured earlier in
// the SAME call must not be surfaced — returning both a confident type and
// an uncertain match for the same span violates detectApplicationType's own
// documented contract ("only surfaced if nothing more confident is found
// anywhere else in the function").
func TestDetectApplicationType_ConfidentHTTPFallbackClearsUncertainMatch(t *testing.T) {
	builder := &TraceServiceMapBuilder{}

	span := TraceSpan{WorkloadName: "services-server", SpanName: "rabbitmq.http-check"}
	attrs := &SpanAttributes{SpanKind: "CLIENT", HTTPStatusCode: 200}
	rawAttrs := map[string]string{"http.server_name": "nginx-proxy"}

	appType, _, uncertain := builder.detectApplicationType(span, attrs, rawAttrs, map[string]string{})
	if appType != "nginx" {
		t.Fatalf("appType = %q, expected the confident \"nginx\" match from http.server_name", appType)
	}
	if uncertain != nil {
		t.Errorf("expected no uncertain match once http.server_name confidently classified this span as nginx, got %+v", uncertain)
	}
}
