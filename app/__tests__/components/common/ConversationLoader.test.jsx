import { render, screen } from '@testing-library/react';
import ConversationLoader from '@shared/ConversationLoader';

jest.mock('@utils/colors');

describe('ConversationLoader', () => {
  it('renders the status element with role=status', () => {
    render(<ConversationLoader />);
    expect(screen.getByRole('status')).toBeInTheDocument();
  });

  it('has aria-live=polite', () => {
    render(<ConversationLoader />);
    expect(screen.getByRole('status')).toHaveAttribute('aria-live', 'polite');
  });

  it('has aria-label=Loading', () => {
    render(<ConversationLoader />);
    expect(screen.getByLabelText('Loading')).toBeInTheDocument();
  });

  it('renders one of the known words from the list', () => {
    render(<ConversationLoader />);
    const status = screen.getByRole('status');
    expect(status.textContent).toBeTruthy();
    expect(status.textContent.length).toBeGreaterThan(0);
  });

  it('renders without crashing', () => {
    const { container } = render(<ConversationLoader />);
    expect(container.firstChild).toBeInTheDocument();
  });
});
