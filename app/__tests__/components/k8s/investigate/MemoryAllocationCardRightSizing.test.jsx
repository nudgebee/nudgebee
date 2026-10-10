import React from 'react';
import { render, screen, fireEvent, waitFor, within } from '@testing-library/react';
import '@testing-library/jest-dom';

const mockGetK8sRecommendation = jest.fn();

// jsdom has no canvas; stand the chart in with its data so the sparkline path
// is exercised instead of failing inside its error boundary.
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
  default: { applyRecommendation: jest.fn(), getK8sRecommendation: (...args) => mockGetK8sRecommendation(...args) },
}));
jest.mock('@api1/kubernetes', () => ({
  __esModule: true,
  default: {
    getK8sWorkload: jest.fn(() => Promise.resolve({ data: { k8s_workloads: [] } })),
    getResourceAttributes: jest.fn(() => Promise.resolve([])),
  },
}));

import MemoryAllocationCard from '@components/k8s/investigate/cards/MemoryAllocationCard';

const MIB = 1024 * 1024;

// #40631, event 90e15f52 on prod: nbdemo-leak's app container was OOMKilled at its
// 512Mi limit. The tune screen proposed 512 x 1.1 = 563.2 MB while Optimize > Right
// Sizing showed the stored recommendation, 640 MB, for the same workload.
const app = {
  container: 'app',
  namespace: 'demo',
  pod: 'nbdemo-leak-6d9f7c8b5-x2k4p',
  requests: { cpu: 0.1, memory: 512 * MIB },
  limits: { cpu: 0.2, memory: 512 * MIB },
};
const evidences = [
  {
    type: 'table',
    data: {
      table_name: '*Pod and Node OOMKilled data*',
      rows: [
        ['Pod', 'nbdemo-leak-6d9f7c8b5-x2k4p'],
        ['Container name', 'app'],
      ],
    },
  },
  {
    type: 'json',
    data: JSON.stringify({
      name: 'pod_metric',
      data: [{ metric: app, timestamps: [1791442500, 1791442560, 1791442620], values: [String(430 * MIB), String(480 * MIB), String(510 * MIB)] }],
      resource_type: 'memory',
    }),
  },
];
const event = {
  id: '90e15f52-238a-4948-9d3e-98e6ceedac43',
  cloud_account_id: 'a60713af-6a89-4eb2-aa86-3dab0faee6e6',
  subject_type: 'pod',
  subject_namespace: 'demo',
  subject_name: 'nbdemo-leak-6d9f7c8b5-x2k4p',
  subject_owner: 'nbdemo-leak',
  subject_owner_kind: 'deployment',
  starts_at: '2026-10-08T10:30:00.000',
  evidences,
};

// A stored pod_right_sizing row as the recommendation API hands it back.
const rightSizingRow = (objectId, requestMb, limitMb = requestMb) => ({
  account_object_id: objectId,
  updated_at: '2026-10-08T00:12:00',
  recommendation: {
    app: [
      { resource: 'cpu', allocated: { request: 0.1, limit: 0.2 }, recommended: { request: 0.05, limit: null } },
      { resource: 'memory', allocated: { request: 256 * MIB, limit: 512 * MIB }, recommended: { request: requestMb * MIB, limit: limitMb * MIB } },
    ],
  },
});

const inputValues = () => Object.fromEntries(Array.from(document.querySelectorAll('input')).map((i) => [i.name, i.value]));

// The memory buffer group is the one offering +20%; CPU's stops at +15%.
const memoryBufferGroup = () => screen.getByRole('radio', { name: '+20% buffer' }).closest('[role="group"]');

const openTuneModal = async () => {
  const card = new MemoryAllocationCard();
  await card.canRenderContent(evidences, event);
  const Resolve = card.getResolveComponent();
  render(<Resolve open onCloseComponent={jest.fn()} />);
  await waitFor(() => expect(document.querySelectorAll('input').length).toBeGreaterThan(0));
};

describe('Tune Resource Configuration — the stored right-sizing figure', () => {
  beforeEach(() => {
    jest.clearAllMocks();
  });

  it('proposes the figure Optimize shows, with no buffer on top', async () => {
    mockGetK8sRecommendation.mockResolvedValue({ data: { recommendation: [rightSizingRow('demo/Deployment/nbdemo-leak', 640)] } });
    await openTuneModal();

    await waitFor(() => expect(inputValues()).toMatchObject({ memoryRequest: '640', memoryLimit: '640' }));
    expect(within(memoryBufferGroup()).getByRole('radio', { name: 'No buffer' })).toHaveAttribute('aria-checked', 'true');
    expect(screen.getByTestId('right-sizing-source')).toHaveTextContent('Right-sizing recommends 640 MB');
    expect(screen.getByTestId('right-sizing-source')).not.toHaveTextContent('current allocation is kept');
    expect(mockGetK8sRecommendation).toHaveBeenCalledWith(
      expect.objectContaining({
        accountId: event.cloud_account_id,
        ruleName: 'pod_right_sizing',
        resourceNamespace: 'demo',
        accountObjectId: 'demo/deployment/nbdemo-leak',
        // The exact key sorts ahead of every sibling sharing it as a prefix, so a
        // page of nbdemo-leak-* rows can't push the workload's own row off it.
        orderBy: 'account_object_id',
        orderAsc: true,
      })
    );

    // The buffer chips now add to the stored figure: 640 x 1.1.
    fireEvent.click(within(memoryBufferGroup()).getByRole('radio', { name: '+10% buffer' }));
    await waitFor(() => expect(inputValues()).toMatchObject({ memoryRequest: '704', memoryLimit: '704' }));
  });

  it('keeps the current allocation when the stored figure is below it, and says so', async () => {
    // Scanned before the workload was raised to 512Mi.
    mockGetK8sRecommendation.mockResolvedValue({ data: { recommendation: [rightSizingRow('demo/Deployment/nbdemo-leak', 400)] } });
    await openTuneModal();

    await waitFor(() => expect(screen.getByTestId('right-sizing-source')).toHaveTextContent('current allocation is kept'));
    expect(inputValues()).toMatchObject({ memoryRequest: '512', memoryLimit: '512' });
  });

  it("ignores another workload's row that shares the name as a prefix", async () => {
    mockGetK8sRecommendation.mockResolvedValue({ data: { recommendation: [rightSizingRow('demo/Deployment/nbdemo-leak-v2', 900)] } });
    await openTuneModal();

    // The event's own math: the 512 MB limit plus the default 10% buffer.
    await waitFor(() => expect(mockGetK8sRecommendation).toHaveBeenCalled());
    expect(inputValues()).toMatchObject({ memoryRequest: '563.2', memoryLimit: '563.2' });
    expect(screen.queryByTestId('right-sizing-source')).not.toBeInTheDocument();
  });
});
