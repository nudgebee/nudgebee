import { gqlStringify, queryGraphQL } from '@lib/HttpService';

interface CreateAccount {
  account_name: string;
}
export const LIST_ACCOUNT_TYPE = `
query list_account_type {
    all_accounts: accounts_list {
      rows {
        account_type
        account_name
        cloud_provider
        id
      }
    }
  }`;

export const LIST_ACCOUNT = `
query list_account {
  all_accounts: accounts_list(where: __WHERE_ACCOUNTS__) {
    rows {
      status
      cloud_provider
      id
      account_name
    }
  }
  integrations: integrations_list(where: __WHERE_INTEGRATIONS__) {
    rows {
      type
      status
      name
    }
  }
}
`;

export const LIST_MESSAGING_PLATFORMS_ACTION = `
query ListMessagingPlatforms($object: messaging_platform_list_input) {
  messagingplatforms_list(object: $object) {
    data {
      id
      username
      team_name
      created_at
      team_id
      channels
      platform
    }
  }
}
`;

export const CREATE_ACCOUNT = `
mutation CreateAccount($object: cloud_accounts_insert_one_input!) {
    accounts_create(object: $object) {
      id
      access_key
      access_secret
    }
  }
`;

export const VALIDATE_CLOUD_CREDENTIALS = `
mutation ValidateCloudCredentials($object: ValidateCloudCredentialsInput!) {
  cloud_check_credentials(object: $object) {
    success
    provider
    missingPermissions
    permissionDetails {
      permission
      hasAccess
      errorDetail
    }
    errorMessage
  }
}
`;

export const GCP_LIST_PROJECTS = `
mutation GcpListProjects($object: GcpListProjectsInput!) {
  gcp_list_projects(object: $object) {
    projects {
      project_id
      name
      state
    }
  }
}
`;

export const GCP_BULK_ONBOARD = `
mutation GcpBulkOnboard($object: GcpBulkOnboardInput!) {
  accounts_create_gcp_projects_bulk(object: $object) {
    accounts {
      project_id
      account_id
      status
      error
    }
    parent_id
  }
}
`;

export const CHECK_GCP_MONITORING_PERMISSION = `
mutation CloudCheckMonitoringPermission($object: CheckGcpMonitoringPermissionInput!) {
  cloud_check_monitoring_permission(object: $object) {
    has_permission
    error_detail
  }
}
`;

export const SETUP_GCP_MONITORING_WEBHOOK = `
mutation CloudSetupMonitoringWebhook($object: SetupGcpMonitoringWebhookInput!) {
  cloud_setup_monitoring_webhook(object: $object) {
    channel_name
  }
}
`;

export const AWS_ORG_ONBOARD = `
mutation AwsOrgOnboard($object: AwsOrgOnboardInput!) {
  accounts_create_aws_org(object: $object) {
    verification_token
    stackset_template_url
    stackset_launch_url
    sns_topic_arn
    stackset_parameters
  }
}
`;

export const AWS_ORG_STATUS = `
mutation AwsOrgStatus {
  aws_org_status {
    org_name
    org_status
    member_accounts {
      account_id
      account_number
      account_name
      status
      created_at
    }
  }
}
`;

export const AWS_ORG_REFRESH_TOKEN = `
mutation AwsOrgRefreshToken {
  aws_org_refresh_token {
    verification_token
  }
}
`;

export const AWS_ONBOARD_STATUS = `
mutation AwsOnboardStatus($object: AwsOnboardStatusInput!) {
  accounts_check_aws_onboarding(object: $object) {
    status
    account_id
    account_name
    account_number
    is_reconnected
  }
}
`;

export const ADD_TICKET_CONFIGURATION_ACCOUNT = `
mutation AddJiraAccount($bodyData: ticket_integration_create_config_input!) {
  ticket_integration_create_config(object: $bodyData) {
    id
  }
}
`;

export const GET_NOTIFICATION_CHANNEL_LIST = `query GetChannelList($platform:String!) {
  notifications_list_channels(platform: $platform) {
    data
  }
}
`;

export const GET_NOTIFICATION_USER_LIST = `query GetUserList($platform:String!) {
  notification_get_user_list(platform: $platform) {
    data
  }
}
`;

export const SEND_TEST_NOTIFICATION = `mutation SendTestNotification($platform: String!, $channel_id: String!, $team_id: String) {
  notifications_check_connection(platform: $platform, channel_id: $channel_id, team_id: $team_id) {
    success
    platform
    error
  }
}`;

export const UPDATE_MESSAGING_PLATFORM = `
mutation UpdateMessagingPlatform($object: messaging_platform_update_input!) {
  messagingplatforms_update(object: $object) {
    affected_rows
  }
}
`;

export const DELETE_MESSAGING_PLATFORM = `
mutation DeleteMessagingPlatform($object: messaging_platform_delete_input!) {
  messagingplatforms_delete(object: $object) {
    id
  }
}
`;

export const INSERT_ACC_ATTRIBUTE = `
mutation InsertAccountAttribute($object: cloud_account_attrs_upsert_input!) {
  cloud_account_attrs_upsert(object: $object) {
    affected_rows
  }
}
`;

export const UPDATE_ACCOUNT = `
mutation UpdateCloudAccount($object: cloud_account_update_input!) {
  cloud_account_update(object: $object) {
    affected_rows
  }
}
`;

export const AZURE_LIST_SUBSCRIPTIONS = `
mutation AzureListSubscriptions($object: AzureListSubscriptionsInput!) {
  azure_list_subscriptions(object: $object) {
    subscriptions {
      subscription_id
      display_name
      state
    }
  }
}
`;

export const AZURE_BULK_ONBOARD = `
mutation AzureBulkOnboard($object: AzureBulkOnboardInput!) {
  accounts_create_azure_subscriptions_bulk(object: $object) {
    accounts {
      subscription_id
      account_id
      status
      error
    }
    parent_id
  }
}
`;

const apiAccount = {
  getNotificationChannelList: async function (platform: string) {
    try {
      const response = await queryGraphQL(GET_NOTIFICATION_CHANNEL_LIST, 'GetChannelList', {
        platform: platform,
      });
      return {
        data: response?.data?.data?.notifications_list_channels || [],
      };
    } catch (err) {
      console.log('Your Error is', err);
      return err;
    }
  },
  getNotificationUserList: async function (platform: string) {
    try {
      const response = await queryGraphQL(GET_NOTIFICATION_USER_LIST, 'GetUserList', {
        platform: platform,
      });
      return {
        data: response?.data?.data?.notification_get_user_list || [],
      };
    } catch (err) {
      console.error('Failed to get notification user list:', err);
      throw err;
    }
  },
  getMessagingPlatform: async function (platform_type: string) {
    try {
      const object: any = {};
      if (platform_type) {
        object.platform = platform_type;
      }
      const response = await queryGraphQL(LIST_MESSAGING_PLATFORMS_ACTION, 'ListMessagingPlatforms', { object });
      return {
        data: response?.data?.data?.messagingplatforms_list?.data || [],
      };
    } catch (err) {
      console.log(`Failed to fetch ${platform_type}- `, err);
      return err;
    }
  },
  updateMessagingPlatform: async function (id: string, updateObj: any) {
    try {
      const response = await queryGraphQL(UPDATE_MESSAGING_PLATFORM, 'UpdateMessagingPlatform', {
        object: { id, channels: updateObj },
      });
      return {
        data: response?.data?.data?.messagingplatforms_update,
      };
    } catch (err) {
      console.log(`Failed to update messaging platform for id ${id}- `, err);
      return err;
    }
  },
  listMessagingPlatform: async function () {
    try {
      const response = await queryGraphQL(LIST_MESSAGING_PLATFORMS_ACTION, 'ListMessagingPlatforms', { object: {} });
      return {
        data: response?.data?.data?.messagingplatforms_list?.data,
      };
    } catch (err) {
      console.log(`Failed to list messaging platform- `, err);
      return err;
    }
  },
  async getAccountTypes() {
    try {
      const response = await queryGraphQL(LIST_ACCOUNT_TYPE, 'list_account_type', {});
      const allAccounts = response?.data?.data?.all_accounts?.rows || [];
      // Derive distinct-on-cloud_provider from all_accounts (client-side dedup)
      const seen = new Set();
      const accountType = allAccounts.filter((item: any) => {
        if (seen.has(item.cloud_provider)) return false;
        seen.add(item.cloud_provider);
        return true;
      });
      return {
        data: { account_type: accountType, all_accounts: allAccounts },
      };
    } catch (err) {
      console.log('Your Error is', err);
      return err;
    }
  },
  async getAllAccount() {
    try {
      const cloudProviders = ['K8s', 'AWS', 'Azure', 'GCP', 'CloudFoundry'];
      const messagingPlatforms = ['slack', 'ms_teams', 'google_chat'];
      const integrationTypes = [
        'github',
        'gitlab',
        'jira',
        'servicenow',
        'pagerduty',
        'zenduty',
        'pagerduty_webhook',
        'zenduty_webhook',
        'prometheus_alertmanager_webhook',
        'datadog_webhook',
        'azure_monitor_webhook',
        'servicenow_webhook',
        'grafana_webhook',
        'postgresql',
        'rabbitmq',
        'mysql',
        'redis',
        'confluence',
        'clickhouse',
        'datadog',
        'argocd',
        'llm',
        'mcp',
        'loggly',
        'loki',
        'signoz',
        'azure_app_insights',
        'prometheus',
        'otel_clickhouse',
        'chronosphere',
        'ssh',
        'observe',
        'jaeger',
        'ES',
        'pinot',
        'hive',
        'newrelic',
        'newrelic_webhook',
        'last9',
        'vm_agent',
        'mssql',
        'oracle',
        'splunk_observability_platform',
        'splunk_webhook',
        'dynatrace',
        'dynatrace_webhook',
        'gcp_monitoring_webhook',
        'solarwinds',
        'solarwinds_webhook',
        'workflow_webhook',
      ];

      const accountsWhere = gqlStringify({ cloud_provider: { _in: cloudProviders } });
      const integrationsWhere = gqlStringify({ type: { _in: integrationTypes } });
      const queryStr = LIST_ACCOUNT.replace('__WHERE_ACCOUNTS__', accountsWhere).replace('__WHERE_INTEGRATIONS__', integrationsWhere);

      // Fetch accounts+integrations and messaging platforms in parallel
      const [accountsResponse, messagingResponse] = await Promise.all([
        queryGraphQL(queryStr, 'list_account', {}),
        queryGraphQL(LIST_MESSAGING_PLATFORMS_ACTION, 'ListMessagingPlatforms', {
          object: { platform: null },
        }),
      ]);

      const data = accountsResponse?.data?.data;
      const messagingData = messagingResponse?.data?.data?.messagingplatforms_list?.data || [];

      // Filter messaging platforms to only include the requested types
      const filteredMessaging = messagingData.filter((mp: any) => messagingPlatforms.includes(mp.platform));

      return {
        data: {
          all_accounts: data?.all_accounts?.rows || [],
          messaging_platforms: filteredMessaging,
          integrations: (data?.integrations?.rows || []).map((i: any) => ({
            ...i,
            status: i.status === 'enabled' ? 'enabled' : i.status,
          })),
        },
      };
    } catch (err) {
      console.log('Your Error is', err);
      return err;
    }
  },
  createAccount: async function (bodyData: CreateAccount) {
    try {
      const response = await queryGraphQL(CREATE_ACCOUNT, 'CreateAccount', { object: bodyData });
      const responseJson = response.data;
      const hasErrors = Array.isArray(responseJson?.errors) && responseJson.errors.length > 0;
      const inserted = responseJson?.data?.accounts_create;
      if (!hasErrors && inserted) {
        return {
          data: {
            data: responseJson.data,
            status: 'SUCCESS',
            message: 'Account added successfully.',
          },
        };
      }
      let message = 'Unable to create account';
      if (hasErrors) {
        message = responseJson.errors[0]?.extensions?.internal?.response?.body?.[0]?.message ?? responseJson.errors[0].message;
      }
      console.error('createAccount failed:', message, { errors: responseJson?.errors, data: responseJson?.data });
      return {
        data: {
          message: message,
          status: 'ERROR',
        },
      };
    } catch (err) {
      console.error('createAccount exception:', err);
      return err;
    }
  },
  addJiraAccount: async function (bodyData: any) {
    try {
      return await queryGraphQL(ADD_TICKET_CONFIGURATION_ACCOUNT, 'AddJiraAccount', {
        bodyData: bodyData,
      });
    } catch (err) {
      console.log('Failed to add jira account', err);
      return err;
    }
  },
  sendTestNotification: async function (platform: string, channel_id: string, team_id?: string) {
    const response = await queryGraphQL(SEND_TEST_NOTIFICATION, 'SendTestNotification', {
      platform,
      channel_id,
      team_id,
    });
    return response?.data?.data?.notifications_check_connection;
  },
  deleteMessagingPlatform: async function (id: string) {
    try {
      return await queryGraphQL(DELETE_MESSAGING_PLATFORM, 'DeleteMessagingPlatform', {
        object: { id },
      });
    } catch (err) {
      console.log('Failed to delete message-', err);
      return err;
    }
  },
  insertAccAttr: async function (data: any) {
    try {
      const response = await queryGraphQL(INSERT_ACC_ATTRIBUTE, 'InsertAccountAttribute', {
        object: { objects: data },
      });
      return response;
    } catch (err) {
      console.log('Failed to add acc attributes-', err);
      return err;
    }
  },
  updateAccount: async function (data: any, update: any) {
    try {
      const object: any = { id: data.id };
      if (update.status) {
        object.status = update.status;
      }
      if (update.account_name) {
        object.account_name = update.account_name;
      }
      if (update.data) {
        object.data = update.data;
      }
      const response = await queryGraphQL(UPDATE_ACCOUNT, 'UpdateCloudAccount', { object });
      return response;
    } catch (err) {
      console.log('failed to updated account-', err);
      return err;
    }
  },
  generateAgentToken: async function (accountId: string, agentType?: string) {
    if (accountId === 'demo') return null;
    try {
      const CREATE_AGENT_TOKEN = `
      mutation CreateAgentToken($accountId: String!, $agentType: String) {
        agents_create_token(object: {account_id: $accountId, agent_type: $agentType}) {
          access_secret
          account_id
          access_key
        }
      }
      `;
      const variables: Record<string, string> = { accountId };
      if (agentType) {
        variables.agentType = agentType;
      }
      const response = await queryGraphQL(CREATE_AGENT_TOKEN, 'CreateAgentToken', variables);
      return response;
    } catch (err) {
      console.log('failed to generate agent token- ', err);
      return err;
    }
  },
  getDefaultProvider: async function (data: any) {
    try {
      const GET_DEFAULT_PROVIDER = `
      query GetDefaultProvider {
        observability_get_default_provider(request: __WHERE__) {
          provider
          default_index
          available_providers {
            provider
            supported_operators
            supported_operator_descriptors {
              token
              chip_label
              line_label
              kinds
            }
          }
          capabilities {
            supports_cross_zone_communication
            supports_trace_grouping
            supports_log_groups
            supports_service_map
            supported_operator_descriptors {
              token
              chip_label
              line_label
              kinds
            }
          }
        }
      }
      `;
      const response = await queryGraphQL(GET_DEFAULT_PROVIDER.replace('__WHERE__', gqlStringify(data)), 'GetDefaultProvider', {});
      return response;
    } catch (error) {
      return error;
    }
  },
  listProviderCapabilities: async function (data: { account_id: string }) {
    if (data.account_id === 'demo') {
      return { data: { data: { observability_list_provider_capabilities: [] } } };
    }
    try {
      const LIST_PROVIDER_CAPABILITIES = `
      query ListProviderCapabilities {
        observability_list_provider_capabilities(request: __WHERE__) {
          provider
          provider_type
          capabilities {
            supports_log_groups
            supports_auto_query
            supports_raw_query
            supports_heatmap
            supports_trace_grouping
            supports_service_map
            supports_cross_zone_communication
            supported_operator_descriptors {
              token
              chip_label
              line_label
              kinds
            }
          }
        }
      }
      `;
      const response = await queryGraphQL(LIST_PROVIDER_CAPABILITIES.replace('__WHERE__', gqlStringify(data)), 'ListProviderCapabilities', {});
      return response;
    } catch (error) {
      return error;
    }
  },
  awsOrgOnboard: async function (bodyData: { account_name: string }) {
    try {
      const response = await queryGraphQL(AWS_ORG_ONBOARD, 'AwsOrgOnboard', { object: bodyData });
      return response?.data;
    } catch (err) {
      console.log('awsOrgOnboard error:', err);
      throw err;
    }
  },
  awsOrgStatus: async function () {
    try {
      const response = await queryGraphQL(AWS_ORG_STATUS, 'AwsOrgStatus', {});
      return response?.data;
    } catch (err) {
      console.log('awsOrgStatus error:', err);
      throw err;
    }
  },
  awsOrgRefreshToken: async function () {
    try {
      const response = await queryGraphQL(AWS_ORG_REFRESH_TOKEN, 'AwsOrgRefreshToken', {});
      return response?.data;
    } catch (err) {
      console.log('awsOrgRefreshToken error:', err);
      throw err;
    }
  },
  awsOnboardStatus: async function (externalId: string) {
    try {
      const response = await queryGraphQL(AWS_ONBOARD_STATUS, 'AwsOnboardStatus', {
        object: { external_id: externalId },
      });
      return response?.data;
    } catch (err) {
      console.log('awsOnboardStatus error:', err);
      throw err;
    }
  },
  listAzureSubscriptions: async function (tenantId: string, clientId: string, clientSecret: string) {
    try {
      const response = await queryGraphQL(AZURE_LIST_SUBSCRIPTIONS, 'AzureListSubscriptions', {
        object: { tenant_id: tenantId, client_id: clientId, client_secret: clientSecret },
      });
      return response?.data?.data?.azure_list_subscriptions?.subscriptions || [];
    } catch (err) {
      console.error('listAzureSubscriptions error:', err);
      throw err;
    }
  },
  azureBulkOnboard: async function (data: {
    account_name: string;
    tenant_id: string;
    client_id: string;
    client_secret: string;
    subscriptions: { subscription_id: string; display_name?: string }[];
  }) {
    try {
      const response = await queryGraphQL(AZURE_BULK_ONBOARD, 'AzureBulkOnboard', {
        object: data,
      });
      if (!response?.data) {
        return {
          data: null,
          error: 'Failed to onboard Azure account due to a network or server error.',
        };
      }
      return {
        data: response?.data?.data?.accounts_create_azure_subscriptions_bulk,
        errors: response?.data?.errors,
      };
    } catch (err) {
      console.error('azureBulkOnboard error:', err);
      throw err;
    }
  },
  validateCloudCredentials: async function (bodyData: Record<string, string>) {
    try {
      const response = await queryGraphQL(VALIDATE_CLOUD_CREDENTIALS, 'ValidateCloudCredentials', {
        object: bodyData,
      });
      return response?.data?.data?.cloud_check_credentials;
    } catch (err) {
      console.log('validateCloudCredentials error:', err);
      throw err;
    }
  },
  listGcpProjects: async function (credentialsJson: string) {
    try {
      const response = await queryGraphQL(GCP_LIST_PROJECTS, 'GcpListProjects', {
        object: { credentials_json: credentialsJson },
      });
      return response?.data?.data?.gcp_list_projects?.projects || [];
    } catch (err) {
      console.log('listGcpProjects error:', err);
      throw err;
    }
  },
  gcpBulkOnboard: async function (data: Record<string, unknown>) {
    try {
      const response = await queryGraphQL(GCP_BULK_ONBOARD, 'GcpBulkOnboard', {
        object: data,
      });
      if (response?.data === null || response?.data === undefined) {
        return {
          data: null,
          errors: [{ message: 'Failed to onboard GCP account due to a network or server error.' }],
        };
      }
      return {
        data: response?.data?.data?.accounts_create_gcp_projects_bulk,
        errors: response?.data?.errors,
      };
    } catch (err) {
      console.log('gcpBulkOnboard error:', err);
      throw err;
    }
  },
  checkGcpMonitoringPermission: async function (accountId: string) {
    if (accountId === 'demo') {
      return null;
    }
    try {
      const response = await queryGraphQL(CHECK_GCP_MONITORING_PERMISSION, 'CloudCheckMonitoringPermission', {
        object: { account_id: accountId },
      });
      if (response?.data?.errors?.length) {
        throw new Error(response.data.errors[0].message || 'Failed to check monitoring permissions');
      }
      return response?.data?.data?.cloud_check_monitoring_permission;
    } catch (err) {
      console.log('checkGcpMonitoringPermission error:', err);
      throw err;
    }
  },
  setupGcpMonitoringWebhook: async function (accountId: string, webhookUrl: string) {
    if (accountId === 'demo') {
      return null;
    }
    try {
      const response = await queryGraphQL(SETUP_GCP_MONITORING_WEBHOOK, 'CloudSetupMonitoringWebhook', {
        object: { account_id: accountId, webhook_url: webhookUrl },
      });
      if (response?.data?.errors?.length) {
        throw new Error(response.data.errors[0].message || 'Failed to setup GCP monitoring webhook');
      }
      return response?.data?.data?.cloud_setup_monitoring_webhook;
    } catch (err) {
      console.log('setupGcpMonitoringWebhook error:', err);
      throw err;
    }
  },
};
export default apiAccount;
