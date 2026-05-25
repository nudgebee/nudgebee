import React from 'react';
import { render, screen } from '@testing-library/react';
import CustomCollapseable from '@components1/common/CustomCollapseable';

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

describe('CustomCollapseable', () => {
  it('renders with title', () => {
    render(
      <CustomCollapseable title='My Title'>
        <div>Content</div>
      </CustomCollapseable>
    );
    expect(screen.getByText('My Title')).toBeInTheDocument();
  });

  it('starts collapsed by default (defaultExpand=false)', () => {
    render(
      <CustomCollapseable title='My Title'>
        <div>Content</div>
      </CustomCollapseable>
    );
    const button = screen.getByRole('button');
    expect(button).toHaveAttribute('aria-expanded', 'false');
  });

  it('starts expanded when defaultExpand=true', () => {
    render(
      <CustomCollapseable title='My Title' defaultExpand={true}>
        <div>Content</div>
      </CustomCollapseable>
    );
    const button = screen.getByRole('button');
    expect(button).toHaveAttribute('aria-expanded', 'true');
  });

  it('shows title when collapsed', () => {
    render(
      <CustomCollapseable title='My Title'>
        <div>Content</div>
      </CustomCollapseable>
    );
    expect(screen.getByText('My Title')).toBeInTheDocument();
  });

  it('hides title when expanded (component hides title when expanded)', () => {
    render(
      <CustomCollapseable title='My Title' defaultExpand={true}>
        <div>Content</div>
      </CustomCollapseable>
    );
    expect(screen.queryByText('My Title')).not.toBeInTheDocument();
  });

  it('renders children when expanded', () => {
    render(
      <CustomCollapseable title='My Title' defaultExpand={true}>
        <div data-testid='child-content'>Child content</div>
      </CustomCollapseable>
    );
    expect(screen.getByTestId('child-content')).toBeInTheDocument();
  });

  it('custom icon is shown when provided', () => {
    render(
      <CustomCollapseable title='My Title' icon={<span data-testid='custom-icon'>Icon</span>}>
        <div>Content</div>
      </CustomCollapseable>
    );
    expect(screen.getByTestId('custom-icon')).toBeInTheDocument();
  });

  it('updates expanded state when defaultExpand prop changes', () => {
    const { rerender } = render(
      <CustomCollapseable title='My Title' defaultExpand={false}>
        <div>Content</div>
      </CustomCollapseable>
    );
    expect(screen.getByRole('button')).toHaveAttribute('aria-expanded', 'false');
    expect(screen.getByText('My Title')).toBeInTheDocument();

    rerender(
      <CustomCollapseable title='My Title' defaultExpand={true}>
        <div>Content</div>
      </CustomCollapseable>
    );
    expect(screen.getByRole('button')).toHaveAttribute('aria-expanded', 'true');
    expect(screen.queryByText('My Title')).not.toBeInTheDocument();
  });
});
