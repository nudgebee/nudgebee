package alertrule

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"nudgebee/services/common"
	"nudgebee/services/security"
)

// Kibana's _find endpoint reports `total`, so paging is exact rather than
// inferred from a short page. The cap is a backstop against a total that keeps
// growing while we walk it.
const (
	kibanaRulePageSize = 100
	kibanaMaxRulePages = 200
)

// kibanaRule is the subset of GET /api/alerting/rules/_find we map onto an
// event_rules row.
type kibanaRule struct {
	Id         string   `json:"id"`
	Name       string   `json:"name"`
	RuleTypeId string   `json:"rule_type_id"`
	Consumer   string   `json:"consumer"`
	Enabled    bool     `json:"enabled"`
	MuteAll    bool     `json:"mute_all"`
	Tags       []string `json:"tags"`
	Schedule   struct {
		Interval string `json:"interval"`
	} `json:"schedule"`
	Params map[string]any `json:"params"`
}

type kibanaFindResponse struct {
	Page    int          `json:"page"`
	PerPage int          `json:"per_page"`
	Total   int          `json:"total"`
	Data    []kibanaRule `json:"data"`
}

// ListAlertRules enumerates Kibana alerting rules for the account's
// Elasticsearch integration.
//
// Note this reads **Kibana** alerting rules, not Watcher watches — the other
// methods on this type drive Watcher, which is a gold-licence feature. Kibana
// alerting is available on a basic licence, so listing works where creating a
// watch would not.
//
// Requires `kibana_url` on the integration: Kibana is a different host and port
// from Elasticsearch and cannot be derived from it. Accounts without it are
// reported as unconfigured rather than silently returning nothing.
func (s *ElasticsearchAlertRuleSource) ListAlertRules(ctx *security.RequestContext, accountId string) ([]ExternalAlertRule, error) {
	cfg, err := getElasticsearchConfigs(ctx, accountId)
	if err != nil {
		return nil, fmt.Errorf("failed to get elasticsearch configs: %w", err)
	}
	if cfg.KibanaUrl == "" {
		return nil, fmt.Errorf("kibana_url is not set on the Elasticsearch integration; it is required to list Kibana alerting rules")
	}
	headers := kibanaHeaders(cfg)

	var out []ExternalAlertRule
	for page := 1; page <= kibanaMaxRulePages; page++ {
		url := fmt.Sprintf("%s/api/alerting/rules/_find?per_page=%d&page=%d",
			cfg.KibanaUrl, kibanaRulePageSize, page)

		resp, err := fetchKibanaRulePage(url, headers, cfg.TlsSkipVerify)
		if err != nil {
			return nil, err
		}
		for _, r := range resp.Data {
			out = append(out, kibanaRuleToExternalRule(r))
		}
		// An empty page ends the walk even if `total` disagrees, so a total
		// that shifts mid-walk cannot spin the loop.
		if len(resp.Data) == 0 || len(out) >= resp.Total {
			return out, nil
		}
	}
	ctx.GetLogger().Warn("kibana: rule listing hit the page cap; results are truncated",
		"account_id", accountId, "max_pages", kibanaMaxRulePages, "fetched", len(out))
	return out, ErrIncompleteListing
}

func fetchKibanaRulePage(url string, headers map[string]string, tlsSkipVerify bool) (*kibanaFindResponse, error) {
	opts := []common.HttpOption{common.HttpWithHeaders(headers)}
	if tlsSkipVerify {
		// Same opt-in the integration already applies to Elasticsearch itself;
		// Kibana on a self-hosted stack usually presents the same self-signed CA.
		opts = append(opts, common.HttpWithInsecureSkipVerify())
	}
	resp, err := common.HttpGet(url, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to list Kibana rules: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read Kibana response: %w", err)
	}
	if resp.StatusCode == http.StatusForbidden {
		// The credentials authenticate (Kibana delegates to Elasticsearch) but
		// the role carries no Kibana feature privilege. Called out explicitly
		// because the generic message ("Unauthorized to find rules for any rule
		// types") does not say what to grant.
		return nil, fmt.Errorf("kibana returned 403 listing rules: the role needs Kibana feature privileges "+
			"(e.g. application kibana-.kibana, privilege feature_stackAlerts.read): %s", string(body))
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("kibana API error (status %d): %s", resp.StatusCode, string(body))
	}

	var parsed kibanaFindResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("failed to parse Kibana rule list: %w", err)
	}
	return &parsed, nil
}

func kibanaRuleToExternalRule(r kibanaRule) ExternalAlertRule {
	labels := kibanaTagsToLabels(r.Tags)

	return ExternalAlertRule{
		ExternalRuleId: r.Id,
		Name:           r.Name,
		AlertType:      kibanaRuleAlertType(r.RuleTypeId),
		Query:          kibanaRuleQuery(r),
		Severity:       kibanaRuleSeverity(labels["severity"]),
		Duration:       r.Schedule.Interval,
		Annotations: map[string]string{
			"summary": r.Name,
		},
		Labels: labels,
		// A muted rule still evaluates but notifies nothing, so for our purposes
		// it is not active.
		Enabled: r.Enabled && !r.MuteAll,
		ProviderConfig: map[string]any{
			"kibana_rule_type_id": r.RuleTypeId,
			"kibana_consumer":     r.Consumer,
			"kibana_tags":         r.Tags,
		},
	}
}

// kibanaRuleQuery extracts something human-meaningful as the rule expression.
// Kibana rule params are per-rule-type, so there is no single field: the ES
// query rule carries `esQuery`, others carry their own shape. Falls back to the
// whole params object so the row is never left with an empty expression.
func kibanaRuleQuery(r kibanaRule) string {
	if r.Params == nil {
		return ""
	}
	if q, ok := r.Params["esQuery"].(string); ok && q != "" {
		return q
	}
	if q, ok := r.Params["searchConfiguration"]; ok {
		if encoded, err := json.Marshal(q); err == nil {
			return string(encoded)
		}
	}
	encoded, err := json.Marshal(r.Params)
	if err != nil {
		return ""
	}
	return string(encoded)
}

// kibanaTagsToLabels turns Kibana's free-form string tags into labels. Kibana
// tags are not key:value, but operators commonly write them that way
// (`severity:critical`), so a colon is honoured when present.
func kibanaTagsToLabels(tags []string) map[string]string {
	labels := make(map[string]string, len(tags))
	for _, tag := range tags {
		key, value, found := strings.Cut(tag, ":")
		if key == "" {
			continue
		}
		if !found {
			labels[key] = ""
			continue
		}
		labels[key] = value
	}
	return labels
}

// kibanaRuleAlertType maps a Kibana rule type onto event_rules.alert_type,
// which is FK-constrained to 'log' / 'metric'. The ES query and log threshold
// rules count documents; everything else is treated as a metric rule.
func kibanaRuleAlertType(ruleTypeId string) string {
	switch ruleTypeId {
	case ".es-query", "logs.alert.document.count":
		return "log"
	default:
		return "metric"
	}
}

// kibanaRuleSeverity maps to event_rule_severity, which holds only 'critical'
// and 'warning'. Kibana rules carry no severity concept, so the only signal is
// an operator-written `severity:` tag.
func kibanaRuleSeverity(severityTag string) string {
	switch strings.ToLower(strings.TrimSpace(severityTag)) {
	case "critical", "error", "fatal", "high", "p1", "p2":
		return "critical"
	default:
		return "warning"
	}
}

// kibanaHeaders builds auth headers for Kibana. Kibana delegates authentication
// to Elasticsearch, so the integration's own credentials apply unchanged — an
// API key is sent as `ApiKey`, otherwise basic auth.
func kibanaHeaders(cfg *elasticsearchConfig) map[string]string {
	headers := map[string]string{
		"Content-Type": "application/json",
	}
	if cfg.ApiKey != "" {
		headers["Authorization"] = "ApiKey " + cfg.ApiKey
		return headers
	}
	if cfg.Username != "" && cfg.Password != "" {
		headers["Authorization"] = "Basic " +
			base64.StdEncoding.EncodeToString([]byte(cfg.Username+":"+cfg.Password))
	}
	return headers
}
