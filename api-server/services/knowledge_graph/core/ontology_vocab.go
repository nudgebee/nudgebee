package core

// canonicalOntologyVocab is the fixed, cross-cloud field vocabulary per ontology
// NodeType. Every OntologyField declared by a specific_type spec MUST be one of
// the keys allowed for its NodeType here; a spec may populate a subset (some
// clouds don't expose every field), but never a key outside the allowlist. The
// consistency guardrail test enforces this so the same NodeType exposes a
// uniform, predictable schema regardless of source cloud.
var canonicalOntologyVocab = map[NodeType][]string{
	NodeTypeService:             {"name", "namespace", "cluster", "environment"},
	NodeTypeComputeInstance:     {"name", "region", "type", "state", "public_ip_address", "private_ip_address", "created_at"},
	NodeTypeComputeInstancePool: {"name", "region", "type", "state", "node_count"},
	NodeTypeDatabase:            {"name", "location", "type", "version", "endpoint", "port", "encrypted"},
	NodeTypeCache:               {"name", "location", "type", "version", "endpoint", "port"},
	NodeTypeStorage:             {"name", "location", "type", "encrypted", "versioning", "public", "size"},
	NodeTypeMessageQueue:        {"name", "location", "type"},
	NodeTypeQueue:               {"name", "location", "type"},
	NodeTypeTopic:               {"name", "location", "type"},
	NodeTypeLoadBalancer:        {"name", "location", "type", "scheme", "dns_name", "state"},
	NodeTypeBackendPool:         {"name", "location", "protocol", "port", "target_type"},
	NodeTypeManagedCluster:      {"name", "location", "version", "endpoint", "state"},
	NodeTypeServerlessFunction:  {"name", "location", "runtime", "memory", "state"},
	NodeTypeVPC:                 {"name", "location", "cidr", "state"},
	NodeTypeSubnet:              {"name", "location", "cidr", "vpc"},
	NodeTypeSecurityGroup:       {"name", "location", "vpc"},
	NodeTypeNetworkInterface:    {"name", "location", "private_ip_address", "public_ip_address", "subnet", "vpc"},
	NodeTypeRouteTable:          {"name", "location", "vpc"},
	NodeTypeNetworkGateway:      {"name", "location", "type", "state", "vpc"},
	NodeTypePrivateEndpoint:     {"name", "location", "state", "vpc"},
	NodeTypePublicIP:            {"name", "location", "address"},
	NodeTypeDNSZone:             {"name", "location", "record_count"},
	NodeTypeDNSRecord:           {"name", "location", "type", "value"},
	NodeTypeCDN:                 {"name", "location", "state", "domain_name"},
	NodeTypeContainerRegistry:   {"name", "location", "uri"},
	NodeTypeContainerImage:      {"name", "registry", "tag", "digest"},
	NodeTypeArtifact:            {"name", "location", "type", "version"},
	NodeTypeSecretVault:         {"name", "location", "type"},
	NodeTypeEncryptionKey:       {"name", "location", "state", "usage"},
	NodeTypeLogAggregator:       {"name", "location", "retention_days"},
	NodeTypeMonitoringService:   {"name", "location", "type"},
	NodeTypeAPIGateway:          {"name", "location", "protocol", "endpoint"},
	NodeTypeInfraStack:          {"name", "location", "state"},
	NodeTypeBackupVault:         {"name", "location"},
	NodeTypeBackupPolicy:        {"name", "location"},
	NodeTypeSecurityService:     {"name", "location", "type", "state"},
	NodeTypeEmailService:        {"name", "location", "state"},
	NodeTypeAIService:           {"name", "location", "model"},
	NodeTypeServiceIdentity:     {"name", "location", "type", "arn"},
	NodeTypeExternalService:     {"name", "dns_name"},

	// Kubernetes concepts.
	NodeTypeWorkload:          {"name", "namespace", "cluster", "type", "replicas"},
	NodeTypeNode:              {"name", "cluster", "type", "region", "capacity_cpu", "capacity_memory"},
	NodeTypeK8sService:        {"name", "namespace", "cluster", "type", "cluster_ip"},
	NodeTypeNamespace:         {"name", "cluster"},
	NodeTypeCluster:           {"name", "version"},
	NodeTypePod:               {"name", "namespace", "cluster"},
	NodeTypePVC:               {"name", "namespace", "cluster", "storage_class", "capacity", "state"},
	NodeTypePV:                {"name", "cluster", "storage_class", "capacity", "state"},
	NodeTypeConfigMap:         {"name", "namespace", "cluster"},
	NodeTypeK8sSecret:         {"name", "namespace", "cluster", "type"},
	NodeTypeK8sServiceAccount: {"name", "namespace", "cluster"},
	NodeTypeIngress:           {"name", "namespace", "cluster", "host"},
	NodeTypeNetworkPolicy:     {"name", "namespace", "cluster"},
	NodeTypeJob:               {"name", "namespace", "cluster"},
	NodeTypeCronJob:           {"name", "namespace", "cluster"},
	NodeTypeCRD:               {"name", "namespace", "cluster", "type"},
	NodeTypeHelmChart:         {"name", "version", "repository"},
	NodeTypeHelmRelease:       {"name", "namespace", "cluster", "version", "state"},
	NodeTypeConfiguration:     {"name", "type"},
	NodeTypeRepository:        {"name", "type", "url"},
	NodeTypeCloudResource:     {"name", "location", "type"},

	// Source control identity (GitHub today; GitLab/Bitbucket/other SaaS people-and-teams later).
	NodeTypeSourceControlOrg: {"name", "type", "url"},
	NodeTypeUserAccount:      {"name", "username", "email", "url"},
	NodeTypeUserGroup:        {"name", "url"},

	// On-call identity (PagerDuty today; Opsgenie/VictorOps or other on-call SaaS later).
	NodeTypeOnCallService: {"name", "url", "status"},
}

// isCanonicalOntologyField reports whether field is allowed for nodeType.
func isCanonicalOntologyField(nodeType NodeType, field string) bool {
	for _, f := range canonicalOntologyVocab[nodeType] {
		if f == field {
			return true
		}
	}
	return false
}
