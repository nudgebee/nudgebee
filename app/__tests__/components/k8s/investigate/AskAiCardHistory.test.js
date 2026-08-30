import React from 'react';
import { fireEvent, render, screen } from '@testing-library/react';
import apiKubernetes from '@api1/kubernetes';
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

jest.mock('@shared/viewers/MarkDowns', () => ({
  __esModule: true,
  default: ({ data }) => <div data-testid='analysis-markdown'>{data}</div>,
}));

jest.mock('@ui/Select', () => ({
  Select: ({ value, onChange, options, id }) => (
    <select id={id} data-testid={id} value={value} onChange={(event) => onChange(event.target.value)}>
      {options.map((option) => (
        <option key={option.value} value={option.value}>
          {option.label}
        </option>
      ))}
    </select>
  ),
}));

jest.mock('@hooks/useConversationSuggestions', () => ({
  useConversationSuggestions: () => ({ suggestions: [], fetchSuggestions: jest.fn(), clearSuggestions: jest.fn() }),
}));
jest.mock('@hooks/useTenantBranding', () => ({ getNubiIconUrl: () => '/nubi.svg' }));
jest.mock('@shared/viewers/SimpleDiffViewer', () => () => null);
jest.mock('@components/recommendations/KubernetesRightSizingUpdateForm', () => () => null);
jest.mock('@components/k8s/investigate/cards/EventRaisePrPanel', () => () => null);

describe('AskAiCard analysis history', () => {
  const event = {
    id: 'event-current',
    fingerprint: 'fingerprint-1',
    cloud_account_id: 'account-1',
  };

  beforeEach(() => {
    jest.clearAllMocks();
  });

  it('switches the complete analysis view to an immutable snapshot', async () => {
    apiKubernetes.generateAiRecommendation.mockResolvedValue({
      event_id: event.id,
      status: 'COMPLETED',
      generated_at: '2026-08-28T12:00:00Z',
      summary: 'Current root cause',
      task_statuses: { summary: 'COMPLETED' },
      analysis_versions: [
        {},
        {
          id: 'snapshot-1',
          event_id: event.id,
          status: 'COMPLETED',
          generated_at: '2026-08-28T11:00:00Z',
          data: {
            event_id: event.id,
            status: 'COMPLETED',
            summary: 'Previous root cause',
            task_statuses: { summary: 'COMPLETED' },
          },
        },
      ],
    });

    const card = new AskAiCard();
    await card.canRenderContent(null, event);
    const Content = card.getContentComponents()[0];
    render(<Content />);

    expect(screen.getByText('Current root cause')).toBeInTheDocument();
    expect(screen.getByTestId('analysis-version-select')).toHaveLength(2);
    fireEvent.change(screen.getByTestId('analysis-version-select'), { target: { value: 'snapshot-1' } });
    expect(await screen.findByText('Previous root cause')).toBeInTheDocument();
  });

  it('shows an embedded recurring event version without regenerating it', async () => {
    apiKubernetes.generateAiRecommendation.mockResolvedValueOnce({
      event_id: event.id,
      status: 'IN_PROGRESS',
      task_statuses: { summary: 'IN_PROGRESS' },
      analysis_versions: [
        {
          id: 'event-older',
          event_id: 'event-previous',
          status: 'COMPLETED',
          generated_at: '2026-08-27T11:00:00Z',
          data: {
            event_id: 'event-previous',
            status: 'COMPLETED',
            summary: 'Previous recurring-event root cause',
            task_statuses: { summary: 'COMPLETED' },
          },
        },
      ],
    });

    const card = new AskAiCard();
    await card.canRenderContent(null, event);
    const Content = card.getContentComponents()[0];
    render(<Content />);

    expect(screen.getByText('A new analysis is in progress. Showing the most recent completed version.')).toBeInTheDocument();
    expect(screen.getByText('Previous recurring-event root cause')).toBeInTheDocument();
    expect(apiKubernetes.generateAiRecommendation).toHaveBeenCalledTimes(1);
  });
});
