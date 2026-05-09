# Recommendation Rule Mapping - Usage Examples

## Direct Code Usage (No Database Required!)

The mapping data is stored directly in Go constants and can be queried in-memory with O(1) lookup performance.

## Quick Examples

### Example 1: Find All Idle Instance Rules Across All Clouds

```go
import "collector-server/cloud-collector/providers/constants"

// Get all provider-specific rules for "idle_instance"
rules := constants.GetProviderRulesForGeneric("idle_instance")

// Returns:
// ["aws_ec2_idle_instance", "aws_rds_idle_instance",
//  "aws_elasticache_idle_instance", "azure_vm_idle_instance",
//  "gcp_sql_inactive_instance"]
```

### Example 2: Convert Provider Rule to Generic Name

```go
// You have: "aws_ec2_idle_instance" from AWS
// You want: The generic name

genericName := constants.GetGenericRuleName("aws_ec2_idle_instance", "AWS")
// Returns: "idle_instance"
```

### Example 3: Filter Recommendations by Generic Rule

```go
// Your existing recommendation query
recommendations := getRecommendations(tenantID, cloudAccountID)

// Group by generic rule names
grouped := make(map[string][]Recommendation)

for _, rec := range recommendations {
    // Convert provider-specific rule to generic
    genericName := constants.GetGenericRuleName(rec.RuleName, rec.Provider)

    if genericName != "" {
        grouped[genericName] = append(grouped[genericName], rec)
    } else {
        // This is a provider-specific rule with no generic equivalent
        grouped[rec.RuleName] = append(grouped[rec.RuleName], rec)
    }
}

// Now you can query: "Show me all idle_instance recommendations"
idleInstances := grouped["idle_instance"]
// Contains: AWS EC2, AWS RDS, Azure VM, GCP SQL idle instances
```

### Example 4: Cross-Cloud Cost Savings Report

```go
import "collector-server/cloud-collector/providers/constants"

func GetCrossCloudSavings(tenantID string) map[string]float64 {
    recommendations := getAllRecommendations(tenantID)

    // Group savings by generic rule name
    savings := make(map[string]float64)

    for _, rec := range recommendations {
        genericName := constants.GetGenericRuleName(rec.RuleName, rec.Provider)

        if genericName != "" {
            savings[genericName] += rec.EstimatedSavings
        }
    }

    return savings
}

// Usage:
savings := GetCrossCloudSavings("tenant-123")
fmt.Printf("Total savings from idle instances: $%.2f\n", savings["idle_instance"])
fmt.Printf("Total savings from backup issues: $%.2f\n", savings["backup_disabled"])
```

### Example 5: Get All Mappings for a Provider

```go
// Get all AWS mappings
awsMappings := constants.GetRuleMappingsByProvider("AWS")

for _, mapping := range awsMappings {
    fmt.Printf("Generic: %s -> AWS: %s (Category: %s)\n",
        mapping.GenericRuleName,
        mapping.ProviderRuleName,
        mapping.Category)
}

// Output:
// Generic: idle_instance -> AWS: aws_ec2_idle_instance (Category: RightSizing)
// Generic: idle_instance -> AWS: aws_rds_idle_instance (Category: RightSizing)
// ...
```

### Example 6: Get All Security Recommendations

```go
// Get all security-related mappings
securityMappings := constants.GetRuleMappingsByCategory("Security")

fmt.Printf("Total security rules mapped: %d\n", len(securityMappings))

// Group by generic rule
securityRules := make(map[string][]string)
for _, mapping := range securityMappings {
    securityRules[mapping.GenericRuleName] = append(
        securityRules[mapping.GenericRuleName],
        mapping.ProviderRuleName,
    )
}

// Show all encryption-related rules across providers
fmt.Println("Encryption at rest rules:")
for _, rule := range securityRules["encryption_at_rest_disabled"] {
    fmt.Printf("  - %s\n", rule)
}
```

### Example 7: Check if Rule is Generic or Provider-Specific

```go
// Check if "idle_instance" is a generic rule
if constants.IsGenericRuleName("idle_instance") {
    fmt.Println("This is a generic rule that applies across providers")
}

// Check if a provider-specific rule has a generic mapping
if genericName := constants.GetGenericRuleName("aws_ec2_idle_instance", "AWS"); genericName != "" {
    fmt.Printf("'aws_ec2_idle_instance' maps to generic rule: %s\n", genericName)
} else {
    fmt.Println("'aws_ec2_idle_instance' is provider-specific only")
}
```

## Advanced Usage

### Example 8: Build Cross-Cloud Dashboard

```go
type RecommendationSummary struct {
    GenericRule      string
    TotalCount       int
    TotalSavings     float64
    ByProvider       map[string]int
    ByCategory       string
}

func GetRecommendationDashboard(tenantID string) []RecommendationSummary {
    recommendations := getAllRecommendations(tenantID)

    // Group by generic rule
    grouped := make(map[string]*RecommendationSummary)

    for _, rec := range recommendations {
        genericName := constants.GetGenericRuleName(rec.RuleName, rec.Provider)

        if genericName == "" {
            // Skip provider-specific rules
            continue
        }

        if _, exists := grouped[genericName]; !exists {
            // Get category from mapping
            mappings := constants.GetRuleMappingsByGenericRule(genericName)
            category := ""
            if len(mappings) > 0 {
                category = mappings[0].Category
            }

            grouped[genericName] = &RecommendationSummary{
                GenericRule:  genericName,
                ByProvider:   make(map[string]int),
                ByCategory:   category,
            }
        }

        summary := grouped[genericName]
        summary.TotalCount++
        summary.TotalSavings += rec.EstimatedSavings
        summary.ByProvider[rec.Provider]++
    }

    // Convert to slice
    result := make([]RecommendationSummary, 0, len(grouped))
    for _, summary := range grouped {
        result = append(result, *summary)
    }

    return result
}

// Usage:
dashboard := GetRecommendationDashboard("tenant-123")
for _, item := range dashboard {
    fmt.Printf("%s (%s):\n", item.GenericRule, item.ByCategory)
    fmt.Printf("  Total Count: %d\n", item.TotalCount)
    fmt.Printf("  Total Savings: $%.2f\n", item.TotalSavings)
    fmt.Printf("  By Provider: %v\n", item.ByProvider)
}

// Output:
// idle_instance (RightSizing):
//   Total Count: 45
//   Total Savings: $1250.00
//   By Provider: map[AWS:25 Azure:15 GCP:5]
```

### Example 9: Generate Cross-Cloud Report

```go
func GenerateCrossCloudReport(tenantID string) {
    recommendations := getAllRecommendations(tenantID)

    // Group by generic rule and provider
    type ProviderCount struct {
        Provider string
        Count    int
        Savings  float64
    }

    report := make(map[string][]ProviderCount)

    for _, rec := range recommendations {
        genericName := constants.GetGenericRuleName(rec.RuleName, rec.Provider)
        if genericName == "" {
            continue
        }

        found := false
        for i := range report[genericName] {
            if report[genericName][i].Provider == rec.Provider {
                report[genericName][i].Count++
                report[genericName][i].Savings += rec.EstimatedSavings
                found = true
                break
            }
        }

        if !found {
            report[genericName] = append(report[genericName], ProviderCount{
                Provider: rec.Provider,
                Count:    1,
                Savings:  rec.EstimatedSavings,
            })
        }
    }

    // Print report
    fmt.Println("Cross-Cloud Recommendation Report")
    fmt.Println("==================================")

    for genericRule, providers := range report {
        fmt.Printf("\n%s:\n", genericRule)
        for _, p := range providers {
            fmt.Printf("  %s: %d recommendations, $%.2f savings\n",
                p.Provider, p.Count, p.Savings)
        }
    }
}
```

### Example 10: Find Gaps in Coverage

```go
// Find which providers are missing for a generic rule
func FindCoverageGaps(genericRule string) {
    mappings := constants.GetRuleMappingsByGenericRule(genericRule)

    providersWithRule := make(map[string]bool)
    for _, m := range mappings {
        providersWithRule[m.Provider] = true
    }

    allProviders := []string{"AWS", "Azure", "GCP"}

    fmt.Printf("Coverage for '%s':\n", genericRule)
    for _, provider := range allProviders {
        if providersWithRule[provider] {
            fmt.Printf("  ✓ %s: Supported\n", provider)
        } else {
            fmt.Printf("  ✗ %s: Not implemented\n", provider)
        }
    }
}

// Usage:
FindCoverageGaps("idle_instance")
// Output:
// Coverage for 'idle_instance':
//   ✓ AWS: Supported
//   ✓ Azure: Supported
//   ✓ GCP: Supported

FindCoverageGaps("mfa_not_enabled")
// Output:
// Coverage for 'mfa_not_enabled':
//   ✓ AWS: Supported
//   ✗ Azure: Not implemented
//   ✗ GCP: Not implemented
```

## Performance

All functions use in-memory maps with O(1) lookup:
- **GetGenericRuleName()**: O(1)
- **GetProviderRulesForGeneric()**: O(1) to find, O(n) to iterate results
- **IsGenericRuleName()**: O(1)

Initialization happens once via `sync.Once` on first use.

## Available Helper Functions

| Function | Purpose | Example |
|----------|---------|---------|
| `GetAllRuleMappings()` | Get all mappings | `constants.GetAllRuleMappings()` |
| `GetRuleMappingsByGenericRule(name)` | Get all provider rules for generic | `constants.GetRuleMappingsByGenericRule("idle_instance")` |
| `GetProviderRulesForGeneric(name)` | Get just the rule names | `constants.GetProviderRulesForGeneric("idle_instance")` |
| `GetGenericRuleName(rule, provider)` | Provider → Generic | `constants.GetGenericRuleName("aws_ec2_idle_instance", "AWS")` |
| `GetProviderRuleName(generic, provider)` | Generic → Provider (first match) | `constants.GetProviderRuleName("idle_instance", "AWS")` |
| `GetRuleMappingsByProvider(provider)` | Get all for AWS/Azure/GCP | `constants.GetRuleMappingsByProvider("AWS")` |
| `GetRuleMappingsByCategory(category)` | Get all for category | `constants.GetRuleMappingsByCategory("Security")` |
| `IsGenericRuleName(name)` | Check if generic | `constants.IsGenericRuleName("idle_instance")` |

## Generic Rule Names

### Compute Resource Optimization (7 rules)
- `idle_instance` - 5 mappings
- `underutilized_instance` - 4 mappings
- `overutilized_instance` - 3 mappings
- `stopped_instance` - 7 mappings
- `old_instance` - 4 mappings
- `instance_generation_upgrade` - 5 mappings
- `alternate_instances` - 2 mappings

### Storage Optimization (4 rules)
- `unattached_volume` - 2 mappings
- `storage_lifecycle` - 3 mappings
- `storage_versioning` - 3 mappings
- `storage_class_optimization` - 6 mappings

### Security (10 rules)
- `encryption_at_rest_disabled` - 11 mappings
- `encryption_in_transit_disabled` - 3 mappings
- `encryption_cmk_disabled` - 16 mappings
- `ssl_disabled` - 2 mappings
- `tls_version_old` - 3 mappings
- `public_access` - 8 mappings
- `public_network_access_enabled` - 18 mappings
- `https_only_disabled` - 5 mappings
- `rbac_disabled` - 2 mappings
- `mfa_not_enabled` - 1 mapping

### Configuration (13 rules)
- `backup_disabled` - 7 mappings
- `geo_redundant_backups_disabled` - 2 mappings
- `snapshot_retention` - 2 mappings
- `logging_disabled` - 15 mappings
- `monitoring_disabled` - 2 mappings
- `audit_logging_disabled` - 2 mappings
- `detailed_monitoring_disabled` - 4 mappings
- `autoscaling_disabled` - 3 mappings
- `health_check_not_configured` - 6 mappings
- `auto_minor_upgrade_disabled` - 4 mappings
- `termination_protection_disabled` - 3 mappings
- `missing_tags` - 5 mappings
- `missing_labels` - 8 mappings

### High Availability (3 rules)
- `no_high_availability` - 2 mappings
- `single_region` - 3 mappings
- `no_redundancy` - 3 mappings

### Infrastructure Upgrade (3 rules)
- `outdated_version` - 2 mappings
- `deprecated_runtime` - 2 mappings
- `old_platform` - 2 mappings

### Container & Kubernetes (3 rules)
- `container_insights_disabled` - 1 mapping
- `network_policy_disabled` - 2 mappings
- `workload_identity_disabled` - 1 mapping

**Total: 42 generic rules, 180+ mappings**

## No Database Required!

Everything works in-memory. Just import the package and start using it:

```go
import "collector-server/cloud-collector/providers/constants"

// That's it! No initialization, no database setup.
// The constants are loaded once on first use via sync.Once
```
