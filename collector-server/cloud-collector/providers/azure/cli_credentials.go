package azure

import (
	"nudgebee/collector/cloud/providers"
)

// CliCredentials returns an environment that authenticates the az CLI, the
// Azure SDKs and terraform as this account.
//
// The service principal's client secret is what Azure gives us, and it cannot
// be exchanged for something shorter-lived without federated credentials
// configured on the customer's side. ExpiresAt is therefore nil, and callers
// that hand these to customer-authored code are expected to say so.
func (a *azureProvider) CliCredentials(ctx providers.CloudProviderContext, account providers.Account, _ providers.CliCredentialsRequest) (providers.CliCredentials, error) {
	session, err := getAzureSessionFromAccount(ctx, account)
	if err != nil {
		return providers.CliCredentials{}, err
	}
	env := map[string]string{
		"AZURE_CLIENT_ID":       session.ClientID,
		"AZURE_CLIENT_SECRET":   session.ClientSecret,
		"AZURE_TENANT_ID":       session.TenantID,
		"AZURE_SUBSCRIPTION_ID": session.SubscriptionID,
		// terraform's azurerm provider reads the ARM_* spellings.
		"ARM_CLIENT_ID":       session.ClientID,
		"ARM_CLIENT_SECRET":   session.ClientSecret,
		"ARM_TENANT_ID":       session.TenantID,
		"ARM_SUBSCRIPTION_ID": session.SubscriptionID,
	}
	return providers.CliCredentials{Env: env}, nil
}
