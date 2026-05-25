import React from 'react';
import { render, screen } from '@testing-library/react';
import CustomTooltip from '@components1/common/CustomTooltip';

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

jest.mock('@components1/common/NewCustomButton', () => ({
  __esModule: true,
  default: ({ text, onClick }: { text: string; onClick: () => void }) => <button onClick={onClick}>{text}</button>,
}));

describe('CustomTooltip', () => {
  it('renders children when title and desc are both falsy (returns children)', () => {
    render(
      <CustomTooltip title=''>
        <span data-testid='child-element'>Child</span>
      </CustomTooltip>
    );
    expect(screen.getByTestId('child-element')).toBeInTheDocument();
  });

  it('returns null when children is not a valid React element', () => {
    // @ts-ignore - intentionally passing invalid children for test
    const { container } = render(<CustomTooltip title='tooltip'>{'just a string'}</CustomTooltip>);
    expect(container).toBeEmptyDOMElement();
  });

  it('renders children with default variant', () => {
    render(
      <CustomTooltip title='Tooltip title'>
        <span data-testid='child-element'>Child</span>
      </CustomTooltip>
    );
    expect(screen.getByTestId('child-element')).toBeInTheDocument();
  });

  it('renders with explainer variant (just renders without error)', () => {
    render(
      <CustomTooltip title='Explainer title' desc='Explainer description' variant='explainer'>
        <span data-testid='child-element'>Child</span>
      </CustomTooltip>
    );
    expect(screen.getByTestId('child-element')).toBeInTheDocument();
  });

  it('renders with interactive variant when linkUrl and linkText provided', () => {
    render(
      <CustomTooltip title='Interactive title' variant='interactive' linkUrl='https://example.com' linkText='Learn More'>
        <span data-testid='child-element'>Child</span>
      </CustomTooltip>
    );
    expect(screen.getByTestId('child-element')).toBeInTheDocument();
  });

  it('passes placement prop to tooltip', () => {
    render(
      <CustomTooltip title='Tooltip title' placement='bottom'>
        <span data-testid='child-element'>Child</span>
      </CustomTooltip>
    );
    expect(screen.getByTestId('child-element')).toBeInTheDocument();
  });
});
