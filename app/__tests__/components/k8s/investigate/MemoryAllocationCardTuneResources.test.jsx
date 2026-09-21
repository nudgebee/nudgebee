import React from 'react';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import '@testing-library/jest-dom';

jest.mock('@api1/tickets', () => ({
  __esModule: true,
  default: { listTicketConfigsForCreate: jest.fn(() => Promise.resolve({ data: [] })) },
}));
jest.mock('@api1/recommendation', () => ({ __esModule: true, default: { applyRecommendation: jest.fn() } }));
jest.mock('@api1/kubernetes', () => ({
  __esModule: true,
  default: {
    // The workload this event points at is no longer active, so the form's spec
    // lookup finds nothing — everything below comes from the event evidence.
    getK8sWorkload: jest.fn(() => Promise.resolve({ data: { k8s_workloads: [] } })),
    getResourceAttributes: jest.fn(() => Promise.resolve([])),
  },
}));

import MemoryAllocationCard from '@components/k8s/investigate/cards/MemoryAllocationCard';

// Verbatim evidence of event 358231de (report-worker / namespace-232, OOMKilled):
// the container is allocated 128Mi/192Mi, and the usage series is four samples of
// 2.74MB — all that was scraped before the container died.
const metric = {
  container: 'report-worker',
  limits: { cpu: 0.3, memory: 201326592 },
  namespace: 'namespace-232',
  pod: 'report-worker',
  requests: { cpu: 0.05, memory: 134217728 },
};
const evidences = [
  { type: 'json', data: JSON.stringify({ name: 'pod_metric', data: [{ metric, timestamps: [], values: [] }], resource_type: 'cpu' }) },
  {
    type: 'json',
    data: JSON.stringify({
      name: 'pod_metric',
      data: [{ metric, timestamps: [1789045102, 1789045104, 1789045106, 1789045108], values: ['2740224', '2740224', '2740224', '2740224'] }],
      resource_type: 'memory',
    }),
  },
];

const event = {
  id: '358231de-f009-4671-9720-736d5062d197',
  cloud_account_id: 'a2a30b02-0f67-42e5-a2ab-c658230fd798',
  subject_type: 'pod',
  service_key: 'namespace-232/report-worker',
  subject_namespace: 'namespace-232',
  subject_name: 'report-worker-76c589bdc5-6t87j',
  starts_at: '2026-09-10T07:33:45.000',
  evidences,
};

const inputValues = () => Object.fromEntries(Array.from(document.querySelectorAll('input')).map((i) => [i.name, i.value]));

const openTuneModal = async () => {
  const card = new MemoryAllocationCard();
  await card.canRenderContent(evidences, event);
  const Resolve = card.getResolveComponent();
  render(<Resolve open onCloseComponent={jest.fn()} />);
  await waitFor(() => expect(document.querySelectorAll('input').length).toBeGreaterThan(0));
};

describe('Tune Resource Configuration — allocation vs observed usage', () => {
  it('shows the allocated CPU and memory as Current', async () => {
    await openTuneModal();

    expect(screen.getByText('0.05')).toBeInTheDocument();
    // The CPU limit is in the evidence; the card used to drop it, so Current
    // claimed the workload had none.
    expect(screen.getByText('0.3')).toBeInTheDocument();
    expect(screen.getByText('128')).toBeInTheDocument();
    expect(screen.getByText('192')).toBeInTheDocument();
  });

  it('never buffers off a usage peak below the current allocation', async () => {
    await openTuneModal();
    expect(inputValues()).toMatchObject({ memoryRequest: '140.8', memoryLimit: '211.2' });

    const buffers = screen.getAllByText('+15% buffer');
    fireEvent.click(buffers[buffers.length - 1]);

    // 128 * 1.15 and 192 * 1.15 — not 2.61 * 1.15 (= 3.01) off the truncated
    // post-OOM usage series, which would shrink an OOMKilled workload.
    await waitFor(() => expect(inputValues()).toMatchObject({ memoryRequest: '147.2', memoryLimit: '220.8' }));
  });
});
