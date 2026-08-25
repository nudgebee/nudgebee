import { render, screen, fireEvent, act } from '@testing-library/react';
import { SnackbarComponent } from '@shared/SnackbarComponent';
import { snackbar } from '@shared/snackbarService';

jest.mock('@utils/colors');

beforeEach(() => {
  jest.useFakeTimers();
});

afterEach(() => {
  jest.useRealTimers();
});

describe('SnackbarComponent', () => {
  describe('empty state', () => {
    it('renders nothing when no toasts', () => {
      render(<SnackbarComponent />);
      expect(screen.queryByRole('region')).not.toBeInTheDocument();
    });
  });

  describe('toast display', () => {
    it('shows toast when snackbar.success is called', () => {
      render(<SnackbarComponent />);
      act(() => {
        snackbar.success('Saved!');
      });
      expect(screen.getByText('Saved!')).toBeInTheDocument();
    });

    it('shows role=region with aria-label=Notifications', () => {
      render(<SnackbarComponent />);
      act(() => {
        snackbar.info('Info!');
      });
      expect(screen.getByRole('region', { name: 'Notifications' })).toBeInTheDocument();
    });

    it('shows role=alert for error toasts', () => {
      render(<SnackbarComponent />);
      act(() => {
        snackbar.error('Error!');
      });
      expect(screen.getByRole('alert')).toBeInTheDocument();
    });

    it('shows role=status for success toasts', () => {
      render(<SnackbarComponent />);
      act(() => {
        snackbar.success('Success!');
      });
      expect(screen.getByRole('status')).toBeInTheDocument();
    });

    it('shows role=status for warning toasts', () => {
      render(<SnackbarComponent />);
      act(() => {
        snackbar.warning('Watch out!');
      });
      expect(screen.getByRole('status')).toBeInTheDocument();
    });
  });

  describe('dismiss', () => {
    it('renders dismiss button with aria-label=Dismiss notification', () => {
      render(<SnackbarComponent />);
      act(() => {
        snackbar.success('Done!');
      });
      expect(screen.getByLabelText('Dismiss notification')).toBeInTheDocument();
    });

    it('removes toast after dismiss click + exit animation', () => {
      render(<SnackbarComponent />);
      act(() => {
        snackbar.success('Done!');
      });
      fireEvent.click(screen.getByLabelText('Dismiss notification'));
      act(() => {
        jest.advanceTimersByTime(400);
      });
      expect(screen.queryByText('Done!')).not.toBeInTheDocument();
    });
  });

  describe('max visible toasts', () => {
    it('shows at most 6 toasts', () => {
      render(<SnackbarComponent />);
      act(() => {
        snackbar.success('Toast 1');
        snackbar.info('Toast 2');
        snackbar.warning('Toast 3');
        snackbar.error('Toast 4');
        snackbar.success('Toast 5');
        snackbar.info('Toast 6');
        snackbar.warning('Toast 7');
        snackbar.error('Toast 8');
      });
      const alerts = screen.queryAllByRole('alert');
      const statuses = screen.queryAllByRole('status');
      expect(alerts.length + statuses.length).toBe(6);
    });
  });

  describe('auto-dismiss', () => {
    it('removes success toast after 3000ms', () => {
      render(<SnackbarComponent />);
      act(() => {
        snackbar.success('Auto-dismiss me');
      });
      expect(screen.getByText('Auto-dismiss me')).toBeInTheDocument();
      act(() => {
        jest.advanceTimersByTime(3000 + 400);
      });
      expect(screen.queryByText('Auto-dismiss me')).not.toBeInTheDocument();
    });
  });
});
