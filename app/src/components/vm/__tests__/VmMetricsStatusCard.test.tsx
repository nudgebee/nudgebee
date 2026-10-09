import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import VmMetricsStatusCard, { reportingNames } from '@components/vm/VmMetricsStatusCard';
import apiVm from '@api1/vm';

const push = jest.fn();
jest.mock('next/router', () => ({ useRouter: () => ({ push }) }));
jest.mock('@api1/vm', () => ({
  __esModule: true,
  SEVERITY_ORDER: ['Critical', 'High', 'Medium', 'Low', 'Info'],
  default: { getMetricsStatus: jest.fn() },
}));

const status = (over: object) => ({ state: 'connected', integration_name: 'vm-prom', reporting: [], ...over });
const agent = (instance: string, hostname?: string) => ({ instance, hostname });

describe('VmMetricsStatusCard', () => {
  beforeEach(() => jest.clearAllMocks());

  it("counts this account's VMs out of its inventory and names them", async () => {
    (apiVm.getMetricsStatus as jest.Mock).mockResolvedValue(
      status({ scope: 'account', inventory: 3, other_agents: 0, reporting: [agent('a', 'web-1'), agent('b')] })
    );
    render(<VmMetricsStatusCard accountId='acc-1' />);

    expect(await screen.findByText('Connected')).toBeInTheDocument();
    expect(screen.getByText(/2 of 3 VMs writing metrics to vm-prom\./)).toBeInTheDocument();
    expect(screen.getByText('web-1, b')).toBeInTheDocument();
    expect(apiVm.getMetricsStatus).toHaveBeenCalledWith('acc-1');
  });

  it('says so when agents from other accounts share the store, so the count never includes them silently', async () => {
    (apiVm.getMetricsStatus as jest.Mock).mockResolvedValue(
      status({ scope: 'account', inventory: 2, other_agents: 3, reporting: [agent('a', 'web-1')] })
    );
    render(<VmMetricsStatusCard accountId='acc-1' />);
    expect(await screen.findByText(/1 of 2 VMs writing metrics to vm-prom\. 3 other agents are writing to the same store\./)).toBeInTheDocument();
  });

  it('describes a store-wide count as such when the account has no VMs on record', async () => {
    (apiVm.getMetricsStatus as jest.Mock).mockResolvedValue(status({ scope: 'store', inventory: 0, reporting: [agent('a', 'web-1')] }));
    render(<VmMetricsStatusCard accountId='acc-1' />);
    expect(await screen.findByText(/1 agent writing to vm-prom\. This account has no VMs on record yet to match them against\./)).toBeInTheDocument();
    expect(screen.queryByText(/of 0 VMs/)).not.toBeInTheDocument();
  });

  it('shows No data when none of the account VMs write, even though others do', async () => {
    (apiVm.getMetricsStatus as jest.Mock).mockResolvedValue(status({ state: 'no_data', scope: 'account', inventory: 2, other_agents: 4 }));
    render(<VmMetricsStatusCard accountId='acc-1' />);
    expect(await screen.findByText('No data')).toBeInTheDocument();
    expect(
      screen.getByText(/none of this account's 2 VMs has written to it in the last 10 minutes\. 4 other agents are writing to the same store\./)
    ).toBeInTheDocument();
    expect(screen.queryByText('Connected')).not.toBeInTheDocument();
  });

  it('shows an unreachable store as a critical banner carrying the reason', async () => {
    (apiVm.getMetricsStatus as jest.Mock).mockResolvedValue(status({ state: 'unreachable', error: 'forager cannot reach 10.0.0.5:9090' }));
    render(<VmMetricsStatusCard accountId='acc-1' />);
    expect(await screen.findByText('Unreachable')).toBeInTheDocument();
    expect(screen.getByText('forager cannot reach 10.0.0.5:9090')).toBeInTheDocument();
    expect(screen.getByText(/vm-prom could not be reached\./)).toBeInTheDocument();
  });

  it('shows a failed check as a failure with its reason, not as "no store connected"', async () => {
    (apiVm.getMetricsStatus as jest.Mock).mockResolvedValue(
      status({ state: 'check_failed', error: 'multiple metrics providers are configured (a, b), but none is marked as default' })
    );
    render(<VmMetricsStatusCard accountId='acc-1' />);
    expect(await screen.findByText('Check failed')).toBeInTheDocument();
    expect(screen.getByText(/none is marked as default/)).toBeInTheDocument();
    expect(screen.queryByText(/No metrics store is connected/)).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Connect' })).not.toBeInTheDocument(); // the user has an integration; do not send them to add one
  });

  it('offers Connect, to the integrations tab that lists Prometheus, when nothing is configured', async () => {
    (apiVm.getMetricsStatus as jest.Mock).mockResolvedValue(status({ state: 'not_configured' }));
    render(<VmMetricsStatusCard accountId='acc-1' />);
    await userEvent.setup().click(await screen.findByRole('button', { name: 'Connect' }));
    expect(push).toHaveBeenCalledWith('/user-management?integration=observability#integrations');
  });

  it('reports a failed request as a failure banner, not as a status', async () => {
    (apiVm.getMetricsStatus as jest.Mock).mockRejectedValue(new Error('gateway timeout'));
    render(<VmMetricsStatusCard accountId='acc-1' />);
    expect(await screen.findByText('Could not check metrics: gateway timeout')).toBeInTheDocument();
    expect(screen.queryByText('Not connected')).not.toBeInTheDocument();
  });

  it('shows Recheck working, and keeps the previous result on screen meanwhile', async () => {
    (apiVm.getMetricsStatus as jest.Mock).mockResolvedValueOnce(status({ state: 'no_data', scope: 'store', inventory: 0 }));
    render(<VmMetricsStatusCard accountId='acc-1' />);
    await screen.findByText('No data');
    expect(screen.getByRole('button', { name: 'Recheck' })).not.toHaveAttribute('aria-busy');

    let finish!: (v: unknown) => void;
    (apiVm.getMetricsStatus as jest.Mock).mockReturnValueOnce(new Promise((resolve) => (finish = resolve)));
    await userEvent.setup().click(screen.getByRole('button', { name: 'Recheck' }));

    await waitFor(() => expect(screen.getByRole('button', { name: 'Recheck' })).toHaveAttribute('aria-busy', 'true'));
    expect(screen.getByText('No data')).toBeInTheDocument(); // old badge stays, but the button says a check is running

    finish(status({ scope: 'store', reporting: [agent('a', 'web-1')] }));
    expect(await screen.findByText('Connected')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Recheck' })).not.toHaveAttribute('aria-busy');
  });
});

describe('reportingNames', () => {
  it('tells apart clones that share a hostname instead of listing it twice', () => {
    expect(reportingNames([agent('aaaaaaaa1111', 'web'), agent('bbbbbbbb2222', 'web'), agent('c', 'db')])).toEqual([
      'web (aaaaaaaa)',
      'web (bbbbbbbb)',
      'db',
    ]);
  });

  it('falls back to the instance when there is no hostname', () => {
    expect(reportingNames([agent('abc')])).toEqual(['abc']);
  });
});
