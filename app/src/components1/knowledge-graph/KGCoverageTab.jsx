// KG Coverage tab.
//
// Migrated from the legacy single-section KGSettings body (NB-30989 Phase 2).
// Behaviour identical: read cloud accounts + tenant filter; persist via
// kg_upsert_tenant_filter. DS components used throughout to match the LLM
// SettingsModal tabs visually.

import { useEffect, useMemo, useState } from 'react';
import { Box, Divider, Typography } from '@mui/material';
import PropTypes from 'prop-types';
import { Button } from '@components1/ds/Button';
import { Checkbox } from '@components1/ds/Checkbox';
import { toast as snackbar } from '@components1/ds/Toast';
import CustomSearch from '@common-new/CustomSearch';
import apiKnowledgeGraph from '@api1/knowledge-graph';
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

const KGCoverageTab = ({ open, onSaved, onClose }) => {
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [cloudAccounts, setCloudAccounts] = useState([]);
  const [selectedAccountIds, setSelectedAccountIds] = useState(new Set());
  const [selectedFlowSources, setSelectedFlowSources] = useState(new Set());
  const [searchTerm, setSearchTerm] = useState('');

  useEffect(() => {
    if (!open) {
      return undefined;
    }
    let cancelled = false;
    setLoading(true);
    setSearchTerm('');

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

        if (filter?.exists) {
          setSelectedAccountIds(new Set(filter.account_ids ?? []));
          setSelectedFlowSources(new Set(filter.flow_sources ?? []));
        } else {
          // No row yet: pre-select everything so save with no changes is a no-op.
          setSelectedAccountIds(new Set(accounts.map((a) => a.id)));
          setSelectedFlowSources(new Set(FLOW_SOURCES.map((f) => f.id)));
        }
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

  const handleSave = async () => {
    setSaving(true);
    try {
      const res = await apiKnowledgeGraph.upsertTenantFilter({
        accountIds: Array.from(selectedAccountIds),
        flowSources: Array.from(selectedFlowSources),
      });
      const errors = res?.data?.errors;
      if (errors?.length) {
        snackbar.error(`Failed to save Knowledge Graph settings: ${errors[0]?.message ?? 'Unknown error'}`);
        return;
      }
      const data = res?.data?.data?.kg_upsert_tenant_filter;
      const removedAcc = data?.removed_accounts?.length || 0;
      const removedFs = data?.removed_flow_sources?.length || 0;
      if (removedAcc || removedFs) {
        snackbar.success(
          `Settings saved. Removed items deactivated immediately (${removedAcc} account${removedAcc === 1 ? '' : 's'}, ${removedFs} flow source${
            removedFs === 1 ? '' : 's'
          }). Newly enabled items appear after the next nightly rebuild.`
        );
      } else {
        snackbar.success('Knowledge Graph settings saved. Newly enabled items appear after the next nightly rebuild.');
      }
      onSaved?.();
    } catch (err) {
      console.error('Failed to save KG settings:', err);
      snackbar.error('Failed to save Knowledge Graph settings.');
    } finally {
      setSaving(false);
    }
  };

  return (
    // Two-column layout: cloud accounts on the left (where the picklist
    // naturally wants width), flow sources on the right (small fixed list).
    // Fills the wide parent modal proportionally — no more vertical stacking
    // that left ~30% of the modal as horizontal whitespace.
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2, py: 2 }}>
      <Typography sx={{ fontSize: '12px', color: ds?.text?.secondaryDark ?? '#6b7280' }}>
        Choose which cloud accounts and flow sources feed the Knowledge Graph. Removed items disappear from the graph immediately. Newly enabled items
        appear after the next nightly rebuild.
      </Typography>

      <Box sx={{ display: 'flex', gap: 3, alignItems: 'flex-start' }}>
        {/* Cloud accounts column — bigger, takes the natural breathing room
            for a checklist with provider metadata on the right of each row. */}
        <Box sx={{ flex: '1 1 0', minWidth: 0 }}>
          <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', mb: 1, gap: 1 }}>
            <Typography sx={{ fontSize: '14px', fontWeight: 600, color: ds?.text?.secondary ?? '#374151' }}>Cloud accounts</Typography>
            {cloudAccounts.length > 0 && (
              <CustomSearch
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
            <Typography sx={{ fontSize: '12px', color: ds?.text?.secondaryDark ?? '#6b7280', fontStyle: 'italic' }}>Loading…</Typography>
          ) : cloudAccounts.length === 0 ? (
            <Typography sx={{ fontSize: '12px', color: ds?.text?.secondaryDark ?? '#6b7280', fontStyle: 'italic' }}>
              No active cloud accounts configured for this tenant.
            </Typography>
          ) : filteredAccounts.length === 0 ? (
            <Typography sx={{ fontSize: '12px', color: ds?.text?.secondaryDark ?? '#6b7280', fontStyle: 'italic' }}>
              No accounts match &ldquo;{searchTerm}&rdquo;.
            </Typography>
          ) : (
            <Box sx={{ display: 'flex', flexDirection: 'column', gap: 0.5, maxHeight: '360px', overflowY: 'auto' }}>
              {filteredAccounts.map((acc) => (
                <Box
                  key={acc.id}
                  sx={{
                    display: 'flex',
                    alignItems: 'center',
                    justifyContent: 'space-between',
                    padding: '6px 8px',
                    borderRadius: '6px',
                    '&:hover': { backgroundColor: ds?.background?.tertiaryLightest ?? '#f3f4f6' },
                  }}
                >
                  <Checkbox
                    checked={selectedAccountIds.has(acc.id)}
                    onChange={() => setSelectedAccountIds((s) => toggle(s, acc.id))}
                    label={acc.account_name || acc.account_number || acc.id}
                    size='sm'
                  />
                  <Typography sx={{ fontSize: '11px', color: ds?.text?.secondaryDark ?? '#6b7280' }}>
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
          <Typography sx={{ fontSize: '14px', fontWeight: 600, color: ds?.text?.secondary ?? '#374151', mb: 1 }}>Flow sources</Typography>
          <Box sx={{ display: 'flex', flexDirection: 'column', gap: 0.75 }}>
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

      <Box sx={{ display: 'flex', justifyContent: 'flex-end', gap: 1, pt: 1 }}>
        <Button tone='secondary' size='md' onClick={onClose} disabled={saving}>
          Cancel
        </Button>
        <Button tone='primary' size='md' onClick={handleSave} disabled={saving || loading} loading={saving}>
          Save
        </Button>
      </Box>
    </Box>
  );
};

KGCoverageTab.propTypes = {
  open: PropTypes.bool.isRequired,
  onSaved: PropTypes.func,
  onClose: PropTypes.func.isRequired,
};

export default KGCoverageTab;
