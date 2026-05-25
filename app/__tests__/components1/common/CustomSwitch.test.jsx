import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import CustomSwitch from '@components1/common/CustomSwitch';

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

describe('CustomSwitch', () => {
  it('renders switch element', () => {
    render(<CustomSwitch id='test' checked={false} onChange={jest.fn()} />);
    const switchEl = screen.getByRole('checkbox');
    expect(switchEl).toBeInTheDocument();
  });

  it('renders with id prop as "${id}-switch"', () => {
    render(<CustomSwitch id='my-feature' checked={false} onChange={jest.fn()} />);
    const switchEl = screen.getByRole('checkbox');
    expect(switchEl).toHaveAttribute('id', 'my-feature-switch');
  });

  it('shows checked state when checked is true', () => {
    render(<CustomSwitch id='test' checked={true} onChange={jest.fn()} />);
    const switchEl = screen.getByRole('checkbox');
    expect(switchEl).toBeChecked();
  });

  it('shows unchecked state when checked is false', () => {
    render(<CustomSwitch id='test' checked={false} onChange={jest.fn()} />);
    const switchEl = screen.getByRole('checkbox');
    expect(switchEl).not.toBeChecked();
  });

  it('calls onChange when toggled', () => {
    const onChange = jest.fn();
    render(<CustomSwitch id='test' checked={false} onChange={onChange} />);
    const switchEl = screen.getByRole('checkbox');
    fireEvent.click(switchEl);
    expect(onChange).toHaveBeenCalledTimes(1);
  });

  it('renders in disabled state when disabled is true', () => {
    render(<CustomSwitch id='test' checked={false} onChange={jest.fn()} disabled={true} />);
    const switchEl = screen.getByRole('checkbox');
    expect(switchEl).toBeDisabled();
  });

  it('renders in enabled state by default', () => {
    render(<CustomSwitch id='test' checked={false} onChange={jest.fn()} />);
    const switchEl = screen.getByRole('checkbox');
    expect(switchEl).not.toBeDisabled();
  });
});
