import { queryGraphQL } from '@lib/HttpService';

interface KnowledgeBaseOutput {
  id: string;
  tenant_id: string;
  account_id: string;
  name: string;
  description?: string;
  data: string;
  data_format: string;
  data_filename: string;
  data_size_bytes?: number;
  status: string;
  kb_type: string;
  kb_source?: string;
  integration_id?: string;
  created_by?: string;
  updated_by?: string;
  created_at?: string;
  updated_at?: string;
  document_count?: number;
  last_loaded_at?: string;
}

interface CreateKnowledgeBasePayload {
  name: string;
  description?: string;
  content: string;
  format?: string;
  fileName?: string;
}

interface UpdateKnowledgeBasePayload {
  name?: string;
  description?: string;
  content?: string;
  format?: string;
  fileName?: string;
}

// Helper function to extract error message from nested GraphQL error response
const extractErrorMessage = (response: any, fallbackMessage: string): string => {
  try {
    if (response?.data?.errors && response.data.errors.length > 0) {
      const error = response.data.errors[0];
      const internalError = error?.extensions?.internal?.response?.body?.errors?.[0]?.message;
      if (internalError) {
        return internalError;
      }
      if (error.message && error.message !== 'internal error') {
        return error.message;
      }
    }
    return fallbackMessage;
  } catch {
    return fallbackMessage;
  }
};

const apiKnowledgeBase = {
  getKnowledgeBases: async (accountId: string) => {
    if (accountId === 'demo') {
      return {
        data: [],
      };
    }
    const LIST_KNOWLEDGE_BASES = `
      query ListKnowledgeBases($request: ListKBRequest!) {
        ai_list_kb(request: $request) {
          data {
            id
            tenant_id
            account_id
            name
            description
            data
            data_format
            data_filename
            data_size_bytes
            status
            kb_type
            kb_source
            integration_id
            created_by
            updated_by
            created_at
            updated_at
            document_count
            last_loaded_at
          }
          errors {
            message
          }
        }
      }
    `;

    try {
      const response = await queryGraphQL(LIST_KNOWLEDGE_BASES, 'ListKnowledgeBases', {
        request: {
          account_id: accountId,
        },
      });

      if (response?.data?.data?.ai_list_kb) {
        const result = response.data.data.ai_list_kb;
        const transformedData = (result.data || []).map((kb: KnowledgeBaseOutput) => ({
          id: kb.id,
          name: kb.name,
          description: kb.description,
          content: kb.data,
          format: kb.data_format,
          fileName: kb.data_filename,
          status: kb.status,
          kb_type: kb.kb_type,
          kb_source: kb.kb_source,
          integration_id: kb.integration_id,
          created_at: kb.created_at,
          updated_at: kb.updated_at,
          created_by: kb.created_by ? { display_name: kb.created_by } : null,
          updated_by: kb.updated_by ? { display_name: kb.updated_by } : null,
          document_count: kb.document_count,
          last_loaded_at: kb.last_loaded_at,
        }));
        return { data: transformedData, errors: result.errors || [] };
      }
      return { data: [], errors: [{ message: 'Failed to fetch knowledge bases' }] };
    } catch (error) {
      console.error('Error fetching knowledge bases:', error);
      return { data: [], errors: [{ message: 'An error occurred while fetching knowledge bases' }] };
    }
  },

  /**
   * Get a single knowledge base by ID
   */
  getKnowledgeBase: async (accountId: string, kbId: string) => {
    if (accountId === 'demo') {
      return {
        data: null,
        errors: [],
      };
    }
    const GET_KNOWLEDGE_BASE = `
      query GetKnowledgeBase($request: GetKBRequest!) {
        ai_get_kb(request: $request) {
          data {
            id
            tenant_id
            account_id
            name
            description
            data
            data_format
            data_filename
            data_size_bytes
            status
            created_by
            updated_by
            created_at
            updated_at
          }
          errors {
            message
          }
        }
      }
    `;

    try {
      const response = await queryGraphQL(GET_KNOWLEDGE_BASE, 'GetKnowledgeBase', {
        request: {
          account_id: accountId,
          id: kbId,
        },
      });

      if (response?.data?.data?.ai_get_kb) {
        const result = response.data.data.ai_get_kb;
        if (result.data) {
          const kb = result.data;
          const transformedData = {
            id: kb.id,
            name: kb.name,
            description: kb.description,
            content: kb.data,
            format: kb.data_format,
            fileName: kb.data_filename,
            status: kb.status,
            created_at: kb.created_at,
            updated_at: kb.updated_at,
            created_by: kb.created_by ? { display_name: kb.created_by } : null,
            updated_by: kb.updated_by ? { display_name: kb.updated_by } : null,
          };
          return { data: transformedData, errors: result.errors || [] };
        }
        return { data: null, errors: result.errors || [] };
      }
      return { data: null, errors: [{ message: 'Failed to fetch knowledge base' }] };
    } catch (error) {
      console.error('Error fetching knowledge base:', error);
      return { data: null, errors: [{ message: 'An error occurred while fetching knowledge base' }] };
    }
  },

  /**
   * Create a new knowledge base
   */
  createKnowledgeBase: async (accountId: string, payload: CreateKnowledgeBasePayload) => {
    if (accountId === 'demo') {
      return {
        data: null,
        errors: [{ message: 'Demo account does not have access.' }],
      };
    }
    const CREATE_KNOWLEDGE_BASE = `
      mutation CreateKnowledgeBase($request: CreateKBRequest!) {
        ai_create_kb(request: $request) {
          data {
            id
            tenant_id
            account_id
            name
            description
            data
            data_format
            data_filename
            data_size_bytes
            status
            created_by
            updated_by
            created_at
            updated_at
          }
          errors {
            message
          }
        }
      }
    `;

    try {
      const response = await queryGraphQL(CREATE_KNOWLEDGE_BASE, 'CreateKnowledgeBase', {
        request: {
          account_id: accountId,
          knowledgebase: {
            name: payload.name,
            description: payload.description || '',
            data: payload.content,
            format: payload.format || 'text',
            file_name: payload.fileName || `${payload.name}.txt`,
          },
        },
      });

      // Check for errors in response
      if (response?.data?.errors && response.data.errors.length > 0) {
        const errorMessage = extractErrorMessage(response, 'Failed to create knowledge base');
        return { data: null, errors: [{ message: errorMessage }] };
      }

      if (response?.data?.data?.ai_create_kb) {
        const result = response.data.data.ai_create_kb;
        if (result.errors && result.errors.length > 0) {
          return { data: null, errors: result.errors };
        }
        if (result.data) {
          const kb = result.data;
          const transformedData = {
            id: kb.id,
            name: kb.name,
            description: kb.description,
            content: kb.data,
            format: kb.data_format,
            fileName: kb.data_filename,
            status: kb.status,
            created_at: kb.created_at,
            updated_at: kb.updated_at,
            created_by: kb.created_by ? { display_name: kb.created_by } : null,
            updated_by: kb.updated_by ? { display_name: kb.updated_by } : null,
          };
          return { data: transformedData, errors: result.errors || [] };
        }
        return { data: null, errors: result.errors || [] };
      }
      return { data: null, errors: [{ message: 'Failed to create knowledge base' }] };
    } catch (error) {
      console.error('Error creating knowledge base:', error);
      return { data: null, errors: [{ message: 'An error occurred while creating knowledge base' }] };
    }
  },

  /**
   * Update an existing knowledge base
   */
  updateKnowledgeBase: async (accountId: string, kbId: string, payload: UpdateKnowledgeBasePayload) => {
    const UPDATE_KNOWLEDGE_BASE = `
      mutation UpdateKnowledgeBase($request: UpdateKBRequest!) {
        ai_update_kb(request: $request) {
          data
          errors {
            message
          }
        }
      }
    `;

    try {
      if (accountId === 'demo') {
        return { data: null, errors: [{ message: 'Demo account does not have access.' }] };
      }
      const response = await queryGraphQL(UPDATE_KNOWLEDGE_BASE, 'UpdateKnowledgeBase', {
        request: {
          account_id: accountId,
          knowledgebase: {
            id: kbId,
            name: payload.name,
            description: payload.description,
            data: payload.content,
            format: payload.format || 'text',
            file_name: payload.fileName,
          },
        },
      });

      if (response?.data?.errors && response.data.errors.length > 0) {
        const errorMessage = extractErrorMessage(response, 'Failed to update knowledge base');
        return { data: null, errors: [{ message: errorMessage }] };
      }

      if (response?.data?.data?.ai_update_kb) {
        const result = response.data.data.ai_update_kb;
        if (result.errors && result.errors.length > 0) {
          return { data: null, errors: result.errors };
        }
        return { data: result.data, errors: [] };
      }
      return { data: null, errors: [{ message: 'Failed to update knowledge base' }] };
    } catch (error) {
      console.error('Error updating knowledge base:', error);
      return { data: null, errors: [{ message: 'An error occurred while updating knowledge base' }] };
    }
  },

  /**
   * Delete a knowledge base
   */
  deleteKnowledgeBase: async (accountId: string, kbId: string) => {
    const DELETE_KNOWLEDGE_BASE = `
      mutation DeleteKnowledgeBase($request: DeleteKBRequest!) {
        ai_delete_kb(request: $request) {
          data
          errors {
            message
          }
        }
      }
    `;

    try {
      if (accountId === 'demo') {
        return { data: null, errors: [{ message: 'Demo account does not have access.' }] };
      }
      const response = await queryGraphQL(DELETE_KNOWLEDGE_BASE, 'DeleteKnowledgeBase', {
        request: {
          account_id: accountId,
          id: kbId,
        },
      });

      if (response?.data?.errors && response.data.errors.length > 0) {
        const errorMessage = extractErrorMessage(response, 'Failed to delete knowledge base');
        return { data: null, errors: [{ message: errorMessage }] };
      }

      if (response?.data?.data?.ai_delete_kb) {
        const result = response.data.data.ai_delete_kb;
        if (result.errors && result.errors.length > 0) {
          return { data: null, errors: result.errors };
        }
        return { data: result.data, errors: [] };
      }
      return { data: null, errors: [{ message: 'Failed to delete knowledge base' }] };
    } catch (error) {
      console.error('Error deleting knowledge base:', error);
      return { data: null, errors: [{ message: 'An error occurred while deleting knowledge base' }] };
    }
  },

  /**
   * Get all knowledge bases mapped to an agent
   */
  getAgentKnowledgeBases: async (accountId: string, agentId: string) => {
    if (accountId == 'demo') {
      return {
        data: [],
      };
    }
    const LIST_AGENT_KBS = `
      query ListAgentKBs($request: ListAgentKBsRequest!) {
        ai_list_agent_kbs(request: $request) {
          data
          errors {
            message
          }
        }
      }
    `;

    try {
      const response = await queryGraphQL(LIST_AGENT_KBS, 'ListAgentKBs', {
        request: {
          account_id: accountId,
          agent_id: agentId,
        },
      });

      if (response?.data?.errors && response.data.errors.length > 0) {
        const errorMessage = extractErrorMessage(response, 'Failed to fetch agent knowledge bases');
        return { data: [], errors: [{ message: errorMessage }] };
      }

      if (response?.data?.data?.ai_list_agent_kbs) {
        const result = response.data.data.ai_list_agent_kbs;
        if (result.errors && result.errors.length > 0) {
          return { data: [], errors: result.errors };
        }
        return { data: result.data || [], errors: [] };
      }
      return { data: [], errors: [{ message: 'Failed to fetch agent knowledge bases' }] };
    } catch (error) {
      console.error('Error fetching agent knowledge bases:', error);
      return { data: [], errors: [{ message: 'An error occurred while fetching agent knowledge bases' }] };
    }
  },

  /**
   * Map a knowledge base to an agent
   */
  mapKnowledgeBaseToAgent: async (accountId: string, kbId: string, agentId: string) => {
    const MAP_KB_TO_AGENT = `
      mutation MapKBToAgent($request: MapKBToAgentRequest!) {
        ai_map_kb_to_agent(request: $request) {
          data
          errors {
            message
          }
        }
      }
    `;

    try {
      if (accountId === 'demo') {
        return {
          data: null,
          errors: [{ message: 'Demo account does not have access.' }],
        };
      }
      const response = await queryGraphQL(MAP_KB_TO_AGENT, 'MapKBToAgent', {
        request: {
          account_id: accountId,
          kb_id: kbId,
          agent_id: agentId,
        },
      });

      if (response?.data?.errors && response.data.errors.length > 0) {
        const errorMessage = extractErrorMessage(response, 'Failed to map knowledge base to agent');
        return { data: null, errors: [{ message: errorMessage }] };
      }

      if (response?.data?.data?.ai_map_kb_to_agent) {
        const result = response.data.data.ai_map_kb_to_agent;
        if (result.errors && result.errors.length > 0) {
          return { data: null, errors: result.errors };
        }
        return { data: result.data, errors: [] };
      }
      return { data: null, errors: [{ message: 'Failed to map knowledge base to agent' }] };
    } catch (error) {
      console.error('Error mapping knowledge base to agent:', error);
      return { data: null, errors: [{ message: 'An error occurred while mapping knowledge base to agent' }] };
    }
  },

  /**
   * Unmap a knowledge base from an agent
   */
  unmapKnowledgeBaseFromAgent: async (accountId: string, kbId: string, agentId: string) => {
    const UNMAP_KB_FROM_AGENT = `
      mutation UnmapKBFromAgent($request: UnmapKBFromAgentRequest!) {
        ai_unmap_kb_from_agent(request: $request) {
          data
          errors {
            message
          }
        }
      }
    `;

    try {
      if (accountId === 'demo') {
        return {
          data: null,
          errors: [{ message: 'Demo account does not have access.' }],
        };
      }
      const response = await queryGraphQL(UNMAP_KB_FROM_AGENT, 'UnmapKBFromAgent', {
        request: {
          account_id: accountId,
          kb_id: kbId,
          agent_id: agentId,
        },
      });

      if (response?.data?.errors && response.data.errors.length > 0) {
        const errorMessage = extractErrorMessage(response, 'Failed to unmap knowledge base from agent');
        return { data: null, errors: [{ message: errorMessage }] };
      }

      if (response?.data?.data?.ai_unmap_kb_from_agent) {
        const result = response.data.data.ai_unmap_kb_from_agent;
        if (result.errors && result.errors.length > 0) {
          return { data: null, errors: result.errors };
        }
        return { data: result.data, errors: [] };
      }
      return { data: null, errors: [{ message: 'Failed to unmap knowledge base from agent' }] };
    } catch (error) {
      console.error('Error unmapping knowledge base from agent:', error);
      return { data: null, errors: [{ message: 'An error occurred while unmapping knowledge base from agent' }] };
    }
  },

  /**
   * Get all agents with their KB mapping counts
   * Returns [{agent_id, kb_count}]
   */
  getAgentsWithKbCounts: async (accountId: string) => {
    if (accountId == 'demo') {
      return {
        data: [],
        errors: [],
      };
    }
    const LIST_AGENTS_WITH_KB_COUNTS = `
      query ListAgentsWithKBCounts($request: ListAgentsWithKBCountsRequest!) {
        ai_list_agents_with_kb_counts(request: $request) {
          data
          errors {
            message
          }
        }
      }
    `;

    try {
      const response = await queryGraphQL(LIST_AGENTS_WITH_KB_COUNTS, 'ListAgentsWithKBCounts', {
        request: {
          account_id: accountId,
        },
      });

      if (response?.data?.data?.ai_list_agents_with_kb_counts) {
        const result = response.data.data.ai_list_agents_with_kb_counts;
        if (result.errors && result.errors.length > 0) {
          return { data: [], errors: result.errors };
        }
        // Data might be returned as a JSON string or array
        let parsedData = result.data;
        if (typeof result.data === 'string') {
          try {
            parsedData = JSON.parse(result.data);
          } catch {
            parsedData = [];
          }
        }
        return { data: parsedData || [], errors: [] };
      }
      return { data: [], errors: [{ message: 'Failed to fetch agents with KB counts' }] };
    } catch (error) {
      console.error('Error fetching agents with KB counts:', error);
      return { data: [], errors: [{ message: 'An error occurred while fetching agents with KB counts' }] };
    }
  },

  getKBLoadHistory: async (accountId: string, kbId: string) => {
    const GET_KB_LOAD_HISTORY = `
      query GetKBLoadHistory($request: GetKBLoadHistoryRequest!) {
        ai_get_kb_load_history(request: $request) {
          data {
            id
            document_count
            expected_document_count
            total_tokens
            embedding_provider
            embedding_model
            request_status
            error_message
            trigger_type
            triggered_by
            load_duration_seconds
            created_at
          }
          errors {
            message
          }
        }
      }
    `;
    try {
      if (accountId === 'demo') {
        return { data: [], errors: [] };
      }
      const response = await queryGraphQL(GET_KB_LOAD_HISTORY, 'GetKBLoadHistory', {
        request: { account_id: accountId, kb_id: kbId },
      });
      if (response?.data?.data?.ai_get_kb_load_history) {
        const result = response.data.data.ai_get_kb_load_history;
        return { data: result.data || [], errors: result.errors || [] };
      }
      return { data: [], errors: [{ message: 'Failed to fetch load history' }] };
    } catch (error) {
      console.error('Error fetching KB load history:', error);
      return { data: [], errors: [{ message: 'An error occurred while fetching load history' }] };
    }
  },

  retriggerKB: async (accountId: string, kbId: string) => {
    const RETRIGGER_KB = `
      mutation RetriggerKB($request: RetriggerKBRequest!) {
        ai_retrigger_kb(request: $request) {
          data
          errors {
            message
          }
        }
      }
    `;
    try {
      if (accountId === 'demo') {
        return { data: null, errors: [{ message: 'Demo account does not have access.' }] };
      }
      const response = await queryGraphQL(RETRIGGER_KB, 'RetriggerKB', {
        request: { account_id: accountId, kb_id: kbId },
      });
      if (response?.data?.data?.ai_retrigger_kb) {
        const result = response.data.data.ai_retrigger_kb;
        if (result.errors && result.errors.length > 0) {
          return { data: null, errors: result.errors };
        }
        return { data: result.data, errors: [] };
      }
      return { data: null, errors: [{ message: extractErrorMessage(response, 'Failed to retrigger knowledge base') }] };
    } catch (error) {
      console.error('Error retriggering KB:', error);
      return { data: null, errors: [{ message: 'An error occurred while retriggering' }] };
    }
  },
};

export default apiKnowledgeBase;
