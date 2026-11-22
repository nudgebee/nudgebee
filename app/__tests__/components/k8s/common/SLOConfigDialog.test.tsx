import React from 'react';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import '@testing-library/jest-dom';
import SLOConfigDialog from '@components/k8s/common/SLOConfigDialog';
import apiKubernetes1 from '@api1/kubernetes1';

const mockToastError = jest.fn();
jest.mock('@ui/Toast', () => ({ toast: { success: jest.fn(), error: (...args: any[]) => mockToastError(...args) } }));
jest.mock('@utils/colors', () => {
  const token: any = new Proxy(() => '', { get: () => token });
  return { ds: token };
});
jest.mock('@api1/kubernetes1', () => ({
  __esModule: true,
  default: {
    createSLOConfig: jest.fn(),
    updateSLOConfig: jest.fn(),
    deleteSLOConfig: jest.fn(),
    generateForecast: jest.fn(),
    getSLOConfig: jest.fn(),
    getMemoryLimitedContainers: jest.fn(),
  },
}));
jest.mock('@api1/kubernetes', () => ({ __esModule: true, default: {} }));
const mockHasWriteAccess = jest.fn((..._args: any[]) => true);
jest.mock('@lib/auth', () => ({ canOnAccount: (_module: string, _class: string, ...args: any[]) => mockHasWriteAccess(...args) }));
jest.mock('@ui/Modal', () => ({
  Modal: ({ children, actionButtons, open }: any) =>
    open ? (
      <div>
        {children}
        {actionButtons}
      </div>
    ) : null,
}));
jest.mock('@ui/Input', () => ({
  Input: ({ id, value, onChange }: any) => <input data-testid={`${id}-input`} value={value ?? ''} onChange={(e) => onChange?.(e.target.value)} />,
}));
jest.mock('@ui/Button', () => ({
  Button: ({ id, children, onClick, disabled }: any) => (
    <button data-testid={id} onClick={onClick} disabled={disabled}>
      {children}
    </button>
  ),
}));
jest.mock('@ui/Select', () => ({
  Select: ({ id, value, options, onChange }: any) => (
    <select data-testid={id} value={value ?? ''} onChange={(e) => onChange?.(e.target.value)}>
      <option value='' />
      {(options ?? []).map((o: string) => (
        <option key={o} value={o}>
          {o}
        </option>
      ))}
    </select>
  ),
}));
jest.mock('@ui/Chip', () => ({ Chip: ({ children }: any) => <span>{children}</span> }));
jest.mock('@ui/Label', () => ({ Label: ({ text }: any) => <span>{text}</span> }));
jest.mock('@ui/Callout', () => ({ Callout: ({ children }: any) => <div role='alert'>{children}</div> }));

const api = apiKubernetes1 as jest.Mocked<any>;
const workload = { name: 'nbdemo-orders', namespace: 'demo', cloud_resource_id: 'wl-1' };

const renderDialog = (props: any = {}) =>
  render(<SLOConfigDialog open onClose={jest.fn()} accountId='acc-1' workload={workload} initialConfig={[]} {...props} />);

beforeEach(() => {
  jest.clearAllMocks();
  api.getMemoryLimitedContainers.mockResolvedValue(['istio-proxy', 'api']);
});

describe('SLOConfigDialog CPU SLOs', () => {
  it('saves a CPU-only SLO, sending only the configured type', async () => {
    api.createSLOConfig.mockResolvedValue({ data: { data: { slo_config_create: { data: { success: true } } } } });
    renderDialog();

    fireEvent.change(await screen.findByTestId('slo-cpu_throttling-threshold-input'), { target: { value: '75' } });
    fireEvent.click(screen.getByTestId('slo-submit-btn'));

    await waitFor(() => expect(api.createSLOConfig).toHaveBeenCalled());
    expect(mockToastError).not.toHaveBeenCalled();
    expect(api.createSLOConfig.mock.calls[0][0].config).toEqual([{ enabled: true, name: 'cpu_throttling', goal: 0, threshold: 75 }]);
  });

  it('asks for at least one SLO and rejects a threshold above 100%', async () => {
    renderDialog();
    fireEvent.click(screen.getByTestId('slo-submit-btn'));
    expect(mockToastError).toHaveBeenLastCalledWith('Configure at least one SLO');

    fireEvent.change(await screen.findByTestId('slo-cpu_saturation-threshold-input'), { target: { value: '150' } });
    fireEvent.click(screen.getByTestId('slo-submit-btn'));
    expect(mockToastError).toHaveBeenLastCalledWith('CPU saturation threshold must be above 0% and at most 100%');
    expect(api.createSLOConfig).not.toHaveBeenCalled();
  });

  it('still requires a complete latency SLO when one is started', async () => {
    renderDialog();
    fireEvent.change(screen.getByTestId('slo-latency-objective-input'), { target: { value: '99' } });
    fireEvent.change(await screen.findByTestId('slo-cpu_throttling-threshold-input'), { target: { value: '75' } });
    fireEvent.click(screen.getByTestId('slo-submit-btn'));
    expect(mockToastError).toHaveBeenLastCalledWith('Configure Latency Threshold');
  });

  it('Check now shows the forecast for the entered threshold', async () => {
    api.generateForecast.mockResolvedValue({
      status: 'projected',
      threshold_pct: 75,
      current_pct: 37.3,
      slope_pct_per_day: 25.6,
      eta_hours: 35,
      level: 'HIGH',
      daily: [{ date: '2026-10-19', value_pct: 21.1 }],
    });
    renderDialog();
    fireEvent.change(await screen.findByTestId('slo-cpu_throttling-threshold-input'), { target: { value: '75' } });
    fireEvent.click(screen.getByTestId('slo-cpu_throttling-check-now'));

    expect(await screen.findByText('Breach projected')).toBeInTheDocument();
    expect(screen.getByText('Reaches 75% in ~35h (now 37.3%, rising 25.6 pp/day)')).toBeInTheDocument();
    expect(api.generateForecast).toHaveBeenCalledWith({
      account_id: 'acc-1',
      workload_namespace: 'demo',
      workload_name: 'nbdemo-orders',
      metric: 'cpu_throttling',
      threshold: 75,
    });
  });

  it('Check now shows the server error, e.g. no access to the account', async () => {
    api.generateForecast.mockRejectedValue(new Error('access denied for account: acc-1'));
    renderDialog();
    fireEvent.change(await screen.findByTestId('slo-cpu_throttling-threshold-input'), { target: { value: '75' } });
    fireEvent.click(screen.getByTestId('slo-cpu_throttling-check-now'));
    expect(await screen.findByRole('alert')).toHaveTextContent('access denied');
  });

  it('Remove deletes only that CPU SLO on the server', async () => {
    api.deleteSLOConfig.mockResolvedValue({ data: { data: { slo_config_delete: { data: { success: true } } } } });
    const onSuccess = jest.fn();
    renderDialog({ isEdit: true, onSuccess, initialConfig: [{ id: 'c1', name: 'cpu_throttling', threshold: '75', goal: '0', enabled: true }] });

    expect(await screen.findByTestId('slo-cpu_throttling-threshold-input')).toHaveValue('75');
    fireEvent.click(screen.getByTestId('slo-cpu_throttling-remove'));

    await waitFor(() =>
      expect(api.deleteSLOConfig).toHaveBeenCalledWith({
        cloud_account_id: 'acc-1',
        workload_name: 'nbdemo-orders',
        namespace: 'demo',
        name: 'cpu_throttling',
      })
    );
    await waitFor(() => expect(screen.getByTestId('slo-cpu_throttling-threshold-input')).toHaveValue(''));
    expect(onSuccess).toHaveBeenCalled();
  });

  it('hides Remove on a saved CPU SLO without account-level write access', async () => {
    mockHasWriteAccess.mockReturnValue(false);
    renderDialog({ isEdit: true, initialConfig: [{ id: 'c1', name: 'cpu_throttling', threshold: '75', goal: '0', enabled: true }] });
    await screen.findByTestId('slo-cpu_throttling-threshold-input');
    expect(screen.queryByTestId('slo-cpu_throttling-remove')).not.toBeInTheDocument();
    expect(mockHasWriteAccess).toHaveBeenCalledWith('acc-1');
    mockHasWriteAccess.mockReturnValue(true);
  });
});

describe('SLOConfigDialog memory SLO', () => {
  it('saves a memory SLO on the app container, not the sidecar, by default', async () => {
    api.createSLOConfig.mockResolvedValue({ data: { data: { slo_config_create: { data: { success: true } } } } });
    renderDialog();
    await waitFor(() => expect(api.getMemoryLimitedContainers).toHaveBeenCalledWith('acc-1', 'demo', 'nbdemo-orders'));
    await screen.findByRole('option', { name: 'api' });

    fireEvent.change(screen.getByTestId('slo-memory_saturation-threshold-input'), { target: { value: '90' } });
    fireEvent.click(screen.getByTestId('slo-submit-btn'));

    await waitFor(() => expect(api.createSLOConfig).toHaveBeenCalled());
    expect(api.createSLOConfig.mock.calls[0][0].config).toEqual([
      { enabled: true, name: 'memory_saturation', goal: 0, threshold: 90, container: 'api' },
    ]);
  });

  it('lets the user pick another container, and Check now forecasts that one', async () => {
    api.generateForecast.mockResolvedValue({ status: 'not_projected', threshold_pct: 90, current_pct: 41, slope_pct_per_day: 0.2, daily: [] });
    renderDialog();
    await screen.findByRole('option', { name: 'istio-proxy' });
    fireEvent.change(screen.getByTestId('slo-memory_saturation-threshold-input'), { target: { value: '90' } });
    fireEvent.change(screen.getByTestId('slo-memory_saturation-container'), { target: { value: 'istio-proxy' } });
    fireEvent.click(screen.getByTestId('slo-memory_saturation-check-now'));

    await waitFor(() =>
      expect(api.generateForecast).toHaveBeenCalledWith({
        account_id: 'acc-1',
        workload_namespace: 'demo',
        workload_name: 'nbdemo-orders',
        metric: 'memory_saturation',
        threshold: 90,
        container: 'istio-proxy',
      })
    );
  });

  it('fills in the default container when the list arrives after the threshold was typed', async () => {
    let resolveContainers: (v: string[]) => void = () => undefined;
    api.getMemoryLimitedContainers.mockReturnValue(new Promise<string[]>((r) => (resolveContainers = r)));
    api.createSLOConfig.mockResolvedValue({ data: { data: { slo_config_create: { data: { success: true } } } } });
    renderDialog();
    fireEvent.change(screen.getByTestId('slo-memory_saturation-threshold-input'), { target: { value: '90' } });
    resolveContainers(['istio-proxy', 'api']);
    await screen.findByRole('option', { name: 'api' });
    await waitFor(() => expect(screen.getByTestId('slo-memory_saturation-container')).toHaveValue('api'));

    fireEvent.click(screen.getByTestId('slo-submit-btn'));
    await waitFor(() => expect(api.createSLOConfig).toHaveBeenCalled());
    expect(api.createSLOConfig.mock.calls[0][0].config[0].container).toBe('api');
  });

  it('refuses to save a memory SLO without a container', async () => {
    api.getMemoryLimitedContainers.mockResolvedValue([]);
    renderDialog();
    await waitFor(() => expect(api.getMemoryLimitedContainers).toHaveBeenCalled());
    fireEvent.change(screen.getByTestId('slo-memory_saturation-threshold-input'), { target: { value: '90' } });
    fireEvent.click(screen.getByTestId('slo-submit-btn'));
    expect(mockToastError).toHaveBeenLastCalledWith('Choose the container the memory SLO watches');
    expect(api.createSLOConfig).not.toHaveBeenCalled();
  });

  it('CPU Check now sends no container', async () => {
    api.generateForecast.mockResolvedValue({ status: 'not_projected', daily: [] });
    renderDialog();
    fireEvent.change(await screen.findByTestId('slo-cpu_throttling-threshold-input'), { target: { value: '75' } });
    fireEvent.click(screen.getByTestId('slo-cpu_throttling-check-now'));
    await waitFor(() => expect(api.generateForecast).toHaveBeenCalled());
    expect(api.generateForecast.mock.calls[0][0]).not.toHaveProperty('container');
  });
});
