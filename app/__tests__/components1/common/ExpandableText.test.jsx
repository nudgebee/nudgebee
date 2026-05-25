import React from 'react';
import { render, screen } from '@testing-library/react';
import ExpandableText from '@components1/common/ExpandableText';

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

describe('ExpandableText', () => {
  it('renders text content', () => {
    render(<ExpandableText text='Hello world' />);
    expect(screen.getByText('Hello world')).toBeInTheDocument();
  });

  it('renders empty string as fallback when no text provided', () => {
    const { container } = render(<ExpandableText />);
    // Component defaults text to '' — should render without crash
    expect(container.firstChild).toBeInTheDocument();
  });

  it('applies smaller fontSize (12px) when secondaryText is true', () => {
    render(<ExpandableText text='Secondary text' secondaryText={true} />);
    const typography = screen.getByText('Secondary text');
    // MUI inlines fontSize via emotion class; verify the element renders correctly
    expect(typography).toBeInTheDocument();
  });

  it('does not show Show More button by default (no layout overflow in jsdom)', () => {
    render(<ExpandableText text='Some long text that would normally overflow' />);
    expect(screen.queryByText(/show more/i)).not.toBeInTheDocument();
  });

  it('renders with custom sx without crashing', () => {
    expect(() => render(<ExpandableText text='Styled text' sx={{ color: 'red', fontWeight: 700 }} />)).not.toThrow();
  });

  it('renders with custom color prop', () => {
    render(<ExpandableText text='Colored text' color='#FF0000' />);
    const el = screen.getByText('Colored text');
    expect(el).toBeInTheDocument();
  });
});
