import { Typography } from '@mui/material';
import { useRouter } from 'next/router';
import ServiceNowAccountModal from '@common/ServiceNowAccountModal';
import ZenDutyAccountModal from '@common/ZenDutyAccountModal';
import ListIntegrations from './ListIntegrations';
import MessagingIntegrationTile from './MessagingIntegrationTile';
import TicketingIntegrationTile from './TicketingIntegrationTile';
import JiraAccountModal from '@components1/common/JiraAccountModal';
import K8sIntegrationTile from './K8sIntegrationTile';
import GithubAccountModal from '@components1/common/GithubAccountModal';
import GitlabAccountModal from '@components1/common/GitlabAccountModal';
import PagerDutyAccountModal from '@components1/common/PagerDutyAccountModal';
import CloudAccountTile from './CloudAccountTile';
import AddAwsAccountModal from './AddAwsAccountModal';
import AddAwsOrgModal from './AddAwsOrgModal';
import AwsOrgDashboard from './AwsOrgDashboard';
import AddAzureAccountModal from './AddAzureAccountModal';
import AddGcpAccountModal from './AddGcpAccountModal';
import AddCloudFoundryAccountModal from './AddCloudFoundryAccountModal';

export default function AddAccountForm() {
  const { cloudProvider } = useRouter().query;

  return (
    <div>
      {(() => {
        switch (cloudProvider?.toLocaleLowerCase()) {
          case 'jira':
            return <TicketingIntegrationTile tool='jira' displayName='Jira' cloudProvider='JIRA' AccountModalComponent={JiraAccountModal} />;
          case 'slack':
            return (
              <MessagingIntegrationTile
                provider='slack'
                displayName='Slack'
                installUrl='/api/slack/install'
                headers={['Team Name', 'Installed At', { name: 'Channels', width: '40%' }, { name: '', width: '10%' }, { name: '', width: '1%' }]}
                hasTeamName={true}
              />
            );
          case 'google_chat':
            return (
              <MessagingIntegrationTile
                provider='google_chat'
                displayName='Google Chat'
                installUrl='/api/integrations/install/google'
                headers={['Installed At', 'Channels', { name: '', width: '20%' }, { name: '', width: '1%' }]}
                hasTeamName={false}
              />
            );
          case 'pagerduty_webhook':
            return <ListIntegrations integrationName={'pagerduty_webhook'} />;
          case 'zenduty_webhook':
            return <ListIntegrations integrationName={'zenduty_webhook'} />;
          case 'argocd':
            return <ListIntegrations integrationName={'argocd'} />;
          case 'prometheus_alertmanager_webhook':
            return <ListIntegrations integrationName={'prometheus_alertmanager_webhook'} />;
          case 'datadog_webhook':
            return <ListIntegrations integrationName={'datadog_webhook'} />;
          case 'clickhouse':
            return <ListIntegrations integrationName={'clickhouse'} />;
          case 'datadog':
            return <ListIntegrations integrationName={'datadog'} />;
          case 'dynatrace':
            return <ListIntegrations integrationName={'dynatrace'} />;
          case 'mysql':
            return <ListIntegrations integrationName={'mysql'} />;
          case 'rabbitmq':
            return <ListIntegrations integrationName={'rabbitmq'} />;
          case 'confluence':
            return <ListIntegrations integrationName={'confluence'} />;
          case 'k8s':
            return <K8sIntegrationTile />;
          case 'aws':
            return (
              <>
                <CloudAccountTile
                  cloudProvider='AWS'
                  title='Amazon Web Services'
                  AddAccountModalComponent={AddAwsAccountModal}
                  addAccountButtonText='Add AWS Account'
                  AddOrgModalComponent={AddAwsOrgModal}
                  addOrgButtonText='AWS Organization'
                />
                <AwsOrgDashboard />
              </>
            );
          case 'gcp':
            return (
              <CloudAccountTile
                cloudProvider='GCP'
                title='Google Cloud Platform'
                AddAccountModalComponent={AddGcpAccountModal}
                addAccountButtonText='Add GCP Account'
              />
            );
          case 'azure':
            return (
              <CloudAccountTile
                cloudProvider='Azure'
                title='Azure'
                AddAccountModalComponent={AddAzureAccountModal}
                addAccountButtonText='Add Azure Account'
              />
            );
          case 'cloudfoundry':
            return (
              <CloudAccountTile
                cloudProvider='CloudFoundry'
                title='Cloud Foundry'
                AddAccountModalComponent={AddCloudFoundryAccountModal}
                addAccountButtonText='Add Cloud Foundry Account'
              />
            );
          case 'msteams':
            return (
              <MessagingIntegrationTile
                provider='ms_teams'
                displayName='Ms Teams'
                installUrl='/api/integrations/install/ms-teams'
                headers={['User Name', 'Installed At', 'Team Name', 'Channels', '', '']}
                mappingMode='team-multi'
              />
            );
          case 'github':
            return <TicketingIntegrationTile tool='github' displayName='Github' cloudProvider='GITHUB' AccountModalComponent={GithubAccountModal} />;
          case 'gitlab':
            return <TicketingIntegrationTile tool='gitlab' displayName='GitLab' cloudProvider='GITLAB' AccountModalComponent={GitlabAccountModal} />;
          case 'servicenow':
            return (
              <TicketingIntegrationTile
                tool='servicenow'
                displayName='ServiceNow'
                cloudProvider='SERVICENOW'
                AccountModalComponent={ServiceNowAccountModal}
              />
            );
          case 'pagerduty':
            return (
              <TicketingIntegrationTile
                tool='pagerduty'
                displayName='PagerDuty'
                cloudProvider='PAGERDUTY'
                AccountModalComponent={PagerDutyAccountModal}
              />
            );
          case 'zenduty':
            return (
              <TicketingIntegrationTile tool='zenduty' displayName='ZenDuty' cloudProvider='ZENDUTY' AccountModalComponent={ZenDutyAccountModal} />
            );
          case 'redis':
            return <ListIntegrations integrationName={'redis'} />;
          case 'llm':
            return <ListIntegrations integrationName={'LLM'} />;
          case 'loggly':
            return <ListIntegrations integrationName={'loggly'} />;
          case 'loki':
            return <ListIntegrations integrationName={'loki'} />;
          case 'es':
            return <ListIntegrations integrationName={'ES'} />;
          case 'pinot':
            return <ListIntegrations integrationName={'pinot'} />;
          case 'hive':
            return <ListIntegrations integrationName={'hive'} />;
          case 'postgres':
            return <ListIntegrations integrationName={'postgres'} />;
          case 'azure_app_insights':
            return <ListIntegrations integrationName={'azure_app_insights'} />;
          case 'prometheus':
            return <ListIntegrations integrationName={'prometheus'} />;
          case 'chronosphere':
            return <ListIntegrations integrationName={'chronosphere'} />;
          case 'otel':
            return <ListIntegrations integrationName={'otel_clickhouse'} />;
          case 'signoz':
            return <ListIntegrations integrationName={'signoz'} />;
          case 'azure_monitor_webhook':
            return <ListIntegrations integrationName={'azure_monitor_webhook'} />;
          case 'ssh':
            return <ListIntegrations integrationName={'ssh'} />;
          case 'observe':
            return <ListIntegrations integrationName={'observe'} />;
          case 'last9':
            return <ListIntegrations integrationName={'last9'} />;
          case 'servicenow_webhook':
            return <ListIntegrations integrationName={'servicenow_webhook'} />;
          case 'jaeger':
            return <ListIntegrations integrationName={'jaeger'} />;
          case 'newrelic_webhook':
            return <ListIntegrations integrationName={'newrelic_webhook'} />;
          case 'grafana_webhook':
            return <ListIntegrations integrationName={'grafana_webhook'} />;
          case 'newrelic':
            return <ListIntegrations integrationName={'newrelic'} />;
          case 'splunk_observability_platform':
            return <ListIntegrations integrationName={'splunk_observability_platform'} />;
          case 'splunk_webhook':
            return <ListIntegrations integrationName={'splunk_webhook'} />;
          case 'solarwinds':
            return <ListIntegrations integrationName={'solarwinds'} />;
          case 'solarwinds_webhook':
            return <ListIntegrations integrationName={'solarwinds_webhook'} />;
          case 'dynatrace_webhook':
            return <ListIntegrations integrationName={'dynatrace_webhook'} />;
          case 'gcp_monitoring_webhook':
            return <ListIntegrations integrationName={'gcp_monitoring_webhook'} />;
          case 'workflow_webhook':
            return <ListIntegrations integrationName={'workflow_webhook'} />;
          case 'vm_agent':
            return <ListIntegrations integrationName={'vm_agent'} />;
          case 'mssql':
            return <ListIntegrations integrationName={'mssql'} />;
          case 'oracle':
            return <ListIntegrations integrationName={'oracle'} />;
          case 'mcp':
            return <ListIntegrations integrationName={'mcp'} />;
          default:
            return <Typography>No integration selected</Typography>;
        }
      })()}
    </div>
  );
}
