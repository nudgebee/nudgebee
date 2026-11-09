package core

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Manual node builders — how a node a user declares by hand gets the same
// identity a collector would give it.
//
// A node's id is NodeIDFor(unique_key, tenant, cloud_account), and each cloud
// source derives unique_key from the node's properties in its own
// GenerateUniqueKey. A hand-built key that drifts from that recipe by one
// character means the collector later writes a second node for the same
// resource. So the cloud sources build manual nodes themselves, through the
// same code path they build inventory rows with, and register that builder
// here: package core is the only place both the RPC layer and the flow source
// can reach without importing sources/<cloud> (sources already imports
// flow_sources).

// ManualNodeField is one identity input a user supplies when declaring a node.
// Every field a source's unique key reads for the type is listed here, so the
// form can make each of them compulsory.
type ManualNodeField struct {
	// Name is the input key: one of the ManualNodeField* constants.
	Name     string `json:"name"`
	Label    string `json:"label"`
	Hint     string `json:"hint,omitempty"`
	Required bool   `json:"required"`
	// PickNodeType means the value is chosen from the account's existing nodes
	// of this type rather than typed: the VPC a resource lives in is keyed by
	// the VPC node's name, which only the graph knows.
	PickNodeType NodeType `json:"pick_node_type,omitempty"`
}

// Identity input keys.
const (
	ManualNodeFieldName      = "name"
	ManualNodeFieldRegion    = "region"
	ManualNodeFieldNetwork   = "network"
	ManualNodeFieldNamespace = "namespace"
)

// ManualNodeKind is one type a user may declare in an account of a provider.
type ManualNodeKind struct {
	SpecificType string            `json:"specific_type"`
	NodeType     NodeType          `json:"node_type"`
	KeyFields    []ManualNodeField `json:"key_fields"`
	// Properties are the optional, descriptive fields: the type's concrete
	// schema (SpecificTypeSchema), never part of the key.
	Properties []ManualNodeProperty `json:"properties"`
}

// ManualNodeProperty is one optional field a declaration may set, with the
// shape its value must have: the shape the collector stores it in.
type ManualNodeProperty struct {
	Name  string        `json:"name"`
	Shape PropertyShape `json:"shape,omitempty"`
}

// ManualNodeNetwork is the network node (VPC / VNet) a declared resource lives
// in, as read from the graph: its cloud resource id and the name the source
// keys children by.
type ManualNodeNetwork struct {
	ResourceID string
	Name       string
}

// ManualNodeInput is what the user declared, plus the account context the RPC
// layer resolves from cloud_accounts.
type ManualNodeInput struct {
	TenantID       string
	CloudAccountID string
	AccountNumber  string
	CloudProvider  string
	SpecificType   string
	Name           string
	Region         string
	Network        *ManualNodeNetwork
	// Namespace is a Kubernetes resource's namespace (part of its key).
	Namespace string
	// AccountName is the connected account's display name; for a Kubernetes
	// account it is the cluster name the collector stamps on its nodes.
	AccountName string
}

// ManualNodeBuilder builds a declared node exactly as the provider's source
// would build the same resource from inventory. Build returns the node as the
// source would emit it (source = the cloud); MaterializeManualNode turns that
// into the declared node.
type ManualNodeBuilder interface {
	Kinds() []ManualNodeKind
	Build(in ManualNodeInput) (*DbNode, error)
}

var (
	manualNodeBuildersMu sync.RWMutex
	manualNodeBuilders   = map[string]ManualNodeBuilder{}
)

// RegisterManualNodeBuilder records the builder for a cloud_accounts
// cloud_provider value ("AWS", "GCP", "Azure", "K8s"). Sources call it from init().
func RegisterManualNodeBuilder(cloudProvider string, b ManualNodeBuilder) {
	manualNodeBuildersMu.Lock()
	defer manualNodeBuildersMu.Unlock()
	if _, dup := manualNodeBuilders[cloudProvider]; dup {
		panic(fmt.Sprintf("manual node builder for %q registered twice", cloudProvider))
	}
	manualNodeBuilders[cloudProvider] = b
}

// ManualNodeBuilderFor returns the builder for a cloud provider, if any.
func ManualNodeBuilderFor(cloudProvider string) (ManualNodeBuilder, bool) {
	manualNodeBuildersMu.RLock()
	defer manualNodeBuildersMu.RUnlock()
	b, ok := manualNodeBuilders[cloudProvider]
	return b, ok
}

// RegisteredManualNodeProviders returns the providers that accept declared
// nodes, sorted.
func RegisteredManualNodeProviders() []string {
	manualNodeBuildersMu.RLock()
	defer manualNodeBuildersMu.RUnlock()
	out := make([]string, 0, len(manualNodeBuilders))
	for p := range manualNodeBuilders {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// ManualNodeTypes are the node types a user may declare in a cloud account or
// outside any connected account. A declared node stays until it is deleted or
// a collector takes it over: markInactiveNodes never sweeps source =
// ManualNodeSource, and the manual flow source retires the ones no
// declaration owns.
var ManualNodeTypes = []NodeType{
	NodeTypeService,
	NodeTypeExternalService,
	NodeTypeDatabase,
	NodeTypeCache,
	NodeTypeMessageQueue,
	NodeTypeQueue,
	NodeTypeTopic,
	NodeTypeComputeInstance,
	NodeTypeLoadBalancer,
	NodeTypeStorage,
	NodeTypeAPIGateway,
	NodeTypeServerlessFunction,
	NodeTypeCloudResource,
}

// ManualKubernetesNodeTypes are the extra node types a user may declare in a
// connected Kubernetes cluster. They are infra-authoritative, which is why the
// sweep exclusion above, not the type, keeps a declared one alive.
var ManualKubernetesNodeTypes = []NodeType{
	NodeTypeWorkload,
	NodeTypeK8sService,
}

// IsManualNodeType reports whether nt may be declared by hand somewhere.
func IsManualNodeType(nt NodeType) bool {
	return IsManualTenantScopedNodeType(nt) || nodeTypeIn(ManualKubernetesNodeTypes, nt)
}

// IsManualTenantScopedNodeType reports whether nt may be declared outside any
// connected account (or in a cloud account).
func IsManualTenantScopedNodeType(nt NodeType) bool {
	return nodeTypeIn(ManualNodeTypes, nt)
}

func nodeTypeIn(list []NodeType, nt NodeType) bool {
	for _, t := range list {
		if t == nt {
			return true
		}
	}
	return false
}

// SchemaProperties returns the concrete schema's properties a declaration may
// set for a specific type, sorted by name, or nil when it has none. It leaves
// out the ones a unique key reads, the ones only the collector can know, and
// lists of objects, which a form cannot express.
func SchemaProperties(specificType string) []ManualNodeProperty {
	s, ok := LookupSpecificTypeSchema(specificType)
	if !ok {
		return nil
	}
	out := make([]ManualNodeProperty, 0, len(s.Properties))
	for _, p := range s.Properties {
		if IsManualReservedProperty(p.Name) || IsManualObservedProperty(p.Name) || p.Shape == PropertyShapeRecords {
			continue
		}
		out = append(out, ManualNodeProperty{Name: p.Name, Shape: p.Shape})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// manualObservedProperties are schema fields only the collector can know, so
// a declaration may not set them: when the resource was created or changed,
// its runtime state and counters, the addresses and ports the platform
// allocates, internal ids, and fields Nudgebee derives from other data. The
// collector fills them in when it takes the node over.
var manualObservedProperties = map[string]struct{}{
	// When it was created or changed (beyond the suffix/prefix rules below).
	"created": {}, "created_at": {}, "deleted": {}, "time_created": {}, "launch_time_unix": {},
	// Runtime state and counters.
	"phase": {}, "progress": {}, "ready": {}, "ready_replicas": {}, "available_replicas": {},
	"observed_generation": {}, "deployment_revision": {}, "container_count": {},
	"state_message": {}, "state_reason": {}, "state_reason_code": {},
	"status_of_primary": {}, "status_of_secondary": {},
	"rows": {}, "size_in_bytes": {}, "size_in_bytes_value": {}, "number_of_mount_targets": {},
	"subscriptions_confirmed": {}, "subscriptions_deleted": {}, "subscriptions_pending": {},
	"backtrack_consumed_change_records": {},
	// Allocated by the platform.
	"cluster_ip": {}, "cluster_ips": {}, "node_ports": {},
	"load_balancer_hostname": {}, "load_balancer_ip": {}, "load_balancer_ingress": {},
	// Internal ids and revisions.
	"uid": {}, "k8s_workload_id": {}, "qualified_name": {}, "dbi_resource_id": {}, "db_cluster_resource_id": {},
	"revision_id": {}, "cluster_revision_number": {}, "meta_generation": {}, "code_sha256": {}, "code_size": {},
	"image_digest": {}, "is_latest_version": {}, "latest_ready_revision": {},
	// Derived by the collector or by Nudgebee. A Kubernetes builder stamps
	// kind itself, from the type picked.
	"kind": {}, "exposed_internet": {}, "is_public_entry": {}, "anonymous_access": {}, "anonymous_actions": {},
	"architecture_normalized": {}, "is_gke_node": {},
}

// Timestamps and runtime state follow naming conventions across clouds.
var (
	manualObservedSuffixes = []string{"_time", "_timestamp", "_date", "_state", "_status"}
	manualObservedPrefixes = []string{"last_"}
)

// IsManualObservedProperty reports whether key is a field only the collector
// can know, which a declaration may not set.
func IsManualObservedProperty(key string) bool {
	if _, ok := manualObservedProperties[key]; ok {
		return true
	}
	for _, s := range manualObservedSuffixes {
		if strings.HasSuffix(key, s) {
			return true
		}
	}
	for _, p := range manualObservedPrefixes {
		if strings.HasPrefix(key, p) {
			return true
		}
	}
	return false
}

// ManualNodeSource is the source stamped on a declared node. It is the manual
// flow source's name: that source re-emits declared nodes every build.
const ManualNodeSource = "manual"

// manualReservedProperties are keys a declared node's optional properties may
// not set: the ones any source's unique key reads (setting them would move the
// node off the key the collector computes) and the ones the builder stamps.
var manualReservedProperties = map[string]struct{}{
	"name": {}, "id": {}, "resource_id": {}, "region": {}, "zone": {}, "location": {},
	"vpc": {}, "vpc_id": {}, "vpc_name_hierarchy": {}, "vnet_id": {}, "vnet_name_hierarchy": {},
	"service_name": {}, "namespace": {}, "cluster": {},
	"source": {}, "specific_type": {}, "labels": {}, "is_active": {}, "description": {},
	"nb_resource_id": {}, "nb_account_id": {}, "manual_declared": {}, "manual_node_id": {},
}

// ManualNodeDetails is the descriptive data layered onto a declared node.
// None of it is identity: editing it never moves the node.
type ManualNodeDetails struct {
	ManualNodeID int64
	Description  string
	Properties   map[string]interface{}
	Labels       map[string]string
}

// IsManualReservedProperty reports whether key may not be set as an optional
// property on a declared node.
func IsManualReservedProperty(key string) bool {
	_, ok := manualReservedProperties[key]
	return ok
}

// MaterializeManualNode turns a node's base properties (as its builder produced
// them) into the declared node: the same id and unique key, the user's details
// layered on, and source = ManualNodeSource. It is also how the manual flow
// source rebuilds a stored declaration each cycle, so the node written at
// create time and the one re-emitted by every build are identical.
func MaterializeManualNode(nodeType NodeType, specificType, uniqueKey string, base map[string]interface{}, details ManualNodeDetails, tenantID, cloudAccountID string) (*DbNode, error) {
	props := make(map[string]interface{}, len(base)+len(details.Properties)+5)
	for k, v := range base {
		props[k] = v
	}
	for k, v := range details.Properties {
		if IsManualReservedProperty(k) {
			return nil, fmt.Errorf("property %q cannot be set on a declared node", k)
		}
		props[k] = v
	}
	if details.Description != "" {
		props["description"] = details.Description
	}
	if len(details.Labels) > 0 {
		labels := make(map[string]interface{}, len(details.Labels))
		for k, v := range details.Labels {
			labels[k] = v
		}
		props["labels"] = labels
	}
	props["manual_declared"] = true
	if details.ManualNodeID != 0 {
		props["manual_node_id"] = details.ManualNodeID
	}
	if specificType != "" {
		props["specific_type"] = specificType
	}
	node := NewNode(nodeType, uniqueKey, props, tenantID, cloudAccountID, ManualNodeSource)
	node.Level = "Tenant"
	return node, nil
}

// BaseProperties returns a built node's properties as a declaration stores
// them: without the keys MaterializeManualNode stamps.
func BaseProperties(node *DbNode) map[string]interface{} {
	out := make(map[string]interface{}, len(node.Properties))
	for k, v := range node.Properties {
		switch k {
		case "source", "labels", "manual_declared", "manual_node_id", "description":
			continue
		}
		out[k] = v
	}
	return out
}
