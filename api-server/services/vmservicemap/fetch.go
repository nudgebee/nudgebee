package vmservicemap

import (
	"encoding/json"
	"fmt"
	"time"

	"nudgebee/services/relay"
)

const (
	forwardAction = "vm_service_map"
	fetchTimeoutS = 60
	DefaultWindow = 15 * time.Minute
	MaxWindow     = 24 * time.Hour
)

// proxyFunc is relay.ExecuteProxy, injectable for tests.
type proxyFunc func(accountID, action, datasourceID string, params map[string]any, timeoutSeconds int, agentType string) (map[string]any, error)

// Fetch asks the account's forager for the map data over [start, end]. store
// names the datasource the forager holds for the account's metrics store.
func Fetch(accountID string, store Store, start, end time.Time) (Response, error) {
	return fetch(relay.ExecuteProxyWithAgentType, accountID, store, start, end)
}

func fetch(call proxyFunc, accountID string, store Store, start, end time.Time) (Response, error) {
	result, err := call(accountID, forwardAction, store.DatasourceKey, map[string]any{
		"start": start.UTC().Format(time.RFC3339),
		"end":   end.UTC().Format(time.RFC3339),
	}, fetchTimeoutS, store.AgentType)
	if err != nil {
		return Response{}, fmt.Errorf("vmservicemap: %s failed: %w", forwardAction, err)
	}
	return parseResult(result)
}

// parseResult decodes relay.ExecuteProxy's unwrapped map. The proxy agent
// answers either {"result": {...}} or the bare object, depending on the action.
func parseResult(result map[string]any) (Response, error) {
	body := any(result)
	if inner, ok := result["result"]; ok {
		body = inner
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return Response{}, fmt.Errorf("vmservicemap: re-marshal response: %w", err)
	}
	var resp Response
	if err := json.Unmarshal(raw, &resp); err != nil {
		return Response{}, fmt.Errorf("vmservicemap: parse response: %w", err)
	}
	return resp, nil
}
