import React from 'react';
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react';
import '@testing-library/jest-dom';

const mockHasWriteAccess = jest.fn();
jest.mock('@lib/auth', () => ({
  hasWriteAccess: (...args) => mockHasWriteAccess(...args),
}));

jest.mock('@api1/user', () => ({
  __esModule: true,
  default: {
    listAccounts: jest.fn(),
    listUserGroups: jest.fn(),
    getUserPreferencesTablePageSize: jest.fn(() => 10),
  },
}));

jest.mock('@components1/common/snackbarService', () => ({
  snackbar: { success: jest.fn(), error: jest.fn() },
}));

jest.mock('@assets', () => ({
  writeIcon: { default: { src: '/write-icon.svg' } },
}));

jest.mock('src/utils/colors', () => ({
  colors: { text: { primary: '#000' }, background: { white: '#fff' } },
}));

jest.mock('src/utils/actionStyles', () => ({
  action: { primary: {} },
}));

jest.mock('src/utils/common', () => ({
  safeJSONParse: (val) => {
    if (typeof val !== 'string') {
      return val;
    }
    try {
      return JSON.parse(val);
    } catch {
      return null;
    }
  },
  snakeToTitleCase: (s) => s,
}));

jest.mock('@components1/common', () => ({
  Text: ({ value }) => <span>{value}</span>,
}));

jest.mock('@components1/common/format/Datetime', () => ({
  __esModule: true,
  default: ({ value }) => <span data-testid='datetime'>{value || '—'}</span>,
}));

jest.mock('@components1/common/NewCustomButton', () => ({
  __esModule: true,
  default: ({ id, text, onClick }) => (
    <button data-testid={`btn-${id || text}`} onClick={onClick}>
      {text}
    </button>
  ),
}));

jest.mock('./../../../src/components1/user-management/modal/GroupModal', () => ({
  __esModule: true,
  default: ({ open, handleClose, groupData, handleSnackBarData }) =>
    open ? (
      <div data-testid='group-modal'>
        <div data-testid='group-modal-mode'>{groupData ? 'edit' : 'add'}</div>
        <div data-testid='group-modal-name'>{groupData?.name || ''}</div>
        <button data-testid='group-modal-close' onClick={() => handleClose(false)}>
          Close
        </button>
        <button data-testid='group-modal-save' onClick={() => handleClose(true)}>
          Save
        </button>
        <button data-testid='group-modal-snackbar-success' onClick={() => handleSnackBarData({ severity: 'success', message: 'Saved!' })}>
          Snack OK
        </button>
        <button data-testid='group-modal-snackbar-error' onClick={() => handleSnackBarData({ severity: 'error', message: 'Oops' })}>
          Snack Err
        </button>
      </div>
    ) : null,
}));

jest.mock('./../../../src/components1/user-management/UserGroupUsers', () => ({
  __esModule: true,
  default: ({ groupId }) => <div data-testid='user-group-users'>{groupId || ''}</div>,
}));

jest.mock('@components1/common/BoxLayout2', () => ({
  __esModule: true,
  default: ({ children, searchOption, extraOptions = [] }) => (
    <div data-testid='box-layout'>
      <div data-testid='extras'>{extraOptions}</div>
      {searchOption?.enabled && (
        <input
          data-testid='search-input'
          value={searchOption.value || ''}
          onChange={searchOption.onChange}
          onKeyDown={(e) => {
            if (e.key === 'Enter' && searchOption.onEnter) {
              searchOption.onEnter();
            }
          }}
        />
      )}
      {searchOption?.enabled === false && <div data-testid='search-disabled'>disabled</div>}
      {children}
    </div>
  ),
}));

jest.mock('@components1/common/tables/CustomTable2', () => ({
  __esModule: true,
  default: ({ tableData, totalRows, loading, pageNumber, onPageChange, expandable, headers }) => (
    <div data-testid='custom-table'>
      {loading && <div data-testid='loading'>loading</div>}
      <div data-testid='total'>{totalRows}</div>
      <div data-testid='page'>{pageNumber}</div>
      <div data-testid='headers'>{headers.map((h) => h.name).join('|')}</div>
      <div data-testid='expandable-tabs'>{(expandable?.tabs || []).map((t) => t.text).join('|')}</div>
      {(tableData || []).map((row, i) => (
        <div key={i} data-testid={`row-${i}`}>
          {row.map((cell, j) => (
            <span key={j} data-testid={`cell-${i}-${j}`}>
              {cell.component}
            </span>
          ))}
          {i === 0 && expandable?.tabs?.[0] && <div data-testid={`row-${i}-users`}>{expandable.tabs[0].componentFn({}, row[0]?.drilldownQuery)}</div>}
        </div>
      ))}
      <button data-testid='next-page' onClick={() => onPageChange(2, 10)}>
        Next
      </button>
    </div>
  ),
}));

import UserGroup from '@components1/user-management/UserGroup';

const apiUserManagement = require('@api1/user').default;
const { snackbar } = require('@components1/common/snackbarService');

const sampleAccounts = [
  { id: 'acc-1', account_name: 'AWS Prod' },
  { id: 'acc-2', account_name: 'GCP Dev' },
];

const sampleGroups = [
  {
    id: 'g-1',
    name: 'Admins',
    description: 'Cluster admins',
    owner_display_name: 'Alice',
    member_count: 5,
    created_at: '2026-05-15T10:00:00Z',
    group_roles: JSON.stringify([
      { entity_type: 'tenant', entity_id: 't-1', role: 'tenant_admin' },
      { entity_type: 'account', entity_id: 'acc-1', role: 'account_admin' },
      { entity_type: 'k8s_namespace', entity_id: 'acc-1:prod', role: 'k8s_namespace_admin' },
    ]),
  },
  {
    id: 'g-2',
    name: 'Viewers',
    description: 'Read-only',
    owner_display_name: 'Bob',
    member_count: 12,
    created_at: '2026-05-15T11:00:00Z',
    group_roles: JSON.stringify([{ entity_type: 'tenant', entity_id: 't-1', role: 'tenant_admin_readonly' }]),
  },
];

const mockGroupsResponse = (rows = sampleGroups, count = rows.length) => ({
  data: {
    usergroups_list: { rows },
    admin_get_user_groups_grouping_v2: { rows: [{ count }] },
  },
});

describe('UserGroup (integration)', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    mockHasWriteAccess.mockReturnValue(true);
    apiUserManagement.listAccounts.mockResolvedValue(sampleAccounts);
    apiUserManagement.listUserGroups.mockResolvedValue(mockGroupsResponse());
  });

  it('fetches accounts + user groups on mount', async () => {
    render(<UserGroup />);

    await waitFor(() => {
      expect(apiUserManagement.listAccounts).toHaveBeenCalled();
      expect(apiUserManagement.listUserGroups).toHaveBeenCalled();
    });
    const call = apiUserManagement.listUserGroups.mock.calls[0][0];
    expect(call).toMatchObject({
      offset: 0,
      limit: 10,
      nameSearch: '',
    });
  });

  it('passes groupNames array to nameSearch param when provided', async () => {
    render(<UserGroup groupNames={['Admins', 'Viewers']} />);

    await waitFor(() => expect(apiUserManagement.listUserGroups).toHaveBeenCalled());
    expect(apiUserManagement.listUserGroups.mock.calls[0][0].nameSearch).toEqual(['Admins', 'Viewers']);
  });

  it('renders rows with group name + member count + description + owner', async () => {
    render(<UserGroup />);

    await waitFor(() => expect(screen.getByText('Admins')).toBeInTheDocument());
    expect(screen.getByText('Viewers')).toBeInTheDocument();
    expect(screen.getByText('Cluster admins')).toBeInTheDocument();
    expect(screen.getByText('Read-only')).toBeInTheDocument();
    expect(screen.getByText('Alice')).toBeInTheDocument();
    expect(screen.getByText('Bob')).toBeInTheDocument();
    expect(screen.getByText('5')).toBeInTheDocument();
    expect(screen.getByText('12')).toBeInTheDocument();
  });

  it('renders headers with Group Name | Total Members | Description | Owner | Roles', async () => {
    render(<UserGroup />);
    await waitFor(() => expect(screen.getByTestId('headers')).toHaveTextContent('Group Name|Total Members|Description|Owner|Roles|Created At|'));
  });

  it('renders permission lists with account name resolved from accounts map', async () => {
    render(<UserGroup />);

    // Wait for both fetches + permission re-render to complete
    await waitFor(() => expect(screen.getAllByText('Namespace Permission').length).toBeGreaterThan(0));
    expect(screen.getAllByText('Account Permission').length).toBeGreaterThan(0);
    expect(screen.getAllByText('Tenant Permission').length).toBeGreaterThan(0);
    // account name resolved
    expect(screen.getAllByText('Account: AWS Prod').length).toBeGreaterThan(0);
    expect(screen.getAllByText('Namespace: prod').length).toBeGreaterThan(0);
  });

  it('shows search input when no groupNames prop', async () => {
    render(<UserGroup />);
    await waitFor(() => expect(screen.getByTestId('search-input')).toBeInTheDocument());
  });

  it('disables search when groupNames prop provided', async () => {
    render(<UserGroup groupNames={['Admins']} />);
    await waitFor(() => expect(screen.getByTestId('search-disabled')).toBeInTheDocument());
    expect(screen.queryByTestId('search-input')).not.toBeInTheDocument();
  });

  it('shows Add User Group button with write access and no groupNames', async () => {
    render(<UserGroup />);
    await waitFor(() => expect(screen.getByTestId('btn-new-user-group')).toBeInTheDocument());
  });

  it('hides Add User Group button when groupNames provided (drilldown mode)', async () => {
    render(<UserGroup groupNames={['Admins']} />);
    await waitFor(() => expect(apiUserManagement.listUserGroups).toHaveBeenCalled());
    expect(screen.queryByTestId('btn-new-user-group')).not.toBeInTheDocument();
  });

  it('hides Add User Group button without write access', async () => {
    mockHasWriteAccess.mockReturnValue(false);
    render(<UserGroup />);
    await waitFor(() => expect(apiUserManagement.listUserGroups).toHaveBeenCalled());
    expect(screen.queryByTestId('btn-new-user-group')).not.toBeInTheDocument();
  });

  it('opens Add group modal on Add button click', async () => {
    render(<UserGroup />);
    await waitFor(() => expect(screen.getByTestId('btn-new-user-group')).toBeInTheDocument());

    fireEvent.click(screen.getByTestId('btn-new-user-group'));

    expect(screen.getByTestId('group-modal')).toBeInTheDocument();
    expect(screen.getByTestId('group-modal-mode')).toHaveTextContent('add');
  });

  it('closes modal without refetch when no update', async () => {
    render(<UserGroup />);
    await waitFor(() => expect(screen.getByTestId('btn-new-user-group')).toBeInTheDocument());
    fireEvent.click(screen.getByTestId('btn-new-user-group'));
    apiUserManagement.listUserGroups.mockClear();

    fireEvent.click(screen.getByTestId('group-modal-close'));

    expect(screen.queryByTestId('group-modal')).not.toBeInTheDocument();
    expect(apiUserManagement.listUserGroups).not.toHaveBeenCalled();
  });

  it('refetches user groups after modal Save', async () => {
    render(<UserGroup />);
    await waitFor(() => expect(screen.getByTestId('btn-new-user-group')).toBeInTheDocument());
    fireEvent.click(screen.getByTestId('btn-new-user-group'));
    apiUserManagement.listUserGroups.mockClear();

    fireEvent.click(screen.getByTestId('group-modal-save'));

    await waitFor(() => expect(apiUserManagement.listUserGroups).toHaveBeenCalled());
  });

  it('snackbar success on modal snack-success event', async () => {
    render(<UserGroup />);
    await waitFor(() => expect(screen.getByTestId('btn-new-user-group')).toBeInTheDocument());
    fireEvent.click(screen.getByTestId('btn-new-user-group'));

    fireEvent.click(screen.getByTestId('group-modal-snackbar-success'));

    expect(snackbar.success).toHaveBeenCalledWith('Saved!');
  });

  it('snackbar error on modal snack-error event', async () => {
    render(<UserGroup />);
    await waitFor(() => expect(screen.getByTestId('btn-new-user-group')).toBeInTheDocument());
    fireEvent.click(screen.getByTestId('btn-new-user-group'));

    fireEvent.click(screen.getByTestId('group-modal-snackbar-error'));

    expect(snackbar.error).toHaveBeenCalledWith('Oops');
  });

  it('refetches with nameSearch on Enter key', async () => {
    render(<UserGroup />);
    await waitFor(() => expect(apiUserManagement.listUserGroups).toHaveBeenCalled());
    apiUserManagement.listUserGroups.mockClear();

    const input = screen.getByTestId('search-input');
    fireEvent.change(input, { target: { value: 'admin' } });
    fireEvent.keyDown(input, { key: 'Enter' });

    await waitFor(() => expect(apiUserManagement.listUserGroups).toHaveBeenCalled());
    expect(apiUserManagement.listUserGroups.mock.calls[0][0].nameSearch).toBe('admin');
  });

  it('paginates and updates offset on next page', async () => {
    render(<UserGroup />);
    await waitFor(() => expect(apiUserManagement.listUserGroups).toHaveBeenCalled());
    apiUserManagement.listUserGroups.mockClear();

    fireEvent.click(screen.getByTestId('next-page'));

    await waitFor(() => expect(apiUserManagement.listUserGroups).toHaveBeenCalled());
    expect(apiUserManagement.listUserGroups.mock.calls[0][0].offset).toBe(10);
  });

  it('expandable Users tab renders UserGroupUsers with group_id', async () => {
    render(<UserGroup />);

    await waitFor(() => expect(screen.getByTestId('row-0-users')).toBeInTheDocument());
    expect(screen.getByTestId('user-group-users')).toHaveTextContent('g-1');
  });

  it('shows loading state during fetch', async () => {
    let resolveFn;
    apiUserManagement.listUserGroups.mockReturnValueOnce(
      new Promise((resolve) => {
        resolveFn = resolve;
      })
    );

    render(<UserGroup />);
    await waitFor(() => expect(screen.getByTestId('loading')).toBeInTheDocument());

    await act(async () => {
      resolveFn(mockGroupsResponse([]));
    });

    await waitFor(() => expect(screen.queryByTestId('loading')).not.toBeInTheDocument());
  });

  it('handles empty list gracefully', async () => {
    apiUserManagement.listUserGroups.mockResolvedValue(mockGroupsResponse([], 0));

    render(<UserGroup />);

    await waitFor(() => expect(screen.getByTestId('total')).toHaveTextContent('0'));
    expect(screen.queryByTestId('row-0')).not.toBeInTheDocument();
  });
});
