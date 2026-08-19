package observability

import (
	"nudgebee/services/query"
)

// esCanonicalK8sFields maps the canonical label names the product builds log
// filters from — the pod/workload Logs tab, the surrounding-logs viewer, the
// event-rule log actions — to the field names Elasticsearch shippers actually
// write them to.
//
// Loki and Pinot resolve this through the Log Label Mapping an operator fills
// in per account (log_labels); Elasticsearch has no such setting, so a filter
// on "namespace" reached the cluster as a term on a field literally named
// "namespace" and matched nothing, with no error — the query simply returned
// zero rows. And a single mapping would not be enough anyway: every shipper
// spells these differently. Fluent-Bit flattens to kubernetes.namespace_name,
// ECS (Beats / Elastic Agent / Logstash) writes kubernetes.namespace, and OTel
// nests under resource.attributes.k8s.namespace.name — sometimes in the same
// index pattern, when a cluster runs more than one collector.
//
// Being one-to-many, this cannot be expressed as a rename the way
// GetLabelMapping does it. buildESQueryFromWhere expands a canonical field into
// a bool/should over every candidate instead, so whichever name the documents
// use is the one that matches.
//
// The canonical name is always the first candidate: an index that really does
// carry a top-level "namespace" field keeps matching exactly as before, and the
// expansion only ever widens.
var esCanonicalK8sFields = map[string][]string{
	"namespace": {
		"namespace",
		"kubernetes.namespace_name",
		"kubernetes.namespace",
		"resource.attributes.k8s.namespace.name",
		"k8s.namespace.name",
	},
	"pod": {
		"pod",
		"kubernetes.pod_name",
		"kubernetes.pod.name",
		"resource.attributes.k8s.pod.name",
		"k8s.pod.name",
	},
	"container": {
		"container",
		"kubernetes.container_name",
		"kubernetes.container.name",
		"resource.attributes.k8s.container.name",
		"k8s.container.name",
	},
	// "app" is the workload name. Fluent-Bit carries it as a pod label; the
	// Elastic Agent k8s integration writes the owning controller's name under
	// its own kind, so all three kinds are candidates.
	// The canonical log-BODY field. llm-server advertises `_body` to the query
	// generator unconditionally (tool_logs.go appends it to the label list), but no
	// shipper writes a field called `_body` — Fluent-Bit writes `log`, ECS writes
	// `message`, OTel writes `body`. Without this mapping the generator's
	// `{"_body": {"_ilike": "%error%"}}` reached ES as a wildcard on a field that
	// does not exist and matched NOTHING, silently, with HTTP 200.
	//
	// Verified on the dev cluster: logs-kubernetes.container_logs-* maps `log`
	// (keyword) and has no `_body` field at all.
	esCanonicalBodyField: {
		"log",
		"message",
		// JSON-structured loggers (zap, logrus, bunyan, winston) write the line to
		// `msg`, which a shipper with a JSON parse filter lands as its own field.
		"msg",
		"body",
		"content",
	},
	"app": {
		"app",
		"kubernetes.labels.app",
		"kubernetes.deployment.name",
		"kubernetes.statefulset.name",
		"kubernetes.daemonset.name",
		"resource.attributes.k8s.deployment.name",
		"k8s.deployment.name",
	},
}

// esCandidateFields returns the ES field names a canonical k8s label may live
// under, each paired with its .keyword multi-field. Mappings differ per index:
// the same name is a keyword in one template and a text field with a .keyword
// subfield in another, and a term query against the text variant never matches.
// A candidate the index does not define simply matches nothing inside the
// should, so listing both is free.
//
// Returns nil for any field that is not a canonical label — the caller then
// renders it verbatim.
func esCandidateFields(field string) []string {
	base, ok := esCanonicalK8sFields[field]
	if !ok {
		return nil
	}
	out := make([]string, 0, len(base)*2)
	for _, f := range base {
		out = append(out, f, f+".keyword")
	}
	return out
}

// binaryClauseForField renders one binary operation into an ES clause,
// expanding a canonical k8s label across every field name a shipper might have
// written it to. Non-canonical fields pass through to binaryToESClause
// unchanged.
//
// The negation flag comes from the underlying operator and is identical for
// every candidate, so it is returned as-is: whereToBool puts the whole should
// into must_not, which reads as "no candidate field holds this value" — the
// correct meaning for _neq / _not_in, and for _is_null true it becomes "none of
// these fields exist".
func binaryClauseForField(field string, op query.BinaryWhereClauseType, val any) (map[string]any, bool, error) {
	candidates := esCandidateFields(field)
	if len(candidates) == 0 {
		return binaryToESClause(field, op, val)
	}

	shoulds := make([]any, 0, len(candidates))
	var negate bool
	for _, candidate := range candidates {
		clause, n, err := binaryToESClause(candidate, op, val)
		if err != nil {
			return nil, false, err
		}
		negate = n
		shoulds = append(shoulds, clause)
	}
	return map[string]any{
		"bool": map[string]any{
			"should":               shoulds,
			"minimum_should_match": 1,
		},
	}, negate, nil
}
