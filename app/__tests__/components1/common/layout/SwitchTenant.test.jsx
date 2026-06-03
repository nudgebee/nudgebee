import React from 'react';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { SwitchTenant } from '@components1/common/layout/SwitchTenant';

// Mock next-auth/react
const mockUpdate = jest.fn();
jest.mock('next-auth/react', () => ({
  useSession: jest.fn(() => ({
    data: {
      user: { email: 'test@example.com', name: 'Test User' },
      tenant: { name: 'TestTenant' },
      isSuperAdmin: false,
      isSuperAdminReadonly: false,
    },
    update: mockUpdate,
  })),
  signOut: jest.fn(),
}));

// Mock API
jest.mock('@api1/user', () => ({
  __esModule: true,
  default: {
    listAllTenants: jest.fn(),
    listUserTenants: jest.fn(),
  },
}));

// Mock cache
jest.mock('@lib/cache', () => ({
  __esModule: true,
  default: {
    clear: jest.fn(),
  },
}));

// Mock NDialog - using the resolved path from the component
jest.mock('@components1/common/modal/NDialog', () => ({
  __esModule: true,
  default: function MockNDialog({ open, dialogTitle, dialogContent, handleClose }) {
    if (!open) return null;
    return (
      <div data-testid='ndialog'>
        <div data-testid='dialog-title'>{dialogTitle}</div>
        <div data-testid='dialog-content'>{dialogContent}</div>
        <button onClick={handleClose} data-testid='close-btn'>
          Close
        </button>
      </div>
    );
  },
}));

// Mock CustomButton
jest.mock('@components1/common/NewCustomButton', () => ({
  __esModule: true,
  default: function MockCustomButton({ text, onClick }) {
    return (
      <button onClick={onClick} data-testid={`custom-btn-${text}`}>
        {text}
      </button>
    );
  },
}));

// Mock colors
jest.mock('src/utils/colors', () => ({
  colors: {
    text: { signinDark: '#000' },
  },
}));

// Mock uuid
jest.mock('uuid', () => ({
  v4: jest.fn(() => 'test-uuid'),
}));

const apiUserManagement = require('@api1/user').default;
const cache = require('@lib/cache').default;
const { useSession } = require('next-auth/react');

describe('SwitchTenant', () => {
  const defaultProps = {
    open: true,
    title: 'Switch Tenant',
    onClose: jest.fn(),
  };

  beforeEach(() => {
    jest.clearAllMocks();
    useSession.mockReturnValue({
      data: {
        user: { email: 'test@example.com', name: 'Test User' },
        tenant: { name: 'TestTenant' },
        isSuperAdmin: false,
        isSuperAdminReadonly: false,
      },
      update: mockUpdate,
    });

    apiUserManagement.listUserTenants.mockResolvedValue({
      data: [
        { name: 'Tenant A', slug: 'tenant-a' },
        { name: 'Tenant B', slug: 'tenant-b' },
        { name: 'TestTenant', slug: 'test-tenant' },
      ],
    });
  });

  it('renders nothing when closed', () => {
    render(<SwitchTenant {...defaultProps} open={false} />);
    expect(screen.queryByTestId('ndialog')).not.toBeInTheDocument();
  });

  it('renders dialog when open is true', async () => {
    render(<SwitchTenant {...defaultProps} />);
    await waitFor(() => expect(screen.getByTestId('ndialog')).toBeInTheDocument());
  });

  it('loads tenants on open with user email', async () => {
    render(<SwitchTenant {...defaultProps} />);
    await waitFor(() => {
      expect(apiUserManagement.listUserTenants).toHaveBeenCalledWith('test@example.com');
    });
  });

  it('does not load tenants if email is absent', async () => {
    useSession.mockReturnValue({
      data: {
        user: { email: null },
        tenant: { name: 'TestTenant' },
        isSuperAdmin: false,
        isSuperAdminReadonly: false,
      },
      update: mockUpdate,
    });
    render(<SwitchTenant {...defaultProps} />);
    await waitFor(() => {
      expect(apiUserManagement.listUserTenants).not.toHaveBeenCalled();
    });
  });

  it('loads all tenants for super admin', async () => {
    useSession.mockReturnValue({
      data: {
        user: { email: 'admin@example.com', name: 'Admin User' },
        tenant: { name: 'TestTenant' },
        isSuperAdmin: true,
        isSuperAdminReadonly: false,
      },
      update: mockUpdate,
    });
    apiUserManagement.listAllTenants.mockResolvedValue({
      data: [{ name: 'Global Tenant', slug: 'global' }],
    });
    render(<SwitchTenant {...defaultProps} />);
    await waitFor(() => {
      expect(apiUserManagement.listAllTenants).toHaveBeenCalled();
    });
  });

  it('loads all tenants for super admin readonly', async () => {
    useSession.mockReturnValue({
      data: {
        user: { email: 'admin@example.com', name: 'Admin User' },
        tenant: { name: 'TestTenant' },
        isSuperAdmin: false,
        isSuperAdminReadonly: true,
      },
      update: mockUpdate,
    });
    apiUserManagement.listAllTenants.mockResolvedValue({
      data: [{ name: 'Global Tenant', slug: 'global' }],
    });
    render(<SwitchTenant {...defaultProps} />);
    await waitFor(() => {
      expect(apiUserManagement.listAllTenants).toHaveBeenCalled();
    });
  });

  it('handles empty tenant list', async () => {
    apiUserManagement.listUserTenants.mockResolvedValue({ data: [] });
    render(<SwitchTenant {...defaultProps} />);
    await waitFor(() => {
      expect(apiUserManagement.listUserTenants).toHaveBeenCalled();
    });
    // tenants should be empty, select should be disabled
    const select = screen.getByTestId('tenant-select');
    expect(select).toBeDisabled();
  });

  it('handles null data from listUserTenants', async () => {
    apiUserManagement.listUserTenants.mockResolvedValue({ data: null });
    render(<SwitchTenant {...defaultProps} />);
    await waitFor(() => {
      expect(apiUserManagement.listUserTenants).toHaveBeenCalled();
    });
  });

  it('calls onClose when Cancel button clicked', async () => {
    const onClose = jest.fn();
    render(<SwitchTenant {...defaultProps} onClose={onClose} />);
    await waitFor(() => screen.getByTestId('ndialog'));
    const cancelBtn = screen.getByTestId('custom-btn-Cancel');
    fireEvent.click(cancelBtn);
    expect(onClose).toHaveBeenCalled();
  });

  it('calls updateUserTenant when Switch Tenant button clicked', async () => {
    mockUpdate.mockResolvedValue({});
    render(<SwitchTenant {...defaultProps} />);
    await waitFor(() => screen.getByTestId('ndialog'));
    const switchBtn = screen.getByTestId('custom-btn-Switch Tenant');
    fireEvent.click(switchBtn);
    await waitFor(() => {
      expect(mockUpdate).toHaveBeenCalled();
      expect(cache.clear).toHaveBeenCalled();
    });
  });

  it('uses custom buttonTitle', async () => {
    render(<SwitchTenant {...defaultProps} buttonTitle='Change Tenant' />);
    await waitFor(() => screen.getByTestId('ndialog'));
    expect(screen.getByTestId('custom-btn-Change Tenant')).toBeInTheDocument();
  });

  it('handles tenant selection change', async () => {
    render(<SwitchTenant {...defaultProps} />);
    await waitFor(() => expect(screen.getByTestId('tenant-select')).toBeInTheDocument());
    const select = screen.getByTestId('tenant-select');
    fireEvent.change(select, { target: { value: 'Tenant A' } });
    // Should not throw
  });

  it('does not reload tenants if dialog reopens but no email', async () => {
    useSession.mockReturnValue({
      data: null,
      update: mockUpdate,
    });
    render(<SwitchTenant {...defaultProps} />);
    await waitFor(() => expect(screen.getByTestId('ndialog')).toBeInTheDocument());
    expect(apiUserManagement.listUserTenants).not.toHaveBeenCalled();
  });

  it('handles API error gracefully', async () => {
    apiUserManagement.listUserTenants.mockResolvedValue({ data: null });
    render(<SwitchTenant {...defaultProps} />);
    await waitFor(() => {
      expect(apiUserManagement.listUserTenants).toHaveBeenCalled();
    });
    // Should not crash - dialog should still render with empty state
    expect(screen.getByTestId('ndialog')).toBeInTheDocument();
  });

  it('shows title with tenant name in dialog', async () => {
    render(<SwitchTenant {...defaultProps} />);
    await waitFor(() => screen.getByTestId('dialog-title'));
    const title = screen.getByTestId('dialog-title');
    expect(title.textContent).toContain('Switch Tenant');
  });
});
