package workflow

import (
	"fmt"
	"regexp"
	"strings"

	"nudgebee/runbook/common"
	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/internal/tasks/tickets"
	"nudgebee/runbook/internal/tasks/types"
	"nudgebee/runbook/services/integrations"
	"nudgebee/runbook/services/security"
)

// loneConfigRef matches a value that is exactly one `{{ Configs.<key> }}` reference.
var loneConfigRef = regexp.MustCompile(`^\{\{\s*Configs\.([A-Za-z0-9_.-]+)\s*\}\}$`)

// resolveIntegrationParam is the integration lookup ResolveIntegrationID performs, as a seam
// for tests.
var resolveIntegrationParam = integrations.ResolveIntegrationID

// integrationKinds returns the integration types a ticket- or integration-typed parameter
// accepts. Ticket parameters that declare none fall back to every ticket platform, the set the
// ticket steps accept at run time. An integration parameter that declares none is not checked.
func integrationKinds(prop types.Property) []string {
	if len(prop.SubTypes) > 0 {
		return prop.SubTypes
	}
	if prop.SubType != "" {
		return []string{prop.SubType}
	}
	if prop.Type == types.PropertyTypeTicket {
		return tickets.TicketPlatforms()
	}
	return nil
}

// validateIntegrationParam checks that a ticket- or integration-typed parameter names an
// integration this tenant has, of a kind the step accepts. A lone `{{ Configs.<key> }}` is
// checked through the config's stored value: it is a literal in the database, not something only
// known at run time. A missing config, or any other template, is left to run time, where
// ResolveIntegrationID makes the same check.
//
// It returns the resolved integration id, or "" when nothing was checked.
func validateIntegrationParam(ctx *security.RequestContext, owner, paramName string, prop types.Property, value string, configValue func(key string) (string, bool)) (string, error) {
	kinds := integrationKinds(prop)
	if len(kinds) == 0 {
		return "", nil
	}

	literal, source := value, ""
	if isTemplated(value) {
		m := loneConfigRef.FindStringSubmatch(strings.TrimSpace(value))
		if m == nil {
			return "", nil
		}
		v, ok := configValue(m[1])
		if !ok || strings.TrimSpace(v) == "" {
			return "", nil
		}
		literal, source = v, fmt.Sprintf(" (set from config '%s')", m[1])
	}

	id, err := resolveIntegrationParam(ctx, literal, kinds)
	if err != nil {
		return "", common.ErrorBadRequest(fmt.Sprintf("%s parameter '%s'%s validation failed: %v", owner, paramName, source, err))
	}
	return id, nil
}

// nonSecretConfigValues returns a lazy key → value lookup over the account's configs. Secret
// configs are left out so their values can never reach a validation error. A config API failure
// reads as "no such config", which skips the check rather than failing the save.
func (s *Service) nonSecretConfigValues(ctx *security.RequestContext, accountID string) func(key string) (string, bool) {
	var values map[string]string
	return func(key string) (string, bool) {
		if values == nil {
			values = map[string]string{}
			if s.configService != nil {
				tenantID := ctx.GetSecurityContext().GetTenantId()
				configs, err := s.configService.ListConfigsDecrypted(ctx.GetContext(), tenantID, &accountID, nil)
				if err != nil {
					ctx.GetLogger().Warn("integration check: could not read configs, skipping config-backed integration params", "error", err)
				}
				for _, cfg := range configs {
					if cfg.Type != model.ConfigTypeSecret {
						values[cfg.Key] = cfg.Value
					}
				}
			}
		}
		v, ok := values[key]
		return v, ok
	}
}
