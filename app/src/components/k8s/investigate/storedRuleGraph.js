// PromQL is stored as the rule author wrote it, line breaks and comments included; two copies of
// one rule differ only in spacing.
const compactQuery = (query) => (typeof query === 'string' ? query.replace(/\s+/g, ' ').trim() : '');

/**
 * Whether the investigation already shows the result of the alert rule's query. The alert pipeline
 * runs that query when the alert fires and stores it as evidence, with the object that fired marked
 * and the time around the alert. The page ran the same query a second time for its "Event Graph"
 * card, which has neither; this says when that second card would be a repeat.
 *
 * Cards are keyed by id when they are shown, so of several stored Prometheus results only the first
 * is drawn. That is the one compared: a stored copy that is not drawn must not hide the page's own.
 *
 * @param {object[]} cards the cards built for the event, in the order they will be shown
 * @param {string} ruleQuery the alert rule's PromQL
 */
export const showsStoredRuleGraph = (cards, ruleQuery) => {
  const wanted = compactQuery(ruleQuery);
  if (!wanted) return false;
  const shown = (Array.isArray(cards) ? cards : []).find((card) => card?.id === 'PrometheusCard');
  return compactQuery(shown?.sourceData?.metadata?.query) === wanted;
};
