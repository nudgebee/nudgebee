# Knowledge Graph Service - Testing Documentation

This document describes the test suite for the Knowledge Graph service.

## Test Structure

```
api-server/services/knowledge_graph/
├── core/
│   ├── types_test.go       ✅ Core types and constants tests
│   ├── helpers_test.go     ✅ Helper functions tests
│   └── service_test.go     ✅ Main service logic tests
├── api/
│   └── handlers_test.go    ✅ HTTP API handlers tests
└── TESTING.md              📄 This file
```

## Running Tests

### Run All Tests

```bash
cd api-server/services/knowledge_graph
go test ./... -v
```

### Run Specific Package Tests

```bash
# Core package tests
go test ./core -v

# API handlers tests
go test ./api -v
```

### Run Specific Test

```bash
# Run a specific test function
go test ./core -run TestGenerateNodeID -v

# Run tests matching a pattern
go test ./core -run "TestNode.*" -v
```

### Run Tests with Coverage

```bash
# Generate coverage report
go test ./... -cover

# Generate detailed coverage report
go test ./... -coverprofile=coverage.out
go tool cover -html=coverage.out -o coverage.html
```

## Test Categories

### 1. Core Types Tests (`core/types_test.go`)

Tests for basic types and constants:

- **NodeType Constants**: Verify all node type constants (Service, Database, Cache, etc.)
- **RelationshipType Constants**: Verify all relationship type constants (CALLS, RUNS_ON, etc.)
- **Node Creation**: Test node struct creation and properties
- **Edge Creation**: Test edge struct creation and relationships
- **Graph Creation**: Test graph struct with nodes and edges
- **BuildRequest Validation**: Test build request structure
- **Metadata**: Test metadata calculation and node type breakdown
- **TimeRange**: Test time range creation and validation

**Example:**
```bash
go test ./core -run TestNodeType_Constants -v
```

### 2. Helper Functions Tests (`core/helpers_test.go`)

Tests for utility functions:

- **ID Generation**:
  - `TestGenerateNodeID`: Test deterministic node ID generation
  - `TestGenerateEdgeID`: Test deterministic edge ID generation
  - `TestGenerateUniqueKey`: Test unique key generation for different node types

- **Node/Edge Creation**:
  - `TestNewNode`: Test node creation with all fields
  - `TestNewEdge`: Test edge creation with relationships

- **Property Management**:
  - `TestMergeProperties`: Test property merging logic
  - `TestGetNodeProperty`: Test property retrieval
  - `TestSetNodeProperty`: Test property setting

- **Deduplication**:
  - `TestDeduplicateNodes`: Test node deduplication by unique key
  - `TestDeduplicateEdges`: Test edge deduplication by ID

- **Validation**:
  - `TestValidateNode`: Test node validation (required fields)
  - `TestValidateEdge`: Test edge validation (required fields)

**Example:**
```bash
go test ./core -run TestDeduplicateNodes -v
```

### 3. Service Tests (`core/service_test.go`)

Tests for main service logic:

- **Service Creation**:
  - `TestNewService`: Test service initialization

- **Source Registration**:
  - `TestService_RegisterSource`: Test source registration
  - Test duplicate registration handling
  - Test nil source rejection

- **Graph Building**:
  - `TestService_BuildGraphs`: Test building from multiple sources
  - `TestService_BuildGraphs_DisabledSource`: Test disabled source handling
  - Test source filtering

- **Graph Querying**:
  - `TestService_GetGraph`: Test graph querying by source
  - `TestService_GetGraph_WithNodeTypeFilter`: Test node type filtering
  - Test error handling for missing sources

- **Source Management**:
  - `TestService_ListSources`: Test source listing
  - `TestService_calculateMetadata`: Test metadata calculation
  - `TestService_filterGraphByNodeType`: Test graph filtering

**Mock Source** is provided for testing without real data sources.

**Example:**
```bash
go test ./core -run TestService_BuildGraphs -v
```

### 4. API Handler Tests (`api/handlers_test.go`)

Tests for HTTP API endpoints:

- **Build Graphs Endpoint**:
  - `TestHandler_BuildGraphs_Success`: Test successful graph building
  - `TestHandler_BuildGraphs_MissingTenantID`: Test validation
  - `TestHandler_BuildGraphs_InvalidJSON`: Test error handling

- **Query Graph Endpoint**:
  - `TestHandler_QueryGraph_MissingSource`: Test validation
  - `TestHandler_QueryGraph_MissingTenantID`: Test validation

- **Source Listing**:
  - `TestHandler_ListSources`: Test source listing endpoint

- **Node Operations**:
  - `TestHandler_GetNode_NotImplemented`: Test unimplemented endpoint

- **Graph Deletion**:
  - `TestHandler_DeleteGraph_MissingTenantID`: Test validation
  - `TestHandler_DeleteGraph_MissingSource`: Test validation

- **Health Check**:
  - `TestHandler_Health`: Test health endpoint

- **Route Registration**:
  - `TestHandler_RegisterRoutes`: Verify all routes are registered
  - `TestNewHandler`: Test handler initialization

**Example:**
```bash
go test ./api -run TestHandler_ListSources -v
```

## Test Coverage Goals

Target coverage by package:

- **core/types.go**: 90%+
- **core/helpers.go**: 95%+
- **core/service.go**: 85%+
- **api/handlers.go**: 80%+

Check current coverage:
```bash
go test ./... -cover
```

## Writing New Tests

### Test Naming Convention

```go
func Test<PackageName>_<FunctionName>_<Scenario>(t *testing.T) {
    // Example: TestService_BuildGraphs_DisabledSource
}
```

### Table-Driven Tests

Use table-driven tests for multiple scenarios:

```go
func TestGenerateUniqueKey(t *testing.T) {
    tests := []struct {
        name       string
        nodeType   NodeType
        properties map[string]interface{}
        want       string
    }{
        {
            name:     "Service with namespace",
            nodeType: NodeTypeService,
            properties: map[string]interface{}{
                "name":      "test-service",
                "namespace": "default",
            },
            want: "service:default:test-service",
        },
        // More test cases...
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            got := GenerateUniqueKey(tt.nodeType, tt.properties)
            if got != tt.want {
                t.Errorf("got %v, want %v", got, tt.want)
            }
        })
    }
}
```

### Mock Objects

Use mock implementations for testing:

```go
// MockSource for testing service without real data sources
type MockSource struct {
    name     string
    enabled  bool
    graph    *Graph
    buildErr error
}

func (m *MockSource) GetName() string {
    return m.name
}

func (m *MockSource) BuildGraph(ctx context.Context, req *SourceBuildRequest) (*Graph, error) {
    if m.buildErr != nil {
        return nil, m.buildErr
    }
    return m.graph, nil
}
```

## Integration Tests

For integration testing with real sources:

1. **Trace Source**: Test with real OpenTelemetry traces
2. **Datadog Source**: Test with Datadog API (requires credentials)
3. **AWS Source**: Test with cloud_resourses table (requires database)
4. **K8s Source**: Test with k8s_workloads table (requires database)

```bash
# Run integration tests (requires setup)
go test ./sources -tags=integration -v
```

## CI/CD Integration

### GitHub Actions Example

```yaml
name: Knowledge Graph Tests

on: [push, pull_request]

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v2

      - name: Set up Go
        uses: actions/setup-go@v2
        with:
          go-version: 1.21

      - name: Run tests
        run: |
          cd api-server/services/knowledge_graph
          go test ./... -v -cover
```

## Test Data

### Sample Test Data

```go
// Sample node
node := &Node{
    ID:        "test-node-id",
    NodeType:  NodeTypeService,
    UniqueKey: "service:test-service",
    Properties: map[string]interface{}{
        "name": "test-service",
        "port": 8080,
    },
    TenantID:  "tenant-1",
    Source:    "test",
}

// Sample edge
edge := &Edge{
    ID:                "test-edge-id",
    SourceNodeID:      "node-1",
    DestinationNodeID: "node-2",
    RelationshipType:  RelationshipCalls,
    Properties: map[string]interface{}{
        "protocol": "HTTP",
    },
    TenantID: "tenant-1",
    Source:   "test",
}

// Sample graph
graph := &Graph{
    Nodes:       []*Node{node},
    Edges:       []*Edge{edge},
    Source:      "test",
    TenantID:    "tenant-1",
    GeneratedAt: time.Now(),
}
```

## Debugging Tests

### Enable Verbose Output

```bash
go test ./... -v
```

### Run Single Test with Details

```bash
go test ./core -run TestDeduplicateNodes -v
```

### Debug with Delve

```bash
dlv test ./core -- -test.run TestGenerateNodeID
```

## Common Issues

### Issue: Tests fail with "no such file"
**Solution**: Ensure you're running tests from the correct directory

### Issue: Mock sources not working
**Solution**: Verify mock implements all interface methods

### Issue: Flaky time-based tests
**Solution**: Use fixed time.Time values instead of time.Now()

## Performance Benchmarks

Add benchmarks for performance-critical functions:

```go
func BenchmarkGenerateNodeID(b *testing.B) {
    for i := 0; i < b.N; i++ {
        GenerateNodeID("service:test-service")
    }
}

func BenchmarkDeduplicateNodes(b *testing.B) {
    nodes := generateTestNodes(1000)
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        DeduplicateNodes(nodes)
    }
}
```

Run benchmarks:
```bash
go test ./core -bench=. -benchmem
```

## Next Steps

1. Add integration tests for each source
2. Add performance benchmarks
3. Increase test coverage to 90%+
4. Add end-to-end API tests
5. Add load testing for graph building

---

**Last Updated**: Based on current test implementation
**Test Count**: 40+ unit tests across 4 test files
