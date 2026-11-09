package observability

import (
	"sort"

	"nudgebee/services/query"
)

// canonicalLogField is a provider-independent log field and its value type — the
// vocabulary api-server tells every client it may filter on.
//
// The logs counterpart of canonicalTraceFields (service.go). It exists because the
// name for a log concept was owned by nobody, so each producer invented its own:
// llm-server staples `_body` onto every provider's advertised field list
// (tool_logs.go), the frontend log builder emits `content`, and the provider maps
// variously key on `body`, `message` or neither. Publishing one list is what lets
// those producers stop guessing.
//
// Deliberately NOT an alias table. The spellings are reconciled at the producers and
// in the provider maps, which already express that relationship — eight providers key
// both `body` and `message` at one target. See #39573.
type canonicalLogField struct {
	name string
	typ  string
}

// canonicalLogFields is the vocabulary. Ordered for presentation: the log line first,
// then the Kubernetes identity fields, then the rest.
//
// A name here is a claim that a caller may filter on it, not that every backend
// resolves it — a provider that genuinely cannot says so through
// GetIgnoredQueryRequestKeys, and buildLogLabels subtracts those.
//
// Two pairs look like duplicates and are not, which is why neither is collapsed:
//   - `level` and `severity`: New Relic maps them to "level" and
//     "newrelic.log.severity" — different fields on the same backend.
//   - `app` and `service`: OpenObserve maps them to "k8s_deployment_name" and
//     "service_name"; Dynatrace, CubeAPM and Splunk Enterprise split them the same way.
var canonicalLogFields = []canonicalLogField{
	{name: "message", typ: query.LabelTypeString},
	{name: "namespace", typ: query.LabelTypeString},
	{name: "pod", typ: query.LabelTypeString},
	{name: "container", typ: query.LabelTypeString},
	{name: "app", typ: query.LabelTypeString},
	{name: "service", typ: query.LabelTypeString},
	{name: "node", typ: query.LabelTypeString},
	{name: "cluster", typ: query.LabelTypeString},
	{name: "level", typ: query.LabelTypeString},
	{name: "severity", typ: query.LabelTypeString},
	{name: "trace_id", typ: query.LabelTypeString},
	{name: "timestamp", typ: query.LabelTypeTimestamp},
}

// resolvableCanonicalLogFields returns the canonical fields a caller may actually
// filter on for this source: the vocabulary minus anything the source declares it
// cannot resolve.
//
// The subtraction is the point. GetIgnoredQueryRequestKeys names fields that are
// STRIPPED from the query before it reaches the backend (FetchLogs) — Dynatrace and
// New Relic both declare `trace_id`. Advertising a stripped field tells a caller it
// may filter on something that will be silently dropped, and a wider result set read
// as a filtered one is worse than an empty one: it is a confidently wrong answer.
//
// Ignored keys are declared in PROVIDER space, so a canonical field is withheld when
// its mapped image — or its own name, when unmapped — appears in that list.
//
// RefusedLogFieldsSource is the other way a field is withheld: the source's builder REFUSES
// it with an error instead of stripping it (SolarWinds' trace_id), so a caller who sends it
// learns why, but it must not be advertised as filterable either.
//
// Apart from those two, nothing withholds a field. Traces additionally
// infer "what a provider can resolve" from whether it publishes a mapping at all
// (providerDeclaresTraceFields), and that inference cost two production incidents
// (#36940 then #37126) when a provider gained an alias-only mapping. It would be worse
// here: Loki's map is entirely aliases for the log line, so the same rule would
// withhold namespace, pod, app and container from the most-used log backend.
func resolvableCanonicalLogFields(source LogSource, mergedMapping map[string]string) []canonicalLogField {
	ignored := map[string]struct{}{}
	if filter, ok := source.(QueryRequestKeyFilter); ok {
		for _, key := range filter.GetIgnoredQueryRequestKeys() {
			ignored[key] = struct{}{}
		}
	}
	if refuser, ok := source.(RefusedLogFieldsSource); ok {
		for _, key := range refuser.RefusedLogFields() {
			ignored[key] = struct{}{}
		}
	}
	if len(ignored) == 0 {
		return canonicalLogFields
	}

	out := make([]canonicalLogField, 0, len(canonicalLogFields))
	for _, f := range canonicalLogFields {
		providerField := f.name
		if mapped, ok := mergedMapping[f.name]; ok && mapped != "" {
			providerField = mapped
		}
		if _, dropped := ignored[providerField]; dropped {
			continue
		}
		out = append(out, f)
	}
	return out
}

// RefusedLogFieldsSource is implemented by a log source whose query builder refuses some
// fields outright. Keys are in provider space, like GetIgnoredQueryRequestKeys.
type RefusedLogFieldsSource interface {
	RefusedLogFields() []string
}

// buildLogLabels returns the field set a caller may filter on: the resolvable canonical
// vocabulary, unioned with the merged mapping's keys and whatever the backend reported
// live, deduped and canonical-first.
//
// Canonical fields carry their value type; mapping and discovered keys keep whatever
// the provider said about them. Pure — no I/O — so the union and dedup are testable
// without a backend.
//
// Mirrors buildTraceLabels, minus the mapping-length inference. See
// resolvableCanonicalLogFields for why that inference is not reproduced here.
func buildLogLabels(canonical []canonicalLogField, mergedMapping map[string]string, discovered []OutputLogLabel) []OutputLogLabel {
	seen := make(map[string]struct{}, len(canonical)+len(mergedMapping)+len(discovered))
	labels := make([]OutputLogLabel, 0, len(canonical)+len(mergedMapping)+len(discovered))

	for _, f := range canonical {
		seen[f.name] = struct{}{}
		labels = append(labels, OutputLogLabel{
			Label:      f.name,
			DataType:   f.typ,
			Attributes: map[string]any{"type": f.typ},
		})
	}

	// Mapping keys next: names a caller may legitimately use that are not canonical
	// (a provider's own convenience aliases, or an operator's override).
	mappingKeys := make([]string, 0, len(mergedMapping))
	for key := range mergedMapping {
		mappingKeys = append(mappingKeys, key)
	}
	sort.Strings(mappingKeys) // map iteration order is random; the advertised list must not be
	for _, key := range mappingKeys {
		if _, dup := seen[key]; dup || key == "" {
			continue
		}
		seen[key] = struct{}{}
		labels = append(labels, OutputLogLabel{Label: key, Attributes: map[string]any{}})
	}

	for _, d := range discovered {
		if _, dup := seen[d.Label]; dup || d.Label == "" {
			continue
		}
		seen[d.Label] = struct{}{}
		labels = append(labels, d)
	}

	return labels
}

// canonicalLogFieldNamesFor is the names-only projection of resolvableCanonicalLogFields,
// sorted — what the provider-capabilities payload advertises. Kept as a projection of
// the same function the label listing uses, so the two surfaces cannot advertise
// different vocabularies.
func canonicalLogFieldNamesFor(source LogSource, mergedMapping map[string]string) []string {
	fields := resolvableCanonicalLogFields(source, mergedMapping)
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		out = append(out, f.name)
	}
	sort.Strings(out)
	return out
}
