package account

import (
	"fmt"
	"nudgebee/collector/cloud/providers"
	"nudgebee/collector/cloud/security"
)

// ApplyCommand executes an inline action command on a cloud resource
func ApplyCommand(ctx *security.RequestContext, accountId string, request providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	// Fetch account details from database
	account, providerName, err := getAccount(ctx, accountId)
	if err != nil {
		ctx.GetLogger().Error("Failed to get account", "error", err, "accountId", accountId)
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("Failed to get account: %v", err),
		}, err
	}

	// Get the cloud provider implementation
	cloudProvider, ok := providers.GetProvider(providerName)
	if !ok {
		ctx.GetLogger().Error("Failed to get cloud provider", "provider", providerName)
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("Cloud provider %s not found", providerName),
		}, fmt.Errorf("provider not found: %s", providerName)
	}

	// Call the cloud provider's ApplyCommand method
	// Note: security.RequestContext implements CloudProviderContext interface
	resp, err := cloudProvider.ApplyCommand(ctx, account, request)
	if err != nil {
		ctx.GetLogger().Error("Failed to apply command",
			"error", err,
			"command", request.Command,
			"resourceId", request.ResourceId,
			"service", request.ServiceName)
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("Failed to apply command: %v", err),
		}, err
	}

	ctx.GetLogger().Info("Successfully applied command",
		"command", request.Command,
		"resourceId", request.ResourceId,
		"service", request.ServiceName,
		"success", resp.Success)

	return resp, nil
}
