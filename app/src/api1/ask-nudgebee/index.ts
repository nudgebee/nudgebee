import { gqlStringify, queryGraphQL } from '@lib/HttpService';
import { getUserSession } from '@lib/auth';

const EVENT_DETAILS_RETRIEVAL_TITLE = 'Event details retrieval by ID';

const api = {
  async askNudgebeeAiGeneratePrometheusQuery(data: any) {
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
  async askAiGenerateLokiQuery(data: any) {
    const ASK_AI_GENERATE_LOKI_QUERY = `
        mutation AskAiGenerateLokiQuery {
          ai_generate_loki_query(request: __REQUEST__) {
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
    const response = await queryGraphQL(ASK_AI_GENERATE_LOKI_QUERY.replace('__REQUEST__', gqlStringify(query)), 'AskAiGenerateLokiQuery', {});
    return response;
  },
  async createAiFeedback(data: any) {
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
      console.log('failed to create ai feedback-', error);
      return error;
    }
  },
  async listAiFeedback(queryRequest: any) {
    const LIST_AI_FEEDBACK = `
        query ListAiFeedback {
          llm_conversation_feedback: llm_conversation_feedback_v2(where: __WHERE__, order_by: [{column: "created_at", order: desc}]) {
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

      const gqlQuery = LIST_AI_FEEDBACK.replace('__WHERE__', gqlStringify(query));

      const response = await queryGraphQL(gqlQuery, 'ListAiFeedback', {});
      return response;
    } catch (error) {
      console.log('failed to create ai feedback-', error);
      return error;
    }
  },
  async aiGenerateInvestigate(data: any) {
    const AI_TRIGGER_INVESTIGATE_RESPONSE = `
        mutation AiTriggerInvestigateResponse {
          ai_trigger_investigation(request: __REQUEST__) {
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
    query.async = true;
    const response = await queryGraphQL(
      AI_TRIGGER_INVESTIGATE_RESPONSE.replace('__REQUEST__', gqlStringify(query)),
      'AiTriggerInvestigateResponse',
      {}
    );
    return response;
  },
  async aiFollowupResponse(data: any) {
    const AI_FOLLOWUP_RESPONSE = `
        mutation AiFollowupResponse {
          ai_followup_response(request: __REQUEST__) {
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
    query.agent_id = data.parent_agent_id;
    query.message_id = data.message_id;
    query.async = true;

    const response = await queryGraphQL(AI_FOLLOWUP_RESPONSE.replace('__REQUEST__', gqlStringify(query)), 'AiFollowupResponse', {});
    return response;
  },
  async llmConversationHistory(data: any) {
    // total_count maps to COUNT(*) OVER() in the derived view; for unbounded
    // listings (e.g. the sidebar) it forces a full per-row sweep before LIMIT,
    // so callers that paginate via "did we get a full page?" should opt out.
    const includeTotalCount = !data.skipTotalCount;
    const GET_LLM_CONVERSATION_HISTORY = `
          query LlMConversationHistory($where: LlmConversationListWhereRequest, $limit: Int, $offset: Int) {
            llm_conversation_list_v2(where: $where, order_by: [{column: "updated_at", order: desc}], limit: $limit, offset: $offset) {
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
    const rows = response?.data?.data?.llm_conversation_list_v2?.rows || [];
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
  async getLlmConversation({
    conversationId,
    accountId,
    sessionId,
    source,
    signal,
  }: {
    conversationId?: string;
    accountId?: string;
    sessionId?: string;
    source?: string;
    signal?: AbortSignal;
  }) {
    const GET_LLM_CONVERSATION = `
        query GetLlmConversation($where: LlmConversationDetailWhereRequest) {
          llm_conversation_detail_v2(where: $where) {
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
    if (source) {
      where.source = { _eq: source };
    }
    const response = await queryGraphQL(GET_LLM_CONVERSATION, 'GetLlmConversation', { where }, undefined, signal);

    // Transform v2 response to maintain backward compatibility
    const rows = response?.data?.data?.llm_conversation_detail_v2?.rows || [];
    if (response?.data?.data) {
      response.data.data.llm_conversations = rows.map((row: any) => {
        const rawMessages = typeof row.messages === 'string' ? JSON.parse(row.messages) : row.messages || [];
        // Transform each message's flat user_display_name back to nested user: { display_name }
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
    const GET_LLM_CONVERSATION_POLLING = `
        query GetLlmConversationPolling($where: LlmConversationDetailWhereRequest) {
          llm_conversation_detail_polling_v2(where: $where) {
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

    const rows = response?.data?.data?.llm_conversation_detail_polling_v2?.rows || [];
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
  async askNudgebeeAiGenerateESDsl(data: any) {
    const ASK_AI_GENERATE_ES_DSL = `
        mutation AskNudgebeeAiGenerateESDsl {
          ai_generate_es_dsl_query(request: __REQUEST__) {
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
    const response = await queryGraphQL(ASK_AI_GENERATE_ES_DSL.replace('__REQUEST__', gqlStringify(query)), 'AskNudgebeeAiGenerateESDsl', {});
    return response;
  },
  async getFeedbackForSessionId(data: any) {
    const GET_FEEBACK_FOR_SESSION_ID = `
        query LLMFeedback {
          llm_conversation_feedback_v2(where: __WHERE__, order_by: {column: "updated_at", order: desc}, limit: 1) {
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
    return response;
  },
  async saveConversation(data: any) {
    const SAVE_CONVERSATION = `
    mutation SaveConversation($data: SaveLLMConversationRequest!) {
      ai_save_conversation(request: $data) {
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
      console.log('failed to save conversation-', error);
      return error;
    }
  },
  async listAgents(data: any) {
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
      console.log('failed to fetch agent list-', error);
      return error;
    }
  },
  async listTools(data: any) {
    if (data.accountId == 'demo') {
      return { data: { data: { ai_list_tools: { data: '[]' } } } };
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
      console.log('failed to fetch tool list-', error);
      return error;
    }
  },
  async createAgent(data: any) {
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
      console.log('failed to fetch create agent-', error);
      return error;
    }
  },
  async createTool(data: any) {
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
      console.log('failed to fetch create tool-', error);
      return error;
    }
  },
  async deleteConversation(data: any) {
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
      console.log('failed to delete conversation-', error);
      return error;
    }
  },
  async deleteSavedConversation(data: any) {
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
      console.log('failed to delete saved conversation-', error);
      return error;
    }
  },
  async updateAgent(data: any) {
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
      console.log('failed to update agent-', error);
      return error;
    }
  },
  async updateTool(data: any) {
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
      console.log('failed to update tool-', error);
      return error;
    }
  },
  async getConversationSuggestions(data: any) {
    const GET_CONVERSATION_SUGGESTIONS = `
    mutation GetConversationSuggestions($data: AIGetConversationSuggestionRequest!) {
      ai_get_conversation_suggestions(request: $data) {
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
      console.log('failed to get conversation suggestions-', error);
      return error;
    }
  },
  async createRagData(data: any) {
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
      console.log(response);
      return response;
    } catch (error) {
      console.log('failed to create rag data-', error);
      return error;
    }
  },
  async listFunctions(data: any) {
    const GET_FUNCTIONS = `
      query GetFunctions($where: LlmFunctionsWhereRequest) {
        llm_functions: llm_functions_v2(where: $where) {
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
      const where: any = { account_id: { _eq: data.accountId } };
      const response = await queryGraphQL(GET_FUNCTIONS, 'GetFunctions', { where });
      const rows = response?.data?.data?.llm_functions?.rows || [];

      return {
        res: { llm_functions: rows },
        errors: response?.data?.errors,
      };
    } catch (error) {
      console.log('failed to get functions-', error);
      return error;
    }
  },
  async createAiFunction(data: any, accountId: string) {
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
      console.log('failed to create ai function-', error);
      return { success: false, error: 'Network error occurred' };
    }
  },
  async aiStopInvestigate(data: any) {
    const AI_STOP_INVESTIGATE = `
        mutation AiStopInvestigation($accountId: String!, $conversationId: String!) {
          ai_stop_investigation(request: {account_id: $accountId, conversation_id: $conversationId}) {
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
      console.log('failed to delete agent-', error);
      return error;
    }
  },
  async createAgentExtension(data: any) {
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
      console.log('failed to create agent extension-', error);
      return error;
    }
  },
  async updateAgentExtension(data: any) {
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
      console.log('failed to create agent extension-', error);
      return error;
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
      llm_agents_installation_v2(where: $where) {
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
      const rows = response.data?.data?.llm_agents_installation_v2?.rows || [];
      return {
        data: rows.map((r: any) => ({
          ...r,
          config: typeof r.config === 'string' ? JSON.parse(r.config) : r.config,
          tools: typeof r.tools === 'string' ? JSON.parse(r.tools) : r.tools,
        })),
        errors: response.data?.errors || [],
      };
    } catch (error) {
      console.log('failed to list agent extensions-', error);
      return { data: [], errors: [error] };
    }
  },
  async deleteLLMFunction({ id, accountId }: { id: string; accountId: string }) {
    if (accountId === 'demo') return null;
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
      console.log('failed to list agent extensions-', error);
      return { data: [], errors: [error] };
    }
  },
  async updateFunction({ accountId, functionId, data }: { accountId?: string; functionId: string; data: any }) {
    if (accountId === 'demo') return null;
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
    console.log('updating function with data-', data, 'accountId-', accountId, 'functionId-', functionId);
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
      console.log('failed to update ai function-', error);
      return { success: false, error: 'Network error occurred' };
    }
  },
  async llmConversationHistoryForInvestigation(data: any) {
    const GET_LLM_CONVERSATION_HISTORY = `
          query LlMConversationHistory($where: LlmConversationListWhereRequest, $limit: Int, $offset: Int) {
            llm_conversation_list_v2(where: $where, order_by: [{column: "updated_at", order: desc}], limit: $limit, offset: $offset) {
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
      where.account_id = { _eq: data.account_id };
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
    const rows = response?.data?.data?.llm_conversation_list_v2?.rows || [];
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
    const GET_LLM_CONVERSATION_COUNT = `
      query LlmConversationCount($where: LlmConversationListWhereRequest) {
        llm_conversation_list_v2(where: $where, limit: 1) {
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
    const rows = response?.data?.data?.llm_conversation_list_v2?.rows || [];
    return rows.length > 0 ? rows[0].total_count : 0;
  },
  llmConversationComparsion: async function (data: any) {
    const LLM_CONVERSATION_GROUPINGS = `
    query LLMConversationGroupings($where: LlmConversationGroupingsWhereRequest) {
      llm_conversation_groupings_v2(where: $where) {
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

      const currentCount = currentRes?.data?.data?.llm_conversation_groupings_v2?.rows?.[0]?.count ?? 0;
      const previousCount = previousRes?.data?.data?.llm_conversation_groupings_v2?.rows?.[0]?.count ?? 0;

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
  async listMemory(accountId: string, conversationId?: string, messageId?: string, memoryType?: string, query?: string) {
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
      if (messageId) {
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
      console.log('failed to list memory-', error);
      return { data: [], errors: [error] };
    }
  },
  async listReferences({ accountId, messageId, conversationId }: { accountId: string; messageId?: string; conversationId?: string }) {
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
      };
      if (conversationId) {
        request.conversation_id = conversationId;
      }
      if (messageId) {
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
      console.log('failed to list references-', error);
      return { data: [], errors: [error] };
    }
  },
  async deleteMemory(accountId: string, memoryId: string) {
    if (accountId === 'demo') return null;
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
      console.log('failed to delete memory-', error);
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
      console.log('failed to list models-', error);
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
      console.log('failed to get model config-', error);
      return { data: {}, errors: [error] };
    }
  },
  async getWorkspaceFile(data: any) {
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
      console.log('failed to get workspace file-', error);
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
      console.log('failed to fetch RCA format-', error);
      return { data: null, errors: [error] };
    }
  },
  async updateRcaFormat(data: { account_id: string; format: string }) {
    const UPDATE_RCA_FORMAT = `
    mutation UpdateRcaFormat($request: AISaveRcaFormatRequest!) {
      ai_save_rcaformat(request: $request) {
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
        data: response.data?.data?.ai_save_rcaformat?.data || null,
        errors: response.data?.data?.ai_save_rcaformat?.errors || response.data?.errors || [],
      };
    } catch (error) {
      console.log('failed to update RCA format-', error);
      return { data: null, errors: [error] };
    }
  },
};

export default api;
