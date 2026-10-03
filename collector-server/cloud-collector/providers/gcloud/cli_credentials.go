package gcloud

import (
	"fmt"
	"time"

	"nudgebee/collector/cloud/providers"

	"cloud.google.com/go/auth/credentials"
)

// cloudPlatformScope is the scope gcloud itself requests, so a minted token can
// do whatever the service account is allowed to do.
const cloudPlatformScope = "https://www.googleapis.com/auth/cloud-platform"

// CliCredentials returns an environment that authenticates gcloud, the Google
// SDKs and terraform as this account.
//
// It mints a short-lived access token rather than handing over the service
// account key: the token expires on its own, and the key — which does not —
// stays in this service.
func (g *gcloudProvider) CliCredentials(ctx providers.CloudProviderContext, account providers.Account, _ providers.CliCredentialsRequest) (providers.CliCredentials, error) {
	session, err := getGcloudSessionFromAccount(ctx, account)
	if err != nil {
		return providers.CliCredentials{}, err
	}
	if session.AccountCred == "" {
		return providers.CliCredentials{}, fmt.Errorf("gcp: account has no service account credentials configured")
	}

	// Same constructor the monitoring client here uses. The older
	// google.CredentialsFromJSON is deprecated: it accepts any credential
	// configuration without validating it.
	creds, err := credentials.NewCredentialsFromJSON(credentials.ServiceAccount, []byte(session.AccountCred),
		&credentials.DetectOptions{Scopes: []string{cloudPlatformScope}})
	if err != nil {
		return providers.CliCredentials{}, fmt.Errorf("gcp: failed to read service account credentials: %w", err)
	}
	token, err := creds.Token(ctx.GetContext())
	if err != nil {
		return providers.CliCredentials{}, fmt.Errorf("gcp: failed to mint access token: %w", err)
	}

	projectID := session.ProjectId
	if projectID == "" {
		projectID = account.AccountNumber
	}
	env := map[string]string{
		// gcloud reads the first; the terraform provider and the Go/Python
		// SDKs read the second. Both are the same token.
		"CLOUDSDK_AUTH_ACCESS_TOKEN": token.Value,
		"GOOGLE_OAUTH_ACCESS_TOKEN":  token.Value,
		"CLOUDSDK_CORE_PROJECT":      projectID,
		"GOOGLE_CLOUD_PROJECT":       projectID,
		"GOOGLE_PROJECT":             projectID,
	}

	var expiresAt *time.Time
	if !token.Expiry.IsZero() {
		expiry := token.Expiry
		expiresAt = &expiry
	}
	return providers.CliCredentials{Env: env, ExpiresAt: expiresAt}, nil
}
