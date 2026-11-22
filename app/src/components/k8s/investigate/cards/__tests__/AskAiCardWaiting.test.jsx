import { act, fireEvent, render, screen } from '@testing-library/react';
import apiAskNudgebee from '@api1/ask-nudgebee';
import AskAiCard from '../AskAiCard';

// With the remediation loop on, the alert has one conversation: the investigation's stages and the remediation's
// approvals are in it together (#40734). The investigation card shows only the investigation's own question.

jest.mock('@api1/kubernetes', () => ({ __esModule: true, default: { generateAiRecommendation: jest.fn() } }));
jest.mock('@api1/ask-nudgebee', () => ({
  __esModule: true,
  default: { llmConversationHistoryForInvestigation: jest.fn(), getLlmConversation: jest.fn() },
  createConversationFetcher: () => ({ fetch: jest.fn().mockResolvedValue(null), reset: jest.fn() }),
}));
jest.mock('@components/llm/common/EvidenceDrawer', () => ({ __esModule: true, default: () => null }));
jest.mock('@hooks/useConversationSuggestions', () => ({
  useConversationSuggestions: () => ({ suggestions: [], fetchSuggestions: jest.fn(), clearSuggestions: jest.fn() }),
}));
jest.mock('@hooks/useTenantBranding', () => ({
  getNubiIconUrl: () => '',
  getAssistantLabel: () => 'Nubi',
  useTenantBranding: () => ({ assistantName: 'nubi' }),
}));
jest.mock('@lib/auth', () => ({ hasWriteAccess: () => true }));
jest.mock('@shared/viewers/MarkDowns', () => ({ __esModule: true, default: ({ data }) => <div data-testid='md'>{data}</div> }));
const mockOpenWithContext = jest.fn();
jest.mock('@context/NubiGlobalChatContext', () => ({ useOptionalNubiGlobalChat: () => ({ openWithContext: mockOpenWithContext }) }));

const stageConfig = JSON.stringify({ investigation_run_id: 'run-1' });
const waitingConversation = { id: 'conv-1', updated_at: '2026-10-24T10:00:00Z', status: 'WAITING', session_id: 'event-fp1' };
const question = (id) => ({
  id,
  message_type: 'followup',
  status: 'WAITING',
  response: '',
  parent_agent_id: 'agent-1',
  message: 'Approve this change?',
  message_config: JSON.stringify({ followupType: 'tool_confirmation', question: 'Approve this change?', toolName: 'run_step' }),
});

const renderCard = async (messages, aiData = {}) => {
  apiAskNudgebee.llmConversationHistoryForInvestigation.mockResolvedValue({ data: { data: { llm_conversations: [waitingConversation] } } });
  apiAskNudgebee.getLlmConversation.mockResolvedValue({ data: { data: { llm_conversations: [{ llm_conversation_messages: messages }] } } });
  const card = new AskAiCard();
  card.event = { id: 'ev-1', cloud_account_id: 'acc-1', fingerprint: 'fp1' };
  // As the page sets it for a tenant with the remediation loop on.
  card.remediationLoopOn = true;
  card.aiData = { event_id: 'ev-1', analysis_versions: [], status: 'completed', summary: 'Root cause.', ...aiData };
  const Content = card.getContentComponents()[0];
  render(<Content />);
  await act(async () => {});
};

describe('AskAiCard: what the investigation waits on (remediation loop on)', () => {
  beforeEach(() => {
    apiAskNudgebee.llmConversationHistoryForInvestigation.mockReset();
    apiAskNudgebee.getLlmConversation.mockReset();
  });

  it('does not show a remediation approval as the investigation’s question', async () => {
    await renderCard([
      { id: 'stage-1', message_type: 'generation', status: 'COMPLETED', message_config: stageConfig },
      { id: 'turn-2', message_type: 'generation', status: 'WAITING', message_config: JSON.stringify({}) },
      question('f-2'),
    ]);
    expect(apiAskNudgebee.llmConversationHistoryForInvestigation).toHaveBeenCalledWith(expect.objectContaining({ session_id: 'event-fp1' }));
    expect(screen.queryByText('Approve this change?')).toBeNull();
  });

  it('shows the investigation’s own waiting question', async () => {
    await renderCard([{ id: 'stage-1', message_type: 'generation', status: 'WAITING', message_config: stageConfig }, question('f-1')]);
    expect(await screen.findByText('Approve this change?')).toBeInTheDocument();
  });

  it('with the loop off (no turn tagged) shows a waiting question as before', async () => {
    await renderCard([{ id: 'turn-1', message_type: 'generation', status: 'WAITING', message_config: '{}' }, question('f-1')]);
    expect(await screen.findByText('Approve this change?')).toBeInTheDocument();
  });

  it('says why a re-investigation waits', async () => {
    await renderCard([], { status: 'in_progress', status_reason: 'waiting on an approval', task_statuses: { investigation: 'IN_PROGRESS' } });
    expect(screen.getByTestId('investigation-waiting-reason')).toHaveTextContent('An approval is waiting in this alert’s conversation');
  });

  it('opens the investigation’s steps in the alert’s conversation, answerable like the Remediation tab', async () => {
    await renderCard([]);
    fireEvent.click(screen.getByTestId('view-investigation-steps'));
    expect(mockOpenWithContext).toHaveBeenCalledWith({ accountId: 'acc-1', sessionId: 'event-fp1', eventId: 'ev-1' });
  });

  it('does not offer the investigation’s steps in the chat with the remediation loop off', async () => {
    apiAskNudgebee.llmConversationHistoryForInvestigation.mockResolvedValue({ data: { data: { llm_conversations: [] } } });
    const card = new AskAiCard();
    card.event = { id: 'ev-1', cloud_account_id: 'acc-1', fingerprint: 'fp1' };
    card.aiData = { event_id: 'ev-1', analysis_versions: [], status: 'completed', summary: 'Root cause.' };
    const Content = card.getContentComponents()[0];
    render(<Content />);
    await act(async () => {});
    expect(screen.queryByTestId('view-investigation-steps')).toBeNull();
  });
});
