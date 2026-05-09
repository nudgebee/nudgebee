import React from 'react';
import { render, screen, fireEvent, act } from '@testing-library/react';
import ButtonMenu from '@components1/common/ButtonMenu';

jest.mock('src/utils/colors', () => ({
  colors: {
    text: {
      secondary: '#374151',
      primary: '#3B82F6',
      white: '#fff',
      tertiary: '#6B7280',
      title: '#111827',
      primaryLight: '#60A5FA',
      success: '#16a34a',
      disabledInput: '#9CA3AF',
      secondaryDark: '#1F2937',
    },
    background: { primaryLightest: '#EFF6FF', buttonTab: '#EFF6FF', white: '#fff', transparent: 'transparent', switchTrackDark: '#3B82F6' },
    border: { secondary: '#D1D5DB', primary: '#3B82F6', success: '#22C55E', buttonTab: '#3B82F6', primaryLight: '#60A5FA' },
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
    primary: '#3B82F6',
  },
}));

const defaultItems = [
  { text: 'Item One', onClick: jest.fn() },
  { text: 'Item Two', onClick: jest.fn() },
];

describe('ButtonMenu', () => {
  beforeEach(() => {
    jest.clearAllMocks();
  });

  it('renders button with default title Options', () => {
    render(<ButtonMenu items={defaultItems} />);
    expect(screen.getByText('Options')).toBeInTheDocument();
  });

  it('renders with custom title', () => {
    render(<ButtonMenu title='Actions' items={defaultItems} />);
    expect(screen.getByText('Actions')).toBeInTheDocument();
  });

  it('clicking button opens menu showing item texts', () => {
    render(<ButtonMenu items={defaultItems} />);
    fireEvent.click(screen.getByText('Options'));
    expect(screen.getByText('Item One')).toBeInTheDocument();
    expect(screen.getByText('Item Two')).toBeInTheDocument();
  });

  it('clicking menu item calls item onClick', () => {
    const onClick = jest.fn();
    const items = [{ text: 'Do Something', onClick }];
    render(<ButtonMenu items={items} />);
    fireEvent.click(screen.getByText('Options'));
    fireEvent.click(screen.getByText('Do Something'));
    expect(onClick).toHaveBeenCalledTimes(1);
  });

  it('clicking menu item closes the menu', async () => {
    const items = [{ text: 'Close Me', onClick: jest.fn() }];
    render(<ButtonMenu items={items} />);
    fireEvent.click(screen.getByText('Options'));
    expect(screen.getByText('Close Me')).toBeInTheDocument();
    await act(async () => {
      fireEvent.click(screen.getByText('Close Me'));
    });
    // After close, MUI menu sets the paper to hidden; the anchor becomes null
    // so the menu is no longer open (aria-expanded is removed from button)
    const button = screen.getByRole('button', { name: /options/i });
    expect(button).not.toHaveAttribute('aria-expanded', 'true');
  });

  it('item is disabled when item.disabled is true', () => {
    const items = [{ text: 'Disabled Item', onClick: jest.fn(), disabled: true }];
    render(<ButtonMenu items={items} />);
    fireEvent.click(screen.getByText('Options'));
    const menuItem = screen.getByText('Disabled Item').closest('li');
    expect(menuItem).toHaveAttribute('aria-disabled', 'true');
  });

  it('item is disabled when item.accountsCount is greater than 0', () => {
    const items = [{ text: 'Has Accounts', onClick: jest.fn(), accountsCount: 3 }];
    render(<ButtonMenu items={items} />);
    fireEvent.click(screen.getByText('Options'));
    const menuItem = screen.getByText('Has Accounts').closest('li');
    expect(menuItem).toHaveAttribute('aria-disabled', 'true');
  });

  it('renders primary variant button', () => {
    render(<ButtonMenu items={defaultItems} variant='primary' />);
    const button = screen.getByRole('button', { name: /options/i });
    expect(button).toBeInTheDocument();
  });

  it('renders with default (blue) variant when no variant specified', () => {
    render(<ButtonMenu items={defaultItems} />);
    const button = screen.getByRole('button', { name: /options/i });
    expect(button).toBeInTheDocument();
  });
});
