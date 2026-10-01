package sources

import (
	"fmt"
	"strings"
	"sync"

	"nudgebee/services/knowledge_graph/core"
)

// Manual nodes for cloud accounts.
//
// A declared node is built by handing the cloud's own row classifier a
// synthetic inventory row carrying what the user typed, so it gets the unique
// key, specific type and property shape the collector gives the real resource.
// When the collector later ingests that resource it writes the same node id
// and the declaration is adopted instead of duplicated.

// ManualNodeEntry is one declarable type: the inventory row shape the cloud's
// classifier turns into it, and the identity fields its unique key reads.
type ManualNodeEntry struct {
	SpecificType string
	NodeType     core.NodeType
	RowType      string
	ServiceName  string
	// NameLabel / NameHint tell the user what the collector stores as the
	// resource's name for this type (EC2 keys by Name tag, RDS by identifier).
	NameLabel string
	NameHint  string
	// Network is set for types whose unique key carries the network the
	// resource lives in (an EC2 instance's VPC). NetworkRequired is false when
	// the collector only sometimes sees one (an RDS instance outside a VPC).
	Network         core.NodeType
	NetworkRequired bool
	NetworkHint     string
}

// CloudManualNodeBuilder implements core.ManualNodeBuilder over a cloud
// source's createNodeFromResource.
type CloudManualNodeBuilder struct {
	// CloudProvider is the cloud_resourses cloud_provider written on the row.
	CloudProvider string
	// Candidates lists the row shapes worth offering, exact row types before
	// service fallbacks; Kinds keeps the first per specific type.
	Candidates func() []ManualNodeEntry
	// BuildRow is the cloud's createNodeFromResource.
	BuildRow func(row *CloudResourceRow, req *core.SourceBuildRequest) *core.DbNode
	// ApplyNetwork stamps the network onto the node and re-keys it the way
	// the source's own network pass does. Nil for clouds without one.
	ApplyNetwork func(node *core.DbNode, network *core.ManualNodeNetwork)

	once    sync.Once
	entries map[string]ManualNodeEntry
	kinds   []core.ManualNodeKind
}

func (b *CloudManualNodeBuilder) load() {
	b.once.Do(func() {
		b.entries = map[string]ManualNodeEntry{}
		for _, e := range b.Candidates() {
			if !core.IsManualTenantScopedNodeType(e.NodeType) || e.NodeType == core.NodeTypeCloudResource {
				continue
			}
			if _, seen := b.entries[e.SpecificType]; seen {
				continue
			}
			// Keep only shapes the classifier really turns into this type, so
			// a renamed kind drops out of the form instead of building a node
			// whose key the collector never produces.
			probe := b.BuildRow(b.row(e, core.ManualNodeInput{Name: "probe", Region: "probe"}), &core.SourceBuildRequest{})
			if probe == nil || probe.NodeType != e.NodeType || probe.SpecificType != e.SpecificType {
				continue
			}
			b.entries[e.SpecificType] = e
			b.kinds = append(b.kinds, core.ManualNodeKind{
				SpecificType: e.SpecificType,
				NodeType:     e.NodeType,
				KeyFields:    entryFields(e),
				Properties:   core.SchemaProperties(e.SpecificType),
			})
		}
	})
}

func entryFields(e ManualNodeEntry) []core.ManualNodeField {
	nameLabel := e.NameLabel
	if nameLabel == "" {
		nameLabel = "Name"
	}
	nameHint := e.NameHint
	if nameHint == "" {
		nameHint = "The resource name as it appears in the cloud console"
	}
	fields := []core.ManualNodeField{
		{Name: core.ManualNodeFieldName, Label: nameLabel, Hint: nameHint, Required: true},
		{Name: core.ManualNodeFieldRegion, Label: "Region", Hint: "The region the resource is in, e.g. us-east-1", Required: true},
	}
	if e.Network != "" {
		fields = append(fields, core.ManualNodeField{
			Name:         core.ManualNodeFieldNetwork,
			Label:        "Network",
			Hint:         e.NetworkHint,
			Required:     e.NetworkRequired,
			PickNodeType: e.Network,
		})
	}
	return fields
}

// Kinds implements core.ManualNodeBuilder.
func (b *CloudManualNodeBuilder) Kinds() []core.ManualNodeKind {
	b.load()
	return b.kinds
}

// Build implements core.ManualNodeBuilder.
func (b *CloudManualNodeBuilder) Build(in core.ManualNodeInput) (*core.DbNode, error) {
	b.load()
	e, ok := b.entries[in.SpecificType]
	if !ok {
		return nil, fmt.Errorf("%s resources of type %q cannot be declared", in.CloudProvider, in.SpecificType)
	}
	in.Name = strings.TrimSpace(in.Name)
	in.Region = strings.TrimSpace(in.Region)
	if in.Name == "" {
		return nil, fmt.Errorf("name is required")
	}
	if in.Region == "" {
		return nil, fmt.Errorf("region is required")
	}
	if in.Network != nil && e.Network == "" {
		return nil, fmt.Errorf("%s is not placed in a network; leave the network empty", in.SpecificType)
	}
	if in.Network == nil && e.NetworkRequired {
		return nil, fmt.Errorf("network is required for %s", in.SpecificType)
	}

	node := b.BuildRow(b.row(e, in), &core.SourceBuildRequest{TenantID: in.TenantID, CloudAccountID: in.CloudAccountID})
	if node == nil {
		return nil, fmt.Errorf("%s resources of type %q cannot be declared", in.CloudProvider, in.SpecificType)
	}
	if in.Network != nil && b.ApplyNetwork != nil {
		b.ApplyNetwork(node, in.Network)
	}
	return node, nil
}

// row is the inventory row the collector would have written for the resource.
// It has no id: the change detector only considers nodes whose nb_resource_id
// is a real inventory row, so a declared node is never proposed for deletion.
func (b *CloudManualNodeBuilder) row(e ManualNodeEntry, in core.ManualNodeInput) *CloudResourceRow {
	return &CloudResourceRow{
		Name:          in.Name,
		Type:          e.RowType,
		ServiceName:   e.ServiceName,
		Account:       in.CloudAccountID,
		Tenant:        in.TenantID,
		CloudProvider: b.CloudProvider,
		Region:        in.Region,
		IsActive:      true,
		AccountNumber: in.AccountNumber,
		Tags:          []byte("{}"),
		Meta:          []byte("{}"),
	}
}
