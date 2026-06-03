package core

import (
	"context"
	"errors"
	"log/slog"
	"nudgebee/services/internal/testenv"
	"nudgebee/services/security"
	"testing"
	"time"
)

// Helper function to create a test request context
func newTestRequestContext() *security.RequestContext {
	logger := slog.Default()
	secCtx := security.NewSecurityContextForSuperAdmin()
	return security.NewRequestContext(context.Background(), secCtx, logger, nil, nil)
}

// MockSource is a mock implementation of SourceInterface for testing
type MockSource struct {
	name        string
	enabled     bool
	graph       *Graph
	buildErr    error
	validateErr error
}

func (m *MockSource) GetName() string {
	return m.name
}

func (m *MockSource) BuildGraph(ctx *security.RequestContext, req *SourceBuildRequest) (*Graph, error) {
	if m.buildErr != nil {
		return nil, m.buildErr
	}
	return m.graph, nil
}

func (m *MockSource) IsEnabled() bool {
	return m.enabled
}

func (m *MockSource) Validate() error {
	return m.validateErr
}

func TestNewService(t *testing.T) {
	ctx := newTestRequestContext()
	logger := slog.Default()
	service := NewService(ctx, logger, nil)

	if service == nil {
		t.Fatal("NewService() returned nil")
		return
	}

	if service.sources == nil {
		t.Error("NewService() sources map is nil")
	}

	if service.logger == nil {
		t.Error("NewService() logger is nil")
	}
}

func TestService_RegisterSource(t *testing.T) {
	ctx := newTestRequestContext()
	service := NewService(ctx, nil, nil)

	tests := []struct {
		name        string
		source      SourceInterface
		wantErr     bool
		errContains string
	}{
		{
			name: "valid source",
			source: &MockSource{
				name:    "test",
				enabled: true,
			},
			wantErr: false,
		},
		{
			name:        "nil source",
			source:      nil,
			wantErr:     true,
			errContains: "source cannot be nil",
		},
		{
			name: "source with empty name",
			source: &MockSource{
				name:    "",
				enabled: true,
			},
			wantErr:     true,
			errContains: "source name cannot be empty",
		},
		{
			name: "source validation failure",
			source: &MockSource{
				name:        "test-invalid",
				enabled:     true,
				validateErr: errors.New("validation failed"),
			},
			wantErr:     true,
			errContains: "source validation failed",
		},
		{
			name: "overwrite existing source",
			source: &MockSource{
				name:    "test",
				enabled: false,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := service.RegisterSource(tt.source)
			if (err != nil) != tt.wantErr {
				t.Errorf("RegisterSource() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && tt.errContains != "" {
				if !contains(err.Error(), tt.errContains) {
					t.Errorf("RegisterSource() error = %v, want error containing %v", err, tt.errContains)
				}
			}
		})
	}
}

// Helper function to check if string contains substring
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(substr); i++ {
				if s[i:i+len(substr)] == substr {
					return true
				}
			}
			return false
		}())
}

func TestService_BuildGraphs(t *testing.T) {
	testenv.RequireMetastore(t)
	ctx := newTestRequestContext()
	service := NewService(ctx, nil, nil)

	// Register mock sources
	mockGraph1 := &Graph{
		Nodes: []*DbNode{
			{ID: "node-1", NodeType: NodeTypeService, UniqueKey: "service:test1", Source: "source1"},
		},
		Edges: []*DbEdge{},
	}

	mockGraph2 := &Graph{
		Nodes: []*DbNode{
			{ID: "node-2", NodeType: NodeTypeDatabase, UniqueKey: "database:postgres", Source: "source2"},
		},
		Edges: []*DbEdge{},
	}

	source1 := &MockSource{
		name:    "source1",
		enabled: true,
		graph:   mockGraph1,
	}

	source2 := &MockSource{
		name:    "source2",
		enabled: true,
		graph:   mockGraph2,
	}

	_ = service.RegisterSource(source1)
	_ = service.RegisterSource(source2)

	tests := []struct {
		name    string
		request *BuildRequest
		wantErr bool
	}{
		{
			name: "Build from all sources",
			request: &BuildRequest{
				TenantID: "tenant-1",
				Sources:  []string{}, // Empty means all sources
			},
			wantErr: false,
		},
		{
			name: "Build from specific source",
			request: &BuildRequest{
				TenantID: "tenant-1",
				Sources:  []string{"source1"},
			},
			wantErr: false,
		},
		{
			name: "Build from non-existent source",
			request: &BuildRequest{
				TenantID: "tenant-1",
				Sources:  []string{"non-existent"},
			},
			wantErr: false, // Should succeed but with no graphs
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reqCtx := newTestRequestContext()
			response, err := service.BuildGraphs(reqCtx, tt.request)

			if (err != nil) != tt.wantErr {
				t.Errorf("BuildGraphs() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			// Verify response is returned
			if err == nil && response == nil {
				t.Errorf("BuildGraphs() returned nil response without error")
				return
			}

			// Verify response contains expected data
			if err == nil && response != nil {
				if response.KnowledgeGraph.Nodes == nil {
					t.Errorf("BuildGraphs() response.KnowledgeGraph.Nodes is nil")
				}
			}
		})
	}
}

func TestService_BuildGraphs_DisabledSource(t *testing.T) {
	testenv.RequireMetastore(t)
	ctx := newTestRequestContext()
	service := NewService(ctx, nil, nil)

	// Register disabled source
	disabledSource := &MockSource{
		name:    "disabled",
		enabled: false,
		graph:   &Graph{Nodes: []*DbNode{}, Edges: []*DbEdge{}},
	}

	_ = service.RegisterSource(disabledSource)

	req := &BuildRequest{
		TenantID: "tenant-1",
		Sources:  []string{"disabled"},
	}

	reqCtx := newTestRequestContext()
	response, err := service.BuildGraphs(reqCtx, req)

	if err != nil {
		t.Errorf("BuildGraphs() error = %v, want nil", err)
	}

	// Verify response is returned even when source is disabled
	if response == nil {
		t.Errorf("BuildGraphs() returned nil response")
	}

	// Response should succeed but not build from disabled source
	if response != nil && !response.Success {
		t.Logf("BuildGraphs() expected to succeed with disabled source, got Success=false")
	}
}

func TestService_GetGraph(t *testing.T) {
	ctx := newTestRequestContext()
	service := NewService(ctx, nil, nil)

	mockGraph := &Graph{
		Nodes: []*DbNode{
			{ID: "node-1", NodeType: NodeTypeService, UniqueKey: "service:test"},
		},
		Edges: []*DbEdge{},
	}

	mockSource := &MockSource{
		name:    "test",
		enabled: true,
		graph:   mockGraph,
	}

	_ = service.RegisterSource(mockSource)

	tests := []struct {
		name    string
		request *QueryRequest
		wantErr bool
	}{
		{
			name: "Valid query",
			request: &QueryRequest{
				TenantID:       "tenant-1",
				CloudAccountID: "account-1",
				Source:         "test",
			},
			wantErr: false,
		},
		{
			name: "Missing source",
			request: &QueryRequest{
				TenantID:       "tenant-1",
				CloudAccountID: "account-1",
				Source:         "",
			},
			wantErr: true,
		},
		{
			name: "Non-existent source",
			request: &QueryRequest{
				TenantID:       "tenant-1",
				CloudAccountID: "account-1",
				Source:         "non-existent",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reqCtx := newTestRequestContext()
			response, err := service.GetGraph(reqCtx, tt.request)

			if (err != nil) != tt.wantErr {
				t.Errorf("GetGraph() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if err == nil {
				if response == nil {
					t.Error("GetGraph() returned nil response")
					return
				}

				if response.Graph == nil {
					t.Error("GetGraph() returned nil graph")
				}
			}
		})
	}
}

func TestService_GetGraph_WithNodeTypeFilter(t *testing.T) {
	ctx := newTestRequestContext()
	service := NewService(ctx, nil, nil)

	mockGraph := &Graph{
		Nodes: []*DbNode{
			{ID: "node-1", NodeType: NodeTypeService, UniqueKey: "service:test1"},
			{ID: "node-2", NodeType: NodeTypeDatabase, UniqueKey: "database:postgres"},
			{ID: "node-3", NodeType: NodeTypeService, UniqueKey: "service:test2"},
		},
		Edges: []*DbEdge{
			{ID: "edge-1", SourceNodeID: "node-1", DestinationNodeID: "node-2", RelationshipType: RelationshipCalls},
		},
	}

	mockSource := &MockSource{
		name:    "test",
		enabled: true,
		graph:   mockGraph,
	}

	_ = service.RegisterSource(mockSource)

	req := &QueryRequest{
		TenantID:       "tenant-1",
		CloudAccountID: "account-1",
		Source:         "test",
		NodeType:       NodeTypeService,
	}

	reqCtx := newTestRequestContext()
	response, err := service.GetGraph(reqCtx, req)

	if err != nil {
		t.Errorf("GetGraph() error = %v, want nil", err)
	}

	if response.Graph == nil {
		t.Fatal("GetGraph() returned nil graph")
		return
	}

	// Should only have Service nodes
	if len(response.Graph.Nodes) != 2 {
		t.Errorf("GetGraph() nodes count = %v, want %v (filtered by Service type)", len(response.Graph.Nodes), 2)
	}

	for _, node := range response.Graph.Nodes {
		if node.NodeType != NodeTypeService {
			t.Errorf("GetGraph() node type = %v, want %v", node.NodeType, NodeTypeService)
		}
	}
}

func TestService_ListSources(t *testing.T) {
	ctx := newTestRequestContext()
	service := NewService(ctx, nil, nil)

	source1 := &MockSource{name: "source1", enabled: true}
	source2 := &MockSource{name: "source2", enabled: false}

	_ = service.RegisterSource(source1)
	_ = service.RegisterSource(source2)

	sources := service.ListSources()

	if len(sources) != 2 {
		t.Errorf("ListSources() count = %v, want %v", len(sources), 2)
	}

	// Check that sources are listed correctly
	sourceMap := make(map[string]bool)
	for _, s := range sources {
		sourceMap[s.Name] = s.Enabled
	}

	if !sourceMap["source1"] {
		t.Error("ListSources() source1 should be enabled")
	}

	if sourceMap["source2"] {
		t.Error("ListSources() source2 should be disabled")
	}
}

func TestService_calculateMetadata(t *testing.T) {
	ctx := newTestRequestContext()
	service := NewService(ctx, nil, nil)

	graph := &Graph{
		Nodes: []*DbNode{
			{NodeType: NodeTypeService},
			{NodeType: NodeTypeService},
			{NodeType: NodeTypeDatabase},
		},
		Edges: []*DbEdge{
			{},
			{},
		},
		Source: "test",
	}

	metadata := service.calculateMetadata(graph)

	if metadata.NodeCount != 3 {
		t.Errorf("calculateMetadata() NodeCount = %v, want %v", metadata.NodeCount, 3)
	}

	if metadata.EdgeCount != 2 {
		t.Errorf("calculateMetadata() EdgeCount = %v, want %v", metadata.EdgeCount, 2)
	}

	if metadata.NodeTypeBreakdown[NodeTypeService] != 2 {
		t.Errorf("calculateMetadata() Service count = %v, want %v", metadata.NodeTypeBreakdown[NodeTypeService], 2)
	}

	if metadata.NodeTypeBreakdown[NodeTypeDatabase] != 1 {
		t.Errorf("calculateMetadata() Database count = %v, want %v", metadata.NodeTypeBreakdown[NodeTypeDatabase], 1)
	}
}

func TestService_filterGraphByNodeType(t *testing.T) {
	ctx := newTestRequestContext()
	service := NewService(ctx, nil, nil)

	graph := &Graph{
		Nodes: []*DbNode{
			{ID: "node-1", NodeType: NodeTypeService},
			{ID: "node-2", NodeType: NodeTypeDatabase},
			{ID: "node-3", NodeType: NodeTypeService},
		},
		Edges: []*DbEdge{
			{SourceNodeID: "node-1", DestinationNodeID: "node-2"}, // Service -> Database
			{SourceNodeID: "node-1", DestinationNodeID: "node-3"}, // Service -> Service
		},
	}

	filtered := service.filterGraphByNodeType(graph, NodeTypeService)

	if len(filtered.Nodes) != 2 {
		t.Errorf("filterGraphByNodeType() nodes count = %v, want %v", len(filtered.Nodes), 2)
	}

	// Should only include edge between two Service nodes
	if len(filtered.Edges) != 1 {
		t.Errorf("filterGraphByNodeType() edges count = %v, want %v", len(filtered.Edges), 1)
	}
}

func TestService_SetDefaultRelationshipsPath(t *testing.T) {
	ctx := newTestRequestContext()
	service := NewService(ctx, nil, nil)

	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{
			name:    "invalid path",
			path:    "/non/existent/path.json",
			wantErr: true,
		},
		{
			name:    "empty path",
			path:    "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := service.SetDefaultRelationshipsPath(tt.path)
			if (err != nil) != tt.wantErr {
				t.Errorf("SetDefaultRelationshipsPath() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestService_GetDefaultRelationships(t *testing.T) {
	ctx := newTestRequestContext()
	service := NewService(ctx, nil, nil)

	// Test with no relationships loaded
	relationships := service.GetDefaultRelationships()
	if relationships == nil {
		t.Error("GetDefaultRelationships() returned nil, want empty slice")
	}
	if len(relationships) != 0 {
		t.Errorf("GetDefaultRelationships() count = %v, want 0", len(relationships))
	}
}

func TestService_getSourceCategory(t *testing.T) {
	ctx := newTestRequestContext()
	service := NewService(ctx, nil, nil)

	tests := []struct {
		name       string
		sourceName string
		want       SourceCategory
	}{
		{
			name:       "aws is account",
			sourceName: "aws",
			want:       SourceCategoryAccount,
		},
		{
			name:       "k8s is account",
			sourceName: "k8s",
			want:       SourceCategoryAccount,
		},
		{
			name:       "unknown is account by default",
			sourceName: "unknown",
			want:       SourceCategoryAccount,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := service.getSourceCategory(tt.sourceName)
			if got != tt.want {
				t.Errorf("getSourceCategory() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestService_getApplicableSourcesForAccount(t *testing.T) {
	ctx := newTestRequestContext()
	service := NewService(ctx, nil, nil)

	tests := []struct {
		name             string
		cloudProvider    string
		requestedSources []string
		wantAccount      []string
		wantIntegration  []string
	}{
		{
			name:             "K8s account with no filter",
			cloudProvider:    "K8s",
			requestedSources: []string{},
			wantAccount:      []string{"k8s"},
			wantIntegration:  []string{},
		},
		{
			name:             "AWS account with no filter",
			cloudProvider:    "AWS",
			requestedSources: []string{},
			wantAccount:      []string{"aws"},
			wantIntegration:  []string{},
		},
		{
			name:             "Azure account with no filter",
			cloudProvider:    "Azure",
			requestedSources: []string{},
			wantAccount:      []string{},
			wantIntegration:  []string{},
		},
		{
			name:             "K8s with specific source filter",
			cloudProvider:    "K8s",
			requestedSources: []string{"k8s"},
			wantAccount:      []string{"k8s"},
			wantIntegration:  []string{},
		},
		{
			name:             "Unknown provider defaults to empty",
			cloudProvider:    "UnknownProvider",
			requestedSources: []string{},
			wantAccount:      []string{},
			wantIntegration:  []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			accountSources, integrationSources := service.getApplicableSourcesForAccount(tt.cloudProvider, tt.requestedSources)

			if len(accountSources) != len(tt.wantAccount) {
				t.Errorf("getApplicableSourcesForAccount() account sources count = %v, want %v", len(accountSources), len(tt.wantAccount))
			}

			if len(integrationSources) != len(tt.wantIntegration) {
				t.Errorf("getApplicableSourcesForAccount() integration sources count = %v, want %v", len(integrationSources), len(tt.wantIntegration))
			}

			// Verify account sources
			for i, source := range accountSources {
				if i < len(tt.wantAccount) && source != tt.wantAccount[i] {
					t.Errorf("getApplicableSourcesForAccount() account source[%d] = %v, want %v", i, source, tt.wantAccount[i])
				}
			}

			// Verify integration sources
			for i, source := range integrationSources {
				if i < len(tt.wantIntegration) && source != tt.wantIntegration[i] {
					t.Errorf("getApplicableSourcesForAccount() integration source[%d] = %v, want %v", i, source, tt.wantIntegration[i])
				}
			}
		})
	}
}

func TestService_groupAccountsByIntegration(t *testing.T) {
	ctx := newTestRequestContext()
	service := NewService(ctx, nil, nil)
	goCtx := context.Background()

	tests := []struct {
		name         string
		sourceName   string
		accountIDs   []string
		tenantID     string
		wantGroups   int
		wantFirstKey string
	}{
		{
			name:       "unknown source creates separate groups",
			sourceName: "unknown",
			accountIDs: []string{"account-1", "account-2"},
			tenantID:   "tenant-1",
			wantGroups: 2,
		},
		{
			name:       "empty account list",
			sourceName: "unknown",
			accountIDs: []string{},
			tenantID:   "tenant-1",
			wantGroups: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			groups := service.groupAccountsByIntegration(goCtx, tt.sourceName, tt.accountIDs, tt.tenantID)

			if len(groups) != tt.wantGroups {
				t.Errorf("groupAccountsByIntegration() groups count = %v, want %v", len(groups), tt.wantGroups)
			}

			if tt.wantFirstKey != "" {
				if _, exists := groups[tt.wantFirstKey]; !exists {
					t.Errorf("groupAccountsByIntegration() expected key %v not found", tt.wantFirstKey)
				}
			}
		})
	}
}

func TestService_NewServiceWithDefaultSources(t *testing.T) {
	t.Skip("NewServiceWithDefaultSources function has been removed/refactored")
	// logger := slog.Default()
	// service, err := NewServiceWithDefaultSources(logger, nil, "tenant-1", "account-1")

	// if err != nil {
	// 	t.Errorf("NewServiceWithDefaultSources() error = %v, want nil", err)
	// }

	// if service == nil {
	// 	t.Fatal("NewServiceWithDefaultSources() returned nil service")
	// }

	// if service.sources == nil {
	// 	t.Error("NewServiceWithDefaultSources() sources map is nil")
	// }

	// if service.logger == nil {
	// 	t.Error("NewServiceWithDefaultSources() logger is nil")
	// }
}

func TestService_BuildGraphs_ErrorHandling(t *testing.T) {
	ctx := newTestRequestContext()
	service := NewService(ctx, nil, nil)

	// Register source that returns error
	errorSource := &MockSource{
		name:     "error-source",
		enabled:  true,
		buildErr: errors.New("build failed"),
	}

	_ = service.RegisterSource(errorSource)

	// This test will fail because BuildGraphs requires database access
	// which we don't have in unit tests. This is a limitation that shows
	// the need for better dependency injection or interface-based database access.
	t.Skip("Skipping BuildGraphs test - requires database mock")
}

func TestService_SaveNodes_NilDbManager(t *testing.T) {
	ctx := newTestRequestContext()
	service := NewService(ctx, nil, nil)

	nodes := []*DbNode{
		{ID: "node-1", UniqueKey: "service:test:prod", CloudAccountID: "account-1", TenantID: "tenant-1"},
	}

	// Should fail because dbManager is nil
	err := service.SaveNodes(nodes, 0)
	if err == nil {
		t.Error("SaveNodes() with nil dbManager should return error")
	}
}

func TestService_SaveEdges_NilDbManager(t *testing.T) {
	ctx := newTestRequestContext()
	service := NewService(ctx, nil, nil)

	edges := []*DbEdge{
		{
			ID:                "edge-1",
			SourceNodeID:      "node-1",
			DestinationNodeID: "node-2",
			RelationshipType:  RelationshipCalls,
			CloudAccountID:    "account-1",
			TenantID:          "tenant-1",
		},
	}

	// Should fail because dbManager is nil
	err := service.SaveEdges(edges, nil, 1)
	if err == nil {
		t.Error("SaveEdges() with nil dbManager should return error")
	}
}

func TestService_GetNodesByTenant_NilDbManager(t *testing.T) {
	ctx := newTestRequestContext()
	service := NewService(ctx, nil, nil)

	_, err := service.GetNodesByTenant("tenant-1")
	if err == nil {
		t.Error("GetNodesByTenant() with nil dbManager should return error")
	}
}

func TestService_GetEdgesByTenant_NilDbManager(t *testing.T) {
	ctx := newTestRequestContext()
	service := NewService(ctx, nil, nil)

	_, err := service.GetEdgesByTenant("tenant-1")
	if err == nil {
		t.Error("GetEdgesByTenant() with nil dbManager should return error")
	}
}

// MockFlowSource is a mock implementation of FlowSourceInterface for testing
type MockFlowSource struct {
	name              string
	enabled           bool
	category          FlowSourceCategory
	edges             []*DbEdge
	nodes             []*DbNode
	buildErr          error
	validateErr       error
	capturedTimeRange *TimeRange // Capture the time range passed to BuildFlowRelationships
}

func (m *MockFlowSource) GetName() string {
	return m.name
}

func (m *MockFlowSource) BuildFlowRelationships(ctx *security.RequestContext, req *FlowSourceBuildRequest) ([]*DbEdge, []*DbNode, error) {
	// Capture the time range for verification
	m.capturedTimeRange = req.TimeRange
	if m.buildErr != nil {
		return nil, nil, m.buildErr
	}
	return m.edges, m.nodes, nil
}

func (m *MockFlowSource) IsEnabled() bool {
	return m.enabled
}

func (m *MockFlowSource) Validate() error {
	return m.validateErr
}

func (m *MockFlowSource) GetSourceCategory() FlowSourceCategory {
	return m.category
}

func TestService_RegisterFlowSource(t *testing.T) {
	ctx := newTestRequestContext()
	service := NewService(ctx, nil, nil)

	tests := []struct {
		name        string
		flowSource  FlowSourceInterface
		wantErr     bool
		errContains string
	}{
		{
			name: "valid flow source",
			flowSource: &MockFlowSource{
				name:     "test-flow",
				enabled:  true,
				category: FlowSourceCategoryTracing,
			},
			wantErr: false,
		},
		{
			name:        "nil flow source",
			flowSource:  nil,
			wantErr:     true,
			errContains: "flow source cannot be nil",
		},
		{
			name: "flow source with empty name",
			flowSource: &MockFlowSource{
				name:     "",
				enabled:  true,
				category: FlowSourceCategoryTracing,
			},
			wantErr:     true,
			errContains: "flow source name cannot be empty",
		},
		{
			name: "flow source validation failure",
			flowSource: &MockFlowSource{
				name:        "test-invalid",
				enabled:     true,
				category:    FlowSourceCategoryTracing,
				validateErr: errors.New("validation failed"),
			},
			wantErr:     true,
			errContains: "flow source validation failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := service.RegisterFlowSource(tt.flowSource)
			if (err != nil) != tt.wantErr {
				t.Errorf("RegisterFlowSource() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && tt.errContains != "" {
				if !contains(err.Error(), tt.errContains) {
					t.Errorf("RegisterFlowSource() error = %v, want error containing %v", err, tt.errContains)
				}
			}
		})
	}
}

func TestService_ListFlowSources(t *testing.T) {
	ctx := newTestRequestContext()
	service := NewService(ctx, nil, nil)

	flowSource1 := &MockFlowSource{name: "flow-source1", enabled: true, category: FlowSourceCategoryTracing}
	flowSource2 := &MockFlowSource{name: "flow-source2", enabled: false, category: FlowSourceCategoryNetworking}

	_ = service.RegisterFlowSource(flowSource1)
	_ = service.RegisterFlowSource(flowSource2)

	flowSources := service.ListFlowSources()

	if len(flowSources) != 2 {
		t.Errorf("ListFlowSources() count = %v, want %v", len(flowSources), 2)
	}

	// Check that flow sources are listed correctly
	flowSourceMap := make(map[string]FlowSourceInfo)
	for _, fs := range flowSources {
		flowSourceMap[fs.Name] = fs
	}

	if info, exists := flowSourceMap["flow-source1"]; !exists {
		t.Error("ListFlowSources() flow-source1 not found")
	} else {
		if !info.Enabled {
			t.Error("ListFlowSources() flow-source1 should be enabled")
		}
		if info.Category != FlowSourceCategoryTracing {
			t.Errorf("ListFlowSources() flow-source1 category = %v, want %v", info.Category, FlowSourceCategoryTracing)
		}
	}

	if info, exists := flowSourceMap["flow-source2"]; !exists {
		t.Error("ListFlowSources() flow-source2 not found")
	} else {
		if info.Enabled {
			t.Error("ListFlowSources() flow-source2 should be disabled")
		}
		if info.Category != FlowSourceCategoryNetworking {
			t.Errorf("ListFlowSources() flow-source2 category = %v, want %v", info.Category, FlowSourceCategoryNetworking)
		}
	}
}

func TestService_defaultEnabledFlowSources(t *testing.T) {
	tests := []struct {
		name    string
		sources []*MockFlowSource
		want    []string
	}{
		{
			name:    "no registered flow sources",
			sources: nil,
			want:    []string{},
		},
		{
			name: "only enabled sources returned, sorted",
			sources: []*MockFlowSource{
				{name: "zeta", enabled: true, category: FlowSourceCategoryTracing},
				{name: "alpha", enabled: true, category: FlowSourceCategoryNetworking},
				{name: "mid", enabled: true, category: FlowSourceCategoryTracing},
			},
			want: []string{"alpha", "mid", "zeta"},
		},
		{
			name: "disabled sources excluded",
			sources: []*MockFlowSource{
				{name: "enabled-1", enabled: true, category: FlowSourceCategoryTracing},
				{name: "disabled-1", enabled: false, category: FlowSourceCategoryTracing},
				{name: "enabled-2", enabled: true, category: FlowSourceCategoryNetworking},
			},
			want: []string{"enabled-1", "enabled-2"},
		},
		{
			name: "all disabled returns empty",
			sources: []*MockFlowSource{
				{name: "d1", enabled: false, category: FlowSourceCategoryTracing},
				{name: "d2", enabled: false, category: FlowSourceCategoryNetworking},
			},
			want: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := newTestRequestContext()
			service := NewService(ctx, nil, nil)
			for _, src := range tt.sources {
				if err := service.RegisterFlowSource(src); err != nil {
					t.Fatalf("RegisterFlowSource(%s) returned unexpected error: %v", src.name, err)
				}
			}

			got := service.defaultEnabledFlowSources()

			if len(got) != len(tt.want) {
				t.Fatalf("defaultEnabledFlowSources() length = %d, want %d (got=%v)", len(got), len(tt.want), got)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("defaultEnabledFlowSources()[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestService_buildFlowRelationships_WithTimeRange(t *testing.T) {
	ctx := newTestRequestContext()
	service := NewService(ctx, nil, nil)

	// Create a mock flow source
	mockEdges := []*DbEdge{
		{
			ID:                "edge-1",
			SourceNodeID:      "node-1",
			DestinationNodeID: "node-2",
			RelationshipType:  RelationshipCalls,
		},
	}
	mockFlowSource := &MockFlowSource{
		name:     "test-flow",
		enabled:  true,
		category: FlowSourceCategoryTracing,
		edges:    mockEdges,
	}

	_ = service.RegisterFlowSource(mockFlowSource)

	// Create a request with a specific time range
	now := time.Now()
	customTimeRange := &TimeRange{
		StartTime: now.Add(-2 * time.Hour),
		EndTime:   now,
	}

	req := &BuildRequest{
		TenantID:    "tenant-1",
		FlowSources: []string{"test-flow"},
		TimeRange:   customTimeRange,
	}

	existingNodes := []*DbNode{
		{ID: "node-1", UniqueKey: "service:test1"},
		{ID: "node-2", UniqueKey: "service:test2"},
	}

	reqCtx := newTestRequestContext()
	edges, nodes, errors := service.buildFlowRelationships(reqCtx, req, existingNodes)

	// Check that edges were returned
	if len(edges) != 1 {
		t.Errorf("buildFlowRelationships() edges count = %v, want %v", len(edges), 1)
	}

	// Check that nodes were returned (can be empty for this test)
	if nodes == nil {
		t.Error("buildFlowRelationships() nodes should not be nil")
	}

	// Only the always-on "manual" source (not registered in this test's
	// registry) contributes a not-found error; the registered test-flow
	// emits the mocked edge cleanly.
	if len(errors) != 1 {
		t.Errorf("buildFlowRelationships() errors count = %v, want 1 (always-on manual)", len(errors))
	}
	if _, exists := errors["manual"]; !exists {
		t.Error("buildFlowRelationships() should have error for always-on 'manual' source")
	}

	// Verify that the custom time range was used
	if mockFlowSource.capturedTimeRange == nil {
		t.Fatal("buildFlowRelationships() did not pass time range to flow source")
		return
	}

	if !mockFlowSource.capturedTimeRange.StartTime.Equal(customTimeRange.StartTime) {
		t.Errorf("buildFlowRelationships() StartTime = %v, want %v",
			mockFlowSource.capturedTimeRange.StartTime, customTimeRange.StartTime)
	}

	if !mockFlowSource.capturedTimeRange.EndTime.Equal(customTimeRange.EndTime) {
		t.Errorf("buildFlowRelationships() EndTime = %v, want %v",
			mockFlowSource.capturedTimeRange.EndTime, customTimeRange.EndTime)
	}
}

func TestService_buildFlowRelationships_WithoutTimeRange_UsesDefault24Hours(t *testing.T) {
	ctx := newTestRequestContext()
	service := NewService(ctx, nil, nil)

	// Create a mock flow source
	mockEdges := []*DbEdge{
		{
			ID:                "edge-1",
			SourceNodeID:      "node-1",
			DestinationNodeID: "node-2",
			RelationshipType:  RelationshipCalls,
		},
	}
	mockFlowSource := &MockFlowSource{
		name:     "test-flow",
		enabled:  true,
		category: FlowSourceCategoryTracing,
		edges:    mockEdges,
	}

	_ = service.RegisterFlowSource(mockFlowSource)

	// Create a request WITHOUT a time range (nil)
	req := &BuildRequest{
		TenantID:    "tenant-1",
		FlowSources: []string{"test-flow"},
		TimeRange:   nil, // Explicitly nil to test default behavior
	}

	existingNodes := []*DbNode{
		{ID: "node-1", UniqueKey: "service:test1"},
		{ID: "node-2", UniqueKey: "service:test2"},
	}

	reqCtx := newTestRequestContext()
	beforeTest := time.Now()
	edges, nodes, errors := service.buildFlowRelationships(reqCtx, req, existingNodes)
	afterTest := time.Now()

	// Check that edges were returned
	if len(edges) != 1 {
		t.Errorf("buildFlowRelationships() edges count = %v, want %v", len(edges), 1)
	}

	// Check that nodes were returned (can be empty for this test)
	if nodes == nil {
		t.Error("buildFlowRelationships() nodes should not be nil")
	}

	// Only the always-on "manual" source (not registered in this test's
	// registry) contributes a not-found error; the registered test-flow
	// emits the mocked edge cleanly.
	if len(errors) != 1 {
		t.Errorf("buildFlowRelationships() errors count = %v, want 1 (always-on manual)", len(errors))
	}
	if _, exists := errors["manual"]; !exists {
		t.Error("buildFlowRelationships() should have error for always-on 'manual' source")
	}

	// Verify that a default time range was created
	if mockFlowSource.capturedTimeRange == nil {
		t.Fatal("buildFlowRelationships() did not pass time range to flow source")
		return
	}

	// Verify that the time range is approximately 24 hours
	// StartTime should be approximately 24 hours before EndTime
	actualDuration := mockFlowSource.capturedTimeRange.EndTime.Sub(mockFlowSource.capturedTimeRange.StartTime)
	expectedDuration := 24 * time.Hour

	// Allow 1 second tolerance for test execution time
	tolerance := 1 * time.Second
	if actualDuration < expectedDuration-tolerance || actualDuration > expectedDuration+tolerance {
		t.Errorf("buildFlowRelationships() time range duration = %v, want approximately %v",
			actualDuration, expectedDuration)
	}

	// Verify that EndTime is approximately now (within test execution time)
	if mockFlowSource.capturedTimeRange.EndTime.Before(beforeTest) ||
		mockFlowSource.capturedTimeRange.EndTime.After(afterTest) {
		t.Errorf("buildFlowRelationships() EndTime = %v, want between %v and %v",
			mockFlowSource.capturedTimeRange.EndTime, beforeTest, afterTest)
	}

	// Verify that StartTime is approximately 24 hours ago
	expectedStartTime := mockFlowSource.capturedTimeRange.EndTime.Add(-24 * time.Hour)
	startTimeDiff := mockFlowSource.capturedTimeRange.StartTime.Sub(expectedStartTime).Abs()
	if startTimeDiff > tolerance {
		t.Errorf("buildFlowRelationships() StartTime = %v, want approximately %v (diff: %v)",
			mockFlowSource.capturedTimeRange.StartTime, expectedStartTime, startTimeDiff)
	}
}

func TestService_buildFlowRelationships_NoFlowSources(t *testing.T) {
	ctx := newTestRequestContext()
	service := NewService(ctx, nil, nil)

	req := &BuildRequest{
		TenantID:    "tenant-1",
		FlowSources: []string{}, // No flow sources requested
	}

	reqCtx := newTestRequestContext()
	edges, nodes, errors := service.buildFlowRelationships(reqCtx, req, []*DbNode{})

	// Should return empty results
	if len(edges) != 0 {
		t.Errorf("buildFlowRelationships() edges count = %v, want 0", len(edges))
	}

	if len(nodes) != 0 {
		t.Errorf("buildFlowRelationships() nodes count = %v, want 0", len(nodes))
	}

	// Even with FlowSources=[] the always-on "manual" source is injected and,
	// being unregistered in this test, contributes a not-found error.
	if len(errors) != 1 {
		t.Errorf("buildFlowRelationships() errors count = %v, want 1 (always-on manual)", len(errors))
	}
	if _, exists := errors["manual"]; !exists {
		t.Error("buildFlowRelationships() should have error for always-on 'manual' source")
	}
}

func TestService_buildFlowRelationships_DisabledFlowSource(t *testing.T) {
	ctx := newTestRequestContext()
	service := NewService(ctx, nil, nil)

	// Create a disabled flow source
	mockFlowSource := &MockFlowSource{
		name:     "disabled-flow",
		enabled:  false, // Disabled
		category: FlowSourceCategoryTracing,
		edges:    []*DbEdge{},
	}

	_ = service.RegisterFlowSource(mockFlowSource)

	req := &BuildRequest{
		TenantID:    "tenant-1",
		FlowSources: []string{"disabled-flow"},
	}

	reqCtx := newTestRequestContext()
	edges, nodes, errors := service.buildFlowRelationships(reqCtx, req, []*DbNode{})

	// Should return empty results since flow source is disabled
	if len(edges) != 0 {
		t.Errorf("buildFlowRelationships() edges count = %v, want 0", len(edges))
	}

	if len(nodes) != 0 {
		t.Errorf("buildFlowRelationships() nodes count = %v, want 0", len(nodes))
	}

	// The disabled mock contributes no error; the always-on "manual" source
	// is injected unconditionally and not registered in this test's
	// registry, so it contributes a single not-found error.
	if len(errors) != 1 {
		t.Errorf("buildFlowRelationships() errors count = %v, want 1 (always-on manual)", len(errors))
	}
	if _, exists := errors["manual"]; !exists {
		t.Error("buildFlowRelationships() should have error for always-on 'manual' source")
	}

	// Verify that the flow source was not called
	if mockFlowSource.capturedTimeRange != nil {
		t.Error("buildFlowRelationships() should not call disabled flow source")
	}
}

func TestService_buildFlowRelationships_NonExistentFlowSource(t *testing.T) {
	ctx := newTestRequestContext()
	service := NewService(ctx, nil, nil)

	req := &BuildRequest{
		TenantID:    "tenant-1",
		FlowSources: []string{"non-existent-flow"},
	}

	reqCtx := newTestRequestContext()
	edges, nodes, errors := service.buildFlowRelationships(reqCtx, req, []*DbNode{})

	// Should return empty edges
	if len(edges) != 0 {
		t.Errorf("buildFlowRelationships() edges count = %v, want 0", len(edges))
	}

	// Should return empty nodes
	if len(nodes) != 0 {
		t.Errorf("buildFlowRelationships() nodes count = %v, want 0", len(nodes))
	}

	// Should have an error for the non-existent flow source. The always-on
	// "manual" source is also injected unconditionally and not registered in
	// this test's registry, so it contributes a second not-found error.
	if len(errors) != 2 {
		t.Errorf("buildFlowRelationships() errors count = %v, want 2 (requested + always-on manual)", len(errors))
	}

	if _, exists := errors["non-existent-flow"]; !exists {
		t.Error("buildFlowRelationships() should have error for 'non-existent-flow'")
	}
	if _, exists := errors["manual"]; !exists {
		t.Error("buildFlowRelationships() should have error for always-on 'manual' source")
	}
}

func TestService_buildFlowRelationships_FlowSourceError(t *testing.T) {
	ctx := newTestRequestContext()
	service := NewService(ctx, nil, nil)

	// Create a flow source that returns an error
	mockFlowSource := &MockFlowSource{
		name:     "error-flow",
		enabled:  true,
		category: FlowSourceCategoryTracing,
		buildErr: errors.New("flow source build failed"),
	}

	_ = service.RegisterFlowSource(mockFlowSource)

	req := &BuildRequest{
		TenantID:    "tenant-1",
		FlowSources: []string{"error-flow"},
	}

	reqCtx := newTestRequestContext()
	edges, nodes, errors := service.buildFlowRelationships(reqCtx, req, []*DbNode{})

	// Should return empty edges
	if len(edges) != 0 {
		t.Errorf("buildFlowRelationships() edges count = %v, want 0", len(edges))
	}

	// Should return empty nodes
	if len(nodes) != 0 {
		t.Errorf("buildFlowRelationships() nodes count = %v, want 0", len(nodes))
	}

	// Should have an error for the failed flow source. The always-on
	// "manual" source is also injected unconditionally and not registered in
	// this test's registry, so it contributes a second not-found error.
	if len(errors) != 2 {
		t.Errorf("buildFlowRelationships() errors count = %v, want 2 (errored + always-on manual)", len(errors))
	}

	if err, exists := errors["error-flow"]; !exists {
		t.Error("buildFlowRelationships() should have error for 'error-flow'")
	} else {
		if !contains(err.Error(), "flow source build failed") {
			t.Errorf("buildFlowRelationships() error = %v, want error containing 'flow source build failed'", err)
		}
	}
	if _, exists := errors["manual"]; !exists {
		t.Error("buildFlowRelationships() should have error for always-on 'manual' source")
	}
}

// ========================================================================
// GetNodeNeighbors Tests
// ========================================================================

func TestService_GetNodeNeighbors_NilDbManager(t *testing.T) {
	ctx := newTestRequestContext()
	service := NewService(ctx, nil, nil)

	_, err := service.GetNodeNeighbors(ctx, "node-1")
	if err == nil {
		t.Error("GetNodeNeighbors() with nil dbManager should return error")
	}
	if err != nil && !contains(err.Error(), "database manager not initialized") {
		t.Errorf("GetNodeNeighbors() error = %v, want error containing 'database manager not initialized'", err)
	}
}

func TestService_GetNodeNeighbors_EmptyNodeID(t *testing.T) {
	ctx := newTestRequestContext()
	service := NewService(ctx, nil, nil)

	_, err := service.GetNodeNeighbors(ctx, "")
	if err == nil {
		t.Error("GetNodeNeighbors() with empty node_id should return error")
	}
	if err != nil && !contains(err.Error(), "node_id is required") {
		t.Errorf("GetNodeNeighbors() error = %v, want error containing 'node_id is required'", err)
	}
}

func TestService_GetNodeNeighbors_ValidationTests(t *testing.T) {
	tests := []struct {
		name        string
		nodeID      string
		wantErr     bool
		errContains string
	}{
		{
			name:        "empty node ID",
			nodeID:      "",
			wantErr:     true,
			errContains: "node_id is required",
		},
		{
			name:        "valid node ID but no db manager",
			nodeID:      "node-123",
			wantErr:     true,
			errContains: "database manager not initialized",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := newTestRequestContext()
			service := NewService(ctx, nil, nil)

			_, err := service.GetNodeNeighbors(ctx, tt.nodeID)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetNodeNeighbors() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && tt.errContains != "" {
				if !contains(err.Error(), tt.errContains) {
					t.Errorf("GetNodeNeighbors() error = %v, want error containing %v", err, tt.errContains)
				}
			}
		})
	}
}

// Note: Full database integration tests for GetNodeNeighbors require a real database connection
// and are better suited for integration tests. The tests above validate input validation
// and error handling without requiring database access.
//
// For complete testing with database mocking, consider implementing a DatabaseManager interface
// and creating a mock implementation that can be injected into the service.

func TestSuccessfulFlowSources_Derivation(t *testing.T) {
	// Mirrors the derivation loop in BuildGraphs (Phase 2.5) that decides which
	// flow sources are eligible for the infra-authoritative sweep branch. The
	// rule: a source is "successful" iff it was requested AND it did not appear
	// in the flowErrors map returned by buildFlowRelationships.
	derive := func(requested []string, errs map[string]error) []string {
		out := make([]string, 0, len(requested))
		for _, fs := range requested {
			if _, errored := errs[fs]; !errored {
				out = append(out, fs)
			}
		}
		return out
	}

	tests := []struct {
		name      string
		requested []string
		errs      map[string]error
		want      []string
	}{
		{
			name:      "all succeeded",
			requested: []string{"ebpf", "datadog-apm", "traces"},
			errs:      map[string]error{},
			want:      []string{"ebpf", "datadog-apm", "traces"},
		},
		{
			name:      "one errored is excluded",
			requested: []string{"ebpf", "datadog-apm", "traces"},
			errs:      map[string]error{"ebpf": errors.New("boom")},
			want:      []string{"datadog-apm", "traces"},
		},
		{
			name:      "all errored returns empty",
			requested: []string{"ebpf", "datadog-apm"},
			errs: map[string]error{
				"ebpf":        errors.New("x"),
				"datadog-apm": errors.New("y"),
			},
			want: []string{},
		},
		{
			name:      "no requested sources returns empty",
			requested: []string{},
			errs:      map[string]error{"ebpf": errors.New("x")},
			want:      []string{},
		},
		{
			name:      "preserves request order",
			requested: []string{"traces", "ebpf", "datadog-apm"},
			errs:      map[string]error{"ebpf": errors.New("x")},
			want:      []string{"traces", "datadog-apm"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := derive(tt.requested, tt.errs)
			if len(got) != len(tt.want) {
				t.Fatalf("derived %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("derived[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// TestUpsertBatchSizesRespectPostgresParamLimit guards against the regression that
// caused issue #30175: a column was added to nodeUpsertCols without lowering
// NodeBatchSize, so every node upsert exceeded PostgreSQL's 65535 parameter cap
// and silently dropped the entire knowledge graph for large tenants. If you add
// a column to either upsert, lower the corresponding batch size until this test
// passes again.
func TestUpsertBatchSizesRespectPostgresParamLimit(t *testing.T) {
	const pgParamLimit = 65535

	if got := NodeBatchSize * len(nodeUpsertCols); got > pgParamLimit {
		t.Errorf("node upsert: NodeBatchSize=%d × len(nodeUpsertCols)=%d = %d params, exceeds PostgreSQL limit %d",
			NodeBatchSize, len(nodeUpsertCols), got, pgParamLimit)
	}
	if got := EdgeBatchSize * len(edgeUpsertCols); got > pgParamLimit {
		t.Errorf("edge upsert: EdgeBatchSize=%d × len(edgeUpsertCols)=%d = %d params, exceeds PostgreSQL limit %d",
			EdgeBatchSize, len(edgeUpsertCols), got, pgParamLimit)
	}
}

// TestApplyAlwaysOnFlowSources verifies that the always-on injection (a)
// appends `manual` when missing, (b) is idempotent when `manual` is already
// present, (c) preserves caller ordering, and (d) handles the nil/empty
// caller case (tenant filter empty).
func TestApplyAlwaysOnFlowSources(t *testing.T) {
	containsManual := func(s []string) bool {
		for _, n := range s {
			if n == "manual" {
				return true
			}
		}
		return false
	}

	t.Run("empty input yields the always-on set", func(t *testing.T) {
		got := applyAlwaysOnFlowSources(nil)
		if !containsManual(got) {
			t.Errorf("expected manual to be injected; got %v", got)
		}
	})

	t.Run("tenant filter without manual gets manual appended", func(t *testing.T) {
		input := []string{"traces", "ebpf", "datadog-apm", "newrelic-apm"}
		got := applyAlwaysOnFlowSources(input)
		if !containsManual(got) {
			t.Errorf("expected manual injected; got %v", got)
		}
		// Caller ordering preserved.
		for i, want := range input {
			if got[i] != want {
				t.Errorf("got[%d] = %q, want %q (input ordering not preserved)", i, got[i], want)
			}
		}
	})

	t.Run("idempotent when manual is already present", func(t *testing.T) {
		input := []string{"traces", "manual", "ebpf"}
		got := applyAlwaysOnFlowSources(input)
		count := 0
		for _, n := range got {
			if n == "manual" {
				count++
			}
		}
		if count != 1 {
			t.Errorf("expected exactly one 'manual' entry; got %d in %v", count, got)
		}
		if len(got) != len(input) {
			t.Errorf("expected no new entries; len=%d input=%d", len(got), len(input))
		}
	})

	t.Run("alwaysOnFlowSources list is non-empty", func(t *testing.T) {
		// Guard against accidentally emptying the always-on set. If/when
		// always-on flow sources expand beyond "manual", this should still
		// stay >= 1 — removing the contract entirely deserves an explicit
		// follow-up decision, not a silent regression.
		if len(alwaysOnFlowSources) == 0 {
			t.Fatal("alwaysOnFlowSources is empty — the always-on contract has been removed")
		}
	})
}
