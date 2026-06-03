package common

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"cloud.google.com/go/bigquery"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/advisor/armadvisor"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/costmanagement/armcostmanagement"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"
	"github.com/google/uuid"
	"google.golang.org/api/cloudresourcemanager/v3"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
	"google.golang.org/api/recommender/v1"
)

// CloudProvider represents supported cloud providers
type CloudProvider string

const (
	CloudProviderAzure CloudProvider = "Azure"
	CloudProviderGCP   CloudProvider = "GCP"
)

// PermissionType represents different permission categories
type PermissionType string

const (
	// Azure permissions
	PermissionAzureCostManagement  PermissionType = "Cost Management"
	PermissionAzureResourceAPI     PermissionType = "Resource API"
	PermissionAzureRecommendations PermissionType = "Recommendations API"

	// GCP permissions
	PermissionGCPResourceManager PermissionType = "Resource Manager"
	PermissionGCPRecommender     PermissionType = "Recommender API"
	PermissionGCPBigQueryBilling PermissionType = "BigQuery Billing Data"
	PermissionGCPCloudMonitoring PermissionType = "Cloud Monitoring (Alerts Webhook)"
)

// PermissionStatus represents the status of a permission check
type PermissionStatus struct {
	Permission  PermissionType `json:"permission"`
	HasAccess   bool           `json:"hasAccess"`
	ErrorDetail string         `json:"errorDetail,omitempty"`
}

// ValidationResult contains the result of credential validation
type ValidationResult struct {
	Success            bool               `json:"success"`
	Provider           CloudProvider      `json:"provider"`
	MissingPermissions []PermissionType   `json:"missingPermissions,omitempty"`
	PermissionDetails  []PermissionStatus `json:"permissionDetails"`
	ErrorMessage       string             `json:"errorMessage,omitempty"`
	// AccountNumber is populated for providers where the validator can
	// derive a canonical account/subscription/project identifier from the
	// supplied credentials (currently only AWS via sts:GetCallerIdentity).
	AccountNumber string `json:"accountNumber,omitempty"`
	// Cur carries the auto-discovered AWS Cost & Usage Report details so
	// the caller can persist them on the cloud_accounts row without
	// re-running DescribeReportDefinitions at sync time.
	Cur *AWSCurInfo `json:"cur,omitempty"`
}

// AzureCredentials contains Azure authentication details
type AzureCredentials struct {
	TenantID       string
	ClientID       string
	ClientSecret   string
	SubscriptionID string
}

// GCPCredentials contains GCP authentication details
type GCPCredentials struct {
	CredentialsJSON  string
	ProjectID        string
	BillingProjectID string // optional: project where billing dataset lives
	BillingDatasetID string // optional: BigQuery dataset name
	BillingTableID   string // optional: BigQuery table name
}

// requiredPermissions defines which permissions are checked for each provider
var requiredPermissions = map[CloudProvider][]PermissionType{
	CloudProviderAzure: {
		PermissionAzureCostManagement,
		PermissionAzureResourceAPI,
		PermissionAzureRecommendations,
	},
	CloudProviderGCP: {
		PermissionGCPResourceManager,
		PermissionGCPRecommender,
	},
}

// GetRequiredPermissions returns the list of permissions that will be checked for a provider
func GetRequiredPermissions(provider CloudProvider) []PermissionType {
	return requiredPermissions[provider]
}

// ValidateAzureCredentials validates Azure credentials and checks all required permissions
func ValidateAzureCredentials(ctx context.Context, creds AzureCredentials) ValidationResult {
	result := ValidationResult{
		Success:            true,
		Provider:           CloudProviderAzure,
		PermissionDetails:  []PermissionStatus{},
		MissingPermissions: []PermissionType{},
	}

	// Validate subscription ID is a valid UUID
	if _, err := uuid.Parse(creds.SubscriptionID); err != nil {
		result.Success = false
		result.ErrorMessage = "invalid subscription ID format: must be a valid UUID"
		return result
	}

	// Create Azure credential
	cred, err := azidentity.NewClientSecretCredential(creds.TenantID, creds.ClientID, creds.ClientSecret, nil)
	if err != nil {
		result.Success = false
		result.ErrorMessage = fmt.Sprintf("failed to create Azure credentials: %v", err)
		return result
	}

	// Check each required permission
	permissions := GetRequiredPermissions(CloudProviderAzure)
	for _, permission := range permissions {
		status := PermissionStatus{Permission: permission}

		switch permission {
		case PermissionAzureCostManagement:
			status.HasAccess = checkAzureCostManagementAccess(ctx, cred, creds.SubscriptionID, &status)
		case PermissionAzureResourceAPI:
			status.HasAccess = checkAzureResourceAPIAccess(ctx, cred, creds.SubscriptionID, &status)
		case PermissionAzureRecommendations:
			status.HasAccess = checkAzureRecommendationsAccess(ctx, cred, creds.SubscriptionID, &status)
		}

		result.PermissionDetails = append(result.PermissionDetails, status)
		if !status.HasAccess {
			result.MissingPermissions = append(result.MissingPermissions, permission)
		}
	}

	// Resource API is critical — if we can't list resource groups, the account is non-functional.
	// Other permissions (Cost Management, Recommendations) remain non-blocking warnings.
	for _, status := range result.PermissionDetails {
		if status.Permission == PermissionAzureResourceAPI && !status.HasAccess {
			result.Success = false
			if strings.Contains(status.ErrorDetail, "InvalidAuthenticationTokenTenant") {
				result.ErrorMessage = "subscription belongs to a different Azure AD tenant than the provided credentials"
			} else {
				result.ErrorMessage = fmt.Sprintf("Resource API access check failed: %s", status.ErrorDetail)
			}
			return result
		}
	}

	result.Success = true
	return result
}

// checkAzureCostManagementAccess validates access to Azure Cost Management API
func checkAzureCostManagementAccess(ctx context.Context, cred *azidentity.ClientSecretCredential, subscriptionID string, status *PermissionStatus) bool {
	costClient, err := armcostmanagement.NewQueryClient(cred, nil)
	if err != nil {
		status.ErrorDetail = fmt.Sprintf("failed to create cost management client: %v", err)
		return false
	}

	// Use a minimal query with 1-day custom timeframe for lightweight permission check
	scope := fmt.Sprintf("/subscriptions/%s", subscriptionID)
	queryType := armcostmanagement.ExportTypeActualCost
	timeframe := armcostmanagement.TimeframeTypeCustom

	// Define a 1-day window (yesterday) for truly minimal data retrieval
	now := time.Now().UTC()
	yesterday := now.AddDate(0, 0, -1)

	timePeriod := &armcostmanagement.QueryTimePeriod{
		From: &yesterday,
		To:   &now,
	}

	query := armcostmanagement.QueryDefinition{
		Type:       &queryType,
		Timeframe:  &timeframe,
		TimePeriod: timePeriod,
		Dataset: &armcostmanagement.QueryDataset{
			Granularity: func() *armcostmanagement.GranularityType {
				g := armcostmanagement.GranularityTypeDaily
				return &g
			}(),
		},
	}

	// Use short timeout for permission check
	queryCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	_, err = costClient.Usage(queryCtx, scope, query, nil)
	if err != nil {
		// Use azcore.ResponseError for proper error detection
		var respErr *azcore.ResponseError
		if errors.As(err, &respErr) {
			if respErr.StatusCode == 403 || respErr.StatusCode == 401 {
				status.ErrorDetail = fmt.Sprintf("authorization failed: %s (status: %d)", respErr.ErrorCode, respErr.StatusCode)
				return false
			}
		}
		// For other errors, log but treat as access denied
		status.ErrorDetail = fmt.Sprintf("cost management API check failed: %v", err)
		return false
	}

	return true
}

// checkAzureResourceAPIAccess validates access to Azure Resource Management API
func checkAzureResourceAPIAccess(ctx context.Context, cred *azidentity.ClientSecretCredential, subscriptionID string, status *PermissionStatus) bool {
	rgClient, err := armresources.NewResourceGroupsClient(subscriptionID, cred, nil)
	if err != nil {
		status.ErrorDetail = fmt.Sprintf("failed to create resource groups client: %v", err)
		return false
	}

	// Attempt to list resource groups (lightweight operation)
	queryCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	pager := rgClient.NewListPager(nil)
	_, err = pager.NextPage(queryCtx)
	if err != nil {
		var respErr *azcore.ResponseError
		if errors.As(err, &respErr) {
			if respErr.StatusCode == 403 || respErr.StatusCode == 401 {
				status.ErrorDetail = fmt.Sprintf("authorization failed: %s (status: %d)", respErr.ErrorCode, respErr.StatusCode)
				return false
			}
		}
		status.ErrorDetail = fmt.Sprintf("resource API check failed: %v", err)
		return false
	}

	return true
}

// checkAzureRecommendationsAccess validates access to Azure Advisor Recommendations API
func checkAzureRecommendationsAccess(ctx context.Context, cred *azidentity.ClientSecretCredential, subscriptionID string, status *PermissionStatus) bool {
	advisorClient, err := armadvisor.NewRecommendationsClient(subscriptionID, cred, nil)
	if err != nil {
		status.ErrorDetail = fmt.Sprintf("failed to create advisor client: %v", err)
		return false
	}

	// Attempt to list recommendations (lightweight operation)
	queryCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	pager := advisorClient.NewListPager(nil)
	_, err = pager.NextPage(queryCtx)
	if err != nil {
		var respErr *azcore.ResponseError
		if errors.As(err, &respErr) {
			if respErr.StatusCode == 403 || respErr.StatusCode == 401 {
				status.ErrorDetail = fmt.Sprintf("authorization failed: %s (status: %d)", respErr.ErrorCode, respErr.StatusCode)
				return false
			}
		}
		status.ErrorDetail = fmt.Sprintf("recommendations API check failed: %v", err)
		return false
	}

	return true
}

// ValidateGCPCredentials validates GCP credentials and checks all required permissions
func ValidateGCPCredentials(ctx context.Context, creds GCPCredentials) ValidationResult {
	result := ValidationResult{
		Success:            true,
		Provider:           CloudProviderGCP,
		PermissionDetails:  []PermissionStatus{},
		MissingPermissions: []PermissionType{},
	}

	// Validate credentials JSON is not empty
	if strings.TrimSpace(creds.CredentialsJSON) == "" {
		result.Success = false
		result.ErrorMessage = "GCP credentials JSON is empty"
		return result
	}

	// Check each required permission
	permissions := GetRequiredPermissions(CloudProviderGCP)
	for _, permission := range permissions {
		status := PermissionStatus{Permission: permission}

		switch permission {
		case PermissionGCPResourceManager:
			status.HasAccess = checkGCPResourceManagerAccess(ctx, creds, &status)
		case PermissionGCPRecommender:
			status.HasAccess = checkGCPRecommenderAccess(ctx, creds, &status)
		}

		result.PermissionDetails = append(result.PermissionDetails, status)
		if !status.HasAccess {
			result.MissingPermissions = append(result.MissingPermissions, permission)
		}
	}

	// If billing data fields are provided, also check BigQuery access
	if strings.TrimSpace(creds.BillingDatasetID) != "" && strings.TrimSpace(creds.BillingTableID) != "" {
		status := PermissionStatus{Permission: PermissionGCPBigQueryBilling}
		status.HasAccess = checkGCPBigQueryBillingAccess(ctx, creds, &status)
		result.PermissionDetails = append(result.PermissionDetails, status)
		if !status.HasAccess {
			result.MissingPermissions = append(result.MissingPermissions, PermissionGCPBigQueryBilling)
		}
	}

	// Check Cloud Monitoring permission (optional — needed for auto webhook setup)
	{
		status := PermissionStatus{Permission: PermissionGCPCloudMonitoring}
		status.HasAccess = checkGCPCloudMonitoringAccess(ctx, creds, &status)
		result.PermissionDetails = append(result.PermissionDetails, status)
		if !status.HasAccess {
			result.MissingPermissions = append(result.MissingPermissions, PermissionGCPCloudMonitoring)
		}
	}

	// Resource Manager is critical — if we can't list projects, the credentials are broken.
	// Other permissions (Recommender, BigQuery, Cloud Monitoring) remain non-blocking warnings.
	for _, status := range result.PermissionDetails {
		if status.Permission == PermissionGCPResourceManager && !status.HasAccess {
			result.Success = false
			result.ErrorMessage = fmt.Sprintf("Resource Manager access check failed: %s", status.ErrorDetail)
			return result
		}
	}

	result.Success = true
	return result
}

// CheckGCPCloudMonitoringPermission checks whether the given GCP credentials have
// Cloud Monitoring notification channel permissions. Returns (hasPermission, errorDetail).
func CheckGCPCloudMonitoringPermission(ctx context.Context, creds GCPCredentials) (bool, string) {
	status := &PermissionStatus{}
	ok := checkGCPCloudMonitoringAccess(ctx, creds, status)
	return ok, status.ErrorDetail
}

// checkGCPCloudMonitoringAccess validates that the SA can create notification channels.
// This permission is needed for auto-setup of webhook notification channels.
func checkGCPCloudMonitoringAccess(ctx context.Context, creds GCPCredentials, status *PermissionStatus) bool {
	queryCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	projectID := creds.ProjectID
	if projectID == "" {
		status.ErrorDetail = "project ID is required for Cloud Monitoring permission check"
		return false
	}

	crmService, err := cloudresourcemanager.NewService(
		queryCtx,
		option.WithAuthCredentialsJSON(option.CredentialsType("service_account"), []byte(creds.CredentialsJSON)),
	)
	if err != nil {
		status.ErrorDetail = fmt.Sprintf("failed to create Resource Manager client: %v", err)
		return false
	}

	requiredPermissions := []string{
		"monitoring.notificationChannels.create",
		"monitoring.notificationChannels.update",
		"monitoring.alertPolicies.update",
	}

	resp, err := crmService.Projects.TestIamPermissions(
		fmt.Sprintf("projects/%s", projectID),
		&cloudresourcemanager.TestIamPermissionsRequest{
			Permissions: requiredPermissions,
		},
	).Context(queryCtx).Do()
	if err != nil {
		if gErr, ok := err.(*googleapi.Error); ok && gErr.Code == 403 {
			status.ErrorDetail = "Service account lacks Cloud Monitoring permissions. Grant roles/monitoring.editor for auto webhook setup, or use manual setup."
		} else {
			status.ErrorDetail = fmt.Sprintf("failed to check Cloud Monitoring access: %v", err)
		}
		return false
	}

	granted := make(map[string]bool, len(resp.Permissions))
	for _, p := range resp.Permissions {
		granted[p] = true
	}

	var missing []string
	for _, p := range requiredPermissions {
		if !granted[p] {
			missing = append(missing, p)
		}
	}
	if len(missing) > 0 {
		status.ErrorDetail = fmt.Sprintf("Service account lacks permissions: %s. Grant roles/monitoring.editor.", strings.Join(missing, ", "))
		return false
	}

	return true
}

// checkGCPBigQueryBillingAccess validates that the BigQuery billing dataset and table exist and are accessible
func checkGCPBigQueryBillingAccess(ctx context.Context, creds GCPCredentials, status *PermissionStatus) bool {
	queryCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	projectID := creds.BillingProjectID
	if strings.TrimSpace(projectID) == "" {
		projectID = creds.ProjectID
	}

	client, err := bigquery.NewClient(
		queryCtx,
		projectID,
		option.WithAuthCredentialsJSON(option.CredentialsType("service_account"), []byte(creds.CredentialsJSON)),
	)
	if err != nil {
		status.ErrorDetail = fmt.Sprintf("failed to create BigQuery client: %v", err)
		return false
	}
	defer func() {
		_ = client.Close()
	}()

	// Check dataset exists
	dataset := client.Dataset(creds.BillingDatasetID)
	_, err = dataset.Metadata(queryCtx)
	if err != nil {
		status.ErrorDetail = fmt.Sprintf(
			"BigQuery dataset '%s' not found in project '%s'. "+
				"Verify billing export is configured in GCP Console > Billing > Billing export. Error: %v",
			creds.BillingDatasetID, projectID, err,
		)
		return false
	}

	// Check table exists
	table := dataset.Table(creds.BillingTableID)
	_, err = table.Metadata(queryCtx)
	if err != nil {
		status.ErrorDetail = fmt.Sprintf(
			"BigQuery table '%s' not found in dataset '%s' (project '%s'). "+
				"Verify the table name matches your billing export configuration. Error: %v",
			creds.BillingTableID, creds.BillingDatasetID, projectID, err,
		)
		return false
	}

	return true
}

// GCPProject represents a discovered GCP project
type GCPProject struct {
	ProjectID string `json:"project_id"`
	Name      string `json:"name"`
	State     string `json:"state"`
}

// ListGCPProjects discovers all accessible GCP projects using the provided service account credentials
func ListGCPProjects(ctx context.Context, credentialsJSON string) ([]GCPProject, error) {
	if strings.TrimSpace(credentialsJSON) == "" {
		return nil, fmt.Errorf("credentials JSON is empty")
	}

	queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	resourceManager, err := cloudresourcemanager.NewService(
		queryCtx,
		option.WithAuthCredentialsJSON(
			option.CredentialsType("service_account"),
			[]byte(credentialsJSON),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource manager service: %w", err)
	}

	const maxProjects = 500
	var projects []GCPProject
	pageToken := ""
	for {
		call := resourceManager.Projects.Search().Context(queryCtx)
		if pageToken != "" {
			call = call.PageToken(pageToken)
		}

		resp, err := call.Do()
		if err != nil {
			return nil, fmt.Errorf("failed to search projects: %w", err)
		}

		for _, p := range resp.Projects {
			if p.State == "ACTIVE" {
				projects = append(projects, GCPProject{
					ProjectID: p.ProjectId,
					Name:      p.DisplayName,
					State:     p.State,
				})
			}
		}

		if resp.NextPageToken == "" || len(projects) >= maxProjects {
			break
		}
		pageToken = resp.NextPageToken
	}

	return projects, nil
}

// checkGCPResourceManagerAccess validates access to GCP Resource Manager API
func checkGCPResourceManagerAccess(ctx context.Context, creds GCPCredentials, status *PermissionStatus) bool {
	queryCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	// Create Resource Manager service
	resourceManager, err := cloudresourcemanager.NewService(
		queryCtx,
		option.WithAuthCredentialsJSON(
			option.CredentialsType("service_account"),
			[]byte(creds.CredentialsJSON),
		),
	)
	if err != nil {
		status.ErrorDetail = fmt.Sprintf("failed to create resource manager service: %v", err)
		return false
	}

	// Attempt to get project details (lightweight operation)
	projectName := fmt.Sprintf("projects/%s", creds.ProjectID)
	_, err = resourceManager.Projects.Get(projectName).Context(queryCtx).Do()
	if err != nil {
		var googleErr *googleapi.Error
		if errors.As(err, &googleErr) {
			if googleErr.Code == 403 || googleErr.Code == 401 {
				status.ErrorDetail = fmt.Sprintf("authorization failed: %v", err)
				return false
			}
		}
		status.ErrorDetail = fmt.Sprintf("resource manager API check failed: %v", err)
		return false
	}

	return true
}

// AzureSubscription represents a single discovered Azure subscription
type AzureSubscription struct {
	SubscriptionID string `json:"subscription_id"`
	DisplayName    string `json:"display_name"`
	State          string `json:"state"`
}

// ListAzureSubscriptions discovers all accessible Azure subscriptions using the provided service principal credentials
func ListAzureSubscriptions(ctx context.Context, tenantID, clientID, clientSecret string) ([]AzureSubscription, error) {
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(clientID) == "" || strings.TrimSpace(clientSecret) == "" {
		return nil, fmt.Errorf("tenant_id, client_id, and client_secret are required")
	}

	cred, err := azidentity.NewClientSecretCredential(tenantID, clientID, clientSecret, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create Azure credentials: %w", err)
	}

	queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	subClient, err := armsubscriptions.NewClient(cred, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create subscriptions client: %w", err)
	}

	var subscriptions []AzureSubscription
	pager := subClient.NewListPager(nil)
	for pager.More() {
		page, err := pager.NextPage(queryCtx)
		if err != nil {
			return nil, fmt.Errorf("failed to list subscriptions: %w", err)
		}

		for _, sub := range page.Value {
			if sub.State != nil && *sub.State == armsubscriptions.SubscriptionStateEnabled {
				displayName := ""
				if sub.DisplayName != nil {
					displayName = *sub.DisplayName
				}
				subID := ""
				if sub.SubscriptionID != nil {
					subID = *sub.SubscriptionID
				}
				subscriptions = append(subscriptions, AzureSubscription{
					SubscriptionID: subID,
					DisplayName:    displayName,
					State:          string(*sub.State),
				})
			}
		}
	}

	return subscriptions, nil
}

// checkGCPRecommenderAccess validates access to GCP Recommender API
func checkGCPRecommenderAccess(ctx context.Context, creds GCPCredentials, status *PermissionStatus) bool {
	queryCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	// Create Recommender service
	recommenderService, err := recommender.NewService(
		queryCtx,
		option.WithAuthCredentialsJSON(
			option.CredentialsType("service_account"),
			[]byte(creds.CredentialsJSON),
		),
	)
	if err != nil {
		status.ErrorDetail = fmt.Sprintf("failed to create recommender service: %v", err)
		return false
	}

	// Attempt to list recommendations (lightweight operation)
	parent := fmt.Sprintf("projects/%s/locations/global/recommenders/google.compute.instance.MachineTypeRecommender", creds.ProjectID)
	_, err = recommenderService.Projects.Locations.Recommenders.Recommendations.List(parent).Context(queryCtx).Do()
	if err != nil {
		var googleErr *googleapi.Error
		if errors.As(err, &googleErr) {
			if googleErr.Code == 403 || googleErr.Code == 401 {
				status.ErrorDetail = fmt.Sprintf("authorization failed: %v", err)
				return false
			}
		}
		status.ErrorDetail = fmt.Sprintf("recommender API check failed: %v", err)
		return false
	}

	return true
}
