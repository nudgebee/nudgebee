import React from 'react';
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import DashboardView from '../DashboardView';
import type { AccountOption, Dashboard, Panel } from '@api1/dashboards';

/*
 * Editing a dashboard as JSON goes through the edit-mode draft: the JSON is
 * reviewed as a diff, applied to the draft, and stored by the ordinary Save.
 */

const saveDashboard = jest.fn();

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
  default: { saveDashboard: (...args: unknown[]) => saveDashboard(...args) },
  EMPTY_DEFINITION: { panels: [] },
  isCommandDatasource: () => false,
  isKubernetesOnlyDatasource: () => false,
  KUBERNETES_ACCOUNT_KIND: 'kubernetes',
}));

jest.mock('@ui/Chart', () => ({ __esModule: true, default: { Line: () => <div data-testid='line-chart' />, Bar: () => null } }));

jest.mock('@shared/widgets/CustomDateTimeRangePicker', () => ({ __esModule: true, default: () => <div data-testid='range-picker' /> }));

// CodeMirror cannot run in jsdom; the editor is its text.
jest.mock('@ui/CodeEditor', () => ({
  __esModule: true,
  CodeEditor: ({ value, onChange }: { value: string; onChange: (next: string) => void }) => (
    <textarea data-testid='json-editor' value={value} onChange={(e) => onChange(e.target.value)} />
  ),
}));

// The diff is the design system's; what matters here is what it is handed.
jest.mock('@ui/DiffViewer', () => ({
  __esModule: true,
  DiffViewer: ({ originalCode, newCode, mode }: { originalCode: string; newCode: string; mode: string }) => (
    <div data-testid='json-diff' data-mode={mode} data-before={originalCode} data-after={newCode} />
  ),
}));

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

const dashboard = { id: 'dash-1', title: 'Fleet', description: '', definition: { panels: [panel] } } as unknown as Dashboard;

const editor = () => screen.getByTestId('json-editor') as HTMLTextAreaElement;
const editJson = (change: (d: any) => void) => {
  const d = JSON.parse(editor().value);
  change(d);
  fireEvent.change(editor(), { target: { value: JSON.stringify(d, null, 2) } });
};

describe('editing a dashboard as JSON', () => {
  beforeEach(() => {
    saveDashboard.mockReset().mockImplementation(async (req: any) => ({ data: { ...dashboard, ...req, id: 'dash-1' } }));
    (global as any).IntersectionObserver = SeenObserver;
    (global as any).ResizeObserver = SeenObserver;
  });
  afterEach(cleanup);

  const open = () => {
    render(<DashboardView dashboard={dashboard} accounts={accounts} canEdit />);
    fireEvent.click(screen.getByTestId('dashboard-edit-btn'));
    fireEvent.click(screen.getByTestId('dashboard-edit-json-btn'));
  };

  it('opens on the draft as Save would store it, with nothing to review yet', () => {
    open();
    expect(JSON.parse(editor().value)).toMatchObject({ title: 'Fleet', definition: { panels: [{ id: 1, title: 'CPU' }] } });
    expect(screen.getByTestId('dashboard-json-review-btn')).toBeDisabled();
    expect(screen.getByTestId('dashboard-json-unchanged')).toBeInTheDocument();
  });

  it('shows the difference, applies it to the draft, and stores it with the ordinary Save', async () => {
    open();
    editJson((d) => {
      d.title = 'Fleet overview';
      d.definition.panels[0].title = 'CPU used';
      d.definition.time_from = 'now-24h';
    });
    fireEvent.click(screen.getByTestId('dashboard-json-review-btn'));

    const diff = screen.getByTestId('json-diff');
    expect(diff).toHaveAttribute('data-mode', 'split');
    expect(diff.getAttribute('data-before')).toContain('"CPU"');
    expect(diff.getAttribute('data-after')).toContain('"CPU used"');

    fireEvent.click(screen.getByTestId('dashboard-json-apply-btn'));
    // Nothing is written by Apply — only the draft changes.
    expect(saveDashboard).not.toHaveBeenCalled();
    expect(screen.queryByTestId('json-editor')).not.toBeInTheDocument();

    await act(async () => {
      fireEvent.click(screen.getByTestId('dashboard-save-btn'));
    });
    expect(saveDashboard).toHaveBeenCalledTimes(1);
    expect(saveDashboard.mock.calls[0][0]).toMatchObject({
      title: 'Fleet overview',
      definition: { time_from: 'now-24h', panels: [{ id: 1, title: 'CPU used' }] },
    });
  });

  it('holds Review until the JSON is something Save would take', () => {
    open();
    editJson((d) => (d.definition.panels[0].account_ids = ['acc-9']));
    expect(screen.getByTestId('dashboard-json-errors')).toHaveTextContent('not available to you here: acc-9');
    expect(screen.getByTestId('dashboard-json-review-btn')).toBeDisabled();
  });

  it('brings the author back to the JSON, with the reason, when Save refuses it', async () => {
    saveDashboard.mockResolvedValueOnce({ errors: [{ message: 'panel "CPU used": a table is required for this datasource' }] });
    open();
    editJson((d) => (d.definition.panels[0].title = 'CPU used'));
    fireEvent.click(screen.getByTestId('dashboard-json-review-btn'));
    fireEvent.click(screen.getByTestId('dashboard-json-apply-btn'));

    await act(async () => {
      fireEvent.click(screen.getByTestId('dashboard-save-btn'));
    });

    await waitFor(() => expect(screen.getByTestId('dashboard-json-save-error')).toHaveTextContent('a table is required for this datasource'));
    // Reopened on what was applied, not on the saved dashboard.
    expect(JSON.parse(editor().value).definition.panels[0].title).toBe('CPU used');
  });

  it('reopens on the draft as it is now, keeping canvas edits made after the JSON was applied', async () => {
    saveDashboard.mockResolvedValueOnce({ errors: [{ message: 'refused' }] });
    open();
    editJson((d) => (d.definition.panels[0].title = 'CPU used'));
    fireEvent.click(screen.getByTestId('dashboard-json-review-btn'));
    fireEvent.click(screen.getByTestId('dashboard-json-apply-btn'));
    // An ordinary edit after the apply.
    fireEvent.change(screen.getByTestId('dashboard-title-input'), { target: { value: 'Renamed on the canvas' } });

    await act(async () => {
      fireEvent.click(screen.getByTestId('dashboard-save-btn'));
    });

    await waitFor(() => expect(screen.getByTestId('dashboard-json-save-error')).toBeInTheDocument());
    const reopened = JSON.parse(editor().value);
    expect(reopened.title).toBe('Renamed on the canvas');
    expect(reopened.definition.panels[0].title).toBe('CPU used');
  });

  it('marks the draft unsaved when only a dashboard setting changed', async () => {
    open();
    editJson((d) => (d.definition.refresh = '1m'));
    fireEvent.click(screen.getByTestId('dashboard-json-review-btn'));
    fireEvent.click(screen.getByTestId('dashboard-json-apply-btn'));
    await act(async () => {
      fireEvent.click(screen.getByTestId('dashboard-save-btn'));
    });
    // A clean draft leaves without a request; this one is written.
    expect(saveDashboard).toHaveBeenCalledTimes(1);
    expect(saveDashboard.mock.calls[0][0].definition.refresh).toBe('1m');
  });

  it('diffs a long dashboard section by section instead of side by side', () => {
    const many = {
      ...dashboard,
      definition: { panels: Array.from({ length: 40 }, (_, i) => ({ ...panel, id: i + 1, title: `CPU ${i + 1}` })) },
    } as unknown as Dashboard;
    render(<DashboardView dashboard={many} accounts={accounts} canEdit />);
    fireEvent.click(screen.getByTestId('dashboard-edit-btn'));
    fireEvent.click(screen.getByTestId('dashboard-edit-json-btn'));
    editJson((d) => (d.definition.panels[3].title = 'renamed'));
    fireEvent.click(screen.getByTestId('dashboard-json-review-btn'));
    expect(screen.getByTestId('json-diff')).toHaveAttribute('data-mode', 'unified');
    expect(within(screen.getByRole('dialog')).getByTestId('dashboard-json-diff-unified')).toBeInTheDocument();
  });
});
