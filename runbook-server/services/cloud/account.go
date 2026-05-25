package cloud

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"nudgebee/runbook/common"
	"nudgebee/runbook/services/security"
)

// ValidateAccount checks if an account with the given ID exists for the tenant.
func ValidateAccount(ctx *security.RequestContext, accountID string) error {
	if accountID == "" {
		return errors.New("account_id is required")
	}

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return err
	}

	var tenantID string
	err = dbms.QueryRowAndScan(&tenantID, "SELECT tenant FROM cloud_accounts WHERE id = $1", accountID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("account with id '%s' not found", accountID)
		}
		slog.Error("cloud: failed to validate account", "error", err, "account_id", accountID)
		return fmt.Errorf("failed to validate account: %w", err)
	}

	if tenantID != ctx.GetSecurityContext().GetTenantId() {
		return fmt.Errorf("account '%s' does not belong to tenant", accountID)
	}

	return nil
}

// GetAccountName returns the human-readable account name scoped to the tenant.
// Returns an empty string (no error) if accountID is empty.
func GetAccountName(tenantID, accountID string) (string, error) {
	if accountID == "" {
		return "", nil
	}

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return "", err
	}

	var name string
	err = dbms.QueryRowAndScan(&name, "SELECT account_name FROM cloud_accounts WHERE id = $1 AND tenant = $2", accountID, tenantID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("account with id '%s' not found for tenant", accountID)
		}
		return "", fmt.Errorf("failed to get account name: %w", err)
	}

	return name, nil
}

// GetAccountProvider returns the cloud provider of the account.
func GetAccountProvider(ctx *security.RequestContext, accountID string) (string, error) {
	if accountID == "" {
		return "", nil
	}

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return "", err
	}

	tenantID := ctx.GetSecurityContext().GetTenantId()

	var provider string
	err = dbms.QueryRowAndScan(&provider, "SELECT cloud_provider FROM cloud_accounts WHERE id = $1 AND tenant = $2", accountID, tenantID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("account with id '%s' not found for tenant", accountID)
		}
		return "", fmt.Errorf("failed to get account provider: %w", err)
	}

	return provider, nil
}
