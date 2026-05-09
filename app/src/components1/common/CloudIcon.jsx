import {
  cloudBlackIcon,
  newAwsLogo,
  AWSIcon,
  AzureIcon,
  GCPIcon,
  ouK8s,
  ouSnowFlake,
  ouOpenAi,
  ouRelic,
  jiraIcon as JiraIcon,
  slackIcon as SlackIcon,
  ouPostgres,
  ouMssql as MssqlIcon,
  ouOracle as OracleIcon,
  ouMsTeams as MsTeamsIcon,
  GithubIcon,
  GChatIcon,
  ServiceNowIcon,
  PagerDutyIcon,
  ZenDutyIcon,
  RabbitmqIcon,
  MySqlIcon,
  RedisLogoIcon,
  PrometheusIcon,
  ClickhouseIcon,
  DatadogIcon,
  DynatraceIcon,
  ArgocdIcon,
  LlmIcon,
  McpIcon,
  LoggleIcon,
  LokiIcon,
  SignozIcon,
  ObserveIcon,
  AzureAppInsightIcon,
  OpentelemetryIcon,
  ChronosphereIcon,
  VictoriaMetricsIcon,
  AzureMonitorWebhookIcon,
  WorkflowWebhookIcon,
  TerminalIcon,
  JaegerIcon,
  SplunkIcon,
  SolarWindsIcon,
  GitLabIcon,
  BitBucketIcon,
  GrafanaTempoIcon,
  GrafanaColorIcon,
  Last9Icon,
  CloudFoundryIcon,
  ElasticSearchIcon,
} from '@assets';
import { Box } from '@mui/material';
import PropTypes from 'prop-types';
import SafeIcon from './SafeIcon';

const CloudProviderIcon = ({ cloud_provider, width, height, sx = {} }) => {
  let Icon = null;

  if (cloud_provider == null) {
    Icon = newAwsLogo;
  } else if (cloud_provider.toUpperCase() === 'AWS') {
    Icon = AWSIcon;
  } else if (cloud_provider.toUpperCase() === 'GCP' || cloud_provider.toUpperCase() === 'GCP_MONITORING_WEBHOOK') {
    Icon = GCPIcon;
  } else if (cloud_provider.toUpperCase() === 'AZURE') {
    Icon = AzureIcon;
  } else if (cloud_provider.toUpperCase() === 'K8S') {
    Icon = ouK8s;
  } else if (cloud_provider.toUpperCase() === 'CLOUDFOUNDRY') {
    Icon = CloudFoundryIcon;
  } else if (cloud_provider.toUpperCase() === 'SNOWFLAKE') {
    Icon = ouSnowFlake;
  } else if (cloud_provider.toUpperCase() === 'OPENAI') {
    Icon = ouOpenAi;
  } else if (cloud_provider.toUpperCase() === 'NEWRELIC' || cloud_provider.toUpperCase() === 'NEWRELIC_WEBHOOK') {
    Icon = ouRelic;
  } else if (cloud_provider.toUpperCase() === 'JIRA') {
    Icon = JiraIcon;
  } else if (cloud_provider.toUpperCase() === 'SLACK') {
    Icon = SlackIcon;
  } else if (cloud_provider.toUpperCase() === 'POSTGRES') {
    Icon = ouPostgres;
  } else if (cloud_provider.toUpperCase() === 'MSTEAMS' || cloud_provider.toUpperCase() === 'MS_TEAMS') {
    Icon = MsTeamsIcon;
  } else if (cloud_provider.toUpperCase() === 'GITHUB') {
    Icon = GithubIcon;
  } else if (cloud_provider.toUpperCase() === 'GOOGLE_CHAT') {
    Icon = GChatIcon;
  } else if (cloud_provider.toUpperCase() === 'SERVICENOW' || cloud_provider.toUpperCase() === 'SERVICENOW_WEBHOOK') {
    Icon = ServiceNowIcon;
  } else if (cloud_provider.toUpperCase() === 'PAGERDUTY' || cloud_provider.toUpperCase() === 'PAGERDUTY_WEBHOOK') {
    Icon = PagerDutyIcon;
  } else if (cloud_provider.toUpperCase() === 'ZENDUTY' || cloud_provider.toUpperCase() === 'ZENDUTY_WEBHOOK') {
    Icon = ZenDutyIcon;
  } else if (cloud_provider.toUpperCase() === 'DATADOG' || cloud_provider.toUpperCase() === 'DATADOG_WEBHOOK') {
    Icon = DatadogIcon;
  } else if (cloud_provider.toUpperCase() === 'DYNATRACE' || cloud_provider.toUpperCase() === 'DYNATRACE_WEBHOOK') {
    Icon = DynatraceIcon;
  } else if (cloud_provider.toUpperCase() === 'ARGOCD') {
    Icon = ArgocdIcon;
  } else if (cloud_provider.toUpperCase() === 'RABBITMQ') {
    Icon = RabbitmqIcon;
  } else if (cloud_provider.toUpperCase() === 'MYSQL') {
    Icon = MySqlIcon;
  } else if (cloud_provider.toUpperCase() === 'REDIS') {
    Icon = RedisLogoIcon;
  } else if (cloud_provider.toUpperCase() === 'CONFLUENCE') {
    Icon = JiraIcon;
  } else if (cloud_provider.toUpperCase() === 'PROMETHEUS_ALERTMANAGER_WEBHOOK') {
    Icon = PrometheusIcon;
  } else if (cloud_provider.toUpperCase() === 'GRAFANA_WEBHOOK') {
    Icon = GrafanaColorIcon;
  } else if (cloud_provider.toUpperCase() === 'CLICKHOUSE') {
    Icon = ClickhouseIcon;
  } else if (cloud_provider.toUpperCase() === 'LLM') {
    Icon = LlmIcon;
  } else if (cloud_provider.toUpperCase() === 'MCP') {
    Icon = McpIcon;
  } else if (cloud_provider.toUpperCase() === 'LOGGLY') {
    Icon = LoggleIcon;
  } else if (cloud_provider.toUpperCase() === 'LOKI') {
    Icon = LokiIcon;
  } else if (cloud_provider.toUpperCase() === 'SIGNOZ') {
    Icon = SignozIcon;
  } else if (cloud_provider.toUpperCase() === 'OBSERVE') {
    Icon = ObserveIcon;
  } else if (cloud_provider.toUpperCase() === 'AZURE_APP_INSIGHTS') {
    Icon = AzureAppInsightIcon;
  } else if (cloud_provider.toUpperCase() === 'PROMETHEUS') {
    Icon = PrometheusIcon;
  } else if (cloud_provider.toUpperCase() === 'CHRONOSPHERE') {
    Icon = ChronosphereIcon;
  } else if (cloud_provider.toUpperCase() === 'OTEL' || cloud_provider.toUpperCase() === 'OTEL_CLICKHOUSE') {
    Icon = OpentelemetryIcon;
  } else if (cloud_provider.toUpperCase() === 'AZURE_MONITOR_WEBHOOK') {
    Icon = AzureMonitorWebhookIcon;
  } else if (cloud_provider.toUpperCase() === 'WORKFLOW_WEBHOOK') {
    Icon = WorkflowWebhookIcon;
  } else if (cloud_provider.toUpperCase() === 'SSH') {
    Icon = TerminalIcon;
  } else if (cloud_provider.toUpperCase() === 'VM_AGENT') {
    Icon = TerminalIcon;
  } else if (cloud_provider.toUpperCase() === 'MSSQL') {
    Icon = MssqlIcon;
  } else if (cloud_provider.toUpperCase() === 'ORACLE') {
    Icon = OracleIcon;
  } else if (cloud_provider.toUpperCase() === 'VICTORIA-METRICS') {
    Icon = VictoriaMetricsIcon;
  } else if (cloud_provider.toUpperCase() === 'JAEGER') {
    Icon = JaegerIcon;
  } else if (cloud_provider.toUpperCase() === 'SPLUNK_OBSERVABILITY_PLATFORM' || cloud_provider.toUpperCase() === 'SPLUNK_WEBHOOK') {
    Icon = SplunkIcon;
  } else if (cloud_provider.toUpperCase() === 'SOLARWINDS' || cloud_provider.toUpperCase() === 'SOLARWINDS_WEBHOOK') {
    Icon = SolarWindsIcon;
  } else if (cloud_provider.toUpperCase() === 'BITBUCKET') {
    Icon = BitBucketIcon;
  } else if (cloud_provider.toUpperCase() === 'GITLAB') {
    Icon = GitLabIcon;
  } else if (cloud_provider.toUpperCase() === 'GRAFANA-TEMPO') {
    Icon = GrafanaTempoIcon;
  } else if (cloud_provider.toUpperCase() === 'ES') {
    Icon = ElasticSearchIcon;
  } else if (cloud_provider.toUpperCase() === 'LAST9') {
    Icon = Last9Icon;
  }

  if (!Icon) {
    Icon = cloudBlackIcon;
  }
  return (
    <Box
      sx={{
        height: height || '24px',
        width: width || '30px',
        objectFit: 'contain',
        ...sx,
        position: 'relative',
      }}
    >
      <SafeIcon src={Icon} alt='cloud provider' fill style={{ objectFit: 'contain' }} />
    </Box>
  );
};

CloudProviderIcon.propTypes = {
  cloud_provider: PropTypes.string,
  width: PropTypes.string,
  height: PropTypes.string,
  sx: PropTypes.object,
};

export default CloudProviderIcon;
