package azure

import (
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
)

// Declaring Azure resources by hand: see sources/manual_node.go. An Azure key
// reads the name and region, plus a VNet only when the row's metadata names
// one, which the declarable types' inventory rows do not.

func azureManualCandidates() []sources.ManualNodeEntry {
	var exact, fallback []sources.ManualNodeEntry
	for _, k := range kinds.Kinds() {
		row := &sources.CloudResourceRow{ServiceName: k.Service}
		if len(k.Types) > 0 {
			row.Type = k.Types[0]
		}
		e := sources.ManualNodeEntry{
			// Some kinds read their label off the row's service (Cosmos DB vs
			// SQL), so the label is the one this exact row shape gets.
			SpecificType: k.specificType(row),
			NodeType:     k.NodeType,
			RowType:      row.Type,
			ServiceName:  row.ServiceName,
		}
		if row.Type != "" {
			exact = append(exact, e)
		} else {
			fallback = append(fallback, e)
		}
	}
	return append(exact, fallback...)
}

func init() {
	src, _ := NewAzureSource(AzureSourceConfig{}, nil)
	core.RegisterManualNodeBuilder("Azure", &sources.CloudManualNodeBuilder{
		CloudProvider: "Azure",
		Candidates:    azureManualCandidates,
		BuildRow:      src.createNodeFromResource,
	})
}
