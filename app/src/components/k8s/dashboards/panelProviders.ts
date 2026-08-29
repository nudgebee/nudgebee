/**
 * Which observability provider each of a panel's accounts will actually be
 * queried through.
 *
 * A metrics/logs/traces panel carries ONE expression but fans out one request
 * per account (usePanelData), and each account resolves its own provider
 * server-side — the integration flagged `default_{metrics,log,traces}_provider`,
 * else the agent-detected one (observability/service.go). So two accounts on one
 * panel can answer through two different query languages, and the author has no
 * way to see it: the mismatched account simply lands in the "No data from X"
 * warning at render time, long after saving.
 *
 * This module resolves that fact for the editor. It is advisory only — nothing
 * here changes which provider a query goes to, it only reports it.
 */
import { useEffect, useRef, useState } from 'react';
import apiAccount from '@api1/account';
import observability from '@api1/observability';
import cache from '@lib/cache';
import { snakeToTitleCase } from 'src/utils/common';
import type { AccountOption, PanelDatasource } from '@api1/dashboards';

/** The `provider_type` argument `observability_get_default_provider` takes. */
export type ProviderType = 'metrics' | 'logs' | 'traces';

/**
 * The provider type a datasource resolves through, or undefined when it has no
 * provider at all: `nudgebee` reads the internal query engine, and the command
 * datasources ARE their integration.
 */
export function providerTypeOf(datasource: PanelDatasource): ProviderType | undefined {
  if (datasource === 'metrics' || datasource === 'logs' || datasource === 'traces') return datasource;
  return undefined;
}

/**
 * CloudWatch is not an integration type — nothing under services/integrations
 * registers it — so it can never carry a `default_metrics_provider` flag and
 * never appears in `available_providers` either. It is reachable only by naming
 * it explicitly, which is exactly what usePanelData does for every AWS account
 * (`metric_provider: 'aws_cloudwatch'`). Reporting the account's stored default
 * for those would name a provider the panel demonstrably does not query.
 */
export const AWS_METRICS_PROVIDER = 'aws_cloudwatch';

/**
 * Elasticsearch, as the provider name is spelled everywhere — `getMetricsSource`
 * and `getLogSource` both match the literal `ES`, so this is the string, not a
 * display label.
 *
 * The one provider whose query needs more than an expression: an ES query names
 * no index, so the backend has to be told which one, and only falls back to the
 * account's configured default when nothing is sent.
 */
export const ES_PROVIDER = 'ES';

/** usePanelData reads this too, so the row and the request can never disagree. */
export function isAwsAccount(account: AccountOption): boolean {
  return (account.cloud_provider || '').toLowerCase() === 'aws';
}

/**
 * CloudWatch is synthesized rather than read off an integration, and CloudIcon
 * has no entry for it — without this it falls through to the generic cloud
 * glyph, on the one provider whose vendor is never in doubt.
 */
export function providerIconKey(provider: string): string {
  return provider === AWS_METRICS_PROVIDER ? 'aws' : provider;
}

/**
 * How a provider is named in the UI. `snakeToTitleCase` is what the Logs tab
 * uses, so vendor names read identically in both places; only the synthesized
 * CloudWatch needs saying differently, since `AWS Cloudwatch` is not the product.
 */
export function providerLabel(provider: string): string {
  return provider === AWS_METRICS_PROVIDER ? 'CloudWatch' : snakeToTitleCase(provider);
}

/**
 * Is this account disabled?
 *
 * `get_cloud_accounts_v2` applies no status filter, so disabled accounts reach
 * every picker in the app, and the observability read path never checks the
 * column either — worse, `GetAgentConnectionDetails` joins the agent row, which
 * OUTLIVES disabling, so a disabled account happily resolves a provider and
 * reports as healthy. Nothing but this check distinguishes it.
 *
 * An account whose status never arrived is treated as live: the field is
 * additive, and guessing "disabled" from a missing value would hide working
 * accounts from any caller that has not been updated.
 */
export function isDisabledAccount(account: AccountOption): boolean {
  const status = (account.status || '').toLowerCase();
  return status !== '' && status !== 'active';
}

export interface AccountProvider {
  account: AccountOption;
  /** The provider the panel will query. Empty means none is configured. */
  provider: string;
  /** Every provider configured for this account and type, default included. */
  available: string[];
  /** The account is disabled, so nothing it resolves will answer a query. */
  disabled: boolean;
}

export interface ProviderGroup {
  provider: string;
  /** Labels of the accounts resolving to this provider, in panel order. */
  accounts: string[];
}

/**
 * The distinct providers across a panel's accounts, commonest first.
 *
 * Accounts with no provider are grouped under `''` like any other, so the row
 * can report "2 accounts have none" rather than silently dropping them.
 */
export function groupByProvider(entries: AccountProvider[]): ProviderGroup[] {
  const groups: ProviderGroup[] = [];
  const byProvider = new Map<string, ProviderGroup>();
  // A disabled account is reported on its own terms, not as a provider that
  // happens to be missing — the fix is to re-enable it, not to configure one.
  for (const entry of entries.filter((e) => !e.disabled)) {
    let group = byProvider.get(entry.provider);
    if (!group) {
      group = { provider: entry.provider, accounts: [] };
      byProvider.set(entry.provider, group);
      groups.push(group);
    }
    group.accounts.push(entry.account.label);
  }
  // Stable: equal counts keep the order the accounts were authored in, so the
  // row does not reshuffle itself as accounts are added.
  return groups.sort((a, b) => b.accounts.length - a.accounts.length);
}

/**
 * The providers offerable for a panel, given what its accounts have configured.
 *
 * `declared` is always included even when no account reports it: a saved panel
 * must not lose its provider because the account that had it was removed from
 * the selection, or because an admin disabled that integration.
 */
export function providerChoices(entries: AccountProvider[], declared: string): string[] {
  const seen = new Set<string>();
  const choices: string[] = [];
  const add = (provider: string) => {
    if (!provider || seen.has(provider)) return;
    seen.add(provider);
    choices.push(provider);
  };
  add(declared);
  for (const entry of entries.filter((e) => !e.disabled)) {
    add(entry.provider);
    entry.available.forEach(add);
  }
  return choices;
}

/** The panel's accounts that will NOT answer a query written for `declared`. */
export function mismatchedAccounts(entries: AccountProvider[], declared: string): string[] {
  if (!declared) return [];
  return entries.filter((e) => !e.disabled && !e.available.includes(declared)).map((e) => e.account.label);
}

/** The panel's accounts that are disabled, and so answer nothing at all. */
export function disabledAccounts(entries: AccountProvider[]): string[] {
  return entries.filter((e) => e.disabled).map((e) => e.account.label);
}

/** Do the panel's accounts disagree about which provider answers the query? */
export function isMixed(groups: ProviderGroup[]): boolean {
  return groups.length > 1;
}

/**
 * The providers configured for one account, as the editor needs them.
 *
 * Never rejects: `get_default_provider` errors for an account with neither a
 * default integration nor a connected agent, and that is a state to REPORT
 * ("no provider configured"), not a failure to swallow the whole row over.
 */
/**
 * Folds CloudWatch into an AWS account's metrics answer.
 *
 * CloudWatch is not an integration type, so the server can neither report it as
 * the account default nor list it as available — yet `usePanelData` forces it for
 * every AWS account that names no provider. It is therefore this account's
 * EFFECTIVE default whatever the server says, and it has to be offered as a
 * choice too.
 *
 * Additive, not a replacement: an AWS account can also have Datadog (or any other
 * metrics integration) configured, and those stay selectable. Overwriting the
 * available list with CloudWatch alone would hide every provider the account
 * actually has.
 */
function withAwsMetrics(account: AccountOption, providerType: ProviderType, resolved: AccountProvider): AccountProvider {
  if (providerType !== 'metrics' || !isAwsAccount(account)) return resolved;
  return {
    ...resolved,
    provider: AWS_METRICS_PROVIDER,
    available: resolved.available.includes(AWS_METRICS_PROVIDER) ? resolved.available : [AWS_METRICS_PROVIDER, ...resolved.available],
  };
}

async function fetchAccountProvider(account: AccountOption, providerType: ProviderType): Promise<AccountProvider> {
  // A disabled account answers nothing, so which provider it would have used is
  // not a question worth a request — and asking would get a confident answer off
  // the surviving agent row, which is how this stays invisible.
  if (isDisabledAccount(account)) {
    return { account, provider: '', available: [], disabled: true };
  }
  const empty: AccountProvider = { account, provider: '', available: [], disabled: false };
  // The demo account has no integrations to resolve; the logs tab skips it too.
  if (!account.value || account.value === 'demo') return withAwsMetrics(account, providerType, empty);

  const key = `panel_provider_${account.value}_${providerType}`;
  const cached = cache.get(key);
  // The cache holds what the SERVER said, so the CloudWatch fold is applied on
  // the way out of both branches rather than baked into the stored value.
  if (cached) {
    return withAwsMetrics(account, providerType, { account, provider: cached.provider, available: cached.available, disabled: false });
  }

  try {
    const res: any = await apiAccount.getDefaultProvider({ account_id: account.value, provider_type: providerType });
    if (res?.data?.errors) return withAwsMetrics(account, providerType, empty);
    const obs = res?.data?.data?.observability_get_default_provider;
    const provider = obs?.provider || '';
    // Drop malformed entries so the badge list never renders an unnamed chip.
    const listed: string[] = Array.isArray(obs?.available_providers)
      ? obs.available_providers.map((p: any) => p?.provider).filter((p: any): p is string => Boolean(p))
      : [];
    // The default is always offered, even when the available list missed it —
    // otherwise the row would claim a provider is configured and then not list it.
    const available = provider && !listed.includes(provider) ? [provider, ...listed] : listed;
    const resolved = { provider, available };
    cache.set(key, resolved, 60 * 60);
    return withAwsMetrics(account, providerType, { account, ...resolved, disabled: false });
  } catch {
    return withAwsMetrics(account, providerType, empty);
  }
}

/**
 * How many accounts the row will resolve providers for.
 *
 * A type-scoped panel ("every K8S account") can resolve to dozens, and each one
 * is its own request — opening the editor must not fan out fifty calls to
 * populate an advisory row. Well above any realistic hand-picked selection, so
 * in practice only the "all accounts of a type" case is ever truncated, and the
 * row says so rather than quietly reporting a subset as the whole answer.
 */
export const MAX_PROVIDER_ACCOUNTS = 25;

export interface PanelProvidersState {
  loading: boolean;
  entries: AccountProvider[];
  /** Accounts in scope, including any beyond MAX_PROVIDER_ACCOUNTS. */
  total: number;
}

/**
 * Resolves every account's provider for the panel being edited.
 *
 * Keyed on the account ids rather than the array, which is a new object every
 * render, and guarded against the response of a superseded selection landing
 * after the current one — the account list changes on every click in a
 * multi-select, so an unguarded fetch here would routinely report the previous
 * selection's providers. Re-resolving is cheap: each account's answer is cached
 * for an hour, so a re-render or a reorder costs no requests.
 */
export function usePanelProviders(accounts: AccountOption[], providerType: ProviderType | undefined): PanelProvidersState {
  const [state, setState] = useState<PanelProvidersState>({ loading: false, entries: [], total: 0 });
  /**
   * The provider type the entries in state were resolved for.
   *
   * Holding the previous answer through a refetch keeps the row from flashing a
   * skeleton every time an account is ticked — but only while it is an answer to
   * the SAME question. Switching the datasource asks a different one, and the old
   * entries would render as metrics badges under a logs panel, captioned with the
   * new type. Wrong information, which is the thing this row exists to prevent.
   */
  const entriesType = useRef<ProviderType | undefined>(undefined);
  const accountsKey = accounts.map((a) => a.value).join(',');

  useEffect(() => {
    if (!providerType || accounts.length === 0) {
      entriesType.current = undefined;
      setState({ loading: false, entries: [], total: 0 });
      return;
    }
    let cancelled = false;
    const total = accounts.length;
    const checked = accounts.slice(0, MAX_PROVIDER_ACCOUNTS);
    const sameQuestion = entriesType.current === providerType;
    entriesType.current = providerType;
    setState((prev) => ({ loading: true, entries: sameQuestion ? prev.entries : [], total }));
    Promise.all(checked.map((account) => fetchAccountProvider(account, providerType))).then((entries) => {
      if (cancelled) return;
      setState({ loading: false, entries, total });
    });
    return () => {
      cancelled = true;
    };
    // accountsKey stands in for `accounts`, which is a new array every render.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [accountsKey, providerType]);

  return state;
}

/**
 * The ES indexes (and data streams) one account exposes, for the panel editor's
 * index picker.
 *
 * Cached under the same key and TTL the log query builder uses, so opening the
 * panel editor after using the Logs tab costs nothing. The picker is free-solo
 * regardless — a wildcard pattern outside the listed indexes is valid, and a
 * failed lookup must not stop an author typing one.
 */
export function useEsIndexes(accountId: string, enabled: boolean): { loading: boolean; indexes: string[] } {
  const [state, setState] = useState<{ loading: boolean; indexes: string[] }>({ loading: false, indexes: [] });

  useEffect(() => {
    if (!enabled || !accountId || accountId === 'demo') {
      setState({ loading: false, indexes: [] });
      return;
    }
    const cached = cache.getWithSuffix(`${accountId}.es.indexes`, null, {});
    if (cached) {
      setState({ loading: false, indexes: cached });
      return;
    }
    let cancelled = false;
    setState({ loading: true, indexes: [] });
    observability
      .logIndexList(accountId, ES_PROVIDER)
      .then((res: any) => {
        if (cancelled) return;
        const indexes: string[] = (res?.data?.data?.logs_list_labels || [])
          .map((m: any) => m?.label)
          .filter((label: any): label is string => Boolean(label));
        if (indexes.length) cache.setWithSuffix(`${accountId}.es.indexes`, indexes, {}, 60 * 60 * 6);
        setState({ loading: false, indexes });
      })
      .catch(() => {
        if (!cancelled) setState({ loading: false, indexes: [] });
      });
    return () => {
      cancelled = true;
    };
  }, [accountId, enabled]);

  return state;
}
