package reports

import (
	"encoding/json"

	"nudgebee/services/common"
	"nudgebee/services/internal/database"

	"github.com/lib/pq"
)

// k8sInsightRow mirrors the SELECT projection used by fetchDailyK8sInsights.
// json.RawMessage is used for jsonb columns so the original JSON shape is
// preserved when re-marshalled into the notification payload.
type k8sInsightRow struct {
	Title        string          `json:"title" db:"title"`
	Type         string          `json:"type" db:"type"`
	UniqueId     string          `json:"unique_id" db:"unique_id"`
	Applications json.RawMessage `json:"applications" db:"applications"`
	AccountId    string          `json:"account_id" db:"account_id"`
}

// k8sAccountRow mirrors the cloud_accounts projection used by both
// fetchDailyK8sInsights and fetchK8sAccountList.
type k8sAccountRow struct {
	Id          string `json:"id" db:"id"`
	AccountName string `json:"account_name" db:"account_name"`
}

// agentStatusRow mirrors the agent projection used by fetchBatchedAgentStatus.
type agentStatusRow struct {
	CloudAccountId   string          `json:"cloud_account_id" db:"cloud_account_id"`
	Type             *string         `json:"type" db:"type"`
	Version          *string         `json:"version" db:"version"`
	StatusMessage    *string         `json:"status_message" db:"status_message"`
	Status           string          `json:"status" db:"status"`
	LastConnectedAt  *string         `json:"last_connected_at" db:"last_connected_at"`
	K8sVersion       *string         `json:"k8s_version" db:"k8s_version"`
	K8sProvider      *string         `json:"k8s_provider" db:"k8s_provider"`
	ConnectionStatus json.RawMessage `json:"connection_status" db:"connection_status"`
}

// toAnySlice round-trips a typed slice through JSON to produce a []interface{}
// of map[string]interface{} entries — matching the shape that downstream
// payload code (filtering, type assertions, MQ publish) expects from a Hasura
// GraphQL response.
func toAnySlice(in any) ([]interface{}, error) {
	b, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	var out []interface{}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// fetchDailyK8sInsights replaces the GetK8sInsights GraphQL query.
// Returns a GqlResponse-shaped result so existing payload handling (filtering,
// isPayloadEmpty, MQ publish) continues to work unchanged.
func fetchDailyK8sInsights(tenantId string) (common.GqlResponse, error) {
	dbm, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return common.GqlResponse{}, err
	}

	insights := []k8sInsightRow{}
	if err := dbm.Db.Select(&insights,
		`SELECT title, type, unique_id, applications, account_id
		 FROM insight
		 WHERE status = 'Open' AND tenant = $1`, tenantId); err != nil {
		return common.GqlResponse{}, err
	}

	accounts := []k8sAccountRow{}
	if err := dbm.Db.Select(&accounts,
		`SELECT id, account_name
		 FROM cloud_accounts
		 WHERE tenant = $1 AND cloud_provider = 'K8s' AND status = 'active'`, tenantId); err != nil {
		return common.GqlResponse{}, err
	}

	insightsAny, err := toAnySlice(insights)
	if err != nil {
		return common.GqlResponse{}, err
	}
	accountsAny, err := toAnySlice(accounts)
	if err != nil {
		return common.GqlResponse{}, err
	}

	return common.GqlResponse{
		Data: map[string]any{
			"insight":        insightsAny,
			"cloud_accounts": accountsAny,
		},
	}, nil
}

// fetchBatchedAgentStatus replaces the GetBatchedAgentHealth GraphQL query.
func fetchBatchedAgentStatus(accountIds []string, agentType string) (common.GqlResponse, error) {
	dbm, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return common.GqlResponse{}, err
	}

	agents := []agentStatusRow{}
	if err := dbm.Db.Select(&agents,
		`SELECT cloud_account_id,
		        type,
		        version,
		        status_message,
		        status,
		        last_connected_at::text AS last_connected_at,
		        k8s_version,
		        k8s_provider,
		        connection_status
		 FROM agent
		 WHERE cloud_account_id = ANY($1) AND type = $2`,
		pq.Array(accountIds), agentType); err != nil {
		return common.GqlResponse{}, err
	}

	agentsAny, err := toAnySlice(agents)
	if err != nil {
		return common.GqlResponse{}, err
	}

	return common.GqlResponse{
		Data: map[string]any{"agent": agentsAny},
	}, nil
}

// fetchK8sAccountList replaces the list_k8s_accounts GraphQL query.
func fetchK8sAccountList(tenantId string) (common.GqlResponse, error) {
	dbm, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return common.GqlResponse{}, err
	}

	accounts := []k8sAccountRow{}
	if err := dbm.Db.Select(&accounts,
		`SELECT id, account_name
		 FROM cloud_accounts
		 WHERE tenant = $1 AND cloud_provider = 'K8s'`, tenantId); err != nil {
		return common.GqlResponse{}, err
	}

	accountsAny, err := toAnySlice(accounts)
	if err != nil {
		return common.GqlResponse{}, err
	}

	return common.GqlResponse{
		Data: map[string]any{"accounts": accountsAny},
	}, nil
}
