import { ArrowRightYellowIcon, ChatRoundedIcon, ListIcon, SaveIconOutlinelight, SaveIconOutlineselect, ShareIconBlue, UsersIcon } from '@assets';
import { useTenantBranding, getNubiIconUrl } from '@hooks/useTenantBranding';
import { useBrowserNotification } from '@hooks/useBrowserNotification';
import NotifyBanner from '@components1/common/NotifyBanner';
import { Text } from '@components1/common';
import ClusterDropDown from '@components1/common/ClusterDropDown';
import ConversationLoader from '@components1/common/ConversationLoader';
import CustomTooltip from '@components1/common/CustomTooltip';
import AskNudgebeeLayout from '@components1/common/layout/AskNudgebeeLayoutV2';
import CustomButton from '@components1/common/NewCustomButton';
import { snackbar } from '@components1/common/snackbarService';
import SettingsModal from '@components1/llm/SettingsModal';
import { useData } from '@context/DataContext';
import { useAgentConfiguration } from '@hooks/useAgentConfiguration';
import { useClusterInsights } from '@hooks/useClusterInsights';
import { useConversationManager } from '@hooks/useConversationManager';
import { useConversationSuggestions } from '@hooks/useConversationSuggestions';
import { useLLMInvestigationControl } from '@hooks/useLLMInvestigationControl';
import { useTokenUsage } from '@hooks/useTokenUsage';
import { getUserSession, hasReadAccess } from '@lib/auth';
import { applyFiltersOnRouter } from '@lib/router';
import { Avatar, Box, CircularProgress, Divider, Typography } from '@mui/material';
import { useRouter } from 'next/router';
import PropTypes from 'prop-types';
import { useEffect, useRef, useReducer, useMemo, useCallback } from 'react';
import { colors } from 'src/utils/colors';
import AutoSuggestTextarea from '@components1/k8s/common/TextAreaV2';
import { SummaryBlock } from '@components1/k8s/KubernetesClusterSummary';
import ConversationShimmer from './common/ConversationShimmer';
import { ConversationTokenUsage } from './common/TokenUsageDisplay';
import ConversationList from './ConversationListV2';
import DynamicGreeting from './DynamicGreeting';
import JumpToLatestPill from './common/JumpToLatestPill';
import FollowupSheet from './common/FollowupSheet';
import MessageStream from './MessageStream';
import TroubleshootList from './TroubleshootList';
import SafeIcon from '@components1/common/SafeIcon';

// --- Component-level state reducer ---

function componentReducer(state, action) {
  switch (action.type) {
    case 'SET_FIELD':
      return {
        ...state,
        [action.field]: typeof action.payload === 'function' ? action.payload(state[action.field]) : action.payload,
      };
    case 'NEW_CHAT':
      return {
        ...state,
        selectedSessionId: '',
        selectedConversationId: '',
        generateQuestionText: '',
        collapsedObj: {},
        isTokenDataFetched: false,
        isFetchingTokenData: false,
      };
    case 'SELECT_CONVERSATION':
      return {
        ...state,
        selectedSessionId: action.sessionId,
        selectedConversationId: '',
        collapsedObj: {},
        isTokenDataFetched: false,
        isFetchingTokenData: false,
      };
    case 'CLEAR_CONVERSATION':
      // Both session and conversation IDs cleared (e.g. browser back to root)
      return {
        ...state,
        selectedSessionId: '',
        selectedConversationId: '',
        collapsedObj: {},
      };
    default:
      return state;
  }
}

const getInitials = (name) => {
  if (!name?.trim()) {
    return '?';
  }
  const parts = name.trim().split(/\s+/);
  const first = parts[0].charAt(0).toUpperCase();
  const last = parts.length > 1 ? parts[parts.length - 1].charAt(0).toUpperCase() : '';
  return first + last;
};

const KubernetesLLMResponseGenerator = ({
  accountId,
  query = '',
  queryPrefix = '',
  querySuffix = '',
  popup = false,
  source = 'ask_nudgbee_chat',
  categorySource = '',
  sessionId = '',
  conversationId = '',
  showBorder = false,
  apiMode = 'investigate', // 'investigate' | 'workflow'
  workflowId = '',
  workflowDefinition = null,
  onWorkflowGenerated = undefined, // callback(workflowJson) when workflow build completes
}) => {
  const router = useRouter();
  const { assistantName, baseTitle } = useTenantBranding();

  const previousAccountIdRef = useRef(accountId);
  // Separate refs for separate IDs
  const previousSessionIdRef = useRef(router.query.session_id);
  const previousConversationIdRef = useRef(router.query.conversation_id);
  const previousConversationStatusRef = useRef('');

  const textareaRef = useRef(null);
  const bottomRef = useRef(null);
  const scrollContainerRef = useRef(null);
  const processedSessionIds = useRef(new Set());
  const prevSessionConvIdRef = useRef({ sessionId, conversationId });
  const isAlertOpen = useRef(false);
  const currentSessionRef = useRef('');

  // Clear processed IDs only when session/conversation actually changes.
  // Comparing against a ref (instead of clearing on every effect setup) avoids
  // wiping the dedup set during React StrictMode's simulated remount, which
  // would otherwise let runAutoCheck fire handleGenerateInvestigation twice
  // and produce duplicate optimistic question messages.
  useEffect(() => {
    if (prevSessionConvIdRef.current.sessionId !== sessionId || prevSessionConvIdRef.current.conversationId !== conversationId) {
      processedSessionIds.current.clear();
      prevSessionConvIdRef.current = { sessionId, conversationId };
    }
  }, [sessionId, conversationId]);

  // Check if we are in a chat screen context based on existence of EITHER ID
  const isChatScreen = useMemo(
    () => Boolean(router.query.session_id || router.query.conversation_id),
    [router.query.session_id, router.query.conversation_id]
  );
  const { selectedCluster, setSelectedCluster } = useData();

  const [uiState, uiDispatch] = useReducer(componentReducer, null, () => ({
    generateQuestionText: queryPrefix || '',
    isConversationListVisible: false,
    collapsedObj: {},
    openSettingsModal: false,
    showFullText: false,
    // Distinct State for Session ID and Conversation ID
    selectedSessionId: router.query.session_id || sessionId || '',
    selectedConversationId: router.query.conversation_id || conversationId || '',
    isTokenDataFetched: false,
    isFetchingTokenData: false,
  }));

  // Auto-open conversation list when navigating with ?status=WAITING
  useEffect(() => {
    if (!router.isReady) {
      return;
    }
    if (router.query.status === 'WAITING') {
      uiDispatch({ type: 'SET_FIELD', field: 'isConversationListVisible', payload: true });
    }
  }, [router.isReady, router.query.status]);

  const {
    generateQuestionText,
    isConversationListVisible,
    collapsedObj,
    openSettingsModal,
    showFullText,
    selectedSessionId,
    selectedConversationId,
    isTokenDataFetched,
    isFetchingTokenData,
  } = uiState;

  // Stable field setters
  const setGenerateQuestionText = useCallback((payload) => uiDispatch({ type: 'SET_FIELD', field: 'generateQuestionText', payload }), []);
  const setIsConversationListVisible = useCallback((payload) => uiDispatch({ type: 'SET_FIELD', field: 'isConversationListVisible', payload }), []);
  const setCollapsedObj = useCallback((payload) => uiDispatch({ type: 'SET_FIELD', field: 'collapsedObj', payload }), []);
  const setOpenSettingsModal = useCallback((payload) => uiDispatch({ type: 'SET_FIELD', field: 'openSettingsModal', payload }), []);
  const setShowFullText = useCallback((payload) => uiDispatch({ type: 'SET_FIELD', field: 'showFullText', payload }), []);
  const setSelectedSessionId = useCallback((payload) => uiDispatch({ type: 'SET_FIELD', field: 'selectedSessionId', payload }), []);
  const setSelectedConversationId = useCallback((payload) => uiDispatch({ type: 'SET_FIELD', field: 'selectedConversationId', payload }), []);
  const setIsTokenDataFetched = useCallback((payload) => uiDispatch({ type: 'SET_FIELD', field: 'isTokenDataFetched', payload }), []);
  const setIsFetchingTokenData = useCallback((payload) => uiDispatch({ type: 'SET_FIELD', field: 'isFetchingTokenData', payload }), []);

  const isNewChat = useMemo(() => !selectedSessionId && !selectedConversationId, [selectedSessionId, selectedConversationId]);
  const { troubleShootData, optimizationData } = useClusterInsights(accountId);
  const {
    rawConversations,
    likedConversations,
    selectedConversation,
    activeFilter,
    savingStates,
    handleLike,
    setLikedConversations,
    setRawConversations,
    setSelectedConversation,
  } = useConversationManager();
  const { allAgents, enabledAgents, loadingAgents, allFunctions, refreshAgents } = useAgentConfiguration(accountId);
  const { tokenUsageData, messageTokenData, fetchTokenUsage, resetTokenMetrics, getAgentTokenDataForMessage } = useTokenUsage(accountId);
  const { suggestions: conversationSuggestions, fetchSuggestions, clearSuggestions } = useConversationSuggestions(accountId);
  const {
    allowStop,
    setAllowStop,
    stopInvestigation,
    startInvestigation,
    currentlyProcessingQuestion,
    setCurrentlyProcessingQuestion,
    resetInvestigationState,
    fetchConversation,
    isLoading: isConversationLoading,
    messages,
    setMessages,
    conversationStatus,
    setConversationStatus,
    conversationTitle,
    conversationIdAtDb,
    checkConversationExists,
    availableModels,
    defaultModel,
    selectedModel,
    setSelectedModel,
  } = useLLMInvestigationControl(accountId);

  const isConversationInProgress = useMemo(
    () => conversationStatus === 'IN_PROGRESS' || !!currentlyProcessingQuestion,
    [conversationStatus, currentlyProcessingQuestion]
  );

  // Derive the latest follow-up that's waiting for the user — this is what the bottom-anchored
  // FollowupSheet renders. The followup message itself is saved as IN_PROGRESS while it sits
  // waiting on the user; only COMPLETED/TERMINATED/KILLED/FAILED are terminal and disqualify
  // it from being the active prompt. Falls back to nothing when none qualify.
  const TERMINAL_FOLLOWUP_STATUSES = ['COMPLETED', 'TERMINATED', 'KILLED', 'FAILED'];
  const activeWaitingFollowup = useMemo(() => {
    for (let i = messages.length - 1; i >= 0; i--) {
      const m = messages[i];
      const type = m?.tool ?? m?.type;
      if (type !== 'followup-question') {
        continue;
      }
      const status = m?.response?.status;
      if (!TERMINAL_FOLLOWUP_STATUSES.includes(status)) {
        return m;
      }
    }
    return null;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [messages]);

  const activeFollowupKey = activeWaitingFollowup
    ? `${activeWaitingFollowup.response?.message_id || ''}:${activeWaitingFollowup.response?.agent_id || ''}`
    : null;

  const showFollowupSheet = Boolean(activeWaitingFollowup);

  const currentSessionId = router.query.session_id || sessionId;
  const notifyNavigateTo = currentSessionId ? `/ask-nudgebee?accountId=${accountId}&session_id=${currentSessionId}` : '';
  const {
    showBanner: showNotifyBanner,
    handleEnable: handleNotifyEnable,
    handleDismiss: handleNotifyDismiss,
  } = useBrowserNotification({
    isProcessing: Boolean(currentlyProcessingQuestion),
    status: conversationStatus,
    hasResults: messages.length > 0,
    title: currentlyProcessingQuestion || assistantName || baseTitle,
    navigateTo: notifyNavigateTo,
  });

  const handleGenerateInvestigation = useCallback(
    async (text) => {
      if (conversationStatus === 'IN_PROGRESS' || !text) {
        return;
      }

      // Verify user has access to the account before starting investigation
      if (accountId && !hasReadAccess(accountId)) {
        snackbar.error('You do not have permission to perform this action');
        return;
      }

      let processedText = text;
      // Set currentlyProcessingQuestion FIRST - this ensures isConversationInProgress is true immediately
      setCurrentlyProcessingQuestion(processedText);
      setGenerateQuestionText('');
      setConversationStatus('');
      clearSuggestions();

      // Add optimistic question message immediately so user sees their question right away.
      // Skip for workflow mode: handleWorkflowGeneration appends the question itself via
      // buildWorkflowConversationMessages, so an optimistic add here would produce a
      // duplicate question (one with user 'You', one with the real userName) once the
      // workflow API returns. querySuffix alone is not a reliable workflow indicator
      // because follow-up calls in workflow mode don't carry a suffix.
      if (apiMode !== 'workflow') {
        const optimisticQuestionMessage = {
          id: `optimistic-${Date.now()}`,
          text: processedText,
          type: 'question',
          created_at: new Date().toISOString(),
          user: 'You',
          isOptimistic: true,
        };
        setMessages((prev) => [...prev, optimisticQuestionMessage]);
      }

      // Call Hook with both IDs (one or both might be empty)
      await startInvestigation({
        text: processedText,
        queryPrefix,
        querySuffix,
        selectedSessionId,
        selectedConversationId,
        apiMode,
        workflowId,
        workflowDefinition,
        categorySource,
        popup,
        router,
        onSuccess: (llmSessionId) => {
          if (!popup) {
            // Standardize on session_id for new chats, clear conversation_id to avoid ambiguity
            applyFiltersOnRouter(router, { session_id: llmSessionId, conversation_id: null });
          }
          setSelectedSessionId(llmSessionId);
          setSelectedConversationId(''); // Reset conversationId as we have a fresh session
          setGenerateQuestionText('');
          setConversationStatus('IN_PROGRESS');
        },
        onFailure: () => {
          setConversationStatus('FAILED');
          if (!popup) {
            applyFiltersOnRouter(router, { session_id: '', conversation_id: '' });
          }
        },
      });
    },
    [
      accountId,
      conversationStatus,
      queryPrefix,
      querySuffix,
      selectedSessionId,
      selectedConversationId,
      apiMode,
      workflowId,
      workflowDefinition,
      categorySource,
      popup,
      router,
      startInvestigation,
      setConversationStatus,
      clearSuggestions,
    ]
  );

  useEffect(() => {
    if (previousAccountIdRef.current !== accountId) {
      previousAccountIdRef.current = accountId; // Handle Account Switching

      // Full Reset
      uiDispatch({ type: 'NEW_CHAT' });
      resetTokenMetrics();
      clearSuggestions();
      resetInvestigationState();
      previousConversationStatusRef.current = '';

      if (!popup) {
        applyFiltersOnRouter(router, { session_id: '', conversation_id: '' });
      }
      return;
    }

    // Update Ref for Polling safety (Prefer session ID if available, else conversation ID)
    currentSessionRef.current = selectedSessionId || selectedConversationId;

    if (selectedSessionId || selectedConversationId) {
      // Load Existing Chat
      const hasUrlId = router.query.session_id || router.query.conversation_id;

      if (!popup && !currentlyProcessingQuestion && hasUrlId) {
        // Verify user has access to the account before loading conversation
        if (accountId && !hasReadAccess(accountId)) {
          snackbar.error('You do not have permission to access this conversation');
          uiDispatch({ type: 'CLEAR_CONVERSATION' });
          applyFiltersOnRouter(router, { session_id: '', conversation_id: '' });
          return;
        }
        setGenerateQuestionText('');
        // Pass both IDs to fetchConversation
        fetchConversation(selectedSessionId, selectedConversationId, 'selected', false);
      }
    } else {
      // New Chat Setup
      setMessages([]);
      if (textareaRef.current) {
        textareaRef.current.focus();
      }
    }
  }, [
    accountId,
    router,
    popup,
    currentlyProcessingQuestion,
    selectedSessionId,
    selectedConversationId,
    router.query.session_id,
    router.query.conversation_id,
  ]);

  // Load conversation history in popup mode when sessionId or conversationId
  // is provided via props (e.g., from workflow AI generation where the
  // conversation already exists in the DB, or from a route that links to a
  // specific historical conversation via ?conversation_id=... — see #29511).
  // Skip when queryPrefix is provided — the session will be created on first
  // query submission. sessionId wins when both are present, since the chat
  // tracks live state by session id.
  useEffect(() => {
    if (!popup || currentlyProcessingQuestion) return;
    if (sessionId) {
      if (sessionId !== selectedSessionId) {
        setSelectedSessionId(sessionId);
      }
      fetchConversation(sessionId, '', 'selected', false);
    } else if (conversationId) {
      if (conversationId !== selectedConversationId) {
        setSelectedConversationId(conversationId);
      }
      fetchConversation('', conversationId, 'selected', false);
    }
  }, [popup, sessionId, conversationId, queryPrefix]);

  // Sync selectedConversation when conversationIdAtDb is populated by fetchConversation
  // This ensures Save/Like actions work when loading a conversation via URL redirect,
  // where selectedConversation is not set by ConversationListV2.
  useEffect(() => {
    if (conversationIdAtDb && !selectedConversation?.id) {
      setSelectedConversation({ id: conversationIdAtDb, sessionId: selectedSessionId });
    }
  }, [conversationIdAtDb]);

  // Handle browser back/forward navigation
  useEffect(() => {
    // Session ID Logic
    const prevSessionId = previousSessionIdRef.current;
    const currentSessionId = router.query.session_id;
    previousSessionIdRef.current = currentSessionId;

    // Conversation ID Logic
    const prevConversationId = previousConversationIdRef.current;
    const currentConversationId = router.query.conversation_id;
    previousConversationIdRef.current = currentConversationId;

    if (!popup) {
      // 1. Session ID Changed
      if (prevSessionId !== currentSessionId) {
        if (!currentSessionId) {
          // If session removed and no conversation ID, clear everything
          if (!currentConversationId) {
            // Both IDs gone — full reset to home
            uiDispatch({ type: 'CLEAR_CONVERSATION' });
            setMessages([]);
            clearSuggestions();
            setConversationStatus('');
            resetInvestigationState();
            previousConversationStatusRef.current = '';
          } else {
            setSelectedSessionId(''); // Just clear session, fallback to conversation if present
          }
        } else {
          setSelectedSessionId(currentSessionId);
          // If we switch to a session, we might want to ensure conversationId matches or is cleared,
          // but strict separate handling implies we just set what we have.
          if (prevSessionId) {
            // Navigating between sessions
            setMessages([]);
            clearSuggestions();
            setCollapsedObj({});
            setConversationStatus('');
            previousConversationStatusRef.current = '';
          }
        }
      }

      // 2. Conversation ID Changed
      if (prevConversationId !== currentConversationId) {
        if (!currentConversationId) {
          if (!currentSessionId) {
            uiDispatch({ type: 'CLEAR_CONVERSATION' });
            setMessages([]);
            clearSuggestions();
            setConversationStatus('');
            resetInvestigationState();
            previousConversationStatusRef.current = '';
          } else {
            setSelectedConversationId('');
          }
        } else {
          setSelectedConversationId(currentConversationId);
          if (prevConversationId) {
            setMessages([]);
            clearSuggestions();
            setCollapsedObj({});
            setConversationStatus('');
            previousConversationStatusRef.current = '';
          }
        }
      }
    }
  }, [router.query.session_id, router.query.conversation_id]);

  useEffect(() => {
    if (queryPrefix) {
      if (messages.length === 0) {
        setGenerateQuestionText(queryPrefix);
      } else {
        setGenerateQuestionText('');
      }
    }
  }, [queryPrefix, messages.length]);

  useEffect(() => {
    if (messages.length > 0) {
      const timeoutId = setTimeout(() => {
        if (messages[messages.length - 1]?.type === 'response') {
          bottomRef.current?.scrollIntoView({ behavior: 'smooth' });
        }
      }, 1000);
      return () => {
        clearTimeout(timeoutId);
      };
    }
  }, [messages]);

  // Auto-scroll to bottom on chat open: when a session/conversation loads its messages for
  // the first time, jump straight to the latest message — including in-progress conversations
  // where the last message is a task (which the response-only effect above doesn't cover).
  const autoScrolledSessionRef = useRef(null);
  useEffect(() => {
    if (messages.length === 0) {
      return;
    }
    const currentSessionKey = selectedSessionId || selectedConversationId || '';
    if (!currentSessionKey || autoScrolledSessionRef.current === currentSessionKey) {
      return;
    }
    autoScrolledSessionRef.current = currentSessionKey;
    requestAnimationFrame(() => {
      const el = scrollContainerRef.current;
      if (el && el.scrollHeight > el.clientHeight + 4) {
        el.scrollTo({ top: el.scrollHeight, behavior: 'auto' });
      } else if (typeof window !== 'undefined') {
        window.scrollTo({ top: document.documentElement.scrollHeight, behavior: 'auto' });
      }
    });
  }, [messages.length, selectedSessionId, selectedConversationId]);

  useEffect(() => {
    const runAutoCheck = async () => {
      // Check based on Session ID first, then Conversation ID
      const targetId = sessionId || conversationId;

      if (query && popup && generateQuestionText !== query && targetId && !processedSessionIds.current.has(targetId)) {
        processedSessionIds.current.add(targetId);
        const result = await checkConversationExists(targetId);
        if (result.error) {
          snackbar.error('Failed to load Conversation');
          setConversationStatus('FAILED');
          if (!popup) {
            applyFiltersOnRouter(router, { session_id: '', conversation_id: '' });
          }
          return;
        }
        if (!result.exists) {
          handleGenerateInvestigation(query);
        }
      }
    };

    runAutoCheck();

    const shouldStopPolling = ['COMPLETED', 'FAILED', 'KILLED', 'TERMINATED'].includes(conversationStatus);
    if (shouldStopPolling) {
      setAllowStop(false);

      // Fetch suggestions when status is terminal and we haven't fetched for this status yet
      const hasStatusChanged = previousConversationStatusRef.current !== conversationStatus;

      if (messages && messages.length > 0 && conversationStatus !== 'TERMINATED') {
        const lastMessageId = messages[messages.length - 1].id;
        if (conversationIdAtDb && lastMessageId && hasStatusChanged) {
          fetchSuggestions(conversationIdAtDb, lastMessageId);
          // Only update ref after successful fetch attempt
          previousConversationStatusRef.current = conversationStatus;
        }
      }
      // If no messages yet, don't update ref so we try again when messages load
      return;
    }
    // Update ref for non-terminal states
    previousConversationStatusRef.current = conversationStatus;
    if (source === 'prompt_test') {
      setAllowStop(false);
    } else {
      setAllowStop(true);
    }

    if ((selectedSessionId !== '' || selectedConversationId !== '') && conversationStatus && !isNewChat) {
      const id = setInterval(async () => {
        // Pass both to poll
        await fetchConversation(selectedSessionId, selectedConversationId, 'poll', false);
      }, 5000);

      return () => clearInterval(id);
    }
  }, [
    popup,
    query,
    sessionId,
    conversationId,
    generateQuestionText,
    conversationStatus,
    selectedSessionId,
    selectedConversationId,
    isNewChat,
    messages,
    conversationIdAtDb,
    source,
    accountId,
    router,
    setAllowStop,
    fetchSuggestions,
    fetchConversation,
    handleGenerateInvestigation,
    setConversationStatus,
    checkConversationExists,
  ]);

  useEffect(() => {
    if (conversationStatus === 'IN_PROGRESS') {
      const timeout = setTimeout(() => {
        console.warn('Investigation loading timeout - forcing reset after 5 minutes');
        setConversationStatus('TIMEOUT');
      }, 600000);

      return () => {
        clearTimeout(timeout);
      };
    }
  }, [conversationStatus, setConversationStatus]);

  // Auto-apply workflow to canvas when workflow generation completes via NubiChat sidebar
  const workflowAppliedRef = useRef(false);
  useEffect(() => {
    if (apiMode === 'workflow' && conversationStatus === 'COMPLETED' && onWorkflowGenerated && messages?.length > 0 && !workflowAppliedRef.current) {
      // Find the last message that contains a workflow JSON (look for definition/tasks keys)
      for (let i = messages.length - 1; i >= 0; i--) {
        const msg = messages[i];
        const text = msg?.text || msg?.response || '';
        if (!text || typeof text !== 'string') {
          continue;
        }

        // Try to extract JSON from the message text
        // Look for JSON that starts with { and contains "definition" or "tasks"
        const jsonMatch = text.match(/\{[\s\S]*"(?:definition|tasks)"[\s\S]*\}/);
        if (jsonMatch) {
          try {
            // Find the first complete JSON object
            let depth = 0;
            let start = text.indexOf('{');
            if (start === -1) {
              continue;
            }
            for (let j = start; j < text.length; j++) {
              if (text[j] === '{') {
                depth++;
              }
              if (text[j] === '}') {
                depth--;
              }
              if (depth === 0) {
                const jsonStr = text.substring(start, j + 1);
                const parsed = JSON.parse(jsonStr);
                if (parsed && (parsed.definition || parsed.tasks)) {
                  workflowAppliedRef.current = true;
                  onWorkflowGenerated(jsonStr, selectedSessionId);
                  return;
                }
              }
            }
          } catch {
            // JSON parse failed, continue searching
          }
        }
      }
    }
    // Reset the ref when a new conversation starts
    if (conversationStatus === 'IN_PROGRESS') {
      workflowAppliedRef.current = false;
    }
  }, [conversationStatus, messages, apiMode, onWorkflowGenerated, selectedSessionId]);

  const handleDropdownChange = useCallback(
    (e) => {
      let cloud_provider = e.cloud_provider.toLowerCase();
      if (router.pathname.indexOf('/cloud-account/details/') > -1 && cloud_provider === 'k8s') {
        router.push(`/kubernetes/details/${e.value}`);
        return;
      } else if (
        e.value &&
        router.pathname.indexOf('/kubernetes/details/') > -1 &&
        (cloud_provider === 'aws' || cloud_provider === 'gcp' || cloud_provider === 'azure')
      ) {
        router.push(`/cloud-account/details/${e.value}`);
        return;
      }
      setSelectedCluster(e);
    },
    [router, setSelectedCluster]
  );

  const handleClusterData = useCallback(
    (clusterOption) => {
      if (clusterOption.length === 0 && !isAlertOpen.current) {
        isAlertOpen.current = true;
        snackbar.error('Currently No kubernetes cluster is configured, Please add a kubernetes cluster');
        router.push('/accounts/account-form?cloudProvider=K8S');
      }
    },
    [router]
  );

  const uniqueParticipantNames = useMemo(() => {
    const names = new Set();
    messages.forEach((msg) => {
      if (msg?.user && typeof msg?.user === 'string' && msg.user?.trim() !== '') {
        names.add(msg.user);
      }
    });
    return names;
  }, [messages]);

  const participantCount = useMemo(() => uniqueParticipantNames.size, [uniqueParticipantNames]);

  const queryCount = useMemo(() => messages?.filter((message) => message.type === 'question' || message.tool === 'question').length ?? 0, [messages]);

  const handleShare = useCallback(() => {
    // Determine share ID
    const shareId = selectedSessionId || selectedConversationId;
    const shareParam = selectedSessionId ? 'session_id' : 'conversation_id';

    navigator.clipboard.writeText(window.location.origin + window.location.pathname + `?accountId=${accountId}&${shareParam}=${shareId}`);
    snackbar.success('Link copied to clipboard');
  }, [selectedSessionId, selectedConversationId, accountId]);

  const handleToggle = useCallback(() => {
    setIsConversationListVisible((prevState) => !prevState);
  }, []);

  const handleNewChat = useCallback(() => {
    uiDispatch({ type: 'NEW_CHAT' });
    setMessages([]);
    setConversationStatus('');
    setCurrentlyProcessingQuestion(null);
    clearSuggestions();
    resetInvestigationState();
    previousConversationStatusRef.current = '';
    if (!popup) {
      applyFiltersOnRouter(router, { session_id: '', conversation_id: '' });
    }
    setTimeout(() => {
      textareaRef.current?.focus();
    }, 0);
  }, [popup, router, setMessages, setConversationStatus, setCurrentlyProcessingQuestion, clearSuggestions, resetInvestigationState]);

  const ConversationHeaderData = useMemo(
    () => ({
      title: conversationTitle || messages[0]?.text,
      queriesAsked: queryCount,
      participants: participantCount,
    }),
    [conversationTitle, messages, queryCount, participantCount]
  );

  const handleSelectConversation = useCallback(
    (index) => {
      if (index !== selectedSessionId) {
        uiDispatch({ type: 'SELECT_CONVERSATION', sessionId: index });
        setConversationStatus('');
        setCurrentlyProcessingQuestion(null);
        if (!popup) {
          applyFiltersOnRouter(router, { session_id: index, conversation_id: null });
        }
        setMessages([]);
        clearSuggestions();
        resetTokenMetrics();
        previousConversationStatusRef.current = '';
      }
    },
    [selectedSessionId, popup, router, setMessages, setConversationStatus, setCurrentlyProcessingQuestion, clearSuggestions, resetTokenMetrics]
  );

  const handleStopInvestigation = useCallback(() => {
    stopInvestigation(conversationIdAtDb, conversationStatus, () => {
      setConversationStatus('TERMINATING');
      clearSuggestions();
    });
  }, [conversationIdAtDb, conversationStatus, stopInvestigation, setConversationStatus, clearSuggestions]);

  const handleTokenUsageHover = useCallback(async () => {
    // Only fetch if we haven't already fetched and we're not currently fetching
    if (!isTokenDataFetched && !isFetchingTokenData && selectedSessionId) {
      setIsFetchingTokenData(true);
      try {
        await fetchTokenUsage(selectedSessionId);
        setIsTokenDataFetched(true);
      } catch (error) {
        console.error('Failed to fetch token usage:', error);
      } finally {
        setIsFetchingTokenData(false);
      }
    }
  }, [isTokenDataFetched, isFetchingTokenData, selectedSessionId, fetchTokenUsage]);

  const clusterDropdownContent = (
    <ClusterDropDown
      showStatusIndicator={true}
      headerStyle={true}
      showIndicator={true}
      rounded={'0px'}
      onChange={handleDropdownChange}
      noLabel
      onClusterDataLoaded={handleClusterData}
      clusterData={selectedCluster}
      minWidth={'224px'}
      groupByCloudProvider
      showPadding={true}
      customStyle={{ height: '28px !important' }}
      showSmallTopPadding={false}
    />
  );

  const content = (
    <>
      <SettingsModal
        open={openSettingsModal}
        onClose={() => setOpenSettingsModal(false)}
        accountId={accountId}
        allAgents={allAgents}
        refreshAgentListing={refreshAgents}
        loadingAgents={loadingAgents}
      />
      <Box
        sx={{
          display: popup ? 'flex' : 'grid',
          transition: 'grid-template-columns 0.3s ease-in-out',
          gridTemplateColumns: '1fr',
          ...(popup && {
            height: '100%',
            overflow: 'hidden',
          }),
        }}
      >
        <ConversationList
          accountId={accountId}
          onSelectConversation={handleSelectConversation}
          selectedId={selectedSessionId || selectedConversationId}
          isConversationListVisible={isConversationListVisible}
          triggerHandleNewChat={() => handleNewChat()}
          handleShare={handleShare}
          likedConversations={likedConversations}
          setLikedConversations={setLikedConversations}
          savingStates={savingStates}
          handleLike={(id, starred) => handleLike(id, starred, selectedSessionId || selectedConversationId)}
          activeFilter={activeFilter}
          setSelectedConversation={setSelectedConversation}
          rawConversations={rawConversations}
          setRawConversations={setRawConversations}
          onCollapseConversationList={() => setIsConversationListVisible(false)}
        />

        <Box
          sx={{
            position: 'relative',
            width: '100%',
            ...(popup && {
              display: 'flex',
              flexDirection: 'column',
              height: '100%',
              overflow: 'hidden',
              boxSizing: 'border-box',
            }),
          }}
        >
          {!isChatScreen && !popup && (
            <Box
              sx={{
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'space-between',
                zIndex: 30,
                position: 'sticky',
                top: '0px',
                padding: '10px 24px',
              }}
            >
              <Box>
                <Box sx={{ display: 'flex', alignItems: 'center', height: '100%', gap: '8px' }}>
                  <SafeIcon src={getNubiIconUrl()} alt={assistantName} width={40} height={40} />
                  <Typography sx={{ fontSize: '26px', fontFamily: 'Roboto', fontWeight: 600, color: colors.text.secondary }}>
                    {assistantName}
                  </Typography>
                </Box>
              </Box>
              <Box
                sx={{
                  display: 'flex',
                  alignItems: 'center',
                  border: `1px solid ${colors.border.secondary}`,
                  borderRadius: '4px',
                  position: 'relative',
                  '&:hover': {
                    borderColor: colors.border.primary,
                  },
                  '.MuiAutocomplete-root': {
                    minHeight: '32px !important',
                  },
                  '& .MuiFormControl-root': {
                    margin: '0px !important',
                    fontSize: '16px !important',
                  },
                  '& .MuiOutlinedInput-notchedOutline': {
                    border: showBorder ? 'inherit' : 'none !important',
                  },
                  '& .MuiInputBase-sizeSmall': {
                    padding: '4px 39px 6px 0px !important',
                  },
                  '& li': {
                    paddingLeft: '0px !important',
                  },
                }}
              >
                {clusterDropdownContent}
              </Box>
            </Box>
          )}
          {isChatScreen && !popup && (
            <Box
              sx={{
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'space-between',
                height: '52px',
                position: 'fixed',
                zIndex: 20,
                top: 0,
                right: 0,
                left: '76px',
                pr: '24px',
                backgroundColor: colors.background.white,
              }}
            >
              <Box sx={{ display: 'flex', flexDirection: 'column', width: '100%', position: 'sticky', top: '0px' }}>
                <Box sx={{ padding: '10px 24px' }}>
                  <Box sx={{ display: 'flex', alignItems: 'center', height: '100%', gap: '8px' }}>
                    <SafeIcon src={getNubiIconUrl()} alt={assistantName} width={40} height={40} />
                    <Typography sx={{ fontSize: '26px', fontFamily: 'Roboto', fontWeight: 600, color: colors.text.secondary }}>
                      {assistantName}
                    </Typography>
                  </Box>
                </Box>
              </Box>

              <Box sx={{ display: 'inline-flex', alignItems: 'center' }}>
                <Box sx={{ display: 'flex', alignItems: 'center', gap: '20px' }}>
                  {ConversationHeaderData.queriesAsked > 0 && (
                    <CustomTooltip
                      placement='bottom'
                      title='Queries Asked'
                      tooltipStyle={{
                        backgroundColor: 'white',
                        color: colors.text.secondary,
                        padding: '8px 12px',
                        fontSize: '12px',
                        borderRadius: '4px',
                        boxShadow: '0 2px 4px rgba(0,0,0,0.1)',
                      }}
                    >
                      <Box sx={{ display: 'flex', alignItems: 'center', gap: '6px' }}>
                        <SafeIcon src={ChatRoundedIcon} alt='chat icon' width={16} height={16} />
                        <Typography
                          sx={{
                            fontSize: '12px',
                            fontWeight: 400,
                            color: colors.text.secondaryDark,
                            fontFamily: 'Roboto',
                            span: {
                              color: colors.text.secondary,
                              fontWeight: 500,
                              mr: '6px',
                              mt: '2px',
                            },
                          }}
                        >
                          <span>{ConversationHeaderData.queriesAsked}</span>
                        </Typography>
                      </Box>
                    </CustomTooltip>
                  )}
                  {ConversationHeaderData.participants > 0 && (
                    <CustomTooltip
                      tooltipStyle={{
                        backgroundColor: colors.background.white,
                        color: colors.text.secondary,
                        boxShadow: '0 2px 10px rgba(0,0,0,0.1)',
                        padding: 0,
                        border: '1px solid rgba(0,0,0,0.08)',
                        borderRadius: '8px',
                      }}
                      title={
                        <Box
                          sx={{
                            display: 'flex',
                            flexDirection: 'column',
                            gap: '12px',
                            padding: '16px',
                          }}
                        >
                          <Typography
                            sx={{
                              fontSize: '14px',
                              fontWeight: 600,
                              color: colors.text.secondary,
                              borderBottom: `1px solid ${colors.background.input}`,
                              paddingBottom: '8px',
                            }}
                          >
                            Participants
                          </Typography>
                          <Box sx={{ display: 'flex', flexDirection: 'column', flexWrap: 'wrap', gap: '10px' }}>
                            {Array.from(uniqueParticipantNames).map((name, index) => (
                              <Box
                                key={index}
                                sx={{
                                  display: 'flex',
                                  alignItems: 'center',
                                  gap: '8px',
                                  backgroundColor: colors.background.suggestionCardBG,
                                  borderRadius: '16px',
                                  padding: '4px 12px 4px 4px',
                                  transition: 'all 0.2s ease',
                                  '&:hover': {
                                    backgroundColor: colors.background.suggestionCardHover,
                                    transform: 'translateY(-1px)',
                                  },
                                }}
                              >
                                <Avatar
                                  sx={{
                                    bgcolor: colors.text.greyDark,
                                    height: '28px',
                                    width: '28px',
                                    fontSize: '14px',
                                    fontWeight: 400,
                                    fontFamily: 'Roboto',
                                    cursor: 'pointer',
                                  }}
                                >
                                  {getInitials(name)}
                                </Avatar>
                                <Typography
                                  sx={{
                                    fontSize: '13px',
                                    fontWeight: 500,
                                    color: colors.text.secondary,
                                  }}
                                >
                                  {name}
                                </Typography>
                              </Box>
                            ))}
                          </Box>
                        </Box>
                      }
                    >
                      <Box sx={{ display: 'flex', alignItems: 'center', gap: '6px' }}>
                        <SafeIcon src={UsersIcon} alt='users icon' width={16} height={16} />
                        <Typography
                          sx={{
                            fontSize: '12px',
                            fontWeight: 400,
                            color: colors.text.secondaryDark,
                            fontFamily: 'Roboto',
                            span: {
                              color: colors.text.secondary,
                              fontWeight: 500,
                              mr: '6px',
                            },
                          }}
                        >
                          <span>{ConversationHeaderData.participants}</span>
                        </Typography>
                      </Box>
                    </CustomTooltip>
                  )}
                  <Box onMouseEnter={handleTokenUsageHover}>
                    <ConversationTokenUsage tokenUsageData={tokenUsageData} isLoading={isFetchingTokenData} />
                  </Box>
                </Box>
                <Divider orientation='vertical' variant='middle' flexItem sx={{ height: '28px', mx: '10px' }} />
                <Box sx={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
                  {selectedConversation?.id && (
                    <CustomButton
                      variant='secondary'
                      sx={{
                        height: '32px',
                        width: '32px',
                        '& img': {
                          filter: likedConversations.includes(selectedSessionId || selectedConversationId)
                            ? 'brightness(0) saturate(100%) invert(45%) sepia(76%) saturate(521%) hue-rotate(179deg) brightness(93%) contrast(108%)'
                            : 'none',
                        },
                      }}
                      startIcon={
                        savingStates[selectedConversation?.id] ? (
                          <CircularProgress size={20} />
                        ) : (
                          <SafeIcon
                            src={likedConversations.includes(selectedConversation?.id) ? SaveIconOutlineselect : SaveIconOutlinelight}
                            width={'24px'}
                            height={'24px'}
                            alt='save'
                          />
                        )
                      }
                      onClick={(e) => {
                        e.stopPropagation();
                        handleLike(selectedConversation?.id, likedConversations.includes(selectedConversation?.id));
                      }}
                      disabled={savingStates[selectedConversation?.id]}
                    />
                  )}
                  <CustomButton
                    startIcon={<SafeIcon src={ShareIconBlue} height={18} width={18} alt={'Share'} />}
                    variant='tertiary'
                    showTooltip
                    sx={{ height: '32px', padding: '0px 6px !important' }}
                    onClick={handleShare}
                  />
                </Box>
                <Divider orientation='vertical' variant='middle' flexItem sx={{ height: '28px', mx: '10px' }} />
                <Box
                  sx={{
                    display: 'flex',
                    alignItems: 'center',
                    border: `1px solid ${colors.border.secondary}`,
                    borderRadius: '4px',
                    height: '32px !important',
                    position: 'relative',
                    '&:hover': {
                      borderColor: colors.border.primary,
                    },
                    '.MuiAutocomplete-root': {
                      minHeight: '32px !important',
                    },
                    '& .MuiAutocomplete-input': {
                      paddingTop: '0px !important',
                    },
                    '& .MuiFormControl-root': {
                      margin: '0px !important',
                      fontSize: '16px !important',
                    },
                    '& .MuiOutlinedInput-notchedOutline': {
                      border: showBorder ? 'inherit' : 'none !important',
                    },
                    '& .MuiOutlinedInput-root': {
                      padding: '4px 40px 5px 0px !important',
                    },
                    '& li': {
                      paddingLeft: '0px !important',
                    },
                  }}
                >
                  {clusterDropdownContent}
                </Box>
              </Box>
            </Box>
          )}
          <Box
            ref={scrollContainerRef}
            sx={{
              overflowY: popup ? 'auto' : isChatScreen ? 'auto' : 'hidden',
              overflowX: 'hidden',
              position: 'relative',
              mx: 'auto',
              maxWidth: popup ? '100%' : '60%',
              mb: popup ? '0px' : isChatScreen ? '160px' : '0px',
              px: popup ? '20px' : '0px',
              pb: popup && (selectedSessionId != '' || selectedConversationId != '') ? '150px' : popup ? '10px' : '0px',
              ...(popup && {
                flex: 1,
                height: 0,
                boxSizing: 'border-box',
                display: 'flex',
                flexDirection: 'column',
              }),
              ...(!popup && {
                minHeight: '100vh',
              }),
              transform: isConversationListVisible ? 'translateX(125px)' : 'translateX(0)',
              transition: 'transform 0.4s cubic-bezier(0.4, 0, 0.2, 1)',

              '&::-webkit-scrollbar': {
                display: isConversationInProgress ? 'none' : 'block',
                width: '6px',
              },

              '&::-webkit-scrollbar-track': {
                background: 'transparent',
                marginRight: '4px', // Additional spacing
              },

              '&::-webkit-scrollbar-thumb': {
                background: colors.background.secondaryDark,
                borderRadius: '3px',
              },

              '&::-webkit-scrollbar-thumb:hover': {
                background: colors.text.tertiary,
              },
            }}
          >
            <Box
              sx={{
                display: 'flex',
                position: 'relative',
                flexDirection: 'column',
                justifyContent: selectedSessionId == '' && selectedConversationId == '' && !popup && 'center',
                ...(popup && selectedSessionId == '' && selectedConversationId == '' && { flex: 1 }),
              }}
            >
              <Box
                display={'flex'}
                flexDirection={'column'}
                position={'relative'}
                sx={{
                  mt: selectedSessionId == '' && selectedConversationId == '' && !popup ? '100px' : '0px',
                  ...(popup &&
                    selectedSessionId == '' &&
                    selectedConversationId == '' && {
                      flex: 1,
                      pb: '20px',
                    }),
                  '@media (max-width: 1280px)': {
                    mt: selectedSessionId == '' && selectedConversationId == '' && !popup ? '60px' : '0px',
                  },
                }}
              >
                {selectedSessionId == '' && selectedConversationId == '' && !currentlyProcessingQuestion && (
                  <Box
                    sx={{
                      display: 'flex',
                      flexDirection: 'column',
                      textAlign: 'center',
                      marginX: 'auto',
                      ...(popup && {
                        mb: '12px',
                        mt: '60px',
                      }),
                    }}
                  >
                    <DynamicGreeting
                      userName={getUserSession()?.user?.name || ''}
                      className={`poppins-font animated-box`}
                      style={{ animationDelay: '0.1s', letterSpacing: '-0.6px', fontWeight: '500' }}
                    />
                  </Box>
                )}
                {selectedSessionId == '' && selectedConversationId == '' && !currentlyProcessingQuestion ? (
                  <Box
                    sx={{
                      maxWidth: popup ? '100%' : '725px',
                      mx: 'auto',
                      width: '100%',
                      mb: popup ? '0px' : '12px',
                      mt: popup ? 'auto' : '0px',
                      '@media (max-width: 1300px)': {
                        maxWidth: popup ? '100%' : '490px',
                      },
                    }}
                  >
                    <Box
                      className={popup ? '' : 'animated-box'}
                      style={popup ? {} : { animationDelay: '0.3s' }}
                      sx={{
                        p: popup ? '0px' : '6px',
                        borderRadius: '12px',
                        background: popup
                          ? 'transparent'
                          : 'linear-gradient(to right,rgb(96, 165, 250, 0.2), rgb(96, 165, 250, 0.1), rgb(96, 165, 250, 0.2))',
                        mt: popup ? '0px' : '20px',
                        mb: '12px',
                        mx: 'auto',
                      }}
                    >
                      <SummaryBlock
                        hideTitle
                        sx={{
                          display: 'flex',
                          alignItems: 'flex-end',
                          gap: popup ? '10px' : '30px',
                          backgroundColor: colors.background.white,
                          borderRadius: '12px',
                          border: popup
                            ? `1px solid ${colors.border.primary} !important`
                            : `0.75px solid ${colors.border.conversationCard} !important`,
                          boxShadow: popup
                            ? '0px 2px 7px 0px #3B82F60F,0px 0px 10px -1px #3B82F638'
                            : '0px 2px 7px 0px #3B82F60F,0px 4px 6px -1px #3B82F61F',
                          padding: popup ? '10px 15px' : '16px 20px',
                          '& textarea': {
                            width: '100%',
                            border: '0px',
                            resize: 'none',
                            boxShadow: 'none',
                            backgroundColor: colors.background.white,
                            minHeight: popup ? '24px' : '80px',
                            px: '0px',
                            '&:focus': {
                              boxShadow: 'none',
                            },
                            '&::placeholder': {
                              color: colors.background.lastSync,
                              fontSize: popup ? '14px' : 'inherit',
                              '@media (max-width: 1300px)': {
                                fontSize: '13px',
                              },
                            },
                            '&::-webkit-scrollbar': {
                              display: 'none',
                            },
                          },
                          '& .MuiOutlinedInput-notchedOutline': {
                            border: '0px !important',
                          },
                          '& button': {
                            padding: '0px 10px !important',
                          },
                        }}
                      >
                        <AutoSuggestTextarea
                          functionSuggestions={allFunctions}
                          ref={textareaRef}
                          value={generateQuestionText}
                          fontSize='14px'
                          fontWeight='400'
                          placeholder={popup ? 'Ask a question...' : 'Ask me about troubleshooting, error logs, resource usage, or optimizations.'}
                          maxRows={popup ? 4 : 3}
                          minRows={popup ? 1 : 3}
                          maxLength={500000}
                          suggestionsAt={enabledAgents}
                          showBorderleft={false}
                          disabled={isConversationLoading || isConversationInProgress}
                          models={availableModels}
                          defaultModel={defaultModel}
                          selectedModel={selectedModel}
                          onModelSelect={setSelectedModel}
                          isFollowUp={popup}
                          buttonProperties={{
                            show: true,
                            enable: !isConversationLoading,
                            onClick: (text) => {
                              handleGenerateInvestigation(text);
                            },
                          }}
                        />
                      </SummaryBlock>
                    </Box>
                  </Box>
                ) : null}

                {selectedSessionId == '' && selectedConversationId == '' && !popup && !currentlyProcessingQuestion && (
                  <Box
                    className={`animated-box`}
                    style={{ animationDelay: '0.4s' }}
                    sx={{
                      mx: 'auto',
                      width: '100%',
                      maxWidth: popup ? '95%' : '725px',
                      px: '10px',
                      mt: '10px',
                    }}
                  >
                    <Box sx={{ display: 'flex', flexDirection: 'column', gap: '40px', mb: '20px' }}>
                      <Box>
                        {troubleShootData.length > 0 && (
                          <Text value='Any Errors in the Past 24 Hours?' sx={{ fontWeight: 500, fontFamily: 'Roboto', mb: '12px' }} />
                        )}
                        {troubleShootData.map((data) => (
                          <Box key={data.id}>
                            <TroubleshootList data={data} type='troubleshooting' accountId={accountId} />
                          </Box>
                        ))}
                      </Box>
                      <Box>
                        {optimizationData.length > 0 && (
                          <Text value='What can we Optimize?' sx={{ fontWeight: 500, fontFamily: 'Roboto', mb: '12px' }} />
                        )}
                        {optimizationData.map((data) => (
                          <Box key={data.id}>
                            <TroubleshootList data={data} type='optimization' accountId={accountId} />
                          </Box>
                        ))}
                      </Box>
                    </Box>
                  </Box>
                )}
              </Box>
            </Box>
            {/* Show shimmer when loading existing conversation OR first query on main page (not popup) */}
            {((isConversationLoading && (selectedSessionId || selectedConversationId)) || (isConversationInProgress && messages.length === 0)) &&
              !popup && <ConversationShimmer />}

            {/* Show ConversationLoader for first query in popup/workflow builder only */}
            {isConversationInProgress && messages.length === 0 && popup && (
              <Box sx={{ mt: '10px', width: '100%', minWidth: '400px' }}>
                <ConversationLoader />
              </Box>
            )}

            {/* Show not found message when conversation returns empty */}
            {conversationStatus === 'NOT_FOUND' && !isConversationLoading && messages.length === 0 && !queryPrefix && (
              <Box
                sx={{
                  display: 'flex',
                  flexDirection: 'column',
                  justifyContent: 'center',
                  alignItems: 'center',
                  py: '80px',
                  gap: '12px',
                }}
              >
                <Box
                  sx={{
                    width: '48px',
                    height: '48px',
                    borderRadius: '50%',
                    backgroundColor: colors.background.tertiaryLightest,
                    display: 'flex',
                    alignItems: 'center',
                    justifyContent: 'center',
                  }}
                >
                  <SafeIcon src={ChatRoundedIcon} alt='not found' width={22} height={22} style={{ opacity: 0.5 }} />
                </Box>
                <Typography sx={{ fontSize: '16px', fontWeight: 500, color: colors.text.secondary, fontFamily: 'Roboto' }}>
                  Conversation not found
                </Typography>
                <Typography sx={{ fontSize: '13px', color: colors.text.secondaryDark, fontFamily: 'Roboto' }}>
                  This conversation may have been deleted or is no longer available.
                </Typography>
                <CustomButton variant='primary' text='Start New Conversation' size='Medium' sx={{ mt: '8px' }} onClick={handleNewChat} />
              </Box>
            )}

            <MessageStream
              messages={messages}
              isProcessing={isConversationInProgress}
              collapsedObj={collapsedObj}
              setCollapsedObj={setCollapsedObj}
              showFullText={showFullText}
              setShowFullText={setShowFullText}
              itemProps={{
                accountId,
                generateQuestionText,
                handleShare,
                sessionId: selectedSessionId || selectedConversationId,
                conversationId: conversationIdAtDb,
                getAgentTokenDataForMessage,
                messageTokenData,
                handleTokenUsageHover,
                isFetchingTokenData,
                selectedModel,
                conversationStatus,
                // The bottom-anchored FollowupSheet is the primary interactive surface for the
                // active WAITING followup. The inline copy in the response body shows read-only
                // when the sheet is rendered so we don't have two interactive entry points for
                // the same question.
                followupReadOnlyKey: showFollowupSheet ? activeFollowupKey : null,
              }}
            />

            {/* Conversation Suggestions - Show after last message */}
            {!isConversationInProgress && !popup && conversationSuggestions && conversationSuggestions.length > 0 && messages.length > 0 && (
              <Box sx={{ mb: '24px', mt: '24px' }}>
                <Box sx={{ display: 'flex', alignItems: 'center', mb: '8px' }}>
                  <SafeIcon src={ListIcon} width={24} height={24} alt='list icon' />
                  <Typography sx={{ fontSize: '16px', color: colors.text.secondary, fontWeight: 500, p: '8px' }}>Related Questions</Typography>
                </Box>
                <Box sx={{ display: 'flex', flexWrap: 'wrap' }}>
                  {conversationSuggestions.map((suggestion, idx) => {
                    const suggestionText = typeof suggestion === 'string' ? suggestion : suggestion?.message || '';
                    if (!suggestionText) {
                      return null;
                    }

                    return (
                      <Box
                        key={suggestion.id || idx}
                        sx={{
                          width: '100%',
                          p: '8px 0px',
                          borderBottom: `0.5px solid ${colors.border.nudgebeeSuggestion}`,
                          cursor: 'pointer',
                          display: 'flex',
                          alignItems: 'center',
                          justifyContent: 'space-between',
                          transition: 'all 0.3s ease',
                          animation: `fadeIn 0.6s ease ${idx * 0.15}s both`,
                          '@keyframes fadeIn': {
                            '0%': {
                              opacity: 0,
                              transform: 'translateY(4px)',
                            },
                            '100%': {
                              opacity: 1,
                              transform: 'translateY(0)',
                            },
                          },
                          '&:hover': {
                            backgroundColor: colors.background.ticketDescription,
                          },
                        }}
                        onClick={() => {
                          setGenerateQuestionText(suggestionText);
                          setTimeout(() => {
                            handleGenerateInvestigation(suggestionText);
                          }, 100);
                        }}
                      >
                        <Text
                          value={suggestionText}
                          sx={{
                            fontSize: '13px',
                            color: colors.text.secondary,
                          }}
                        />
                        <SafeIcon src={ArrowRightYellowIcon} alt='arrow right icon' width={28} height={28} style={{ marginLeft: 'auto' }} />
                      </Box>
                    );
                  })}
                </Box>
              </Box>
            )}

            {/* Render the Nubi "thinking" loader (avatar + ripple/pulse + Hive-warming label)
                throughout the streaming run. It mounts below the inline task list and stays
                visible until a `response` arrives for the active group or the conversation
                transitions to a terminal state. */}
            {(() => {
              if (!isConversationInProgress || messages.length === 0) {
                return null;
              }
              if (!(selectedSessionId !== '' || selectedConversationId !== '')) {
                return null;
              }
              const lastMsg = messages[messages.length - 1];
              const lastType = lastMsg?.tool ?? lastMsg?.type;
              // Once the response arrives for the active group, the run is effectively done — hide.
              if (lastType === 'response') {
                return null;
              }
              return (
                <Box mb={popup ? '20px' : '70px'} sx={{ width: '100%', minWidth: popup ? '400px' : 0, boxSizing: 'border-box' }}>
                  <ConversationLoader />
                </Box>
              );
            })()}
          </Box>
          {(selectedSessionId != '' || selectedConversationId != '' || !!currentlyProcessingQuestion) &&
          (conversationStatus !== 'NOT_FOUND' || queryPrefix) ? (
            <Box
              sx={{
                display: 'flex',
                flexDirection: 'column',
                ...(!popup && {
                  width: '100%',
                  maxWidth: '60%',
                  mx: 'auto',
                  position: 'fixed',
                  bottom: '0px',
                  left: '100px',
                  right: '0px',
                  zIndex: 10,
                  backgroundColor: 'transparent',
                  px: '0px',
                  transform: isConversationListVisible ? 'translateX(125px)' : 'translateX(0)',
                  transition: 'transform 0.4s cubic-bezier(0.4, 0, 0.2, 1)',
                }),
                ...(popup && {
                  flexShrink: 0,
                  position: 'absolute',
                  bottom: '0px',
                  left: '20px',
                  right: '20px',
                  backgroundColor: colors.background.white,
                  boxSizing: 'border-box',
                }),
                pb: popup ? '10px' : '0px',
              }}
            >
              {messages.length > 0 && <JumpToLatestPill scrollContainerRef={scrollContainerRef} />}
              {isConversationInProgress && messages.length > 0 && (
                <NotifyBanner visible={showNotifyBanner} onEnable={handleNotifyEnable} onDismiss={handleNotifyDismiss} />
              )}
              {showFollowupSheet && (
                <Box sx={{ pb: '8px' }}>
                  <FollowupSheet
                    followup={activeWaitingFollowup}
                    accountId={accountId}
                    conversationId={conversationIdAtDb}
                    selectedModel={selectedModel}
                    popup={popup}
                    onStop={handleStopInvestigation}
                  />
                </Box>
              )}
              {!showFollowupSheet && (
                <SummaryBlock
                  hideTitle
                  sx={{
                    display: 'flex',
                    alignItems: popup ? 'stretch' : 'flex-end',
                    flexDirection: popup ? 'column' : 'row',
                    backgroundColor: colors.background.white,
                    borderRadius: '12px',
                    border: `1px solid ${colors.border.primary} !important`,
                    boxShadow: '0px 2px 7px 0px #3B82F60F,0px 0px 10px -1px #3B82F638',
                    mt: '0px',
                    padding: popup ? '12px 15px' : '10px 15px',
                    width: '100%',
                    boxSizing: 'border-box',
                    mb: popup ? '8px' : '0px',
                    '& textarea': {
                      width: '100%',
                      border: '0px',
                      resize: 'none',
                      boxShadow: 'none',
                      maxHeight: popup ? '30vh' : undefined,
                      '&:focus': {
                        boxShadow: 'none',
                      },
                      '&::placeholder': {
                        color: colors.background.lastSync,
                        fontSize: '14px',
                        fontWeight: 400,
                      },
                      '&::-webkit-scrollbar': {
                        display: 'none',
                      },
                    },
                    '& .MuiOutlinedInput-notchedOutline': {
                      border: '0px !important',
                    },
                    '& button': {
                      padding: '0px 10px !important',
                    },
                  }}
                >
                  <AutoSuggestTextarea
                    functionSuggestions={allFunctions}
                    ref={textareaRef}
                    fontSize='14px'
                    fontWeight='400'
                    value={generateQuestionText}
                    placeholder='Ask a question...'
                    maxRows={popup ? 20 : 8}
                    minRows={popup ? 1 : undefined}
                    maxLength={500000}
                    disabled={isConversationLoading || isConversationInProgress}
                    suggestionsAt={enabledAgents}
                    models={availableModels}
                    defaultModel={defaultModel}
                    selectedModel={selectedModel}
                    onModelSelect={setSelectedModel}
                    buttonProperties={{
                      show: true,
                      enable: !isConversationLoading && !isConversationInProgress,
                      onClick: (text) => {
                        handleGenerateInvestigation(text);
                      },
                      onClickStop: () => {
                        handleStopInvestigation();
                      },
                    }}
                    chatScreen={false}
                    popupInitial={popup && !!queryPrefix && messages.length === 0}
                    isFollowUp={true}
                    // Show Stop only when there's something genuinely actionable to stop:
                    // either the user has an in-flight question they initiated this session
                    // (currentlyProcessingQuestion), or the server-side conversation is
                    // strictly IN_PROGRESS (e.g. user refreshed mid-response and we want
                    // them to be able to abort). Excluding WAITING is deliberate — the
                    // useLLMInvestigationControl downgrade logic flips effectiveStatus to
                    // WAITING when a non-followup message is stuck waiting (workflow-
                    // builder leaves a scripting message like this after Approve and Build),
                    // and in that state there's no user-facing action to terminate.
                    allowStop={allowStop && messages.length > 0 && (!!currentlyProcessingQuestion || conversationStatus === 'IN_PROGRESS')}
                  />
                </SummaryBlock>
              )}
              {!popup && (
                <Box sx={{ backgroundColor: colors.background.white, pb: '6px' }}>
                  <Typography
                    sx={{
                      textAlign: 'center',
                      fontSize: '11px',
                      fontFamily: 'Roboto',
                      fontWeight: 400,
                      fontStyle: 'italic',
                      color: colors.text.tertiary,
                      pt: '5px',
                    }}
                  >
                    AI-generated content may include hallucinations — double-check critical details
                  </Typography>
                </Box>
              )}
            </Box>
          ) : null}
        </Box>
        <div ref={bottomRef} />
      </Box>
    </>
  );

  return !popup ? (
    <AskNudgebeeLayout
      handleNewChat={() => handleNewChat()}
      handleHomePage={() => router.push('/home')}
      handleToggle={handleToggle}
      onAgentsRefreshed={refreshAgents}
      externalAgents={allAgents}
      externalAgentsLoading={loadingAgents}
    >
      {content}
    </AskNudgebeeLayout>
  ) : (
    <Box sx={{ height: '100%', display: 'flex', flexDirection: 'column', overflow: 'hidden' }}>{content}</Box>
  );
};

KubernetesLLMResponseGenerator.propTypes = {
  accountId: PropTypes.string,
  sessionId: PropTypes.string,
  conversationId: PropTypes.string,
  source: PropTypes.string,
  categorySource: PropTypes.string,
  query: PropTypes.string,
  queryPrefix: PropTypes.string,
  querySuffix: PropTypes.string,
  popup: PropTypes.bool,
  showBorder: PropTypes.bool,
  apiMode: PropTypes.oneOf(['investigate', 'workflow']),
  workflowId: PropTypes.string,
  workflowDefinition: PropTypes.object,
};

export default KubernetesLLMResponseGenerator;
