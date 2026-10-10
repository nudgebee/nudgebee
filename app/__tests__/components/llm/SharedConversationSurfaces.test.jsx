import React from 'react';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';

// #30669: one conversation open in two surfaces (the Ask Nudgebee page and the automation
// editor's Nubi panel). Each surface must notice turns the other one runs.

const noop = jest.fn();
const router = { query: {}, isReady: true, push: noop, replace: noop, events: { on: noop, off: noop } };
// What the server would return for the conversation on the next read.
const server = { status: 'COMPLETED', messages: [] };
const mockFetchConversation = jest.fn();
const mockStart = jest.fn();

const question = (id, text) => ({ id: `${id}-question`, messageId: id, type: 'question', text });

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
    const [conversationStatus, setConversationStatus] = React.useState('');
    const [messages, setMessages] = React.useState([]);
    // A read applies whatever the server holds at that moment, like the real delta fetcher,
    // which also ends the in-flight question once the turn is no longer running.
    const fetchConversation = React.useCallback(async (...args) => {
      mockFetchConversation(...args);
      setConversationStatus(server.status);
      setMessages(server.messages);
      if (['COMPLETED', 'FAILED', 'KILLED', 'TERMINATED', 'WAITING'].includes(server.status)) {
        setCurrentlyProcessingQuestion(null);
      }
    }, []);
    const startInvestigation = React.useCallback(
      (args) => mockStart(args, { setConversationStatus, setMessages, setCurrentlyProcessingQuestion }),
      []
    );
    return {
      currentlyProcessingQuestion,
      setCurrentlyProcessingQuestion,
      conversationStatus,
      setConversationStatus,
      messages,
      setMessages,
      startInvestigation,
      queueInvestigationMessage: noop,
      conversationIdAtDb: 'conversation',
      queueSupport: { enabled: true, maxMessages: 5 },
      queuedMessages: [],
      setAllowStop: noop,
      fetchConversation,
      resetInvestigationState: noop,
      checkConversationExists: noop,
      availableModels: [],
      availableCredentials: [],
    };
  },
}));

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
jest.doMock('@components/llm/MessageStream', () => ({ __esModule: true, default: () => null }));
jest.doMock('@components/llm/common/TokenUsageDisplay', () => ({ ConversationTokenUsage: () => null }));
jest.doMock('@components/k8s/KubernetesClusterSummary', () => ({ SummaryBlock: ({ children }) => <div>{children}</div> }));
const Investigation = require('@components/llm/KubernetesLLMResponseGeneratorV2').default;

const pollReads = () => mockFetchConversation.mock.calls.filter((call) => call[2] === 'poll');

function renderSidebar(props = {}) {
  return render(<Investigation accountId='account' sessionId='session' popup apiMode='workflow' persistLastSession={false} {...props} />);
}

async function loaded() {
  await waitFor(() => expect(mockFetchConversation).toHaveBeenCalledWith('session', '', 'selected', false));
  await waitFor(() => expect(screen.getByRole('textbox')).not.toBeDisabled());
}

function returnToTab() {
  act(() => {
    window.dispatchEvent(new Event('focus'));
    document.dispatchEvent(new Event('visibilitychange'));
  });
}

beforeAll(() => {
  // jsdom does not implement scrolling; the thread scrolls to the latest message on load.
  window.scrollTo = jest.fn();
});

beforeEach(() => {
  jest.clearAllMocks();
  router.query = {};
  server.status = 'COMPLETED';
  server.messages = [question('m1', 'create an automation')];
});

it('editor panel picks up a turn running in the other surface when the user returns, and blocks its own prompt', async () => {
  renderSidebar();
  await loaded();

  // The user went to the Ask Nudgebee tab and asked for a change there.
  server.status = 'IN_PROGRESS';
  server.messages = [question('m1', 'create an automation'), question('m2', 'add a namespace filter')];
  returnToTab();

  await waitFor(() => expect(screen.getByRole('textbox')).toBeDisabled());
  // Focus and visibilitychange both fire on a tab switch; only one read goes out.
  expect(pollReads()).toEqual([['session', '', 'poll', false]]);
});

it('Ask Nudgebee page switches to queueing when a turn is running in the editor panel', async () => {
  render(<Investigation accountId='account' sessionId='session' popup persistLastSession={false} />);
  await loaded();
  expect(screen.getByRole('textbox')).toHaveAttribute('placeholder', 'Ask a question...');

  server.status = 'IN_PROGRESS';
  returnToTab();

  await waitFor(() => expect(screen.getByRole('textbox')).toHaveAttribute('placeholder', 'Queue a follow-up message...'));
});

it('editor reloads the automation once for a turn that finished in the other surface, not on the first load', async () => {
  const onConversationComplete = jest.fn();
  renderSidebar({ onConversationComplete });
  await loaded();
  expect(onConversationComplete).not.toHaveBeenCalled();

  // The other surface's turn started and finished while this tab was in the background.
  server.messages = [question('m1', 'create an automation'), question('m2', 'add a namespace filter')];
  returnToTab();

  await waitFor(() => expect(onConversationComplete).toHaveBeenCalledWith('session'));
  // Coming back again with nothing new does not reload a second time.
  returnToTab();
  await waitFor(() => expect(pollReads()).toHaveLength(2));
  expect(onConversationComplete).toHaveBeenCalledTimes(1);
});

it('a turn this panel watched to completion still reloads exactly once', async () => {
  const onConversationComplete = jest.fn();
  renderSidebar({ onConversationComplete });
  await loaded();

  server.status = 'IN_PROGRESS';
  server.messages = [question('m1', 'create an automation'), question('m2', 'add a namespace filter')];
  returnToTab();
  await waitFor(() => expect(screen.getByRole('textbox')).toBeDisabled());

  // The 1 s poll that is now running sees the turn finish.
  server.status = 'COMPLETED';
  await waitFor(() => expect(onConversationComplete).toHaveBeenCalledTimes(1), { timeout: 3000 });
  await waitFor(() => expect(screen.getByRole('textbox')).not.toBeDisabled());
  expect(onConversationComplete).toHaveBeenCalledTimes(1);
});

it("the panel's own turn reloads the automation exactly once", async () => {
  const onConversationComplete = jest.fn();
  // Mirrors the workflow send path: the response lands with the new question already in the
  // thread, then the caller marks the conversation running until the poll sees it finish.
  mockStart.mockImplementation(async ({ onSuccess }, { setConversationStatus, setMessages, setCurrentlyProcessingQuestion }) => {
    server.messages = [question('m1', 'create an automation'), question('m2', 'add a namespace filter')];
    setConversationStatus('COMPLETED');
    setCurrentlyProcessingQuestion(null);
    setMessages(server.messages);
    onSuccess('session');
    return true;
  });
  renderSidebar({ onConversationComplete });
  await loaded();

  fireEvent.change(screen.getByRole('textbox'), { target: { value: 'add a namespace filter' } });
  fireEvent.click(screen.getByRole('button', { name: 'Send', exact: true }));

  await waitFor(() => expect(onConversationComplete).toHaveBeenCalledTimes(1), { timeout: 3000 });
  await waitFor(() => expect(screen.getByRole('textbox')).not.toBeDisabled());
  returnToTab();
  await waitFor(() => expect(pollReads().length).toBeGreaterThan(1));
  expect(onConversationComplete).toHaveBeenCalledTimes(1);
});

it('does not read the conversation for a hidden tab or a new chat', async () => {
  renderSidebar({ sessionId: '' });
  returnToTab();
  expect(pollReads()).toHaveLength(0);

  const visibility = jest.spyOn(document, 'visibilityState', 'get').mockReturnValue('hidden');
  try {
    renderSidebar();
    await waitFor(() => expect(mockFetchConversation).toHaveBeenCalledWith('session', '', 'selected', false));
    act(() => {
      document.dispatchEvent(new Event('visibilitychange'));
    });
    expect(pollReads()).toHaveLength(0);
  } finally {
    visibility.mockRestore();
  }
});
