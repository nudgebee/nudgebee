import React from 'react';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import DashboardView from '../DashboardView';
import type { AccountOption, Dashboard, Panel } from '@api1/dashboards';

/*
 * Two people editing one dashboard: a save made from a copy someone else has
 * since saved over is refused by the server (409), and the author chooses
 * between loading the newer version and writing theirs over it.
 */

const saveDashboard = jest.fn();
const getDashboard = jest.fn();

jest.mock('next/router', () => ({
  useRouter: () => ({ events: { on: jest.fn(), off: jest.fn() }, push: jest.fn(), replace: jest.fn(), asPath: '/x', query: {} }),
}));

jest.mock('@api1/observability', () => ({
  __esModule: true,
  default: { metricsQuery: jest.fn(() => new Promise(() => {})), fetchLogs: jest.fn() },
}));

jest.mock('@api1/account', () => ({ __esModule: true, default: { getDefaultProvider: async () => ({ data: { data: null } }) } }));

jest.mock('@api1/dashboards', () => ({
  __esModule: true,
  default: {
    saveDashboard: (...args: unknown[]) => saveDashboard(...args),
    getDashboard: (...args: unknown[]) => getDashboard(...args),
  },
  EMPTY_DEFINITION: { panels: [] },
  isCommandDatasource: () => false,
  isKubernetesOnlyDatasource: () => false,
  KUBERNETES_ACCOUNT_KIND: 'kubernetes',
}));

jest.mock('@ui/Chart', () => ({ __esModule: true, default: { Line: () => <div data-testid='line-chart' />, Bar: () => null } }));

jest.mock('@shared/widgets/CustomDateTimeRangePicker', () => ({ __esModule: true, default: () => <div data-testid='range-picker' /> }));

class SeenObserver {
  observe() {}
  unobserve() {}
  disconnect() {}
}

const accounts = [{ value: 'acc-1', label: 'prod' }] as AccountOption[];

const panel = {
  id: 1,
  title: 'CPU',
  type: 'stat',
  datasource: 'metrics',
  account_ids: ['acc-1'],
  grid_pos: { x: 0, y: 0, w: 6, h: 8 },
  targets: [{ ref_id: 'A', expr: 'sum(cpu)' }],
  unit: '',
} as Panel;

// What this screen loaded: revision 3.
const loaded = { id: 'dash-1', title: 'Fleet', description: '', version: 3, definition: { panels: [panel] } } as unknown as Dashboard;

// What a colleague saved after it was loaded: revision 4, with a panel of their own.
const theirs = {
  ...loaded,
  version: 4,
  updated_by_name: 'Ada Lovelace',
  updated_at: new Date().toISOString(),
  definition: { panels: [panel, { ...panel, id: 2, title: 'Memory' }] },
} as unknown as Dashboard;

const conflictResponse = {
  data: null,
  errors: [{ message: 'this dashboard was saved by someone else after you opened it', extensions: { upstream: { status: 409, body: {} } } }],
};

describe('saving over someone else’s save', () => {
  let onChange: jest.Mock;

  beforeEach(() => {
    onChange = jest.fn();
    saveDashboard.mockReset().mockImplementation(async (req: any) => ({ data: { ...loaded, ...req, version: req.expected_version + 1 } }));
    getDashboard.mockReset().mockResolvedValue({ data: { dashboard: theirs, bindings: [] } });
    (global as any).IntersectionObserver = SeenObserver;
    (global as any).ResizeObserver = SeenObserver;
  });
  afterEach(cleanup);

  /** Opens the editor, renames the dashboard, and presses Save. */
  const renameAndSave = () => {
    render(<DashboardView dashboard={loaded} accounts={accounts} canEdit onChange={onChange} />);
    fireEvent.click(screen.getByTestId('dashboard-edit-btn'));
    fireEvent.change(screen.getByTestId('dashboard-title-input'), { target: { value: 'Fleet (mine)' } });
    fireEvent.click(screen.getByTestId('dashboard-save-btn'));
  };

  it('sends the version it loaded, so the server can tell a stale copy', async () => {
    renameAndSave();
    await waitFor(() => expect(saveDashboard).toHaveBeenCalledTimes(1));
    expect(saveDashboard.mock.calls[0][0]).toMatchObject({ id: 'dash-1', title: 'Fleet (mine)', expected_version: 3 });
    expect(screen.queryByTestId('dashboard-conflict-message')).not.toBeInTheDocument();
  });

  it('names who saved in between instead of failing with a toast', async () => {
    saveDashboard.mockResolvedValueOnce(conflictResponse);
    renameAndSave();
    expect(await screen.findByTestId('dashboard-conflict-message')).toHaveTextContent('Ada Lovelace saved Fleet');
    expect(getDashboard).toHaveBeenCalledWith('dash-1');
    expect(onChange).not.toHaveBeenCalled();
  });

  it('Overwrite writes this copy over the newer version, and only that version', async () => {
    saveDashboard.mockResolvedValueOnce(conflictResponse);
    renameAndSave();
    fireEvent.click(await screen.findByTestId('dashboard-conflict-overwrite-btn'));

    await waitFor(() => expect(saveDashboard).toHaveBeenCalledTimes(2));
    // Checked against revision 4, so a third save landing meanwhile is refused too.
    expect(saveDashboard.mock.calls[1][0]).toMatchObject({ title: 'Fleet (mine)', expected_version: 4 });
    await waitFor(() => expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ title: 'Fleet (mine)', version: 5 })));
    await waitFor(() => expect(screen.queryByTestId('dashboard-conflict-message')).not.toBeInTheDocument());
    expect(screen.queryByTestId('dashboard-save-btn')).not.toBeInTheDocument();
  });

  it('Reload shows their version and drops the unsaved edits', async () => {
    saveDashboard.mockResolvedValueOnce(conflictResponse);
    renameAndSave();
    fireEvent.click(await screen.findByTestId('dashboard-conflict-reload-btn'));

    await waitFor(() => expect(onChange).toHaveBeenCalledWith(theirs));
    expect(saveDashboard).toHaveBeenCalledTimes(1);
    // Out of edit mode: the rename is gone with the rest of the draft.
    expect(screen.queryByTestId('dashboard-save-btn')).not.toBeInTheDocument();
    await waitFor(() => expect(screen.queryByTestId('dashboard-conflict-message')).not.toBeInTheDocument());
  });

  it('Cancel keeps the edits on screen, unsaved', async () => {
    saveDashboard.mockResolvedValueOnce(conflictResponse);
    renameAndSave();
    fireEvent.click(await screen.findByTestId('dashboard-conflict-cancel-btn'));

    await waitFor(() => expect(screen.queryByTestId('dashboard-conflict-message')).not.toBeInTheDocument());
    expect(screen.getByTestId('dashboard-title-input')).toHaveValue('Fleet (mine)');
    expect(saveDashboard).toHaveBeenCalledTimes(1);
    expect(onChange).not.toHaveBeenCalled();
  });

  it('any other refusal stays a plain error, with no conflict prompt', async () => {
    saveDashboard.mockResolvedValueOnce({
      data: null,
      errors: [{ message: 'panel 0: title is required', extensions: { upstream: { status: 400, body: {} } } }],
    });
    renameAndSave();
    await waitFor(() => expect(saveDashboard).toHaveBeenCalledTimes(1));
    expect(getDashboard).not.toHaveBeenCalled();
    expect(screen.queryByTestId('dashboard-conflict-message')).not.toBeInTheDocument();
    expect(screen.getByTestId('dashboard-save-btn')).toBeInTheDocument();
  });
});
