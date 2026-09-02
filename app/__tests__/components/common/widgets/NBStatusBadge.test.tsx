import { render, screen, fireEvent, act, waitFor } from '@testing-library/react';
import NBStatusBadge from '@shared/widgets/NBStatusBadge';

jest.mock('src/api1/triage', () => ({
  __esModule: true,
  default: {
    updateNBStatus: jest.fn(),
  },
}));

jest.mock('@shared/snackbarService', () => ({
  snackbar: { error: jest.fn(), success: jest.fn() },
}));

jest.mock('@ui/Label', () => ({
  Label: ({ text, showDropdownArrow }: any) => (
    <span data-testid='status-label' data-arrow={String(showDropdownArrow ?? false)}>
      {text}
    </span>
  ),
}));

jest.mock('@ui/DropdownMenu', () => ({
  DropdownMenu: ({ trigger, items }: any) => (
    <div data-testid='dropdown-menu'>
      {trigger}
      <ul>
        {items.map((item: any) => (
          <li key={item.id}>
            <button data-testid={`item-${item.id}`} onClick={item.onSelect} disabled={item.disabled}>
              {item.label}
            </button>
          </li>
        ))}
      </ul>
    </div>
  ),
}));

jest.mock('@ui/Modal', () => ({
  Modal: ({ open, title, children, handleClose, onConfirm, confirmText }: any) =>
    open ? (
      <div data-testid='modal' data-title={title}>
        <button data-testid='modal-close' onClick={handleClose} />
        {onConfirm && (
          <button data-testid='modal-confirm' onClick={onConfirm}>
            {confirmText}
          </button>
        )}
        {children}
      </div>
    ) : null,
}));

jest.mock('@ui/Button', () => ({
  Button: ({ children, onClick }: any) => (
    <button onClick={onClick} data-testid='btn'>
      {children}
    </button>
  ),
}));

jest.mock('@ui/Tooltip', () => ({
  __esModule: true,
  default: ({ children, title, open }: any) => (
    <>
      {open && <div data-testid='tooltip-content'>{title}</div>}
      {children}
    </>
  ),
}));

jest.mock('@utils/colors');

import apiTriage from 'src/api1/triage';
import { snackbar } from '@shared/snackbarService';

const mockUpdateNBStatus = apiTriage.updateNBStatus as jest.Mock;
const mockError = snackbar.error as jest.Mock;

beforeEach(() => {
  jest.clearAllMocks();
});

describe('NBStatusBadge', () => {
  describe('rendering', () => {
    it('renders the status label', () => {
      render(<NBStatusBadge eventId='evt1' currentStatus='OPEN' />);
      expect(screen.getByTestId('status-label')).toHaveTextContent('Open');
    });

    it('renders label for RESOLVED status', () => {
      render(<NBStatusBadge eventId='evt1' currentStatus='RESOLVED' />);
      expect(screen.getByTestId('status-label')).toHaveTextContent('Resolved');
    });

    it('renders label for ACTION_REQUIRED status', () => {
      render(<NBStatusBadge eventId='evt1' currentStatus='ACTION_REQUIRED' />);
      expect(screen.getByTestId('status-label')).toHaveTextContent('Action Required');
    });

    it('renders dropdown menu when not disabled and has transitions', () => {
      render(<NBStatusBadge eventId='evt1' currentStatus='OPEN' />);
      expect(screen.getByTestId('dropdown-menu')).toBeInTheDocument();
    });

    it('does not render dropdown menu when disabled', () => {
      render(<NBStatusBadge eventId='evt1' currentStatus='OPEN' disabled={true} />);
      expect(screen.queryByTestId('dropdown-menu')).not.toBeInTheDocument();
    });
  });

  describe('status change', () => {
    it('opens reason dialog when a non-snooze transition is selected', async () => {
      render(<NBStatusBadge eventId='evt1' currentStatus='OPEN' />);
      await act(async () => {
        fireEvent.click(screen.getByTestId('item-ACTION_REQUIRED'));
      });
      expect(screen.getByTestId('modal')).toHaveAttribute('data-title', expect.stringContaining('Action Required'));
    });

    it('calls updateNBStatus after confirming the reason dialog', async () => {
      mockUpdateNBStatus.mockResolvedValue({ success: true });
      render(<NBStatusBadge eventId='evt1' currentStatus='OPEN' onStatusChange={jest.fn()} />);
      await act(async () => {
        fireEvent.click(screen.getByTestId('item-RESOLVED'));
      });
      await act(async () => {
        fireEvent.click(screen.getByTestId('modal-confirm'));
      });
      expect(mockUpdateNBStatus).toHaveBeenCalledWith(expect.objectContaining({ nb_status: 'RESOLVED' }));
    });

    it('does not call updateNBStatus when reason dialog is dismissed', async () => {
      render(<NBStatusBadge eventId='evt1' currentStatus='OPEN' />);
      await act(async () => {
        fireEvent.click(screen.getByTestId('item-ACTION_REQUIRED'));
      });
      fireEvent.click(screen.getByTestId('modal-close'));
      expect(mockUpdateNBStatus).not.toHaveBeenCalled();
    });
  });

  describe('optimistic update', () => {
    it('calls onStatusChange immediately when reason dialog is confirmed — before API resolves', async () => {
      // Never resolves — proves the optimistic call happens before any API response
      mockUpdateNBStatus.mockReturnValue(new Promise(() => {}));
      const onStatusChange = jest.fn();
      render(<NBStatusBadge eventId='evt1' currentStatus='OPEN' onStatusChange={onStatusChange} />);
      await act(async () => {
        fireEvent.click(screen.getByTestId('item-RESOLVED'));
      });
      // Confirm reason dialog synchronously
      act(() => {
        fireEvent.click(screen.getByTestId('modal-confirm'));
      });
      expect(onStatusChange).toHaveBeenCalledTimes(1);
      expect(onStatusChange).toHaveBeenCalledWith('RESOLVED', undefined);
    });

    it('does not call onStatusChange again after a successful API response', async () => {
      mockUpdateNBStatus.mockResolvedValue({ success: true });
      const onStatusChange = jest.fn();
      render(<NBStatusBadge eventId='evt1' currentStatus='OPEN' onStatusChange={onStatusChange} />);
      await act(async () => {
        fireEvent.click(screen.getByTestId('item-RESOLVED'));
      });
      await act(async () => {
        fireEvent.click(screen.getByTestId('modal-confirm'));
      });
      expect(onStatusChange).toHaveBeenCalledTimes(1);
      expect(onStatusChange).toHaveBeenCalledWith('RESOLVED', undefined);
    });

    it('reverts by calling onStatusChange with the previous status when API throws', async () => {
      mockUpdateNBStatus.mockRejectedValue(new Error('network error'));
      const onStatusChange = jest.fn();
      render(<NBStatusBadge eventId='evt1' currentStatus='OPEN' onStatusChange={onStatusChange} />);
      await act(async () => {
        fireEvent.click(screen.getByTestId('item-RESOLVED'));
      });
      await act(async () => {
        fireEvent.click(screen.getByTestId('modal-confirm'));
      });
      await waitFor(() => expect(onStatusChange).toHaveBeenCalledTimes(2));
      expect(onStatusChange).toHaveBeenNthCalledWith(1, 'RESOLVED', undefined); // optimistic
      expect(onStatusChange).toHaveBeenNthCalledWith(2, 'OPEN', undefined); // revert
    });

    it('reverts snoozedUntil to previous prop value when API throws', async () => {
      mockUpdateNBStatus.mockRejectedValue(new Error('network error'));
      const onStatusChange = jest.fn();
      render(<NBStatusBadge eventId='evt1' currentStatus='SNOOZED' snoozedUntil='2025-01-01T00:00:00Z' onStatusChange={onStatusChange} />);
      await act(async () => {
        fireEvent.click(screen.getByTestId('item-RESOLVED'));
      });
      await act(async () => {
        fireEvent.click(screen.getByTestId('modal-confirm'));
      });
      await waitFor(() => expect(onStatusChange).toHaveBeenCalledTimes(2));
      expect(onStatusChange).toHaveBeenNthCalledWith(1, 'RESOLVED', undefined); // optimistic
      expect(onStatusChange).toHaveBeenNthCalledWith(2, 'SNOOZED', '2025-01-01T00:00:00Z'); // revert with old snoozedUntil
    });

    it('shows error snackbar when API throws', async () => {
      mockUpdateNBStatus.mockRejectedValue(new Error('network error'));
      render(<NBStatusBadge eventId='evt1' currentStatus='OPEN' />);
      await act(async () => {
        fireEvent.click(screen.getByTestId('item-RESOLVED'));
      });
      await act(async () => {
        fireEvent.click(screen.getByTestId('modal-confirm'));
      });
      await waitFor(() => expect(mockError).toHaveBeenCalledWith('Failed to update status'));
    });
  });

  describe('SNOOZED status', () => {
    it('opens snooze dialog when SNOOZED transition is selected', async () => {
      render(<NBStatusBadge eventId='evt1' currentStatus='OPEN' />);
      await act(async () => {
        fireEvent.click(screen.getByTestId('item-SNOOZED'));
      });
      expect(screen.getByTestId('modal')).toBeInTheDocument();
      expect(screen.getByTestId('modal')).toHaveAttribute('data-title', 'Snooze for how long?');
    });

    it('closes snooze dialog on modal close', async () => {
      render(<NBStatusBadge eventId='evt1' currentStatus='OPEN' />);
      await act(async () => {
        fireEvent.click(screen.getByTestId('item-SNOOZED'));
      });
      fireEvent.click(screen.getByTestId('modal-close'));
      expect(screen.queryByTestId('modal')).not.toBeInTheDocument();
    });
  });

  describe('ticket prompt (ACTION_REQUIRED with onCreateTicket)', () => {
    it('opens ticket prompt immediately after confirming ACTION_REQUIRED (optimistic — no API wait)', async () => {
      mockUpdateNBStatus.mockReturnValue(new Promise(() => {})); // never resolves
      const onStatusChange = jest.fn();
      render(<NBStatusBadge eventId='evt1' currentStatus='OPEN' onStatusChange={onStatusChange} onCreateTicket={jest.fn()} />);
      await act(async () => {
        fireEvent.click(screen.getByTestId('item-ACTION_REQUIRED'));
      });
      act(() => {
        fireEvent.click(screen.getByTestId('modal-confirm'));
      });
      // Status updated optimistically before the API responds
      expect(onStatusChange).toHaveBeenCalledWith('ACTION_REQUIRED', undefined);
      // Ticket prompt is already open
      expect(screen.getByTestId('modal')).toHaveAttribute('data-title', 'Create a Ticket?');
    });

    it('calls onCreateTicket when user confirms the ticket prompt', async () => {
      mockUpdateNBStatus.mockResolvedValue({ success: true });
      const onCreateTicket = jest.fn();
      render(<NBStatusBadge eventId='evt1' currentStatus='OPEN' onStatusChange={jest.fn()} onCreateTicket={onCreateTicket} />);
      await act(async () => {
        fireEvent.click(screen.getByTestId('item-ACTION_REQUIRED'));
      });
      // Confirm reason dialog — ticket prompt opens
      await act(async () => {
        fireEvent.click(screen.getByTestId('modal-confirm'));
      });
      // Confirm ticket prompt
      await act(async () => {
        fireEvent.click(screen.getByTestId('modal-confirm'));
      });
      expect(onCreateTicket).toHaveBeenCalledTimes(1);
    });

    it('does not call onCreateTicket when user dismisses the ticket prompt', async () => {
      mockUpdateNBStatus.mockResolvedValue({ success: true });
      const onCreateTicket = jest.fn();
      render(<NBStatusBadge eventId='evt1' currentStatus='OPEN' onStatusChange={jest.fn()} onCreateTicket={onCreateTicket} />);
      await act(async () => {
        fireEvent.click(screen.getByTestId('item-ACTION_REQUIRED'));
      });
      await act(async () => {
        fireEvent.click(screen.getByTestId('modal-confirm'));
      });
      await act(async () => {
        fireEvent.click(screen.getByTestId('modal-close'));
      });
      expect(onCreateTicket).not.toHaveBeenCalled();
    });

    it('closes ticket prompt and reverts status when API throws', async () => {
      mockUpdateNBStatus.mockRejectedValue(new Error('network error'));
      const onStatusChange = jest.fn();
      render(<NBStatusBadge eventId='evt1' currentStatus='OPEN' onStatusChange={onStatusChange} onCreateTicket={jest.fn()} />);
      await act(async () => {
        fireEvent.click(screen.getByTestId('item-ACTION_REQUIRED'));
      });
      await act(async () => {
        fireEvent.click(screen.getByTestId('modal-confirm'));
      });
      await waitFor(() => expect(onStatusChange).toHaveBeenCalledTimes(2));
      // Ticket prompt should be gone
      expect(screen.queryByTestId('modal')).not.toBeInTheDocument();
      // Status reverted
      expect(onStatusChange).toHaveBeenNthCalledWith(2, 'OPEN', undefined);
    });

    it('does not show ticket prompt for ACTION_REQUIRED when onCreateTicket is not provided', async () => {
      mockUpdateNBStatus.mockResolvedValue({ success: true });
      render(<NBStatusBadge eventId='evt1' currentStatus='OPEN' onStatusChange={jest.fn()} />);
      await act(async () => {
        fireEvent.click(screen.getByTestId('item-ACTION_REQUIRED'));
      });
      await act(async () => {
        fireEvent.click(screen.getByTestId('modal-confirm'));
      });
      expect(screen.queryByTestId('modal')).not.toBeInTheDocument();
    });
  });

  describe('tooltipTitle prop', () => {
    it('does not render tooltip wrapper when tooltipTitle is not provided', () => {
      render(<NBStatusBadge eventId='evt1' currentStatus='OPEN' />);
      expect(screen.queryByTestId('nb-status-tooltip-wrapper')).not.toBeInTheDocument();
    });

    it('renders tooltip wrapper when tooltipTitle is provided', () => {
      render(<NBStatusBadge eventId='evt1' currentStatus='OPEN' tooltipTitle='Triage info' />);
      expect(screen.getByTestId('nb-status-tooltip-wrapper')).toBeInTheDocument();
    });

    it('tooltip content is hidden before hover', () => {
      render(<NBStatusBadge eventId='evt1' currentStatus='OPEN' tooltipTitle='Triage info' />);
      expect(screen.queryByTestId('tooltip-content')).not.toBeInTheDocument();
    });

    it('shows tooltip content on mouseEnter', () => {
      render(<NBStatusBadge eventId='evt1' currentStatus='OPEN' tooltipTitle='Triage info' />);
      fireEvent.mouseEnter(screen.getByTestId('nb-status-tooltip-wrapper'));
      expect(screen.getByTestId('tooltip-content')).toHaveTextContent('Triage info');
    });

    it('hides tooltip content on mouseLeave', () => {
      render(<NBStatusBadge eventId='evt1' currentStatus='OPEN' tooltipTitle='Triage info' />);
      const wrapper = screen.getByTestId('nb-status-tooltip-wrapper');
      fireEvent.mouseEnter(wrapper);
      expect(screen.getByTestId('tooltip-content')).toBeInTheDocument();
      fireEvent.mouseLeave(wrapper);
      expect(screen.queryByTestId('tooltip-content')).not.toBeInTheDocument();
    });

    it('hides tooltip content on mouseDown (dropdown click)', () => {
      render(<NBStatusBadge eventId='evt1' currentStatus='OPEN' tooltipTitle='Triage info' />);
      const wrapper = screen.getByTestId('nb-status-tooltip-wrapper');
      fireEvent.mouseEnter(wrapper);
      expect(screen.getByTestId('tooltip-content')).toBeInTheDocument();
      fireEvent.mouseDown(wrapper);
      expect(screen.queryByTestId('tooltip-content')).not.toBeInTheDocument();
    });
  });

  describe('disableSnoozeTooltip prop', () => {
    it('accepts disableSnoozeTooltip without error', () => {
      expect(() =>
        render(<NBStatusBadge eventId='evt1' currentStatus='SNOOZED' snoozedUntil='2025-01-01T00:00:00Z' disableSnoozeTooltip />)
      ).not.toThrow();
    });
  });
});
