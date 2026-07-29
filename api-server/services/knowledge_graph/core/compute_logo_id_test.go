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

// TestComputeLogoID_Workload covers K8s Workload logo resolution: recognized kinds get their
// own icon, and anything else — an uncommon kind like ReplicaSet, a missing kind, or a stray
// service_name/type property that has no business on a k8s node — falls back to the generic
// Deployment icon so a Workload node is never left without a logo.
func TestComputeLogoID_Workload(t *testing.T) {
	tests := []struct {
		name       string
		properties map[string]interface{}
		want       string
	}{
		{"Deployment kind", map[string]interface{}{"kind": "Deployment"}, "deployment"},
		{"StatefulSet kind", map[string]interface{}{"kind": "StatefulSet"}, "statefulset"},
		{"DaemonSet kind", map[string]interface{}{"kind": "DaemonSet"}, "daemonset"},
		{"Job kind", map[string]interface{}{"kind": "Job"}, "job"},
		{"CronJob kind", map[string]interface{}{"kind": "CronJob"}, "cronjob"},
		{"unrecognized kind falls back to deployment", map[string]interface{}{"kind": "ReplicaSet"}, "deployment"},
		{"missing kind falls back to deployment", map[string]interface{}{}, "deployment"},
		{
			// A Workload's own kind must win over a service_name/type that has no legitimate
			// reason to be set on a k8s node (e.g. leaked from service-map/eBPF matching) —
			// this is the "wrong logo" (external-service globe) failure mode.
			name:       "kind wins over a stray service_name",
			properties: map[string]interface{}{"kind": "Deployment", "service_name": "http", "type": "http"},
			want:       "deployment",
		},
		{
			// The in-cluster datastore facet (db_classifier.go) still wins over kind — a
			// Workload running Redis should show the Redis icon, not the generic Deployment one.
			name:       "engine still wins over kind",
			properties: map[string]interface{}{"kind": "StatefulSet", "engine": "redis"},
			want:       "redis",
		},
		{
			// A known runtime language (set by trace/eBPF enrichment on a matched k8s node)
			// still wins over the generic kind icon, matching this function's pre-existing
			// engine > language > kind priority for Workload nodes.
			name:       "language wins over kind",
			properties: map[string]interface{}{"kind": "Deployment", "language": "python"},
			want:       "python",
		},
		{
			// Normalized language keys (see languageLogoID) still apply for Workload nodes.
			name:       "language normalization still applies (typescript -> nodejs)",
			properties: map[string]interface{}{"kind": "Deployment", "language": "typescript"},
			want:       "nodejs",
		},
		{
			// Regression: GetPrimaryLanguage (traces/ebpf enrichment) falls back to returning
			// a raw, unclassified app.Type tag verbatim when it isn't a recognized language —
			// e.g. "http" (a protocol tag) or "Service" (a kind tag) — and that value lands in
			// properties["language"] on the matched k8s Workload node. "http" happens to collide
			// with LangTypeIcon's unrelated externalservice/http icon case, so trusting it
			// verbatim renders the wrong (external-service globe) icon on a real Deployment.
			// A non-language value must be ignored and fall through to kind instead.
			name:       "non-language value in the language property falls back to kind, not passed through",
			properties: map[string]interface{}{"kind": "Deployment", "language": "http"},
			want:       "deployment",
		},
		{
			name:       "another non-language value (Service) falls back to kind",
			properties: map[string]interface{}{"kind": "Deployment", "language": "Service"},
			want:       "deployment",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ComputeLogoID(NodeTypeWorkload, "", "k8s", tt.properties); got != tt.want {
				t.Errorf("ComputeLogoID() = %q, want %q", got, tt.want)
			}
		})
	}
}
