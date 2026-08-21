package core

// PagerDuty ontology field mappings — how each concrete PagerDuty specific_type
// populates the cross-cloud-normalized ontology_attributes for its NodeType. See
// ontology_data_github.go/ontology_data_gitlab.go for the same pattern applied to
// source-control concepts; TestOntologyFieldsAreCanonical enforces that every
// OntologyField here is in canonicalOntologyVocab for its NodeType.
//
// NodeField keys are the raw properties written at node creation time by
// sources/pagerduty/{users,teams,services}.go.

func init() { registerOntology(pagerdutyOntology) }

var pagerdutyOntology = map[string]OntologyNodeSpec{
	"PagerDutyUser": {NodeType: NodeTypeUserAccount, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "username", NodeField: "username"},
		{OntologyField: "email", NodeField: "email"},
		{OntologyField: "url", NodeField: "url"},
	}},
	"PagerDutyTeam": {NodeType: NodeTypeUserGroup, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "url", NodeField: "url"},
	}},
	"PagerDutyService": {NodeType: NodeTypeOnCallService, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "url", NodeField: "url"},
		{OntologyField: "status", NodeField: "status"},
	}},
}
