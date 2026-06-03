import { ArrowRightYellowIcon, ChatRoundedIcon, ListIcon, SaveIconOutlinelight, SaveIconOutlineselect, ShareIconBlue, UsersIcon } from '@assets';
import { useTenantBranding, getNubiIconUrl } from '@hooks/useTenantBranding';
import { useBrowserNotification } from '@hooks/useBrowserNotification';
import NotifyBanner from '@components1/common/NotifyBanner';
import Text from '@common-new/format/Text';
import ClusterDropDown from '@components1/common/ClusterDropDown';
import ConversationLoader from '@common-new/ConversationLoader';
import Tooltip from '@components1/ds/Tooltip';
import AskNudgebeeLayout from '@components1/common/layout/AskNudgebeeLayoutV2';
import { Button } from '@components1/ds/Button';
import { toast as snackbar } from '@components1/ds/Toast';
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
import { ds } from '@utils/colors';
import AutoSuggestTextarea from '@components1/k8s/common/TextAreaV2';
import { SummaryBlock } from '@components1/k8s/KubernetesClusterSummary';
import { isCompleteWorkflowDefinition } from './utils/isCompleteWorkflowDefinition';
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
    imageSupport,
  } = useLLMInvestigationControl(accountId);

  const isConversationInProgress = useMemo(
    () => conversationStatus === 'IN_PROGRESS' || !!currentlyProcessingQuestion,
    [conversationStatus, currentlyProcessingQuestion]
  );

  // Backend holds the parent conversationMessage in WAITING between a followup answer
  // POST and the next agent tick — `isConversationInProgress` (IN_PROGRESS-only) goes
  // false in that window, so anything that means "is the system working right now?"
  // (loader, run-end UI gating) must use `isSystemBusy` instead.
  const TERMINAL_FOLLOWUP_STATUSES = ['COMPLETED', 'TERMINATED', 'KILLED', 'FAILED'];
  const isSystemBusy = useMemo(
    () =>
      !!currentlyProcessingQuestion ||
      (conversationStatus !== '' && conversationStatus !== 'NOT_FOUND' && !TERMINAL_FOLLOWUP_STATUSES.includes(conversationStatus)),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [conversationStatus, currentlyProcessingQuestion]
  );

  // Derive the latest follow-up that's waiting for the user — this is what the bottom-anchored
  // FollowupSheet renders. The followup message itself is saved as IN_PROGRESS while it sits
  // waiting on the user; only COMPLETED/TERMINATED/KILLED/FAILED are terminal and disqualify
  // it from being the active prompt. Falls back to nothing when none qualify.
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
    async (text, images) => {
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
          // Local base64 for instant preview; same render shape as persisted
          // attachments (which add id/size/description and may have null data).
          attachments: (images || []).map((img) => ({ data: img.data, mime_type: img.mime_type })),
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
        images,
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
                // Only auto-apply a genuine, complete workflow — not a partial fragment a
                // read-only answer (issue #30825) might quote. See helper for the exact rule.
                if (isCompleteWorkflowDefinition(parsed)) {
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
      customStyle={{ height: `${ds.space.mul(1, 7)} !important` }}
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
                padding: `${ds.space.mul(0, 5)} ${ds.space[5]}`,
              }}
            >
              <Box>
                <Box sx={{ display: 'flex', alignItems: 'center', height: '100%', gap: ds.space[2] }}>
                  <SafeIcon src={getNubiIconUrl()} alt={assistantName} width={40} height={40} />
                  <Typography
                    sx={{
                      fontSize: 'var(--ds-text-display)',
                      fontFamily: ds.font.sans,
                      fontWeight: 'var(--ds-font-weight-semibold)',
                      color: 'var(--ds-gray-700)',
                    }}
                  >
                    {assistantName}
                  </Typography>
                </Box>
              </Box>
              <Box
                sx={{
                  display: 'flex',
                  alignItems: 'center',
                  border: `1px solid var(--ds-gray-300)`,
                  borderRadius: ds.radius.sm,
                  position: 'relative',
                  '&:hover': {
                    borderColor: 'var(--ds-blue-500)',
                  },
                  '.MuiAutocomplete-root': {
                    minHeight: `${ds.space[6]} !important`,
                  },
                  '& .MuiFormControl-root': {
                    margin: '0px !important',
                    fontSize: 'var(--ds-text-title) !important',
                  },
                  '& .MuiOutlinedInput-notchedOutline': {
                    border: showBorder ? 'inherit' : 'none !important',
                  },
                  '& .MuiInputBase-sizeSmall': {
                    padding: `${ds.space[1]} ${ds.space.mul(1, 10)} ${ds.space.mul(0, 3)} 0px !important`,
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
                height: ds.space.mul(0, 26),
                position: 'fixed',
                zIndex: 20,
                top: 0,
                right: 0,
                left: ds.space.mul(0, 38),
                pr: ds.space[5],
                backgroundColor: 'var(--ds-background-100)',
              }}
            >
              <Box sx={{ display: 'flex', flexDirection: 'column', width: '100%', position: 'sticky', top: '0px' }}>
                <Box sx={{ padding: `${ds.space.mul(0, 5)} ${ds.space[5]}` }}>
                  <Box sx={{ display: 'flex', alignItems: 'center', height: '100%', gap: ds.space[2] }}>
                    <SafeIcon src={getNubiIconUrl()} alt={assistantName} width={40} height={40} />
                    <Typography
                      sx={{
                        fontSize: 'var(--ds-text-display)',
                        fontFamily: ds.font.sans,
                        fontWeight: 'var(--ds-font-weight-semibold)',
                        color: 'var(--ds-gray-700)',
                      }}
                    >
                      {assistantName}
                    </Typography>
                  </Box>
                </Box>
              </Box>

              <Box sx={{ display: 'inline-flex', alignItems: 'center' }}>
                <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space.mul(1, 5) }}>
                  {ConversationHeaderData.queriesAsked > 0 && (
                    <Tooltip
                      placement='bottom'
                      title='Queries Asked'
                      tooltipStyle={{
                        backgroundColor: 'white',
                        color: 'var(--ds-gray-700)',
                        padding: `${ds.space[2]} ${ds.space[3]}`,
                        fontSize: 'var(--ds-text-small)',
                        borderRadius: ds.radius.sm,
                        boxShadow: '0 2px 4px rgba(0,0,0,0.1)',
                      }}
                    >
                      <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space.mul(0, 3) }}>
                        <SafeIcon src={ChatRoundedIcon} alt='chat icon' width={16} height={16} />
                        <Typography
                          sx={{
                            fontSize: 'var(--ds-text-small)',
                            fontWeight: 'var(--ds-font-weight-regular)',
                            color: 'var(--ds-gray-400)',
                            fontFamily: ds.font.sans,
                            span: {
                              color: 'var(--ds-gray-700)',
                              fontWeight: 'var(--ds-font-weight-medium)',
                              mr: ds.space.mul(0, 3),
                              mt: ds.space[0],
                            },
                          }}
                        >
                          <span>{ConversationHeaderData.queriesAsked}</span>
                        </Typography>
                      </Box>
                    </Tooltip>
                  )}
                  {ConversationHeaderData.participants > 0 && (
                    <Tooltip
                      tooltipStyle={{
                        backgroundColor: 'var(--ds-background-100)',
                        color: 'var(--ds-gray-700)',
                        boxShadow: '0 2px 10px rgba(0,0,0,0.1)',
                        padding: 0,
                        border: '1px solid rgba(0,0,0,0.08)',
                        borderRadius: ds.radius.lg,
                      }}
                      title={
                        <Box
                          sx={{
                            display: 'flex',
                            flexDirection: 'column',
                            gap: ds.space[3],
                            padding: ds.space[4],
                          }}
                        >
                          <Typography
                            sx={{
                              fontSize: 'var(--ds-text-body-lg)',
                              fontWeight: 'var(--ds-font-weight-semibold)',
                              color: 'var(--ds-gray-700)',
                              borderBottom: `1px solid ${'var(--ds-background-200)'}`,
                              paddingBottom: ds.space[2],
                            }}
                          >
                            Participants
                          </Typography>
                          <Box sx={{ display: 'flex', flexDirection: 'column', flexWrap: 'wrap', gap: ds.space.mul(0, 5) }}>
                            {Array.from(uniqueParticipantNames).map((name, index) => (
                              <Box
                                key={index}
                                sx={{
                                  display: 'flex',
                                  alignItems: 'center',
                                  gap: ds.space[2],
                                  backgroundColor: 'var(--ds-background-200)',
                                  borderRadius: ds.space[4],
                                  padding: `${ds.space[1]} ${ds.space[3]} ${ds.space[1]} ${ds.space[1]}`,
                                  transition: 'all 0.2s ease',
                                  '&:hover': {
                                    backgroundColor: 'var(--ds-background-200)',
                                    transform: 'translateY(-1px)',
                                  },
                                }}
                              >
                                <Avatar
                                  sx={{
                                    bgcolor: 'var(--ds-gray-600)',
                                    height: ds.space.mul(1, 7),
                                    width: ds.space.mul(1, 7),
                                    fontSize: 'var(--ds-text-body-lg)',
                                    fontWeight: 'var(--ds-font-weight-regular)',
                                    fontFamily: ds.font.sans,
                                    cursor: 'pointer',
                                  }}
                                >
                                  {getInitials(name)}
                                </Avatar>
                                <Typography
                                  sx={{
                                    fontSize: 'var(--ds-text-body)',
                                    fontWeight: 'var(--ds-font-weight-medium)',
                                    color: 'var(--ds-gray-700)',
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
                      <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space.mul(0, 3) }}>
                        <SafeIcon src={UsersIcon} alt='users icon' width={16} height={16} />
                        <Typography
                          sx={{
                            fontSize: 'var(--ds-text-small)',
                            fontWeight: 'var(--ds-font-weight-regular)',
                            color: 'var(--ds-gray-400)',
                            fontFamily: ds.font.sans,
                            span: {
                              color: 'var(--ds-gray-700)',
                              fontWeight: 'var(--ds-font-weight-medium)',
                              mr: ds.space.mul(0, 3),
                            },
                          }}
                        >
                          <span>{ConversationHeaderData.participants}</span>
                        </Typography>
                      </Box>
                    </Tooltip>
                  )}
                  <Box onMouseEnter={handleTokenUsageHover}>
                    <ConversationTokenUsage tokenUsageData={tokenUsageData} isLoading={isFetchingTokenData} />
                  </Box>
                </Box>
                <Divider orientation='vertical' variant='middle' flexItem sx={{ height: ds.space.mul(1, 7), mx: ds.space.mul(0, 5) }} />
                <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2] }}>
                  {selectedConversation?.id && (
                    <Button
                      tone='secondary'
                      size='md'
                      composition='icon-only'
                      aria-label='Save conversation'
                      icon={
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
                  <Button
                    tone='secondary'
                    size='md'
                    composition='icon-only'
                    aria-label='Share'
                    icon={<SafeIcon src={ShareIconBlue} height={18} width={18} alt={'Share'} />}
                    onClick={handleShare}
                  />
                </Box>
                <Divider orientation='vertical' variant='middle' flexItem sx={{ height: ds.space.mul(1, 7), mx: ds.space.mul(0, 5) }} />
                <Box
                  sx={{
                    display: 'flex',
                    alignItems: 'center',
                    border: `1px solid var(--ds-gray-300)`,
                    borderRadius: ds.radius.sm,
                    height: `${ds.space[6]} !important`,
                    position: 'relative',
                    '&:hover': {
                      borderColor: 'var(--ds-blue-500)',
                    },
                    '.MuiAutocomplete-root': {
                      minHeight: `${ds.space[6]} !important`,
                    },
                    '& .MuiAutocomplete-input': {
                      paddingTop: '0px !important',
                    },
                    '& .MuiFormControl-root': {
                      margin: '0px !important',
                      fontSize: 'var(--ds-text-title) !important',
                    },
                    '& .MuiOutlinedInput-notchedOutline': {
                      border: showBorder ? 'inherit' : 'none !important',
                    },
                    '& .MuiOutlinedInput-root': {
                      padding: `${ds.space[1]} ${ds.space.mul(1, 10)} ${ds.space[1]} 0px !important`,
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
              mb: popup ? '0px' : isChatScreen ? ds.space.mul(1, 40) : '0px',
              px: popup ? ds.space.mul(1, 5) : '0px',
              pb: popup && (selectedSessionId != '' || selectedConversationId != '') ? ds.space.mul(0, 75) : popup ? ds.space.mul(0, 5) : '0px',
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
                width: ds.space.mul(0, 3),
              },

              '&::-webkit-scrollbar-track': {
                background: 'transparent',
                marginRight: ds.space[1], // Additional spacing
              },

              '&::-webkit-scrollbar-thumb': {
                background: 'var(--ds-gray-400)',
                borderRadius: ds.space[0],
              },

              '&::-webkit-scrollbar-thumb:hover': {
                background: 'var(--ds-gray-500)',
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
                  mt: selectedSessionId == '' && selectedConversationId == '' && !popup ? ds.space.mul(1, 25) : '0px',
                  ...(popup &&
                    selectedSessionId == '' &&
                    selectedConversationId == '' && {
                      flex: 1,
                      pb: ds.space.mul(1, 5),
                    }),
                  '@media (max-width: 1280px)': {
                    mt: selectedSessionId == '' && selectedConversationId == '' && !popup ? ds.space.mul(1, 15) : '0px',
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
                        mb: ds.space[3],
                        mt: ds.space.mul(1, 15),
                      }),
                    }}
                  >
                    <DynamicGreeting
                      userName={getUserSession()?.user?.name || ''}
                      className={`poppins-font animated-box`}
                      style={{ animationDelay: '0.1s', letterSpacing: '-0.6px', fontWeight: 'var(--ds-font-weight-medium)' }}
                    />
                  </Box>
                )}
                {selectedSessionId == '' && selectedConversationId == '' && !currentlyProcessingQuestion ? (
                  <Box
                    sx={{
                      maxWidth: popup ? '100%' : ds.space.mul(0, 362),
                      mx: 'auto',
                      width: '100%',
                      mb: popup ? '0px' : ds.space[3],
                      mt: popup ? 'auto' : '0px',
                      '@media (max-width: 1300px)': {
                        maxWidth: popup ? '100%' : ds.space.mul(0, 245),
                      },
                    }}
                  >
                    <Box
                      className={popup ? '' : 'animated-box'}
                      style={popup ? {} : { animationDelay: '0.3s' }}
                      sx={{
                        p: popup ? '0px' : ds.space.mul(0, 3),
                        borderRadius: ds.radius.xl,
                        background: popup
                          ? 'transparent'
                          : 'linear-gradient(to right,rgb(96, 165, 250, 0.2), rgb(96, 165, 250, 0.1), rgb(96, 165, 250, 0.2))',
                        mt: popup ? '0px' : ds.space.mul(1, 5),
                        mb: ds.space[3],
                        mx: 'auto',
                      }}
                    >
                      <SummaryBlock
                        hideTitle
                        sx={{
                          display: 'flex',
                          alignItems: 'flex-end',
                          gap: popup ? ds.space.mul(0, 5) : ds.space.mul(0, 15),
                          backgroundColor: 'var(--ds-background-100)',
                          borderRadius: ds.radius.xl,
                          border: popup ? `1px solid var(--ds-blue-500) !important` : `0.75px solid ${'var(--ds-gray-600)'} !important`,
                          boxShadow: popup
                            ? '0px 2px 7px 0px #3B82F60F,0px 0px 10px -1px #3B82F638'
                            : '0px 2px 7px 0px #3B82F60F,0px 4px 6px -1px #3B82F61F',
                          padding: popup ? `${ds.space.mul(0, 5)} ${ds.space[4]}` : `${ds.space[4]} ${ds.space.mul(1, 5)}`,
                          '& textarea': {
                            width: '100%',
                            border: '0px',
                            resize: 'none',
                            boxShadow: 'none',
                            backgroundColor: 'var(--ds-background-100)',
                            minHeight: popup ? ds.space[5] : ds.space.mul(1, 20),
                            px: '0px',
                            '&:focus': {
                              boxShadow: 'none',
                            },
                            '&::placeholder': {
                              color: 'var(--ds-gray-400)',
                              fontSize: popup ? 'var(--ds-text-body-lg)' : 'inherit',
                              '@media (max-width: 1300px)': {
                                fontSize: 'var(--ds-text-body)',
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
                            padding: `0px ${ds.space.mul(0, 5)} !important`,
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
                          imageSupport={imageSupport}
                          isFollowUp={popup}
                          buttonProperties={{
                            show: true,
                            enable: !isConversationLoading,
                            onClick: (text, _config, images) => {
                              handleGenerateInvestigation(text, images);
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
                      maxWidth: popup ? '95%' : ds.space.mul(0, 362),
                      px: ds.space.mul(0, 5),
                      mt: ds.space.mul(0, 5),
                    }}
                  >
                    <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space.mul(1, 10), mb: ds.space.mul(1, 5) }}>
                      <Box>
                        {troubleShootData.length > 0 && (
                          <Text
                            value='Any Errors in the Past 24 Hours?'
                            sx={{ fontWeight: 'var(--ds-font-weight-medium)', fontFamily: ds.font.sans, mb: ds.space[3] }}
                          />
                        )}
                        {troubleShootData.map((data) => (
                          <Box key={data.id}>
                            <TroubleshootList data={data} type='troubleshooting' accountId={accountId} />
                          </Box>
                        ))}
                      </Box>
                      <Box>
                        {optimizationData.length > 0 && (
                          <Text
                            value='What can we Optimize?'
                            sx={{ fontWeight: 'var(--ds-font-weight-medium)', fontFamily: ds.font.sans, mb: ds.space[3] }}
                          />
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
              <Box sx={{ mt: ds.space.mul(0, 5), width: '100%', minWidth: ds.space.mul(1, 100) }}>
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
                  py: ds.space.mul(1, 20),
                  gap: ds.space[3],
                }}
              >
                <Box
                  sx={{
                    width: ds.space[7],
                    height: ds.space[7],
                    borderRadius: '50%',
                    backgroundColor: 'var(--ds-background-200)',
                    display: 'flex',
                    alignItems: 'center',
                    justifyContent: 'center',
                  }}
                >
                  <SafeIcon src={ChatRoundedIcon} alt='not found' width={22} height={22} style={{ opacity: 0.5 }} />
                </Box>
                <Typography
                  sx={{
                    fontSize: 'var(--ds-text-title)',
                    fontWeight: 'var(--ds-font-weight-medium)',
                    color: 'var(--ds-gray-700)',
                    fontFamily: ds.font.sans,
                  }}
                >
                  Conversation not found
                </Typography>
                <Typography sx={{ fontSize: 'var(--ds-text-body)', color: 'var(--ds-gray-400)', fontFamily: ds.font.sans }}>
                  This conversation may have been deleted or is no longer available.
                </Typography>
                <Button tone='primary' size='md' sx={{ mt: ds.space[2] }} onClick={handleNewChat}>
                  Start New Conversation
                </Button>
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

            {!isSystemBusy && !popup && conversationSuggestions && conversationSuggestions.length > 0 && messages.length > 0 && (
              <Box sx={{ mb: ds.space[5], mt: ds.space[5] }}>
                <Box sx={{ display: 'flex', alignItems: 'center', mb: ds.space[2] }}>
                  <SafeIcon src={ListIcon} width={24} height={24} alt='list icon' />
                  <Typography
                    sx={{ fontSize: 'var(--ds-text-title)', color: 'var(--ds-gray-700)', fontWeight: 'var(--ds-font-weight-medium)', p: ds.space[2] }}
                  >
                    Related Questions
                  </Typography>
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
                          p: `${ds.space[2]} 0px`,
                          borderBottom: `0.5px solid ${'var(--ds-gray-200)'}`,
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
                            backgroundColor: 'var(--ds-background-200)',
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
                            fontSize: 'var(--ds-text-body)',
                            color: 'var(--ds-gray-700)',
                          }}
                        />
                        <SafeIcon src={ArrowRightYellowIcon} alt='arrow right icon' width={28} height={28} style={{ marginLeft: 'auto' }} />
                      </Box>
                    );
                  })}
                </Box>
              </Box>
            )}

            {(() => {
              if (!isSystemBusy || messages.length === 0 || showFollowupSheet) {
                return null;
              }
              if (!(selectedSessionId !== '' || selectedConversationId !== '')) {
                return null;
              }
              return (
                <Box
                  mb={popup ? ds.space.mul(1, 5) : ds.space.mul(0, 35)}
                  sx={{ width: '100%', minWidth: popup ? ds.space.mul(1, 100) : 0, boxSizing: 'border-box' }}
                >
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
                  left: ds.space.mul(1, 25),
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
                  left: ds.space.mul(1, 5),
                  right: ds.space.mul(1, 5),
                  backgroundColor: 'var(--ds-background-100)',
                  boxSizing: 'border-box',
                }),
                pb: popup ? ds.space.mul(0, 5) : '0px',
              }}
            >
              {messages.length > 0 && <JumpToLatestPill scrollContainerRef={scrollContainerRef} />}
              {isConversationInProgress && messages.length > 0 && (
                <NotifyBanner visible={showNotifyBanner} onEnable={handleNotifyEnable} onDismiss={handleNotifyDismiss} />
              )}
              {showFollowupSheet && (
                <Box sx={{ pb: ds.space[2] }}>
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
                    backgroundColor: 'var(--ds-background-100)',
                    borderRadius: ds.radius.xl,
                    border: `1px solid var(--ds-blue-500) !important`,
                    boxShadow: '0px 2px 7px 0px #3B82F60F,0px 0px 10px -1px #3B82F638',
                    mt: '0px',
                    padding: popup ? `${ds.space[3]} ${ds.space[4]}` : `${ds.space.mul(0, 5)} ${ds.space[4]}`,
                    width: '100%',
                    boxSizing: 'border-box',
                    mb: popup ? ds.space[2] : '0px',
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
                        color: 'var(--ds-gray-400)',
                        fontSize: 'var(--ds-text-body-lg)',
                        fontWeight: 'var(--ds-font-weight-regular)',
                      },
                      '&::-webkit-scrollbar': {
                        display: 'none',
                      },
                    },
                    '& .MuiOutlinedInput-notchedOutline': {
                      border: '0px !important',
                    },
                    '& button': {
                      padding: `0px ${ds.space.mul(0, 5)} !important`,
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
                    imageSupport={imageSupport}
                    buttonProperties={{
                      show: true,
                      enable: !isConversationLoading && !isConversationInProgress,
                      onClick: (text, _config, images) => {
                        handleGenerateInvestigation(text, images);
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
                <Box sx={{ backgroundColor: 'var(--ds-background-100)', pb: ds.space.mul(0, 3) }}>
                  <Typography
                    sx={{
                      textAlign: 'center',
                      fontSize: 'var(--ds-text-caption)',
                      fontFamily: ds.font.sans,
                      fontWeight: 'var(--ds-font-weight-regular)',
                      fontStyle: 'italic',
                      color: 'var(--ds-gray-500)',
                      pt: ds.space[1],
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
