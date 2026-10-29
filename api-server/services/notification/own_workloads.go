package notification

import (
	"strings"

	"nudgebee/services/internal/database/models"

	"github.com/jmoiron/sqlx"
)

// NudgebeeWorkloadsAttr is the cloud_account_attrs key for the account's
// "NudgeBee's own workloads" setting (see account.validateNudgebeeWorkloadsAttribute):
// {"hide_in_notifications": bool, "namespaces": [...]}. Hiding is on unless
// hide_in_notifications is false, so an account with no row hides its agent's
// namespace.
const NudgebeeWorkloadsAttr = "nudgebee_workloads"

// OwnNamespacesSQL lists, per account, the namespaces NudgeBee's own workloads
// run in, which notifications leave out: the namespace each Kubernetes agent
// reports it is installed in, plus the namespaces listed in the account's
// setting. Accounts that turned hiding off list nothing. Columns:
// cloud_account_id (uuid), namespace (text). Only notifications use it; the app
// still shows these workloads.
//
// Other attributes hold values that are not JSON, and Postgres may evaluate
// WHERE conditions in any order, so every cast sits behind a CASE on the
// attribute's name.
const OwnNamespacesSQL = `
	SELECT a.cloud_account_id, a.connection_status->>'installationNamespace' AS namespace
	FROM agent a
	WHERE a.type = 'k8s'
	  AND COALESCE(a.connection_status->>'installationNamespace', '') <> ''
	  AND NOT EXISTS (
	      SELECT 1 FROM cloud_account_attrs s
	      WHERE s.cloud_account_id = a.cloud_account_id AND s.name = '` + NudgebeeWorkloadsAttr + `'
	        AND (CASE WHEN s.name = '` + NudgebeeWorkloadsAttr + `' THEN s.value::jsonb END)->>'hide_in_notifications' = 'false')
	UNION
	SELECT s.cloud_account_id, listed.namespace
	FROM cloud_account_attrs s
	CROSS JOIN LATERAL (SELECT CASE WHEN s.name = '` + NudgebeeWorkloadsAttr + `' THEN s.value::jsonb END AS v) settings
	CROSS JOIN LATERAL jsonb_array_elements_text(CASE
	    WHEN jsonb_typeof(settings.v->'namespaces') = 'array'
	     AND COALESCE(settings.v->>'hide_in_notifications', 'true') <> 'false'
	    THEN settings.v->'namespaces' ELSE '[]'::jsonb END) AS listed(namespace)
	WHERE s.name = '` + NudgebeeWorkloadsAttr + `'`

// RecommendationNamespaceSQL is the Kubernetes namespace of the recommendation
// aliased as r, or NULL for one that has none (cloud resources). Rightsizing
// payloads are keyed by container and carry no namespace, so the last fallback
// is the "namespace/..." prefix of account_object_id, taken only when it is a
// valid namespace name; cloud ids (ARNs) contain colons and never match.
const RecommendationNamespaceSQL = `COALESCE(r.recommendation->'metadata'->>'namespace', r.recommendation->>'namespace', ` + models.PopeyeNamespaceSQL + `,
	substring(r.account_object_id from '^([a-z0-9](?:[-a-z0-9]*[a-z0-9])?)/'))`

// NotOwnRecommendationSQL is a WHERE condition that keeps out recommendations
// (aliased as r) on NudgeBee's own workloads.
const NotOwnRecommendationSQL = `NOT EXISTS (SELECT 1 FROM (` + OwnNamespacesSQL + `) own
	WHERE own.cloud_account_id = r.cloud_account_id AND own.namespace = ` + RecommendationNamespaceSQL + `)`

// NotOwnImageSQL returns a WHERE condition that keeps out image-scan findings
// (recommendation aliased as r), which carry an image rather than a namespace,
// when every active pod running the image on that account is in one of its own
// namespaces. An image a customer workload also runs stays. tenantExpr is the
// tenant the query reads (a parameter, or r.tenant_id for a single row), so the
// set of such images is built once per query, not once per finding.
func NotOwnImageSQL(tenantExpr string) string {
	return `NOT COALESCE(r.rule_name IN ('image_scan', 'image_scan_summary') AND (r.cloud_account_id, r.recommendation->>'image_name') IN (
		SELECT p.cloud_account_id, container->>'image'
		FROM k8s_pods p
		CROSS JOIN LATERAL jsonb_array_elements(CASE WHEN jsonb_typeof(p.meta->'config'->'containers') = 'array'
		    THEN p.meta->'config'->'containers' ELSE '[]'::jsonb END) AS container
		LEFT JOIN (` + OwnNamespacesSQL + `) own ON own.cloud_account_id = p.cloud_account_id AND own.namespace = p.namespace
		WHERE p.tenant_id = ` + tenantExpr + ` AND p.is_active IS NOT FALSE
		GROUP BY 1, 2
		HAVING bool_and(own.namespace IS NOT NULL)), false)`
}

// NotOwnEventSQL returns a WHERE condition that keeps out events (aliased as
// alias) on NudgeBee's own workloads.
func NotOwnEventSQL(alias string) string {
	return `NOT EXISTS (SELECT 1 FROM (` + OwnNamespacesSQL + `) own
	WHERE own.cloud_account_id = ` + alias + `.cloud_account_id AND own.namespace = ` + alias + `.subject_namespace)`
}

// OwnNamespacesByAccount returns the namespaces notifications leave out on
// each of the tenant's accounts, for queries that cannot join OwnNamespacesSQL.
func OwnNamespacesByAccount(db *sqlx.DB, tenantID string) (map[string][]string, error) {
	var rows []struct {
		AccountID string `db:"account_id"`
		Namespace string `db:"namespace"`
	}
	err := db.Select(&rows, `SELECT own.cloud_account_id::text AS account_id, own.namespace
		FROM (`+OwnNamespacesSQL+`) own
		JOIN cloud_accounts ca ON ca.id = own.cloud_account_id
		WHERE ca.tenant = $1::uuid
		ORDER BY 1, 2`, tenantID)
	if err != nil {
		return nil, err
	}
	byAccount := make(map[string][]string)
	for _, row := range rows {
		byAccount[row.AccountID] = append(byAccount[row.AccountID], row.Namespace)
	}
	return byAccount, nil
}

// isOwnWorkload reports whether notifications leave out namespace on
// accountID. It fails open: on an error the notification is sent.
func isOwnWorkload(db *sqlx.DB, accountID, namespace string) (bool, error) {
	if accountID == "" || strings.TrimSpace(namespace) == "" {
		return false, nil
	}
	var own bool
	err := db.Get(&own, `SELECT EXISTS (SELECT 1 FROM (`+OwnNamespacesSQL+`) own
		WHERE own.cloud_account_id = $1::uuid AND own.namespace = $2)`, accountID, namespace)
	return own, err
}

// isOwnRecommendation reports whether notifications leave out the
// recommendation with this id.
func isOwnRecommendation(db *sqlx.DB, recommendationID string) (bool, error) {
	var own bool
	err := db.Get(&own, `SELECT EXISTS (SELECT 1 FROM recommendation r WHERE r.id = $1::uuid
		AND NOT (`+NotOwnRecommendationSQL+` AND `+NotOwnImageSQL("r.tenant_id")+`))`, recommendationID)
	return own, err
}
