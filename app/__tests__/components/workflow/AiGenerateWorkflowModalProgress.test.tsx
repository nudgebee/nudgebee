import React from 'react';
import { render, screen, fireEvent, act } from '@testing-library/react';
import AiGenerateWorkflowModal from '@components/workflow/components/AiGenerateWorkflowModal';

jest.mock('@shared/viewers/MarkDowns', () => {
  return function MockMarkDowns({ data }: { data: string }) {
    return <div data-testid='markdown-output'>{data}</div>;
  };
});

/**
 * What the modal shows while a build is running.
 *
 * Its only progress signal used to be a label that advances on a timer, so a run that had died
 * looked exactly like one that was working: a follow-up answer the server never picked up left
 * the modal on "Validating and refining…" with a moving progress bar for as long as it stayed
 * open.
 */
const STALLED = /No progress for 3 minutes/;
const MINUTE = 60 * 1000;

// Minutes of fake time mean hundreds of one-second elapsed-timer renders; give them room.
jest.setTimeout(60000);

type PollResult = Record<string, unknown> & { status: string };

async function advance(ms: number) {
  await act(async () => {
    await jest.advanceTimersByTimeAsync(ms);
  });
}

async function startBuild(poll: () => Promise<PollResult | null>, onApproveOrRespond = jest.fn().mockResolvedValue(undefined)) {
  render(
    <AiGenerateWorkflowModal
      open
      onClose={jest.fn()}
      onGenerate={jest.fn()}
      onGenerateAsync={jest.fn().mockResolvedValue({ sessionId: 'session-1', conversationId: 'conversation-1' })}
      onPollConversation={poll as any}
      onApproveOrRespond={onApproveOrRespond}
      loading={false}
    />
  );
  fireEvent.change(document.querySelector('textarea') as HTMLTextAreaElement, { target: { value: 'Build a nightly report' } });
  await act(async () => {
    fireEvent.click(document.getElementById('wf-ai-generate-btn') as HTMLElement);
  });
}

describe('AiGenerateWorkflowModal — while a build runs', () => {
  beforeEach(() => {
    jest.useFakeTimers();
  });

  afterEach(() => {
    jest.useRealTimers();
  });

  it('shows how many tasks the builder has added so far', async () => {
    await startBuild(async () => ({ status: 'IN_PROGRESS', progress: { steps: 12, tasksAdded: 7 } }));
    await advance(4000);

    expect(screen.getByText('Building automation: 7 tasks added')).toBeInTheDocument();
  });

  it('says the run may be stuck after three minutes without change, and takes it back when the run moves', async () => {
    let result: PollResult = { status: 'IN_PROGRESS', progress: { steps: 5, tasksAdded: 1 } };
    await startBuild(async () => result);

    await advance(2 * MINUTE);
    expect(screen.queryByText(STALLED)).not.toBeInTheDocument();

    await advance(2 * MINUTE);
    expect(screen.getByText(STALLED)).toBeInTheDocument();

    result = { status: 'IN_PROGRESS', progress: { steps: 6, tasksAdded: 2 } };
    await advance(4000);
    expect(screen.queryByText(STALLED)).not.toBeInTheDocument();
  });

  it('does not call a build stuck while its step count keeps growing', async () => {
    let steps = 0;
    await startBuild(async () => ({ status: 'IN_PROGRESS', progress: { steps: (steps += 1), tasksAdded: steps } }));

    await advance(4 * MINUTE);
    expect(screen.queryByText(STALLED)).not.toBeInTheDocument();
  });

  it('ignores a result that arrives after the run was cancelled', async () => {
    // The poll is a request in flight; cancelling must not let its late answer put the modal on
    // a result screen for a run the user has left.
    let deliver: (result: PollResult) => void = () => {};
    await startBuild(
      () =>
        new Promise<PollResult>((resolve) => {
          deliver = resolve;
        })
    );
    await advance(4000);

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Cancel Generation' }));
    });
    await act(async () => {
      deliver({ status: 'COMPLETED', saved: true, summaryText: 'The automation has been built and saved.' });
    });

    expect(screen.queryByRole('button', { name: 'Open in Editor' })).not.toBeInTheDocument();
    expect(document.getElementById('wf-ai-generate-btn')).toBeInTheDocument();
  });

  it('says the run may be stuck when the conversation cannot be read for three minutes', async () => {
    await startBuild(async () => null);

    await advance(2 * MINUTE);
    expect(screen.queryByText(STALLED)).not.toBeInTheDocument();

    await advance(2 * MINUTE);
    expect(screen.getByText(STALLED)).toBeInTheDocument();
  });

  it('says so when an answered question is never picked up', async () => {
    // The conversation keeps reporting the same question as still waiting — what the modal sees
    // when the server accepted the answer but did not act on it.
    const question = {
      status: 'WAITING',
      planText: 'Which account should this run against?',
      followupType: 'text',
      messageId: 'followup-1',
      progress: { steps: 0, tasksAdded: 0 },
    };
    const respond = jest.fn().mockResolvedValue(undefined);
    await startBuild(async () => question, respond);
    await advance(4000);

    fireEvent.change(document.querySelector('textarea') as HTMLTextAreaElement, { target: { value: 'the first account' } });
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Submit' }));
    });
    expect(respond).toHaveBeenCalledTimes(1);

    await advance(2 * MINUTE);
    expect(screen.queryByText(STALLED)).not.toBeInTheDocument();

    await advance(2 * MINUTE);
    expect(screen.getByText(STALLED)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Cancel Generation' })).toBeInTheDocument();
  });
});
