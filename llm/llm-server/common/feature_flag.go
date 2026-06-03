package common

func IsFeatureEnabled(feature string, tenant string) (bool, error) {
	dbManager, err := GetDatabaseManager(Metastore)
	if err != nil {
		return false, err
	}

	return IsFeatureEnabledWithDB(dbManager, feature, tenant)
}

// IsFeatureEnabledWithDB checks if a feature is enabled for a tenant using the provided database manager
func IsFeatureEnabledWithDB(dbManager *DatabaseManager, feature string, tenant string) (bool, error) {
	rows, err := dbManager.Db.Queryx("SELECT tenant_id FROM feature_flag WHERE feature_id = $1 and status = 'enabled' and tenant_id = $2", feature, tenant)
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

func IsFeatureEnabledForAccount(feature string, tenantId string, accountId string) (bool, error) {
	dbManager, err := GetDatabaseManager(Metastore)
	if err != nil {
		return false, err
	}

	// Step 1: Check if feature is enabled at account level
	rows, err := dbManager.Db.Queryx("SELECT account_id FROM feature_flag WHERE feature_id = $1 AND status = 'enabled' AND account_id = $2", feature, accountId)
	if err != nil {
		return false, err
	}
	defer func() {
		_ = rows.Close()
	}()

	// If found at account level, feature is enabled
	if rows.Next() {
		return true, nil
	}

	// Step 2: Feature not enabled at account level, check tenant level fallback (applies to all accounts)
	return IsFeatureEnabledWithDB(dbManager, feature, tenantId)
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
