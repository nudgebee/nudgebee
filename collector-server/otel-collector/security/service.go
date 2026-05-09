package security

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"nudgebee/collector/otel/common"
	"strings"
)

type Account struct {
	TenantId  string `json:"tenant_id"`
	AccountId string `json:"account_id"`
	AgentId   string `json:"agent_id"`
}

const cacheNamespace = "otel_collector_auth"

func init() {
	common.CacheCreateNamespace(cacheNamespace)
}

func GetAccountFromAgentToken(token string) (Account, error) {

	jsonData, ok := common.CacheGet(cacheNamespace, token)
	if ok {
		var account Account
		err := json.Unmarshal(jsonData, &account)
		if err == nil {
			return account, err
		}
	}

	//decode base64 token
	decodedToken, err := base64.StdEncoding.DecodeString(token)
	if err != nil {
		return Account{}, fmt.Errorf("auth: invalid token format: %w", err)
	}

	parts := strings.Split(string(decodedToken), ":")
	if len(parts) != 2 {
		return Account{}, fmt.Errorf("auth: invalid token structure")
	}

	// use access to lookup db
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return Account{}, err
	}

	rows, err := dbms.Query(`select id::text,tenant::text,cloud_account_id::text,access_secret_v2 from agent where type = $1 and access_key = $2`, "k8s", parts[0])
	if err != nil {
		return Account{}, err
	}
	defer func() { _ = rows.Close() }()

	// decode returned value
	var tenantId, accountId, agentId, accessSecretV2 string
	for rows.Next() {
		err := rows.Scan(&agentId, &tenantId, &accountId, &accessSecretV2)
		if err != nil {
			slog.Error("auth: unable to process row", "error", err)
			return Account{}, fmt.Errorf("auth: invalid auth token")
		}
	}

	if tenantId == "" || accountId == "" {
		return Account{}, fmt.Errorf("auth: invalid auth token")
	}

	if err := common.ValidateHashKey(parts[1], accessSecretV2); err != nil {
		return Account{}, fmt.Errorf("auth: invalid auth token")
	}

	account := Account{
		TenantId:  tenantId,
		AccountId: accountId,
		AgentId:   agentId,
	}

	accountBytes, err := json.Marshal(account)
	if err != nil {
		return Account{}, fmt.Errorf("auth: unable to marshal account")
	}

	err = common.CacheSet(cacheNamespace, token, accountBytes)
	if err != nil {
		slog.Error("auth: unable to cache account", "error", err)
	}
	return account, nil
}
