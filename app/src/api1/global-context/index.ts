import { queryGraphQL } from '@lib/HttpService';

interface GlobalContextOutput {
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
  created_by?: string;
  updated_by?: string;
  created_at?: string;
  updated_at?: string;
}

interface CreateGlobalContextPayload {
  name: string;
  description?: string;
  content: string;
  format?: string;
  fileName?: string;
}

interface UpdateGlobalContextPayload {
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

const apiGlobalContext = {
  /**
   * List all global contexts for an account
   */
  getGlobalContexts: async (accountId: string) => {
    const LIST_GLOBAL_CONTEXTS = `
      query ListGlobalContexts($request: ListGCRequest!) {
        ai_list_gc(request: $request) {
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
      if (accountId === 'demo') {
        return { data: [], errors: [] };
      }
      const response = await queryGraphQL(LIST_GLOBAL_CONTEXTS, 'ListGlobalContexts', {
        request: {
          account_id: accountId,
        },
      });

      if (response?.data?.data?.ai_list_gc) {
        const result = response.data.data.ai_list_gc;
        const transformedData = (result.data || []).map((gc: GlobalContextOutput) => ({
          id: gc.id,
          name: gc.name,
          description: gc.description,
          content: gc.data,
          format: gc.data_format,
          fileName: gc.data_filename,
          status: gc.status,
          created_at: gc.created_at,
          updated_at: gc.updated_at,
          created_by: gc.created_by ? { display_name: gc.created_by } : null,
          updated_by: gc.updated_by ? { display_name: gc.updated_by } : null,
        }));
        return { data: transformedData, errors: result.errors || [] };
      }
      return { data: [], errors: [{ message: 'Failed to fetch global contexts' }] };
    } catch (error) {
      console.error('Error fetching global contexts:', error);
      return { data: [], errors: [{ message: 'An error occurred while fetching global contexts' }] };
    }
  },

  /**
   * Get a single global context by ID
   */
  getGlobalContext: async (accountId: string, contextId: string) => {
    const GET_GLOBAL_CONTEXT = `
      query GetGlobalContext($request: GetGCRequest!) {
        ai_get_gc(request: $request) {
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
      if (accountId === 'demo') {
        return { data: null, errors: [] };
      }
      const response = await queryGraphQL(GET_GLOBAL_CONTEXT, 'GetGlobalContext', {
        request: {
          account_id: accountId,
          id: contextId,
        },
      });

      if (response?.data?.data?.ai_get_gc) {
        const result = response.data.data.ai_get_gc;
        if (result.data) {
          const gc = result.data;
          const transformedData = {
            id: gc.id,
            name: gc.name,
            description: gc.description,
            content: gc.data,
            format: gc.data_format,
            fileName: gc.data_filename,
            status: gc.status,
            created_at: gc.created_at,
            updated_at: gc.updated_at,
            created_by: gc.created_by ? { display_name: gc.created_by } : null,
            updated_by: gc.updated_by ? { display_name: gc.updated_by } : null,
          };
          return { data: transformedData, errors: result.errors || [] };
        }
        return { data: null, errors: result.errors || [] };
      }
      return { data: null, errors: [{ message: 'Failed to fetch global context' }] };
    } catch (error) {
      console.error('Error fetching global context:', error);
      return { data: null, errors: [{ message: 'An error occurred while fetching global context' }] };
    }
  },

  /**
   * Create a new global context
   */
  createGlobalContext: async (accountId: string, payload: CreateGlobalContextPayload) => {
    const CREATE_GLOBAL_CONTEXT = `
      mutation CreateGlobalContext($request: CreateGCRequest!) {
        ai_create_gc(request: $request) {
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
      if (accountId === 'demo') {
        return { data: null, errors: [{ message: 'Demo account does not have access.' }] };
      }
      const response = await queryGraphQL(CREATE_GLOBAL_CONTEXT, 'CreateGlobalContext', {
        request: {
          account_id: accountId,
          global_context: {
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
        const errorMessage = extractErrorMessage(response, 'Failed to create global context');
        return { data: null, errors: [{ message: errorMessage }] };
      }

      if (response?.data?.data?.ai_create_gc) {
        const result = response.data.data.ai_create_gc;
        if (result.errors && result.errors.length > 0) {
          return { data: null, errors: result.errors };
        }
        if (result.data) {
          const gc = result.data;
          const transformedData = {
            id: gc.id,
            name: gc.name,
            description: gc.description,
            content: gc.data,
            format: gc.data_format,
            fileName: gc.data_filename,
            status: gc.status,
            created_at: gc.created_at,
            updated_at: gc.updated_at,
            created_by: gc.created_by ? { display_name: gc.created_by } : null,
            updated_by: gc.updated_by ? { display_name: gc.updated_by } : null,
          };
          return { data: transformedData, errors: result.errors || [] };
        }
        return { data: null, errors: result.errors || [] };
      }
      return { data: null, errors: [{ message: 'Failed to create global context' }] };
    } catch (error) {
      console.error('Error creating global context:', error);
      return { data: null, errors: [{ message: 'An error occurred while creating global context' }] };
    }
  },

  /**
   * Update an existing global context
   */
  updateGlobalContext: async (accountId: string, contextId: string, payload: UpdateGlobalContextPayload) => {
    const UPDATE_GLOBAL_CONTEXT = `
      mutation UpdateGlobalContext($request: UpdateGCRequest!) {
        ai_update_gc(request: $request) {
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
      const response = await queryGraphQL(UPDATE_GLOBAL_CONTEXT, 'UpdateGlobalContext', {
        request: {
          account_id: accountId,
          global_context: {
            id: contextId,
            name: payload.name,
            description: payload.description,
            data: payload.content,
            format: payload.format || 'text',
            file_name: payload.fileName,
          },
        },
      });

      // Check for errors in response
      if (response?.data?.errors && response.data.errors.length > 0) {
        const errorMessage = extractErrorMessage(response, 'Failed to update global context');
        return { data: null, errors: [{ message: errorMessage }] };
      }

      if (response?.data?.data?.ai_update_gc) {
        const result = response.data.data.ai_update_gc;
        if (result.errors && result.errors.length > 0) {
          return { data: null, errors: result.errors };
        }
        return { data: result.data, errors: [] };
      }
      return { data: null, errors: [{ message: 'Failed to update global context' }] };
    } catch (error) {
      console.error('Error updating global context:', error);
      return { data: null, errors: [{ message: 'An error occurred while updating global context' }] };
    }
  },

  /**
   * Delete a global context
   */
  deleteGlobalContext: async (accountId: string, contextId: string) => {
    const DELETE_GLOBAL_CONTEXT = `
      mutation DeleteGlobalContext($request: DeleteGCRequest!) {
        ai_delete_gc(request: $request) {
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
      const response = await queryGraphQL(DELETE_GLOBAL_CONTEXT, 'DeleteGlobalContext', {
        request: {
          account_id: accountId,
          id: contextId,
        },
      });

      // Check for errors in response
      if (response?.data?.errors && response.data.errors.length > 0) {
        const errorMessage = extractErrorMessage(response, 'Failed to delete global context');
        return { data: null, errors: [{ message: errorMessage }] };
      }

      if (response?.data?.data?.ai_delete_gc) {
        const result = response.data.data.ai_delete_gc;
        if (result.errors && result.errors.length > 0) {
          return { data: null, errors: result.errors };
        }
        return { data: result.data, errors: [] };
      }
      return { data: null, errors: [{ message: 'Failed to delete global context' }] };
    } catch (error) {
      console.error('Error deleting global context:', error);
      return { data: null, errors: [{ message: 'An error occurred while deleting global context' }] };
    }
  },
};

export default apiGlobalContext;
