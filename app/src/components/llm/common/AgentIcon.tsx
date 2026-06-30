// agentIcons.ts
import { getNubiIconUrl } from '@hooks/useTenantBranding';
import {
  ElasticSearchIcon,
  gcloudLogo,
  K8sIcon,
  LlmIcon,
  LogAnalysisBlueIcon,
  MySqlIcon,
  newAwsLogo,
  ouAzure,
  OptimizeBlueIcon,
  PostgresIcon,
  PrometheusIcon,
  RabbitmqIcon,
  RedisLogoIcon,
  SearchBlueIcon,
  TracesBlueIcon,
  SecurityBlueIcon,
  TicketBlueIcon,
  LokiIcon,
  GithubIcon,
  AutopilotIconBlue,
  ClickhouseIcon,
  ServiceMapIcon,
  DatadogIcon,
  ArgocdIcon,
  SignozIcon,
  DocumentationIconBlue,
  WrenchIcon,
  EventIconBlue,
  validationError,
  UserIcon,
  FollowUpBlueIcon,
  QueryLogIcon,
  HelmUpgradeIcon,
  MarticsPromQLIcon,
  TerminalIcon,
  DocumentationIcon,
  GraphOutlineIcon,
  WorkflowIconBlue,
} from '@assets';

export const getIcon = (toolName: string) => {
  if (!toolName) return undefined;
  toolName = toolName.toLowerCase();
  if (toolName === 'react_critique' || toolName.includes('critique')) {
    return LlmIcon;
  } else if (toolName.includes('compression')) {
    return LlmIcon;
  } else if (toolName.includes('vizualiz') || toolName.includes('visualiz')) {
    return GraphOutlineIcon;
  } else if (toolName.includes('think')) {
    return LlmIcon;
  } else if (toolName.includes('prometheus') || toolName.includes('promql')) {
    return PrometheusIcon;
  } else if (toolName == 'getmetrics' || toolName.includes('metrics')) {
    return MarticsPromQLIcon;
  } else if (toolName == 'getresourcerecommendations') {
    return WrenchIcon;
  } else if (toolName.includes('shell')) {
    return TerminalIcon;
  } else if (toolName.includes('notebook')) {
    return DocumentationIconBlue;
  } else if (toolName.includes('recommendation')) {
    return OptimizeBlueIcon;
  } else if (toolName.includes('loki') || toolName.includes('logql')) {
    return LokiIcon;
  } else if (toolName.includes('elastic') || toolName == 'queryES') {
    return ElasticSearchIcon;
  } else if (toolName == 'KubectlExecutor' || toolName == 'k8s' || toolName.includes('kubectl')) {
    return K8sIcon;
  } else if (toolName.includes('event')) {
    return EventIconBlue;
  } else if (toolName.includes('postgres')) {
    return PostgresIcon;
  } else if (toolName.includes('mysql')) {
    return MySqlIcon;
  } else if (toolName.includes('trace')) {
    return TracesBlueIcon;
  } else if (toolName == 'planner' || toolName == 'docs' || toolName == 'search_docs' || toolName == 'docs_agent' || toolName.includes('code')) {
    return DocumentationIconBlue;
  } else if (toolName == 'response') {
    return getNubiIconUrl();
  } else if (toolName == 'acknowledgment') {
    return getNubiIconUrl();
  } else if (toolName == 'error') {
    return validationError;
  } else if (toolName == 'question') {
    return UserIcon;
  } else if (toolName == 'followup-question' || toolName.includes('ask_clarif') || toolName.includes('clarification')) {
    return FollowUpBlueIcon;
  } else if (toolName.includes('delegate')) {
    return LlmIcon;
  } else if (toolName.includes('automat')) {
    return WorkflowIconBlue;
  } else if (toolName.includes('aws')) {
    return newAwsLogo;
  } else if (toolName.includes('azure')) {
    return ouAzure;
  } else if (toolName.includes('gcloud') || toolName.includes('gcp')) {
    return gcloudLogo;
  } else if (toolName.includes('rabbit')) {
    return RabbitmqIcon;
  } else if (toolName == 'llm' || toolName == 'debug' || toolName.includes('load_skills')) {
    return LlmIcon;
  } else if (toolName.includes('redis')) {
    return RedisLogoIcon;
  } else if (toolName.includes('search') || toolName.includes('crawl')) {
    return SearchBlueIcon;
  } else if (toolName == 'security' || toolName == 'security_execute' || toolName == 'trigger_image_scan') {
    return SecurityBlueIcon;
  } else if (toolName.includes('loganalysis')) {
    return LogAnalysisBlueIcon;
  } else if (toolName == 'help') {
    return DocumentationIcon;
  } else if (toolName.includes('github')) {
    return GithubIcon;
  } else if (toolName.includes('ticket')) {
    return TicketBlueIcon;
  } else if (toolName.includes('runbook')) {
    return AutopilotIconBlue;
  } else if (toolName.includes('clickhouse')) {
    return ClickhouseIcon;
  } else if (toolName.includes('service_dependency_graph')) {
    return ServiceMapIcon;
  } else if (toolName.includes('datadog')) {
    return DatadogIcon;
  } else if (toolName.includes('argocd')) {
    return ArgocdIcon;
  } else if (toolName.includes('signoz')) {
    return SignozIcon;
  } else if (toolName.includes('log') || toolName.includes('query')) {
    return QueryLogIcon;
  } else if (toolName.includes('helm')) {
    return HelmUpgradeIcon;
  }
};
