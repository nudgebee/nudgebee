import { getSessionAccountIds, hasAnyCustomGrant, isTenantWideRole } from '@lib/auth';
import { accessibleAccountOptions, listenAccountPickerOptions, listensToSeveralAccounts, triggerListenAccounts } from '../listenAccounts';

jest.mock('@lib/auth', () => ({
  getSessionAccountIds: jest.fn(),
  hasAnyCustomGrant: jest.fn(),
  isTenantWideRole: jest.fn(),
}));

const trigger = (type: string, params: Record<string, any>) => ({ type: 'trigger', data: { trigger: { type, params } } });

describe('triggerListenAccounts', () => {
  it('reads accounts on event triggers and clusters on optimization triggers', () => {
    expect(triggerListenAccounts({ type: 'event', params: { accounts: ['a', 'b'] } })).toEqual(['a', 'b']);
    expect(triggerListenAccounts({ type: 'optimization', params: { clusters: ['c'] } })).toEqual(['c']);
  });

  it('drops blanks and duplicates, and is empty for other trigger types', () => {
    expect(triggerListenAccounts({ type: 'event', params: { accounts: ['a', '', 'a', 7] } })).toEqual(['a']);
    expect(triggerListenAccounts({ type: 'schedule', params: { accounts: ['a'] } })).toEqual([]);
    expect(triggerListenAccounts(undefined)).toEqual([]);
  });
});

describe('listensToSeveralAccounts', () => {
  it('is true only when a trigger names more than one account', () => {
    expect(listensToSeveralAccounts([trigger('event', { accounts: ['a', 'b'] })])).toBe(true);
    expect(listensToSeveralAccounts([trigger('optimization', { clusters: ['a', 'b'] })])).toBe(true);
    expect(listensToSeveralAccounts([trigger('event', { accounts: ['a'] }), { type: 'action', data: {} }])).toBe(false);
    expect(listensToSeveralAccounts([trigger('event', { accounts: ['a', 'a'] })])).toBe(false);
  });
});

describe('accessibleAccountOptions', () => {
  const options = [{ value: 'a' }, { value: 'b' }, { value: 'c' }];

  beforeEach(() => {
    (isTenantWideRole as jest.Mock).mockReturnValue(false);
    (hasAnyCustomGrant as jest.Mock).mockReturnValue(false);
    (getSessionAccountIds as jest.Mock).mockReturnValue(['a', 'c']);
  });

  it('keeps only the accounts the session grants', () => {
    expect(accessibleAccountOptions(options)).toEqual([{ value: 'a' }, { value: 'c' }]);
  });

  it('keeps every account for tenant-wide and custom-grant users', () => {
    (isTenantWideRole as jest.Mock).mockReturnValue(true);
    expect(accessibleAccountOptions(options)).toEqual(options);
    (isTenantWideRole as jest.Mock).mockReturnValue(false);
    (hasAnyCustomGrant as jest.Mock).mockReturnValue(true);
    expect(accessibleAccountOptions(options)).toEqual(options);
  });
});

// #40526: while accounts loaded, saved accounts showed as raw IDs and the own account vanished.
describe('listenAccountPickerOptions', () => {
  const acct = (value: string, label: string) => ({ value, label, cloud_provider: 'K8s', group: 'K8s' });

  beforeEach(() => {
    (isTenantWideRole as jest.Mock).mockReturnValue(false);
    (hasAnyCustomGrant as jest.Mock).mockReturnValue(false);
    (getSessionAccountIds as jest.Mock).mockReturnValue(['own', 'b']);
  });

  it('labels every shown account, own included, without calling any out of reach while loading', () => {
    const options = listenAccountPickerOptions([], ['own', 'b'], 'own', true);
    expect(options.map((o) => [o.value, o.label, o.group])).toEqual([
      ['own', "This automation's account", ''],
      ['b', 'Loading…', ''],
    ]);
  });

  it('uses account names once loaded and lists only the unreachable one by id', () => {
    const options = listenAccountPickerOptions([acct('own', 'k8s-dev'), acct('b', 'k8s-prod')], ['own', 'gone'], 'own', false);
    expect(options.map((o) => [o.value, o.label, o.group])).toEqual([
      ['own', 'k8s-dev', 'K8s'],
      ['b', 'k8s-prod', 'K8s'],
      ['gone', 'gone', 'No access'],
    ]);
  });

  it('does not call accounts out of reach when the account list failed to load', () => {
    const options = listenAccountPickerOptions([], ['own', 'b'], 'own', false);
    expect(options.map((o) => [o.value, o.group])).toEqual([
      ['own', ''],
      ['b', ''],
    ]);
  });

  it('keeps the own account when the account list has no entry for it', () => {
    expect(listenAccountPickerOptions([], ['own'], 'own', false)).toEqual([
      { value: 'own', label: "This automation's account", cloud_provider: '', group: '' },
    ]);
  });
});
