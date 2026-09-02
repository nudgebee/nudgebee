import { classifyDisagreement, RANK_ORDER, SOURCE_SEVERITY_TO_RANK } from '../severityRank';

describe('classifyDisagreement', () => {
  it('agrees when the rank matches the mapped expectation', () => {
    expect(classifyDisagreement('HIGH', 'P1')).toBe('agreed');
    expect(classifyDisagreement('MEDIUM', 'P2')).toBe('agreed');
    expect(classifyDisagreement('LOW', 'P3')).toBe('agreed');
    expect(classifyDisagreement('INFO', 'P3')).toBe('agreed');
  });

  it('reads a less urgent rank as ranked below the source', () => {
    expect(classifyDisagreement('HIGH', 'P3')).toBe('below');
    expect(classifyDisagreement('HIGH', 'P2')).toBe('below');
    expect(classifyDisagreement('MEDIUM', 'P3')).toBe('below');
  });

  it('reads a more urgent rank as ranked above the source', () => {
    expect(classifyDisagreement('INFO', 'P2')).toBe('above');
    expect(classifyDisagreement('LOW', 'P2')).toBe('above');
    expect(classifyDisagreement('HIGH', 'P0')).toBe('above');
  });

  it('marks unscored rows rather than counting them as agreement', () => {
    expect(classifyDisagreement('HIGH', null)).toBe('unscored');
    expect(classifyDisagreement('HIGH', '')).toBe('unscored');
    expect(classifyDisagreement(null, 'P1')).toBe('unscored');
    expect(classifyDisagreement('UNKNOWN_SEVERITY', 'P1')).toBe('unscored');
    expect(classifyDisagreement('HIGH', 'P9')).toBe('unscored');
  });

  it('is case-insensitive on both axes', () => {
    expect(classifyDisagreement('high', 'p1')).toBe('agreed');
    expect(classifyDisagreement('High', 'p3')).toBe('below');
  });

  it('covers every severity in the mapping against every rank', () => {
    Object.entries(SOURCE_SEVERITY_TO_RANK).forEach(([severity, expectedRank]) => {
      const expectedIndex = RANK_ORDER.indexOf(expectedRank);
      RANK_ORDER.forEach((rank, index) => {
        const verdict = classifyDisagreement(severity, rank);
        if (index === expectedIndex) expect(verdict).toBe('agreed');
        else if (index > expectedIndex) expect(verdict).toBe('below');
        else expect(verdict).toBe('above');
      });
    });
  });

  it('orders ranks from most to least urgent', () => {
    expect(RANK_ORDER).toEqual(['P0', 'P1', 'P2', 'P3']);
  });
});
