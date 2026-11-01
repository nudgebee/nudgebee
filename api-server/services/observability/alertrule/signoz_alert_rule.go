package alertrule

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"nudgebee/services/common"
	"nudgebee/services/integrations/signozclient"
	"nudgebee/services/security"
)

// SigNozAlertRuleSource creates, updates, deletes, lists and reads alert rules in
// a user-configured SigNoz (self-hosted v0.51 and later, or Cloud).
//
// The /api/v1/rules route is the same on every release, but the body is not:
// v0.118+ only accepts v5 bodies (compositeQuery.queries[]), older releases only
// v4 (compositeQuery.promQueries). signozclient.RuleSchema tells them apart.
type SigNozAlertRuleSource struct{}

const signozRulesPath = "/api/v1/rules"

// signozPromQLWrapPrefix / Suffix turn a Prometheus-style alert expression into
// a SigNoz threshold. Nudgebee rules carry the condition inside the expression
// ("up == 0", "x > 5"), where every series returned is a firing alert; SigNoz
// instead compares the returned VALUES with a target. Multiplying by zero and
// adding one keeps every series and its labels and sets its value to 1, so
// "value above 0" fires for exactly the series Prometheus would fire for — even
// when the matching value is 0 or negative (up == 0).
const (
	signozPromQLWrapPrefix = "("
	signozPromQLWrapSuffix = ") * 0 + 1"
)

func (s *SigNozAlertRuleSource) CreateAlertRule(ctx *security.RequestContext, config AlertRuleConfig) (*AlertRuleResult, error) {
	creds, hc, err := signozRuleConn(ctx, config.AccountId)
	if err != nil {
		return nil, err
	}
	body, err := signozRuleBodyFor(ctx, hc, creds, config)
	if err != nil {
		return nil, err
	}
	respBody, err := signozRulesRequest(ctx, hc, creds, http.MethodPost, signozRulesPath, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create SigNoz alert rule: %w", err)
	}

	var result struct {
		Data struct {
			Id json.RawMessage `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse SigNoz response: %w", err)
	}
	ruleId := signozRuleID(result.Data.Id)
	if ruleId == "" {
		return nil, fmt.Errorf("SigNoz created the rule but returned no id: %s", truncateSignozBody(respBody))
	}
	return &AlertRuleResult{ExternalRuleId: ruleId, Name: config.Name, Status: "created"}, nil
}

// UpdateAlertRule replaces the rule: SigNoz's PUT takes the whole definition.
func (s *SigNozAlertRuleSource) UpdateAlertRule(ctx *security.RequestContext, externalRuleId string, config AlertRuleConfig) (*AlertRuleResult, error) {
	creds, hc, err := signozRuleConn(ctx, config.AccountId)
	if err != nil {
		return nil, err
	}
	body, err := signozRuleBodyFor(ctx, hc, creds, config)
	if err != nil {
		return nil, err
	}
	path := signozRulesPath + "/" + neturl.PathEscape(externalRuleId)
	if _, err := signozRulesRequest(ctx, hc, creds, http.MethodPut, path, body); err != nil {
		return nil, fmt.Errorf("failed to update SigNoz alert rule: %w", err)
	}
	return &AlertRuleResult{ExternalRuleId: externalRuleId, Name: config.Name, Status: "updated"}, nil
}

func (s *SigNozAlertRuleSource) DeleteAlertRule(ctx *security.RequestContext, accountId string, externalRuleId string) error {
	creds, hc, err := signozRuleConn(ctx, accountId)
	if err != nil {
		return err
	}
	path := signozRulesPath + "/" + neturl.PathEscape(externalRuleId)
	if _, err := signozRulesRequest(ctx, hc, creds, http.MethodDelete, path, nil); err != nil {
		return fmt.Errorf("failed to delete SigNoz alert rule: %w", err)
	}
	return nil
}

// ListAlertRules implements AlertRuleLister. SigNoz returns every rule in one
// unpaginated response on all releases, so the listing is complete.
func (s *SigNozAlertRuleSource) ListAlertRules(ctx *security.RequestContext, accountId string) ([]ExternalAlertRule, error) {
	creds, hc, err := signozRuleConn(ctx, accountId)
	if err != nil {
		return nil, err
	}
	respBody, err := signozRulesRequest(ctx, hc, creds, http.MethodGet, signozRulesPath, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list SigNoz alert rules: %w", err)
	}
	rules, err := parseSignozRuleList(respBody)
	if err != nil {
		return nil, err
	}
	out := make([]ExternalAlertRule, 0, len(rules))
	for _, rule := range rules {
		if ext, ok := rule.external(); ok {
			out = append(out, ext)
		}
	}
	return out, nil
}

// GetAlertRule implements AlertRuleGetter.
func (s *SigNozAlertRuleSource) GetAlertRule(ctx *security.RequestContext, accountId, externalRuleId string) (*ExternalAlertRule, error) {
	creds, hc, err := signozRuleConn(ctx, accountId)
	if err != nil {
		return nil, err
	}
	respBody, err := signozRulesRequest(ctx, hc, creds, http.MethodGet, signozRulesPath+"/"+neturl.PathEscape(externalRuleId), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to read SigNoz alert rule %s: %w", externalRuleId, err)
	}
	var env struct {
		Data signozRule `json:"data"`
	}
	if err := json.Unmarshal(respBody, &env); err != nil {
		return nil, fmt.Errorf("failed to parse SigNoz alert rule: %w", err)
	}
	ext, ok := env.Data.external()
	if !ok {
		return nil, fmt.Errorf("SigNoz returned no rule for id %s", externalRuleId)
	}
	return &ext, nil
}

func signozRuleConn(ctx *security.RequestContext, accountId string) (signozclient.Credentials, *http.Client, error) {
	creds, err := getSignozCredentials(ctx, accountId)
	if err != nil {
		return signozclient.Credentials{}, nil, fmt.Errorf("failed to get SigNoz auth: %w", err)
	}
	return creds, common.HttpClient(), nil
}

// signozRuleBodyFor builds the body the server accepts, routed to this
// account's Nudgebee SigNoz webhook channel(s).
func signozRuleBodyFor(ctx *security.RequestContext, hc *http.Client, creds signozclient.Credentials, config AlertRuleConfig) (map[string]any, error) {
	schema, err := signozclient.RuleSchema(ctx.GetContext(), hc, creds)
	if err != nil {
		return nil, err
	}
	channels, err := signozRuleChannels(ctx, hc, creds, config, schema)
	if err != nil {
		return nil, err
	}
	return buildSignozAlertRule(config, schema, channels)
}

// signozRuleChannels picks the notification channels a new or updated rule
// notifies. An explicit provider_config "channels" list wins. Otherwise the rule
// goes to the channels that post to THIS account's SigNoz webhook, recognised by
// the integration token in the channel URL — the token is what the receiver
// resolves the account from, so matching on it (not on the path) keeps one
// account's rule from firing into another account or environment that shares
// the SigNoz.
//
// Finding none is an error that says what to set up (see the check below).
func signozRuleChannels(ctx *security.RequestContext, hc *http.Client, creds signozclient.Credentials, config AlertRuleConfig, schema string) ([]string, error) {
	if explicit := stringList(config.ProviderConfig["channels"]); len(explicit) > 0 {
		return explicit, nil
	}
	tokens, err := listSignozWebhookTokens(ctx, config.AccountId)
	if err != nil {
		return nil, err
	}
	var channels []string
	if len(tokens) > 0 {
		respBody, err := signozRulesRequest(ctx, hc, creds, http.MethodGet, "/api/v1/channels", nil)
		if err != nil {
			return nil, fmt.Errorf("failed to list SigNoz notification channels: %w", err)
		}
		channels = signozChannelsForTokens(respBody, tokens)
	}
	// Required on every release: v0.118+ refuses a rule without a channel, and older
	// releases notify EVERY channel when none is named, which on a shared SigNoz would
	// page other teams for a rule Nudgebee created.
	if len(channels) == 0 {
		if len(tokens) == 0 {
			return nil, errors.New("SigNoz needs a notification channel on every alert rule: add a SigNoz Webhook integration " +
				"for this account in Nudgebee, then add its URL as a Webhook notification channel in SigNoz")
		}
		return nil, errors.New("no SigNoz notification channel posts to this account's Nudgebee SigNoz webhook: " +
			"add the SigNoz Webhook integration's URL (with its token) as a Webhook notification channel in SigNoz")
	}
	return channels, nil
}

// signozChannelsForTokens returns the names of webhook channels whose URL carries
// one of tokens as its token query parameter.
func signozChannelsForTokens(body []byte, tokens []string) []string {
	var env struct {
		Data []struct {
			Name string `json:"name"`
			Type string `json:"type"`
			Data string `json:"data"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &env) != nil {
		return nil
	}
	var names []string
	for _, ch := range env.Data {
		var cfg struct {
			WebhookConfigs []struct {
				URL string `json:"url"`
			} `json:"webhook_configs"`
		}
		if json.Unmarshal([]byte(ch.Data), &cfg) != nil {
			continue
		}
		for _, wc := range cfg.WebhookConfigs {
			u, err := neturl.Parse(wc.URL)
			if err != nil {
				continue
			}
			if tok := u.Query().Get("token"); tok != "" && slices.Contains(tokens, tok) {
				names = append(names, ch.Name)
				break
			}
		}
	}
	return names
}

// buildSignozAlertRule builds a rule body for the given schema generation.
//
// Metric rules are PromQL. A rule with no explicit provider_config "threshold"
// is a Prometheus-style expression (the condition is inside it) and is wrapped
// so every returned series fires; with a threshold, the expression is a value
// compared against it. "for" (Duration) becomes "all the time over a window that
// long"; without it the rule fires on the last minute. Log rules count matching
// log lines with a v5 builder query whose filter is the query expression.
func buildSignozAlertRule(config AlertRuleConfig, schema string, channels []string) (map[string]any, error) {
	if strings.TrimSpace(config.Query) == "" {
		return nil, errors.New("a SigNoz alert rule needs a query")
	}
	pc := config.ProviderConfig

	evalWindow := "1m0s"
	matchType := "1"
	if d, err := time.ParseDuration(strings.TrimSpace(config.Duration)); err == nil && d > 0 {
		evalWindow = d.String()
		matchType = "2"
	}
	if v, ok := pc["eval_window"].(string); ok && v != "" {
		evalWindow = v
	}
	frequency := "1m0s"
	if v, ok := pc["frequency"].(string); ok && v != "" {
		frequency = v
	}
	if v, ok := pc["match_type"].(string); ok && v != "" {
		matchType = v
	}

	threshold := 0.0
	_, hasThreshold := pc["threshold"]
	if hasThreshold {
		v, err := toFloat64(pc["threshold"])
		if err != nil {
			return nil, fmt.Errorf("invalid SigNoz threshold: %w", err)
		}
		threshold = v
	}
	// A wrapped expression's value is always 1, so only "above 0" can fire: the
	// operator applies only to an explicit threshold.
	op := "1"
	if raw, ok := pc["threshold_operator"].(string); ok && raw != "" && hasThreshold {
		code, err := signozCompareOp(raw, schema)
		if err != nil {
			return nil, err
		}
		op = code
	}

	labels := map[string]string{}
	for k, v := range config.Labels {
		if v != "" {
			labels[k] = v
		}
	}
	labels["severity"] = normalizeCubeAPMSeverity(firstNonEmpty(config.Severity, labels["severity"]))

	annotations := map[string]string{}
	for k, v := range config.Annotations {
		if v != "" {
			annotations[k] = v
		}
	}

	condition := map[string]any{
		"op":                op,
		"target":            threshold,
		"matchType":         matchType,
		"selectedQueryName": "A",
	}
	rule := map[string]any{
		"alert":       config.Name,
		"evalWindow":  evalWindow,
		"frequency":   frequency,
		"labels":      labels,
		"annotations": annotations,
		"disabled":    !config.Enabled,
		"version":     schema,
		"condition":   condition,
	}
	if len(channels) > 0 {
		rule["preferredChannels"] = channels
	}

	if config.AlertType == "log" {
		if schema != signozclient.RuleSchemaV5 {
			return nil, errors.New("log alerts can be created in SigNoz v0.118 and later only; create this rule in SigNoz")
		}
		rule["alertType"] = "LOGS_BASED_ALERT"
		rule["ruleType"] = "threshold_rule"
		condition["compositeQuery"] = map[string]any{
			"queryType": "builder",
			"panelType": "graph",
			"queries": []any{map[string]any{
				"type": "builder_query",
				"spec": map[string]any{
					"name":         "A",
					"signal":       "logs",
					"stepInterval": 60,
					"aggregations": []any{map[string]any{"expression": "count()"}},
					"filter":       map[string]any{"expression": config.Query},
				},
			}},
		}
		return rule, nil
	}

	query := config.Query
	// A `bool` comparison already answers 1 for true and 0 for false on every series,
	// so it is sent as it is and fires on "above 0"; wrapping it would turn every
	// series into 1 and fire constantly.
	if !hasThreshold && !promQLHasBoolModifier(query) {
		query = signozPromQLWrapPrefix + query + signozPromQLWrapSuffix
	}
	rule["alertType"] = "METRIC_BASED_ALERT"
	rule["ruleType"] = "promql_rule"
	promQuery := map[string]any{"name": "A", "query": query, "disabled": false}
	if schema == signozclient.RuleSchemaV5 {
		condition["compositeQuery"] = map[string]any{
			"queryType": "promql",
			"panelType": "graph",
			"queries":   []any{map[string]any{"type": "promql", "spec": promQuery}},
		}
	} else {
		condition["compositeQuery"] = map[string]any{
			"queryType":   "promql",
			"panelType":   "graph",
			"promQueries": map[string]any{"A": promQuery},
		}
	}
	return rule, nil
}

// promQLBoolModifierRe finds the `bool` keyword outside string literals.
var promQLBoolModifierRe = regexp.MustCompile(`(^|[^A-Za-z0-9_:])bool([^A-Za-z0-9_:]|$)`)

// promQLHasBoolModifier reports whether a comparison in expr uses `bool`.
func promQLHasBoolModifier(expr string) bool {
	return promQLBoolModifierRe.MatchString(promQLStripStrings(expr))
}

// promQLStripStrings blanks quoted literals so a label value cannot look like syntax.
func promQLStripStrings(expr string) string {
	var b strings.Builder
	var quote byte
	for i := 0; i < len(expr); i++ {
		c := expr[i]
		switch {
		case quote != 0:
			if c == '\\' && quote != '`' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'' || c == '`':
			quote = c
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// signozCompareOp maps an operator to SigNoz's numeric code, which every release
// accepts (v0.118+ also takes symbols; older ones only codes 1–4).
func signozCompareOp(raw, schema string) (string, error) {
	codes := map[string]string{
		">": "1", "above": "1", "1": "1",
		"<": "2", "below": "2", "2": "2",
		"==": "3", "=": "3", "eq": "3", "equal": "3", "3": "3",
		"!=": "4", "not_eq": "4", "not_equal": "4", "4": "4",
		">=": "5", "above_or_equal": "5", "above_or_eq": "5", "5": "5",
		"<=": "6", "below_or_equal": "6", "below_or_eq": "6", "6": "6",
	}
	code, ok := codes[strings.ToLower(strings.TrimSpace(raw))]
	if !ok {
		return "", fmt.Errorf("unsupported SigNoz threshold operator %q", raw)
	}
	if schema != signozclient.RuleSchemaV5 && (code == "5" || code == "6") {
		return "", fmt.Errorf("SigNoz releases before v0.118 have no %q operator", raw)
	}
	return code, nil
}

// signozRulesRequest sends one rules-API call and returns the body of a 2xx
// response; anything else is an error carrying SigNoz's own message.
func signozRulesRequest(ctx *security.RequestContext, hc *http.Client, creds signozclient.Credentials, method, path string, body any) ([]byte, error) {
	var payload []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		payload = b
	}
	resp, err := signozclient.Do(ctx.GetContext(), hc, creds, method, path, payload)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("failed to read SigNoz response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("signoz API error (status %d): %s", resp.StatusCode, truncateSignozBody(respBody))
	}
	return respBody, nil
}

// signozRule is one rule as GET /api/v1/rules returns it, in either generation.
type signozRule struct {
	Id          json.RawMessage   `json:"id"`
	Alert       string            `json:"alert"`
	AlertType   string            `json:"alertType"`
	RuleType    string            `json:"ruleType"`
	Version     string            `json:"version"`
	State       string            `json:"state"`
	Disabled    bool              `json:"disabled"`
	EvalWindow  string            `json:"evalWindow"`
	Frequency   string            `json:"frequency"`
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
	Condition   struct {
		Op                string  `json:"op"`
		Target            float64 `json:"target"`
		MatchType         string  `json:"matchType"`
		SelectedQueryName string  `json:"selectedQueryName"`
		CompositeQuery    struct {
			QueryType string `json:"queryType"`
			Queries   []struct {
				Type string `json:"type"`
				Spec struct {
					Name   string `json:"name"`
					Query  string `json:"query"`
					Filter *struct {
						Expression string `json:"expression"`
					} `json:"filter"`
				} `json:"spec"`
			} `json:"queries"`
			PromQueries map[string]struct {
				Query string `json:"query"`
			} `json:"promQueries"`
			ChQueries map[string]struct {
				Query string `json:"query"`
			} `json:"chQueries"`
		} `json:"compositeQuery"`
	} `json:"condition"`
}

func parseSignozRuleList(body []byte) ([]signozRule, error) {
	// Legacy and current releases wrap the list as data.rules; the ruler's own
	// list handler returns it as data directly.
	var wrapped struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &wrapped); err != nil {
		return nil, fmt.Errorf("failed to parse SigNoz alert rules: %w", err)
	}
	var withRules struct {
		Rules []signozRule `json:"rules"`
	}
	if json.Unmarshal(wrapped.Data, &withRules) == nil && withRules.Rules != nil {
		return withRules.Rules, nil
	}
	var list []signozRule
	if err := json.Unmarshal(wrapped.Data, &list); err != nil {
		return nil, fmt.Errorf("failed to parse SigNoz alert rules: %s", truncateSignozBody(body))
	}
	return list, nil
}

func (r signozRule) external() (ExternalAlertRule, bool) {
	id := signozRuleID(r.Id)
	if id == "" || r.Alert == "" {
		return ExternalAlertRule{}, false
	}
	selected := r.Condition.SelectedQueryName
	cq := r.Condition.CompositeQuery
	var query string
	for _, q := range cq.Queries {
		if selected != "" && q.Spec.Name != selected {
			continue
		}
		query = q.Spec.Query
		if query == "" && q.Spec.Filter != nil {
			query = q.Spec.Filter.Expression
		}
		break
	}
	if query == "" {
		if q, ok := cq.PromQueries[firstNonEmpty(selected, "A")]; ok {
			query = q.Query
		} else if q, ok := cq.ChQueries[firstNonEmpty(selected, "A")]; ok {
			query = q.Query
		}
	}
	if strings.HasPrefix(query, signozPromQLWrapPrefix) && strings.HasSuffix(query, signozPromQLWrapSuffix) {
		query = strings.TrimSuffix(strings.TrimPrefix(query, signozPromQLWrapPrefix), signozPromQLWrapSuffix)
	}

	alertType := strings.ToLower(strings.TrimSuffix(r.AlertType, "_BASED_ALERT"))
	switch alertType {
	case "logs":
		alertType = "log"
	case "":
		alertType = "metric"
	}
	duration := ""
	if r.Condition.MatchType == "2" || r.Condition.MatchType == "all_the_times" {
		duration = r.EvalWindow
	}
	return ExternalAlertRule{
		ExternalRuleId: id,
		Name:           r.Alert,
		AlertType:      alertType,
		Query:          query,
		Severity:       normalizeCubeAPMSeverity(r.Labels["severity"]),
		Duration:       duration,
		Annotations:    r.Annotations,
		Labels:         r.Labels,
		Enabled:        !r.Disabled,
		ProviderConfig: map[string]any{
			"rule_type":   r.RuleType,
			"query_type":  cq.QueryType,
			"version":     r.Version,
			"state":       r.State,
			"eval_window": r.EvalWindow,
			"frequency":   r.Frequency,
			"match_type":  r.Condition.MatchType,
			"op":          r.Condition.Op,
			"target":      r.Condition.Target,
		},
	}, true
}

// signozRuleID reads a rule id, which is a UUID string on current releases and
// an integer on legacy ones.
func signozRuleID(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s)
	}
	var n json.Number
	if json.Unmarshal(raw, &n) == nil {
		return n.String()
	}
	return ""
}

func stringList(v any) []string {
	var out []string
	switch t := v.(type) {
	case []string:
		out = t
	case []any:
		for _, item := range t {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
	}
	var cleaned []string
	for _, s := range out {
		if s = strings.TrimSpace(s); s != "" {
			cleaned = append(cleaned, s)
		}
	}
	return cleaned
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func truncateSignozBody(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}
