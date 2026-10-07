import { findingRank, placeByFinding } from '../cardOrder';

const card = (id, ...severities) => ({ id, getHighLightsData: () => severities.map((severity) => ({ message: id, severity })) });
const ids = (cards) => cards.map((c) => c.id);
const place = (...cards) => cards.reduce(placeByFinding, []);

describe('findingRank', () => {
  it('ranks a card by its strongest line', () => {
    expect(findingRank(card('logs', 'Info', 'Critical'))).toBe(2);
    expect(findingRank(card('node', 'High', 'Info'))).toBe(1);
    expect(findingRank(card('timeline', 'Info'))).toBe(0);
  });

  it('reads a severity whatever its casing', () => {
    expect(findingRank(card('node', 'HIGH'))).toBe(1);
    expect(findingRank(card('alert', 'high'))).toBe(1);
    expect(findingRank(card('logs', 'CRITICAL'))).toBe(2);
  });

  it('ranks a card with no lines, plain-string lines or no summary at all as 0', () => {
    expect(findingRank(card('empty'))).toBe(0);
    expect(findingRank({ id: 'ImpactCard', getHighLightsData: () => ['2 more alerts on checkout — same problem'] })).toBe(0);
    expect(findingRank({ id: 'bare' })).toBe(0);
    expect(findingRank({ id: 'broken', getHighLightsData: () => null })).toBe(0);
  });
});

describe('placeByFinding', () => {
  it('puts critical cards first, then high, then the rest in the order they arrived', () => {
    expect(
      ids(
        place(
          card('timeline', 'Info'),
          card('alert labels'),
          card('pod events', 'Critical'),
          card('node', 'High'),
          card('cpu', 'Info'),
          card('logs', 'Critical')
        )
      )
    ).toEqual(['pod events', 'logs', 'node', 'timeline', 'alert labels', 'cpu']);
  });

  it('keeps the incident card, and whatever came before it, ahead of every finding', () => {
    expect(
      ids(
        place(
          card('AskAiCard'),
          card('RCACard'),
          card('ExecutePrometheus'),
          card('ImpactCard'),
          card('TimelineCard', 'Info'),
          card('logs', 'Critical')
        )
      )
    ).toEqual(['AskAiCard', 'RCACard', 'ExecutePrometheus', 'ImpactCard', 'logs', 'TimelineCard']);
  });

  it('adds a card with no critical or high line at the end', () => {
    expect(ids(place(card('logs', 'Critical'), card('timeline', 'Info')))).toEqual(['logs', 'timeline']);
  });

  it('leaves the list it was given untouched', () => {
    const cards = [card('timeline', 'Info')];
    placeByFinding(cards, card('logs', 'Critical'));
    expect(ids(cards)).toEqual(['timeline']);
  });
});
