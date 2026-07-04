import React from 'react';
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react';
import '@testing-library/jest-dom';

const mockUseSession = jest.fn();
jest.mock('next-auth/react', () => ({
  useSession: (...args) => mockUseSession(...args),
}));

const mockHasWriteAccess = jest.fn();
jest.mock('@lib/auth', () => ({
  hasWriteAccess: (...args) => mockHasWriteAccess(...args),
}));

jest.mock('@api1/user', () => ({
  __esModule: true,
  default: {
    getAllStatuses: jest.fn(),
    getUserPreferencesTablePageSize: jest.fn(() => 10),
  },
}));

jest.mock('@lib/UserService', () => ({
  getUsersByTenant: jest.fn(),
}));

jest.mock('@ui/Toast', () => ({
  toast: { success: jest.fn(), error: jest.fn() },
}));

jest.mock('@assets', () => ({
  writeIcon: { default: { src: '/write-icon.svg' } },
}));

jest.mock('@utils/colors');

jest.mock('src/utils/actionStyles', () => ({
  action: { primary: {} },
}));

jest.mock('src/utils/common', () => ({
  safeJSONParse: (val) => {
    if (typeof val !== 'string') return val;
    try {
      return JSON.parse(val);
    } catch {
      return null;
    }
  },
  snakeToTitleCase: (s) => s,
}));

jest.mock('@shared/format/Text', () => ({
  __esModule: true,
  default: ({ value }) => <span>{value}</span>,
}));

jest.mock('@shared/format/Datetime', () => ({
  __esModule: true,
  default: ({ value }) => <span data-testid='datetime'>{value || '-'}</span>,
}));

jest.mock('@ui/Label', () => {
  const TONE_TO_COLOR = { success: 'green', critical: 'red', warning: 'yellow', neutral: 'grey' };
  return {
    Label: ({ text, children, tone, variant }) => {
      const color = variant || TONE_TO_COLOR[tone] || 'plain';
      return <span data-testid={`label-${color}`}>{children || text}</span>;
    },
  };
});

jest.mock('@shared/icons/SafeIcon', () => ({
  __esModule: true,
  default: ({ alt }) => <span data-testid={`icon-${alt}`}>icon</span>,
}));

jest.mock('@ui/Button', () => ({
  Button: ({ children, onClick, id, disabled, loading }) => (
    <button data-testid={id || `btn-${children}`} onClick={onClick} disabled={disabled || loading}>
      {children}
    </button>
  ),
}));

jest.mock('@ui/ListingLayout', () => {
  const ListingLayout = ({ children, id }) => (
    <div data-testid='listing-layout' id={id}>
      {children}
    </div>
  );
  ListingLayout.Toolbar = ({ children, actions }) => (
    <div data-testid='toolbar'>
      <div data-testid='toolbar-actions'>{actions}</div>
      {children}
    </div>
  );
  ListingLayout.Body = ({ children }) => <div data-testid='body'>{children}</div>;
  return { __esModule: true, ListingLayout, default: ListingLayout };
});

jest.mock('@ui/FilterDropdown', () => ({
  __esModule: true,
  default: ({ label, options = [], value, onSelect }) => {
    const currentValue = typeof value === 'object' && value !== null ? value.value : value;
    return (
      <select
        data-testid={`filter-${label}`}
        value={currentValue || ''}
        onChange={(e) => onSelect?.({ target: { value: e.target.value } }, { value: e.target.value, label: e.target.value })}
      >
        <option value=''>--</option>
        {(options || []).map((opt, idx) => {
          const v = typeof opt === 'string' ? opt : opt.value;
          const l = typeof opt === 'string' ? opt : opt.label;
          return (
            <option key={(v || '_') + '-' + idx} value={v}>
              {l}
            </option>
          );
        })}
      </select>
    );
  },
}));

jest.mock('@ui/SearchInput', () => ({
  __esModule: true,
  default: ({ label, value, onChange, onEnterPress }) => (
    <input
      data-testid={`search-${label}`}
      value={value || ''}
      onChange={(e) => onChange?.(e.target.value)}
      onKeyDown={(e) => {
        if (e.key === 'Enter' && onEnterPress) onEnterPress();
      }}
    />
  ),
}));

jest.mock('@shared/tables/CustomTable', () => ({
  __esModule: true,
  default: ({ tableData, loading, totalRows, onSortChange, sort, pageNumber }) => (
    <div data-testid='table'>
      {loading && <div data-testid='loading'>loading</div>}
      <div data-testid='total'>{totalRows}</div>
      <div data-testid='page'>{pageNumber}</div>
      <div data-testid='sort'>
        {sort?.name}-{sort?.order}
      </div>
      <button data-testid='sort-email' onClick={() => onSortChange({ name: 'Email', order: 'desc' })}>
        Sort Email
      </button>
      {(tableData || []).map((row, i) => (
        <div key={i} data-testid={`row-${i}`}>
          {row.map((cell, j) => (
            <span key={j} data-testid={`cell-${i}-${j}`}>
              {cell.component}
            </span>
          ))}
        </div>
      ))}
    </div>
  ),
}));

jest.mock('@components/user-management/modal/UserModal', () => ({
  __esModule: true,
  default: ({ open, handleClose, mode, userData }) =>
    open ? (
      <div data-testid={`user-modal-${mode}`}>
        <span data-testid='modal-user-email'>{userData?.username || ''}</span>
        <button data-testid={`close-modal-${mode}`} onClick={() => handleClose(false)}>
          Close
        </button>
        <button data-testid={`save-modal-${mode}`} onClick={() => handleClose(true)}>
          Save
        </button>
      </div>
    ) : null,
}));

jest.mock('@components/user-management/UserGroup', () => ({
  __esModule: true,
  default: () => <div data-testid='user-group'>user-group</div>,
}));

import AllUsers from '@components/user-management/AllUsers';

const apiUserManagement = require('@api1/user').default;
const { getUsersByTenant } = require('@lib/UserService');

const sampleUsers = [
  {
    username: 'alice@example.com',
    display_name: 'Alice',
    status: 'active',
    last_accessed_at: '2026-05-15T10:00:00Z',
    user_groups: JSON.stringify([{ name: 'Admins' }]),
    user_roles: JSON.stringify([{ role_display_name: 'Tenant Admin', role: 'tenant_admin' }]),
  },
  {
    username: 'bob@example.com',
    display_name: 'Bob',
    status: 'inactive',
    last_accessed_at: null,
    user_groups: JSON.stringify([]),
    user_roles: JSON.stringify([{ role: 'readonly' }]),
  },
];

const mockUsersResponse = (rows = sampleUsers, count = rows.length) => ({
  users_list_by_tenant: { rows },
  // Source reads `users_aggregate_by_tenant.rows[0].count` for the total — old key
  // `admin_get_users_grouping_by_tenant_v2` was renamed.
  users_aggregate_by_tenant: { rows: [{ count }] },
});

describe('AllUsers (integration)', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    mockUseSession.mockReturnValue({ data: { user: { email: 'me@example.com' } } });
    mockHasWriteAccess.mockReturnValue(true);
    apiUserManagement.getAllStatuses.mockResolvedValue({
      data: { user_status_type: [{ value: 'active' }, { value: 'inactive' }] },
    });
    getUsersByTenant.mockResolvedValue(mockUsersResponse());
  });

  it('fetches statuses and users on mount with default filters', async () => {
    render(<AllUsers />);

    await waitFor(() => {
      expect(apiUserManagement.getAllStatuses).toHaveBeenCalledTimes(1);
      expect(getUsersByTenant).toHaveBeenCalled();
    });

    const firstCall = getUsersByTenant.mock.calls[0][0];
    expect(firstCall).toMatchObject({
      offset: 0,
      limit: 10,
      sortOrder: 'asc',
      sortCol: 'display_name',
      nameSearch: '',
      statusSearch: 'active',
    });
  });

  it('renders user rows with parsed groups and roles', async () => {
    render(<AllUsers />);

    await waitFor(() => expect(screen.getByText('Alice')).toBeInTheDocument());
    expect(screen.getByText('Bob')).toBeInTheDocument();
    expect(screen.getByText('Tenant Admin')).toBeInTheDocument();
    expect(screen.getByText('readonly')).toBeInTheDocument();
    expect(screen.getByText('Admins')).toBeInTheDocument();
  });

  it('populates status dropdown from getAllStatuses', async () => {
    render(<AllUsers />);

    await waitFor(() => expect(screen.getByTestId('filter-Status')).toBeInTheDocument());
    expect(screen.getByRole('option', { name: 'Active' })).toBeInTheDocument();
    expect(screen.getByRole('option', { name: 'Inactive' })).toBeInTheDocument();
  });

  it('refetches with new statusSearch when status filter changes', async () => {
    render(<AllUsers />);
    await waitFor(() => expect(getUsersByTenant).toHaveBeenCalled());
    getUsersByTenant.mockClear();

    fireEvent.change(screen.getByTestId('filter-Status'), { target: { value: 'inactive' } });

    await waitFor(() => expect(getUsersByTenant).toHaveBeenCalled());
    const call = getUsersByTenant.mock.calls[0][0];
    expect(call.statusSearch).toBe('inactive');
    expect(call.offset).toBe(0);
  });

  it('updates sortCol mapping when sort changes to Email', async () => {
    render(<AllUsers />);
    await waitFor(() => expect(getUsersByTenant).toHaveBeenCalled());
    getUsersByTenant.mockClear();

    fireEvent.click(screen.getByTestId('sort-email'));

    await waitFor(() => expect(getUsersByTenant).toHaveBeenCalled());
    const call = getUsersByTenant.mock.calls[0][0];
    expect(call.sortCol).toBe('username');
    expect(call.sortOrder).toBe('desc');
  });

  it('shows Add New User button only when user has write access', async () => {
    mockHasWriteAccess.mockReturnValue(false);
    const { rerender } = render(<AllUsers />);
    await waitFor(() => expect(getUsersByTenant).toHaveBeenCalled());
    expect(screen.queryByTestId('new-user')).not.toBeInTheDocument();

    mockHasWriteAccess.mockReturnValue(true);
    rerender(<AllUsers key='re' />);
    await waitFor(() => expect(screen.getByTestId('new-user')).toBeInTheDocument());
  });

  it('opens Add User modal when Add button clicked', async () => {
    render(<AllUsers />);
    await waitFor(() => expect(screen.getByTestId('new-user')).toBeInTheDocument());

    fireEvent.click(screen.getByTestId('new-user'));

    expect(screen.getByTestId('user-modal-add')).toBeInTheDocument();
  });

  it('closes Add modal without refetching when no update', async () => {
    render(<AllUsers />);
    await waitFor(() => expect(getUsersByTenant).toHaveBeenCalled());
    fireEvent.click(screen.getByTestId('new-user'));
    getUsersByTenant.mockClear();

    fireEvent.click(screen.getByTestId('close-modal-add'));

    expect(screen.queryByTestId('user-modal-add')).not.toBeInTheDocument();
    expect(getUsersByTenant).not.toHaveBeenCalled();
  });

  it('refetches after Add modal saves successfully', async () => {
    render(<AllUsers />);
    await waitFor(() => expect(getUsersByTenant).toHaveBeenCalled());
    fireEvent.click(screen.getByTestId('new-user'));
    getUsersByTenant.mockClear();

    fireEvent.click(screen.getByTestId('save-modal-add'));

    await waitFor(() => expect(getUsersByTenant).toHaveBeenCalledTimes(1));
  });

  it('searches by name on Enter key', async () => {
    render(<AllUsers />);
    await waitFor(() => expect(getUsersByTenant).toHaveBeenCalled());
    getUsersByTenant.mockClear();

    const search = screen.getByTestId('search-Enter Name');
    fireEvent.change(search, { target: { value: 'alice' } });
    fireEvent.keyDown(search, { key: 'Enter' });

    await waitFor(() => expect(getUsersByTenant).toHaveBeenCalled());
    const call = getUsersByTenant.mock.calls[0][0];
    expect(call.nameSearch).toBe('alice');
  });

  it('clears table and shows loading during fetch', async () => {
    let resolveFn;
    getUsersByTenant.mockReturnValueOnce(
      new Promise((resolve) => {
        resolveFn = resolve;
      })
    );

    render(<AllUsers />);
    expect(screen.getByTestId('loading')).toBeInTheDocument();
    expect(screen.getByTestId('total')).toHaveTextContent('0');

    await act(async () => {
      resolveFn(mockUsersResponse());
    });

    await waitFor(() => expect(screen.queryByTestId('loading')).not.toBeInTheDocument());
    expect(screen.getByTestId('total')).toHaveTextContent('2');
  });

  it('handles empty users response gracefully', async () => {
    getUsersByTenant.mockResolvedValue(mockUsersResponse([], 0));
    render(<AllUsers />);

    await waitFor(() => expect(screen.getByTestId('total')).toHaveTextContent('0'));
    expect(screen.queryByTestId('row-0')).not.toBeInTheDocument();
  });
});
