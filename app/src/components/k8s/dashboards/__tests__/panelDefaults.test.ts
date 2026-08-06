import { blankPanel, nextPanelId } from '../panelDefaults';
import type { Panel } from '@api1/dashboards';

function panel(id: number): Panel {
  return { id, title: `p${id}`, type: 'timeseries', datasource: 'metrics', grid_pos: { x: 0, y: 0, w: 6, h: 8 } };
}

describe('nextPanelId', () => {
  it('goes past the highest id, not the count', () => {
    // Removing a panel leaves a hole; reusing an id would make a saved edit
    // land on the wrong panel.
    expect(nextPanelId([panel(1), panel(7)])).toBe(8);
    expect(nextPanelId([])).toBe(1);
  });
});

describe('blankPanel', () => {
  it('carries nothing over from the panels already on the dashboard', () => {
    const fresh = blankPanel([{ ...panel(3), account_type: 'K8S', account_ids: ['acc-1'], unit: 'ms', targets: [{ ref_id: 'A', expr: 'up' }] }]);
    expect(fresh.id).toBe(4);
    expect(fresh.title).toBe('');
    expect(fresh.account_type).toBeUndefined();
    expect(fresh.account_ids).toEqual([]);
    expect(fresh.unit).toBe('');
    expect(fresh.targets).toEqual([{ ref_id: 'A', expr: '' }]);
  });

  it('hands back a new object each time', () => {
    // Both editors mutate their copy through setState; a shared default would
    // leak one author's half-finished panel into the next.
    const a = blankPanel([]);
    const b = blankPanel([]);
    expect(a).not.toBe(b);
    expect(a.targets).not.toBe(b.targets);
  });
});
