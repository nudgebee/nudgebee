import React, { useEffect, useRef, useState } from 'react';
import { Box, IconButton } from '@mui/material';
import Typography from '@mui/material/Typography';
import { Input } from '@ui/Input';
import { useForm } from 'react-hook-form';
import apiUserManagement from '@api1/user';
import CustomTable from '@shared/tables/CustomTable';
import { Select } from '@ui/Select';
import { Tabs } from '@ui/Tabs';
import { useSession } from 'next-auth/react';
import SafeIcon from '@shared/icons/SafeIcon';
import PropTypes from 'prop-types';
import { Button } from '@ui/Button';
import { Modal } from '@ui/Modal';
import { hasWriteAccess } from '@lib/auth';
import { textValidation } from '@lib/validation';
import { ds } from 'src/utils/colors';
import { DeleteIconRed as DeleteIcon, modalerror, AWSIcon, AzureIcon, GCPIcon, ouK8s as KubernetesIcon } from '@assets';
import { Label } from '@ui/Label';

const RBAC_TABS = [
  { id: 'tenant', label: 'Tenant' },
  { id: 'account', label: 'Account' },
  { id: 'k8s_namespace', label: 'K8s Namespace' },
];

const TENANT_ROLE_OPTIONS = [
  { label: 'Admin', value: 'tenant_admin' },
  { label: 'ReadOnly Admin', value: 'tenant_admin_readonly' },
];

const ACCOUNT_ROLE_OPTIONS = [
  { label: 'Admin', value: 'account_admin' },
  { label: 'ReadOnly Admin', value: 'account_admin_readonly' },
];

const NAMESPACE_ROLE_OPTIONS = [
  { label: 'Admin', value: 'k8s_namespace_admin' },
  { label: 'ReadOnly Admin', value: 'k8s_namespace_admin_readonly' },
];

const MEMBER_FILTER_TABS = [
  { id: 'active', label: 'Active' },
  { id: 'inactive', label: 'Inactive' },
];

const PROVIDER_ICON_MAP = {
  AWS: AWSIcon,
  Azure: AzureIcon,
  GCP: GCPIcon,
  K8s: KubernetesIcon,
};

const trashBtnSx = {
  width: ds.space[6],
  height: ds.space[6],
  borderRadius: 'var(--ds-radius-sm)',
  padding: 'var(--ds-space-1)',
  '&:hover': { background: ds.red[200] },
};

// Forces tables inside RBAC/members surfaces to respect modal width.
// Without this, long emails / role names (`some.user@example.com`,
// `k8s_namespace_admin_readonly`) push the table beyond container, triggering
// MUI TableContainer's internal horizontal scroll. table-layout:fixed makes
// columns honor declared % widths; cell ellipsis truncates overflow gracefully.
// No vertical maxHeight — let pagination + table render at natural height; modal
// handles outer scrolling at 90vh when viewport is too small.
const tableWrapperSx = {
  width: '100%',
  '& table': { tableLayout: 'fixed', width: '100% !important' },
  '& td, & th': { overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' },
  '& .MuiTableContainer-root': { overflowX: 'hidden' },
};

function SegmentedFilter({ tabs, value, onChange, dataTestId }) {
  return (
    <Box
      sx={{
        display: 'inline-flex',
        padding: 'var(--ds-space-1)',
        background: ds.background[300],
        borderRadius: 'var(--ds-radius-lg)',
        gap: 'var(--ds-space-1)',
      }}
    >
      {tabs.map((t) => {
        const selected = value === t.id;
        return (
          <Box
            key={t.id}
            component='button'
            type='button'
            onClick={() => onChange(t.id)}
            data-testid={dataTestId ? `${dataTestId}-${t.id}` : undefined}
            sx={{
              display: 'inline-flex',
              alignItems: 'center',
              padding: 'var(--ds-space-1) var(--ds-space-3)',
              borderRadius: 'var(--ds-radius-md)',
              background: selected ? ds.background[100] : 'transparent',
              color: selected ? ds.blue[500] : ds.gray[600],
              boxShadow: selected ? `0px 1px 3px 0px ${ds.gray.alpha[300]}` : 'none',
              border: 'none',
              cursor: 'pointer',
              fontFamily: 'Roboto',
              fontWeight: selected ? 600 : 500,
              fontSize: 'var(--ds-text-small)',
              textTransform: 'capitalize',
              transition: 'all 0.15s',
            }}
          >
            {t.label}
          </Box>
        );
      })}
    </Box>
  );
}

SegmentedFilter.propTypes = {
  tabs: PropTypes.array,
  value: PropTypes.string,
  onChange: PropTypes.func,
  dataTestId: PropTypes.string,
};

function SectionLabel({ children }) {
  return (
    <Box
      sx={{
        font: "500 11px/1 'Roboto'",
        letterSpacing: '0.4px',
        textTransform: 'uppercase',
        color: ds.gray[400],
      }}
    >
      {children}
    </Box>
  );
}

SectionLabel.propTypes = { children: PropTypes.node };

function ProviderTag({ provider }) {
  const icon = PROVIDER_ICON_MAP[provider];
  if (!icon) return null;
  return <SafeIcon src={icon} alt={provider} width={20} height={20} />;
}

ProviderTag.propTypes = { provider: PropTypes.string };

function fieldLabel(text, required) {
  return (
    <Box component='label' sx={{ display: 'block', font: "500 12px/1.2 'Roboto'", color: ds.gray[700], mb: 'var(--ds-space-1)' }}>
      {text}
      {required && (
        <Box component='span' sx={{ color: ds.red[600], ml: 'var(--ds-space-1)' }}>
          *
        </Box>
      )}
    </Box>
  );
}

function GroupModal({ open, handleClose, groupData, handleSnackBarData }) {
  const { handleSubmit, reset } = useForm();
  const { data: currentUser } = useSession({ required: true });

  const isEdit = groupData && Object.keys(groupData).length > 0;

  const groupUsersLoaded = useRef(false);

  const [validationError, setValidationError] = useState({});
  const [users, setUsers] = useState([]);
  const [userOptions, setUserOptions] = useState([]);
  const [selectedUsers, setSelectedUsers] = useState([]);
  const [loading, setLoading] = useState(false);
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [groupNameValue, setGroupNameValue] = useState('');
  const [groupDescValue, setGroupDescValue] = useState('');

  const [userStatusFilter, setUserStatusFilter] = useState('active');
  const [userAdded, setUserAdded] = useState(new Set());
  const [userRemoved, setUserRemoved] = useState(new Set());
  const [rbacType, setRbacType] = useState('tenant');
  const [groupRole, setGroupRole] = useState('');
  const [accountOptions, setAccountOptions] = useState([]);
  const [accounts, setAccounts] = useState([]);
  const [showSelectedAccounts, setShowSelectedAccounts] = useState([]);
  const [selectedAccount, setSelectedAccount] = useState('');
  // Account RBAC tab supports selecting multiple accounts at once (single select
  // `selectedAccount` above is still used by the K8s Namespace tab).
  const [selectedAccounts, setSelectedAccounts] = useState([]);
  const [selectedAccountRole, setSelectedAccountRole] = useState('');
  const [accountNamespaceOptions, setAccountNamespaceOptions] = useState([]);
  const [accountNamespaceAdded, setAccountNamespaceAdded] = useState([]);
  const [accountNamespaceRemoved, setAccountNamespaceRemoved] = useState([]);
  const [showSelectedAccountNamespaces, setShowSelectedAccountNamespaces] = useState([]);
  const [selectedAccountNamespace, setSelectedAccountNamespace] = useState('');
  const [selectedAccountNamespaceRole, setSelectedAccountNamespaceRole] = useState('');

  const handleKeyDown = (e) => {
    if (e.key === 'Enter') e.preventDefault();
  };

  const groupNameExists = async (name) => {
    let groupNameList = (await apiUserManagement.checkGroupNameExists(name))?.data;
    return !!groupNameList?.length;
  };

  function cleanState() {
    setUserStatusFilter('active');
    setValidationError({});
    setUsers([]);
    setUserOptions([]);
    setSelectedUsers([]);
    setGroupNameValue('');
    setGroupDescValue('');
    setUserAdded(new Set());
    setUserRemoved(new Set());
    setRbacType('tenant');
    setGroupRole('');
    setAccountOptions([]);
    setAccounts([]);
    setShowSelectedAccounts([]);
    setSelectedAccount('');
    setSelectedAccounts([]);
    setSelectedAccountRole('');
    setAccountNamespaceOptions([]);
    setAccountNamespaceAdded([]);
    setAccountNamespaceRemoved([]);
    setShowSelectedAccountNamespaces([]);
    setSelectedAccountNamespace('');
    setSelectedAccountNamespaceRole('');
    setLoading(false);
    setIsSubmitting(false);
    groupUsersLoaded.current = false;
    reset();
  }

  function adjustCloseAction(shouldUpdate = false) {
    cleanState();
    handleClose(shouldUpdate);
  }

  const submitForm = async () => {
    // Show loader the moment user clicks — prevents the 1-2s gap during the async
    // checkGroupNameExists call where the button would otherwise look idle.
    setIsSubmitting(true);

    const nameToValidate = groupNameValue;
    // Capture validation result synchronously. textValidation calls our handler with the
    // computed errors object; we read the value out before React's async state update settles,
    // so the immediately-following branch sees fresh truth instead of stale validationError.
    let nameError;
    textValidation(
      nameToValidate ?? '',
      validationError,
      (next) => {
        const computed = typeof next === 'function' ? next(validationError) : next;
        nameError = computed.groupname;
        setValidationError(computed);
      },
      'groupname',
      ['required', 'firstLetterAlphaNum', 'minlength5', 'alphaNumWithSpace']
    );
    if (!nameToValidate || nameError) {
      setIsSubmitting(false);
      return;
    }

    if (!isEdit || (isEdit && groupNameValue !== groupData.name)) {
      if (await groupNameExists(nameToValidate)) {
        setValidationError({ groupname: 'Group name already in use' });
        setIsSubmitting(false);
        return;
      }
    }
    setValidationError({});

    if (isEdit) {
      try {
        let formData = {
          id: groupData.id,
          name: groupNameValue,
          description: groupDescValue,
          role: groupRole || '',
        };
        if (
          formData.name != groupData.name ||
          formData.description != groupData.description ||
          groupData.group_roles?.filter((gr) => gr.entity_type == 'tenant' && gr.role == groupRole).length == 0
        ) {
          let resp = await apiUserManagement.updateUserGroup(formData);
          if (resp?.status !== 'success') {
            handleSnackBarData({ message: 'Failed to update group', severity: 'error' });
            setIsSubmitting(false);
            return;
          }
        }

        const updatePromises = [];
        if (userAdded?.size > 0 || userRemoved?.size > 0) {
          updatePromises.push(
            apiUserManagement.manageGroupUsers({
              group_id: groupData.id,
              add_usernames: [...userAdded],
              remove_usernames: [...userRemoved],
            })
          );
        }

        // Backend uses replace-all semantics for these two mutations — it DELETEs all rows
        // for (group_id, entity_type) and re-inserts the supplied list. So sending an empty
        // list IS the way to clear all account/namespace roles. We must call the API whenever
        // either the current list OR the initial list is non-empty; otherwise an
        // "all-deleted" save silently no-ops because the guard skips the call.
        const initialAccountCount = (groupData?.group_roles ?? []).filter((gr) => gr.entity_type === 'account').length;
        const initialNamespaceCount = (groupData?.group_roles ?? []).filter((gr) => gr.entity_type === 'k8s_namespace').length;

        const userGroupAccountObj = showSelectedAccounts.map((a) => ({
          account_id: a[0].drilldownQuery.id,
          role: a[1].text,
        }));
        if (userGroupAccountObj.length > 0 || initialAccountCount > 0) {
          updatePromises.push(apiUserManagement.upsertGroupAccountRoles({ group_id: groupData.id, account_roles: userGroupAccountObj }));
        }

        const userGroupAccountNamespaceObj = accountNamespaceAdded
          .filter((a) => {
            for (let aR of accountNamespaceRemoved) {
              if (aR.accountId == a.accountId && aR.namespace == a.namespace && aR.role == a.role) return false;
            }
            return true;
          })
          .map((a) => ({ account_id: a.accountId, role: a.role, namespace: a.namespace }));
        if (userGroupAccountNamespaceObj.length > 0 || initialNamespaceCount > 0) {
          updatePromises.push(
            apiUserManagement.upsertGroupAccountNamespaceRoles({
              group_id: groupData.id,
              k8saccount_namespace_roles: userGroupAccountNamespaceObj,
            })
          );
        }

        if (updatePromises.length > 0) {
          await Promise.all(updatePromises);
        }
        handleSnackBarData({ message: 'Group updated successfully', severity: 'success' });
        adjustCloseAction(true);
      } catch (error) {
        console.error('Error updating group:', error);
        handleSnackBarData({ message: 'Failed to update group. Please try again.', severity: 'error' });
        setIsSubmitting(false);
      }
    } else if (selectedUsers && selectedUsers.length > 0) {
      apiUserManagement
        .addUserGroup(groupNameValue, groupDescValue)
        .then((result) => {
          const group = result?.data?.data?.id;
          const usernames = selectedUsers.map((user) => user[1].drilldownQuery.username);
          if (usernames && usernames.length > 0) {
            apiUserManagement
              .manageGroupUsers({ group_id: group, add_usernames: usernames, remove_usernames: [] })
              .then(() => {
                handleSnackBarData({ message: 'Group added successfully', icon: '', severity: 'success' });
                adjustCloseAction(true);
              })
              .catch(() => {
                handleSnackBarData({ message: 'An error occurred', severity: 'error', icon: modalerror.default.src });
                adjustCloseAction(false);
              });
          }
        })
        .catch(() => {
          handleSnackBarData({ message: 'An error occurred', severity: 'error', icon: modalerror.default.src });
          adjustCloseAction(false);
        });
    } else {
      apiUserManagement
        .addUserGroup(groupNameValue, groupDescValue)
        .then(() => {
          handleSnackBarData({ message: 'Group added successfully', severity: 'success', icon: '' });
          adjustCloseAction(true);
        })
        .catch(() => {
          handleSnackBarData({ message: 'An error occurred', severity: 'error', icon: modalerror.default.src });
          adjustCloseAction(false);
        });
    }
  };

  useEffect(() => {
    if (open) {
      apiUserManagement.listUsers({ status: 'active' }).then((res) => {
        setUsers(res?.data);
        const opts = res?.data?.filter((m) => m.username != '').map((u) => ({ label: u.username, value: u.username }));
        setUserOptions(opts);
      });
      // Fetch accounts once per modal session, on open — NOT gated on the active tab. Both the
      // role-population effect below and the save path need the full account list: if a group has
      // account/namespace roles but the user saves without ever opening those tabs, an unfetched
      // list left showSelectedAccounts empty and the replace-all upsert then silently wiped every
      // existing account/namespace role. The `accounts.length === 0` guard keeps this a single
      // fetch, so switching tabs never re-runs the population effect (which would re-add rows the
      // user just deleted); cleanState() resets accounts to [] on close, so each open re-fetches.
      if (isEdit && accounts.length === 0) {
        apiUserManagement.listAccounts().then((res) => {
          setAccounts(res);
          const allSelectedAccountIds = showSelectedAccounts.map((item) => item[0].drilldownQuery.id);
          const accountOpts = res
            ?.filter((m) => !allSelectedAccountIds.includes(m.id))
            .map((u) => ({ label: u.account_name, value: u.id, cloud_provider: u.cloud_provider }));
          setAccountOptions(accountOpts);
        });
      }
      if (isEdit && rbacType == 'k8s_namespace') {
        apiUserManagement.listK8sNamespaces().then((res) => {
          setAccountNamespaceOptions(res?.k8s_namespaces?.rows ?? []);
        });
      }
    }
  }, [open, rbacType, isEdit]);

  useEffect(() => {
    if (isEdit) {
      setGroupNameValue(groupData?.name);
      setGroupDescValue(groupData?.description);
      if (open && groupData?.group_roles?.length > 0) {
        if (accounts.length > 0) {
          // Collapse any legacy duplicate account rows (same account saved with multiple roles)
          // to one per account, preferring admin over read-only — mirrors the most-permissive
          // resolution the backend applies, so the modal shows a single, unambiguous role.
          const accountRoleByEntity = new Map();
          for (let gr of groupData?.group_roles ?? []) {
            if (gr.entity_type == 'account') {
              const prevRole = accountRoleByEntity.get(gr.entity_id);
              if (prevRole === undefined || gr.role === 'account_admin') {
                accountRoleByEntity.set(gr.entity_id, gr.role);
              }
            } else if (gr.entity_type == 'k8s_namespace') {
              let entitySplits = gr.entity_id.split(':');
              handleAccountNamespaceSelection(entitySplits[0], entitySplits[1], gr.role);
            }
          }
          for (const [entityId, role] of accountRoleByEntity) {
            handleAccountSelection(entityId, role);
          }
        }
        const tenant = groupData?.group_roles.filter((gf) => gf.entity_type == 'tenant') || [];
        if (tenant && tenant.length > 0) {
          setGroupRole(tenant[0].role);
        }
      }
    }
  }, [open, groupData, accounts, isEdit]);

  useEffect(() => {
    if (isEdit && open && groupData.id && currentUser && !groupUsersLoaded.current) {
      groupUsersLoaded.current = true;
      const data = { offset: 0, limit: 100, id: groupData.id, isCountOnly: false };
      setLoading(true);
      apiUserManagement.listUserGroupUsers(data).then((res) => {
        let result = res?.data?.usergroup_users ?? [];
        let alreadySelected = result.map((entry) => buildMemberRow(entry.user));
        setLoading(false);
        setSelectedUsers(alreadySelected);
      });
    }
  }, [groupData, open, isEdit, currentUser]);

  function buildMemberRow(user) {
    return [
      { text: user?.display_name },
      { text: user?.username, drilldownQuery: { username: user?.username }, status: user?.status },
      { component: <Label text={user?.status} /> },
      {
        component: (
          <IconButton
            sx={trashBtnSx}
            onClick={() => handleUserDelete(user?.username)}
            disabled={!hasWriteAccess() && currentUser?.user?.email == user?.username}
          >
            <SafeIcon alt='delete icon' src={DeleteIcon} height='20' width='20' />
          </IconButton>
        ),
      },
    ];
  }

  function handleDeleteAdd(username) {
    setSelectedUsers((prev) => prev.filter((user) => user[1].drilldownQuery.username !== username));
  }

  function handleUserDelete(username) {
    setSelectedUsers((prev) => prev.filter((user) => user[1].drilldownQuery.username !== username));
    if (userAdded.has(username)) {
      setUserAdded((prev) => {
        const next = new Set(prev);
        next.delete(username);
        return next;
      });
    } else {
      setUserRemoved((prev) => new Set([...prev, username]));
    }
  }

  function handleUserSelectionAdd(value) {
    if (!value) return;
    const filterUser = users.find((u) => u.username === value);
    if (!filterUser) return;
    const newUser = [
      { text: filterUser.display_name },
      { text: filterUser.username, drilldownQuery: { username: filterUser.username }, status: filterUser.status },
      { component: <Label text={filterUser.status} /> },
      {
        component: (
          <IconButton sx={trashBtnSx} onClick={() => handleDeleteAdd(filterUser.username)}>
            <SafeIcon alt='delete icon' src={DeleteIcon} height='20' width='20' />
          </IconButton>
        ),
      },
    ];
    setSelectedUsers((prev) => {
      if (prev.some((user) => user[1].drilldownQuery.username === value)) return prev;
      return [...prev, newUser];
    });
  }

  function handleUserSelectionEdit(value) {
    const filterUser = users.find((u) => u.username === value);
    if (!filterUser) return;
    const newUser = buildMemberRow(filterUser);
    setSelectedUsers((prev) => {
      if (prev.some((user) => user[1].drilldownQuery.username === value)) return prev;
      return [...prev, newUser];
    });
    setUserAdded((prev) => new Set([...prev, value]));
    setUserRemoved((prev) => {
      const next = new Set(prev);
      next.delete(value);
      return next;
    });
  }

  function handleUserSelection(value) {
    if (isEdit) handleUserSelectionEdit(value);
    else handleUserSelectionAdd(value);
  }

  function handleAccountSelection(account, accountRole) {
    if (!account || !accountRole) return;
    const filterAccount = accounts.find((u) => u.id === account);
    if (!filterAccount) return;
    setShowSelectedAccounts((prev) => {
      // One role per account: assigning a role for an account that already has one REPLACES it
      // (override), rather than stacking a second row. Functional updater avoids a stale-state
      // bug during the init useEffect's synchronous loop.
      const existingIdx = prev.findIndex((a) => a[0].drilldownQuery.id === account);
      if (existingIdx !== -1 && prev[existingIdx][1].text === accountRole) return prev; // same role already set — no-op
      const newAccount = [
        {
          component: (
            <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)' }}>
              <ProviderTag provider={filterAccount.cloud_provider} />
              <Box sx={{ font: "500 12.5px 'Roboto'", color: ds.gray[700] }}>{filterAccount.account_name}</Box>
            </Box>
          ),
          text: filterAccount.account_name,
          drilldownQuery: { id: filterAccount.id },
        },
        { text: accountRole },
        {
          component: (
            <IconButton sx={trashBtnSx} onClick={() => handleAccountDelete(filterAccount.id, accountRole)}>
              <SafeIcon alt='delete icon' src={DeleteIcon} height='20' width='20' />
            </IconButton>
          ),
        },
      ];
      if (existingIdx !== -1) {
        // Replace the existing row in place so the account keeps its position in the table.
        const next = [...prev];
        next[existingIdx] = newAccount;
        return next;
      }
      return [...prev, newAccount];
    });
    setSelectedAccount('');
    setSelectedAccountRole('');
  }

  function handleAccountNamespaceSelection(accountId, namespace, namespaceRole) {
    if (!accountId || !namespaceRole || !namespace) return;
    const filterAccount = accounts.find((u) => u.id === accountId);
    if (!filterAccount) return;
    setShowSelectedAccountNamespaces((prev) => {
      // Dedup key is (account, namespace, role). Same (account, namespace) with different roles
      // is allowed; only block exact tuple duplicates.
      if (
        prev.some(
          (n) => n[0].drilldownQuery.id === accountId && n[0].drilldownQuery.namespace === namespace && n[0].drilldownQuery.role === namespaceRole
        )
      ) {
        return prev;
      }
      const newRow = [
        {
          text: filterAccount.account_name,
          drilldownQuery: { id: filterAccount.id, namespace, role: namespaceRole },
        },
        { text: namespace },
        { text: namespaceRole },
        {
          component: (
            <IconButton sx={trashBtnSx} onClick={() => handleAccountNamespaceDelete(accountId, namespace, namespaceRole)}>
              <SafeIcon alt='delete icon' src={DeleteIcon} height='20' width='20' />
            </IconButton>
          ),
        },
      ];
      return [...prev, newRow];
    });
    setAccountNamespaceAdded((prev) => {
      if (prev.some((a) => a.accountId === accountId && a.namespace === namespace && a.role === namespaceRole)) return prev;
      return [...prev, { accountId, role: namespaceRole, namespace }];
    });
    setAccountNamespaceRemoved((prev) => prev.filter((a) => !(a.accountId === accountId && a.namespace === namespace && a.role === namespaceRole)));
    setSelectedAccount('');
    setSelectedAccountNamespace('');
    setSelectedAccountNamespaceRole('');
  }

  function handleAccountDelete(id, role) {
    setShowSelectedAccounts((prev) => prev.filter((account) => !(account[0].drilldownQuery.id === id && account[1].text === role)));
  }

  function handleAccountNamespaceDelete(accountId, namespace, namespaceRole) {
    setShowSelectedAccountNamespaces((prev) =>
      prev.filter(
        (account) =>
          !(
            account[0].drilldownQuery.id == accountId &&
            account[0].drilldownQuery.namespace == namespace &&
            account[0].drilldownQuery.role == namespaceRole
          )
      )
    );
    const idx = accountNamespaceAdded.findIndex((a) => a.accountId === accountId && a.namespace === namespace && a.role === namespaceRole);
    if (idx !== -1) {
      setAccountNamespaceAdded((prev) => prev.filter((_, i) => i !== idx));
    } else {
      setAccountNamespaceRemoved((prev) => [...prev, { accountId, role: namespaceRole, namespace }]);
    }
  }

  // Users already added to the group are listed in the members table below (with a
  // remove action), so filter them out of the picker options to avoid duplication.
  const selectedUsernames = new Set(selectedUsers.map((user) => user[1].drilldownQuery.username));
  const availableUserOptions = userOptions?.filter((u) => !selectedUsernames.has(u.value)) ?? [];

  const filteredMembers = isEdit ? selectedUsers.filter((u) => u[1].status === userStatusFilter) : selectedUsers;

  return (
    <Modal
      open={open}
      handleClose={() => (isSubmitting ? undefined : adjustCloseAction(false))}
      title={isEdit ? 'Edit Group' : 'Add Group'}
      width={isEdit ? 'md' : 'sm'}
      loader={isSubmitting}
      sx={{ '& .MuiDialog-paper': { maxWidth: isEdit ? ds.space.mul(0, 380) : ds.space.mul(0, 360), maxHeight: '90vh' } }}
      contentStyles={{ padding: 'var(--ds-space-4) var(--ds-space-5)', overflowX: 'hidden' }}
      actionButtons={
        <Box
          sx={{
            display: 'flex',
            justifyContent: 'flex-end',
            gap: 'var(--ds-space-2)',
          }}
        >
          <Button id='cancel' tone='secondary' size='md' onClick={() => adjustCloseAction(false)} disabled={isSubmitting}>
            Cancel
          </Button>
          <Button id='submit' type='submit' size='md' disabled={isSubmitting} loading={isSubmitting} onClick={handleSubmit(submitForm)}>
            {isEdit ? 'Save changes' : 'Create group'}
          </Button>
        </Box>
      }
    >
      <Box
        component='form'
        onSubmit={(e) => e.preventDefault()}
        onKeyDown={handleKeyDown}
        sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-2)' }}
      >
        {/* Group name + description */}
        {isEdit ? (
          <Box sx={{ display: 'grid', gridTemplateColumns: '1fr 1.4fr', gap: 'var(--ds-space-3)', '& > *': { minWidth: 0 } }}>
            <Box>
              {fieldLabel('Group name', true)}
              <Input
                id='groupname'
                size='sm'
                value={groupNameValue || ''}
                onChange={(next) => {
                  setGroupNameValue(next);
                  textValidation(next, validationError, setValidationError, 'groupname', [
                    'required',
                    'firstLetterAlphaNum',
                    'minlength5',
                    'alphaNumWithSpace',
                  ]);
                }}
                error={validationError.groupname}
              />
            </Box>
            <Box>
              {fieldLabel('Description')}
              <Input id='description' size='sm' placeholder='Optional' value={groupDescValue || ''} onChange={setGroupDescValue} />
            </Box>
          </Box>
        ) : (
          <>
            <Box>
              {fieldLabel('Group name', true)}
              <Input
                id='groupname'
                size='sm'
                placeholder='e.g. Platform-Eng'
                value={groupNameValue || ''}
                onChange={(next) => {
                  setGroupNameValue(next);
                  textValidation(next, validationError, setValidationError, 'groupname', [
                    'required',
                    'firstLetterAlphaNum',
                    'minlength5',
                    'alphaNumWithSpace',
                  ]);
                }}
                error={validationError.groupname}
                help={'Letters, numbers, dashes, underscores, and spaces only.'}
              />
            </Box>
            <Box>
              {fieldLabel('Description')}
              <Input
                id='description'
                size='sm'
                type='textarea'
                rows={3}
                placeholder='What is this group for? (optional)'
                value={groupDescValue || ''}
                onChange={setGroupDescValue}
              />
            </Box>
          </>
        )}

        {/* RBAC tabs (edit only) */}
        {isEdit && (
          <>
            <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-1)' }}>
              <SectionLabel>Assign Roles</SectionLabel>
              <Tabs tabs={RBAC_TABS} value={rbacType} onChange={(next) => setRbacType(next)} size='sm' ariaLabel='Assign roles' />
            </Box>

            {rbacType === 'tenant' && (
              <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-1)' }}>
                <Box sx={{ maxWidth: ds.space.mul(0, 140) }}>
                  {fieldLabel('Tenant role')}
                  <Select
                    id='group-tenant-role'
                    value={groupRole || ''}
                    options={TENANT_ROLE_OPTIONS}
                    onChange={(next) => setGroupRole(next)}
                    placeholder='Select tenant role'
                    minWidth='100%'
                  />
                </Box>
                <Box sx={{ font: "400 11.5px/1.4 'Roboto'", color: ds.gray[400] }}>Tenant-level role applies to every account in this tenant.</Box>
              </Box>
            )}

            {rbacType === 'account' && (
              <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-3)' }}>
                <Box sx={{ display: 'flex', alignItems: 'flex-end', gap: 'var(--ds-space-2)' }}>
                  <Box sx={{ flex: 1, minWidth: 0 }}>
                    {fieldLabel('Accounts')}
                    <Select
                      id='group-account'
                      multiple
                      value={selectedAccounts}
                      options={accountOptions}
                      onChange={(next) => setSelectedAccounts(next)}
                      placeholder='Select Accounts'
                      minWidth='100%'
                      maxChips={2}
                    />
                  </Box>
                  <Box sx={{ flex: 1, minWidth: 0 }}>
                    {fieldLabel('Role')}
                    <Select
                      id='group-account-role'
                      value={selectedAccountRole || ''}
                      options={ACCOUNT_ROLE_OPTIONS}
                      onChange={(next) => setSelectedAccountRole(next)}
                      placeholder='Select role'
                      minWidth='100%'
                    />
                  </Box>
                  <Box sx={{ flexShrink: 0 }}>
                    <Button
                      type='button'
                      size='md'
                      onClick={() => {
                        const toAdd = selectedAccounts.filter(
                          (accountId) => !showSelectedAccounts.some((a) => a[0].drilldownQuery.id === accountId && a[1].text === selectedAccountRole)
                        );
                        if (toAdd.length === 0) {
                          handleSnackBarData({
                            message: 'Selected account(s) already have this role assigned.',
                            severity: 'warning',
                          });
                          return;
                        }
                        // Accounts already carrying a different role are overridden (one role per account).
                        const replacedCount = toAdd.filter((accountId) =>
                          showSelectedAccounts.some((a) => a[0].drilldownQuery.id === accountId)
                        ).length;
                        toAdd.forEach((accountId) => handleAccountSelection(accountId, selectedAccountRole));
                        if (replacedCount > 0) {
                          handleSnackBarData({
                            message: `Replaced the existing role for ${replacedCount} account${replacedCount > 1 ? 's' : ''}.`,
                            severity: 'info',
                          });
                        } else if (toAdd.length < selectedAccounts.length) {
                          handleSnackBarData({
                            message: 'Some accounts already had this role and were skipped.',
                            severity: 'warning',
                          });
                        }
                        setSelectedAccounts([]);
                        setSelectedAccountRole('');
                      }}
                      disabled={selectedAccounts.length === 0 || !selectedAccountRole}
                    >
                      Add
                    </Button>
                  </Box>
                </Box>
                {showSelectedAccounts.length > 0 && (
                  <Box sx={tableWrapperSx}>
                    <CustomTable
                      tableData={showSelectedAccounts}
                      headers={[
                        { name: 'Account', width: '50%' },
                        { name: 'Role', width: '42%' },
                        { name: '', width: '8%' },
                      ]}
                      id='selected-accounts'
                      showExpandable={false}
                      rowsPerPage={showSelectedAccounts.length}
                      totalRows={showSelectedAccounts.length}
                      loading={loading}
                      showEmptyStateText={true}
                    />
                  </Box>
                )}
              </Box>
            )}

            {rbacType === 'k8s_namespace' && (
              <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-3)' }}>
                <Box
                  sx={{
                    display: 'grid',
                    gridTemplateColumns: '1fr 1fr 1fr auto',
                    gap: 'var(--ds-space-2)',
                    alignItems: 'flex-end',
                    '& > *': { minWidth: 0 },
                  }}
                >
                  <Box>
                    {fieldLabel('K8s account')}
                    <Select
                      id='group-k8s-account'
                      value={selectedAccount || ''}
                      options={accountOptions?.filter((a) => a.cloud_provider == 'K8s')}
                      onChange={(next) => setSelectedAccount(next)}
                      placeholder='Select K8s account'
                      minWidth='100%'
                    />
                  </Box>
                  <Box>
                    {fieldLabel('Namespace')}
                    <Select
                      id='group-k8s-namespace'
                      value={selectedAccountNamespace || ''}
                      options={accountNamespaceOptions?.filter((a) => a.account_id == selectedAccount).map((a) => ({ label: a.name, value: a.name }))}
                      onChange={(next) => setSelectedAccountNamespace(next)}
                      placeholder='Select namespace'
                      minWidth='100%'
                    />
                  </Box>
                  <Box>
                    {fieldLabel('Role')}
                    <Select
                      id='group-k8s-role'
                      value={selectedAccountNamespaceRole || ''}
                      options={NAMESPACE_ROLE_OPTIONS}
                      onChange={(next) => setSelectedAccountNamespaceRole(next)}
                      placeholder='Select role'
                      minWidth='100%'
                    />
                  </Box>
                  <Box sx={{ flexShrink: 0 }}>
                    <Button
                      type='button'
                      size='md'
                      onClick={() => {
                        const isDup = showSelectedAccountNamespaces.some(
                          (n) =>
                            n[0].drilldownQuery.id === selectedAccount &&
                            n[0].drilldownQuery.namespace === selectedAccountNamespace &&
                            n[0].drilldownQuery.role === selectedAccountNamespaceRole
                        );
                        if (isDup) {
                          handleSnackBarData({
                            message: 'This namespace already has this role assigned.',
                            severity: 'warning',
                          });
                          return;
                        }
                        handleAccountNamespaceSelection(selectedAccount, selectedAccountNamespace, selectedAccountNamespaceRole);
                      }}
                      disabled={!selectedAccount || !selectedAccountNamespace || !selectedAccountNamespaceRole}
                    >
                      Add
                    </Button>
                  </Box>
                </Box>
                {showSelectedAccountNamespaces.length > 0 && (
                  <Box sx={tableWrapperSx}>
                    <CustomTable
                      tableData={showSelectedAccountNamespaces}
                      headers={[
                        { name: 'Account', width: '35%' },
                        { name: 'Namespace', width: '27%' },
                        { name: 'Role', width: '30%' },
                        { name: '', width: '8%' },
                      ]}
                      id='selected-account-namespaces'
                      showExpandable={false}
                      rowsPerPage={showSelectedAccountNamespaces.length}
                      totalRows={showSelectedAccountNamespaces.length}
                      loading={loading}
                      showEmptyStateText={true}
                    />
                  </Box>
                )}
              </Box>
            )}
          </>
        )}

        {/* Members section */}
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-2)' }}>
          <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
            <SectionLabel>{isEdit ? `Members · ${selectedUsers.length}` : 'Initial members'}</SectionLabel>
            {isEdit && (
              <SegmentedFilter tabs={MEMBER_FILTER_TABS} value={userStatusFilter} onChange={setUserStatusFilter} dataTestId='member-filter' />
            )}
          </Box>

          <Select
            id='all-users-for-group'
            multiple
            // Add-only picker: selected users live in the members table below, so the
            // value stays empty and already-selected users are dropped from the options.
            value={[]}
            options={availableUserOptions}
            clearable={false}
            hideOptionCheckbox
            maxChips={3}
            placeholder={isEdit ? 'Add user' : 'Select users'}
            searchPlaceholder={isEdit ? 'Add user…' : 'Search users…'}
            minWidth='100%'
            onChange={(usernames) => {
              usernames.forEach((u) => handleUserSelection(u));
            }}
          />
          {!isEdit && (
            <Typography sx={{ font: "400 11.5px/1.4 'Roboto'", color: ds.gray[400] }}>
              You can assign roles and permissions after creation.
            </Typography>
          )}

          {(isEdit ? true : selectedUsers.length > 0) && (
            <Box sx={tableWrapperSx}>
              <CustomTable
                tableData={filteredMembers}
                headers={[
                  { name: 'Display Name', width: '32%' },
                  { name: 'Username', width: '40%' },
                  { name: 'Status', width: '20%' },
                  { name: '', width: '8%' },
                ]}
                id='selected-users'
                showExpandable={false}
                rowsPerPage={selectedUsers.length || 1}
                totalRows={selectedUsers.length}
                loading={loading}
                showEmptyStateText={true}
              />
            </Box>
          )}
        </Box>
      </Box>
    </Modal>
  );
}

GroupModal.propTypes = {
  open: PropTypes.bool,
  handleClose: PropTypes.func,
  groupData: PropTypes.object,
  handleSnackBarData: PropTypes.func,
};

export default GroupModal;
