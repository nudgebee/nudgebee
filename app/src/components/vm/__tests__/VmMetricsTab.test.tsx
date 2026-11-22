import { render, screen, waitFor } from '@testing-library/react';
import VmMetricsTab from '@components/vm/VmMetricsTab';
import apiVm from '@api1/vm';

const push = jest.fn();

jest.mock('@api1/vm', () => ({
  ...jest.requireActual('@api1/vm'),
  __esModule: true,
  default: { getMetricsStatus: jest.fn(), getVmFleetMetrics: jest.fn(), listVms: jest.fn() },
}));
jest.mock('next/router', () => ({ useRouter: () => ({ push, query: {} }) }));
jest.mock('@components/vm/VmMetrics', () => ({
  __esModule: true,
  default: (props: any) => <div data-testid='vm-metrics'>{props.vm.id}</div>,
}));

const vm = { id: 'vm-1', name: 'web-1', resourse_id: 'i-1' };

describe('VmMetricsTab', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    (apiVm.listVms as jest.Mock).mockResolvedValue({ rows: [vm], total: 1 });
    (apiVm.getVmFleetMetrics as jest.Mock).mockResolvedValue({ 'vm-1': { cpuPct: 1, memPct: 2 } });
  });

  it('suggests connecting a metrics source when none is configured', async () => {
    (apiVm.getMetricsStatus as jest.Mock).mockResolvedValue({ state: 'not_configured' });
    render(<VmMetricsTab accountId='account-1' />);

    expect(await screen.findByText('No metrics source connected')).toBeInTheDocument();
    expect(screen.queryByTestId('vm-metrics')).not.toBeInTheDocument();
  });

  it('reports a failed check with its reason, not as a missing integration', async () => {
    (apiVm.getMetricsStatus as jest.Mock).mockResolvedValue({ state: 'check_failed', error: 'forager timed out' });
    render(<VmMetricsTab accountId='account-1' />);

    expect(await screen.findByText(/forager timed out/)).toBeInTheDocument();
    expect(screen.queryByText('No metrics source connected')).not.toBeInTheDocument();
  });

  it('shows the metrics of a reporting VM when connected', async () => {
    (apiVm.getMetricsStatus as jest.Mock).mockResolvedValue({ state: 'connected' });
    render(<VmMetricsTab accountId='account-1' />);

    await waitFor(() => expect(screen.getByTestId('vm-metrics')).toHaveTextContent('vm-1'));
  });
});
