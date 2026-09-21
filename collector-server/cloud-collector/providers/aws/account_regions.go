package aws

import (
	"nudgebee/collector/cloud/common"
	"nudgebee/collector/cloud/providers"
	"regexp"
	"strings"

	"github.com/samber/lo"
)

// accountRegionsKey is the account.Data key holding the operator-configured
// region allowlist for an AWS account.
const accountRegionsKey = "regions"

// awsRegionShape matches the AWS region code format (e.g. ap-southeast-2,
// us-gov-east-1, eu-central-1). Region strings reach us from operator input, and
// an unrecognised one does not fail loudly: the SDK either fails DNS resolution
// (skipped as a missing endpoint) or the crawl simply returns nothing, and the
// zero-regions archival safeguard then logs "refusing to archive" — an account
// that looks healthy while collecting nothing. Reject malformed values here so
// the configured list is never silently useless.
var awsRegionShape = regexp.MustCompile(`^[a-z]{2}(-[a-z]+)+-[0-9]+$`)

// IsValidAWSRegion reports whether s has the shape of an AWS region code. It is
// deliberately a shape check, not a membership check against a pinned list: AWS
// adds regions and a hardcoded set would reject a new one until we shipped a
// release.
func IsValidAWSRegion(s string) bool {
	return awsRegionShape.MatchString(strings.TrimSpace(s))
}

// configuredRegions returns the operator-configured region allowlist for an
// account, or nil when none is set.
//
// An account whose IAM role is scoped with an aws:RequestedRegion condition
// cannot be crawled by discovering regions from the account itself: the
// ec2:DescribeRegions call is denied, and even when it is allowed the resulting
// all-region fan-out produces AccessDenied in every other region, which
// ListResources reports as an error and StoreResources treats as a total
// failure — discarding the one region that did work. Such an account needs the
// region set supplied out of band, which is what this list is.
//
// An empty or absent list means "discover regions from the account", NOT "no
// regions" — the latter would collect nothing while looking healthy.
func configuredRegions(account providers.Account) []string {
	if account.Data == nil || *account.Data == "" {
		return nil
	}

	accountData := map[string]any{}
	if err := common.UnmarshalJson([]byte(*account.Data), &accountData); err != nil {
		return nil
	}

	raw, ok := accountData[accountRegionsKey].([]any)
	if !ok {
		return nil
	}

	regions := make([]string, 0, len(raw))
	for _, entry := range raw {
		s, ok := entry.(string)
		if !ok {
			continue
		}
		if s = strings.TrimSpace(s); s != "" && IsValidAWSRegion(s) {
			regions = append(regions, s)
		}
	}

	if len(regions) == 0 {
		return nil
	}
	return lo.Uniq(regions)
}

// ConfiguredRegions exposes the account's region allowlist to callers outside
// this package (the resource ETL, which decides both what to crawl and what to
// reconcile archival against). Returns nil when no list is configured.
func ConfiguredRegions(account providers.Account) []string {
	return configuredRegions(account)
}
