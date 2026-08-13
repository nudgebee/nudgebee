package aws

import "nudgebee/services/knowledge_graph/core"

// Concrete schemas for AWS specific_types that have no dedicated source module or
// per-type extractor: they are resolved purely via awsServiceSpecificTypeFallback
// (specific_type.go) and land on a generic NodeType with base + common properties
// only. Each still needs a registered SpecificTypeSchema so the coverage guard and
// query_attributes wiring have an entry; the per-NodeType QueryablePropertiesMap
// fallback continues to drive their filtering.

// secretsManagerSecretSchema — Secrets Manager secrets (NodeTypeSecretVault).
// No resource-specific fields are lifted.
var secretsManagerSecretSchema = core.SpecificTypeSchema{
	SpecificType: "SecretsManagerSecret",
	NodeType:     core.NodeTypeSecretVault,
	Properties: []core.PropertyDef{
		// Additional provider fields (not yet emitted by our extractor):
		{Name: "description"},
		{Name: "rotation_enabled"},
		{Name: "rotation_lambda_arn"},
		{Name: "rotation_rules_automatically_after_days"},
		{Name: "created_date"},
		{Name: "last_rotated_date"},
		{Name: "last_changed_date"},
		{Name: "last_accessed_date"},
		{Name: "deleted_date"},
		{Name: "kms_key_id"},
		{Name: "owning_service"},
		{Name: "primary_region"},
	},
}

// The following roll up to the generic NodeTypeCloudResource ontology. They carry
// only base properties (type/region/state), so no resource-specific PropertyDefs.
var acmCertificateSchema = core.SpecificTypeSchema{
	SpecificType: "ACMCertificate",
	NodeType:     core.NodeTypeCloudResource,
	Properties: []core.PropertyDef{
		// Additional provider fields (not yet emitted by our extractor):
		{Name: "domain_name"},
		{Name: "key_algorithm"},
		{Name: "signature_algorithm"},
		{Name: "not_before"},
		{Name: "not_after"},
		{Name: "in_use_by"},
	},
}

var sageMakerResourceSchema = core.SpecificTypeSchema{
	SpecificType: "SageMakerResource",
	NodeType:     core.NodeTypeCloudResource,
	Properties:   []core.PropertyDef{},
}

var kinesisFirehoseStreamSchema = core.SpecificTypeSchema{
	SpecificType: "KinesisFirehoseStream",
	NodeType:     core.NodeTypeCloudResource,
	Properties:   []core.PropertyDef{},
}

var xRayResourceSchema = core.SpecificTypeSchema{
	SpecificType: "XRayResource",
	NodeType:     core.NodeTypeCloudResource,
	Properties:   []core.PropertyDef{},
}

func init() {
	core.RegisterSpecificTypeSchema(secretsManagerSecretSchema)
	core.RegisterSpecificTypeSchema(acmCertificateSchema)
	core.RegisterSpecificTypeSchema(sageMakerResourceSchema)
	core.RegisterSpecificTypeSchema(kinesisFirehoseStreamSchema)
	core.RegisterSpecificTypeSchema(xRayResourceSchema)
}
