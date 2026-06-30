package core

import "testing"

// TestComputeLogoID_Azure covers Azure resource-provider resolution: every Azure node must
// resolve to a non-empty, frontend-renderable logo id (never the raw "microsoft.*/..." string).
func TestComputeLogoID_Azure(t *testing.T) {
	tests := []struct {
		name       string
		nodeType   NodeType
		source     string
		properties map[string]interface{}
		want       string
	}{
		{
			name:       "VM",
			nodeType:   NodeTypeComputeInstance,
			source:     "azure",
			properties: map[string]interface{}{"service_name": "microsoft.compute/virtualmachines"},
			want:       "azure-vm",
		},
		{
			name:       "subnet",
			nodeType:   NodeTypeSubnet,
			source:     "azure",
			properties: map[string]interface{}{"service_name": "microsoft.network/virtualnetworks/subnets"},
			want:       "azure-subnet",
		},
		{
			// Azure must short-circuit BEFORE the generic SecurityGroup node-type override so an
			// Azure NSG gets the Azure icon, not the AWS "securitygroup" one.
			name:       "NSG short-circuits node-type override",
			nodeType:   NodeTypeSecurityGroup,
			source:     "azure",
			properties: map[string]interface{}{"service_name": "microsoft.network/networksecuritygroups"},
			want:       "azure-nsg",
		},
		{
			name:       "cosmos db",
			nodeType:   NodeTypeDatabase,
			source:     "azure",
			properties: map[string]interface{}{"service_name": "microsoft.documentdb/databaseaccounts"},
			want:       "azure-cosmos-db",
		},
		{
			name:       "reservation friendly name virtual machines",
			nodeType:   NodeTypeCloudResource,
			source:     "azure",
			properties: map[string]interface{}{"service_name": "virtual machines"},
			want:       "azure-vm",
		},
		{
			name:       "unknown azure service falls back to generic",
			nodeType:   NodeTypeCloudResource,
			source:     "azure",
			properties: map[string]interface{}{"service_name": "microsoft.somethingnew/resources"},
			want:       "azure-resource",
		},
		{
			name:       "azure node with no service_name falls back to generic",
			nodeType:   NodeTypeCloudResource,
			source:     "azure",
			properties: map[string]interface{}{},
			want:       "azure-resource",
		},
		{
			// Source casing must not matter.
			name:       "source casing is ignored",
			nodeType:   NodeTypeStorage,
			source:     "Azure",
			properties: map[string]interface{}{"service_name": "microsoft.storage/storageaccounts"},
			want:       "azure-storage",
		},
		{
			// An engine property still wins over the Azure provider type (future managed-DB nodes).
			name:       "engine wins over azure resolution",
			nodeType:   NodeTypeDatabase,
			source:     "azure",
			properties: map[string]interface{}{"engine": "postgres", "service_name": "microsoft.dbforpostgresql/flexibleservers"},
			want:       "postgres",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ComputeLogoID(tt.nodeType, tt.source, tt.properties); got != tt.want {
				t.Errorf("ComputeLogoID() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestComputeLogoID_NonAzureUnaffected guards that adding the Azure branch did not change
// resolution for other sources.
func TestComputeLogoID_NonAzureUnaffected(t *testing.T) {
	tests := []struct {
		name       string
		nodeType   NodeType
		source     string
		properties map[string]interface{}
		want       string
	}{
		{
			name:       "aws VPC",
			nodeType:   NodeTypeVPC,
			source:     "aws",
			properties: map[string]interface{}{},
			want:       "AmazonVPC",
		},
		{
			name:       "aws security group uses node-type override",
			nodeType:   NodeTypeSecurityGroup,
			source:     "aws",
			properties: map[string]interface{}{"service_name": "AmazonVPC"},
			want:       "securitygroup",
		},
		{
			name:       "k8s pod node-type fallback",
			nodeType:   NodeTypePod,
			source:     "k8s",
			properties: map[string]interface{}{},
			want:       "pod",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ComputeLogoID(tt.nodeType, tt.source, tt.properties); got != tt.want {
				t.Errorf("ComputeLogoID() = %q, want %q", got, tt.want)
			}
		})
	}
}
