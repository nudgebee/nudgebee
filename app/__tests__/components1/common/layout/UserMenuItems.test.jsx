import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import { createGetMenuItem, generateMenuItems } from '@components1/common/layout/UserMenuItems';

// Mock next-auth/react
jest.mock('next-auth/react', () => ({
  signOut: jest.fn(),
  useSession: () => ({ data: { user: { name: 'Test User' }, roles: [], tenant: 'test' }, status: 'authenticated' }),
}));

// Mock auth lib
jest.mock('@lib/auth', () => ({
  getUserSession: jest.fn(() => ({
    user: { name: 'Test User', email: 'test@example.com', image: null },
    hasMultipleTenantAccess: false,
    appVersion: '1.0.0',
  })),
  isTenantAdmin: jest.fn(() => false),
}));

// Mock @assets with all icons
jest.mock('@assets', () => ({
  SwitchTenentIconDark: '/switch-icon.png',
  LogoutIconDark: '/logout-icon.png',
  SettingsIcon: '/settings-icon.png',
  ApiIcon: '/api-icon.png',
}));

// Mock colors
jest.mock('src/utils/colors', () => {
  const actual = jest.requireActual('src/utils/colors');
  return {
    ...actual,
    colors: {
      ...actual.colors,
      text: { ...actual.colors.text, secondary: '#333', secondaryDark: '#555' },
    },
  };
});

const { signOut } = require('next-auth/react');
const { getUserSession, isTenantAdmin } = require('@lib/auth');

describe('UserMenuItems', () => {
  let setAnchorElUser;
  let setOpenSwitchAccount;
  let setOpenSettings;
  let setOpenApiTokens;
  let handleSubMenuClick;
  let getMenuItem;

  beforeEach(() => {
    jest.clearAllMocks();
    setAnchorElUser = jest.fn();
    setOpenSwitchAccount = jest.fn();
    setOpenSettings = jest.fn();
    setOpenApiTokens = jest.fn();
    handleSubMenuClick = jest.fn();
    getMenuItem = createGetMenuItem({
      setAnchorElUser,
      setOpenSwitchAccount,
      setOpenSettings,
      setOpenApiTokens,
      handleSubMenuClick,
    });
  });

  describe('createGetMenuItem', () => {
    it('returns a function with displayName', () => {
      expect(typeof getMenuItem).toBe('function');
      expect(getMenuItem.displayName).toBe('MenuItem');
    });

    it('renders UserInfo item without image', () => {
      getUserSession.mockReturnValue({
        user: { name: 'Test User', email: 'test@example.com', image: null },
        appVersion: '1.0.0',
      });
      const { container } = render(<>{getMenuItem('UserInfo')}</>);
      expect(container).toBeInTheDocument();
    });

    it('renders UserInfo item with image', () => {
      getUserSession.mockReturnValue({
        user: { name: 'Test User', email: 'test@example.com', image: 'http://example.com/avatar.png' },
        appVersion: '1.0.0',
      });
      const { container } = render(<>{getMenuItem('UserInfo')}</>);
      expect(container).toBeInTheDocument();
    });

    it('renders Switch Tenant item and handles click', () => {
      render(<>{getMenuItem('Switch Tenant')}</>);
      const menuItem = screen.getByText(/Switch Tenant/);
      fireEvent.click(menuItem.closest('[role="menuitem"]') || menuItem);
      expect(setAnchorElUser).toHaveBeenCalledWith(null);
      expect(setOpenSwitchAccount).toHaveBeenCalledWith(true);
    });

    it('renders Logout item and handles click', () => {
      render(<>{getMenuItem('Logout')}</>);
      const menuItem = screen.getByText(/Logout/);
      fireEvent.click(menuItem.closest('[role="menuitem"]') || menuItem);
      expect(setAnchorElUser).toHaveBeenCalledWith(null);
      expect(signOut).toHaveBeenCalledWith({ callbackUrl: '/' });
    });

    it('renders Version item with appVersion', () => {
      getUserSession.mockReturnValue({
        user: { name: 'Test User', email: 'test@example.com' },
        appVersion: '2.5.0',
      });
      render(<>{getMenuItem('Version')}</>);
      expect(screen.getByText(/Version: 2.5.0/)).toBeInTheDocument();
    });

    it('renders Version item with N/A when no appVersion', () => {
      getUserSession.mockReturnValue({
        user: { name: 'Test User', email: 'test@example.com' },
        appVersion: null,
      });
      render(<>{getMenuItem('Version')}</>);
      expect(screen.getByText(/Version: N\/A/)).toBeInTheDocument();
    });

    it('renders Settings item and handles click', () => {
      render(<>{getMenuItem('Settings')}</>);
      const menuItem = screen.getByText(/Settings/);
      fireEvent.click(menuItem.closest('[role="menuitem"]') || menuItem);
      expect(setAnchorElUser).toHaveBeenCalledWith(null);
      expect(setOpenSettings).toHaveBeenCalledWith(true);
    });

    it('renders API Tokens item and handles click', () => {
      render(<>{getMenuItem('API Tokens')}</>);
      const menuItem = screen.getByText(/API Tokens/);
      fireEvent.click(menuItem.closest('[role="menuitem"]') || menuItem);
      expect(setAnchorElUser).toHaveBeenCalledWith(null);
      expect(setOpenApiTokens).toHaveBeenCalledWith(true);
    });

    it('renders fallback generic menu item for unknown settings', () => {
      render(<>{getMenuItem('SomeOtherItem')}</>);
      const menuItem = screen.getByText('SomeOtherItem');
      expect(menuItem).toBeInTheDocument();
      fireEvent.click(menuItem.closest('[role="menuitem"]') || menuItem);
      expect(handleSubMenuClick).toHaveBeenCalledWith('SomeOtherItem');
    });
  });

  describe('generateMenuItems', () => {
    it('returns basic menu without multi-tenant and not admin', () => {
      isTenantAdmin.mockReturnValue(false);
      const menu = generateMenuItems(false);
      expect(menu).toContain('UserInfo');
      expect(menu).not.toContain('Switch Tenant');
      expect(menu).not.toContain('Settings');
      expect(menu).toContain('API Tokens');
      expect(menu).toContain('Logout');
      expect(menu).toContain('Version');
    });

    it('includes Switch Tenant when hasMultipleTenantAccess is true', () => {
      isTenantAdmin.mockReturnValue(false);
      const menu = generateMenuItems(true);
      expect(menu).toContain('Switch Tenant');
    });

    it('includes Settings when user is tenant admin', () => {
      isTenantAdmin.mockReturnValue(true);
      const menu = generateMenuItems(false);
      expect(menu).toContain('Settings');
    });

    it('includes both Switch Tenant and Settings when both conditions true', () => {
      isTenantAdmin.mockReturnValue(true);
      const menu = generateMenuItems(true);
      expect(menu).toContain('Switch Tenant');
      expect(menu).toContain('Settings');
    });

    it('uses default parameter value of false for hasMultipleTenantAccess', () => {
      isTenantAdmin.mockReturnValue(false);
      const menu = generateMenuItems();
      expect(menu).not.toContain('Switch Tenant');
    });
  });
});
