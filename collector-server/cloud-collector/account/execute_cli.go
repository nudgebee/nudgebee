package account

import (
	"fmt"
	"nudgebee/collector/cloud/providers"
	"nudgebee/collector/cloud/security"
	"time"
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

	// Every CLI execution is a fresh interpreter process (plus a re-auth on GCP
	// and Azure), so this route dominates cloud-collector CPU. Log the verb — not
	// the command, which carries ARNs and secrets — so the per-command split is
	// measurable. Both /execute_cli and /execute_cli_batch land here; only the
	// batch path writes an audit record, so this is the sole record of the
	// single-command traffic.
	start := time.Now()
	response, err := cloudProvider.ExecuteCliCommand(ctx, account, command)
	status := "ok"
	if err != nil {
		status = "error"
	}
	ctx.GetLogger().Info("execute_cli command completed",
		"verb", CommandVerb(command),
		"provider", provider,
		"accountId", accountId,
		"status", status,
		"duration_ms", time.Since(start).Milliseconds())

	return response, err
}
