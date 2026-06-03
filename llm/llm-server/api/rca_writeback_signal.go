package api

import (
	"fmt"
	"net/http"

	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
)

// postRCAWritebackSignal best-effort notifies api-server that an RCA report has
// been saved for an event, so api-server can post it back onto the source
// incident (e.g. a ZenDuty note). Failures are logged and swallowed — the saved
// RCA is the source of truth and must never fail on a writeback error. Mirrors
// the llm-server -> api-server RPC pattern in tools.doKGActionRequest (same
// /rpc envelope, header set, and tenant resolution).
func postRCAWritebackSignal(ctx *security.RequestContext, eventId, accountId, rcaText string) {
	if eventId == "" || accountId == "" {
		return
	}

	tenant := ctx.GetSecurityContext().GetTenantId()
	if tenant == "" {
		t, err := security.GetTenantIdFromAccountId(accountId)
		if err != nil {
			ctx.GetLogger().Warn("rca_writeback signal: unable to resolve tenant", "error", err, "account_id", accountId)
			return
		}
		tenant = t
	}
	if tenant == "" {
		ctx.GetLogger().Warn("rca_writeback signal: empty tenant, skipping", "event_id", eventId)
		return
	}

	payload := map[string]any{
		"action": map[string]any{"name": "event_rca_writeback"},
		"input": map[string]any{
			"request": map[string]any{
				"event_id":   eventId,
				"account_id": accountId,
				"rca_text":   rcaText,
			},
		},
	}

	resp, err := common.HttpPost(
		fmt.Sprintf("%s/rpc/event", config.Config.ServiceEndpoint),
		common.HttpWithHeaders(map[string]string{
			"Content-Type":   "application/json",
			"Accept":         "application/json",
			"X-ACTION-TOKEN": config.Config.ServiceApiServerToken,
			"x-tenant-id":    tenant,
			"x-user-id":      ctx.GetSecurityContext().GetUserId(),
		}),
		common.HttpWithJsonBody(payload),
	)
	if err != nil {
		ctx.GetLogger().Warn("rca_writeback signal: post failed", "error", err, "event_id", eventId)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		ctx.GetLogger().Warn("rca_writeback signal: api-server returned non-200", "status", resp.StatusCode, "event_id", eventId)
		return
	}
	ctx.GetLogger().Info("rca_writeback signal: sent", "event_id", eventId)
}
