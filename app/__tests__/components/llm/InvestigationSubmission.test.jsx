import React from 'react';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';

const noop = jest.fn();
const mockStart = jest.fn();
const mockQueue = jest.fn();
const router = { query: { session_id: 'session' }, isReady: true, push: noop, replace: noop, events: { on: noop, off: noop } };

jest.mock('@ui/Tooltip', () => ({ __esModule: true, default: ({ children }) => <>{children}</> }));
jest.mock('next-auth/react', () => ({ useSession: () => ({ data: null, status: 'authenticated' }) }));
jest.mock('next/router', () => ({ useRouter: () => router }));
jest.mock('@lib/auth', () => ({ getUserSession: () => ({ user: { name: 'Tester' } }), hasReadAccess: () => true, hasPermission: () => true }));
jest.mock('@lib/router', () => ({ applyFiltersOnRouter: jest.fn() }));
jest.mock('@context/DataContext', () => ({ useData: () => ({ selectedCluster: {}, setSelectedCluster: noop }) }));
jest.mock('@context/NubiGlobalChatContext', () => ({ useOptionalNubiGlobalChat: () => null }));
jest.mock('@hooks/useBrowserNotification', () => ({ useBrowserNotification: () => ({}) }));
jest.mock('@hooks/useClusterInsights', () => ({ useClusterInsights: () => ({ troubleShootData: [], optimizationData: [] }) }));
jest.mock('@hooks/useAgentConfiguration', () => ({
  useAgentConfiguration: () => ({ allAgents: [], enabledAgents: [], allFunctions: [], refreshAgents: noop }),
}));
jest.mock('@hooks/useConversationManager', () => ({
  useConversationManager: () => ({ rawConversations: [], likedConversations: [], selectedConversation: { id: 'conversation' } }),
}));
jest.mock('@hooks/useConversationSuggestions', () => ({
  useConversationSuggestions: () => ({ suggestions: [], clearSuggestions: noop, fetchSuggestions: noop }),
}));
jest.mock('@hooks/useTokenUsage', () => ({ useTokenUsage: () => ({ resetTokenMetrics: noop, fetchTokenUsage: noop }) }));
jest.mock('@hooks/useLLMInvestigationControl', () => ({
  useLLMInvestigationControl: () => {
    const [currentlyProcessingQuestion, setCurrentlyProcessingQuestion] = React.useState(null);
    const [conversationStatus, setConversationStatus] = React.useState('COMPLETED');
    const [messages, setMessages] = React.useState([]);
    return {
      currentlyProcessingQuestion,
      setCurrentlyProcessingQuestion,
      conversationStatus,
      setConversationStatus,
      messages,
      setMessages,
      startInvestigation: mockStart,
      queueInvestigationMessage: mockQueue,
      conversationIdAtDb: 'conversation',
      queueSupport: { enabled: true, maxMessages: 1 },
      queuedMessages: [],
      setAllowStop: noop,
      fetchConversation: noop,
      resetInvestigationState: noop,
      checkConversationExists: noop,
      availableModels: [],
      availableCredentials: [],
    };
  },
}));

// Keep the real composer, pending list and submission hooks; unrelated page panels
// have independent providers and network calls that are outside this regression.
for (const path of [
  '@shared/NotifyBanner',
  '@shared/navigation/ClusterDropDown',
  '@shared/ConversationLoader',
  '@shared/layout/AskNudgebeeLayoutV2',
  '@components/llm/common/ConversationShimmer',
  '@components/llm/ConversationListV2',
  '@components/llm/ConversationListDrawer',
  '@components/llm/DynamicGreeting',
  '@components/llm/NubiWelcome',
  '@components/llm/common/JumpToLatestPill',
  '@components/llm/common/QuestionNavigator',
  '@components/llm/common/FollowupSheet',
  '@components/llm/TroubleshootList',
]) {
  jest.doMock(path, () => ({ __esModule: true, default: ({ children }) => <>{children}</> }));
}
jest.doMock('@components/llm/MessageStream', () => ({
  __esModule: true,
  default: ({ messages }) => (
    <div data-testid='transcript'>
      {(messages || []).map((message) => (
        <p key={message.id}>{message.text}</p>
      ))}
    </div>
  ),
}));
jest.doMock('@components/llm/common/TokenUsageDisplay', () => ({ ConversationTokenUsage: () => null }));
jest.doMock('@components/k8s/KubernetesClusterSummary', () => ({ SummaryBlock: ({ children }) => <div>{children}</div> }));
const Investigation = require('@components/llm/KubernetesLLMResponseGeneratorV2').default;

beforeEach(() => {
  jest.clearAllMocks();
  router.query = { session_id: 'session' };
  let id = 0;
  Object.defineProperty(global.crypto, 'randomUUID', { configurable: true, value: () => `pending-${++id}` });
  mockQueue.mockImplementation(() => new Promise(() => {}));
});

function typeDraft(text) {
  const composer = screen.getByRole('textbox');
  fireEvent.change(composer, { target: { value: text } });
  return composer;
}

it('clears a normal send and queues another message before the first response returns; late success preserves a newer draft', async () => {
  let finish;
  mockStart.mockImplementation(
    ({ onSuccess }) =>
      new Promise((resolve) => {
        finish = () => {
          onSuccess('session');
          resolve(true);
        };
      })
  );
  render(<Investigation accountId='account' sessionId='session' popup persistLastSession={false} />);
  typeDraft('first question');
  fireEvent.click(screen.getByRole('button', { name: 'Send', exact: true }));
  await waitFor(() => expect(screen.getByRole('textbox')).toHaveValue(''));
  expect(mockStart).toHaveBeenCalledTimes(1);
  // Shown straight away as a question in the transcript, not as a pending queue row.
  expect(within(screen.getByTestId('transcript')).getByText('first question')).toBeInTheDocument();
  expect(screen.queryByTestId('queued-messages-banner')).not.toBeInTheDocument();

  typeDraft('queued follow-up');
  fireEvent.click(screen.getByRole('button', { name: 'Send', exact: true }));
  await waitFor(() => expect(screen.getByRole('textbox')).toHaveValue(''));
  // The server rejects a queued message until the pending send is recorded, so it waits.
  expect(within(screen.getByTestId('queued-messages-banner')).getByText('queued follow-up')).toBeInTheDocument();
  expect(mockQueue).not.toHaveBeenCalled();
  typeDraft('unsent draft');
  await act(async () => finish());
  await waitFor(() => expect(mockQueue).toHaveBeenCalledWith({ text: 'queued follow-up', images: undefined }));
  expect(screen.getByRole('textbox')).toHaveValue('unsent draft');
  expect(mockStart).toHaveBeenCalledTimes(1);
});

it('retains a failed normal submission without replacing a newer draft or retrying', async () => {
  let finish;
  mockStart.mockImplementation(
    ({ onFailure }) =>
      new Promise((resolve) => {
        finish = () => {
          onFailure();
          resolve(false);
        };
      })
  );
  render(<Investigation accountId='account' sessionId='session' popup persistLastSession={false} />);
  typeDraft('unconfirmed question');
  fireEvent.click(screen.getByRole('button', { name: 'Send', exact: true }));
  await waitFor(() => expect(screen.getByRole('textbox')).toHaveValue(''));
  typeDraft('keep my new draft');
  await act(async () => finish());
  expect(screen.getByRole('textbox')).toHaveValue('keep my new draft');
  // The failed send leaves the transcript and is kept in the queue tray for recovery.
  expect(within(screen.getByTestId('transcript')).queryByText('unconfirmed question')).not.toBeInTheDocument();
  expect(within(screen.getByTestId('queued-messages-banner')).getByText('unconfirmed question')).toBeInTheDocument();
  expect(screen.getByText('Refresh history')).toBeInTheDocument();
  expect(mockStart).toHaveBeenCalledTimes(1);
  expect(mockQueue).not.toHaveBeenCalled();
});

it('keeps pending follow-ups when a conversation-id-only foreground send succeeds', async () => {
  router.query = { conversation_id: 'conversation' };
  let finish;
  mockStart.mockImplementation(
    ({ onSuccess }) =>
      new Promise((resolve) => {
        finish = () => {
          onSuccess(undefined);
          resolve(true);
        };
      })
  );
  render(<Investigation accountId='account' conversationId='conversation' popup persistLastSession={false} />);
  typeDraft('first');
  fireEvent.click(screen.getByRole('button', { name: 'Send', exact: true }));
  await waitFor(() => expect(screen.getByRole('textbox')).toHaveValue(''));
  typeDraft('pending follow-up');
  fireEvent.click(screen.getByRole('button', { name: 'Send', exact: true }));
  await waitFor(() => expect(screen.getByText('pending follow-up')).toBeInTheDocument());
  await act(async () => finish());
  await waitFor(() => expect(mockQueue).toHaveBeenCalled());
  expect(screen.getByText('pending follow-up')).toBeInTheDocument();
});

it('retires the question bubble when a superseded normal send is not confirmed', async () => {
  let finish;
  // A superseded request resolves false without running onFailure.
  mockStart.mockImplementation(
    () =>
      new Promise((resolve) => {
        finish = () => resolve(false);
      })
  );
  render(<Investigation accountId='account' sessionId='session' popup persistLastSession={false} />);
  typeDraft('superseded question');
  fireEvent.click(screen.getByRole('button', { name: 'Send', exact: true }));
  await waitFor(() => expect(within(screen.getByTestId('transcript')).getByText('superseded question')).toBeInTheDocument());
  await act(async () => finish());
  expect(within(screen.getByTestId('transcript')).queryByText('superseded question')).not.toBeInTheDocument();
  expect(within(screen.getByTestId('queued-messages-banner')).getByText('superseded question')).toBeInTheDocument();
});
