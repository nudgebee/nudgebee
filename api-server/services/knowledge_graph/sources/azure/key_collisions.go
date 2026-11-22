package azure

import (
	"sort"
	"strings"

	"nudgebee/services/knowledge_graph/core"
)

// maxCollidedResourceIDs caps the ids stamped on a collided node.
const maxCollidedResourceIDs = 10

// markKeyCollisions stamps the nodes whose unique key is shared by different
// Azure resources, before DeduplicateNodes merges them into one, and returns how
// many keys collided.
//
// Two nodes for the same resource sharing a key is expected (a subnet read from
// its VNet's metadata and its own inventory row). Two resources sharing one is a
// gap in GenerateUniqueKey: one of them silently disappears from the graph. The
// stamp makes that visible on the surviving node: `key_collisions` (how many
// resources share the key) and `collided_resource_ids`.
func markKeyCollisions(nodes []*core.DbNode) int {
	byKey := make(map[string][]*core.DbNode, len(nodes))
	for _, n := range nodes {
		byKey[n.UniqueKey] = append(byKey[n.UniqueKey], n)
	}

	collided := 0
	for _, group := range byKey {
		if len(group) < 2 {
			continue
		}
		seen := map[string]bool{}
		var ids []string
		for _, n := range group {
			// The Azure id names the resource; a row that stores none is told
			// apart by its inventory row id instead.
			id, _ := core.GetNodePropertyString(n, "resource_id")
			id = strings.ToLower(id)
			if id == "" {
				id, _ = core.GetNodePropertyString(n, "nb_resource_id")
			}
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			ids = append(ids, id)
		}
		if len(ids) < 2 {
			continue
		}
		collided++
		sort.Strings(ids)
		count := len(ids)
		if len(ids) > maxCollidedResourceIDs {
			ids = ids[:maxCollidedResourceIDs]
		}
		for _, n := range group {
			// A group can hold a node skipped above for carrying no id, and
			// nothing guarantees such a node has a property map.
			if n.Properties == nil {
				n.Properties = map[string]interface{}{}
			}
			n.Properties["key_collisions"] = count
			n.Properties["collided_resource_ids"] = ids
		}
	}
	return collided
}
