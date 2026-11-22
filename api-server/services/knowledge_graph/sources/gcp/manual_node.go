package gcp

import (
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
)

// Declaring GCP resources by hand: see sources/manual_node.go. A GCP key reads
// only the name and region (its hierarchy is derived from the name), so no
// type needs a network.

func gcpManualCandidates() []sources.ManualNodeEntry {
	var exact, fallback []sources.ManualNodeEntry
	for _, k := range kinds.Kinds() {
		if k.Node != nil {
			continue
		}
		e := sources.ManualNodeEntry{
			SpecificType: k.specificType(),
			NodeType:     k.NodeType,
			ServiceName:  k.Service,
			NameHint:     "The short resource name, not the projects/… path",
		}
		if len(k.Types) > 0 {
			e.RowType = k.Types[0]
			exact = append(exact, e)
		} else {
			fallback = append(fallback, e)
		}
	}
	return append(exact, fallback...)
}

func init() {
	src, _ := NewGCPSource(GCPSourceConfig{}, nil)
	core.RegisterManualNodeBuilder("GCP", &sources.CloudManualNodeBuilder{
		CloudProvider: "GCP",
		Candidates:    gcpManualCandidates,
		BuildRow:      src.createNodeFromResource,
	})
}
