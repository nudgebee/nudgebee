package gcloud

import (
	"fmt"
	"nudgebee/collector/cloud/common"
	"nudgebee/collector/cloud/providers"
	"os"
	"strings"

	compute "cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/api/option"
)

type gcloudAuthSession struct {
	Opts                    []option.ClientOption
	AccountCred             string
	Type                    string `json:"type"`
	ProjectId               string `json:"project_id"`
	PrivateKeyId            string `json:"private_key_id"`
	PrivateKey              string `json:"private_key"`
	ClientEmail             string `json:"client_email"`
	ClientId                string `json:"client_id"`
	AuthUri                 string `json:"auth_uri"`
	TokenUri                string `json:"token_uri"`
	AuthProviderX509CertUrl string `json:"auth_provider_x509_cert_url"`
	ClientX509CertUrl       string `json:"client_x509_cert_url"`
	UniverseDomain          string `json:"universe_domain"`
}

func getGcloudSessionFromAccount(ctx providers.CloudProviderContext, account providers.Account) (gcloudAuthSession, error) {
	opts := []option.ClientOption{}

	// Primary path: credentials provided via encrypted AccessSecret
	if account.AccessSecret != nil && *account.AccessSecret != "" {
		decryptedAccessSecret, err := common.Decrypt(*account.AccessSecret)
		if err != nil {
			ctx.GetLogger().Error("Failed to decrypt access secret for account %s: %v", account.AccountNumber, err)
			return gcloudAuthSession{}, fmt.Errorf("failed to decrypt access secret: %w", err)
		}
		opts = append(opts, option.WithAuthCredentialsJSON(
			option.CredentialsType("service_account"),
			[]byte(decryptedAccessSecret),
		))
		session := gcloudAuthSession{
			Opts:        opts,
			AccountCred: decryptedAccessSecret,
		}
		if err := common.UnmarshalJson([]byte(session.AccountCred), &session); err != nil {
			ctx.GetLogger().Error("Failed to unmarshal credentials for account %s: %v", account.AccountNumber, err)
			return gcloudAuthSession{}, fmt.Errorf("failed to unmarshal credentials: %w", err)
		}
		// For multi-project setups, account.AccountNumber is the target GCP project,
		// which may differ from the SA's home project in the credentials JSON.
		if account.AccountNumber != "" {
			session.ProjectId = account.AccountNumber
		}
		return session, nil
	}

	// Fallback path: read from GOOGLE_APPLICATION_CREDENTIALS if AccessSecret is not provided
	if credPath := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"); credPath != "" {
		content, err := os.ReadFile(credPath)
		if err != nil {
			ctx.GetLogger().Error("Failed to read GOOGLE_APPLICATION_CREDENTIALS file %s: %v", credPath, err)
			return gcloudAuthSession{}, fmt.Errorf("failed to read GOOGLE_APPLICATION_CREDENTIALS file: %w", err)
		}
		opts = append(opts, option.WithAuthCredentialsJSON(
			option.CredentialsType("service_account"),
			content,
		))
		session := gcloudAuthSession{
			Opts:        opts,
			AccountCred: string(content),
		}
		if err := common.UnmarshalJson(content, &session); err != nil {
			ctx.GetLogger().Error("Failed to unmarshal credentials from GOOGLE_APPLICATION_CREDENTIALS file %s: %v", credPath, err)
			return gcloudAuthSession{}, fmt.Errorf("failed to unmarshal credentials from GOOGLE_APPLICATION_CREDENTIALS: %w", err)
		}
		return session, nil
	}

	ctx.GetLogger().Error(fmt.Sprintf("Missing credentials for account %s: either AccessSecret must be set on account or GOOGLE_APPLICATION_CREDENTIALS must point to a service account key file", account.AccountNumber))

	return gcloudAuthSession{}, fmt.Errorf("missing credentials: either AccessSecret must be set on account or GOOGLE_APPLICATION_CREDENTIALS must point to a service account key file")
}

// GetInstanceNumericIdFromResourceId extracts instance information from a resource ID and fetches its numeric ID from GCP.
// This is useful for services that need to query logs or metrics using the numeric instance ID.
//
// Supported resource ID formats:
// - Network Interface: projects/{project}/zones/{zone}/instances/{instance}/networkInterfaces/{index}
// - Disk: projects/{project}/zones/{zone}/disks/{disk} (when attached to instance)
// - Any resource containing: projects/{project}/zones/{zone}/instances/{instance}/...
//
// Returns the numeric instance ID as a string, or empty string if not found or on error.
func GetInstanceNumericIdFromResourceId(ctx providers.CloudProviderContext, account providers.Account, resourceId string) string {
	projectId, zone, instanceName := parseInstanceInfoFromResourceId(resourceId)
	if projectId == "" || zone == "" || instanceName == "" {
		return ""
	}

	return getInstanceNumericId(ctx, account, projectId, zone, instanceName)
}

// parseInstanceInfoFromResourceId extracts project, zone, and instance name from various GCP resource ID formats.
// It handles multiple resource types that reference instances (NICs, disks, etc.)
func parseInstanceInfoFromResourceId(resourceId string) (projectId, zone, instanceName string) {
	if resourceId == "" {
		return "", "", ""
	}

	parts := strings.Split(resourceId, "/")

	for i, part := range parts {
		if part == "projects" && i+1 < len(parts) {
			projectId = parts[i+1]
		} else if part == "zones" && i+1 < len(parts) {
			zone = parts[i+1]
		} else if part == "instances" && i+1 < len(parts) {
			instanceName = parts[i+1]
			// Found instance name, we can stop here
			break
		}
	}

	return projectId, zone, instanceName
}

// getInstanceNumericId fetches the numeric instance ID from GCP Compute API.
// This makes a real-time API call to get the current instance details.
func getInstanceNumericId(ctx providers.CloudProviderContext, account providers.Account, projectId, zone, instanceName string) string {
	session, err := getGcloudSessionFromAccount(ctx, account)
	if err != nil {
		ctx.GetLogger().Warn("failed to get GCP session for instance ID lookup", "error", err)
		return ""
	}

	client, err := compute.NewInstancesRESTClient(ctx.GetContext(), session.Opts...)
	if err != nil {
		ctx.GetLogger().Warn("failed to create compute client for instance ID lookup", "error", err)
		return ""
	}
	defer func() {
		if cerr := client.Close(); cerr != nil {
			ctx.GetLogger().Error("failed to close compute client", "error", cerr)
		}
	}()

	// Get instance details
	req := &computepb.GetInstanceRequest{
		Project:  projectId,
		Zone:     zone,
		Instance: instanceName,
	}

	instance, err := client.Get(ctx.GetContext(), req)
	if err != nil {
		ctx.GetLogger().Warn("failed to get instance for numeric ID lookup",
			"error", err,
			"instance", instanceName,
			"zone", zone,
			"project", projectId)
		return ""
	}

	if instance.Id != nil {
		return fmt.Sprintf("%d", *instance.Id)
	}

	return ""
}
