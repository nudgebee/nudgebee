package tools

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"
)

// Views of an alert's own metric saved before an investigation (see the
// analyzer's alert history). The analysis reports the "now" view first.
const (
	AlertHistoryNormalView = "normal level: last 3 days, 15-minute step"
	AlertHistoryNowView    = "now: last 2 hours, 1-minute step"
)

// AlertLevelQuery splits an alert rule of the form `<series> <op> <number>`
// (also inside `... and <condition>` or `... unless <condition>`, where the
// left side is what fires) into the series it compares and the level it fires
// at. ok is false for a rule with no level to trend toward: a state test
// (== or !=), a presence test (absent()), a comparison of two series, an `or`,
// or text that does not parse.
func AlertLevelQuery(expr string) (series, op string, level float64, ok bool) {
	node, err := promQLParser.ParseExpr(expr)
	if err != nil {
		return "", "", 0, false
	}
	return alertLevelNode(node)
}

func alertLevelNode(node parser.Expr) (string, string, float64, bool) {
	node = unwrapParens(node)
	b, isBinary := node.(*parser.BinaryExpr)
	if !isBinary {
		return "", "", 0, false
	}
	switch b.Op {
	case parser.LAND, parser.LUNLESS:
		return alertLevelNode(b.LHS)
	case parser.GTR, parser.GTE, parser.LSS, parser.LTE:
	default:
		return "", "", 0, false
	}
	lhs, rhs := unwrapParens(b.LHS), unwrapParens(b.RHS)
	if n, isNum := rhs.(*parser.NumberLiteral); isNum {
		return lhs.String(), b.Op.String(), n.Val, true
	}
	if n, isNum := lhs.(*parser.NumberLiteral); isNum {
		return rhs.String(), flipComparison(b.Op).String(), n.Val, true
	}
	return "", "", 0, false
}

func unwrapParens(node parser.Expr) parser.Expr {
	for {
		p, ok := node.(*parser.ParenExpr)
		if !ok {
			return node
		}
		node = p.Expr
	}
}

// flipComparison turns `5 < x` into `x > 5`.
func flipComparison(op parser.ItemType) parser.ItemType {
	switch op {
	case parser.GTR:
		return parser.LSS
	case parser.GTE:
		return parser.LTE
	case parser.LSS:
		return parser.GTR
	case parser.LTE:
		return parser.GTE
	}
	return op
}

// FetchAlertMetricHistory runs series over [start, end] at step through the
// account's metrics provider and returns the one series that belongs to this
// alert, as one entry of the file shape the Prometheus tool saves ({query:
// [{metric, timestamps, values}]}). A series belongs to the alert when every label it
// shares with alertLabels agrees (see labelAgrees); a lone series belongs to
// it whatever its labels. When several belong (the pods of one workload), the
// one still reporting latest is taken — a pod replaced before the alert stops
// early and is not the one alerting — and among those, the one furthest toward
// the alert's level at the end. ok is false when none belongs: a wrong file named in the
// investigation prompt would be read as the alert's own history.
func FetchAlertMetricHistory(ctx security.RequestContext, accountId, series, op string, alertLabels map[string]string, start, end time.Time, step time.Duration) (any, bool, error) {
	query := func(q string) ([]any, error) {
		response, err := promQueryMetrics(ctx, core.ObservabilityMetricsQueryRequest{
			AccountId:    accountId,
			Queries:      map[string]string{"query": q},
			StartTime:    start.UnixMilli(),
			EndTime:      end.UnixMilli(),
			StepInterval: int(step.Seconds()),
		})
		if err != nil {
			return nil, fmt.Errorf("metrics query: %w", err)
		}
		return PrometheusExecuteTool{}.seriesFromMetricsResponse(response)
	}
	// Ask the server for the alert's series only; a rule over every pod would
	// otherwise return all of them. When the narrowed query finds nothing (a
	// label spelled differently in the series), fall back to the whole rule.
	var all []any
	var err error
	if narrowed, ok := narrowToAlert(series, alertLabels); ok {
		all, err = query(narrowed)
	}
	if err == nil && len(all) == 0 {
		all, err = query(series)
	}
	if err != nil {
		return nil, false, fmt.Errorf("FetchAlertMetricHistory: %w", err)
	}
	picked := alertSeries(all, alertLabels, op)
	if picked == nil {
		return nil, false, nil
	}
	return sanitizeFloats(picked), true, nil
}

// alertSeries picks the series of all that belongs to the alert (see
// FetchAlertMetricHistory).
func alertSeries(all []any, alertLabels map[string]string, op string) any {
	if len(all) == 1 {
		return all[0]
	}
	var picked any
	var bestTs, best float64
	for _, s := range all {
		m, _ := s.(map[string]any)
		metric, _ := m["metric"].(map[string]any)
		shared, agrees := 0, true
		for k, v := range metric {
			if k == "__name__" {
				continue
			}
			want, has := alertLabels[k]
			if !has {
				continue
			}
			shared++
			if !labelAgrees(fmt.Sprint(v), want) {
				agrees = false
				break
			}
		}
		if !agrees || shared == 0 {
			continue
		}
		ts, last, ok := lastPoint(m)
		if !ok {
			continue
		}
		nearer := (strings.HasPrefix(op, "<") && last < best) || (!strings.HasPrefix(op, "<") && last > best)
		if picked == nil || ts > bestTs || (ts == bestTs && nearer) {
			picked, bestTs, best = s, ts, last
		}
	}
	return picked
}

// labelAgrees is true when a series label value is the alert's value, or a pod
// of the workload the alert names: alert sources often report the workload
// ("checkout") where the series carries the pod ("checkout-7c9d8f-x2l4p").
func labelAgrees(series, alert string) bool {
	return series == alert || strings.HasPrefix(series, alert+"-")
}

// lastPoint returns the timestamp and value of a series' last numeric point.
func lastPoint(series map[string]any) (ts, value float64, ok bool) {
	vs, _ := series["values"].([]any)
	tss, _ := series["timestamps"].([]any)
	for i := len(vs) - 1; i >= 0; i-- {
		f, isNum := vs[i].(float64)
		if !isNum {
			continue
		}
		if i < len(tss) {
			ts, _ = tss[i].(float64)
		}
		return ts, f, true
	}
	return 0, 0, false
}

// cloudWatchAlarm is the part of a CloudWatch DescribeAlarms entry (the stored
// rule of a CloudWatch alarm) that names the metric and the level it fires at.
type cloudWatchAlarm struct {
	AlarmArn           string   `json:"AlarmArn"`
	MetricName         string   `json:"MetricName"`
	Namespace          string   `json:"Namespace"`
	Statistic          string   `json:"Statistic"`
	ExtendedStatistic  string   `json:"ExtendedStatistic"`
	Threshold          *float64 `json:"Threshold"`
	ComparisonOperator string   `json:"ComparisonOperator"`
	Dimensions         []struct {
		Name  string `json:"Name"`
		Value string `json:"Value"`
	} `json:"Dimensions"`
	Metrics []any `json:"Metrics"`
}

var cloudWatchComparisons = map[string]string{
	"GreaterThanThreshold":          ">",
	"GreaterThanOrEqualToThreshold": ">=",
	"LessThanThreshold":             "<",
	"LessThanOrEqualToThreshold":    "<=",
}

// cloudWatchSafe matches the values placed in the CLI command. Anything else
// (quotes, shell metacharacters) is refused rather than escaped.
var cloudWatchSafe = regexp.MustCompile(`^[A-Za-z0-9 _./:=@+\-*#,]+$`)

// CloudWatchAlarmCommand turns a CloudWatch alarm's stored definition into an
// `aws cloudwatch get-metric-statistics` command for [start, end] at period,
// with the level the alarm fires at. ok is false for an alarm with no single
// metric and fixed threshold (metric math, anomaly bands) or with values that
// are not safe to place in a command.
func CloudWatchAlarmCommand(expr string, start, end time.Time, period time.Duration) (cmd, op string, level float64, ok bool) {
	var a cloudWatchAlarm
	if err := json.Unmarshal([]byte(expr), &a); err != nil || a.MetricName == "" || a.Namespace == "" || len(a.Metrics) > 0 || a.Threshold == nil {
		return "", "", 0, false
	}
	op, known := cloudWatchComparisons[a.ComparisonOperator]
	if !known {
		return "", "", 0, false
	}
	parts := strings.Split(a.AlarmArn, ":")
	if len(parts) < 4 || parts[3] == "" {
		return "", "", 0, false
	}
	region := parts[3]
	values := []string{a.MetricName, a.Namespace, region}
	stat := "--statistics " + cmpOr(a.Statistic, "Average")
	if a.ExtendedStatistic != "" {
		stat = "--extended-statistics " + a.ExtendedStatistic
		values = append(values, a.ExtendedStatistic)
	} else {
		values = append(values, cmpOr(a.Statistic, "Average"))
	}
	dims := make([]string, 0, len(a.Dimensions))
	for _, d := range a.Dimensions {
		values = append(values, d.Name, d.Value)
		dims = append(dims, fmt.Sprintf("Name='%s',Value='%s'", d.Name, d.Value))
	}
	for _, v := range values {
		if !cloudWatchSafe.MatchString(v) {
			return "", "", 0, false
		}
	}
	cmd = fmt.Sprintf("aws cloudwatch get-metric-statistics --region %s --namespace '%s' --metric-name '%s' --start-time %s --end-time %s --period %d %s",
		region, a.Namespace, a.MetricName, start.UTC().Format(time.RFC3339), end.UTC().Format(time.RFC3339), int(period.Seconds()), stat)
	if len(dims) > 0 {
		cmd += " --dimensions " + strings.Join(dims, " ")
	}
	return cmd + " --output json", op, *a.Threshold, true
}

func cmpOr(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

// alertIdentityLabels are the labels used to narrow a rule whose result keeps
// every label of its inputs (a bare selector or a function of one), where
// nothing in the expression says which labels identify the alert. Rules that
// aggregate are narrowed on the labels they group by instead.
var alertIdentityLabels = map[string]bool{
	"namespace": true, "pod": true, "container": true, "service_name": true, "k8s_deployment_name": true,
	"job": true, "instance": true, "node": true, "persistentvolumeclaim": true, "queue": true, "host": true,
}

// narrowToAlert adds the alert's labels as matchers to every vector selector
// of series, for each label the expression's result keeps, so the server
// returns the alert's own series rather than every series the rule covers.
// A label the result does not keep is never added: it would change what is
// summed or compared, not just which series come back. A pod label carrying
// a workload name matches that workload's pods. ok is false when nothing was
// added.
func narrowToAlert(series string, alertLabels map[string]string) (string, bool) {
	expr, err := promQLParser.ParseExpr(series)
	if err != nil {
		return series, false
	}
	keep := keptLabels(expr)
	var add []*labels.Matcher
	for k, v := range alertLabels {
		if v == "" || k == "__name__" || !keep.has(k) || (keep.all && !alertIdentityLabels[k]) {
			continue
		}
		var m *labels.Matcher
		if k == "pod" {
			m, err = labels.NewMatcher(labels.MatchRegexp, k, regexp.QuoteMeta(v)+"(-.*)?")
		} else {
			m, err = labels.NewMatcher(labels.MatchEqual, k, v)
		}
		if err == nil {
			add = append(add, m)
		}
	}
	if len(add) == 0 {
		return series, false
	}
	parser.Inspect(expr, func(node parser.Node, _ []parser.Node) error {
		if vs, ok := node.(*parser.VectorSelector); ok {
			for _, m := range add {
				if !selectorHasLabel(vs, m.Name) {
					vs.LabelMatchers = append(vs.LabelMatchers, m)
				}
			}
		}
		return nil
	})
	return expr.String(), true
}

func selectorHasLabel(vs *parser.VectorSelector, name string) bool {
	for _, m := range vs.LabelMatchers {
		if m.Name == name {
			return true
		}
	}
	return false
}

// labelSet is the labels an expression's result keeps: all of them (minus
// out) or exactly in.
type labelSet struct {
	all bool
	in  map[string]bool
	out map[string]bool
}

func (s labelSet) has(l string) bool {
	if s.all {
		return !s.out[l]
	}
	return s.in[l]
}

// keptLabels is the conservative set of labels the expression's result keeps.
// Anything not understood keeps nothing, so it is never narrowed.
func keptLabels(e parser.Expr) labelSet {
	switch n := e.(type) {
	case *parser.VectorSelector, *parser.MatrixSelector:
		return labelSet{all: true}
	case *parser.ParenExpr:
		return keptLabels(n.Expr)
	case *parser.UnaryExpr:
		return keptLabels(n.Expr)
	case *parser.SubqueryExpr:
		return keptLabels(n.Expr)
	case *parser.AggregateExpr:
		inner := keptLabels(n.Expr)
		out := labelSet{in: map[string]bool{}}
		if n.Without {
			if !inner.all {
				return labelSet{}
			}
			out = labelSet{all: true, out: map[string]bool{}}
			for l := range inner.out {
				out.out[l] = true
			}
			for _, l := range n.Grouping {
				out.out[l] = true
			}
			return out
		}
		for _, l := range n.Grouping {
			if inner.has(l) {
				out.in[l] = true
			}
		}
		return out
	case *parser.Call:
		for _, a := range n.Args {
			if a.Type() == parser.ValueTypeVector || a.Type() == parser.ValueTypeMatrix {
				s := keptLabels(a)
				if n.Func.Name == "histogram_quantile" && s.all {
					s.out = map[string]bool{"le": true}
				} else if n.Func.Name == "histogram_quantile" {
					delete(s.in, "le")
				}
				return s
			}
		}
		return labelSet{}
	case *parser.BinaryExpr:
		// A vector result takes its labels from the left side (one-to-one and
		// group_left) or the right (group_right); with a scalar, from the vector.
		switch {
		case n.LHS.Type() != parser.ValueTypeVector:
			return keptLabels(n.RHS)
		case n.RHS.Type() != parser.ValueTypeVector:
			return keptLabels(n.LHS)
		case n.VectorMatching != nil && n.VectorMatching.Card == parser.CardOneToMany:
			return keptLabels(n.RHS)
		default:
			return keptLabels(n.LHS)
		}
	}
	return labelSet{}
}
