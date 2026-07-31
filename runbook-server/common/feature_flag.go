package common

import (
	"database/sql"
	"errors"
	"log/slog"
)

// FeatureAIWorkflowTools gates AI (Nubi) invocation of automations: discovery of
// opted-in workflows and every AI-originated trigger. Registered in the
// public.feature catalog by the workflow_ai_invocation migration and enabled per
// tenant or per account with a public.feature_flag row:
//
//	INSERT INTO public.feature_flag (feature_id, tenant_id, status)
//	VALUES ('AI_WORKFLOW_TOOLS', '<tenant-uuid>', 'enabled');
//
// Registering the feature does not enable it — see IsFeatureEnabledForAccount.
const FeatureAIWorkflowTools = "AI_WORKFLOW_TOOLS"

// IsFeatureEnabledForAccount reports whether `feature` is enabled for an
// account, falling back to its tenant.
//
// Precedence matches llm-server's reader of the same table
// (llm/llm-server/common/feature_flag.go): an account-scoped row wins, then a
// tenant-wide row, otherwise disabled.
//
// Opt-in and FAIL-CLOSED: no row, a status other than 'enabled', a metastore
// that cannot be reached, or a database that has not run the migration yet all
// resolve to false. Callers gating a side effect want the gate shut when the
// answer is unknown, so an error is returned alongside false for logging — but
// the boolean alone is always safe to act on.
func IsFeatureEnabledForAccount(feature, tenantID, accountID string) (bool, error) {
	dbManager, err := GetDatabaseManager(Metastore)
	if err != nil {
		return false, err
	}
	return IsFeatureEnabledForAccountWithDB(dbManager, feature, tenantID, accountID)
}

// IsFeatureEnabledForAccountWithDB is IsFeatureEnabledForAccount against an
// explicit database manager (mirrors llm-server's IsFeatureEnabledWithDB).
func IsFeatureEnabledForAccountWithDB(dbManager *DatabaseManager, feature, tenantID, accountID string) (bool, error) {
	// An uninitialized manager would otherwise panic here. Panicking is not
	// failing closed — it takes down the request instead of denying it — so the
	// gate reports "disabled, and here is why".
	if dbManager == nil || dbManager.Db == nil {
		return false, errors.New("feature flag lookup: database manager is not initialized")
	}

	if accountID != "" {
		enabled, err := featureFlagEnabled(dbManager,
			"SELECT status FROM feature_flag WHERE feature_id = $1 AND account_id = $2::uuid",
			feature, accountID)
		if err != nil {
			return false, err
		}
		if enabled {
			return true, nil
		}
	}

	if tenantID == "" {
		return false, nil
	}
	return featureFlagEnabled(dbManager,
		"SELECT status FROM feature_flag WHERE feature_id = $1 AND tenant_id = $2::uuid AND account_id IS NULL",
		feature, tenantID)
}

// IsFeatureEnabledForAccountOrLog is the convenience form for call sites that
// only branch on the boolean. It logs the lookup error (so a misconfigured
// metastore is visible in logs rather than silently disabling the feature) and
// returns false.
func IsFeatureEnabledForAccountOrLog(feature, tenantID, accountID string) bool {
	enabled, err := IsFeatureEnabledForAccount(feature, tenantID, accountID)
	if err != nil {
		slog.Error("feature flag lookup failed; treating feature as disabled",
			"feature", feature, "tenant_id", tenantID, "account_id", accountID, "error", err)
		return false
	}
	return enabled
}

func featureFlagEnabled(dbManager *DatabaseManager, query, feature, scopeID string) (bool, error) {
	var status string
	err := dbManager.Db.Get(&status, query, feature, scopeID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return status == "enabled", nil
}
