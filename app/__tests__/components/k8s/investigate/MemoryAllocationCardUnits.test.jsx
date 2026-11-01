import React from 'react';
import { render, screen } from '@testing-library/react';
import '@testing-library/jest-dom';

// jsdom has no canvas; the chart titles and the series handed to each chart are what matter here.
jest.mock('@ui/Chart', () => ({
  __esModule: true,
  default: { Line: ({ dataset }) => <div data-testid='chart' data-series={JSON.stringify(dataset.map((d) => d.data))} /> },
}));

import MemoryAllocationCard from '@components/k8s/investigate/cards/MemoryAllocationCard';

const MIB = 1024 * 1024;
const GIB = 1024 * MIB;

const blob = (body) => ({ type: 'json', data: JSON.stringify(body) });
const podElement = (values, requests = {}, limits = {}) => ({
  metric: { container: 'app', pod: 'app-1-2', namespace: 'ns', requests, limits },
  timestamps: [1, 2, 3],
  values: values.map(String),
});

const renderCard = async (evidences) => {
  const card = new MemoryAllocationCard();
  const event = { id: 'e', subject_type: 'pod', subject_name: 'app-1-2', starts_at: '2026-09-17T00:00:00', evidences };
  await card.canRenderContent(evidences, event);
  const components = card.getContentComponents();
  render(
    <>
      {components.map((Component, i) => (
        <Component key={i} />
      ))}
    </>
  );
  return card;
};

describe('MemoryAllocationCard — units on the resource charts', () => {
  it('keeps each pod_metric blob’s own resource type when both are present', async () => {
    const card = await renderCard([
      blob({
        name: 'pod_metric',
        resource_type: 'memory',
        data: [podElement([100 * MIB, 200 * MIB, 150 * MIB], { memory: 64 * MIB }, { memory: 256 * MIB })],
      }),
      blob({ name: 'pod_metric', resource_type: 'cpu', data: [podElement([0.0004, 0.25, 0.1], { cpu: 0.1 }, { cpu: 1 })] }),
    ]);
    expect(card.podMemoryAllocationItem.map((i) => i.resource_type)).toEqual(['memory', 'cpu']);
    expect(screen.getByText('Pod memory (MiB)')).toBeInTheDocument();
    expect(screen.getByText('Pod cpu (millicores)')).toBeInTheDocument();
    // The memory-leak guide belongs to the memory chart, whichever blob came last.
    expect(screen.getByText('Memory Leak')).toBeInTheDocument();
  });

  it('draws sub-unit usage unrounded, so 0.4 millicores is not 0', async () => {
    const card = await renderCard([blob({ name: 'pod_metric', resource_type: 'cpu', data: [podElement([0.0004, 0.25, 0.1])] })]);
    // Plus the duplicated point the card inserts at the event's start time.
    expect(card.podGraphData[0].data).toEqual(expect.arrayContaining([0.4, 250, 100]));
    expect(card.podGraphData[0].data).not.toContain(0);
    expect(screen.queryByText('Memory Leak')).not.toBeInTheDocument();
  });

  it('plots node CPU in cores instead of converting it as bytes', async () => {
    const card = await renderCard([blob({ name: 'node_metric', resource_type: 'CPU', data: [{ timestamps: [1, 2], values: ['1.5', '3.25'] }] })]);
    expect(card.nodeGraphData.data).toEqual([1.5, 3.25]);
    expect(screen.getByText('Node cpu (cores)')).toBeInTheDocument();
  });

  it('plots node memory in GiB', async () => {
    const card = await renderCard([
      blob({ name: 'node_metric', resource_type: 'Memory', data: [{ timestamps: [1, 2], values: [String(2 * GIB), String(1536.5 * GIB)] }] }),
    ]);
    // Above 1,000 GiB too, where a locale-formatted string read back as 1.
    expect(card.nodeGraphData.data).toEqual([2, 1536.5]);
    expect(screen.getByText('Node memory (GiB)')).toBeInTheDocument();
  });

  it('a node_metric does not relabel the pod chart', async () => {
    await renderCard([
      blob({ name: 'pod_metric', resource_type: 'memory', data: [podElement([100 * MIB, 200 * MIB, 150 * MIB])] }),
      blob({ name: 'node_metric', resource_type: 'cpu', data: [{ timestamps: [1, 2, 3], values: ['1', '2', '3'] }] }),
    ]);
    expect(screen.getByText('Pod memory (MiB)')).toBeInTheDocument();
    expect(screen.getByText('Node cpu (cores)')).toBeInTheDocument();
  });
});
