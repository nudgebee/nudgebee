package observability

import (
	"fmt"
	"strings"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"
)

// promQLParser is a shared, stateless PromQL parser. ParseExpr builds a fresh
// internal parser per call, so one package-level instance is concurrency-safe
// and avoids per-call allocations.
var promQLParser = parser.NewParser(parser.Options{})

// scopePromQLSelectors adds the matchers in matcherFragment (the integration's
// `cluster="prod",env="x"` form) to every vector selector of query.
//
// The internal enrichers were written against the agent's in-cluster Prometheus,
// so most of their queries carry no cluster matcher at all — kube_pod_info{pod_ip=~…}
// on a shared Thanos/Mimir backend would match pods from every cluster it holds,
// and pod CIDRs overlap across clusters as a matter of course. Rewriting the
// parsed AST rather than the query text is what makes this hold for `or`-joined
// selectors, subqueries and functions alike. A matcher a selector already carries
// is not added twice. An empty fragment returns the query unchanged.
func scopePromQLSelectors(query, matcherFragment string) (string, error) {
	matcherFragment = strings.TrimSpace(matcherFragment)
	if matcherFragment == "" {
		return query, nil
	}
	matchers, err := promQLParser.ParseMetricSelector("{" + matcherFragment + "}")
	if err != nil {
		return "", fmt.Errorf("promql scope: invalid additional labels %q: %w", matcherFragment, err)
	}
	expr, err := promQLParser.ParseExpr(query)
	if err != nil {
		return "", fmt.Errorf("promql scope: %w", err)
	}
	parser.Inspect(expr, func(node parser.Node, _ []parser.Node) error {
		selector, ok := node.(*parser.VectorSelector)
		if !ok {
			return nil
		}
		for _, matcher := range matchers {
			if !hasMatcher(selector.LabelMatchers, matcher) {
				selector.LabelMatchers = append(selector.LabelMatchers, matcher)
			}
		}
		return nil
	})
	return expr.String(), nil
}

func hasMatcher(existing []*labels.Matcher, candidate *labels.Matcher) bool {
	for _, m := range existing {
		if m.Type == candidate.Type && m.Name == candidate.Name && m.Value == candidate.Value {
			return true
		}
	}
	return false
}
