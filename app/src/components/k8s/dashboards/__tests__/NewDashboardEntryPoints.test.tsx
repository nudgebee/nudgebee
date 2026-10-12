import React from 'react';
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import CustomDashboards from '../CustomDashboards';
import apiDashboards from '@api1/dashboards';
import { snackbar } from '@ui/Toast';
import { stashGeneratedDashboard } from '../generatedDashboard';

/*
 * The ways into a new dashboard: the "Create a New Dashboard" chooser (the same
 * four-way start Automation offers) and "Open as dashboard" on a Nubi
 * conversation. Every one of them ends in an unsaved draft in the editor.
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
jest.mock('@hooks/useTenantBranding', () => ({
  ...jest.requireActual('@hooks/useTenantBranding'),
  useTenantBranding: () => ({ assistantName: 'Nubi', baseTitle: 'Brand' }),
}));
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
// The modal's own preview is tested in GenerateDashboardModal.test.tsx; here it
// only shows what it was handed and passes it on as the draft.
jest.mock('../GenerateDashboardModal', () => ({
  __esModule: true,
  default: ({ open, initialDraft, onDraft }: { open: boolean; initialDraft?: any; onDraft: (d: any) => void }) =>
    open ? (
      <div data-testid='generate-modal-open'>
        {initialDraft && (
          <button
            data-testid='preview-open-btn'
            onClick={() => onDraft({ id: '', title: initialDraft.model.title, description: '', definition: initialDraft.model.definition })}
          >
            {initialDraft.accountId} {initialDraft.model.title}
          </button>
        )}
      </div>
    ) : null,
}));

const api = apiDashboards as unknown as { listDashboards: jest.Mock; createDashboard: jest.Mock };
const inEditMode = () => document.getElementById('dashboard-save-btn') !== null;

const GENERATED = {
  title: 'Cluster health',
  description: 'Headline numbers then trends.',
  definition: {
    panels: [
      {
        id: 1,
        title: 'Ready nodes',
        type: 'stat',
        datasource: 'metrics',
        grid_pos: { x: 0, y: 0, w: 3, h: 8 },
        targets: [{ ref_id: 'A', expr: 'sum(up)' }],
      },
    ],
  },
};
const finished = (dashboard: unknown) => ({
  account_id: 'k8s-1',
  source: 'DashboardBuilder',
  status: 'COMPLETED',
  llm_conversation_messages: [{ response: JSON.stringify(dashboard), llm_conversation_agents: [] }],
});

const openChooser = async () => {
  render(<CustomDashboards />);
  fireEvent.click(await screen.findByTestId('new-dashboard-btn'));
  return screen.findByText('Create a New Dashboard');
};

describe('starting a new dashboard', () => {
  beforeEach(() => {
    mockRoute.current = { asPath: '/dashboards', query: {} };
    api.listDashboards.mockResolvedValue({ data: [] });
    sessionStorage.clear();
  });
  afterEach(() => {
    cleanup();
    jest.restoreAllMocks();
    jest.clearAllMocks();
  });

  it('offers the four ways to start, Generate with Nubi among them', async () => {
    await openChooser();
    for (const id of ['new-blank-dashboard-btn', 'open-template-gallery-btn', 'open-generate-dashboard-btn', 'open-import-dashboard-btn']) {
      expect(screen.getByTestId(id)).toBeInTheDocument();
    }
    expect(screen.getByTestId('open-generate-dashboard-btn')).toHaveTextContent('Generate with Nubi');

    fireEvent.click(screen.getByTestId('open-generate-dashboard-btn'));
    expect(await screen.findByTestId('generate-modal-open')).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByText('Create a New Dashboard')).not.toBeInTheDocument());
  });

  it('opens a blank draft in the editor from the chooser', async () => {
    await openChooser();
    fireEvent.click(screen.getByTestId('new-blank-dashboard-btn'));
    await waitFor(() => expect(inEditMode()).toBe(true));
    expect(api.createDashboard).not.toHaveBeenCalled();
  });

  it('previews a dashboard handed over from a Nubi conversation, then opens it as an unsaved draft', async () => {
    expect(stashGeneratedDashboard(finished(GENERATED))).toBeNull();
    mockRoute.current = { asPath: '/dashboards?draftFrom=nubi#list', query: { draftFrom: 'nubi' } };
    render(<CustomDashboards />);

    // Through Generate's preview first, for the conversation's account.
    const preview = await screen.findByTestId('preview-open-btn');
    expect(preview).toHaveTextContent('k8s-1 Cluster health');
    expect(inEditMode()).toBe(false);
    fireEvent.click(preview);

    await waitFor(() => expect(inEditMode()).toBe(true));
    expect(screen.getByDisplayValue('Cluster health')).toBeInTheDocument();
    expect(screen.queryByTestId('generate-modal-open')).not.toBeInTheDocument();
    expect(api.createDashboard).not.toHaveBeenCalled();
    // Taken once: a reload or Back must not reopen it.
    expect(sessionStorage.length).toBe(0);
  });

  it('takes a second hand-off without a remount, as from the Nubi popup over this page', async () => {
    expect(stashGeneratedDashboard(finished(GENERATED))).toBeNull();
    mockRoute.current = { asPath: '/dashboards?draftFrom=nubi#list', query: { draftFrom: 'nubi' } };
    render(<CustomDashboards />);
    fireEvent.click(await screen.findByTestId('preview-open-btn'));
    await waitFor(() => expect(screen.getByDisplayValue('Cluster health')).toBeInTheDocument());

    // The param is stripped, then Nubi hands over a different dashboard.
    await act(() => mockNavigate('/dashboards'));
    expect(stashGeneratedDashboard(finished({ ...GENERATED, title: 'Second one' }))).toBeNull();
    await act(() => mockNavigate('/dashboards?draftFrom=nubi'));

    fireEvent.click(await screen.findByTestId('preview-open-btn'));
    await waitFor(() => expect(screen.getByDisplayValue('Second one')).toBeInTheDocument());
    expect(sessionStorage.length).toBe(0);
  });

  it('says so when the Nubi hand-off has nothing to open', async () => {
    const error = jest.spyOn(snackbar, 'error').mockImplementation(() => undefined as any);
    mockRoute.current = { asPath: '/dashboards?draftFrom=nubi#list', query: { draftFrom: 'nubi' } };
    render(<CustomDashboards />);

    await waitFor(() => expect(error).toHaveBeenCalledWith(expect.stringContaining('was not found')));
    expect(inEditMode()).toBe(false);
    await act(async () => undefined);
  });
});
