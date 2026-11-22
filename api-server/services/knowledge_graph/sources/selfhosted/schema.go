package selfhosted

import "nudgebee/services/knowledge_graph/core"

// selfHostedVMSchema is the concrete property schema of SelfHostedVM nodes.
// Every field is written by ConvertHostRows and owned by this source; all are
// Indexed so the scan summary can be filtered on (kg_list_nodes).
var selfHostedVMSchema = core.SpecificTypeSchema{
	SpecificType: SpecificTypeSelfHostedVM,
	NodeType:     core.NodeTypeComputeInstance,
	Properties: []core.PropertyDef{
		{Name: "nb_resource_id", Indexed: true, Required: true},
		{Name: "host_kind", Indexed: true, Required: true},
		{Name: "instance_provider", Indexed: true},
		{Name: "instance_id", Indexed: true},
		{Name: "private_ip", Indexed: true},
		{Name: "public_ip", Indexed: true},
		{Name: "os_family", Indexed: true},
		{Name: "os_version", Indexed: true},
		{Name: "package_count", Indexed: true},
		{Name: "finding_count", Indexed: true},
		{Name: "scan_status", Indexed: true},
		{Name: "scanned_at", Indexed: true, Required: true},
	},
}

func init() { core.RegisterSpecificTypeSchema(selfHostedVMSchema) }
