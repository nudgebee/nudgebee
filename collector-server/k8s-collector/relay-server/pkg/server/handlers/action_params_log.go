package handlers

import (
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
)

// paramsLoggedInFull lists the actions whose params are written to the logs
// as-is: read-only lookups whose params are resource names, time ranges and
// telemetry queries, which is what says which lookup the agent failed to answer.
//
// Every other action is logged as its param names and size only. Their values
// can hold credentials (a shell command with a password typed into it, the
// auth_credentials of an MCP call) and a log line is read far more widely than
// the request that produced it. The full request is still reachable from the
// caller's side through corr and trace_id.
var paramsLoggedInFull = map[string]struct{}{
	"get_resource":                {},
	"logs_enricher":               {},
	"prometheus_queries_enricher": {},
	"query_data":                  {},
	"service_map":                 {},
}

// loggableActionParams returns what a log line may carry for an action's params.
func loggableActionParams(actionName string, params gjson.Result) string {
	if _, ok := paramsLoggedInFull[actionName]; ok {
		return params.String()
	}
	var keys []string
	if params.IsObject() {
		params.ForEach(func(key, _ gjson.Result) bool {
			keys = append(keys, key.String())
			return true
		})
	}
	return "keys=[" + strings.Join(keys, " ") + "] bytes=" + strconv.Itoa(len(params.Raw))
}
