package account

import (
	"database/sql"
	"fmt"
	"nudgebee/collector/cloud/common"
	"nudgebee/collector/cloud/providers"
	"nudgebee/collector/cloud/security"
	"strings"
	"time"

	"github.com/lib/pq"
	"golang.org/x/exp/maps"
)

func getAccount(ctx *security.RequestContext, accountId string) (providers.Account, string, error) {
	databaseManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		ctx.GetLogger().Error("unable to get dbms", "error", err)
		return providers.Account{}, "", err
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
			account_name,
			tenant,
			parent_account_id::text
		FROM cloud_accounts
		WHERE id = $1 AND status = 'active'
	`
	r, err := databaseManager.QueryRow(query, accountId)
	if err != nil {
		ctx.GetLogger().Error("unable to fetch account", "error", err)
		return providers.Account{}, "", err
	}

	var (
		assumeRole, accessKey, accessSecret, region, cloudProvider, accountNumber, accountName *string
		data                                                                                   sql.NullString
		accountTenant                                                                          string
		parentAccountId                                                                        sql.NullString
	)

	err = r.Scan(&assumeRole, &accessKey, &accessSecret, &region, &data, &cloudProvider, &accountNumber, &accountName, &accountTenant, &parentAccountId)
	if err != nil {
		if err == sql.ErrNoRows {
			return providers.Account{}, "", fmt.Errorf("account with id %s not found", accountId)
		}
		ctx.GetLogger().Error("unable to scan account", "error", err)
		return providers.Account{}, "", err
	}

	requestTenantId := ctx.GetSecurityContext().GetTenantId()
	if requestTenantId != "" && accountTenant != requestTenantId {
		ctx.GetLogger().Error("tenant mismatch: account belongs to different tenant",
			"accountId", accountId, "accountTenant", accountTenant, "requestTenant", requestTenantId)
		return providers.Account{}, "", fmt.Errorf("account %s does not belong to requesting tenant", accountId)
	}

	if accountNumber == nil {
		return providers.Account{}, "", fmt.Errorf("account number not found")
	}
	if cloudProvider == nil {
		return providers.Account{}, "", fmt.Errorf("cloud provider not found")
	}
	if assumeRole == nil && accessSecret == nil {
		return providers.Account{}, "", fmt.Errorf("credentials not found")
	}
	if accountName == nil {
		accountName = accountNumber
	}

	var dataValue *string
	if data.Valid {
		dataValue = &data.String
	}

	normalizedProvider := normalizeCloudProviderName(*cloudProvider)
	if normalizedProvider == "" {
		ctx.GetLogger().Error("invalid cloud provider type found in database", "provider", *cloudProvider, "accountId", accountId)
		return providers.Account{}, "", fmt.Errorf("invalid cloud provider type: %s", *cloudProvider)
	}
	var parentAccountIdValue *string
	if parentAccountId.Valid {
		parentAccountIdValue = &parentAccountId.String
	}

	acnt := providers.Account{
		ID:              accountId,
		AssumeRole:      assumeRole,
		AccessKey:       accessKey,
		AccessSecret:    accessSecret,
		Region:          region,
		Data:            dataValue,
		AccountNumber:   *accountNumber,
		AccountName:     *accountName,
		CloudProvider:   normalizedProvider,
		ParentAccountId: parentAccountIdValue,
	}

	ctx.GetLogger().Info("Fetched account", "accountId", accountId, "cloudProvider", *cloudProvider, "accountNumber", *accountNumber)

	return acnt, normalizedProvider, nil
}

func buildExternalResourceId(provider string, accountId, region, serviceName, resourceType string, resourceId string, resourceSubId string) string {
	return common.BuildExternalResourceId(provider, accountId, region, serviceName, resourceType, resourceId, resourceSubId)
}

// reconcileExternalResourceIds folds existing AWS/GCP rows onto the canonical
// external_resource_id before an upsert that arbiters on
// (account, external_resource_id).
//
// cloud_resourses carries two unique constraints — (account, external_resource_id)
// and the natural 5-column key (account, resourse_id, type, region, service_name).
// external_resource_id is a *lossy* normalization of the same fields (it
// lowercases, collapses whitespace, and strips the amazon/aws/gcp/google service
// prefix; see common.BuildExternalResourceId), so two rows that are distinct
// under the raw 5-column key can collapse to the same external_resource_id. An
// upsert can only name one arbiter; whichever it names, the *other* constraint
// can still be violated and Postgres then raises a hard duplicate-key error
// instead of doing DO UPDATE.
//
// We standardize on external_resource_id as the identity (it is the lookup key
// referenced by spends and events). This pre-step matches each batch resource to
// an existing row by the 5-column key (resourse_id case-insensitively, mirroring
// the Azure realtime path) and rewrites that row's external_resource_id to the
// canonical value, so the following INSERT ... ON CONFLICT (account,
// external_resource_id) updates that row rather than inserting a duplicate that
// would violate the unique index.
//
// The statement is collision-proof on both fronts so it can never raise a
// duplicate-key error of its own:
//   - NOT EXISTS: if the canonical external_resource_id is already held by some
//     other row, no candidate is touched (the upsert then updates that row).
//   - ROW_NUMBER() ... PARTITION BY erid: when several existing rows match the
//     same canonical id (e.g. resourse_id differing only in case, each carrying
//     a different legacy external_resource_id), only the most-recently-seen one
//     is rewritten — otherwise the single UPDATE would set two rows to the same
//     external_resource_id at once and violate the unique index.
func reconcileExternalResourceIds(tx common.DatabaseManagerTx, accountId string, resources []map[string]any) error {
	rids := make([]string, 0, len(resources))
	types := make([]string, 0, len(resources))
	regions := make([]string, 0, len(resources))
	services := make([]string, 0, len(resources))
	erids := make([]string, 0, len(resources))
	for _, r := range resources {
		rid, _ := r["resourse_id"].(string)
		erid, _ := r["external_resource_id"].(string)
		if rid == "" || erid == "" {
			continue
		}
		typ, _ := r["type"].(string)
		region, _ := r["region"].(string)
		svc, _ := r["service_name"].(string)
		rids = append(rids, rid)
		types = append(types, typ)
		regions = append(regions, region)
		services = append(services, svc)
		erids = append(erids, erid)
	}
	if len(erids) == 0 {
		return nil
	}
	const reconcileQuery = `
		WITH candidates AS (
			SELECT c.id, v.erid,
				ROW_NUMBER() OVER (
					PARTITION BY v.erid
					ORDER BY c.last_seen DESC NULLS LAST, c.created_at DESC NULLS LAST, c.id
				) AS rn
			FROM cloud_resourses c
			JOIN (
				SELECT * FROM unnest($2::text[], $3::text[], $4::text[], $5::text[], $6::text[])
					AS t(resourse_id, type, region, service_name, erid)
			) v
				ON c.account = $1
				AND LOWER(c.resourse_id) = LOWER(v.resourse_id)
				AND c.type = v.type
				AND c.region = v.region
				AND c.service_name = v.service_name
				AND c.external_resource_id IS DISTINCT FROM v.erid
			WHERE NOT EXISTS (
				SELECT 1 FROM cloud_resourses g
				WHERE g.account = $1 AND g.external_resource_id = v.erid
			)
		)
		UPDATE cloud_resourses c
		SET external_resource_id = candidates.erid
		FROM candidates
		WHERE c.id = candidates.id
			AND candidates.rn = 1`
	_, err := tx.Exec(reconcileQuery, accountId,
		pq.Array(rids), pq.Array(types), pq.Array(regions), pq.Array(services), pq.Array(erids))
	return err
}

func getUsageDataInternal(ctx *security.RequestContext, accountId string, month time.Month, year int) (providers.GetUsageReportResponse, providers.Account, error) {
	account, _, err := getAccount(ctx, accountId)
	if err != nil {
		ctx.GetLogger().Error("unable to fetch account", "error", err, "accountId", accountId)
		return providers.GetUsageReportResponse{}, providers.Account{}, err
	}
	// Use lowercase provider name for provider registry lookup.
	cloudProvider, ok := providers.GetProvider(strings.ToLower(account.CloudProvider))
	if !ok {
		return providers.GetUsageReportResponse{}, providers.Account{}, fmt.Errorf("provider not found")
	}
	report, err := cloudProvider.GetUsageReport(ctx, account, month, year)
	return report, account, err
}

// normalizeCloudProviderName normalizes provider names to match cloud_provider_type enum values
var cloudProviderNameMap = map[string]string{
	"aws":          "AWS",
	"azure":        "Azure",
	"gcp":          "GCP",
	"k8s":          "K8s",
	"cloudfoundry": "CloudFoundry",
}

func normalizeCloudProviderName(provider string) string {
	// Convert to lowercase for case-insensitive lookup
	lowerProvider := strings.ToLower(provider)
	if normalized, ok := cloudProviderNameMap[lowerProvider]; ok {
		return normalized
	}
	// Return empty string if provider not found
	return ""
}

func getAgentJobStatus(ctx *security.RequestContext, dbms *common.DatabaseManager, accountId string) (map[string]any, error) {
	query := `select connection_status from agent where cloud_account_id = $1 and status = 'CONNECTED' and connection_status is not null`
	connectionStatuses := []string{}
	err := dbms.QueryAndScan(&connectionStatuses, query, accountId)
	if err != nil {
		ctx.GetLogger().Error("unable to fetch agent status", "error", err)
		return map[string]any{}, err
	}
	data := map[string]any{}
	for _, connectionStatus := range connectionStatuses {
		if connectionStatus == "" {
			continue
		}
		var connectionStatusMap map[string]any
		err = common.UnmarshalJson([]byte(connectionStatus), &connectionStatusMap)
		if err != nil {
			ctx.GetLogger().Error("unable to parse connection status", "error", err)
			return map[string]any{}, err
		}
		data = connectionStatusMap
		break
	}
	return data, nil
}

func updateOrCreateAgentStatus(ctx *security.RequestContext, accountId string, status AgentStatus, statusMessage string, isSynced bool, conenctionStatus map[string]any) error {
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		ctx.GetLogger().Error("unable to get dbms", "error", err)
		return err
	}

	account, _, err1 := getAccount(ctx, accountId)
	if err1 != nil {
		ctx.GetLogger().Error("unable to account details", "error", err1)
		return err1
	}

	// Normalize provider name to match cloud_provider_type enum (AWS, Azure, GCP, K8s)
	normalizedProvider := normalizeCloudProviderName(account.CloudProvider)
	if normalizedProvider == "" {
		ctx.GetLogger().Error("invalid cloud provider type", "provider", account.CloudProvider)
		return fmt.Errorf("invalid cloud provider type: %s", account.CloudProvider)
	}

	connectionStatusStr := "{}"
	if len(conenctionStatus) > 0 {
		jsonBytes, err := common.MarshalJson(conenctionStatus)
		if err != nil {
			ctx.GetLogger().Error("unable to marshal connection status", "error", err)
			return err
		}
		connectionStatusStr = string(jsonBytes)
	}

	// Use normalized provider to match cloud_provider_type table (AWS, Azure, GCP)
	agentType := normalizedProvider
	timestamp := time.Now().Format(time.RFC3339)

	// Handle empty tenant_id - use nil instead of empty string to avoid UUID parsing errors
	tenantId := ctx.GetSecurityContext().GetTenantId()
	var tenantIdParam interface{}
	if tenantId == "" {
		tenantIdParam = nil
	} else {
		tenantIdParam = tenantId
	}

	if isSynced {
		query := `INSERT INTO agent (cloud_account_id, tenant, updated_at, type, status, last_connected_at, last_synced_at, access_secret, connection_status, status_message )
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (tenant, cloud_account_id, type) WHERE type != 'proxy'
			DO UPDATE SET updated_at = excluded.updated_at,
				last_connected_at = excluded.last_connected_at,
				last_synced_at = excluded.last_synced_at,
				connection_status = agent.connection_status || excluded.connection_status,
				status = CASE
					WHEN excluded.status = 'DISCONNECTED' THEN 'DISCONNECTED'
					WHEN jsonb_exists(excluded.connection_status::jsonb, 'spends') THEN excluded.status
					ELSE agent.status
				END,
				status_message = CASE
					WHEN excluded.status = 'DISCONNECTED' THEN excluded.status_message
					WHEN jsonb_exists(excluded.connection_status::jsonb, 'spends') THEN excluded.status_message
					ELSE agent.status_message
				END
		`
		_, err = dbms.Exec(query, accountId, tenantIdParam, timestamp, agentType, status, timestamp, timestamp, "dummy", connectionStatusStr, statusMessage)
	} else {
		query := `INSERT INTO agent (cloud_account_id, tenant, updated_at, type, status, last_connected_at, last_synced_at, access_secret, connection_status )
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (tenant, cloud_account_id, type) WHERE type != 'proxy'
			DO UPDATE SET updated_at = excluded.updated_at,
				last_connected_at = excluded.last_connected_at,
				connection_status = agent.connection_status || excluded.connection_status,
				status = CASE
					WHEN excluded.status = 'DISCONNECTED' THEN 'DISCONNECTED'
					WHEN jsonb_exists(excluded.connection_status::jsonb, 'spends') THEN excluded.status
					ELSE agent.status
				END
		`
		_, err = dbms.Exec(query, accountId, tenantIdParam, timestamp, agentType, status, timestamp, timestamp, "dummy", connectionStatusStr)
	}

	if err != nil {
		ctx.GetLogger().Error("unable to update agent status", "error", err)
		return err
	}
	return nil
}

func getExternalIdAndResourceIdMap(ctx *security.RequestContext, dbms *common.DatabaseManager, accountId string, externalIds []string) (map[string]string, error) {
	externalResourceIdMap := map[string]string{}
	for _, e := range externalIds {
		externalResourceIdMap[e] = ""
	}

	// Return early if no external IDs to look up
	if len(externalIds) == 0 {
		return externalResourceIdMap, nil
	}

	// Use ANY($2::text[]) with pq.Array to send the ID list as a single
	// parameter. The previous IN ($2) form was expanded by sqlx.In into one
	// positional parameter per ID, which exceeds PostgreSQL's 65535-parameter
	// limit for accounts with large monthly usage reports.
	query := `select external_resource_id::text, id::text from cloud_resourses where account = $1 and external_resource_id = ANY($2::text[])`
	resourceArnKeys := maps.Keys(externalResourceIdMap)
	queryResponse := []map[string]any{}
	err := dbms.QueryAndScan(&queryResponse, query, accountId, pq.Array(resourceArnKeys))
	if err != nil {
		ctx.GetLogger().Error("unable to fetch resources", "error", err)
		return map[string]string{}, err
	}
	for _, qr := range queryResponse {
		externalResourceIdMap[qr["external_resource_id"].(string)] = qr["id"].(string)
	}
	return externalResourceIdMap, nil
}

func getAllServices(ctx *security.RequestContext, accountId string) ([]string, error) {
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil, err
	}

	availableServices := []string{}
	query := `select distinct lower(service_name) from cloud_resourses where account = $1`
	err = dbms.QueryAndScan(&availableServices, query, accountId)
	if err != nil {
		ctx.GetLogger().Error("unable to fetch available services", "error", err, "account_id", accountId)
	}
	return availableServices, nil
}
