import React from 'react';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import '@testing-library/jest-dom';

const mockGetK8sWorkload = jest.fn();
const mockApplyRecommendation = jest.fn();
const mockToastError = jest.fn();

jest.mock('chart.js', () => ({
  Chart: { register: jest.fn() },
  CategoryScale: jest.fn(),
  LinearScale: jest.fn(),
  PointElement: jest.fn(),
  LineElement: jest.fn(),
  Filler: jest.fn(),
  Title: jest.fn(),
  Tooltip: jest.fn(),
  Legend: jest.fn(),
  Colors: jest.fn(),
}));
jest.mock('react-chartjs-2', () => ({
  Line: ({ data, id }) => <div data-testid={`line-chart-${id}`} data-points={data.datasets[0]?.data?.length ?? 0} />,
}));
jest.mock('@api1/tickets', () => ({
  __esModule: true,
  default: { listTicketConfigsForCreate: jest.fn(() => Promise.resolve({ data: [] })) },
}));
jest.mock('@api1/recommendation', () => ({
  __esModule: true,
  default: { applyRecommendation: (...args) => mockApplyRecommendation(...args) },
}));
jest.mock('@api1/kubernetes', () => ({
  __esModule: true,
  default: {
    getK8sWorkload: (...args) => mockGetK8sWorkload(...args),
    getResourceAttributes: jest.fn(() => Promise.resolve([])),
  },
}));
jest.mock('@ui/Toast', () => ({
  toast: { error: (...args) => mockToastError(...args), success: jest.fn() },
}));

import MemoryAllocationCard from '@components/k8s/investigate/cards/MemoryAllocationCard';

const MIB = 1024 * 1024;

// #38634, event ac9cf9c2 on test: load-generator was OOMKilled at its 512Mi limit.
// A second exporter (OpenCost) published the request series next to
// kube-state-metrics, so the evidence summed it to 1Gi (and CPU to 0.128) while the
// limit stayed right. The form showed "1024 MB request, 512 MB limit" and proposed
// 1126.4 / 563.2 — a pair Kubernetes refuses.
const doubled = {
  container: 'load-generator',
  namespace: 'demo',
  pod: 'load-generator-5c78dd9b76-kgf2n',
  requests: { cpu: 0.128, memory: 1024 * MIB },
  limits: { cpu: 0.064, memory: 512 * MIB },
};
const evidences = [
  {
    type: 'table',
    data: {
      table_name: '*Pod and Node OOMKilled data*',
      rows: [
        ['Pod', 'load-generator-5c78dd9b76-kgf2n'],
        ['Container name', 'load-generator'],
      ],
    },
  },
  {
    type: 'json',
    data: JSON.stringify({
      name: 'pod_metric',
      data: [
        {
          metric: doubled,
          timestamps: [1790583600, 1790583660, 1790583720],
          values: [String(470 * MIB), String(494 * MIB), String(480 * MIB)],
        },
      ],
      resource_type: 'memory',
    }),
  },
];
const event = {
  id: 'ac9cf9c2-4d6a-4019-9585-2d91878c602c',
  cloud_account_id: 'ef5c9907-4c86-4dc3-966a-954c797f2ab3',
  subject_type: 'pod',
  service_key: 'demo/load-generator',
  subject_namespace: 'demo',
  subject_name: 'load-generator-5c78dd9b76-kgf2n',
  subject_owner: 'load-generator',
  subject_owner_kind: 'deployment',
  starts_at: '2026-09-28T08:10:22.000',
  evidences,
};
const deployment = {
  data: {
    k8s_workloads: [
      {
        cloud_resource_id: 'res-1',
        meta: {
          config: {
            annotations: {},
            labels: {},
            containers: [
              {
                name: 'load-generator',
                resources: { requests: { cpu: '64m', memory: '512Mi' }, limits: { cpu: '64m', memory: '512Mi' } },
              },
            ],
          },
        },
      },
    ],
  },
};

const inputValues = () => Object.fromEntries(Array.from(document.querySelectorAll('input')).map((i) => [i.name, i.value]));

const openTuneModal = async () => {
  const card = new MemoryAllocationCard();
  await card.canRenderContent(evidences, event);
  const Resolve = card.getResolveComponent();
  render(<Resolve open onCloseComponent={jest.fn()} />);
  await waitFor(() => expect(document.querySelectorAll('input').length).toBeGreaterThan(0));
};

const submitButton = () => screen.getAllByRole('button', { name: 'Update' }).find((b) => b.getAttribute('type') === 'submit');

describe('Tune Resource Configuration — request never above the limit', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    mockGetK8sWorkload.mockResolvedValue(deployment);
  });

  it('proposes a request no higher than the limit when the evidence double-counted the request', async () => {
    await openTuneModal();

    // Both from the 512 MB limit plus the default 10% buffer — not 1126.4 / 563.2.
    await waitFor(() => expect(inputValues()).toMatchObject({ memoryRequest: '563.2', memoryLimit: '563.2' }));
    expect(screen.getByTestId('memory-headroom-check')).not.toHaveTextContent('Request is above the limit');
  });

  it('says so, and refuses to apply, when a request above the limit is entered', async () => {
    await openTuneModal();
    await waitFor(() => expect(inputValues()).toMatchObject({ memoryLimit: '563.2' }));

    fireEvent.change(document.querySelector('input[name="memoryRequest"]'), { target: { value: '1126.4' } });
    await waitFor(() =>
      expect(screen.getByTestId('memory-headroom-check')).toHaveTextContent('Request is above the limit (563 MB) — Kubernetes will refuse it.')
    );

    fireEvent.click(submitButton());
    await waitFor(() => expect(mockToastError).toHaveBeenCalledWith(expect.stringContaining('Memory request is above the memory limit')));
    expect(mockApplyRecommendation).not.toHaveBeenCalled();
  });
});
