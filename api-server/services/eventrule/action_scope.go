package eventrule

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"nudgebee/services/common"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"

	"github.com/google/uuid"
)

// Action parameters that choose where an action runs. account_id replaces the
// event's account (kubectl, SSH, database, HTTP, cloud, logs/metrics/traces,
// ArgoCD, GitHub, …). datasource_id names an integration; the proxy actions run
// against the account it is linked to when no account_id is given.
const (
	actionParamAccountID    = "account_id"
	actionParamDatasourceID = "datasource_id"
)

var errActionOutsideTenant = errors.New("the action names an account or integration outside this organisation")

// cloud_accounts carries its tenant in the column "tenant"; integrations in "tenant_id".
const (
	accountsShareTenantSQL = `SELECT EXISTS (
		SELECT 1 FROM cloud_accounts e JOIN cloud_accounts o ON o.tenant = e.tenant
		WHERE e.id = $1 AND o.id = $2)`
	integrationInAccountTenantSQL = `SELECT EXISTS (
		SELECT 1 FROM integrations i JOIN cloud_accounts e ON e.tenant = i.tenant_id
		WHERE e.id = $1 AND i.id = $2)`
	integrationTenantAndAccountSQL = `SELECT i.tenant_id::text AS tenant_id,
		COALESCE((SELECT ica.cloud_account_id::text FROM integrations_cloud_accounts ica
		          WHERE ica.integration_id = i.id LIMIT 1), '') AS account_id
		FROM integrations i WHERE i.id = $1`
)

// accountsShareTenant reports whether otherAccountID belongs to the same tenant
// as eventAccountID. A package variable so tests can run without a database.
var accountsShareTenant = func(eventAccountID, otherAccountID string) (bool, error) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return false, err
	}
	var ok bool
	err = dbms.Db.Get(&ok, accountsShareTenantSQL, eventAccountID, otherAccountID)
	return ok, err
}

// integrationInAccountTenant reports whether integrationID belongs to the same
// tenant as eventAccountID.
var integrationInAccountTenant = func(eventAccountID, integrationID string) (bool, error) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return false, err
	}
	var ok bool
	err = dbms.Db.Get(&ok, integrationInAccountTenantSQL, eventAccountID, integrationID)
	return ok, err
}

// integrationTenantAndAccount returns an integration's tenant and the account
// the proxy actions would run it on (the same first linked account
// resolveAccountIDForIntegration picks). found is false for an unknown id.
var integrationTenantAndAccount = func(integrationID string) (tenantID, accountID string, found bool, err error) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return "", "", false, err
	}
	var row struct {
		TenantID  string `db:"tenant_id"`
		AccountID string `db:"account_id"`
	}
	err = dbms.Db.Get(&row, integrationTenantAndAccountSQL, integrationID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, err
	}
	return row.TenantID, row.AccountID, true, nil
}

// paramValues returns the trimmed, non-blank values of every parameter whose
// name matches key ignoring case. Actions decode their params with
// mapstructure, which matches field names case-insensitively, so `Account_ID`
// sets AccountId just as `account_id` does. A non-string value is an error.
func paramValues(params map[string]any, key string) ([]string, error) {
	var values []string
	for name, v := range params {
		if !strings.EqualFold(name, key) || v == nil {
			continue
		}
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("%s must be a string", key)
		}
		if s = strings.TrimSpace(s); s != "" {
			values = append(values, s)
		}
	}
	return values, nil
}

// checkActionScope runs just before an action executes, on its rendered
// parameters. Whatever account or integration they name must belong to the
// tenant of the event's account: the relay calls that follow carry no tenant,
// so nothing downstream would stop a rule from reaching another organisation.
func checkActionScope(eventAccountID string, params map[string]any) error {
	accounts, err := paramValues(params, actionParamAccountID)
	if err != nil {
		return err
	}
	for _, account := range accounts {
		if strings.EqualFold(account, eventAccountID) {
			continue
		}
		if _, perr := uuid.Parse(account); perr != nil {
			return errActionOutsideTenant
		}
		ok, err := accountsShareTenant(eventAccountID, account)
		if err != nil {
			return fmt.Errorf("checking the action's account: %w", err)
		}
		if !ok {
			return errActionOutsideTenant
		}
	}

	datasources, err := paramValues(params, actionParamDatasourceID)
	if err != nil {
		return err
	}
	for _, datasource := range datasources {
		// A non-UUID datasource_id is a datasource key, resolved by the agent of
		// the account the action runs on — which the check above has scoped.
		if _, perr := uuid.Parse(datasource); perr != nil {
			continue
		}
		ok, err := integrationInAccountTenant(eventAccountID, datasource)
		if err != nil {
			return fmt.Errorf("checking the action's integration: %w", err)
		}
		if !ok {
			return errActionOutsideTenant
		}
	}
	return nil
}

// accountAccess is the part of *security.SecurityContext the save-time check uses.
type accountAccess interface {
	CanAccessAccount(accountID string, access security.SecurityAccessType) bool
	GetTenantId() string
	IsSuperAdmin() bool
}

// checkActionParamAccess runs when a rule is saved. An action may target an
// account other than the rule's own only if the caller holds the same access
// on that account as the save itself needs on the rule's account. Values must
// be fixed: a template could only be resolved when the rule fires, after the
// caller is gone.
func checkActionParamAccess(sc accountAccess, ruleAccountID string, access security.SecurityAccessType, actionParams []map[string]any) error {
	for _, step := range actionParams {
		for actionName, raw := range step {
			params, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			for _, key := range []string{actionParamAccountID, actionParamDatasourceID} {
				values, err := paramValues(params, key)
				if err != nil {
					return common.ErrorBadRequest(fmt.Sprintf("%s: %s", actionName, err))
				}
				for _, value := range values {
					if err := checkParamValueAccess(sc, ruleAccountID, access, actionName, key, value); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

// checkParamValueAccess checks one account_id / datasource_id value of one action.
func checkParamValueAccess(sc accountAccess, ruleAccountID string, access security.SecurityAccessType, actionName, key, value string) error {
	if strings.Contains(value, "{{") || strings.Contains(value, "{%") {
		return common.ErrorBadRequest(fmt.Sprintf("%s: %s must name a fixed account or integration, not a template", actionName, key))
	}
	target := value
	if key == actionParamDatasourceID {
		if _, perr := uuid.Parse(value); perr != nil {
			return nil // a datasource key; the account check covers where it runs
		}
		tenantID, accountID, found, err := integrationTenantAndAccount(value)
		if err != nil {
			return fmt.Errorf("checking %s of %s: %w", key, actionName, err)
		}
		if !found || (!sc.IsSuperAdmin() && !strings.EqualFold(tenantID, sc.GetTenantId())) {
			return common.ErrorUnauthorized(fmt.Sprintf("%s: integration %s is not available", actionName, value))
		}
		if accountID == "" {
			return nil
		}
		target = accountID
	}
	if strings.EqualFold(target, ruleAccountID) {
		return nil
	}
	// Account ids are stored lowercase and CanAccessAccount compares exactly.
	if !sc.CanAccessAccount(strings.ToLower(target), access) {
		return common.ErrorUnauthorized(fmt.Sprintf("%s: account %s unauthorized", actionName, target))
	}
	return nil
}
