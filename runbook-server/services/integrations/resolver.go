package integrations

import (
	"errors"
	"fmt"
	"nudgebee/runbook/common"
	"nudgebee/runbook/services/security"
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
//   - value is a UUID → returned unchanged. No DB hit, so existing workflows
//     that store UUIDs see zero overhead.
//   - value is a name → resolved to the matching enabled integration's UUID
//     within (tenant, expectedTypes). Zero or multiple matches return errors.
func ResolveIntegrationID(ctx *security.RequestContext, value string, expectedTypes []string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}

	if _, err := uuid.Parse(value); err == nil {
		return value, nil
	}

	if len(expectedTypes) == 0 {
		return "", errors.New("integration name resolution requires at least one expected integration type")
	}

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return "", fmt.Errorf("unable to connect to database: %w", err)
	}

	tenantId := ctx.GetSecurityContext().GetTenantId()

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
		return "", fmt.Errorf("integration with name '%s' not found for type(s) %v", value, expectedTypes)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("integration name '%s' is ambiguous (matches %d integrations); use the integration UUID instead", value, len(matches))
	}
}
