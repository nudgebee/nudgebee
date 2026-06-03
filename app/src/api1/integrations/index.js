import { gqlStringify, queryGraphQL } from '@lib/HttpService';
import cache from '@lib/cache';

// Drop client-side ticket caches after an integration changes so the next
// Create-Ticket form reflects new projects/priorities/users and create-meta,
// rather than serving the 1h config-list / 5m create-meta cache. The backend
// Redis create-meta cache is invalidated server-side on save/test/sync.
function invalidateTicketCaches() {
  cache.del('tickets.listTicketConfigurations');
  cache.delWithSuffix('tickets.getTicketMeta:');
}

export const LIST_TICKET_CONFIGURATIONS = `
query ListTicketConfigurations($limit: Int!, $offset: Int!) {
  integrations: integrations_list(limit: $limit, offset: $offset, where: __WHERE__) {
    rows {
      id
      name
      type
      status
      created_by
      updated_at
      integration_config_values
      created_by_display_name
    }
  }
  integrations_count: integrations_aggregate(where: __WHERE__) {
    rows {
      count
    }
  }
}
`;

export const CREATE_TICKET_INTEGRATION = `
mutation CreateTicketIntegration($object: ticket_integration_create_config_input!) {
  ticket_integration_create_config(object: $object) {
    id
  }
}
`;

export const GET_TICKET_BY_REFERENCE_ID = `
query GetTicketByReferenceId($reference_id:String!) {
  tickets: tickets_list(where:{reference_id:{_eq:$reference_id}}) {
    rows {
      assignee
      integration_id
      reference_id
      url
      created_by_display_name
      ticket_id
      ticket_type
      status
      created_at
    }
  }
}
`;

export const TEST_TICKET_CONNECTION_BY_CONFIG = `
mutation TestTicketConnectionByConfig($object: ticket_integration_create_config_input!) {
  ticket_check_connection_by_config(object: $object) {
    success
    message
    tool
    projects_count
    error
  }
}
`;

export const DISABLE_TICKET_CONFIGURATION = `
mutation UpdateTicketConfiguration($id: String!, $status: String!) {
  integration_update_status_by_pk(id: $id, status: $status) {
    id
  }
}
`;

// Helper function to transform integrations response to legacy jira_configurations format
const transformIntegrationToLegacyFormat = (integration) => {
  const rawConfigValues = integration.integration_config_values;
  const configValues = typeof rawConfigValues === 'string' ? JSON.parse(rawConfigValues) : rawConfigValues || [];
  const getConfigValue = (name) => {
    const config = configValues.find((c) => c.name === name);
    return config ? config.value : null;
  };

  // Map status: enabled → Active, disabled → Disabled
  const status = integration.status === 'enabled' ? 'Active' : 'Disabled';
  const is_active = integration.status === 'enabled';

  return {
    id: integration.id,
    name: integration.name,
    tool: integration.type,
    status: status,
    is_active: is_active,
    url: getConfigValue('url'),
    username: getConfigValue('username'),
    auth_type: getConfigValue('auth_type'),
    projects: getConfigValue('projects') ? JSON.parse(getConfigValue('projects')) : null,
    priorities: getConfigValue('priorities') ? JSON.parse(getConfigValue('priorities')) : null,
    users: getConfigValue('users') ? JSON.parse(getConfigValue('users')) : null,
    sync_knowledge_base: getConfigValue('sync_knowledge_base') === 'true',
    rca_writeback_enabled: getConfigValue('rca_writeback_enabled') === 'true',
    last_connected: getConfigValue('last_connected') || integration.updated_at,
    created_by: integration.created_by,
    user: { display_name: integration.created_by_display_name },
  };
};

const apiIntegrations = {
  listTicketConfigurationsByTool: async function (query) {
    try {
      const gqlQuery = {};
      if (query) {
        if (query?.status) {
          gqlQuery['status'] = { _eq: query.status };
        }
        if (query?.tool) {
          gqlQuery['type'] = { _eq: query.tool };
        }
        if (query?.name) {
          gqlQuery['name'] = { _ilike: `%${query.name}%` };
        }
      }
      const limit = query?.limit || 10;
      const offset = query?.offset || 0;
      let response = await queryGraphQL(LIST_TICKET_CONFIGURATIONS.replaceAll('__WHERE__', gqlStringify(gqlQuery)), 'ListTicketConfigurations', {
        limit,
        offset,
      });

      const integrations = response?.data?.data?.integrations?.rows || [];
      const totalCount = response?.data?.data?.integrations_count?.rows?.[0]?.count || 0;
      const transformed = integrations.map(transformIntegrationToLegacyFormat);

      return {
        data: transformed,
        totalCount,
      };
    } catch (err) {
      console.error('Failed to list ticket configurations:', err);
      return err;
    }
  },

  createTicketIntegration: async function (data) {
    try {
      let response = await queryGraphQL(CREATE_TICKET_INTEGRATION, 'CreateTicketIntegration', { object: data });
      invalidateTicketCaches();
      return response;
    } catch (err) {
      console.log('Failed to create ticket integration:', err);
      throw err;
    }
  },

  getTicketByReferenceId: async function (reference_id) {
    try {
      let response = await queryGraphQL(GET_TICKET_BY_REFERENCE_ID, 'GetTicketByReferenceId', {
        reference_id: reference_id,
      });
      const tickets = (response.data?.data?.tickets?.rows || []).map((t) => ({
        ...t,
        user: { display_name: t.created_by_display_name },
      }));
      return {
        data: tickets,
      };
    } catch (err) {
      console.log('Your Error is', err);
      return err;
    }
  },

  testTicketConnectionByConfig: async function (object) {
    try {
      const response = await queryGraphQL(TEST_TICKET_CONNECTION_BY_CONFIG, 'TestTicketConnectionByConfig', { object });
      return response?.data?.data?.ticket_check_connection_by_config;
    } catch (err) {
      console.log('Failed to test ticket connection:', err);
      return { success: false, error: 'Failed to test connection' };
    }
  },

  disableTicketConfiguration: async function (data) {
    try {
      // Map active boolean to status string: true → 'enabled', false → 'disabled'
      const status = data.active ? 'enabled' : 'disabled';

      const response = await queryGraphQL(DISABLE_TICKET_CONFIGURATION, 'UpdateTicketConfiguration', {
        id: data.id,
        status: status,
      });
      invalidateTicketCaches();
      return response;
    } catch (err) {
      console.log('failed to update jira config status-', err);
      return err;
    }
  },

  listIntegrations: async function (data) {
    const LIST_INTEGRATION = `
    query ListIntegrations($limit: Int!, $offset: Int!) {
      integrations_list(limit: $limit, offset: $offset, where: __WHERE__) {
        rows{
          created_at
          created_by
          id
          labels
          name
          source
          status
          tenant_id
          type
          updated_at
          updated_by
          integrations_cloud_accounts
          updated_by_display_name
          created_by_display_name
          integration_config_values
        }
      }
      integrations_aggregate(where: __WHERE__) {
        rows{
          count
        }
      }
    }    
    `;
    const query = {};
    if (data.id) {
      query['id'] = { _eq: data.id };
    }
    if (data.type) {
      if (Array.isArray(data.type)) {
        query['type'] = { _in: data.type };
      } else {
        query['type'] = { _eq: data.type };
      }
    }
    if (data.name) {
      query['name'] = { _ilike: `%${data.name}%` };
    }
    if (data.status) {
      query['status'] = { _eq: data.status };
    }
    // cloud_account_id is pushed down server-side via the
    // integrations_list action's extractFilterSQL extension. Lets the
    // caller fetch only integrations wired to one specific cloud account
    // (used by the workflow trigger picker) instead of pulling every
    // tenant integration and filtering client-side.
    if (data.cloudAccountId) {
      query['cloud_account_id'] = { _eq: data.cloudAccountId };
    }
    try {
      let response = await queryGraphQL(LIST_INTEGRATION.replaceAll('__WHERE__', gqlStringify(query)), 'ListIntegrations', {
        limit: data.limit || 10,
        offset: data.offset || 0,
      });
      return response;
    } catch (err) {
      console.log('failed to list integrations-', err);
      return err;
    }
  },

  addIntegrations: async function (data) {
    const ADD_INTEGRATION = `
    mutation AddIntegrations($data: CreateIntegrationConfigRequest!) {
      integrations_create_config(request: $data) {
        id
        name
        configs {
          value
          name
        }
        tags
      }
    }
    `;
    try {
      let response = await queryGraphQL(ADD_INTEGRATION, 'AddIntegrations', {
        data: data,
      });
      return response;
    } catch (err) {
      console.log('failed to add integrations-', err);
      return err;
    }
  },

  deleteIntegrations: async function (data) {
    const DELETE_INTEGRATION = `
    mutation DeleteIntegrationConfig($data: DeleteIntegrationConfigRequest!) {
      integrations_delete_config(request: $data) {
        status
      }
    }
    `;
    try {
      let response = await queryGraphQL(DELETE_INTEGRATION, 'DeleteIntegrationConfig', {
        data: data,
      });
      return response;
    } catch (err) {
      console.log('failed to delete integrations-', err);
      return err;
    }
  },

  listIntegrationSchema: async function (data) {
    const LIST_INTEGRATION_SCHEMA = `
    query ListIntegrationSchema($data: IntegrationSchemaRequest!) {
      integrations_get_schema(request: $data) {
        data
      }
    }
    `;
    try {
      let response = await queryGraphQL(LIST_INTEGRATION_SCHEMA, 'ListIntegrationSchema', {
        data: data,
      });
      return response;
    } catch (err) {
      console.log('failed to list integrations schema- ', err);
      return err;
    }
  },

  // Fetches form-context-dependent autocomplete options for an integration
  // form field whose schema declares an AutoGenerateFunc. Returns
  // { options: [{ label, value }], message } — message is a user-visible
  // hint (e.g. "Re-enter password to load columns") when options is empty.
  getAutogenOptions: async function ({ autogen_func, form_values }) {
    const GET_AUTOGEN_OPTIONS = `
    query GetAutogenOptions($data: IntegrationAutogenOptionsRequest!) {
      integrations_autogen_options(request: $data) {
        options {
          label
          value
        }
        message
      }
    }
    `;
    try {
      const response = await queryGraphQL(GET_AUTOGEN_OPTIONS, 'GetAutogenOptions', {
        data: { autogen_func, form_values: form_values || {} },
      });
      return response?.data?.data?.integrations_autogen_options || { options: [], message: '' };
    } catch (err) {
      console.log('failed to fetch autogen options-', err);
      return { options: [], message: 'Failed to load suggestions.' };
    }
  },
  testIntegrationConnectionByConfig: async function (integrationName, accountIds, configValues, source, integrationId) {
    const TEST_INTEGRATION_CONNECTION_CONFIG = `
    mutation TestIntegrationConnectionConfig($data: IntegrationTestConnectionConfigRequest!) {
      integrations_check_connection_config(request: $data) {
        success
        message
        error
      }
    }
    `;
    try {
      // integration_id is optional; on edit the modal passes it so the backend
      // can augment blank secret fields with the stored (encrypted) values
      // before probing. Without it, an edit-time Test that doesn't re-type
      // the API key would probe with a blank secret and falsely fail auth.
      const data = {
        integration_name: integrationName,
        account_ids: accountIds,
        integration_config_values: configValues,
        source: source || 'user',
      };
      if (integrationId) {
        data.integration_id = integrationId;
      }
      let response = await queryGraphQL(TEST_INTEGRATION_CONNECTION_CONFIG, 'TestIntegrationConnectionConfig', { data });
      return response?.data?.data?.integrations_check_connection_config;
    } catch (err) {
      console.log('failed to test integration connection by config-', err);
      return { success: false, error: 'Failed to test connection' };
    }
  },

  testIntegrationConnection: async function (integrationId) {
    const TEST_INTEGRATION_CONNECTION = `
    mutation TestIntegrationConnection($data: IntegrationTestConnectionRequest!) {
      integrations_check_connection(request: $data) {
        success
        message
        error
      }
    }
    `;
    try {
      let response = await queryGraphQL(TEST_INTEGRATION_CONNECTION, 'TestIntegrationConnection', {
        data: { integration_id: integrationId },
      });
      return response?.data?.data?.integrations_check_connection;
    } catch (err) {
      console.log('failed to test integration connection-', err);
      return { success: false, error: 'Failed to test connection' };
    }
  },

  updateIntegrationStatus: async function (data) {
    const UPDATE_INTEGRATION = `
    mutation UpdateIntegrationConfig($data: DeleteIntegrationConfigRequest!) {
      integrations_update_status(request: $data) {
        status
      }
    }
    `;
    try {
      let response = await queryGraphQL(UPDATE_INTEGRATION, 'UpdateIntegrationConfig', {
        data: data,
      });
      return response;
    } catch (err) {
      console.log('failed to disable integrations-', err);
      return err;
    }
  },

  getWebhookIntegrationByWorkflowId: async function (workflowId) {
    const where = {
      type: { _eq: 'workflow_webhook' },
      status: { _eq: 'enabled' },
      config_value_name: { _eq: 'workflow_id' },
      config_value_value: { _eq: workflowId },
    };
    const GET_WEBHOOK_INTEGRATION = `
      query GetWebhookIntegrationByWorkflowId {
        integrations_list(where: ${gqlStringify(where)}, limit: 1) {
          rows {
            id
            name
            integration_config_values
          }
        }
      }
    `;
    try {
      let response = await queryGraphQL(GET_WEBHOOK_INTEGRATION, 'GetWebhookIntegrationByWorkflowId');
      const rows = response?.data?.data?.integrations_list?.rows || [];
      const integrations = rows.map((row) => ({
        id: row.id,
        name: row.name,
        integration_config_values:
          typeof row.integration_config_values === 'string' ? JSON.parse(row.integration_config_values) : row.integration_config_values || [],
      }));
      return { data: { data: { integrations } } };
    } catch (err) {
      console.log('failed to get webhook integration-', err);
      return err;
    }
  },
};

export default apiIntegrations;
