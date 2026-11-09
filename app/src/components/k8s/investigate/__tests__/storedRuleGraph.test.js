import { showsStoredRuleGraph } from '../storedRuleGraph';

const RULE_QUERY = 'kube_deployment_spec_replicas{job="kube-state-metrics"} > kube_deployment_status_replicas_available{job="kube-state-metrics"}';

// The cards the page builds, reduced to what is read here. A PrometheusCard is only built for a
// stored result that has data, and keeps the evidence it was built from as `sourceData`; the
// evidence is what api-server's prometheus auto action stores
// (api-server/services/observability/eventrule_actions_metrics.go).
const storedGraphCard = (query) => ({
  id: 'PrometheusCard',
  sourceData: { type: 'prometheus', additional_info: { action_name: 'prometheus_enricher' }, metadata: { query, 'query-result-version': '1.0' } },
});
const eventGraphCard = { id: 'ExecutePrometheus' };
const timelineCard = { id: 'TimelineCard' };

describe('showsStoredRuleGraph', () => {
  it('finds the stored result of the rule query among the cards', () => {
    expect(showsStoredRuleGraph([eventGraphCard, timelineCard, storedGraphCard(RULE_QUERY)], RULE_QUERY)).toBe(true);
  });

  it('matches a rule written over several lines', () => {
    const multiline = '(\n  sum by (cluster) (kube_pod_info)\n  > 0\n)';
    expect(showsStoredRuleGraph([storedGraphCard(multiline)], '( sum by (cluster) (kube_pod_info) > 0 )')).toBe(true);
  });

  it('is false when the stored result is of another query', () => {
    expect(showsStoredRuleGraph([eventGraphCard, storedGraphCard('up == 0')], RULE_QUERY)).toBe(false);
  });

  // Only the first card with an id is shown, so a later copy of the rule's result is never drawn.
  it('is false when the rule query is stored but another Prometheus result is the one shown', () => {
    expect(showsStoredRuleGraph([storedGraphCard('up == 0'), storedGraphCard(RULE_QUERY)], RULE_QUERY)).toBe(false);
  });

  it('is false when no stored Prometheus result is shown', () => {
    expect(showsStoredRuleGraph([eventGraphCard, timelineCard], RULE_QUERY)).toBe(false);
    expect(showsStoredRuleGraph([], RULE_QUERY)).toBe(false);
    expect(showsStoredRuleGraph(undefined, RULE_QUERY)).toBe(false);
  });

  it('is false when the stored result does not say which query it ran', () => {
    expect(showsStoredRuleGraph([{ id: 'PrometheusCard', sourceData: { type: 'prometheus' } }], RULE_QUERY)).toBe(false);
  });

  it('is false with no rule query', () => {
    expect(showsStoredRuleGraph([storedGraphCard('')], '')).toBe(false);
    expect(showsStoredRuleGraph([storedGraphCard(RULE_QUERY)], undefined)).toBe(false);
  });
});
