import { gqlStringify, queryGraphQL, unwrapGraphQL } from '@lib/HttpService';
import apiIntegrations from '@api1/integrations';
import { safeJSONParse } from 'src/utils/common';

export const LIST_INSTALLED_NOTIFICATION_TOOLS = `query ListInstalledNotificationTools {
    messagingplatforms_list {
      data {
        id
        platform
      }
    }
  }
  `;

export const LIST_NOTIFICATION_RULES = `query ListNotificationRules($limit: Int,$offset: Int) {
  notifications_list_rules(limit: $limit, offset: $offset,where:__WHERE__, order_by: [{column: "created_at", order: desc}]) {
    rows {
      account_id
      scope_mode
      account_ids
      exceptions
      source_mode
      sources
      is_active
      event_types
      id
      source
      created_at
      cluster
      description
      aggregation_key
      expires_at
      workload
      name
      namespace
      is_suppressed
      created_by
      severity
      delivery_mode
      frequency
      tenant_id
      created_by_display_name
      notification_rule_mappings
    }
  }
  notifications_aggregate_rules(where:__WHERE__) {
    rows {
      count
    }
  }
}
`;

// Only automation rules carry event types, and those rules exist only where the
// server knows the column, so this is fetched per rule rather than in the list.
export const GET_NOTIFICATION_RULE_EVENT_TYPES = `query GetNotificationRuleEventTypes {
  notifications_list_rules(limit: 1, offset: 0, where:__WHERE__) {
    rows {
      id
      event_types
    }
  }
}
`;

export const LIST_NOTIFICATION_DEFAULT_POLICIES = `query ListNotificationDefaultPolicies {
  notifications_list_default_policies {
    policies {
      source
      action
      mappings
    }
    error
  }
}
`;

export const UPSERT_NOTIFICATION_DEFAULT_POLICY = `
mutation UpsertNotificationDefaultPolicy($request: notification_default_policy_input!) {
  notifications_upsert_default_policy(request: $request) {
    source
    action
    mappings
    error
  }
}
`;

export const CHECK_NOTIFICATION_ROUTE = `query CheckNotificationRoute($request: notification_route_check_input!) {
  notifications_check_route(request: $request) {
    result
    reason
    stopped_by
    rules
    channels
    default_channels
    error
  }
}
`;

// Insertion Queries

export const INSERT_NOTIFICATION_RULE = `
mutation InsertNotificationRule($object: notification_rule_upsert_input!) {
  notifications_upsert_rule(rule: $object) {
    id
    error
  }
}
`;

export const DELETE_NOTIFICATION_RULE = `
mutation DeleteNotificationRule($id: String!) {
  notification_rule_delete(id: $id) {
    id
  }
}`;

export const INSERT_NOTIFICATION_CHANNEL_ACCOUNT_MAPPING = `
mutation InsertNotificationChannelAccountMapping($ac_id: String, $platform: String!, $team_id: String, $channel_id: String!) {
  notification_channel_mapping_create(account_id: $ac_id, platform: $platform, team_id: $team_id, channel_id: $channel_id) {
    id
    created_by
    created_at
    channel_id
    account_id
    platform
    team_id
  }
}`;

export const LIST_NOTIFICATION_CHANNEL_ACCOUNT_MAPPINGS = `
query ListNotificationChannelAccountMappings($where: NotificationChannelAccountMappingWhereRequest) {
  notification_channel_account_mappings: notification_channel_account_mapping_v2(where: $where, order_by: [{column: "created_at", order: desc}]) {
    rows {
      id
      account_id
      platform
      team_id
      channel_id
      created_at
      created_by
      account_name
      cloud_provider
      created_by_display_name
    }
  }
}`;

export const DELETE_NOTIFICATION_CHANNEL_ACCOUNT_MAPPING = `
mutation DeleteNotificationChannelAccountMapping($id: String!) {
  notification_channel_mapping_delete(id: $id) {
    id
  }
}`;

export const UPDATE_NOTIFICATION_CHANNEL_ACCOUNT_MAPPING = `
mutation UpdateNotificationChannelAccountMapping($id: String!, $account_id: String, $team_id: String, $channel_id: String) {
  notification_channel_mapping_update(id: $id, account_id: $account_id, team_id: $team_id, channel_id: $channel_id) {
    id
    account_id
    team_id
    channel_id
  }
}`;

export const LIST_WATCHABLE_CHANNELS = `query ListWatchableChannels($platform: String!, $team_id: String) {
  notifications_list_watchable_channels(platform: $platform, team_id: $team_id) {
    data
    team_id
    partial
    error
  }
}`;

export const ENABLE_CHANNEL_WATCH = `mutation EnableChannelWatch($platform: String!, $channel_id: String!, $team_id: String, $channel_name: String) {
  notifications_enable_channel_watch(platform: $platform, channel_id: $channel_id, team_id: $team_id, channel_name: $channel_name) {
    data
    error
  }
}`;

export const DISABLE_CHANNEL_WATCH = `mutation DisableChannelWatch($platform: String!, $channel_id: String!, $team_id: String) {
  notifications_disable_channel_watch(platform: $platform, channel_id: $channel_id, team_id: $team_id) {
    data
    error
  }
}`;

// Watch actions return {data} on success and {error:{message}} with a 4xx status
// on failure; queryGraphQL hands back the raw response either way, so unwrap
// every shape a failure can arrive in and keep the server's message intact.
function unwrapWatchAction(response: any, action: string) {
  const payload = response?.data?.data?.[action];
  if (payload && (payload.data !== undefined || payload.error !== undefined)) {
    return payload;
  }
  const message = response?.data?.error?.message || response?.data?.errors?.[0]?.message;
  return { error: { message: message || 'Request failed' } };
}

// What happens to notifications of a type that no rule matches. "all" is the
// setting every type follows unless it has its own; "inherit" removes a type's own.
export type NotificationDefaultAction = 'deliver_defaults' | 'deliver_channels' | 'drop' | 'inherit';
export interface NotificationDefaultPolicy {
  source: string;
  action: NotificationDefaultAction;
  mappings?: { platform: string; channels: any }[];
}

export interface NotificationRouteCheckRequest {
  source: string;
  event?: string | null;
  account_id?: string | null;
  namespace?: string | null;
  workload?: string | null;
  aggregation_key?: string | null;
  severity?: string | null;
}

export interface NotificationRouteCheck {
  result: 'sent' | 'not_sent' | 'held' | 'paused';
  reason: 'rule' | 'default_policy' | 'severity' | 'always_sent';
  stopped_by: { id: string; name?: string } | null;
  rules: { id: string; name?: string }[];
  channels: { platform: string; channels: any; rule_id: string | null }[];
  default_channels: boolean;
}

const firstError = (response: any, action: string) =>
  response?.data?.data?.[action]?.error || response?.data?.errors?.[0]?.message || response?.data?.error?.message;

const apiNotifications = {
  async getInstalledTools() {
    try {
      const response = await queryGraphQL(LIST_INSTALLED_NOTIFICATION_TOOLS, 'ListInstalledNotificationTools');
      const platforms = response?.data?.data?.messagingplatforms_list?.data || [];
      // Slack/MS Teams/Discord may live in the integrations table now; surface them so
      // the rule builder's platform toggles light up. Integration installs win (one per tenant).
      let integrationPlatforms: any[] = [];
      try {
        const res: any = await apiIntegrations.listIntegrations({ type: ['slack', 'ms_teams', 'discord'], limit: 50 });
        const rows = res?.data?.data?.integrations_list?.rows || [];
        integrationPlatforms = rows.map((r: any) => ({ id: r.id, platform: r.type }));
      } catch {
        integrationPlatforms = [];
      }
      const overridden = new Set(integrationPlatforms.map((p) => p.platform));
      const merged = platforms.filter((p: any) => !overridden.has(p.platform)).concat(integrationPlatforms);
      return { messaging_platforms: merged };
    } catch {
      console.log('failed to fetch installed notifications tools');
      return { messaging_platforms: [] };
    }
  },
  async getNotificationRules(query: any, limit: number, offset: number) {
    try {
      if (query.accountId === 'demo') {
        return { data: { notifications_list_rules: { rows: [] }, notifications_aggregate_rules: { rows: [{ count: 0 }] } } };
      }
      const queryParams: any = {};
      if (query.accountId) {
        // A rule matches when the account is any one of its accounts.
        queryParams['account_ids'] = { _contains: JSON.stringify([query.accountId]) };
      } else if (query.isAccountNull) {
        queryParams['account_id'] = { _is_null: true };
      }
      if (query.source) {
        queryParams['source'] = { _eq: query.source };
      } else if (query.excludeSource) {
        queryParams['source'] = { _neq: query.excludeSource };
      }
      if (query.namespace) {
        queryParams['namespace'] = { _eq: query.namespace };
      }
      if (query.workload) {
        queryParams['workload'] = { _eq: query.workload };
      }
      const constructedQuery = LIST_NOTIFICATION_RULES.replaceAll('__WHERE__', gqlStringify(queryParams));
      const response = await queryGraphQL(constructedQuery, 'ListNotificationRules', { limit: limit, offset: offset });
      const errors = response?.data?.errors;
      if (errors?.length) {
        return { error: errors[0]?.message || 'Failed to load notification rules' };
      }
      return { data: response?.data.data };
    } catch (error) {
      console.log(error);
      return { error: 'Failed to load notification rules' };
    }
  },
  async getNotificationRuleEventTypes(ruleId: string): Promise<string[]> {
    try {
      const constructedQuery = GET_NOTIFICATION_RULE_EVENT_TYPES.replaceAll('__WHERE__', gqlStringify({ id: { _eq: ruleId } }));
      const response = await queryGraphQL(constructedQuery, 'GetNotificationRuleEventTypes', {});
      const raw = response?.data?.data?.notifications_list_rules?.rows?.[0]?.event_types;
      // The rules list returns json columns as encoded strings.
      const eventTypes = typeof raw === 'string' ? safeJSONParse(raw) : raw;
      return Array.isArray(eventTypes) ? eventTypes.filter((e: unknown): e is string => typeof e === 'string') : [];
    } catch (error) {
      console.log(error);
      return [];
    }
  },
  // Every rule but the AI cost report's (managed on the Cost Report tab).
  async listRules(): Promise<{ rows?: any[]; error?: string }> {
    const res: any = await apiNotifications.getNotificationRules({ excludeSource: 'ai_cost' }, 1000, 0);
    if (res?.error) return { error: res.error };
    return { rows: res?.data?.notifications_list_rules?.rows || [] };
  },
  async upsertRule(object: Record<string, any>): Promise<{ id?: string; error?: string }> {
    try {
      const response = await queryGraphQL(INSERT_NOTIFICATION_RULE, 'InsertNotificationRule', { object });
      const error = firstError(response, 'notifications_upsert_rule');
      if (error) {
        return {
          error: String(error).toLowerCase().includes('unique constraint') ? 'A notification rule with this name already exists.' : String(error),
        };
      }
      return { id: response?.data?.data?.notifications_upsert_rule?.id };
    } catch (error) {
      console.log(error);
      return { error: 'Couldn’t save the rule. Try again in a minute.' };
    }
  },
  async getDefaultPolicies(): Promise<{ policies?: NotificationDefaultPolicy[]; error?: string }> {
    try {
      const response = await queryGraphQL(LIST_NOTIFICATION_DEFAULT_POLICIES, 'ListNotificationDefaultPolicies', {});
      const error = firstError(response, 'notifications_list_default_policies');
      if (error) return { error };
      const policies = response?.data?.data?.notifications_list_default_policies?.policies || [];
      return {
        policies: policies.map((p: any) => ({ ...p, mappings: typeof p.mappings === 'string' ? safeJSONParse(p.mappings) || [] : p.mappings || [] })),
      };
    } catch (error) {
      console.log(error);
      return { error: 'Couldn’t load what happens when no rule matches.' };
    }
  },
  async upsertDefaultPolicy(request: NotificationDefaultPolicy): Promise<{ error?: string }> {
    try {
      const response = await queryGraphQL(UPSERT_NOTIFICATION_DEFAULT_POLICY, 'UpsertNotificationDefaultPolicy', { request });
      const error = firstError(response, 'notifications_upsert_default_policy');
      return error ? { error } : {};
    } catch (error) {
      console.log(error);
      return { error: 'Couldn’t save what happens when no rule matches.' };
    }
  },
  async checkRoute(request: NotificationRouteCheckRequest): Promise<{ answer?: NotificationRouteCheck; error?: string }> {
    try {
      const response = await queryGraphQL(CHECK_NOTIFICATION_ROUTE, 'CheckNotificationRoute', { request });
      const error = firstError(response, 'notifications_check_route');
      if (error) return { error };
      const raw = response?.data?.data?.notifications_check_route || {};
      const parse = (value: any) => (typeof value === 'string' ? safeJSONParse(value) : value);
      return {
        answer: {
          result: raw.result,
          reason: raw.reason,
          stopped_by: parse(raw.stopped_by) || null,
          rules: parse(raw.rules) || [],
          channels: parse(raw.channels) || [],
          default_channels: raw.default_channels === true || raw.default_channels === 'true',
        },
      };
    } catch (error) {
      console.log(error);
      return { error: 'Couldn’t check where it would go. Try again in a minute.' };
    }
  },
  async insertNotificationRule(query: any) {
    try {
      if (query.accountId === 'demo') {
        return {
          data: {
            data: null,
            errors: [{ message: 'Insertion of notification rules is not supported for Demo account.' }],
          },
        };
      }
      const mappingObject: any = {};
      if (query.ruleName) {
        mappingObject.name = query.ruleName;
      }
      if (query.source) {
        mappingObject.source = query.source;
      }
      mappingObject.is_suppressed = query.isSuppressed;
      if (query.namespace) {
        mappingObject.namespace = query.namespace;
      }
      if (query.workload) {
        mappingObject.workload = query.workload;
      } else {
        mappingObject.workload = null;
      }
      if (query.expires_at) {
        mappingObject.expires_at = query.expires_at;
      } else {
        mappingObject.expires_at = null;
      }
      if (query.aggregation_key) {
        mappingObject.aggregation_key = query.aggregation_key;
      }
      if (query.workload) {
        mappingObject.workload = query.workload;
      } else {
        mappingObject.workload = null;
      }
      if (query.mappings !== undefined) {
        mappingObject.mappings = query.mappings;
      }
      if (query.id) {
        mappingObject.id = query.id;
      }
      if (Array.isArray(query.accountIds)) {
        mappingObject.account_ids = query.accountIds;
      }
      if (query.description) {
        mappingObject.description = query.description;
      }
      if (query.severity) {
        mappingObject.severity = query.severity;
      }
      if (query.delivery) {
        mappingObject.delivery_mode = query.delivery;
      }
      if (query.frequency) {
        mappingObject.frequency = query.frequency;
      }
      if (Array.isArray(query.event_types)) {
        mappingObject.event_types = query.event_types;
      }

      const response = await queryGraphQL(INSERT_NOTIFICATION_RULE, 'InsertNotificationRule', { object: mappingObject });
      return { data: response?.data };
    } catch (e) {
      console.log(e);
      return e;
    }
  },
  async deleteNotificationRule(id: any) {
    try {
      const response = await queryGraphQL(DELETE_NOTIFICATION_RULE, 'DeleteNotificationRule', { id: id });
      return { data: response?.data };
    } catch (e) {
      console.log(e);
      return e;
    }
  },
  async insertChannelAccountMapping(data: { ac_id: string; platform: string; team_id: string; channel_id: string }) {
    const response = await queryGraphQL(INSERT_NOTIFICATION_CHANNEL_ACCOUNT_MAPPING, 'InsertNotificationChannelAccountMapping', data);
    return { data: unwrapGraphQL(response, 'Failed to create channel mapping') };
  },
  async listChannelAccountMappings(platform: string) {
    try {
      const where: { platform?: { _eq: string } } = {};
      if (platform) {
        where.platform = { _eq: platform };
      }
      const response = await queryGraphQL(LIST_NOTIFICATION_CHANNEL_ACCOUNT_MAPPINGS, 'ListNotificationChannelAccountMappings', { where });
      const rows = (response?.data?.data?.notification_channel_account_mappings?.rows || []).map((row: any) => ({
        ...row,
        cloud_account: { id: row.account_id, account_name: row.account_name, cloud_provider: row.cloud_provider },
        user_created_by: { display_name: row.created_by_display_name },
      }));
      return { data: rows };
    } catch (e) {
      console.log('Error listing channel account mappings:', e);
      return { error: e, data: [] };
    }
  },
  async deleteChannelAccountMapping(id: string) {
    try {
      const response = await queryGraphQL(DELETE_NOTIFICATION_CHANNEL_ACCOUNT_MAPPING, 'DeleteNotificationChannelAccountMapping', { id });
      return { data: response?.data };
    } catch (e) {
      console.log('Error deleting channel account mapping:', e);
      return { error: e };
    }
  },
  async updateChannelAccountMapping(data: { id: string; account_id: string; team_id: string; channel_id: string }) {
    if (data.account_id === 'demo') {
      throw new Error('Updating channel account mapping is not supported for Demo account.');
    }
    const response = await queryGraphQL(UPDATE_NOTIFICATION_CHANNEL_ACCOUNT_MAPPING, 'UpdateNotificationChannelAccountMapping', data);
    return { data: unwrapGraphQL(response, 'Failed to update channel mapping') };
  },
  async listWatchableChannels(platform: string, teamId?: string) {
    try {
      const variables: any = { platform };
      if (teamId) {
        variables.team_id = teamId;
      }
      const response = await queryGraphQL(LIST_WATCHABLE_CHANNELS, 'ListWatchableChannels', variables);
      return unwrapWatchAction(response, 'notifications_list_watchable_channels');
    } catch (e) {
      console.log('Error listing watchable channels:', e);
      return { error: { message: 'Failed to load channels' } };
    }
  },
  async enableChannelWatch(params: { platform: string; channelId: string; teamId?: string; channelName?: string }) {
    try {
      const variables: any = { platform: params.platform, channel_id: params.channelId };
      if (params.teamId) {
        variables.team_id = params.teamId;
      }
      if (params.channelName) {
        variables.channel_name = params.channelName;
      }
      const response = await queryGraphQL(ENABLE_CHANNEL_WATCH, 'EnableChannelWatch', variables);
      return unwrapWatchAction(response, 'notifications_enable_channel_watch');
    } catch (e) {
      console.log('Error enabling channel watch:', e);
      return { error: { message: 'Failed to start watching the channel' } };
    }
  },
  async disableChannelWatch(params: { platform: string; channelId: string; teamId?: string }) {
    try {
      const variables: any = { platform: params.platform, channel_id: params.channelId };
      if (params.teamId) {
        variables.team_id = params.teamId;
      }
      const response = await queryGraphQL(DISABLE_CHANNEL_WATCH, 'DisableChannelWatch', variables);
      return unwrapWatchAction(response, 'notifications_disable_channel_watch');
    } catch (e) {
      console.log('Error disabling channel watch:', e);
      return { error: { message: 'Failed to stop watching the channel' } };
    }
  },
};

export default apiNotifications;
