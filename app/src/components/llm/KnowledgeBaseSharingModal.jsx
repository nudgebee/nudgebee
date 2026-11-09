import { useEffect, useMemo, useState } from 'react';
import PropTypes from 'prop-types';
import { Box, Typography } from '@mui/material';
import { ToggleGroup } from '@ui/ToggleGroup';
import { Select } from '@ui/Select';
import { Button } from '@ui/Button';
import { Modal } from '@ui/Modal';
import { toast as snackbar } from '@ui/Toast';
import apiUserManagement from '@api1/user';
import apiKnowledgeBase from '@api1/knowledge-base';
import { ds } from '@utils/colors';

const SCOPE_ORGANISATION = 'organisation';
const SCOPE_SELECTED_ACCOUNTS = 'selected_accounts';
const SCOPE_OPTIONS = [
  { value: SCOPE_ORGANISATION, label: 'Organisation' },
  { value: SCOPE_SELECTED_ACCOUNTS, label: 'Selected accounts' },
];

/**
 * Who can use an integration's knowledge base (#40605). It is an organisation
 * knowledge base: every account of the organisation by default, or only the
 * accounts a tenant admin picks. Its content comes from the integration, so this
 * is the only thing about it a person sets here.
 */
const KnowledgeBaseSharingModal = ({ open, onClose, knowledgeBase, onSaved }) => {
  const [visibility, setVisibility] = useState(SCOPE_ORGANISATION);
  const [accountIds, setAccountIds] = useState([]);
  const [initialAccountIds, setInitialAccountIds] = useState([]);
  const [tenantAccounts, setTenantAccounts] = useState([]);
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    if (!open || !knowledgeBase) return undefined;
    let active = true;
    const current = knowledgeBase.visibility === SCOPE_SELECTED_ACCOUNTS ? SCOPE_SELECTED_ACCOUNTS : SCOPE_ORGANISATION;
    setVisibility(current);
    setAccountIds([]);
    setInitialAccountIds([]);
    setLoading(true);
    Promise.all([
      apiUserManagement.listAccounts().catch(() => []),
      current === SCOPE_SELECTED_ACCOUNTS ? apiKnowledgeBase.listKnowledgeBaseAccountOverrides(knowledgeBase.id) : Promise.resolve(null),
    ])
      .then(([accounts, overrides]) => {
        if (!active) return;
        setTenantAccounts(Array.isArray(accounts) ? accounts : []);
        if (overrides?.errors?.length > 0) {
          snackbar.error('Failed to load the accounts this knowledge base is shared with');
        }
        const shared = (overrides?.data || []).filter((o) => o.enabled).map((o) => o.account_id);
        setAccountIds(shared);
        setInitialAccountIds(shared);
      })
      .catch((err) => {
        console.error('Error loading knowledge base sharing:', err);
        if (active) snackbar.error('Failed to load the accounts this knowledge base is shared with');
      })
      .finally(() => active && setLoading(false));
    return () => {
      active = false;
    };
  }, [open, knowledgeBase]);

  const accountOptions = useMemo(
    () => tenantAccounts.map((a) => ({ value: a.id, label: a.account_name || a.id, group: a.cloud_provider || 'Other' })),
    [tenantAccounts]
  );

  const unchanged =
    knowledgeBase &&
    visibility === (knowledgeBase.visibility || SCOPE_ORGANISATION) &&
    (visibility !== SCOPE_SELECTED_ACCOUNTS ||
      (accountIds.length === initialAccountIds.length && accountIds.every((id) => initialAccountIds.includes(id))));

  const handleSave = async () => {
    if (visibility === SCOPE_SELECTED_ACCOUNTS && accountIds.length === 0) {
      snackbar.error('Pick at least one account to share this knowledge base with');
      return;
    }
    setSaving(true);
    try {
      const response = await apiKnowledgeBase.updateKnowledgeBaseVisibility(
        knowledgeBase.id,
        visibility,
        visibility === SCOPE_SELECTED_ACCOUNTS ? accountIds : [],
        ''
      );
      if (response.errors && response.errors.length > 0) {
        snackbar.error(response.errors[0]?.message || 'Failed to change who can use this knowledge base');
        return;
      }
      snackbar.success(
        visibility === SCOPE_SELECTED_ACCOUNTS
          ? `"${knowledgeBase.name}" is now used by the selected accounts only`
          : `"${knowledgeBase.name}" is now used by every account`
      );
      onSaved?.();
      onClose();
    } catch (err) {
      console.error('Error changing knowledge base sharing:', err);
      snackbar.error('An error occurred while changing who can use this knowledge base');
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal
      open={open}
      handleClose={onClose}
      title='Who can use it'
      subtitle={knowledgeBase ? `Accounts that use "${knowledgeBase.name}" in their answers.` : ''}
      width='sm'
    >
      <Box sx={{ padding: ds.space[5] }} data-testid='kb-sharing-modal'>
        <ToggleGroup
          selection='single'
          size='sm'
          ariaLabel='Who can use this knowledge base'
          value={visibility}
          onChange={(next) => next && setVisibility(next)}
          options={SCOPE_OPTIONS}
        />
        <Typography sx={{ marginTop: ds.space[2], fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-600)' }}>
          {visibility === SCOPE_SELECTED_ACCOUNTS
            ? 'Only the accounts you pick use it. The others no longer search it or see it as a skill.'
            : 'Every account in the organisation uses it, for all of its agents. An account admin can switch it off for their account.'}
        </Typography>
        {visibility === SCOPE_SELECTED_ACCOUNTS && (
          <Box sx={{ marginTop: ds.space[3] }}>
            <Select
              id='kb-sharing-accounts'
              label='Accounts'
              placeholder='Select accounts'
              multiple
              grouped
              searchable
              options={accountOptions}
              value={accountIds}
              onChange={(next) => setAccountIds(Array.isArray(next) ? next : [])}
              loading={loading}
            />
          </Box>
        )}
        <Box sx={{ display: 'flex', justifyContent: 'flex-end', gap: ds.space[3], marginTop: ds.space[5] }}>
          <Button tone='secondary' size='md' onClick={onClose} disabled={saving}>
            Cancel
          </Button>
          <Button tone='primary' size='md' onClick={handleSave} loading={saving} disabled={saving || loading || unchanged}>
            Save
          </Button>
        </Box>
      </Box>
    </Modal>
  );
};

KnowledgeBaseSharingModal.propTypes = {
  open: PropTypes.bool.isRequired,
  onClose: PropTypes.func.isRequired,
  knowledgeBase: PropTypes.shape({
    id: PropTypes.string,
    name: PropTypes.string,
    visibility: PropTypes.string,
  }),
  onSaved: PropTypes.func,
};

export default KnowledgeBaseSharingModal;
