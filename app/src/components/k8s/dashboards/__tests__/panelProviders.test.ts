import type { AccountOption } from '@api1/dashboards';
import {
  AWS_METRICS_PROVIDER,
  disabledAccounts,
  groupByProvider,
  isAwsAccount,
  isDisabledAccount,
  isMixed,
  mismatchedAccounts,
  providerChoices,
  providerLabel,
  providerTypeOf,
  type AccountProvider,
} from '../panelProviders';

const account = (label: string, cloud_provider = 'K8S'): AccountOption => ({ label, value: label, cloud_provider });

const entry = (label: string, provider: string, available: string[] = []): AccountProvider => ({
  account: account(label),
  provider,
  available: available.length > 0 ? available : provider ? [provider] : [],
  disabled: false,
});

const disabledEntry = (label: string): AccountProvider => ({
  account: { label, value: label, cloud_provider: 'K8S', status: 'disabled' },
  provider: '',
  available: [],
  disabled: true,
});

describe('providerTypeOf', () => {
  it('maps the three provider-backed datasources to their provider type', () => {
    expect(providerTypeOf('metrics')).toBe('metrics');
    expect(providerTypeOf('logs')).toBe('logs');
    expect(providerTypeOf('traces')).toBe('traces');
  });

  it('has no provider for the query-engine and command datasources', () => {
    // nudgebee reads the internal query engine; redis/rabbitmq/postgresql ARE
    // their integration. Resolving a provider for these would be a wasted call
    // and a row claiming something the panel never consults.
    expect(providerTypeOf('nudgebee')).toBeUndefined();
    expect(providerTypeOf('redis')).toBeUndefined();
    expect(providerTypeOf('rabbitmq')).toBeUndefined();
    expect(providerTypeOf('postgresql')).toBeUndefined();
  });
});

describe('isAwsAccount', () => {
  it('matches usePanelData regardless of case, which is where the value comes from', () => {
    expect(isAwsAccount(account('a', 'AWS'))).toBe(true);
    expect(isAwsAccount(account('a', 'aws'))).toBe(true);
    expect(isAwsAccount(account('a', 'K8S'))).toBe(false);
    expect(isAwsAccount({ label: 'a', value: 'a', cloud_provider: '' })).toBe(false);
  });

  it('names CloudWatch as the AWS metrics provider', () => {
    // Not an integration type, so it can never come back from the API — the row
    // has to synthesize it or it will report a provider the panel never queries.
    expect(AWS_METRICS_PROVIDER).toBe('aws_cloudwatch');
  });
});

describe('groupByProvider', () => {
  it('collapses accounts that agree into one group', () => {
    const groups = groupByProvider([entry('prod', 'prometheus'), entry('staging', 'prometheus')]);
    expect(groups).toEqual([{ provider: 'prometheus', accounts: ['prod', 'staging'] }]);
    expect(isMixed(groups)).toBe(false);
  });

  it('orders the commonest provider first', () => {
    const groups = groupByProvider([entry('a', 'datadog'), entry('b', 'prometheus'), entry('c', 'prometheus')]);
    expect(groups.map((g) => g.provider)).toEqual(['prometheus', 'datadog']);
    expect(isMixed(groups)).toBe(true);
  });

  it('keeps accounts with no provider as their own group rather than dropping them', () => {
    // Dropping them would let the row report "Prometheus, all 2 accounts" for a
    // panel where one account returns nothing at all.
    const groups = groupByProvider([entry('prod', 'prometheus'), entry('orphan', '')]);
    expect(groups).toContainEqual({ provider: '', accounts: ['orphan'] });
  });

  it('preserves authoring order inside a group', () => {
    const groups = groupByProvider([entry('z', 'loki'), entry('a', 'loki')]);
    expect(groups[0].accounts).toEqual(['z', 'a']);
  });

  it('is empty for no accounts', () => {
    expect(groupByProvider([])).toEqual([]);
    expect(isMixed([])).toBe(false);
  });
});

describe('providerChoices', () => {
  it('offers every provider any account has, not only the defaults', () => {
    expect(providerChoices([entry('a', 'prometheus', ['prometheus', 'datadog']), entry('b', 'ES')], '')).toEqual(['prometheus', 'datadog', 'ES']);
  });

  it('keeps a saved provider no account reports any more', () => {
    // An admin disabling the integration, or the author dropping the only
    // account that had it, must not silently rewrite what the panel queries.
    expect(providerChoices([entry('a', 'prometheus')], 'datadog')).toEqual(['datadog', 'prometheus']);
  });

  it('never offers the empty provider as a choice', () => {
    expect(providerChoices([entry('a', '')], '')).toEqual([]);
  });
});

describe('mismatchedAccounts', () => {
  it('is empty when no provider is named — every account uses its own default', () => {
    expect(mismatchedAccounts([entry('a', 'prometheus'), entry('b', 'ES')], '')).toEqual([]);
  });

  it('accepts an account that HAS the provider without defaulting to it', () => {
    // Naming a provider overrides the account default on the request itself, so
    // "configured" is the test, not "default".
    expect(mismatchedAccounts([entry('a', 'datadog', ['datadog', 'prometheus'])], 'prometheus')).toEqual([]);
  });

  it('names the accounts that cannot serve it', () => {
    expect(mismatchedAccounts([entry('good', 'prometheus'), entry('bad', 'ES')], 'prometheus')).toEqual(['bad']);
  });
});

describe('providerLabel', () => {
  it('says CloudWatch, which is what the product is called', () => {
    expect(providerLabel(AWS_METRICS_PROVIDER)).toBe('CloudWatch');
  });

  it('otherwise reads exactly as the Logs tab names it', () => {
    expect(providerLabel('prometheus')).toBe('Prometheus');
    expect(providerLabel('azure_app_insights')).toBe('Azure App Insights');
    expect(providerLabel('ES')).toBe('ES');
  });
});

describe('disabled accounts', () => {
  it('treats a missing status as live, since the field is additive', () => {
    // Guessing "disabled" from an absent value would hide working accounts from
    // any caller that has not been updated to send it.
    expect(isDisabledAccount({ label: 'a', value: 'a', cloud_provider: 'K8S' })).toBe(false);
    expect(isDisabledAccount({ label: 'a', value: 'a', cloud_provider: 'K8S', status: '' })).toBe(false);
  });

  it('treats anything that is not active as disabled', () => {
    expect(isDisabledAccount({ label: 'a', value: 'a', cloud_provider: 'K8S', status: 'active' })).toBe(false);
    expect(isDisabledAccount({ label: 'a', value: 'a', cloud_provider: 'K8S', status: 'ACTIVE' })).toBe(false);
    expect(isDisabledAccount({ label: 'a', value: 'a', cloud_provider: 'K8S', status: 'disabled' })).toBe(true);
    expect(isDisabledAccount({ label: 'a', value: 'a', cloud_provider: 'K8S', status: 'inactive' })).toBe(true);
  });

  it('is reported on its own terms, not as a provider disagreement', () => {
    // A disabled account has no provider, and grouping it as one would turn a
    // homogeneous panel into a spurious "these accounts disagree" warning.
    const entries = [entry('live', 'prometheus'), disabledEntry('off')];
    expect(groupByProvider(entries)).toEqual([{ provider: 'prometheus', accounts: ['live'] }]);
    expect(isMixed(groupByProvider(entries))).toBe(false);
    expect(disabledAccounts(entries)).toEqual(['off']);
  });

  it('is never counted as failing to match a declared provider', () => {
    // "off does not have Prometheus configured" is the wrong diagnosis; the fix
    // is to re-enable it, not to configure a provider on it.
    expect(mismatchedAccounts([entry('live', 'prometheus'), disabledEntry('off')], 'prometheus')).toEqual([]);
  });

  it('contributes no provider choices', () => {
    expect(providerChoices([entry('live', 'prometheus'), disabledEntry('off')], '')).toEqual(['prometheus']);
  });
});
