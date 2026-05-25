package core

import (
	"fmt"
	"log/slog"
	"nudgebee/services/common"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"

	"github.com/jmoiron/sqlx"
)

type AgentCredentials struct {
	AccessKey    string
	AccessSecret string
}

// CreateProxyAgent creates a new proxy agent for the given account.
// Each vm_agent integration gets its own proxy agent with unique credentials.
func CreateProxyAgent(ctx *security.RequestContext, accountId string) (*AgentCredentials, error) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, fmt.Errorf("failed to get database manager: %w", err)
	}

	accessKey := common.GenerateUUID()
	accessSecret, err := common.GenerateRandomHexString(36)
	if err != nil {
		return nil, fmt.Errorf("failed to generate agent secret: %w", err)
	}

	hashedSecret, err := common.HashPassword(accessSecret)
	if err != nil {
		return nil, fmt.Errorf("failed to hash agent secret: %w", err)
	}

	tenantId := ctx.GetSecurityContext().GetTenantId()

	_, err = dbms.Db.Exec(`
		INSERT INTO agent (cloud_account_id, tenant, access_key, access_secret_v2, status, type)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		accountId, tenantId, accessKey, hashedSecret, "NOT_CONNECTED", "proxy",
	)
	if err != nil {
		slog.Error("integrations: failed to insert proxy agent",
			"account_id", accountId, "error", err)
		return nil, fmt.Errorf("failed to create proxy agent: %w", err)
	}

	return &AgentCredentials{
		AccessKey:    accessKey,
		AccessSecret: accessSecret,
	}, nil
}

// DeleteProxyAgent removes the agent record associated with the given access key.
func DeleteProxyAgent(tx *sqlx.Tx, accessKey string) error {
	if accessKey == "" {
		return nil
	}
	_, err := tx.Exec(`DELETE FROM agent WHERE access_key = $1`, accessKey)
	if err != nil {
		slog.Error("integrations: failed to delete proxy agent", "access_key", accessKey, "error", err)
		return fmt.Errorf("failed to delete proxy agent: %w", err)
	}
	return nil
}
