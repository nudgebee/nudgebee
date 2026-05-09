import React from 'react';
import { render } from '@testing-library/react';
import LangTypeIcon from '@components1/common/LangTypeIcon';

jest.mock('react-icons/fa6', () => ({
  FaGolang: (_props) => <svg data-testid='fa-golang' />,
  FaAws: (_props) => <svg data-testid='fa-aws' />,
}));
jest.mock('react-icons/fa', () => ({
  FaDatabase: () => <svg data-testid='fa-database' />,
  FaServer: () => <svg data-testid='fa-server' />,
  FaGlobeAsia: () => <svg data-testid='fa-globe' />,
  FaGithub: () => <svg data-testid='fa-github' />,
}));
jest.mock('react-icons/bi', () => ({
  BiLogoPython: () => <svg data-testid='bi-python' />,
  BiLogoJava: () => <svg data-testid='bi-java' />,
  BiLogoPhp: () => <svg data-testid='bi-php' />,
}));
jest.mock('react-icons/tb', () => ({
  TbLoadBalancer: () => <svg data-testid='tb-loadbalancer' />,
}));
jest.mock('react-icons/si', () => ({
  SiNodedotjs: () => <svg data-testid='si-node' />,
  SiRuby: () => <svg data-testid='si-ruby' />,
  SiDotnet: () => <svg data-testid='si-dotnet' />,
  SiMysql: () => <svg data-testid='si-mysql' />,
  SiMongodb: () => <svg data-testid='si-mongo' />,
  SiRedis: () => <svg data-testid='si-redis' />,
  SiClickhouse: () => <svg data-testid='si-clickhouse' />,
  SiElasticsearch: () => <svg data-testid='si-elastic' />,
  SiApachecassandra: () => <svg data-testid='si-cassandra' />,
  SiOpensearch: () => <svg data-testid='si-opensearch' />,
  SiRabbitmq: () => <svg data-testid='si-rabbitmq' />,
  SiApachekafka: () => <svg data-testid='si-kafka' />,
  SiApachepulsar: () => <svg data-testid='si-pulsar' />,
  SiNginx: () => <svg data-testid='si-nginx' />,
  SiAwselasticloadbalancing: () => <svg data-testid='si-alb' />,
  SiAmazons3: () => <svg data-testid='si-s3' />,
  SiAmazondynamodb: () => <svg data-testid='si-dynamo' />,
  SiAmazonsqs: () => <svg data-testid='si-sqs' />,
  SiAmazoncloudwatch: () => <svg data-testid='si-cloudwatch' />,
  SiAwslambda: () => <svg data-testid='si-lambda' />,
  SiAmazonec2: () => <svg data-testid='si-ec2' />,
  SiAmazoneks: () => <svg data-testid='si-eks' />,
  SiAwssecretsmanager: () => <svg data-testid='si-secrets' />,
  SiHelm: () => <svg data-testid='si-helm' />,
  SiKubernetes: () => <svg data-testid='si-k8s' />,
}));
jest.mock('react-icons/di', () => ({
  DiPostgresql: () => <svg data-testid='di-postgres' />,
}));
jest.mock('@assets', () => ({
  AWSCloudFormationIcon: '/cf.svg',
  AWSCloudTrailIcon: '/ct.svg',
  AWSEBSIcon: '/ebs.svg',
  AWSECRIcon: '/ecr.svg',
  AWSKMSIcon: '/kms.svg',
  AWSNatGatewayIcon: '/nat.svg',
  AWSSESIcon: '/ses.svg',
  AWSSNSIcon: '/sns.svg',
  AWSSecurityGroupIcon: '/sg.svg',
  AWSVPCIcon: '/vpc.svg',
  K8sServiceIcon: '/k8s-svc.svg',
  K8sPVCIcon: '/pvc.svg',
  K8sPVIcon: '/pv.svg',
  K8sDeploymentIcon: '/deploy.svg',
  K8sDaemonSetIcon: '/ds.svg',
  K8sJobIcon: '/job.svg',
  K8sCronJobIcon: '/cronjob.svg',
  K8sStatefulSetIcon: '/sts.svg',
  K8sNodeIcon: '/node.svg',
  NamespaceIcon: ({ height, width }) => <svg data-testid='namespace-icon' style={{ height, width }} />,
  GCPbigQueryIcon: '/bq.svg',
  GCPComputeEngineIcon: ({ height, width }) => <svg data-testid='gcp-ce' style={{ height, width }} />,
  GCPCloudSQLIcon: ({ height: _height, width: _width }) => <svg data-testid='gcp-sql' />,
  GCPCloudStorageIcon: ({ height: _height, width: _width }) => <svg data-testid='gcp-storage' />,
  GCPGKEIcon: '/gke.svg',
  GCPVertexAIIcon: '/vertex.svg',
  GCPCloudRunIcon: '/run.svg',
  GCPCloudSpannerIcon: '/spanner.svg',
  GCPAlloyDBIcon: '/alloy.svg',
  GCPAnthosIcon: '/anthos.svg',
  GCPApigeeIcon: '/apigee.svg',
  GCPDistributedCloudIcon: '/dc.svg',
  GCPHyperdiskIcon: '/hd.svg',
  GCPLookerIcon: '/looker.svg',
  GCPMandiantIcon: '/mandiant.svg',
  GCPSecurityCommandCenterIcon: '/scc.svg',
  GCPSecOpsIcon: '/secops.svg',
  GCPThreatIntelligenceIcon: '/ti.svg',
  GCPAIHypercomputerIcon: '/ai.svg',
}));
jest.mock('@components1/common/SafeIcon', () => ({
  __esModule: true,
  default: ({ alt, ...props }) => <img alt={alt} data-testid='safe-icon' {...props} />,
}));

describe('LangTypeIcon', () => {
  test('renders go language icon', () => {
    const { container } = render(<LangTypeIcon appLang='go' />);
    expect(container.querySelector('[data-testid="fa-golang"]')).toBeInTheDocument();
  });

  test('renders python icon', () => {
    const { container } = render(<LangTypeIcon appLang='python' />);
    expect(container.querySelector('[data-testid="bi-python"]')).toBeInTheDocument();
  });

  test('renders java icon', () => {
    const { container } = render(<LangTypeIcon appLang='java' />);
    expect(container.querySelector('[data-testid="bi-java"]')).toBeInTheDocument();
  });

  test('renders nodejs icon', () => {
    const { container } = render(<LangTypeIcon appLang='nodejs' />);
    expect(container.querySelector('[data-testid="si-node"]')).toBeInTheDocument();
  });

  test('renders postgres icon', () => {
    const { container } = render(<LangTypeIcon appLang='postgres' />);
    expect(container.querySelector('[data-testid="di-postgres"]')).toBeInTheDocument();
  });

  test('renders kafka icon', () => {
    const { container } = render(<LangTypeIcon appLang='kafka' />);
    expect(container.querySelector('[data-testid="si-kafka"]')).toBeInTheDocument();
  });

  test('renders nginx icon', () => {
    const { container } = render(<LangTypeIcon appLang='nginx' />);
    expect(container.querySelector('[data-testid="si-nginx"]')).toBeInTheDocument();
  });

  test('renders aws s3 icon', () => {
    const { container } = render(<LangTypeIcon appLang='s3' />);
    expect(container.querySelector('[data-testid="si-s3"]')).toBeInTheDocument();
  });

  test('renders ec2 icon', () => {
    const { container } = render(<LangTypeIcon appLang='ec2' />);
    expect(container.querySelector('[data-testid="si-ec2"]')).toBeInTheDocument();
  });

  test('renders kubernetes icon', () => {
    const { container } = render(<LangTypeIcon appLang='cluster' />);
    expect(container.querySelector('[data-testid="si-k8s"]')).toBeInTheDocument();
  });

  test('renders null for unknown language', () => {
    const { container } = render(<LangTypeIcon appLang='unknownlang123' />);
    expect(container.firstChild).toBeNull();
  });

  test('renders null when appLang is undefined', () => {
    const { container } = render(<LangTypeIcon appLang={undefined} />);
    expect(container.firstChild).toBeNull();
  });

  test('renders multiple icons when appLang is an array', () => {
    const { container } = render(<LangTypeIcon appLang={['go', 'python']} />);
    expect(container.querySelector('[data-testid="fa-golang"]')).toBeInTheDocument();
    expect(container.querySelector('[data-testid="bi-python"]')).toBeInTheDocument();
  });

  test('renders with custom size prop', () => {
    const { container } = render(<LangTypeIcon appLang='go' size={40} />);
    expect(container.querySelector('[data-testid="fa-golang"]')).toBeInTheDocument();
  });
});
