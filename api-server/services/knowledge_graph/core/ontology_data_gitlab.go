package core

// GitLab ontology field mappings — how each concrete GitLab specific_type populates
// the cross-cloud-normalized ontology_attributes for its NodeType. See
// ontology_data_github.go for the same pattern applied to GitHub;
// TestOntologyFieldsAreCanonical enforces that every OntologyField here is in
// canonicalOntologyVocab for its NodeType.
//
// NodeField keys are the raw properties written at node creation time by
// sources/gitlab/{orgs,repos,teams,users}.go.

func init() { registerOntology(gitlabOntology) }

var gitlabOntology = map[string]OntologyNodeSpec{
	"GitLabOrganization": {NodeType: NodeTypeSourceControlOrg, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "type", NodeField: "type"}, // "Group" or "User" (personal-namespace fallback)
		{OntologyField: "url", NodeField: "url"},
	}},
	"GitLabGroup": {NodeType: NodeTypeUserGroup, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "url", NodeField: "url"},
	}},
	"GitLabProject": {NodeType: NodeTypeRepository, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "type", NodeField: "language"},
		{OntologyField: "url", NodeField: "url"},
	}},
	"GitLabUser": {NodeType: NodeTypeUserAccount, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "username", NodeField: "username"},
		{OntologyField: "email", NodeField: "email"},
		{OntologyField: "url", NodeField: "url"},
	}},
}
