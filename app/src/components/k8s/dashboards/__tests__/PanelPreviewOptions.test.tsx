import React from 'react';
import { render } from '@testing-library/react';
import type { AccountOption, Panel } from '@api1/dashboards';
import PanelPreview from '../PanelPreview';

/*
 * The preview holds the draft back until its query settles, so typing does not
 * fire a request per character. A panel's `options` — how its accounts combine,
 * its thresholds — change nothing about that request, so they must reach the
 * preview at once. Held back with the query, switching a gauge from Average to
 * Sum left the preview showing the average until something else changed.
 */

const rendered: Panel[] = [];
jest.mock('../DashboardPanel', () => ({
  __esModule: true,
  default: (props: { panel: Panel }) => {
    rendered.push(props.panel);
    return <div data-testid='preview-panel' />;
  },
}));

const accounts = [
  { value: 'acc-1', label: 'prod-eu', cloud_provider: 'K8s' },
  { value: 'acc-2', label: 'prod-us', cloud_provider: 'K8s' },
] as AccountOption[];

const gauge = (options?: Record<string, unknown>): Panel =>
  ({
    id: 1,
    title: 'Memory used',
    type: 'gauge',
    datasource: 'metrics',
    account_ids: ['acc-1', 'acc-2'],
    grid_pos: { x: 0, y: 0, w: 6, h: 8 },
    targets: [{ ref_id: 'A', expr: 'avg(mem)' }],
    ...(options ? { options } : {}),
  } as unknown as Panel);

const mount = (panel: Panel) => <PanelPreview panel={panel} accountOptions={accounts} variables={{}} startTime={0} endTime={1} />;

describe('PanelPreview — panel options', () => {
  beforeEach(() => {
    rendered.length = 0;
  });

  it('shows a change to how the accounts combine straight away', () => {
    const { rerender } = render(mount(gauge()));
    rerender(mount(gauge({ combine: 'sum' })));
    expect(rendered[rendered.length - 1].options).toEqual({ combine: 'sum' });
  });

  it('shows a new threshold straight away', () => {
    const { rerender } = render(mount(gauge()));
    const thresholds = [{ value: 80, color: 'red' }];
    rerender(mount(gauge({ thresholds })));
    expect(rendered[rendered.length - 1].options).toEqual({ thresholds });
  });

  it('still holds a query edit back until the typing stops', () => {
    const { rerender } = render(mount(gauge()));
    rerender(mount({ ...gauge(), targets: [{ ref_id: 'A', expr: 'avg(mem) * 2' }] } as Panel));
    expect(rendered[rendered.length - 1].targets?.[0].expr).toBe('avg(mem)');
  });
});
