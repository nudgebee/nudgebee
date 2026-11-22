import React from 'react';
import { act, fireEvent, render, screen } from '@testing-library/react';
import apiKubernetes from '@api1/kubernetes';
import { hasWriteAccess } from '@lib/auth';
import AskAiCard from '@components/k8s/investigate/cards/AskAiCard';

jest.mock('@api1/kubernetes', () => ({
  __esModule: true,
  default: { generateAiRecommendation: jest.fn() },
}));

jest.mock('@api1/ask-nudgebee', () => ({
  __esModule: true,
  default: {
    llmConversationHistoryForInvestigation: jest.fn().mockResolvedValue({ data: { data: { llm_conversations: [] } } }),
  },
  createConversationFetcher: () => ({ reset: jest.fn(), fetch: jest.fn() }),
}));

jest.mock('@lib/auth', () => ({ hasWriteAccess: jest.fn() }));

jest.mock('@shared/viewers/MarkDowns', () => ({
  __esModule: true,
  default: ({ data }) => <div data-testid='analysis-markdown'>{data}</div>,
}));

jest.mock('@hooks/useConversationSuggestions', () => ({
  useConversationSuggestions: () => ({ suggestions: [], fetchSuggestions: jest.fn(), clearSuggestions: jest.fn() }),
}));
jest.mock('@hooks/useTenantBranding', () => ({ getNubiIconUrl: () => '/nubi.svg' }));
jest.mock('@shared/viewers/SimpleDiffViewer', () => () => null);
jest.mock('@components/recommendations/KubernetesRightSizingUpdateForm', () => () => null);
jest.mock('@components/k8s/investigate/cards/EventRaisePrPanel', () => () => null);

// The Investigate page reads an analysis' status on a timer while it runs. It used to stop after
// 50 reads (about 4 minutes) and leave "Step 2 of 4 · Investigating" on screen for a run that had
// since finished or failed (#40738).
describe('AskAiCard status polling', () => {
  const event = { id: 'event-1', fingerprint: 'fingerprint-1', cloud_account_id: 'account-1' };

  const running = {
    event_id: event.id,
    status: 'IN_PROGRESS',
    summary: 'First summary',
    task_statuses: { summary: 'COMPLETED', investigation: 'IN_PROGRESS', log_analysis: 'IN_PROGRESS', detailed_response: 'IN_PROGRESS' },
  };
  const completed = {
    ...running,
    status: 'COMPLETED',
    detailed_response: 'Final report',
    task_statuses: { summary: 'COMPLETED', investigation: 'COMPLETED', log_analysis: 'COMPLETED', detailed_response: 'COMPLETED' },
  };
  const failed = {
    ...running,
    status: 'FAILED',
    status_reason: 'analysis failed - The AI service is temporarily unavailable',
    task_statuses: { summary: 'COMPLETED', investigation: 'FAILED', log_analysis: 'COMPLETED', detailed_response: 'FAILED' },
  };

  const STAGE_LINE = /Step 2 of 4 · Investigating/;

  // Renders the card as the page does after its first read, which returned `first`.
  const renderCard = async (first = running) => {
    apiKubernetes.generateAiRecommendation.mockResolvedValueOnce(first);
    const card = new AskAiCard();
    await card.canRenderContent(null, event);
    const Content = card.getContentComponents()[0];
    // Async so the waiting-conversation check made on mount settles inside act.
    await act(async () => {
      render(<Content />);
    });
    return card;
  };

  const advance = (ms) => act(() => jest.advanceTimersByTimeAsync(ms));

  beforeEach(() => {
    jest.clearAllMocks();
    jest.useFakeTimers();
    hasWriteAccess.mockReturnValue(true);
  });

  afterEach(() => {
    jest.useRealTimers();
  });

  it('shows the result when a running investigation completes', async () => {
    await renderCard();
    expect(screen.getByText(STAGE_LINE)).toBeInTheDocument();

    apiKubernetes.generateAiRecommendation.mockResolvedValue(completed);
    await advance(5000);

    expect(screen.getByText('Final report')).toBeInTheDocument();
    expect(screen.queryByText(STAGE_LINE)).not.toBeInTheDocument();
  });

  it('shows the failure, without the progress line, when a running investigation fails', async () => {
    await renderCard();
    expect(screen.getByText(STAGE_LINE)).toBeInTheDocument();

    apiKubernetes.generateAiRecommendation.mockResolvedValue(failed);
    await advance(5000);

    expect(screen.getByRole('alert')).toHaveTextContent('The investigation failed');
    expect(screen.getByRole('alert')).toHaveTextContent('The AI service is temporarily unavailable');
    expect(screen.queryByText(STAGE_LINE)).not.toBeInTheDocument();
    expect(screen.queryByRole('status')).not.toBeInTheDocument();
  });

  it('starts a new run from the failure message', async () => {
    await renderCard(failed);

    apiKubernetes.generateAiRecommendation.mockResolvedValue(running);
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));

    expect(apiKubernetes.generateAiRecommendation).toHaveBeenLastCalledWith(event.cloud_account_id, event.id, 'pod_log_analysis', true);
  });

  it('offers no retry to a reader who may not start a run', async () => {
    hasWriteAccess.mockReturnValue(false);
    await renderCard(failed);

    expect(screen.getByRole('alert')).toHaveTextContent('The investigation failed');
    expect(screen.queryByRole('button', { name: 'Retry' })).not.toBeInTheDocument();
  });

  it('offers a retry when the first read already failed with nothing to show', async () => {
    await renderCard({ event_id: event.id, status: 'FAILED', status_reason: 'budget exceeded' });

    expect(screen.getByRole('alert')).toHaveTextContent('budget exceeded');
    expect(screen.getByRole('button', { name: 'Retry' })).toBeInTheDocument();
  });

  it('keeps reading the status of a run that outlasts 50 reads', async () => {
    await renderCard();
    apiKubernetes.generateAiRecommendation.mockResolvedValue(running);
    // 6 minutes: past the old limit, which was reached after 4m10s, and into the slower pace.
    await advance(6 * 60 * 1000);
    expect(screen.getByText(STAGE_LINE)).toBeInTheDocument();

    apiKubernetes.generateAiRecommendation.mockResolvedValue(completed);
    await advance(15000);

    expect(screen.getByText('Final report')).toBeInTheDocument();
    expect(screen.queryByText(STAGE_LINE)).not.toBeInTheDocument();
  });

  it('stops reading once the run has ended', async () => {
    await renderCard();
    apiKubernetes.generateAiRecommendation.mockResolvedValue(completed);
    await advance(5000);
    const reads = apiKubernetes.generateAiRecommendation.mock.calls.length;

    await advance(60000);

    expect(apiKubernetes.generateAiRecommendation).toHaveBeenCalledTimes(reads);
  });

  it('says the status cannot be read instead of repeating the last one, and recovers', async () => {
    await renderCard();
    // What the API layer returns when the request got no answer.
    apiKubernetes.generateAiRecommendation.mockResolvedValue('');
    await advance(15000);

    expect(screen.getByRole('alert')).toHaveTextContent("Can't read the investigation's status");
    expect(screen.queryByText(STAGE_LINE)).not.toBeInTheDocument();

    apiKubernetes.generateAiRecommendation.mockResolvedValue(running);
    await advance(5000);

    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    expect(screen.getByText(STAGE_LINE)).toBeInTheDocument();
  });
});
