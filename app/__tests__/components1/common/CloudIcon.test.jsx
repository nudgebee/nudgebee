import React from 'react';
import { render, screen } from '@testing-library/react';
import CloudIcon from 'src/components1/common/CloudIcon';

jest.mock('src/utils/colors', () => ({
  colors: {
    text: { secondary: '#374151', primary: '#3B82F6', white: '#fff', tertiary: '#6B7280' },
    background: { primaryLightest: '#EFF6FF', white: '#fff', transparent: 'transparent' },
    border: { secondary: '#D1D5DB', primary: '#3B82F6' },
    primary: '#3B82F6',
  },
}));

jest.mock('next/router', () => ({ useRouter: jest.fn(() => ({ push: jest.fn(), pathname: '/', asPath: '/' })) }));

jest.mock('next/link', () => ({
  __esModule: true,
  default: ({ href, children, ...rest }) => (
    <a href={href} {...rest}>
      {children}
    </a>
  ),
}));

jest.mock('@assets', () => ({
  cloudBlackIcon: { src: 'cloud-black.svg' },
  newAwsLogo: { src: 'aws.svg' },
  ouAws: { src: 'aws.svg' },
  ouAzure: { src: 'azure.svg' },
  AWSIcon: { src: 'aws-icon.svg' },
  AzureIcon: { src: 'azure-icon.svg' },
  GCPIcon: { src: 'gcp-icon.svg' },
  ouGoogle: { src: 'gcp.svg' },
  ouK8s: { src: 'k8s.svg' },
  ouSnowFlake: { src: 'snowflake.svg' },
  ouOpenAi: { src: 'openai.svg' },
  ouRelic: { src: 'newrelic.svg' },
  jiraIcon: (props) => <svg data-testid='jira-svg' {...props} />,
  slackIcon: (props) => <svg data-testid='slack-svg' {...props} />,
  ouPostgres: { src: 'postgres.svg' },
  ouMsTeams: { src: 'msteams.svg' },
  GithubIcon: { src: 'github.svg' },
  GChatIcon: { src: 'gchat.svg' },
  ServiceNowIcon: { src: 'servicenow.svg' },
  PagerDutyIcon: { src: 'pagerduty.svg' },
  ZenDutyIcon: { src: 'zenduty.svg' },
  RabbitmqIcon: { src: 'rabbitmq.svg' },
  MySqlIcon: { src: 'mysql.svg' },
  RedisLogoIcon: { src: 'redis.svg' },
  PrometheusIcon: { src: 'prometheus.svg' },
  ClickhouseIcon: { src: 'clickhouse.svg' },
  DatadogIcon: { src: 'datadog.svg' },
  ArgocdIcon: { src: 'argocd.svg' },
  LlmIcon: { src: 'llm.svg' },
  LoggleIcon: { src: 'loggly.svg' },
  LokiIcon: { src: 'loki.svg' },
  SignozIcon: { src: 'signoz.svg' },
  ObserveIcon: { src: 'observe.svg' },
  AzureAppInsightIcon: { src: 'azureinsights.svg' },
  OpentelemetryIcon: { src: 'otel.svg' },
  ChronosphereIcon: { src: 'chronosphere.svg' },
  VictoriaMetricsIcon: { src: 'victoria.svg' },
  AzureMonitorWebhookIcon: { src: 'azuremonitor.svg' },
  TerminalIcon: { src: 'terminal.svg' },
  JaegerIcon: { src: 'jaeger.svg' },
  SplunkIcon: { src: 'splunk.svg' },
  GitLabIcon: { src: 'gitlab.svg' },
  BitBucketIcon: { src: 'bitbucket.svg' },
  GrafanaTempoIcon: { src: 'grafanatempo.svg' },
  GrafanaColorIcon: { src: 'grafana.svg' },
  OpenSearchIcon: { src: 'opensearch.svg' },
  Last9Icon: { src: 'last9.svg' },
  CloudFoundryIcon: { src: 'cloudfoundry.svg' },
}));

jest.mock('@components1/common/SafeIcon', () => ({
  __esModule: true,
  default: ({ alt, src }) => {
    let srcStr = 'icon';
    if (typeof src === 'string') {
      srcStr = src;
    } else if (src && typeof src === 'object' && src.src) {
      srcStr = src.src;
    }
    return <img alt={alt} src={srcStr} data-testid='cloud-icon-img' />;
  },
}));

describe('CloudIcon', () => {
  it('renders <img> with AWS src for AWS provider', () => {
    render(<CloudIcon cloud_provider='AWS' />);
    const icon = screen.getByTestId('cloud-icon-img');
    expect(icon).toBeInTheDocument();
    expect(icon).toHaveAttribute('src', 'aws-icon.svg');
  });

  it('renders <img> with GCP src for GCP provider', () => {
    render(<CloudIcon cloud_provider='GCP' />);
    const icon = screen.getByTestId('cloud-icon-img');
    expect(icon).toBeInTheDocument();
    expect(icon).toHaveAttribute('src', 'gcp-icon.svg');
  });

  it('renders <img> with K8s src for K8S provider', () => {
    render(<CloudIcon cloud_provider='K8S' />);
    const icon = screen.getByTestId('cloud-icon-img');
    expect(icon).toBeInTheDocument();
    expect(icon).toHaveAttribute('src', 'k8s.svg');
  });

  it('renders <img> with null cloud_provider (defaults to newAwsLogo)', () => {
    render(<CloudIcon cloud_provider={null} />);
    const icon = screen.getByTestId('cloud-icon-img');
    expect(icon).toBeInTheDocument();
    expect(icon).toHaveAttribute('src', 'aws.svg');
  });

  it('renders <img> with fallback cloudBlackIcon for unknown provider', () => {
    render(<CloudIcon cloud_provider='TOTALLY_UNKNOWN_XYZ' />);
    const icon = screen.getByTestId('cloud-icon-img');
    expect(icon).toBeInTheDocument();
    expect(icon).toHaveAttribute('src', 'cloud-black.svg');
  });

  it('renders AZURE correctly', () => {
    render(<CloudIcon cloud_provider='AZURE' />);
    const icon = screen.getByTestId('cloud-icon-img');
    expect(icon).toBeInTheDocument();
    expect(icon).toHaveAttribute('src', 'azure-icon.svg');
  });
});
