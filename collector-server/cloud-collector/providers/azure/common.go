package azure

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"nudgebee/collector/cloud/common"
	"nudgebee/collector/cloud/providers"
	"os"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

var nbStatusFromAzureProvisioningState = map[string]providers.ResourceStatus{
	"Succeeded":               providers.ResourceStatusActive,
	"Failed":                  providers.ResourceStatusInactive,
	"Canceled":                providers.ResourceStatusInactive,
	"Running":                 providers.ResourceStatusActive,
	"Creating":                providers.ResourceStatusActive,
	"Deleting":                providers.ResourceStatusDeleted,
	"Updating":                providers.ResourceStatusActive,
	"Online":                  providers.ResourceStatusActive,   // Azure SQL Database active status
	"Paused":                  providers.ResourceStatusInactive, // Azure SQL Database paused status
	"Pausing":                 providers.ResourceStatusInactive, // Azure SQL Database pausing
	"Resuming":                providers.ResourceStatusActive,   // Azure SQL Database resuming
	"Scaling":                 providers.ResourceStatusActive,   // Azure SQL Database scaling
	"Inaccessible":            providers.ResourceStatusInactive, // Azure SQL Database inaccessible
	"Standby":                 providers.ResourceStatusInactive, // Azure SQL Database standby
	"Disabled":                providers.ResourceStatusInactive, // Azure resources disabled state
	"ResolvingDNS":            providers.ResourceStatusActive,   // Storage Account resolving DNS
	"ValidatingConfiguration": providers.ResourceStatusActive,   // Storage Account validating
	"Available":               providers.ResourceStatusActive,   // Storage Account available
}

func toAzureTags(tags map[string]*string) map[string][]string {
	result := make(map[string][]string)
	for k, v := range tags {
		if v != nil {
			result[k] = []string{*v}
		}
	}
	return result
}

func structToMap(v any) map[string]any {
	data, err := json.Marshal(v)
	if err != nil {
		slog.Warn("structToMap: marshal failed", "error", err, "type", fmt.Sprintf("%T", v))
		return nil
	}
	if len(data) == 0 || string(data) == "null" {
		slog.Warn("structToMap: marshal produced empty/null", "type", fmt.Sprintf("%T", v), "json", string(data))
		return nil
	}
	var result map[string]any
	err = json.Unmarshal(data, &result)
	if err != nil {
		slog.Warn("structToMap: unmarshal failed", "error", err, "type", fmt.Sprintf("%T", v), "json_len", len(data))
		return nil
	}
	if len(result) == 0 {
		slog.Warn("structToMap: produced empty map", "type", fmt.Sprintf("%T", v), "json_len", len(data))
	}
	return result
}

type azureAuthSession struct {
	ClientID       string `json:"clientId"`
	ClientSecret   string `json:"clientSecret"`
	SubscriptionID string `json:"subscriptionId"`
	TenantID       string `json:"tenantId"`
}

func getAzureSessionFromAccount(ctx providers.CloudProviderContext, account providers.Account) (azureAuthSession, error) {
	// Env-var fallback for local integration tests / dev. Mirrors the GCP
	// GOOGLE_APPLICATION_CREDENTIALS path. Only used when the account record
	// has no AccessKey set, so production accounts stored in the DB are
	// unaffected.
	if account.AccessKey == nil && account.AccessSecret == nil {
		clientID := os.Getenv("AZURE_CLIENT_ID")
		clientSecret := os.Getenv("AZURE_CLIENT_SECRET")
		tenantID := os.Getenv("AZURE_TENANT_ID")
		subscriptionID := os.Getenv("AZURE_SUBSCRIPTION_ID")
		if clientID != "" && clientSecret != "" && tenantID != "" && subscriptionID != "" {
			return azureAuthSession{
				ClientID:       clientID,
				ClientSecret:   clientSecret,
				TenantID:       tenantID,
				SubscriptionID: subscriptionID,
			}, nil
		}
	}

	if account.AccessKey == nil {
		return azureAuthSession{}, fmt.Errorf("access key (client ID) is not provided")
	}
	if account.AccessSecret == nil {
		return azureAuthSession{}, fmt.Errorf("access secret is not provided")
	}
	if account.AssumeRole == nil {
		return azureAuthSession{}, fmt.Errorf("assume role (subscription ID) is not provided")
	}
	decryptedAccessSecret, err := common.Decrypt(*account.AccessSecret)
	if err != nil {
		return azureAuthSession{}, fmt.Errorf("failed to decrypt access secret: %w", err)
	}

	session := azureAuthSession{
		ClientID:       *account.AccessKey,
		ClientSecret:   decryptedAccessSecret,
		TenantID:       account.AccountNumber,
		SubscriptionID: *account.AssumeRole,
	}
	return session, nil
}

func getAzureCredsForAccount(ctx providers.CloudProviderContext, account providers.Account) (*azidentity.ClientSecretCredential, azureAuthSession, error) {
	session, err := getAzureSessionFromAccount(ctx, account)
	if err != nil {
		return nil, session, fmt.Errorf("failed to get azure session: %w", err)
	}

	cred, err := azidentity.NewClientSecretCredential(session.TenantID, session.ClientID, session.ClientSecret, nil)
	if err != nil {
		return nil, session, fmt.Errorf("failed to create credential: %w", err)
	}
	return cred, session, nil
}

// getAzureAuditOpts returns ARM client options with the permission audit policy attached,
// using audit info from the context (injected by auditedAzureService.enrichContext).
// Returns nil if no audit info is present in the context.
func getAzureAuditOpts(ctx providers.CloudProviderContext) *arm.ClientOptions {
	info := getAzureAuditInfo(ctx.GetContext())
	if info == nil {
		ctx.GetLogger().Debug("azure audit options not created as audit info was not found in context")
	}
	return azureAuditClientOptions(info)
}

func extractResourceGroup(id string) (string, error) {
	// Example ID: /subscriptions/subid/resourceGroups/rgname/providers/Microsoft.Sql/servers/servername
	parts := strings.Split(id, "/")
	for i, p := range parts {
		if strings.EqualFold(p, "resourceGroups") && i+1 < len(parts) {
			return parts[i+1], nil
		}
	}
	return "", fmt.Errorf("could not extract resource group from ID: %s", id)
}

// parseAzureResourceIDSegments splits an Azure ARM resource ID into a
// case-insensitive lookup of segment-name → following-value.
//
// Azure resource IDs are case-insensitive, and the collector stores them
// lowercased (account/etl_resources.go lowercases item.Id for Azure). So an
// ApplyCommand resource ID arriving from the UI looks like
// ".../resourcegroups/<rg>/providers/microsoft.sql/servers/<srv>/databases/<db>"
// — matching segment keys case-sensitively (e.g. "resourceGroups",
// "flexibleServers") silently fails, leaving an empty resource group / server
// name. That surfaced to users as the "invalid resource ID" error when
// pausing/stopping a database (issue #31091).
//
// Keys are lowercased; callers look up with lowercase names, e.g.
// seg["resourcegroups"], seg["flexibleservers"], seg["databases"].
func parseAzureResourceIDSegments(resourceID string) map[string]string {
	// Azure resource IDs strictly alternate type/name (e.g.
	// resourceGroups/<rg>/providers/<ns>/servers/<srv>/databases/<db>), so the
	// type segments sit at even indices and their names at the following odd
	// index. Drop empty segments (leading "/", doubled separators) first, then
	// step by 2 keying only on the type segments. Mapping every segment to its
	// successor instead would also insert names as keys — and a resource named
	// like a well-known type (e.g. a resource group named "servers") would then
	// clobber the real type's value.
	rawParts := strings.Split(resourceID, "/")
	parts := make([]string, 0, len(rawParts))
	for _, part := range rawParts {
		if part != "" {
			parts = append(parts, part)
		}
	}
	segments := make(map[string]string, len(parts)/2)
	for i := 0; i+1 < len(parts); i += 2 {
		segments[strings.ToLower(parts[i])] = parts[i+1]
	}
	return segments
}

var azureRegionMap = map[string]string{
	"useast":         "eastus",
	"useast2":        "eastus2",
	"uswest":         "westus",
	"uswest2":        "westus2",
	"uscentral":      "centralus",
	"usnorthcentral": "northcentralus",
	"ussouthcentral": "southcentralus",
	"uswestcentral":  "westcentralus",
	"cacentral":      "canadacentral",
	"caeast":         "canadaeast",
	"brsouth":        "brazilsouth",
	"brsoutheast":    "brazilsoutheast",
	"euwest":         "westeurope",
	"eunorth":        "northeurope",
	"uksouth":        "uksouth",
	"ukwest":         "ukwest",
	"dewestcentral":  "germanywestcentral",
	"denorth":        "germanynorth",
	"frcentral":      "francecentral",
	"frsouth":        "francesouth",
	"chnorth":        "switzerlandnorth",
	"chwest":         "switzerlandwest",
	"noeast":         "norwayeast",
	"nowest":         "norwaywest",
	"secentral":      "swedencentral",
	"zanorth":        "southafricanorth",
	"zawest":         "southafricawest",
	"aenorth":        "uaenorth",
	"aecentral":      "uaecentral",
	"inwest":         "westindia",
	"incentral":      "centralindia",
	"insouth":        "southindia",
	"apeast":         "eastasia",
	"apsoutheast":    "southeastasia",
	"aueast":         "australiaeast",
	"ausoutheast":    "australiasoutheast",
	"jpeast":         "japaneast",
	"jpwest":         "japanwest",
	"krcentral":      "koreacentral",
	"krsouth":        "koreasouth",
}

func normalizeAzureRegion(region string) string {
	sanitizedRegion := strings.ToLower(region)
	sanitizedRegion = strings.ReplaceAll(sanitizedRegion, " ", "")
	sanitizedRegion = strings.ReplaceAll(sanitizedRegion, "-", "")

	if normalized, ok := azureRegionMap[sanitizedRegion]; ok {
		return normalized
	}
	return sanitizedRegion
}

func strPtr(s string) *string {
	return &s
}

// getCreatedAtFromTags extracts creation time from resource tags
// Searches for tag keys containing "created" and parses the value as RFC3339
// Returns zero time if no valid creation time tag is found
func getCreatedAtFromTags(tags map[string]*string) time.Time {
	if tags == nil {
		return time.Time{}
	}
	for key, value := range tags {
		if strings.Contains(strings.ToLower(key), "created") && value != nil {
			if t, err := time.Parse(time.RFC3339, *value); err == nil {
				return t
			}
		}
	}
	return time.Time{}
}
