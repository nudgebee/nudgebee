import React from 'react';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import KubernetesSLOConfigs from '../KubernetesSLOConfigs';

/*
 * The SLO table's Edit action opens the existing SLO dialog pre-filled with
 * the row's current values, and saving goes through the update action.
 */

const getMemoryLimitedContainers = jest.fn(async (..._args: unknown[]) => [] as string[]);
const listSLOConfigs = jest.fn();
const updateSLOConfig = jest.fn();
const createSLOConfig = jest.fn();

jest.mock('@api1/kubernetes1', () => ({
  __esModule: true,
  default: {
    listSLOConfigs: (...args: unknown[]) => listSLOConfigs(...args),
    updateSLOConfig: (...args: unknown[]) => updateSLOConfig(...args),
    createSLOConfig: (...args: unknown[]) => createSLOConfig(...args),
    getSLOObservation: async () => ({ data: { data: { slo_report_observation_v2: { rows: [] } } } }),
    deleteSLOConfig: jest.fn(),
    getSLOConfig: jest.fn(),
    getMemoryLimitedContainers: (...args: unknown[]) => getMemoryLimitedContainers(...args),
  },
}));

jest.mock('@api1/kubernetes', () => ({
  __esModule: true,
  default: { getK8sNamespaceNames: jest.fn(async () => ({ data: { namespaces: [] } })), getAllK8sWorkload: jest.fn() },
}));

const hasWriteAccess = jest.fn((_accountId?: string, _namespace?: string) => true);
jest.mock('@lib/auth', () => ({ hasWriteAccess: (a?: string, n?: string) => hasWriteAccess(a, n) }));
jest.mock('@ui/Toast', () => ({ toast: { success: jest.fn(), error: jest.fn() } }));
jest.mock('@components/events/KubernetesEvents', () => () => null);
jest.mock('../details/KubernetesTracesListing', () => () => null);
jest.mock('@shared/widgets/CustomDateTimeRangePicker', () => () => null);
jest.mock('@shared/buttons/DownloadButton', () => () => null);
jest.mock('@ui/Chart', () => ({ __esModule: true, default: { Line: () => null, Bar: () => null } }));

const row = (name: string, id: string, goal: number, threshold: number) => ({
  window: 3600,
  workload_name: 'checkout',
  workload_namespace: 'shop',
  cloud_account_id: 'acc-1',
  enabled: true,
  id,
  name,
  goal,
  threshold,
});

beforeEach(() => {
  jest.clearAllMocks();
  hasWriteAccess.mockImplementation(() => true);
  listSLOConfigs.mockResolvedValue({
    data: { data: { slo_config: [row('availability', 'id-a', 0.999, 0), row('latency', 'id-l', 0.95, 500)] } },
  });
  updateSLOConfig.mockResolvedValue({ data: { data: { slo_config_update: { data: { success: true } } } } });
});

test('Edit opens the dialog pre-filled and saves through the update action', async () => {
  render(<KubernetesSLOConfigs accountId='acc-1' />);

  const editButton = await screen.findByRole('button', { name: 'Edit SLO config for checkout' });
  fireEvent.click(editButton);

  await screen.findByText('Update SLO Config for checkout');
  expect((document.getElementById('slo-availability-objective') as HTMLInputElement).value).toBe('99.9');
  expect((document.getElementById('slo-latency-objective') as HTMLInputElement).value).toBe('95');

  fireEvent.change(document.getElementById('slo-availability-objective') as HTMLInputElement, { target: { value: '99.5' } });
  fireEvent.click(within(document.body).getByRole('button', { name: 'Update' }));

  await waitFor(() => expect(updateSLOConfig).toHaveBeenCalledTimes(1));
  expect(createSLOConfig).not.toHaveBeenCalled();
  const payload = updateSLOConfig.mock.calls[0][0];
  expect(payload).toMatchObject({ cloud_account_id: 'acc-1', namespace: 'shop', workload_name: 'checkout' });
  expect(payload.config).toEqual(
    expect.arrayContaining([
      expect.objectContaining({ id: 'id-a', name: 'availability', goal: 0.995, enabled: true }),
      expect.objectContaining({ id: 'id-l', name: 'latency', goal: 0.95, threshold: 500, enabled: true }),
    ])
  );
  // Save refreshes the table.
  await waitFor(() => expect(listSLOConfigs.mock.calls.length).toBeGreaterThan(2));
});

test('a namespace admin can edit SLOs in their namespace but not delete them', async () => {
  // Scoped only to namespace "shop": no account-level write access.
  hasWriteAccess.mockImplementation((_accountId?: string, namespace?: string) => namespace === 'shop');
  render(<KubernetesSLOConfigs accountId='acc-1' />);

  fireEvent.click(await screen.findByRole('button', { name: 'Edit SLO config for checkout' }));
  await screen.findByText('Update SLO Config for checkout');
  expect(screen.queryByRole('button', { name: 'Delete SLO config for checkout' })).toBeNull();
});

test('Edit keeps a memory SLO on its stored container, not the default one', async () => {
  getMemoryLimitedContainers.mockResolvedValue(['istio-proxy', 'api']);
  listSLOConfigs.mockResolvedValue({
    data: { data: { slo_config: [{ ...row('memory_saturation', 'id-m', 0, 90), container: 'istio-proxy' }] } },
  });
  render(<KubernetesSLOConfigs accountId='acc-1' />);
  expect(await screen.findByText('Memory (istio-proxy) ≤ 90%')).toBeInTheDocument();

  fireEvent.click(await screen.findByRole('button', { name: 'Edit SLO config for checkout' }));
  await screen.findByText('Update SLO Config for checkout');
  await waitFor(() => expect(getMemoryLimitedContainers).toHaveBeenCalled());
  fireEvent.click(within(document.body).getByRole('button', { name: 'Update' }));

  await waitFor(() => expect(updateSLOConfig).toHaveBeenCalledTimes(1));
  expect(updateSLOConfig.mock.calls[0][0].config).toEqual([
    expect.objectContaining({ name: 'memory_saturation', threshold: 90, container: 'istio-proxy' }),
  ]);
});
