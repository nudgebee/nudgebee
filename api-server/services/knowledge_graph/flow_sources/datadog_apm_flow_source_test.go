package flow_sources

import (
	"log/slog"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/traces"
	"testing"
)

func TestNewDatadogAPMFlowSource(t *testing.T) {
	logger := slog.Default()
	source := NewDatadogAPMFlowSource(logger)

	if source == nil {
		t.Fatal("NewDatadogAPMFlowSource() returned nil")
		return
	}

	if source.GetName() != "datadog-apm" {
		t.Errorf("GetName() = %v, want 'datadog-apm'", source.GetName())
	}

	if source.GetSourceCategory() != core.FlowSourceCategoryTracing {
		t.Errorf("GetSourceCategory() = %v, want %v", source.GetSourceCategory(), core.FlowSourceCategoryTracing)
	}

	if !source.IsEnabled() {
		t.Error("IsEnabled() = false, want true")
	}
}

func TestDatadogAPMFlowSource_Validate(t *testing.T) {
	logger := slog.Default()
	source := NewDatadogAPMFlowSource(logger)

	err := source.Validate()
	if err != nil {
		t.Errorf("Validate() error = %v, want nil", err)
	}
}

func TestDatadogAPMFlowSource_extractEntityNameAndKind(t *testing.T) {
	logger := slog.Default()
	source := NewDatadogAPMFlowSource(logger)

	tests := []struct {
		name        string
		entity      *traces.APMEntity
		wantName    string
		wantKind    string
		description string
	}{
		{
			name: "service entity",
			entity: &traces.APMEntity{
				ID:   "entity-1",
				Type: "apm-entity",
				Attributes: traces.APMEntityAttributes{
					IDTags: map[string]string{
						"service": "my-service",
					},
				},
			},
			wantName:    "my-service",
			wantKind:    "Service",
			description: "Normal service should return service name and Service kind",
		},
		{
			name: "database peer with db name",
			entity: &traces.APMEntity{
				ID:   "entity-2",
				Type: "apm-entity",
				Attributes: traces.APMEntityAttributes{
					IDTags: map[string]string{
						"peer.db.system": "postgres",
						"peer.db.name":   "mydb",
					},
				},
			},
			wantName:    "mydb",
			wantKind:    "postgres",
			description: "Database with name should return db name and db system",
		},
		{
			name: "database peer without db name",
			entity: &traces.APMEntity{
				ID:   "entity-3",
				Type: "apm-entity",
				Attributes: traces.APMEntityAttributes{
					IDTags: map[string]string{
						"peer.db.system": "redis",
					},
				},
			},
			wantName:    "redis",
			wantKind:    "redis",
			description: "Database without name should return db system for both",
		},
		{
			name: "RPC service peer",
			entity: &traces.APMEntity{
				ID:   "entity-4",
				Type: "apm-entity",
				Attributes: traces.APMEntityAttributes{
					IDTags: map[string]string{
						"peer.rpc.service": "grpc-service",
					},
				},
			},
			wantName:    "grpc-service",
			wantKind:    "Service",
			description: "RPC service should return service name and Service kind",
		},
		{
			name: "hostname peer",
			entity: &traces.APMEntity{
				ID:   "entity-5",
				Type: "apm-entity",
				Attributes: traces.APMEntityAttributes{
					IDTags: map[string]string{
						"peer.hostname": "external-api.com",
					},
				},
			},
			wantName:    "external-api.com",
			wantKind:    "ExternalService",
			description: "Hostname peer should return hostname and ExternalService kind",
		},
		{
			name: "Kafka messaging destination",
			entity: &traces.APMEntity{
				ID:   "entity-6",
				Type: "apm-entity",
				Attributes: traces.APMEntityAttributes{
					IDTags: map[string]string{
						"peer.messaging.destination": "my-topic",
					},
				},
			},
			wantName:    "my-topic",
			wantKind:    "kafka",
			description: "Kafka topic should return topic name and kafka kind",
		},
		{
			name: "entity with no specific tags",
			entity: &traces.APMEntity{
				ID:   "entity-7",
				Type: "apm-entity",
				Attributes: traces.APMEntityAttributes{
					IDTags: map[string]string{},
				},
			},
			wantName:    "entity-7",
			wantKind:    "Service",
			description: "Entity with no tags should default to entity ID and Service kind",
		},
		{
			name:        "nil entity",
			entity:      nil,
			wantName:    "",
			wantKind:    "",
			description: "Nil entity should return empty strings",
		},
		{
			name: "service takes priority over other tags",
			entity: &traces.APMEntity{
				ID:   "entity-8",
				Type: "apm-entity",
				Attributes: traces.APMEntityAttributes{
					IDTags: map[string]string{
						"service":       "priority-service",
						"peer.hostname": "other-host.com",
					},
				},
			},
			wantName:    "priority-service",
			wantKind:    "Service",
			description: "Service tag should take priority over other tags",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotName, gotKind := source.extractEntityNameAndKind(tt.entity)

			if gotName != tt.wantName {
				t.Errorf("extractEntityNameAndKind() name = %v, want %v\nDescription: %s",
					gotName, tt.wantName, tt.description)
			}

			if gotKind != tt.wantKind {
				t.Errorf("extractEntityNameAndKind() kind = %v, want %v\nDescription: %s",
					gotKind, tt.wantKind, tt.description)
			}
		})
	}
}

func TestDatadogAPMFlowSource_inferProtocol(t *testing.T) {
	logger := slog.Default()
	source := NewDatadogAPMFlowSource(logger)

	tests := []struct {
		name         string
		operation    string
		wantProtocol string
	}{
		{
			name:         "HTTP request",
			operation:    "http.request",
			wantProtocol: "http",
		},
		{
			name:         "Universal HTTP",
			operation:    "universal.http.request",
			wantProtocol: "http",
		},
		{
			name:         "Web request",
			operation:    "web.request",
			wantProtocol: "http",
		},
		{
			name:         "HTTP client",
			operation:    "http.client.request",
			wantProtocol: "http",
		},
		{
			name:         "gRPC",
			operation:    "grpc.client.call",
			wantProtocol: "grpc",
		},
		{
			name:         "Postgres query",
			operation:    "postgres.query",
			wantProtocol: "postgres",
		},
		{
			name:         "Redis command",
			operation:    "redis.command",
			wantProtocol: "redis",
		},
		{
			name:         "Kafka consume",
			operation:    "kafka.consume",
			wantProtocol: "kafka",
		},
		{
			name:         "Kafka produce",
			operation:    "kafka.produce",
			wantProtocol: "kafka",
		},
		{
			name:         "MongoDB query",
			operation:    "mongo.query.find",
			wantProtocol: "mongo",
		},
		{
			name:         "S3 command",
			operation:    "s3.command.getobject",
			wantProtocol: "s3",
		},
		{
			name:         "Unknown operation",
			operation:    "custom.unknown.operation",
			wantProtocol: "unknown",
		},
		{
			name:         "Case insensitive HTTP",
			operation:    "HTTP.REQUEST",
			wantProtocol: "http",
		},
		{
			name:         "Mixed case gRPC",
			operation:    "GRPC.Server.Call",
			wantProtocol: "grpc",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := source.inferProtocol(tt.operation)

			if got != tt.wantProtocol {
				t.Errorf("inferProtocol(%q) = %v, want %v",
					tt.operation, got, tt.wantProtocol)
			}
		})
	}
}

func TestDatadogAPMFlowSource_buildEdgeProperties(t *testing.T) {
	logger := slog.Default()
	source := NewDatadogAPMFlowSource(logger)

	sourceEntity := &traces.APMEntity{
		ID:   "source-entity-1",
		Type: "apm-entity",
		Attributes: traces.APMEntityAttributes{
			IDTags: map[string]string{
				"service": "source-service",
			},
		},
	}

	destEntity := &traces.APMEntity{
		ID:   "dest-entity-1",
		Type: "apm-entity",
		Attributes: traces.APMEntityAttributes{
			IDTags: map[string]string{
				"service": "dest-service",
			},
		},
	}

	apmEdge := &traces.APMEntityEdge{
		ID:   "edge-1",
		Type: "apm-entity-edge",
		Attributes: traces.APMEntityEdgeAttributes{
			Operation: "http.request",
			SpanKind:  "client",
		},
	}

	properties := source.buildEdgeProperties(apmEdge, sourceEntity, destEntity)

	// Verify all expected properties are present
	expectedProperties := map[string]interface{}{
		"dd_operation":        "http.request",
		"dd_span_kind":        "client",
		"protocol":            "http",
		"dd_source_entity_id": "source-entity-1",
		"dd_dest_entity_id":   "dest-entity-1",
		"dd_edge_id":          "edge-1",
		"dd_source_service":   "source-service",
		"dd_dest_service":     "dest-service",
	}

	for key, expectedValue := range expectedProperties {
		if value, exists := properties[key]; !exists {
			t.Errorf("buildEdgeProperties() missing property %q", key)
		} else if value != expectedValue {
			t.Errorf("buildEdgeProperties() property %q = %v, want %v", key, value, expectedValue)
		}
	}
}

func TestDatadogAPMFlowSource_buildEdgeProperties_NoServiceTags(t *testing.T) {
	logger := slog.Default()
	source := NewDatadogAPMFlowSource(logger)

	sourceEntity := &traces.APMEntity{
		ID:   "source-entity-2",
		Type: "apm-entity",
		Attributes: traces.APMEntityAttributes{
			IDTags: map[string]string{
				"peer.hostname": "external-api.com",
			},
		},
	}

	destEntity := &traces.APMEntity{
		ID:   "dest-entity-2",
		Type: "apm-entity",
		Attributes: traces.APMEntityAttributes{
			IDTags: map[string]string{
				"peer.db.system": "postgres",
			},
		},
	}

	apmEdge := &traces.APMEntityEdge{
		ID:   "edge-2",
		Type: "apm-entity-edge",
		Attributes: traces.APMEntityEdgeAttributes{
			Operation: "postgres.query",
			SpanKind:  "client",
		},
	}

	properties := source.buildEdgeProperties(apmEdge, sourceEntity, destEntity)

	// Verify essential properties are present
	if _, exists := properties["dd_operation"]; !exists {
		t.Error("buildEdgeProperties() missing dd_operation")
	}

	if _, exists := properties["dd_span_kind"]; !exists {
		t.Error("buildEdgeProperties() missing dd_span_kind")
	}

	if _, exists := properties["protocol"]; !exists {
		t.Error("buildEdgeProperties() missing protocol")
	}

	// Verify service tags are NOT present (since entities don't have service tags)
	if _, exists := properties["dd_source_service"]; exists {
		t.Error("buildEdgeProperties() should not have dd_source_service when entity has no service tag")
	}

	if _, exists := properties["dd_dest_service"]; exists {
		t.Error("buildEdgeProperties() should not have dd_dest_service when entity has no service tag")
	}
}

func TestDatadogAPMFlowSource_buildEdgeProperties_UnknownProtocol(t *testing.T) {
	logger := slog.Default()
	source := NewDatadogAPMFlowSource(logger)

	sourceEntity := &traces.APMEntity{
		ID:   "source-entity-3",
		Type: "apm-entity",
		Attributes: traces.APMEntityAttributes{
			IDTags: map[string]string{
				"service": "source-service",
			},
		},
	}

	destEntity := &traces.APMEntity{
		ID:   "dest-entity-3",
		Type: "apm-entity",
		Attributes: traces.APMEntityAttributes{
			IDTags: map[string]string{
				"service": "dest-service",
			},
		},
	}

	apmEdge := &traces.APMEntityEdge{
		ID:   "edge-3",
		Type: "apm-entity-edge",
		Attributes: traces.APMEntityEdgeAttributes{
			Operation: "custom.unknown.operation",
			SpanKind:  "client",
		},
	}

	properties := source.buildEdgeProperties(apmEdge, sourceEntity, destEntity)

	// Verify protocol is not added when it's "unknown"
	if protocol, exists := properties["protocol"]; exists {
		t.Errorf("buildEdgeProperties() should not add protocol when unknown, got: %v", protocol)
	}
}

func TestDatadogAPMFlowSource_extractEntityNameAndKind_EdgeCases(t *testing.T) {
	logger := slog.Default()
	source := NewDatadogAPMFlowSource(logger)

	tests := []struct {
		name     string
		entity   *traces.APMEntity
		wantName string
		wantKind string
	}{
		{
			name: "database with empty name",
			entity: &traces.APMEntity{
				ID:   "entity-empty-db",
				Type: "apm-entity",
				Attributes: traces.APMEntityAttributes{
					IDTags: map[string]string{
						"peer.db.system": "mysql",
						"peer.db.name":   "",
					},
				},
			},
			wantName: "mysql",
			wantKind: "mysql",
		},
		{
			name: "multiple peer tags - service takes priority",
			entity: &traces.APMEntity{
				ID:   "entity-multi-peer",
				Type: "apm-entity",
				Attributes: traces.APMEntityAttributes{
					IDTags: map[string]string{
						"service":                    "my-service",
						"peer.db.system":             "postgres",
						"peer.rpc.service":           "rpc-service",
						"peer.hostname":              "host.com",
						"peer.messaging.destination": "topic",
					},
				},
			},
			wantName: "my-service",
			wantKind: "Service",
		},
		{
			name: "empty service tag - returns empty service",
			entity: &traces.APMEntity{
				ID:   "entity-empty-service",
				Type: "apm-entity",
				Attributes: traces.APMEntityAttributes{
					IDTags: map[string]string{
						"service":        "",
						"peer.db.system": "redis",
					},
				},
			},
			wantName: "", // Service tag takes priority even if empty
			wantKind: "Service",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotName, gotKind := source.extractEntityNameAndKind(tt.entity)

			if gotName != tt.wantName {
				t.Errorf("extractEntityNameAndKind() name = %v, want %v", gotName, tt.wantName)
			}

			if gotKind != tt.wantKind {
				t.Errorf("extractEntityNameAndKind() kind = %v, want %v", gotKind, tt.wantKind)
			}
		})
	}
}
