package core

import (
	"log/slog"
	"strings"
)

// resolveGCPManagedServicePropResolvedBy is stamped onto every CALLS edge whose
// destination this pass rewrites, so the frontend / LLM prompts can tell an
// account-inferred data-tier link apart from a directly-observed one.
const resolveGCPManagedServicePropResolvedBy = "resolved_by"
const resolveGCPManagedServiceResolver = "gcp_managed_service_account_match"

// gcpManagedServiceLeafTarget describes the collected GCP node a trace-derived
// ExternalService "leaf" (a managed-service display name like "GCP Datastore")
// should resolve to.
//
//   - nodeType constrains the candidate to one KG node type.
//   - dbType, when non-empty, further constrains a Database candidate to a
//     specific properties["type"] (GCP has one Firestore "(default)" per project
//     but may also expose Cloud SQL Databases, which are a different service).
type gcpManagedServiceLeafTarget struct {
	nodeType NodeType
	dbType   string
}

// gcpManagedServiceLeafTargets maps normalized (lower-cased, trimmed) trace
// ExternalService names for GCP *managed data services* to the collected node
// type they represent. These leaves carry a human display name ("GCP Datastore",
// "Redis"), never a DNS host, so the DNS-based CentralizedExternalServiceEnricher
// cannot match them — they survive as orphaned stubs. This pass resolves them by
// the calling service's own GCP account instead of by endpoint.
//
// Redis/Memorystore is included for completeness but rarely resolves in practice:
// Memorystore instances usually live in a shared infra project, not the caller's,
// so the same-account guard below declines them (correctly — the target is
// ambiguous from a trace name alone).
var gcpManagedServiceLeafTargets = map[string]gcpManagedServiceLeafTarget{
	"gcp datastore":   {NodeTypeDatabase, "firestore"},
	"cloud datastore": {NodeTypeDatabase, "firestore"},
	"datastore":       {NodeTypeDatabase, "firestore"},
	"gcp firestore":   {NodeTypeDatabase, "firestore"},
	"cloud firestore": {NodeTypeDatabase, "firestore"},
	"firestore":       {NodeTypeDatabase, "firestore"},
	"redis":           {NodeTypeCache, ""},
	"memorystore":     {NodeTypeCache, ""},
}

// ResolveGCPManagedServiceCalls repoints CALLS edges that terminate on a
// GCP managed-service ExternalService stub ("GCP Datastore", "Redis", …) to the
// real collected node of that service type in the *calling* service's GCP
// account. It is a pure function over (nodes, edges) and returns the rewritten
// slices plus the number of edges it repointed.
//
// Why per-edge and account-keyed (not per-stub like CollapseEnrichedExternalServices):
// one "GCP Datastore" / "Redis" stub is shared tenant-wide across every caller in
// every account, but each account has its own Firestore database. A single
// stub→node redirect would mis-attribute cross-account callers. So we resolve each
// CALLS edge independently, using the edge's *source* (caller) account.
//
// Safety guard — a stub edge is only rewritten when the caller's account contains
// *exactly one* candidate node of the target type. Firestore is always a single
// "(default)" per project, so it resolves cleanly; ambiguous cases (e.g. a caller
// account with several Memorystore instances, or none) are left on the stub rather
// than guessed. Stubs left with no remaining referencing edge are dropped.
//
// This runs after CollapseEnrichedExternalServices so it only ever sees the
// managed-service leaves the DNS enricher could not match. The caller is
// responsible for running DeduplicateEdgesWithPriority afterwards.
func ResolveGCPManagedServiceCalls(
	nodes []*DbNode,
	edges []*DbEdge,
	logger *slog.Logger,
) (outNodes []*DbNode, outEdges []*DbEdge, rewritten int) {
	if logger == nil {
		logger = slog.Default()
	}

	nodeByID := make(map[string]*DbNode, len(nodes))
	// candidatesByAccount holds collected GCP data-tier nodes keyed by account.
	candidatesByAccount := make(map[string][]*DbNode)
	// stubTargets holds ExternalService stub nodes we might resolve.
	stubTargets := make(map[string]gcpManagedServiceLeafTarget)

	for _, n := range nodes {
		if n == nil {
			continue
		}
		nodeByID[n.ID] = n
		switch n.NodeType {
		case NodeTypeExternalService:
			if t, ok := gcpManagedServiceLeafTargets[normalizeManagedServiceName(n)]; ok {
				stubTargets[n.ID] = t
			}
		case NodeTypeDatabase, NodeTypeCache:
			// Only collected GCP resources are resolution candidates.
			if strings.EqualFold(n.Source, "gcp") && n.CloudAccountID != "" {
				candidatesByAccount[n.CloudAccountID] = append(candidatesByAccount[n.CloudAccountID], n)
			}
		}
	}

	if len(stubTargets) == 0 {
		return nodes, edges, 0
	}

	// Track which stubs still have a referencing edge after rewriting; only
	// fully-detached stubs are dropped.
	referenced := make(map[string]bool)

	for _, e := range edges {
		if e == nil {
			continue
		}
		target, isStubDest := stubTargets[e.DestinationNodeID]
		if !isStubDest || e.RelationshipType != RelationshipCalls {
			// Any non-CALLS edge, or a CALLS edge not aimed at a stub, keeps the
			// stub alive if it references one.
			if stubReferencedByEdge(e, stubTargets) {
				referenced[stubReferencedID(e, stubTargets)] = true
			}
			continue
		}

		caller := nodeByID[e.SourceNodeID]
		match := uniqueManagedServiceCandidate(caller, candidatesByAccount, target)
		if match == nil {
			// Ambiguous / no candidate in caller's account: leave the edge on the stub.
			referenced[e.DestinationNodeID] = true
			continue
		}

		if e.Properties == nil {
			e.Properties = make(map[string]interface{})
		}
		stub := nodeByID[e.DestinationNodeID]
		if stub != nil {
			e.Properties[CollapseEdgePropOriginalHostname], _ = stub.Properties["name"].(string)
			e.Properties[CollapseEdgePropOriginalESUniqueKey] = stub.UniqueKey
		}
		e.Properties[resolveGCPManagedServicePropResolvedBy] = resolveGCPManagedServiceResolver
		e.DestinationNodeID = match.ID
		// The edge primary key is GenerateEdgeID(src, dst, rel) (see helpers.go /
		// saveGraphToDB, which persists edge.ID verbatim). Repointing the
		// destination without regenerating the ID leaves it hashing the old stub
		// destination, colliding with the stub's still-present PK row on insert.
		e.ID = GenerateEdgeID(e.SourceNodeID, match.ID, e.RelationshipType)
		rewritten++
	}

	if rewritten == 0 {
		return nodes, edges, 0
	}

	outNodes = dropDetachedManagedServiceStubs(nodes, stubTargets, referenced)

	logger.Info("resolved gcp managed-service calls",
		"rewritten_calls_edges", rewritten,
		"candidate_stubs", len(stubTargets),
		"dropped_stub_nodes", len(nodes)-len(outNodes),
		"input_nodes", len(nodes),
		"output_nodes", len(outNodes))

	return outNodes, edges, rewritten
}

// normalizeManagedServiceName returns the lower-cased, trimmed display name used
// as the lookup key in gcpManagedServiceLeafTargets.
func normalizeManagedServiceName(n *DbNode) string {
	name, _ := n.Properties["name"].(string)
	return strings.ToLower(strings.TrimSpace(name))
}

// uniqueManagedServiceCandidate returns the single collected node in the caller's
// account matching the target spec, or nil when the caller is unknown or the
// account has zero or more than one matching candidate (ambiguous).
func uniqueManagedServiceCandidate(
	caller *DbNode,
	candidatesByAccount map[string][]*DbNode,
	target gcpManagedServiceLeafTarget,
) *DbNode {
	if caller == nil || caller.CloudAccountID == "" {
		return nil
	}
	var found *DbNode
	for _, cand := range candidatesByAccount[caller.CloudAccountID] {
		if cand.NodeType != target.nodeType {
			continue
		}
		if target.dbType != "" {
			if dbType, _ := cand.Properties["type"].(string); !strings.EqualFold(dbType, target.dbType) {
				continue
			}
		}
		if found != nil {
			return nil // more than one candidate → ambiguous
		}
		found = cand
	}
	return found
}

// stubReferencedByEdge reports whether an edge touches any candidate stub.
func stubReferencedByEdge(e *DbEdge, stubTargets map[string]gcpManagedServiceLeafTarget) bool {
	_, src := stubTargets[e.SourceNodeID]
	_, dst := stubTargets[e.DestinationNodeID]
	return src || dst
}

// stubReferencedID returns the stub ID an edge touches (destination preferred).
func stubReferencedID(e *DbEdge, stubTargets map[string]gcpManagedServiceLeafTarget) string {
	if _, ok := stubTargets[e.DestinationNodeID]; ok {
		return e.DestinationNodeID
	}
	return e.SourceNodeID
}

// dropDetachedManagedServiceStubs removes stub nodes that no longer have any
// referencing edge after rewriting. Preserves order.
func dropDetachedManagedServiceStubs(
	nodes []*DbNode,
	stubTargets map[string]gcpManagedServiceLeafTarget,
	referenced map[string]bool,
) []*DbNode {
	out := make([]*DbNode, 0, len(nodes))
	for _, n := range nodes {
		if n == nil {
			continue
		}
		if _, isStub := stubTargets[n.ID]; isStub && !referenced[n.ID] {
			continue
		}
		out = append(out, n)
	}
	return out
}
