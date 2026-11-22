package azure

import (
	"log/slog"

	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
)

// Azure per-resource update policy.
//
// This file holds only the mechanics shared by every Azure resource type. What a
// given type allows is declared in that type's own file through
// registerPatchSpec (networking.go, ...).
//
// No Azure type is creatable yet. Beyond its dispatch builders, the Azure build
// runs row-driven reference passes (createReferenceEdges, createAppReferenceEdges,
// createRoleAssignmentEdges) that emit edges *out of* ordinary resources (a
// user-assigned identity's RUNS_AS, a VNet rule's ASSOCIATED_WITH) using an index
// over the whole account. A node created from one row would miss those, so a new
// Azure resource still rebuilds its account. Updates refresh properties.

// edgeBuilder rebuilds one node's own outgoing edges. No Azure spec declares one
// yet; the type exists so a spec can, without changing the policy.
type edgeBuilder func(s *AzureSource, node *core.DbNode, lookup *sources.NodeLookup, req *core.SourceBuildRequest) (edges []*core.DbEdge, newNodes []*core.DbNode)

var patchSpecs = sources.NewPatchSpecRegistry[edgeBuilder]()

// registerPatchSpec declares how one Azure resource type is patched. Call it from
// the resource file's init(). build is nil for a spec without edges.
func registerPatchSpec(spec sources.PatchSpec, build edgeBuilder) {
	patchSpecs.Register(spec, build, build != nil)
}

func init() {
	sources.RegisterCloudPatchPolicy("azure", func(logger *slog.Logger) sources.CloudPatchPolicy {
		return newPatchPolicy(logger)
	})
}

// patchPolicy implements sources.CloudPatchPolicy for Azure.
type patchPolicy struct {
	source *AzureSource
}

func newPatchPolicy(logger *slog.Logger) *patchPolicy {
	// Configured exactly as the source registry configures it for a graph build.
	source, _ := NewAzureSource(AzureSourceConfig{ServiceTypeFilter: DefaultAzureServiceTypeFilter}, logger)
	return &patchPolicy{source: source}
}

func (p *patchPolicy) Source() string { return "azure" }

// IncludeForGraph mirrors fetchAzureResources' SQL filter, shouldIncludeResource
// and shouldSuppressAzureResource.
func (p *patchPolicy) IncludeForGraph(row *sources.CloudResourceRow) bool {
	return row.IsActive && p.source.shouldIncludeResource(row) && !shouldSuppressAzureResource(row)
}

func (p *patchPolicy) Classify(row *sources.CloudResourceRow, req *core.SourceBuildRequest) (sources.ResourceKind, bool) {
	node := p.source.createNodeFromResource(row, req)
	if node == nil {
		return sources.ResourceKind{}, false
	}
	return sources.KindOf(node), true
}

func (p *patchPolicy) KindOf(node *core.DbNode) sources.ResourceKind {
	return sources.KindOf(node)
}

func (p *patchPolicy) EdgesBuiltElsewhere() []core.EdgeKind {
	return patchSpecs.EdgesBuiltElsewhere()
}

func (p *patchPolicy) Spec(kind sources.ResourceKind) sources.PatchSpec {
	spec, _, _ := patchSpecs.Lookup(kind)
	return spec
}

// baseContext is what every Azure patch loads: VNets, whose names
// propagateVNetNamesToResources writes onto resources.
var baseContext = []core.NodeType{core.NodeTypeVPC}

func (p *patchPolicy) ContextFor(kind sources.ResourceKind) []core.NodeType {
	return sources.MergeNodeTypes(baseContext, p.Spec(kind).Context)
}

// BuildNodes replays convertResourcesToGraph's node steps for one resource,
// including the VNet-name propagation resolved against contextNodes. The key
// reads only the resource id, region and name, not the network, so the node
// keys the same as in the account build; the applier's id check still sends any
// mismatch to the account rebuild.
func (p *patchPolicy) BuildNodes(rows []sources.CloudResourceRow, contextNodes []*core.DbNode, req *core.SourceBuildRequest) ([]*core.DbNode, []string) {
	nodes := make([]*core.DbNode, 0, len(rows))
	for i := range rows {
		if !p.IncludeForGraph(&rows[i]) {
			continue
		}
		if node := p.source.createNodeFromResource(&rows[i], req); node != nil {
			nodes = append(nodes, node)
		}
	}
	p.source.propagateVNetNamesToResources(nodes, sources.NewNodeLookup(contextNodes))
	return nodes, nil
}

func (p *patchPolicy) OutgoingEdges(node *core.DbNode, contextNodes []*core.DbNode, req *core.SourceBuildRequest) ([]*core.DbEdge, []*core.DbNode, core.EdgeSignature, bool) {
	spec, build, found := patchSpecs.Lookup(sources.KindOf(node))
	if !found || build == nil {
		return nil, nil, core.EdgeSignature{}, false
	}
	lookup := sources.NewNodeLookup(append([]*core.DbNode{node}, contextNodes...))
	edges, newNodes := build(p.source, node, lookup, req)
	return edges, newNodes, spec.Signature, true
}
