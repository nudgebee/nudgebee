import { render, screen, fireEvent } from '@testing-library/react';
import NotifyBanner from '@shared/NotifyBanner';

jest.mock('@ui/Button', () => ({
  Button: ({ children, onClick, id }) => (
    <button id={id} onClick={onClick}>
      {children}
    </button>
  ),
}));

jest.mock('@utils/colors');

describe('NotifyBanner', () => {
  describe('visibility', () => {
    it('renders nothing when visible=false', () => {
      render(<NotifyBanner visible={false} onEnable={jest.fn()} onDismiss={jest.fn()} />);
      expect(screen.queryByTestId('notify-banner')).not.toBeInTheDocument();
    });

    it('renders banner when visible=true', () => {
      render(<NotifyBanner visible={true} onEnable={jest.fn()} onDismiss={jest.fn()} />);
      expect(screen.getByTestId('notify-banner')).toBeInTheDocument();
    });
  });

  describe('message', () => {
    it('shows default message when no message prop is passed', () => {
      render(<NotifyBanner visible={true} onEnable={jest.fn()} onDismiss={jest.fn()} />);
      expect(screen.getByText('Want to be notified when your investigation completes?')).toBeInTheDocument();
    });

    it('shows custom message when message prop is provided', () => {
      render(<NotifyBanner visible={true} onEnable={jest.fn()} onDismiss={jest.fn()} message='Custom message' />);
      expect(screen.getByText('Custom message')).toBeInTheDocument();
    });
  });

  describe('callbacks', () => {
    it('calls onEnable when Notify button is clicked', () => {
      const onEnable = jest.fn();
      render(<NotifyBanner visible={true} onEnable={onEnable} onDismiss={jest.fn()} />);
      fireEvent.click(screen.getByText('Notify'));
      expect(onEnable).toHaveBeenCalledTimes(1);
    });

    it('calls onDismiss when dismiss button is clicked', () => {
      const onDismiss = jest.fn();
      render(<NotifyBanner visible={true} onEnable={jest.fn()} onDismiss={onDismiss} />);
      fireEvent.click(screen.getByTestId('notify-dismiss-btn'));
      expect(onDismiss).toHaveBeenCalledTimes(1);
    });
  });

  describe('elements', () => {
    it('renders Notify button with id=notify-btn', () => {
      render(<NotifyBanner visible={true} onEnable={jest.fn()} onDismiss={jest.fn()} />);
      expect(screen.getByText('Notify').closest('[id="notify-btn"]')).toBeInTheDocument();
    });

    it('renders dismiss button with accessible label', () => {
      render(<NotifyBanner visible={true} onEnable={jest.fn()} onDismiss={jest.fn()} />);
      expect(screen.getByLabelText('Dismiss notification banner')).toBeInTheDocument();
    });
  });
});
