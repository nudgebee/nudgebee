import apiWorkflow from '@api1/workflow';
import {
  activateSavedWorkflow,
  dropPausedNotes,
  findWorkflowSavedBySession,
  finishEventPageBuild,
  type AiBuildActivation,
} from '@components/workflow/aiBuildSession';

jest.mock('@api1/workflow', () => ({
  __esModule: true,
  default: { listWorkflows: jest.fn(), resumeWorkflow: jest.fn() },
}));

const listWorkflows = apiWorkflow.listWorkflows as jest.Mock;
const resumeWorkflow = apiWorkflow.resumeWorkflow as jest.Mock;

const paused = { id: 'wf-1', name: 'elasticsearch-anomaly-rca', status: 'PAUSED', created_from_session_id: 'session-1' };
const listing = (...workflows: any[]) => ({ data: { workflow_list: { workflows } } });
const resumed = { data: { workflow_resume: { data: 'ok' } } };

// #40444: an automation built from an event must end saved, live v1 and ACTIVE, so it is in that
// event's Automations dropdown and can be run there.
describe('findWorkflowSavedBySession', () => {
  beforeEach(() => jest.resetAllMocks());

  it('returns the automation this session saved, among others', async () => {
    listWorkflows.mockResolvedValue(listing({ id: 'other', created_from_session_id: 'session-0' }, paused, { id: 'manual' }));
    await expect(findWorkflowSavedBySession('account-1', 'session-1')).resolves.toEqual(paused);
    expect(listWorkflows.mock.calls[0][0]).toEqual(['account-1']);
  });

  it('returns the newest when two rows carry the session (the list is newest first)', async () => {
    const newer = { ...paused, id: 'wf-2' };
    listWorkflows.mockResolvedValue(listing(newer, paused));
    await expect(findWorkflowSavedBySession('account-1', 'session-1')).resolves.toEqual(newer);
  });

  it('returns null when the session saved nothing', async () => {
    listWorkflows.mockResolvedValue(listing({ id: 'other', created_from_session_id: 'session-0' }));
    await expect(findWorkflowSavedBySession('account-1', 'session-1')).resolves.toBeNull();
  });

  it('returns undefined when the list could not be read', async () => {
    listWorkflows.mockRejectedValue(new Error('network'));
    await expect(findWorkflowSavedBySession('account-1', 'session-1')).resolves.toBeUndefined();
    listWorkflows.mockResolvedValue({ errors: [{ message: 'boom' }] });
    await expect(findWorkflowSavedBySession('account-1', 'session-1')).resolves.toBeUndefined();
  });

  it('does not call the list without an account or session', async () => {
    await expect(findWorkflowSavedBySession('', 'session-1')).resolves.toBeUndefined();
    await expect(findWorkflowSavedBySession('account-1', '')).resolves.toBeUndefined();
    expect(listWorkflows).not.toHaveBeenCalled();
  });
});

describe('activateSavedWorkflow', () => {
  beforeEach(() => jest.resetAllMocks());

  it('activates a paused automation and reports it active', async () => {
    resumeWorkflow.mockResolvedValue(resumed);
    const result = await activateSavedWorkflow('account-1', paused);
    expect(resumeWorkflow).toHaveBeenCalledWith('account-1', 'wf-1');
    expect(result).toEqual({ liveState: 'active', savedWorkflow: { ...paused, status: 'ACTIVE' } });
  });

  it('reports it still paused, with the reason, when activation is refused', async () => {
    resumeWorkflow.mockResolvedValue({ data: null, errors: [{ message: 'not allowed to resume' }] });
    const result = await activateSavedWorkflow('account-1', paused);
    expect(result).toEqual({ liveState: 'paused', savedWorkflow: paused, error: 'not allowed to resume' });
  });

  it('reports it still paused when the call throws', async () => {
    resumeWorkflow.mockRejectedValue(new Error('timeout'));
    const result = await activateSavedWorkflow('account-1', paused);
    expect(result).toEqual({ liveState: 'paused', savedWorkflow: paused, error: 'timeout' });
  });

  it('leaves an automation saved INACTIVE (missing configs) alone', async () => {
    const result = await activateSavedWorkflow('account-1', { ...paused, status: 'INACTIVE' });
    expect(resumeWorkflow).not.toHaveBeenCalled();
    expect(result.liveState).toBe('inactive');
  });

  it('does not call resume for an automation already active', async () => {
    const result = await activateSavedWorkflow('account-1', { ...paused, status: 'ACTIVE' });
    expect(resumeWorkflow).not.toHaveBeenCalled();
    expect(result.liveState).toBe('active');
  });

  it('reports unverified when there is no saved automation to activate', async () => {
    await expect(activateSavedWorkflow('account-1', undefined)).resolves.toEqual({ liveState: 'unverified' });
    expect(resumeWorkflow).not.toHaveBeenCalled();
  });
});

describe('dropPausedNotes', () => {
  const headline = 'The automation **`x`** has been built and saved.';
  const tasks = '**Tasks:**\n1. Check if the deployment is paused\n2. Post to Slack';

  it('drops a one-line paragraph that says the automation is paused, whatever its wording', () => {
    expect(dropPausedNotes(`${headline}\n\nIt is saved **paused**: its schedule will not fire until you activate it.\n\n${tasks}`)).toBe(
      `${headline}\n\n${tasks}`
    );
    expect(dropPausedNotes(`${headline}\n\nIt was saved as Paused, so activate it to start its trigger.\n\n${tasks}`)).toBe(
      `${headline}\n\n${tasks}`
    );
  });

  it('keeps a one-line paragraph that mentions "paused" for another reason', () => {
    const summary = `${headline}\n\nIt skips paused nodes during maintenance.\n\n${tasks}`;
    expect(dropPausedNotes(summary)).toBe(summary);
  });

  it('keeps the task list even when a task mentions "paused"', () => {
    expect(dropPausedNotes(`${headline}\n\n${tasks}`)).toBe(`${headline}\n\n${tasks}`);
  });

  it('leaves a reply with no such paragraph unchanged', () => {
    const summary = `${headline}\n\nIt runs only when you start it.`;
    expect(dropPausedNotes(summary)).toBe(summary);
  });
});

describe('finishEventPageBuild', () => {
  const completed = {
    status: 'COMPLETED',
    workflowJson: '{}',
    summaryText: 'The automation **`x`** has been built and saved.\n\nIt is saved **paused**: its schedule will not fire until you activate it.',
  };

  beforeEach(() => jest.resetAllMocks());

  it('saves → finds by session → activates once, and reports it active with the paused note gone', async () => {
    listWorkflows.mockResolvedValue(listing(paused));
    resumeWorkflow.mockResolvedValue(resumed);
    const onSettled = jest.fn();

    const out = await finishEventPageBuild('account-1', 'session-1', completed, new Map(), onSettled);

    expect(resumeWorkflow).toHaveBeenCalledTimes(1);
    expect(onSettled).toHaveBeenCalledTimes(1);
    expect(out).toMatchObject({ saved: true, liveState: 'active', savedWorkflow: { id: 'wf-1', status: 'ACTIVE' } });
    expect(out.summaryText).toBe('The automation **`x`** has been built and saved.');
  });

  it('calls resume and onSettled exactly once when polls overlap', async () => {
    listWorkflows.mockResolvedValue(listing(paused));
    let release: (v: any) => void = () => {};
    resumeWorkflow.mockReturnValue(new Promise((r) => (release = r)));
    const activations = new Map<string, Promise<AiBuildActivation>>();
    const onSettled = jest.fn();

    const first = finishEventPageBuild('account-1', 'session-1', completed, activations, onSettled);
    const second = finishEventPageBuild('account-1', 'session-1', completed, activations, onSettled);
    await new Promise((r) => setTimeout(r, 0));
    release(resumed);
    const outs = await Promise.all([first, second]);
    const third = await finishEventPageBuild('account-1', 'session-1', completed, activations, onSettled);

    expect(resumeWorkflow).toHaveBeenCalledTimes(1);
    expect(onSettled).toHaveBeenCalledTimes(1);
    expect([...outs, third].map((o) => o.liveState)).toEqual(['active', 'active', 'active']);
  });

  it('keeps it paused, keeps the builder note and says why when activation fails', async () => {
    listWorkflows.mockResolvedValue(listing(paused));
    resumeWorkflow.mockResolvedValue({ data: null, errors: [{ message: 'permission denied' }] });

    const out = await finishEventPageBuild('account-1', 'session-1', completed, new Map(), jest.fn());

    expect(out).toMatchObject({ saved: true, liveState: 'paused', liveStateError: 'permission denied', savedWorkflow: paused });
    expect(out.summaryText).toBe(completed.summaryText);
  });

  it('does not activate an automation saved INACTIVE for missing configs', async () => {
    listWorkflows.mockResolvedValue(listing({ ...paused, status: 'INACTIVE' }));

    const out = await finishEventPageBuild('account-1', 'session-1', completed, new Map(), jest.fn());

    expect(resumeWorkflow).not.toHaveBeenCalled();
    expect(out.liveState).toBe('inactive');
  });

  it('reports nothing saved when the session saved nothing, and activates nothing', async () => {
    listWorkflows.mockResolvedValue(listing());
    const onSettled = jest.fn();

    const out = await finishEventPageBuild('account-1', 'session-1', completed, new Map(), onSettled);

    expect(out.saved).toBe(false);
    expect(resumeWorkflow).not.toHaveBeenCalled();
    expect(onSettled).not.toHaveBeenCalled();
  });

  it('reports unverified, leaving `saved` unset, when the list could not be read', async () => {
    listWorkflows.mockRejectedValue(new Error('network'));

    const out = await finishEventPageBuild('account-1', 'session-1', completed, new Map(), jest.fn());

    expect(out.liveState).toBe('unverified');
    expect(out).not.toHaveProperty('saved');
    expect(resumeWorkflow).not.toHaveBeenCalled();
  });
});
