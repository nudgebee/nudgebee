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
