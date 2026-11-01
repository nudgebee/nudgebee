import { DEFAULT_EVENT_ANALYSIS_FORM, buildEventAnalysisValue, parseEventAnalysisForm } from '@api1/account/eventAnalysisSettings';

describe('event analysis settings', () => {
  it('defaults when the account has no stored value', () => {
    expect(parseEventAnalysisForm(undefined)).toEqual(DEFAULT_EVENT_ANALYSIS_FORM);
    expect(parseEventAnalysisForm('{bad json')).toEqual(DEFAULT_EVENT_ANALYSIS_FORM);
  });

  it('round-trips a stored value through the form', () => {
    const stored = JSON.stringify({
      reuse_related: false,
      reuse_window_hours: 6,
      always_fresh_namespaces: ['demo', 'qa'],
      excluded_namespaces: ['sandbox'],
    });
    const form = parseEventAnalysisForm(stored);
    expect(form).toEqual({ reuseRelated: false, reuseWindowHours: '6', alwaysFreshNamespaces: 'demo, qa', excludedNamespaces: 'sandbox' });
    expect(buildEventAnalysisValue(form)).toEqual({ value: stored });
  });

  it('saves a blank window as the platform default', () => {
    const built = buildEventAnalysisValue(DEFAULT_EVENT_ANALYSIS_FORM);
    expect(built).toEqual({
      value: JSON.stringify({ reuse_related: true, reuse_window_hours: null, always_fresh_namespaces: [], excluded_namespaces: [] }),
    });
  });

  it.each(['0', '721', '1.5', 'abc'])('rejects reuse window %s', (reuseWindowHours) => {
    expect(buildEventAnalysisValue({ ...DEFAULT_EVENT_ANALYSIS_FORM, reuseWindowHours })).toHaveProperty('error');
  });

  it.each(['Demo', 'bad_name', 'a'.repeat(64)])('rejects namespace %s', (ns) => {
    expect(buildEventAnalysisValue({ ...DEFAULT_EVENT_ANALYSIS_FORM, excludedNamespaces: ns })).toHaveProperty('error');
  });
});
