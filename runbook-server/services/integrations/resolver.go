package integrations

import (
	"database/sql"
	"errors"
	"fmt"
	"nudgebee/runbook/common"
	"nudgebee/runbook/services/security"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// ResolveIntegrationID maps a frontend-provided integration_id value to a real
// integration UUID. The value is whatever the workflow templating engine
// produced at run time — most often a literal UUID, but it may also be a name
// (when the user templated `{{ Inputs.foo }}` on the integration dropdown).
//
// expectedTypes scopes the lookup to integration types this caller cares about
// — e.g. ticket tasks pass the set of supported ticket platforms. Without that
// scope a name like "prod" could ambiguously match across unrelated types.
//
// Behavior:
//   - empty value → returns "" with no error (callers that require it perform
//     the "is required" check themselves, matching existing patterns).
//   - value is a UUID → returned unchanged when the tenant has an integration
//     with that id whose type is one of expectedTypes (any type when
//     expectedTypes is empty). A deleted integration, one from another tenant,
//     or one of the wrong kind (a webhook passed where a ServiceNow integration
//     is expected) returns an error instead of failing later inside the step.
//   - value is a name → resolved to the matching enabled integration's UUID
//     within (tenant, expectedTypes). Zero or multiple matches return errors.
//
// The errors are written to be shown to the person who built the automation:
// they say whether the account has no integration of the needed kind at all,
// which integrations of that kind it does have, or what kind the given one is.
func ResolveIntegrationID(ctx *security.RequestContext, value string, expectedTypes []string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}

	_, uuidErr := uuid.Parse(value)
	isUUID := uuidErr == nil

	if !isUUID && len(expectedTypes) == 0 {
		return "", errors.New("integration name resolution requires at least one expected integration type")
	}

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return "", fmt.Errorf("unable to connect to database: %w", err)
	}

	tenantId := ctx.GetSecurityContext().GetTenantId()

	if isUUID {
		var found struct {
			Name string `db:"name"`
			Type string `db:"type"`
		}
		err := dbms.QueryRowAndScan(&found,
			`SELECT name, type FROM integrations WHERE id = $1 AND tenant_id = $2`, value, tenantId)
		if errors.Is(err, sql.ErrNoRows) {
			return "", missingIntegrationError(dbms, tenantId, value, expectedTypes)
		}
		if err != nil {
			return "", fmt.Errorf("failed to look up integration %s: %w", value, err)
		}
		if len(expectedTypes) > 0 && !slices.Contains(expectedTypes, found.Type) {
			return "", wrongKindError(found.Name, found.Type, expectedTypes)
		}
		return value, nil
	}

	rows, err := dbms.Query(
		`SELECT id::text FROM integrations
		 WHERE tenant_id = $1
		   AND LOWER(name) = LOWER($2)
		   AND type = ANY($3)
		   AND status = 'enabled'`,
		tenantId, value, pq.Array(expectedTypes),
	)
	if err != nil {
		return "", fmt.Errorf("failed to look up integration by name: %w", err)
	}
	defer func() { _ = rows.Close() }()

	matches := make([]string, 0, 1)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return "", fmt.Errorf("failed to scan integration id: %w", err)
		}
		matches = append(matches, id)
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("error iterating integration rows: %w", err)
	}

	switch len(matches) {
	case 0:
		return "", missingIntegrationError(dbms, tenantId, value, expectedTypes)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("integration name '%s' is ambiguous (matches %d integrations); use the integration UUID instead", value, len(matches))
	}
}

// missingIntegrationError explains why value matched no enabled integration of expectedTypes. It
// runs only on the failure path, so the extra lookups cost nothing for a working step. A lookup
// that fails is returned as an error, never reported as a missing integration.
func missingIntegrationError(dbms *common.DatabaseManager, tenantId, value string, expectedTypes []string) error {
	if len(expectedTypes) == 0 {
		return fmt.Errorf("integration '%s' not found on this account", value)
	}

	// The name exists, but disabled or as another kind of integration.
	var named struct {
		Type   string `db:"type"`
		Status string `db:"status"`
	}
	err := dbms.QueryRowAndScan(&named,
		`SELECT type, status FROM integrations WHERE tenant_id = $1 AND LOWER(name) = LOWER($2) ORDER BY (type = ANY($3)) DESC, (status = 'enabled') DESC LIMIT 1`,
		tenantId, value, pq.Array(expectedTypes))
	switch {
	case err == nil && !slices.Contains(expectedTypes, named.Type):
		return wrongKindError(value, named.Type, expectedTypes)
	case err == nil && named.Status != "enabled":
		return fmt.Errorf("integration '%s' is disabled; enable it under Integrations", value)
	case err != nil && !errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("failed to look up integration '%s': %w", value, err)
	}

	rows, err := dbms.Query(
		`SELECT name, status FROM integrations WHERE tenant_id = $1 AND type = ANY($2) ORDER BY name LIMIT 20`,
		tenantId, pq.Array(expectedTypes))
	if err != nil {
		return fmt.Errorf("failed to list %s integrations: %w", kindList(expectedTypes), err)
	}
	defer func() { _ = rows.Close() }()
	var enabled, disabled []string
	for rows.Next() {
		var name, status string
		if err := rows.Scan(&name, &status); err != nil {
			return fmt.Errorf("failed to read %s integrations: %w", kindList(expectedTypes), err)
		}
		if status == "enabled" {
			enabled = append(enabled, name)
		} else {
			disabled = append(disabled, name)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("failed to read %s integrations: %w", kindList(expectedTypes), err)
	}

	switch {
	case len(enabled) > 0:
		return fmt.Errorf("integration '%s' not found on this account; %s integrations it has: %s",
			value, kindList(expectedTypes), strings.Join(enabled, ", "))
	case len(disabled) > 0:
		return fmt.Errorf("integration '%s' not found on this account; its %s integrations are disabled: %s — enable one under Integrations",
			value, kindList(expectedTypes), strings.Join(disabled, ", "))
	default:
		return fmt.Errorf("this account has no %s integration connected", kindList(expectedTypes))
	}
}

func wrongKindError(name, integrationType string, expectedTypes []string) error {
	return fmt.Errorf("integration '%s' is a %s integration; this step needs a %s integration", name, integrationType, kindList(expectedTypes))
}

// kindList renders integration types for a person: "jira", "jira or github", "jira, github or gitlab".
func kindList(types []string) string {
	if len(types) <= 1 {
		return strings.Join(types, "")
	}
	return strings.Join(types[:len(types)-1], ", ") + " or " + types[len(types)-1]
}
