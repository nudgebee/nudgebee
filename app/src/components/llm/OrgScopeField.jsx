import { useEffect, useMemo, useState } from 'react';
import PropTypes from 'prop-types';
import { Box, Typography } from '@mui/material';
import { ToggleGroup } from '@ui/ToggleGroup';
import { Select } from '@ui/Select';
import apiUserManagement from '@api1/user';
import { ds } from '@utils/colors';

export const SCOPE_ACCOUNT = 'account';
export const SCOPE_ORGANISATION = 'organisation';
export const SCOPE_SELECTED_ACCOUNTS = 'selected_accounts';

// What each "Who can use it" choice means, for the create forms and the
// list's "Who can use it" dialog.
export const ORG_SCOPE_DESCRIPTIONS = {
  [SCOPE_ACCOUNT]: 'Only this account can use it.',
  [SCOPE_ORGANISATION]: 'Every account in the organisation can use it. An account admin can switch it off for their account.',
  [SCOPE_SELECTED_ACCOUNTS]: 'Only the accounts you pick can use it.',
};

export const isOrgScope = (visibility) => visibility === SCOPE_ORGANISATION || visibility === SCOPE_SELECTED_ACCOUNTS;

// An organisation agent or tool (#41073) belongs to the tenant: no account_id,
// and visibility says which accounts see it.
export const isOrgItem = (item) => Boolean(item) && !item.account_id && isOrgScope(item.visibility);

// "Who can use it" choices for a create form, for tenant admins only (null
// hides the field). "This account" needs an account in scope; the tenant-wide
// view offers only the organisation scopes.
export const getScopeOptions = (tenantAdmin, accountId) =>
  tenantAdmin
    ? [
        ...(accountId ? [{ value: SCOPE_ACCOUNT, label: 'This account' }] : []),
        { value: SCOPE_ORGANISATION, label: 'Organisation' },
        { value: SCOPE_SELECTED_ACCOUNTS, label: 'Selected accounts' },
      ]
    : null;

/**
 * "Who can use it" for a new custom agent or tool, mirroring the organisation
 * knowledge base form: this account, the whole organisation, or the accounts a
 * tenant admin picks.
 */
const OrgScopeField = ({ noun, options, visibility, onVisibilityChange, accountIds, onAccountIdsChange, disabled = false }) => {
  const [tenantAccounts, setTenantAccounts] = useState([]);
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    let active = true;
    setLoading(true);
    apiUserManagement
      .listAccounts()
      .then((rows) => active && setTenantAccounts(Array.isArray(rows) ? rows : []))
      .catch(() => active && setTenantAccounts([]))
      .finally(() => active && setLoading(false));
    return () => {
      active = false;
    };
  }, []);

  const accountOptions = useMemo(
    () => tenantAccounts.map((a) => ({ value: a.id, label: a.account_name || a.id, group: a.cloud_provider || 'Other' })),
    [tenantAccounts]
  );

  return (
    <Box data-testid='org-scope-field'>
      <Typography sx={{ fontSize: ds.text.small, fontWeight: ds.weight.medium, color: ds.gray[700], marginBottom: ds.space[2] }}>
        Who can use it
      </Typography>
      <ToggleGroup
        selection='single'
        size='sm'
        ariaLabel={`Who can use this ${noun}`}
        value={visibility}
        onChange={(next) => next && onVisibilityChange(next)}
        options={disabled ? options.map((o) => ({ ...o, disabled: true })) : options}
      />
      <Typography sx={{ marginTop: ds.space[2], fontSize: ds.text.caption, color: ds.gray[600] }}>{ORG_SCOPE_DESCRIPTIONS[visibility]}</Typography>
      {visibility === SCOPE_SELECTED_ACCOUNTS && (
        <Box sx={{ marginTop: ds.space[3] }}>
          <Select
            id='org-scope-accounts'
            label='Accounts'
            placeholder='Select accounts'
            multiple
            grouped
            searchable
            options={accountOptions}
            value={accountIds}
            onChange={(next) => onAccountIdsChange(Array.isArray(next) ? next : [])}
            loading={loading}
          />
        </Box>
      )}
    </Box>
  );
};

OrgScopeField.propTypes = {
  noun: PropTypes.string.isRequired,
  options: PropTypes.arrayOf(PropTypes.shape({ value: PropTypes.string, label: PropTypes.string })).isRequired,
  visibility: PropTypes.string.isRequired,
  onVisibilityChange: PropTypes.func.isRequired,
  accountIds: PropTypes.arrayOf(PropTypes.string).isRequired,
  onAccountIdsChange: PropTypes.func.isRequired,
  disabled: PropTypes.bool,
};

export default OrgScopeField;
