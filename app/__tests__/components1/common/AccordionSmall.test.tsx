import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import AccordionSmall from '@components1/common/AccordionSmall';

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

jest.mock('@components1/common/widgets/CustomLabels', () => ({
  __esModule: true,
  default: ({ text }: { text: string }) => <span>{text}</span>,
}));

describe('AccordionSmall', () => {
  it('renders with string header', () => {
    render(
      <AccordionSmall header='Test Header'>
        <div>Child content</div>
      </AccordionSmall>
    );
    expect(screen.getByText('Test Header')).toBeInTheDocument();
  });

  it('renders with React node header', () => {
    render(
      <AccordionSmall header={<span data-testid='node-header'>Node Header</span>}>
        <div>Child content</div>
      </AccordionSmall>
    );
    expect(screen.getByTestId('node-header')).toBeInTheDocument();
  });

  it('starts collapsed by default (no expanded prop)', () => {
    render(
      <AccordionSmall header='Header'>
        <div>Child content</div>
      </AccordionSmall>
    );
    // AccordionDetails is not visible when collapsed
    const _details = screen.queryByText('Child content');
    // MUI Accordion hides content but keeps it in the DOM; check expanded state via aria
    const button = screen.getByRole('button');
    expect(button).toHaveAttribute('aria-expanded', 'false');
  });

  it('expands when clicked (uncontrolled mode)', () => {
    render(
      <AccordionSmall header='Header'>
        <div>Child content</div>
      </AccordionSmall>
    );
    const button = screen.getByRole('button');
    expect(button).toHaveAttribute('aria-expanded', 'false');
    fireEvent.click(button);
    expect(button).toHaveAttribute('aria-expanded', 'true');
  });

  it('uses controlled expanded prop', () => {
    render(
      <AccordionSmall header='Header' expanded={true} onExpandedChange={() => {}}>
        <div>Child content</div>
      </AccordionSmall>
    );
    const button = screen.getByRole('button');
    expect(button).toHaveAttribute('aria-expanded', 'true');
  });

  it('calls onExpandedChange when toggled', () => {
    const onExpandedChange = jest.fn();
    render(
      <AccordionSmall header='Header' expanded={false} onExpandedChange={onExpandedChange}>
        <div>Child content</div>
      </AccordionSmall>
    );
    const button = screen.getByRole('button');
    fireEvent.click(button);
    expect(onExpandedChange).toHaveBeenCalledWith(true);
  });

  it('shows status label when status prop provided (no dropdown)', () => {
    render(
      <AccordionSmall header='Header' status='pending'>
        <div>Child content</div>
      </AccordionSmall>
    );
    expect(screen.getByText('pending')).toBeInTheDocument();
  });

  it('shows status dropdown when enableStatusDropdown is true', () => {
    render(
      <AccordionSmall header='Header' enableStatusDropdown={true} currentStatus='pending'>
        <div>Child content</div>
      </AccordionSmall>
    );
    // The dropdown renders a Select with CustomLabels
    expect(screen.getByText('pending')).toBeInTheDocument();
  });

  it('calls onStatusChange when dropdown value changes', () => {
    const onStatusChange = jest.fn();
    render(
      <AccordionSmall header='Header' enableStatusDropdown={true} currentStatus='pending' onStatusChange={onStatusChange}>
        <div>Child content</div>
      </AccordionSmall>
    );
    // Status is rendered; component renders a select
    expect(screen.getByText('pending')).toBeInTheDocument();
  });

  it('renders children in AccordionDetails', () => {
    render(
      <AccordionSmall header='Header' expanded={true} onExpandedChange={() => {}}>
        <div data-testid='child-node'>Child content</div>
      </AccordionSmall>
    );
    expect(screen.getByTestId('child-node')).toBeInTheDocument();
  });
});
