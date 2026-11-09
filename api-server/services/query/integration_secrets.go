package query

import (
	"errors"
	"maps"
	"slices"
	"strings"
)

// IntegrationSecretFields names the integration config fields whose stored value
// must never be returned to a client, keyed by lowercase integration type.
type IntegrationSecretFields struct {
	// Exact holds field names that are secret for that type.
	Exact map[string][]string
	// Prefix holds field-name prefixes that are secret for that type (the LLM
	// integration's per-agent and per-tier keys have no fixed schema entry).
	Prefix map[string][]string
}

// integrationSecretFieldsFn is set by integrations/core, which owns the schemas
// and cannot be imported from here (it imports query).
var integrationSecretFieldsFn func() IntegrationSecretFields

// RegisterIntegrationSecretFields sets the lookup the integrations list uses to
// redact secret config values. integrations/core calls it from init.
func RegisterIntegrationSecretFields(fn func() IntegrationSecretFields) {
	integrationSecretFieldsFn = fn
}

var errIntegrationSecretFieldsUnset = errors.New("query: integration secret fields are not registered")

// integrationSecretFieldSQL returns a SQL predicate that is true when the
// integration type in typeExpr and the field name in nameExpr name a secret
// field. It fails closed: with no lookup registered, or an empty one, it returns
// an error rather than a predicate that matches nothing.
func integrationSecretFieldSQL(typeExpr, nameExpr string) (string, error) {
	if integrationSecretFieldsFn == nil {
		return "", errIntegrationSecretFieldsUnset
	}
	fields := integrationSecretFieldsFn()
	quote := (&postgresDialect{}).QuoteLiteral

	var pairs []string
	for _, integrationType := range sortedKeys(fields.Exact) {
		for _, name := range sortedUnique(fields.Exact[integrationType]) {
			pairs = append(pairs, "("+quote(strings.ToLower(integrationType))+", "+quote(name)+")")
		}
	}
	var terms []string
	if len(pairs) > 0 {
		terms = append(terms, "(LOWER("+typeExpr+"), "+nameExpr+") IN ("+strings.Join(pairs, ", ")+")")
	}
	for _, integrationType := range sortedKeys(fields.Prefix) {
		for _, prefix := range sortedUnique(fields.Prefix[integrationType]) {
			terms = append(terms, "(LOWER("+typeExpr+") = "+quote(strings.ToLower(integrationType))+" AND starts_with("+nameExpr+", "+quote(prefix)+"))")
		}
	}
	if len(terms) == 0 {
		return "", errIntegrationSecretFieldsUnset
	}
	return "(" + strings.Join(terms, " OR ") + ")", nil
}

func sortedKeys(m map[string][]string) []string {
	return slices.Sorted(maps.Keys(m))
}

func sortedUnique(values []string) []string {
	out := slices.DeleteFunc(append([]string(nil), values...), func(v string) bool { return v == "" })
	slices.Sort(out)
	return slices.Compact(out)
}
