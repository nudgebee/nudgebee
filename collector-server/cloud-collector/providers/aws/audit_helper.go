package aws

import (
	"nudgebee/collector/cloud/audit"
	"nudgebee/collector/cloud/providers"
)

// logResourceActionAudit logs a resource action to the audit table
func logResourceActionAudit(
	ctx providers.CloudProviderContext,
	command providers.ApplyCommandRequest,
	account providers.Account,
	status string,
	errorMessage string,
) error {
	var auditStatus audit.EventStatus
	if status == "SUCCESS" {
		auditStatus = audit.EventStatusSuccess
	} else {
		auditStatus = audit.EventStatusFailure
	}

	return audit.LogResourceAction(ctx, command, account, auditStatus, errorMessage)
}

// logResourceActionAuditBatch logs resource actions for batch operations
func logResourceActionAuditBatch(
	ctx providers.CloudProviderContext,
	command providers.ApplyCommandRequest,
	account providers.Account,
	status string,
	errorMessage string,
	resourceIDs []string,
) error {
	var auditStatus audit.EventStatus
	if status == "SUCCESS" {
		auditStatus = audit.EventStatusSuccess
	} else {
		auditStatus = audit.EventStatusFailure
	}

	return audit.LogResourceActionBatch(ctx, command, account, auditStatus, errorMessage, resourceIDs)
}
