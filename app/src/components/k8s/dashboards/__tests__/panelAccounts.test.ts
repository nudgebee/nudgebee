import type { AccountOption } from '@api1/dashboards';
import {
  applyAccountFilter,
  coversAllOfTypes,
  deriveAccountType,
  deriveAccountTypes,
  describePanelScope,
  panelScopeFromTypes,
  panelQueryAccounts,
  panelScope,
  panelScopeLabels,
  resolvePanelAccounts,
} from '../panelAccounts';

const ACCOUNTS: AccountOption[] = [
  { label: 'prod-eks', value: 'a1', cloud_provider: 'K8S' },
  { label: 'staging-eks', value: 'a2', cloud_provider: 'K8S' },
  { label: 'billing-aws', value: 'a3', cloud_provider: 'AWS' },
];

describe('resolvePanelAccounts', () => {
  it('expands an account type to every account of that provider', () => {
    expect(resolvePanelAccounts({ account_type: 'K8S' }, ACCOUNTS).map((a) => a.value)).toEqual(['a1', 'a2']);
    expect(resolvePanelAccounts({ account_type: 'AWS' }, ACCOUNTS).map((a) => a.value)).toEqual(['a3']);
  });

  it('resolves explicit ids in the order they were authored', () => {
    // Not the order of the account list — series should stay where the author
    // put them.
    expect(resolvePanelAccounts({ account_ids: ['a3', 'a1'] }, ACCOUNTS).map((a) => a.value)).toEqual(['a3', 'a1']);
  });

  it('drops ids the viewer cannot see instead of querying them', () => {
    // Deleted account, or access revoked. Passing it through would just 403.
    expect(resolvePanelAccounts({ account_ids: ['a1', 'gone'] }, ACCOUNTS).map((a) => a.value)).toEqual(['a1']);
  });

  it('returns nothing for an unknown type or an empty selection', () => {
    expect(resolvePanelAccounts({ account_type: 'AZURE' }, ACCOUNTS)).toEqual([]);
    expect(resolvePanelAccounts({ account_ids: [] }, ACCOUNTS)).toEqual([]);
    expect(resolvePanelAccounts({}, ACCOUNTS)).toEqual([]);
  });

  it('lets account_type win when both are somehow present', () => {
    // The backend rejects both-or-neither, but a hand-edited or imported
    // definition must still render deterministically rather than merge the two.
    const resolved = resolvePanelAccounts({ account_type: 'AWS', account_ids: ['a1'] }, ACCOUNTS);
    expect(resolved.map((a) => a.value)).toEqual(['a3']);
  });

  it('returns nothing when the viewer has no accounts at all', () => {
    expect(resolvePanelAccounts({ account_type: 'K8S' }, [])).toEqual([]);
    expect(resolvePanelAccounts({ account_ids: ['a1'] }, [])).toEqual([]);
  });
});

describe('deriveAccountType', () => {
  it('reads the type back off the accounts when the panel stores only ids', () => {
    // An id-scoped panel stores no type, but the editor still has to show one —
    // otherwise reopening the panel filters the account list to nothing.
    expect(deriveAccountType({ account_ids: ['a3'] }, ACCOUNTS)).toBe('AWS');
    expect(deriveAccountType({ account_ids: ['a1', 'a2'] }, ACCOUNTS)).toBe('K8S');
  });

  it('prefers an explicit type', () => {
    expect(deriveAccountType({ account_type: 'AZURE' }, ACCOUNTS)).toBe('AZURE');
  });

  it('is empty when nothing resolves', () => {
    expect(deriveAccountType({ account_ids: ['gone'] }, ACCOUNTS)).toBe('');
    expect(deriveAccountType({}, ACCOUNTS)).toBe('');
  });
});

describe('panelScope', () => {
  it('drops the type once specific accounts are chosen', () => {
    // Both controls are populated while editing — the type also filters the
    // account list — but the stored panel must carry only one of them.
    expect(panelScope('K8S', ['a1', 'a2'])).toEqual({ account_type: undefined, account_ids: ['a1', 'a2'] });
  });

  it('keeps the type when no accounts are chosen', () => {
    expect(panelScope('K8S', [])).toEqual({ account_type: 'K8S', account_ids: [] });
  });

  it('treats blank ids as no selection', () => {
    expect(panelScope('AWS', ['', ''])).toEqual({ account_type: 'AWS', account_ids: [] });
  });

  it('produces an empty scope when neither is set, so save-time validation catches it', () => {
    expect(panelScope('', [])).toEqual({ account_type: undefined, account_ids: [] });
  });
});

describe('panelScopeLabels', () => {
  it('shows the provider for a type-scoped panel', () => {
    expect(panelScopeLabels({ account_type: 'AWS' }, ACCOUNTS)).toEqual(['AWS']);
  });

  it('shows every account name for an id-scoped panel', () => {
    expect(panelScopeLabels({ account_ids: ['a1', 'a3'] }, ACCOUNTS)).toEqual(['prod-eks', 'billing-aws']);
  });

  it('is empty when nothing resolves', () => {
    expect(panelScopeLabels({ account_ids: ['gone'] }, ACCOUNTS)).toEqual([]);
    expect(panelScopeLabels({}, ACCOUNTS)).toEqual([]);
  });
});

// The panel filter's options are exactly one panel's own accounts, so the
// per-panel narrowing is resolvePanelAccounts + applyAccountFilter composed.
describe('applyAccountFilter', () => {
  it('treats an empty filter as no filter, not as "show nothing"', () => {
    expect(applyAccountFilter(ACCOUNTS, [])).toEqual(ACCOUNTS);
    expect(applyAccountFilter(ACCOUNTS, undefined)).toEqual(ACCOUNTS);
  });

  it('narrows a type-scoped panel to the picked accounts', () => {
    const scoped = resolvePanelAccounts({ account_type: 'K8S' }, ACCOUNTS);
    expect(applyAccountFilter(scoped, ['a2']).map((a) => a.value)).toEqual(['a2']);
  });

  it('cannot widen a panel beyond its own scope', () => {
    // a3 is an AWS account; a K8S panel must never chart it just because the
    // filter names it.
    const scoped = resolvePanelAccounts({ account_type: 'K8S' }, ACCOUNTS);
    expect(applyAccountFilter(scoped, ['a3'])).toEqual([]);
  });

  it('preserves the panel-order the author picked', () => {
    const scoped = resolvePanelAccounts({ account_ids: ['a3', 'a1'] }, ACCOUNTS);
    expect(applyAccountFilter(scoped, ['a1', 'a3']).map((a) => a.value)).toEqual(['a3', 'a1']);
  });
});

describe('panelQueryAccounts', () => {
  it('loads a single-account panel straight away — there is nothing to choose', () => {
    const scoped = resolvePanelAccounts({ account_ids: ['a1'] }, ACCOUNTS);
    expect(panelQueryAccounts(scoped, [])).toEqual({ accounts: scoped, autoSelected: false });
    // Even a type-scoped panel loads when the provider has exactly one account.
    const oneOfType = resolvePanelAccounts({ account_type: 'AWS' }, ACCOUNTS);
    expect(panelQueryAccounts(oneOfType, [])).toEqual({ accounts: oneOfType, autoSelected: false });
  });

  it('auto-selects the first account when the panel spans several', () => {
    // Charting ~100 clusters at once is unreadable and costs one provider
    // request per account. Querying ONE costs the same as waiting for a choice
    // and shows data instead of an empty panel.
    const scoped = resolvePanelAccounts({ account_type: 'K8S' }, ACCOUNTS);
    expect(panelQueryAccounts(scoped, [])).toEqual({ accounts: [ACCOUNTS[0]], autoSelected: true });
    expect(panelQueryAccounts(scoped, undefined)).toEqual({ accounts: [ACCOUNTS[0]], autoSelected: true });
  });

  it('auto-selects in AUTHORING order, so every viewer lands on the same account', () => {
    const scoped = resolvePanelAccounts({ account_ids: ['a3', 'a1'] }, ACCOUNTS);
    expect(panelQueryAccounts(scoped, []).accounts).toEqual([ACCOUNTS[2]]);
  });

  it('queries exactly what was picked once a selection exists', () => {
    const scoped = resolvePanelAccounts({ account_type: 'K8S' }, ACCOUNTS);
    expect(panelQueryAccounts(scoped, ['a2'])).toEqual({ accounts: [ACCOUNTS[1]], autoSelected: false });
  });

  it('reports a filter miss as a miss, not as an auto-selection', () => {
    // a3 is AWS; a K8S panel filtered to it has nothing to draw. Falling back to
    // the first account would silently ignore what the viewer picked.
    const scoped = resolvePanelAccounts({ account_type: 'K8S' }, ACCOUNTS);
    expect(panelQueryAccounts(scoped, ['a3'])).toEqual({ accounts: [], autoSelected: false });
  });

  it('has nothing to select when the panel has no accounts at all', () => {
    expect(panelQueryAccounts([], [])).toEqual({ accounts: [], autoSelected: false });
  });

  it('keeps every account for a datasource that takes them in one call', () => {
    // The query engine answers an account_id LIST in a single request, so the
    // two reasons for auto-selecting — cost per call, and N identical series —
    // do not apply. Slicing there would show one account's rows under a title
    // that claims the whole estate, which is the rollup widgets' entire job.
    const scoped = resolvePanelAccounts({ account_type: 'K8S' }, ACCOUNTS);
    expect(panelQueryAccounts(scoped, [], true)).toEqual({ accounts: scoped, autoSelected: false });

    // An explicit filter still wins, exactly as it does for every other panel.
    expect(panelQueryAccounts(scoped, ['a2'], true)).toEqual({ accounts: [ACCOUNTS[1]], autoSelected: false });
  });
});

describe('describePanelScope', () => {
  it('names the type, the single account, or the count', () => {
    expect(describePanelScope({ account_type: 'AWS' }, ACCOUNTS)).toBe('All AWS');
    expect(describePanelScope({ account_ids: ['a1'] }, ACCOUNTS)).toBe('prod-eks');
    expect(describePanelScope({ account_ids: ['a1', 'a3'] }, ACCOUNTS)).toBe('2 accounts');
  });

  it('says so when nothing resolves', () => {
    expect(describePanelScope({ account_ids: ['gone'] }, ACCOUNTS)).toBe('No account');
    expect(describePanelScope({}, ACCOUNTS)).toBe('No account');
  });
});

describe('multi-provider scope', () => {
  const ACCOUNTS = [
    { label: 'cluster', value: 'k1', cloud_provider: 'K8S', kind: 'kubernetes' },
    { label: 'aws-prod', value: 'a1', cloud_provider: 'AWS', kind: 'cloud' },
    { label: 'aws-dev', value: 'a2', cloud_provider: 'AWS', kind: 'cloud' },
    { label: 'gcp-prod', value: 'g1', cloud_provider: 'GCP', kind: 'cloud' },
  ];

  it('keeps one provider as a type, so the panel widens as accounts are connected', () => {
    expect(panelScopeFromTypes(['AWS'], [], ACCOUNTS)).toEqual({ account_type: 'AWS', account_ids: [] });
  });

  it('spells several providers out as accounts, which is the only way to store them', () => {
    expect(panelScopeFromTypes(['AWS', 'GCP'], [], ACCOUNTS)).toEqual({ account_type: undefined, account_ids: ['a1', 'a2', 'g1'] });
  });

  it('lets hand-picked accounts win over the providers that filtered them', () => {
    expect(panelScopeFromTypes(['AWS', 'GCP'], ['a1'], ACCOUNTS)).toEqual({ account_type: undefined, account_ids: ['a1'] });
  });

  it('answers nothing for no choice at all, rather than every account', () => {
    expect(panelScopeFromTypes([], [], ACCOUNTS)).toEqual({ account_type: undefined, account_ids: [] });
  });

  it('reads the providers back out of a stored panel', () => {
    expect(deriveAccountTypes({ account_type: 'AWS', account_ids: [] }, ACCOUNTS)).toEqual(['AWS']);
    // Deduplicated: two AWS accounts are one provider.
    expect(deriveAccountTypes({ account_ids: ['a1', 'a2', 'g1'] }, ACCOUNTS)).toEqual(['AWS', 'GCP']);
  });

  it('tells "all of these providers" apart from a hand-picked subset', () => {
    // Every AWS + GCP account — what panelScopeFromTypes writes, so the editor
    // should reopen it as two providers with the account picker empty.
    expect(coversAllOfTypes({ account_ids: ['a1', 'a2', 'g1'] }, ACCOUNTS)).toBe(true);
    // One of two AWS accounts is a real choice, and must be shown as one.
    expect(coversAllOfTypes({ account_ids: ['a1'] }, ACCOUNTS)).toBe(false);
    expect(coversAllOfTypes({ account_ids: [] }, ACCOUNTS)).toBe(false);
  });

  it('round-trips a multi-provider scope through the editor', () => {
    const stored = panelScopeFromTypes(['AWS', 'GCP'], [], ACCOUNTS);
    const types = deriveAccountTypes(stored, ACCOUNTS);
    const ids = coversAllOfTypes(stored, ACCOUNTS) ? [] : stored.account_ids || [];

    expect(types).toEqual(['AWS', 'GCP']);
    expect(ids).toEqual([]);
    expect(panelScopeFromTypes(types, ids, ACCOUNTS)).toEqual(stored);
  });
});
