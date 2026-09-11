package account

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"nudgebee/services/internal/database"
)

// AccountRegionsKey is the account data key holding the operator-configured AWS
// region allowlist. The cloud-collector reads the same key when deciding which
// regions to crawl.
const AccountRegionsKey = "regions"

// awsRegionShape matches the AWS region code format (e.g. ap-southeast-2,
// us-gov-east-1). It is a shape check rather than a membership check against a
// pinned list: AWS adds regions, and a hardcoded set would reject a new one
// until we shipped a release.
var awsRegionShape = regexp.MustCompile(`^[a-z]{2}(-[a-z]+)+-[0-9]+$`)

// normalizeAWSRegions trims, de-duplicates and validates an operator-supplied
// region allowlist, preserving input order.
//
// Validation is deliberately strict and fails the request. A malformed region
// does not surface as an error downstream — the crawl simply returns nothing and
// the collector's zero-regions safeguard logs "refusing to archive", leaving an
// account that looks healthy while collecting nothing. Rejecting at the edge is
// the only place this is visible to the person who typed it.
func normalizeAWSRegions(regions []string) ([]string, error) {
	if len(regions) == 0 {
		return nil, nil
	}

	seen := make(map[string]struct{}, len(regions))
	normalized := make([]string, 0, len(regions))
	for _, region := range regions {
		region = strings.TrimSpace(region)
		if region == "" {
			continue
		}
		if !awsRegionShape.MatchString(region) {
			return nil, fmt.Errorf("account: %q is not a valid AWS region code (expected e.g. ap-southeast-2)", region)
		}
		if _, dup := seen[region]; dup {
			continue
		}
		seen[region] = struct{}{}
		normalized = append(normalized, region)
	}

	if len(normalized) == 0 {
		return nil, nil
	}
	return normalized, nil
}

// applyAWSRegions folds a validated region allowlist into the account data blob
// and returns the region the AWS SDK should bootstrap in.
//
// The bootstrap region must be one the allowlist covers: a role scoped with an
// aws:RequestedRegion condition denies every call made outside it, including the
// ec2:DescribeRegions call the collector would otherwise use to discover
// regions. Defaulting it to the first entry keeps the two in step without
// asking the operator for the same value twice.
func applyAWSRegions(data map[string]any, regions []string, currentRegion string) (map[string]any, string, error) {
	normalized, err := normalizeAWSRegions(regions)
	if err != nil {
		return data, currentRegion, err
	}
	if len(normalized) == 0 {
		return data, currentRegion, nil
	}

	if data == nil {
		data = map[string]any{}
	}
	data[AccountRegionsKey] = normalized

	// An explicit region outside the allowlist would be denied by exactly the
	// policy this feature exists to support, so the allowlist wins.
	if currentRegion == "" || !containsRegion(normalized, currentRegion) {
		currentRegion = normalized[0]
	}
	return data, currentRegion, nil
}

func containsRegion(regions []string, region string) bool {
	for _, r := range regions {
		if r == region {
			return true
		}
	}
	return false
}

// mergeAccountRegions reads the account's existing data blob, applies the new
// region allowlist to it, and returns the merged blob plus the bootstrap region
// to persist. An empty list clears the allowlist, returning the account to
// region auto-discovery.
//
// It merges because the data blob is shared — it also carries the CUR billing
// config — so writing a regions-only object would silently drop cost settings.
func mergeAccountRegions(dbms *database.DatabaseManager, accountId, tenantId string, regions []string, overrides map[string]any) (map[string]any, string, error) {
	normalized, err := normalizeAWSRegions(regions)
	if err != nil {
		return nil, "", err
	}

	var existingJSON *string
	if err := dbms.Db.Get(&existingJSON, "SELECT data FROM cloud_accounts WHERE id = $1 AND tenant = $2", accountId, tenantId); err != nil {
		return nil, "", fmt.Errorf("account: unable to read account data: %w", err)
	}

	data := map[string]any{}
	if existingJSON != nil && *existingJSON != "" {
		if err := json.Unmarshal([]byte(*existingJSON), &data); err != nil {
			return nil, "", fmt.Errorf("account: unable to parse existing account data: %w", err)
		}
	}

	merged, region := applyRegionsToData(data, normalized, overrides)
	return merged, region, nil
}

// applyRegionsToData layers an update onto an account's existing data blob:
// existing keys first, then any data edits from the same request, then the
// region allowlist. That order matters — a caller updating data and regions
// together must not be able to clobber the validated allowlist with a raw value
// of its own.
//
// An empty allowlist removes the key, returning the account to region
// auto-discovery, and leaves the bootstrap region alone: it is still a valid
// region to call from, and discovery takes over from there.
func applyRegionsToData(existing map[string]any, normalized []string, overrides map[string]any) (map[string]any, string) {
	if existing == nil {
		existing = map[string]any{}
	}
	for k, v := range overrides {
		existing[k] = v
	}

	if len(normalized) == 0 {
		delete(existing, AccountRegionsKey)
		return existing, ""
	}

	existing[AccountRegionsKey] = normalized
	return existing, normalized[0]
}

// carryForwardRegions preserves a stored region allowlist across a data write
// that does not mention regions.
//
// Writes to the data column replace it wholesale, so without this an unrelated
// edit — attaching a CUR, say — would drop the allowlist. The account would
// silently revert to discovering regions itself, which is exactly what a
// region-scoped IAM role forbids, and collection would stop again with nothing
// to indicate why.
//
// Only the allowlist is carried over; every other key keeps the existing
// replace semantics, so this changes nothing for callers that never set one.
func carryForwardRegions(dbms *database.DatabaseManager, accountId, tenantId string, data map[string]any) (map[string]any, error) {
	// An explicit regions key in the payload is the caller's own business —
	// don't second-guess it.
	if _, ok := data[AccountRegionsKey]; ok {
		return data, nil
	}

	var existingJSON *string
	if err := dbms.Db.Get(&existingJSON, "SELECT data FROM cloud_accounts WHERE id = $1 AND tenant = $2", accountId, tenantId); err != nil {
		return nil, fmt.Errorf("account: unable to read account data: %w", err)
	}
	if existingJSON == nil || *existingJSON == "" {
		return data, nil
	}

	existing := map[string]any{}
	if err := json.Unmarshal([]byte(*existingJSON), &existing); err != nil {
		return nil, fmt.Errorf("account: unable to parse existing account data: %w", err)
	}

	return carryRegionsInto(data, existing), nil
}

// carryRegionsInto copies data and restores the stored allowlist onto it. The
// incoming map is left untouched so the caller's request struct is not mutated.
func carryRegionsInto(data, existing map[string]any) map[string]any {
	stored, ok := existing[AccountRegionsKey]
	if !ok {
		return data
	}
	if _, ok := data[AccountRegionsKey]; ok {
		return data
	}

	carried := make(map[string]any, len(data)+1)
	for k, v := range data {
		carried[k] = v
	}
	carried[AccountRegionsKey] = stored
	return carried
}
