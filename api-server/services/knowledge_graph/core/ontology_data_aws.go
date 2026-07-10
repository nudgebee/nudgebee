package core

// AWS ontology field mappings — how each concrete AWS specific_type populates the
// cross-cloud-normalized ontology_attributes for its NodeType.
//
// See ontology_data_gcp.go / ontology_data_azure.go for the same canonical field
// keys wired to other clouds; TestOntologyFieldsAreCanonical enforces that every
// OntologyField is in canonicalOntologyVocab for its NodeType.
//
// NodeField keys are the raw properties written at node CREATION time
// (createNodeFromResource + the extract*Metadata funcs in sources/aws/*.go),
// never fields added by later CLI/edge enrichment. Where a concept's fields are
// not populated until enrichment (Target Groups, Elastic IP address, Pod
// namespace/cluster, Subnet CIDR on real rows), the spec is intentionally minimal
// ({name, location}); the declared-but-absent fields simply drop out.

func init() { registerOntology(awsOntology) }

var awsOntology = map[string]OntologyNodeSpec{
	// ---- ComputeInstance (region-based vocab) ----
	"EC2Instance": {NodeType: NodeTypeComputeInstance, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "region", NodeField: "region"},
		{OntologyField: "type", NodeField: "instance_type"},
		{OntologyField: "state", NodeField: "instance_state"},
		{OntologyField: "public_ip_address", NodeField: "public_ip"},
		{OntologyField: "private_ip_address", NodeField: "private_ip"},
	}},
	"SSMManagedInstance": {NodeType: NodeTypeComputeInstance, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "region", NodeField: "region"},
		{OntologyField: "type", NodeField: "instance_type"},
		{OntologyField: "state", NodeField: "instance_state"},
		{OntologyField: "public_ip_address", NodeField: "public_ip"},
		{OntologyField: "private_ip_address", NodeField: "private_ip"},
	}},

	// ---- ComputeInstancePool (region-based vocab) ----
	"EKSNodeGroup": {NodeType: NodeTypeComputeInstancePool, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "region", NodeField: "region"},
		{OntologyField: "state", NodeField: "status"},
	}},

	// ---- ServiceIdentity (IAM Role / User; set directly in iam.go builders) ----
	"IAMRole": {NodeType: NodeTypeServiceIdentity, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"}, // "global"
		{OntologyField: "type", NodeField: "subtype"},    // "IAMRole"
		{OntologyField: "arn", NodeField: "arn"},
	}},
	"IAMUser": {NodeType: NodeTypeServiceIdentity, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"}, // "global"
		{OntologyField: "type", NodeField: "subtype"},    // "IAMUser"
		{OntologyField: "arn", NodeField: "arn"},
	}},

	// ---- Database ----
	"RDSInstance": {NodeType: NodeTypeDatabase, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "type", NodeField: "engine"},
		{OntologyField: "version", NodeField: "engine_version"},
		{OntologyField: "endpoint", NodeField: "endpoint_address"},
		{OntologyField: "port", NodeField: "endpoint_port"},
		{OntologyField: "encrypted", NodeField: "encrypted"},
	}},
	"RDSCluster": {NodeType: NodeTypeDatabase, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "type", NodeField: "engine"},
		{OntologyField: "version", NodeField: "engine_version"},
		{OntologyField: "endpoint", NodeField: "endpoint_address"},
		{OntologyField: "port", NodeField: "endpoint_port"},
		{OntologyField: "encrypted", NodeField: "encrypted"},
	}},
	"RedshiftCluster": {NodeType: NodeTypeDatabase, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "type", NodeField: "engine"},
		{OntologyField: "version", NodeField: "engine_version"},
		{OntologyField: "endpoint", NodeField: "endpoint_address"},
		{OntologyField: "port", NodeField: "endpoint_port"},
		{OntologyField: "encrypted", NodeField: "encrypted"},
	}},
	"DynamoDBTable": {NodeType: NodeTypeDatabase, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "encrypted", NodeField: "encrypted"},
	}},
	"OpenSearchDomain": {NodeType: NodeTypeDatabase, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "type", NodeField: "engine"},
		{OntologyField: "version", NodeField: "engine_version"},
		{OntologyField: "endpoint", NodeField: "endpoint_address"},
		{OntologyField: "encrypted", NodeField: "encrypted"},
	}},

	// ---- Cache ----
	"ElastiCacheCluster": {NodeType: NodeTypeCache, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "type", NodeField: "engine"},
		{OntologyField: "version", NodeField: "engine_version"},
		{OntologyField: "endpoint", NodeField: "configuration_endpoint", Special: "coalesce",
			Extra: map[string]any{"fields": []string{"primary_endpoint", "reader_endpoint"}}},
	}},

	// ---- Storage ----
	"S3Bucket": {NodeType: NodeTypeStorage, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "encrypted", NodeField: "encryption_enabled"},
		{OntologyField: "versioning", NodeField: "versioning_enabled"},
	}},
	"EBSVolume": {NodeType: NodeTypeStorage, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "type", NodeField: "volume_type"},
		{OntologyField: "encrypted", NodeField: "encrypted"},
		{OntologyField: "size", NodeField: "size_gb"},
	}},
	"EBSSnapshot": {NodeType: NodeTypeStorage, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "type", NodeField: "storage_type"},
		{OntologyField: "encrypted", NodeField: "encrypted"},
		{OntologyField: "size", NodeField: "size_gb"},
	}},
	"EFSFileSystem": {NodeType: NodeTypeStorage, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "encrypted", NodeField: "encrypted"},
		{OntologyField: "size", NodeField: "size_in_bytes"},
	}},

	// ---- MessageQueue ----
	"SQSQueue": {NodeType: NodeTypeMessageQueue, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "type", NodeField: "subtype"},
	}},
	"MSKCluster": {NodeType: NodeTypeMessageQueue, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "type", NodeField: "subtype"},
	}},

	// ---- Topic ----
	"SNSTopic": {NodeType: NodeTypeTopic, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "type", NodeField: "subtype"},
	}},

	// ---- Workload (ECS Service; namespace/cluster/replicas not set at creation) ----
	"ECSService": {NodeType: NodeTypeWorkload, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
	}},

	// ---- ServerlessFunction ----
	"LambdaFunction": {NodeType: NodeTypeServerlessFunction, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "runtime", NodeField: "runtime"},
		{OntologyField: "memory", NodeField: "memory_mb"},
	}},

	// ---- CDN (CloudFront) ----
	"CloudFrontDistribution": {NodeType: NodeTypeCDN, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "state", NodeField: "status"},
		{OntologyField: "domain_name", NodeField: "domain_name"},
	}},
	"CloudFrontFunction": {NodeType: NodeTypeCDN, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "state", NodeField: "status"},
		{OntologyField: "domain_name", NodeField: "domain_name"},
	}},

	// ---- LoadBalancer (ELB / ALB / NLB) ----
	"ELBLoadBalancer": {NodeType: NodeTypeLoadBalancer, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "type", NodeField: "load_balancer_type"},
		{OntologyField: "scheme", NodeField: "scheme"},
		{OntologyField: "dns_name", NodeField: "dns_name"},
		{OntologyField: "state", NodeField: "state"},
	}},
	"ALBLoadBalancer": {NodeType: NodeTypeLoadBalancer, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "type", NodeField: "load_balancer_type"},
		{OntologyField: "scheme", NodeField: "scheme"},
		{OntologyField: "dns_name", NodeField: "dns_name"},
		{OntologyField: "state", NodeField: "state"},
	}},
	"NLBLoadBalancer": {NodeType: NodeTypeLoadBalancer, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "type", NodeField: "load_balancer_type"},
		{OntologyField: "scheme", NodeField: "scheme"},
		{OntologyField: "dns_name", NodeField: "dns_name"},
		{OntologyField: "state", NodeField: "state"},
	}},

	// ---- BackendPool (Target Group; protocol/port/target_type require CLI) ----
	"ELBTargetGroup": {NodeType: NodeTypeBackendPool, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
	}},

	// ---- ManagedCluster (EKS / ECS) ----
	"EKSCluster": {NodeType: NodeTypeManagedCluster, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "version", NodeField: "cluster_version"},
		{OntologyField: "endpoint", NodeField: "cluster_endpoint"},
		{OntologyField: "state", NodeField: "cluster_status"},
	}},
	"ECSCluster": {NodeType: NodeTypeManagedCluster, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "version", NodeField: "cluster_version"},
		{OntologyField: "endpoint", NodeField: "cluster_endpoint"},
		{OntologyField: "state", NodeField: "cluster_status"},
	}},

	// ---- VPC (cidr/state populated on inferred VPC nodes) ----
	"EC2Vpc": {NodeType: NodeTypeVPC, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "cidr", NodeField: "cidr_block"},
		{OntologyField: "state", NodeField: "vpc_state"},
	}},

	// ---- Subnet (cidr not extracted at creation) ----
	"EC2Subnet": {NodeType: NodeTypeSubnet, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "vpc", NodeField: "vpc_id"},
	}},

	// ---- SecurityGroup ----
	"EC2SecurityGroup": {NodeType: NodeTypeSecurityGroup, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "vpc", NodeField: "vpc_id"},
	}},

	// ---- NetworkGateway (NAT / Internet Gateway) ----
	"NATGateway": {NodeType: NodeTypeNetworkGateway, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "type", NodeField: "connectivity_type"},
		{OntologyField: "state", NodeField: "nat_state"},
		{OntologyField: "vpc", NodeField: "vpc_id"},
	}},
	"InternetGateway": {NodeType: NodeTypeNetworkGateway, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "state", NodeField: "status"},
		{OntologyField: "vpc", NodeField: "vpc_id"},
	}},

	// ---- NetworkInterface (ENI) ----
	"NetworkInterface": {NodeType: NodeTypeNetworkInterface, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "private_ip_address", NodeField: "private_ip_address"},
		{OntologyField: "subnet", NodeField: "subnet_id"},
		{OntologyField: "vpc", NodeField: "vpc_id"},
	}},

	// ---- PrivateEndpoint (VPC Endpoint; endpoint state set at edge time) ----
	"VPCEndpoint": {NodeType: NodeTypePrivateEndpoint, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "state", NodeField: "status"},
		{OntologyField: "vpc", NodeField: "vpc_id"},
	}},

	// ---- PublicIP (Elastic IP; address resolved at edge time) ----
	"ElasticIP": {NodeType: NodeTypePublicIP, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
	}},

	// ---- DNSZone (record_count not extracted) ----
	"Route53HostedZone": {NodeType: NodeTypeDNSZone, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
	}},

	// ---- ContainerRegistry (ECR) ----
	"ECRRepository": {NodeType: NodeTypeContainerRegistry, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "uri", NodeField: "repository_uri"},
	}},
	"ECRPublicRepository": {NodeType: NodeTypeContainerRegistry, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "uri", NodeField: "repository_uri"},
	}},

	// ---- SecretVault (Secrets Manager) ----
	"SecretsManagerSecret": {NodeType: NodeTypeSecretVault, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
	}},

	// ---- EncryptionKey (KMS; usage not extracted) ----
	"KMSKey": {NodeType: NodeTypeEncryptionKey, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "state", NodeField: "status"},
	}},

	// ---- LogAggregator (retention_days not extracted) ----
	"CloudWatchLogGroup": {NodeType: NodeTypeLogAggregator, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
	}},
	"VPCFlowLog": {NodeType: NodeTypeLogAggregator, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
	}},
	"CloudTrail": {NodeType: NodeTypeLogAggregator, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
	}},
	"CloudTrailEventDataStore": {NodeType: NodeTypeLogAggregator, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
	}},

	// ---- APIGateway (endpoint DNS synthesized for API Gateway) ----
	"APIGatewayRestApi": {NodeType: NodeTypeAPIGateway, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "endpoint", NodeField: "dns_name"},
	}},
	"APIGatewayHttpApi": {NodeType: NodeTypeAPIGateway, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "endpoint", NodeField: "dns_name"},
	}},
	"APIGatewayWebSocketApi": {NodeType: NodeTypeAPIGateway, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "endpoint", NodeField: "dns_name"},
	}},

	// ---- InfraStack (CloudFormation) ----
	"CloudFormationStack": {NodeType: NodeTypeInfraStack, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "state", NodeField: "status"},
	}},

	// ---- BackupVault ----
	"BackupVault": {NodeType: NodeTypeBackupVault, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
	}},

	// ---- BackupPolicy (Backup Plan) ----
	"BackupPlan": {NodeType: NodeTypeBackupPolicy, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
	}},

	// ---- SecurityService (SecurityHub / GuardDuty) ----
	"SecurityHub": {NodeType: NodeTypeSecurityService, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "type", NodeField: "type"},
		{OntologyField: "state", NodeField: "status"},
	}},
	"SecurityHubStandard": {NodeType: NodeTypeSecurityService, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "type", NodeField: "type"},
		{OntologyField: "state", NodeField: "status"},
	}},
	"GuardDutyDetector": {NodeType: NodeTypeSecurityService, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "type", NodeField: "type"},
		{OntologyField: "state", NodeField: "status"},
	}},

	// ---- EmailService (SES) ----
	"SESConfigurationSet": {NodeType: NodeTypeEmailService, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "state", NodeField: "status"},
	}},
	"SESIdentity": {NodeType: NodeTypeEmailService, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "state", NodeField: "status"},
	}},

	// ---- Pod (EKS Pod; namespace/cluster not set at creation) ----
	"EKSPod": {NodeType: NodeTypePod, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
	}},

	// ---- CloudResource (generic fallbacks) ----
	"RDSClusterSnapshot": {NodeType: NodeTypeCloudResource, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "type", NodeField: "type"},
	}},
	"SageMakerResource": {NodeType: NodeTypeCloudResource, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "type", NodeField: "type"},
	}},
	"ACMCertificate": {NodeType: NodeTypeCloudResource, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "type", NodeField: "type"},
	}},
	"KinesisFirehoseStream": {NodeType: NodeTypeCloudResource, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "type", NodeField: "type"},
	}},
	"XRayResource": {NodeType: NodeTypeCloudResource, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "type", NodeField: "type"},
	}},
}
