package sources

import (
	"strings"

	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources/model"
)

// NodeLookup implements model.NodeIndex so the declarative edge engine can
// resolve RelSpec targets against the same in-memory indexes the hand-written
// edge builders use. These adapters carry no logic of their own — they forward
// to the exported maps/helpers. They are named Lookup* (not By*) so they don't
// collide with the exported NodeLookup fields of the same concept.
var _ model.NodeIndex = (*NodeLookup)(nil)

// LookupResourceID resolves a node by its properties["resource_id"].
func (l *NodeLookup) LookupResourceID(id string) (*core.DbNode, bool) {
	n, ok := l.ByResourceID[id]
	return n, ok
}

// LookupResourceIDLower resolves by the lower-cased resource id — mirrors the
// Azure edge builders, which look up `ByResourceID[strings.ToLower(id)]` because
// Azure resource ids are case-inconsistent.
func (l *NodeLookup) LookupResourceIDLower(id string) (*core.DbNode, bool) {
	n, ok := l.ByResourceID[strings.ToLower(id)]
	return n, ok
}

// LookupTypeAndName forwards to the existing O(1) (nodeType, name) index.
func (l *NodeLookup) LookupTypeAndName(nt core.NodeType, name string) []*core.DbNode {
	return l.GetNodesByTypeAndName(nt, name)
}
