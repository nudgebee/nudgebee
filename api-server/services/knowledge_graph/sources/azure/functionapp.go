package azure

import (
	"encoding/json"
	"strings"

	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
)

// azureFunctionAppSchema is the concrete per-specific_type property schema for
// the AzureFunctionApp specific_type. The Azure source runs no dedicated
// function-app extractor (runtime/memory unavailable), so beyond the universal
// base keys only resource_group is populated. Registered to satisfy specific_type
// coverage.
var azureFunctionAppSchema = core.SpecificTypeSchema{
	SpecificType: "AzureFunctionApp",
	NodeType:     core.NodeTypeServerlessFunction,
	Properties: []core.PropertyDef{
		{Name: "resource_group", Indexed: true},
		// Additional provider fields (not yet emitted by our extractor). `state`
		// is a universal base key and therefore not repeated here.
		{Name: "kind"},
		{Name: "default_host_name"},
		{Name: "https_only"},
		{Name: "is_container"},
		{Name: "deployment_type"},
		{Name: "image_uri"},
		{Name: "image_digest"},
		{Name: "architecture_normalized"},
	},
}

// azureAppServiceSchema, azureLogicAppSchema and azureAppServicePlanSchema are
// the property schemas for the other Microsoft.Web rows: web apps, Logic Apps and
// the plans they run on. Like function apps they get no dedicated extractor.
var azureAppServiceSchema = core.SpecificTypeSchema{
	SpecificType: "AzureAppService",
	NodeType:     core.NodeTypeServerlessFunction,
	Properties: []core.PropertyDef{
		{Name: "resource_group", Indexed: true},
		{Name: "kind"},
		{Name: "default_host_name"},
		{Name: "https_only"},
	},
}

var azureLogicAppSchema = core.SpecificTypeSchema{
	SpecificType: "AzureLogicApp",
	NodeType:     core.NodeTypeServerlessFunction,
	Properties: []core.PropertyDef{
		{Name: "resource_group", Indexed: true},
		{Name: "kind"},
	},
}

var azureAppServicePlanSchema = core.SpecificTypeSchema{
	SpecificType: "AzureAppServicePlan",
	NodeType:     core.NodeTypeComputeInstancePool,
	Properties: []core.PropertyDef{
		{Name: "resource_group", Indexed: true},
		{Name: "kind"},
		{Name: "sku"},
		{Name: "node_count"},
	},
}

func init() {
	core.RegisterSpecificTypeSchema(azureFunctionAppSchema)
	core.RegisterSpecificTypeSchema(azureAppServiceSchema)
	core.RegisterSpecificTypeSchema(azureLogicAppSchema)
	core.RegisterSpecificTypeSchema(azureAppServicePlanSchema)
}

// azureSiteLabel tells apart what Azure keeps under Microsoft.Web/sites: web
// apps, function apps and Logic Apps (Standard), which differ only in the row's
// kind ("app", "functionapp,linux", "functionapp,workflowapp"). A row with no
// kind could be any of them, so it gets the umbrella App Service label.
func azureSiteLabel(resource *sources.CloudResourceRow) string {
	var meta struct {
		Kind string `json:"kind"`
	}
	_ = json.Unmarshal(resource.Meta, &meta)
	kind := strings.ToLower(meta.Kind)
	switch {
	case strings.Contains(kind, "workflowapp"):
		return "AzureLogicApp"
	case strings.Contains(kind, "functionapp"):
		return "AzureFunctionApp"
	}
	return "AzureAppService"
}

// The inventory rows this file's resources claim: what each row's node
// type, concrete label and metadata extraction are (see kinds.go). Only these
// rows are claimed: anything else under Microsoft.Web or Microsoft.Logic
// (certificates, connections, integration accounts) is not a function and
// stays a CloudResource.
func init() {
	registerKind(resourceKind{
		Types:        []string{"functionapp", "functions"},
		NodeType:     core.NodeTypeServerlessFunction,
		SpecificType: "AzureFunctionApp",
	})
	registerKind(resourceKind{
		Types:    []string{"sites"},
		NodeType: core.NodeTypeServerlessFunction,
		Label:    azureSiteLabel,
	})
	registerKind(resourceKind{
		Types:        []string{"workflows"},
		NodeType:     core.NodeTypeServerlessFunction,
		SpecificType: "AzureLogicApp",
	})
	registerKind(resourceKind{
		Types:        []string{"serverfarms"},
		NodeType:     core.NodeTypeComputeInstancePool,
		SpecificType: "AzureAppServicePlan",
	})
}
