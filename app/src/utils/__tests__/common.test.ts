import { toSeverityLevel } from '../common';

describe('toSeverityLevel', () => {
  it('maps the canonical 5-tier values to themselves', () => {
    expect(toSeverityLevel('critical')).toBe('critical');
    expect(toSeverityLevel('high')).toBe('high');
    expect(toSeverityLevel('medium')).toBe('medium');
    expect(toSeverityLevel('low')).toBe('low');
    expect(toSeverityLevel('info')).toBe('info');
  });

  it("folds 'warning' onto the amber 'medium' tier (the DS has no Warning level)", () => {
    expect(toSeverityLevel('warning')).toBe('medium');
    expect(toSeverityLevel('Warning')).toBe('medium');
  });

  it('collapses unknown / empty values to info', () => {
    expect(toSeverityLevel('bogus')).toBe('info');
    expect(toSeverityLevel('')).toBe('info');
    expect(toSeverityLevel(undefined)).toBe('info');
    expect(toSeverityLevel(null)).toBe('info');
  });
});
