package core

// Ontology field mapping for the canonical identity layer (sources/identity_enricher.go).
// NudgebeeUser is the internal `users` table row materialized as a KG node; every
// per-integration identity (GitHubUser today) links to it via SAME_AS rather than
// duplicating its fields, so this spec only needs the two fields the enricher
// actually writes — see ontology_data_aws.go for the general pattern this follows.

func init() { registerOntology(identityOntology) }

var identityOntology = map[string]OntologyNodeSpec{
	"NudgebeeUser": {NodeType: NodeTypeUserAccount, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "username", NodeField: "username"},
	}},
}
