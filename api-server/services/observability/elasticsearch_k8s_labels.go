package observability

import (
	"strings"

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
// esCanonicalBodyField is the canonical log-body name llm-server advertises.
const esCanonicalBodyField = "_body"

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
	// Every candidate is rendered, including the `.keyword` variants of the body
	// field. Dropping them for the body looked like a cheap win — the body is matched
	// with a leading-wildcard scan, the most expensive clause we emit — but it is
	// wrong on any cluster whose body field is `text`.
	//
	// A wildcard is a TERM-level query. Against an analyzed `text` field it is matched
	// per token, so a multi-word pattern like `*connection reset*` can never equal a
	// single token and returns nothing. Matching a phrase needs the unanalyzed
	// `.keyword` subfield, which keeps the whole line as one term. Dev's body field
	// (`log`) is plain keyword, where multi-word patterns already work — verified:
	// wildcard `*query execution*` → 10000 hits — but an ECS/dynamic-mapping estate
	// where `message` is `text` + `message.keyword` would silently return nothing,
	// which is precisely the failure this change set exists to remove.
	//
	// The cost is bounded by what the index actually defines: a `.keyword` candidate
	// that does not exist matches nothing inside the should, and only clusters that
	// really have the subfield pay for scanning it — the same clusters that need it.
	out := make([]string, 0, len(base)*2)
	for _, f := range base {
		out = append(out, f, f+".keyword")
	}
	return out
}

// esKeywordSuffixCandidates handles a non-canonical field the caller wrote with a
// `.keyword` suffix. `.keyword` is only real when the parent is mapped `text` —
// Elasticsearch adds that subfield for text, not for keyword. Shippers map
// `kubernetes.namespace_name`, `kubernetes.pod_name` and friends as plain keyword, so
// `<field>.keyword` does not exist and a term against it matches ZERO documents and
// returns HTTP 200. The caller then reads "no logs" where the truth is "wrong field".
//
// Measured on the dev cluster against logs-kubernetes.container_logs-*:
// term on `kubernetes.namespace_name` → 10000 hits; term on
// `kubernetes.namespace_name.keyword` → 0 hits, no error. Same for
// `kubernetes.namespace` / `kubernetes.namespace.keyword`.
//
// So a suffixed field is matched against BOTH spellings. A candidate the index does
// not define simply matches nothing inside the should, exactly as for the canonical
// expansion above, which makes listing both free.
//
// The reverse direction (caller wrote the bare name, index maps it as text) is
// deliberately NOT expanded: a term against a text field can match analyzed tokens and
// would widen results rather than repair them. Canonical labels already cover that case
// through esCanonicalK8sFields.
func esKeywordSuffixCandidates(field string) []string {
	base, ok := strings.CutSuffix(field, ".keyword")
	if !ok || base == "" {
		return nil
	}
	return []string{field, base}
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
		candidates = esKeywordSuffixCandidates(field)
	}
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
