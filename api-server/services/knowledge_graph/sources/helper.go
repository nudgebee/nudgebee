package sources

import (
	"fmt"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
	"strings"
)

// GetResourceIDFromARN extracts the resource ID from an AWS ARN.
// ARN formats vary by AWS service:
//   - arn:partition:service:region:account-id:resource-type/resource-id
//   - arn:partition:service:region:account-id:resource-type:resource-id
//   - arn:partition:service:region:account-id:resource-id
//
// Examples:
//   - arn:aws:ec2:us-east-1:123456789012:instance/i-1234567890abcdef0 -> i-1234567890abcdef0
//   - arn:aws:rds:us-east-1:123456789012:db:my-database -> my-database
//   - arn:aws:s3:::my-bucket -> my-bucket
//   - arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/app/my-lb/50dc6c495c0c9188 -> my-lb/50dc6c495c0c9188
func GetResourceIDFromARN(arn string) string {
	if arn == "" {
		return ""
	}

	// Split ARN by colons
	parts := strings.Split(arn, ":")

	// ARN must have at least 6 parts: arn:partition:service:region:account-id:resource
	if len(parts) < 6 {
		return ""
	}

	// The resource part is everything after the 5th colon
	// This could be "resource-type/resource-id" or "resource-type:resource-id" or just "resource-id"
	resourcePart := strings.Join(parts[5:], ":")

	// Handle different resource formats:

	// Case 1: resource-type/resource-id (e.g., instance/i-12345)
	if slashIndex := strings.Index(resourcePart, "/"); slashIndex != -1 {
		resourceID := resourcePart[slashIndex+1:]
		return resourceID
	}

	// Case 2: resource-type:resource-id (e.g., db:my-database)
	if colonIndex := strings.Index(resourcePart, ":"); colonIndex != -1 {
		resourceID := resourcePart[colonIndex+1:]
		return resourceID
	}

	// Case 3: just resource-id (e.g., S3 buckets)
	return resourcePart
}

// GetResourceTypeFromARN extracts the resource type from an AWS ARN.
// Returns the resource type portion of the ARN, or empty string if not found.
//
// Examples:
//   - arn:aws:ec2:us-east-1:123456789012:instance/i-1234567890abcdef0 -> instance
//   - arn:aws:rds:us-east-1:123456789012:db:my-database -> db
//   - arn:aws:s3:::my-bucket -> (empty - S3 buckets don't have a resource type prefix)
func GetResourceTypeFromARN(arn string) string {
	if arn == "" {
		return ""
	}

	// Split ARN by colons
	parts := strings.Split(arn, ":")

	// ARN must have at least 6 parts
	if len(parts) < 6 {
		return ""
	}

	// The resource part is everything after the 5th colon
	resourcePart := strings.Join(parts[5:], ":")

	// Handle different resource formats:

	// Case 1: resource-type/resource-id
	if slashIndex := strings.Index(resourcePart, "/"); slashIndex != -1 {
		return resourcePart[:slashIndex]
	}

	// Case 2: resource-type:resource-id
	if colonIndex := strings.Index(resourcePart, ":"); colonIndex != -1 {
		return resourcePart[:colonIndex]
	}

	// Case 3: just resource-id (no type prefix)
	return ""
}

// GetServiceFromARN extracts the AWS service name from an ARN.
// Returns the service portion of the ARN (3rd component), or empty string if not found.
//
// Examples:
//   - arn:aws:ec2:us-east-1:123456789012:instance/i-1234567890abcdef0 -> ec2
//   - arn:aws:rds:us-east-1:123456789012:db:my-database -> rds
//   - arn:aws:s3:::my-bucket -> s3
func GetServiceFromARN(arn string) string {
	if arn == "" {
		return ""
	}

	parts := strings.Split(arn, ":")
	if len(parts) < 3 {
		return ""
	}

	return parts[2]
}

// GetRegionFromARN extracts the AWS region from an ARN.
// Returns the region portion of the ARN (4th component), or empty string if not found.
// Note: Some ARNs (like S3) may not have a region.
//
// Examples:
//   - arn:aws:ec2:us-east-1:123456789012:instance/i-1234567890abcdef0 -> us-east-1
//   - arn:aws:s3:::my-bucket -> (empty - S3 is global)
func GetRegionFromARN(arn string) string {
	if arn == "" {
		return ""
	}

	parts := strings.Split(arn, ":")
	if len(parts) < 4 {
		return ""
	}

	return parts[3]
}

// GetAccountIDFromARN extracts the AWS account ID from an ARN.
// Returns the account ID portion of the ARN (5th component), or empty string if not found.
// Note: Some ARNs (like S3) may not have an account ID.
//
// Examples:
//   - arn:aws:ec2:us-east-1:123456789012:instance/i-1234567890abcdef0 -> 123456789012
//   - arn:aws:s3:::my-bucket -> (empty - S3 doesn't include account ID)
func GetAccountIDFromARN(arn string) string {
	if arn == "" {
		return ""
	}

	parts := strings.Split(arn, ":")
	if len(parts) < 5 {
		return ""
	}

	return parts[4]
}

// GetCloudAccountAttributes retrieves cloud account attributes from the database.
// It queries the cloud_account_attrs table for k8s-related attributes and optionally
// looks up the cloud account ID if k8s_provider_account_number is present.
//
// Returns a map of attribute names to values, including:
//   - k8s_provider: The Kubernetes provider (e.g., eks, aks, gke)
//   - k8s_provider_account_number: The cloud provider account number
//   - k8s_provider_cluster_name: The cluster name
//   - cloud_account_id: The internal cloud account ID (added if account_number exists)
func GetCloudAccountAttributes(ctx *security.RequestContext, accountID string) (map[string]string, error) {
	databaseManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, fmt.Errorf("failed to get database manager: %w", err)
	}

	query := `
		select
			name,
			value
		from
			cloud_account_attrs
		where
			cloud_account_id = $1
			and name in (
				'k8s_provider',
				'k8s_provider_account_number',
				'k8s_provider_cluster_name'
			)
	`

	rows, err := databaseManager.Db.Queryx(query, accountID)
	if err != nil {
		return nil, fmt.Errorf("failed to query cloud account attributes: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			ctx.GetLogger().Error("Failed to close attribute rows", "error", err)
		}
	}()

	attributes := make(map[string]string)
	for rows.Next() {
		var name, value string
		err := rows.Scan(&name, &value)
		if err != nil {
			return nil, fmt.Errorf("failed to scan cloud account attribute: %w", err)
		}
		attributes[name] = value
	}

	if accountNumber, exists := attributes["k8s_provider_account_number"]; exists && accountNumber != "" {
		cloudAccountQuery := `select id from cloud_accounts where account_number = $1`

		var cloudAccountID string
		err := databaseManager.Db.QueryRowx(cloudAccountQuery, accountNumber).Scan(&cloudAccountID)
		if err != nil {
			return nil, fmt.Errorf("failed to query cloud account ID for account_number %s: %w", accountNumber, err)
		}

		attributes["cloud_account_id"] = cloudAccountID
	}

	return attributes, nil
}
