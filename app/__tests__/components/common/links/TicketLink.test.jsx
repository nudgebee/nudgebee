import { render, screen } from '@testing-library/react';
import TicketLink from '@shared/links/TicketLink';

jest.mock('@ui/Link', () => ({
  Link: ({ children, href, openInNew, maxWidth }) => (
    <a href={href} target={openInNew ? '_blank' : undefined} data-testid='ticket-link' data-max-width={maxWidth || ''}>
      {children}
    </a>
  ),
}));

jest.mock('@shared/format/Text', () => ({
  __esModule: true,
  default: ({ value, showAutoEllipsis, sx }) => (
    <span data-testid='ticket-text' data-show-auto-ellipsis={String(!!showAutoEllipsis)} data-has-max-width={sx && sx.maxWidth ? 'true' : 'false'}>
      {value}
    </span>
  ),
}));

jest.mock('@utils/colors');

describe('TicketLink', () => {
  describe('prefix', () => {
    it('always renders "Ticket -" prefix', () => {
      render(<TicketLink ticketURL='https://example.com' ticketID='JIRA-123' />);
      expect(screen.getByText('Ticket -')).toBeInTheDocument();
    });
  });

  describe('with ticketURL', () => {
    it('renders a link when ticketURL is provided', () => {
      render(<TicketLink ticketURL='https://example.com/JIRA-123' ticketID='JIRA-123' />);
      expect(screen.getByTestId('ticket-link')).toBeInTheDocument();
    });

    it('link href points to ticketURL', () => {
      render(<TicketLink ticketURL='https://example.com/JIRA-123' ticketID='JIRA-123' />);
      expect(screen.getByTestId('ticket-link')).toHaveAttribute('href', 'https://example.com/JIRA-123');
    });

    it('link opens in new tab', () => {
      render(<TicketLink ticketURL='https://example.com/JIRA-123' ticketID='JIRA-123' />);
      expect(screen.getByTestId('ticket-link')).toHaveAttribute('target', '_blank');
    });

    it('renders ticketID as link text', () => {
      render(<TicketLink ticketURL='https://example.com/JIRA-123' ticketID='JIRA-123' />);
      expect(screen.getByTestId('ticket-link')).toHaveTextContent('JIRA-123');
    });
  });

  describe('without ticketURL', () => {
    it('renders plain Text when ticketURL is empty', () => {
      render(<TicketLink ticketURL='' ticketID='JIRA-456' />);
      expect(screen.getByTestId('ticket-text')).toBeInTheDocument();
    });

    it('does not render a link when ticketURL is empty', () => {
      render(<TicketLink ticketURL='' ticketID='JIRA-456' />);
      expect(screen.queryByTestId('ticket-link')).not.toBeInTheDocument();
    });

    it('renders ticketID value in Text component', () => {
      render(<TicketLink ticketURL='' ticketID='JIRA-456' />);
      expect(screen.getByTestId('ticket-text')).toHaveTextContent('JIRA-456');
    });
  });

  describe('ellipsis / truncation (showAutoEllipsis)', () => {
    describe('link variant (with ticketURL)', () => {
      it('passes maxWidth to Link when showAutoEllipsis=true (default)', () => {
        render(<TicketLink ticketURL='https://example.com' ticketID='JIRA-1' />);
        expect(screen.getByTestId('ticket-link')).toHaveAttribute('data-max-width');
        expect(screen.getByTestId('ticket-link').getAttribute('data-max-width')).not.toBe('');
      });

      it('does not pass maxWidth to Link when showAutoEllipsis=false', () => {
        render(<TicketLink ticketURL='https://example.com' ticketID='JIRA-1' showAutoEllipsis={false} />);
        expect(screen.getByTestId('ticket-link')).toHaveAttribute('data-max-width', '');
      });

      it('forwards custom maxWidth to Link when showAutoEllipsis=true', () => {
        render(<TicketLink ticketURL='https://example.com' ticketID='JIRA-1' maxWidth='200px' />);
        expect(screen.getByTestId('ticket-link')).toHaveAttribute('data-max-width', '200px');
      });
    });

    describe('text variant (without ticketURL)', () => {
      it('passes showAutoEllipsis=true to Text by default', () => {
        render(<TicketLink ticketURL='' ticketID='JIRA-2' />);
        expect(screen.getByTestId('ticket-text')).toHaveAttribute('data-show-auto-ellipsis', 'true');
      });

      it('passes showAutoEllipsis=false to Text when disabled', () => {
        render(<TicketLink ticketURL='' ticketID='JIRA-2' showAutoEllipsis={false} />);
        expect(screen.getByTestId('ticket-text')).toHaveAttribute('data-show-auto-ellipsis', 'false');
      });

      it('sets sx.maxWidth on Text when showAutoEllipsis=true', () => {
        render(<TicketLink ticketURL='' ticketID='JIRA-2' />);
        expect(screen.getByTestId('ticket-text')).toHaveAttribute('data-has-max-width', 'true');
      });

      it('passes empty sx to Text when showAutoEllipsis=false', () => {
        render(<TicketLink ticketURL='' ticketID='JIRA-2' showAutoEllipsis={false} />);
        expect(screen.getByTestId('ticket-text')).toHaveAttribute('data-has-max-width', 'false');
      });
    });
  });
});
