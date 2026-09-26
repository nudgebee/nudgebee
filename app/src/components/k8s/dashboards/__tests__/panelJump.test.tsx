import React from 'react';
import { act, fireEvent, render, screen } from '@testing-library/react';
import DashboardView from '../DashboardView';
import DashboardPanel from '../DashboardPanel';
import { PANEL_PARAM, withPanelParam } from '../panelLink';
import type { Dashboard, Panel } from '@api1/dashboards';

const replace = jest.fn();
let query: Record<string, string> = {};

jest.mock('next/router', () => ({
  useRouter: () => ({ events: { on: jest.fn(), off: jest.fn() }, push: jest.fn(), replace, asPath: '/dashboards', query, isReady: true }),
}));

jest.mock('@api1/dashboards', () => ({
  __esModule: true,
  default: { updateDashboard: jest.fn(), createDashboard: jest.fn() },
  EMPTY_DEFINITION: { panels: [] },
  isCommandDatasource: () => false,
}));

jest.mock('@shared/widgets/CustomDateTimeRangePicker', () => ({ __esModule: true, default: () => <div /> }));

const snackbarSuccess = jest.fn();
jest.mock('@ui/Toast', () => ({ snackbar: { success: (...a: unknown[]) => snackbarSuccess(...a), error: jest.fn(), warning: jest.fn() } }));

// Text panels fetch nothing, so the view renders without any provider mocked.
const textPanel = (id: number, title: string): Panel =>
  ({ id, title, type: 'text', content: title, grid_pos: { x: 0, y: 0, w: 12, h: 4 } } as unknown as Panel);

const dashboard = {
  id: 'dash-1',
  title: 'Fleet',
  description: '',
  definition: { panels: [textPanel(1, 'CPU'), textPanel(2, 'Memory'), textPanel(3, 'Pod restarts')] },
} as unknown as Dashboard;

let scrolled: string[] = [];

beforeEach(() => {
  jest.useFakeTimers();
  replace.mockClear();
  snackbarSuccess.mockClear();
  query = {};
  scrolled = [];
  window.HTMLElement.prototype.scrollIntoView = function scrollIntoView(this: HTMLElement) {
    scrolled.push(this.getAttribute('data-testid') || '');
  };
});

afterEach(() => jest.useRealTimers());

describe('withPanelParam', () => {
  it('sets and clears the panel, keeping the rest of the address', () => {
    const linked = withPanelParam('https://app.test/dashboards?dashboard=d1#dashboards', '7');
    expect(linked).toBe('https://app.test/dashboards?dashboard=d1&panel=7#dashboards');
    expect(withPanelParam(linked, null)).toBe('https://app.test/dashboards?dashboard=d1#dashboards');
  });
});

describe('Jump to panel', () => {
  it('scrolls to the picked panel, outlines it, and names it in the URL', () => {
    render(<DashboardView dashboard={dashboard} accounts={[]} />);

    fireEvent.click(screen.getByTestId('dashboard-jump-btn'));
    // The menu is a MUI Menu, which marks the rest of the tree aria-hidden while it is open.
    fireEvent.click(screen.getByRole('menuitem', { name: /Pod restarts/, hidden: true }));

    expect(scrolled).toEqual(['sortable-panel-3']);
    expect(screen.getByTestId('sortable-panel-3')).toHaveStyle({ outline: `2px solid ${'var(--ds-blue-500)'}` });
    expect(replace).toHaveBeenCalledWith(expect.stringContaining(`${PANEL_PARAM}=3`), undefined, { shallow: true, scroll: false });

    // The outline is a pointer, not a state — it fades.
    act(() => jest.advanceTimersByTime(2000));
    expect(screen.getByTestId('sortable-panel-3')).not.toHaveStyle({ outline: `2px solid ${'var(--ds-blue-500)'}` });
  });

  it('follows a link naming a panel once the dashboard has drawn', () => {
    query = { [PANEL_PARAM]: '2' };
    render(<DashboardView dashboard={dashboard} accounts={[]} />);
    act(() => jest.advanceTimersByTime(20));
    expect(scrolled).toEqual(['sortable-panel-2']);
  });

  it('ignores a link to a panel this dashboard does not have', () => {
    query = { [PANEL_PARAM]: '99' };
    render(<DashboardView dashboard={dashboard} accounts={[]} />);
    act(() => jest.advanceTimersByTime(20));
    expect(scrolled).toEqual([]);
  });
});

describe('Copy link', () => {
  it('copies the page address with this panel named in it', async () => {
    const writeText = jest.fn().mockResolvedValue(undefined);
    Object.assign(navigator, { clipboard: { writeText } });
    render(<DashboardPanel panel={textPanel(5, 'Notes')} accounts={[]} variables={{}} startTime={0} endTime={1} />);

    fireEvent.click(screen.getByRole('button', { name: 'More actions' }));
    await act(async () => {
      fireEvent.click(screen.getByRole('menuitem', { name: /Copy link/, hidden: true }));
    });

    expect(writeText).toHaveBeenCalledWith(expect.stringContaining(`${PANEL_PARAM}=5`));
    expect(snackbarSuccess).toHaveBeenCalled();
  });
});
