import { render, screen, fireEvent, act } from '@testing-library/react';
import PagerDutyAccountModal from '@components/integrations/modal/PagerDutyAccountModal';

jest.mock('@ui/Modal', () => ({
  Modal: ({ open, title, children }) =>
    open ? (
      <div data-testid='modal'>
        <span data-testid='modal-title'>{title}</span>
        {children}
      </div>
    ) : null,
}));

jest.mock('@ui/Input', () => ({
  Input: ({ id, value, onChange, label, error }) => (
    <div>
      {label && <label htmlFor={id}>{label}</label>}
      <input id={id} data-testid={id} value={value} onChange={(e) => onChange?.(e.target.value)} data-error={error || ''} />
    </div>
  ),
}));

jest.mock('@ui/Checkbox', () => ({
  Checkbox: ({ id, checked, onChange, label }) => (
    <label>
      <input id={id} data-testid={id} type='checkbox' checked={checked} onChange={(e) => onChange?.(e.target.checked)} />
      {label}
    </label>
  ),
}));

jest.mock('@ui/Button', () => ({
  Button: ({ children, onClick, id, disabled, loading }) => (
    <button id={id} data-testid={id} onClick={onClick} disabled={disabled || loading}>
      {children}
    </button>
  ),
}));

jest.mock('@ui/Toast', () => ({ toast: { success: jest.fn(), error: jest.fn() } }));

jest.mock('@api1/integrations', () => ({
  __esModule: true,
  default: {
    testTicketConnectionByConfig: jest.fn(),
  },
}));

jest.mock('@api1/tickets', () => ({
  __esModule: true,
  default: {},
}));

jest.mock('src/utils/common', () => ({
  getAccountCreationSuccessMsg: jest.fn(() => 'Account created'),
}));

jest.mock('@utils/colors');

beforeEach(() => {
  jest.clearAllMocks();
});

describe('PagerDutyAccountModal', () => {
  describe('rendering', () => {
    it('does not render when openModal=false', () => {
      render(<PagerDutyAccountModal openModal={false} handleClose={jest.fn()} />);
      expect(screen.queryByTestId('modal')).not.toBeInTheDocument();
    });

    it('renders modal when openModal=true', () => {
      render(<PagerDutyAccountModal openModal={true} handleClose={jest.fn()} />);
      expect(screen.getByTestId('modal')).toBeInTheDocument();
    });

    it('shows Save button in create mode', () => {
      render(<PagerDutyAccountModal openModal={true} handleClose={jest.fn()} />);
      expect(screen.getByTestId('create-pagerduty-acc')).toBeInTheDocument();
    });

    it('shows Update button in edit mode', () => {
      const editConfig = { id: '1', name: 'PD', username: 'test@test.com', rca_writeback_enabled: false };
      render(<PagerDutyAccountModal openModal={true} handleClose={jest.fn()} editConfig={editConfig} />);
      expect(screen.getByTestId('update-pagerduty-acc')).toBeInTheDocument();
    });

    it('renders Cancel button', () => {
      render(<PagerDutyAccountModal openModal={true} handleClose={jest.fn()} />);
      expect(screen.getByTestId('cancel-btn')).toBeInTheDocument();
    });

    it('renders Test Connection button', () => {
      render(<PagerDutyAccountModal openModal={true} handleClose={jest.fn()} />);
      expect(screen.getByTestId('test-pagerduty-connection')).toBeInTheDocument();
    });
  });

  describe('edit mode pre-fill', () => {
    it('pre-fills name in edit mode', () => {
      const editConfig = { id: '1', name: 'My PD', username: 'user@example.com', rca_writeback_enabled: false };
      render(<PagerDutyAccountModal openModal={true} handleClose={jest.fn()} editConfig={editConfig} />);
      const nameInput = screen.getByTestId('pagerDutyName');
      expect(nameInput).toHaveValue('My PD');
    });

    it('pre-fills email in edit mode', () => {
      const editConfig = { id: '1', name: 'My PD', username: 'user@example.com', rca_writeback_enabled: false };
      render(<PagerDutyAccountModal openModal={true} handleClose={jest.fn()} editConfig={editConfig} />);
      expect(screen.getByTestId('pagerDutyEmail')).toHaveValue('user@example.com');
    });
  });

  describe('test connection', () => {
    it('shows error when name/email missing and test connection clicked', async () => {
      render(<PagerDutyAccountModal openModal={true} handleClose={jest.fn()} />);
      await act(async () => {
        fireEvent.click(screen.getByTestId('test-pagerduty-connection'));
      });
      const { toast } = require('@ui/Toast');
      expect(toast.error).toHaveBeenCalledWith('Please fill name and email before testing');
    });

    it('calls testTicketConnectionByConfig when fields are filled', async () => {
      const { default: api } = require('@api1/integrations');
      api.testTicketConnectionByConfig.mockResolvedValue({ success: true });
      const { toast } = require('@ui/Toast');
      render(<PagerDutyAccountModal openModal={true} handleClose={jest.fn()} />);
      fireEvent.change(screen.getByTestId('pagerDutyName'), { target: { value: 'Test PD' } });
      fireEvent.change(screen.getByTestId('pagerDutyEmail'), { target: { value: 'user@test.com' } });
      await act(async () => {
        fireEvent.click(screen.getByTestId('test-pagerduty-connection'));
      });
      expect(api.testTicketConnectionByConfig).toHaveBeenCalled();
      expect(toast.success).toHaveBeenCalledWith('PagerDuty connection successful');
    });
  });
});
