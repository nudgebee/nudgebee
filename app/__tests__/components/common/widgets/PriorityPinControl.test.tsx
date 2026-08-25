import { render, screen, fireEvent, act, waitFor } from '@testing-library/react';
import PriorityPinControl from '@shared/widgets/PriorityPinControl';

jest.mock('@api1/triage', () => ({
  __esModule: true,
  default: {
    classifyEvent: jest.fn(),
  },
}));

jest.mock('@shared/snackbarService', () => ({
  snackbar: { success: jest.fn(), error: jest.fn() },
}));

jest.mock('@ui/DropdownMenu', () => ({
  DropdownMenu: ({ trigger, items }: any) => (
    <div data-testid='dropdown-menu'>
      {trigger}
      <ul>
        {items.map((item: any) => (
          <li key={item.id}>
            <button data-testid={`priority-option-${item.id}`} data-active={String(item.active ?? false)} onClick={item.onSelect}>
              {item.label}
            </button>
          </li>
        ))}
      </ul>
    </div>
  ),
}));

jest.mock('@ui/Button', () => ({
  Button: ({ children, onClick, tooltip, 'aria-label': ariaLabel }: any) => (
    <button data-testid='pin-trigger' aria-label={ariaLabel} title={tooltip} onClick={onClick}>
      {children}
    </button>
  ),
}));

jest.mock('@utils/colors');

import apiTriage from '@api1/triage';
import { snackbar } from '@shared/snackbarService';

const mockClassify = apiTriage.classifyEvent as jest.Mock;
const mockSuccess = snackbar.success as jest.Mock;
const mockError = snackbar.error as jest.Mock;

const DEFAULT_PROPS = {
  eventId: 'evt-1',
  accountId: 'acc-1',
  currentPriority: 'P2',
  currentScore: 47,
  canWrite: true,
};

beforeEach(() => {
  jest.clearAllMocks();
});

describe('PriorityPinControl', () => {
  describe('rendering', () => {
    it('renders the trigger button with the current priority', () => {
      render(<PriorityPinControl {...DEFAULT_PROPS} />);
      expect(screen.getByTestId('pin-trigger')).toHaveTextContent('P2');
    });

    it('renders "Set" when no currentPriority is given', () => {
      render(<PriorityPinControl {...DEFAULT_PROPS} currentPriority={null} />);
      expect(screen.getByTestId('pin-trigger')).toHaveTextContent('Set');
    });

    it('renders all four priority options in the dropdown', () => {
      render(<PriorityPinControl {...DEFAULT_PROPS} />);
      expect(screen.getByTestId('priority-option-P0')).toBeInTheDocument();
      expect(screen.getByTestId('priority-option-P1')).toBeInTheDocument();
      expect(screen.getByTestId('priority-option-P2')).toBeInTheDocument();
      expect(screen.getByTestId('priority-option-P3')).toBeInTheDocument();
    });

    it('marks the current priority as active and others as inactive', () => {
      render(<PriorityPinControl {...DEFAULT_PROPS} currentPriority='P2' />);
      expect(screen.getByTestId('priority-option-P2')).toHaveAttribute('data-active', 'true');
      expect(screen.getByTestId('priority-option-P0')).toHaveAttribute('data-active', 'false');
      expect(screen.getByTestId('priority-option-P1')).toHaveAttribute('data-active', 'false');
      expect(screen.getByTestId('priority-option-P3')).toHaveAttribute('data-active', 'false');
    });

    it('marks no option as active when currentPriority is null', () => {
      render(<PriorityPinControl {...DEFAULT_PROPS} currentPriority={null} />);
      ['P0', 'P1', 'P2', 'P3'].forEach((p) => {
        expect(screen.getByTestId(`priority-option-${p}`)).toHaveAttribute('data-active', 'false');
      });
    });

    it('returns null when canWrite is false', () => {
      const { container } = render(<PriorityPinControl {...DEFAULT_PROPS} canWrite={false} />);
      expect(container).toBeEmptyDOMElement();
    });

    it('returns null when eventId is missing', () => {
      const { container } = render(<PriorityPinControl {...DEFAULT_PROPS} eventId={null} />);
      expect(container).toBeEmptyDOMElement();
    });

    it('returns null when accountId is missing', () => {
      const { container } = render(<PriorityPinControl {...DEFAULT_PROPS} accountId={null} />);
      expect(container).toBeEmptyDOMElement();
    });

    it('does not call the API when the same priority is selected again', async () => {
      render(<PriorityPinControl {...DEFAULT_PROPS} currentPriority='P2' />);
      await act(async () => {
        fireEvent.click(screen.getByTestId('priority-option-P2'));
      });
      expect(mockClassify).not.toHaveBeenCalled();
    });
  });

  describe('optimistic update', () => {
    it('calls onChanged immediately with the new priority before the API resolves', async () => {
      // Never resolves during this test — verifies the optimistic call happens synchronously
      mockClassify.mockReturnValue(new Promise(() => {}));
      const onChanged = jest.fn();
      render(<PriorityPinControl {...DEFAULT_PROPS} onChanged={onChanged} />);
      fireEvent.click(screen.getByTestId('priority-option-P0'));
      expect(onChanged).toHaveBeenCalledTimes(1);
      expect(onChanged).toHaveBeenCalledWith('P0', 80);
    });

    it('does not call onChanged again after a successful API response', async () => {
      mockClassify.mockResolvedValue({ success: true });
      const onChanged = jest.fn();
      render(<PriorityPinControl {...DEFAULT_PROPS} onChanged={onChanged} />);
      await act(async () => {
        fireEvent.click(screen.getByTestId('priority-option-P0'));
      });
      expect(onChanged).toHaveBeenCalledTimes(1);
      expect(onChanged).toHaveBeenCalledWith('P0', 80);
    });

    // Must mirror priorityToMinScore in api-server/services/triage/scoring.go —
    // the optimistic score has to equal what the backend will persist.
    it.each([
      ['P0', 80],
      ['P1', 60],
      ['P3', 20],
    ])('reports %s with its band-floor score %i', async (priority, bandFloor) => {
      mockClassify.mockResolvedValue({ success: true });
      const onChanged = jest.fn();
      render(<PriorityPinControl {...DEFAULT_PROPS} onChanged={onChanged} />);
      await act(async () => {
        fireEvent.click(screen.getByTestId(`priority-option-${priority}`));
      });
      expect(onChanged).toHaveBeenCalledWith(priority, bandFloor);
    });
  });

  describe('successful pin', () => {
    beforeEach(() => {
      mockClassify.mockResolvedValue({ success: true });
    });

    it('calls classifyEvent with correct payload on selecting a priority', async () => {
      render(<PriorityPinControl {...DEFAULT_PROPS} />);
      await act(async () => {
        fireEvent.click(screen.getByTestId('priority-option-P1'));
      });
      expect(mockClassify).toHaveBeenCalledWith({
        event_id: 'evt-1',
        classification: 'true_positive',
        reason_code: 'correct_severity',
        corrected_priority: 'P1',
        apply_scope: 'this_fingerprint',
        apply_to_existing: true,
        confirmed: true,
        // A pin is a pure priority correction — it must never force an
        // nb_status transition, so preserve_status is always sent.
        preserve_status: true,
      });
    });

    it('does not show a success snackbar (UI already updated optimistically)', async () => {
      render(<PriorityPinControl {...DEFAULT_PROPS} />);
      await act(async () => {
        fireEvent.click(screen.getByTestId('priority-option-P0'));
      });
      expect(mockSuccess).not.toHaveBeenCalled();
    });

    it('uses apply_to_existing=false for this_event scope', async () => {
      render(<PriorityPinControl {...DEFAULT_PROPS} scope='this_event' />);
      await act(async () => {
        fireEvent.click(screen.getByTestId('priority-option-P1'));
      });
      expect(mockClassify).toHaveBeenCalledWith(expect.objectContaining({ apply_scope: 'this_event', apply_to_existing: false }));
    });
  });

  describe('failed pin — API returns success: false', () => {
    it('shows error snackbar when API returns success: false', async () => {
      mockClassify.mockResolvedValue({ success: false });
      render(<PriorityPinControl {...DEFAULT_PROPS} />);
      await act(async () => {
        fireEvent.click(screen.getByTestId('priority-option-P1'));
      });
      expect(mockError).toHaveBeenCalledWith('Failed to set priority');
      expect(mockSuccess).not.toHaveBeenCalled();
    });

    it('reverts by calling onChanged with the previous priority and score when API returns success: false', async () => {
      mockClassify.mockResolvedValue({ success: false });
      const onChanged = jest.fn();
      render(<PriorityPinControl {...DEFAULT_PROPS} currentPriority='P2' onChanged={onChanged} />);
      await act(async () => {
        fireEvent.click(screen.getByTestId('priority-option-P1'));
      });
      expect(onChanged).toHaveBeenCalledTimes(2);
      expect(onChanged).toHaveBeenNthCalledWith(1, 'P1', 60); // optimistic
      expect(onChanged).toHaveBeenNthCalledWith(2, 'P2', 47); // revert
    });

    it('reverts to nulls when there was no previous priority or score', async () => {
      mockClassify.mockResolvedValue({ success: false });
      const onChanged = jest.fn();
      render(<PriorityPinControl {...DEFAULT_PROPS} currentPriority={null} currentScore={null} onChanged={onChanged} />);
      await act(async () => {
        fireEvent.click(screen.getByTestId('priority-option-P0'));
      });
      expect(onChanged).toHaveBeenNthCalledWith(2, null, null);
    });
  });

  describe('failed pin — API throws', () => {
    it('shows error snackbar when classifyEvent rejects', async () => {
      mockClassify.mockRejectedValue(new Error('network error'));
      render(<PriorityPinControl {...DEFAULT_PROPS} />);
      await act(async () => {
        fireEvent.click(screen.getByTestId('priority-option-P0'));
      });
      await waitFor(() => {
        expect(mockError).toHaveBeenCalledWith('Failed to set priority');
      });
      expect(mockSuccess).not.toHaveBeenCalled();
    });

    it('reverts by calling onChanged with the previous priority and score when classifyEvent rejects', async () => {
      mockClassify.mockRejectedValue(new Error('network error'));
      const onChanged = jest.fn();
      render(<PriorityPinControl {...DEFAULT_PROPS} currentPriority='P2' onChanged={onChanged} />);
      await act(async () => {
        fireEvent.click(screen.getByTestId('priority-option-P0'));
      });
      await waitFor(() => expect(onChanged).toHaveBeenCalledTimes(2));
      expect(onChanged).toHaveBeenNthCalledWith(1, 'P0', 80); // optimistic
      expect(onChanged).toHaveBeenNthCalledWith(2, 'P2', 47); // revert
    });
  });

  describe('scope defaults', () => {
    it('defaults to this_fingerprint scope', async () => {
      mockClassify.mockResolvedValue({ success: true });
      render(<PriorityPinControl {...DEFAULT_PROPS} />);
      await act(async () => {
        fireEvent.click(screen.getByTestId('priority-option-P1'));
      });
      expect(mockClassify).toHaveBeenCalledWith(expect.objectContaining({ apply_scope: 'this_fingerprint', apply_to_existing: true }));
    });
  });
});
