import React from 'react';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import GenerateDashboardModal from '../GenerateDashboardModal';
import apiDashboards, { type AccountOption } from '@api1/dashboards';
import observability from '@api1/observability';

/*
 * The generated dashboard must reach the editor the way an import does — as an
 * unsaved draft scoped to the chosen account — and only with panels whose
 * queries actually ran.
 */

jest.mock('@api1/dashboards', () => ({
  __esModule: true,
  default: { generateDashboard: jest.fn(), saveDashboard: jest.fn() },
  KUBERNETES_ACCOUNT_KIND: 'kubernetes',
}));
jest.mock('@api1/observability', () => ({ __esModule: true, default: { metricsQuery: jest.fn() } }));
jest.mock('@hooks/useTenantBranding', () => ({ useTenantBranding: () => ({ assistantName: 'Nubi', baseTitle: 'Brand' }) }));
// ds/Select is a MUI popover in jsdom; a native select is enough to drive onChange.
jest.mock('@ui/Select', () => ({
  __esModule: true,
  Select: ({
    id,
    options,
    value,
    onChange,
    disabled,
  }: {
    id: string;
    options: { value: string; label: string }[];
    value: string;
    onChange: (v: string) => void;
    disabled?: boolean;
  }) => (
    <select data-testid={id} value={value} disabled={disabled} onChange={(e) => onChange(e.target.value)}>
      {options.map((o) => (
        <option key={o.value} value={o.value}>
          {o.label}
        </option>
      ))}
    </select>
  ),
}));

/** The conversation the next poll will "finish" with. */
let finishedConversation: any = null;
jest.mock('@hooks/useLlmAsyncPolling', () => ({
  useLlmAsyncPolling: () => ({
    startPolling: (_sessionId: string, onComplete: (conv: any) => void) => onComplete(finishedConversation),
    stopPolling: jest.fn(),
  }),
}));

const generateDashboard = apiDashboards.generateDashboard as jest.Mock;
const saveDashboard = apiDashboards.saveDashboard as jest.Mock;
const metricsQuery = observability.metricsQuery as jest.Mock;

const accounts = [
  { value: 'acc-off', label: 'old', cloud_provider: 'K8s', status: 'disabled', kind: 'kubernetes' },
  // A cloud account listed first: its metrics are refused, so it is not the default.
  { value: 'acc-aws', label: 'aws-prod', cloud_provider: 'AWS', status: 'active', kind: 'cloud' },
  { value: 'acc-1', label: 'k8s-dev', cloud_provider: 'K8s', status: 'active', kind: 'kubernetes' },
  { value: 'acc-2', label: 'k8s-test', cloud_provider: 'K8s', status: 'active', kind: 'kubernetes' },
] as AccountOption[];

const DRAFT = {
  title: 'Cluster health',
  description: 'Headline numbers then trends.',
  warnings: ['"Pie": dropped — unsupported panel type "piechart"'],
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
      {
        id: 2,
        title: 'OOM kills',
        type: 'timeseries',
        datasource: 'metrics',
        grid_pos: { x: 0, y: 0, w: 6, h: 8 },
        targets: [{ ref_id: 'A', expr: 'oom' }],
      },
      {
        id: 3,
        title: 'Broken',
        type: 'timeseries',
        datasource: 'metrics',
        grid_pos: { x: 0, y: 0, w: 6, h: 8 },
        targets: [{ ref_id: 'A', expr: 'bad' }],
      },
    ],
  },
};

const completed = (response: string) => ({ status: 'COMPLETED', llm_conversation_messages: [{ response, llm_conversation_agents: [] }] });

const renderModal = (onDraft = jest.fn()) => {
  render(<GenerateDashboardModal open accountOptions={accounts} onClose={jest.fn()} onDraft={onDraft} />);
  return onDraft;
};

const describeDashboard = (text: string) => {
  fireEvent.change(screen.getByPlaceholderText('Kubernetes cluster health: CPU, memory, pod restarts and node count'), { target: { value: text } });
};

describe('GenerateDashboardModal', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    generateDashboard.mockResolvedValue({ data: { session_id: 'sess-1' } });
    metricsQuery.mockImplementation(async ({ queries }: { queries: Record<string, string> }) => {
      if (queries.A === 'bad') return { data: { data: { metrics_list: { results: [{ query_key: 'A', error: 'unknown function bad' }] } } } };
      if (queries.A === 'oom') return { data: { data: { metrics_list: { results: [{ query_key: 'A', payload: [] }] } } } };
      return { data: { data: { metrics_list: { results: [{ query_key: 'A', payload: [{ value: 3 }] }] } } } };
    });
    finishedConversation = completed(JSON.stringify(DRAFT));
  });

  it('asks for the first live Kubernetes account and opens an unsaved draft without the failed panel', async () => {
    const onDraft = renderModal();
    describeDashboard('cluster health');
    fireEvent.click(screen.getByTestId('generate-dashboard-submit-btn'));

    await waitFor(() => expect(screen.getByTestId('generate-dashboard-open-btn')).toBeEnabled());
    expect(generateDashboard).toHaveBeenCalledWith({ account_id: 'acc-1', query: 'cluster health' });
    expect(metricsQuery).toHaveBeenCalledTimes(3);
    for (const call of metricsQuery.mock.calls) expect(call[0]).toMatchObject({ account_id: 'acc-1' });
    // Single-value panels are checked instantly, charts over a range, as they render.
    const instantByQuery = Object.fromEntries(metricsQuery.mock.calls.map(([req]) => [req.queries.A, req.instant]));
    expect(instantByQuery).toEqual({ 'sum(up)': true, oom: false, bad: false });

    // The empty panel is kept and named; the failed one is left out with its reason.
    const warnings = screen.getByTestId('generate-dashboard-warnings');
    expect(warnings).toHaveTextContent('OOM kills returned no data');
    expect(warnings).toHaveTextContent('Broken was left out: unknown function bad');
    expect(warnings).toHaveTextContent('unsupported panel type "piechart"');

    fireEvent.click(screen.getByTestId('generate-dashboard-open-btn'));

    expect(onDraft).toHaveBeenCalledTimes(1);
    const draft = onDraft.mock.calls[0][0];
    expect(draft.id).toBe('');
    expect(draft.title).toBe('Cluster health');
    expect(draft.definition.panels.map((p: any) => p.title)).toEqual(['Ready nodes', 'OOM kills']);
    for (const panel of draft.definition.panels) expect(panel.account_ids).toEqual(['acc-1']);
    expect(saveDashboard).not.toHaveBeenCalled();
  });

  it('shows the server’s refusal when generation cannot start', async () => {
    generateDashboard.mockResolvedValue({
      data: null,
      errors: [{ message: 'AI dashboard generation currently supports Prometheus-compatible metrics backends only' }],
    });
    renderModal();
    describeDashboard('cluster health');
    fireEvent.click(screen.getByTestId('generate-dashboard-submit-btn'));

    expect(await screen.findByText(/Prometheus-compatible metrics backends only/)).toBeInTheDocument();
    expect(screen.queryByTestId('generate-dashboard-open-btn')).not.toBeInTheDocument();
    expect(screen.getByTestId('generate-dashboard-submit-btn')).toHaveTextContent('Try again');
  });

  it('shows the agent’s error when the turn fails', async () => {
    finishedConversation = {
      status: 'FAILED',
      llm_conversation_messages: [{ response: 'dashboard generation: no usable panels', llm_conversation_agents: [] }],
    };
    renderModal();
    describeDashboard('something odd');
    fireEvent.click(screen.getByTestId('generate-dashboard-submit-btn'));

    expect(await screen.findByText('dashboard generation: no usable panels')).toBeInTheDocument();
    expect(metricsQuery).not.toHaveBeenCalled();
  });

  it('cannot open a draft whose every panel failed', async () => {
    finishedConversation = completed(JSON.stringify({ ...DRAFT, definition: { panels: [DRAFT.definition.panels[2]] } }));
    const onDraft = renderModal();
    describeDashboard('cluster health');
    fireEvent.click(screen.getByTestId('generate-dashboard-submit-btn'));

    await waitFor(() => expect(screen.getByTestId('generate-dashboard-open-btn')).toBeDisabled());
    expect(onDraft).not.toHaveBeenCalled();
  });

  it('drops a failed query from a panel whose other query answered', async () => {
    const mixed = {
      ...DRAFT,
      definition: {
        panels: [
          {
            ...DRAFT.definition.panels[0],
            targets: [
              { ref_id: 'A', expr: 'sum(up)' },
              { ref_id: 'B', expr: 'nope' },
            ],
          },
        ],
      },
    };
    finishedConversation = completed(JSON.stringify(mixed));
    metricsQuery.mockResolvedValue({
      data: {
        data: {
          metrics_list: {
            results: [
              { query_key: 'A', payload: [{ value: 1 }] },
              { query_key: 'B', error: 'unknown function nope' },
            ],
          },
        },
      },
    });
    const onDraft = renderModal();
    describeDashboard('cluster health');
    fireEvent.click(screen.getByTestId('generate-dashboard-submit-btn'));

    await waitFor(() => expect(screen.getByTestId('generate-dashboard-open-btn')).toBeEnabled());
    expect(screen.getByTestId('generate-dashboard-warnings')).toHaveTextContent('a query that failed was left out (unknown function nope)');
    fireEvent.click(screen.getByTestId('generate-dashboard-open-btn'));

    const [panel] = onDraft.mock.calls[0][0].definition.panels;
    expect(panel.targets.map((t: any) => t.ref_id)).toEqual(['A']);
  });

  it('sends the preview back to Generate when the account changes', async () => {
    const onDraft = renderModal();
    describeDashboard('cluster health');
    fireEvent.click(screen.getByTestId('generate-dashboard-submit-btn'));
    await waitFor(() => expect(screen.getByTestId('generate-dashboard-open-btn')).toBeEnabled());

    // Panels were checked against acc-1; they must not reach the editor scoped to it.
    fireEvent.change(screen.getByTestId('generate-dashboard-account'), { target: { value: 'acc-2' } });
    expect(screen.queryByTestId('generate-dashboard-open-btn')).not.toBeInTheDocument();
    expect(onDraft).not.toHaveBeenCalled();

    fireEvent.click(screen.getByTestId('generate-dashboard-submit-btn'));
    await waitFor(() => expect(screen.getByTestId('generate-dashboard-open-btn')).toBeEnabled());
    expect(generateDashboard).toHaveBeenLastCalledWith({ account_id: 'acc-2', query: 'cluster health' });
    fireEvent.click(screen.getByTestId('generate-dashboard-open-btn'));
    for (const panel of onDraft.mock.calls[0][0].definition.panels) expect(panel.account_ids).toEqual(['acc-2']);
  });

  it('sends the preview back to Generate when the description changes', async () => {
    const onDraft = renderModal();
    describeDashboard('cluster health');
    fireEvent.click(screen.getByTestId('generate-dashboard-submit-btn'));
    await waitFor(() => expect(screen.getByTestId('generate-dashboard-open-btn')).toBeEnabled());

    // The preview answers "cluster health"; it must not open as the new description.
    describeDashboard('redis overview');
    expect(screen.queryByTestId('generate-dashboard-open-btn')).not.toBeInTheDocument();
    expect(screen.getByTestId('generate-dashboard-submit-btn')).toBeEnabled();
    expect(onDraft).not.toHaveBeenCalled();
  });

  // "Open as dashboard" in Nubi hands over a dashboard already generated. It gets
  // the same check and preview, for the account it was generated on.
  it('previews a dashboard handed over from Nubi without generating again', async () => {
    const onDraft = jest.fn();
    render(
      <GenerateDashboardModal
        open
        accountOptions={accounts}
        onClose={jest.fn()}
        onDraft={onDraft}
        initialDraft={{ accountId: 'acc-2', model: DRAFT as any, prompt: 'cluster health' }}
      />
    );

    await waitFor(() => expect(screen.getByTestId('generate-dashboard-open-btn')).toBeEnabled());
    expect(generateDashboard).not.toHaveBeenCalled();
    expect(screen.getByDisplayValue('cluster health')).toBeInTheDocument();
    for (const call of metricsQuery.mock.calls) expect(call[0]).toMatchObject({ account_id: 'acc-2' });
    expect(screen.getByTestId('generate-dashboard-warnings')).toHaveTextContent('Broken was left out: unknown function bad');
    expect(screen.getByTestId('generate-dashboard-warnings')).toHaveTextContent('unsupported panel type "piechart"');

    fireEvent.click(screen.getByTestId('generate-dashboard-open-btn'));
    const draft = onDraft.mock.calls[0][0];
    expect(draft.definition.panels.map((p: any) => p.title)).toEqual(['Ready nodes', 'OOM kills']);
    for (const panel of draft.definition.panels) expect(panel.account_ids).toEqual(['acc-2']);
  });

  it('does not start without a description', () => {
    renderModal();
    expect(screen.getByTestId('generate-dashboard-submit-btn')).toBeDisabled();
  });
});
