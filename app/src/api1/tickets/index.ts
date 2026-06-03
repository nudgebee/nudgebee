import { gqlStringify, queryGraphQL } from '@lib/HttpService';
import cache from '@lib/cache';

export const LIST_TICKET_CONFIGURATIONS = `
query ListTicketConfigurations {
  integrations: integrations_list(where: __WHERE__, order_by: [{column: "name", order: asc}]) {
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
}
`;

export const CREATE_TICKET = `
mutation CreateTicket($assignee: String, $integration_id: String, $reference_id: String, $ticket_type: String, $project_key: String, $description: String, $title: String, $source: String, $severity: String, $account_id: String, $additional_fields: jsonb) {
  tickets_create(object: {assignee: $assignee, integration_id: $integration_id, reference_id: $reference_id, ticket_type: $ticket_type, project_key: $project_key, description: $description, title: $title, source: $source, severity: $severity, account_id: $account_id, additional_fields: $additional_fields}) {
    data {
      insert_tickets_one {
        id
        error
        action
        message
        ticket_id
        url
      }
    }
  }
}
`;

export const LIST_TICKETS = `
query ListJiraTickets($limit:Int!, $offset:Int!) {
  tickets_aggregate: ticket_groupings_v2(where:__WHERE__){
    rows{
      count
    }
  }
  tickets: tickets_list(limit:$limit, offset:$offset, where:__WHERE__, order_by:[{column: "created_at", order: desc}]) {
    rows {
      id
      assignee
      integration_id
      reference_id
      url
      created_by_display_name
      ticket_id
      ticket_type
      status
      created_at
      severity
      description
      title
      source
      platform
      account_id
      updated_at
      project_key
    }
  }
}
`;

export const LIST_TOOL = `
query ListJiraTool {
  integrations: integrations_aggregate(where: __WHERE__, column_transformations: [{expr: "distinct", name: "type"}]) {
    rows {
      type
    }
  }
}
`;

export const LIST_SEVERITY = `
query ListJiraSeverity{
  tickets: ticket_groupings_v2(column_transformations: {expr: "distinct", name: "severity"}) {
    rows {
      severity
    }
  }
}
`;

export const LIST_STATUS = `
query ListJiraStatus{
  tickets: ticket_groupings_v2(column_transformations: {expr: "distinct", name: "status"}) {
    rows {
      status
    }
  }
}
`;

export const LIST_ASSIGNEE = `
query ListJiraAssignee{
  tickets: ticket_groupings_v2(column_transformations: {expr: "distinct", name: "assignee"}) {
    rows {
      assignee
    }
  }
}
`;

export const LIST_SUMMARY = `
query ListJiraSummary{
  tickets_aggregate: ticket_groupings_v2(where:__WHERE__){
    rows{
      count
    }
  }
  severity_groupings: ticket_groupings_v2(group_by:["severity"], columns:["severity", "count"],  where:__WHERE__){
    rows{
      severity
      count  
    }
  }
  status_groupings: ticket_groupings_v2(group_by:["status"], columns:["status", "count"], where:__WHERE__){
    rows{
      status
      count  
    }
  }
  today_status_groupings: ticket_groupings_v2(group_by:["status"], columns:["status", "count"], where:__WHERE_TODAY__){
    rows{
      status
      count  
    }
  }
}
`;

const GET_JIRA_META = `
query GetJiraMeta($integration_id: String!, $project_key: String!) {
  tickets_get_create_meta(integration_id: $integration_id, project_key: $project_key) {
    data
  }
}
`;

const GET_FIELD_VALUES = `
query GetFieldValues($integration_id: String!, $auto_complete_url: String!, $key: String!, $search: String!) {
  tickets_get_field_values(integration_id: $integration_id, key: $key, url: $auto_complete_url, search_term: $search) {
    data
  }
}
`;

const GET_TICKET_COMMENTS = `
query GetCommentsQuery($account_id: String!, $integration_id: String!, $source: String!, $ticket_id: String!) {
  ticket_get_comments(object: {account_id: $account_id, integration_id: $integration_id, source: $source, ticket_id: $ticket_id}) {
    ticket_id
    error
    comments {
      author
      comment
      created_at
      updated_at
    }
  }
}
`;

const ADD_TICKET_COMMENT = `
mutation UpdateCommentsQuery($account_id: String!, $integration_id: String!, $source: String!, $ticket_id: String!, $comment: String!) {
  ticket_add_comment(
    object: {
      account_id: $account_id,
      integration_id: $integration_id,
      source: $source,
      ticket_id: $ticket_id,
      comment: $comment
    }) {
    ticket_id
    error
    comments {
      author
      comment
      created_at
      updated_at
    }
  }
}
`;

// Helper function to transform integrations response to legacy jira_configurations format
const transformIntegrationToLegacyFormat = (integration: any) => {
  const rawConfigValues = integration.integration_config_values;
  const configValues = typeof rawConfigValues === 'string' ? JSON.parse(rawConfigValues) : rawConfigValues || [];
  const getConfigValue = (name: string) => {
    const config = configValues.find((c: any) => c.name === name);
    return config ? config.value : null;
  };

  // Map status: enabled → Active, disabled → Disabled
  const status = integration.status === 'enabled' ? 'Active' : 'Disabled';
  const is_active = integration.status === 'enabled';

  // Parse JSON fields
  const parseJSON = (value: string | null) => {
    if (!value) {
      return null;
    }
    try {
      return JSON.parse(value);
    } catch {
      return null;
    }
  };

  return {
    id: integration.id,
    name: integration.name,
    tool: integration.type,
    status: status,
    is_active: is_active,
    url: getConfigValue('url'),
    username: getConfigValue('username'),
    auth_type: getConfigValue('auth_type'),
    projects: parseJSON(getConfigValue('projects')),
    priorities: parseJSON(getConfigValue('priorities')),
    users: parseJSON(getConfigValue('users')),
    // Keep in sync with the duplicate transform in @api1/integrations.
    sync_knowledge_base: getConfigValue('sync_knowledge_base') === 'true',
    rca_writeback_enabled: getConfigValue('rca_writeback_enabled') === 'true',
    last_connected: getConfigValue('last_connected') || integration.updated_at,
    created_by: integration.created_by,
    user: { display_name: integration.created_by_display_name },
  };
};

const apiIntegrations = {
  listTicketConfigurations: async function (query?: any, refresh = false) {
    try {
      let cacheResponse = cache.get('tickets.listTicketConfigurations');
      if (!cacheResponse || refresh) {
        const gqlQuery: any = {};
        // Map is_active to status (enabled/disabled)
        gqlQuery.status = { _eq: 'enabled' };
        // Filter for only ticketing-related integrations
        const ticketingTypes = ['jira', 'github', 'gitlab', 'servicenow', 'pagerduty', 'zenduty'];
        if (query?.tool) {
          gqlQuery.type = { _eq: query.tool };
        } else {
          gqlQuery.type = { _in: ticketingTypes };
        }
        const response = await queryGraphQL(LIST_TICKET_CONFIGURATIONS.replaceAll('__WHERE__', gqlStringify(gqlQuery)), 'ListTicketConfigurations');

        // Transform integrations to legacy jira_configurations format
        const integrations = response.data.data.integrations?.rows || [];
        const transformed = integrations.map(transformIntegrationToLegacyFormat);

        cache.set('tickets.listTicketConfigurations', transformed, 60 * 60);
        cacheResponse = transformed;
      }
      return {
        data: cacheResponse,
      };
    } catch (err) {
      console.log('Your Error is', err);
      return err;
    }
  },

  listTool: async function () {
    try {
      // Filter for only ticketing-related integrations
      const ticketingTypes = ['jira', 'github', 'gitlab', 'servicenow', 'pagerduty', 'zenduty'];
      const gqlQuery = {
        type: { _in: ticketingTypes },
        status: { _eq: 'enabled' },
      };
      const response = await queryGraphQL(LIST_TOOL.replaceAll('__WHERE__', gqlStringify(gqlQuery)), 'ListJiraTool');
      const tools = (response.data?.data?.integrations?.rows || []).map((config: any) => config.type).filter((t: string) => t);

      return {
        data: {
          tool: tools,
        },
      };
    } catch (err) {
      console.log(err);
      return err;
    }
  },

  listTickets: async function ({
    limit = 10,
    offset = 0,
    where = {},
  }: {
    limit?: number;
    offset?: number;
    where?: { status?: string; severity?: string; assignee?: string; title?: string; createdBy?: string; tool?: string; account_id?: string };
  }) {
    try {
      let query = LIST_TICKETS;
      const whereQuery: any = {};
      if (where?.status) {
        whereQuery['status'] = { _eq: where.status };
      }
      if (where?.severity) {
        whereQuery['severity'] = { _eq: where.severity };
      }
      if (where?.assignee) {
        whereQuery['assignee'] = { _eq: where.assignee };
      }
      if (where?.title) {
        whereQuery['title'] = { _ilike: '%' + where.title + '%' };
      }
      if (where?.createdBy) {
        whereQuery['created_by'] = { _eq: where.createdBy };
      }
      if (where?.tool) {
        whereQuery['platform'] = { _eq: where.tool };
      }
      if (where?.account_id) {
        whereQuery['account_id'] = { _eq: where.account_id };
      }
      query = LIST_TICKETS.replaceAll('__WHERE__', gqlStringify(whereQuery));

      const response = await queryGraphQL(query, 'ListJiraTickets', { limit, offset });
      const ticketRows = response.data?.data?.tickets?.rows?.map((item: any) => ({
        ...item,
        user: { display_name: item.created_by_display_name },
      }));
      return {
        data: {
          tickets: ticketRows,
          count: response.data?.data?.tickets_aggregate?.rows?.[0]?.count,
        },
      };
    } catch (err) {
      console.log('Your Error is', err);
      return err;
    }
  },

  listPriority: async function () {
    try {
      let query = LIST_SEVERITY;
      const where: any = {};
      query = LIST_SEVERITY.replaceAll('__WHERE__', gqlStringify(where));
      const response = await queryGraphQL(query, 'ListJiraSeverity');
      return {
        data: {
          priority: (response.data?.data?.tickets?.rows || []).map((ticket: any) => ticket.severity).filter((s: string) => s),
        },
      };
    } catch (err) {
      console.log('Your Error is', err);
      return err;
    }
  },

  listStatus: async function () {
    try {
      let query = LIST_STATUS;
      const where: any = {};
      query = LIST_STATUS.replaceAll('__WHERE__', gqlStringify(where));
      const response = await queryGraphQL(query, 'ListJiraStatus');
      return {
        data: {
          status: (response.data?.data?.tickets?.rows || []).map((ticket: any) => ticket.status),
        },
      };
    } catch (err) {
      console.log('Your Error is', err);
      return err;
    }
  },

  listAssignee: async function () {
    try {
      let query = LIST_ASSIGNEE;
      const where: any = {};
      query = LIST_ASSIGNEE.replaceAll('__WHERE__', gqlStringify(where));
      const response = await queryGraphQL(query, 'ListJiraAssignee');
      return {
        data: {
          assignee: (response.data?.data?.tickets?.rows || []).map((ticket: any) => ticket.assignee).filter((a: string) => a),
        },
      };
    } catch (err) {
      console.log('Your Error is', err);
      return err;
    }
  },

  getSummary: async function (query: any) {
    try {
      if (query.account_id === 'demo') {
        return {
          data: {
            total_count: 0,
            status_groupings: [],
            severity_groupings: [],
            today_status_groupings: [],
          },
        };
      }
      const currentDate = new Date();
      let where: any = {};
      if (query?.assignee) {
        where['assignee'] = { _eq: query.assignee };
      }
      if (query?.status) {
        where['status'] = { _eq: query.status };
      }
      if (query?.severity) {
        where['severity'] = { _eq: query.severity };
      }
      if (query?.tool) {
        where['platform'] = { _eq: query.tool };
      }
      if (query?.title) {
        where['title'] = { _ilike: '%' + query.title + '%' };
      }
      if (query?.account_id) {
        where['account_id'] = { _eq: query.account_id };
      }
      let whereQuery = LIST_SUMMARY.replaceAll('__WHERE__', gqlStringify(where));
      where = {
        ...where,
        created_at: { _eq: `${currentDate.getFullYear()}-${currentDate.getMonth() + 1}-${currentDate.getDate()}` },
      };
      whereQuery = whereQuery.replaceAll('__WHERE_TODAY__', gqlStringify(where));
      const response = await queryGraphQL(whereQuery, 'ListJiraSummary');
      return {
        data: {
          total_count: response.data?.data?.tickets_aggregate?.rows?.[0]?.count ?? 0,
          status_groupings: response.data?.data?.status_groupings?.rows ?? [],
          severity_groupings: response.data?.data?.severity_groupings?.rows ?? [],
          today_status_groupings: response.data?.data?.today_status_groupings?.rows ?? [],
        },
      };
    } catch (err) {
      console.log('Your Error is', err);
      return err;
    }
  },

  createTicket: async function (data: any) {
    try {
      const response = await queryGraphQL(CREATE_TICKET, 'CreateTicket', data);
      return {
        data: response,
      };
    } catch (err) {
      console.log('Your Error is', err);
      return err;
    }
  },
  getTicketMeta: async function (configuration_id: string, project_key: string) {
    // Create-meta is one of Jira's most expensive endpoints. Cache per
    // (integration, project) for 5 minutes — long enough to absorb sidebar
    // re-renders and tab switches, short enough that admins editing a project
    // don't see stale metadata for long.
    const cacheKey = `tickets.getTicketMeta:${configuration_id}:${project_key}`;
    const cached = cache.get(cacheKey);
    if (cached) return cached;
    try {
      const response = await queryGraphQL(GET_JIRA_META, 'GetJiraMeta', {
        integration_id: configuration_id,
        project_key: project_key,
      });
      const data = response?.data || [];
      cache.set(cacheKey, data, 5 * 60);
      return data;
    } catch (err) {
      console.log('failed to fetch ticket metadata- ', err);
      return err;
    }
  },
  getTicketFieldValues: async function (configuration_id: string, key: string, auto_complete_url: string, search: string) {
    try {
      const response = await queryGraphQL(GET_FIELD_VALUES, 'GetFieldValues', {
        integration_id: configuration_id,
        auto_complete_url: auto_complete_url,
        key: key,
        search: search,
      });
      return response?.data || [];
    } catch (err) {
      console.log('failed to fetch ticket field values- ', err);
      return err;
    }
  },
  listTicketsSummary: async function ({ reference_id }: { reference_id: string[] }) {
    if (reference_id.length === 0) {
      return {
        data: {
          tickets: [],
        },
      };
    }

    const LIST_TICKETS = `
    query ListTickets{
      tickets: tickets_list(where:__WHERE__, order_by:[{column: "created_at", order: desc}]) {
        rows {
          reference_id
          url
          ticket_id
        }
      }
    }
    `;
    try {
      const whereQuery: any = {};
      whereQuery['reference_id'] = { _in: reference_id };
      const query = LIST_TICKETS.replaceAll('__WHERE__', gqlStringify(whereQuery));
      const response = await queryGraphQL(query, 'ListTickets', {});
      return {
        data: {
          tickets: response?.data?.data?.tickets?.rows ?? [],
        },
      };
    } catch (err) {
      console.log('Your Error is', err);
      return err;
    }
  },

  getTicketComments: async function (account_id: string, configuration_id: string, source: string, ticket_id: string) {
    try {
      if (account_id === 'demo') {
        return {
          data: {
            comments: [],
            error: null,
          },
        };
      }
      const response = await queryGraphQL(GET_TICKET_COMMENTS, 'GetCommentsQuery', {
        account_id,
        integration_id: configuration_id,
        source,
        ticket_id,
      });
      return {
        data: {
          comments: response?.data?.data?.ticket_get_comments?.comments || [],
          error: response?.data?.data?.ticket_get_comments?.error,
        },
      };
    } catch (err) {
      console.log('Your Error is', err);
      return {
        data: {
          comments: [],
          error: 'Failed to fetch comments',
        },
      };
    }
  },

  addTicketComment: async function (account_id: string, configuration_id: string, source: string, ticket_id: string, comment: string) {
    try {
      if (account_id === 'demo') {
        return {
          data: {
            success: false,
            error: 'Insertion of comments is not supported for Demo account.',
            comments: [],
          },
        };
      }
      const response = await queryGraphQL(ADD_TICKET_COMMENT, 'UpdateCommentsQuery', {
        account_id,
        integration_id: configuration_id,
        source,
        ticket_id,
        comment,
      });
      return {
        data: {
          success: !response?.data?.data?.ticket_add_comment?.error,
          error: response?.data?.data?.ticket_add_comment?.error,
          comments: response?.data?.data?.ticket_add_comment?.comments || [],
        },
      };
    } catch (err) {
      console.log('Your Error is', err);
      return {
        data: {
          success: false,
          error: 'Failed to add comment',
          comments: [],
        },
      };
    }
  },
};

export default apiIntegrations;
