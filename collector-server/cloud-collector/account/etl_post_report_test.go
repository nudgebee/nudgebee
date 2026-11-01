package account

import (
	"strings"
	"testing"

	awsProvider "nudgebee/collector/cloud/providers/aws"

	"github.com/stretchr/testify/assert"
)

func serviceRow(serviceName, region string) map[string]any {
	return map[string]any{"service_name": serviceName, "region": region}
}

// The cost report stores WAF as "awswaf" and the WAF lister as "AWSWAF". Grouped
// apart, each spelling ran its own full scan of WAF in every post-report job.
func TestGroupServiceRegions_OneScanPerServiceAcrossSpellings(t *testing.T) {
	rows := []map[string]any{
		serviceRow("awswaf", "us-east-1"),
		serviceRow("AWSWAF", "us-east-1"),
		serviceRow("AWSWAF", "eu-west-1"),
	}

	got, added := groupServiceRegions(rows, []string{"AWSWAF", "AmazonEC2"})

	assert.Equal(t, map[string][]string{
		"AWSWAF":    {"us-east-1", "eu-west-1"},
		"AmazonEC2": {},
	}, got)
	assert.Equal(t, []string{"AmazonEC2"}, added)
}

// A default whose rows exist only under another spelling is not added again with
// no regions, which would scan every region of the account.
func TestGroupServiceRegions_DefaultMatchedCaseInsensitively(t *testing.T) {
	got, added := groupServiceRegions([]map[string]any{serviceRow("awswaf", "us-east-1")}, []string{"AWSWAF"})

	assert.Equal(t, map[string][]string{"AWSWAF": {"us-east-1"}}, got)
	assert.Empty(t, added)
}

func TestGroupServiceRegions_NonDefaultKeepsFirstSpelling(t *testing.T) {
	rows := []map[string]any{
		serviceRow("AmazonApiGateway", "us-east-1"),
		serviceRow("amazonapigateway", "us-west-2"),
	}

	got, added := groupServiceRegions(rows, nil)

	assert.Equal(t, map[string][]string{"AmazonApiGateway": {"us-east-1", "us-west-2"}}, got)
	assert.Empty(t, added)
}

func TestGroupServiceRegions_AzureInsightsTypesShareOneService(t *testing.T) {
	rows := []map[string]any{
		serviceRow("microsoft.insights/actiongroups", "global"),
		serviceRow("microsoft.insights/metricalerts", "global"),
	}

	got, _ := groupServiceRegions(rows, nil)

	assert.Equal(t, map[string][]string{"microsoft.insights": {"global"}}, got)
}

// Every AWS default must name a lister, and no two may name the same one: the list
// carried "AWSECS" next to "AmazonECS", so ECS was scanned twice on every account.
// X-Ray must use the name its lister stores; under "AmazonXray" the scan archived
// rows named "amazonxray", which never exist, so deleted X-Ray groups stayed active.
func TestProviderDefaultServices_AwsNamesOneListerEach(t *testing.T) {
	defaults := providerDefaultServices["aws"]
	seen := map[string]string{}
	for _, name := range defaults {
		_, ok := awsProvider.GetAwsService(name)
		assert.Truef(t, ok, "AWS default service %q has no lister", name)

		key := strings.TrimPrefix(strings.TrimPrefix(strings.ToLower(name), "aws"), "amazon")
		if other, dup := seen[key]; dup {
			t.Errorf("AWS default services %q and %q resolve to the same lister %q", other, name, key)
		}
		seen[key] = name
	}

	assert.Contains(t, defaults, awsProvider.ServiceNameECS)
	assert.Contains(t, defaults, awsProvider.ServiceNameXray)
	assert.Contains(t, defaults, awsProvider.ServiceNameWAF)
}
