import type { Panel } from '@api1/dashboards';

/** The shape a brand-new panel starts from. */

/** Panel ids only need to be unique inside one dashboard. */
export function nextPanelId(panels: Panel[]): number {
  return panels.reduce((max, p) => Math.max(max, p.id), 0) + 1;
}

/** The widths a panel may take, in 12ths. */
export const PANEL_WIDTHS = [3, 4, 6, 8, 12];

/** Nearest legal width to a fractional column count. */
export function snapPanelWidth(columns: number): number {
  return PANEL_WIDTHS.reduce((best, w) => (Math.abs(w - columns) < Math.abs(best - columns) ? w : best), PANEL_WIDTHS[0]);
}

/** How many of the 12 columns the panel occupies. */
export function panelSpan(panel: Panel): number {
  return Math.min(Math.max(panel.grid_pos?.w || 12, 1), 12);
}

/** Row height in px. `h` is in grid rows, mirroring the Grafana panel model. */
export function panelMinHeight(panel: Panel): number {
  return (panel.grid_pos?.h || 8) * 30;
}

/** Every field starts empty. */
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
