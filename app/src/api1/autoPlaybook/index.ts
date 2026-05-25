/**
 * TODO: Remove this entire module when the remaining components1/runbooks/ files
 * (BlockWithHeading.tsx, RunbookTargetResource.tsx, styles.ts) are migrated and deleted.
 * Runbook functionality has been replaced by Workflows.
 * This module is kept for backward compatibility with existing executions.
 */
import getMockData from '@api1/mock';
import { gqlStringify, queryGraphQL } from '@lib/HttpService';
import { validate as isValidUUID } from 'uuid';

export const AUTO_PLAYBOOK_LISTING = `
query AutoplaybookV2($limit: Int!, $offset: Int!) {
  auto_playbook_v2 (limit: $limit, offset: $offset, where: __WHERE__, order_by: __ORDERBY__) {
    rows {
      id
      name
      status
      last_executed_time
      created_at
      tasks
      trigger
      username
      display_name
      updated_username
      updated_user_displayname
      resource_filter
      attributes
    }
  }
  total_count: auto_playbook_grouping_v2(where: __WHERE__) {
    rows {
      count
    }
  }
}
`;

export const LIST_SLACK_CHANNELS = `
query listSlackChannels($platforms: String!){
  get_notification_channel_list: notification_get_channel_list(platform: $platforms) {
    data
  }
}
`;
export const LIST_MS_TEAMS_CHANNELS = `
query listMSTeamsChannels($platforms: String!){
  get_notification_channel_list: notification_get_channel_list(platform: $platforms) {
    data
  }
}
`;

export const SINGLE_CONFIG_UPDATE_AUTO_PILOT = `
mutation singleConfigUpdateAuotPilot($data: auto_optimize_update_one!) {
  autoOptimize_update_one: auto_optimize_update_one(arg1: $data) {
    id
  }
}`;

const apiAutoPlaybook = {
  async singleConfigUpdateAuotPilot(data?: any) {
    const response = await queryGraphQL(SINGLE_CONFIG_UPDATE_AUTO_PILOT, 'singleConfigUpdateAuotPilot', {
      data: data,
    });
    return {
      data: response?.data?.data?.autoOptimize_update_one?.id,
      errors: response?.data?.errors,
    };
  },
  async listAutoPlaybook(
    data: any,
    limit: number,
    offset: number,
    sortQuery: {
      sort_by: string;
      sort_order: string;
    }
  ) {
    if (data.accountId === 'demo' || data.account_id?._eq === 'demo') {
      const demoData = await getMockData('autoPlaybook');
      return {
        data: {
          auto_playbook_listing: {
            rows: demoData.autopilotListing.data.auto_playbook_listing,
          },
          total_count: {
            rows: [{ count: 5 }],
          },
        },
      };
    }

    const orderBy = [
      {
        column: sortQuery.sort_by,
        order: sortQuery.sort_order,
      },
    ];

    const query: any = {};
    query['account_id'] = { _eq: data.accountId };
    if (data.status) {
      query['status'] = { _eq: data.status };
    }
    if (data.selectedName) {
      if (typeof data.selectedName === 'string' && isValidUUID(data.selectedName)) {
        query['id'] = { _eq: data.selectedName };
      } else {
        query['name'] = { _ilike: '%' + data.selectedName + '%' };
      }
    }

    if (data.selectedApp) {
      query['resource_filter'] = { _contains: JSON.stringify([{ namespace: data.selectedApp.split(':')[1], name: data.selectedApp.split(':')[0] }]) };
    }
    if (data.eventAggregationKey) {
      if (data.eventAggregationKey == 'schedule') {
        query['trigger'] = { _has_key: 'schedule' };
      } else {
        query['trigger'] = { _contains: JSON.stringify({ event: { type: data.eventAggregationKey } }) };
      }
    }
    if (data.category) {
      query['trigger'] = { _has_key: data.category };
    }

    const formattedQuery = AUTO_PLAYBOOK_LISTING.replaceAll('__WHERE__', gqlStringify(query)).replace(
      '__ORDERBY__',
      gqlStringify(orderBy, ['order'])
    );

    const response = await queryGraphQL(formattedQuery, 'AutoplaybookV2', {
      limit: limit,
      offset: offset,
    });
    return {
      data: {
        auto_playbook_listing: response?.data?.data?.auto_playbook_v2,
        total_count: response?.data?.data?.total_count,
      },
    };
  },
  async listSlackChannels(platforms: string) {
    try {
      const response = await queryGraphQL(LIST_SLACK_CHANNELS, 'listSlackChannels', {
        platforms: platforms,
      });
      const data = response?.data?.data?.get_notification_channel_list || [];
      return data;
    } catch (error) {
      console.log('failed to get slack channels', error);
      return error;
    }
  },
  async listMSTeamsChannels(platforms: string) {
    try {
      const response = await queryGraphQL(LIST_MS_TEAMS_CHANNELS, 'listMSTeamsChannels', {
        platforms: platforms,
      });
      const data = response?.data?.data?.get_notification_channel_list || [];
      return data;
    } catch (error) {
      console.log('failed to get Ms Teams channels', error);
      return error;
    }
  },
};

export default apiAutoPlaybook;
