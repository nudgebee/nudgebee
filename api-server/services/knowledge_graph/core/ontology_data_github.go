package core

// GitHub ontology field mappings — how each concrete GitHub specific_type populates
// the cross-cloud-normalized ontology_attributes for its NodeType. See
// ontology_data_aws.go / ontology_data_gcp.go / ontology_data_azure.go / ontology_data_k8s.go
// for the same pattern applied to cloud/k8s concepts; TestOntologyFieldsAreCanonical
// enforces that every OntologyField here is in canonicalOntologyVocab for its NodeType.
//
// NodeField keys are the raw properties written at node creation time by
// sources/github/{orgs,repos,teams,users}.go.

func init() { registerOntology(githubOntology) }

var githubOntology = map[string]OntologyNodeSpec{
	"GitHubOrganization": {NodeType: NodeTypeSourceControlOrg, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "type", NodeField: "type"}, // "Organization" or "User" (personal-account owner)
		{OntologyField: "url", NodeField: "url"},
	}},
	"GitHubRepository": {NodeType: NodeTypeRepository, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "type", NodeField: "language"},
		{OntologyField: "url", NodeField: "url"},
	}},
	"GitHubTeam": {NodeType: NodeTypeUserGroup, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "url", NodeField: "url"},
	}},
	"GitHubUser": {NodeType: NodeTypeUserAccount, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "username", NodeField: "username"},
		{OntologyField: "email", NodeField: "email"},
		{OntologyField: "url", NodeField: "url"},
	}},
}
