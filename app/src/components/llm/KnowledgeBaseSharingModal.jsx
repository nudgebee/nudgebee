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

const SCOPE_ACCOUNT = 'account';
const SCOPE_ORGANISATION = 'organisation';
const SCOPE_SELECTED_ACCOUNTS = 'selected_accounts';
const SCOPE_OPTIONS = [
  { value: SCOPE_ORGANISATION, label: 'Organisation' },
  { value: SCOPE_SELECTED_ACCOUNTS, label: 'Selected accounts' },
];

const KB_DESCRIPTIONS = {
  [SCOPE_ORGANISATION]: 'Every account in the organisation uses it, for all of its agents. An account admin can switch it off for their account.',
  [SCOPE_SELECTED_ACCOUNTS]: 'Only the accounts you pick use it. The others no longer search it or see it as a skill.',
};

/**
 * Who can use an integration's knowledge base (#40605). It is an organisation
 * knowledge base: every account of the organisation by default, or only the
 * accounts a tenant admin picks. Its content comes from the integration, so this
 * is the only thing about it a person sets here.
 *
 * Organisation custom agents and tools (#41073) reuse the same dialog: they pass
 * `item`, a `noun`, their own `saveVisibility` / `listOverrides` calls and, to
 * move an item back to one account, an `accountOption`
 * ({ label, targetAccountId }). Left unset, it behaves exactly as for a
 * knowledge base.
 */
const KnowledgeBaseSharingModal = ({
  open,
  onClose,
  knowledgeBase,
  item,
  onSaved,
  noun = 'knowledge base',
  saveVisibility = apiKnowledgeBase.updateKnowledgeBaseVisibility,
  listOverrides = apiKnowledgeBase.listKnowledgeBaseAccountOverrides,
  accountOption = null,
  descriptions = KB_DESCRIPTIONS,
  testId = 'kb-sharing-modal',
}) => {
  const target = item || knowledgeBase;
  const scopeOptions = accountOption ? [{ value: SCOPE_ACCOUNT, label: accountOption.label }, ...SCOPE_OPTIONS] : SCOPE_OPTIONS;
  const initialVisibility = (t) => {
    const v = t?.visibility || (accountOption ? SCOPE_ACCOUNT : SCOPE_ORGANISATION);
    return scopeOptions.some((o) => o.value === v) ? v : SCOPE_ORGANISATION;
  };
  const [visibility, setVisibility] = useState(SCOPE_ORGANISATION);
  const [accountIds, setAccountIds] = useState([]);
  const [initialAccountIds, setInitialAccountIds] = useState([]);
  const [tenantAccounts, setTenantAccounts] = useState([]);
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    if (!open || !target) return undefined;
    let active = true;
    const current = initialVisibility(target);
    setVisibility(current);
    setAccountIds([]);
    setInitialAccountIds([]);
    setLoading(true);
    Promise.all([
      apiUserManagement.listAccounts().catch(() => []),
      current === SCOPE_SELECTED_ACCOUNTS ? listOverrides(target.id) : Promise.resolve(null),
    ])
      .then(([accounts, overrides]) => {
        if (!active) return;
        setTenantAccounts(Array.isArray(accounts) ? accounts : []);
        if (overrides?.errors?.length > 0) {
          snackbar.error(`Failed to load the accounts this ${noun} is shared with`);
        }
        const shared = (overrides?.data || []).filter((o) => o.enabled).map((o) => o.account_id);
        setAccountIds(shared);
        setInitialAccountIds(shared);
      })
      .catch((err) => {
        console.error(`Error loading ${noun} sharing:`, err);
        if (active) snackbar.error(`Failed to load the accounts this ${noun} is shared with`);
      })
      .finally(() => active && setLoading(false));
    return () => {
      active = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps -- reload only when the dialog opens or targets another item
  }, [open, target]);

  const accountOptions = useMemo(
    () => tenantAccounts.map((a) => ({ value: a.id, label: a.account_name || a.id, group: a.cloud_provider || 'Other' })),
    [tenantAccounts]
  );

  const unchanged =
    target &&
    visibility === initialVisibility(target) &&
    (visibility !== SCOPE_SELECTED_ACCOUNTS ||
      (accountIds.length === initialAccountIds.length && accountIds.every((id) => initialAccountIds.includes(id))));

  const handleSave = async () => {
    if (visibility === SCOPE_SELECTED_ACCOUNTS && accountIds.length === 0) {
      snackbar.error(`Pick at least one account to share this ${noun} with`);
      return;
    }
    setSaving(true);
    try {
      const response = await saveVisibility(
        target.id,
        visibility,
        visibility === SCOPE_SELECTED_ACCOUNTS ? accountIds : [],
        visibility === SCOPE_ACCOUNT ? accountOption?.targetAccountId || '' : ''
      );
      if (response.errors && response.errors.length > 0) {
        snackbar.error(response.errors[0]?.message || `Failed to change who can use this ${noun}`);
        return;
      }
      snackbar.success(
        visibility === SCOPE_SELECTED_ACCOUNTS
          ? `"${target.name}" is now used by the selected accounts only`
          : visibility === SCOPE_ACCOUNT
          ? `"${target.name}" is now used by one account only`
          : `"${target.name}" is now used by every account`
      );
      onSaved?.();
      onClose();
    } catch (err) {
      console.error(`Error changing ${noun} sharing:`, err);
      snackbar.error(`An error occurred while changing who can use this ${noun}`);
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal
      open={open}
      handleClose={onClose}
      title='Who can use it'
      subtitle={target ? `Accounts that use "${target.name}" in their answers.` : ''}
      width='sm'
    >
      <Box sx={{ padding: ds.space[5] }} data-testid={testId}>
        <ToggleGroup
          selection='single'
          size='sm'
          ariaLabel={`Who can use this ${noun}`}
          value={visibility}
          onChange={(next) => next && setVisibility(next)}
          options={scopeOptions}
        />
        <Typography sx={{ marginTop: ds.space[2], fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-600)' }}>
          {descriptions[visibility] || ''}
        </Typography>
        {visibility === SCOPE_SELECTED_ACCOUNTS && (
          <Box sx={{ marginTop: ds.space[3] }}>
            <Select
              id={`${testId}-accounts`}
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
  item: PropTypes.shape({
    id: PropTypes.string,
    name: PropTypes.string,
    visibility: PropTypes.string,
  }),
  onSaved: PropTypes.func,
  noun: PropTypes.string,
  saveVisibility: PropTypes.func,
  listOverrides: PropTypes.func,
  accountOption: PropTypes.shape({ label: PropTypes.string, targetAccountId: PropTypes.string }),
  descriptions: PropTypes.objectOf(PropTypes.string),
  testId: PropTypes.string,
};

export default KnowledgeBaseSharingModal;
