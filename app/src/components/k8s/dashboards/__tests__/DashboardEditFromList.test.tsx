import React from 'react';
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import CustomDashboards from '../CustomDashboards';
import apiDashboards from '@api1/dashboards';

/*
 * The listing's pencil means "edit THIS dashboard". The page remembered it as a
 * flag that outlived the dashboard it was said about, so the next one opened in
 * place — the header search rewrites ?dashboard= without going back to the list —
 * arrived in edit mode too, one drag away from a change nobody asked for.
 */

const mockRoute = { current: { asPath: '/dashboards', query: {} as Record<string, string> } };
const mockListeners = new Set<() => void>();

/** What the router does on a shallow push: the query changes and the page re-renders. */
function mockNavigate(url: string) {
  const params = new URLSearchParams(url.split('?')[1] || '');
  mockRoute.current = { asPath: url, query: Object.fromEntries(params) };
  mockListeners.forEach((rerender) => rerender());
  return Promise.resolve(true);
}

jest.mock('next/router', () => {
  const { useEffect, useMemo, useReducer } = jest.requireActual('react');
  const events = { on: jest.fn(), off: jest.fn(), emit: jest.fn() };
  return {
    useRouter: () => {
      const [, rerender] = useReducer((n: number) => n + 1, 0);
      useEffect(() => {
        mockListeners.add(rerender);
        return () => {
          mockListeners.delete(rerender);
        };
      }, []);
      const route = mockRoute.current;
      // One object per route, as Next hands out — the page keys callbacks on it.
      return useMemo(() => ({ isReady: true, events, push: mockNavigate, replace: jest.fn(), asPath: route.asPath, query: route.query }), [route]);
    },
  };
});

jest.mock('@context/DataContext', () => ({
  useData: () => ({ allCluster: [{ label: 'k8s-prod', value: 'k8s-1', cloud_provider: 'K8s', account_type: 'kubernetes', status: 'active' }] }),
}));
jest.mock('@lib/auth', () => ({
  ...jest.requireActual('@lib/auth'),
  canManage: () => true,
  hasWriteAccess: () => true,
  isGrantsOnlyUser: () => false,
}));
jest.mock('@api1/observability', () => ({ __esModule: true, default: { metricsQuery: jest.fn(), fetchLogs: jest.fn() } }));
jest.mock('@api1/dashboards', () => ({
  __esModule: true,
  default: { listDashboards: jest.fn(), getDashboard: jest.fn(), updateDashboard: jest.fn(), createDashboard: jest.fn(), deleteDashboard: jest.fn() },
  EMPTY_DEFINITION: { panels: [] },
  isCommandDatasource: () => false,
  isKubernetesOnlyDatasource: () => false,
  KUBERNETES_ACCOUNT_KIND: 'kubernetes',
}));
jest.mock('@ui/Chart', () => ({ __esModule: true, default: { Line: () => null, Bar: () => null } }));
jest.mock('@shared/widgets/CustomDateTimeRangePicker', () => ({ __esModule: true, default: () => null }));
jest.mock('@components/integrations/modal/K8sAccountModal', () => ({ __esModule: true, default: () => null }));
// The listing mounts these closed; neither is part of opening a dashboard.
jest.mock('../ImportDashboardModal', () => ({ __esModule: true, default: () => null }));
jest.mock('../TemplateGalleryModal', () => ({ __esModule: true, default: () => null }));

const text = (id: number, content: string) => ({
  id,
  title: '',
  type: 'text',
  datasource: 'metrics',
  grid_pos: { x: 0, y: 0, w: 12, h: 4 },
  content,
});
const CAPACITY = { id: 'dash-a', title: 'Capacity review', description: '', definition: { panels: [text(1, 'capacity notes')] } };
const ERRORS = { id: 'dash-b', title: 'Error monitoring', description: '', definition: { panels: [text(1, 'error notes')] } };
const BY_ID: Record<string, typeof CAPACITY> = { 'dash-a': CAPACITY, 'dash-b': ERRORS };

const api = apiDashboards as unknown as { listDashboards: jest.Mock; getDashboard: jest.Mock };

const inEditMode = () => document.getElementById('dashboard-save-btn') !== null;

/** The header search: it rewrites the open dashboard in the URL while one is already showing. */
const openFromHeaderSearch = (id: string) => act(() => void mockNavigate(`/dashboards?dashboard=${id}`));

/** The pencil on a listing row. */
const clickEdit = async (id: string) => {
  const pencil = await waitFor(() => {
    const el = document.getElementById(`edit-dashboard-${id}`);
    if (!el) throw new Error(`no edit action for ${id} yet`);
    return el;
  });
  fireEvent.click(pencil);
};

async function editFromList(id: string) {
  render(<CustomDashboards />);
  await clickEdit(id);
  await waitFor(() => expect(inEditMode()).toBe(true));
}

describe('editing a dashboard from the listing', () => {
  beforeEach(() => {
    mockRoute.current = { asPath: '/dashboards', query: {} };
    api.listDashboards.mockResolvedValue({ data: [CAPACITY, ERRORS] });
    api.getDashboard.mockImplementation((id: string) => Promise.resolve({ data: { dashboard: BY_ID[id], bindings: [] } }));
  });
  afterEach(() => {
    cleanup();
    jest.clearAllMocks();
  });

  it('opens that dashboard in edit mode', async () => {
    await editFromList('dash-a');

    expect((document.getElementById('dashboard-title-input') as HTMLInputElement).value).toBe('Capacity review');
  });

  it('opens the next dashboard, reached without going back to the list, for viewing', async () => {
    await editFromList('dash-a');

    openFromHeaderSearch('dash-b');

    expect(await screen.findByText('Error monitoring')).toBeInTheDocument();
    expect(inEditMode()).toBe(false);
    expect(document.getElementById('dashboard-edit-btn')).not.toBeNull();
  });

  it('opens the same dashboard for viewing when it is reached again by a link', async () => {
    await editFromList('dash-a');
    openFromHeaderSearch('dash-b');
    await screen.findByText('Error monitoring');

    openFromHeaderSearch('dash-a');

    expect(await screen.findByText('Capacity review')).toBeInTheDocument();
    expect(inEditMode()).toBe(false);
  });

  it('forgets the request when the viewer leaves before the dashboard has loaded', async () => {
    let answer: (res: unknown) => void = () => undefined;
    api.getDashboard.mockImplementationOnce(() => new Promise((resolve) => (answer = resolve)));
    render(<CustomDashboards />);
    await clickEdit('dash-a');
    // Back to the listing while the open is still in flight; its answer then arrives for nobody.
    act(() => void mockNavigate('/dashboards'));
    await act(async () => answer({ data: { dashboard: CAPACITY, bindings: [] } }));

    fireEvent.click(await screen.findByTestId('open-dashboard-dash-a'));

    expect(await screen.findByText('capacity notes')).toBeInTheDocument();
    expect(inEditMode()).toBe(false);
  });

  it('forgets the request when the dashboard cannot be opened', async () => {
    api.getDashboard.mockImplementationOnce(() => Promise.resolve({ errors: [{ message: 'gone' }] }));
    render(<CustomDashboards />);
    await clickEdit('dash-a');
    // Back on the listing after the failed open; the same dashboard is then opened to read.
    fireEvent.click(await screen.findByTestId('open-dashboard-dash-a'));

    expect(await screen.findByText('capacity notes')).toBeInTheDocument();
    expect(inEditMode()).toBe(false);
  });
});
