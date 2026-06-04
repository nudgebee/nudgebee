import { useCallback, useRef, useEffect, useReducer } from 'react';
import { v4 as uuidv4 } from 'uuid';
import apiAskNudgebee, { createConversationFetcher } from '@api1/ask-nudgebee'; // Adjust path
import { snackbar } from '@components1/common/snackbarService';
import { parseHttpResponseBodyMessage, safeJSONParse } from 'src/utils/common'; // Adjust path
import { buildWorkflowConversationMessages } from '@components1/workflow/utils';
import apiWorkflow from '@api1/workflow';
import { getUserSession } from '@lib/auth';
import { getBrandTitle } from '@hooks/useTenantBranding';
import { getUpstreamStatus, mapUpstreamError } from '@lib/errorMessages';

const NULL_AGENT_ID = '00000000-0000-0000-0000-000000000000';

const parseConversationMessages = (conversationMessages, accountId) => {
  const allMessages = [];
  const collapsedIndexes = {}; // To track which items should be auto-collapsed

  if (!conversationMessages || conversationMessages.length === 0) {
    return { allMessages, collapsedIndexes };
  }

  const agentIdMap = {};
  const followupMessages = {};

  // 1. Build Maps
  conversationMessages.forEach((cm) => {
    cm.llm_conversation_agents?.forEach((agent) => {
      agentIdMap[agent.id] = agent;
    });
    if (cm.message_type === 'followup') {
      if (!followupMessages[cm.parent_agent_id]) {
        followupMessages[cm.parent_agent_id] = [cm];
      } else {
        followupMessages[cm.parent_agent_id].push(cm);
      }
    }
  });

  // 1b. Orphan followups — followups whose parent_agent_id doesn't resolve to
  //     a row in llm_conversation_agent. Happens in newer memory-driven flows
  //     where the LLM emits followups against a synthetic agent id that's
  //     never persisted to the agent table. Without this fallback they'd
  //     never render: the existing loop at agentsWithoutRouter.forEach only
  //     calls pushFollowups() when iterating real agents.
  //
  //     Assigns each orphan to the most recent preceding generation message
  //     by chronological order — that's the conversation message they belong
  //     to in any plausible writer path.
  const orphanFollowupsByGenId = {};
  const sortedByCreated = [...conversationMessages].sort((a, b) => new Date(a.created_at).getTime() - new Date(b.created_at).getTime());
  let currentGenId = null;
  sortedByCreated.forEach((cm) => {
    if (cm.message_type !== 'followup') {
      currentGenId = cm.id;
      return;
    }
    if (cm.parent_agent_id && !agentIdMap[cm.parent_agent_id] && currentGenId) {
      if (!orphanFollowupsByGenId[currentGenId]) orphanFollowupsByGenId[currentGenId] = [];
      orphanFollowupsByGenId[currentGenId].push(cm);
    }
  });

  const getParentAgents = (agentMessage) => {
    if (!agentMessage.parent_agent_id || agentMessage.parent_agent_id === NULL_AGENT_ID) {
      return [];
    }
    let parentAgents = [];
    let parentAgent = agentIdMap[agentMessage.parent_agent_id];
    const visited = new Set();
    while (parentAgent) {
      if (visited.has(parentAgent.id)) {
        break;
      }
      visited.add(parentAgent.id);
      if (!parentAgent.parent_agent_id || parentAgent.parent_agent_id === NULL_AGENT_ID) {
        break;
      }
      parentAgents.push(parentAgent.agent_name);
      parentAgent = agentIdMap[parentAgent.parent_agent_id];
    }
    return parentAgents.reverse();
  };

  conversationMessages.forEach((conversationMessage) => {
    if (conversationMessage.message_type === 'followup') {
      return;
    }

    try {
      const toolRequestResponse = {};
      const messageSequence = [];
      const responseReferences = [];
      let agentsWithoutRouter = conversationMessage?.llm_conversation_agents?.filter((r) => r.agent_name !== 'router') ?? [];

      // Planner Logic
      let hasPlanner = false;
      let plannerId = null;
      agentsWithoutRouter = agentsWithoutRouter.map((r) => {
        hasPlanner = hasPlanner || r.agent_name === 'planner';
        if (hasPlanner && !plannerId) {
          plannerId = r.id;
        }
        return r;
      });

      if (hasPlanner) {
        const queryAgents = [
          'query_generator',
          'elastic_search_query',
          'datadog_metrics_query',
          'logql_query_generator',
          'promql_query',
          'signoz_log_query_generator',
        ];
        agentsWithoutRouter = agentsWithoutRouter?.filter((r) => !queryAgents.includes(r.agent_name)) ?? [];
      }

      // Question Block
      if (!messageSequence.includes(conversationMessage.id + '-question')) {
        messageSequence.push(conversationMessage.id + '-question');
        toolRequestResponse[conversationMessage.id + '-question'] = {
          messageId: conversationMessage.id,
          text: conversationMessage.message,
          type: 'question',
          agentName: conversationMessage?.agent_name,
          parentAgents: getParentAgents(conversationMessage),
          created_at: conversationMessage.created_at,
          updated_at: conversationMessage.updated_at,
          user: conversationMessage?.user?.display_name || 'System',
          attachments: conversationMessage.attachments || [],
        };

        if (conversationMessage.ack_message?.trim()) {
          messageSequence.push(conversationMessage.id + '-acknowledgment');
          toolRequestResponse[conversationMessage.id + '-acknowledgment'] = {
            text: conversationMessage.ack_message,
            content: conversationMessage.ack_message,
            type: 'acknowledgment',
            created_at: conversationMessage.created_at,
            updated_at: conversationMessage.updated_at,
            user: conversationMessage?.user?.display_name || 'System',
          };
        }
        if (hasPlanner) {
          messageSequence.push(plannerId);
        }
      }
      const plannerIdChildMapping = {};
      const childAgentsToSkip = [];

      // Pre-pass: collect all sub-agent IDs that should be hidden as separate task cards.
      // Building this upfront ensures correct exclusion regardless of iteration order —
      // previously, a child agent appearing before its parent in the list would not be skipped.
      const debugAgentNames = ['k8s_debug', 'aws_debug', 'gcp_debug', 'azure_debug', 'datadog_debug'];
      agentsWithoutRouter.forEach((agent) => {
        if (!debugAgentNames.includes(agent?.agent_name)) {
          (agent?.llm_conversation_tool_calls || []).forEach((t) => {
            if (t.child_agent_id) {
              childAgentsToSkip.push(t.child_agent_id);
            }
          });
        }
      });

      agentsWithoutRouter.forEach((agent) => {
        const followups = followupMessages[agent.id];

        const pushFollowups = () => {
          if (!followups) return;
          followups.forEach((fMsg, i) => {
            const key = fMsg.id + '-followup-question-' + i;
            messageSequence.push(key);
            toolRequestResponse[key] = {
              text: fMsg.message,
              type: 'followup-question',
              tool: 'followup-question',
              ack_message: fMsg.ack_message,
              response: {
                type: 'followup-response',
                text: fMsg.response,
                status: fMsg.status,
                message_config: fMsg.message_config,
                message_id: conversationMessage.id,
                account_id: accountId,
                agent_id: agent.id,
                parent_agent_id: agent.parent_agent_id,
              },
            };
          });
        };

        if (debugAgentNames.includes(agent?.agent_name)) {
          (agent?.llm_conversation_tool_calls || []).forEach((t) => {
            if (t.child_agent_id) {
              plannerIdChildMapping[t.child_agent_id] = t.tool_id;
              messageSequence.push(t.child_agent_id);
            } else {
              plannerIdChildMapping[t.tool_id] = t.tool_id;
              toolRequestResponse[t.tool_id] = {
                response_text: t.response,
                response_status: t.status,
                text: t.parameters,
                tool: t.tool_name,
                tool_id: t.id,
                type: 'tool_call',
                toolParameters: t.parameters,
                references: t.references,
                created_at: t.created_at,
                updated_at: t.updated_at,
                response: { type: 'tool_call_response', text: t.response },
              };
              messageSequence.push(t.tool_id);
            }
          });
          pushFollowups();
          return;
        }

        if (childAgentsToSkip.includes(agent.id)) {
          pushFollowups();
          return;
        }
        if ((agent?.llm_conversation_tool_calls?.length ?? 0) === 0 && agent?.agent_name !== 'planner') {
          agent['llm_conversation_tool_calls'] = [
            { thought: agent.thought, response: agent.response, created_at: agent.created_at, updated_at: agent.updated_at },
          ];
        }

        if (!messageSequence.includes(agent.id)) {
          messageSequence.push(agent.id);
        }
        pushFollowups();
        let toolCallIndex = (agent?.llm_conversation_tool_calls?.length ?? 0) - 1;
        if (toolCallIndex < 0) {
          toolCallIndex = 0;
        }
        const activeTool = agent?.llm_conversation_tool_calls?.[toolCallIndex] || {};
        let agentReferences = [];
        (agent?.llm_conversation_tool_calls || []).forEach((t) => {
          if (t.references) {
            if (typeof t.references === 'string') {
              const parsed = safeJSONParse(t.references);
              if (Array.isArray(parsed)) {
                responseReferences.push(...parsed);
                agentReferences.push(...parsed);
              } else {
                console.warn('Failed to parse references for tool call:', t.tool_id);
              }
            } else if (Array.isArray(t.references)) {
              responseReferences.push(...t.references);
              agentReferences.push(...t.references);
            }
          }
        });

        if (agent.references) {
          if (typeof agent.references === 'string') {
            const parsed = safeJSONParse(agent.references);
            if (Array.isArray(parsed)) {
              responseReferences.push(...parsed);
              agentReferences.push(...parsed);
            } else {
              console.warn('Failed to parse references for agent:', agent.id);
            }
          } else if (Array.isArray(agent.references)) {
            responseReferences.push(...agent.references);
            agentReferences.push(...agent.references);
          }
        }

        let parameters = {};
        if (activeTool.parameters) {
          const parsedParameters = safeJSONParse(activeTool?.parameters);
          if (parsedParameters) {
            parameters = parsedParameters;
          }
        }
        toolRequestResponse[agent.id] = {
          // Map Agent to Message
          response_text: agent.response,
          response_status: agent.status,
          response_summary: agent.response_summary,
          log: (activeTool.thought || agent.thought || '').split('\n\nAction:')[0],
          tool: activeTool.tool_name ?? agent.agent_name,
          tool_id: agent.id,
          created_at: agent.created_at,
          updated_at: agent.updated_at,
          agentName: agent.agent_name,
          thought: agent.thought,
          query: agent.query,
          parentAgents: getParentAgents(agent),
          plannerId: plannerIdChildMapping[agent.id],
          type: 'tool_call',
          toolParameters: parameters,
          references: agentReferences.length > 0 ? agentReferences : undefined,
          toolCalls: agent?.llm_conversation_tool_calls || [],
          response: {
            type: 'tool_call_response',
            text: activeTool.response,
            created_at: activeTool.created_at,
            updated_at: activeTool.updated_at,
          },
        };
      });

      // Render orphan followups for this generation message (their
      // parent_agent_id has no corresponding llm_conversation_agent row).
      const orphanFollowups = orphanFollowupsByGenId[conversationMessage.id] ?? [];
      orphanFollowups.forEach((fMsg, i) => {
        const key = fMsg.id + '-followup-question-orphan-' + i;
        messageSequence.push(key);
        toolRequestResponse[key] = {
          text: fMsg.message,
          type: 'followup-question',
          tool: 'followup-question',
          ack_message: fMsg.ack_message,
          response: {
            type: 'followup-response',
            text: fMsg.response,
            status: fMsg.status,
            message_config: fMsg.message_config,
            message_id: conversationMessage.id,
            account_id: accountId,
            agent_id: fMsg.parent_agent_id,
            parent_agent_id: null,
          },
        };
      });

      const convResponse = conversationMessage.response && conversationMessage.status !== 'WAITING' ? conversationMessage.response : '';
      // For completed messages, always create a response entry even if the response text is empty
      // (lightweight polling query strips response text but the tab should still exist)
      const isMessageCompleted = ['COMPLETED', 'FAILED', 'KILLED', 'TERMINATED'].includes(conversationMessage.status);
      if (convResponse || isMessageCompleted) {
        messageSequence.push(conversationMessage.id);
        const lastAgent = conversationMessage?.llm_conversation_agents?.at(-1);
        toolRequestResponse[conversationMessage.id] = {
          text: convResponse,
          type: 'response',
          created_at: conversationMessage.created_at,
          updated_at: conversationMessage.updated_at,
          id: conversationMessage.id,
          agentName: lastAgent?.agent_name || '',
          parentAgents: [],
          references: responseReferences,
          ack_message: conversationMessage.ack_message,
          status: conversationMessage.status,
        };
      }
      const finalData = messageSequence.map((s) => toolRequestResponse[s]).filter(Boolean);
      allMessages.push(...finalData);
    } catch (e) {
      console.error('Parse Error', e);
    }
  });

  return { allMessages };
};

// --- Reducer ---

const initialState = {
  allowStop: false,
  isProcessing: false,
  currentlyProcessingQuestion: null,
  messages: [],
  conversationStatus: '',
  conversationTitle: '',
  conversationIdAtDb: '',
  isLoading: false,
  availableModels: [],
  defaultModel: null,
  selectedModel: null,
  // Server-advertised image capability (from ai_list_models). Defaults to
  // disabled so the attach UI stays hidden until the backend confirms support.
  imageSupport: { enabled: false, maxPerMessage: 0, maxSizeMb: 0, allowedMimeTypes: [] },
};

function investigationReducer(state, action) {
  switch (action.type) {
    case 'SET_MESSAGES':
      return {
        ...state,
        messages: typeof action.payload === 'function' ? action.payload(state.messages) : action.payload,
      };
    case 'SET_CONVERSATION_STATUS':
      return { ...state, conversationStatus: action.payload };
    case 'SET_ALLOW_STOP':
      return { ...state, allowStop: action.payload };
    case 'SET_IS_PROCESSING':
      return { ...state, isProcessing: action.payload };
    case 'SET_CURRENTLY_PROCESSING_QUESTION':
      return { ...state, currentlyProcessingQuestion: action.payload };
    case 'SET_IS_LOADING':
      return { ...state, isLoading: action.payload };
    case 'SET_SELECTED_MODEL':
      return { ...state, selectedModel: action.payload };
    case 'SET_MODELS':
      return {
        ...state,
        availableModels: action.availableModels,
        defaultModel: action.defaultModel,
        imageSupport: action.imageSupport ?? state.imageSupport,
      };
    case 'UPDATE_CONVERSATION_META':
      // Atomic update for title + id + status (used in fetchConversation)
      return { ...state, ...action.fields };
    case 'INVESTIGATION_FAILED':
      return { ...state, isProcessing: false, currentlyProcessingQuestion: null, conversationStatus: 'FAILED' };
    case 'RESET':
      return {
        ...initialState,
        // Preserve loaded model list + capabilities across resets
        availableModels: state.availableModels,
        defaultModel: state.defaultModel,
        imageSupport: state.imageSupport,
      };
    default:
      return state;
  }
}

export const useLLMInvestigationControl = (accountId) => {
  const [state, dispatch] = useReducer(investigationReducer, initialState);

  const {
    allowStop,
    isProcessing,
    currentlyProcessingQuestion,
    messages,
    conversationStatus,
    conversationTitle,
    conversationIdAtDb,
    isLoading,
    availableModels,
    defaultModel,
    selectedModel,
    imageSupport,
  } = state;

  const currentSessionRef = useRef('');
  const isMountedRef = useRef(true);
  const fetchIdRef = useRef(0);
  const abortControllerRef = useRef(null);
  const conversationStatusRef = useRef(conversationStatus);
  conversationStatusRef.current = conversationStatus;
  // One fetcher per hook instance. Holds a cursor + merged Maps of
  // messages/agents/tool_calls so each poll fetches only rows updated since
  // the last call. Auto-resets when the bound session/conversation identity
  // changes; resetInvestigationState calls reset() explicitly on teardown.
  const conversationFetcherRef = useRef(null);
  if (!conversationFetcherRef.current) {
    conversationFetcherRef.current = createConversationFetcher();
  }

  useEffect(() => {
    isMountedRef.current = true;
    return () => {
      isMountedRef.current = false;
      if (abortControllerRef.current) {
        abortControllerRef.current.abort();
      }
    };
  }, []);

  // Stable setters – empty deps so references never change, preventing downstream re-renders
  const setMessages = useCallback((payload) => dispatch({ type: 'SET_MESSAGES', payload }), []);
  const setConversationStatus = useCallback((payload) => dispatch({ type: 'SET_CONVERSATION_STATUS', payload }), []);
  const setAllowStop = useCallback((payload) => dispatch({ type: 'SET_ALLOW_STOP', payload }), []);
  const setIsProcessing = useCallback((payload) => dispatch({ type: 'SET_IS_PROCESSING', payload }), []);
  const setCurrentlyProcessingQuestion = useCallback((payload) => dispatch({ type: 'SET_CURRENTLY_PROCESSING_QUESTION', payload }), []);
  const setSelectedModel = useCallback((payload) => dispatch({ type: 'SET_SELECTED_MODEL', payload }), []);

  useEffect(() => {
    if (!accountId) {
      return;
    }
    let cancelled = false;

    apiAskNudgebee
      .listModels(accountId)
      .then((res) => {
        if (!isMountedRef.current || cancelled) return;
        const availableModels = res?.data?.models || [];
        const defaultModel = res?.data?.default || null;
        const rawImageSupport = res?.data?.image_support;
        const imageSupport = rawImageSupport
          ? {
              enabled: !!rawImageSupport.enabled,
              maxPerMessage: rawImageSupport.max_per_message || 0,
              maxSizeMb: rawImageSupport.max_size_mb || 0,
              allowedMimeTypes: rawImageSupport.allowed_mime_types || [],
            }
          : undefined;
        dispatch({ type: 'SET_MODELS', availableModels, defaultModel, imageSupport });
      })
      .catch((err) => console.error('Failed to fetch models', err));

    return () => {
      cancelled = true;
    };
  }, [accountId]);

  // --- STOP LOGIC ---
  const stopInvestigation = useCallback(
    async (conversationId, currentStatus, onStopSuccess) => {
      if (currentStatus === 'COMPLETED' || currentStatus === 'FAILED' || currentStatus === 'TERMINATED' || !allowStop) {
        return;
      }

      const sessionKey = currentSessionRef.current;
      try {
        dispatch({ type: 'SET_IS_PROCESSING', payload: true });

        const res = await apiAskNudgebee.aiStopInvestigate({
          accountId: accountId,
          conversationId: conversationId,
        });

        if (!isMountedRef.current || currentSessionRef.current !== sessionKey) return;

        const response = res?.data?.data?.ai_cancel_investigation || {};

        if (response?.data?.status === 'terminated') {
          dispatch({ type: 'SET_ALLOW_STOP', payload: false });
          snackbar.success('Investigation terminated successfully');
          if (onStopSuccess) {
            onStopSuccess();
          }
        } else {
          snackbar.error('Failed to stop investigation');
        }
      } catch (error) {
        console.error('Error stopping investigation:', error);
        if (isMountedRef.current && currentSessionRef.current === sessionKey) {
          snackbar.error('An error occurred while stopping the investigation');
        }
      } finally {
        if (isMountedRef.current && currentSessionRef.current === sessionKey) {
          dispatch({ type: 'SET_IS_PROCESSING', payload: false });
        }
      }
    },
    [accountId, allowStop]
  );

  const handleWorkflowGeneration = useCallback(
    async (finalQuery, llmSessionId, workflowId, workflowDefinition, currentCluster, onSuccess) => {
      const sessionKey = currentSessionRef.current;
      const userSession = getUserSession();
      const config = {
        ...(selectedModel && {
          llm_provider: selectedModel.provider,
          llm_model_name: selectedModel.model,
        }),
        ...(workflowId && { workflow_id: workflowId }),
        ...(!workflowId && workflowDefinition && { workflow_definition: workflowDefinition }),
        // Default the automation to the cluster/account the user is currently viewing so the
        // builder doesn't ask which account/cluster to target (#30162).
        ...(currentCluster?.value && { current_cluster_id: currentCluster.value }),
        ...(currentCluster?.label && { current_cluster: currentCluster.label }),
      };

      const res = await apiWorkflow.aiGenerateWorkflow(
        accountId,
        finalQuery,
        conversationIdAtDb || undefined,
        llmSessionId,
        Object.keys(config).length > 0 ? config : undefined,
        true
      );

      if (!isMountedRef.current || currentSessionRef.current !== sessionKey) return;

      const response = res?.data?.ai_generate_workflow?.data ?? {};

      if (!response?.query && !response?.response) {
        const errorMessage = res?.errors?.[0]?.message;
        const msg = errorMessage?.includes('budget')
          ? `Monthly Budget Limit exceeded for this account. Contact ${getBrandTitle()} Support team.`
          : errorMessage || 'Failed to generate automation.';
        snackbar.error(msg);
        throw new Error('Automation generation failed');
      }

      const sessionIdToUse = response.session_id || llmSessionId;
      const newMessages = buildWorkflowConversationMessages(response, userSession?.user?.name || 'User', finalQuery);

      dispatch({
        type: 'UPDATE_CONVERSATION_META',
        fields: {
          conversationStatus: response.status || 'COMPLETED',
          ...(response.conversation_id && { conversationIdAtDb: response.conversation_id }),
          conversationTitle: response.query || finalQuery,
          ...(response.status !== 'IN_PROGRESS' && { isProcessing: false, currentlyProcessingQuestion: null }),
        },
      });
      dispatch({ type: 'SET_MESSAGES', payload: (prev) => [...prev, ...newMessages] });

      if (onSuccess) {
        onSuccess(sessionIdToUse);
      }
    },
    [accountId, conversationIdAtDb, selectedModel]
  );

  const handleInvestigationGeneration = useCallback(
    async (finalQuery, llmSessionId, onSuccess, categorySource, images) => {
      const sessionKey = currentSessionRef.current;
      const requestPayload = {
        account_id: accountId,
        query: finalQuery,
        session_id: llmSessionId,
      };

      if (selectedModel) {
        requestPayload.config = {
          llm_provider: selectedModel.provider,
          llm_model_name: selectedModel.model,
        };
      }

      if (categorySource) {
        requestPayload.source = categorySource;
      }

      if (images?.length) {
        requestPayload.images = images;
      }

      const res = await apiAskNudgebee.aiGenerateInvestigate(requestPayload);

      if (!isMountedRef.current || currentSessionRef.current !== sessionKey) return;

      const response = res?.data?.data?.ai_execute_investigation ?? {};

      if (!response?.data?.query) {
        const status = getUpstreamStatus(res?.data);
        const fallback = parseHttpResponseBodyMessage(res?.data) || 'Failed to start investigation.';
        snackbar.error(mapUpstreamError(status, fallback));
        throw new Error('Investigation generation failed');
      }

      dispatch({ type: 'SET_CONVERSATION_STATUS', payload: 'IN_PROGRESS' });
      if (onSuccess) {
        onSuccess(llmSessionId);
      }
    },
    [accountId, selectedModel]
  );

  const startInvestigation = useCallback(
    async ({
      text,
      queryPrefix: _queryPrefix,
      querySuffix,
      selectedSessionId,
      apiMode = 'investigate',
      workflowId,
      workflowDefinition,
      currentCluster,
      categorySource,
      images,
      onSuccess,
      onFailure,
    }) => {
      if (!text) {
        return;
      }

      const finalQuery = querySuffix ? `${text} ${querySuffix}` : text;
      const llmSessionId = selectedSessionId || uuidv4();

      dispatch({ type: 'UPDATE_CONVERSATION_META', fields: { currentlyProcessingQuestion: finalQuery, isProcessing: true } });

      try {
        if (apiMode === 'workflow') {
          await handleWorkflowGeneration(finalQuery, llmSessionId, workflowId, workflowDefinition, currentCluster, onSuccess);
        } else {
          await handleInvestigationGeneration(finalQuery, llmSessionId, onSuccess, categorySource, images);
        }
      } catch (error) {
        console.error(error);
        dispatch({ type: 'INVESTIGATION_FAILED' });

        if (!error.message.includes('Automation') && !error.message.includes('Investigation')) {
          const status = error?.response?.status || error?.status;
          snackbar.error(status === 429 ? 'Monthly Budget Limit exceeded for this account.' : `An error occurred while generating ${apiMode}`);
        }

        if (onFailure) {
          onFailure();
        }
      }
    },
    [accountId, handleWorkflowGeneration, handleInvestigationGeneration]
  );

  const resetInvestigationState = useCallback(() => {
    // Abort any in-flight requests and invalidate pending fetches
    if (abortControllerRef.current) {
      abortControllerRef.current.abort();
      abortControllerRef.current = null;
    }
    fetchIdRef.current++;
    dispatch({ type: 'RESET' });
    currentSessionRef.current = ''; // Invalidate any in-flight fetchConversation calls
    conversationFetcherRef.current?.reset();
  }, []);

  const fetchConversation = useCallback(
    async (sessionId, conversationId, source, isNewChat) => {
      if ((!sessionId && !conversationId) || (isNewChat && source === 'poll')) {
        return;
      }

      // Abort previous in-flight request when starting a new non-poll fetch
      if (source !== 'poll' && abortControllerRef.current) {
        abortControllerRef.current.abort();
      }

      // Increment generation counter to detect "switch away and back" races
      const thisFetchId = ++fetchIdRef.current;
      const controller = source !== 'poll' ? new AbortController() : null;
      if (controller) {
        abortControllerRef.current = controller;
      }
      const signal = controller?.signal;

      // Check if this fetch is still the latest one or component unmounted
      const isStale = () => !isMountedRef.current || fetchIdRef.current !== thisFetchId;

      currentSessionRef.current = sessionId;
      if (source !== 'poll') {
        dispatch({ type: 'SET_IS_LOADING', payload: true });
        dispatch({ type: 'SET_SELECTED_MODEL', payload: null });
      }

      try {
        // When conversationId is known, fetch model config in parallel with conversation data
        const modelConfigPromise =
          source !== 'poll' && conversationId
            ? apiAskNudgebee.getModelConfig(accountId, conversationId, signal).catch((err) => {
                if (err?.name === 'AbortError' || err?.name === 'CanceledError') return null;
                console.error('Failed to fetch model config:', err);
                return null;
              })
            : Promise.resolve(null);

        // ai_get_conversation_v3 via the stateful fetcher: every call (initial
        // + poll) hits the same delta endpoint. The fetcher tracks a cursor +
        // merged maps internally, so polls fetch only rows updated since the
        // last call. New tool_call output appears the moment it's written —
        // no more "wait for terminal status to see responses" (the regression
        // PR #28028 introduced when it stripped TOAST columns to bound poll
        // cost). Live tool output is restored, polling cost is bounded by
        // change rate instead of conversation size.
        const res = await conversationFetcherRef.current.fetch({
          accountId,
          sessionId,
          conversationId,
          signal,
        });

        // After async response, check if this fetch is still relevant
        if (isStale()) return;

        const errors = res?.data?.errors ?? [];
        if (errors.length > 0) {
          if (!isStale() && source !== 'poll') {
            dispatch({ type: 'SET_CONVERSATION_STATUS', payload: 'FAILED' });
          }
          if (!isStale()) {
            dispatch({ type: 'SET_IS_LOADING', payload: false });
          }
          return { error: true };
        }

        const conversationResponses = res?.data?.data?.llm_conversations ?? [];

        if (conversationResponses.length > 0) {
          const response = conversationResponses[conversationResponses.length - 1];

          // Atomic update for conversation metadata
          // The conversation-level status may be stale (e.g. COMPLETED from a previous question)
          // while a new question is still processing. Check individual message statuses to avoid
          // prematurely stopping the polling cycle.
          let effectiveStatus = response.status;
          if (['COMPLETED', 'FAILED', 'KILLED', 'TERMINATED'].includes(response.status)) {
            const msgs = response.llm_conversation_messages || [];
            const hasWaitingMessages = msgs.some((m) => m.message_type !== 'followup' && m.status === 'WAITING');
            const hasActiveMessages = msgs.some((m) => m.message_type !== 'followup' && ['IN_PROGRESS', 'WAITING'].includes(m.status));
            if (hasWaitingMessages) {
              effectiveStatus = 'WAITING';
            } else if (hasActiveMessages) {
              effectiveStatus = 'IN_PROGRESS';
            }
          }
          if (!isStale()) {
            dispatch({
              type: 'UPDATE_CONVERSATION_META',
              fields: {
                conversationTitle: conversationResponses[0].title,
                conversationIdAtDb: conversationResponses[0].id,
                conversationStatus: effectiveStatus,
              },
            });
          }

          // Fetch and set model configuration for this conversation
          if (!isStale() && source !== 'poll') {
            try {
              const modelConfigRes = conversationId
                ? await modelConfigPromise
                : await apiAskNudgebee.getModelConfig(accountId, conversationResponses[0].id, signal);
              if (!isStale() && modelConfigRes?.data && !modelConfigRes?.errors?.length) {
                const modelConfig = modelConfigRes.data;
                if (modelConfig.is_custom && modelConfig.current) {
                  dispatch({
                    type: 'SET_SELECTED_MODEL',
                    payload: {
                      provider: modelConfig.current.provider,
                      model: modelConfig.current.model,
                    },
                  });
                }
              }
            } catch (error) {
              if (error?.name !== 'AbortError' && error?.name !== 'CanceledError') {
                console.error('Failed to fetch model config:', error);
              }
            }
          }

          if (!isStale() && source === 'poll') {
            dispatch({ type: 'SET_ALLOW_STOP', payload: true });
          }

          const { allMessages } = parseConversationMessages(response.llm_conversation_messages, accountId);
          if (!isNewChat && !isStale()) {
            dispatch({
              type: 'SET_MESSAGES',
              payload: (prev) => {
                const normalizeTs = (ts) => (ts && !ts.endsWith('Z') && !ts.includes('+') ? ts + 'Z' : ts);
                const confirmedQuestions = allMessages
                  .filter((m) => m.type === 'question')
                  .map((q) => ({
                    text: (q.text || '').trim(),
                    time: new Date(normalizeTs(q.created_at)).getTime(),
                  }));

                const optimistic = prev.filter((m) => {
                  if (!m.isOptimistic) return false;
                  const msgText = (m.text || '').trim();
                  const msgTime = new Date(m.created_at).getTime();
                  return !confirmedQuestions.some((c) => c.text === msgText && c.time >= msgTime);
                });
                return [...allMessages, ...optimistic];
              },
            });
            // Clear currentlyProcessingQuestion when we reach a terminal or waiting status.
            // WAITING means the agent is paused for user input (followup question) — the UI
            // should stop showing the loading indicator and let the user interact.
            const isTerminalStatus = ['COMPLETED', 'FAILED', 'KILLED', 'TERMINATED'].includes(effectiveStatus);
            const isWaitingStatus = effectiveStatus === 'WAITING';
            if (isTerminalStatus || isWaitingStatus) {
              dispatch({ type: 'SET_CURRENTLY_PROCESSING_QUESTION', payload: null });
            }
          }
        } else if (!isStale() && source !== 'poll') {
          dispatch({ type: 'SET_CONVERSATION_STATUS', payload: 'NOT_FOUND' });
        }

        return { success: true, data: conversationResponses };
      } catch (error) {
        if (error?.name === 'AbortError' || error?.name === 'CanceledError') return;
        console.error(error);
        if (!isStale() && source !== 'poll') {
          dispatch({ type: 'SET_CONVERSATION_STATUS', payload: 'FAILED' });
        }
      } finally {
        if (source !== 'poll' && (!isStale() || !signal?.aborted)) {
          dispatch({ type: 'SET_IS_LOADING', payload: false });
        }
      }
    },
    [accountId]
  );

  const checkConversationExists = useCallback(
    async (sessionId) => {
      if (!sessionId) {
        return { exists: false };
      }

      try {
        const res = await apiAskNudgebee.getLlmConversation({
          accountId,
          sessionId,
        });

        const errors = res?.data?.errors ?? [];
        if (errors.length > 0) {
          return { exists: false, error: true };
        }

        const conversations = res?.data?.data?.llm_conversations ?? [];
        return { exists: conversations.length > 0, data: conversations };
      } catch (error) {
        console.error(error);
        return { exists: false, error: true };
      }
    },
    [accountId]
  );

  return {
    messages,
    setMessages,
    conversationStatus,
    setConversationStatus,
    conversationTitle,
    conversationIdAtDb,
    allowStop,
    setAllowStop,
    stopInvestigation,
    startInvestigation,
    isProcessing,
    setIsProcessing,
    isLoading,
    currentlyProcessingQuestion,
    setCurrentlyProcessingQuestion,
    fetchConversation,
    resetInvestigationState,
    checkConversationExists,
    availableModels,
    defaultModel,
    selectedModel,
    setSelectedModel,
    imageSupport,
  };
};
