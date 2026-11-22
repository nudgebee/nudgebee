package core

import (
	"fmt"

	"nudgebee/services/internal/database"
	"nudgebee/services/security"
)

// IsForagerRegisteredDatasource reports whether an integration row is a datasource the
// account's forager registered from its own config file. The relay files those under
// source=agent, like the Kubernetes agent's own provider rows, but under the datasource's
// own name, in connection_mode=vm_agent and with the key the forager knows it by. Such a
// row is reached through the forager like a hand-added vm_agent connection, so a
// server-side client can use it; the agent's own row is not.
//
// Both the config rows are required, not the name alone: a name that differs from the
// type says nothing about how the row is reached.
func IsForagerRegisteredDatasource(dto IntegrationDto) (bool, error) {
	if dto.Source != "agent" || dto.Name == dto.Type {
		return false, nil
	}
	// A row listed with its config (the loaders') answers from it; one listed without
	// (the resolver's) is read.
	if len(dto.Configs) > 0 {
		var vmAgentMode, hasKey bool
		for _, c := range dto.Configs {
			switch {
			case c.Name == "connection_mode" && c.Value == "vm_agent":
				vmAgentMode = true
			case c.Name == "datasource_key" && c.Value != "":
				hasKey = true
			}
		}
		return vmAgentMode && hasKey, nil
	}
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return false, fmt.Errorf("integrations: database manager: %w", err)
	}
	var registered bool
	err = dbms.Db.Get(&registered, `
		SELECT EXISTS (
			SELECT 1 FROM integration_config_values
			WHERE integration_id = $1::uuid AND name = 'connection_mode' AND value = 'vm_agent'
		) AND EXISTS (
			SELECT 1 FROM integration_config_values
			WHERE integration_id = $1::uuid AND name = 'datasource_key' AND value <> ''
		)`, dto.Id)
	if err != nil {
		return false, fmt.Errorf("integrations: read forager registration of %s: %w", dto.Id, err)
	}
	return registered, nil
}

// PickServerReachableIntegration chooses, among an account's integrations of one type,
// the one a server-side client connects to: a hand-added row, or a datasource the
// forager registered. The agent's own rows are skipped, since they carry no URL or
// credentials and are served through the Kubernetes agent instead.
//
// When more than one row is reachable, disabled rows are dropped (the config listing
// returns them) and the account's default for the signal (defaultConfigName, e.g.
// DefaultMetricsProvider) wins if it is among the rest; otherwise the first hand-added
// row, then the first forager-registered one. Taking the default keeps the client on the
// integration the provider resolver and the status cards already name. With a single
// reachable row nothing else is read.
func PickServerReachableIntegration(sc *security.RequestContext, accountId, defaultConfigName string, dtos []IntegrationDto) (IntegrationDto, bool, error) {
	activeIDs := func() (map[string]bool, error) {
		active, err := listActiveIntegrations(sc, accountId)
		if err != nil {
			return nil, fmt.Errorf("integrations: list active integrations: %w", err)
		}
		ids := make(map[string]bool, len(active))
		for _, a := range active {
			ids[a.Id] = true
		}
		return ids, nil
	}
	defaultID := func() (string, error) {
		def, err := GetIntegrationByConfigNameValues(sc, accountId, defaultConfigName, "true")
		if err != nil {
			return "", fmt.Errorf("integrations: read the account's default %s: %w", defaultConfigName, err)
		}
		if def == nil {
			return "", nil
		}
		return def.Id, nil
	}
	return pickServerReachable(dtos, activeIDs, defaultID, IsForagerRegisteredDatasource)
}

// listActiveIntegrations is ListActiveIntegrationsForAccount, indirected so a test needs no database.
var listActiveIntegrations = ListActiveIntegrationsForAccount

// pickServerReachable is PickServerReachableIntegration with its lookups passed in. activeIDs
// and defaultID are only called when more than one integration is reachable, so the common
// single-integration account pays for no extra read on each metrics or logs query.
func pickServerReachable(
	dtos []IntegrationDto,
	activeIDs func() (map[string]bool, error),
	defaultID func() (string, error),
	isForagerRegistered func(IntegrationDto) (bool, error),
) (IntegrationDto, bool, error) {
	type candidate struct {
		dto  *IntegrationDto
		user bool
	}
	var candidates []candidate
	for i := range dtos {
		dto := &dtos[i]
		switch dto.Source {
		case "user":
			candidates = append(candidates, candidate{dto, true})
		case "agent":
			registered, err := isForagerRegistered(*dto)
			if err != nil {
				return IntegrationDto{}, false, err
			}
			if registered {
				candidates = append(candidates, candidate{dto, false})
			}
		}
	}

	if len(candidates) > 1 {
		active, err := activeIDs()
		if err != nil {
			return IntegrationDto{}, false, err
		}
		// A row with no id was never persisted (a config override's synthetic record), so
		// it is not in the active set and is not disabled either.
		var enabled []candidate
		for _, c := range candidates {
			if c.dto.Id == "" || active[c.dto.Id] {
				enabled = append(enabled, c)
			}
		}
		// Every candidate disabled: keep them rather than report none, as a single
		// disabled row is used.
		if len(enabled) > 0 {
			candidates = enabled
		}
	}

	if len(candidates) > 1 {
		id, err := defaultID()
		if err != nil {
			return IntegrationDto{}, false, err
		}
		if id != "" {
			for _, c := range candidates {
				if c.dto.Id == id {
					return *c.dto, true, nil
				}
			}
		}
	}

	for _, c := range candidates {
		if c.user {
			return *c.dto, true, nil
		}
	}
	if len(candidates) > 0 {
		return *candidates[0].dto, true, nil
	}
	return IntegrationDto{}, false, nil
}
