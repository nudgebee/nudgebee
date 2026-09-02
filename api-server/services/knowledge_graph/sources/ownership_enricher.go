package sources

import (
	"fmt"
	"log/slog"

	"nudgebee/services/internal/database"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/ownership"
	"nudgebee/services/security"
)

func init() {
	RegisterCrossSourceEnricherFactory("ownership", func(logger *slog.Logger) core.CrossSourceEnricherInterface {
		return NewOwnershipEnricher(logger)
	}, "Creates canonical NudgebeeGroup nodes from user_groups + MEMBER_OF edges from "+
		"usergroup_users, and OWNS edges from each workload/namespace/cluster/cloud "+
		"resource to its effective owner, resolved via ownership.Resolve — the same "+
		"direct/rule/inherited logic the Ownership UI shows.")

	core.RegisterSpecificTypeSchema(nudgebeeGroupSchema)
}

// nudgebeeGroupSchema is the concrete property schema for the canonical
// NudgebeeGroup specific_type — one node per internal user_groups row,
// mirroring how identity_enricher.go materializes NudgebeeUser nodes.
var nudgebeeGroupSchema = core.SpecificTypeSchema{
	SpecificType: "NudgebeeGroup",
	NodeType:     core.NodeTypeUserGroup,
	Properties: []core.PropertyDef{
		{Name: "name", Indexed: true},
		{Name: "nudgebee_group_id", Indexed: true},
	},
}

// resolveChunkSize caps how many resources go into a single ownership.Resolve
// call — defensive batching against per-call latency/memory on very large
// tenants, not a correctness requirement (Resolve bulk-loads its dependencies
// once per call, not per resource).
const resolveChunkSize = 2000

// OwnershipEnricher builds the ownership layer of the Knowledge Graph:
//  1. One NudgebeeGroup node per user_groups row, plus a MEMBER_OF edge from
//     each member's canonical NudgebeeUser node (usergroup_users).
//  2. An OWNS edge from the effective owner (user or group) to every already-built
//     workload/namespace/cluster/cloud-resource node, reusing ownership.Resolve
//     unmodified so the graph never diverges from what the Ownership UI shows.
//
// Runs in Phase 2.1 (cross-source enrichment), after all per-account sources have
// built their nodes, so every ownable node this cycle already exists in allNodes.
//
// resource_type="service" is intentionally NOT handled here: most Service nodes
// (trace/eBPF-observed) are built later, by flow sources in Phase 2.5, so this
// enricher — running in 2.1 — would only ever see the minority built earlier by
// static sources (e.g. k8s Ingress). That would make "service" ownership silently
// work for some services and not others, which is worse than not attempting it.
//
// Enricher run order is non-deterministic (the registry is a map), so this does
// NOT assume identity_enricher's NudgebeeUser nodes are already in allNodes — it
// recomputes the same deterministic node ID formula independently (see
// buildUserNodeIDs). If identity_enricher's NudgebeeUser unique-key formula ever
// changes, this must change with it.
type OwnershipEnricher struct {
	logger *slog.Logger
}

// NewOwnershipEnricher creates a new ownership enricher.
func NewOwnershipEnricher(logger *slog.Logger) *OwnershipEnricher {
	if logger == nil {
		logger = slog.Default()
	}
	return &OwnershipEnricher{logger: logger}
}

// GetName returns the name of the enricher.
func (e *OwnershipEnricher) GetName() string { return "ownership" }

// EnrichCrossSources adds NudgebeeGroup nodes, MEMBER_OF edges, and OWNS edges.
func (e *OwnershipEnricher) EnrichCrossSources(
	reqCtx *security.RequestContext,
	allNodes []*core.DbNode,
	allEdges []*core.DbEdge,
	tenantID string,
) ([]*core.DbNode, []*core.DbEdge, error) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return allNodes, allEdges, fmt.Errorf("ownership_enricher: failed to get database manager: %w", err)
	}

	groupNodes, groupsByID, err := e.buildGroupNodes(dbms, tenantID)
	if err != nil {
		e.logger.Warn("ownership_enricher: failed to build group nodes, skipping", "error", err)
		return allNodes, allEdges, nil
	}

	userNodeIDs, err := e.buildUserNodeIDs(dbms, tenantID)
	if err != nil {
		e.logger.Warn("ownership_enricher: failed to build user node ID map, skipping edges", "error", err)
		return append(allNodes, groupNodes...), allEdges, nil
	}

	memberships, err := e.fetchMemberships(dbms, tenantID)
	if err != nil {
		e.logger.Warn("ownership_enricher: failed to fetch group memberships, skipping MEMBER_OF edges", "error", err)
		memberships = nil
	}
	memberOfEdges := buildMemberOfEdges(userNodeIDs, groupsByID, memberships, tenantID)

	idx := collectOwnableResources(allNodes)
	ownsEdges, err := e.resolveOwnsEdges(reqCtx, idx, userNodeIDs, groupsByID, tenantID)
	if err != nil {
		e.logger.Warn("ownership_enricher: failed to resolve ownership, skipping OWNS edges", "error", err)
		ownsEdges = nil
	}

	newEdges := append(memberOfEdges, ownsEdges...)
	return append(allNodes, groupNodes...), append(allEdges, newEdges...), nil
}

// buildGroupNodes creates one NudgebeeGroup node per user_groups row for the
// tenant, keyed by group id for edge-target lookup (byGroupID). The unique
// key's Name component is the group's name — enforced unique per tenant at
// the DB level by user_groups_tenant_name_key (see the migration that added
// it), the same way every other node type's Name segment is a readable,
// tenant-unique identifier. A rename changes the node's identity (new id on
// the next build, old one tombstoned — NodeTypeUserGroup is in
// InfraAuthoritativeNodeTypes for exactly this), matching how a k8s resource
// rename is really a delete+recreate.
func (e *OwnershipEnricher) buildGroupNodes(dbms *database.DatabaseManager, tenantID string) ([]*core.DbNode, map[string]*core.DbNode, error) {
	rows, err := dbms.Query(`SELECT id::text, name FROM user_groups WHERE tenant = $1`, tenantID)
	if err != nil {
		return nil, nil, fmt.Errorf("query user groups: %w", err)
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil {
			e.logger.Warn("ownership_enricher: failed to close user group rows", "error", cerr)
		}
	}()

	var nodes []*core.DbNode
	byGroupID := make(map[string]*core.DbNode)
	for rows.Next() {
		var groupID, name string
		if err := rows.Scan(&groupID, &name); err != nil {
			return nil, nil, fmt.Errorf("scan user group row: %w", err)
		}
		properties := map[string]interface{}{
			"specific_type":     "NudgebeeGroup",
			"name":              name,
			"nudgebee_group_id": groupID,
		}
		uniqueKey := core.BuildUniqueKey(core.CloudProviderExternal, tenantID, "", core.NodeTypeUserGroup, "", name)
		node := core.NewNode(core.NodeTypeUserGroup, uniqueKey, properties, tenantID, tenantID, "ownership")
		nodes = append(nodes, node)
		byGroupID[groupID] = node
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate user group rows: %w", err)
	}
	return nodes, byGroupID, nil
}

// buildUserNodeIDs computes the canonical NudgebeeUser node ID for every active
// tenant user, without creating the nodes themselves (identity_enricher.go owns
// that) — this only needs the IDs to point MEMBER_OF/OWNS edges at, and doing so
// independently avoids depending on identity_enricher having already run this
// cycle. Duplicates identity_enricher.buildCanonicalUserNodes' query + ID formula
// deliberately (both are in package sources, so there's no export boundary to
// cross for ~10 lines); factor a shared helper if a third consumer needs it.
func (e *OwnershipEnricher) buildUserNodeIDs(dbms *database.DatabaseManager, tenantID string) (map[string]string, error) {
	rows, err := dbms.Query(`
		SELECT u.id::text, u.username::text
		FROM users u
		JOIN tenant_users tu ON tu.user = u.id
		WHERE tu.tenant = $1 AND u.status = 'active'
	`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("query tenant users: %w", err)
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil {
			e.logger.Warn("ownership_enricher: failed to close user rows", "error", cerr)
		}
	}()

	ids := make(map[string]string)
	for rows.Next() {
		var userID, username string
		if err := rows.Scan(&userID, &username); err != nil {
			return nil, fmt.Errorf("scan tenant user row: %w", err)
		}
		uniqueKey := core.BuildUniqueKey(core.CloudProviderExternal, tenantID, "", core.NodeTypeUserAccount, "", username)
		ids[userID] = core.GenerateNodeID(uniqueKey + tenantID + tenantID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate tenant user rows: %w", err)
	}
	return ids, nil
}

// membershipRow is one usergroup_users row.
type membershipRow struct {
	userID  string
	groupID string
}

// fetchMemberships loads every group membership row for groups belonging to the
// tenant.
func (e *OwnershipEnricher) fetchMemberships(dbms *database.DatabaseManager, tenantID string) ([]membershipRow, error) {
	rows, err := dbms.Query(`
		SELECT gu."user"::text, gu."group"::text
		FROM usergroup_users gu
		JOIN user_groups g ON g.id = gu."group"
		WHERE g.tenant = $1
	`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("query group memberships: %w", err)
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil {
			e.logger.Warn("ownership_enricher: failed to close membership rows", "error", cerr)
		}
	}()

	var results []membershipRow
	for rows.Next() {
		var m membershipRow
		if err := rows.Scan(&m.userID, &m.groupID); err != nil {
			return nil, fmt.Errorf("scan group membership row: %w", err)
		}
		results = append(results, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate group membership rows: %w", err)
	}
	return results, nil
}

// buildMemberOfEdges links each group member's canonical user node to its
// group node. Pulled out of EnrichCrossSources as a pure function (no DB access)
// so the matching logic is unit-testable without a database.
func buildMemberOfEdges(
	userNodeIDs map[string]string,
	groupNodes map[string]*core.DbNode,
	memberships []membershipRow,
	tenantID string,
) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0, len(memberships))
	for _, m := range memberships {
		userNodeID, ok := userNodeIDs[m.userID]
		if !ok {
			continue // member isn't an active tenant user this cycle
		}
		groupNode, ok := groupNodes[m.groupID]
		if !ok {
			continue // group row vanished between the two queries (race, defensive)
		}
		edges = append(edges, core.NewEdge(
			userNodeID, groupNode.ID, core.RelationshipMemberOf,
			map[string]interface{}{}, tenantID, tenantID, "ownership",
		))
	}
	return edges
}

// ownableResourceIndex maps an ownership.ResourceRef (resource_type, resource_key)
// to the KG node it identifies this cycle, so a resolved OwnerResult can be
// turned into an edge whose destination is guaranteed to exist.
type ownableResourceIndex struct {
	refs  []ownership.ResourceRef
	nodes map[string]*core.DbNode // keyed by resource_type+"\x00"+resource_key
}

// collectOwnableResources walks allNodes once, reconstructing each node's
// ownership.resource_key from whatever identifying property its source already
// stamps for that purpose — so the join is real, not assumed:
//
//	Workload  -> properties["k8s_workload_id"]           (sources/k8s/workload.go)
//	Namespace -> node.CloudAccountID + "/" + properties["name"]  (sources/k8s/cluster_namespace.go)
//	Cluster   -> node.CloudAccountID                       (k8s-enabled accounts only)
//	AWS/GCP/Azure resources -> properties["nb_resource_id"] (sources/<cloud>/classify.go)
//
// Non-k8s cloud accounts have no KG node representing "the whole account" — only
// k8s mints a Cluster scaffold node — so cloud_account ownership on a pure
// AWS/GCP/Azure account produces no ref here and therefore no edge. Pre-existing
// KG modeling gap, not something this enricher can fix.
func collectOwnableResources(allNodes []*core.DbNode) ownableResourceIndex {
	idx := ownableResourceIndex{nodes: make(map[string]*core.DbNode)}
	add := func(resourceType, resourceKey string, node *core.DbNode) {
		if resourceKey == "" {
			return
		}
		key := resourceType + "\x00" + resourceKey
		if _, dup := idx.nodes[key]; dup {
			return
		}
		idx.nodes[key] = node
		idx.refs = append(idx.refs, ownership.ResourceRef{ResourceType: resourceType, ResourceKey: resourceKey})
	}

	for _, n := range allNodes {
		switch n.NodeType {
		case core.NodeTypeWorkload:
			if id, ok := n.Properties["k8s_workload_id"].(string); ok {
				add(ownership.ResourceTypeWorkload, id, n)
			}
		case core.NodeTypeNamespace:
			if name, ok := n.Properties["name"].(string); ok && n.CloudAccountID != "" {
				add(ownership.ResourceTypeNamespace, n.CloudAccountID+"/"+name, n)
			}
		case core.NodeTypeCluster:
			if n.CloudAccountID != "" {
				add(ownership.ResourceTypeCloudAccount, n.CloudAccountID, n)
			}
		default:
			if n.Source != "aws" && n.Source != "gcp" && n.Source != "azure" {
				continue
			}
			if id, ok := n.Properties["nb_resource_id"].(string); ok && id != "" {
				add(ownership.ResourceTypeCloudResource, id, n)
			}
		}
	}
	return idx
}

// resolveOwnsEdges resolves every collected resource ref's effective owner via
// ownership.Resolve, in chunks, and turns the results into OWNS edges.
func (e *OwnershipEnricher) resolveOwnsEdges(
	reqCtx *security.RequestContext,
	idx ownableResourceIndex,
	userNodeIDs map[string]string,
	groupNodes map[string]*core.DbNode,
	tenantID string,
) ([]*core.DbEdge, error) {
	var edges []*core.DbEdge
	for start := 0; start < len(idx.refs); start += resolveChunkSize {
		end := start + resolveChunkSize
		if end > len(idx.refs) {
			end = len(idx.refs)
		}
		results, err := ownership.Resolve(reqCtx, ownership.ResolveRequest{Resources: idx.refs[start:end]})
		if err != nil {
			return edges, err
		}
		edges = append(edges, buildOwnsEdges(results, idx, userNodeIDs, groupNodes, tenantID)...)
	}
	return edges, nil
}

// buildOwnsEdges turns resolved effective-owner results into OWNS edges (owner
// as source, owned resource as destination — matching the existing convention in
// sources/github/repos.go and sources/pagerduty/services.go). Pulled out as a
// pure function so this logic — the highest-risk part of the enricher — is
// unit-testable without a database or a live ownership.Resolve call.
func buildOwnsEdges(
	results []ownership.OwnerResult,
	idx ownableResourceIndex,
	userNodeIDs map[string]string,
	groupNodes map[string]*core.DbNode,
	tenantID string,
) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0, len(results))
	for _, r := range results {
		if !r.Found {
			continue
		}
		resourceNode, ok := idx.nodes[r.ResourceType+"\x00"+r.ResourceKey]
		if !ok {
			continue // resource node not present this cycle (defensive — refs came from allNodes)
		}
		var ownerNodeID string
		switch r.OwnerType {
		case ownership.OwnerTypeUser:
			id, ok := userNodeIDs[r.OwnerId]
			if !ok {
				continue // owner isn't an active tenant user this cycle
			}
			ownerNodeID = id
		case ownership.OwnerTypeGroup:
			node, ok := groupNodes[r.OwnerId]
			if !ok {
				continue // owner group not found this cycle
			}
			ownerNodeID = node.ID
		default:
			continue
		}
		edges = append(edges, core.NewEdge(
			ownerNodeID, resourceNode.ID, core.RelationshipOwns,
			map[string]interface{}{"source": r.Source, "via": r.Via},
			tenantID, tenantID, "ownership",
		))
	}
	return edges
}
