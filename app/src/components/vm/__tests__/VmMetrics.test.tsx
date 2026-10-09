import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import VmMetrics from '@components/vm/VmMetrics';
import apiVm from '@api1/vm';

jest.mock('@api1/vm', () => ({
  __esModule: true,
  SEVERITY_ORDER: ['Critical', 'High', 'Medium', 'Low', 'Info'],
  default: { getVmMetrics: jest.fn() },
}));
jest.mock('@ui/Chart', () => ({
  __esModule: true,
  default: { TimeSeries: ({ id }: { id: string }) => <div data-testid={id} /> },
}));

const vm = { id: 'res-1', name: 'web-1', resourse_id: 'i-0abc', meta: { private_ip: '10.0.0.5' } } as any;
const series = (key: string, t: number[], v: number[], metric: Record<string, string> = {}) => ({
  key,
  series: [{ metric, timestamps: t, values: v }],
});

describe('VmMetrics', () => {
  beforeEach(() => jest.clearAllMocks());

  it('asks for the last hour of the VM and renders charts and services', async () => {
    (apiVm.getVmMetrics as jest.Mock).mockResolvedValue([
      series('cpu_usage_line', [60], [0.5]),
      series('memory_usage_line', [60], [1024 ** 3]),
      series('cpu_total', [60], [4]),
      series('vm_service_cpu_usage', [60], [0.3], { container_id: '/system.slice/nginx.service' }),
    ]);
    render(<VmMetrics accountId='acc-1' vm={vm} />);

    expect(await screen.findByTestId('vm-cpu-res-1')).toBeInTheDocument();
    expect(screen.getByText('nginx')).toBeInTheDocument();

    const call = (apiVm.getVmMetrics as jest.Mock).mock.calls[0][0];
    expect(call.accountId).toBe('acc-1');
    expect(call.vm).toBe(vm);
    expect(call.endDate - call.startDate).toBe(60 * 60 * 1000);
  });

  describe('loading state', () => {
    const deferred = () => {
      let resolve!: (v: any) => void;
      let reject!: (e: Error) => void;
      const promise = new Promise<any>((res, rej) => {
        resolve = res;
        reject = rej;
      });
      return { promise, resolve, reject };
    };

    it('shows placeholders on the first load, not the empty-state message', async () => {
      const d = deferred();
      (apiVm.getVmMetrics as jest.Mock).mockReturnValue(d.promise);
      render(<VmMetrics accountId='acc-1' vm={vm} />);

      expect(screen.getByRole('status', { name: 'Loading metrics' })).toHaveAttribute('aria-busy', 'true');
      expect(screen.getByText('Loading metrics for web-1…')).toBeInTheDocument();
      expect(screen.queryByText(/No metrics for this VM yet/)).not.toBeInTheDocument();
      expect(screen.queryByTestId('vm-cpu-res-1')).not.toBeInTheDocument();

      d.resolve([series('cpu_usage_line', [60], [0.5])]);
      expect(await screen.findByTestId('vm-cpu-res-1')).toBeInTheDocument();
      expect(screen.queryByRole('status', { name: 'Loading metrics' })).not.toBeInTheDocument();
    });

    it('swaps the placeholders for the empty-state message when nothing comes back', async () => {
      const d = deferred();
      (apiVm.getVmMetrics as jest.Mock).mockReturnValue(d.promise);
      render(<VmMetrics accountId='acc-1' vm={vm} />);
      expect(screen.getByRole('status', { name: 'Loading metrics' })).toBeInTheDocument();

      d.resolve([{ key: 'cpu_usage_line', series: [], reason: 'no machine is writing' }]);
      expect(await screen.findByText(/No metrics for this VM yet/)).toBeInTheDocument();
      expect(screen.queryByRole('status', { name: 'Loading metrics' })).not.toBeInTheDocument();
    });

    it('shows an error instead of placeholders when the first load fails', async () => {
      const d = deferred();
      (apiVm.getVmMetrics as jest.Mock).mockReturnValue(d.promise);
      render(<VmMetrics accountId='acc-1' vm={vm} />);

      d.reject(new Error('provider timed out'));
      expect(await screen.findByRole('alert')).toHaveTextContent('provider timed out');
      expect(screen.queryByRole('status', { name: 'Loading metrics' })).not.toBeInTheDocument();
    });

    it('keeps the charts and shows a progress bar while a new range loads', async () => {
      (apiVm.getVmMetrics as jest.Mock).mockResolvedValueOnce([series('cpu_usage_line', [60], [0.5])]);
      render(<VmMetrics accountId='acc-1' vm={vm} />);
      expect(await screen.findByTestId('vm-cpu-res-1')).toBeInTheDocument();
      expect(screen.queryByRole('progressbar')).not.toBeInTheDocument();

      const d = deferred();
      (apiVm.getVmMetrics as jest.Mock).mockReturnValueOnce(d.promise);
      await userEvent.setup().click(screen.getByRole('button', { name: '6h' }));

      expect(await screen.findByRole('progressbar', { name: 'Refreshing metrics' })).toBeInTheDocument();
      expect(screen.getByTestId('vm-cpu-res-1')).toBeInTheDocument(); // last charts stay up, not a blank dialog
      expect(screen.queryByRole('status', { name: 'Loading metrics' })).not.toBeInTheDocument(); // placeholders are first-load only

      d.resolve([series('cpu_usage_line', [60, 120], [0.4, 0.6])]);
      await waitFor(() => expect(screen.queryByRole('progressbar')).not.toBeInTheDocument());
      expect(screen.getByTestId('vm-cpu-res-1')).toBeInTheDocument();
    });
  });

  it('says why when the VM has no data', async () => {
    (apiVm.getVmMetrics as jest.Mock).mockResolvedValue([
      { key: 'cpu_usage_line', series: [], reason: 'no metrics provider resolved for this account' },
    ]);
    render(<VmMetrics accountId='acc-1' vm={vm} />);

    expect(await screen.findByText(/No metrics for this VM yet/)).toBeInTheDocument();
    expect(screen.getByText('no metrics provider resolved for this account')).toBeInTheDocument();
    expect(screen.queryByTestId('vm-cpu-res-1')).not.toBeInTheDocument();
  });

  it('shows a load failure, not an empty-data message', async () => {
    (apiVm.getVmMetrics as jest.Mock).mockRejectedValue(new Error('provider timed out'));
    render(<VmMetrics accountId='acc-1' vm={vm} />);

    expect(await screen.findByRole('alert')).toHaveTextContent('provider timed out');
    expect(screen.queryByText(/No metrics for this VM yet/)).not.toBeInTheDocument();
  });

  it('does not refetch when the parent re-renders with a new object for the same VM', async () => {
    (apiVm.getVmMetrics as jest.Mock).mockResolvedValue([]);
    const { rerender } = render(<VmMetrics accountId='acc-1' vm={vm} />);
    await waitFor(() => expect(apiVm.getVmMetrics).toHaveBeenCalledTimes(1));

    rerender(<VmMetrics accountId='acc-1' vm={{ ...vm }} />);
    rerender(<VmMetrics accountId='acc-1' vm={{ ...vm, status: 'Active' }} />);
    await new Promise((r) => setTimeout(r, 20));
    expect(apiVm.getVmMetrics).toHaveBeenCalledTimes(1);

    rerender(<VmMetrics accountId='acc-1' vm={{ ...vm, id: 'res-2' }} />);
    await waitFor(() => expect(apiVm.getVmMetrics).toHaveBeenCalledTimes(2));
  });

  it('refetches with a wider window when the range changes', async () => {
    (apiVm.getVmMetrics as jest.Mock).mockResolvedValue([]);
    render(<VmMetrics accountId='acc-1' vm={vm} />);
    await waitFor(() => expect(apiVm.getVmMetrics).toHaveBeenCalledTimes(1));

    await userEvent.setup().click(screen.getByRole('button', { name: '24h' }));

    await waitFor(() => expect(apiVm.getVmMetrics).toHaveBeenCalledTimes(2));
    const call = (apiVm.getVmMetrics as jest.Mock).mock.calls[1][0];
    expect(call.endDate - call.startDate).toBe(24 * 60 * 60 * 1000);
  });
});
