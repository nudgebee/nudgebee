import React from 'react';
import { render, screen, fireEvent, act } from '@testing-library/react';
import AiGenerateWorkflowModal from '@components/workflow/components/AiGenerateWorkflowModal';

// The markdown viewer is not under test — render the string so the screen can be searched.
jest.mock('@shared/viewers/MarkDowns', () => {
  return function MockMarkDowns({ data }: { data: string }) {
    return <div data-testid='markdown-output'>{data}</div>;
  };
});

/**
 * What the success screen offers once a build has saved an automation.
 *
 * It used to stop at the AI's summary and an Open in Editor button. A new automation is saved
 * paused, so a scheduled or event-driven one never fired, and none could be tried without finding
 * the editor's run button: most AI-built automations were never run, and almost none with a
 * schedule was ever switched on. The screen now offers Run now and Activate for the automation
 * the build saved, and hands each to the caller.
 */
const SAVED_REPLY = 'The automation **`nightly-report`** has been built and saved.';

const saved = (status: string, ...triggerTypes: string[]) => ({
  id: 'automation-1',
  name: 'nightly-report',
  account_id: 'account-1',
  status,
  definition: { triggers: triggerTypes.map((type) => ({ type })) },
});

type Handlers = { onRunNow?: jest.Mock; onActivate?: jest.Mock };

async function completeBuild(savedWorkflow: ReturnType<typeof saved> | undefined, handlers: Handlers) {
  render(
    <AiGenerateWorkflowModal
      open
      onClose={jest.fn()}
      onGenerate={jest.fn()}
      onGenerateAsync={jest.fn().mockResolvedValue({ sessionId: 'session-1', conversationId: 'conversation-1' })}
      onPollConversation={jest
        .fn()
        .mockResolvedValue({ status: 'COMPLETED', saved: true, workflowJson: '', summaryText: SAVED_REPLY, savedWorkflow })}
      onRunNow={handlers.onRunNow}
      onActivate={handlers.onActivate}
      loading={false}
    />
  );
  fireEvent.change(document.querySelector('textarea') as HTMLTextAreaElement, { target: { value: 'Build a nightly report' } });
  await act(async () => {
    fireEvent.click(document.getElementById('wf-ai-generate-btn') as HTMLElement);
  });
  // One poll tick.
  await act(async () => {
    await jest.advanceTimersByTimeAsync(4000);
  });
}

const runNow = () => document.getElementById('wf-ai-run-now-btn');
const activate = () => document.getElementById('wf-ai-activate-btn');

describe('AiGenerateWorkflowModal — after a build has saved an automation', () => {
  beforeEach(() => {
    jest.useFakeTimers();
  });

  afterEach(() => {
    jest.useRealTimers();
  });

  it('offers Run now and Activate for a paused scheduled automation, and hands over the saved automation', async () => {
    const handlers = { onRunNow: jest.fn(), onActivate: jest.fn() };
    const automation = saved('PAUSED', 'schedule');
    await completeBuild(automation, handlers);

    expect(screen.getByText(SAVED_REPLY)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Open in Editor' })).toBeInTheDocument();

    fireEvent.click(runNow() as HTMLElement);
    expect(handlers.onRunNow).toHaveBeenCalledWith(automation);
    expect(handlers.onActivate).not.toHaveBeenCalled();

    fireEvent.click(activate() as HTMLElement);
    expect(handlers.onActivate).toHaveBeenCalledWith(automation);
  });

  it('offers only Run now for a manual automation', async () => {
    await completeBuild(saved('PAUSED', 'manual'), { onRunNow: jest.fn(), onActivate: jest.fn() });

    expect(runNow()).toBeInTheDocument();
    expect(activate()).not.toBeInTheDocument();
  });

  it('offers only Activate for an event automation', async () => {
    await completeBuild(saved('PAUSED', 'event'), { onRunNow: jest.fn(), onActivate: jest.fn() });

    expect(runNow()).not.toBeInTheDocument();
    expect(activate()).toBeInTheDocument();
  });

  it('offers neither for an automation saved inactive', async () => {
    await completeBuild(saved('INACTIVE', 'schedule'), { onRunNow: jest.fn(), onActivate: jest.fn() });

    expect(runNow()).not.toBeInTheDocument();
    expect(activate()).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Open in Editor' })).toBeInTheDocument();
  });

  it('offers neither when the saved automation is not known', async () => {
    await completeBuild(undefined, { onRunNow: jest.fn(), onActivate: jest.fn() });

    expect(runNow()).not.toBeInTheDocument();
    expect(activate()).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Open in Editor' })).toBeInTheDocument();
  });

  it('hides a button the caller did not wire — a user who may not run or activate', async () => {
    await completeBuild(saved('PAUSED', 'schedule'), {});

    expect(runNow()).not.toBeInTheDocument();
    expect(activate()).not.toBeInTheDocument();
  });
});
