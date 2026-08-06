import type { Panel } from '@api1/dashboards';

/**
 * The shape a brand-new panel starts from.
 *
 * Shared by the dashboard editor and the in-place "Add panel" action on the
 * dashboard view so both open the same blank form — a second copy of these
 * defaults would drift the moment one of them gained a field.
 */

/** Panel ids only need to be unique inside one dashboard. */
export function nextPanelId(panels: Panel[]): number {
  return panels.reduce((max, p) => Math.max(max, p.id), 0) + 1;
}

/**
 * Every field starts empty. Nothing is carried over from the previously
 * authored panel — a seeded account or query the author does not notice is
 * worse than picking one again.
 */
export function blankPanel(panels: Panel[]): Panel {
  return {
    id: nextPanelId(panels),
    title: '',
    description: '',
    type: 'timeseries',
    datasource: 'metrics',
    account_type: undefined,
    account_ids: [],
    grid_pos: { x: 0, y: 0, w: 6, h: 8 },
    targets: [{ ref_id: 'A', expr: '' }],
    content: '',
    unit: '',
  };
}
