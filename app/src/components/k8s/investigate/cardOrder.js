// Evidence producers do not agree on casing ("High", "HIGH", "high"), so severities are compared
// lower-cased.
const RANK_BY_SEVERITY = { critical: 2, high: 1 };

/**
 * How strongly a card's own finding points at the alert: 2 when one of its summary lines is
 * Critical, 1 when one is High, 0 otherwise. Lines without a severity (some cards return plain
 * strings) count as 0.
 *
 * @param {{getHighLightsData?: () => Array<{severity?: string}|string>}} card
 */
export const findingRank = (card) => {
  const lines = card?.getHighLightsData?.();
  return (Array.isArray(lines) ? lines : []).reduce(
    (rank, line) => Math.max(rank, RANK_BY_SEVERITY[String(line?.severity ?? '').toLowerCase()] ?? 0),
    0
  );
};

/**
 * The card list with a newly ready card put where its finding belongs: ahead of the cards whose
 * finding is weaker than its own, behind the ones as strong or stronger. The cards used to sit in
 * the order the evidence was collected, so the one holding the critical line could be anywhere in
 * the list. Cards already listed keep their places, so nothing the reader is looking at moves, and
 * the incident card still leads. A card with no Critical or High line goes last, as before.
 *
 * @param {object[]} cards
 * @param {object} card
 */
export const placeByFinding = (cards, card) => {
  const rank = findingRank(card);
  if (rank === 0) return [...cards, card];
  const afterIncident = cards.findIndex((placed) => placed?.id === 'ImpactCard') + 1;
  const at = cards.findIndex((placed, index) => index >= afterIncident && findingRank(placed) < rank);
  return at === -1 ? [...cards, card] : [...cards.slice(0, at), card, ...cards.slice(at)];
};
