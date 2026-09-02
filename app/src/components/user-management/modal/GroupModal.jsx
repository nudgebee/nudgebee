import React, { useEffect, useMemo, useRef, useState } from 'react';
import { Box, IconButton } from '@mui/material';
import Typography from '@mui/material/Typography';
import { Input } from '@ui/Input';
import { useForm } from 'react-hook-form';
import apiUserManagement from '@api1/user';
import { listCustomRoles, updateRoleGroupAssignments, updateRoleGroupAccountAssignments } from '@api1/roles';
import CustomTable from '@shared/tables/CustomTable';
import { Select } from '@ui/Select';
import Tabs from '@shared/navigation/Tabs';
import { ToggleGroup } from '@ui/ToggleGroup';
import { useSession } from 'next-auth/react';
import SafeIcon from '@shared/icons/SafeIcon';
import PropTypes from 'prop-types';
import { Button } from '@ui/Button';
import { Modal } from '@ui/Modal';
import { canManage, isCustomRolesEnabled, isTenantAdmin } from '@lib/auth';
import { textValidation } from '@lib/validation';
import { ds } from 'src/utils/colors';
import { DeleteIconRed as DeleteIcon, modalerror, AWSIcon, AzureIcon, GCPIcon, ouK8s as KubernetesIcon } from '@assets';
import { Label } from '@ui/Label';
import { Card } from '@ui/Card';
import { sameIdSet } from '@utils/common';
import CloudProviderIcon from '@shared/icons/CloudIcon';

const renderAccountGroupIcon = (provider) => <CloudProviderIcon cloud_provider={provider} width='14px' height='14px' />;

const RBAC_TABS = {
  tabOptions: [
    { value: 'tenant', text: 'Tenant' },
    { value: 'account', text: 'Account' },
  ],
};

const TENANT_ROLE_OPTIONS = [
  { label: 'Admin', value: 'tenant_admin' },
  { label: 'ReadOnly Admin', value: 'tenant_admin_readonly' },
];

const ACCOUNT_ROLE_OPTIONS = [
  { label: 'Admin', value: 'account_admin' },
  { label: 'ReadOnly Admin', value: 'account_admin_readonly' },
];

// The two built-in account roles. Anything else picked in the Account tab's role
// dropdown is a custom role id — built-ins carry data scope and are written to
// group_roles, custom roles are additive and written to custom_role_assignments
// scoped to the account ("Group G holds Role R on Account A").
const ACCOUNT_BUILTIN_ROLES = new Set(ACCOUNT_ROLE_OPTIONS.map((o) => o.value));
const isBuiltinAccountRole = (role) => ACCOUNT_BUILTIN_ROLES.has(role);

const MEMBER_FILTER_TABS = [
  { value: 'active', label: 'Active' },
  { value: 'inactive', label: 'Inactive' },
  { value: 'suspended', label: 'Suspended' },
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

function CardHeader({ title, description, action }) {
  return (
    <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 'var(--ds-space-3)' }}>
      <Box sx={{ minWidth: 0 }}>
        <Typography sx={{ fontSize: ds.text.title, fontWeight: ds.weight.semibold, color: ds.gray[700] }}>{title}</Typography>
        {description && <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500], mt: ds.space[0] }}>{description}</Typography>}
      </Box>
      {action}
    </Box>
  );
}

CardHeader.propTypes = { title: PropTypes.string, description: PropTypes.string, action: PropTypes.node };

// Each editable section owns its own write. The four axes a group carries
// (name/description, tenant role, account roles, namespace roles, membership)
// live in different tables behind disjoint RPCs, so a section saves without
// touching the others — a failure is contained to the section that caused it
// instead of leaving a half-applied group behind. `dirty` gates the button so
// an untouched section never issues a request (each RBAC write busts the
// tenant's security-context cache upstream).
function SectionSave({ dirty, saving, disabled, onSave, testId }) {
  return (
    <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)', flexShrink: 0 }}>
      {dirty && !saving && <Box sx={{ font: "400 11px/1 'Roboto'", color: ds.gray[500] }}>Unsaved</Box>}
      <Button type='button' size='sm' tone='secondary' disabled={!dirty || saving || disabled} loading={saving} onClick={onSave} data-testid={testId}>
        Save
      </Button>
    </Box>
  );
}

SectionSave.propTypes = {
  dirty: PropTypes.bool,
  saving: PropTypes.bool,
  disabled: PropTypes.bool,
  onSave: PropTypes.func,
  testId: PropTypes.string,
};

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

  const [isEdit, setIsEdit] = useState(false);
  if (open) {
    const currentIsEdit = !!(groupData && Object.keys(groupData).length > 0);
    if (currentIsEdit !== isEdit) {
      setIsEdit(currentIsEdit);
    }
  }

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
  // Group administration (name, description, membership) is delegable via a
  // usergroups:Write grant; handing out authority THROUGH a group is not. Mirrors
  // the backend: userroles_upsert_group and the customroles assignment actions are
  // non-grantable, and mayChangePrivilegedGroupMembership keeps a grant holder off
  // the membership of any group that already carries a role.
  const canAssignGroupRoles = isTenantAdmin();
  const [accountOptions, setAccountOptions] = useState([]);
  const [accounts, setAccounts] = useState([]);
  const [showSelectedAccounts, setShowSelectedAccounts] = useState([]);
  // Account RBAC tab supports selecting multiple accounts at once.
  const [selectedAccounts, setSelectedAccounts] = useState([]);
  const [selectedAccountRole, setSelectedAccountRole] = useState('');
  // Dynamic-RBAC custom roles: full role objects (with group_ids) for the diff,
  // and the ids currently picked in the multi-select.
  const [customRolesList, setCustomRolesList] = useState([]);
  const [selectedCustomRoles, setSelectedCustomRoles] = useState([]);
  // Account-scoped custom-role rows waiting for `accounts` to load before they
  // can be rendered (handleAccountSelection needs the account to resolve).
  const [pendingScopedRoleRows, setPendingScopedRoleRows] = useState([]);

  // Which section is mid-save ('info' | 'tenant' | 'account' | 'namespace' | 'members'),
  // so only that section's button spins.
  const [savingSection, setSavingSection] = useState(null);
  // A section's baseline is normally derived from groupData/customRolesList. After
  // a successful save those props are stale (the modal stays open and the parent
  // list refetches only on close), so the saved value is recorded here and wins.
  const [savedBaseline, setSavedBaseline] = useState({});
  // True once anything has been written, so closing refreshes the parent list.
  const [anySaved, setAnySaved] = useState(false);
  // The account tab's save is replace-all. Until the accounts list has actually
  // resolved, showSelectedAccounts is empty for reasons that have nothing to do
  // with user intent, and saving then would wipe every existing account role.
  const [accountsLoaded, setAccountsLoaded] = useState(false);
  const [customRolesLoaded, setCustomRolesLoaded] = useState(false);

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
    setSelectedAccounts([]);
    setSelectedAccountRole('');
    setCustomRolesList([]);
    setPendingScopedRoleRows([]);
    setSelectedCustomRoles([]);
    setLoading(false);
    setIsSubmitting(false);
    setSavingSection(null);
    setSavedBaseline({});
    setAnySaved(false);
    setAccountsLoaded(false);
    setCustomRolesLoaded(false);
    groupUsersLoaded.current = false;
    reset();
  }

  function adjustCloseAction(shouldUpdate = false) {
    cleanState();
    handleClose(shouldUpdate);
  }

  // Validates the group name against the same rules the input enforces, plus the
  // async uniqueness check. Returns true when the name is usable.
  async function validateGroupName() {
    let nameError;
    textValidation(
      groupNameValue ?? '',
      validationError,
      (next) => {
        const computed = typeof next === 'function' ? next(validationError) : next;
        nameError = computed.groupname;
        setValidationError(computed);
      },
      'groupname',
      ['required', 'firstLetterAlphaNum', 'minlength5', 'alphaNumWithSpace']
    );
    if (!groupNameValue || nameError) return false;
    // Compare against baseline.name, not groupData.name: after an Info save that
    // renamed the group, groupData is stale, so checking against it would run the
    // uniqueness query for the group's own (new) name and reject every later save.
    if (!isEdit || groupNameValue !== baseline.name) {
      if (await groupNameExists(groupNameValue)) {
        setValidationError({ groupname: 'Group name already in use' });
        return false;
      }
    }
    setValidationError({});
    return true;
  }

  // ---------------------------------------------------------------------------
  // Per-section baselines and dirty flags.
  //
  // Each baseline is derived from the same source its section hydrates from, so
  // a section is dirty exactly when the rendered state diverges from what the
  // server holds — no snapshot-timing coupling to the hydration effects.
  // ---------------------------------------------------------------------------
  const baseline = useMemo(() => {
    const groupRoles = groupData?.group_roles ?? [];
    // Hydration collapses legacy duplicate account rows (same account saved under
    // several built-in roles) to one, preferring admin. The baseline has to apply
    // the identical collapse or such a group would read as permanently dirty.
    const builtinByAccount = new Map();
    for (const gr of groupRoles) {
      if (gr.entity_type !== 'account') continue;
      const prev = builtinByAccount.get(gr.entity_id);
      if (prev === undefined || gr.role === 'account_admin') builtinByAccount.set(gr.entity_id, gr.role);
    }
    const accountKeys = [...builtinByAccount].map(([accountId, role]) => `${accountId}|${role}`);
    for (const r of customRolesList) {
      for (const ga of r.group_account_assignments ?? []) {
        if (ga.principal_id === groupData?.id) accountKeys.push(`${ga.entity_id}|${r.id}`);
      }
    }
    return {
      name: groupData?.name ?? '',
      description: groupData?.description ?? '',
      tenantRole: groupRoles.find((gr) => gr.entity_type === 'tenant')?.role ?? '',
      customRoleIds: customRolesList.filter((r) => (r.group_ids ?? []).includes(groupData?.id)).map((r) => r.id),
      accountKeys,
      ...savedBaseline,
    };
  }, [groupData, customRolesList, savedBaseline]);

  const currentAccountKeys = showSelectedAccounts.map((a) => `${a[0].drilldownQuery.id}|${a[1].roleValue}`);

  // A binding whose account isn't in the (active-only) accounts list can't render
  // — handleAccountSelection drops ids it can't resolve — so it never appears in
  // currentAccountKeys. Left unhandled, the section reads permanently dirty and a
  // replace-all save silently drops the binding. Track those keys so they're
  // excluded from the dirty diff AND re-attached to the save payload. Before the
  // accounts list loads, `accounts` is [] so every baseline key counts as
  // unresolved, which correctly makes the section clean (and un-saveable) until
  // the data is in.
  const knownAccountIds = useMemo(() => new Set((accounts ?? []).map((a) => a.id)), [accounts]);
  const accountIdOfKey = (key) => key.slice(0, key.lastIndexOf('|'));
  const unresolvedAccountKeys = baseline.accountKeys.filter((k) => !knownAccountIds.has(accountIdOfKey(k)));

  const infoDirty = (groupNameValue ?? '') !== baseline.name || (groupDescValue ?? '') !== baseline.description;
  const tenantDirty = (groupRole ?? '') !== baseline.tenantRole || !sameIdSet(selectedCustomRoles, baseline.customRoleIds);
  const accountDirty = !sameIdSet([...currentAccountKeys, ...unresolvedAccountKeys], baseline.accountKeys);
  const membersDirty = userAdded.size > 0 || userRemoved.size > 0;

  const dirtySections = [infoDirty && 'Group Info', tenantDirty && 'Tenant roles', accountDirty && 'Account roles', membersDirty && 'Members'].filter(
    Boolean
  );

  // Runs one section's write, reporting success/failure for that section alone.
  async function runSectionSave(section, work, successMessage, baselinePatch) {
    setSavingSection(section);
    try {
      await work();
      setSavedBaseline((prev) => ({ ...prev, ...baselinePatch() }));
      setAnySaved(true);
      handleSnackBarData({ message: successMessage, severity: 'success' });
    } catch (error) {
      console.error(`Error saving ${section}:`, error);
      handleSnackBarData({ message: error?.message || `Failed to update ${section}`, severity: 'error' });
    } finally {
      setSavingSection(null);
    }
  }

  async function handleSaveInfo() {
    if (!(await validateGroupName())) return;
    // Snapshot the inputs before the await so the recorded baseline matches what
    // was written even if the fields change while the request is in flight.
    const name = groupNameValue;
    const description = groupDescValue;
    // `role` is deliberately omitted: usergroup_update treats it as a partial
    // (*string, nil = leave alone), while name/description are unconditional
    // overwrites. The tenant role has its own section below.
    await runSectionSave(
      'info',
      () => apiUserManagement.updateUserGroup({ id: groupData.id, name, description }),
      'Group details updated',
      () => ({ name, description })
    );
  }

  async function handleSaveTenant() {
    const desired = new Set(selectedCustomRoles);
    const initial = new Set(baseline.customRoleIds);
    // Custom-role assignment is role-side replace-all, so only the roles whose
    // membership for THIS group flipped get rewritten.
    const changedRoles = customRolesList.filter((r) => desired.has(r.id) !== initial.has(r.id));
    const tenantRoleChanged = (groupRole ?? '') !== baseline.tenantRole;
    // Snapshot for the baseline patch — captured before the await.
    const savedTenantRole = groupRole ?? '';
    const savedCustomRoleIds = [...selectedCustomRoles];
    await runSectionSave(
      'tenant',
      async () => {
        const tenantWrites = tenantRoleChanged ? [apiUserManagement.upsertGroupTenantRole({ group_id: groupData.id, role: groupRole || '' })] : [];
        // Tagged with their role id and kept in a separate array from the
        // tenant-role write, so the tenant-role payload can't leak into the
        // roles-written map below (it carries no roleId).
        const roleWrites = changedRoles.map((r) => {
          const current = r.group_ids ?? [];
          const nextIds = desired.has(r.id) ? Array.from(new Set([...current, groupData.id])) : current.filter((id) => id !== groupData.id);
          return updateRoleGroupAssignments(r.id, nextIds).then(() => ({ roleId: r.id, nextIds }));
        });
        const [, roleResults] = await Promise.all([Promise.all(tenantWrites), Promise.all(roleWrites)]);
        // Keep the local snapshot in step with what was just written, so a second
        // save in the same session diffs against reality rather than replaying.
        if (roleResults.length) {
          const written = new Map(roleResults.map((res) => [res.roleId, res.nextIds]));
          setCustomRolesList((prev) => prev.map((r) => (written.has(r.id) ? { ...r, group_ids: written.get(r.id) } : r)));
        }
      },
      'Tenant permissions updated',
      () => ({ tenantRole: savedTenantRole, customRoleIds: savedCustomRoleIds })
    );
  }

  async function handleSaveAccount() {
    // Built-in account roles are group_roles rows; custom roles are
    // account-scoped custom_role_assignments. Routing a custom role id into
    // upsertGroupAccountRoles would write a role string group_roles can't read.
    const builtinRows = showSelectedAccounts
      .filter((a) => isBuiltinAccountRole(a[1].roleValue))
      .map((a) => ({ account_id: a[0].drilldownQuery.id, role: a[1].roleValue }));

    const desiredRoleAccounts = new Map();
    for (const a of showSelectedAccounts) {
      if (isBuiltinAccountRole(a[1].roleValue)) continue;
      const list = desiredRoleAccounts.get(a[1].roleValue) ?? [];
      list.push(a[0].drilldownQuery.id);
      desiredRoleAccounts.set(a[1].roleValue, list);
    }

    // Re-attach bindings on accounts absent from the (active-only) list so the
    // replace-all writes below don't silently drop them (see unresolvedAccountKeys).
    for (const key of unresolvedAccountKeys) {
      const accountId = accountIdOfKey(key);
      const role = key.slice(key.lastIndexOf('|') + 1);
      if (isBuiltinAccountRole(role)) {
        builtinRows.push({ account_id: accountId, role });
      } else {
        const list = desiredRoleAccounts.get(role) ?? [];
        list.push(accountId);
        desiredRoleAccounts.set(role, list);
      }
    }

    // Roles whose accounts were cleared entirely still need an (empty) write, or
    // the removal never persists — hence the union with what was bound initially.
    const initiallyScoped = new Set(
      customRolesList.filter((r) => (r.group_account_assignments ?? []).some((ga) => ga.principal_id === groupData.id)).map((r) => r.id)
    );
    const roleIds = [...new Set([...desiredRoleAccounts.keys(), ...initiallyScoped])];

    // What we are about to write, snapshotted before the await so the baseline
    // records the payload rather than whatever the table shows when it resolves.
    const writtenAccountKeys = [
      ...builtinRows.map((r) => `${r.account_id}|${r.role}`),
      ...roleIds.flatMap((roleId) => (desiredRoleAccounts.get(roleId) ?? []).map((id) => `${id}|${roleId}`)),
    ];

    await runSectionSave(
      'account',
      async () => {
        await Promise.all([
          apiUserManagement.upsertGroupAccountRoles({ group_id: groupData.id, account_roles: builtinRows }),
          ...roleIds.map((roleId) => updateRoleGroupAccountAssignments(roleId, groupData.id, desiredRoleAccounts.get(roleId) ?? [])),
        ]);
        setCustomRolesList((prev) =>
          prev.map((r) => {
            if (!roleIds.includes(r.id)) return r;
            const others = (r.group_account_assignments ?? []).filter((ga) => ga.principal_id !== groupData.id);
            const mine = (desiredRoleAccounts.get(r.id) ?? []).map((accountId) => ({
              principal_id: groupData.id,
              entity_type: 'account',
              entity_id: accountId,
            }));
            return { ...r, group_account_assignments: [...others, ...mine] };
          })
        );
      },
      'Account permissions updated',
      () => ({ accountKeys: writtenAccountKeys })
    );
  }

  async function handleSaveMembers() {
    const added = [...userAdded];
    const removed = [...userRemoved];
    await runSectionSave(
      'members',
      async () => {
        await apiUserManagement.manageGroupUsers({ group_id: groupData.id, add_usernames: added, remove_usernames: removed });
        // Subtract only the usernames we actually sent — a membership change made
        // while the request was in flight stays pending instead of being wiped.
        setUserAdded((prev) => {
          const next = new Set(prev);
          added.forEach((u) => next.delete(u));
          return next;
        });
        setUserRemoved((prev) => {
          const next = new Set(prev);
          removed.forEach((u) => next.delete(u));
          return next;
        });
      },
      'Members updated',
      () => ({})
    );
  }

  // Create-only. In edit mode every section writes through its own handler
  // above, so there is no combined submit.
  const submitForm = async () => {
    // Show loader the moment user clicks — prevents the 1-2s gap during the async
    // checkGroupNameExists call where the button would otherwise look idle.
    setIsSubmitting(true);
    if (!(await validateGroupName())) {
      setIsSubmitting(false);
      return;
    }
    // Create the group first, on its own. A failure here persisted nothing, so
    // keep the form open for a clean retry.
    let groupId;
    try {
      const result = await apiUserManagement.addUserGroup(groupNameValue, groupDescValue);
      groupId = result?.data?.id;
      // A create that resolves without an id is a failure we can't act on —
      // treat it as one rather than proceeding to the member call with an
      // undefined group_id, which the gateway rejects as a missing required
      // variable and which reads as a member-attach problem on a group that
      // was never created.
      if (!groupId) {
        throw new Error('Group was not created');
      }
    } catch (error) {
      console.error('Error creating group:', error);
      handleSnackBarData({ message: error?.message || 'An error occurred', severity: 'error', icon: modalerror.default.src });
      setIsSubmitting(false);
      return;
    }
    // The group now exists. A member-add failure must NOT reopen the create form:
    // retrying would fail the name-uniqueness check against the group we just made,
    // and the group would be invisible (list never refreshed). Close and refresh,
    // surfacing that members didn't attach so the admin can re-open to add them.
    const usernames = selectedUsers.map((user) => user[1].drilldownQuery.username);
    if (usernames.length > 0) {
      try {
        await apiUserManagement.manageGroupUsers({ group_id: groupId, add_usernames: usernames, remove_usernames: [] });
      } catch (error) {
        console.error('Error adding members to new group:', error);
        handleSnackBarData({ message: `Group created, but adding members failed: ${error?.message || 'unknown error'}`, severity: 'warning' });
        adjustCloseAction(true);
        return;
      }
    }
    handleSnackBarData({ message: 'Group added successfully', icon: '', severity: 'success' });
    adjustCloseAction(true);
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
          // listAccounts swallows failures and returns the Error object, not a
          // rejection. Mark the section loaded ONLY on a real array — otherwise a
          // failed fetch would (a) store the Error as `accounts`, crashing the
          // baseline derivation, and (b) open the replace-all Account/Namespace
          // saves against an empty list, wiping every existing role. Leaving
          // accountsLoaded false keeps those saves disabled until the data lands.
          if (!Array.isArray(res)) return;
          setAccounts(res);
          const allSelectedAccountIds = showSelectedAccounts.map((item) => item[0].drilldownQuery.id);
          const accountOpts = res
            .filter((m) => !allSelectedAccountIds.includes(m.id))
            .map((u) => ({ label: u.account_name, value: u.id, cloud_provider: u.cloud_provider, group: u.cloud_provider || 'Other' }));
          setAccountOptions(accountOpts);
          setAccountsLoaded(true);
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

  // Load custom roles once per open (edit only). Keyed on groupData.id so
  // switching RBAC tabs doesn't refetch and discard the picker's selection.
  // Non-tenant-admins get an empty list → the picker stays hidden, and the fetch
  // is skipped entirely while the tenant's CUSTOM_ROLES feature is off.
  useEffect(() => {
    if (open && isEdit && groupData?.id && isCustomRolesEnabled()) {
      listCustomRoles()
        .then((roles) => {
          const list = roles ?? [];
          setCustomRolesList(list);
          setSelectedCustomRoles(list.filter((r) => (r.group_ids ?? []).includes(groupData.id)).map((r) => r.id));
          // Account-scoped bindings for this group live in custom_role_assignments,
          // not group_roles, so they hydrate here rather than in the group_roles
          // effect above. Deferred a tick so `accounts` is populated —
          // handleAccountSelection drops ids it can't resolve to an account.
          const scoped = list.flatMap((r) =>
            (r.group_account_assignments ?? [])
              .filter((ga) => ga.principal_id === groupData.id)
              .map((ga) => ({ roleId: r.id, accountId: ga.entity_id }))
          );
          if (scoped.length) setPendingScopedRoleRows(scoped);
        })
        .catch(() => setCustomRolesList([]))
        .finally(() => setCustomRolesLoaded(true));
    }
  }, [open, isEdit, groupData?.id]);

  // Render the scoped rows once both the accounts list and the role names are
  // available, then clear the queue so this runs once per open.
  useEffect(() => {
    if (!pendingScopedRoleRows.length || accounts.length === 0 || customRolesList.length === 0) return;
    pendingScopedRoleRows.forEach(({ accountId, roleId }) => handleAccountSelection(accountId, roleId));
    setPendingScopedRoleRows([]);
  }, [pendingScopedRoleRows, accounts, customRolesList]);

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
            disabled={!canManage('usergroups', 'Write') && currentUser?.user?.email == user?.username}
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
    const builtin = isBuiltinAccountRole(accountRole);
    // Custom roles are additive, so they stack alongside the built-in role and
    // alongside each other; only the single built-in role per account overrides.
    const roleLabel = builtin ? accountRole : customRolesList.find((r) => r.id === accountRole)?.name ?? accountRole;
    setShowSelectedAccounts((prev) => {
      // One BUILT-IN role per account: assigning one for an account that already has one REPLACES it
      // (override), rather than stacking a second row. Custom roles match on the exact role so several
      // can coexist. Functional updater avoids a stale-state bug during the init useEffect's loop.
      const existingIdx = builtin
        ? prev.findIndex((a) => a[0].drilldownQuery.id === account && isBuiltinAccountRole(a[1].roleValue))
        : prev.findIndex((a) => a[0].drilldownQuery.id === account && a[1].roleValue === accountRole);
      if (existingIdx !== -1 && prev[existingIdx][1].roleValue === accountRole) return prev; // same role already set — no-op
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
        // text = what the admin reads (custom roles show their name, not the id);
        // roleValue = what we persist and match on.
        { text: roleLabel, roleValue: accountRole },
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
    setSelectedAccountRole('');
  }

  function handleAccountDelete(id, role) {
    setShowSelectedAccounts((prev) => prev.filter((account) => !(account[0].drilldownQuery.id === id && account[1].roleValue === role)));
  }

  // Users already added to the group are listed in the members table below (with a
  // remove action), so filter them out of the picker options to avoid duplication.
  const selectedUsernames = new Set(selectedUsers.map((user) => user[1].drilldownQuery.username));
  const availableUserOptions = userOptions?.filter((u) => !selectedUsernames.has(u.value)) ?? [];

  const filteredMembers = isEdit ? selectedUsers.filter((u) => u[1].status === userStatusFilter) : selectedUsers;

  return (
    <Modal
      open={open}
      handleClose={() => (isSubmitting || savingSection ? undefined : adjustCloseAction(anySaved))}
      title={isEdit ? 'Edit Group' : 'Add Group'}
      width={isEdit ? 'md' : 'sm'}
      loader={isSubmitting}
      sx={{
        '& .MuiDialog-paper': {
          maxWidth: isEdit ? ds.space.mul(0, 380) : ds.space.mul(0, 360),
          maxHeight: '90vh',
          minHeight: isEdit ? '90vh' : 'unset',
        },
      }}
      contentStyles={{ padding: 'var(--ds-space-4) var(--ds-space-5)', overflowX: 'hidden' }}
      actionButtons={
        <Box
          sx={{
            display: 'flex',
            alignItems: 'center',
            justifyContent: isEdit && dirtySections.length > 0 ? 'space-between' : 'flex-end',
            gap: 'var(--ds-space-2)',
            width: '100%',
          }}
        >
          {/* Edit mode has no combined submit — each section saves itself — so name
              the sections still holding unsaved edits rather than losing them silently. */}
          {isEdit && dirtySections.length > 0 && (
            <Box sx={{ font: "400 11.5px/1.4 'Roboto'", color: ds.gray[500] }}>Unsaved changes in: {dirtySections.join(', ')}</Box>
          )}
          {isEdit ? (
            <Button id='cancel' tone='secondary' size='md' onClick={() => adjustCloseAction(anySaved)} disabled={!!savingSection}>
              Close
            </Button>
          ) : (
            <>
              <Button id='cancel' tone='secondary' size='md' onClick={() => adjustCloseAction(false)} disabled={isSubmitting}>
                Cancel
              </Button>
              <Button id='submit' type='submit' size='md' disabled={isSubmitting} loading={isSubmitting} onClick={handleSubmit(submitForm)}>
                Create group
              </Button>
            </>
          )}
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
        <Card
          variant='outlined'
          elevation='flat'
          header={
            <CardHeader
              title='Group Info'
              action={
                isEdit ? (
                  <SectionSave
                    dirty={infoDirty}
                    saving={savingSection === 'info'}
                    disabled={!!savingSection}
                    onSave={handleSaveInfo}
                    testId='save-group-info'
                  />
                ) : null
              }
            />
          }
        >
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
            <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-3)' }}>
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
            </Box>
          )}
        </Card>

        {/* RBAC tabs (edit only) */}
        {isEdit && (
          <Card
            variant='outlined'
            elevation='flat'
            header={
              <CardHeader
                title='Assign Roles'
                action={
                  /* One save per tab: tenant and account grants are two disjoint
                     RPCs, so the button acts on the open tab only. */
                  rbacType === 'tenant' ? (
                    <SectionSave
                      dirty={tenantDirty}
                      saving={savingSection === 'tenant'}
                      disabled={!!savingSection || !customRolesLoaded}
                      onSave={handleSaveTenant}
                      testId='save-tenant-roles'
                    />
                  ) : (
                    <SectionSave
                      dirty={accountDirty}
                      saving={savingSection === 'account'}
                      disabled={!!savingSection || !accountsLoaded || !customRolesLoaded}
                      onSave={handleSaveAccount}
                      testId='save-account-roles'
                    />
                  )
                }
              />
            }
          >
            <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-2)' }}>
              <Tabs options={RBAC_TABS} value={rbacType} onChange={(next) => setRbacType(next)} behavior='filter' ariaLabel='Assign roles' />

              {rbacType === 'tenant' && (
                <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-1)' }}>
                  {fieldLabel('Role')}
                  <Select
                    multiple
                    id='group-tenant-role'
                    placeholder='Select role(s)'
                    // Assigning a role to a group is privilege administration: every
                    // member inherits it, so it stays tenant-admin-only on both write
                    // paths (userroles_upsert_group and the customroles assignment
                    // actions are in non-grantable modules, and SyncUserRoles re-checks).
                    // A usergroups:Write holder can administer the group but not hand
                    // out authority through it — without this the picker looked usable
                    // and every save 403'd.
                    disabled={!canAssignGroupRoles}
                    instructionText={canAssignGroupRoles ? undefined : 'Only a tenant admin can assign roles to a group.'}
                    value={[...(groupRole ? [groupRole] : []), ...selectedCustomRoles]}
                    onChange={(next) => {
                      // One merged picker: the tenant built-in role (single — carries data
                      // scope, written to group_roles) plus custom roles (additive, written
                      // to custom_role_assignments). Enforce a single built-in.
                      const builtinSet = new Set(TENANT_ROLE_OPTIONS.map((o) => o.value));
                      const builtins = next.filter((v) => builtinSet.has(v));
                      const customs = next.filter((v) => !builtinSet.has(v));
                      setGroupRole(builtins.length ? builtins[builtins.length - 1] : '');
                      setSelectedCustomRoles(customs);
                    }}
                    options={[...TENANT_ROLE_OPTIONS, ...customRolesList.map((r) => ({ value: r.id, label: r.name }))]}
                    maxChips={3}
                    minWidth='100%'
                  />
                  <Box sx={{ font: "400 11.5px/1.4 'Roboto'", color: ds.gray[400] }}>
                    Tenant role grants access to every account; custom roles add extra action permissions.
                  </Box>
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
                        grouped
                        groupIcon={renderAccountGroupIcon}
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
                        options={[...ACCOUNT_ROLE_OPTIONS, ...customRolesList.map((r) => ({ value: r.id, label: r.name }))]}
                        onChange={(next) => setSelectedAccountRole(next)}
                        placeholder='Select role'
                        // Same rule as the tenant tab: binding a role to a group is
                        // privilege administration whatever the scope, and both write
                        // paths (userroles_upsert_account_group, the customroles
                        // account-assignment action) are non-grantable.
                        disabled={!canAssignGroupRoles}
                        instructionText={canAssignGroupRoles ? undefined : 'Only a tenant admin can assign roles to a group.'}
                        minWidth='100%'
                      />
                    </Box>
                    <Box sx={{ flexShrink: 0 }}>
                      <Button
                        type='button'
                        size='md'
                        onClick={() => {
                          const toAdd = selectedAccounts.filter(
                            (accountId) =>
                              !showSelectedAccounts.some((a) => a[0].drilldownQuery.id === accountId && a[1].roleValue === selectedAccountRole)
                          );
                          if (toAdd.length === 0) {
                            handleSnackBarData({
                              message: 'Selected account(s) already have this role assigned.',
                              severity: 'warning',
                            });
                            return;
                          }
                          // Only a built-in role overrides — it's one built-in per account. Custom
                          // roles are additive, so adding one never displaces anything.
                          const replacedCount = isBuiltinAccountRole(selectedAccountRole)
                            ? toAdd.filter((accountId) =>
                                showSelectedAccounts.some((a) => a[0].drilldownQuery.id === accountId && isBuiltinAccountRole(a[1].roleValue))
                              ).length
                            : 0;
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
                        disabled={!canAssignGroupRoles || selectedAccounts.length === 0 || !selectedAccountRole}
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
                        loading={loading}
                        showEmptyStateText={true}
                      />
                    </Box>
                  )}
                </Box>
              )}
            </Box>
          </Card>
        )}

        {/* Members section */}
        <Card
          variant='outlined'
          elevation='flat'
          header={
            <CardHeader
              title={selectedUsers.length > 0 ? `Members · ${selectedUsers.length}` : 'Members'}
              action={
                isEdit ? (
                  <SectionSave
                    dirty={membersDirty}
                    saving={savingSection === 'members'}
                    disabled={!!savingSection}
                    onSave={handleSaveMembers}
                    testId='save-group-members'
                  />
                ) : null
              }
            />
          }
        >
          <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-2)' }}>
            <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)' }}>
              <Box sx={{ flex: 1, minWidth: 0 }}>
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
                  placeholder={isEdit ? 'Add active user' : 'Select users'}
                  searchPlaceholder={isEdit ? 'Search active users…' : 'Search users…'}
                  minWidth='100%'
                  onChange={(usernames) => {
                    usernames.forEach((u) => handleUserSelection(u));
                  }}
                />
              </Box>
              {isEdit && (
                <ToggleGroup
                  selection='single'
                  options={MEMBER_FILTER_TABS}
                  value={userStatusFilter}
                  onChange={setUserStatusFilter}
                  size='md'
                  ariaLabel='Member filter'
                />
              )}
            </Box>
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
                  loading={loading}
                  showEmptyStateText={true}
                />
              </Box>
            )}
          </Box>
        </Card>
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
