package aws

import (
	"context"
	"fmt"
	"net"
	"nudgebee/collector/cloud/common"
	"nudgebee/collector/cloud/providers"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/lib/pq"
)

// VPCFlowLogRecord represents a parsed VPC Flow Log entry
type VPCFlowLogRecord struct {
	AccountID   string
	InterfaceID string
	SrcAddr     string
	DstAddr     string
	SrcPort     int
	DstPort     int
	Protocol    int
	Packets     int64
	Bytes       int64
	Start       int64
	End         int64
	Action      string
	LogStatus   string
}

// FlowLogConnection represents aggregated connection data between two resources
type FlowLogConnection struct {
	SourceIP     string
	DestIP       string
	DestPort     int
	TotalBytes   int64
	TotalPackets int64
	Connections  int
}

// GetVPCFlowLogRelationships queries VPC Flow Logs for a specific resource and returns relationships
func GetVPCFlowLogRelationships(
	ctx providers.CloudProviderContext,
	account providers.Account,
	region string,
	resourceIP string,
	resourcePort int,
	timeRange time.Duration,
) ([]providers.ServiceApplicationLink, error) {

	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws session", "error", err, "accountNumber", account.AccountNumber)
		return nil, err
	}
	cfg.Region = region

	// Get flow log group name (assumes VPC Flow Logs are configured)
	// This would come from the calling function that knows the VPC ID
	// For now, we'll construct a query that works with any log group containing flow logs

	// Build CloudWatch Logs Insights query
	_ = buildFlowLogQuery(resourceIP, resourcePort)

	// Query CloudWatch Logs
	_ = time.Now().Add(-timeRange)
	_ = time.Now()

	// Note: This uses the existing QueryLogs infrastructure from main.go
	// The caller should provide the log group name via GetLogGroupName

	ctx.GetLogger().Info("querying VPC Flow Logs",
		"resourceIP", resourceIP,
		"resourcePort", resourcePort,
		"timeRange", timeRange)

	// Return empty for now - the actual integration will be done in aws_servicemap.go
	// where we have access to the QueryLogs function
	return []providers.ServiceApplicationLink{}, nil
}

// buildFlowLogQuery constructs a CloudWatch Logs Insights query for VPC Flow Logs
func buildFlowLogQuery(targetIP string, targetPort int) string {
	// Query for all connections TO the target resource
	query := fmt.Sprintf(`
fields @timestamp, srcaddr, dstaddr, srcport, dstport, bytes, packets, action
| filter dstaddr = "%s" and dstport = %d and action = "ACCEPT"
| stats sum(bytes) as total_bytes, sum(packets) as total_packets, count(*) as connections by srcaddr
| sort total_bytes desc
| limit 100
`, targetIP, targetPort)

	return strings.TrimSpace(query)
}

// dbResourceResult represents a resource from database query
type dbResourceResult struct {
	ResourceID  string `db:"resourse_id"`
	Name        string `db:"name"`
	ServiceName string `db:"service_name"`
	Type        string `db:"type"`
	ARN         string `db:"arn"`
}

// queryResourceByPrivateIP queries the database for a resource by its private IP
// This is much faster than AWS API calls (< 10ms vs 500ms)
func queryResourceByPrivateIP(
	ctx providers.CloudProviderContext,
	accountID string,
	region string,
	privateIP string,
) (*providers.ServiceApplicationId, error) {
	db, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		ctx.GetLogger().Debug("failed to get database manager for IP lookup",
			"error", err,
			"ip", privateIP)
		return nil, err
	}

	query := `
		SELECT resourse_id, name, service_name, type, arn
		FROM cloud_resourses
		WHERE account = $1
		  AND region = $2
		  AND meta->>'PrivateIpAddress' = $3
		  AND is_active = true
		LIMIT 1
	`

	var result dbResourceResult
	err = db.QueryRowAndScan(&result, query, accountID, region, privateIP)
	if err != nil {
		ctx.GetLogger().Debug("no resource found in DB for IP",
			"ip", privateIP,
			"error", err)
		return nil, err
	}

	// Map service name to resource kind
	kind := mapServiceNameToKind(result.ServiceName, result.Type)

	ctx.GetLogger().Debug("found resource in DB by private IP",
		"ip", privateIP,
		"resourceId", result.ResourceID,
		"serviceName", result.ServiceName,
		"kind", kind)

	return &providers.ServiceApplicationId{
		Name:      result.ResourceID,
		Kind:      kind,
		Namespace: region,
	}, nil
}

// queryResourcesByPrivateIPs queries the database for multiple resources by their private IPs in one query
// This is much more efficient than calling queryResourceByPrivateIP in a loop
func queryResourcesByPrivateIPs(
	ctx providers.CloudProviderContext,
	account providers.Account,
	region string,
	privateIPs []string,
) (map[string]*providers.ServiceApplicationId, error) {
	if len(privateIPs) == 0 {
		return make(map[string]*providers.ServiceApplicationId), nil
	}

	// Use the account ID directly from the Account struct
	// This prevents cross-account contamination (multiple Nudgebee accounts can share same AWS account number)
	if account.ID == "" {
		return nil, fmt.Errorf("account ID is empty for account_number=%s account_name=%s",
			account.AccountNumber, account.AccountName)
	}

	ctx.GetLogger().Info("querying resources by private IPs",
		"accountID", account.ID,
		"accountNumber", account.AccountNumber,
		"accountName", account.AccountName)

	db, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		ctx.GetLogger().Debug("failed to get database manager for bulk IP lookup",
			"error", err,
			"ipCount", len(privateIPs))
		return nil, err
	}

	// Build query with ANY clause for multiple IPs (PostgreSQL array syntax)
	// This query handles two cases:
	// 1. ENI resources with PrivateIpAddress (single value)
	// 2. Lambda resources with PrivateIpAddresses (JSON array)
	baseQuery := `
		SELECT resourse_id, name, service_name, type, arn, is_active,
		       COALESCE(meta->>'PrivateIpAddress', ip_elem.value::text) as private_ip,
		       meta->>'Description' as description
		FROM cloud_resourses
		LEFT JOIN LATERAL jsonb_array_elements_text(meta->'PrivateIpAddresses') AS ip_elem(value) ON true
		WHERE account = $1
		  AND region = $2
		  AND %s
		  AND (
		    meta->>'PrivateIpAddress' = ANY($3)
		    OR ip_elem.value::text = ANY($3)
		  )
	`

	type resultRow struct {
		ResourceID  string  `db:"resourse_id"`
		Name        string  `db:"name"`
		ServiceName string  `db:"service_name"`
		Type        string  `db:"type"`
		ARN         string  `db:"arn"`
		IsActive    bool    `db:"is_active"`
		PrivateIP   string  `db:"private_ip"`
		Description *string `db:"description"` // Nullable - some resources don't have descriptions
	}

	// Try querying active resources first
	queryActive := fmt.Sprintf(baseQuery, "is_active = true")
	var results []resultRow
	err = db.QueryAndScan(&results, queryActive, account.ID, region, pq.Array(privateIPs))
	if err != nil {
		ctx.GetLogger().Debug("bulk IP lookup failed",
			"error", err,
			"ipCount", len(privateIPs))
		return nil, err
	}

	// Build map of IP -> ServiceApplicationId
	ipToResource := make(map[string]*providers.ServiceApplicationId)
	foundIPs := make(map[string]bool)

	for _, result := range results {
		kind := mapServiceNameToKind(result.ServiceName, result.Type)
		name := result.ResourceID

		// Extract AWS account number from ARN (format: arn:aws:service:region:account:resource)
		awsAccountNumber := ""
		if result.ARN != "" {
			arnParts := strings.Split(result.ARN, ":")
			if len(arnParts) >= 5 {
				awsAccountNumber = arnParts[4]
			}
		}

		// Special handling for ENIs: check if it's a Lambda ENI or ELB ENI
		if result.Type == "network-interface" && result.ServiceName == "AmazonVPC" {
			// Try to extract Lambda name from Name field first, then Description field
			lambdaName := extractLambdaNameFromENI(result.Name)
			if lambdaName == "" && result.Description != nil && *result.Description != "" {
				lambdaName = extractLambdaNameFromENI(*result.Description)
			}

			if lambdaName != "" {
				name = lambdaName
				kind = "lambda"
				ctx.GetLogger().Debug("mapped ENI to Lambda function",
					"eni", result.ResourceID,
					"lambdaName", lambdaName,
					"ip", result.PrivateIP)
			} else {
				// Try to extract ELB identifier from Name field first, then Description field
				elbIdentifier, _ := extractELBIdentifierFromENI(result.Name)
				if elbIdentifier == "" && result.Description != nil && *result.Description != "" {
					elbIdentifier, _ = extractELBIdentifierFromENI(*result.Description)
				}

				if elbIdentifier != "" && awsAccountNumber != "" {
					// Build full ARN to match AWS Config format
					// Format: arn:aws:elasticloadbalancing:region:account:loadbalancer/app/name/id
					name = fmt.Sprintf("arn:aws:elasticloadbalancing:%s:%s:loadbalancer/%s",
						region, awsAccountNumber, elbIdentifier)
					kind = "elb" // Use "elb" for all load balancers to match AWS Config
					ctx.GetLogger().Debug("mapped ENI to ELB",
						"eni", result.ResourceID,
						"elbARN", name,
						"elbKind", kind,
						"ip", result.PrivateIP)
				}
			}
		}

		ipToResource[result.PrivateIP] = &providers.ServiceApplicationId{
			Name:      name,
			Kind:      kind,
			Namespace: region,
		}
		foundIPs[result.PrivateIP] = true
	}

	// If some IPs weren't found in active resources, query inactive resources
	if len(ipToResource) < len(privateIPs) {
		// Find IPs that weren't found
		missingIPs := []string{}
		for _, ip := range privateIPs {
			if !foundIPs[ip] {
				missingIPs = append(missingIPs, ip)
			}
		}

		ctx.GetLogger().Info("some IPs not found in active resources, checking inactive",
			"queriedIPs", len(privateIPs),
			"foundActive", len(ipToResource),
			"missingIPs", len(missingIPs))

		if len(missingIPs) > 0 {
			ctx.GetLogger().Info("querying inactive resources for missing IPs",
				"missingIPsList", missingIPs)

			// Query without is_active filter for missing IPs
			queryInactive := fmt.Sprintf(baseQuery, "is_active = false")
			ctx.GetLogger().Info("executing inactive resource query",
				"accountID", account.ID,
				"region", region,
				"ipCount", len(missingIPs),
				"query", queryInactive)
			var inactiveResults []resultRow
			err = db.QueryAndScan(&inactiveResults, queryInactive, account.ID, region, pq.Array(missingIPs))
			if err != nil {
				ctx.GetLogger().Error("inactive resource lookup failed",
					"error", err,
					"ipCount", len(missingIPs))
				// Don't fail - just continue with what we have
			} else {
				ctx.GetLogger().Info("inactive resource query returned results",
					"resultCount", len(inactiveResults))
				// Add inactive resources to results
				for _, result := range inactiveResults {
					kind := mapServiceNameToKind(result.ServiceName, result.Type)
					name := result.ResourceID

					// Extract AWS account number from ARN
					awsAccountNumber := ""
					if result.ARN != "" {
						arnParts := strings.Split(result.ARN, ":")
						if len(arnParts) >= 5 {
							awsAccountNumber = arnParts[4]
						}
					}

					// Special handling for ENIs: check if it's a Lambda ENI or ELB ENI
					if result.Type == "network-interface" && result.ServiceName == "AmazonVPC" {
						lambdaName := extractLambdaNameFromENI(result.Name)
						if lambdaName == "" && result.Description != nil && *result.Description != "" {
							lambdaName = extractLambdaNameFromENI(*result.Description)
						}
						if lambdaName != "" {
							name = lambdaName
							kind = "lambda"
						} else {
							// Try to extract ELB identifier
							elbIdentifier, _ := extractELBIdentifierFromENI(result.Name)
							if elbIdentifier == "" && result.Description != nil && *result.Description != "" {
								elbIdentifier, _ = extractELBIdentifierFromENI(*result.Description)
							}
							if elbIdentifier != "" && awsAccountNumber != "" {
								// Build full ARN to match AWS Config format
								name = fmt.Sprintf("arn:aws:elasticloadbalancing:%s:%s:loadbalancer/%s",
									region, awsAccountNumber, elbIdentifier)
								kind = "elb" // Use "elb" for all load balancers
							}
						}
					}

					ipToResource[result.PrivateIP] = &providers.ServiceApplicationId{
						Name:      name,
						Kind:      kind,
						Namespace: region,
					}

					ctx.GetLogger().Info("mapped IP to inactive resource",
						"ip", result.PrivateIP,
						"resourceId", name,
						"kind", kind)
				}
			}
		}
	}

	ctx.GetLogger().Info("bulk IP lookup completed",
		"queriedIPs", len(privateIPs),
		"foundResources", len(ipToResource))

	return ipToResource, nil
}

// mapServiceNameToKind converts AWS service name to resource kind
func mapServiceNameToKind(serviceName string, resourceType string) string {
	switch serviceName {
	case "AmazonEC2":
		if resourceType == "compute-instance" {
			return "ec2"
		}
		return "ec2"
	case "AWSLambda":
		return "lambda"
	case "AmazonRDS":
		return "rds"
	case "AmazonECS":
		return "ecs"
	case "AWSELB", "AmazonELB":
		return "elb"
	default:
		return strings.ToLower(serviceName)
	}
}

// MapIPsToAWSResourcesBatch maps multiple IP addresses to AWS resources using a single batch API call
// This is much more efficient than calling MapIPToAWSResource in a loop (1 API call vs N API calls)
func MapIPsToAWSResourcesBatch(
	ctx providers.CloudProviderContext,
	cfg aws.Config,
	ips []string,
	region string,
) (map[string]*providers.ServiceApplicationId, error) {
	if len(ips) == 0 {
		return make(map[string]*providers.ServiceApplicationId), nil
	}

	ctx.GetLogger().Debug("batch querying AWS API for IPs", "ipCount", len(ips))
	ec2Svc := ec2.NewFromConfig(cfg)

	// Query EC2 for network interfaces with these private IPs (single batch call)
	eniOutput, err := ec2Svc.DescribeNetworkInterfaces(context.TODO(), &ec2.DescribeNetworkInterfacesInput{
		Filters: []ec2types.Filter{
			{
				Name:   aws.String("addresses.private-ip-address"),
				Values: ips,
			},
		},
	})

	if err != nil {
		ctx.GetLogger().Debug("failed to query network interfaces for IPs", "ipCount", len(ips), "error", err)
		return nil, err
	}

	// Build map of IP -> ServiceApplicationId
	ipToResource := make(map[string]*providers.ServiceApplicationId)

	for _, eni := range eniOutput.NetworkInterfaces {
		// Extract the private IP from this ENI
		var eniIP string
		if eni.PrivateIpAddress != nil {
			eniIP = *eni.PrivateIpAddress
		} else {
			continue // Skip ENIs without private IP
		}

		// Determine resource type based on ENI characteristics
		var resource *providers.ServiceApplicationId

		// Check EC2 instance attachment
		if eni.Attachment != nil && eni.Attachment.InstanceId != nil {
			resource = &providers.ServiceApplicationId{
				Name:      *eni.Attachment.InstanceId,
				Kind:      "ec2",
				Namespace: region,
			}
		} else if eni.Description != nil && strings.Contains(*eni.Description, "AWS Lambda VPC ENI") {
			// Lambda function - extract name from description using helper function
			functionName := extractLambdaNameFromENI(*eni.Description)
			if functionName != "" {
				resource = &providers.ServiceApplicationId{
					Name:      functionName,
					Kind:      "lambda",
					Namespace: region,
				}
			} else {
				// Fallback: use ENI ID if extraction failed
				resource = &providers.ServiceApplicationId{
					Name:      *eni.NetworkInterfaceId,
					Kind:      "lambda",
					Namespace: region,
				}
			}
		} else if eni.Description != nil && (strings.Contains(*eni.Description, "ecs-") || strings.Contains(*eni.Description, "ECS")) {
			// ECS task
			resource = &providers.ServiceApplicationId{
				Name:      *eni.NetworkInterfaceId,
				Kind:      "ecs",
				Namespace: region,
			}
		} else if eni.RequesterId != nil {
			requesterId := *eni.RequesterId
			// ELB
			if strings.Contains(requesterId, "amazon-elb") || strings.Contains(requesterId, "elasticloadbalancing") {
				resource = &providers.ServiceApplicationId{
					Name:      *eni.NetworkInterfaceId,
					Kind:      "elb",
					Namespace: region,
				}
			} else if strings.HasPrefix(requesterId, "amazon-rds") {
				// RDS
				resource = &providers.ServiceApplicationId{
					Name:      *eni.NetworkInterfaceId,
					Kind:      "rds",
					Namespace: region,
				}
			}
		}

		// Default: unknown resource type
		if resource == nil {
			resource = &providers.ServiceApplicationId{
				Name:      *eni.NetworkInterfaceId,
				Kind:      "vpc",
				Namespace: region,
			}
		}

		ipToResource[eniIP] = resource
		ctx.GetLogger().Debug("mapped IP via batch AWS API",
			"ip", eniIP,
			"resourceId", resource.Name,
			"kind", resource.Kind)
	}

	ctx.GetLogger().Debug("batch AWS API query completed",
		"queriedIPs", len(ips),
		"foundResources", len(ipToResource))

	return ipToResource, nil
}

// extractLambdaNameFromENI extracts Lambda function name from ENI description
// Expected formats:
//   - "AWS Lambda VPC ENI-FunctionName-UUID" → "FunctionName"
//   - "AWS Lambda VPC ENI-Function-Name-UUID" → "Function-Name"
//   - "AWS Lambda VPC ENI-FunctionName" → "FunctionName" (no UUID)
//
// Returns empty string if not a Lambda ENI
func extractLambdaNameFromENI(eniDescription string) string {
	if !strings.HasPrefix(eniDescription, "AWS Lambda VPC ENI-") {
		return ""
	}

	// Remove prefix: "AWS Lambda VPC ENI-"
	withoutPrefix := strings.TrimPrefix(eniDescription, "AWS Lambda VPC ENI-")
	if withoutPrefix == "" {
		return ""
	}

	// AWS sometimes includes UUID at the end (last segment is alphanumeric 8+ chars)
	// Other times it's just the function name
	parts := strings.Split(withoutPrefix, "-")
	if len(parts) == 0 {
		return ""
	}

	// Check if last part looks like a UUID (8+ hex characters)
	lastPart := parts[len(parts)-1]
	isUUID := len(lastPart) >= 8 && isAlphanumeric(lastPart)

	if isUUID && len(parts) > 1 {
		// Last part is UUID, remove it
		functionName := strings.Join(parts[:len(parts)-1], "-")
		return functionName
	}

	// No UUID, the whole string is the function name
	return withoutPrefix
}

// extractELBIdentifierFromENI extracts ELB identifier and kind from ENI description
// Expected formats:
//   - "ELB app/Demo-Frontend-ALB/9ef0c75b824fa80c" → ("app/Demo-Frontend-ALB/9ef0c75b824fa80c", "elbv2")
//   - "ELB net/my-nlb/50dc6c495f0c9188" → ("net/my-nlb/50dc6c495f0c9188", "elbv2")
//   - "ELB my-classic-elb" → ("my-classic-elb", "elb")
//
// Returns (identifier, kind) or ("", "") if not an ELB ENI
func extractELBIdentifierFromENI(eniDescription string) (string, string) {
	// Check for ELB prefix (case-insensitive)
	if !strings.HasPrefix(eniDescription, "ELB ") && !strings.HasPrefix(eniDescription, "elb ") {
		return "", ""
	}

	// Remove prefix: "ELB " or "elb "
	identifier := strings.TrimSpace(eniDescription[4:])
	if identifier == "" {
		return "", ""
	}

	// Determine kind based on identifier format:
	// - ALB/NLB format: "app/..." or "net/..." → use "elbv2"
	// - Classic ELB format: just a name → use "elb"
	if strings.HasPrefix(identifier, "app/") || strings.HasPrefix(identifier, "net/") {
		// Application or Network Load Balancer
		return identifier, "elbv2"
	}

	// Classic ELB (no type prefix)
	return identifier, "elb"
}

// isAlphanumeric checks if a string contains only alphanumeric characters
func isAlphanumeric(s string) bool {
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

// MapIPToAWSResource maps an IP address to an AWS resource (EC2, RDS, Lambda, ECS)
// It tries database lookup first (fast), then falls back to AWS API (slow)
func MapIPToAWSResource(
	ctx providers.CloudProviderContext,
	account providers.Account,
	cfg aws.Config,
	ip string,
	region string,
) (*providers.ServiceApplicationId, error) {

	// Try database lookup first (fast path - <10ms)
	if account.AccountNumber != "" {
		resource, err := queryResourceByPrivateIP(ctx, account.AccountNumber, region, ip)
		if err == nil && resource != nil {
			ctx.GetLogger().Info("found resource in database by private IP",
				"ip", ip,
				"resourceId", resource.Name,
				"kind", resource.Kind)
			return resource, nil
		}
		// Log debug message if DB lookup failed
		ctx.GetLogger().Debug("database lookup failed, falling back to AWS API",
			"ip", ip,
			"error", err)
	}

	// Fallback to AWS API lookup (slow path - 500ms+)
	ctx.GetLogger().Debug("using AWS API to map IP to resource", "ip", ip)
	ec2Svc := ec2.NewFromConfig(cfg)

	// Query EC2 for network interface with this private IP
	eniOutput, err := ec2Svc.DescribeNetworkInterfaces(context.TODO(), &ec2.DescribeNetworkInterfacesInput{
		Filters: []ec2types.Filter{
			{
				Name:   aws.String("addresses.private-ip-address"),
				Values: []string{ip},
			},
		},
	})

	if err != nil {
		ctx.GetLogger().Debug("failed to find network interface for IP", "ip", ip, "error", err)
		return nil, err
	}

	if len(eniOutput.NetworkInterfaces) == 0 {
		ctx.GetLogger().Debug("no network interface found for IP", "ip", ip)
		return nil, fmt.Errorf("no network interface found for IP %s", ip)
	}

	eni := eniOutput.NetworkInterfaces[0]

	// Determine resource type based on ENI attachment
	if eni.Attachment != nil && eni.Attachment.InstanceId != nil {
		// EC2 Instance
		return &providers.ServiceApplicationId{
			Name:      *eni.Attachment.InstanceId,
			Kind:      "ec2",
			Namespace: region,
		}, nil
	}

	// Check description for Lambda function
	if eni.Description != nil && strings.Contains(*eni.Description, "AWS Lambda VPC ENI") {
		// Extract Lambda function name from description
		// Format: "AWS Lambda VPC ENI-function-name-uuid"
		desc := *eni.Description
		if strings.HasPrefix(desc, "AWS Lambda VPC ENI-") {
			parts := strings.Split(desc, "-")
			if len(parts) >= 4 {
				functionName := strings.Join(parts[1:len(parts)-1], "-")
				return &providers.ServiceApplicationId{
					Name:      functionName,
					Kind:      "lambda",
					Namespace: region,
				}, nil
			}
		}

		// Fallback: use ENI ID
		return &providers.ServiceApplicationId{
			Name:      *eni.NetworkInterfaceId,
			Kind:      "lambda",
			Namespace: region,
		}, nil
	}

	// Check description for ECS task
	if eni.Description != nil && (strings.Contains(*eni.Description, "ecs-") || strings.Contains(*eni.Description, "ECS")) {
		// ECS tasks - try to extract task ARN or use ENI ID
		if eni.RequesterId != nil && strings.Contains(*eni.RequesterId, "ecs") {
			return &providers.ServiceApplicationId{
				Name:      *eni.NetworkInterfaceId,
				Kind:      "ecs",
				Namespace: region,
			}, nil
		}
	}

	// Note: RDS reverse lookup is not implemented as it requires DNS resolution
	// RDS endpoints are hostnames, not IPs, so we'd need to resolve them first.
	// For now, rely on database-based IP lookup for RDS instances.
	_ = cfg // Keep cfg parameter for future use

	// Check for other interface types via requester ID
	if eni.RequesterId != nil {
		requesterId := *eni.RequesterId

		// Amazon ELB
		if strings.Contains(requesterId, "amazon-elb") || strings.Contains(requesterId, "elasticloadbalancing") {
			return &providers.ServiceApplicationId{
				Name:      *eni.NetworkInterfaceId,
				Kind:      "elb",
				Namespace: region,
			}, nil
		}

		// RDS (requester ID starts with "amazon-rds")
		if strings.HasPrefix(requesterId, "amazon-rds") {
			return &providers.ServiceApplicationId{
				Name:      *eni.NetworkInterfaceId,
				Kind:      "rds",
				Namespace: region,
			}, nil
		}
	}

	// Default: unknown resource type, return ENI ID
	ctx.GetLogger().Debug("unknown resource type for ENI", "eniId", *eni.NetworkInterfaceId, "ip", ip)
	return &providers.ServiceApplicationId{
		Name:      *eni.NetworkInterfaceId,
		Kind:      "vpc",
		Namespace: region,
	}, nil
}

// ResolveRDSEndpointToIP resolves an RDS endpoint hostname to an IP address
// Uses DNS resolution to convert RDS endpoint to actual IP
func ResolveRDSEndpointToIP(
	ctx providers.CloudProviderContext,
	hostname string,
) (string, error) {
	ctx.GetLogger().Debug("resolving RDS endpoint to IP", "hostname", hostname)

	// Perform DNS lookup
	ips, err := net.LookupIP(hostname)
	if err != nil {
		ctx.GetLogger().Warn("failed to resolve RDS endpoint",
			"hostname", hostname,
			"error", err)
		return "", fmt.Errorf("failed to resolve %s: %w", hostname, err)
	}

	if len(ips) == 0 {
		return "", fmt.Errorf("no IP addresses found for %s", hostname)
	}

	// Return first IPv4 address
	for _, ip := range ips {
		if ip.To4() != nil {
			ctx.GetLogger().Debug("resolved RDS endpoint to IPv4",
				"hostname", hostname,
				"ip", ip.String())
			return ip.String(), nil
		}
	}

	// If no IPv4 found, return first IPv6
	ctx.GetLogger().Debug("no IPv4 address found, using IPv6",
		"hostname", hostname,
		"ip", ips[0].String())
	return ips[0].String(), nil
}

// GetResourceIPAddress extracts the private IP address for a given AWS resource
func GetResourceIPAddress(
	ctx providers.CloudProviderContext,
	account providers.Account,
	serviceApplicationId providers.ServiceApplicationId,
) (string, int, error) {

	resourceId := serviceApplicationId.Name
	resourceKind := serviceApplicationId.Kind
	region := serviceApplicationId.Namespace

	// Use service's DescribeResource to get metadata instead of direct SDK calls
	service, ok := GetAwsService(resourceKind)
	if !ok {
		return "", 0, fmt.Errorf("unsupported service: %s", resourceKind)
	}

	// Call DescribeResource to get all resource details
	metadata, err := service.DescribeResource(ctx, account, region, resourceId)
	if err != nil {
		return "", 0, fmt.Errorf("failed to get resource metadata for %s: %w", resourceKind, err)
	}

	// Use metadata to return IP and port
	if metadata.PrivateIP == "" {
		return "", 0, fmt.Errorf("resource %s has no private IP", resourceId)
	}

	return metadata.PrivateIP, metadata.Port, nil
}
