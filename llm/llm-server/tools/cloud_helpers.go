package tools

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"nudgebee/llm/common"
	"nudgebee/llm/tools/core"
)

var (
	ErrAccountNumberNotFound = errors.New("account number not found")
	ErrCloudProviderNotFound = errors.New("cloud provider not found")
)

type CloudAccountCredentials struct {
	ID            string
	AssumeRole    *string
	AccessKey     *string
	AccessSecret  *string
	Region        *string
	Data          *string
	AccountNumber string
	AccountName   string
	CloudProvider string
}

func GetCloudAccountCredentials(accountId string) (CloudAccountCredentials, error) {
	databaseManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return CloudAccountCredentials{}, err
	}

	query := `
		SELECT
			assume_role,
			access_key,
			access_secret,
			region,
			data::varchar,
			cloud_provider,
			account_number,
			account_name
		FROM cloud_accounts
		WHERE id = $1
	`
	r, err := databaseManager.QueryRow(query, accountId)
	if err != nil {
		return CloudAccountCredentials{}, err
	}

	var (
		assumeRole, accessKey, accessSecret, region, cloudProvider, accountNumber, accountName *string
		data                                                                                   sql.NullString
	)

	err = r.Scan(&assumeRole, &accessKey, &accessSecret, &region, &data, &cloudProvider, &accountNumber, &accountName)
	if err != nil {
		if err == sql.ErrNoRows {
			return CloudAccountCredentials{}, fmt.Errorf("account with id %s not found", accountId)
		}
		return CloudAccountCredentials{}, err
	}

	if accountNumber == nil {
		return CloudAccountCredentials{}, ErrAccountNumberNotFound
	}
	if cloudProvider == nil {
		return CloudAccountCredentials{}, ErrCloudProviderNotFound
	}

	if accountName == nil {
		accountName = accountNumber
	}

	var dataValue *string
	if data.Valid {
		dataValue = &data.String
	}

	creds := CloudAccountCredentials{
		ID:            accountId,
		AssumeRole:    assumeRole,
		AccessKey:     accessKey,
		AccessSecret:  accessSecret,
		Region:        region,
		Data:          dataValue,
		AccountNumber: *accountNumber,
		AccountName:   *accountName,
		CloudProvider: *cloudProvider,
	}

	// Attempt to decrypt AccessSecret
	if creds.AccessSecret != nil {
		decrypted, err := common.Decrypt(*creds.AccessSecret)
		if err != nil {
			return CloudAccountCredentials{}, fmt.Errorf("failed to decrypt access secret: %w", err)
		}
		creds.AccessSecret = &decrypted
	}

	return creds, nil
}

// extractCommandFromToolInput extracts the "command" field from a JSON tool input string.
// Tool inputs arrive as JSON (e.g. {"command":"az vm list --help"}) but the verb-classification
// heuristics expect a plain command string. Returns the input unchanged if it's not valid JSON
// or doesn't contain a "command" string field.
func extractCommandFromToolInput(input string) string {
	var parsed struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal([]byte(input), &parsed); err == nil && parsed.Command != "" {
		return parsed.Command
	}
	return input
}

// isCloudCLIInfoFlag checks if the command parts contain flags that only
// display help, usage, or version information (always read-only operations).
func isCloudCLIInfoFlag(parts []string) core.ToolRequestType {
	for _, p := range parts {
		if p == "--help" || p == "-h" || p == "--version" {
			return core.ToolRequestTypeRead
		}
	}
	return ""
}
