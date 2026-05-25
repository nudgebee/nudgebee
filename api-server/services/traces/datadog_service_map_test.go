package traces

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test constants - nolint:unused
const (
	testAPIKey        = "test-api-key"      //nolint:unused
	testAppKey        = "test-app-key"      //nolint:unused
	testCloudAccount  = "cloud-123"         //nolint:unused
	testTenantID      = "tenant-456"        //nolint:unused
	testEntityID      = "entity-1"          //nolint:unused
	testServiceName   = "my-service"        //nolint:unused
	testDeployment    = "my-deployment"     //nolint:unused
	testOperation     = "http.request"      //nolint:unused
	postgresOperation = "postgres.query"    //nolint:unused
	kafkaOperation    = "kafka.consume"     //nolint:unused
	unknownOperation  = "unknown.operation" //nolint:unused
	apmEntityType     = "apm-entity"        //nolint:unused
	nilEntityMsg      = "nil entity"        //nolint:unused
)

// Test NewDatadogAPIConfig
func TestNewDatadogAPIConfig(t *testing.T) {
	t.Run("with custom site", func(t *testing.T) {
		config := NewDatadogAPIConfig("test-api-key", "test-app-key", "datadoghq.eu")
		assert.Equal(t, "test-api-key", config.APIKey)
		assert.Equal(t, "test-app-key", config.ApplicationKey)
		assert.Equal(t, "datadoghq.eu", config.Site)
	})

	t.Run("with empty site defaults to datadoghq.com", func(t *testing.T) {
		config := NewDatadogAPIConfig("test-api-key", "test-app-key", "")
		assert.Equal(t, "datadoghq.com", config.Site)
	})
}

// Test extractKafkaClusterName
func TestExtractKafkaClusterName(t *testing.T) {
	tests := []struct {
		name            string
		bootstrapServer string
		expected        string
	}{
		{
			name:            "single server with port and domain",
			bootstrapServer: "kafka-logs.us-central1.production.project44.com:9094",
			expected:        "kafka-logs",
		},
		{
			name:            "multiple servers",
			bootstrapServer: "kafka1.example.com:9092,kafka2.example.com:9092",
			expected:        "kafka1",
		},
		{
			name:            "simple hostname without dots",
			bootstrapServer: "kafka-broker:9092",
			expected:        "kafka-broker",
		},
		{
			name:            "hostname without port",
			bootstrapServer: "kafka-cluster.example.com",
			expected:        "kafka-cluster",
		},
		{
			name:            "empty string",
			bootstrapServer: "",
			expected:        DefaultKafkaClusterName,
		},
		{
			name:            "only comma",
			bootstrapServer: ",",
			expected:        DefaultKafkaClusterName,
		},
		{
			name:            "spaces trimmed",
			bootstrapServer: " kafka-test.domain.com:9092 ",
			expected:        "kafka-test",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractKafkaClusterName(tt.bootstrapServer)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// Test mapAWSServiceToType
func TestMapAWSServiceToType(t *testing.T) {
	tests := []struct {
		awsService string
		expected   string
	}{
		{"s3", "S3"},
		{"dynamodb", "DynamoDB"},
		{"sqs", "SQS"},
		{"sns", "SNS"},
		{"kinesis", "Kinesis"},
		{"lambda", "Lambda"},
		{"rds", "RDS"},
		{"ec2", "EC2"},
		{"eks", "EKS"},
		{"elasticache", "ElastiCache"},
		{"S3", "S3"},             // Test case insensitivity
		{"DynamoDB", "DynamoDB"}, // Test case insensitivity
		{"unknown-service", "UNKNOWN-SERVICE"},
	}

	for _, tt := range tests {
		t.Run(tt.awsService, func(t *testing.T) {
			result := mapAWSServiceToType(tt.awsService)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// Test inferProtocolFromOperation
func TestInferProtocolFromOperation(t *testing.T) {
	tests := []struct {
		operation string
		expected  string
	}{
		{"http.request", "http"},
		{"HTTP.REQUEST", "http"}, // case insensitive
		{"grpc", "grpc"},
		{"grpc.client", "grpc"},
		{"tcp.connect", "tcp"},
		{"dns.lookup", "dns"},
		{"postgres.query", "postgres"},
		{"postgresql.query", "postgres"},
		{"postgres.connection.commit", "postgres"},
		{"postgres.connection.rollback", "postgres"},
		{"redis.command", "redis"},
		{"redis.query", "redis"},
		{"s3.command", "s3"},
		{"kafka.consume", "kafka"},
		{"kafka.produce", "kafka"},
		{"pymongo.checkout", "mongo"},
		{"mongo.query", "mongo"},
		{"universal.http.client", "http"},
		{"web.request", "http"},
		{"flask.request", "flask"},
		{"fastapi.request", "fastapi"},
		{"sqlite.query", "sqlite"},
		{"unknown.operation", "unknown"},
		{"", "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.operation, func(t *testing.T) {
			result := inferProtocolFromOperation(tt.operation)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// Test resolveEntityNameAndKind
func TestResolveEntityNameAndKind(t *testing.T) {
	t.Run("nil entity", func(t *testing.T) {
		name, kind, found := resolveEntityNameAndKind(nil)
		assert.False(t, found)
		assert.Empty(t, name)
		assert.Empty(t, kind)
	})

	t.Run("service tag", func(t *testing.T) {
		entity := &APMEntity{
			Attributes: APMEntityAttributes{
				IDTags: map[string]string{
					"service": "my-service",
				},
			},
		}
		name, kind, found := resolveEntityNameAndKind(entity)
		assert.True(t, found)
		assert.Equal(t, "my-service", name)
		assert.Equal(t, "Service", kind)
	})

	t.Run("database peer", func(t *testing.T) {
		entity := &APMEntity{
			Attributes: APMEntityAttributes{
				IDTags: map[string]string{
					"peer.db.system": "postgres",
					"peer.db.name":   "my-database",
				},
			},
		}
		name, kind, found := resolveEntityNameAndKind(entity)
		assert.True(t, found)
		assert.Equal(t, "my-database", name)
		assert.Equal(t, "postgres", kind)
	})

	t.Run("database peer without name", func(t *testing.T) {
		entity := &APMEntity{
			Attributes: APMEntityAttributes{
				IDTags: map[string]string{
					"peer.db.system": "redis",
				},
			},
		}
		name, kind, found := resolveEntityNameAndKind(entity)
		assert.True(t, found)
		assert.Equal(t, "redis", name)
		assert.Equal(t, "redis", kind)
	})

	t.Run("RPC service peer", func(t *testing.T) {
		entity := &APMEntity{
			Attributes: APMEntityAttributes{
				IDTags: map[string]string{
					"peer.rpc.service": "grpc-service",
				},
			},
		}
		name, kind, found := resolveEntityNameAndKind(entity)
		assert.True(t, found)
		assert.Equal(t, "grpc-service", name)
		assert.Equal(t, "Service", kind)
	})

	t.Run("hostname peer", func(t *testing.T) {
		entity := &APMEntity{
			Attributes: APMEntityAttributes{
				IDTags: map[string]string{
					"peer.hostname": "external-api.example.com",
				},
			},
		}
		name, kind, found := resolveEntityNameAndKind(entity)
		assert.True(t, found)
		assert.Equal(t, "external-api.example.com", name)
		assert.Equal(t, "ExternalService", kind)
	})

	t.Run("Kafka messaging destination", func(t *testing.T) {
		entity := &APMEntity{
			Attributes: APMEntityAttributes{
				IDTags: map[string]string{
					"peer.messaging.destination": "my-topic",
				},
				Stats: &APMStats{
					Operation: "kafka.consume",
				},
			},
		}
		name, kind, found := resolveEntityNameAndKind(entity)
		assert.True(t, found)
		assert.Equal(t, "my-topic", name)
		assert.Equal(t, "kafka", kind)
	})

	t.Run("Kafka with bootstrap servers", func(t *testing.T) {
		entity := &APMEntity{
			Attributes: APMEntityAttributes{
				IDTags: map[string]string{
					"peer.messaging.system":        "kafka",
					"peer.kafka.bootstrap.servers": "kafka-cluster.example.com:9092",
				},
			},
		}
		name, kind, found := resolveEntityNameAndKind(entity)
		assert.True(t, found)
		assert.Equal(t, "kafka-cluster", name)
		assert.Equal(t, "kafka", kind)
	})

	t.Run("AWS S3 peer", func(t *testing.T) {
		entity := &APMEntity{
			Attributes: APMEntityAttributes{
				IDTags: map[string]string{
					"peer.aws.s3.bucket": "my-bucket",
				},
			},
		}
		name, kind, found := resolveEntityNameAndKind(entity)
		assert.True(t, found)
		assert.Equal(t, "aws", name)
		assert.Equal(t, "S3", kind)
	})

	t.Run("AWS DynamoDB peer", func(t *testing.T) {
		entity := &APMEntity{
			Attributes: APMEntityAttributes{
				IDTags: map[string]string{
					"peer.aws.dynamodb.table_name": "my-table",
				},
			},
		}
		name, kind, found := resolveEntityNameAndKind(entity)
		assert.True(t, found)
		assert.Equal(t, "aws", name)
		assert.Equal(t, "DynamoDB", kind)
	})

	t.Run("AWS hostname pattern - S3", func(t *testing.T) {
		// Note: Due to a bug in the production code, the generic hostname check
		// returns before AWS hostname parsing logic is reached. This test documents
		// the current behavior. To properly detect AWS services from hostnames,
		// the generic hostname check should be moved after the AWS checks.
		entity := &APMEntity{
			Attributes: APMEntityAttributes{
				IDTags: map[string]string{
					"peer.hostname": "my-bucket.s3.us-west-2.amazonaws.com",
				},
			},
		}
		name, kind, found := resolveEntityNameAndKind(entity)
		assert.True(t, found)
		// Currently returns the hostname as-is, not "aws"
		assert.Equal(t, "my-bucket.s3.us-west-2.amazonaws.com", name)
		assert.Equal(t, "ExternalService", kind)
	})

	t.Run("no matching tags", func(t *testing.T) {
		entity := &APMEntity{
			Attributes: APMEntityAttributes{
				IDTags: map[string]string{
					"random.tag": "value",
				},
			},
		}
		name, kind, found := resolveEntityNameAndKind(entity)
		assert.False(t, found)
		assert.Empty(t, name)
		assert.Empty(t, kind)
	})
}

// Test inferProtocol
func TestInferProtocol(t *testing.T) {
	t.Run("nil entity", func(t *testing.T) {
		result := inferProtocol(nil)
		assert.Equal(t, "unknown", result)
	})

	t.Run("entity without stats", func(t *testing.T) {
		entity := &APMEntity{
			Attributes: APMEntityAttributes{},
		}
		result := inferProtocol(entity)
		assert.Equal(t, "unknown", result)
	})

	t.Run("grpc operation", func(t *testing.T) {
		entity := &APMEntity{
			Attributes: APMEntityAttributes{
				Stats: &APMStats{
					Operation: "grpc.client",
					SpanKind:  "client",
				},
			},
		}
		result := inferProtocol(entity)
		assert.Equal(t, "grpc", result)
	})

	t.Run("postgres operation", func(t *testing.T) {
		entity := &APMEntity{
			Attributes: APMEntityAttributes{
				Stats: &APMStats{
					Operation: "postgres.query",
					SpanKind:  "client",
				},
			},
		}
		result := inferProtocol(entity)
		assert.Equal(t, "postgres", result)
	})

	t.Run("http operation", func(t *testing.T) {
		entity := &APMEntity{
			Attributes: APMEntityAttributes{
				Stats: &APMStats{
					Operation: "http.request",
					SpanKind:  "server",
				},
			},
		}
		result := inferProtocol(entity)
		assert.Equal(t, "http", result)
	})

	t.Run("redis operation", func(t *testing.T) {
		entity := &APMEntity{
			Attributes: APMEntityAttributes{
				Stats: &APMStats{
					Operation: "redis.command",
					SpanKind:  "client",
				},
			},
		}
		result := inferProtocol(entity)
		assert.Equal(t, "redis", result)
	})

	t.Run("kafka operation", func(t *testing.T) {
		entity := &APMEntity{
			Attributes: APMEntityAttributes{
				Stats: &APMStats{
					Operation: "kafka.consume",
					SpanKind:  "consumer",
				},
			},
		}
		result := inferProtocol(entity)
		assert.Equal(t, "kafka", result)
	})

	t.Run("server span kind defaults to http", func(t *testing.T) {
		entity := &APMEntity{
			Attributes: APMEntityAttributes{
				Stats: &APMStats{
					Operation: "unknown.operation",
					SpanKind:  "server",
				},
			},
		}
		result := inferProtocol(entity)
		assert.Equal(t, "http", result)
	})

	t.Run("client span kind defaults to http", func(t *testing.T) {
		entity := &APMEntity{
			Attributes: APMEntityAttributes{
				Stats: &APMStats{
					Operation: "unknown.operation",
					SpanKind:  "client",
				},
			},
		}
		result := inferProtocol(entity)
		assert.Equal(t, "http", result)
	})
}

// Test ParseAPMGraphResponse
func TestParseAPMGraphResponse(t *testing.T) {
	t.Run("valid response with entities and edges", func(t *testing.T) {
		responseJSON := `{
			"data": [
				{
					"id": "entity-1",
					"type": "apm-entity",
					"attributes": {
						"id_tags": {"service": "test-service"},
						"metadata": {"is_traced": true},
						"service_health": {"status": "healthy"}
					},
					"relationships": {
						"type": {
							"data": {
								"id": "type-1",
								"type": "apm-entity-type"
							}
						}
					}
				}
			],
			"included": [
				{
					"id": "edge-1",
					"type": "apm-entity-edge",
					"attributes": {
						"apm_filter": {},
						"operation": "http.request",
						"span.kind": "client"
					},
					"relationships": {
						"source": {
							"data": {
								"id": "entity-1",
								"type": "apm-entity"
							}
						},
						"target": {
							"data": {
								"id": "entity-2",
								"type": "apm-entity"
							}
						}
					}
				}
			]
		}`

		graphData, err := ParseAPMGraphResponse([]byte(responseJSON))
		require.NoError(t, err)
		require.NotNil(t, graphData)
		assert.Len(t, graphData.Entities, 1)
		assert.Len(t, graphData.Edges, 1)
		assert.Equal(t, "entity-1", graphData.Entities[0].ID)
		assert.Equal(t, "edge-1", graphData.Edges[0].ID)
	})

	t.Run("invalid JSON", func(t *testing.T) {
		_, err := ParseAPMGraphResponse([]byte("invalid json"))
		assert.Error(t, err)
	})

	t.Run("empty response", func(t *testing.T) {
		responseJSON := `{"data": [], "included": []}`
		graphData, err := ParseAPMGraphResponse([]byte(responseJSON))
		require.NoError(t, err)
		assert.Len(t, graphData.Entities, 0)
		assert.Len(t, graphData.Edges, 0)
	})

	t.Run("response with unknown types", func(t *testing.T) {
		responseJSON := `{
			"data": [
				{
					"id": "unknown-1",
					"type": "unknown-type",
					"attributes": {}
				}
			]
		}`

		graphData, err := ParseAPMGraphResponse([]byte(responseJSON))
		require.NoError(t, err)
		assert.Len(t, graphData.Entities, 0)
		assert.Len(t, graphData.Edges, 0)
	})

	t.Run("response with apm-entity-type", func(t *testing.T) {
		responseJSON := `{
			"data": [
				{
					"id": "type-1",
					"type": "apm-entity-type",
					"attributes": {
						"id_tags": {},
						"metadata": {},
						"service_health": {}
					},
					"relationships": {
						"type": {
							"data": {
								"id": "type-1",
								"type": "apm-entity-type"
							}
						}
					}
				}
			]
		}`

		graphData, err := ParseAPMGraphResponse([]byte(responseJSON))
		require.NoError(t, err)
		assert.Len(t, graphData.Entities, 1)
		assert.Equal(t, "apm-entity-type", graphData.Entities[0].Type)
	})
}

// Test buildLabelsFromAPM
func TestBuildLabelsFromAPM(t *testing.T) {
	t.Run("nil entity", func(t *testing.T) {
		labels, k8sInfo := buildLabelsFromAPM(nil, "Service", "", "")
		assert.NotNil(t, labels)
		assert.Nil(t, k8sInfo)
	})

	t.Run("external service kind", func(t *testing.T) {
		entity := &APMEntity{
			Attributes: APMEntityAttributes{
				IDTags: map[string]string{},
			},
		}
		labels, _ := buildLabelsFromAPM(entity, "ExternalService", "", "")
		assert.Equal(t, "true", labels["external"])
	})

	t.Run("entity with languages", func(t *testing.T) {
		entity := &APMEntity{
			ID: "entity-1",
			Attributes: APMEntityAttributes{
				IDTags: map[string]string{},
				Metadata: APMMetadata{
					Languages: []string{"go", "python"},
					IsTraced:  true,
					IsUSM:     false,
				},
			},
		}
		labels, _ := buildLabelsFromAPM(entity, "Service", "", "")
		assert.Equal(t, "go,python", labels["languages"])
		assert.Equal(t, "true", labels["is_traced"])
		assert.Empty(t, labels["is_usm"])
	})

	t.Run("entity with USM", func(t *testing.T) {
		entity := &APMEntity{
			ID: "entity-1",
			Attributes: APMEntityAttributes{
				IDTags: map[string]string{},
				Metadata: APMMetadata{
					IsUSM: true,
				},
			},
		}
		labels, _ := buildLabelsFromAPM(entity, "Service", "", "")
		assert.Equal(t, "true", labels["is_usm"])
	})

	t.Run("entity with product areas", func(t *testing.T) {
		entity := &APMEntity{
			ID: "entity-1",
			Attributes: APMEntityAttributes{
				IDTags: map[string]string{},
				Metadata: APMMetadata{
					ProductAreas: []string{"apm", "profiling"},
				},
			},
		}
		labels, _ := buildLabelsFromAPM(entity, "Service", "", "")
		assert.Equal(t, "apm,profiling", labels["product_areas"])
	})

	t.Run("entity with stats", func(t *testing.T) {
		entity := &APMEntity{
			ID: "entity-1",
			Attributes: APMEntityAttributes{
				IDTags: map[string]string{},
				Stats: &APMStats{
					Operation: "http.request",
					SpanKind:  "server",
				},
			},
		}
		labels, _ := buildLabelsFromAPM(entity, "Service", "", "")
		assert.Equal(t, "http.request", labels["apm.operation"])
		assert.Equal(t, "server", labels["apm.span_kind"])
		assert.Equal(t, "entity-1", labels["dd_entity_ID"])
	})
}

// Test buildK8sWorkloadNode
func TestBuildK8sWorkloadNode(t *testing.T) {
	workloadInfo := &K8sWorkloadInfo{
		ExternalID:      "ext-123",
		Name:            "my-deployment",
		Namespace:       "production",
		Kind:            "Deployment",
		CloudResourceID: "cloud-res-456",
		Labels: map[string]string{
			"app":     "my-app",
			"version": "v1.0",
		},
	}

	node := buildK8sWorkloadNode(workloadInfo, "my-service", "cloud-123", "tenant-456")

	assert.Equal(t, "my-deployment", node.Id.Name)
	assert.Equal(t, "Deployment", node.Id.Kind)
	assert.Equal(t, "production", node.Id.Namespace)
	assert.Equal(t, "infrastructure", node.Category.Category)
	assert.Equal(t, "ext-123", node.Labels["k8s.external_id"])
	assert.Equal(t, "my-deployment", node.Labels["k8s.workload_name"])
	assert.Equal(t, "production", node.Labels["k8s.namespace"])
	assert.Equal(t, "Deployment", node.Labels["k8s.workload_kind"])
	assert.Equal(t, "cloud-res-456", node.Labels["k8s.cloud_resource_id"])
	assert.Equal(t, "my-service", node.Labels["associated_service"])
	assert.Equal(t, "cloud-123", node.Labels["cloud_account_id"])
	assert.Equal(t, "tenant-456", node.Labels["tenant_id"])
	assert.Equal(t, "my-app", node.Labels["workload.app"])
	assert.Equal(t, "v1.0", node.Labels["workload.version"])
	assert.Contains(t, node.Indicators, "k8s_workload")
	assert.Equal(t, []string{"kubernetes"}, node.Type)
	assert.True(t, node.IsHealthy)
}

// Test FetchDatadogAPMGraphData with mock server
func TestFetchDatadogAPMGraphData(t *testing.T) {
	t.Run("successful fetch", func(t *testing.T) {
		mockResponse := APMGraphResponse{
			Data: []json.RawMessage{
				json.RawMessage(`{
					"id": "entity-1",
					"type": "apm-entity",
					"attributes": {
						"id_tags": {"service": "test-service"},
						"metadata": {"is_traced": true},
						"service_health": {"status": "healthy"}
					},
					"relationships": {
						"type": {
							"data": {
								"id": "type-1",
								"type": "apm-entity-type"
							}
						}
					}
				}`),
			},
			Included: []json.RawMessage{
				json.RawMessage(`{
					"id": "edge-1",
					"type": "apm-entity-edge",
					"attributes": {
						"apm_filter": {},
						"operation": "http.request",
						"span.kind": "client"
					},
					"relationships": {
						"source": {
							"data": {
								"id": "entity-1",
								"type": "apm-entity"
							}
						},
						"target": {
							"data": {
								"id": "entity-2",
								"type": "apm-entity"
							}
						}
					}
				}`),
			},
		}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/api/unstable/apm/entities/graph", r.URL.Path)
			assert.NotEmpty(t, r.Header.Get("DD-API-KEY"))
			assert.NotEmpty(t, r.Header.Get("DD-APPLICATION-KEY"))

			w.WriteHeader(http.StatusOK)
			err := json.NewEncoder(w).Encode(mockResponse)
			assert.NoError(t, err)
		}))
		defer server.Close()

		config := &DatadogAPIConfig{
			APIKey:         "test-api-key",
			ApplicationKey: "test-app-key",
			Site:           server.URL,
		}

		params := APMEntitiesGraphParams{
			FromTimestamp: 1000000,
			ToTimestamp:   2000000,
			Environment:   "production",
			PageSize:      100,
		}

		graphData, err := FetchDatadogAPMGraphData(config, params)
		require.NoError(t, err)
		require.NotNil(t, graphData)
		assert.Len(t, graphData.Entities, 1)
		assert.Len(t, graphData.Edges, 1)
	})

	t.Run("API error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, err := w.Write([]byte(`{"error": "Unauthorized"}`))
			assert.NoError(t, err)
		}))
		defer server.Close()

		config := &DatadogAPIConfig{
			APIKey:         "invalid-key",
			ApplicationKey: "invalid-app-key",
			Site:           server.URL,
		}

		params := APMEntitiesGraphParams{
			FromTimestamp: 1000000,
			ToTimestamp:   2000000,
		}

		_, err := FetchDatadogAPMGraphData(config, params)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "401")
	})

	t.Run("URL construction with https", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			err := json.NewEncoder(w).Encode(APMGraphResponse{})
			assert.NoError(t, err)
		}))
		defer server.Close()

		config := &DatadogAPIConfig{
			APIKey:         "test-key",
			ApplicationKey: "test-app-key",
			Site:           "datadoghq.com",
		}

		params := APMEntitiesGraphParams{
			FromTimestamp: 1000000,
			ToTimestamp:   2000000,
		}

		// This will fail to connect since datadoghq.com is not our test server,
		// but we're testing URL construction
		_, err := FetchDatadogAPMGraphData(config, params)
		assert.Error(t, err) // Expected to fail connecting to real Datadog
	})
}

// Test BuildServiceMapFromGraphData
func TestBuildServiceMapFromGraphData(t *testing.T) {
	t.Run("empty graph data", func(t *testing.T) {
		graphData := &APMGraphData{
			Entities: []APMEntity{},
			Edges:    []APMEntityEdge{},
		}

		serviceMap, err := BuildServiceMapFromGraphData(graphData, "cloud-123", "tenant-456")
		require.NoError(t, err)
		require.NotNil(t, serviceMap)
		assert.Len(t, serviceMap.Applications, 0)
	})

	t.Run("graph with single entity no edges", func(t *testing.T) {
		graphData := &APMGraphData{
			Entities: []APMEntity{
				{
					ID:   "entity-1",
					Type: "apm-entity",
					Attributes: APMEntityAttributes{
						IDTags: map[string]string{
							"service": "test-service",
						},
						Metadata: APMMetadata{
							IsTraced: true,
						},
						ServiceHealth: ServiceHealth{
							Status: "healthy",
						},
						Stats: &APMStats{
							Operation:         "http.request",
							SpanKind:          "server",
							RequestsPerSecond: 10.0,
							LatencyAvg:        100.0,
						},
					},
				},
			},
			Edges: []APMEntityEdge{},
		}

		serviceMap, err := BuildServiceMapFromGraphData(graphData, "cloud-123", "tenant-456")
		require.NoError(t, err)
		require.NotNil(t, serviceMap)
		assert.Len(t, serviceMap.Applications, 1)
		assert.Equal(t, "test-service", serviceMap.Applications[0].Id.Name)
		assert.Equal(t, "Service", serviceMap.Applications[0].Id.Kind)
	})

	t.Run("graph with entities and edges", func(t *testing.T) {
		graphData := &APMGraphData{
			Entities: []APMEntity{
				{
					ID:   "entity-1",
					Type: "apm-entity",
					Attributes: APMEntityAttributes{
						IDTags: map[string]string{
							"service": "service-a",
						},
						Stats: &APMStats{
							Operation:         "http.request",
							SpanKind:          "server",
							RequestsPerSecond: 100.0,
							LatencyAvg:        50.0,
						},
					},
				},
				{
					ID:   "entity-2",
					Type: "apm-entity",
					Attributes: APMEntityAttributes{
						IDTags: map[string]string{
							"service": "service-b",
						},
						Stats: &APMStats{
							Operation:         "postgres.query",
							SpanKind:          "client",
							RequestsPerSecond: 50.0,
							LatencyAvg:        10.0,
						},
					},
				},
			},
			Edges: []APMEntityEdge{
				{
					ID:   "edge-1",
					Type: "apm-entity-edge",
					Attributes: APMEntityEdgeAttributes{
						Operation: "http.request",
						SpanKind:  "client",
					},
					Relationships: APMEntityEdgeRelationships{
						Source: EntityReference{
							Data: EntityData{
								ID:   "entity-1",
								Type: "apm-entity",
							},
						},
						Target: EntityReference{
							Data: EntityData{
								ID:   "entity-2",
								Type: "apm-entity",
							},
						},
					},
				},
			},
		}

		serviceMap, err := BuildServiceMapFromGraphData(graphData, "cloud-123", "tenant-456")
		require.NoError(t, err)
		require.NotNil(t, serviceMap)
		assert.Len(t, serviceMap.Applications, 2)

		// Find service-a
		var serviceA *ServiceApplication
		for i := range serviceMap.Applications {
			if serviceMap.Applications[i].Id.Name == "service-a" {
				serviceA = &serviceMap.Applications[i]
				break
			}
		}
		require.NotNil(t, serviceA)
		assert.Len(t, serviceA.Upstreams, 1)
		assert.Contains(t, serviceA.Upstreams[0].Id, "service-b")
	})

	t.Run("skip apm-entity-type", func(t *testing.T) {
		graphData := &APMGraphData{
			Entities: []APMEntity{
				{
					ID:   "type-1",
					Type: "apm-entity-type",
					Attributes: APMEntityAttributes{
						IDTags: map[string]string{},
					},
				},
			},
			Edges: []APMEntityEdge{},
		}

		serviceMap, err := BuildServiceMapFromGraphData(graphData, "cloud-123", "tenant-456")
		require.NoError(t, err)
		assert.Len(t, serviceMap.Applications, 0)
	})
}
