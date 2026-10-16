package tasks

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"nudgebee/runbook/common"
	"nudgebee/runbook/internal/tasks/types"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/samber/lo"
)

// IntegrationUse is an integration a step will use.
type IntegrationUse struct {
	// Ref is the integration's id or name as the step names it; empty when the
	// step picks an integration of Types itself.
	Ref string
	// Types are the integration types the step accepts; empty when any.
	Types []string
	// Optional: Ref may name no integration at all, so a Ref the tenant does
	// not have is not refused (GitOps repository_name is the integration's
	// name in HandleGitOpsOrTicket and a repository in pv_rightsize).
	Optional bool
}

// IntegrationUses lists the integrations a step's params name: every
// integration-typed field of the task's input schema (nested objects
// included, skipping one whose `enabled` is false), plus the GitOps and ticket
// settings the k8s change steps read without declaring them.
func IntegrationUses(schema *types.Schema, params map[string]any) []IntegrationUse {
	var uses []IntegrationUse
	if schema != nil {
		uses = schemaIntegrationUses(schema, params)
	}
	// GitOps opens a pull request through an integration of `provider`
	// (GitHub unless set). Which of integration_id, name and repository_name
	// picks it differs by task (HandleGitOpsOrTicket prefers the names,
	// pv_rightsize reads only the id), so each one given is checked. With none,
	// the pull request goes through a GitHub integration whatever the provider.
	if cfg, ok := enabledConfig(params, "gitops_config"); ok {
		provider := stringParam(cfg, "provider")
		if provider == "" {
			provider = "github"
		}
		named := false
		for _, key := range []string{"integration_id", "name", "repository_name"} {
			if ref := stringParam(cfg, key); ref != "" {
				uses = append(uses, IntegrationUse{Ref: ref, Types: []string{provider}, Optional: key == "repository_name"})
				named = named || key != "repository_name"
			}
		}
		if !named {
			uses = append(uses, IntegrationUse{Types: lo.Uniq([]string{provider, "github"})})
		}
	}
	// A ticket in place of the change, on the integration configuration_id.
	if cfg, ok := enabledConfig(params, "ticket_config"); ok {
		uses = append(uses, IntegrationUse{Ref: stringParam(cfg, "configuration_id")})
	}
	return uses
}

func schemaIntegrationUses(schema *types.Schema, params map[string]any) []IntegrationUse {
	var uses []IntegrationUse
	for name, prop := range schema.Properties {
		switch prop.Type {
		case types.PropertyTypeIntegration, types.PropertyTypeTicket:
			if ref := stringParam(params, name); ref != "" {
				uses = append(uses, IntegrationUse{Ref: ref, Types: propertyIntegrationTypes(prop)})
			}
		default:
			nested, ok := params[name].(map[string]any)
			if !ok || prop.Schema == nil || name == "gitops_config" || name == "ticket_config" {
				continue
			}
			if enabled, set := nested["enabled"].(bool); set && !enabled {
				continue
			}
			uses = append(uses, schemaIntegrationUses(prop.Schema, nested)...)
		}
	}
	return uses
}

func propertyIntegrationTypes(prop types.Property) []string {
	if len(prop.SubTypes) > 0 {
		return prop.SubTypes
	}
	if prop.SubType != "" {
		return []string{prop.SubType}
	}
	return nil
}

func enabledConfig(params map[string]any, key string) (map[string]any, bool) {
	cfg, ok := params[key].(map[string]any)
	if !ok {
		return nil, false
	}
	enabled, _ := cfg["enabled"].(bool)
	return cfg, enabled
}

func stringParam(params map[string]any, key string) string {
	s, _ := params[key].(string)
	return strings.TrimSpace(s)
}

// errIntegrationNotFound: the step names an integration the tenant does not have.
var errIntegrationNotFound = errors.New("integration not found")

// integrationTypes is the stored types a use may resolve to in the tenant: the
// type of the integration with that id; every type of an enabled integration
// with that name (among use.Types, when set), as a name can match more than
// one; or use.Types when the step names none. A variable so tests need no
// metastore.
var integrationTypes = func(ctx context.Context, tenantID string, use IntegrationUse) ([]string, error) {
	if use.Ref == "" {
		return use.Types, nil
	}
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil, err
	}
	var query string
	args := []any{tenantID, use.Ref}
	if _, err := uuid.Parse(use.Ref); err == nil {
		query = `SELECT type FROM integrations WHERE tenant_id = $1 AND id = $2::uuid`
	} else {
		query = `SELECT DISTINCT type FROM integrations
			WHERE tenant_id = $1 AND LOWER(name) = LOWER($2) AND status = 'enabled'
			  AND (COALESCE(cardinality($3::text[]), 0) = 0 OR type = ANY($3))`
		args = append(args, pq.Array(use.Types))
	}
	rows, err := dbManager.Db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var found []string
	for rows.Next() {
		var typ string
		if err := rows.Scan(&typ); err != nil {
			return nil, err
		}
		found = append(found, typ)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(found) == 0 {
		return nil, fmt.Errorf("%w: %q", errIntegrationNotFound, use.Ref)
	}
	return found, nil
}
