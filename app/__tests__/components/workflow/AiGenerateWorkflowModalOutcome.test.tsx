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
 * What the modal shows when a build conversation completes.
 *
 * It used to decide "an automation was built" from a successful `finalize` tool call in the
 * conversation. That signal is wrong in both directions: the builder can save an automation
 * without recording a finalize call (the modal then said "This prompt didn't result in an
 * automation" about an automation sitting in the list), and a finalize call does not mean the save
 * went through (the modal then showed the success screen for an automation that was never saved).
 * The modal also replaced the AI's own reply — which carries the real reason — with a generic hint
 * to rephrase the prompt, under a title that said generation had failed.
 */
const GENERIC_HINT = /This prompt didn't result in an automation/;
const SAVED_REPLY = 'The automation **`nightly-report`** has been built and saved.';
const SAVE_REFUSED_REPLY =
  'The automation **`nightly-report`** was built but could not be saved.\n\n' +
  '**Reason:** an automation named "nightly-report" already exists in this account; ask again with a different name, or open the existing one to edit it';

type PollResult = { status: string; saved?: boolean; workflowJson?: string; summaryText?: string };

async function completeWith(result: PollResult) {
  render(
    <AiGenerateWorkflowModal
      open
      onClose={jest.fn()}
      onGenerate={jest.fn()}
      onGenerateAsync={jest.fn().mockResolvedValue({ sessionId: 'session-1', conversationId: 'conversation-1' })}
      onPollConversation={jest.fn().mockResolvedValue(result)}
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

describe('AiGenerateWorkflowModal — outcome of a completed build', () => {
  beforeEach(() => {
    jest.useFakeTimers();
  });

  afterEach(() => {
    jest.useRealTimers();
  });

  it('shows the success screen when the automation was saved without a finalize call', async () => {
    await completeWith({ status: 'COMPLETED', saved: true, workflowJson: '', summaryText: SAVED_REPLY });

    expect(screen.getByText(SAVED_REPLY)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Open in Editor' })).toBeInTheDocument();
    expect(screen.queryByText(GENERIC_HINT)).not.toBeInTheDocument();
  });

  it("shows the AI's reply, with the save error, when nothing was saved", async () => {
    await completeWith({ status: 'COMPLETED', saved: false, workflowJson: '', summaryText: SAVE_REFUSED_REPLY });

    expect(screen.getByText('Automation not saved')).toBeInTheDocument();
    expect(screen.queryByText('Generation Failed')).not.toBeInTheDocument();
    expect(screen.getByText(/already exists in this account; ask again with a different name/)).toBeInTheDocument();
    expect(screen.queryByText(GENERIC_HINT)).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Open in Editor' })).not.toBeInTheDocument();
  });

  it('does not show the success screen when a finalize call was recorded but the save was refused', async () => {
    await completeWith({ status: 'COMPLETED', saved: false, workflowJson: '{"name":"nightly-report"}', summaryText: SAVE_REFUSED_REPLY });

    expect(screen.getByText(/already exists in this account; ask again with a different name/)).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Open in Editor' })).not.toBeInTheDocument();
  });

  it("falls back to the conversation's own signal when the saved automation could not be checked", async () => {
    await completeWith({ status: 'COMPLETED', workflowJson: '{"name":"nightly-report"}', summaryText: SAVED_REPLY });

    expect(screen.getByRole('button', { name: 'Open in Editor' })).toBeInTheDocument();
  });

  it('keeps the rephrase hint when the run produced neither an automation nor a reply', async () => {
    await completeWith({ status: 'COMPLETED', saved: false, workflowJson: '', summaryText: '' });

    expect(screen.getByText(GENERIC_HINT)).toBeInTheDocument();
    expect(screen.getByText('Generation Failed')).toBeInTheDocument();
  });
});
