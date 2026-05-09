import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import CustomBorderCard from '@components1/common/CustomBorderCard';

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

describe('CustomBorderCard', () => {
  it('renders children', () => {
    render(
      <CustomBorderCard>
        <span data-testid='child'>Hello</span>
      </CustomBorderCard>
    );
    expect(screen.getByTestId('child')).toBeInTheDocument();
  });

  it('calls onClick when clicked', () => {
    const onClick = jest.fn();
    render(
      <CustomBorderCard onClick={onClick}>
        <span>Content</span>
      </CustomBorderCard>
    );
    fireEvent.click(screen.getByText('Content'));
    expect(onClick).toHaveBeenCalledTimes(1);
  });

  it('cursor is "pointer" when onClick provided', () => {
    const onClick = jest.fn();
    const { container } = render(
      <CustomBorderCard onClick={onClick}>
        <span>Content</span>
      </CustomBorderCard>
    );
    const card = container.firstChild;
    expect(card).toHaveStyle({ cursor: 'pointer' });
  });

  it('cursor is "default" when no onClick', () => {
    const { container } = render(
      <CustomBorderCard>
        <span>Content</span>
      </CustomBorderCard>
    );
    const card = container.firstChild;
    expect(card).toHaveStyle({ cursor: 'default' });
  });

  it('renders with custom borderColor', () => {
    const { container } = render(
      <CustomBorderCard borderColor='#ff0000'>
        <span>Content</span>
      </CustomBorderCard>
    );
    expect(container.firstChild).toBeInTheDocument();
  });

  it('renders with showLeftBorder=false', () => {
    const { container } = render(
      <CustomBorderCard showLeftBorder={false}>
        <span>Content</span>
      </CustomBorderCard>
    );
    const card = container.firstChild;
    expect(card).toHaveStyle({ borderLeftStyle: 'none' });
  });

  it('accepts custom sx prop', () => {
    const { container } = render(
      <CustomBorderCard sx={{ marginTop: '10px' }}>
        <span>Content</span>
      </CustomBorderCard>
    );
    expect(container.firstChild).toBeInTheDocument();
  });

  it('renders with custom padding', () => {
    const { container } = render(
      <CustomBorderCard padding='8px 12px'>
        <span>Content</span>
      </CustomBorderCard>
    );
    const card = container.firstChild;
    expect(card).toHaveStyle({ padding: '8px 12px' });
  });
});
