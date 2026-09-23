// KG Coverage tab.
//
// Migrated from the legacy single-section KGSettings body (NB-30989 Phase 2).
// Behaviour identical: read cloud accounts + tenant filter; persist via
// kg_upsert_tenant_filter. DS components used throughout to match the LLM
// SettingsModal tabs visually.

import { useEffect, useMemo, useState } from 'react';
import { Box, Divider, Typography } from '@mui/material';
import PropTypes from 'prop-types';
import { Banner } from '@ui/Banner';
import { Button } from '@ui/Button';
import { Checkbox } from '@ui/Checkbox';
import { toast as snackbar } from '@ui/Toast';
import SearchInput from '@ui/SearchInput';
import apiKnowledgeGraph from '@api1/knowledge-graph';
import ConfirmDialog from './ConfirmDialog';
import { ds } from 'src/utils/colors';

// User-toggleable flow sources. Identifiers must match what's registered with
// RegisterFlowSourceFactory in api-server (see knowledge_graph/flow_sources/*).
// `manual` is intentionally NOT here — it's an always-on flow source on the
// backend (the act of declaring a row IS the opt-in). Adding it to a toggle
// would be redundant and creates a footgun where a row gets declared but
// silently never emits an edge.
const FLOW_SOURCES = [
  { id: 'ebpf', label: 'eBPF' },
  { id: 'traces', label: 'Traces' },
  { id: 'datadog-apm', label: 'Datadog APM' },
  { id: 'newrelic-apm', label: 'New Relic APM' },
];

// Same expression the account checkbox renders, reused by the removal warning
// so the two always name an account identically.
const accountLabel = (acc) => acc.account_name || acc.account_number || acc.id;

const KGCoverageTab = ({ open, onSaved, onClose }) => {
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [cloudAccounts, setCloudAccounts] = useState([]);
  const [selectedAccountIds, setSelectedAccountIds] = useState(new Set());
  const [selectedFlowSources, setSelectedFlowSources] = useState(new Set());
  // Coverage as it stands in the graph right now, i.e. the saved filter with
  // "empty == all" already expanded. The backend diffs the incoming selection
  // against this same expanded universe to decide what to deactivate, so
  // diffing against it here reproduces that decision exactly.
  const [baselineAccountIds, setBaselineAccountIds] = useState(new Set());
  const [baselineFlowSources, setBaselineFlowSources] = useState(new Set());
  const [confirmOpen, setConfirmOpen] = useState(false);
  // True only after a successful read. `!loading` is not enough to gate the
  // warning below: a failed read also ends the load, but leaves the selections
  // empty, which would render the "an empty selection means all" notice next to
  // the failure toast on a form that never populated.
  const [loaded, setLoaded] = useState(false);
  const [searchTerm, setSearchTerm] = useState('');

  useEffect(() => {
    if (!open) {
      return undefined;
    }
    let cancelled = false;
    setLoading(true);
    setSearchTerm('');
    setConfirmOpen(false);
    setLoaded(false);

    Promise.all([apiKnowledgeGraph.getCloudAccounts(), apiKnowledgeGraph.getTenantFilter()])
      .then(([accountsRes, filterRes]) => {
        if (cancelled) {
          return;
        }
        const rows = accountsRes?.data?.data?.cloud_accounts?.rows ?? [];
        // Newest-first; rows without created_at sink to the bottom so they
        // don't masquerade as recent.
        const accounts = [...rows].sort((a, b) => {
          const ta = a.created_at ? Date.parse(a.created_at) : 0;
          const tb = b.created_at ? Date.parse(b.created_at) : 0;
          return tb - ta;
        });
        const filter = filterRes?.data?.data?.kg_get_tenant_filter ?? null;

        setCloudAccounts(accounts);

        // An empty (or missing) account_ids / flow_sources list means "all" — that's
        // exactly how the backend resolves the filter at build time: an empty list
        // expands to every active account / every enabled flow source. The hourly
        // cron also pre-creates a default row with empty arrays for every tenant, so
        // `exists` is almost always true with empty lists. Mirror the backend here:
        // empty => pre-select everything, so the UI reflects what the graph actually
        // builds instead of showing every box unchecked (which read as "nothing on").
        const savedAccountIds = filter?.account_ids ?? [];
        const savedFlowSources = filter?.flow_sources ?? [];
        const activeAccountIds = savedAccountIds.length > 0 ? new Set(savedAccountIds) : new Set(accounts.map((a) => a.id));
        const activeFlowSources = savedFlowSources.length > 0 ? new Set(savedFlowSources) : new Set(FLOW_SOURCES.map((f) => f.id));
        setSelectedAccountIds(activeAccountIds);
        setSelectedFlowSources(activeFlowSources);
        setBaselineAccountIds(activeAccountIds);
        setBaselineFlowSources(activeFlowSources);
        setLoaded(true);
      })
      .catch((err) => {
        console.error('Failed to load KG settings:', err);
        snackbar.error('Failed to load Knowledge Graph settings.');
      })
      .finally(() => {
        if (!cancelled) {
          setLoading(false);
        }
      });

    return () => {
      cancelled = true;
    };
  }, [open]);

  const filteredAccounts = useMemo(() => {
    const q = searchTerm.trim().toLowerCase();
    if (!q) {
      return cloudAccounts;
    }
    return cloudAccounts.filter((acc) =>
      [acc.account_name, acc.account_number, acc.cloud_provider, acc.id].some((field) => (field || '').toString().toLowerCase().includes(q))
    );
  }, [cloudAccounts, searchTerm]);

  const toggle = (set, id) => {
    const next = new Set(set);
    if (next.has(id)) {
      next.delete(id);
    } else {
      next.add(id);
    }
    return next;
  };

  // The exact lists the save will send. A full selection collapses to []
  // ("empty == all" on the backend) so accounts added later stay covered —
  // persisting today's full list would silently exclude them.
  const payload = useMemo(() => {
    const allAccountsSelected = cloudAccounts.length > 0 && cloudAccounts.every((acc) => selectedAccountIds.has(acc.id));
    const allFlowSourcesSelected = FLOW_SOURCES.every((fs) => selectedFlowSources.has(fs.id));
    return {
      accountIds: allAccountsSelected ? [] : Array.from(selectedAccountIds),
      flowSources: allFlowSourcesSelected ? [] : Array.from(selectedFlowSources),
    };
  }, [cloudAccounts, selectedAccountIds, selectedFlowSources]);

  // What the graph covers *after* this save, resolved the way the backend
  // resolves it (FilterRepository.expandIfEmpty: an empty list expands back to
  // the whole universe). Deriving the warning from the payload rather than from
  // the checkbox state is what keeps it honest — see `emptySections` below for
  // the case where an empty selection means "all", not "none".
  const coverage = useMemo(() => {
    const coveredAccountIds = payload.accountIds.length > 0 ? new Set(payload.accountIds) : new Set(cloudAccounts.map((a) => a.id));
    const coveredFlowSourceIds = payload.flowSources.length > 0 ? new Set(payload.flowSources) : new Set(FLOW_SOURCES.map((f) => f.id));
    return {
      coveredAccountIds,
      coveredFlowSourceIds,
      removedAccounts: cloudAccounts.filter((acc) => baselineAccountIds.has(acc.id) && !coveredAccountIds.has(acc.id)),
      removedFlowSources: FLOW_SOURCES.filter((fs) => baselineFlowSources.has(fs.id) && !coveredFlowSourceIds.has(fs.id)),
    };
  }, [payload, cloudAccounts, baselineAccountIds, baselineFlowSources]);

  const { removedAccounts, removedFlowSources } = coverage;
  const hasRemovals = removedAccounts.length > 0 || removedFlowSources.length > 0;

  // Unticking every box in a section is NOT "cover nothing": the payload above
  // collapses to [], which the backend reads as "all", so nothing is removed.
  // Say so instead of letting the user believe they switched the section off.
  const emptySections = useMemo(
    () =>
      [
        cloudAccounts.length > 0 && selectedAccountIds.size === 0 ? 'cloud accounts' : null,
        selectedFlowSources.size === 0 ? 'flow sources' : null,
      ].filter(Boolean),
    [cloudAccounts.length, selectedAccountIds.size, selectedFlowSources.size]
  );

  const removedAccountNames = removedAccounts.map(accountLabel).join(', ');
  const removedFlowSourceNames = removedFlowSources.map((fs) => fs.label).join(', ');

  const persist = async () => {
    setSaving(true);
    try {
      const res = await apiKnowledgeGraph.upsertTenantFilter(payload);
      const errors = res?.data?.errors;
      if (errors?.length) {
        snackbar.error(`Failed to save Knowledge Graph settings: ${errors[0]?.message ?? 'Unknown error'}`);
        return;
      }
      const data = res?.data?.data?.kg_upsert_tenant_filter;
      const removedAcc = data?.removed_accounts?.length || 0;
      const removedFs = data?.removed_flow_sources?.length || 0;
      // Move the baseline AND the ticked state forward to what was actually
      // persisted. Both matter: the baseline stops a second save re-warning
      // about removals already applied, and the selection has to snap back when
      // an empty selection was stored as "all" — otherwise the boxes keep
      // reading "nothing on" while the graph covers everything, which is the
      // exact UI/backend mismatch #32328 was filed for.
      setBaselineAccountIds(coverage.coveredAccountIds);
      setBaselineFlowSources(coverage.coveredFlowSourceIds);
      setSelectedAccountIds(coverage.coveredAccountIds);
      setSelectedFlowSources(coverage.coveredFlowSourceIds);
      setConfirmOpen(false);
      if (removedAcc || removedFs) {
        snackbar.success(
          `Settings saved. Removed items deactivated immediately (${removedAcc} account${removedAcc === 1 ? '' : 's'}, ${removedFs} flow source${
            removedFs === 1 ? '' : 's'
          }). Newly enabled items appear after the next hourly rebuild.`
        );
      } else {
        snackbar.success('Knowledge Graph settings saved. Newly enabled items appear after the next hourly rebuild.');
      }
      onSaved?.();
    } catch (err) {
      console.error('Failed to save KG settings:', err);
      snackbar.error('Failed to save Knowledge Graph settings.');
    } finally {
      setSaving(false);
    }
  };

  // Deactivation is immediate and only reverses on the next rebuild, so a save
  // that removes coverage goes through the same danger-confirm the Manual
  // Dependencies tab uses for its destructive actions. A save that only *adds*
  // coverage is harmless and saves straight away.
  //
  // "hourly" in the copy below tracks the build_knowledge_graph cron in
  // runbook-server/internal/system/cron_triggers.yaml ('30 * * * *', :30 past
  // each hour). It was nightly until #33263 changed the schedule; re-check that
  // file before restating the cadence to users.
  const handleSave = () => {
    if (hasRemovals) {
      setConfirmOpen(true);
      return;
    }
    persist();
  };

  return (
    // Two-column layout: cloud accounts on the left (where the picklist
    // naturally wants width), flow sources on the right (small fixed list).
    // Fills the wide parent modal proportionally — no more vertical stacking
    // that left ~30% of the modal as horizontal whitespace.
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[4], py: ds.space[4] }}>
      {/* Deliberately does NOT restate what a removal does — the warning Banner
          below owns that, and says it only when a removal is actually pending,
          naming the items. The rebuild delay stays here because it applies to
          the *additive* path, which has no warning of its own. */}
      <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500] }}>
        Choose which cloud accounts and flow sources feed the Knowledge Graph. Newly enabled items appear after the next hourly rebuild.
      </Typography>

      <Box sx={{ display: 'flex', gap: ds.space[5], alignItems: 'flex-start' }}>
        {/* Cloud accounts column — bigger, takes the natural breathing room
            for a checklist with provider metadata on the right of each row. */}
        <Box sx={{ flex: '1 1 0', minWidth: 0 }}>
          <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', mb: ds.space[2], gap: ds.space[2] }}>
            <Typography sx={{ fontSize: ds.text.bodyLg, fontWeight: ds.weight.semibold, color: ds.brand[500] }}>Cloud accounts</Typography>
            {cloudAccounts.length > 0 && (
              <SearchInput
                id='kg-coverage-account-search'
                label='Search accounts'
                value={searchTerm}
                onChange={setSearchTerm}
                minWidth='180px'
                maxWidth='220px'
              />
            )}
          </Box>
          {loading ? (
            <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500], fontStyle: 'italic' }}>Loading…</Typography>
          ) : cloudAccounts.length === 0 ? (
            <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500], fontStyle: 'italic' }}>
              No active cloud accounts configured for this tenant.
            </Typography>
          ) : filteredAccounts.length === 0 ? (
            <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500], fontStyle: 'italic' }}>
              No accounts match &ldquo;{searchTerm}&rdquo;.
            </Typography>
          ) : (
            <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[1], maxHeight: '360px', overflowY: 'auto' }}>
              {filteredAccounts.map((acc) => (
                <Box
                  key={acc.id}
                  sx={{
                    display: 'flex',
                    alignItems: 'center',
                    justifyContent: 'space-between',
                    padding: `${ds.space.mul(0, 3)} ${ds.space[2]}`,
                    borderRadius: ds.radius.md,
                    '&:hover': { backgroundColor: ds.gray[100] },
                  }}
                >
                  <Checkbox
                    checked={selectedAccountIds.has(acc.id)}
                    onChange={() => setSelectedAccountIds((s) => toggle(s, acc.id))}
                    label={accountLabel(acc)}
                    size='sm'
                  />
                  <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500] }}>
                    {acc.cloud_provider}
                    {acc.account_number ? ` · ${acc.account_number}` : ''}
                  </Typography>
                </Box>
              ))}
            </Box>
          )}
        </Box>

        {/* Visual separator between the two columns. Vertical to match the
            two-column orientation; matches the horizontal Divider we'd use
            if the sections were stacked. */}
        <Divider orientation='vertical' flexItem sx={{ alignSelf: 'stretch' }} />

        {/* Flow sources column — small set; stack vertically so each one is
            easy to scan. Fixed-ish width keeps the cloud accounts column
            dominant per visual weight of the two sections. */}
        <Box sx={{ flex: '0 0 240px' }}>
          <Typography sx={{ fontSize: ds.text.bodyLg, fontWeight: ds.weight.semibold, color: ds.brand[500], mb: ds.space[2] }}>
            Flow sources
          </Typography>
          <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space.mul(1, 1.5) }}>
            {FLOW_SOURCES.map((fs) => (
              <Checkbox
                key={fs.id}
                checked={selectedFlowSources.has(fs.id)}
                onChange={() => setSelectedFlowSources((s) => toggle(s, fs.id))}
                label={fs.label}
                size='sm'
              />
            ))}
          </Box>
        </Box>
      </Box>

      {/* Live warning for the destructive half of this form. One Banner only
          (per DS spec, warnings don't stack), so when an untick both removes
          coverage AND empties a section, both facts go in the same message. */}
      {loaded && (hasRemovals || emptySections.length > 0) && (
        <Banner
          tone='warning'
          surface='section'
          title={hasRemovals ? 'Saving will remove data from the graph' : 'An empty selection means “all”, not “none”'}
          message={
            <>
              {removedAccounts.length > 0 && (
                <Box component='span' sx={{ display: 'block' }}>
                  {removedAccounts.length} cloud account{removedAccounts.length === 1 ? '' : 's'} (<strong>{removedAccountNames}</strong>) lose every
                  node and edge they own, immediately on save.
                </Box>
              )}
              {removedFlowSources.length > 0 && (
                <Box component='span' sx={{ display: 'block' }}>
                  {removedFlowSources.length} flow source{removedFlowSources.length === 1 ? '' : 's'} (<strong>{removedFlowSourceNames}</strong>) lose
                  every edge they created, immediately on save.
                </Box>
              )}
              {hasRemovals && (
                <Box component='span' sx={{ display: 'block' }}>
                  Re-enabling them later only restores the graph after the next hourly rebuild.
                </Box>
              )}
              {emptySections.length > 0 && (
                <Box component='span' sx={{ display: 'block' }}>
                  No {emptySections.join(' or ')} are ticked, which saves as &ldquo;all&rdquo; rather than &ldquo;none&rdquo; — nothing is removed.
                  Tick the ones you want to keep instead.
                </Box>
              )}
            </>
          }
        />
      )}

      <Box sx={{ display: 'flex', justifyContent: 'flex-end', gap: ds.space[2], pt: ds.space[2] }}>
        <Button tone='secondary' size='md' onClick={onClose} disabled={saving}>
          Cancel
        </Button>
        <Button tone={hasRemovals ? 'danger' : 'primary'} size='md' onClick={handleSave} disabled={saving || loading} loading={saving}>
          Save
        </Button>
      </Box>

      <ConfirmDialog
        open={confirmOpen}
        title='Remove coverage from the Knowledge Graph?'
        confirmLabel='Remove and save'
        danger
        submitting={saving}
        onConfirm={persist}
        onClose={() => setConfirmOpen(false)}
        message={
          <>
            {removedAccounts.length > 0 && (
              <>
                Every node and edge owned by <strong>{removedAccountNames}</strong> is deactivated immediately.{' '}
              </>
            )}
            {removedFlowSources.length > 0 && (
              <>
                Every edge created by <strong>{removedFlowSourceNames}</strong> is deactivated immediately.{' '}
              </>
            )}
            Re-enabling brings them back only after the next hourly rebuild, not straight away. Continue?
          </>
        }
      />
    </Box>
  );
};

KGCoverageTab.propTypes = {
  open: PropTypes.bool.isRequired,
  onSaved: PropTypes.func,
  onClose: PropTypes.func.isRequired,
};

export default KGCoverageTab;
