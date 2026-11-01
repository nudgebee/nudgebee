package common

func IsFeatureEnabled(feature string, tenant string) (bool, error) {
	dbManager, err := GetDatabaseManager(Metastore)
	if err != nil {
		return false, err
	}

	return IsFeatureEnabledWithDB(dbManager, feature, tenant)
}

// IsFeatureEnabledWithDB checks if a feature is enabled for a tenant using the provided database manager.
// Only the tenant-wide row (account_id IS NULL) counts; an account-level row must not enable a tenant-wide feature.
func IsFeatureEnabledWithDB(dbManager *DatabaseManager, feature string, tenant string) (bool, error) {
	rows, err := dbManager.Db.Queryx("SELECT tenant_id FROM feature_flag WHERE feature_id = $1 and status = 'enabled' and tenant_id = $2 and account_id IS NULL", feature, tenant)
	if err != nil {
		return false, err
	}
	defer func() {
		_ = rows.Close()
	}()

	// If any row is found, the feature is enabled for this tenant
	if rows.Next() {
		return true, nil
	}

	return false, nil
}

// IsFeatureEnabledForAccount is the opt-in, account-aware reader.
// Precedence: account row (either status) → tenant row → disabled. Both
// lookups are scoped to the tenant, and the tenant lookup only reads the
// tenant-wide row (account_id IS NULL), so one account's row never applies to
// other accounts or other tenants.
func IsFeatureEnabledForAccount(feature string, tenantId string, accountId string) (bool, error) {
	dbManager, err := GetDatabaseManager(Metastore)
	if err != nil {
		return false, err
	}

	var statuses []string
	if accountId != "" {
		err = dbManager.Db.Select(&statuses,
			"SELECT status FROM feature_flag WHERE feature_id = $1 AND tenant_id = $2 AND account_id = $3",
			feature, tenantId, accountId)
		if err != nil {
			return false, err
		}
		if len(statuses) > 0 {
			return statuses[0] == "enabled", nil
		}
	}

	err = dbManager.Db.Select(&statuses,
		"SELECT status FROM feature_flag WHERE feature_id = $1 AND tenant_id = $2 AND account_id IS NULL",
		feature, tenantId)
	if err != nil {
		return false, err
	}
	return len(statuses) > 0 && statuses[0] == "enabled", nil
}

// IsFeatureEnabledByDefaultForAccount is the default-enabled, account-aware
// reader matching api-server's tenant.IsFeatureEnabledByDefaultForAccount.
// Precedence: account row → tenant row → default enabled. Fail-open on DB error.
func IsFeatureEnabledByDefaultForAccount(feature string, tenantId string, accountId string) (bool, error) {
	if tenantId == "" {
		return true, nil
	}
	dbManager, err := GetDatabaseManager(Metastore)
	if err != nil {
		return true, err
	}

	if accountId != "" {
		var status string
		err = dbManager.Db.Get(&status,
			"SELECT status FROM feature_flag WHERE feature_id = $1 AND tenant_id = $2 AND account_id = $3",
			feature, tenantId, accountId)
		if err == nil {
			return status != "disabled", nil
		}
	}

	var status string
	err = dbManager.Db.Get(&status,
		"SELECT status FROM feature_flag WHERE feature_id = $1 AND tenant_id = $2 AND account_id IS NULL",
		feature, tenantId)
	if err != nil {
		return true, nil
	}
	return status != "disabled", nil
}
