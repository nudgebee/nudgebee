package gcp

import (
	"encoding/json"
	"fmt"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
	"strings"
)

// gcpServiceAccountSchema is the concrete per-specific_type property schema for
// the GCPServiceAccount specific_type. Names are the node.Properties keys written by
// createServiceIdentityFromGCPSA. name/arn/subtype/service_name are universal base
// keys (the ontology's source fields); the resource-specific descriptors are
// declared here. None are Indexed — ServiceIdentity filtering uses the universal
// arn/subtype/service_name keys via core.QueryablePropertiesMap.
var gcpServiceAccountSchema = core.SpecificTypeSchema{
	SpecificType: "GCPServiceAccount",
	NodeType:     core.NodeTypeServiceIdentity,
	Properties: []core.PropertyDef{
		{Name: "email"},
		{Name: "display_name"},
		{Name: "disabled"},
		{Name: "unique_id"},
		{Name: "gcp_project_id"},
		// Additional provider fields (not yet emitted by our extractor):
		{Name: "oauth2_client_id"},
	},
}

func init() { core.RegisterSpecificTypeSchema(gcpServiceAccountSchema) }

// createUsesSecretEdges links a compute node to the Secret Manager secrets it
// references (from meta.secrets — env secretKeyRef + secret volumes), matching each
// secret's short name to the SecretVault node.
func (s *GCPSource) createUsesSecretEdges(lookup *sources.NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)
	srvNodes, ok := lookup.ByNodeType[core.NodeTypeServerlessFunction]
	if !ok {
		return edges
	}
	for _, node := range srvNodes {
		meta, _ := node.Properties["meta"].(map[string]interface{})
		if meta == nil {
			continue
		}
		secrets, _ := meta["secrets"].([]interface{})
		for _, sv := range secrets {
			name, _ := sv.(string)
			if name == "" {
				continue
			}
			if secretNode := findNodeByNameAndType(lookup, core.NodeTypeSecretVault, extractGCPShortName(name)); secretNode != nil {
				edges = append(edges, s.createEdge(node, secretNode, core.RelationshipUsesSecret,
					map[string]interface{}{"connection_type": "secret_ref"}, req))
			}
		}
	}
	s.logger.Info("created GCP USES_SECRET edges", "edge_count", len(edges))
	return edges
}

// gcpIAMRoleTarget maps a GCP IAM role to the KG data resource it grants access
// to, or ok=false when the role is not a data-tier grant we model. Primitive
// roles (roles/owner|editor|viewer) and org-level custom roles are deliberately
// excluded: they grant access to nearly everything and expanding them would
// swamp the graph with noise rather than describe real data dependencies.
func gcpIAMRoleTarget(role string) (iamAccessTarget, bool) {
	switch {
	case strings.HasPrefix(role, "roles/datastore."):
		// Any datastore role (user/owner/viewer/importExportAdmin/indexAdmin)
		// indicates the identity touches the project's single Firestore database.
		return iamAccessTarget{core.NodeTypeDatabase, core.RelationshipHasAccessTo, "firestore"}, true
	case role == "roles/bigquery.dataViewer", role == "roles/bigquery.dataEditor",
		role == "roles/bigquery.dataOwner", role == "roles/bigquery.admin", role == "roles/bigquery.user":
		// Data-access roles only — jobUser/metadataViewer/readSessionUser don't
		// map to a specific dataset. Target Datasets, never the (huge) Table set.
		return iamAccessTarget{core.NodeTypeDatabase, core.RelationshipHasAccessTo, "bigquery-dataset"}, true
	case strings.HasPrefix(role, "roles/storage."):
		return iamAccessTarget{core.NodeTypeStorage, core.RelationshipHasAccessTo, ""}, true
	case role == "roles/pubsub.publisher":
		return iamAccessTarget{core.NodeTypeTopic, core.RelationshipPublishesTo, ""}, true
	case role == "roles/pubsub.subscriber":
		return iamAccessTarget{core.NodeTypeTopic, core.RelationshipSubscribesTo, ""}, true
	case role == "roles/pubsub.editor", role == "roles/pubsub.admin", role == "roles/pubsub.viewer":
		return iamAccessTarget{core.NodeTypeTopic, core.RelationshipHasAccessTo, ""}, true
	}
	return iamAccessTarget{}, false
}

// gcpDatabaseMatchesConstraint reports whether a Database node's properties["type"]
// satisfies an iamAccessTarget.dbConstraint.
func gcpDatabaseMatchesConstraint(dbType, constraint string) bool {
	switch constraint {
	case "firestore":
		return strings.EqualFold(dbType, "firestore")
	case "bigquery-dataset":
		return strings.HasSuffix(strings.ToLower(dbType), "/dataset")
	}
	return true
}

// createHasAccessEdges turns project IAM policy binding rows into edges from a
// ServiceIdentity (the granted service account) to the data resources its roles
// permit: Firestore/BigQuery datasets/Storage buckets (HAS_ACCESS_TO) and Pub/Sub
// topics (PUBLISHES_TO / SUBSCRIBES_TO). Combined with RUNS_AS (service→identity)
// this links each service to the data tier — the dependency traces alone can't
// see. Binding rows for identities without a ServiceIdentity node in this account
// (cross-project grantees) or roles outside gcpIAMRoleTarget are skipped.
func (s *GCPSource) createHasAccessEdges(resources []sources.CloudResourceRow, lookup *sources.NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	// Candidate cache per (nodeType|dbConstraint) so a project's node set is
	// filtered once, not per binding.
	candCache := make(map[string][]*core.DbNode)
	candidates := func(t iamAccessTarget) []*core.DbNode {
		key := string(t.nodeType) + "|" + t.dbConstraint
		if c, ok := candCache[key]; ok {
			return c
		}
		var out []*core.DbNode
		for _, n := range lookup.ByNodeType[t.nodeType] {
			if t.nodeType == core.NodeTypeDatabase {
				dbType, _ := n.Properties["type"].(string)
				if !gcpDatabaseMatchesConstraint(dbType, t.dbConstraint) {
					continue
				}
			}
			out = append(out, n)
		}
		candCache[key] = out
		return out
	}

	seen := make(map[string]bool) // (saID|targetID|rel) dedup across overlapping roles
	for i := range resources {
		email, role, target, ok := gcpIAMAccessBinding(&resources[i])
		if !ok {
			continue
		}
		saNode := findNodeByNameAndType(lookup, core.NodeTypeServiceIdentity, email)
		if saNode == nil {
			continue // cross-project grantee without a ServiceIdentity node here
		}
		cands := candidates(target)
		if len(cands) == 0 {
			continue
		}
		if len(cands) > maxIAMAccessFanout {
			s.logger.Info("skipping broad IAM grant (fan-out cap)",
				"identity", email, "role", role, "node_type", target.nodeType, "candidates", len(cands))
			continue
		}
		edges = append(edges, s.hasAccessEdgesFor(saNode, cands, target, role, seen, req)...)
	}
	s.logger.Info("created GCP IAM access edges", "edge_count", len(edges))
	return edges
}

// gcpIAMAccessBinding extracts a mapped (identity email, role, target) from an IAM
// policy binding row, or ok=false when the row is not a data-tier grant we model.
func gcpIAMAccessBinding(r *sources.CloudResourceRow) (email, role string, target iamAccessTarget, ok bool) {
	if !strings.EqualFold(r.ServiceName, gcpIAMPolicyServiceName) && !strings.EqualFold(r.Type, gcpIAMBindingType) {
		return "", "", iamAccessTarget{}, false
	}
	var meta map[string]interface{}
	if err := json.Unmarshal(r.Meta, &meta); err != nil {
		return "", "", iamAccessTarget{}, false
	}
	role, _ = meta["role"].(string)
	email, _ = meta["member"].(string)
	if role == "" || email == "" {
		return "", "", iamAccessTarget{}, false
	}
	target, ok = gcpIAMRoleTarget(role)
	if !ok {
		return "", "", iamAccessTarget{}, false
	}
	return email, role, target, true
}

// hasAccessEdgesFor emits deduplicated access edges from one identity to every
// candidate data node, recording provenance (role) on each.
func (s *GCPSource) hasAccessEdgesFor(saNode *core.DbNode, cands []*core.DbNode, target iamAccessTarget, role string, seen map[string]bool, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0, len(cands))
	for _, tn := range cands {
		dk := saNode.ID + "|" + tn.ID + "|" + string(target.rel)
		if seen[dk] {
			continue
		}
		seen[dk] = true
		edges = append(edges, s.createEdge(saNode, tn, target.rel,
			map[string]interface{}{"evidence": "iam_policy", "granted_role": role}, req))
	}
	return edges
}

// createRunsAsEdges links a compute node to the ServiceIdentity it runs as, matching
// the runtime service-account email (collected into meta.service_account) to the
// ServiceIdentity node keyed on that email.
func (s *GCPSource) createRunsAsEdges(lookup *sources.NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)
	srvNodes, ok := lookup.ByNodeType[core.NodeTypeServerlessFunction]
	if !ok {
		return edges
	}
	for _, node := range srvNodes {
		meta, _ := node.Properties["meta"].(map[string]interface{})
		if meta == nil {
			continue
		}
		sa, _ := meta["service_account"].(string)
		if sa == "" {
			continue
		}
		if saNode := findNodeByNameAndType(lookup, core.NodeTypeServiceIdentity, sa); saNode != nil {
			edges = append(edges, s.createEdge(node, saNode, core.RelationshipRunsAs,
				map[string]interface{}{"connection_type": "service_account"}, req))
		}
	}
	s.logger.Info("created GCP RUNS_AS edges", "edge_count", len(edges))
	return edges
}

// createServiceIdentityFromGCPSA builds a typed ServiceIdentity node from a
// GCP IAM ServiceAccount row. Mirrors aws_source.go:createServiceIdentityFromIAMUser.
//
// Unique key shape: gcp:{accountID}:global:ServiceIdentity:GCPServiceAccount:{email}.
// The 5th segment (hierarchy) carries the subtype "GCPServiceAccount" so the key
// is non-colliding with any future GCP identity type (e.g. workforce identities).
// Email is used as the name because it's the value K8s SAs reference via the
// `iam.gke.io/gcp-service-account` Workload Identity annotation — matching by
// email is the cross-account rule's join key.
func (s *GCPSource) createServiceIdentityFromGCPSA(resource *sources.CloudResourceRow, req *core.SourceBuildRequest) *core.DbNode {
	properties := map[string]interface{}{
		"name":           resource.Name, // SA email
		"email":          resource.Name, // explicit alias for the cross-account matcher
		"arn":            resource.ARN,  // projects/{p}/serviceAccounts/{email}
		"cloud_provider": "GCP",
		"region":         "global",
		"subtype":        "GCPServiceAccount",
		"specific_type":  "GCPServiceAccount",
		"service_name":   "GCPIAM",
		"nb_account_id":  req.CloudAccountID,
		"resource_id":    resource.ResourceID,
		"is_active":      resource.IsActive,
	}

	// Hoist meta fields downstream consumers care about. The full meta is
	// also stored under properties["meta"] so nothing is lost.
	if len(resource.Meta) > 0 && string(resource.Meta) != "{}" {
		var metaMap map[string]interface{}
		if err := json.Unmarshal(resource.Meta, &metaMap); err == nil && len(metaMap) > 0 {
			properties["meta"] = metaMap
			if display, ok := metaMap["display_name"].(string); ok && display != "" {
				properties["display_name"] = display
			}
			if disabled, ok := metaMap["disabled"].(bool); ok {
				properties["disabled"] = disabled
			}
			if uniqueID, ok := metaMap["unique_id"].(string); ok && uniqueID != "" {
				properties["unique_id"] = uniqueID
			}
			if projectID, ok := metaMap["project_id"].(string); ok && projectID != "" {
				properties["gcp_project_id"] = projectID
			}
		}
	}

	uniqueKey := fmt.Sprintf("gcp:%s:global:ServiceIdentity:GCPServiceAccount:%s", req.CloudAccountID, resource.Name)

	return core.NewNode(
		core.NodeTypeServiceIdentity,
		uniqueKey,
		properties,
		req.TenantID,
		req.CloudAccountID,
		"gcp",
	)
}
