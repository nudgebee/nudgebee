import { gqlStringify, queryGraphQL } from '@lib/HttpService';
import { getUserSession } from '@lib/auth';
import type {
  AgentDefinition,
  AiFeedbackCreateRequest,
  AiFunctionData,
  ConversationComparisonRequest,
  ConversationHistoryForInvestigationRequest,
  ConversationHistoryRequest,
  ConversationSuggestionsRequest,
  ConversationV3Agent,
  ConversationV3Message,
  ConversationV3Shell,
  ConversationV3ToolCall,
  CreateAgentExtensionRequest,
  CreateRagDataRequest,
  DeleteConversationRequest,
  DeleteSavedConversationRequest,
  FollowupRequest,
  FeedbackForSessionRequest,
  GenerateQueryRequest,
  GetWorkspaceFileRequest,
  InvestigateRequest,
  ListAiFeedbackRequest,
  ListAgentsRequest,
  ListFunctionsRequest,
  ListToolsRequest,
  SaveConversationRequest,
  StopInvestigateRequest,
  ToolDefinition,
  UpdateAgentExtensionRequest,
  UpdateFunctionData,
} from '../../types/ask-nudgebee';

const EVENT_DETAILS_RETRIEVAL_TITLE = 'Event details retrieval by ID';

// Per-env watch flag: /v1/watches mounts only when LLM_SERVER_WATCH_ENABLED is on; a 404 latches it off for the page session.
let watchFeatureDisabled = false;

// --- ai_get_conversation_v3 helpers ----------------------------------------
// The new action returns flat arrays; consumers want the legacy nested shape
// (llm_conversations[].llm_conversation_messages[].llm_conversation_agents[]
// .llm_conversation_tool_calls[]). We split the work into:
//   1. _callConversationV3 — raw GraphQL call, returns the action payload.
//   2. _assembleConversationLegacyEnvelope — builds the nested tree from
//      Maps of merged state.
// Single-shot path uses (1) then builds Maps from the raw payload and calls (2).
// The stateful fetcher (createConversationFetcher) keeps Maps across calls so
// each delta poll merges into the existing tree without re-fetching unchanged
// rows.

const GET_LLM_CONVERSATION_V3_QUERY = `
  query AiGetConversationV3($request: AiGetConversationV3Request!) {
    ai_get_conversation_v3(request: $request) {
      conversation {
        id
        session_id
        account_id
        tenant_id
        user_id
        user_display_name
        created_at
        updated_at
        source
        context
        status
        title
      }
      messages {
        id
        user_id
        user_display_name
        created_at
        updated_at
        message
        message_type
        response
        role
        status
        parent_agent_id
        message_config
        ack_message
        metadata
        attachments {
          id
          mime_type
          size_bytes
          description
          created_at
          data
        }
      }
      agents {
        id
        message_id
        agent_name
        response
        response_summary
        query
        thought
        parent_agent_id
        status
        references
        created_at
        updated_at
      }
      tool_calls {
        id
        agent_id
        tool_name
        parameters
        response
        thought
        tool_type
        child_agent_id
        references
        tool_id
        status
        created_at
        updated_at
      }
      cursor
    }
  }
`;

type ConversationV3RawPayload = {
  conversation: ConversationV3Shell | null;
  messages: ConversationV3Message[];
  agents: ConversationV3Agent[];
  tool_calls: ConversationV3ToolCall[];
  cursor: string | null;
};

async function _callConversationV3(opts: {
  accountId: string;
  conversationId?: string;
  sessionId?: string;
  since?: string | null;
  signal?: AbortSignal;
}): Promise<{ rawResponse: any; payload: ConversationV3RawPayload | null }> {
  const request: any = { account_id: opts.accountId };
  if (opts.conversationId) request.conversation_id = opts.conversationId;
  if (opts.sessionId) request.session_id = opts.sessionId;
  if (opts.since) request.since = opts.since;
  const rawResponse = await queryGraphQL(GET_LLM_CONVERSATION_V3_QUERY, 'AiGetConversationV3', { request }, undefined, opts.signal);
  const payload = rawResponse?.data?.data?.ai_get_conversation_v3 ?? null;

  if (payload?.conversation?.status === 'COMPLETED') {
    const critiques = await api.listConversationCritiques({
      accountId: opts.accountId,
      conversationId: payload.conversation.id,
    });
    // Ride along on the same object that becomes rawConversationRef.current in
    // useLLMInvestigationControl.js, so the "download conversation JSON" button
    // (KubernetesLLMRequestResponseV2.jsx:handleDownloadConversation) includes it
    // without a separate fetch.
    if (rawResponse?.data?.data) {
      rawResponse.data.data.critiques = critiques?.data ?? [];
    }
  }

  return { rawResponse, payload };
}

const _sortByCreated = (a: { created_at: string }, b: { created_at: string }) =>
  a.created_at < b.created_at ? -1 : a.created_at > b.created_at ? 1 : 0;

// Build the legacy `llm_conversations[0]` shape from merged state Maps. Used by
// both the single-shot wrapper (Maps populated from a single response) and the
// stateful fetcher (Maps populated incrementally across poll deltas).
function _assembleConversationLegacyEnvelope(
  rawResponse: any,
  shell: ConversationV3Shell | null,
  messages: Map<string, ConversationV3Message>,
  agents: Map<string, ConversationV3Agent>,
  toolCalls: Map<string, ConversationV3ToolCall>
) {
  if (!rawResponse?.data?.data) return rawResponse;
  if (!shell) {
    rawResponse.data.data.llm_conversations = [];
    return rawResponse;
  }

  const agentsByMessage = new Map<string, ConversationV3Agent[]>();
  agents.forEach((a) => {
    const list = agentsByMessage.get(a.message_id) ?? [];
    list.push(a);
    agentsByMessage.set(a.message_id, list);
  });

  const toolsByAgent = new Map<string, ConversationV3ToolCall[]>();
  toolCalls.forEach((t) => {
    const list = toolsByAgent.get(t.agent_id) ?? [];
    list.push(t);
    toolsByAgent.set(t.agent_id, list);
  });

  const llmConversationMessages = Array.from(messages.values())
    .sort(_sortByCreated)
    .map((msg) => ({
      ...msg,
      user: msg.user_display_name ? { display_name: msg.user_display_name } : undefined,
      llm_conversation_agents: (agentsByMessage.get(msg.id) ?? []).sort(_sortByCreated).map((agent) => ({
        ...agent,
        llm_conversation_tool_calls: (toolsByAgent.get(agent.id) ?? []).sort(_sortByCreated),
      })),
    }));

  rawResponse.data.data.llm_conversations = [
    {
      id: shell.id,
      session_id: shell.session_id,
      account_id: shell.account_id,
      created_at: shell.created_at,
      updated_at: shell.updated_at,
      source: shell.source,
      context: shell.context,
      status: shell.status,
      user_id: shell.user_id,
      title: shell.title,
      user: { display_name: shell.user_display_name },
      llm_conversation_messages: llmConversationMessages,
    },
  ];
  return rawResponse;
}

// True when a cached conversation's last turn failed (every agent status contains 'fail'/'error') — a stale failure callers regenerate instead of re-serving; success/in-progress stay cached.
export function isFailedCachedConversation(conversations: any[]): boolean {
  const messages = conversations?.[0]?.llm_conversation_messages ?? [];
  const lastMessage = messages[messages.length - 1];
  const agents = lastMessage?.llm_conversation_agents ?? [];
  return (
    agents.length > 0 &&
    agents.every((a: any) => {
      const s = typeof a?.status === 'string' ? a.status.toLowerCase() : '';
      return s.includes('fail') || s.includes('error');
    })
  );
}

// Stateful fetcher: maintains shell + Maps + cursor across calls so each call
// is a delta fetch over the previous state. The first call (cursor null) loads
// everything; subsequent calls request only rows updated since the last cursor
// and merge into local state by id.
//
// Auto-resets when the bound (accountId, conversationId, sessionId) identity
// changes, so reusing one fetcher across sessions is safe.
//
// NOT thread-safe — concurrent fetch() calls can race the state mutation.
// The polling consumers in this codebase wait for the previous response before
// firing the next call, so this is fine in practice.
export type ConversationFetcher = {
  fetch: (opts: { accountId: string; conversationId?: string; sessionId?: string; signal?: AbortSignal }) => Promise<any>;
  reset: () => void;
};

export function createConversationFetcher(): ConversationFetcher {
  let bound: { accountId?: string; conversationId?: string; sessionId?: string } = {};
  let cursor: string | null = null;
  let shell: ConversationV3Shell | null = null;
  const messages = new Map<string, ConversationV3Message>();
  const agents = new Map<string, ConversationV3Agent>();
  const toolCalls = new Map<string, ConversationV3ToolCall>();

  const reset = () => {
    bound = {};
    cursor = null;
    shell = null;
    messages.clear();
    agents.clear();
    toolCalls.clear();
  };

  const fetch = async (opts: { accountId: string; conversationId?: string; sessionId?: string; signal?: AbortSignal }) => {
    const sameTarget = bound.accountId === opts.accountId && bound.conversationId === opts.conversationId && bound.sessionId === opts.sessionId;
    if (!sameTarget) {
      reset();
      bound = { accountId: opts.accountId, conversationId: opts.conversationId, sessionId: opts.sessionId };
    }

    const { rawResponse, payload } = await _callConversationV3({ ...opts, since: cursor });
    if (payload) {
      if (payload.conversation) shell = payload.conversation;
      payload.messages.forEach((m) => messages.set(m.id, m));
      payload.agents.forEach((a) => agents.set(a.id, a));
      payload.tool_calls.forEach((t) => toolCalls.set(t.id, t));
      // Server returns max(updated_at) across the full snapshot it pinned for
      // this call (REPEATABLE READ), so the cursor reflects everything we've
      // now consumed — including unchanged rows already in our state.
      if (payload.cursor) cursor = payload.cursor;
    }
    return _assembleConversationLegacyEnvelope(rawResponse, shell, messages, agents, toolCalls);
  };

  return { fetch, reset };
}

const api = {
  async askNudgebeeAiGeneratePrometheusQuery(data: GenerateQueryRequest) {
    if (data.account_id === 'demo') return null;
    const ASK_AI_GENERATE_PROMETHEUS_QUERY = `
        mutation AskNudgebeeAiGeneratePrometheusQuery {
          ai_generate_prometheus_query(request: __REQUEST__) {
            data {
              agent_step_response
              response
              query
              chain_name
              conversation_id
              session_id
            }
          }
        }
        `;
    const query: any = {};
    query.account_id = data.account_id;
    query.query = data.query;
    query.async = true;
    const response = await queryGraphQL(
      ASK_AI_GENERATE_PROMETHEUS_QUERY.replace('__REQUEST__', gqlStringify(query)),
      'AskNudgebeeAiGeneratePrometheusQuery',
      {}
    );
    return response;
  },
  async askAiGenerateLogQuery(data: GenerateQueryRequest) {
    if (data.account_id === 'demo') return null;
    const ASK_AI_GENERATE_LOG_QUERY = `
        mutation AskAiGenerateLogQuery {
          ai_generate_log_query(request: __REQUEST__) {
            data {
              agent_step_response
              response
              query
              chain_name
              conversation_id
              session_id
            }
          }
        }
        `;
    const query: any = {};
    query.account_id = data.account_id;
    query.query = data.query;
    query.async = true;
    if (data.log_provider) {
      query.log_provider = data.log_provider;
    }
    const response = await queryGraphQL(ASK_AI_GENERATE_LOG_QUERY.replace('__REQUEST__', gqlStringify(query)), 'AskAiGenerateLogQuery', {});
    return response;
  },
  async createAiFeedback(data: AiFeedbackCreateRequest) {
    const CREATE_AI_FEEDBACK = `
        mutation CreateAiFeedback($data: AiFeedbackCreateRequest!) {
          ai_feedback_create(request: $data) {
            data {
              success
            }
          }
        }
        `;
    try {
      const response = await queryGraphQL(CREATE_AI_FEEDBACK, 'CreateAiFeedback', {
        data: data,
      });
      return response;
    } catch (error) {
      console.error('failed to create ai feedback-', error);
      return null;
    }
  },
  async listAiFeedback(queryRequest: ListAiFeedbackRequest = {}) {
    if (queryRequest?.cloud_account_id === 'demo') return null;
    const LIST_AI_FEEDBACK = `
        query ListAiFeedback {
          llm_conversation_feedback: ai_list_conversation_feedback(where: __WHERE__, order_by: [{column: "created_at", order: desc}]) {
            rows {
              id
              created_at
              session_id
              module
              question
              llm_response
              user_corrected_response
              useful
              additional_notes
              conversation_id
              user_id
              cloud_account_id
            }
          }
        }
        `;
    try {
      let query: any = {};
      if (queryRequest.module) {
        query.module = { _eq: queryRequest.module };
      }
      if (queryRequest.useful !== undefined) {
        query.useful = { _eq: queryRequest.useful };
      }
      if (queryRequest.question) {
        query.question = { _ilike: '%' + queryRequest.question + '%' };
      }
      if (queryRequest.user_id) {
        query.user_id = { _eq: queryRequest.user_id };
      }
      if (queryRequest.cloud_account_id) {
        query.cloud_account_id = { _eq: queryRequest.cloud_account_id };
      }
      if (queryRequest.start_time || queryRequest.end_time) {
        const dateConditions = [];
        if (queryRequest.start_time) dateConditions.push({ created_at: { _gte: new Date(queryRequest.start_time).toISOString() } });
        if (queryRequest.end_time) dateConditions.push({ created_at: { _lte: new Date(queryRequest.end_time).toISOString() } });
        query._and = dateConditions;
      }

      const gqlQuery = LIST_AI_FEEDBACK.replace('__WHERE__', gqlStringify(query));

      const response = await queryGraphQL(gqlQuery, 'ListAiFeedback', {});
      return response;
    } catch (error) {
      console.error('failed to list ai feedback-', error);
      return null;
    }
  },
  async aiGenerateInvestigate(data: InvestigateRequest) {
    if (data.account_id === 'demo') {
      return {
        data: {
          data: null,
          errors: [{ message: 'Demo account does not have access.' }],
        },
      };
    }
    const AI_TRIGGER_INVESTIGATE_RESPONSE = `
        mutation AiTriggerInvestigateResponse {
          ai_execute_investigation(request: __REQUEST__) {
            data {
              agent_step_response
              response
              query
              chain_name
            }
          }
        }
        `;
    const query: any = {};
    query.account_id = data.account_id;
    query.query = data.query;
    if (data.session_id) {
      query.session_id = data.session_id;
    }
    if (data.conversation_id) {
      query.conversation_id = data.conversation_id;
    }
    if (data.agent_id) {
      query.agent_id = data.agent_id;
    }
    if (data.message_id) {
      query.message_id = data.message_id;
    }
    if (data.config) {
      query.config = data.config;
    }
    if (data.source) {
      query.source = data.source;
    }
    if (data.images?.length) {
      query.images = data.images;
    }
    query.async = true;
    const response = await queryGraphQL(
      AI_TRIGGER_INVESTIGATE_RESPONSE.replace('__REQUEST__', gqlStringify(query)),
      'AiTriggerInvestigateResponse',
      {}
    );
    return response;
  },
  async aiFollowupResponse(data: FollowupRequest) {
    if (data.account_id === 'demo') return null;
    const AI_FOLLOWUP_RESPONSE = `
        mutation AiFollowupResponse {
          ai_get_followup_response(request: __REQUEST__) {
            data {
              response
            }
          }
        }
        `;
    const query: any = {};
    query.account_id = data.account_id;
    query.query = data.query;
    query.conversation_id = data.conversation_id;
    query.agent_id = data.agent_id;
    query.message_id = data.message_id;
    query.async = true;

    const response = await queryGraphQL(AI_FOLLOWUP_RESPONSE.replace('__REQUEST__', gqlStringify(query)), 'AiFollowupResponse', {});
    return response;
  },
  async llmConversationHistory(data: ConversationHistoryRequest) {
    if (data.account_id === 'demo') return null;
    // total_count maps to COUNT(*) OVER() in the derived view; for unbounded
    // listings (e.g. the sidebar) it forces a full per-row sweep before LIMIT,
    // so callers that paginate via "did we get a full page?" should opt out.
    const includeTotalCount = !data.skipTotalCount;
    const GET_LLM_CONVERSATION_HISTORY = `
          query LlMConversationHistory($where: LlmConversationListWhereRequest, $limit: Int, $offset: Int) {
            ai_list_conversations(where: $where, order_by: [{column: "updated_at", order: desc}], limit: $limit, offset: $offset) {
              rows {
                id
                updated_at
                status
                user_id
                session_id
                created_at
                source
                title
                user_display_name
                user_username
                for_status
                is_saved${includeTotalCount ? '\n                total_count' : ''}
              }
            }
          }
        `;

    const where: any = {};
    where.account_id = { _eq: data.account_id };
    if (data.latestLastRecordedAt) {
      where.updated_at = { _gt: data.latestLastRecordedAt };
    }

    if (Array.isArray(data.source)) {
      where.source = { _in: data.source };
    } else if (data.source) {
      where.source = { _eq: data.source };
    }
    if (data.activeFilter == 'Mine') {
      where.user_username = { _eq: getUserSession().user.email };
    } else if (data.activeFilter == 'Saved') {
      where.is_saved = { _eq: true };
    } else if (data.activeFilter == 'Waiting') {
      where.status = { _eq: 'WAITING' };
    }
    if (data.status) {
      where.status = { _eq: data.status };
    }
    if (data.user_username) {
      where.user_username = { _eq: data.user_username };
    }
    if (data.user_username_neq) {
      where.user_username = { _neq: data.user_username_neq };
    }
    if (data.source_not_in) {
      where.source = { ...where.source, _not_in: data.source_not_in };
    }
    if (data.searchText) {
      where._or = [{ message_search: { _ilike: `%${data.searchText}%` } }, { title: { _ilike: `%${data.searchText}%` } }];
    }

    const response = await queryGraphQL(GET_LLM_CONVERSATION_HISTORY, 'LlMConversationHistory', {
      where,
      limit: data.limit,
      offset: data.offset,
    });

    // Transform v2 response to maintain backward compatibility
    const rows = response?.data?.data?.ai_list_conversations?.rows || [];
    const totalCount = includeTotalCount && rows.length > 0 ? rows[0].total_count : 0;
    if (response?.data?.data) {
      response.data.data.llm_conversations = rows.map((row: any) => {
        const forStatus = typeof row.for_status === 'string' ? JSON.parse(row.for_status) : row.for_status;
        return {
          id: row.id,
          updated_at: row.updated_at,
          status: row.status,
          user_id: row.user_id,
          session_id: row.session_id,
          created_at: row.created_at,
          source: row.source,
          title: row.title,
          user: { display_name: row.user_display_name, username: row.user_username },
          for_status: forStatus ? [forStatus] : [],
          llm_conversation_saveds: row.is_saved ? [{ conversation_id: row.id }] : [],
        };
      });
      if (includeTotalCount) {
        response.data.data.llm_conversations_aggregate = { aggregate: { count: totalCount } };
      }
    }

    return response;
  },
  // getLlmConversation is a thin wrapper over the v3 delta-fetch action.
  // Calling without `since` does a full fetch (cursor = epoch backend-side):
  // 4 flat indexed queries instead of one 3-level nested SubPlan tree, no
  // JSON-built-in-DB. Response envelope is stable for existing consumers.
  async getLlmConversation({
    conversationId,
    accountId,
    sessionId,
    signal,
  }: {
    conversationId?: string;
    accountId?: string;
    sessionId?: string;
    signal?: AbortSignal;
  }) {
    if (accountId === 'demo') return null;
    if (!accountId) {
      throw new Error('getLlmConversation: accountId is required');
    }
    return api.getLlmConversationV3({ accountId, conversationId, sessionId, signal });
  },
  // Single-shot v3 fetch. For polling consumers that need delta semantics
  // across calls, use createConversationFetcher() instead.
  async getLlmConversationV3({
    accountId,
    conversationId,
    sessionId,
    since,
    signal,
  }: {
    accountId: string;
    conversationId?: string;
    sessionId?: string;
    since?: string | null;
    signal?: AbortSignal;
  }) {
    if (accountId === 'demo') return null;
    const { rawResponse, payload } = await _callConversationV3({ accountId, conversationId, sessionId, since, signal });
    const messages = new Map<string, ConversationV3Message>();
    const agents = new Map<string, ConversationV3Agent>();
    const toolCalls = new Map<string, ConversationV3ToolCall>();
    if (payload) {
      payload.messages.forEach((m) => messages.set(m.id, m));
      payload.agents.forEach((a) => agents.set(a.id, a));
      payload.tool_calls.forEach((t) => toolCalls.set(t.id, t));
    }
    return _assembleConversationLegacyEnvelope(rawResponse, payload?.conversation ?? null, messages, agents, toolCalls);
  },
  async getLlmConversationPolling({
    accountId,
    sessionId,
    conversationId,
    signal,
  }: {
    accountId?: string;
    sessionId?: string;
    conversationId?: string;
    signal?: AbortSignal;
  }) {
    if (accountId === 'demo') return null;
    const GET_LLM_CONVERSATION_POLLING = `
        query GetLlmConversationPolling($where: LlmConversationDetailWhereRequest) {
          ai_get_conversation_detail_polling(where: $where) {
            rows {
              id
              session_id
              account_id
              created_at
              updated_at
              source
              context
              status
              user_id
              title
              user_display_name
              messages
            }
          }
        }
        `;
    const where: any = {};
    if (accountId) {
      where.account_id = { _eq: accountId };
    }
    if (sessionId) {
      where.session_id = { _eq: sessionId };
    }
    if (conversationId) {
      where.id = { _eq: conversationId };
    }
    const response = await queryGraphQL(GET_LLM_CONVERSATION_POLLING, 'GetLlmConversationPolling', { where }, undefined, signal);

    const rows = response?.data?.data?.ai_get_conversation_detail_polling?.rows || [];
    if (response?.data?.data) {
      response.data.data.llm_conversations = rows.map((row: any) => {
        const rawMessages = typeof row.messages === 'string' ? JSON.parse(row.messages) : row.messages || [];
        const messages = rawMessages.map((msg: any) => ({
          ...msg,
          user: msg.user_display_name ? { display_name: msg.user_display_name } : undefined,
        }));
        return {
          id: row.id,
          session_id: row.session_id,
          account_id: row.account_id,
          created_at: row.created_at,
          updated_at: row.updated_at,
          source: row.source,
          context: row.context,
          status: row.status,
          user_id: row.user_id,
          title: row.title,
          user: { display_name: row.user_display_name },
          llm_conversation_messages: messages,
        };
      });
    }

    return response;
  },
  async getFeedbackForSessionId(data: FeedbackForSessionRequest) {
    if (data.account_id === 'demo') return null;
    // `session_id` throughout this call (and the ai_list_conversation_feedback.session_id
    // column it filters on) is actually a per-message id, not a conversation/session id —
    // single callers pass a message id (e.g. KubernetesLLMRequestResponseV2's toolCall.id),
    // and the batched caller (useMessageAdditionalData) passes an array of message ids.
    // No `limit` here — a batched call (session_id as an array) needs one row per message, and
    // a limit applies to the whole result set rather than per session_id. Rows come back
    // ordered by updated_at desc, so we keep only the first (latest) row per session_id below
    // to preserve the "one row per message" contract single callers rely on.
    const GET_FEEBACK_FOR_SESSION_ID = `
        query LLMFeedback {
          ai_list_conversation_feedback(where: __WHERE__, order_by: {column: "updated_at", order: desc}) {
            rows {
              useful
              updated_at
              module
              additional_notes
              session_id
            }
          }
        }
        `;
    const query: any = {};
    query.cloud_account_id = { _eq: data.account_id };
    if (Array.isArray(data.session_id)) {
      query.session_id = { _in: data.session_id };
    } else {
      query.session_id = { _eq: data.session_id };
    }
    const response = await queryGraphQL(GET_FEEBACK_FOR_SESSION_ID.replace('__WHERE__', gqlStringify(query)), 'LLMFeedback', {});
    const feedbackResult = response?.data?.data?.ai_list_conversation_feedback;
    if (feedbackResult && Array.isArray(feedbackResult.rows)) {
      const seenSessionIds = new Set();
      feedbackResult.rows = feedbackResult.rows.filter((row: any) => {
        if (seenSessionIds.has(row.session_id)) return false;
        seenSessionIds.add(row.session_id);
        return true;
      });
    }
    return response;
  },
  async saveConversation(data: SaveConversationRequest) {
    const SAVE_CONVERSATION = `
    mutation SaveConversation($data: SaveLLMConversationRequest!) {
      ai_create_saved_conversation(request: $data) {
        data {
          success
        }
      }
    }
    `;
    try {
      const response = await queryGraphQL(SAVE_CONVERSATION, 'SaveConversation', {
        data: data,
      });
      return response;
    } catch (error) {
      console.error('failed to save conversation-', error);
      return null;
    }
  },
  async listAgents(data: ListAgentsRequest) {
    if (data.accountId === 'demo') return null;
    const LIST_AGENTS = `
    query ListAgents {
      ai_list_agents(request: __WHERE__) {
        data
      }
    }
    `;
    const query: any = {};
    query.account_id = data.accountId;
    try {
      const response = await queryGraphQL(LIST_AGENTS.replaceAll('__WHERE__', gqlStringify(query)), 'ListAgents', {});
      return response;
    } catch (error) {
      console.error('failed to fetch agent list-', error);
      return null;
    }
  },
  async listTools(data: ListToolsRequest) {
    if (data.accountId == 'demo') {
      return { data: { data: { ai_list_tools: { data: [] } } } };
    }
    const LIST_TOOLS = `
    query ListTools {
      ai_list_tools(request: __WHERE__) {
        data
      }
    }
    `;
    const query: any = {};
    query.account_id = data.accountId;
    try {
      const response = await queryGraphQL(LIST_TOOLS.replaceAll('__WHERE__', gqlStringify(query)), 'ListTools', {});
      return response;
    } catch (error) {
      console.error('failed to fetch tool list-', error);
      return null;
    }
  },
  async createAgent(data: AgentDefinition) {
    if (data?.account_id === 'demo') {
      return {
        data: {
          data: null,
          errors: [{ message: 'Demo account does not have access.' }],
        },
      };
    }
    const CREATE_AGENT = `
    mutation AiCreateAgent {
      ai_create_agent(request: __WHERE__) {
        data
        errors
      }
    }
    `;
    try {
      const response = await queryGraphQL(CREATE_AGENT.replaceAll('__WHERE__', gqlStringify(data)), 'AiCreateAgent', {});
      return response;
    } catch (error) {
      console.error('failed to create agent-', error);
      return null;
    }
  },
  async createTool(data: ToolDefinition) {
    if (data?.account_id === 'demo') {
      return {
        data: {
          data: null,
          errors: [{ message: 'Demo account does not have access.' }],
        },
      };
    }
    const CREATE_TOOL = `
    mutation AiCreateTool {
      ai_create_tool(request: __WHERE__) {
        data
        errors
      }
    }
    `;
    try {
      const response = await queryGraphQL(CREATE_TOOL.replaceAll('__WHERE__', gqlStringify(data)), 'AiCreateTool', {});
      return response;
    } catch (error) {
      console.error('failed to create tool-', error);
      return null;
    }
  },
  async deleteConversation(data: DeleteConversationRequest) {
    const DELETE_CONVERSATION = `
    mutation DeleteConversation($data: DeleteLlmConversationByIdRequest!) {
      ai_delete_llm_conversation_by_id(request: $data) {
        data {
          success
        }
      }
    }
    `;
    try {
      const response = await queryGraphQL(DELETE_CONVERSATION, 'DeleteConversation', {
        data: data,
      });
      return response;
    } catch (error) {
      console.error('failed to delete conversation-', error);
      return null;
    }
  },
  async deleteSavedConversation(data: DeleteSavedConversationRequest) {
    const DELETE_SAVED_CONVERSATION = `
    mutation DeleteSavedConversation($data: DeleteLLMConversationRequest!) {
      ai_delete_saved_conversation(request: $data) {
        data {
          success
        }
      }
    }
    `;
    try {
      const response = await queryGraphQL(DELETE_SAVED_CONVERSATION, 'DeleteSavedConversation', {
        data: data,
      });
      return response;
    } catch (error) {
      console.error('failed to delete saved conversation-', error);
      return null;
    }
  },
  async updateAgent(data: AgentDefinition) {
    if (data?.account_id === 'demo') {
      return {
        data: {
          data: null,
          errors: [{ message: 'Demo account does not have access.' }],
        },
      };
    }
    const UPDATE_AGENT = `
    mutation AiUpdateAgent {
      ai_update_agent(request: __WHERE__) {
        data
        errors
      }
    }
    `;
    try {
      const response = await queryGraphQL(UPDATE_AGENT.replaceAll('__WHERE__', gqlStringify(data)), 'AiUpdateAgent', {});
      return response;
    } catch (error) {
      console.error('failed to update agent-', error);
      return null;
    }
  },
  async updateTool(data: ToolDefinition) {
    if (data?.account_id === 'demo') {
      return {
        data: {
          data: null,
          errors: [{ message: 'Demo account does not have access.' }],
        },
      };
    }
    const UPDATE_TOOL = `
    mutation AiUpdateTool {
      ai_update_tool(request: __WHERE__) {
        data
        errors
      }
    }
    `;
    try {
      const response = await queryGraphQL(UPDATE_TOOL.replaceAll('__WHERE__', gqlStringify(data)), 'AiUpdateTool', {});
      return response;
    } catch (error) {
      console.error('failed to update tool-', error);
      return null;
    }
  },
  async getConversationSuggestions(data: ConversationSuggestionsRequest) {
    if (data?.account_id === 'demo') return null;
    const GET_CONVERSATION_SUGGESTIONS = `
    mutation GetConversationSuggestions($data: AIGetConversationSuggestionRequest!) {
      ai_list_conversation_suggestions(request: $data) {
        data
      }
    }
    `;
    try {
      const response = await queryGraphQL(GET_CONVERSATION_SUGGESTIONS, 'GetConversationSuggestions', {
        data: data,
      });
      return response;
    } catch (error) {
      console.error('failed to get conversation suggestions-', error);
      return null;
    }
  },
  async createRagData(data: CreateRagDataRequest) {
    if (data?.account_id === 'demo') {
      return {
        data: {
          data: null,
          errors: [{ message: 'Demo account does not have access.' }],
        },
      };
    }
    const CREATE_RAG_DATA = `
      mutation AiCreateRagData($data: CreateAgentRagInput!) {
        ai_create_rag(request: $data) {
         data
        }
      }
    `;

    try {
      const response = await queryGraphQL(CREATE_RAG_DATA, 'AiCreateRagData', {
        data: data,
      });
      return response;
    } catch (error) {
      console.error('failed to create rag data-', error);
      return null;
    }
  },
  async listFunctions(data: ListFunctionsRequest) {
    if (data.accountId === 'demo') return { res: { llm_functions: [] }, errors: [] };
    const GET_FUNCTIONS = `
      query GetFunctions($where: LlmFunctionsWhereRequest) {
        llm_functions: ai_list_functions(where: $where) {
          rows {
            id
            name
            description
            prompt
            variables
            variable_defaults
            status
            version
            account_id
            created_by
            updated_by
            created_at
            updated_at
          }
        }
      }
    `;

    try {
      // Empty accountId == tenant-wide read (b-Cortex sidebar surface).
      // Omit the account_id filter and let api-server's query service apply
      // its standard row-level account ACL automatically (tenant/super
      // admins see all in tenant; account-admins see only their assigned
      // accounts; everyone else 403s).
      const where: any = data.accountId ? { account_id: { _eq: data.accountId } } : {};
      const response = await queryGraphQL(GET_FUNCTIONS, 'GetFunctions', { where });
      const rows = response?.data?.data?.llm_functions?.rows || [];

      return {
        res: { llm_functions: rows },
        errors: response?.data?.errors,
      };
    } catch (error) {
      console.error('failed to get functions-', error);
      return null;
    }
  },
  async createAiFunction(data: AiFunctionData, accountId: string) {
    if (accountId === 'demo') {
      return {
        success: false,
        error: 'Demo account does not have access.',
      };
    }
    const CREATE_AI_FUNCTION = `
      mutation CreateAiFunction($account_id: String!) {
        ai_create_function(
          account_id: $account_id,
          function: __WHERE__
        ) {
          data{
            success
            message
            function {
              id
              name
              description
            }
          }
        }
      }
    `;
    try {
      const response = await queryGraphQL(CREATE_AI_FUNCTION.replaceAll('__WHERE__', gqlStringify(data)), 'CreateAiFunction', {
        account_id: accountId,
      });

      // Check for GraphQL errors first
      if (response.data?.errors && response.data.errors.length > 0) {
        const error = response.data.errors[0];

        return { success: false, error: error.message };
      }

      // Check for successful response
      if (response.data?.data?.ai_create_function?.data?.success) {
        return {
          success: true,
        };
      } else if (response.data?.data?.ai_create_function?.data?.message) {
        return { success: false, error: response.data.data.ai_create_function.data.message };
      }
      return { success: false, error: 'Unknown error occurred' };
    } catch (error) {
      console.error('failed to create ai function-', error);
      return { success: false, error: 'Network error occurred' };
    }
  },
  async aiStopInvestigate(data: StopInvestigateRequest) {
    if (data.accountId === 'demo') return null;
    const AI_STOP_INVESTIGATE = `
        mutation AiStopInvestigation($accountId: String!, $conversationId: String!) {
          ai_cancel_investigation(request: {account_id: $accountId, conversation_id: $conversationId}) {
            data
          }
        }
    `;
    const response = await queryGraphQL(AI_STOP_INVESTIGATE, 'AiStopInvestigation', {
      accountId: data.accountId,
      conversationId: data.conversationId,
    });
    return response;
  },
  async getConversationUsageMetrics(accountId: string, conversationId: string) {
    if (accountId === 'demo') return null;
    const query = `mutation GetConversationUsageMetrics($accountId:String!, $conversationId: String!) {
      ai_get_conversation_usage_metrics(request: {account_id: $accountId, conversation_id: $conversationId}) {
        data
      }
    }
    `;
    const response = await queryGraphQL(query, 'GetConversationUsageMetrics', {
      accountId: accountId,
      conversationId: conversationId,
    });

    return response?.data?.data?.ai_get_conversation_usage_metrics?.data;
  },
  // accountId is optional — pass an empty string to roll up across every
  // account the caller's session is permitted to read (the troubleshoot
  // dashboard widget uses this multi-account mode). Pass an explicit
  // accountId to scope to one. The action input field is non-null on the
  // GraphQL side, so we send '' rather than omitting it.
  async getConversationTimeAggregates(data: { accountId?: string; startDate: string; endDate: string; sources?: string[]; eventScoped?: boolean }) {
    if (data.accountId === 'demo') return null;
    const query = `mutation GetConversationTimeAggregates(
      $accountId: String!
      $startDate: String!
      $endDate: String!
      $sources: [String!]
      $eventScoped: Boolean
    ) {
      ai_get_conversation_time_aggregates(request: {
        account_id: $accountId,
        start_date: $startDate,
        end_date: $endDate,
        sources: $sources,
        event_scoped: $eventScoped
      }) {
        data
      }
    }
    `;
    const response = await queryGraphQL(query, 'GetConversationTimeAggregates', {
      accountId: data.accountId ?? '',
      startDate: data.startDate,
      endDate: data.endDate,
      sources: data.sources ?? null,
      eventScoped: data.eventScoped ?? null,
    });

    return response?.data?.data?.ai_get_conversation_time_aggregates?.data;
  },
  async deleteAgent(accountId: string, agentName: string) {
    if (accountId === 'demo') return null;
    const DELETE_AGENT = `mutation AiDeleteAgent($accountId:String!, $name:String!) {
      ai_delete_agent(request: {account_id: $accountId, name: $name}) {
        data
        errors
      }
    }`;
    try {
      const response = await queryGraphQL(DELETE_AGENT, 'AiDeleteAgent', { accountId: accountId, name: agentName });
      return response;
    } catch (error) {
      console.error('failed to delete agent-', error);
      return null;
    }
  },
  async deleteTool(accountId: string, toolName: string) {
    if (accountId === 'demo') return null;
    const DELETE_TOOL = `mutation AiDeleteTool($accountId:String!, $name:String!) {
      ai_delete_tool(request: {account_id: $accountId, name: $name}) {
        data
        errors
      }
    }`;
    try {
      const response = await queryGraphQL(DELETE_TOOL, 'AiDeleteTool', { accountId: accountId, name: toolName });
      return response;
    } catch (error) {
      console.error('failed to delete tool-', error);
      return null;
    }
  },
  async createAgentExtension(data: CreateAgentExtensionRequest) {
    if (data?.account_id === 'demo') {
      return {
        data: {
          data: null,
          errors: [{ message: 'Demo account does not have access.' }],
        },
      };
    }
    const CREATE_AGENT_EXTENSION = `
    mutation AiCreateAgentExtension {
      ai_create_agent_extension(request: __REQUEST__) {
        data
        err
      }
    }
    `;
    try {
      const response = await queryGraphQL(CREATE_AGENT_EXTENSION.replace('__REQUEST__', gqlStringify(data)), 'AiCreateAgentExtension', {});
      return response;
    } catch (error) {
      console.error('failed to create agent extension-', error);
      return null;
    }
  },
  async updateAgentExtension(data: UpdateAgentExtensionRequest) {
    if (data?.account_id === 'demo') {
      return {
        data: {
          data: null,
          errors: [{ message: 'Demo account does not have access.' }],
        },
      };
    }
    const UPDATE_AGENT_EXTENSION = `
    mutation AiUpdateAgentExtension {
      ai_update_agent_extension(request: __REQUEST__) {
        data
        err
      }
    }
    `;
    try {
      const response = await queryGraphQL(UPDATE_AGENT_EXTENSION.replace('__REQUEST__', gqlStringify(data)), 'AiUpdateAgentExtension', {});
      return response;
    } catch (error) {
      console.error('failed to update agent extension-', error);
      return null;
    }
  },
  async listAgentExtensions(accountId: string) {
    if (accountId == 'demo') {
      return {
        data: [],
      };
    }
    const LIST_AGENT_EXTENSIONS = `
    query ListAgentExtensions($where: LlmAgentsInstallationWhereRequest) {
      ai_list_agent_installations(where: $where) {
        rows {
          additional_instructions
          agent_id
          config
          created_at
          created_by
          id
          tools
          account_id
        }
      }
    }
    `;
    try {
      const response = await queryGraphQL(LIST_AGENT_EXTENSIONS, 'ListAgentExtensions', {
        where: { account_id: { _eq: accountId } },
      });
      const rows = response.data?.data?.ai_list_agent_installations?.rows || [];
      return {
        data: rows.map((r: any) => ({
          ...r,
          config: typeof r.config === 'string' ? JSON.parse(r.config) : r.config,
          tools: typeof r.tools === 'string' ? JSON.parse(r.tools) : r.tools,
        })),
        errors: response.data?.errors || [],
      };
    } catch (error) {
      console.error('failed to list agent extensions-', error);
      return { data: [], errors: [error] };
    }
  },
  async deleteLLMFunction({ id, accountId }: { id: string; accountId: string }) {
    if (accountId === 'demo') {
      return {
        data: null,
        errors: [{ message: 'Demo account does not have access.' }],
      };
    }
    const DELETE_LLM_FUNCTION = `mutation AiDeleteFunction($accountId: String!, $functionId:String!){
      ai_delete_function(request:{account_id:$accountId, function_id:$functionId}){
    data
      }
    }`;

    try {
      const response = await queryGraphQL(DELETE_LLM_FUNCTION, 'AiDeleteFunction', {
        accountId: accountId,
        functionId: id,
      });
      return {
        data: response.data?.data?.ai_delete_function.data || [],
        errors: response.data?.errors || [],
      };
    } catch (error) {
      console.error('failed to delete llm function-', error);
      return { data: [], errors: [error] };
    }
  },
  async updateFunction({ accountId, functionId, data }: { accountId?: string; functionId: string; data: UpdateFunctionData }) {
    if (accountId === 'demo') {
      return {
        success: false,
        error: 'Demo account does not have access.',
      };
    }
    const UPDATE_AI_FUNCTION = `
      mutation AiEditFunction($account_id: String!, $function_id: String!) {
        ai_update_function(
          account_id: $account_id,
          function_id: $function_id,
          function: __WHERE__
        ) {
          data
        }
      }
    `;
    try {
      const response = await queryGraphQL(UPDATE_AI_FUNCTION.replaceAll('__WHERE__', gqlStringify(data)), 'AiEditFunction', {
        account_id: accountId,
        function_id: functionId,
      });

      // Check for GraphQL errors first
      if (response.data?.errors && response.data.errors.length > 0) {
        const error = response.data.errors[0];
        return { success: false, error: error.message };
      }

      // Check for successful response
      if (response.data?.data?.ai_update_function?.data?.success) {
        return {
          success: true,
        };
      } else if (response.data?.data?.ai_update_function?.data?.message) {
        return { success: false, error: response.data.data.ai_update_function.data.message };
      }
      return { success: false, error: 'Unknown error occurred' };
    } catch (error) {
      console.error('failed to update ai function-', error);
      return { success: false, error: 'Network error occurred' };
    }
  },
  async llmConversationHistoryForInvestigation(data: ConversationHistoryForInvestigationRequest) {
    if (data.account_id === 'demo') return null;
    const GET_LLM_CONVERSATION_HISTORY = `
          query LlMConversationHistory($where: LlmConversationListWhereRequest, $limit: Int, $offset: Int) {
            ai_list_conversations(where: $where, order_by: [{column: "updated_at", order: desc}], limit: $limit, offset: $offset) {
              rows {
                id
                updated_at
                account_id
                status
                user_id
                session_id
                created_at
                source
                title
                user_display_name
                user_username
                total_count
              }
            }
          }
        `;

    const where: any = {};
    if (data.account_id) {
      if (Array.isArray(data.account_id) && data.account_id.length) {
        where.account_id = { _in: data.account_id };
      } else if (typeof data.account_id === 'string') {
        where.account_id = { _eq: data.account_id };
      }
    }
    if (data.latestLastRecordedAt) {
      where.updated_at = { _gt: data.latestLastRecordedAt };
    }
    if (data.startUpdatedAt || data.endUpdatedAt) {
      const andConditions: any[] = [];
      if (data.startUpdatedAt) andConditions.push({ updated_at: { _gte: data.startUpdatedAt } });
      if (data.endUpdatedAt) andConditions.push({ updated_at: { _lte: data.endUpdatedAt } });
      where._and = andConditions;
    }
    if (data.status) {
      where.status = { _eq: data.status };
    }
    if (data.user_id) {
      where.user_id = { _eq: data.user_id };
    }

    if (Array.isArray(data.source)) {
      where.source = { _in: data.source };
    } else if (data.source) {
      where.source = { _eq: data.source };
    }
    if (data.session_id) {
      where.session_id = { _eq: data.session_id };
    }
    where.title = { _neq: EVENT_DETAILS_RETRIEVAL_TITLE };
    if (data.title) {
      where._or = [{ message_search: { _ilike: `%${data.title}%` } }, { title: { _ilike: `%${data.title}%` } }];
    }
    if (data.extractEventIdsFromTitle) {
      where.extract_event_ids_from_title = { _eq: true };
    }
    if (data.event_status) {
      where.event_status = { _eq: data.event_status };
    }

    const response = await queryGraphQL(GET_LLM_CONVERSATION_HISTORY, 'LlMConversationHistory', {
      where,
      limit: data.limit,
      offset: data.offset,
    });

    // Transform v2 response to maintain backward compatibility
    const rows = response?.data?.data?.ai_list_conversations?.rows || [];
    const totalCount = rows.length > 0 ? rows[0].total_count : 0;
    if (response?.data?.data) {
      response.data.data.llm_conversations = rows.map((row: any) => ({
        id: row.id,
        updated_at: row.updated_at,
        account_id: row.account_id,
        status: row.status,
        user_id: row.user_id,
        session_id: row.session_id,
        created_at: row.created_at,
        source: row.source,
        title: row.title,
        user: { display_name: row.user_display_name, username: row.user_username },
      }));
      response.data.data.llm_conversations_aggregate = { aggregate: { count: totalCount } };
    }

    return response;
  },
  async llmConversationCount(data: { account_id: string; status?: string }) {
    if (data.account_id === 'demo') return 0;
    const GET_LLM_CONVERSATION_COUNT = `
      query LlmConversationCount($where: LlmConversationListWhereRequest) {
        ai_list_conversations(where: $where, limit: 1) {
          rows {
            total_count
          }
        }
      }
    `;

    const where: any = { account_id: { _eq: data.account_id } };
    if (data.status) {
      where.status = { _eq: data.status };
    }

    const response = await queryGraphQL(GET_LLM_CONVERSATION_COUNT, 'LlmConversationCount', { where });
    const rows = response?.data?.data?.ai_list_conversations?.rows || [];
    return rows.length > 0 ? rows[0].total_count : 0;
  },
  llmConversationComparsion: async function (data: ConversationComparisonRequest) {
    const LLM_CONVERSATION_GROUPINGS = `
    query LLMConversationGroupings($where: LlmConversationGroupingsWhereRequest) {
      ai_aggregate_conversations(where: $where) {
        rows {
          count
        }
      }
    }
    `;

    const currentWhere: any = {};
    const previousWhere: any = {};

    currentWhere._and = [{ updated_at: { _gte: data.startDate } }, { updated_at: { _lte: data.endDate } }];
    previousWhere._and = [{ updated_at: { _gte: data.previousStartDate } }, { updated_at: { _lte: data.previousEndDate } }];

    if (Array.isArray(data.source)) {
      currentWhere.source = { _in: data.source };
      previousWhere.source = { _in: data.source };
    } else if (data.source) {
      currentWhere.source = { _eq: data.source };
      previousWhere.source = { _eq: data.source };
    }
    currentWhere.title = { _neq: EVENT_DETAILS_RETRIEVAL_TITLE };
    previousWhere.title = { _neq: EVENT_DETAILS_RETRIEVAL_TITLE };

    if (data.extractEventIdsFromTitle) {
      currentWhere.extract_event_ids_from_title = { _eq: true };
      previousWhere.extract_event_ids_from_title = { _eq: true };
    }

    try {
      const [currentRes, previousRes] = await Promise.all([
        queryGraphQL(LLM_CONVERSATION_GROUPINGS, 'LLMConversationGroupings', { where: currentWhere }),
        queryGraphQL(LLM_CONVERSATION_GROUPINGS, 'LLMConversationGroupings', { where: previousWhere }),
      ]);

      const currentCount = currentRes?.data?.data?.ai_aggregate_conversations?.rows?.[0]?.count ?? 0;
      const previousCount = previousRes?.data?.data?.ai_aggregate_conversations?.rows?.[0]?.count ?? 0;

      return {
        data: {
          data: {
            current: { aggregate: { count: currentCount } },
            previous: { aggregate: { count: previousCount } },
          },
        },
      };
    } catch (err) {
      console.error('Error in LLMConversationComparison:', err);
      throw err;
    }
  },
  async listMemory(accountId: string, conversationId?: string, messageId?: string, memoryType?: string, query?: string, messageIds?: string[]) {
    if (accountId === 'demo') return null;
    const LIST_MEMORY = `
    query ListMemory($request: ListAIMemoryRequest!) {
      ai_list_memory(request: $request) {
        data {
          id
          account_id
          conversation_id
          message_id
          content
          memory_type
          created_at
        }
        errors {
          message
          code
        }
      }
    }
    `;
    try {
      const request: any = {
        account_id: accountId,
        limit: 100,
        offset: 0,
      };
      if (conversationId) {
        request.conversation_id = conversationId;
      }
      if (messageIds?.length) {
        request.message_ids = messageIds;
      } else if (messageId) {
        request.message_id = messageId;
      }
      if (memoryType) {
        request.memory_type = memoryType;
      }
      if (query) {
        request.query = query;
      }
      const response = await queryGraphQL(LIST_MEMORY, 'ListMemory', {
        request: request,
      });
      return {
        data: response.data?.data?.ai_list_memory?.data || [],
        errors: response.data?.data?.ai_list_memory?.errors || response.data?.errors || [],
      };
    } catch (error) {
      console.error('failed to list memory-', error);
      return { data: [], errors: [error] };
    }
  },
  async listReferences({
    accountId,
    messageId,
    messageIds,
    conversationId,
  }: {
    accountId: string;
    messageId?: string;
    messageIds?: string[];
    conversationId?: string;
  }) {
    if (accountId === 'demo') return null;
    const LIST_REFERENCES = `
    query AiListReferences($request: ListAIReferencesRequest!) {
      ai_list_references(request: $request) {
        data {
          id
          account_id
          conversation_id
          message_id
          agent_id
          reference_id
          type
          content
          metadata
          used
          used_by_agent
          created_at
        }
        errors {
          message
          code
        }
      }
    }
    `;
    try {
      const request: any = {
        account_id: accountId,
        limit: 100,
        offset: 0,
      };
      if (conversationId) {
        request.conversation_id = conversationId;
      }
      if (messageIds?.length) {
        request.message_ids = messageIds;
      } else if (messageId) {
        request.message_id = messageId;
      }
      const response = await queryGraphQL(LIST_REFERENCES, 'AiListReferences', {
        request: request,
      });
      return {
        data: response.data?.data?.ai_list_references?.data || [],
        errors: response.data?.data?.ai_list_references?.errors || response.data?.errors || [],
      };
    } catch (error) {
      console.error('failed to list references-', error);
      return { data: [], errors: [error] };
    }
  },
  // Per-conversation critique rows (llm_conversation_agent_critiques)
  async listConversationCritiques({ accountId, conversationId, messageId }: { accountId: string; conversationId?: string; messageId?: string }) {
    if (accountId === 'demo') return null;
    const CRITIQUES_LIST = `
    query CritiquesList($where: LlmConversationAgentCritiquesWhereRequest) {
      critiques_list(where: $where) {
        rows {
          id
          conversation_id
          message_id
          account_id
          agent_name
          critiqued_content
          input
          critique_type
          feedback
          decision
          created_at
        }
      }
    }
    `;
    try {
      const where: any = { account_id: { _eq: accountId } };
      if (conversationId) {
        where.conversation_id = { _eq: conversationId };
      }
      if (messageId) {
        where.message_id = { _eq: messageId };
      }
      const response = await queryGraphQL(CRITIQUES_LIST, 'CritiquesList', { where });
      return {
        data: response?.data?.data?.critiques_list?.rows || [],
        errors: response?.data?.errors || [],
      };
    } catch (error) {
      console.error('failed to list critiques-', error);
      return { data: [], errors: [error] };
    }
  },
  async deleteMemory(accountId: string, memoryId: string) {
    if (accountId === 'demo')
      return {
        data: null,
        errors: [{ message: 'Demo account does not have access.' }],
      };
    const DELETE_MEMORY = `
    mutation DeleteMemory($request: DeleteMemoryRequest!) {
      ai_delete_memory(request: $request) {
        data
        errors {
          message
        }
      }
    }
    `;
    try {
      const response = await queryGraphQL(DELETE_MEMORY, 'DeleteMemory', {
        request: {
          account_id: accountId,
          id: memoryId,
        },
      });
      return {
        data: response.data?.data?.ai_delete_memory?.data,
        errors: response.data?.data?.ai_delete_memory?.errors || response.data?.errors || [],
      };
    } catch (error) {
      console.error('failed to delete memory-', error);
      return { data: null, errors: [error] };
    }
  },
  async listModels(accountId: string) {
    if (accountId === 'demo') return null;
    const LIST_MODELS = `
    query ListModels($request: AIListModelsRequest!) {
      ai_list_models(request: $request) {
        data
        errors
      }
    }
    `;
    try {
      const response = await queryGraphQL(LIST_MODELS, 'ListModels', {
        request: {
          account_id: accountId,
        },
      });
      return {
        data: response.data?.data?.ai_list_models?.data || {},
        errors: response.data?.data?.ai_list_models?.errors || response.data?.errors || [],
      };
    } catch (error) {
      console.error('failed to list models-', error);
      return { data: {}, errors: [error] };
    }
  },
  // Model pricing. Rows come back as built-in (shipped defaults, shared by
  // every tenant) plus this tenant's own overrides, distinguished by
  // is_built_in — the tenant is resolved server-side from the session, never
  // sent from here.
  async listModelPricing() {
    const LIST_MODEL_PRICING = `
    query ListModelPricing($request: AIListModelPricingRequest!) {
      ai_list_model_pricing(request: $request) {
        data
        errors
      }
    }
    `;
    try {
      // No account: prices are tenant-scoped and the tenant comes from the
      // session server-side.
      const response = await queryGraphQL(LIST_MODEL_PRICING, 'ListModelPricing', { request: {} });
      return {
        data: response.data?.data?.ai_list_model_pricing?.data || {},
        errors: response.data?.data?.ai_list_model_pricing?.errors || response.data?.errors || [],
      };
    } catch (error) {
      console.error('failed to list model pricing-', error);
      return { data: {}, errors: [error] };
    }
  },
  // Writes this tenant's own rates. Restricted to tenant admins server-side;
  // the UI hides the controls for everyone else but does not rely on that.
  async upsertModelPricing(prices: Record<string, unknown>[]) {
    const UPSERT_MODEL_PRICING = `
    mutation UpsertModelPricing($request: AIUpsertModelPricingRequest!) {
      ai_upsert_model_pricing(request: $request) {
        data
        errors
      }
    }
    `;
    try {
      const response = await queryGraphQL(UPSERT_MODEL_PRICING, 'UpsertModelPricing', { request: { prices } });
      return {
        data: response.data?.data?.ai_upsert_model_pricing?.data || {},
        errors: response.data?.data?.ai_upsert_model_pricing?.errors || response.data?.errors || [],
      };
    } catch (error) {
      console.error('failed to save model pricing-', error);
      return { data: {}, errors: [error] };
    }
  },
  // Drops this tenant's override so the model bills at the built-in rate again.
  // The tenant is taken from the session server-side, never from these args.
  async deleteModelPricing(providerName: string, modelName: string) {
    const DELETE_MODEL_PRICING = `
    mutation DeleteModelPricing($request: AIDeleteModelPricingRequest!) {
      ai_delete_model_pricing(request: $request) {
        data
        errors
      }
    }
    `;
    try {
      const response = await queryGraphQL(DELETE_MODEL_PRICING, 'DeleteModelPricing', {
        request: { provider_name: providerName, model_name: modelName },
      });
      return {
        data: response.data?.data?.ai_delete_model_pricing?.data || {},
        errors: response.data?.data?.ai_delete_model_pricing?.errors || response.data?.errors || [],
      };
    } catch (error) {
      console.error('failed to delete model pricing-', error);
      return { data: {}, errors: [error] };
    }
  },
  async getModelConfig(accountId: string, conversationId?: string, signal?: AbortSignal) {
    if (accountId === 'demo') return null;
    const GET_MODEL_CONFIG = `
    query GetModelConfig($request: AIGetModelConfigRequest!) {
      ai_get_model_config(request: $request) {
        data
        errors
      }
    }
    `;
    try {
      const request: any = { account_id: accountId };
      if (conversationId) {
        request.conversation_id = conversationId;
      }
      const response = await queryGraphQL(
        GET_MODEL_CONFIG,
        'GetModelConfig',
        {
          request: request,
        },
        undefined,
        signal
      );
      return {
        data: response.data?.data?.ai_get_model_config?.data || {},
        errors: response.data?.data?.ai_get_model_config?.errors || response.data?.errors || [],
      };
    } catch (error) {
      console.error('failed to get model config-', error);
      return { data: {}, errors: [error] };
    }
  },
  async getWorkspaceFile(data: GetWorkspaceFileRequest) {
    if (data.account_id === 'demo') return null;
    const GET_WORKSPACE_FILE = `
    query GetWorkspaceFile($request: AiGetWorspaceFile!) {
      ai_get_workspace_file(request: $request)
    }
    `;
    try {
      const response = await queryGraphQL(GET_WORKSPACE_FILE, 'GetWorkspaceFile', {
        request: {
          account_id: data.account_id,
          conversation_id: data.conversation_id,
          path: data.path,
          download: data.download,
        },
      });

      const responseData = response.data?.data?.ai_get_workspace_file !== undefined ? response.data.data.ai_get_workspace_file : null;

      return {
        data: responseData,
        errors: response.data?.errors || [],
      };
    } catch (error) {
      console.error('failed to get workspace file-', error);
      return { data: null, errors: [error] };
    }
  },
  async getRcaFormat(accountId: string) {
    if (accountId === 'demo') return null;
    const GET_RCA_FORMAT = `
    query GetRcaFormat($request: AIGetRcaFormatRequest!) {
      ai_get_rcaformat(request: $request) {
        data {
          is_default
          format
        }
        errors
      }
    }
    `;
    const request = { account_id: accountId };
    try {
      const response = await queryGraphQL(GET_RCA_FORMAT, 'GetRcaFormat', { request });
      return {
        data: response.data?.data?.ai_get_rcaformat?.data || null,
        errors: response.data?.errors || [],
      };
    } catch (error) {
      console.error('failed to fetch RCA format-', error);
      return { data: null, errors: [error] };
    }
  },
  async updateRcaFormat(data: { account_id: string; format: string }) {
    if (data.account_id === 'demo')
      return {
        data: null,
        errors: [{ message: 'Demo account does not have access.' }],
      };
    const UPDATE_RCA_FORMAT = `
    mutation UpdateRcaFormat($request: AISaveRcaFormatRequest!) {
      ai_upsert_rcaformat(request: $request) {
        data {
          is_default
          format
        }
        errors
      }
    }
    `;
    try {
      const response = await queryGraphQL(UPDATE_RCA_FORMAT, 'UpdateRcaFormat', { request: data });
      return {
        data: response.data?.data?.ai_upsert_rcaformat?.data || null,
        errors: response.data?.data?.ai_upsert_rcaformat?.errors || response.data?.errors || [],
      };
    } catch (error) {
      console.error('failed to update RCA format-', error);
      return { data: null, errors: [error] };
    }
  },

  // ---------------------------------------------------------------------------
  // Watch tasks (#28294)
  // Lists / cancels background watches scoped to a single conversation. The
  // list is shown in the Watches tab inside LLMConversationWithTabs; cancel is
  // a direct Hasura UPDATE since the v1 watch path is idempotent and has no
  // server-side side effects beyond the row state change.
  // ---------------------------------------------------------------------------
  // Watch reads + cancel both go through Hasura actions (not raw table
  // queries) so the same calls work under RPC_BYPASS_HASURA=true — the
  // bypass router can route registered actions to llm-server but has no
  // path for arbitrary table queries. The previous raw `llm_watch_tasks`
  // and `update_llm_watch_tasks` queries are obsolete; server-side
  // tenant scoping is now enforced in llm-server's HTTP handlers
  // (api/watches.go) rather than Hasura row permissions.
  async listWatchesByConversation({ conversationId }: { conversationId: string }) {
    if (watchFeatureDisabled) return [];
    const LIST_WATCHES_BY_CONVERSATION = `
      query AILlmWatchList($conversationId: String!) {
        ai_list_watches_by_conversation(request: { conversation_id: $conversationId }) {
          data {
            watches {
              id
              status
              source_kind
              predicate_kind
              predicate_expr
              predicate_negate
              poll_interval_sec
              max_duration_sec
              poll_count
              failure_count
              next_poll_at
              last_poll_at
              last_poll_result
              final_result
              error
              expires_at
              created_at
              updated_at
            }
          }
          err
        }
      }
    `;
    try {
      const response = await queryGraphQL(LIST_WATCHES_BY_CONVERSATION, 'AILlmWatchList', { conversationId });
      const errs: { extensions?: { upstream?: { status?: number } } }[] = response?.data?.errors ?? [];
      if (errs.some((e) => e?.extensions?.upstream?.status === 404)) {
        // Latch client-side only: the module var is shared per-process on the server.
        if (typeof window !== 'undefined') watchFeatureDisabled = true;
        return [];
      }
      return response?.data?.data?.ai_list_watches_by_conversation?.data?.watches || [];
    } catch {
      return [];
    }
  },

  async cancelWatch({ watchId }: { watchId: string }) {
    // The non-terminal-only guard (PENDING/ACTIVE → CANCELLED) used to
    // live in the Hasura update permission. It now lives in
    // watch.Manager.Cancel's SQL, and the HTTP handler reads the row
    // back to compute affected_rows. The UI keeps the same
    // affected_rows + returning shape so the WatchesTab component's
    // success/already-terminal branching does not have to change.
    const CANCEL_WATCH = `
      mutation AILlmWatchCancel($watchId: String!) {
        ai_cancel_watch(request: { watch_id: $watchId }) {
          data {
            affected_rows
            watch_id
            status
          }
          err
        }
      }
    `;
    const response = await queryGraphQL(CANCEL_WATCH, 'AILlmWatchCancel', { watchId });
    const data = response?.data?.data?.ai_cancel_watch?.data;
    if (!data) {
      return { affected_rows: 0, returning: [] };
    }
    return {
      affected_rows: data.affected_rows ?? 0,
      // Synthesise the old `returning` array shape from the new
      // {watch_id, status} pair so the WatchesTab component does not
      // need to switch on response shape.
      returning: data.watch_id ? [{ id: data.watch_id, status: data.status }] : [],
    };
  },
};

export default api;
