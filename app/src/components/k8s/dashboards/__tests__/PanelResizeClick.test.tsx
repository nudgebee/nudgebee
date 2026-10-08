import React from 'react';
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';
import DashboardView from '../DashboardView';
import type { Dashboard } from '@api1/dashboards';

/*
 * In edit mode a click on a panel opens its editor. A resize is a press on the
 * panel's right edge and a release somewhere else, and the browser turns that
 * pair into a click on whatever the two have in common — the panel, once it has
 * snapped wider under the pointer. So widening a panel opened its editor. The
 * handle stopped its own click, which only covered a release on the handle.
 */

jest.mock('next/router', () => ({
  useRouter: () => ({ events: { on: jest.fn(), off: jest.fn() }, push: jest.fn(), replace: jest.fn(), asPath: '/x', query: {} }),
}));
jest.mock('@api1/observability', () => ({ __esModule: true, default: { metricsQuery: jest.fn(), fetchLogs: jest.fn() } }));
jest.mock('@api1/account', () => ({ __esModule: true, default: { getDefaultProvider: async () => ({ data: { data: null } }) } }));
jest.mock('@api1/dashboards', () => ({
  __esModule: true,
  default: { updateDashboard: jest.fn(), createDashboard: jest.fn() },
  EMPTY_DEFINITION: { panels: [] },
  isCommandDatasource: () => false,
  isKubernetesOnlyDatasource: () => false,
  KUBERNETES_ACCOUNT_KIND: 'kubernetes',
}));
jest.mock('@ui/Chart', () => ({ __esModule: true, default: { Line: () => null, Bar: () => null } }));
jest.mock('@shared/widgets/CustomDateTimeRangePicker', () => ({ __esModule: true, default: () => null }));

const dashboard = {
  id: 'dash-1',
  title: 'Capacity review',
  description: '',
  definition: {
    panels: [{ id: 1, title: 'Notes', type: 'text', datasource: 'metrics', grid_pos: { x: 0, y: 0, w: 6, h: 4 }, content: 'capacity notes' }],
  },
} as unknown as Dashboard;

const GRID_WIDTH = 1200;
const panel = () => screen.getByTestId('sortable-panel-1');
const handle = () => screen.getByTestId('resize-panel-1');
const editorOpen = () => screen.queryByText('Edit panel') !== null;

/** Press on the right edge, move, release — the release is a mouseup on the document, as in a browser. */
function drag(fromX: number, toX: number) {
  fireEvent.mouseDown(handle(), { clientX: fromX });
  fireEvent.mouseMove(document, { clientX: toX });
  fireEvent.mouseUp(document, { clientX: toX });
}

describe('resizing a panel by dragging its edge', () => {
  let rect: jest.SpyInstance;

  beforeEach(() => {
    jest.useFakeTimers();
    // jsdom lays nothing out; the resize needs a grid width to know what a column is.
    rect = jest
      .spyOn(HTMLElement.prototype, 'getBoundingClientRect')
      .mockReturnValue({ width: GRID_WIDTH, height: 400, top: 0, left: 0, right: GRID_WIDTH, bottom: 400, x: 0, y: 0, toJSON: () => ({}) });
    render(<DashboardView dashboard={dashboard} accounts={[]} canEdit initialEditing onBack={jest.fn()} onChange={jest.fn()} />);
  });
  afterEach(() => {
    rect.mockRestore();
    jest.useRealTimers();
    cleanup();
  });

  it('does not open the editor when the release lands on the panel', () => {
    drag(600, 1100);
    // What the browser dispatches next: a click on the panel, the press and the release's common ancestor.
    fireEvent.click(panel());

    expect(editorOpen()).toBe(false);
    expect(panel()).toHaveStyle({ gridColumn: 'span 12' });
  });

  it('still opens the editor on the next real click', () => {
    drag(600, 1100);
    fireEvent.click(panel());
    act(() => {
      jest.runOnlyPendingTimers();
    });

    fireEvent.click(panel());

    expect(editorOpen()).toBe(true);
  });

  it('does not eat a later click when the release made none', () => {
    // Released outside the window: a mouseup, and no click after it.
    drag(600, 1100);
    act(() => {
      jest.runOnlyPendingTimers();
    });

    fireEvent.click(panel());

    expect(editorOpen()).toBe(true);
  });
});
