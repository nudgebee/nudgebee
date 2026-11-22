package core

// Self-hosted VM ontology field mappings — how a host scanned through a VM
// account populates the cross-cloud-normalized ComputeInstance fields, mapped
// the same way as EC2Instance (ontology_data_aws.go). TestOntologyFieldsAreCanonical
// enforces that every OntologyField here is in canonicalOntologyVocab.
//
// NodeField keys are the raw properties written by sources/selfhosted.

// SpecificTypeSelfHostedVM is the concrete type of a host scanned through a
// self-hosted VM account. Declared here, not in sources/selfhosted, because
// core resolves these nodes by it (ResolveNodeByProviderIdentifier).
const SpecificTypeSelfHostedVM = "SelfHostedVM"

func init() { registerOntology(selfhostedOntology) }

var selfhostedOntology = map[string]OntologyNodeSpec{
	SpecificTypeSelfHostedVM: {NodeType: NodeTypeComputeInstance, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "region", NodeField: "region"},
		{OntologyField: "public_ip_address", NodeField: "public_ip"},
		{OntologyField: "private_ip_address", NodeField: "private_ip"},
	}},
}
