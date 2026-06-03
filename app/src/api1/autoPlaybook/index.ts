/**
 * TODO: Remove this entire module when the remaining components1/runbooks/ files
 * (BlockWithHeading.tsx, RunbookTargetResource.tsx, styles.ts) are migrated and deleted.
 * Runbook functionality has been replaced by Workflows.
 * This module is kept for backward compatibility with existing executions.
 */
import { queryGraphQL } from '@lib/HttpService';

export const LIST_SLACK_CHANNELS = `
query listSlackChannels($platforms: String!){
  get_notification_channel_list: notifications_list_channels(platform: $platforms) {
    data
  }
}
`;
export const LIST_MS_TEAMS_CHANNELS = `
query listMSTeamsChannels($platforms: String!){
  get_notification_channel_list: notifications_list_channels(platform: $platforms) {
    data
  }
}
`;

export const SINGLE_CONFIG_UPDATE_AUTO_PILOT = `
mutation singleConfigUpdateAuotPilot($data: autooptimize_update!) {
  autoOptimize_update_one: autooptimize_update(arg1: $data) {
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
