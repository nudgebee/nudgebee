package logql2ch

import (
	"sort"
	"strings"
)

// Schema says how the label columns are read.
type Schema int

const (
	// SchemaExpressions reads labels from the exporter's attribute maps. It
	// works on every otel_logs table, including the stock exporter's and dev's
	// older one, and is the safe default when the schema cannot be checked.
	SchemaExpressions Schema = iota
	// SchemaColumns reads the runner's materialized label columns, which
	// lead its ORDER BY.
	SchemaColumns
)

// labelColumns are the runner's materialized columns, each with the
// expression it materializes. SchemaExpressions inlines the expression.
var labelColumns = map[string]string{
	"namespace": "ResourceAttributes['k8s.namespace.name']",
	"workload": "multiIf(" +
		"ResourceAttributes['k8s.deployment.name'] != '', ResourceAttributes['k8s.deployment.name'], " +
		"ResourceAttributes['k8s.statefulset.name'] != '', ResourceAttributes['k8s.statefulset.name'], " +
		"ResourceAttributes['k8s.daemonset.name'] != '', ResourceAttributes['k8s.daemonset.name'], " +
		"ResourceAttributes['k8s.cronjob.name'] != '', ResourceAttributes['k8s.cronjob.name'], " +
		"ResourceAttributes['k8s.job.name'] != '', ResourceAttributes['k8s.job.name'], " +
		"ResourceAttributes['k8s.pod.name'])",
	"pod":       "ResourceAttributes['k8s.pod.name']",
	"container": "ResourceAttributes['k8s.container.name']",
	"node":      "ResourceAttributes['k8s.node.name']",
	"stream":    "LogAttributes['log.iostream']",
	"level": "multiIf(" +
		"SeverityNumber >= 21, 'critical', SeverityNumber >= 17, 'error', SeverityNumber >= 13, 'warning', " +
		"SeverityNumber >= 9, 'info', SeverityNumber >= 1, 'debug', " +
		`match(Body, '(?i)\\b(fatal|panic|critical)\\b'), 'critical', ` +
		`match(Body, '(?i)\\b(error|err|exception)\\b'), 'error', ` +
		`match(Body, '(?i)\\bwarn(ing)?\\b'), 'warning', 'unknown')`,
}

// labelAliases maps each LogQL label Nudgebee accepts to SQL over otel_logs,
// written against the runner's column names (testdata/label_aliases.yaml pins
// it). Any other label is refused: an unknown name never reaches the SQL.
var labelAliases = map[string][]string{
	// Each alias is a list of pieces; a single piece is the whole expression,
	// several are concat() arguments (so a metric series can inline them).
	"namespace":    {"namespace"},
	"pod":          {"pod"},
	"container":    {"container"},
	"node_name":    {"node"},
	"stream":       {"stream"},
	"level":        {"level"},
	"app":          {"workload"},
	"job":          {"namespace", "'/'", "workload"},
	"filename":     {"LogAttributes['log.file.path']"},
	"service_name": {"ServiceName"},
}

// streamLabels are the aliases Loki carries as stream labels. level is not one:
// it is derived per line, so after a parser `level` names the extracted field,
// and a metric query without an aggregation does not group by it.
var streamLabels = []string{"app", "container", "filename", "job", "namespace", "node_name", "pod", "service_name", "stream"}

// outputLabels is the column order of a log query's labels. The first three
// match the spike's parity reader (namespace, pod, container).
var outputLabels = []string{"namespace", "pod", "container", "app", "filename", "job", "level", "node_name", "service_name", "stream"}

// levelValues maps Loki's level spellings onto the ones the level column holds.
var levelValues = map[string]string{"warn": "warning", "fatal": "critical"}

// levelColumnValues are the only values the level expression produces.
var levelColumnValues = []string{"critical", "error", "warning", "info", "debug", "unknown"}

// Labels returns the label names the translator accepts, sorted.
func Labels() []string {
	out := make([]string, 0, len(labelAliases))
	for k := range labelAliases {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// LabelExpr returns the SQL expression for a label, or false if the label is
// not in the alias map.
func LabelExpr(label string, schema Schema) (string, bool) {
	pieces, ok := labelPieces(label, schema)
	if !ok {
		return "", false
	}
	if len(pieces) == 1 {
		return pieces[0], true
	}
	return "concat(" + strings.Join(pieces, ", ") + ")", true
}

func labelPieces(label string, schema Schema) ([]string, bool) {
	pieces, ok := labelAliases[label]
	if !ok {
		return nil, false
	}
	out := make([]string, len(pieces))
	for i, p := range pieces {
		if expr, isColumn := labelColumns[p]; isColumn && schema == SchemaExpressions {
			p = expr
		}
		out[i] = p
	}
	return out, true
}

func isStreamLabel(name string) bool {
	for _, l := range streamLabels {
		if l == name {
			return true
		}
	}
	return false
}

func unknownLabelError(name string) error {
	return &Error{Kind: KindUnknownLabel, msg: `unknown label "` + name + `"; supported labels: ` + strings.Join(Labels(), ", ")}
}

// SchemaProbeSQL lists which of the runner's label columns table has; pass the
// returned names to SchemaFromColumns.
func SchemaProbeSQL(table string) string {
	names := make([]string, 0, len(labelColumns))
	for k := range labelColumns {
		names = append(names, quote(k))
	}
	sort.Strings(names)
	db, tbl := "currentDatabase()", table
	if i := strings.IndexByte(table, '.'); i >= 0 {
		db, tbl = quote(table[:i]), table[i+1:]
	}
	return "SELECT name FROM system.columns WHERE database = " + db + " AND table = " + quote(tbl) +
		" AND name IN (" + strings.Join(names, ", ") + ")"
}

// SchemaFromColumns picks SchemaColumns only when every label column exists.
func SchemaFromColumns(names []string) Schema {
	have := map[string]bool{}
	for _, n := range names {
		have[n] = true
	}
	for k := range labelColumns {
		if !have[k] {
			return SchemaExpressions
		}
	}
	return SchemaColumns
}
