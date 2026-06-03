# Cloud Recommendation Rules - Constants Package

## Structure
- **generic_rules.go**: 41 cross-provider rules
- **aws_rules.go**: 221 AWS-specific rules
- **azure_rules.go**: 210 Azure-specific rules
- **gcp_rules.go**: 51 GCP-specific rules
- **rule_mapping.go**: Generic ↔ Provider mappings

## Usage Examples

```go
// 1. Generic rule
ruleName := constants.GetProviderRuleName(constants.BackupDisabled, constants.ProviderAzure)
// Returns: azure_vm_backup_disabled

// 2. Provider-specific (AWS-only)
constants.AwsEC2SpotInterruptionRisk

// 3. Cross-cloud query
idleRules := constants.GetProviderRulesForGeneric(constants.IdleInstance)
// Returns: [aws_ec2_idle_instance, azure_vm_idle_instance, ...]
```

## Helper Functions
- `GetProviderRuleName(generic, provider)` - Get provider-specific rule
- `GetGenericRuleName(providerRule, provider)` - Get generic rule
- `GetProviderRulesForGeneric(generic)` - All provider rules for generic
- `GetRuleMappingsByProvider(provider)` - All rules for provider
