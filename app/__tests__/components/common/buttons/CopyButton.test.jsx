import { render, screen, fireEvent, act } from '@testing-library/react';
import CopyButton from '@shared/buttons/CopyButton';

jest.mock('@ui/Button', () => ({
  Button: ({ 'aria-label': ariaLabel, onClick, icon }) => (
    <button aria-label={ariaLabel} onClick={onClick} data-testid='copy-btn'>
      {icon}
    </button>
  ),
}));

jest.mock('@shared/icons/SafeIcon', () => ({
  __esModule: true,
  default: ({ alt }) => <span data-testid='icon'>{alt}</span>,
}));

jest.mock('@assets', () => ({ CopyIcon: 'copy-icon.svg' }));

jest.mock('@ui/Toast', () => ({ toast: { success: jest.fn(), error: jest.fn() } }));

// eslint-disable-next-line import/first
import { toast as mockToast } from '@ui/Toast';

let originalClipboard;

beforeEach(() => {
  mockToast.success.mockClear();
  mockToast.error.mockClear();
  jest.useFakeTimers();
  originalClipboard = Object.getOwnPropertyDescriptor(navigator, 'clipboard');
  Object.defineProperty(navigator, 'clipboard', {
    value: { writeText: jest.fn() },
    writable: true,
    configurable: true,
  });
});

afterEach(() => {
  jest.useRealTimers();
  if (originalClipboard) {
    Object.defineProperty(navigator, 'clipboard', originalClipboard);
  }
});

describe('CopyButton', () => {
  describe('initial state', () => {
    it('renders with aria-label=Copy initially', () => {
      render(<CopyButton text='hello' />);
      expect(screen.getByLabelText('Copy')).toBeInTheDocument();
    });

    it('shows CopyIcon initially', () => {
      render(<CopyButton text='hello' />);
      expect(screen.getByText('Copy')).toBeInTheDocument();
    });
  });

  describe('on successful copy', () => {
    it('copies the text to clipboard', async () => {
      navigator.clipboard.writeText.mockResolvedValue();
      render(<CopyButton text='hello world' />);
      await act(async () => {
        fireEvent.click(screen.getByTestId('copy-btn'));
      });
      expect(navigator.clipboard.writeText).toHaveBeenCalledWith('hello world');
    });

    it('changes aria-label to Copied after successful copy', async () => {
      navigator.clipboard.writeText.mockResolvedValue();
      render(<CopyButton text='hello' />);
      await act(async () => {
        fireEvent.click(screen.getByTestId('copy-btn'));
      });
      expect(screen.getByLabelText('Copied')).toBeInTheDocument();
    });

    it('reverts aria-label back to Copy after 2 seconds', async () => {
      navigator.clipboard.writeText.mockResolvedValue();
      render(<CopyButton text='hello' />);
      await act(async () => {
        fireEvent.click(screen.getByTestId('copy-btn'));
      });
      act(() => {
        jest.advanceTimersByTime(2000);
      });
      expect(screen.getByLabelText('Copy')).toBeInTheDocument();
    });

    it('shows toast when toastMessage is provided', async () => {
      navigator.clipboard.writeText.mockResolvedValue();
      render(<CopyButton text='hello' toastMessage='Copied!' />);
      await act(async () => {
        fireEvent.click(screen.getByTestId('copy-btn'));
      });
      expect(mockToast.success).toHaveBeenCalledWith('Copied!');
    });

    it('does not show toast when toastMessage is not provided', async () => {
      navigator.clipboard.writeText.mockResolvedValue();
      render(<CopyButton text='hello' />);
      await act(async () => {
        fireEvent.click(screen.getByTestId('copy-btn'));
      });
      expect(mockToast.success).not.toHaveBeenCalled();
    });
  });

  describe('on copy failure', () => {
    it('shows error toast when clipboard write fails', async () => {
      navigator.clipboard.writeText.mockRejectedValue(new Error('denied'));
      render(<CopyButton text='hello' />);
      await act(async () => {
        fireEvent.click(screen.getByTestId('copy-btn'));
      });
      expect(mockToast.error).toHaveBeenCalledWith('Failed to copy to clipboard');
    });
  });
});
