import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import ResolveButton from '@components1/common/ResolveButton';

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

jest.mock('src/utils/actionStyles', () => ({
  action: {
    investigateOutline: { borderRadius: '4px', height: '28px', width: '28px' },
  },
}));

jest.mock('@assets', () => ({
  AutoPilotGreyIcon: '/mock-icon.svg',
  MenuArrowDownIcon: '/mock-icon.svg',
  ArrowBackGrayIcon: '/mock-icon.svg',
}));

jest.mock('react-icons/fi', () => ({
  FiArrowRight: () => <svg data-testid='arrow-icon' />,
}));

describe('ResolveButton', () => {
  it('renders without crashing', () => {
    render(<ResolveButton />);
    expect(screen.getByRole('button')).toBeInTheDocument();
  });

  it('has aria-label "Optimize" by default', () => {
    render(<ResolveButton />);
    expect(screen.getByRole('button', { name: 'Optimize' })).toBeInTheDocument();
  });

  it('has aria-label "Autopilot Configured" when isResolvedConfigured=true', () => {
    render(<ResolveButton isResolvedConfigured={true} />);
    expect(screen.getByRole('button', { name: 'Autopilot Configured' })).toBeInTheDocument();
  });

  it('calls onClick when clicked', () => {
    const onClick = jest.fn();
    render(<ResolveButton onClick={onClick} />);
    fireEvent.click(screen.getByRole('button'));
    expect(onClick).toHaveBeenCalledTimes(1);
  });

  it('shows "Optimize" text when displayText=true and isResolvedConfigured=false', () => {
    render(<ResolveButton displayText={true} isResolvedConfigured={false} />);
    expect(screen.getByText('Optimize')).toBeInTheDocument();
  });

  it('shows "Pilot on" text when displayText=true and isResolvedConfigured=true', () => {
    render(<ResolveButton displayText={true} isResolvedConfigured={true} />);
    expect(screen.getByText('Pilot on')).toBeInTheDocument();
  });

  it('shows icon-only (arrow icon) when displayText=false (default)', () => {
    render(<ResolveButton displayText={false} />);
    expect(screen.getByTestId('arrow-icon')).toBeInTheDocument();
    expect(screen.queryByText('Optimize')).not.toBeInTheDocument();
  });
});
