package sources

import (
	"encoding/json"

	"nudgebee/services/knowledge_graph/core"
)

// This file holds the types shared by every cloud source (AWS, GCP, Azure, K8s):
// the raw DB row they read from `cloud_resourses`, and the in-memory node index
// used for edge resolution. They live here (rather than in aws_source.go, where
// they were originally defined) so the per-cloud subpackages can share them
// during the resource-module refactor. NodeLookup's fields and constructor are
// exported for exactly that reason — the cloud subpackages read/mutate them.

// CloudResourceRow is one row of the `cloud_resourses` table — the denormalized
// per-resource record all cloud sources read. (The `resourse_id` column name is a
// pre-existing DB typo, preserved in the db tag.)
type CloudResourceRow struct {
	ID                 string          `db:"id"`
	ResourceID         string          `db:"resourse_id"` // Note: typo in DB column name
	Name               string          `db:"name"`
	Type               string          `db:"type"`
	Status             string          `db:"status"`
	Account            string          `db:"account"` // nb_account_id (cloud_accounts.id)
	Tenant             string          `db:"tenant"`
	CloudProvider      string          `db:"cloud_provider"`
	Region             string          `db:"region"`
	ARN                string          `db:"arn"`
	Tags               json.RawMessage `db:"tags"`
	Meta               json.RawMessage `db:"meta"`
	ServiceName        string          `db:"service_name"`
	IsActive           bool            `db:"is_active"`
	ExternalResourceID string          `db:"external_resource_id"`
	AccountNumber      string          `db:"account_number"` // aws_account_id (cloud_accounts.account_number)
}

// NodeLookup provides efficient lookup of nodes by various identifiers. Edge
// builders resolve their targets against these indexes; the declarative engine
// reaches them through the model.NodeIndex adapter in nodelookup_index.go.
//
// Fields are exported so per-cloud subpackages can both read them and mutate them
// post-construction (e.g. appending late-registered ServiceIdentity nodes).
type NodeLookup struct {
	ByResourceID      map[string]*core.DbNode                     // resource_id -> node
	ByNodeType        map[core.NodeType][]*core.DbNode            // nodeType -> nodes
	ByARN             map[string]*core.DbNode                     // arn -> node
	ByNodeTypeAndName map[core.NodeType]map[string][]*core.DbNode // (nodeType, name) -> nodes — O(1) name lookup
}

// NewNodeLookup creates lookup maps from a list of nodes.
func NewNodeLookup(nodes []*core.DbNode) *NodeLookup {
	lookup := &NodeLookup{
		ByResourceID:      make(map[string]*core.DbNode),
		ByNodeType:        make(map[core.NodeType][]*core.DbNode),
		ByARN:             make(map[string]*core.DbNode),
		ByNodeTypeAndName: make(map[core.NodeType]map[string][]*core.DbNode),
	}

	for _, node := range nodes {
		// Index by resource_id
		if resourceID, ok := node.Properties["resource_id"].(string); ok && resourceID != "" {
			lookup.ByResourceID[resourceID] = node
		}

		// Index by node type
		lookup.ByNodeType[node.NodeType] = append(lookup.ByNodeType[node.NodeType], node)

		// Index by ARN
		if arn, ok := node.Properties["arn"].(string); ok && arn != "" {
			lookup.ByARN[arn] = node
		}

		// Index by (nodeType, name) for O(1) name-based lookups
		if name, ok := node.Properties["name"].(string); ok && name != "" {
			if lookup.ByNodeTypeAndName[node.NodeType] == nil {
				lookup.ByNodeTypeAndName[node.NodeType] = make(map[string][]*core.DbNode)
			}
			lookup.ByNodeTypeAndName[node.NodeType][name] = append(lookup.ByNodeTypeAndName[node.NodeType][name], node)
		}
	}

	return lookup
}

// GetNodesByTypeAndName returns nodes of a given type with a matching "name" property, in O(1).
func (l *NodeLookup) GetNodesByTypeAndName(nodeType core.NodeType, name string) []*core.DbNode {
	if nameMap, ok := l.ByNodeTypeAndName[nodeType]; ok {
		return nameMap[name]
	}
	return nil
}
