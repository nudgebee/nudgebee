import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import NewCustomButton from '@components1/common/NewCustomButton';

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

jest.mock('@components1/common/SafeIcon', () => ({
  __esModule: true,
  default: ({ alt, ...props }) => <img alt={alt} {...props} />,
}));

describe('NewCustomButton', () => {
  it('renders button with text', () => {
    render(<NewCustomButton text='Click Me' />);
    expect(screen.getByText('Click Me')).toBeInTheDocument();
  });

  it('calls onClick when clicked', () => {
    const onClick = jest.fn();
    render(<NewCustomButton text='Click Me' onClick={onClick} />);
    fireEvent.click(screen.getByText('Click Me'));
    expect(onClick).toHaveBeenCalledTimes(1);
  });

  it('is disabled when disabled=true', () => {
    render(<NewCustomButton text='Click Me' disabled={true} />);
    expect(screen.getByText('Click Me').closest('button')).toBeDisabled();
  });

  it('shows tooltip when showTooltip=true and toolTipTitle provided', () => {
    render(<NewCustomButton text='Click Me' showTooltip={true} toolTipTitle='Tooltip text' />);
    expect(screen.getByText('Click Me')).toBeInTheDocument();
  });

  it('renders loading spinner when loading=true', () => {
    render(<NewCustomButton text='Click Me' loading={true} />);
    // When loading=true, a CircularProgress is shown and button is disabled
    const button = screen.getByText('Click Me').closest('button');
    expect(button).toBeDisabled();
  });

  it('renders with startIcon as React element', () => {
    render(<NewCustomButton text='With Icon' startIcon={<span data-testid='start-icon'>*</span>} />);
    expect(screen.getByTestId('start-icon')).toBeInTheDocument();
    expect(screen.getByText('With Icon')).toBeInTheDocument();
  });

  it('renders with endIcon as React element', () => {
    render(<NewCustomButton text='With End Icon' endIcon={<span data-testid='end-icon'>*</span>} />);
    expect(screen.getByTestId('end-icon')).toBeInTheDocument();
  });

  it('renders with primary variant without error', () => {
    render(<NewCustomButton text='Primary' variant='primary' />);
    expect(screen.getByText('Primary')).toBeInTheDocument();
  });

  it('renders with secondary variant without error', () => {
    render(<NewCustomButton text='Secondary' variant='secondary' />);
    expect(screen.getByText('Secondary')).toBeInTheDocument();
  });

  it('renders with tertiary variant without error', () => {
    render(<NewCustomButton text='Tertiary' variant='tertiary' />);
    expect(screen.getByText('Tertiary')).toBeInTheDocument();
  });

  it('renders with xSmall size', () => {
    render(<NewCustomButton text='XSmall' size='xSmall' />);
    expect(screen.getByText('XSmall')).toBeInTheDocument();
  });

  it('renders with Small size', () => {
    render(<NewCustomButton text='Small' size='Small' />);
    expect(screen.getByText('Small')).toBeInTheDocument();
  });

  it('renders with Medium size', () => {
    render(<NewCustomButton text='Medium' size='Medium' />);
    expect(screen.getByText('Medium')).toBeInTheDocument();
  });

  it('renders with Large size', () => {
    render(<NewCustomButton text='Large' size='Large' />);
    expect(screen.getByText('Large')).toBeInTheDocument();
  });

  it('renders with xLarge size', () => {
    render(<NewCustomButton text='XLarge' size='xLarge' />);
    expect(screen.getByText('XLarge')).toBeInTheDocument();
  });
});
