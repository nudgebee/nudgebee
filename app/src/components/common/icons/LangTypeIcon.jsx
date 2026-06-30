import { FaGolang, FaAws } from 'react-icons/fa6';
import { FaDatabase, FaServer, FaGlobeAsia, FaGithub } from 'react-icons/fa';
import { BiLogoPython, BiLogoJava, BiLogoPhp } from 'react-icons/bi';
import { TbLoadBalancer } from 'react-icons/tb';
import {
  SiNodedotjs,
  SiRuby,
  SiDotnet,
  SiMysql,
  SiMongodb,
  SiRedis,
  SiClickhouse,
  SiElasticsearch,
  SiApachecassandra,
  SiOpensearch,
  SiRabbitmq,
  SiApachekafka,
  SiApachepulsar,
  SiNginx,
  SiAwselasticloadbalancing,
  SiAmazons3,
  SiAmazondynamodb,
  SiAmazonsqs,
  SiAmazoncloudwatch,
  SiAwslambda,
  SiAmazonec2,
  SiAmazoneks,
  SiAwssecretsmanager,
  SiHelm,
  SiKubernetes,
} from 'react-icons/si';
import { DiPostgresql } from 'react-icons/di';
import {
  AWSCloudFormationIcon,
  AWSCloudTrailIcon,
  AWSEBSIcon,
  AWSECRIcon,
  AWSKMSIcon,
  AWSNatGatewayIcon,
  AWSSESIcon,
  AWSSNSIcon,
  AWSSecurityGroupIcon,
  AWSIAMIcon,
  AWSVPCIcon,
  K8sServiceIcon,
  K8sPVCIcon,
  K8sPVIcon,
  K8sDeploymentIcon,
  K8sDaemonSetIcon,
  K8sJobIcon,
  K8sCronJobIcon,
  K8sStatefulSetIcon,
  K8sNodeIcon,
  K8sPodIcon,
  K8sServiceAccountIcon,
  K8sIngressIcon,
  K8sSecretIcon,
  K8sConfigMapIcon,
  K8sCRDIcon,
  NamespaceIcon,
  GCPbigQueryIcon,
  GCPComputeEngineIcon,
  GCPCloudSQLIcon,
  GCPCloudStorageIcon,
  GCPGKEIcon,
  GCPVertexAIIcon,
  GCPCloudRunIcon,
  GCPCloudSpannerIcon,
  GCPAlloyDBIcon,
  GCPAnthosIcon,
  GCPApigeeIcon,
  GCPDistributedCloudIcon,
  GCPHyperdiskIcon,
  GCPLookerIcon,
  GCPMandiantIcon,
  GCPSecurityCommandCenterIcon,
  GCPSecOpsIcon,
  GCPThreatIntelligenceIcon,
  GCPAIHypercomputerIcon,
  GCPCloudPubSubIcon,
  GCPCloudLoadBalancingIcon,
  GCPArtifactRegistryIcon,
  GCPIAMIcon,
  AzureVMIcon,
  AzureSqlIcon,
  AzureVMScaleSetsIcon,
  AzureDisksIcon,
  AzureDiskSnapshotIcon,
  AzureImageIcon,
  AzureComputeGalleriesIcon,
  AzureVirtualNetworksIcon,
  AzureSubnetIcon,
  AzureNSGIcon,
  AzureNetworkInterfacesIcon,
  AzurePublicIPIcon,
  AzurePrivateEndpointsIcon,
  AzureDNSZonesIcon,
  AzureLoadBalancersIcon,
  AzureApplicationGatewaysIcon,
  AzureStorageAccountsIcon,
  AzureFilesharesIcon,
  AzureAppServicesIcon,
  AzureAppServicePlansIcon,
  AzureStaticAppsIcon,
  AzureKeyVaultsIcon,
  AzureManagedIdentitiesIcon,
  AzureServiceBusIcon,
  AzureEventHubsIcon,
  AzureEventGridTopicsIcon,
  AzureLogicAppsIcon,
  AzureContainerRegistriesIcon,
  AzureKubernetesServicesIcon,
  AzureDatabricksIcon,
  AzureCognitiveServicesIcon,
  AzureDatabasePostgreSQLIcon,
  AzureDatabaseMySQLIcon,
  AzureCosmosDBIcon,
  AzureCacheRedisIcon,
  AzureRecoveryServicesVaultsIcon,
  AzureDataFactoriesIcon,
  AzureMachineLearningIcon,
  AzureAutomationAccountsIcon,
  AzureSynapseAnalyticsIcon,
  AzureCognitiveSearchIcon,
  AzureMonitorIcon,
  AzureArcIcon,
  AzureCommunicationServicesIcon,
  AzureMarketplaceIcon,
  AzureResourceIcon,
} from '@assets';
import SafeIcon from './SafeIcon';
import { memo } from 'react';

// Memoize the icon component to prevent unnecessary re-renders in large graphs (e.g. KnowledgeGraph, ServiceMap)
// where many nodes are rendered and updated frequently on hover/selection.
const LangTypeIcon = memo(({ appLang, size = 25 }) => {
  const iconProps = { size };

  const getIcon = (lang) => {
    if (!lang) {
      return null;
    }

    // Force input to lowercase for comparison
    switch (lang.toLowerCase()) {
      // Languages
      case 'go':
      case 'golang':
        return <FaGolang {...iconProps} color='#00ADD8' />;
      case 'python':
        return <BiLogoPython {...iconProps} color='#3776AB' />;
      case 'java':
        return <BiLogoJava {...iconProps} color='#E51F24' />;
      case 'nodejs':
        return <SiNodedotjs {...iconProps} color='#8CC84B' />;
      case 'ruby':
        return <SiRuby {...iconProps} color='#CC342D' />;
      case 'dotnet':
        return <SiDotnet {...iconProps} color='#512BD4' />;
      case 'php':
        return <BiLogoPhp {...iconProps} color='#777BB4' />;

      // Databases
      case 'postgres':
        return <DiPostgresql {...iconProps} color='#336791' />;
      case 'mysql':
        return <SiMysql {...iconProps} color='#00758F' />;
      case 'mongodb':
        return <SiMongodb {...iconProps} color='#47A248' />;
      case 'redis':
        return <SiRedis {...iconProps} color='#D82C20' />;
      case 'clickhouse':
        return <SiClickhouse {...iconProps} color='#F7C32E' />;
      case 'elasticsearch':
        return <SiElasticsearch {...iconProps} color='#005571' />;
      case 'cassandra':
        return <SiApachecassandra {...iconProps} color='#1287B1' />;
      case 'opensearch':
        return <SiOpensearch {...iconProps} color='#005EB8' />;
      case 'memcached':
        return <FaDatabase {...iconProps} color='#00A4EF' />;

      // Messaging
      case 'rabbitmq':
        return <SiRabbitmq {...iconProps} color='#FF6600' />;
      case 'kafka':
        return <SiApachekafka {...iconProps} color='#231F20' />;
      case 'pulsar':
        return <SiApachepulsar {...iconProps} color='#188FFF' />;
      case 'activemq':
      case 'nats':
      case 'rocketmq':
      case 'zookeeper':
        return <FaServer {...iconProps} color='#FF6B35' />;

      // Web Servers
      case 'nginx':
        return <SiNginx {...iconProps} color='#009639' />;

      // Load Balancer (generic)
      case 'loadbalancer':
        return <TbLoadBalancer {...iconProps} color='#6366F1' />;

      // AWS Services (ALL LOWERCASE CASES)
      case 'aws-alb':
      case 'aws-nlb':
      case 'aws-elb':
        return <SiAwselasticloadbalancing {...iconProps} color='#FF9900' />;
      case 'aws-rds':
      case 'amazonrds':
      case 'rds':
        return <FaAws {...iconProps} color='#527FFF' />;
      case 'aws-elasticache':
        return <FaAws {...iconProps} color='#C925D1' />;
      case 'amazoncloudfront':
      case 'cloudfront':
      case 'cdn':
        return <FaAws {...iconProps} color='#8C4FFF' />;
      case 'cloudwatch':
      case 'amazoncloudwatch':
        return <SiAmazoncloudwatch {...iconProps} color='#FF4F8B' />;
      case 'lambda':
      case 'awslambda':
      case 'serverlessfunction':
        return <SiAwslambda {...iconProps} color='#FF9900' />;
      case 'aws-s3':
      case 's3':
      case 'amazons3':
        return <SiAmazons3 {...iconProps} color='#569A31' />;
      case 'ec2':
      case 'amazonec2':
        return <SiAmazonec2 {...iconProps} color='#FF9900' />;
      case 'aws-dynamodb':
      case 'amazondynamodb':
      case 'dynamodb':
        return <SiAmazondynamodb {...iconProps} color='#4053D6' />;
      case 'ecr':
      case 'amazonecr':
        return <SafeIcon src={AWSECRIcon} height={25} width={25} alt='ECR' />;
      case 'aws-sqs':
      case 'sqs':
      case 'amazonsqs':
      case 'awsqueueservice':
        return <SiAmazonsqs {...iconProps} color='#FF4F8B' />;
      case 'amazoneks':
        return <SiAmazoneks {...iconProps} color='#FF9900' />;
      case 'natgateway':
        return <SafeIcon src={AWSNatGatewayIcon} height={25} width={25} alt='NAT Gateway' />;
      case 'vpc':
      case 'amazonvpc':
        return <SafeIcon src={AWSVPCIcon} height={25} width={25} alt='VPC' />;
      case 'routetable':
        return <SafeIcon src={AWSVPCIcon} height={25} width={25} alt='Route Table' />;
      case 'securitygroup':
        return <SafeIcon src={AWSSecurityGroupIcon} height={25} width={25} alt='Security Group' />;
      case 'awscloudtrail':
      case 'cloudtrail':
        return <SafeIcon src={AWSCloudTrailIcon} height={25} width={25} alt='CloudTrail' />;
      case 'awssecurityhub':
      case 'securityhub':
        return <FaAws {...iconProps} color='#FF9900' />;
      case 'amazonsns':
      case 'sns':
        return <SafeIcon src={AWSSNSIcon} height={25} width={25} alt='SNS' />;
      case 'awscloudformation':
      case 'cloudformation':
        return <SafeIcon src={AWSCloudFormationIcon} height={25} width={25} alt='CloudFormation' />;
      case 'awssecretsmanager':
      case 'secretsmanager':
        return <SiAwssecretsmanager {...iconProps} color='#DD344C' />;
      case 'awskms':
      case 'kms':
        return <SafeIcon src={AWSKMSIcon} height={25} width={25} alt='KMS' />;
      case 'awsiam':
      case 'serviceidentity':
      case 'iam':
        return <SafeIcon src={AWSIAMIcon} height={25} width={25} alt='IAM' />;
      case 'amazonses':
      case 'ses':
        return <SafeIcon src={AWSSESIcon} height={25} width={25} alt='SES' />;
      case 'amazonebs':
        return <SafeIcon src={AWSEBSIcon} height={25} width={25} alt='EBS' />;
      case 'node':
        return <SafeIcon src={K8sNodeIcon} height={25} width={25} alt='K8s Node' />;

      // GCP Services (service_name values from cloud_resourses table)
      case 'bigquery':
      case 'bigquery.googleapis.com':
        return <SafeIcon src={GCPbigQueryIcon} height={size} width={size} alt='BigQuery' />;
      case 'compute engine':
      case 'computeengine':
        return <GCPComputeEngineIcon height={size} width={size} />;
      case 'cloud sql':
      case 'cloudsql':
        return <GCPCloudSQLIcon height={size} width={size} />;
      case 'cloud storage':
      case 'cloudstorage':
        return <GCPCloudStorageIcon height={size} width={size} />;
      case 'kubernetes engine':
      case 'gke':
        return <SafeIcon src={GCPGKEIcon} height={size} width={size} alt='GKE' />;
      case 'vertex ai':
      case 'vertexai':
        return <SafeIcon src={GCPVertexAIIcon} height={size} width={size} alt='Vertex AI' />;
      case 'gemini api':
      case 'geminiapi':
        return <SafeIcon src={GCPVertexAIIcon} height={size} width={size} alt='Gemini API' />;
      case 'cloud run':
      case 'cloudrun':
        return <SafeIcon src={GCPCloudRunIcon} height={size} width={size} alt='Cloud Run' />;
      case 'cloud spanner':
      case 'cloudspanner':
        return <SafeIcon src={GCPCloudSpannerIcon} height={size} width={size} alt='Cloud Spanner' />;
      case 'cloud filestore':
      case 'cloudfilestore':
      case 'filestore':
        return <GCPCloudStorageIcon height={size} width={size} />;
      case 'cloud logging':
      case 'cloudlogging':
        return <SafeIcon src={GCPSecOpsIcon} height={size} width={size} alt='Cloud Logging' />;
      case 'cloud monitoring':
      case 'cloudmonitoring':
        return <SafeIcon src={GCPSecurityCommandCenterIcon} height={size} width={size} alt='Cloud Monitoring' />;
      case 'networking':
      case 'subnet':
        return <SafeIcon src={GCPDistributedCloudIcon} height={size} width={size} alt='Networking' />;
      case 'vm manager':
      case 'vmmanager':
        return <GCPComputeEngineIcon height={size} width={size} />;
      case 'alloydb':
        return <SafeIcon src={GCPAlloyDBIcon} height={size} width={size} alt='AlloyDB' />;
      case 'anthos':
        return <SafeIcon src={GCPAnthosIcon} height={size} width={size} alt='Anthos' />;
      case 'apigee':
        return <SafeIcon src={GCPApigeeIcon} height={size} width={size} alt='Apigee' />;
      case 'distributed cloud':
      case 'distributedcloud':
        return <SafeIcon src={GCPDistributedCloudIcon} height={size} width={size} alt='Distributed Cloud' />;
      case 'hyperdisk':
        return <SafeIcon src={GCPHyperdiskIcon} height={size} width={size} alt='Hyperdisk' />;
      case 'looker':
        return <SafeIcon src={GCPLookerIcon} height={size} width={size} alt='Looker' />;
      case 'mandiant':
        return <SafeIcon src={GCPMandiantIcon} height={size} width={size} alt='Mandiant' />;
      case 'security command center':
      case 'securitycommandcenter':
        return <SafeIcon src={GCPSecurityCommandCenterIcon} height={size} width={size} alt='Security Command Center' />;
      case 'security operations':
      case 'securityoperations':
      case 'secops':
        return <SafeIcon src={GCPSecOpsIcon} height={size} width={size} alt='Security Operations' />;
      case 'threat intelligence':
      case 'threatintelligence':
        return <SafeIcon src={GCPThreatIntelligenceIcon} height={size} width={size} alt='Threat Intelligence' />;
      case 'ai hypercomputer':
      case 'aihypercomputer':
        return <SafeIcon src={GCPAIHypercomputerIcon} height={size} width={size} alt='AI Hypercomputer' />;
      case 'claude sonnet 4.5':
        return <SafeIcon src={GCPVertexAIIcon} height={size} width={size} alt='Claude Sonnet 4.5' />;
      case 'cloud pub/sub':
      case 'cloud pubsub':
      case 'cloudpubsub':
      case 'pubsub':
        return <SafeIcon src={GCPCloudPubSubIcon} height={size} width={size} alt='Cloud Pub/Sub' />;
      case 'cloud load balancing':
      case 'cloudloadbalancing':
      case 'loadbalancing':
        return <SafeIcon src={GCPCloudLoadBalancingIcon} height={size} width={size} alt='Cloud Load Balancing' />;
      case 'artifact registry':
      case 'artifactregistry':
      case 'artifact-registry':
        return <SafeIcon src={GCPArtifactRegistryIcon} height={size} width={size} alt='Artifact Registry' />;
      case 'gcpiam':
      case 'gcp iam':
        return <SafeIcon src={GCPIAMIcon} height={size} width={size} alt='GCP IAM' />;
      case 'cloud dns':
      case 'clouddns':
        return <SafeIcon src={GCPDistributedCloudIcon} height={size} width={size} alt='Cloud DNS' />;
      case 'cloud cdn':
      case 'cloudcdn':
        return <SafeIcon src={GCPCloudLoadBalancingIcon} height={size} width={size} alt='Cloud CDN' />;
      case 'compute.googleapis.com/disk':
      case 'persistent disk':
      case 'persistentdisk':
        return <SafeIcon src={GCPHyperdiskIcon} height={size} width={size} alt='Persistent Disk' />;

      // Azure Services (logo_id values computed by the backend from service_name; see
      // azureServiceLogoMap in knowledge_graph/core/helpers.go)
      case 'azure-vm':
        return <SafeIcon src={AzureVMIcon} height={size} width={size} alt='Azure VM' />;
      case 'azure-vmss':
        return <SafeIcon src={AzureVMScaleSetsIcon} height={size} width={size} alt='Azure VM Scale Set' />;
      case 'azure-disk':
        return <SafeIcon src={AzureDisksIcon} height={size} width={size} alt='Azure Disk' />;
      case 'azure-snapshot':
        return <SafeIcon src={AzureDiskSnapshotIcon} height={size} width={size} alt='Azure Disk Snapshot' />;
      case 'azure-image':
        return <SafeIcon src={AzureImageIcon} height={size} width={size} alt='Azure Image' />;
      case 'azure-compute-gallery':
        return <SafeIcon src={AzureComputeGalleriesIcon} height={size} width={size} alt='Azure Compute Gallery' />;
      case 'azure-vnet':
        return <SafeIcon src={AzureVirtualNetworksIcon} height={size} width={size} alt='Azure Virtual Network' />;
      case 'azure-subnet':
        return <SafeIcon src={AzureSubnetIcon} height={size} width={size} alt='Azure Subnet' />;
      case 'azure-nsg':
        return <SafeIcon src={AzureNSGIcon} height={size} width={size} alt='Azure Network Security Group' />;
      case 'azure-nic':
        return <SafeIcon src={AzureNetworkInterfacesIcon} height={size} width={size} alt='Azure Network Interface' />;
      case 'azure-public-ip':
        return <SafeIcon src={AzurePublicIPIcon} height={size} width={size} alt='Azure Public IP' />;
      case 'azure-private-endpoint':
        return <SafeIcon src={AzurePrivateEndpointsIcon} height={size} width={size} alt='Azure Private Endpoint' />;
      case 'azure-dns':
        return <SafeIcon src={AzureDNSZonesIcon} height={size} width={size} alt='Azure DNS Zone' />;
      case 'azure-lb':
        return <SafeIcon src={AzureLoadBalancersIcon} height={size} width={size} alt='Azure Load Balancer' />;
      case 'azure-appgw':
        return <SafeIcon src={AzureApplicationGatewaysIcon} height={size} width={size} alt='Azure Application Gateway' />;
      case 'azure-storage':
        return <SafeIcon src={AzureStorageAccountsIcon} height={size} width={size} alt='Azure Storage Account' />;
      case 'azure-files':
        return <SafeIcon src={AzureFilesharesIcon} height={size} width={size} alt='Azure Files' />;
      case 'azure-app-service':
        return <SafeIcon src={AzureAppServicesIcon} height={size} width={size} alt='Azure App Service' />;
      case 'azure-app-service-plan':
        return <SafeIcon src={AzureAppServicePlansIcon} height={size} width={size} alt='Azure App Service Plan' />;
      case 'azure-static-app':
        return <SafeIcon src={AzureStaticAppsIcon} height={size} width={size} alt='Azure Static Web App' />;
      case 'azure-key-vault':
        return <SafeIcon src={AzureKeyVaultsIcon} height={size} width={size} alt='Azure Key Vault' />;
      case 'azure-managed-identity':
        return <SafeIcon src={AzureManagedIdentitiesIcon} height={size} width={size} alt='Azure Managed Identity' />;
      case 'azure-recovery-vault':
        return <SafeIcon src={AzureRecoveryServicesVaultsIcon} height={size} width={size} alt='Azure Recovery Services Vault' />;
      case 'azure-service-bus':
        return <SafeIcon src={AzureServiceBusIcon} height={size} width={size} alt='Azure Service Bus' />;
      case 'azure-event-hub':
        return <SafeIcon src={AzureEventHubsIcon} height={size} width={size} alt='Azure Event Hubs' />;
      case 'azure-event-grid':
        return <SafeIcon src={AzureEventGridTopicsIcon} height={size} width={size} alt='Azure Event Grid' />;
      case 'azure-logic-app':
        return <SafeIcon src={AzureLogicAppsIcon} height={size} width={size} alt='Azure Logic App' />;
      case 'azure-acr':
        return <SafeIcon src={AzureContainerRegistriesIcon} height={size} width={size} alt='Azure Container Registry' />;
      case 'azure-aks':
        return <SafeIcon src={AzureKubernetesServicesIcon} height={size} width={size} alt='Azure Kubernetes Service' />;
      case 'azure-databricks':
        return <SafeIcon src={AzureDatabricksIcon} height={size} width={size} alt='Azure Databricks' />;
      case 'azure-cognitive-services':
        return <SafeIcon src={AzureCognitiveServicesIcon} height={size} width={size} alt='Azure Cognitive Services' />;
      case 'azure-postgresql':
        return <SafeIcon src={AzureDatabasePostgreSQLIcon} height={size} width={size} alt='Azure Database for PostgreSQL' />;
      case 'azure-mysql':
        return <SafeIcon src={AzureDatabaseMySQLIcon} height={size} width={size} alt='Azure Database for MySQL' />;
      case 'azure-cosmos-db':
        return <SafeIcon src={AzureCosmosDBIcon} height={size} width={size} alt='Azure Cosmos DB' />;
      case 'azure-sql':
        return <SafeIcon src={AzureSqlIcon} height={size} width={size} alt='Azure SQL' />;
      case 'azure-cache-redis':
        return <SafeIcon src={AzureCacheRedisIcon} height={size} width={size} alt='Azure Cache for Redis' />;
      case 'azure-data-factory':
        return <SafeIcon src={AzureDataFactoriesIcon} height={size} width={size} alt='Azure Data Factory' />;
      case 'azure-machine-learning':
        return <SafeIcon src={AzureMachineLearningIcon} height={size} width={size} alt='Azure Machine Learning' />;
      case 'azure-automation':
        return <SafeIcon src={AzureAutomationAccountsIcon} height={size} width={size} alt='Azure Automation' />;
      case 'azure-synapse':
        return <SafeIcon src={AzureSynapseAnalyticsIcon} height={size} width={size} alt='Azure Synapse Analytics' />;
      case 'azure-search':
        return <SafeIcon src={AzureCognitiveSearchIcon} height={size} width={size} alt='Azure AI Search' />;
      case 'azure-monitor':
        return <SafeIcon src={AzureMonitorIcon} height={size} width={size} alt='Azure Monitor' />;
      case 'azure-arc':
        return <SafeIcon src={AzureArcIcon} height={size} width={size} alt='Azure Arc' />;
      case 'azure-communication':
        return <SafeIcon src={AzureCommunicationServicesIcon} height={size} width={size} alt='Azure Communication Services' />;
      case 'azure-marketplace':
        return <SafeIcon src={AzureMarketplaceIcon} height={size} width={size} alt='Azure Marketplace' />;
      case 'azure-resource':
        return <SafeIcon src={AzureResourceIcon} height={size} width={size} alt='Azure Resource' />;

      // External Services
      case 'externalservice':
      case 'http':
      case 'https':
      case 'grpc':
        return <FaGlobeAsia {...iconProps} color='#47A248' />;

      // Kubernetes
      case 'helmchart':
        return <SiHelm {...iconProps} color='#0F1689' />;
      case 'cluster':
        return <SiKubernetes {...iconProps} color='#326CE5' />;
      case 'namespace':
        return <NamespaceIcon height={size} width={size} />;
      case 'k8sservice':
        return <SafeIcon src={K8sServiceIcon} height={25} width={25} alt='K8s Service' />;
      case 'persistentvolumeclaim':
        return <SafeIcon src={K8sPVCIcon} height={25} width={25} alt='PVC' />;
      case 'persistentvolume':
        return <SafeIcon src={K8sPVIcon} height={25} width={25} alt='PV' />;
      case 'deployment':
        return <SafeIcon src={K8sDeploymentIcon} height={25} width={25} alt='Deployment' />;
      case 'daemonset':
        return <SafeIcon src={K8sDaemonSetIcon} height={25} width={25} alt='DaemonSet' />;
      case 'job':
        return <SafeIcon src={K8sJobIcon} height={25} width={25} alt='Job' />;
      case 'cronjob':
        return <SafeIcon src={K8sCronJobIcon} height={25} width={25} alt='CronJob' />;
      case 'statefulset':
        return <SafeIcon src={K8sStatefulSetIcon} height={25} width={25} alt='StatefulSet' />;
      case 'pod':
        return <SafeIcon src={K8sPodIcon} height={25} width={25} alt='Pod' />;
      case 'k8sserviceaccount':
      case 'serviceaccount':
        return <SafeIcon src={K8sServiceAccountIcon} height={25} width={25} alt='ServiceAccount' />;
      case 'ingress':
        return <SafeIcon src={K8sIngressIcon} height={25} width={25} alt='Ingress' />;
      case 'k8ssecret':
      case 'secret':
        return <SafeIcon src={K8sSecretIcon} height={25} width={25} alt='Secret' />;
      case 'configmap':
        return <SafeIcon src={K8sConfigMapIcon} height={25} width={25} alt='ConfigMap' />;
      case 'customresource':
      case 'crd':
        return <SafeIcon src={K8sCRDIcon} height={25} width={25} alt='CustomResource' />;

      // Source Control
      case 'repository':
        return <FaGithub {...iconProps} color='#181717' />;

      default:
        return null;
    }
  };

  if (Array.isArray(appLang)) {
    return (
      <>
        {appLang.map((lang, index) => (
          <span key={index}>{getIcon(lang)}</span>
        ))}
      </>
    );
  }

  return getIcon(appLang);
});

export default LangTypeIcon;
