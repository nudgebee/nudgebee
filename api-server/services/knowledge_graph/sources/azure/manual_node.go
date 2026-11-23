package azure

import (
	"encoding/json"

	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
)

// Declaring Azure resources by hand: see sources/manual_node.go. An Azure key
// reads the name, the region and the resource group (see GenerateUniqueKey), so
// every declarable type asks for all three. A resource inside another one is
// keyed by its path below the top-level resource, so its name is typed that way.

const azureManualNameHint = "The resource name as Azure shows it; for a resource inside another, its resource id path below the parent resource, e.g. sqlsrv1/databases/orders"

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
			SpecificType:  k.specificType(row),
			NodeType:      k.NodeType,
			RowType:       row.Type,
			ServiceName:   row.ServiceName,
			NameHint:      azureManualNameHint,
			ResourceGroup: true,
		}
		if row.Type != "" {
			exact = append(exact, e)
		} else {
			fallback = append(fallback, e)
		}
	}
	return append(exact, fallback...)
}

// azureManualRowMeta puts the declared resource group where the classifier
// reads it from an inventory row's Resource Graph meta.
func azureManualRowMeta(in core.ManualNodeInput) []byte {
	if in.ResourceGroup == "" {
		return []byte("{}")
	}
	meta, _ := json.Marshal(map[string]string{"resourceGroup": in.ResourceGroup})
	return meta
}

func init() {
	src, _ := NewAzureSource(AzureSourceConfig{}, nil)
	core.RegisterManualNodeBuilder("Azure", &sources.CloudManualNodeBuilder{
		CloudProvider: "Azure",
		Candidates:    azureManualCandidates,
		BuildRow:      src.createNodeFromResource,
		RowMeta:       azureManualRowMeta,
	})
}
