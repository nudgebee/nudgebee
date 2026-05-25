import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import CustomDrawer from '@components1/common/CustomDrawer';

jest.mock('src/utils/colors', () => ({
  colors: {
    primary: '#3B82F6',
    nudgebeeMain: '#3B82F6',
    text: {
      primary: '#3B82F6',
      secondary: '#374151',
      white: '#fff',
      black: '#000',
      tertiary: '#6B7280',
      title: '#111827',
      primaryLight: '#60A5FA',
      success: '#16a34a',
      disabledInput: '#9CA3AF',
      secondaryDark: '#1F2937',
      yellowLabel: '#F59E0B',
      tertiarymedium: '#6B7280',
    },
    background: {
      primaryLightest: '#EFF6FF',
      white: '#fff',
      transparent: 'transparent',
      switchTrackDark: '#3B82F6',
      tertiaryLightest: '#F0F9FF',
      input: '#F9FAFB',
    },
    border: {
      secondary: '#D1D5DB',
      primary: '#3B82F6',
      success: '#22C55E',
      primaryLight: '#60A5FA',
      secondaryLight: '#E5E7EB',
      white: '#fff',
      vertical: '#E5E7EB',
    },
    button: {
      primary: '#3B82F6',
      primaryText: '#fff',
      primaryHover: '#2563EB',
      primaryDisabled: '#93C5FD',
      primaryDisabledText: '#fff',
      secondary: '#fff',
      secondaryBorder: '#D1D5DB',
      secondaryText: '#374151',
      secondaryHover: '#F9FAFB',
      secondaryHoverBorder: '#9CA3AF',
      secondaryDisabled: '#F3F4F6',
      secondaryDisabledText: '#9CA3AF',
      secondaryDisabledBorder: '#E5E7EB',
      tertiary: '#EFF6FF',
      tertiaryBorder: '#BFDBFE',
      tertiaryText: '#3B82F6',
      tertiaryHover: '#DBEAFE',
      tertiaryDisabled: '#F9FAFB',
      tertiaryDisabledText: '#93C5FD',
      tertiaryDisabledBorder: '#DBEAFE',
    },
  },
}));

describe('CustomDrawer', () => {
  it('renders title when open=true', () => {
    render(
      <CustomDrawer open={true} onClose={() => {}} title='Drawer Title'>
        <div>Content</div>
      </CustomDrawer>
    );
    expect(screen.getByText('Drawer Title')).toBeInTheDocument();
  });

  it('does not show content when open=false', () => {
    render(
      <CustomDrawer open={false} onClose={() => {}} title='Drawer Title'>
        <div>Content</div>
      </CustomDrawer>
    );
    expect(screen.queryByText('Drawer Title')).not.toBeInTheDocument();
  });

  it('renders children inside drawer when open', () => {
    render(
      <CustomDrawer open={true} onClose={() => {}} title='Drawer Title'>
        <div data-testid='drawer-child'>Child</div>
      </CustomDrawer>
    );
    expect(screen.getByTestId('drawer-child')).toBeInTheDocument();
  });

  it('calls onClose when close button clicked (data-testid="custom-drawer-close")', () => {
    const onClose = jest.fn();
    render(
      <CustomDrawer open={true} onClose={onClose} title='Drawer Title'>
        <div>Content</div>
      </CustomDrawer>
    );
    const closeButton = screen.getByTestId('custom-drawer-close');
    fireEvent.click(closeButton);
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('renders custom title', () => {
    render(
      <CustomDrawer open={true} onClose={() => {}} title={<span data-testid='custom-title'>Custom Title Node</span>}>
        <div>Content</div>
      </CustomDrawer>
    );
    expect(screen.getByTestId('custom-title')).toBeInTheDocument();
  });

  it('renders without error with default width', () => {
    render(
      <CustomDrawer open={true} onClose={() => {}}>
        <div>Content</div>
      </CustomDrawer>
    );
    expect(screen.getByTestId('custom-drawer-close')).toBeInTheDocument();
  });
});
