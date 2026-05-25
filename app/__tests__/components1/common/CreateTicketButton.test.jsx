import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import CreateTicketButton from '@components1/common/CreateTicketButton';

jest.mock('@assets/sidebar-icon/tickets-icon.svg', () => '/tickets-icon.svg', { virtual: true });

jest.mock('@components1/common/CustomTooltip', () => ({
  __esModule: true,
  default: ({ children, title }) => <div title={title}>{children}</div>,
}));

jest.mock('@components1/common/SafeIcon', () => ({
  __esModule: true,
  default: ({ alt, ...props }) => <img alt={alt} data-testid='safe-icon' {...props} />,
}));

describe('CreateTicketButton', () => {
  test('renders the button with id "create-ticket"', () => {
    render(<CreateTicketButton onClick={jest.fn()} />);
    expect(document.getElementById('create-ticket')).toBeInTheDocument();
  });

  test('has aria-label "Create Ticket"', () => {
    render(<CreateTicketButton onClick={jest.fn()} />);
    expect(screen.getByRole('button', { name: 'Create Ticket' })).toBeInTheDocument();
  });

  test('calls onClick when clicked', () => {
    const onClick = jest.fn();
    render(<CreateTicketButton onClick={onClick} />);
    fireEvent.click(screen.getByRole('button', { name: 'Create Ticket' }));
    expect(onClick).toHaveBeenCalledTimes(1);
  });

  test('renders SafeIcon with alt "Create Ticket"', () => {
    render(<CreateTicketButton onClick={jest.fn()} />);
    expect(screen.getByAltText('Create Ticket')).toBeInTheDocument();
  });
});
