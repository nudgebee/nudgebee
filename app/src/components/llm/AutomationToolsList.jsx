import React, { useCallback, useEffect, useMemo, useState } from 'react';
import PropTypes from 'prop-types';
import { Stack, Typography } from '@mui/material';
import { ListingLayout } from '@ui/ListingLayout';
import { Chip } from '@ui/Chip';
import { Link } from '@ui/Link';
import { Button } from '@ui/Button';
import { Switch } from '@ui/Switch';
import { Modal } from '@ui/Modal';
import Tooltip from '@ui/Tooltip';
import Text from '@shared/format/Text';
import SearchInput from '@ui/SearchInput';
import FilterDropdown from '@ui/FilterDropdown';
import CustomTable from '@shared/tables/CustomTable';
import { toast as snackbar } from '@ui/Toast';
import { queryGraphQL } from '@lib/HttpService';
import { hasWriteAccess } from '@lib/auth';
import { listToolApprovalAccounts } from '@api1/tool-approval';

// Automations offered to Nubi as tools, or that could be (not an agent's, and
// opted in or described for the AI), with whether each is offered right now.
// The server judges that on the published version, as Nubi's own list does.
const LIST_AI_SETTINGS = `
query ListAutomationAISettings {
  workflows_list_ai_settings(request: {}) {
    workflows {
      id
      name
      account_id
      ai_invocable
      ai_auto_approve
      offered
      not_offered_reason
      created_by_user {
        display_name
        username
      }
    }
  }
}`;

const UPDATE_AI_SETTINGS = `
mutation UpdateAutomationAISettings($request: WorkflowUpdateAISettingsRequest!) {
  workflows_update_ai_settings(request: $request) {
    id
    ai_invocable
    ai_auto_approve
    offered
    not_offered_reason
  }
}`;

// What to change for Nubi to be offered it, by the server's reason code.
const NOT_OFFERED_REASONS = {
  not_enabled: 'Automations as Nubi tools are not enabled for this account.',
  not_opted_in: 'Nubi is not allowed to run it.',
  not_active: 'The automation is paused or inactive.',
  not_published: 'The automation has no published version.',
  no_manual_trigger: 'The published version has no manual trigger.',
  no_llm_description: 'The published version has no AI description. Publish the version that has one.',
};

const OFFERED_OPTIONS = [
  { label: 'Offered', value: 'offered' },
  { label: 'Not offered', value: 'not_offered' },
];

const HEADERS = ['Name', 'Account', 'Offered to Nubi', 'Approval', 'Created By'];

export async function listAutomationAISettings() {
  const response = await queryGraphQL(LIST_AI_SETTINGS, 'ListAutomationAISettings', {});
  const error = response?.data?.errors?.[0]?.message;
  if (error) throw new Error(error);
  return response?.data?.data?.workflows_list_ai_settings?.workflows || [];
}

/**
 * Automations Nubi can run as tools (#40399): whether each is offered, and
 * whether it asks first. Anyone who can edit an automation can change both
 * here, as in the automation's own settings.
 */
const AutomationToolsList = ({ stickyTable = false }) => {
  const [automations, setAutomations] = useState([]);
  const [accounts, setAccounts] = useState([]);
  const [loading, setLoading] = useState(false);
  const [search, setSearch] = useState('');
  const [offeredFilter, setOfferedFilter] = useState('');
  const [target, setTarget] = useState(null);
  const [draft, setDraft] = useState({ ai_invocable: false, ai_auto_approve: false });
  const [saving, setSaving] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const [rows, accountRows] = await Promise.all([listAutomationAISettings(), listToolApprovalAccounts()]);
      setAutomations(rows);
      setAccounts(accountRows);
    } catch (err) {
      snackbar.error(err?.message || 'Failed to load automations');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  const accountName = useCallback((id) => accounts.find((a) => a.id === id)?.account_name || id || '-', [accounts]);

  const openManage = useCallback((w) => {
    setTarget(w);
    setDraft({ ai_invocable: Boolean(w.ai_invocable), ai_auto_approve: Boolean(w.ai_auto_approve) });
  }, []);

  const save = async () => {
    setSaving(true);
    try {
      const response = await queryGraphQL(UPDATE_AI_SETTINGS, 'UpdateAutomationAISettings', {
        request: { account_id: target.account_id, id: target.id, ...draft },
      });
      const error = response?.data?.errors?.[0]?.message;
      if (error) {
        snackbar.error(error);
        return;
      }
      const updated = response?.data?.data?.workflows_update_ai_settings;
      setAutomations((rows) => rows.map((row) => (row.id === target.id ? { ...row, ...updated } : row)));
      snackbar.success('Saved. Nubi picks this up within 5 minutes.');
      setTarget(null);
    } catch {
      snackbar.error('Failed to save the automation settings');
    } finally {
      setSaving(false);
    }
  };

  const tableData = useMemo(
    () =>
      automations
        .filter((w) => !search.trim() || w.name?.toLowerCase().includes(search.trim().toLowerCase()))
        .filter((w) => !offeredFilter || (offeredFilter === 'offered') === Boolean(w.offered))
        .map((w) => [
          {
            component: (
              <Link href={`/automation/${w.id}?accountId=${w.account_id}`} openInNew>
                {w.name}
              </Link>
            ),
          },
          { text: accountName(w.account_id) },
          {
            component: w.offered ? (
              <Chip size='sm' variant='tag' tone='success'>
                Offered
              </Chip>
            ) : (
              <Tooltip title={NOT_OFFERED_REASONS[w.not_offered_reason] || 'Nubi is not offered it.'} placement='top'>
                <span>
                  <Chip size='sm' variant='tag' tone='neutral'>
                    Not offered
                  </Chip>
                </span>
              </Tooltip>
            ),
          },
          {
            component: (
              <Stack direction='row' spacing={1} alignItems='center'>
                {/* Asking only means something for an automation Nubi may run. */}
                {w.ai_invocable ? (
                  <Tooltip
                    title={
                      w.ai_auto_approve ? 'Nubi runs it without asking. Permission checks still apply.' : 'Nubi asks for approval before every run.'
                    }
                    placement='top'
                  >
                    <span>
                      <Chip size='sm' variant='tag' tone={w.ai_auto_approve ? 'neutral' : 'warning'}>
                        {w.ai_auto_approve ? 'Never asks' : 'Always asks'}
                      </Chip>
                    </span>
                  </Tooltip>
                ) : (
                  <Text value='—' />
                )}
                {hasWriteAccess(w.account_id) && (
                  <Button tone='secondary' size='xs' onClick={() => openManage(w)} data-testid={`automation-ai-manage-${w.id}`}>
                    Manage
                  </Button>
                )}
              </Stack>
            ),
          },
          { text: w.created_by_user?.display_name || w.created_by_user?.username || '-' },
        ]),
    [automations, search, offeredFilter, accountName, openManage]
  );

  const selectedOffered = OFFERED_OPTIONS.find((o) => o.value === offeredFilter) ?? null;

  return (
    <>
      <ListingLayout id='automation-tools-list'>
        <ListingLayout.Toolbar>
          <Stack direction='row' alignItems='center' spacing={1}>
            <FilterDropdown
              id='automation-tools-offered-filter'
              label='Offered to Nubi'
              options={OFFERED_OPTIONS}
              value={selectedOffered}
              onSelect={(_e, item) => setOfferedFilter(item?.value || '')}
            />
            <SearchInput id='automation-tools-search' value={search} onChange={setSearch} onClear={() => setSearch('')} label='Enter Name' />
          </Stack>
        </ListingLayout.Toolbar>
        <ListingLayout.Body>
          <CustomTable
            id='automation-tools'
            loading={loading}
            tableData={tableData}
            headers={HEADERS}
            totalRows={tableData.length}
            rowsPerPage={tableData.length || 10}
            stickyHeader={stickyTable}
            sx={stickyTable ? { maxHeight: 'calc(90vh - 340px)', overflowY: 'auto' } : undefined}
          />
        </ListingLayout.Body>
      </ListingLayout>
      <Modal width='sm' open={!!target} handleClose={() => setTarget(null)} title={target ? `Nubi settings · ${target.name}` : ''}>
        {target && (
          <Stack spacing={2}>
            <Switch
              checked={draft.ai_invocable}
              onChange={(_e, checked) => setDraft((prev) => ({ ai_invocable: checked, ai_auto_approve: checked && prev.ai_auto_approve }))}
              label='Nubi can run it'
              disabled={saving}
            />
            <Switch
              checked={draft.ai_auto_approve}
              onChange={(_e, checked) => setDraft((prev) => ({ ...prev, ai_auto_approve: checked }))}
              label='Runs without asking'
              disabled={saving || !draft.ai_invocable}
            />
            <Typography variant='caption' color='text.secondary' component='div'>
              Nubi also needs a published version with an AI description and a manual trigger. Changes reach Nubi within 5 minutes.
            </Typography>
            <Stack direction='row' spacing={1} justifyContent='flex-end'>
              <Button tone='secondary' size='md' onClick={() => setTarget(null)} disabled={saving}>
                Cancel
              </Button>
              <Button tone='primary' size='md' onClick={save} loading={saving} data-testid='automation-ai-save'>
                Save
              </Button>
            </Stack>
          </Stack>
        )}
      </Modal>
    </>
  );
};

AutomationToolsList.propTypes = { stickyTable: PropTypes.bool };

export default AutomationToolsList;
