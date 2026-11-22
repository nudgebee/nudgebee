import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import VmLogsStatusCard from '@components/vm/VmLogsStatusCard';
import apiVm from '@api1/vm';

const push = jest.fn();
jest.mock('next/router', () => ({ useRouter: () => ({ push }) }));
jest.mock('@api1/vm', () => ({
  __esModule: true,
  SEVERITY_ORDER: ['Critical', 'High', 'Medium', 'Low', 'Info'],
  default: { getLogsStatus: jest.fn() },
}));

const status = (over: object) => ({ state: 'connected', integration_name: 'vm-loki', ...over });

describe('VmLogsStatusCard', () => {
  beforeEach(() => jest.clearAllMocks());

  it('shows Connected when the store holds recent logs', async () => {
    (apiVm.getLogsStatus as jest.Mock).mockResolvedValue(status({}));
    render(<VmLogsStatusCard accountId='acc-1' />);
    expect(await screen.findByText('Connected')).toBeInTheDocument();
    expect(screen.getByText(/vm-loki holds logs from the last 10 minutes\./)).toBeInTheDocument();
    expect(apiVm.getLogsStatus).toHaveBeenCalledWith('acc-1');
  });

  it('shows No data for a reachable but empty store', async () => {
    (apiVm.getLogsStatus as jest.Mock).mockResolvedValue(status({ state: 'no_data' }));
    render(<VmLogsStatusCard accountId='acc-1' />);
    expect(await screen.findByText('No data')).toBeInTheDocument();
    expect(screen.queryByText('Connected')).not.toBeInTheDocument();
  });

  it('shows an unreachable store as a banner carrying the reason', async () => {
    (apiVm.getLogsStatus as jest.Mock).mockResolvedValue(status({ state: 'unreachable', error: 'forager cannot reach loki:3100' }));
    render(<VmLogsStatusCard accountId='acc-1' />);
    expect(await screen.findByText('Unreachable')).toBeInTheDocument();
    expect(screen.getByText('forager cannot reach loki:3100')).toBeInTheDocument();
  });

  it('offers Connect when nothing is configured, and not after a failed check', async () => {
    (apiVm.getLogsStatus as jest.Mock).mockResolvedValue(status({ state: 'not_configured' }));
    const { unmount } = render(<VmLogsStatusCard accountId='acc-1' />);
    await userEvent.setup().click(await screen.findByRole('button', { name: 'Connect' }));
    expect(push).toHaveBeenCalledWith('/user-management?integration=observability#integrations');
    unmount();

    (apiVm.getLogsStatus as jest.Mock).mockResolvedValue(status({ state: 'check_failed', error: 'boom' }));
    render(<VmLogsStatusCard accountId='acc-1' />);
    expect(await screen.findByText('Check failed')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Connect' })).not.toBeInTheDocument();
  });
});
