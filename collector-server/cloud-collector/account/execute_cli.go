package account

import (
	"fmt"
	"nudgebee/collector/cloud/providers"
	"nudgebee/collector/cloud/security"
)

func ExecuteCliCommand(ctx *security.RequestContext, accountId string, command string) (string, error) {
	account, provider, err := getAccount(ctx, accountId)
	if err != nil {
		ctx.GetLogger().Error("unable to fetch account", "error", err, "accountId", accountId)
		return "", err
	}
	cloudProvider, ok := providers.GetProvider(provider)
	if !ok {
		return "", fmt.Errorf("provider not found")
	}
	response, err := cloudProvider.ExecuteCliCommand(ctx, account, command)
	return response, err

}
