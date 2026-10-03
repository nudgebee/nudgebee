package account

import (
	"fmt"
	"time"

	"nudgebee/collector/cloud/providers"
	"nudgebee/collector/cloud/security"
)

// CliCredentials returns an environment that authenticates a process as the
// account, for callers that run something themselves instead of asking this
// service to run it — currently workflow script steps, which run
// customer-authored scripts that use the cloud SDKs directly rather than the
// CLI a shim could intercept.
//
// Credential resolution stays here. The alternative, every caller reading the
// account row and rebuilding assume-role chains, external ids, key decryption
// and token minting, is how three implementations of the same logic drift
// apart.
func CliCredentials(ctx *security.RequestContext, accountId string, duration time.Duration) (providers.CliCredentials, error) {
	account, providerName, err := getAccount(ctx, accountId)
	if err != nil {
		return providers.CliCredentials{}, err
	}
	provider, ok := providers.GetProvider(providerName)
	if !ok {
		return providers.CliCredentials{}, fmt.Errorf("provider not found")
	}
	issuer, ok := provider.(providers.CliCredentialProvider)
	if !ok {
		return providers.CliCredentials{}, fmt.Errorf("provider %s cannot supply credentials to a caller", providerName)
	}

	credentials, err := issuer.CliCredentials(ctx, account, providers.CliCredentialsRequest{Duration: duration})
	if err != nil {
		return providers.CliCredentials{}, err
	}

	// Log that credentials were issued, for which account and until when —
	// never what they are.
	expiry := "never"
	if credentials.ExpiresAt != nil {
		expiry = credentials.ExpiresAt.UTC().Format(time.RFC3339)
	}
	ctx.GetLogger().Info("issued cli credentials",
		"account_id", accountId,
		"provider", providerName,
		"expires_at", expiry,
		"variables", len(credentials.Env),
	)
	return credentials, nil
}
