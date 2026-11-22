import observability from '@api1/observability';
import type { Panel } from '@api1/dashboards';
import { panelStep } from '../panelBounds';
import { checkPanel, checkPanels, extractGeneratedDashboard, stashGeneratedDashboard, takeStashedDashboard } from '../generatedDashboard';

jest.mock('@api1/observability', () => ({ __esModule: true, default: { metricsQuery: jest.fn() } }));

const metricsQuery = observability.metricsQuery as jest.Mock;

const DRAFT = { title: 'Cluster health', definition: { panels: [{ title: 'Ready nodes' }] }, warnings: ['"X": dropped'] };

type AgentRow = { agent_name: string; response: unknown; parent_agent_id?: string | null };

const conversation = (status: string, messageResponse: unknown, agents: AgentRow[] = []) => ({
  status,
  llm_conversation_messages: [{ response: messageResponse, llm_conversation_agents: agents }],
});

/** The writer's raw answer: a shape the Go side accepts but the importer does not, and never checked. */
const RAW_WRITER = JSON.stringify({
  title: 'Raw',
  definition: { panels: [{ title: 'Ready nodes', type: 'stat', width: 3, targets: [{ expr: 'up' }] }] },
});
const builderRow = (response: unknown): AgentRow => ({ agent_name: 'dashboard_builder', response, parent_agent_id: null });
const writerRow: AgentRow = { agent_name: 'dashboard_writer', response: RAW_WRITER, parent_agent_id: 'builder-id' };

const metricsPanel = (id: number, exprs: string[], type: Panel['type'] = 'timeseries'): Panel => ({
  id,
  title: `P${id}`,
  type,
  datasource: 'metrics',
  account_ids: ['acc-1'],
  grid_pos: { x: 0, y: 0, w: 6, h: 8 },
  targets: exprs.map((expr, i) => ({ ref_id: String.fromCharCode(65 + i), expr })),
});

const answer = (results: unknown[]) => ({ data: { data: { metrics_list: { results } } } });

describe('extractGeneratedDashboard', () => {
  it('reads the draft from the message response', () => {
    expect(extractGeneratedDashboard(conversation('COMPLETED', JSON.stringify(DRAFT)))).toEqual({ model: DRAFT });
  });

  it('reads the draft from the builder row when the message carries none', () => {
    expect(extractGeneratedDashboard(conversation('COMPLETED', '', [builderRow(JSON.stringify(DRAFT)), writerRow]))).toEqual({ model: DRAFT });
  });

  // The writer row is saved after the builder row and holds the unchecked answer;
  // reading it bypassed the Go checks and lost their warnings.
  it('never reads the writer agent’s raw answer', () => {
    expect(extractGeneratedDashboard(conversation('COMPLETED', JSON.stringify(DRAFT), [builderRow(JSON.stringify(DRAFT)), writerRow]))).toEqual({
      model: DRAFT,
    });
    const onlyWriter = extractGeneratedDashboard(conversation('COMPLETED', '', [builderRow(''), writerRow]));
    expect('error' in onlyWriter).toBe(true);
  });

  it('takes nothing from a turn the builder failed, even when the writer row holds JSON', () => {
    expect(extractGeneratedDashboard(conversation('FAILED', 'dashboard generation: no usable panels', [builderRow(''), writerRow]))).toEqual({
      error: 'dashboard generation: no usable panels',
    });
  });

  it('shows the agent’s own error text for a failed turn', () => {
    const msg = 'AI dashboard generation currently supports Prometheus-compatible metrics backends only';
    expect(extractGeneratedDashboard(conversation('FAILED', msg))).toEqual({ error: msg });
  });

  it('refuses a completed turn whose answer is not a dashboard', () => {
    const result = extractGeneratedDashboard(conversation('COMPLETED', '{"promql": "up"}'));
    expect('error' in result && result.error).toContain('not a dashboard');
  });

  it('says so when the conversation is empty', () => {
    expect(extractGeneratedDashboard({ status: 'FAILED', llm_conversation_messages: [] })).toEqual({
      error: 'Dashboard generation did not finish. Try again.',
    });
  });
});

describe('checkPanel', () => {
  beforeEach(() => metricsQuery.mockReset());

  it('sends a single-value panel’s queries as one instant request for the account', async () => {
    metricsQuery.mockResolvedValue(answer([{ query_key: 'A', payload: [{ value: 1 }] }]));

    await expect(checkPanel(metricsPanel(1, ['up', 'sum(up)'], 'stat'), 'acc-1', 10_000_000)).resolves.toEqual({ status: 'data' });
    expect(metricsQuery).toHaveBeenCalledWith({
      account_id: 'acc-1',
      queries: { A: 'up', B: 'sum(up)' },
      start_time: 10_000_000 - 3_600_000,
      end_time: 10_000_000,
      instant: true,
    });
  });

  // A chart renders with a range query, which rejects some expressions an
  // instant query accepts — so it is checked the way it is drawn.
  it('checks a chart with the range query it renders with', async () => {
    metricsQuery.mockResolvedValue(answer([{ query_key: 'A', payload: [{ value: 1 }] }]));

    await checkPanel(metricsPanel(1, ['rate(x[5m])']), 'acc-1', 10_000_000);
    expect(metricsQuery).toHaveBeenCalledWith({
      account_id: 'acc-1',
      queries: { A: 'rate(x[5m])' },
      start_time: 10_000_000 - 3_600_000,
      end_time: 10_000_000,
      instant: false,
      step_interval: panelStep(10_000_000 - 3_600_000, 10_000_000),
    });
  });

  it('reports a panel whose queries match nothing as empty', async () => {
    metricsQuery.mockResolvedValue(answer([{ query_key: 'A', payload: [] }]));
    await expect(checkPanel(metricsPanel(1, ['up']), 'acc-1')).resolves.toEqual({ status: 'empty' });
  });

  // The metrics endpoint reports a bad query as a key carrying `error` inside a 200.
  it('reports a key error as an error with the provider’s message', async () => {
    metricsQuery.mockResolvedValue(answer([{ query_key: 'A', error: 'parse error at char 5' }]));
    await expect(checkPanel(metricsPanel(1, ['up(']), 'acc-1')).resolves.toEqual({ status: 'error', reason: 'parse error at char 5' });
  });

  it('counts a panel as answered when one query returns data, naming the failed one', async () => {
    metricsQuery.mockResolvedValue(
      answer([
        { query_key: 'A', error: 'bad' },
        { query_key: 'B', payload: [{ value: 2 }] },
      ])
    );
    await expect(checkPanel(metricsPanel(1, ['bad', 'up']), 'acc-1')).resolves.toEqual({ status: 'data', failedRefIds: ['A'], reason: 'bad' });
  });

  it('reports gateway errors and rejected requests as errors', async () => {
    metricsQuery.mockResolvedValueOnce({ data: { errors: [{ message: 'access denied for account: acc-1' }] } });
    await expect(checkPanel(metricsPanel(1, ['up']), 'acc-1')).resolves.toEqual({ status: 'error', reason: 'access denied for account: acc-1' });

    metricsQuery.mockRejectedValueOnce(new Error('timeout'));
    await expect(checkPanel(metricsPanel(1, ['up']), 'acc-1')).resolves.toEqual({ status: 'error', reason: 'timeout' });
  });

  it('does not query text panels', async () => {
    const text = { ...metricsPanel(1, []), type: 'text' as const, content: 'notes' };
    await expect(checkPanel(text, 'acc-1')).resolves.toEqual({ status: 'data' });
    expect(metricsQuery).not.toHaveBeenCalled();
  });
});

describe('checkPanels', () => {
  beforeEach(() => metricsQuery.mockReset());

  it('checks every panel, keyed by id, with at most four requests in flight', async () => {
    let inFlight = 0;
    let peak = 0;
    metricsQuery.mockImplementation(async ({ queries }: { queries: Record<string, string> }) => {
      inFlight++;
      peak = Math.max(peak, inFlight);
      await new Promise((resolve) => setTimeout(resolve, 5));
      inFlight--;
      return answer(queries.A === 'empty' ? [{ query_key: 'A', payload: [] }] : [{ query_key: 'A', payload: [{ value: 1 }] }]);
    });
    const panels = Array.from({ length: 9 }, (_, i) => metricsPanel(i + 1, [i === 4 ? 'empty' : 'up']));

    const results = await checkPanels(panels, 'acc-1');

    expect(Object.keys(results)).toHaveLength(9);
    expect(results[5]).toEqual({ status: 'empty' });
    expect(results[1]).toEqual({ status: 'data' });
    expect(peak).toBeLessThanOrEqual(4);
  });
});

describe('handing a Nubi conversation to the Dashboards page', () => {
  const conversation = (status: string, response: string) => ({
    account_id: 'acc-1',
    status,
    llm_conversation_messages: [{ id: 'm-1', status, message: 'cluster health', response, llm_conversation_agents: [] }],
  });

  beforeEach(() => sessionStorage.clear());

  it('stashes a finished draft with its account and prompt, and gives it up once', () => {
    expect(stashGeneratedDashboard(conversation('COMPLETED', JSON.stringify(DRAFT)), 'm-1')).toBeNull();
    expect(takeStashedDashboard()).toEqual({ accountId: 'acc-1', model: DRAFT, prompt: 'cluster health' });
    expect(takeStashedDashboard()).toBeNull();
  });

  // A follow-up makes a later turn the latest; the button under the dashboard
  // answer still opens that answer.
  it('opens the turn it was asked for, not the latest one', () => {
    const followedUp = {
      account_id: 'acc-1',
      status: 'COMPLETED',
      llm_conversation_messages: [
        { id: 'm-1', status: 'COMPLETED', message: 'cluster health', response: JSON.stringify(DRAFT), llm_conversation_agents: [] },
        { id: 'm-2', status: 'COMPLETED', message: 'why is CPU high?', response: 'CPU is high because…', llm_conversation_agents: [] },
      ],
    };
    expect(extractGeneratedDashboard(followedUp)).toEqual({ error: expect.stringContaining('not a dashboard') });
    expect(stashGeneratedDashboard(followedUp, 'm-1')).toBeNull();
    expect(takeStashedDashboard()?.model).toEqual(DRAFT);
  });

  it('refuses a turn that did not finish and stashes nothing', () => {
    expect(stashGeneratedDashboard(conversation('FAILED', 'dashboard generation: no usable panels'))).toBe('dashboard generation: no usable panels');
    expect(takeStashedDashboard()).toBeNull();
  });

  it('ignores a stashed value that is not a dashboard', () => {
    sessionStorage.setItem('nubiGeneratedDashboard', JSON.stringify({ accountId: 'acc-1', model: { title: 'x' } }));
    expect(takeStashedDashboard()).toBeNull();
  });
});
