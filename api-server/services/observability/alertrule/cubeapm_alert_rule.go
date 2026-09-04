package alertrule

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"strconv"
	"strings"
	"time"

	"nudgebee/services/common"
	"nudgebee/services/security"
)

// CubeAPMAlertRuleSource creates, updates, deletes and lists alert rules in
// CubeAPM.
//
// Alert-rule management lives behind CubeAPM's ADMIN server (port 3199), not the
// query port every other CubeAPM call in this codebase uses. That server is
// disabled by default on some deployments and can require its own bearer token,
// which is why the integration carries a separate admin URL and admin token —
// and why the errors here name those fields specifically.
type CubeAPMAlertRuleSource struct{}

const cubeAPMAlertRulesPath = "/api/alerts/api/v1/rules"

const cubeAPMAlertRuleTimeout = 30 * time.Second

// CubeAPM alert-rule defaults. All are overridable through ProviderConfig.
const (
	// cubeAPMDefaultEvalInterval is how often the rule is evaluated, in seconds.
	cubeAPMDefaultEvalInterval = 60
	// cubeAPMDefaultForSeconds is how long the condition must hold before firing.
	cubeAPMDefaultForSeconds = 300
	// cubeAPMDefaultRepeatInterval is how long before a still-firing alert
	// re-notifies, in seconds.
	cubeAPMDefaultRepeatInterval = 3600
)

// cubeAPMRuleRequest is the create/update body. Field names and units come from
// CubeAPM's alert-rules API: `interval`, `for` and `repeat_interval` are all
// SECONDS as integers, not Prometheus duration strings.
type cubeAPMRuleRequest struct {
	ID              int64             `json:"id,omitempty"`
	Name            string            `json:"name"`
	Datasource      string            `json:"datasource"`
	Kind            string            `json:"kind"`
	Status          string            `json:"status"`
	GroupingDisable bool              `json:"grouping_disable"`
	Interval        int               `json:"interval"`
	Expr            string            `json:"expr"`
	For             int               `json:"for"`
	RepeatInterval  int               `json:"repeat_interval"`
	Labels          map[string]string `json:"labels"`
	Annotations     map[string]string `json:"annotations"`
	Config          map[string]any    `json:"config"`
	Receiver        map[string]any    `json:"receiver"`
	Mute            map[string]any    `json:"mute"`
	Permissions     []any             `json:"permissions"`
}

// cubeAPMRuleResponse covers both the create/update response and one entry of a
// listing. `id` is numeric in CubeAPM, and `query` is the rendered expression the
// server stored, which can differ from the `expr` that was submitted.
type cubeAPMRuleResponse struct {
	ID          json.Number       `json:"id"`
	Name        string            `json:"name"`
	Datasource  string            `json:"datasource"`
	Kind        string            `json:"kind"`
	Status      string            `json:"status"`
	Interval    json.Number       `json:"interval"`
	Duration    json.Number       `json:"duration"`
	Query       string            `json:"query"`
	Expr        string            `json:"expr"`
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
}

func (s *CubeAPMAlertRuleSource) CreateAlertRule(ctx *security.RequestContext, config AlertRuleConfig) (*AlertRuleResult, error) {
	cfg, err := cubeAPMAdminConfig(ctx, config.AccountId)
	if err != nil {
		return nil, err
	}

	body, err := json.Marshal(buildCubeAPMRule(config, 0))
	if err != nil {
		return nil, fmt.Errorf("failed to encode CubeAPM alert rule: %w", err)
	}

	respBody, err := cubeAPMAdminRequest(cfg, http.MethodPost, cubeAPMAlertRulesPath, body)
	if err != nil {
		return nil, err
	}

	ruleID := parseCubeAPMRuleID(respBody)
	if ruleID == "" {
		// Without an id the rule exists but is unaddressable, so a later update or
		// delete would silently create a duplicate instead. CubeAPM's create
		// response shape is not documented, so fall back to finding the rule we
		// just created by name rather than returning an unusable handle.
		ruleID = s.findRuleIDByName(cfg, config.Name)
	}
	if ruleID == "" {
		return nil, fmt.Errorf("CubeAPM accepted the alert rule %q but returned no rule id, and it "+
			"could not be found by name — the rule may exist but cannot be updated or deleted from here", config.Name)
	}

	return &AlertRuleResult{ExternalRuleId: ruleID, Name: config.Name, Status: "created"}, nil
}

func (s *CubeAPMAlertRuleSource) UpdateAlertRule(ctx *security.RequestContext, externalRuleId string, config AlertRuleConfig) (*AlertRuleResult, error) {
	cfg, err := cubeAPMAdminConfig(ctx, config.AccountId)
	if err != nil {
		return nil, err
	}

	id, err := strconv.ParseInt(strings.TrimSpace(externalRuleId), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("CubeAPM alert rule id must be numeric (got %q): %w", externalRuleId, err)
	}

	body, err := json.Marshal(buildCubeAPMRule(config, id))
	if err != nil {
		return nil, fmt.Errorf("failed to encode CubeAPM alert rule: %w", err)
	}

	if _, err := cubeAPMAdminRequest(cfg, http.MethodPut, cubeAPMAlertRulesPath, body); err != nil {
		return nil, err
	}

	return &AlertRuleResult{ExternalRuleId: externalRuleId, Name: config.Name, Status: "updated"}, nil
}

func (s *CubeAPMAlertRuleSource) DeleteAlertRule(ctx *security.RequestContext, accountId string, externalRuleId string) error {
	cfg, err := cubeAPMAdminConfig(ctx, accountId)
	if err != nil {
		return err
	}

	id := strings.TrimSpace(externalRuleId)
	if _, err := strconv.ParseInt(id, 10, 64); err != nil {
		return fmt.Errorf("CubeAPM alert rule id must be numeric (got %q): %w", externalRuleId, err)
	}

	params := neturl.Values{}
	params.Set("id", id)
	_, err = cubeAPMAdminRequest(cfg, http.MethodDelete, cubeAPMAlertRulesPath+"?"+params.Encode(), nil)
	return err
}

// ListAlertRules implements AlertRuleLister so CubeAPM rules can be synced back
// into event_rules — the same capability the Datadog and Elasticsearch sources
// provide.
func (s *CubeAPMAlertRuleSource) ListAlertRules(ctx *security.RequestContext, accountId string) ([]ExternalAlertRule, error) {
	cfg, err := cubeAPMAdminConfig(ctx, accountId)
	if err != nil {
		return nil, err
	}

	rules, err := s.listRules(cfg)
	if err != nil {
		return nil, err
	}

	out := make([]ExternalAlertRule, 0, len(rules))
	for _, rule := range rules {
		id := rule.ID.String()
		if id == "" || id == "0" {
			continue
		}

		query := rule.Query
		if query == "" {
			query = rule.Expr
		}

		out = append(out, ExternalAlertRule{
			ExternalRuleId: id,
			Name:           rule.Name,
			AlertType:      cubeAPMAlertTypeFromDatasource(rule.Datasource),
			Query:          query,
			Severity:       normalizeCubeAPMSeverity(rule.Labels["severity"]),
			Duration:       cubeAPMDurationString(rule.Duration),
			Annotations:    rule.Annotations,
			Labels:         rule.Labels,
			// CubeAPM pauses a rule rather than deleting it; "ACTIVE" is the only
			// status that actually evaluates.
			Enabled: strings.EqualFold(rule.Status, "ACTIVE"),
			ProviderConfig: map[string]any{
				"datasource": rule.Datasource,
				"kind":       rule.Kind,
				"status":     rule.Status,
			},
		})
	}
	return out, nil
}

// listRules fetches every rule from the admin API.
func (s *CubeAPMAlertRuleSource) listRules(cfg cubeAPMConn) ([]cubeAPMRuleResponse, error) {
	body, err := cubeAPMAdminRequest(cfg, http.MethodGet, cubeAPMAlertRulesPath, nil)
	if err != nil {
		return nil, err
	}
	return parseCubeAPMRulesResponse(body)
}

// parseCubeAPMRulesResponse decodes a rules listing. Three shapes are accepted
// because CubeAPM's API reference documents the GET example as a single rule
// object and does not specify the collection wrapper either way; guessing one
// and rejecting the rest would break listing on whichever build differs.
func parseCubeAPMRulesResponse(body []byte) ([]cubeAPMRuleResponse, error) {
	var asArray []cubeAPMRuleResponse
	if err := json.Unmarshal(body, &asArray); err == nil {
		return asArray, nil
	}

	var wrapped struct {
		Data  []cubeAPMRuleResponse `json:"data"`
		Rules []cubeAPMRuleResponse `json:"rules"`
	}
	if err := json.Unmarshal(body, &wrapped); err == nil {
		if len(wrapped.Data) > 0 {
			return wrapped.Data, nil
		}
		if len(wrapped.Rules) > 0 {
			return wrapped.Rules, nil
		}
	}

	var single cubeAPMRuleResponse
	if err := json.Unmarshal(body, &single); err == nil && single.Name != "" {
		return []cubeAPMRuleResponse{single}, nil
	}

	return nil, fmt.Errorf("could not parse CubeAPM alert rules response")
}

// findRuleIDByName recovers the id of a rule that was just created, for the case
// where the create response did not carry one. Returns "" when the lookup fails —
// the caller turns that into an explicit error rather than a silent success.
func (s *CubeAPMAlertRuleSource) findRuleIDByName(cfg cubeAPMConn, name string) string {
	rules, err := s.listRules(cfg)
	if err != nil {
		return ""
	}
	for _, rule := range rules {
		if rule.Name == name {
			if id := rule.ID.String(); id != "" && id != "0" {
				return id
			}
		}
	}
	return ""
}

// buildCubeAPMRule renders an AlertRuleConfig into CubeAPM's rule body. An id of 0
// is omitted, which is what distinguishes a create from an update.
func buildCubeAPMRule(config AlertRuleConfig, id int64) cubeAPMRuleRequest {
	labels := map[string]string{
		"severity": normalizeCubeAPMSeverity(config.Severity),
		// Marks rules this system owns, so an operator looking at CubeAPM can tell
		// them apart from rules authored in its own UI.
		"source": "nudgebee",
	}
	for k, v := range config.Labels {
		labels[k] = v
	}

	annotations := map[string]string{"summary": config.Name}
	for k, v := range config.Annotations {
		if v != "" {
			annotations[k] = v
		}
	}

	status := "ACTIVE"
	if !config.Enabled {
		// CubeAPM has no "disabled" state — a rule that should not evaluate is
		// PAUSED, which is what the listing reads back as Enabled: false.
		status = "PAUSED"
	}

	rule := cubeAPMRuleRequest{
		ID:             id,
		Name:           config.Name,
		Datasource:     cubeAPMDatasource(config),
		Kind:           "static",
		Status:         status,
		Interval:       cubeAPMProviderInt(config.ProviderConfig, "interval", cubeAPMDefaultEvalInterval),
		Expr:           config.Query,
		For:            cubeAPMForSeconds(config),
		RepeatInterval: cubeAPMProviderInt(config.ProviderConfig, "repeat_interval", cubeAPMDefaultRepeatInterval),
		Labels:         labels,
		Annotations:    annotations,
		Config:         map[string]any{"receiver_group_ids": []any{}, "mute_group_ids": []any{}},
		// Receiver is sent empty on purpose. Notification routing is configured in
		// CubeAPM (which is also where the webhook back to NudgeBee is registered);
		// populating it here would silently override whatever the operator set up.
		Receiver:    map[string]any{},
		Mute:        map[string]any{"time_intervals": []any{}},
		Permissions: []any{},
	}

	if receiver, ok := config.ProviderConfig["receiver"].(map[string]any); ok {
		rule.Receiver = receiver
	}
	if groupingDisable, ok := config.ProviderConfig["grouping_disable"].(bool); ok {
		rule.GroupingDisable = groupingDisable
	}

	return rule
}

// cubeAPMDatasource picks the datasource for a rule. "prometheus" is the
// documented value and backs metric rules; log rules use "logs", which CubeAPM's
// UI writes but its API reference does not spell out — hence the ProviderConfig
// override, so a deployment using a different name is not blocked on a code change.
func cubeAPMDatasource(config AlertRuleConfig) string {
	if ds, ok := config.ProviderConfig["datasource"].(string); ok && ds != "" {
		return ds
	}
	if config.AlertType == "log" {
		return "logs"
	}
	return "prometheus"
}

func cubeAPMAlertTypeFromDatasource(datasource string) string {
	if strings.EqualFold(datasource, "logs") {
		return "log"
	}
	return "metric"
}

// cubeAPMForSeconds converts the config's Prometheus-style duration into the
// integer seconds CubeAPM expects.
func cubeAPMForSeconds(config AlertRuleConfig) int {
	if seconds := cubeAPMProviderInt(config.ProviderConfig, "for", 0); seconds > 0 {
		return seconds
	}
	if seconds, ok := parsePromDurationSeconds(config.Duration); ok {
		return seconds
	}
	return cubeAPMDefaultForSeconds
}

// parsePromDurationSeconds converts a Prometheus duration string ("5m", "1h30m",
// "90s") to seconds. A bare number is read as seconds. Returns ok=false for
// anything unparseable so the caller can fall back to its default rather than
// creating a rule that fires instantly.
func parsePromDurationSeconds(duration string) (int, bool) {
	duration = strings.TrimSpace(duration)
	if duration == "" {
		return 0, false
	}
	if n, err := strconv.Atoi(duration); err == nil {
		return n, n > 0
	}

	units := map[byte]int{'s': 1, 'm': 60, 'h': 3600, 'd': 86400, 'w': 604800}

	total, digits := 0, ""
	for i := 0; i < len(duration); i++ {
		c := duration[i]
		if c >= '0' && c <= '9' {
			digits += string(c)
			continue
		}
		scale, known := units[c]
		if !known || digits == "" {
			return 0, false
		}
		n, err := strconv.Atoi(digits)
		if err != nil {
			return 0, false
		}
		total += n * scale
		digits = ""
	}
	// A trailing digit run means a unit was missing (e.g. "5m30").
	if digits != "" {
		return 0, false
	}
	return total, total > 0
}

// cubeAPMDurationString renders a seconds count back as a Prometheus duration for
// the ExternalAlertRule contract, which carries durations as strings.
func cubeAPMDurationString(seconds json.Number) string {
	n, err := seconds.Int64()
	if err != nil || n <= 0 {
		return ""
	}
	return (time.Duration(n) * time.Second).String()
}

// normalizeCubeAPMSeverity maps onto the severity vocabulary the rest of the
// system uses, defaulting to warning for anything unrecognized.
func normalizeCubeAPMSeverity(severity string) string {
	switch strings.ToLower(strings.TrimSpace(severity)) {
	case "critical", "fatal", "error", "high", "p1":
		return "critical"
	case "info", "informational", "low":
		return "info"
	default:
		return "warning"
	}
}

// cubeAPMProviderInt reads an integer from ProviderConfig, tolerating the several
// numeric types a JSON round-trip can produce.
func cubeAPMProviderInt(providerConfig map[string]any, key string, fallback int) int {
	raw, ok := providerConfig[key]
	if !ok {
		return fallback
	}
	switch v := raw.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case json.Number:
		if n, err := v.Int64(); err == nil {
			return int(n)
		}
	case string:
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

// parseCubeAPMRuleID digs the created rule's id out of a create response, which
// may return the rule at the top level or wrapped in `data`.
func parseCubeAPMRuleID(body []byte) string {
	var direct cubeAPMRuleResponse
	if err := json.Unmarshal(body, &direct); err == nil {
		if id := direct.ID.String(); id != "" && id != "0" {
			return id
		}
	}

	var wrapped struct {
		Data cubeAPMRuleResponse `json:"data"`
	}
	if err := json.Unmarshal(body, &wrapped); err == nil {
		if id := wrapped.Data.ID.String(); id != "" && id != "0" {
			return id
		}
	}
	return ""
}

// cubeAPMAdminConfig resolves the account's CubeAPM config and asserts that an
// admin URL is available. Failing here with a specific message is deliberate: the
// admin URL is derived from the query URL only for the standard port pair, so a
// deployment on custom ports needs to be told exactly which field to fill in.
func cubeAPMAdminConfig(ctx *security.RequestContext, accountId string) (cubeAPMConn, error) {
	cfg, err := getCubeAPMConfigs(ctx, accountId)
	if err != nil {
		return cfg, fmt.Errorf("failed to get CubeAPM configs: %w", err)
	}
	if cfg.AdminURL == "" {
		return cfg, fmt.Errorf("CubeAPM alert-rule management needs the admin API — set cubeapm_admin_url "+
			"on the CubeAPM integration (it could not be derived from %q, which is not on the standard "+
			"query port %s)", cfg.QueryURL, cubeAPMQueryPort)
	}
	return cfg, nil
}

// cubeAPMAdminRequest issues an authenticated call against the admin API and
// returns the response body.
func cubeAPMAdminRequest(cfg cubeAPMConn, method, path string, body []byte) ([]byte, error) {
	endpoint := cfg.AdminURL + path
	options := []common.HttpOption{
		common.HttpWithHeaders(cubeAPMAdminHeaders(cfg.AdminToken)),
		common.HttpWithTimeout(cubeAPMAlertRuleTimeout),
	}
	if len(body) > 0 {
		options = append(options, common.HttpWithStringBody(string(body)))
	}

	var resp *http.Response
	var err error
	switch method {
	case http.MethodGet:
		resp, err = common.HttpGet(endpoint, options...)
	case http.MethodPost:
		resp, err = common.HttpPost(endpoint, options...)
	case http.MethodPut:
		resp, err = common.HttpPut(endpoint, options...)
	case http.MethodDelete:
		resp, err = common.HttpDelete(endpoint, options...)
	default:
		return nil, fmt.Errorf("unsupported method for CubeAPM admin API: %s", method)
	}
	if err != nil {
		return nil, fmt.Errorf("CubeAPM admin API request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, cubeAPMAdminStatusError(resp.StatusCode, respBody, cfg.AdminURL)
	}
	return respBody, nil
}

// cubeAPMAdminStatusError turns a failed admin call into an error that names the
// likely cause. 404 is called out specifically because CubeAPM ships with the
// admin server disabled on some deployments, and a bare "404" reads as a wrong
// path rather than a server that is not listening.
func cubeAPMAdminStatusError(status int, body []byte, adminURL string) error {
	text := strings.TrimSpace(string(body))
	if len(text) > 500 {
		text = text[:500] + "…"
	}
	switch status {
	case http.StatusUnauthorized:
		return fmt.Errorf("CubeAPM admin API rejected the credentials (HTTP 401) — set cubeapm_admin_token " +
			"to match the server's http-token-admin setting")
	case http.StatusForbidden:
		return fmt.Errorf("insufficient permissions for the CubeAPM admin API (HTTP 403)")
	case http.StatusNotFound:
		return fmt.Errorf("CubeAPM admin API not found at %s — the admin server is disabled by default on "+
			"some deployments (http-host-admin); enable it or correct cubeapm_admin_url", adminURL)
	default:
		return fmt.Errorf("CubeAPM admin API returned HTTP %d: %s", status, text)
	}
}
