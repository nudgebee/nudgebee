import React from 'react';
import { render, screen } from '@testing-library/react';
import WidgetCard from '@components1/common/WidgetCard';

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

describe('WidgetCard', () => {
  it('renders children', () => {
    render(
      <WidgetCard>
        <span data-testid='child'>Hello</span>
      </WidgetCard>
    );
    expect(screen.getByTestId('child')).toBeInTheDocument();
  });

  it('accepts custom sx prop', () => {
    render(
      <WidgetCard sx={{ padding: '8px' }} data-testid='widget-card'>
        <span>Content</span>
      </WidgetCard>
    );
    expect(screen.getByTestId('widget-card')).toBeInTheDocument();
  });

  it('renders without children', () => {
    const { container } = render(<WidgetCard />);
    expect(container.firstChild).toBeInTheDocument();
  });

  it('passes additional props to Box (e.g. data-testid)', () => {
    render(
      <WidgetCard data-testid='my-widget-card'>
        <span>Content</span>
      </WidgetCard>
    );
    expect(screen.getByTestId('my-widget-card')).toBeInTheDocument();
  });
});
