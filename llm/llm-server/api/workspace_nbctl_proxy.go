package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/workspace"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// WorkspaceNbctlPath is where nbctl in Nubi's workspace sends its GraphQL; the
// workspace sets NUDGEBEE_ENDPOINT to the llm-server URL plus the prefix, and
// nbctl appends /api/graphql.
const WorkspaceNbctlPath = "/api/v1/workspace/nbctl"

// nbctlAction is one action nbctl may run from the workspace: the
// services-server RPC that serves it and the roles the app requires for it
// (app/src/lib/actions.yaml), so a role that cannot run an action in the app
// cannot run it through Nubi either.
type nbctlAction struct {
	rpcPath string
	module  string
	roles   []string
	// fields is the shape of the `request` argument nbctl sends for this
	// action: a key maps to true for a value passed as is, or to a nested
	// field set. Any other key is refused, so a hand-written request cannot
	// reach backend options nbctl never sets (integration ids and settings,
	// stored secrets, history writes).
	fields nbctlFields
}

type nbctlFields map[string]any

// nbctlProviderParams marks the nested `request` map that the logs and metrics
// sources read provider parameters from (Elasticsearch index and query_type,
// CloudWatch region, Observe dataset_id…). services-server treats that map as
// parameter space LLM tools fill freely, so it is passed through as is; the
// fields that are not, such as integration settings and history writes, sit at
// the top level and stay refused.
const nbctlProviderParams = "provider_params"

var nbctlMetricsRoles = []string{"account_admin_readonly", "account_admin", "tenant_admin_readonly", "tenant_admin"}
var nbctlLogsRoles = []string{"account_admin_readonly", "account_admin", "tenant_admin", "tenant_admin_readonly", "tenant_usage"}

// workspaceNbctlActions is the allowlist: read-only metrics and logs. Anything
// else nbctl asks for is refused.
var workspaceNbctlActions = map[string]nbctlAction{
	"metrics_list": {rpcPath: "/rpc/metrics", module: "metrics", roles: nbctlMetricsRoles, fields: nbctlFields{
		"account_id": true, "queries": nbctlFields{"query": true}, "instant": true,
		"start_time": true, "end_time": true, "step_interval": true, "request": nbctlProviderParams,
	}},
	"metrics_list_labels":       {rpcPath: "/rpc/metrics", module: "metrics", roles: nbctlMetricsRoles, fields: nbctlFields{"account_id": true, "metric": true}},
	"metrics_list_label_values": {rpcPath: "/rpc/metrics", module: "metrics", roles: nbctlMetricsRoles, fields: nbctlFields{"account_id": true, "label": true}},
	"metrics_list_names": {rpcPath: "/rpc/metrics", module: "metrics", roles: []string{
		"account_admin", "account_admin_readonly", "k8s_namespace_admin", "k8s_namespace_admin_readonly",
		"tenant_usage", "tenant_admin_readonly", "tenant_admin",
	}, fields: nbctlFields{"account_id": true}},
	"logs_list": {rpcPath: "/rpc/logs", module: "logs", roles: nbctlLogsRoles, fields: nbctlFields{
		"account_id": true, "query": true, "start_time": true, "end_time": true, "limit": true, "offset": true,
		"request": nbctlProviderParams, "include_raw": true,
	}},
	"logs_list_labels": {rpcPath: "/rpc/logs", module: "logs", roles: nbctlLogsRoles, fields: nbctlFields{
		"account_id": true, "request": nbctlProviderParams, "fields_only": true,
	}},
	"logs_list_label_values": {rpcPath: "/rpc/logs", module: "logs", roles: nbctlLogsRoles, fields: nbctlFields{
		"account_id": true, "label_name": true, "request": nbctlProviderParams,
	}},
}

// nbctlMaxLogLimit caps logs_list's limit, the largest page the logs tools ask for.
const nbctlMaxLogLimit = 5000

// nbctlMaxRequestBytes and nbctlMaxResponseBytes bound what one proxied call
// holds in memory.
const (
	nbctlMaxRequestBytes  = 1 << 20
	nbctlMaxResponseBytes = 32 << 20
)

// checkNbctlFields refuses any key in v that fields does not list.
func checkNbctlFields(path string, v any, fields nbctlFields) error {
	obj, ok := v.(map[string]any)
	if !ok {
		return fmt.Errorf("%s must be an object", path)
	}
	for k, item := range obj {
		allowed, ok := fields[k]
		if !ok {
			return fmt.Errorf("%s.%s is not available in Nubi's workspace", path, k)
		}
		if allowed == nbctlProviderParams {
			if _, isObject := item.(map[string]any); !isObject {
				return fmt.Errorf("%s.%s must be an object", path, k)
			}
			continue
		}
		if nested, isNested := allowed.(nbctlFields); isNested {
			if err := checkNbctlFields(path+"."+k, item, nested); err != nil {
				return err
			}
		}
	}
	return nil
}

// nbctlProviderDefaults pins query_type to "dsl" when the query is a JSON
// object and the request names no type. Only Elasticsearch reads query_type;
// its metrics source reads a JSON object as Query DSL only with that pin
// (otherwise it expects Nudgebee's own filter list, a JSON array). Deciding
// from the query keeps the request path free of the account's provider
// lookup, which can fall back to the wrong provider.
func nbctlProviderDefaults(request map[string]any) {
	query, _ := request["query"].(string)
	if queries, ok := request["queries"].(map[string]any); ok && query == "" {
		query, _ = queries["query"].(string)
	}
	var parsed any
	if json.Unmarshal([]byte(query), &parsed) != nil {
		return
	}
	if _, isObject := parsed.(map[string]any); !isObject {
		return
	}
	params, _ := request["request"].(map[string]any)
	if params == nil {
		params = map[string]any{}
		request["request"] = params
	}
	if _, ok := params["query_type"]; !ok {
		params["query_type"] = "dsl"
	}
}

// nbctlIndexParam matches an index name or pattern. Index-like provider
// parameters become part of the provider's URL path, so anything else (a "/",
// "?" or "#") could reach an endpoint other than search.
var nbctlIndexParam = regexp.MustCompile(`^[A-Za-z0-9._*,:-]+$`)

// nbctlESRefusedKeys are Elasticsearch body keys that run code or return whole
// documents outside the query's own filter (a global aggregation ignores the
// query and time range; top_hits returns full documents).
var nbctlESRefusedKeys = map[string]bool{
	"script": true, "scripts": true, "script_fields": true, "runtime_mappings": true,
	"scripted_metric": true, "global": true, "top_hits": true,
	// wrapper carries a base64-encoded query, which this check cannot see into.
	"wrapper": true,
}

// nbctlCheckProviderRequest refuses provider parameters and query bodies the
// workspace must not send: index names that are not plain index names, PPL
// (it names its own index), and JSON query bodies that run scripts or read
// past the query. It clamps a JSON body's size to the logs limit. The body
// checks do not depend on the account's provider: a provider lookup can fall
// back to the wrong answer, and PromQL, LogQL and SPL are not JSON, so only
// JSON query languages (Elasticsearch DSL, SigNoz) are inspected.
func nbctlCheckProviderRequest(request map[string]any) error {
	params, _ := request["request"].(map[string]any)
	for _, key := range []string{"index", "metric_name", "metric_index"} {
		if v, ok := params[key]; ok {
			name, isString := v.(string)
			if !isString || !nbctlIndexParam.MatchString(name) {
				return fmt.Errorf("request.request.%s must be an index name or pattern (letters, digits and . _ * , : -)", key)
			}
		}
	}
	if qt, ok := params["query_type"]; ok && qt != "dsl" && qt != "kql" {
		return fmt.Errorf("query_type %v is not available in Nubi's workspace; use dsl or kql", qt)
	}
	// Each query field is checked and clamped on its own: logs_list sends
	// query, metrics_list sends queries.query.
	if q, ok := request["query"].(string); ok {
		checked, err := nbctlCheckQueryBody(q)
		if err != nil {
			return err
		}
		request["query"] = checked
	}
	if queries, ok := request["queries"].(map[string]any); ok {
		if q, ok := queries["query"].(string); ok {
			checked, err := nbctlCheckQueryBody(q)
			if err != nil {
				return err
			}
			queries["query"] = checked
		}
	}
	return nil
}

// nbctlCheckQueryBody refuses a JSON query body that holds a refused key and
// returns it with its size clamped to the logs limit. A query that is not
// JSON (PromQL, LogQL, SPL, KQL) is returned unchanged.
func nbctlCheckQueryBody(body string) (string, error) {
	var parsed any
	if json.Unmarshal([]byte(body), &parsed) != nil {
		return body, nil
	}
	if key := nbctlFindKey(parsed, nbctlESRefusedKeys); key != "" {
		return "", fmt.Errorf("%q is not available in Nubi's workspace queries", key)
	}
	obj, isObject := parsed.(map[string]any)
	if !isObject {
		return body, nil
	}
	size, hasSize := obj["size"]
	if s, isString := size.(string); isString {
		// Elasticsearch accepts a numeric string for size.
		n, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
		if err != nil {
			return "", fmt.Errorf("size must be a number")
		}
		size = n
	} else if _, isNumber := size.(float64); hasSize && !isNumber {
		return "", fmt.Errorf("size must be a number")
	}
	if n, ok := size.(float64); !ok || n <= nbctlMaxLogLimit {
		return body, nil
	}
	obj["size"] = nbctlMaxLogLimit
	clamped, err := json.Marshal(obj)
	if err != nil {
		return "", fmt.Errorf("unable to read the query body")
	}
	return string(clamped), nil
}

// nbctlFindKey returns the first key of v, at any depth, that is in keys.
func nbctlFindKey(v any, keys map[string]bool) string {
	switch t := v.(type) {
	case map[string]any:
		for k, item := range t {
			if keys[k] {
				return k
			}
			if found := nbctlFindKey(item, keys); found != "" {
				return found
			}
		}
	case []any:
		for _, item := range t {
			if found := nbctlFindKey(item, keys); found != "" {
				return found
			}
		}
	}
	return ""
}

// nbctlMaxConcurrentPerAccount bounds how many proxied queries one account
// runs at once (per llm-server replica), so a script cannot fan a burst of
// expensive queries out at the customer's backend.
const nbctlMaxConcurrentPerAccount = 4

var nbctlInFlight = struct {
	sync.Mutex
	counts map[string]int
}{counts: map[string]int{}}

func nbctlAcquireSlot(account string) (func(), bool) {
	nbctlInFlight.Lock()
	defer nbctlInFlight.Unlock()
	if nbctlInFlight.counts[account] >= nbctlMaxConcurrentPerAccount {
		return nil, false
	}
	nbctlInFlight.counts[account]++
	var once sync.Once
	return func() {
		once.Do(func() {
			nbctlInFlight.Lock()
			defer nbctlInFlight.Unlock()
			if nbctlInFlight.counts[account]--; nbctlInFlight.counts[account] <= 0 {
				delete(nbctlInFlight.counts, account)
			}
		})
	}, true
}

// nbctlRequestTimeout keeps one proxied request inside the workspace's command
// limit (SERVER_WRITE_TIMEOUT - 2s, 58s by default) so nbctl gets an answer
// before its command is killed. It does not stop a query already running in
// services-server.
const nbctlRequestTimeout = 50 * time.Second

// nbctlBudget counts proxied requests per execution token, so a script loop
// cannot flood a customer's backend. Counted per llm-server replica.
var nbctlBudget = struct {
	sync.Mutex
	counts    map[string]int
	expiry    map[string]time.Time
	lastSweep time.Time
}{counts: map[string]int{}, expiry: map[string]time.Time{}}

// nbctlBudgetSweepEvery is how often the whole budget map is swept for
// expired tokens; the token being spent is expired on every call.
const nbctlBudgetSweepEvery = time.Minute

func nbctlSpendBudget(token string, limit int, now time.Time) bool {
	sum := sha256.Sum256([]byte(token))
	key := hex.EncodeToString(sum[:])
	nbctlBudget.Lock()
	defer nbctlBudget.Unlock()
	if exp, ok := nbctlBudget.expiry[key]; ok && now.After(exp) {
		delete(nbctlBudget.expiry, key)
		delete(nbctlBudget.counts, key)
	}
	if now.Sub(nbctlBudget.lastSweep) >= nbctlBudgetSweepEvery {
		for k, exp := range nbctlBudget.expiry {
			if now.After(exp) {
				delete(nbctlBudget.expiry, k)
				delete(nbctlBudget.counts, k)
			}
		}
		nbctlBudget.lastSweep = now
	}
	if _, ok := nbctlBudget.expiry[key]; !ok {
		nbctlBudget.expiry[key] = now.Add(10 * time.Minute)
	}
	if nbctlBudget.counts[key] >= limit {
		return false
	}
	nbctlBudget.counts[key]++
	return true
}

type nbctlGraphQLRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables"`
}

func nbctlGraphQLError(c *gin.Context, status int, message string) {
	c.JSON(status, gin.H{"errors": []gin.H{{"message": message}}})
}

// handleWorkspaceNbctlGraphQL serves nbctl's GraphQL from Nubi's workspace. It
// authenticates the per-command execution token (sent by nbctl as a Bearer),
// allows only the actions in workspaceNbctlActions, pins the account to the
// one the command may act on, applies the app's role lists, and forwards the
// action to services-server the way the app gateway does.
func handleWorkspaceNbctlGraphQL(c *gin.Context, tracer trace.Tracer, meter metric.Meter) {
	if !config.Config.LlmServerWorkspaceNbctlEnabled {
		nbctlGraphQLError(c, http.StatusNotFound, "nbctl is not enabled in Nubi's workspace")
		return
	}
	token := strings.TrimSpace(strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer "))
	if token == "" || token == c.GetHeader("Authorization") {
		nbctlGraphQLError(c, http.StatusUnauthorized, "missing bearer token")
		return
	}
	// The workspace account inside the token decides which account the auth
	// check runs against; authorizeWorkspaceRequest then verifies the token in
	// full (signature, audience, tenant, grants).
	var unverified workspace.WorkspaceTokenClaims
	if _, _, err := jwt.NewParser().ParseUnverified(token, &unverified); err != nil || unverified.AccountId == "" {
		nbctlGraphQLError(c, http.StatusUnauthorized, "invalid token")
		return
	}
	c.Request.Header.Set("X-Workspace-Token", token)
	ctx, err := authorizeWorkspaceRequest(c, unverified.AccountId, tracer, meter)
	if err != nil {
		nbctlGraphQLError(c, http.StatusUnauthorized, err.Error())
		return
	}
	// Every authenticated request spends budget, refused ones included, so a
	// loop of refused requests is capped like any other.
	if !nbctlSpendBudget(token, config.Config.LlmServerWorkspaceNbctlMaxRequests, time.Now()) {
		recordWorkspaceNbctl(c.Request.Context(), "other", "budget")
		nbctlGraphQLError(c, http.StatusTooManyRequests, fmt.Sprintf("request budget for this command used up (%d requests); run fewer queries per command", config.Config.LlmServerWorkspaceNbctlMaxRequests))
		return
	}

	var req nbctlGraphQLRequest
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, nbctlMaxRequestBytes)
	if err := c.ShouldBindJSON(&req); err != nil {
		nbctlGraphQLError(c, http.StatusBadRequest, "invalid GraphQL request body")
		return
	}
	op, err := parseNbctlOperation(req.Query, req.Variables)
	if err != nil {
		nbctlGraphQLError(c, http.StatusBadRequest, err.Error())
		return
	}
	audit := WorkspaceExecuteRequest{AccountId: unverified.AccountId, Tool: "nbctl", Command: op.Field}
	action, allowed := workspaceNbctlActions[op.Field]
	actionLabel := op.Field
	if !allowed {
		actionLabel = "other"
	}
	record := func(outcome string) { recordWorkspaceNbctl(c.Request.Context(), actionLabel, outcome) }
	if !allowed {
		record("refused_action")
		writeWorkspaceExecuteAudit(ctx, audit, "nbctl", unverified.AccountId, false)
		nbctlGraphQLError(c, http.StatusForbidden, fmt.Sprintf("%s is not available in Nubi's workspace", op.Field))
		return
	}

	for k := range op.Args {
		if k != "request" {
			record("refused_field")
			writeWorkspaceExecuteAudit(ctx, audit, "nbctl", unverified.AccountId, false)
			nbctlGraphQLError(c, http.StatusBadRequest, fmt.Sprintf("argument %s is not available in Nubi's workspace", k))
			return
		}
	}
	if err := checkNbctlFields("request", op.Args["request"], action.fields); err != nil {
		record("refused_field")
		writeWorkspaceExecuteAudit(ctx, audit, "nbctl", unverified.AccountId, false)
		nbctlGraphQLError(c, http.StatusBadRequest, err.Error())
		return
	}
	request, _ := op.Args["request"].(map[string]any)
	if limit, ok := request["limit"].(float64); ok && limit > nbctlMaxLogLimit {
		request["limit"] = float64(nbctlMaxLogLimit)
	}
	dataAccount, _ := request["account_id"].(string)
	allowedAccount := unverified.AccountId
	if bound, ok := c.Get("workspace_allowed_target"); ok {
		allowedAccount, _ = bound.(string)
	}
	if dataAccount == "" || dataAccount != allowedAccount {
		record("refused_account")
		writeWorkspaceExecuteAudit(ctx, audit, "nbctl", unverified.AccountId, false)
		nbctlGraphQLError(c, http.StatusForbidden, "this command can only read account "+allowedAccount)
		return
	}
	sc := ctx.GetSecurityContext()
	if !slices.ContainsFunc(sc.GetRoles(), func(r string) bool { return slices.Contains(action.roles, r) }) ||
		!sc.CanReadAccountData(dataAccount, action.module) {
		record("refused_role")
		writeWorkspaceExecuteAudit(ctx, audit, "nbctl", dataAccount, false)
		nbctlGraphQLError(c, http.StatusForbidden, fmt.Sprintf("your role cannot run %s", op.Field))
		return
	}
	if err := nbctlCheckProviderRequest(request); err != nil {
		record("refused_query")
		writeWorkspaceExecuteAudit(ctx, audit, "nbctl", dataAccount, false)
		nbctlGraphQLError(c, http.StatusBadRequest, err.Error())
		return
	}
	nbctlProviderDefaults(request)
	release, ok := nbctlAcquireSlot(dataAccount)
	if !ok {
		record("busy")
		writeWorkspaceExecuteAudit(ctx, audit, "nbctl", dataAccount, false)
		nbctlGraphQLError(c, http.StatusTooManyRequests, fmt.Sprintf("%d nbctl queries are already running for this account; run them one after another", nbctlMaxConcurrentPerAccount))
		return
	}
	defer release()
	result, err := forwardNbctlAction(ctx, action.rpcPath, op.Field, map[string]any{"request": request})
	writeWorkspaceExecuteAudit(ctx, audit, "nbctl", dataAccount, err == nil)
	if err != nil {
		record("backend_error")
		nbctlGraphQLError(c, http.StatusOK, err.Error())
		return
	}
	record("ok")
	c.JSON(http.StatusOK, gin.H{"data": gin.H{op.Field: result}})
}

// forwardNbctlAction sends one action to services-server as the app gateway
// would ({action:{name}, input}) and returns the raw result.
func forwardNbctlAction(ctx *security.RequestContext, rpcPath, action string, input map[string]any) (json.RawMessage, error) {
	sc := ctx.GetSecurityContext()
	reqCtx, cancel := context.WithTimeout(ctx.GetContext(), nbctlRequestTimeout)
	defer cancel()
	resp, err := common.HttpPost(config.Config.ServiceEndpoint+rpcPath,
		common.HttpWithContext(reqCtx),
		common.HttpWithHeaders(map[string]string{
			"Content-Type":   "application/json",
			"Accept":         "application/json",
			"X-ACTION-TOKEN": config.Config.ServiceApiServerToken,
			"x-tenant-id":    sc.GetTenantId(),
			"x-user-id":      sc.EffectiveUserIdForRPC(),
		}),
		common.HttpWithJsonBody(map[string]any{
			"action": map[string]any{"name": action},
			"input":  input,
		}))
	// Errors returned here reach the model through nbctl, so they never carry
	// internal addresses: the full error goes to the log, and a backend error
	// keeps its reason with addresses redacted. A transport failure is
	// reported as "unavailable" so the model retries or moves on rather than
	// probing the network.
	if err != nil {
		ctx.GetLogger().Warn("workspace nbctl: services-server request failed", "action", action, "error", err)
		return nil, fmt.Errorf("%s: the metrics and logs service is unavailable right now; try again shortly", action)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, nbctlMaxResponseBytes+1))
	if err != nil {
		ctx.GetLogger().Warn("workspace nbctl: unable to read services-server response", "action", action, "error", err)
		return nil, fmt.Errorf("%s: the metrics and logs service returned an unreadable response", action)
	}
	if len(body) > nbctlMaxResponseBytes {
		return nil, fmt.Errorf("%s: the result is larger than %d MB; narrow the query or the time range", action, nbctlMaxResponseBytes>>20)
	}
	if resp.StatusCode != http.StatusOK {
		ctx.GetLogger().Warn("workspace nbctl: services-server returned an error", "action", action, "status", resp.StatusCode, "body", nbctlTrimMessage(string(body), 1000))
		return nil, fmt.Errorf("%s failed: %s", action, nbctlRedactAddresses(nbctlTrimMessage(string(body), 1000)))
	}
	if !json.Valid(body) {
		ctx.GetLogger().Warn("workspace nbctl: services-server returned invalid JSON", "action", action)
		return nil, fmt.Errorf("%s: the metrics and logs service returned an unreadable response", action)
	}
	return nbctlRedactResultMessages(body), nil
}

// nbctlAddressPattern matches URLs and IP addresses (with an optional port) in
// backend error text.
var nbctlAddressPattern = regexp.MustCompile(`[a-zA-Z][a-zA-Z0-9+.-]*://[^\s"'()<>\\]+|\b\d{1,3}(?:\.\d{1,3}){3}(?::\d+)?\b|\[[0-9a-fA-F:]+\](?::\d+)?|\b[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)*\.(?:svc(?:\.cluster\.local)?|cluster\.local|internal)\b(?::\d+)?`)

// nbctlRedactResultMessages redacts addresses in the backend's own message
// fields of a successful result: suggestion and series_note on logs, and
// error and note on each metrics result (a query that failed inside an
// otherwise good response reports the provider's address there). Only those
// fields are touched, as raw JSON text; the provider's data (log lines, series)
// is never decoded, so large results cost one top-level split.
func nbctlRedactResultMessages(body []byte) json.RawMessage {
	var top map[string]json.RawMessage
	if json.Unmarshal(body, &top) != nil {
		return json.RawMessage(body)
	}
	changed := nbctlRedactFields(top, "suggestion", "series_note", "error", "note")
	if raw, ok := top["results"]; ok {
		var results []map[string]json.RawMessage
		if json.Unmarshal(raw, &results) == nil {
			resultsChanged := false
			for _, r := range results {
				if nbctlRedactFields(r, "error", "note") {
					resultsChanged = true
				}
			}
			if resultsChanged {
				if encoded, err := json.Marshal(results); err == nil {
					top["results"] = encoded
					changed = true
				}
			}
		}
	}
	if !changed {
		return json.RawMessage(body)
	}
	return nbctlJoinObject(top)
}

// nbctlJoinObject writes an object from already-valid raw values without
// json.Marshal, which would re-check every value (the whole log array) again.
func nbctlJoinObject(m map[string]json.RawMessage) json.RawMessage {
	keys := make([]string, 0, len(m))
	size := 2
	for k, v := range m {
		keys = append(keys, k)
		size += len(k) + len(v) + 4
	}
	sort.Strings(keys)
	out := make([]byte, 0, size)
	out = append(out, '{')
	for i, k := range keys {
		if i > 0 {
			out = append(out, ',')
		}
		name, _ := json.Marshal(k)
		out = append(out, name...)
		out = append(out, ':')
		out = append(out, m[k]...)
	}
	return append(out, '}')
}

// nbctlRedactFields redacts addresses in the raw JSON of the named fields of m
// (a string, or an object such as a provider error) and reports whether any
// changed. The address pattern never matches a quote or a backslash, so the
// JSON stays valid.
func nbctlRedactFields(m map[string]json.RawMessage, keys ...string) bool {
	changed := false
	for _, k := range keys {
		raw, ok := m[k]
		if !ok {
			continue
		}
		if redacted := nbctlAddressPattern.ReplaceAll(raw, []byte("<address>")); !bytes.Equal(redacted, raw) {
			m[k] = redacted
			changed = true
		}
	}
	return changed
}

// nbctlRedactAddresses keeps a backend error useful to the model (a bad query
// says why) without handing it internal addresses to probe.
func nbctlRedactAddresses(s string) string {
	return nbctlAddressPattern.ReplaceAllString(s, "<address>")
}

// nbctlTrimMessage shortens a backend error body for the model.
func nbctlTrimMessage(s string, limit int) string {
	s = strings.TrimSpace(s)
	if len(s) <= limit {
		return s
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}
