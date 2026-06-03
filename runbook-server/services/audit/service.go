package audit

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"nudgebee/runbook/common"
	"nudgebee/runbook/config"
	"nudgebee/runbook/services/security"
)

func validateAuditRequest(auditRequest *AuditRequest) error {
	if auditRequest == nil {
		return common.ErrorBadRequest("audit: auditRequest is required")
	}

	if len(auditRequest.Audits) == 0 {
		return common.ErrorBadRequest("audit: audits is required")
	}
	for _, audit := range auditRequest.Audits {
		err := common.ValidateStruct(audit)
		if err != nil {
			return err
		}
	}

	// more validation based on different source categories

	return nil
}

func CreateAudit(context *security.RequestContext, auditRequest *AuditRequest) error {

	err := validateAuditRequest(auditRequest)
	if err != nil {
		context.GetLogger().Error("audit: validation failed", "error", err)
		return err
	}
	url := config.Config.ServiceEndpoint

	if url[len(url)-1] != '/' {
		url += "/"
	}
	url += "v1/audit"
	payloadBytes, err := common.MarshalJson(auditRequest)
	if err != nil {
		slog.Info("security: failed to marshal payload", "error", err)
		return err
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(payloadBytes))
	if err != nil {
		context.GetLogger().Error("audit: failed to create request", "error", err)
		return err
	}

	req.Header.Set("X-ACTION-TOKEN", config.Config.ServiceApiServerToken)
	req.Header.Set("Content-Type", "application/json")

	client := common.HttpClient()
	resp, err := client.Do(req)
	if err != nil {
		context.GetLogger().Error("audit: failed to send request", "error", err)
		return err
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			context.GetLogger().Error("audit: failed to close response body", "error", err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		context.GetLogger().Error("audit: received non-OK response", "status_code", resp.StatusCode)
		return errors.New("audit: unable to create audit")
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		context.GetLogger().Error("audit: failed to read response body", "error", err)
		return err
	}
	context.GetLogger().Info("audit: created audit", "response", string(body))

	return nil
}
