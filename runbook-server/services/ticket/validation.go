package ticket

import (
	"fmt"
	"nudgebee/runbook/common"
	"nudgebee/runbook/services/security"
)

// ValidateTicketConfig checks if a ticket configuration with the given ID exists for the tenant.
func ValidateTicketConfig(ctx *security.RequestContext, configID string) error {
	if configID == "" {
		return fmt.Errorf("config_id is required")
	}

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return err
	}

	var count int
	err = dbms.QueryRowAndScan(&count, "SELECT count(1) FROM integrations WHERE id = $1 AND tenant_id = $2", configID, ctx.GetSecurityContext().GetTenantId())
	if err != nil {
		return fmt.Errorf("failed to validate ticket config: %w", err)
	}

	if count == 0 {
		return fmt.Errorf("ticket configuration with id '%s' not found", configID)
	}

	return nil
}
