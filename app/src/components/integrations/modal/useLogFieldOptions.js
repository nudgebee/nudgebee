/**
 * useLogFieldOptions — the queryable field names of a log backend, probed from the
 * config values currently in the form.
 *
 * Shared by the two Advanced Settings sections that need them: Default Log Filters
 * (which column to always filter on) and Log Label Mapping (which field holds each
 * concept). They are the same list, so they share one fetch — two independent probes
 * of the same endpoint for the same account would be wasteful and could disagree.
 *
 * Uses `logs_list_labels` with its optional `integration_config_values`: without them it
 * resolves the SAVED integration, which is why correcting a wrong URL in the form and
 * looking for the new backend's fields used to query the old one. Passing the form's
 * current values makes it answer for the configuration being edited instead.
 *
 * Keyed by (account, index) because both change what comes back.
 */
import { useCallback, useEffect, useRef, useState } from 'react';
import observability from '@api1/observability';

export const fieldOptionsKey = (accountId, index) => `${accountId}::${index || ''}`;

/**
 * Resolves which index a given account's fields should come from.
 *
 * Per-account first: one Elasticsearch endpoint routinely serves several accounts, each
 * with its own index (what the Per-Account Index cards configure). Reading the top-level
 * index for all of them would offer fields that do not exist in the index this account's
 * queries actually run against — the failure these editors exist to prevent. Empty lets
 * the provider fall back to whatever it has configured.
 */
export function indexForAccount(indexRules, accountId, topLevelIndex) {
  if (!accountId) return '';
  const match = (indexRules || []).find((r) => r.accountId === accountId);
  return (match?.log_index || '').trim() || (topLevelIndex || '').trim();
}

export default function useLogFieldOptions({ enabled, provider, providerSource, buildProbeConfigValues, accountIndexPairs }) {
  const [options, setOptions] = useState({});

  // Held in a ref because the modal rebuilds this closure every render; as a hook
  // dependency it would re-run the probe on every keystroke in the form.
  const probeValuesRef = useRef(buildProbeConfigValues);
  probeValuesRef.current = buildProbeConfigValues;

  const load = useCallback(
    async (accountId, index) => {
      const key = fieldOptionsKey(accountId, index);
      setOptions((prev) => ({ ...prev, [key]: { loading: true, options: [], message: '' } }));
      try {
        const res = await observability.fetchLogLabels({
          account_id: accountId,
          log_provider: provider,
          log_provider_source: providerSource || 'user',
          integration_config_values: probeValuesRef.current ? probeValuesRef.current() : [],
          ...(index ? { request: { index } } : {}),
        });
        const fields = (res?.data?.data?.logs_list_labels || []).map((l) => (typeof l === 'string' ? l : l?.label ?? l?.name)).filter(Boolean);
        setOptions((prev) => ({ ...prev, [key]: { loading: false, options: fields, message: '' } }));
      } catch {
        // Suggestions are a convenience; every input stays usable as free text.
        setOptions((prev) => ({ ...prev, [key]: { loading: false, options: [], message: 'Failed to load fields.' } }));
      }
    },
    [provider, providerSource]
  );

  // Only probe once the connection is verified. Before that the form may hold a
  // half-typed URL and every attempt costs a timeout. The caller resets `enabled` when
  // a testable field changes, which is also what makes a stale list impossible after a
  // URL edit.
  const pairsKey = (accountIndexPairs || []).map((p) => fieldOptionsKey(p.accountId, p.index)).join('|');

  useEffect(() => {
    if (!enabled) return;
    (accountIndexPairs || []).forEach(({ accountId, index }) => {
      if (!accountId) return;
      const key = fieldOptionsKey(accountId, index);
      if (!options[key]) load(accountId, index);
    });
    // `options` is what this effect writes, and the guard above already makes each
    // (account, index) load exactly once.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [pairsKey, enabled, load]);

  // Drop everything when the connection is invalidated, so a field list from the
  // previous endpoint is never offered for the new one.
  useEffect(() => {
    if (!enabled) setOptions({});
  }, [enabled]);

  return options;
}
