import React from 'react';
import { render, screen } from '@testing-library/react';
import CustomTicketLink from '@components1/common/CustomTicketLink';

jest.mock('src/utils/colors', () => ({
  colors: {
    primary: '#3B82F6',
    nudgebeeMain: '#3B82F6',
    yellow: '#F59E0B',
    clusterIndicator: '#10B981',
    error: '#EF4444',
    iconColor: '#6B7280',
    text: {
      primary: '#3B82F6',
      secondary: '#374151',
      white: '#fff',
      black: '#000',
      tertiary: '#6B7280',
      yellowLabel: '#F59E0B',
      tertiarymedium: '#6B7280',
      disabled: '#9CA3AF',
      secondaryDark: '#1F2937',
    },
    background: {
      primaryLightest: '#EFF6FF',
      white: '#fff',
      transparent: 'transparent',
      tertiaryLightest: '#F0F9FF',
      input: '#F9FAFB',
      infoGraphic: '#F8FAFC',
      error: '#EF4444',
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
      tertiaryBorder: '#BFDBFE',
      secondary: '#fff',
      secondaryBorder: '#D1D5DB',
      secondaryText: '#374151',
    },
  },
}));

jest.mock('next/link', () => ({
  __esModule: true,
  default: ({ href, children, target, ...rest }) => (
    <a href={href} target={target} {...rest}>
      {children}
    </a>
  ),
}));

jest.mock('@components1/common/SafeIcon', () => ({
  __esModule: true,
  default: ({ alt, ...props }) => <img alt={alt} data-testid='safe-icon' {...props} />,
}));

describe('CustomTicketLink', () => {
  it('renders "Ticket -" label', () => {
    render(<CustomTicketLink ticketURL='https://example.com/ticket/123' ticketID='TICKET-123' />);
    expect(screen.getByText('Ticket -')).toBeInTheDocument();
  });

  it('renders a link with correct href when ticketURL is provided', () => {
    render(<CustomTicketLink ticketURL='https://example.com/ticket/123' ticketID='TICKET-123' />);
    const link = screen.getByRole('link');
    expect(link).toHaveAttribute('href', 'https://example.com/ticket/123');
  });

  it('renders ticketID as link text when ticketURL is provided', () => {
    render(<CustomTicketLink ticketURL='https://example.com/ticket/123' ticketID='TICKET-123' />);
    expect(screen.getByText('TICKET-123')).toBeInTheDocument();
  });

  it('opens link in new tab when ticketURL is provided', () => {
    render(<CustomTicketLink ticketURL='https://example.com/ticket/123' ticketID='TICKET-123' />);
    const link = screen.getByRole('link');
    expect(link).toHaveAttribute('target', '_blank');
  });

  it('renders ticketID as plain text when ticketURL is empty string', () => {
    render(<CustomTicketLink ticketURL='' ticketID='TICKET-456' />);
    expect(screen.queryByRole('link')).not.toBeInTheDocument();
    expect(screen.getByText('TICKET-456')).toBeInTheDocument();
  });

  it('renders with showAutoEllipsis prop without crashing', () => {
    render(<CustomTicketLink ticketURL='https://example.com/ticket/123' ticketID='TICKET-123' showAutoEllipsis={true} maxWidth='80px' />);
    expect(screen.getByText('TICKET-123')).toBeInTheDocument();
  });

  it('renders without showAutoEllipsis prop (default false)', () => {
    render(<CustomTicketLink ticketURL='https://example.com/ticket/123' ticketID='TICKET-789' />);
    expect(screen.getByText('TICKET-789')).toBeInTheDocument();
  });

  it('renders with custom maxWidth prop', () => {
    render(<CustomTicketLink ticketURL='' ticketID='TICKET-999' showAutoEllipsis={true} maxWidth='100px' />);
    expect(screen.getByText('TICKET-999')).toBeInTheDocument();
  });
});
