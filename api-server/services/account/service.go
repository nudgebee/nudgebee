package account

import (
	"bytes"
	ctx "context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"nudgebee/services/audit"
	"nudgebee/services/cloud"
	"nudgebee/services/common"
	"nudgebee/services/config"
	"nudgebee/services/internal/database"
	"nudgebee/services/internal/database/models"
	"nudgebee/services/license"
	"nudgebee/services/security"
	tenantpkg "nudgebee/services/tenant"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/subscription/armsubscription"
)

const (
	// azureCostManagementWarningMsg is the message shown when Azure credentials lack Cost Management API access
	azureCostManagementWarningMsg = "Azure account created successfully, but the credentials do not have permission to access the Azure Cost Management API. Please grant the 'Cost Management Reader' role or equivalent to enable cost tracking. You can update the permissions in the Azure Portal and the system will automatically detect the change on the next sync."
)

type GcpOnboardRequest struct {
	AccountName    string `json:"account_name"`
	GcpProjectId   string `json:"gcp_project_id"`
	GcpCredentials string `json:"gcp_credentials"`
}

type GcpOnboardResponse struct {
	Id string `json:"id"`
}

func GcpOnboard(context *security.RequestContext, query GcpOnboardRequest) (GcpOnboardResponse, error) {
	err := common.ValidateStruct(query)
	if err != nil {
		return GcpOnboardResponse{}, err
	}

	createdBy := context.GetSecurityContext().GetUserId()
	tenant := context.GetSecurityContext().GetTenantId()
	if createdBy == "" && tenant == "" && !context.GetSecurityContext().IsSuperAdmin() {
		return GcpOnboardResponse{}, fmt.Errorf("account: unauthorized")
	}

	// Validate GCP credentials
	var gcpCredentials struct {
		ProjectID string `json:"project_id"`
	}
	err = json.Unmarshal([]byte(query.GcpCredentials), &gcpCredentials)
	if err != nil {
		return GcpOnboardResponse{}, fmt.Errorf("failed to parse GCP credentials JSON: %w", err)
	}
	if gcpCredentials.ProjectID == "" {
		return GcpOnboardResponse{}, fmt.Errorf("invalid GCP credentials, 'project_id' field is missing")
	}

	// Validate credentials by calling collector-server
	validationResult := validateGCPCredentialsInternal(context.GetContext(), query.GcpCredentials, query.GcpProjectId, "", "", "")
	if !validationResult.Success {
		return GcpOnboardResponse{}, fmt.Errorf("invalid GCP credentials: %s", validationResult.ErrorMessage)
	}

	// Encrypt and save the service account key
	encryptedServiceAccountKey, err := common.Encrypt(query.GcpCredentials)
	if err != nil {
		return GcpOnboardResponse{}, fmt.Errorf("failed to encrypt service account key: %w", err)
	}

	// Create the account in the database
	createAccountRequest := AccountCreateRequest{
		AccountName:   query.AccountName,
		CloudProvider: "GCP",
		AccountType:   "cloud",
		Tenant:        tenant,
		CreatedBy:     createdBy,
		UpdatedBy:     createdBy,
		AccessSecret:  encryptedServiceAccountKey,
		AccountNumber: gcpCredentials.ProjectID,
	}

	createAccountResponse, err := CreateAccount(context, createAccountRequest)
	if err != nil {
		return GcpOnboardResponse{}, fmt.Errorf("failed to create account in database: %w", err)
	}

	return GcpOnboardResponse{Id: createAccountResponse.Id}, nil
}

// ListGCPProjects discovers all accessible GCP projects using provided service account credentials
func ListGCPProjects(context *security.RequestContext, query GcpListProjectsRequest) (GcpListProjectsResponse, error) {
	err := common.ValidateStruct(query)
	if err != nil {
		return GcpListProjectsResponse{}, err
	}

	projects, err := listGCPProjectsInternal(context.GetContext(), query.CredentialsJSON)
	if err != nil {
		return GcpListProjectsResponse{}, fmt.Errorf("failed to list GCP projects: %w", err)
	}

	return GcpListProjectsResponse{Projects: projects}, nil
}

// GcpBulkOnboard creates multiple cloud_accounts rows for a list of GCP project IDs,
// sharing the same service account credentials and optional billing configuration.
func GcpBulkOnboard(context *security.RequestContext, query GcpBulkOnboardRequest) (GcpBulkOnboardResponse, error) {
	err := common.ValidateStruct(query)
	if err != nil {
		return GcpBulkOnboardResponse{}, err
	}

	if len(query.ProjectIDs) == 0 {
		return GcpBulkOnboardResponse{}, fmt.Errorf("at least one project ID is required")
	}

	createdBy := context.GetSecurityContext().GetUserId()
	tenant := context.GetSecurityContext().GetTenantId()

	// Parse and validate credentials once
	var gcpCredentials struct {
		ProjectID string `json:"project_id"`
	}
	if err := json.Unmarshal([]byte(query.CredentialsJSON), &gcpCredentials); err != nil {
		return GcpBulkOnboardResponse{}, fmt.Errorf("failed to parse GCP credentials JSON: %w", err)
	}

	// Validate credentials once via collector using the SA's home project
	validationResult := validateGCPCredentialsInternal(context.GetContext(), query.CredentialsJSON, gcpCredentials.ProjectID, "", "", "")
	if !validationResult.Success {
		return GcpBulkOnboardResponse{}, fmt.Errorf("invalid GCP credentials: %s", validationResult.ErrorMessage)
	}

	// Build billing data map if provided
	var billingData map[string]any
	if query.BillingDatasetID != "" && query.BillingTableID != "" {
		billingData = map[string]any{
			"billing_project_id": query.BillingProjectID,
			"dataset_name":       query.BillingDatasetID,
			"table_name":         query.BillingTableID,
		}
		if billingData["billing_project_id"] == "" {
			billingData["billing_project_id"] = gcpCredentials.ProjectID
		}
	}

	response := GcpBulkOnboardResponse{
		Accounts: make([]BulkOnboardAccountResult, 0, len(query.ProjectIDs)),
	}

	// The SA's home project becomes the parent account. If it's not in the
	// selected list, use the first selected project as parent instead.
	parentProjectID := ""
	for _, pid := range query.ProjectIDs {
		pid = strings.TrimSpace(pid)
		if pid == "" {
			continue
		}
		if pid == gcpCredentials.ProjectID {
			parentProjectID = pid
			break
		}
		if parentProjectID == "" {
			parentProjectID = pid
		}
	}
	if parentProjectID == "" {
		return GcpBulkOnboardResponse{}, fmt.Errorf("no valid project IDs provided")
	}

	parentAccountName := query.AccountName
	if len(query.ProjectIDs) > 1 {
		parentAccountName = fmt.Sprintf("%s - %s", query.AccountName, parentProjectID)
	}

	parentData := map[string]any{"project_id": parentProjectID}
	if billingData != nil {
		parentData["billing_data"] = billingData
	}

	parentResp, err := CreateAccount(context, AccountCreateRequest{
		AccountName:   parentAccountName,
		CloudProvider: "GCP",
		AccountType:   "cloud",
		Tenant:        tenant,
		CreatedBy:     createdBy,
		UpdatedBy:     createdBy,
		AccessSecret:  query.CredentialsJSON,
		AccountNumber: parentProjectID,
		Data:          parentData,
	})
	if err != nil {
		return GcpBulkOnboardResponse{}, fmt.Errorf("failed to create parent account for project %s: %w", parentProjectID, err)
	}

	parentID := parentResp.Id
	response.ParentID = parentID
	response.Accounts = append(response.Accounts, BulkOnboardAccountResult{
		ProjectID: parentProjectID,
		AccountID: parentResp.Id,
		Status:    "created",
	})

	// Create remaining projects as children
	for _, projectID := range query.ProjectIDs {
		projectID = strings.TrimSpace(projectID)
		if projectID == "" || projectID == parentProjectID {
			continue
		}

		result := BulkOnboardAccountResult{ProjectID: projectID}

		data := map[string]any{"project_id": projectID}
		if billingData != nil {
			data["billing_data"] = billingData
		}

		accountName := fmt.Sprintf("%s - %s", query.AccountName, projectID)

		createResp, err := CreateAccount(context, AccountCreateRequest{
			AccountName:     accountName,
			CloudProvider:   "GCP",
			AccountType:     "cloud",
			Tenant:          tenant,
			CreatedBy:       createdBy,
			UpdatedBy:       createdBy,
			AccessSecret:    query.CredentialsJSON,
			AccountNumber:   projectID,
			Data:            data,
			ParentAccountId: parentID,
		})
		if err != nil {
			result.Status = "error"
			result.Error = err.Error()
			response.Accounts = append(response.Accounts, result)
			continue
		}

		result.AccountID = createResp.Id
		result.Status = "created"
		response.Accounts = append(response.Accounts, result)
	}

	return response, nil
}

type tableDetails struct {
	name              string
	accountColumnName string
	whereSubclause    string
}

func deleteTables(context *security.RequestContext, tx *sql.Tx, tables []tableDetails, accountId string, defaultAccountIdColumn string) error {
	if defaultAccountIdColumn == "" {
		defaultAccountIdColumn = "cloud_account_id"
	}

	var err error
	for _, table := range tables {
		context.GetLogger().Info("Deleting table", "table", table.name, "account_id", accountId)
		if table.accountColumnName == "" {
			table.accountColumnName = defaultAccountIdColumn
		}
		if table.whereSubclause != "" {
			_, err = tx.Exec(fmt.Sprintf("DELETE FROM %s WHERE %s", table.name, table.whereSubclause), accountId)
		} else {
			_, err = tx.Exec(fmt.Sprintf("DELETE FROM %s WHERE %s = $1", table.name, table.accountColumnName), accountId)
		}

		if err != nil {
			context.GetLogger().Error("Error deleting", "table", table.name, "error", err)
			err1 := tx.Rollback()
			if err1 != nil {
				context.GetLogger().Error("Error rolling back transaction", "error", err1)
			}
			return err
		}
	}
	return nil
}

func DeleteAccount(context *security.RequestContext, query AccountDeleteRequest) (AccountDeleteResponse, error) {
	err := common.ValidateStruct(query)
	if err != nil {
		return AccountDeleteResponse{}, err
	}

	createdBy := context.GetSecurityContext().GetUserId()
	tenant := context.GetSecurityContext().GetTenantId()
	if createdBy == "" && tenant == "" && !context.GetSecurityContext().IsSuperAdmin() {
		return AccountDeleteResponse{}, fmt.Errorf("Unauthorized")
	}

	databaseManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		context.GetLogger().Error("Unable to create databasebase manager", "error", err)
		return AccountDeleteResponse{}, err
	}

	context.GetLogger().Info("deleting account", "account_id", query.Id)

	var accountDetailRow *sqlx.Row
	if tenant != "" {
		accountDetailRow = databaseManager.Db.QueryRowx("SELECT * FROM cloud_accounts WHERE id = $1 AND tenant = $2", query.Id, tenant)
	} else {
		accountDetailRow = databaseManager.Db.QueryRowx("SELECT * FROM cloud_accounts WHERE id = $1", query.Id)
	}

	if accountDetailRow.Err() != nil {
		return AccountDeleteResponse{}, fmt.Errorf("invalid Account Id - %s", query.Id)
	}

	accountDetail := map[string]any{}
	err = accountDetailRow.MapScan(accountDetail)
	if err != nil {
		return AccountDeleteResponse{}, err
	}

	removeAccount := !query.OnlyClean

	// remove all in single transaction
	tx, err := databaseManager.Db.Begin()
	if err != nil {
		return AccountDeleteResponse{}, err
	}
	// projects tables
	projectTables := []tableDetails{{name: "project_accounts", accountColumnName: "account_id"}}
	err = deleteTables(context, tx, projectTables, query.Id, "cloud_account_id")
	if err != nil {
		return AccountDeleteResponse{}, err
	}

	// metrices & spends
	metricsSpendsTables := []tableDetails{{name: "spends", accountColumnName: "cloud_account"}, {name: "cloud_resource_metrics", whereSubclause: "cloud_resource_id in (select id from cloud_resourses where account = $1)"}}
	err = deleteTables(context, tx, metricsSpendsTables, query.Id, "cloud_account_id")
	if err != nil {
		return AccountDeleteResponse{}, err
	}
	// one off scenario necaise pf data population issue
	_, err = tx.Exec("DELETE FROM spends WHERE cloud_resource_id in (select id from cloud_resourses WHERE account = $1)", query.Id)
	if err != nil {
		context.GetLogger().Error("Error deleting spends > resources", "error", err)
		err = tx.Rollback()
		if err != nil {
			context.GetLogger().Error("Error rolling back transaction", "error", err)
		}
		return AccountDeleteResponse{}, err
	}

	// recommendations
	recommendationsTables := []tableDetails{{name: "recommendation_resolution", whereSubclause: "recommendation_id in (select id from recommendation where cloud_account_id = $1)"}, {name: "recommendation"}}
	err = deleteTables(context, tx, recommendationsTables, query.Id, "cloud_account_id")
	if err != nil {
		return AccountDeleteResponse{}, err
	}

	// dw tables
	dwTables := []tableDetails{{name: "dw_pipe_usage"}, {name: "dw_pipe"}, {name: "dw_queries", accountColumnName: "account_id"}, {name: "dw_query_profile_data"}, {name: "dw_tables"}}
	err = deleteTables(context, tx, dwTables, query.Id, "cloud_account_id")
	if err != nil {
		return AccountDeleteResponse{}, err
	}

	// agent related tables
	agentTables := []tableDetails{{name: "agent_task"}, {name: "agent_playbook"}, {name: "agent"}}
	err = deleteTables(context, tx, agentTables, query.Id, "cloud_account_id")
	if err != nil {
		return AccountDeleteResponse{}, err
	}

	// auto_pilot
	// auto_pilot_task with join to auto_pilot by auto_pilot_id and id and filter by account id
	context.GetLogger().Info("Deleting auto_pilot_task ", "account_id", query.Id)
	_, err = tx.Exec("DELETE FROM auto_pilot_task WHERE auto_pilot_id in (select id from auto_pilot WHERE account_id = $1)", query.Id)
	if err != nil {
		context.GetLogger().Error("Error deleting auto_pilot_task", "error", err)
		err = tx.Rollback()
		if err != nil {
			context.GetLogger().Error("Error rolling back transaction", "error", err)
		}
		return AccountDeleteResponse{}, err
	}

	autopilotTables := []tableDetails{{name: "auto_pilot_reviewee"}, {name: "auto_pilot_reviewers"}, {name: "auto_pilot_approvals"}, {name: "auto_pilot_approval_policy"}, {name: "auto_playbook_task"}, {name: "auto_playbook_executions"}, {name: "auto_optimize_resource_map"}, {name: "auto_pilot"}, {name: "auto_playbook"}, {name: "runbook_task_output"}}
	err = deleteTables(context, tx, autopilotTables, query.Id, "account_id")
	if err != nil {
		return AccountDeleteResponse{}, err
	}

	// notifications
	notificationTables := []tableDetails{{name: "notification_channel_account_mappings", accountColumnName: "account_id"}, {name: "notification_rules"}}
	err = deleteTables(context, tx, notificationTables, query.Id, "account_id")
	if err != nil {
		return AccountDeleteResponse{}, err
	}

	//	tickets
	ticketTables := []tableDetails{{name: "tickets"}}
	err = deleteTables(context, tx, ticketTables, query.Id, "account_id")
	if err != nil {
		return AccountDeleteResponse{}, err
	}

	// user_history
	context.GetLogger().Info("Deleting user_history ", "account_id", query.Id)
	_, err = tx.Exec("DELETE FROM user_history WHERE account_id = $1", query.Id)
	if err != nil {
		context.GetLogger().Error("Error deleting user_history", "error", err)
		err = tx.Rollback()
		if err != nil {
			context.GetLogger().Error("Error rolling back transaction", "error", err)
		}
		return AccountDeleteResponse{}, err
	}

	context.GetLogger().Info("Deleting cloud resources attributes ", "account_id", query.Id)
	_, err = tx.Exec("DELETE FROM cloud_resource_attributes WHERE resource_id in (select id from cloud_resourses WHERE account = $1)", query.Id)
	if err != nil {
		context.GetLogger().Error("Error deleting cloud_resource_attributes", "error", err)
		err = tx.Rollback()
		if err != nil {
			context.GetLogger().Error("Error rolling back transaction", "error", err)
		}
		return AccountDeleteResponse{}, err
	}
	// resources tables
	resourcesTables := []tableDetails{{name: "application_group_mapping", accountColumnName: "account_id"}, {name: "application_profile"}, {name: "k8s_namespaces"}, {name: "k8s_pods"}, {name: "k8s_workloads"}, {name: "k8s_nodes"}, {name: "cloud_resourses", accountColumnName: "account"}}
	err = deleteTables(context, tx, resourcesTables, query.Id, "cloud_account_id")
	if err != nil {
		return AccountDeleteResponse{}, err
	}

	// insight tables
	insightTables := []tableDetails{{name: "insight"}}
	err = deleteTables(context, tx, insightTables, query.Id, "account_id")
	if err != nil {
		return AccountDeleteResponse{}, err
	}

	// llm related tables
	llmTables := []tableDetails{{name: "llm_rag_audit", accountColumnName: "cloud_account_id"}, {name: "llm_rags"}, {name: "llm_conversation_history"}, {name: "llm_conversation_saved", whereSubclause: "conversation_id in (select id from llm_conversations where account_id = $1)"}, {name: "llm_functions"}, {name: "llm_tools_installation"}, {name: "llm_agents_installation"}, {name: "llm_conversation_feedback", accountColumnName: "cloud_account_id"}, {name: "llm_conversation_tool_calls"}, {name: "llm_conversation_agent"}, {name: "llm_conversation_messages"}, {name: "llm_conversations"}}
	err = deleteTables(context, tx, llmTables, query.Id, "account_id")
	if err != nil {
		return AccountDeleteResponse{}, err
	}

	// anomaly/slo related tables
	anomalySloTables := []tableDetails{{name: "anomaly_config"}, {name: "anomaly", accountColumnName: "account_id"}, {name: "slo_report"}, {name: "slo_config"}}
	err = deleteTables(context, tx, anomalySloTables, query.Id, "cloud_account_id")
	if err != nil {
		return AccountDeleteResponse{}, err
	}

	// event related tables
	eventTables := []tableDetails{{name: "event_resolution", whereSubclause: "event_id in (select id from events where cloud_account_id = $1)"}, {name: "event_duplicates"}, {name: "event_rules", accountColumnName: "account_id"}, {name: "event_log_analysis"}, {name: "events"}}
	err = deleteTables(context, tx, eventTables, query.Id, "cloud_account_id")
	if err != nil {
		return AccountDeleteResponse{}, err
	}

	// integration related tables
	integrationTables := []tableDetails{{name: "integrations_cloud_accounts"}}
	err = deleteTables(context, tx, integrationTables, query.Id, "cloud_account_id")
	if err != nil {
		return AccountDeleteResponse{}, err
	}

	// additional tables
	additionalAccountTables := []tableDetails{{name: "cloud_account_score"}, {name: "billing_usage_cost", accountColumnName: "account_id"}, {name: "cloud_api_permission_errors"}, {name: "feature_flag", accountColumnName: "account_id"}, {name: "upgrade_plan_steps", accountColumnName: "account_id"}, {name: "upgrade_plan_audit", accountColumnName: "account_id"}}
	err = deleteTables(context, tx, additionalAccountTables, query.Id, "cloud_account_id")
	if err != nil {
		return AccountDeleteResponse{}, err
	}

	if removeAccount {
		// account attrs
		context.GetLogger().Info("Deleting Account Attrs", "account_id", query.Id)
		_, err = tx.Exec("DELETE FROM cloud_account_attrs WHERE cloud_account_id = $1", query.Id)
		if err != nil {
			context.GetLogger().Error("Error deleting cloud_account_attrs", "error", err)
			err = tx.Rollback()
			if err != nil {
				context.GetLogger().Error("Error rolling back transaction", "error", err)
			}
			return AccountDeleteResponse{}, err
		}

		// accounts
		context.GetLogger().Info("Deleting Account ", "account_id", query.Id)
		_, err = tx.Exec("DELETE FROM cloud_accounts WHERE id = $1", query.Id)
		if err != nil {
			context.GetLogger().Error("Error deleting cloud_accounts", "error", err)
			err1 := tx.Rollback()
			if err1 != nil {
				context.GetLogger().Error("Error rolling back transaction", "error", err1)
			}
			return AccountDeleteResponse{}, err
		}
	}

	err = tx.Commit()
	if err != nil {
		return AccountDeleteResponse{}, err
	}

	audit.LogChange(context, audit.ChangeInput{
		EventCategory: audit.EventCategoryAccount,
		EventType:     audit.EventTypeAccountDelete,
		EventAction:   audit.EventActionDelete,
		TargetID:      query.Id,
		AccountID:     query.Id,
		TableName:     "cloud_accounts",
		OldData:       accountDetail,
	})

	if err := audit.PublishAuditEvent(context, audit.Audit{
		AccountId:     query.Id,
		TenantId:      context.GetSecurityContext().GetTenantId(),
		UserId:        context.GetSecurityContext().GetUserId(),
		EventTime:     time.Now(),
		EventCategory: audit.EventCategoryAccount,
		EventType:     audit.EventTypeAccountDelete,
		EventState:    accountDetail,
		EventActor:    audit.EventActorApiService,
		EventTarget:   "account",
		EventAction:   audit.EventActionDelete,
		EventStatus:   audit.EventStatusSuccess,
	}); err != nil {
		context.GetLogger().Error("failed to publish audit event", "error", err)
	}

	return AccountDeleteResponse{
		Id: query.Id,
	}, nil

}

// insertRowFromMap inserts a row into the given table using a map of column->value pairs.
// It dynamically builds the INSERT query and returns the generated id.
func insertRowFromMap(dbms *database.DatabaseManager, table string, data map[string]any) (string, error) {
	cols := make([]string, 0, len(data))
	placeholders := make([]string, 0, len(data))
	args := make([]any, 0, len(data))
	i := 1
	for col, val := range data {
		cols = append(cols, `"`+col+`"`)
		placeholders = append(placeholders, fmt.Sprintf("$%d", i))
		// Marshal map/slice values to JSON for JSONB columns
		switch v := val.(type) {
		case map[string]any, []any:
			jsonBytes, err := json.Marshal(v)
			if err != nil {
				return "", fmt.Errorf("failed to marshal %s: %w", col, err)
			}
			args = append(args, string(jsonBytes))
		default:
			args = append(args, val)
		}
		i++
	}

	query := fmt.Sprintf(`INSERT INTO %s (%s) VALUES (%s) RETURNING id`,
		table, strings.Join(cols, ", "), strings.Join(placeholders, ", "))

	var id string
	err := dbms.Db.QueryRowx(query, args...).Scan(&id)
	if err != nil {
		return "", err
	}
	return id, nil
}

// structToInsertMap converts a struct to a map using JSON marshaling (matching RPC behavior).
// Fields with omitempty that are zero-valued are excluded.
func structToInsertMap(obj any) (map[string]any, error) {
	data, err := json.Marshal(obj)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func insertCloudAccountOnboardingError(tenantId string, createdBy string, cloudProvider string, config string, errorMessage any) (any, error) {
	dbManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, err
	}

	errorMessageJson, err := common.MarshalJson(errorMessage)
	if err != nil {
		return nil, err
	}

	_, err = dbManager.Db.Exec(`insert into cloud_account_onboarding_errors (tenant_id, created_by, account_name, config, error_message) values ($1, $2, $3, $4, $5)`, tenantId, createdBy, cloudProvider, config, string(errorMessageJson))
	if err != nil {
		return nil, err
	}

	return nil, nil
}

func CreateAccount(context *security.RequestContext, query AccountCreateRequest) (AccountCreateResponse, error) {
	err := common.ValidateStruct(query)
	if err != nil {
		return AccountCreateResponse{}, err
	}

	createdBy := context.GetSecurityContext().GetUserId()
	tenant := context.GetSecurityContext().GetTenantId()
	if createdBy == "" && tenant == "" && !context.GetSecurityContext().IsSuperAdmin() {
		return AccountCreateResponse{}, fmt.Errorf("account: unauthorized")
	}

	if !common.IsValidK8sAccountName(query.AccountName) && strings.EqualFold(query.CloudProvider, "k8s") {
		return AccountCreateResponse{}, fmt.Errorf("account: account name is not valid")
	}

	// Normalize cloud_provider for AWS, Azure, GCP BEFORE duplicate check and validation
	// This ensures consistent matching against database enum values
	if strings.EqualFold(query.CloudProvider, "aws") {
		query.CloudProvider = "AWS"
	} else if strings.EqualFold(query.CloudProvider, "azure") {
		query.CloudProvider = "Azure"
	} else if strings.EqualFold(query.CloudProvider, "gcp") {
		query.CloudProvider = "GCP"
	} else if strings.EqualFold(query.CloudProvider, "cloudfoundry") {
		query.CloudProvider = "CloudFoundry"
	}

	// License validation for all account types
	currentAccounts, err := getCurrentAccountCount(tenant)
	if err != nil {
		return AccountCreateResponse{}, err
	}

	maxAccounts := license.GetMaxAccounts()
	if maxAccounts == 0 {
		context.GetLogger().Warn("account create rejected: license gate",
			"reason", "max_accounts_zero",
			"tenant", tenant)
		return AccountCreateResponse{}, fmt.Errorf("account: can not add a new account, your license has expired")
	} else if maxAccounts > 0 && currentAccounts >= maxAccounts {
		context.GetLogger().Warn("account create rejected: license limit",
			"reason", "max_accounts_exceeded",
			"current", currentAccounts,
			"max", maxAccounts,
			"tenant", tenant)
		return AccountCreateResponse{}, fmt.Errorf("account: account limit exceeded for your license. maximum allowed accounts: %d", maxAccounts)
	}

	// Check for existing account to ensure idempotency
	dbManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return AccountCreateResponse{}, fmt.Errorf("account: unable to process request, please try again")
	}
	var existingId string
	err = dbManager.Db.Get(&existingId, "SELECT id FROM cloud_accounts WHERE tenant = $1 AND account_name = $2", tenant, query.AccountName)
	if err == nil && existingId != "" {
		context.GetLogger().Warn("account: attempt to create a duplicate account", "name", query.AccountName, "tenant", tenant)
		return AccountCreateResponse{}, fmt.Errorf("account: an account with name '%s' already exists", query.AccountName)
	}

	query.CreatedBy = context.GetSecurityContext().GetUserId()
	query.UpdatedBy = context.GetSecurityContext().GetUserId()
	query.Tenant = context.GetSecurityContext().GetTenantId()

	jsonQuery, err := common.MarshalJson(query)
	if err != nil {
		return AccountCreateResponse{}, err
	}

	encrtptedAccountCreateRequestConfig, err := common.Encrypt(string(jsonQuery))
	if err != nil {
		return AccountCreateResponse{}, err
	}

	if query.CloudProvider == "" {
		return AccountCreateResponse{}, fmt.Errorf("cloud_provider is required")
	}

	context.GetLogger().Info("creating account", "account_id", query.AccountType)

	k8sAccessSecret := ""
	azureCostManagementWarning := "" // Track Azure Cost Management permission issue

	if strings.EqualFold(query.CloudProvider, "k8s") || strings.EqualFold(query.AccountType, "kubernetes") {
		k8sAccessKey := common.GenerateUUID()
		k8sAccessSecret, err = common.GenerateRandomHexString(36)
		if err != nil {
			return AccountCreateResponse{}, err
		}
		k8sAccessSecretHashed, err := common.HashPassword(k8sAccessSecret)
		if err != nil {
			return AccountCreateResponse{}, err
		}

		query.AgentAccessKey = k8sAccessKey
		query.AgentAccessSecretV2 = k8sAccessSecretHashed
		query.CloudProvider = "K8s"
		query.AccountType = "kubernetes"
	} else if strings.EqualFold("cloud", query.AccountType) || slices.Contains([]string{"aws", "azure", "gcp", "cloudfoundry"}, strings.ToLower(query.CloudProvider)) {
		switch query.CloudProvider {
		case "AWS":
			hasRole := strings.TrimSpace(query.AssumeRole) != ""
			hasKeys := strings.TrimSpace(query.AccessKey) != "" && strings.TrimSpace(query.AccessSecret) != ""
			if !hasRole && !hasKeys {
				return AccountCreateResponse{}, fmt.Errorf("account: only supported authentication types are Secret Key and Assume Role")
			}
			if hasRole && hasKeys {
				return AccountCreateResponse{}, fmt.Errorf("account: provide either assume_role or access_key+access_secret, not both")
			}

			// Re-run validation server-side at create time (STS + CUR + S3
			// probe). This blocks onboarding when no usable Cost & Usage
			// Report is discoverable — see plan "Block onboarding (hard error)".
			validationResult := validateAWSCredentialsInternal(context.GetContext(), AwsValidateInternalRequest{
				AssumeRole:   query.AssumeRole,
				ExternalID:   query.ExternalId,
				AccessKey:    query.AccessKey,
				AccessSecret: query.AccessSecret,
				Region:       query.Region,
			})
			if !validationResult.Success {
				context.GetLogger().Error("account: AWS validation failed at create time", "error", validationResult.ErrorMessage)
				_, err2 := insertCloudAccountOnboardingError(query.Tenant, query.CreatedBy, query.CloudProvider, encrtptedAccountCreateRequestConfig, fmt.Errorf("%s", validationResult.ErrorMessage))
				if err2 != nil {
					context.GetLogger().Error("account: failed to add cloud account onboarding error", "error", err2)
				}
				return AccountCreateResponse{}, fmt.Errorf("account: %s", validationResult.ErrorMessage)
			}

			query.AccountNumber = validationResult.AccountNumber

			// Persist discovered CUR config into cloud_accounts.data using the
			// same JSON keys the CF callback uses (event_org_registration.go:676-686)
			// so collector ingestion is identical across CF / role / keys flows.
			if validationResult.Cur != nil {
				if query.Data == nil {
					query.Data = map[string]any{}
				}
				query.Data["cost_report_s3_bucket"] = validationResult.Cur.BucketName
				query.Data["cost_report_name"] = validationResult.Cur.ReportName
				query.Data["cost_report_s3_prefix"] = validationResult.Cur.Prefix
				query.Data["cost_report_s3_region"] = validationResult.Cur.Region
				query.Data["cost_report_compression"] = validationResult.Cur.Compression
				query.Data["cost_report_time_unit"] = validationResult.Cur.TimeUnit
				query.Data["cost_report_versioning"] = validationResult.Cur.Versioning
				query.Data["cost_report_format"] = validationResult.Cur.Format
				if _, ok := query.Data["cur_source"]; !ok {
					if hasKeys {
						query.Data["cur_source"] = "access_keys"
					} else {
						query.Data["cur_source"] = "manual_role"
					}
				}
			}
		case "Azure":
			// AccountNumber = Tenant (Directory) ID
			// AssumeRole = Subscription ID
			// AccessKey = Application (Client) ID
			if query.AssumeRole != "" {
				// Validate Azure credentials via collector-server
				validationResult := validateAzureCredentialsInternal(context.GetContext(),
					query.AccountNumber, query.AccessKey, query.AccessSecret, query.AssumeRole)

				if !validationResult.Success {
					context.GetLogger().Error("account: failed to validate azure credentials",
						"tenant_id", query.AccountNumber, "error", validationResult.ErrorMessage)
					return AccountCreateResponse{}, fmt.Errorf("account: failed to authenticate with Azure: %s", validationResult.ErrorMessage)
				}

				// Check if cost management permission is missing (non-blocking)
				hasCostManagement := false
				for _, perm := range validationResult.PermissionDetails {
					if perm.Permission == "Cost Management" && perm.HasAccess {
						hasCostManagement = true
						break
					}
				}
				if !hasCostManagement {
					azureCostManagementWarning = azureCostManagementWarningMsg
					context.GetLogger().Warn("azure: account will be created without cost management access", "subscription_id", query.AssumeRole)
				}
			} else if query.AccountNumber != "" {
				cred, err := azidentity.NewClientSecretCredential(query.AccountNumber, query.AccessKey, query.AccessSecret, nil)
				if err != nil {
					context.GetLogger().Error("failed to create credential", "error", err)
				}
				scope := "https://management.azure.com/.default"
				_, err = cred.GetToken(ctx.Background(), policy.TokenRequestOptions{
					Scopes: []string{scope},
				})

				if err != nil {
					context.GetLogger().Error("account: failed to validate azure credentials", "error", err, "tenant_id", query.AccountNumber)
					return AccountCreateResponse{}, fmt.Errorf("account: failed to authenticate with Azure, please verify your credentials")
				}
				subClient, err := armsubscription.NewSubscriptionsClient(cred, nil)
				if err != nil {
					context.GetLogger().Error("failed to create subscriptions client", "error", err, "tenant_id", query.AccountNumber)
					return AccountCreateResponse{}, fmt.Errorf("failed to create subscriptions client")
				}
				var subscriptionIDs []string
				pager := subClient.NewListPager(nil)
				for pager.More() {
					page, err := pager.NextPage(ctx.Background())
					if err != nil {
						context.GetLogger().Error("failed to get next page of subscriptions", "error", err, "tenant_id", query.AccountNumber)
						return AccountCreateResponse{}, fmt.Errorf("account: failed to retrieve Azure subscriptions, please verify your credentials and permissions")
					}

					for _, sub := range page.Value {
						if sub.SubscriptionID != nil {
							subscriptionIDs = append(subscriptionIDs, *sub.SubscriptionID)
						}
					}
				}
				query.AssumeRole = strings.Join(subscriptionIDs, ",")
				if query.AssumeRole == "" {
					return AccountCreateResponse{}, fmt.Errorf("azure: no subscriptions found for the provided credentials. Ensure the service principal has Reader role on at least one subscription")
				}

				// Validate Azure credentials for the first subscription via collector-server (non-blocking)
				// If multiple subscriptions exist, check the first one as a representative sample
				// Note: Cost management permissions are granted per-subscription. Checking only the first
				// subscription may not reflect the permissions on other subscriptions.
				if len(subscriptionIDs) > 0 {
					validationResult := validateAzureCredentialsInternal(context.GetContext(),
						query.AccountNumber, query.AccessKey, query.AccessSecret, subscriptionIDs[0])

					hasCostManagement := false
					for _, perm := range validationResult.PermissionDetails {
						if perm.Permission == "Cost Management" && perm.HasAccess {
							hasCostManagement = true
							break
						}
					}
					if !hasCostManagement {
						azureCostManagementWarning = azureCostManagementWarningMsg
						context.GetLogger().Warn("azure: account will be created without cost management access",
							"tenant_id", query.AccountNumber,
							"subscriptions_count", len(subscriptionIDs),
							"checked_subscription", subscriptionIDs[0])
					}
				}
			} else {
				return AccountCreateResponse{}, fmt.Errorf("account: only supported authentication types are Secret Key and Assume Role")
			}
		case "GCP":
			if query.AccessSecret == "" && query.Data == nil {
				return AccountCreateResponse{}, fmt.Errorf("account: access secret or data is required for GCP")
			}

			// validate credentials
			var credsJSON string
			if query.AccessSecret != "" {
				credsJSON = query.AccessSecret
			} else if query.Data != nil {
				// Fallback to data field if access_secret is empty
				// query.Data is a structured map[string]any; marshal it to JSON string.
				b, err := common.MarshalJson(query.Data)
				if err != nil {
					return AccountCreateResponse{}, fmt.Errorf("account: failed to marshal GCP credentials from data: %w", err)
				}
				credsJSON = string(b)
			}

			var gcpCredentials struct {
				ProjectID string `json:"project_id"`
			}
			err := json.Unmarshal([]byte(credsJSON), &gcpCredentials)
			if err != nil {
				return AccountCreateResponse{}, fmt.Errorf("account: failed to parse GCP credentials JSON: %w", err)
			}
			if gcpCredentials.ProjectID == "" {
				return AccountCreateResponse{}, fmt.Errorf("account: invalid GCP credentials, 'project_id' field is missing")
			}

			// Set account number from credentials if not already provided (e.g., by bulk onboard)
			if query.AccountNumber == "" {
				query.AccountNumber = gcpCredentials.ProjectID
			}
			validationResult := validateGCPCredentialsInternal(context.GetContext(), credsJSON, gcpCredentials.ProjectID, "", "", "")
			if !validationResult.Success {
				return AccountCreateResponse{}, fmt.Errorf("account: invalid GCP credentials: %s", validationResult.ErrorMessage)
			}
		case "CloudFoundry":
			// CF API URL is required in the data field
			if query.Data == nil {
				return AccountCreateResponse{}, fmt.Errorf("account: data field with cf_api_url is required for CloudFoundry")
			}
			cfAPIURL, _ := query.Data["cf_api_url"].(string)
			if cfAPIURL == "" {
				return AccountCreateResponse{}, fmt.Errorf("account: cf_api_url is required for CloudFoundry")
			}

			// AccessSecret holds the token or client_secret
			if query.AccessSecret == "" {
				return AccountCreateResponse{}, fmt.Errorf("account: access_secret (bearer token or UAA client secret) is required for CloudFoundry")
			}

			// Set account number from CF API URL host if not provided
			if query.AccountNumber == "" {
				parsedURL, parseErr := url.Parse(cfAPIURL)
				if parseErr == nil {
					query.AccountNumber = parsedURL.Host
				} else {
					query.AccountNumber = cfAPIURL
				}
			}

			// Validate connectivity by calling CF API /v3/info
			err := validateCFCredentialsInternal(context.GetContext(), cfAPIURL, query.AccessSecret, query.AccessKey, query.Data)
			if err != nil {
				return AccountCreateResponse{}, fmt.Errorf("account: CloudFoundry credential validation failed: %w", err)
			}
		}
	} else {
		return AccountCreateResponse{}, errors.New("account: unknown cloud provider")
	}

	if query.AccessSecret != "" {
		encryptedAccessSecret, err := common.Encrypt(query.AccessSecret)
		if err != nil {
			return AccountCreateResponse{}, err
		}
		query.AccessSecret = encryptedAccessSecret
	}

	// Generate external_id for cloud accounts (AWS/Azure/GCP) - used for EventBridge token-based account lookup
	if slices.Contains([]string{"AWS", "Azure", "GCP", "CloudFoundry"}, query.CloudProvider) && query.ExternalId == "" {
		query.ExternalId = common.GenerateUUID()
		context.GetLogger().Info("account: generated external_id for cloud account", "external_id", query.ExternalId, "provider", query.CloudProvider)
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return AccountCreateResponse{}, fmt.Errorf("failed to get database manager: %w", err)
	}

	accountFields, err := structToInsertMap(query)
	if err != nil {
		return AccountCreateResponse{}, fmt.Errorf("account: failed to convert request: %w", err)
	}
	// Remove agent-only fields that are not cloud_accounts columns
	delete(accountFields, "agent_access_key")
	delete(accountFields, "agent_access_secret")

	newAccountId, err := insertRowFromMap(dbms, "cloud_accounts", accountFields)
	if err != nil {
		context.GetLogger().Error("account: failed to add cloud account", "error", err, "query", slog.AnyValue(query))
		_, err2 := insertCloudAccountOnboardingError(query.Tenant, query.CreatedBy, query.CloudProvider, encrtptedAccountCreateRequestConfig, err)
		if err2 != nil {
			context.GetLogger().Error("account: failed to add cloud account onboarding error", "error", err2)
		}

		if strings.Contains(err.Error(), "duplicate key") {
			return AccountCreateResponse{}, fmt.Errorf("account: an account with this name already exists")
		}
		return AccountCreateResponse{}, fmt.Errorf("account: unable to create account, please try again")
	}

	if query.AgentAccessKey != "" {
		agentFields := map[string]any{
			"cloud_account_id": newAccountId,
			"tenant":           query.Tenant,
			"access_key":       query.AgentAccessKey,
			"status":           "NOT_CONNECTED",
			"type":             "k8s",
		}
		if query.AgentAccessSecretV2 != "" {
			agentFields["access_secret_v2"] = query.AgentAccessSecretV2
		}
		if query.AgentAccessSecret != "" {
			agentFields["access_secret"] = query.AgentAccessSecret
		}

		_, err := insertRowFromMap(dbms, "agent", agentFields)
		if err != nil {
			context.GetLogger().Error("failed to add cloud account agent", "error", err)
			_, err2 := insertCloudAccountOnboardingError(query.Tenant, query.CreatedBy, query.CloudProvider, encrtptedAccountCreateRequestConfig, err)
			if err2 != nil {
				context.GetLogger().Error("failed to add cloud account onboarding error", "error", err2)
			}
			return AccountCreateResponse{}, fmt.Errorf("account: unable to complete account setup, please try again")
		}
	}

	// invalidate tenant cache
	err = security.InvalidateCacheForTenant(context.GetSecurityContext().GetTenantId())
	if err != nil {
		context.GetLogger().Error("unable to invalidate tenant cache", "error", err)
	}

	//load initial data
	if query.CloudProvider == "AWS" || query.CloudProvider == "Azure" || query.CloudProvider == "GCP" || query.CloudProvider == "CloudFoundry" {
		go func() {
			context.GetLogger().Info("account: triggering intial load for account", "account", newAccountId)
			_, err := cloud.StoreUsageReport(context, cloud.StoreUsageRequest{
				AccountId: newAccountId,
				Month:     time.Now().Month(),
				Year:      time.Now().Year(),
			})
			if err != nil {
				context.GetLogger().Error("failed to hit store_usage", "error", err)
			}
		}()
	}

	audit.LogChange(context, audit.ChangeInput{
		EventCategory: audit.EventCategoryAccount,
		EventType:     audit.EventTypeAccountCreate,
		EventAction:   audit.EventActionCreate,
		TargetID:      newAccountId,
		AccountID:     newAccountId,
		TableName:     "cloud_accounts",
		NewData:       accountFields,
	})

	newAccount, err := GetAccount(context, newAccountId)
	if err != nil {
		context.GetLogger().Error("failed to get newly created account", "error", err)
	} else {
		if err := audit.PublishAuditEvent(context, audit.Audit{
			AccountId:     newAccountId,
			TenantId:      context.GetSecurityContext().GetTenantId(),
			UserId:        context.GetSecurityContext().GetUserId(),
			EventTime:     time.Now(),
			EventCategory: audit.EventCategoryAccount,
			EventType:     audit.EventTypeAccountCreate,
			EventState:    newAccount,
			EventActor:    audit.EventActorApiService,
			EventTarget:   "account",
			EventAction:   audit.EventActionCreate,
			EventStatus:   audit.EventStatusSuccess,
		}); err != nil {
			context.GetLogger().Error("failed to publish audit event", "error", err)
		}
	}
	return AccountCreateResponse{
		Id:           newAccountId,
		AccessKey:    query.AgentAccessKey,
		AccessSecret: k8sAccessSecret,
		Warning:      azureCostManagementWarning,
	}, nil
}

func getCurrentAccountCount(tenantId string) (int, error) {
	databaseManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return 0, err
	}
	var count int
	err = databaseManager.Db.Get(&count, "SELECT COUNT(*) FROM cloud_accounts WHERE tenant = $1 AND status = 'active'", tenantId)
	if err != nil {
		return 0, err
	}
	return count, nil
}

// extractAwsAccountIdFromRoleArn extracts the AWS account ID from an IAM role ARN
// ARN format: arn:aws:iam::ACCOUNT_ID:role/ROLE_NAME
func extractAwsAccountIdFromRoleArn(roleArn string) (string, error) {
	if roleArn == "" {
		return "", fmt.Errorf("role ARN is empty")
	}

	// Split by ':' to parse ARN components
	parts := strings.Split(roleArn, ":")
	if len(parts) < 5 {
		return "", fmt.Errorf("invalid IAM role ARN format: %s", roleArn)
	}

	// ARN format: arn:aws:iam::ACCOUNT_ID:role/ROLE_NAME
	// parts[0] = "arn"
	// parts[1] = "aws"
	// parts[2] = "iam"
	// parts[3] = "" (empty for IAM)
	// parts[4] = ACCOUNT_ID
	accountId := parts[4]
	if accountId == "" {
		return "", fmt.Errorf("account ID not found in IAM role ARN: %s", roleArn)
	}

	return accountId, nil
}

// extractQueueNameFromSqsIdentifier extracts the queue name from SQS ARN, URL, or name
// Supports:
// - ARN: arn:aws:sqs:<region>:<aws-account-id>:nudgebee-eventbridge-queue
// - URL: https://sqs.<region>.amazonaws.com/<aws-account-id>/nudgebee-eventbridge-queue
// - Name: nudgebee-eventbridge-queue
func extractQueueNameFromSqsIdentifier(identifier string) string {
	if identifier == "" {
		return "nudgebee-eventbridge-queue" // default
	}

	// Check if it's an ARN (arn:aws:sqs:region:account-id:queue-name)
	if strings.HasPrefix(identifier, "arn:aws:sqs:") {
		parts := strings.Split(identifier, ":")
		if len(parts) == 6 {
			return parts[5] // queue name is the last part
		}
	}

	// Check if it's a URL (https://sqs.region.amazonaws.com/account-id/queue-name)
	if strings.Contains(identifier, "sqs.") && strings.Contains(identifier, "amazonaws.com") {
		parts := strings.Split(identifier, "/")
		if len(parts) >= 3 {
			return parts[len(parts)-1] // queue name is the last part after /
		}
	}

	// Otherwise assume it's just the queue name
	return identifier
}

func AwsOnBoardUrl(context *security.RequestContext, query AccountCreateRequest) (AwsOnBoardResponse, error) {
	err := common.ValidateStruct(query)
	if err != nil {
		return AwsOnBoardResponse{}, err
	}
	if query.CloudProvider == "AWS" {
		createdBy := context.GetSecurityContext().GetUserId()
		tenant := context.GetSecurityContext().GetTenantId()
		randomId := fmt.Sprintf("%d", time.Now().UnixNano()/int64(time.Millisecond))
		stackName := fmt.Sprintf("connectToNudgebee-%s", randomId)
		reportName := fmt.Sprintf("nudgebeeReport-%s", randomId)
		bucketName := fmt.Sprintf("nudgebee-cur-%s", randomId)

		// Generate external_id for EventBridge token-based account lookup
		externalId := common.GenerateUUID()
		context.GetLogger().Info("account: generated external_id for AWS onboarding", "external_id", externalId)

		// Generate per-request verification token for auto-registration
		// Keyed by external_id (already unique per request)
		verificationToken, err := common.GenerateRandomHexString(32)
		if err != nil {
			return AwsOnBoardResponse{}, fmt.Errorf("account: failed to generate verification token: %w", err)
		}

		// Determine access mode (default: readwrite)
		accessMode := "readwrite"
		if query.AccountAccess == "readonly" {
			accessMode = "readonly"
		}

		// Store SHA-256 hash in tenant_attrs keyed by external_id, along with access mode
		tokenHash := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(verificationToken)))
		tenantAttrs := []tenantpkg.AttributeObject{
			{Name: fmt.Sprintf("aws_onboard_token_%s", externalId), Value: tokenHash},
		}
		if accessMode == "readonly" {
			tenantAttrs = append(tenantAttrs, tenantpkg.AttributeObject{
				Name: fmt.Sprintf("aws_onboard_access_mode_%s", externalId), Value: accessMode,
			})
		}
		if query.SsmAccess {
			tenantAttrs = append(tenantAttrs, tenantpkg.AttributeObject{
				Name: fmt.Sprintf("aws_onboard_ssm_access_%s", externalId), Value: "enabled",
			})
		}
		_, err = tenantpkg.UpsertTenantAttributes(context, tenantpkg.TenantAttributeUpsertRequest{
			Object: tenantAttrs,
		})
		if err != nil {
			return AwsOnBoardResponse{}, fmt.Errorf("account: failed to store onboard token: %w", err)
		}

		// Extract AWS account ID from the instance role ARN instead of using config
		nudgebeeAwsAccountId, err := extractAwsAccountIdFromRoleArn(config.Config.NUDGEBEE_INSTANCE_ROLE)
		if err != nil {
			context.GetLogger().Error("account: failed to extract AWS account ID from instance role ARN", "error", err, "role_arn", config.Config.NUDGEBEE_INSTANCE_ROLE)
			return AwsOnBoardResponse{}, fmt.Errorf("failed to extract AWS account ID from instance role: %w", err)
		}
		context.GetLogger().Debug("account: extracted AWS account ID from instance role", "account_id", nudgebeeAwsAccountId, "role_arn", config.Config.NUDGEBEE_INSTANCE_ROLE)

		// Build CloudFormation URL using net/url package for better maintainability
		baseURL := "https://us-east-1.console.aws.amazon.com/cloudformation/home"
		params := url.Values{}
		params.Set("region", "us-east-1")

		// Fragment parameters (after #)
		fragmentParams := url.Values{}
		fragmentParams.Set("templateURL", config.Config.AWS_TEMPLATE_URL)
		fragmentParams.Set("stackName", stackName)
		fragmentParams.Set("param_NudgebeeID", tenant)
		fragmentParams.Set("param_NudgebeeExternalId", externalId)
		fragmentParams.Set("param_NudgebeeAwsAccountId", nudgebeeAwsAccountId)
		fragmentParams.Set("param_NudgebeeDomain", config.Config.BaseUrl)
		fragmentParams.Set("param_NudgebeeIamRole", config.Config.NUDGEBEE_INSTANCE_ROLE)
		fragmentParams.Set("param_ReportName", reportName)
		fragmentParams.Set("param_BucketName", bucketName)
		fragmentParams.Set("param_NudgebeeUserId", createdBy)
		fragmentParams.Set("param_NudgebeeAccountName", query.AccountName)

		// Extract queue name from SQS identifier (can be ARN, URL, or name)
		sqsQueueName := extractQueueNameFromSqsIdentifier(config.Config.CloudCollectorAwsEventbridgeSqs)
		fragmentParams.Set("param_NudgebeeSqsQueueName", sqsQueueName)

		// SNS auto-registration parameters (conditional in CF template)
		if config.Config.AwsOrgSnsTopicArn != "" {
			fragmentParams.Set("param_NudgebeePingbackArn", config.Config.AwsOrgSnsTopicArn)
			fragmentParams.Set("param_NudgebeeVerificationToken", verificationToken)
		}

		// Access mode parameter (readonly skips write policies and EventBridge)
		fragmentParams.Set("param_NudgebeeAccessMode", accessMode)

		// SSM access parameter
		if query.SsmAccess {
			fragmentParams.Set("param_NudgebeeSsmAccess", "enabled")
		}

		cloudFormationURL := fmt.Sprintf("%s?%s#/stacks/quickcreate?%s",
			baseURL,
			params.Encode(),
			fragmentParams.Encode())

		context.GetLogger().Info("account: AWS onboard URL generated with auto-registration",
			"externalId", externalId, "tenantId", tenant)

		return AwsOnBoardResponse{
			Url:                  cloudFormationURL,
			BucketName:           bucketName,
			ExternalId:           externalId,
			AutoDetectionEnabled: config.Config.AwsOrgSnsTopicArn != "",
		}, nil
	}
	return AwsOnBoardResponse{}, fmt.Errorf("acount: only for AWS")
}

// AwsOnboardStatus checks if a single-account auto-registration has completed.
func AwsOnboardStatus(ctx *security.RequestContext, req AwsOnboardStatusRequest) (AwsOnboardStatusResponse, error) {
	err := common.ValidateStruct(req)
	if err != nil {
		return AwsOnboardStatusResponse{Status: "pending"}, err
	}

	tenantId := ctx.GetSecurityContext().GetTenantId()

	manager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return AwsOnboardStatusResponse{Status: "pending"}, fmt.Errorf("account: failed to get database manager: %w", err)
	}

	var result struct {
		Id            string    `db:"id"`
		AccountName   string    `db:"account_name"`
		AccountNumber string    `db:"account_number"`
		CreatedAt     time.Time `db:"created_at"`
		UpdatedAt     time.Time `db:"updated_at"`
	}
	err = manager.Db.Get(&result,
		`SELECT id, account_name, account_number, created_at, updated_at
		FROM cloud_accounts
		WHERE tenant = $1 AND external_id = $2
		LIMIT 1`,
		tenantId, req.ExternalId,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AwsOnboardStatusResponse{Status: "pending"}, nil
		}
		return AwsOnboardStatusResponse{Status: "pending"}, fmt.Errorf("account: failed to query onboard status: %w", err)
	}

	isReconnected := result.UpdatedAt.Sub(result.CreatedAt) > time.Minute

	return AwsOnboardStatusResponse{
		Status:        "completed",
		AccountId:     result.Id,
		AccountName:   result.AccountName,
		AccountNumber: result.AccountNumber,
		IsReconnected: isReconnected,
	}, nil
}

const latestTemplateVersion = "2"

func CloudUpdateCloudformationPermissions(context *security.RequestContext, req CloudUpdateCloudformationPermissionsRequest) (CloudUpdateCloudformationPermissionsResponse, error) {
	err := common.ValidateStruct(req)
	if err != nil {
		return CloudUpdateCloudformationPermissionsResponse{}, err
	}

	if !context.GetSecurityContext().HasAccountAccess(req.AccountId, security.SecurityAccessTypeUpdate) {
		return CloudUpdateCloudformationPermissionsResponse{}, common.ErrorUnauthorized("unauthorized")
	}

	// Call cloud-collector to discover the stack
	body, err := json.Marshal(map[string]string{"account_id": req.AccountId})
	if err != nil {
		return CloudUpdateCloudformationPermissionsResponse{}, fmt.Errorf("failed to marshal request: %w", err)
	}

	collectorURL := fmt.Sprintf("%s/v1/cloud/aws_cf_stack_info", config.Config.CloudCollectorServerUrl)
	httpReq, err := http.NewRequestWithContext(context.GetContext(), "POST", collectorURL, bytes.NewReader(body))
	if err != nil {
		return CloudUpdateCloudformationPermissionsResponse{}, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set(config.Config.CloudCollectorServerTokenHeader, config.Config.CloudCollectorServerToken)
	httpReq.Header.Set("x-tenant-id", context.GetSecurityContext().GetTenantId())

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return CloudUpdateCloudformationPermissionsResponse{}, fmt.Errorf("failed to call cloud-collector: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return CloudUpdateCloudformationPermissionsResponse{}, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != 200 {
		return CloudUpdateCloudformationPermissionsResponse{}, fmt.Errorf("cloud-collector returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var stackInfo struct {
		StackName       string `json:"stack_name"`
		StackRegion     string `json:"stack_region"`
		StackStatus     string `json:"stack_status"`
		TemplateVersion string `json:"template_version"`
	}
	if err := json.Unmarshal(respBody, &stackInfo); err != nil {
		return CloudUpdateCloudformationPermissionsResponse{}, fmt.Errorf("failed to parse stack info: %w", err)
	}

	needsUpdate := stackInfo.TemplateVersion != latestTemplateVersion

	// Build CloudFormation update URL
	region := stackInfo.StackRegion
	if region == "" {
		region = "us-east-1"
	}

	updateURL := fmt.Sprintf(
		"https://%s.console.aws.amazon.com/cloudformation/home?region=%s#/stacks/update/template?stackId=%s&templateURL=%s",
		region,
		region,
		url.QueryEscape(stackInfo.StackName),
		url.QueryEscape(config.Config.AWS_TEMPLATE_URL),
	)

	latest := latestTemplateVersion
	return CloudUpdateCloudformationPermissionsResponse{
		Url:             &updateURL,
		StackName:       &stackInfo.StackName,
		TemplateVersion: &stackInfo.TemplateVersion,
		LatestVersion:   &latest,
		NeedsUpdate:     &needsUpdate,
	}, nil
}

func AzureEventGridOnboardUrl(context *security.RequestContext, req AzureEventGridOnboardRequest) (AzureEventGridOnboardResponse, error) {
	err := common.ValidateStruct(req)
	if err != nil {
		return AzureEventGridOnboardResponse{}, err
	}

	tenantId := context.GetSecurityContext().GetTenantId()

	manager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return AzureEventGridOnboardResponse{}, fmt.Errorf("account: failed to get database manager: %w", err)
	}

	var account struct {
		ExternalId *string `db:"external_id"`
	}
	err = manager.Db.Get(&account,
		`SELECT external_id FROM cloud_accounts
		WHERE id = $1 AND tenant = $2 AND lower(cloud_provider) = 'azure' AND status = 'active'
		LIMIT 1`,
		req.AccountId, tenantId,
	)
	if err != nil {
		return AzureEventGridOnboardResponse{}, fmt.Errorf("account: azure account not found: %w", err)
	}

	// Generate and persist external_id for accounts created before this field existed
	if account.ExternalId == nil || *account.ExternalId == "" {
		newExternalId := common.GenerateUUID()
		_, err = manager.Db.Exec(
			`UPDATE cloud_accounts SET external_id = $1 WHERE id = $2 AND tenant = $3`,
			newExternalId, req.AccountId, tenantId,
		)
		if err != nil {
			return AzureEventGridOnboardResponse{}, fmt.Errorf("account: failed to generate external_id: %w", err)
		}
		account.ExternalId = &newExternalId
		context.GetLogger().Info("account: generated external_id for existing azure account",
			slog.String("account_id", req.AccountId),
			slog.String("external_id", newExternalId),
		)
	}

	armTemplateURL := config.Config.AzureARMTemplateURL
	baseUrl := config.Config.BaseUrl

	if armTemplateURL == "" {
		return AzureEventGridOnboardResponse{}, fmt.Errorf("account: azure_arm_template_url config is not set")
	}
	if baseUrl == "" {
		return AzureEventGridOnboardResponse{}, fmt.Errorf("account: base_url config is not set")
	}

	// Build the webhook URL that Event Grid will POST events to
	webhookUrl := fmt.Sprintf("%s/api/webhooks/azure-eventgrid?token=%s", baseUrl, *account.ExternalId)

	// Build Azure Portal ARM template deployment URL
	deployURL := fmt.Sprintf(
		"https://portal.azure.com/#create/Microsoft.Template/uri/%s",
		url.PathEscape(armTemplateURL),
	)

	context.GetLogger().Info("account: generated Azure EventGrid ARM template URL",
		slog.String("account_id", req.AccountId),
	)

	// Create azure_monitor_webhook integration record if one doesn't already exist for this account.
	// This enables the frontend to show a "Real-Time Events" indicator on the accounts page.
	var existingCount int
	countErr := manager.Db.Get(&existingCount,
		`SELECT COUNT(*) FROM integrations i
		 JOIN integrations_cloud_accounts ica ON ica.integration_id = i.id
		 WHERE i.type = 'azure_monitor_webhook' AND i.tenant_id = $1 AND ica.cloud_account_id = $2`,
		tenantId, req.AccountId,
	)
	if countErr != nil {
		context.GetLogger().Warn("account: failed to check existing azure_monitor_webhook integration (non-fatal)",
			slog.String("account_id", req.AccountId),
			slog.String("error", countErr.Error()),
		)
	}
	if existingCount == 0 && countErr == nil {
		integrationId := common.GenerateUUID()
		now := time.Now()
		userId := context.GetSecurityContext().GetUserId()
		tx, txErr := manager.Db.Beginx()
		if txErr == nil {
			_, txErr = tx.Exec(
				`INSERT INTO integrations (id, tenant_id, type, source, name, status, created_at, updated_at, created_by, updated_by)
				 VALUES ($1, $2, 'azure_monitor_webhook', 'user', $3, 'enabled', $4, $4, $5, $5)
				 ON CONFLICT DO NOTHING`,
				integrationId, tenantId, fmt.Sprintf("Azure Event Grid - %s", req.AccountId), now, userId,
			)
			if txErr == nil {
				_, txErr = tx.Exec(
					`INSERT INTO integration_config_values (id, integration_id, name, value, is_encrypted, created_at, updated_at, created_by, updated_by)
					 VALUES ($1, $2, 'token', $3, false, $4, $4, $5, $5)
					 ON CONFLICT DO NOTHING`,
					common.GenerateUUID(), integrationId, *account.ExternalId, now, userId,
				)
			}
			if txErr == nil {
				_, txErr = tx.Exec(
					`INSERT INTO integrations_cloud_accounts (integration_id, cloud_account_id, tenant_id)
					 VALUES ($1, $2, $3)
					 ON CONFLICT DO NOTHING`,
					integrationId, req.AccountId, tenantId,
				)
			}
			if txErr == nil {
				_ = tx.Commit()
			} else {
				_ = tx.Rollback()
				context.GetLogger().Warn("account: failed to create azure_monitor_webhook integration (non-fatal)",
					slog.String("account_id", req.AccountId),
					slog.String("error", txErr.Error()),
				)
			}
		}
	}

	return AzureEventGridOnboardResponse{
		Url:        deployURL,
		ExternalId: *account.ExternalId,
		WebhookUrl: webhookUrl,
	}, nil
}

func AwsEventBridgeOnboardUrl(context *security.RequestContext, req AwsEventBridgeOnboardRequest) (AwsEventBridgeOnboardResponse, error) {
	err := common.ValidateStruct(req)
	if err != nil {
		return AwsEventBridgeOnboardResponse{}, err
	}

	tenantId := context.GetSecurityContext().GetTenantId()

	manager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return AwsEventBridgeOnboardResponse{}, fmt.Errorf("account: failed to get database manager: %w", err)
	}

	var acct struct {
		ExternalId *string `db:"external_id"`
	}
	err = manager.Db.Get(&acct,
		`SELECT external_id FROM cloud_accounts
		WHERE id = $1 AND tenant = $2 AND lower(cloud_provider) = 'aws' AND status = 'active'
		LIMIT 1`,
		req.AccountId, tenantId,
	)
	if err != nil {
		return AwsEventBridgeOnboardResponse{}, fmt.Errorf("account: aws account not found: %w", err)
	}

	// Generate and persist external_id for accounts created before this field existed
	if acct.ExternalId == nil || *acct.ExternalId == "" {
		newExternalId := common.GenerateUUID()
		_, err = manager.Db.Exec(
			`UPDATE cloud_accounts SET external_id = $1 WHERE id = $2 AND tenant = $3`,
			newExternalId, req.AccountId, tenantId,
		)
		if err != nil {
			return AwsEventBridgeOnboardResponse{}, fmt.Errorf("account: failed to generate external_id: %w", err)
		}
		acct.ExternalId = &newExternalId
		context.GetLogger().Info("account: generated external_id for existing aws account",
			slog.String("account_id", req.AccountId),
			slog.String("external_id", newExternalId),
		)
	}

	addonTemplateURL := config.Config.AwsEventBridgeAddonTemplateURL
	if addonTemplateURL == "" {
		return AwsEventBridgeOnboardResponse{}, fmt.Errorf("account: aws_eventbridge_addon_template_url config is not set")
	}

	nudgebeeAwsAccountId, err := extractAwsAccountIdFromRoleArn(config.Config.NUDGEBEE_INSTANCE_ROLE)
	if err != nil {
		return AwsEventBridgeOnboardResponse{}, fmt.Errorf("account: failed to extract AWS account ID: %w", err)
	}

	sqsQueueName := extractQueueNameFromSqsIdentifier(config.Config.CloudCollectorAwsEventbridgeSqs)

	// Build CloudFormation quickcreate URL for the EventBridge addon template
	baseURL := "https://us-east-1.console.aws.amazon.com/cloudformation/home"
	params := url.Values{}
	params.Set("region", "us-east-1")

	fragmentParams := url.Values{}
	fragmentParams.Set("templateURL", addonTemplateURL)
	fragmentParams.Set("stackName", fmt.Sprintf("nudgebee-eventbridge-%s", (*acct.ExternalId)[:8]))
	fragmentParams.Set("param_NudgebeeExternalId", *acct.ExternalId)
	fragmentParams.Set("param_NudgebeeAwsAccountId", nudgebeeAwsAccountId)
	fragmentParams.Set("param_NudgebeeSqsQueueName", sqsQueueName)

	cloudFormationURL := fmt.Sprintf("%s?%s#/stacks/quickcreate?%s",
		baseURL,
		params.Encode(),
		fragmentParams.Encode())

	context.GetLogger().Info("account: AWS EventBridge onboard URL generated",
		slog.String("account_id", req.AccountId),
		slog.String("external_id", *acct.ExternalId),
	)

	// Stamp connection_status.eventbridge.connected_at so the "Real-Time Events"
	// indicator can flip to active immediately. The collector overwrites
	// last_event_at on every received event; this field is only set once at
	// onboarding and represents "user has clicked Connect EventBridge".
	//
	// Use the jsonb `||` deep-merge operator instead of `jsonb_set` because
	// jsonb_set does not create intermediate objects: if the agent row's
	// connection_status has no `eventbridge` key yet, jsonb_set with path
	// '{eventbridge,connected_at}' returns the document unchanged. The merge
	// approach also preserves any sibling keys the collector has already set
	// (regions, last_event_at, etc).
	//
	// The IS NULL guard makes the call idempotent — repeated clicks of
	// Connect EventBridge don't rewrite the same value.
	now := time.Now().Format(time.RFC3339)
	if _, stampErr := manager.Db.Exec(`
		UPDATE agent
		SET updated_at = NOW(),
			connection_status = COALESCE(connection_status, '{}'::jsonb) ||
				jsonb_build_object(
					'eventbridge',
					COALESCE(connection_status->'eventbridge', '{}'::jsonb) ||
						jsonb_build_object('connected_at', $1::text)
				)
		WHERE cloud_account_id = $2
		  AND tenant IS NOT DISTINCT FROM $3
		  AND type = 'AWS'
		  AND (connection_status->'eventbridge'->'connected_at') IS NULL`,
		now, req.AccountId, tenantId,
	); stampErr != nil {
		context.GetLogger().Warn("account: failed to stamp eventbridge.connected_at (non-fatal)",
			slog.String("account_id", req.AccountId),
			slog.String("error", stampErr.Error()),
		)
	}

	return AwsEventBridgeOnboardResponse{
		Url:        cloudFormationURL,
		ExternalId: *acct.ExternalId,
	}, nil
}

func GcpPubSubOnboardUrl(context *security.RequestContext, req GcpPubSubOnboardRequest) (GcpPubSubOnboardResponse, error) {
	err := common.ValidateStruct(req)
	if err != nil {
		return GcpPubSubOnboardResponse{}, err
	}

	tenantId := context.GetSecurityContext().GetTenantId()

	manager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return GcpPubSubOnboardResponse{}, fmt.Errorf("account: failed to get database manager: %w", err)
	}

	var account struct {
		ExternalId    string `db:"external_id"`
		AccountNumber string `db:"account_number"` // GCP Project ID
	}
	err = manager.Db.Get(&account,
		`SELECT external_id, account_number FROM cloud_accounts
		WHERE id = $1 AND tenant = $2 AND lower(cloud_provider) = 'gcp' AND status = 'active'
		LIMIT 1`,
		req.AccountId, tenantId,
	)
	if err != nil {
		return GcpPubSubOnboardResponse{}, fmt.Errorf("account: gcp account not found: %w", err)
	}

	if account.ExternalId == "" {
		return GcpPubSubOnboardResponse{}, fmt.Errorf("account: gcp account has no external_id")
	}

	templateYamlURL := config.Config.GcpPubSubTemplateURL
	nudgebeePubSubProjectId := config.Config.GcpProjectID
	if nudgebeePubSubProjectId == "" {
		nudgebeePubSubProjectId = account.AccountNumber // fallback to GCP project ID from account if not set in config
	}
	nudgebeeSubscriptionName := config.Config.CloudCollectorGcpPubSubSubscriptionID

	if templateYamlURL == "" {
		return GcpPubSubOnboardResponse{}, fmt.Errorf("account: gcp_pubsub_template_url config is not set")
	}
	if nudgebeePubSubProjectId == "" {
		return GcpPubSubOnboardResponse{}, fmt.Errorf("account: cloud_collector_gcp_pubsub_project_id config is not set")
	}
	if nudgebeeSubscriptionName == "" {
		return GcpPubSubOnboardResponse{}, fmt.Errorf("account: cloud_collector_gcp_pubsub_subscription_id config is not set")
	}

	// Build GCP Deployment Manager URL
	// Format: https://console.cloud.google.com/dm/deployments/new?template=<encoded-template-url>&project=<project-id>
	// Note: GCP Console supports pre-filling project but not template parameters via URL
	// User must manually paste the external_id token after clicking the link
	deployURL := fmt.Sprintf(
		"https://console.cloud.google.com/dm/deployments/new?template=%s&project=%s",
		url.QueryEscape(templateYamlURL),
		url.QueryEscape(account.AccountNumber),
	)

	context.GetLogger().Info("account: generated GCP Pub/Sub Deployment Manager URL",
		slog.String("account_id", req.AccountId),
		slog.String("external_id", account.ExternalId),
		slog.String("project_id", account.AccountNumber),
		slog.String("pubsub_project_id", nudgebeePubSubProjectId),
	)

	return GcpPubSubOnboardResponse{
		DeploymentManagerUrl: deployURL,
		ExternalId:           account.ExternalId,
		PubSubProjectId:      nudgebeePubSubProjectId,
		SubscriptionName:     nudgebeeSubscriptionName,
		TemplateYamlUrl:      templateYamlURL,
	}, nil
}

func SetupGCPMonitoringWebhook(ctx *security.RequestContext, req GcpMonitoringWebhookSetupRequest) (GcpMonitoringWebhookSetupResponse, error) {
	if !ctx.GetSecurityContext().HasAccountAccess(req.AccountId, security.SecurityAccessTypeUpdate) {
		return GcpMonitoringWebhookSetupResponse{}, common.ErrorUnauthorized("unauthorized")
	}

	err := common.ValidateStruct(req)
	if err != nil {
		return GcpMonitoringWebhookSetupResponse{}, err
	}

	tenantId := ctx.GetSecurityContext().GetTenantId()

	// Proxy to cloud-collector which has the SA credentials
	body, err := json.Marshal(req)
	if err != nil {
		return GcpMonitoringWebhookSetupResponse{}, fmt.Errorf("failed to marshal request: %w", err)
	}

	collectorURL := fmt.Sprintf("%s/v1/cloud/setup_gcp_monitoring_webhook", config.Config.CloudCollectorServerUrl)
	httpReq, err := http.NewRequestWithContext(ctx.GetContext(), "POST", collectorURL, bytes.NewReader(body))
	if err != nil {
		return GcpMonitoringWebhookSetupResponse{}, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set(config.Config.CloudCollectorServerTokenHeader, config.Config.CloudCollectorServerToken)
	httpReq.Header.Set("x-tenant-id", tenantId)

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return GcpMonitoringWebhookSetupResponse{}, fmt.Errorf("failed to call collector-server: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return GcpMonitoringWebhookSetupResponse{}, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return GcpMonitoringWebhookSetupResponse{}, fmt.Errorf("collector-server returned %d: %s", resp.StatusCode, string(respBody))
	}

	var apiResponse struct {
		Data struct {
			ChannelName string `json:"channel_name"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(respBody, &apiResponse); err != nil {
		return GcpMonitoringWebhookSetupResponse{}, fmt.Errorf("failed to parse response: %w", err)
	}
	if len(apiResponse.Errors) > 0 {
		return GcpMonitoringWebhookSetupResponse{}, fmt.Errorf("%s", apiResponse.Errors[0].Message)
	}

	ctx.GetLogger().Info("setup GCP monitoring webhook", "accountId", req.AccountId, "channelName", apiResponse.Data.ChannelName)

	return GcpMonitoringWebhookSetupResponse{
		ChannelName: apiResponse.Data.ChannelName,
	}, nil
}

func CheckGCPMonitoringPermission(ctx *security.RequestContext, req GcpCheckMonitoringPermissionRequest) (GcpCheckMonitoringPermissionResponse, error) {
	if !ctx.GetSecurityContext().HasAccountAccess(req.AccountId, security.SecurityAccessTypeRead) {
		return GcpCheckMonitoringPermissionResponse{}, common.ErrorUnauthorized("unauthorized")
	}

	err := common.ValidateStruct(req)
	if err != nil {
		return GcpCheckMonitoringPermissionResponse{}, err
	}

	tenantId := ctx.GetSecurityContext().GetTenantId()

	body, err := json.Marshal(req)
	if err != nil {
		return GcpCheckMonitoringPermissionResponse{}, fmt.Errorf("failed to marshal request: %w", err)
	}

	collectorURL := fmt.Sprintf("%s/v1/cloud/check_gcp_monitoring_permission", config.Config.CloudCollectorServerUrl)
	httpReq, err := http.NewRequestWithContext(ctx.GetContext(), "POST", collectorURL, bytes.NewReader(body))
	if err != nil {
		return GcpCheckMonitoringPermissionResponse{}, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set(config.Config.CloudCollectorServerTokenHeader, config.Config.CloudCollectorServerToken)
	httpReq.Header.Set("x-tenant-id", tenantId)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return GcpCheckMonitoringPermissionResponse{}, fmt.Errorf("failed to call collector-server: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return GcpCheckMonitoringPermissionResponse{}, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return GcpCheckMonitoringPermissionResponse{}, fmt.Errorf("collector-server returned %d: %s", resp.StatusCode, string(respBody))
	}

	var apiResponse struct {
		Data struct {
			HasPermission bool   `json:"has_permission"`
			ErrorDetail   string `json:"error_detail"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(respBody, &apiResponse); err != nil {
		return GcpCheckMonitoringPermissionResponse{}, fmt.Errorf("failed to parse response: %w", err)
	}
	if len(apiResponse.Errors) > 0 {
		return GcpCheckMonitoringPermissionResponse{}, fmt.Errorf("%s", apiResponse.Errors[0].Message)
	}

	return GcpCheckMonitoringPermissionResponse{
		HasPermission: apiResponse.Data.HasPermission,
		ErrorDetail:   apiResponse.Data.ErrorDetail,
	}, nil
}

func GCPOnBoardUrl(context *security.RequestContext, query AccountCreateRequest) (GCPOnBoardResponse, error) {
	err := common.ValidateStruct(query)
	if err != nil {
		return GCPOnBoardResponse{}, err
	}

	if query.CloudProvider == "GCP" {
		createdBy := context.GetSecurityContext().GetUserId()
		tenant := context.GetSecurityContext().GetTenantId()
		randomId := common.GenerateUUID()
		projectName := fmt.Sprintf("nudgebee-project-%s", randomId)
		bucketName := fmt.Sprintf("nudgebee-gcp-cur-%s", randomId)

		// Construct a GCP deployment manager template URL or marketplace onboarding link
		baseURL := "https://console.cloud.google.com/dm/deploy/new"
		params := url.Values{}
		params.Set("project", projectName)
		// for now harcoded
		params.Set("templateUrl", "https://storage.googleapis.com/nudgebee-templates/nudgebee-gcp-cloud-formation.json")
		params.Set("param_NudgebeeID", tenant)
		params.Set("param_NudgebeeDomain", config.Config.NUDGEBEE_URL)
		params.Set("param_NudgebeeIamRole", config.Config.NUDGEBEE_INSTANCE_ROLE)
		params.Set("param_BucketName", bucketName)
		params.Set("param_NudgebeeUserId", createdBy)
		params.Set("param_NudgebeeAccountName", query.AccountName)

		encodedUrl := fmt.Sprintf("%s?%s", baseURL, params.Encode())

		return GCPOnBoardResponse{
			Url:        encodedUrl,
			BucketName: bucketName,
		}, nil
	}

	return GCPOnBoardResponse{}, fmt.Errorf("account: only for GCP")
}

func GetResource(ctx *security.RequestContext, id string) (models.Resource, error) {
	databaseManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return models.Resource{}, err
	}
	r := databaseManager.Db.QueryRowx(`SELECT id, created_at, created_by, updated_at, updated_by, resourse_id, name, type,
			status, resourse_created_on, account, cloud_provider, region, arn, tenant, tags, meta,
			service_name, first_seen, last_seen, is_active, external_resource_id
		FROM cloud_resourses WHERE id = $1`, id)
	if r.Err() != nil {
		return models.Resource{}, r.Err()
	}
	rcrc := models.Resource{}
	err = r.StructScan(&rcrc)
	return rcrc, err
}

func ListResource(ctx *security.RequestContext, id string) (models.Resource, error) {
	databaseManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return models.Resource{}, err
	}
	r := databaseManager.Db.QueryRowx(`SELECT id, created_at, created_by, updated_at, updated_by, resourse_id, name, type,
			status, resourse_created_on, account, cloud_provider, region, arn, tenant, tags, meta,
			service_name, first_seen, last_seen, is_active, external_resource_id
		FROM cloud_resourses WHERE id = $1`, id)
	if r.Err() != nil {
		return models.Resource{}, r.Err()
	}
	rcrc := models.Resource{}
	err = r.StructScan(&rcrc)
	return rcrc, err
}

func GetAccount(ctx *security.RequestContext, id string) (models.Account, error) {
	databaseManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return models.Account{}, err
	}
	r := databaseManager.Db.QueryRowx(`SELECT id, cloud_provider, account_number, account_name, created_at, created_by,
			updated_at, updated_by, billing_source, start_date, tenant, assume_role, region, status,
			account_url, budget, synced_at, sync_status, account_access, account_purpose, data,
			access_key, access_secret, account_type, agent_access_key, agent_access_secret,
			agent_synced_at, sync_status_message, external_id, etl_attempt, parent_account_id,
			access_secret_v2, account_env
		FROM cloud_accounts WHERE id = $1`, id)
	if r.Err() != nil {
		return models.Account{}, r.Err()
	}
	acnt := models.Account{}
	err = r.StructScan(&acnt)
	return acnt, err
}

func ListAccounts(ctx *security.RequestContext, tenantId string) ([]models.Account, error) {
	databaseManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return []models.Account{}, err
	}
	rows, err := databaseManager.Db.Queryx(`SELECT id, cloud_provider, account_number, account_name, created_at, created_by,
			updated_at, updated_by, billing_source, start_date, tenant, assume_role, region, status,
			account_url, budget, synced_at, sync_status, account_access, account_purpose, data,
			access_key, access_secret, account_type, agent_access_key, agent_access_secret,
			agent_synced_at, sync_status_message, external_id, etl_attempt, parent_account_id,
			access_secret_v2, account_env
		FROM cloud_accounts WHERE tenant = $1 AND cloud_provider = 'K8s' AND status = 'active'`, tenantId)
	if err != nil {
		return []models.Account{}, err
	}
	defer func(ctx *security.RequestContext) {
		err := rows.Close()
		if err != nil {
			ctx.GetLogger().Error("Error closing rows", "error", err)
		}
	}(ctx)

	rowsMap := make([]models.Account, 0)
	for rows.Next() {
		var row = models.Account{}
		err = rows.StructScan(&row)
		if err != nil {
			return nil, err
		}
		rowsMap = append(rowsMap, row)
	}

	return rowsMap, nil
}

func ListActiveAccountsWithConnectedAgents(ctx *security.RequestContext, tenantId string) ([]models.Account, error) {
	databaseManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return []models.Account{}, err
	}
	rows, err := databaseManager.Db.Queryx(`SELECT DISTINCT ca.id, ca.cloud_provider, ca.account_number, ca.account_name,
			ca.created_at, ca.created_by, ca.updated_at, ca.updated_by, ca.billing_source, ca.start_date,
			ca.tenant, ca.assume_role, ca.region, ca.status, ca.account_url, ca.budget, ca.synced_at,
			ca.sync_status, ca.account_access, ca.account_purpose, ca.data, ca.access_key,
			ca.access_secret, ca.account_type, ca.agent_access_key, ca.agent_access_secret,
			ca.agent_synced_at, ca.sync_status_message, ca.external_id, ca.etl_attempt,
			ca.parent_account_id, ca.access_secret_v2, ca.account_env
		FROM cloud_accounts ca, agent ag
		WHERE ca.cloud_provider = 'K8s' AND ca.status = 'active' AND ca.id = ag.cloud_account_id
			AND ag.status = 'CONNECTED' AND ca.tenant = $1`, tenantId)
	if err != nil {
		return []models.Account{}, err
	}
	defer func(ctx *security.RequestContext) {
		err := rows.Close()
		if err != nil {
			ctx.GetLogger().Error("Error closing rows", "error", err)
		}
	}(ctx)

	rowsMap := make([]models.Account, 0)
	for rows.Next() {
		var row = models.Account{}
		err = rows.StructScan(&row)
		if err != nil {
			return nil, err
		}
		rowsMap = append(rowsMap, row)
	}

	return rowsMap, nil
}

func GetActiveK8sNodeCountForAccounts(tenantId string) ([]models.AccountNodeCount, error) {
	databaseManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, err
	}
	rows, err := databaseManager.Db.Queryx("SELECT kn.cloud_account_id, count(kn.name) FROM k8s_nodes kn, tenant t WHERE kn.is_active AND kn.tenant_id = t.id AND t.id = $1 GROUP BY cloud_account_id", tenantId)
	if err != nil {
		return nil, err
	}
	defer func() {
		err := rows.Close()
		if err != nil {
			slog.Error("Error closing rows", "error", err)
		}
	}()

	accountNodeCounts := make([]models.AccountNodeCount, 0)
	for rows.Next() {
		var accountNodeCount models.AccountNodeCount
		if err := rows.Scan(&accountNodeCount.CloudAccountId, &accountNodeCount.Count); err != nil {
			return nil, err
		}
		accountNodeCounts = append(accountNodeCounts, accountNodeCount)
	}

	return accountNodeCounts, nil
}

// ValidateCloudCredentials validates cloud provider credentials and checks required permissions
// This function does NOT create an account - it only validates credentials and permissions
// Returns a list of missing permissions (if any) but always succeeds for valid credentials
func ValidateCloudCredentials(context *security.RequestContext, query ValidateCloudCredentialsRequest) (ValidateCloudCredentialsResponse, error) {
	err := common.ValidateStruct(query)
	if err != nil {
		return ValidateCloudCredentialsResponse{}, err
	}

	// Normalize cloud provider name
	provider := strings.TrimSpace(query.CloudProvider)
	if strings.EqualFold(provider, "azure") {
		provider = "Azure"
	} else if strings.EqualFold(provider, "gcp") {
		provider = "GCP"
	} else if strings.EqualFold(provider, "aws") {
		provider = "AWS"
	}

	context.GetLogger().Info("validating cloud credentials", "provider", provider)

	switch provider {
	case "AWS":
		hasRole := strings.TrimSpace(query.AssumeRole) != ""
		hasKeys := strings.TrimSpace(query.AccessKey) != "" && strings.TrimSpace(query.AccessSecret) != ""
		if !hasRole && !hasKeys {
			return ValidateCloudCredentialsResponse{
				Success:      false,
				Provider:     provider,
				ErrorMessage: "AWS credentials require either assume_role or access_key+access_secret",
			}, nil
		}
		if hasRole && hasKeys {
			return ValidateCloudCredentialsResponse{
				Success:      false,
				Provider:     provider,
				ErrorMessage: "provide either assume_role or access_key+access_secret, not both",
			}, nil
		}

		result := validateAWSCredentialsInternal(context.GetContext(), AwsValidateInternalRequest{
			AssumeRole:   query.AssumeRole,
			ExternalID:   query.ExternalID,
			AccessKey:    query.AccessKey,
			AccessSecret: query.AccessSecret,
			Region:       query.Region,
		})

		if !result.Success {
			context.GetLogger().Warn("aws credentials validation failed",
				"assume_role", query.AssumeRole != "",
				"access_key", query.AccessKey != "",
				"error", result.ErrorMessage)
		} else {
			context.GetLogger().Info("aws credentials validated successfully",
				"account_number", result.AccountNumber)
		}

		return result, nil

	case "Azure":
		// Validate required Azure fields
		if query.TenantID == "" || query.ClientID == "" || query.ClientSecret == "" || query.SubscriptionID == "" {
			return ValidateCloudCredentialsResponse{
				Success:      false,
				Provider:     provider,
				ErrorMessage: "Azure credentials require tenant_id, client_id, client_secret, and subscription_id",
			}, nil
		}

		// Call internal validator
		result := validateAzureCredentialsInternal(context.GetContext(), query.TenantID, query.ClientID, query.ClientSecret, query.SubscriptionID)

		// Log results
		if len(result.MissingPermissions) > 0 {
			context.GetLogger().Warn("azure credentials validated with missing permissions",
				"subscription_id", query.SubscriptionID,
				"missing_permissions", result.MissingPermissions)
		} else {
			context.GetLogger().Info("azure credentials validated successfully",
				"subscription_id", query.SubscriptionID)
		}

		return result, nil

	case "GCP":
		// Validate required GCP fields
		if query.CredentialsJSON == "" || query.ProjectID == "" {
			return ValidateCloudCredentialsResponse{
				Success:      false,
				Provider:     provider,
				ErrorMessage: "GCP credentials require credentials_json and project_id",
			}, nil
		}

		// Call internal validator
		result := validateGCPCredentialsInternal(context.GetContext(), query.CredentialsJSON, query.ProjectID, query.BillingProjectID, query.BillingDatasetID, query.BillingTableID)

		// Log results
		if len(result.MissingPermissions) > 0 {
			context.GetLogger().Warn("gcp credentials validated with missing permissions",
				"project_id", query.ProjectID,
				"missing_permissions", result.MissingPermissions)
		} else {
			context.GetLogger().Info("gcp credentials validated successfully",
				"project_id", query.ProjectID)
		}

		return result, nil

	default:
		return ValidateCloudCredentialsResponse{
			Success:      false,
			ErrorMessage: fmt.Sprintf("unsupported cloud provider: %s (supported: AWS, Azure, GCP)", provider),
		}, nil
	}
}

// ListAzureSubscriptions discovers all accessible Azure subscriptions using provided service principal credentials
func ListAzureSubscriptions(context *security.RequestContext, query AzureListSubscriptionsRequest) (AzureListSubscriptionsResponse, error) {
	err := common.ValidateStruct(query)
	if err != nil {
		return AzureListSubscriptionsResponse{}, err
	}

	subscriptions, err := listAzureSubscriptionsInternal(context.GetContext(), query.TenantID, query.ClientID, query.ClientSecret)
	if err != nil {
		return AzureListSubscriptionsResponse{}, fmt.Errorf("failed to list Azure subscriptions: %w", err)
	}

	return AzureListSubscriptionsResponse{Subscriptions: subscriptions}, nil
}

// AzureBulkOnboard creates multiple cloud_accounts rows for a list of Azure subscriptions,
// sharing the same service principal credentials.
func AzureBulkOnboard(context *security.RequestContext, query AzureBulkOnboardRequest) (AzureBulkOnboardResponse, error) {
	err := common.ValidateStruct(query)
	if err != nil {
		return AzureBulkOnboardResponse{}, err
	}

	if len(query.Subscriptions) == 0 {
		return AzureBulkOnboardResponse{}, fmt.Errorf("at least one subscription is required")
	}

	createdBy := context.GetSecurityContext().GetUserId()
	tenant := context.GetSecurityContext().GetTenantId()

	// Validate credentials once using the first subscription
	firstSub := query.Subscriptions[0]
	validationResult := validateAzureCredentialsInternal(context.GetContext(),
		query.TenantID, query.ClientID, query.ClientSecret, firstSub.SubscriptionID)
	if !validationResult.Success {
		return AzureBulkOnboardResponse{}, fmt.Errorf("invalid Azure credentials: %s", validationResult.ErrorMessage)
	}

	response := AzureBulkOnboardResponse{
		Accounts: make([]AzureBulkOnboardAccountResult, 0, len(query.Subscriptions)),
	}

	// Create the first subscription as the parent account
	firstAccountName := query.AccountName
	if len(query.Subscriptions) > 1 {
		displayName := firstSub.DisplayName
		if displayName == "" {
			displayName = firstSub.SubscriptionID
		}
		firstAccountName = fmt.Sprintf("%s - %s", query.AccountName, displayName)
	}

	firstResp, err := CreateAccount(context, AccountCreateRequest{
		AccountName:   firstAccountName,
		CloudProvider: "Azure",
		AccountType:   "cloud",
		Tenant:        tenant,
		CreatedBy:     createdBy,
		UpdatedBy:     createdBy,
		AccountNumber: query.TenantID,
		AccessKey:     query.ClientID,
		AccessSecret:  query.ClientSecret,
		AssumeRole:    firstSub.SubscriptionID,
	})
	if err != nil {
		return AzureBulkOnboardResponse{}, fmt.Errorf("failed to create parent account for subscription %s: %w", firstSub.SubscriptionID, err)
	}

	parentID := firstResp.Id
	response.ParentID = parentID
	response.Accounts = append(response.Accounts, AzureBulkOnboardAccountResult{
		SubscriptionID: firstSub.SubscriptionID,
		AccountID:      firstResp.Id,
		Status:         "created",
	})

	// Create remaining subscriptions as children concurrently
	remaining := query.Subscriptions[1:]
	if len(remaining) > 0 {
		results := make([]AzureBulkOnboardAccountResult, len(remaining))
		var wg sync.WaitGroup
		for i, sub := range remaining {
			wg.Add(1)
			go func(idx int, sub AzureBulkOnboardSubInput) {
				defer wg.Done()
				result := AzureBulkOnboardAccountResult{SubscriptionID: sub.SubscriptionID}

				displayName := sub.DisplayName
				if displayName == "" {
					displayName = sub.SubscriptionID
				}
				accountName := fmt.Sprintf("%s - %s", query.AccountName, displayName)

				createResp, err := CreateAccount(context, AccountCreateRequest{
					AccountName:     accountName,
					CloudProvider:   "Azure",
					AccountType:     "cloud",
					Tenant:          tenant,
					CreatedBy:       createdBy,
					UpdatedBy:       createdBy,
					AccountNumber:   query.TenantID,
					AccessKey:       query.ClientID,
					AccessSecret:    query.ClientSecret,
					AssumeRole:      sub.SubscriptionID,
					ParentAccountId: parentID,
				})
				if err != nil {
					result.Status = "error"
					result.Error = err.Error()
				} else {
					result.AccountID = createResp.Id
					result.Status = "created"
				}
				results[idx] = result
			}(i, sub)
		}
		wg.Wait()
		response.Accounts = append(response.Accounts, results...)
	}

	return response, nil
}

func RegenerateAgentKeys(ctx *security.RequestContext, accountId string, agentType string) (AgentRegenerateKeyResponse, error) {
	if agentType == "" {
		agentType = "k8s"
	}
	if !ctx.GetSecurityContext().HasAccountAccess(accountId, security.SecurityAccessTypeUpdate) {
		return AgentRegenerateKeyResponse{}, common.ErrorUnauthorized("unauthorized")
	}
	databaseManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return AgentRegenerateKeyResponse{}, err
	}

	agent := models.Agent{}
	err = databaseManager.Db.Get(&agent, `SELECT id, created_at, updated_at, tenant, cloud_account_id, type, status,
			last_connected_at, access_key, access_secret, status_message, last_synced_at, version,
			k8s_version, connection_status, k8s_provider, access_secret_v2
		FROM agent WHERE cloud_account_id = $1 AND type = $2`, accountId, agentType)
	if err != nil {
		return AgentRegenerateKeyResponse{}, err
	}

	oldAccessKey := ""
	if agent.AccessKey != nil {
		oldAccessKey = *agent.AccessKey
	}

	secretKey, err := common.GenerateRandomHexString(36)
	if err != nil {
		return AgentRegenerateKeyResponse{}, err
	}
	accessKey := common.GenerateUUID()
	hashedKey, err := common.HashPassword(secretKey)
	if err != nil {
		return AgentRegenerateKeyResponse{}, err
	}
	agent.AccessSecretV2 = &hashedKey
	agent.AccessKey = &accessKey

	tx, err := databaseManager.Db.Beginx()
	if err != nil {
		return AgentRegenerateKeyResponse{}, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	_, err = tx.Exec("UPDATE agent SET access_key = $1, access_secret_v2 = $2, access_secret='' WHERE id = $3", *agent.AccessKey, *agent.AccessSecretV2, agent.Id)
	if err != nil {
		return AgentRegenerateKeyResponse{}, err
	}

	switch agentType {
	case "k8s":
		_, err = tx.Exec("UPDATE cloud_accounts SET agent_access_key = $1, access_secret_v2 = $2, agent_access_secret='' WHERE id = $3", *agent.AccessKey, *agent.AccessSecretV2, accountId)
		if err != nil {
			return AgentRegenerateKeyResponse{}, err
		}
	case "proxy":
		_, err = tx.Exec("UPDATE integration_config_values SET value = $1, updated_at = NOW() WHERE name = 'access_key' AND value = $2", *agent.AccessKey, oldAccessKey)
		if err != nil {
			return AgentRegenerateKeyResponse{}, err
		}
	}

	if err = tx.Commit(); err != nil {
		return AgentRegenerateKeyResponse{}, fmt.Errorf("failed to commit transaction: %w", err)
	}

	audit.LogChange(ctx, audit.ChangeInput{
		EventCategory: audit.EventCategoryAccount,
		EventType:     audit.EventTypeAccountUpdate,
		EventAction:   audit.EventActionUpdate,
		TargetID:      accountId,
		AccountID:     accountId,
		TableName:     "cloud_accounts",
		NewData:       map[string]any{"id": accountId, "agent_access_key": *agent.AccessKey},
	})

	if err := audit.PublishAuditEvent(ctx, audit.Audit{
		AccountId:     accountId,
		TenantId:      ctx.GetSecurityContext().GetTenantId(),
		UserId:        ctx.GetSecurityContext().GetUserId(),
		EventTime:     time.Now(),
		EventCategory: audit.EventCategoryAccount,
		EventType:     audit.EventTypeUpdateAgentToken,
		EventState:    map[string]string{"account_id": accountId, "agent_type": agentType},
		EventActor:    audit.EventActorApiService,
		EventTarget:   "agent",
		EventAction:   audit.EventActionUpdate,
		EventStatus:   audit.EventStatusSuccess,
	}); err != nil {
		ctx.GetLogger().Error("failed to publish audit event", "error", err)
	}

	return AgentRegenerateKeyResponse{
		AccessKey:    *agent.AccessKey,
		AccessSecret: secretKey,
		AccountId:    accountId,
	}, nil
}
func CleanUpAgentTask(ctx *security.RequestContext) error {
	databaseManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return err
	}
	taskCleanupDuration := 1800
	_, err = databaseManager.Db.Exec(fmt.Sprintf("update agent_task set status = 'TIMEOUT' where status = 'TODO' and created_at < (CURRENT_TIMESTAMP - INTERVAL '%d seconds')", taskCleanupDuration))
	if err != nil {
		return err
	}

	_, err = databaseManager.Db.Exec(fmt.Sprintf("update agent_task set status = 'TIMEOUT' where status = 'PROCESSING' and created_at < (CURRENT_TIMESTAMP - INTERVAL '%d seconds')", taskCleanupDuration*2))
	if err != nil {
		return err
	}
	return nil
}

// UpsertAccountAttrs inserts or updates cloud account attributes, scoped to tenant
func UpsertAccountAttrs(context *security.RequestContext, request AccountAttrUpsertRequest) (AccountAttrUpsertResponse, error) {
	err := common.ValidateStruct(request)
	if err != nil {
		return AccountAttrUpsertResponse{}, err
	}

	tenantId := context.GetSecurityContext().GetTenantId()
	if tenantId == "" {
		return AccountAttrUpsertResponse{}, fmt.Errorf("unauthorized: missing tenant")
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return AccountAttrUpsertResponse{}, fmt.Errorf("failed to get database: %w", err)
	}

	// Collect unique account IDs and verify tenant ownership in a single query
	accountIdSet := make(map[string]struct{})
	for _, attr := range request.Objects {
		accountIdSet[attr.CloudAccountId] = struct{}{}
	}
	uniqueAccountIds := make([]string, 0, len(accountIdSet))
	for id := range accountIdSet {
		uniqueAccountIds = append(uniqueAccountIds, id)
	}

	query, args, err := sqlx.In("SELECT id FROM cloud_accounts WHERE id IN (?) AND tenant = ?", uniqueAccountIds, tenantId)
	if err != nil {
		return AccountAttrUpsertResponse{}, fmt.Errorf("failed to build query: %w", err)
	}
	query = dbms.Db.Rebind(query)

	var verifiedIds []string
	err = dbms.Db.Select(&verifiedIds, query, args...)
	if err != nil {
		return AccountAttrUpsertResponse{}, fmt.Errorf("failed to verify account ownership: %w", err)
	}

	if len(verifiedIds) != len(uniqueAccountIds) {
		verifiedSet := make(map[string]struct{}, len(verifiedIds))
		for _, id := range verifiedIds {
			verifiedSet[id] = struct{}{}
		}
		for _, id := range uniqueAccountIds {
			if _, ok := verifiedSet[id]; !ok {
				return AccountAttrUpsertResponse{}, fmt.Errorf("unauthorized: account %s not found or does not belong to tenant", id)
			}
		}
	}

	// Build a single bulk INSERT ... ON CONFLICT statement
	valueStrings := make([]string, 0, len(request.Objects))
	valueArgs := make([]any, 0, len(request.Objects)*3)
	for i, attr := range request.Objects {
		base := i*3 + 1
		valueStrings = append(valueStrings, fmt.Sprintf("($%d, $%d, $%d)", base, base+1, base+2))
		valueArgs = append(valueArgs, attr.CloudAccountId, attr.Name, attr.Value)
	}

	bulkQuery := fmt.Sprintf(
		`INSERT INTO cloud_account_attrs (cloud_account_id, name, value) VALUES %s
		ON CONFLICT (cloud_account_id, name) DO UPDATE SET value = EXCLUDED.value`,
		strings.Join(valueStrings, ", "))

	result, err := dbms.Db.Exec(bulkQuery, valueArgs...)
	if err != nil {
		return AccountAttrUpsertResponse{}, fmt.Errorf("failed to upsert account attributes: %w", err)
	}

	affected, _ := result.RowsAffected()
	return AccountAttrUpsertResponse{AffectedRows: int(affected)}, nil
}

// UpdateAccountByAction updates a cloud account's status or name, scoped to tenant
func UpdateAccountByAction(context *security.RequestContext, request AccountUpdateRequest) (AccountUpdateResponse, error) {
	err := common.ValidateStruct(request)
	if err != nil {
		return AccountUpdateResponse{}, err
	}

	tenantId := context.GetSecurityContext().GetTenantId()
	if tenantId == "" {
		return AccountUpdateResponse{}, fmt.Errorf("unauthorized: missing tenant")
	}

	if request.Status == "" && request.AccountName == "" && len(request.Data) == 0 {
		return AccountUpdateResponse{}, fmt.Errorf("at least one of status, account_name, or data must be provided")
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return AccountUpdateResponse{}, fmt.Errorf("failed to get database: %w", err)
	}

	setClauses := []string{}
	args := []any{}
	argIdx := 1

	if request.Status != "" {
		setClauses = append(setClauses, fmt.Sprintf("status = $%d", argIdx))
		args = append(args, request.Status)
		argIdx++
	}
	if request.AccountName != "" {
		if strings.EqualFold(strings.TrimSpace(request.AccountName), "demo") {
			return AccountUpdateResponse{}, fmt.Errorf("account name 'Demo' is reserved and cannot be used")
		}
		setClauses = append(setClauses, fmt.Sprintf("account_name = $%d", argIdx))
		args = append(args, request.AccountName)
		argIdx++
	}
	if len(request.Data) > 0 {
		dataJSON, jsonErr := json.Marshal(request.Data)
		if jsonErr != nil {
			return AccountUpdateResponse{}, fmt.Errorf("failed to marshal data: %w", jsonErr)
		}
		setClauses = append(setClauses, fmt.Sprintf("data = $%d", argIdx))
		args = append(args, string(dataJSON))
		argIdx++
	}

	setClauses = append(setClauses, fmt.Sprintf("updated_by = $%d", argIdx))
	args = append(args, context.GetSecurityContext().GetUserId())
	argIdx++

	setClauses = append(setClauses, "updated_at = now()")

	query := fmt.Sprintf("UPDATE cloud_accounts SET %s WHERE id = $%d AND tenant = $%d",
		strings.Join(setClauses, ", "), argIdx, argIdx+1)
	args = append(args, request.Id, tenantId)

	result, err := dbms.Db.Exec(query, args...)
	if err != nil {
		return AccountUpdateResponse{}, fmt.Errorf("failed to update account: %w", err)
	}

	affected, _ := result.RowsAffected()
	if affected > 0 {
		audit.LogChange(context, audit.ChangeInput{
			EventCategory: audit.EventCategoryAccount,
			EventType:     audit.EventTypeAccountUpdate,
			EventAction:   audit.EventActionUpdate,
			TargetID:      request.Id,
			AccountID:     request.Id,
			TableName:     "cloud_accounts",
			NewData:       map[string]any{"id": request.Id, "status": request.Status, "account_name": request.AccountName},
		})
	}
	return AccountUpdateResponse{AffectedRows: int(affected)}, nil
}
