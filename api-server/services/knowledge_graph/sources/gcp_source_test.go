package sources

import (
	"log/slog"
	"nudgebee/services/knowledge_graph/core"
	"os"
	"testing"
)

func TestGCPSourceGenerateUniqueKey(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	source, err := NewGCPSource(GCPSourceConfig{}, logger)
	if err != nil {
		t.Fatalf("Failed to create GCPSource: %v", err)
	}

	tests := []struct {
		name    string
		node    *core.DbNode
		wantKey string
	}{
		{
			name: "ComputeInstance node",
			node: &core.DbNode{
				NodeType: core.NodeTypeComputeInstance,
				Properties: map[string]interface{}{
					"name":   "web-server-1",
					"region": "us-central1",
				},
				CloudAccountID: "acc-1",
			},
			wantKey: "gcp:acc-1:us-central1:ComputeInstance:web-server-1:web-server-1",
		},
		{
			name: "Database node with project_id",
			node: &core.DbNode{
				NodeType: core.NodeTypeDatabase,
				Properties: map[string]interface{}{
					"name":           "my-sql-instance",
					"region":         "us-central1",
					"gcp_project_id": "my-project",
				},
				CloudAccountID: "acc-1",
			},
			wantKey: "gcp:acc-1:us-central1:Database:my-sql-instance:my-sql-instance",
		},
		{
			name: "VPC node",
			node: &core.DbNode{
				NodeType: core.NodeTypeVPC,
				Properties: map[string]interface{}{
					"name":   "default",
					"region": "us-central1",
				},
				CloudAccountID: "acc-1",
			},
			wantKey: "gcp:acc-1:us-central1:VPC:default:default",
		},
		{
			name:    "Nil node returns empty",
			node:    nil,
			wantKey: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := source.GenerateUniqueKey(tt.node)
			if key != tt.wantKey {
				t.Errorf("GenerateUniqueKey() = %q, want %q", key, tt.wantKey)
			}

			// Deterministic
			if tt.node != nil {
				key2 := source.GenerateUniqueKey(tt.node)
				if key != key2 {
					t.Errorf("Not deterministic: %q != %q", key, key2)
				}
			}
		})
	}
}

func TestGCPSourceDetermineNodeType(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	source, err := NewGCPSource(GCPSourceConfig{}, logger)
	if err != nil {
		t.Fatalf("Failed to create GCPSource: %v", err)
	}

	tests := []struct {
		name        string
		resType     string
		serviceName string
		want        core.NodeType
	}{
		{"compute-engine", "compute-engine", "Compute Engine", core.NodeTypeComputeInstance},
		{"compute asset", "compute.googleapis.com/Instance", "Compute Engine", core.NodeTypeComputeInstance},
		{"cloud-sql", "cloud-sql", "Cloud SQL", core.NodeTypeDatabase},
		{"sqladmin asset", "sqladmin.googleapis.com/Instance/POSTGRES_17", "Cloud SQL", core.NodeTypeDatabase},
		{"kubernetes-engine", "kubernetes-engine", "Kubernetes Engine", core.NodeTypeManagedCluster},
		{"gke asset", "container.googleapis.com/Cluster", "Kubernetes Engine", core.NodeTypeManagedCluster},
		{"bigquery", "bigquery", "BigQuery", core.NodeTypeDatabase},
		{"bq dataset", "bigquery.googleapis.com/Dataset", "BigQuery", core.NodeTypeDatabase},
		{"bq table", "bigquery.googleapis.com/Table", "BigQuery", core.NodeTypeDatabase},
		{"storage bucket", "storage.googleapis.com/Bucket", "Cloud Storage", core.NodeTypeStorage},
		{"networking", "networking", "Networking", core.NodeTypeVPC},
		{"subnet", "subnet", "Networking", core.NodeTypeSubnet},
		{"cloud-logging", "cloud-logging", "Cloud Logging", core.NodeTypeLogAggregator},
		{"cloud-monitoring", "cloud-monitoring", "Cloud Monitoring", core.NodeTypeMonitoringService},
		{"vertex-ai", "vertex-ai", "Vertex AI", core.NodeTypeAIService},
		{"gemini-api", "gemini-api", "Gemini API", core.NodeTypeAIService},
		{"unknown type fallback to service", "unknown-type", "Compute Engine", core.NodeTypeComputeInstance},
		{"completely unknown", "unknown-type", "Unknown Service", core.NodeTypeCloudResource},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := source.determineNodeType(tt.resType, tt.serviceName)
			if got != tt.want {
				t.Errorf("determineNodeType(%q, %q) = %q, want %q", tt.resType, tt.serviceName, got, tt.want)
			}
		})
	}
}

func TestExtractGCPShortName(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"full path", "my-project/zones/us-central1-a/instances/web-server-1", "web-server-1"},
		{"simple name", "web-server-1", "web-server-1"},
		{"two parts", "project/instance-name", "instance-name"},
		{"empty", "", ""},
		{"trailing slash", "project/", "project"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractGCPShortName(tt.input)
			if got != tt.want {
				t.Errorf("extractGCPShortName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestExtractGCPProjectID(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"full path", "my-project/zones/us-central1-a/instances/foo", "my-project"},
		{"simple name", "simple-name", "simple-name"},
		{"two parts", "project/instance", "project"},
		{"empty", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractGCPProjectID(tt.input)
			if got != tt.want {
				t.Errorf("extractGCPProjectID(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestExtractGCPResourceNameFromURL(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			"full URL",
			"https://www.googleapis.com/compute/v1/projects/my-project/global/networks/default",
			"default",
		},
		{
			"path only",
			"projects/my-project/regions/us-central1/subnetworks/default-subnet",
			"default-subnet",
		},
		{"simple name", "default", "default"},
		{"empty", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractGCPResourceNameFromURL(tt.input)
			if got != tt.want {
				t.Errorf("extractGCPResourceNameFromURL(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestGCPSourceGetName(t *testing.T) {
	source, err := NewGCPSource(GCPSourceConfig{}, nil)
	if err != nil {
		t.Fatalf("Failed to create GCPSource: %v", err)
	}

	if got := source.GetName(); got != "gcp" {
		t.Errorf("GetName() = %q, want 'gcp'", got)
	}
}

func TestGCPSourceIsEnabled(t *testing.T) {
	source, err := NewGCPSource(GCPSourceConfig{}, nil)
	if err != nil {
		t.Fatalf("Failed to create GCPSource: %v", err)
	}

	if !source.IsEnabled() {
		t.Error("IsEnabled() = false, want true")
	}
}

func TestGCPSourceValidate(t *testing.T) {
	source, err := NewGCPSource(GCPSourceConfig{}, nil)
	if err != nil {
		t.Fatalf("Failed to create GCPSource: %v", err)
	}

	if err := source.Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil", err)
	}
}

func TestGCPSourceShouldIncludeResource(t *testing.T) {
	source, err := NewGCPSource(GCPSourceConfig{
		ServiceTypeFilter: GCPDefaultServiceTypeFilter,
	}, nil)
	if err != nil {
		t.Fatalf("Failed to create GCPSource: %v", err)
	}

	tests := []struct {
		name     string
		resource CloudResourceRow
		want     bool
	}{
		{
			"allowed compute-engine type",
			CloudResourceRow{Type: "compute-engine", ServiceName: "Compute Engine"},
			true,
		},
		{
			"allowed asset inventory compute type",
			CloudResourceRow{Type: "compute.googleapis.com/Instance", ServiceName: "Compute Engine"},
			true,
		},
		{
			"disallowed type for filtered service",
			CloudResourceRow{Type: "some-unknown-type", ServiceName: "Compute Engine"},
			false,
		},
		{
			"unknown service passes through",
			CloudResourceRow{Type: "custom-thing", ServiceName: "Custom Service"},
			true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := source.shouldIncludeResource(&tt.resource)
			if got != tt.want {
				t.Errorf("shouldIncludeResource(%s/%s) = %v, want %v",
					tt.resource.Type, tt.resource.ServiceName, got, tt.want)
			}
		})
	}
}

func TestParseGCloudCLIResponse(t *testing.T) {
	tests := []struct {
		name    string
		resp    map[string]any
		wantErr bool
		wantVal string
	}{
		{
			"data field string",
			map[string]any{"data": `[{"name":"test"}]`},
			false,
			`[{"name":"test"}]`,
		},
		{
			"output field",
			map[string]any{"output": `[{"name":"test2"}]`},
			false,
			`[{"name":"test2"}]`,
		},
		{
			"result field",
			map[string]any{"result": `[{"name":"test3"}]`},
			false,
			`[{"name":"test3"}]`,
		},
		{
			"empty response",
			map[string]any{},
			true,
			"",
		},
		{
			"nil response",
			nil,
			true,
			"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseGCloudCLIResponse(tt.resp)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseGCloudCLIResponse() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.wantVal {
				t.Errorf("parseGCloudCLIResponse() = %q, want %q", got, tt.wantVal)
			}
		})
	}
}
