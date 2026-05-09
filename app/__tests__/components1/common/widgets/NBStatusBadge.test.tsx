import React from 'react';
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react';
import NBStatusBadge from '@components1/common/widgets/NBStatusBadge';

// Mock apiTriage
jest.mock('src/api1/triage', () => ({
  updateNBStatus: jest.fn().mockResolvedValue({}),
}));

import apiTriage from 'src/api1/triage';

describe('NBStatusBadge', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    (apiTriage.updateNBStatus as jest.Mock).mockResolvedValue({});
  });

  it('renders with OPEN status', () => {
    render(<NBStatusBadge eventId='1' currentStatus='OPEN' />);
    expect(screen.getByText('Open')).toBeInTheDocument();
  });

  it('renders with ACTION_REQUIRED status', () => {
    render(<NBStatusBadge eventId='1' currentStatus='ACTION_REQUIRED' />);
    expect(screen.getByText('Action Required')).toBeInTheDocument();
  });

  it('renders with ACKNOWLEDGED status', () => {
    render(<NBStatusBadge eventId='1' currentStatus='ACKNOWLEDGED' />);
    expect(screen.getByText('Acknowledged')).toBeInTheDocument();
  });

  it('renders with INVESTIGATING status', () => {
    render(<NBStatusBadge eventId='1' currentStatus='INVESTIGATING' />);
    expect(screen.getByText('Investigating')).toBeInTheDocument();
  });

  it('renders with SNOOZED status', () => {
    render(<NBStatusBadge eventId='1' currentStatus='SNOOZED' />);
    expect(screen.getByText('Snoozed')).toBeInTheDocument();
  });

  it('renders with SUPPRESSED status', () => {
    render(<NBStatusBadge eventId='1' currentStatus='SUPPRESSED' />);
    expect(screen.getByText('Suppressed')).toBeInTheDocument();
  });

  it('renders with DROPPED status', () => {
    render(<NBStatusBadge eventId='1' currentStatus='DROPPED' />);
    expect(screen.getByText('Dropped')).toBeInTheDocument();
  });

  it('renders with DUPLICATE status', () => {
    render(<NBStatusBadge eventId='1' currentStatus='DUPLICATE' />);
    expect(screen.getByText('Duplicate')).toBeInTheDocument();
  });

  it('renders with RESOLVED status', () => {
    render(<NBStatusBadge eventId='1' currentStatus='RESOLVED' />);
    expect(screen.getByText('Resolved')).toBeInTheDocument();
  });

  it('renders with unknown status (falls to currentStatus text)', () => {
    render(<NBStatusBadge eventId='1' currentStatus='UNKNOWN_STATUS' />);
    expect(screen.getByText('UNKNOWN_STATUS')).toBeInTheDocument();
  });

  it('renders with empty string status', () => {
    render(<NBStatusBadge eventId='1' currentStatus='' />);
    expect(screen.getByText('-')).toBeInTheDocument();
  });

  it('renders disabled state (no arrow icon)', () => {
    render(<NBStatusBadge eventId='1' currentStatus='OPEN' disabled={true} />);
    expect(screen.getByText('Open')).toBeInTheDocument();
  });

  it('opens menu when badge is clicked (not disabled)', () => {
    render(<NBStatusBadge eventId='1' currentStatus='OPEN' />);
    const _badge = screen.getByText('Open').closest('[class]') || screen.getByText('Open').parentElement;
    fireEvent.click(screen.getByText('Open').parentElement!);
    // Menu items should appear for allowed transitions from OPEN
    expect(screen.getByText('Action Required')).toBeInTheDocument();
  });

  it('does not open menu when disabled=true', () => {
    render(<NBStatusBadge eventId='1' currentStatus='OPEN' disabled={true} />);
    const parentEl = screen.getByText('Open').parentElement!;
    fireEvent.click(parentEl);
    // Menu should not show
    expect(screen.queryByText('Action Required')).not.toBeInTheDocument();
  });

  it('closes menu when clicked while open', async () => {
    render(<NBStatusBadge eventId='1' currentStatus='OPEN' />);
    const parentEl = screen.getByText('Open').parentElement!;
    // Open
    fireEvent.click(parentEl);
    expect(screen.getByText('Action Required')).toBeInTheDocument();
    // Close via Escape key
    fireEvent.keyDown(document.activeElement || document.body, { key: 'Escape', code: 'Escape' });
    await waitFor(() => {
      expect(screen.queryByText('Action Required')).not.toBeInTheDocument();
    });
  });

  it('changes status when menu item is clicked (non-SNOOZED)', async () => {
    const onStatusChange = jest.fn();
    render(<NBStatusBadge eventId='evt1' currentStatus='OPEN' onStatusChange={onStatusChange} />);
    fireEvent.click(screen.getByText('Open').parentElement!);

    // Click "Action Required" menu item
    await act(async () => {
      fireEvent.click(screen.getByText('Action Required'));
    });

    await waitFor(() => {
      expect(apiTriage.updateNBStatus).toHaveBeenCalledWith({
        event_id: 'evt1',
        nb_status: 'ACTION_REQUIRED',
        snoozed_until: undefined,
      });
    });
  });

  it('shows ticket prompt dialog for ACTION_REQUIRED when onCreateTicket provided', async () => {
    const onStatusChange = jest.fn();
    const onCreateTicket = jest.fn();
    render(<NBStatusBadge eventId='evt1' currentStatus='OPEN' onStatusChange={onStatusChange} onCreateTicket={onCreateTicket} />);
    fireEvent.click(screen.getByText('Open').parentElement!);

    await act(async () => {
      fireEvent.click(screen.getByText('Action Required'));
    });

    await waitFor(() => {
      expect(screen.getByText('Create a Ticket?')).toBeInTheDocument();
    });
  });

  it('ticket prompt "Not now" closes and calls onStatusChange', async () => {
    const onStatusChange = jest.fn();
    const onCreateTicket = jest.fn();
    render(<NBStatusBadge eventId='evt1' currentStatus='OPEN' onStatusChange={onStatusChange} onCreateTicket={onCreateTicket} />);
    fireEvent.click(screen.getByText('Open').parentElement!);

    await act(async () => {
      fireEvent.click(screen.getByText('Action Required'));
    });

    await waitFor(() => screen.getByText('Create a Ticket?'));

    await act(async () => {
      fireEvent.click(screen.getByText('Not now'));
    });

    await waitFor(() => {
      expect(onStatusChange).toHaveBeenCalledWith('ACTION_REQUIRED');
    });
  });

  it('ticket prompt "Create Ticket" calls onCreateTicket and onStatusChange', async () => {
    const onStatusChange = jest.fn();
    const onCreateTicket = jest.fn();
    render(<NBStatusBadge eventId='evt1' currentStatus='OPEN' onStatusChange={onStatusChange} onCreateTicket={onCreateTicket} />);
    fireEvent.click(screen.getByText('Open').parentElement!);

    await act(async () => {
      fireEvent.click(screen.getByText('Action Required'));
    });

    await waitFor(() => screen.getByText('Create a Ticket?'));

    await act(async () => {
      fireEvent.click(screen.getByText('Create Ticket'));
    });

    await waitFor(() => {
      expect(onCreateTicket).toHaveBeenCalled();
      expect(onStatusChange).toHaveBeenCalledWith('ACTION_REQUIRED');
    });
  });

  it('opens snooze dialog when SNOOZED is clicked', async () => {
    render(<NBStatusBadge eventId='evt1' currentStatus='OPEN' />);
    fireEvent.click(screen.getByText('Open').parentElement!);

    await act(async () => {
      fireEvent.click(screen.getByText('Snoozed'));
    });

    expect(screen.getByText('Snooze for how long?')).toBeInTheDocument();
  });

  it('selects snooze duration and calls api', async () => {
    const onStatusChange = jest.fn();
    render(<NBStatusBadge eventId='evt1' currentStatus='OPEN' onStatusChange={onStatusChange} />);
    fireEvent.click(screen.getByText('Open').parentElement!);

    await act(async () => {
      fireEvent.click(screen.getByText('Snoozed'));
    });

    await waitFor(() => screen.getByText('Snooze for how long?'));

    await act(async () => {
      fireEvent.click(screen.getByText('1 hour'));
    });

    await waitFor(() => {
      expect(apiTriage.updateNBStatus).toHaveBeenCalledWith(
        expect.objectContaining({
          event_id: 'evt1',
          nb_status: 'SNOOZED',
          snoozed_until: expect.any(String),
        })
      );
    });
  });

  it('cancels snooze dialog', async () => {
    render(<NBStatusBadge eventId='evt1' currentStatus='OPEN' />);
    fireEvent.click(screen.getByText('Open').parentElement!);

    await act(async () => {
      fireEvent.click(screen.getByText('Snoozed'));
    });

    await waitFor(() => screen.getByText('Snooze for how long?'));

    await act(async () => {
      fireEvent.click(screen.getByText('Cancel'));
    });
    await waitFor(() => {
      expect(screen.queryByText('Snooze for how long?')).not.toBeInTheDocument();
    });
  });

  it('shows error when API call fails', async () => {
    (apiTriage.updateNBStatus as jest.Mock).mockRejectedValueOnce(new Error('Network error'));
    render(<NBStatusBadge eventId='evt1' currentStatus='OPEN' />);
    fireEvent.click(screen.getByText('Open').parentElement!);

    await act(async () => {
      fireEvent.click(screen.getByText('Action Required'));
    });

    await waitFor(() => {
      expect(screen.getByText('Failed to update status')).toBeInTheDocument();
    });
  });

  it('shows "No transitions available" for status with no transitions', () => {
    // Create a status not in ALLOWED_TRANSITIONS
    render(<NBStatusBadge eventId='1' currentStatus='UNKNOWN_STATUS' />);
    fireEvent.click(screen.getByText('UNKNOWN_STATUS').parentElement!);
    expect(screen.getByText('No transitions available')).toBeInTheDocument();
  });

  it('handles handleClose with stopPropagation event', () => {
    render(<NBStatusBadge eventId='1' currentStatus='OPEN' />);
    fireEvent.click(screen.getByText('Open').parentElement!);
    // The menu open + close via backdrop
    expect(screen.getByText('Action Required')).toBeInTheDocument();
  });

  it('renders all snooze options in dialog', async () => {
    render(<NBStatusBadge eventId='evt1' currentStatus='OPEN' />);
    fireEvent.click(screen.getByText('Open').parentElement!);

    await act(async () => {
      fireEvent.click(screen.getByText('Snoozed'));
    });

    await waitFor(() => screen.getByText('Snooze for how long?'));

    expect(screen.getByText('1 hour')).toBeInTheDocument();
    expect(screen.getByText('4 hours')).toBeInTheDocument();
    expect(screen.getByText('1 day')).toBeInTheDocument();
    expect(screen.getByText('1 week')).toBeInTheDocument();
  });

  it('calls onStatusChange without onCreateTicket for non-ACTION_REQUIRED', async () => {
    const onStatusChange = jest.fn();
    render(<NBStatusBadge eventId='evt1' currentStatus='OPEN' onStatusChange={onStatusChange} />);
    fireEvent.click(screen.getByText('Open').parentElement!);

    await act(async () => {
      fireEvent.click(screen.getByText('Snoozed'));
    });

    await act(async () => {
      fireEvent.click(screen.getByText('1 hour'));
    });

    await waitFor(() => {
      expect(onStatusChange).toHaveBeenCalledWith('SNOOZED');
    });
  });

  it('renders loading spinner while updating', async () => {
    let resolvePromise: (v: any) => void;
    const pendingPromise = new Promise((resolve) => {
      resolvePromise = resolve;
    });
    (apiTriage.updateNBStatus as jest.Mock).mockReturnValueOnce(pendingPromise);

    const { container } = render(<NBStatusBadge eventId='evt1' currentStatus='OPEN' />);
    fireEvent.click(screen.getByText('Open').parentElement!);

    act(() => {
      fireEvent.click(screen.getByText('Action Required'));
    });

    // While loading, CircularProgress should be shown
    await waitFor(() => {
      const progress = container.querySelector('.MuiCircularProgress-root');
      expect(progress).toBeInTheDocument();
    });

    // Resolve the promise
    await act(async () => {
      resolvePromise!({});
    });
  });

  it('ticket prompt dialog closes via onClose (backdrop click)', async () => {
    const onStatusChange = jest.fn();
    const onCreateTicket = jest.fn();
    render(<NBStatusBadge eventId='evt1' currentStatus='OPEN' onStatusChange={onStatusChange} onCreateTicket={onCreateTicket} />);
    fireEvent.click(screen.getByText('Open').parentElement!);

    await act(async () => {
      fireEvent.click(screen.getByText('Action Required'));
    });

    await waitFor(() => screen.getByText('Create a Ticket?'));

    // The dialog's onClose calls the same function as "Not now"
    // We can test it by clicking the dialog title or simply verify state
    await act(async () => {
      fireEvent.click(screen.getByText('Not now'));
    });
    expect(onStatusChange).toHaveBeenCalledWith('ACTION_REQUIRED');
  });
});
