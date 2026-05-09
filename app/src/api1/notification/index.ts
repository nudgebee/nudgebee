import { gqlStringify, queryGraphQL } from '@lib/HttpService';

export const LIST_INSTALLED_NOTIFICATION_TOOLS = `query ListInstalledNotificationTools {
    messaging_platform_list {
      data {
        id
        platform
      }
    }
  }
  `;

export const LIST_NOTIFICATION_RULES = `query ListNotificationRules($limit: Int,$offset: Int) {
  admin_get_notification_rules_v2(limit: $limit, offset: $offset,where:__WHERE__, order_by: [{column: "created_at", order: desc}]) {
    rows {
      account_id
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
  admin_get_notification_rules_grouping_v2(where:__WHERE__) {
    rows {
      count
    }
  }
}
`;

// Insertion Queries

export const INSERT_NOTIFICATION_RULE = `
mutation InsertNotificationRule($object: notification_rule_upsert_input!) {
  notification_rule_upsert_one(rule: $object) {
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

const apiNotifications = {
  async getInstalledTools() {
    try {
      const response = await queryGraphQL(LIST_INSTALLED_NOTIFICATION_TOOLS, 'ListInstalledNotificationTools');
      const platforms = response?.data?.data?.messaging_platform_list?.data || [];
      return { messaging_platforms: platforms };
    } catch {
      console.log('failed to fetch installed notifications tools');
      return { messaging_platforms: [] };
    }
  },
  async getNotificationRules(query: any, limit: number, offset: number) {
    try {
      const queryParams: any = {};
      if (query.accountId) {
        queryParams['account_id'] = { _eq: query.accountId };
      } else if (query.isAccountNull) {
        queryParams['account_id'] = { _is_null: true };
      }
      if (query.namespace) {
        queryParams['namespace'] = { _eq: query.namespace };
      }
      if (query.workload) {
        queryParams['workload'] = { _eq: query.workload };
      }
      const constructedQuery = LIST_NOTIFICATION_RULES.replaceAll('__WHERE__', gqlStringify(queryParams));
      const response = await queryGraphQL(constructedQuery, 'ListNotificationRules', { limit: limit, offset: offset });
      return { data: response?.data.data };
    } catch (error) {
      console.log(error);
      return error;
    }
  },
  async insertNotificationRule(query: any) {
    try {
      const mappingObject: any = {};
      if (query.cluster) {
        mappingObject.cluster = query.cluster;
      }
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
      if (query.accountId) {
        mappingObject.account_id = query.accountId;
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
    try {
      const response = await queryGraphQL(INSERT_NOTIFICATION_CHANNEL_ACCOUNT_MAPPING, 'InsertNotificationChannelAccountMapping', data);
      return { data: response?.data?.data };
    } catch (e) {
      console.log('Error inserting channel account mapping:', e);
      return { error: e };
    }
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
    try {
      const response = await queryGraphQL(UPDATE_NOTIFICATION_CHANNEL_ACCOUNT_MAPPING, 'UpdateNotificationChannelAccountMapping', data);
      return { data: response?.data?.data };
    } catch (e) {
      console.log('Error updating channel account mapping:', e);
      return { error: e };
    }
  },
};

export default apiNotifications;
