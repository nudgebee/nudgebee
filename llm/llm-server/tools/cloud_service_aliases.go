package tools

import (
	"strings"
	"unicode"
)

// cloudServiceAliases maps the names people use for a cloud service to how the
// inventory stores it in cloud_resourses.service_name. Azure stores ARM types,
// so a search for "cosmos" never matched "microsoft.documentdb/databaseaccounts"
// and Nubi reported a customer's Cosmos DB accounts as missing (#42036).
//
// Keys are compact (lowercase letters and digits only) and are looked up with
// and without a provider prefix ("Azure Cosmos DB" -> "cosmosdb"). Values are
// substrings of the stored service_name. Names whose stored form already
// contains the compact input ("dynamodb" in AmazonDynamoDB, "keyvault" in
// microsoft.keyvault/vaults) need no entry.
var cloudServiceAliases = map[string][]string{
	// Azure
	"cosmos":                {"microsoft.documentdb/databaseaccounts"},
	"cosmosdb":              {"microsoft.documentdb/databaseaccounts"},
	"aks":                   {"microsoft.containerservice/managedclusters"},
	"kubernetesservice":     {"microsoft.containerservice/managedclusters"},
	"appservice":            {"microsoft.web/sites"},
	"appservices":           {"microsoft.web/sites"},
	"webapp":                {"microsoft.web/sites"},
	"webapps":               {"microsoft.web/sites"},
	"functionapp":           {"microsoft.web/sites"},
	"functionapps":          {"microsoft.web/sites"},
	"function":              {"microsoft.web/sites/functions", "AWSLambda", "Cloud Functions"},
	"functions":             {"microsoft.web/sites/functions", "AWSLambda", "Cloud Functions"},
	"appserviceplan":        {"microsoft.web/serverfarms"},
	"vm":                    {"microsoft.compute/virtualmachines", "AmazonEC2", "Compute Engine"},
	"vms":                   {"microsoft.compute/virtualmachines", "AmazonEC2", "Compute Engine"},
	"vmss":                  {"microsoft.compute/virtualmachinescalesets"},
	"scaleset":              {"microsoft.compute/virtualmachinescalesets"},
	"scalesets":             {"microsoft.compute/virtualmachinescalesets"},
	"sqldatabase":           {"microsoft.sql/servers"},
	"sqldatabases":          {"microsoft.sql/servers"},
	"sqlmanagedinstance":    {"microsoft.sql/managedinstances"},
	"postgres":              {"microsoft.dbforpostgresql"},
	"cacheforredis":         {"microsoft.cache/redis"},
	"blob":                  {"microsoft.storage/storageaccounts", "AmazonS3", "Cloud Storage"},
	"blobs":                 {"microsoft.storage/storageaccounts", "AmazonS3", "Cloud Storage"},
	"blobstorage":           {"microsoft.storage/storageaccounts"},
	"files":                 {"microsoft.storage/storageaccounts/fileservices"},
	"acr":                   {"microsoft.containerregistry/registries"},
	"acrs":                  {"microsoft.containerregistry/registries"},
	"loganalytics":          {"microsoft.operationalinsights/workspaces"},
	"appgateway":            {"microsoft.network/applicationgateways"},
	"vnet":                  {"microsoft.network/virtualnetworks", "AmazonVPC"},
	"vnets":                 {"microsoft.network/virtualnetworks", "AmazonVPC"},
	"nsg":                   {"microsoft.network/networksecuritygroups"},
	"nsgs":                  {"microsoft.network/networksecuritygroups"},
	"publicip":              {"microsoft.network/publicipaddresses"},
	"publicips":             {"microsoft.network/publicipaddresses"},
	"logicapp":              {"microsoft.logic/workflows"},
	"logicapps":             {"microsoft.logic/workflows"},
	"sentinel":              {"microsoft.securityinsights"},
	"defender":              {"microsoft.security/pricings"},
	"defenderforcloud":      {"microsoft.security/pricings"},
	"recoveryservices":      {"microsoft.recoveryservices/vaults"},
	"recoveryservicesvault": {"microsoft.recoveryservices/vaults"},
	"eventhubs":             {"microsoft.eventhub/namespaces"},
	"arc":                   {"microsoft.hybridcompute/machines"},
	"ml":                    {"microsoft.machinelearningservices/workspaces"},
	"machinelearning":       {"microsoft.machinelearningservices/workspaces"},
	"loadbalancer":          {"microsoft.network/loadbalancers", "AWSELB", "Cloud Load Balancing"},
	"loadbalancers":         {"microsoft.network/loadbalancers", "AWSELB", "Cloud Load Balancing"},
	"applicationgateway":    {"microsoft.network/applicationgateways"},
	"applicationgateways":   {"microsoft.network/applicationgateways"},
	"virtualnetwork":        {"microsoft.network/virtualnetworks"},
	"networksecuritygroup":  {"microsoft.network/networksecuritygroups"},
	// AWS
	"sqs":                     {"AWSQueueService"},
	"elb":                     {"AWSELB"},
	"elbs":                    {"AWSELB"},
	"alb":                     {"AWSELB"},
	"albs":                    {"AWSELB"},
	"nlb":                     {"AWSELB"},
	"nlbs":                    {"AWSELB"},
	"applicationloadbalancer": {"AWSELB"},
	"networkloadbalancer":     {"AWSELB"},
	"kafka":                   {"AmazonMSK"},
	"opensearch":              {"AmazonES"},
	"elasticsearch":           {"AmazonES"},
	"aurora":                  {"AmazonRDS"},
	"ssm":                     {"AWSSystemsManager"},
	"parameterstore":          {"AWSSystemsManager"},
	// GCP
	"gce":              {"Compute Engine"},
	"gke":              {"Kubernetes Engine"},
	"kubernetesengine": {"Kubernetes Engine"},
	"gcs":              {"Cloud Storage"},
	"pubsub":           {"Cloud Pub/Sub"},
	"cloudsql":         {"Cloud SQL"},
	"cloudfunctions":   {"Cloud Functions"},
	"cloudrun":         {"Cloud Run"},
}

// cloudServicePrefixes are provider words people put in front of a service
// name. Stripping them lets "Azure Key Vault" match like "key vault".
var cloudServicePrefixes = []string{"microsoft", "azure", "amazon", "aws", "google", "gcp"}

// minStrippedPatternLen is the shortest prefix-stripped name used as a search
// pattern on its own.
const minStrippedPatternLen = 4

// storedServicePrefixes are how service names start in inventory: AWS
// (AmazonRDS, AWSKMS) and Azure (microsoft.sql/servers). GCP stores plain
// names ("Cloud SQL") and needs no prefix.
var storedServicePrefixes = []string{"amazon", "aws", "microsoft."}

// compactServiceName lowercases s and drops everything but letters and digits.
func compactServiceName(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// cloudServiceAliasPatterns returns the stored service names a friendly name
// stands for, or nil when the table has no entry.
func cloudServiceAliasPatterns(s string) []string {
	compact := compactServiceName(s)
	if aliases, ok := cloudServiceAliases[compact]; ok {
		return aliases
	}
	if stripped := stripServicePrefix(compact); stripped != compact {
		return cloudServiceAliases[stripped]
	}
	return nil
}

func stripServicePrefix(compact string) string {
	for _, p := range cloudServicePrefixes {
		if rest, ok := strings.CutPrefix(compact, p); ok && len(rest) >= 2 {
			return rest
		}
	}
	return compact
}

// cloudServicePatterns returns the ILIKE patterns a service filter matches:
// the input as given, its compact form ("secrets manager" matches
// AWSSecretsManager), the compact form without a provider prefix, and any
// aliases. Duplicates are dropped; order is stable.
func cloudServicePatterns(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	compact := compactServiceName(s)
	candidates := []string{s, compact}
	// A short remainder is too broad to search on: "AmazonES" would become
	// %es% and match most services. Instead it is rejoined with each
	// provider's stored prefix, so "AWS RDS" finds AmazonRDS and "Azure SQL"
	// finds microsoft.sql/servers.
	if stripped := stripServicePrefix(compact); len(stripped) >= minStrippedPatternLen {
		candidates = append(candidates, stripped)
	} else if stripped != compact {
		for _, p := range storedServicePrefixes {
			candidates = append(candidates, p+stripped)
		}
	}
	candidates = append(candidates, cloudServiceAliasPatterns(s)...)

	seen := make(map[string]bool, len(candidates))
	var patterns []string
	for _, c := range candidates {
		key := strings.ToLower(c)
		if c == "" || seen[key] {
			continue
		}
		seen[key] = true
		patterns = append(patterns, "%"+c+"%")
	}
	return patterns
}
