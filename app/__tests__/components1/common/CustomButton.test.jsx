import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import CustomButton from '@components1/common/NewCustomButton';

jest.mock('src/utils/colors', () => ({
  colors: {
    text: {
      secondary: '#374151',
      primary: '#3B82F6',
      white: '#fff',
      black: '#000',
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

describe('CustomButton', () => {
  it('renders button with text', () => {
    render(<CustomButton text='Click Me' />);
    expect(screen.getByText('Click Me')).toBeInTheDocument();
  });

  it('calls onClick when clicked', () => {
    const onClick = jest.fn();
    render(<CustomButton text='Submit' onClick={onClick} />);
    fireEvent.click(screen.getByText('Submit'));
    expect(onClick).toHaveBeenCalledTimes(1);
  });

  it('is disabled when disabled prop is true', () => {
    render(<CustomButton text='Disabled' disabled={true} />);
    const button = screen.getByRole('button', { name: /disabled/i });
    expect(button).toBeDisabled();
  });

  it('shows tooltip text when showTooltip is true', () => {
    render(<CustomButton text='Hover Me' showTooltip={true} toolTipTitle='Tooltip content' />);
    const button = screen.getByText('Hover Me');
    expect(button).toBeInTheDocument();
  });

  it('applies id prop to button', () => {
    render(<CustomButton text='My Button' id='my-custom-button' />);
    const button = screen.getByRole('button', { name: /my button/i });
    expect(button).toHaveAttribute('id', 'my-custom-button');
  });

  it('renders with startIcon', () => {
    const icon = <span data-testid='start-icon'>icon</span>;
    render(<CustomButton text='With Icon' startIcon={icon} />);
    expect(screen.getByTestId('start-icon')).toBeInTheDocument();
    expect(screen.getByText('With Icon')).toBeInTheDocument();
  });

  it('renders with aria-label from label prop', () => {
    render(<CustomButton text='Save' label='save-action' />);
    const button = screen.getByRole('button', { name: 'save-action' });
    expect(button).toHaveAttribute('aria-label', 'save-action');
  });
});
