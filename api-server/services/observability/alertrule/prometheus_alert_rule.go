package alertrule

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	neturl "net/url"
	"strings"
	"time"

	"nudgebee/services/integrations/promclient"
	"nudgebee/services/security"

	"github.com/prometheus/common/model"
)

// PrometheusSaasAlertRuleSource manages alert rules through the Cortex ruler API
// that Mimir, Cortex and Grafana Cloud expose, for a user-configured (non-agent)
// Prometheus integration. It follows LokiSaasAlertRuleSource: one rule group per
// rule under the nudgebee namespace, because on the ruler API the group name is
// the group's identity, so writing every rule under one name would make each
// create overwrite the last.
//
// The agent path is untouched: an in-cluster Prometheus gets its rules as a
// PrometheusRule CR over the relay (eventrule.CreateEventRule), never through here.
type PrometheusSaasAlertRuleSource struct{}

const (
	// prometheusRulerNamespace is the ruler namespace Nudgebee owns.
	prometheusRulerNamespace = "nudgebee"
	// prometheusRulerPath is the Cortex ruler API, relative to the ruler base URL —
	// which already carries any prefix the API sits under (/prometheus, /api/prom).
	prometheusRulerPath = "/config/v1/rules"
)

// PrometheusNoRulerReason says, in operator terms, why a direct Prometheus
// integration with no declared ruler cannot take alert rules. The capability the
// UI reads and the triage apply gate both surface it, so every screen says the
// same thing.
const PrometheusNoRulerReason = "this Prometheus integration has no rule-management API configured — set Ruler type to Mimir / Cortex / Grafana Cloud on the integration, or manage rules in Prometheus itself"

var errPrometheusNoRuler = errors.New(PrometheusNoRulerReason)

func (s *PrometheusSaasAlertRuleSource) CreateAlertRule(ctx *security.RequestContext, config AlertRuleConfig) (*AlertRuleResult, error) {
	cfg, err := prometheusRulerFor(ctx, config.AccountId)
	if err != nil {
		return nil, err
	}

	// The group is named after the rule so each rule is its own addressable group.
	if err := postPrometheusRuleGroup(ctx, cfg, buildRuleGroup(config, config.Name)); err != nil {
		return nil, fmt.Errorf("failed to create Prometheus alert rule: %w", err)
	}

	return &AlertRuleResult{
		ExternalRuleId: config.Name,
		Name:           config.Name,
		Status:         "created",
	}, nil
}

func (s *PrometheusSaasAlertRuleSource) UpdateAlertRule(ctx *security.RequestContext, externalRuleId string, config AlertRuleConfig) (*AlertRuleResult, error) {
	cfg, err := prometheusRulerFor(ctx, config.AccountId)
	if err != nil {
		return nil, err
	}

	// The ruler API has no update verb: re-POSTing a group replaces it, so the
	// group name must stay the one this rule was created under.
	if err := postPrometheusRuleGroup(ctx, cfg, buildRuleGroup(config, externalRuleId)); err != nil {
		return nil, fmt.Errorf("failed to update Prometheus alert rule: %w", err)
	}

	return &AlertRuleResult{
		ExternalRuleId: externalRuleId,
		Name:           config.Name,
		Status:         "updated",
	}, nil
}

func (s *PrometheusSaasAlertRuleSource) DeleteAlertRule(ctx *security.RequestContext, accountId string, externalRuleId string) error {
	cfg, err := prometheusRulerFor(ctx, accountId)
	if err != nil {
		return err
	}
	if err := deletePrometheusRuleGroup(ctx, cfg, externalRuleId); err != nil {
		return fmt.Errorf("failed to delete Prometheus alert rule: %w", err)
	}
	return nil
}

// ListAlertRules enumerates the alerting rules the endpoint evaluates, from the
// Prometheus rules API every compatible engine serves (VictoriaMetrics excepted —
// it answers 404, vmalert holds its rules). It reads the query endpoint, not the
// ruler, so it works whether or not a ruler is declared: the inventory Alert
// Tuning works from is mostly the customer's own rules, which Nudgebee never wrote.
func (s *PrometheusSaasAlertRuleSource) ListAlertRules(ctx *security.RequestContext, accountId string) ([]ExternalAlertRule, error) {
	cfg, err := getPrometheusUserConfig(ctx, accountId)
	if err != nil {
		return nil, err
	}
	return listPrometheusRules(ctx, cfg)
}

// prometheusRulerFor resolves the account's direct Prometheus connection addressed
// at its ruler, refusing — before any request — when no ruler is declared.
func prometheusRulerFor(ctx *security.RequestContext, accountId string) (promclient.PrometheusUserConfig, error) {
	cfg, err := getPrometheusUserConfig(ctx, accountId)
	if err != nil {
		return cfg, err
	}
	return prometheusRulerFromConfig(cfg)
}

func prometheusRulerFromConfig(cfg promclient.PrometheusUserConfig) (promclient.PrometheusUserConfig, error) {
	if !cfg.HasRuler() {
		return cfg, errPrometheusNoRuler
	}
	return cfg.Ruler(), nil
}

// PrometheusRulerConfigured reports whether the account's direct Prometheus
// integration declares a ruler API — the precondition for writing alert rules to
// it — and, when it does not, the reason to show the operator.
func PrometheusRulerConfigured(ctx *security.RequestContext, accountId string) (bool, string) {
	cfg, err := getPrometheusUserConfig(ctx, accountId)
	if err != nil {
		return false, "no direct Prometheus integration is configured for this account"
	}
	if !cfg.HasRuler() {
		return false, PrometheusNoRulerReason
	}
	return true, ""
}

// postPrometheusRuleGroup writes a rule group into the Nudgebee ruler namespace.
func postPrometheusRuleGroup(ctx *security.RequestContext, cfg promclient.PrometheusUserConfig, ruleGroup string) error {
	// The body is JSON, which the ruler's YAML parser accepts as a YAML subset; the
	// endpoint only advertises application/yaml.
	resp, err := cfg.Do(requestContext(ctx), http.MethodPost, prometheusRulerPath+"/"+prometheusRulerNamespace, nil, "application/yaml", []byte(ruleGroup))
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	return checkPrometheusRulerStatus(resp, "write")
}

// deletePrometheusRuleGroup removes one group from the Nudgebee ruler namespace.
func deletePrometheusRuleGroup(ctx *security.RequestContext, cfg promclient.PrometheusUserConfig, externalRuleId string) error {
	path := prometheusRulerPath + "/" + prometheusRulerNamespace + "/" + neturl.PathEscape(externalRuleId)
	resp, err := cfg.Do(requestContext(ctx), http.MethodDelete, path, nil, "", nil)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	// A group that is already gone is the state the caller asked for.
	if resp.StatusCode == http.StatusNotFound {
		slog.Info("prometheus alert rule already absent on delete", "rule", externalRuleId)
		return nil
	}
	return checkPrometheusRulerStatus(resp, "delete")
}

// checkPrometheusRulerStatus turns a ruler response into an operator-facing error.
func checkPrometheusRulerStatus(resp *http.Response, operation string) error {
	if resp.StatusCode >= 200 && resp.StatusCode <= 299 {
		return nil
	}
	bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	body := strings.TrimSpace(string(bodyBytes))

	switch {
	case isNoRulerAPI(resp.StatusCode, body):
		// An endpoint without the ruler API says so in its own dialect: 404 or 405
		// on a path it does not serve, and VictoriaMetrics a 400 whose body reads
		// `unsupported path requested`.
		return fmt.Errorf("the endpoint has no ruler API (HTTP %d on %s) — rule %s needs Mimir, Cortex or Grafana Cloud; a plain Prometheus, Thanos or VictoriaMetrics cannot store rules written by Nudgebee", resp.StatusCode, requestPath(resp), operation)
	case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden:
		return fmt.Errorf("the ruler rejected the credentials (HTTP %d) for rule %s — a Grafana Cloud token needs the rules:write scope: %s", resp.StatusCode, operation, body)
	}
	return fmt.Errorf("prometheus ruler %s: HTTP %d: %s", operation, resp.StatusCode, body)
}

// isNoRulerAPI reports whether a ruler response means "this endpoint does not
// serve the ruler API" rather than a transient failure. Mimir and Cortex answer
// 404/405 on an unserved path; VictoriaMetrics answers 400 and names the reason
// in the body, which would otherwise surface as an unexplained bad request.
func isNoRulerAPI(status int, body string) bool {
	switch status {
	case http.StatusNotFound, http.StatusMethodNotAllowed:
		return true
	case http.StatusBadRequest:
		return strings.Contains(strings.ToLower(body), "unsupported path")
	}
	return false
}

// prometheusRuleGroup is one group of GET /api/v1/rules. Mimir reports the ruler
// namespace as `file`, which is how a group written by Nudgebee is recognised.
type prometheusRuleGroup struct {
	Name  string `json:"name"`
	File  string `json:"file"`
	Rules []struct {
		Type        string            `json:"type"`
		Name        string            `json:"name"`
		Query       string            `json:"query"`
		Duration    float64           `json:"duration"`
		Labels      map[string]string `json:"labels"`
		Annotations map[string]string `json:"annotations"`
	} `json:"rules"`
}

func listPrometheusRules(ctx *security.RequestContext, cfg promclient.PrometheusUserConfig) ([]ExternalAlertRule, error) {
	resp, err := cfg.DoGet(requestContext(ctx), "/api/v1/rules", neturl.Values{"type": []string{"alert"}})
	if err != nil {
		return nil, fmt.Errorf("failed to list Prometheus alert rules: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, fmt.Errorf("failed to read Prometheus rules response: %w", err)
	}
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, fmt.Errorf("the endpoint does not serve /api/v1/rules (HTTP 404), so its rules cannot be listed — check the URL, including any path prefix the API is served under")
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("failed to list Prometheus alert rules: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var payload struct {
		Status string `json:"status"`
		Error  string `json:"error"`
		Data   struct {
			Groups []prometheusRuleGroup `json:"groups"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("failed to parse Prometheus rules response: %w", err)
	}
	if payload.Status == "error" {
		return nil, fmt.Errorf("failed to list Prometheus alert rules: %s", payload.Error)
	}
	return prometheusRulesToExternal(payload.Data.Groups), nil
}

// prometheusRulesToExternal flattens rule groups into the sync shape. The external
// id is `<namespace>/<group>` for reconciliation only: synced rows land on the
// webhook source, which never dispatches back to the ruler. Groups in the nudgebee
// namespace are the ones Nudgebee wrote; everything else is the customer's and is
// flagged so the UI keeps it read-only.
func prometheusRulesToExternal(groups []prometheusRuleGroup) []ExternalAlertRule {
	var out []ExternalAlertRule
	for _, group := range groups {
		for _, rule := range group.Rules {
			if rule.Type != "alerting" {
				continue
			}
			out = append(out, ExternalAlertRule{
				ExternalRuleId: group.File + "/" + group.Name,
				Name:           rule.Name,
				AlertType:      "metric",
				Query:          rule.Query,
				Severity:       rule.Labels["severity"],
				Duration:       prometheusDuration(rule.Duration),
				Annotations:    rule.Annotations,
				Labels:         rule.Labels,
				Enabled:        true,
				ProviderConfig: map[string]any{
					"namespace":           group.File,
					"group":               group.Name,
					"managed_by_nudgebee": group.File == prometheusRulerNamespace,
				},
			})
		}
	}
	return out
}

// prometheusDuration renders the rules API's `duration` seconds as the `for`
// literal a rule file takes ("5m"), which is what event_rules.duration holds.
func prometheusDuration(seconds float64) string {
	if seconds <= 0 {
		return ""
	}
	return model.Duration(time.Duration(seconds * float64(time.Second))).String()
}

func requestContext(ctx *security.RequestContext) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx.GetContext()
}

func requestPath(resp *http.Response) string {
	if resp.Request != nil && resp.Request.URL != nil {
		return resp.Request.URL.Path
	}
	return ""
}
