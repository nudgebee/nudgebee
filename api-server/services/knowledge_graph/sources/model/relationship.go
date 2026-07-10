// Package model is the declarative graph-building engine for Knowledge Graph
// sources — a declarative property/relationship mapping resolved at build time.
//
// A resource type declares its edges as data (RelSpec) instead of a hand-written
// edge builder. The generic edge builder here resolves each RelSpec against an
// in-memory NodeIndex (property-based node matching) and emits core.DbEdge
// values via a caller-supplied EdgeFactory.
//
// It depends only on core so it can be imported by every cloud subpackage without
// an import cycle; the concrete NodeLookup in package sources implements NodeIndex.
package model

import "nudgebee/services/knowledge_graph/core"

// LookupKind selects how a RelSpec resolves its target node in the NodeIndex.
type LookupKind int

const (
	// ByResourceID matches on properties["resource_id"] (the common cloud case).
	ByResourceID LookupKind = iota
	// ByResourceIDLower matches on the lower-cased resource id (Azure ids are
	// case-inconsistent; the lookup is built lower-cased).
	ByResourceIDLower
	// ByNameAndType matches on (TargetType, properties["name"]).
	ByNameAndType
)

// PropShape describes the Go type of the source property a RelSpec reads.
type PropShape int

const (
	// Scalar: a single string id/name.
	Scalar PropShape = iota
	// StringSlice: []string of ids/names.
	StringSlice
	// IfaceSlice: []interface{} whose elements are strings.
	IfaceSlice
	// MapSliceSubKey: []interface{} whose elements are maps; the target id is
	// element[SubKey] and ExtraProps may pull additional edge properties from
	// other keys of the same element.
	MapSliceSubKey
)

// RelSpec is a declarative edge rule — a target-node matcher plus direction and
// rel_label, resolved against a NodeIndex. One source node can produce zero or
// more edges from a single RelSpec (one per resolved target).
type RelSpec struct {
	// Key is the property on the SOURCE node holding the target id/name.
	Key string
	// Shape is the Go type of that property.
	Shape PropShape
	// SubKey is the map key holding the id when Shape == MapSliceSubKey.
	SubKey string
	// Lookup selects the NodeIndex resolution strategy.
	Lookup LookupKind
	// TargetType is the target NodeType when Lookup == ByNameAndType.
	TargetType core.NodeType
	// RelType is the emitted relationship type.
	RelType core.RelationshipType
	// Reverse emits target->source instead of source->target.
	Reverse bool
	// Props are static edge properties copied onto every emitted edge (a fresh
	// map per edge, so callers never share a mutated map).
	Props map[string]interface{}
	// ExtraProps pulls extra edge properties from a MapSliceSubKey element:
	// edgePropName -> element key. Only consulted for MapSliceSubKey.
	ExtraProps map[string]string
	// Guard, when set, skips the source node if it returns false.
	Guard func(n *core.DbNode) bool
	// StopAtFirst emits at most one edge for this RelSpec (mirrors edge builders
	// that `break` after the first match, e.g. EC2->EKS by cluster name).
	StopAtFirst bool
}

// NodeIndex is the resolution surface a RelSpec needs. sources.NodeLookup
// implements it. Kept minimal so model imports only core. Methods are named
// Lookup* so the implementing type can keep exported By* index fields of the
// same concept without a name collision.
type NodeIndex interface {
	LookupResourceID(id string) (*core.DbNode, bool)
	LookupResourceIDLower(id string) (*core.DbNode, bool)
	LookupTypeAndName(nt core.NodeType, name string) []*core.DbNode
}

// EdgeFactory builds a concrete edge. Each cloud passes a closure that calls
// core.NewEdge with its source string, tenant, and account — so the engine stays
// free of source-specific stamping.
type EdgeFactory func(src, dst *core.DbNode, rel core.RelationshipType, props map[string]interface{}) *core.DbEdge

// BuildEdges applies every RelSpec to every node and returns the resolved edges,
// in node-major then rel-major then target order — matching the hand-written
// builders' emission order.
func BuildEdges(nodes []*core.DbNode, rels []RelSpec, idx NodeIndex, mk EdgeFactory) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)
	for _, node := range nodes {
		for i := range rels {
			edges = append(edges, applyRel(node, &rels[i], idx, mk)...)
		}
	}
	return edges
}

// applyRel resolves a single RelSpec on a single source node.
func applyRel(node *core.DbNode, rel *RelSpec, idx NodeIndex, mk EdgeFactory) []*core.DbEdge {
	if rel.Guard != nil && !rel.Guard(node) {
		return nil
	}
	out := make([]*core.DbEdge, 0)
	emit := func(target *core.DbNode, extra map[string]interface{}) bool {
		if target == nil {
			return false
		}
		props := mergeProps(rel.Props, extra)
		src, dst := node, target
		if rel.Reverse {
			src, dst = target, node
		}
		if e := mk(src, dst, rel.RelType, props); e != nil {
			out = append(out, e)
		}
		return true
	}

	switch rel.Shape {
	case Scalar:
		id, _ := node.Properties[rel.Key].(string)
		if id == "" {
			return nil
		}
		if t := resolveTarget(rel, idx, id); t != nil {
			emit(t, nil)
		}
	case StringSlice:
		ids, _ := node.Properties[rel.Key].([]string)
		for _, id := range ids {
			if id == "" {
				continue
			}
			if t := resolveTarget(rel, idx, id); t != nil {
				if emit(t, nil) && rel.StopAtFirst {
					break
				}
			}
		}
	case IfaceSlice:
		raw, _ := node.Properties[rel.Key].([]interface{})
		for _, v := range raw {
			id, _ := v.(string)
			if id == "" {
				continue
			}
			if t := resolveTarget(rel, idx, id); t != nil {
				if emit(t, nil) && rel.StopAtFirst {
					break
				}
			}
		}
	case MapSliceSubKey:
		raw, _ := node.Properties[rel.Key].([]interface{})
		for _, v := range raw {
			m, ok := v.(map[string]interface{})
			if !ok {
				continue
			}
			id, _ := m[rel.SubKey].(string)
			if id == "" {
				continue
			}
			if t := resolveTarget(rel, idx, id); t != nil {
				var extra map[string]interface{}
				if len(rel.ExtraProps) > 0 {
					extra = make(map[string]interface{}, len(rel.ExtraProps))
					for edgeKey, srcKey := range rel.ExtraProps {
						extra[edgeKey] = m[srcKey]
					}
				}
				if emit(t, extra) && rel.StopAtFirst {
					break
				}
			}
		}
	}
	return out
}

// resolveTarget picks the first matching node for a RelSpec's lookup strategy.
func resolveTarget(rel *RelSpec, idx NodeIndex, id string) *core.DbNode {
	switch rel.Lookup {
	case ByResourceID:
		if n, ok := idx.LookupResourceID(id); ok {
			return n
		}
	case ByResourceIDLower:
		if n, ok := idx.LookupResourceIDLower(id); ok {
			return n
		}
	case ByNameAndType:
		if ns := idx.LookupTypeAndName(rel.TargetType, id); len(ns) > 0 {
			return ns[0]
		}
	}
	return nil
}

// mergeProps returns a fresh property map combining a RelSpec's static Props with
// any per-target extras. A new map per edge guarantees callers can't observe a
// shared/mutated map (the hand-written builders allocate a literal per edge).
func mergeProps(static, extra map[string]interface{}) map[string]interface{} {
	props := make(map[string]interface{}, len(static)+len(extra))
	for k, v := range static {
		props[k] = v
	}
	for k, v := range extra {
		props[k] = v
	}
	return props
}
