import React from 'react';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import '@testing-library/jest-dom';

// Per-section save: each axis of a group (name/description, tenant roles,
// account roles, namespace roles, membership) writes through its own RPC. These
// tests pin the two properties that matter:
//   1. an untouched section issues NO request (editing the description must not
//      re-write role assignments), and
//   2. the Group Info save omits `role`, because usergroup_update overwrites
//      name/description unconditionally but treats role as a partial.

jest.mock('@lib/auth', () => ({
  canManage: () => true,
  // This suite exercises the custom-role picker and its per-section save, so it
  // runs with the CUSTOM_ROLES feature ON (with it off the modal skips the
  // custom-role fetch entirely — see the feature-switch tests in lib/auth).
  isCustomRolesEnabled: () => true,
}));

jest.mock('@lib/validation', () => ({
  textValidation: (value, _errors, setter) => setter({ groupname: value ? '' : 'Required' }),
}));

jest.mock('next-auth/react', () => ({
  useSession: () => ({ data: { user: { email: 'admin@example.com' } } }),
}));

jest.mock('@api1/user', () => ({
  __esModule: true,
  default: {
    listUsers: jest.fn(),
    listAccounts: jest.fn(),
    listK8sNamespaces: jest.fn(),
    listUserGroupUsers: jest.fn(),
    checkGroupNameExists: jest.fn(),
    updateUserGroup: jest.fn(),
    upsertGroupTenantRole: jest.fn(),
    upsertGroupAccountRoles: jest.fn(),
    upsertGroupAccountNamespaceRoles: jest.fn(),
    manageGroupUsers: jest.fn(),
    addUserGroup: jest.fn(),
  },
}));

jest.mock('@api1/roles', () => ({
  listCustomRoles: jest.fn(),
  updateRoleGroupAssignments: jest.fn(),
  updateRoleGroupAccountAssignments: jest.fn(),
}));

jest.mock('@utils/colors');
jest.mock('@assets', () => ({
  DeleteIconRed: { default: { src: '/del.svg' } },
  modalerror: { default: { src: '/err.svg' } },
  AWSIcon: { default: { src: '/aws.svg' } },
  AzureIcon: { default: { src: '/azure.svg' } },
  GCPIcon: { default: { src: '/gcp.svg' } },
  ouK8s: { default: { src: '/k8s.svg' } },
}));

jest.mock('@ui/Button', () => ({
  Button: ({ children, onClick, id, disabled, loading, 'data-testid': testId }) => (
    <button data-testid={testId || id || `btn-${children}`} onClick={onClick} disabled={disabled || loading}>
      {children}
    </button>
  ),
}));

jest.mock('@ui/Modal', () => ({
  Modal: ({ open, children, actionButtons }) =>
    open ? (
      <div data-testid='modal'>
        {children}
        {actionButtons}
      </div>
    ) : null,
}));

jest.mock('@ui/Card', () => ({
  Card: ({ header, children }) => (
    <div data-testid='card'>
      {header}
      {children}
    </div>
  ),
}));

jest.mock('@ui/Input', () => ({
  Input: ({ id, value, onChange }) => <input data-testid={`input-${id}`} value={value || ''} onChange={(e) => onChange(e.target.value)} />,
}));

jest.mock('@ui/Select', () => ({
  Select: ({ id, value, options, onChange, multiple }) => (
    <select
      data-testid={`select-${id}`}
      multiple={multiple}
      value={value}
      onChange={(e) => onChange(multiple ? [...e.target.selectedOptions].map((o) => o.value) : e.target.value)}
    >
      <option value='' />
      {(options || []).map((o) => (
        <option key={o.value} value={o.value}>
          {o.label}
        </option>
      ))}
    </select>
  ),
}));

jest.mock('@ui/Label', () => ({ Label: ({ text }) => <span>{text}</span> }));
jest.mock('@ui/ToggleGroup', () => ({ ToggleGroup: () => <div data-testid='toggle-group' /> }));
jest.mock('@shared/navigation/Tabs', () => ({
  __esModule: true,
  default: ({ options, onChange }) => (
    <div data-testid='rbac-tabs'>
      {options.tabOptions.map((t) => (
        <button key={t.value} data-testid={`tab-${t.value}`} onClick={() => onChange(t.value)}>
          {t.text}
        </button>
      ))}
    </div>
  ),
}));
jest.mock('@shared/icons/SafeIcon', () => ({ __esModule: true, default: ({ alt }) => <span>{alt}</span> }));
jest.mock('@shared/tables/CustomTable', () => ({
  __esModule: true,
  default: ({ tableData, id }) => <div data-testid={`table-${id}`}>{(tableData || []).length}</div>,
}));

import GroupModal from '@components/user-management/modal/GroupModal';

const api = require('@api1/user').default;
const roles = require('@api1/roles');

const GROUP = {
  id: 'g-1',
  name: 'Platform Eng',
  description: 'Owns the platform',
  group_roles: [
    { entity_type: 'tenant', entity_id: 't-1', role: 'tenant_admin' },
    { entity_type: 'account', entity_id: 'acc-1', role: 'account_admin' },
  ],
};

function renderModal() {
  return render(<GroupModal open groupData={GROUP} handleClose={jest.fn()} handleSnackBarData={jest.fn()} />);
}

beforeEach(() => {
  jest.clearAllMocks();
  api.listUsers.mockResolvedValue({ data: [{ username: 'bob@example.com', display_name: 'Bob', status: 'active' }] });
  api.listAccounts.mockResolvedValue([{ id: 'acc-1', account_name: 'AWS Prod', cloud_provider: 'AWS' }]);
  api.listK8sNamespaces.mockResolvedValue({ k8s_namespaces: { rows: [] } });
  api.listUserGroupUsers.mockResolvedValue({ data: { usergroup_users: [] } });
  api.checkGroupNameExists.mockResolvedValue({ data: [] });
  api.updateUserGroup.mockResolvedValue({ status: 'success' });
  roles.listCustomRoles.mockResolvedValue([{ id: 'role-1', name: 'Auditor', group_ids: ['g-1'], group_account_assignments: [] }]);
});

describe('GroupModal per-section save', () => {
  it('disables every section save while the form matches the server state', async () => {
    renderModal();
    await waitFor(() => expect(roles.listCustomRoles).toHaveBeenCalled());

    expect(screen.getByTestId('save-group-info')).toBeDisabled();
    expect(screen.getByTestId('save-tenant-roles')).toBeDisabled();
    expect(screen.getByTestId('save-group-members')).toBeDisabled();
  });

  it('saving a description change writes only the group row — no role or member RPCs', async () => {
    renderModal();
    await waitFor(() => expect(roles.listCustomRoles).toHaveBeenCalled());

    fireEvent.change(screen.getByTestId('input-description'), { target: { value: 'Owns platform + infra' } });

    const infoSave = screen.getByTestId('save-group-info');
    await waitFor(() => expect(infoSave).not.toBeDisabled());
    // A description edit must not make the other sections look dirty.
    expect(screen.getByTestId('save-tenant-roles')).toBeDisabled();
    expect(screen.getByTestId('save-group-members')).toBeDisabled();

    fireEvent.click(infoSave);
    await waitFor(() => expect(api.updateUserGroup).toHaveBeenCalledTimes(1));

    // `role` omitted: usergroup_update overwrites name/description unconditionally
    // but leaves a nil role alone, so a details-only save can't clear the grant.
    const payload = api.updateUserGroup.mock.calls[0][0];
    expect(payload).toEqual({ id: 'g-1', name: 'Platform Eng', description: 'Owns platform + infra' });
    expect(payload).not.toHaveProperty('role');

    expect(api.manageGroupUsers).not.toHaveBeenCalled();
    expect(api.upsertGroupTenantRole).not.toHaveBeenCalled();
    expect(api.upsertGroupAccountRoles).not.toHaveBeenCalled();
    expect(roles.updateRoleGroupAssignments).not.toHaveBeenCalled();
    expect(roles.updateRoleGroupAccountAssignments).not.toHaveBeenCalled();
  });

  it('clears the section back to clean after a successful save', async () => {
    renderModal();
    await waitFor(() => expect(roles.listCustomRoles).toHaveBeenCalled());

    fireEvent.change(screen.getByTestId('input-description'), { target: { value: 'Changed' } });
    const infoSave = screen.getByTestId('save-group-info');
    await waitFor(() => expect(infoSave).not.toBeDisabled());

    fireEvent.click(infoSave);
    await waitFor(() => expect(api.updateUserGroup).toHaveBeenCalled());
    // Baseline moves forward, so the section stops asking to be saved again.
    await waitFor(() => expect(screen.getByTestId('save-group-info')).toBeDisabled());
  });

  it('reports the upstream error message when a section save fails', async () => {
    const handleSnackBarData = jest.fn();
    api.updateUserGroup.mockRejectedValue(new Error('A group with this name already exists'));
    render(<GroupModal open groupData={GROUP} handleClose={jest.fn()} handleSnackBarData={handleSnackBarData} />);
    await waitFor(() => expect(roles.listCustomRoles).toHaveBeenCalled());

    fireEvent.change(screen.getByTestId('input-description'), { target: { value: 'Changed' } });
    fireEvent.click(screen.getByTestId('save-group-info'));

    await waitFor(() =>
      expect(handleSnackBarData).toHaveBeenCalledWith(
        expect.objectContaining({ message: 'A group with this name already exists', severity: 'error' })
      )
    );
  });

  it('does not re-check name uniqueness against the group’s own renamed value', async () => {
    // After a rename, the group itself holds the new name. Comparing the next
    // save against the stale groupData.name would run the uniqueness query for
    // that new name, find the group we just renamed, and block every later save.
    let persistedName = 'Platform Eng';
    api.checkGroupNameExists.mockImplementation((name) => Promise.resolve({ data: name === persistedName ? [{ id: 'g-1', name }] : [] }));
    api.updateUserGroup.mockImplementation(({ name }) => {
      persistedName = name;
      return Promise.resolve({ status: 'success' });
    });
    renderModal();
    await waitFor(() => expect(roles.listCustomRoles).toHaveBeenCalled());

    // Rename and save.
    fireEvent.change(screen.getByTestId('input-groupname'), { target: { value: 'Beta Team' } });
    fireEvent.click(screen.getByTestId('save-group-info'));
    await waitFor(() => expect(api.updateUserGroup).toHaveBeenCalledTimes(1));

    // Edit the description and save again — the name check must be skipped since
    // the name still matches the (updated) baseline, so this save must go through.
    fireEvent.change(screen.getByTestId('input-description'), { target: { value: 'v2' } });
    await waitFor(() => expect(screen.getByTestId('save-group-info')).not.toBeDisabled());
    fireEvent.click(screen.getByTestId('save-group-info'));
    await waitFor(() => expect(api.updateUserGroup).toHaveBeenCalledTimes(2));
    expect(api.updateUserGroup.mock.calls[1][0]).toEqual({ id: 'g-1', name: 'Beta Team', description: 'v2' });
  });

  it('does not mark the account section dirty for a binding on an unlistable account', async () => {
    // acc-9 holds a role but is absent from the active-accounts list (deactivated).
    // It can't render, so a naive dirty-check would show the section as edited and
    // a replace-all save would drop the binding. It must read clean instead.
    const group = {
      ...GROUP,
      group_roles: [
        { entity_type: 'account', entity_id: 'acc-1', role: 'account_admin' },
        { entity_type: 'account', entity_id: 'acc-9', role: 'account_admin' },
      ],
    };
    render(<GroupModal open groupData={group} handleClose={jest.fn()} handleSnackBarData={jest.fn()} />);
    await waitFor(() => expect(roles.listCustomRoles).toHaveBeenCalled());
    fireEvent.click(screen.getByTestId('tab-account'));

    // Wait until accounts have loaded and the resolvable row (acc-1) has hydrated
    // (the table only renders on the account tab), so the disabled assertion
    // reflects the not-dirty state rather than the pre-load gate.
    await waitFor(() => expect(screen.getByTestId('table-selected-accounts')).toHaveTextContent('1'));
    expect(screen.getByTestId('save-account-roles')).toBeDisabled();
  });
});
