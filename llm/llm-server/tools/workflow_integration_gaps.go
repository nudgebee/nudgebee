package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
)

// An automation step can point at an integration that is not there: a required integration left
// empty, or a {{ Configs.<key> }} whose config does not exist. The runbook server checks literal
// values when the automation is saved, but it leaves both of these to run time, where the step
// fails. These helpers find them from the task registry and the account's configs, so the builder
// can refuse to save such an automation and anyone asking "why doesn't it run" is told what is
// missing (#42204).

// IntegrationParam is a task parameter the runbook server declares as an integration reference
// (input_schema type "ticket" or "integration"), with the integration types it accepts.
type IntegrationParam struct {
	Kinds    []string
	Ticket   bool
	Required bool
}

// KindLabel names what a parameter needs, for a person: "jira or servicenow", "ticketing".
func (p IntegrationParam) KindLabel() string {
	switch {
	case len(p.Kinds) == 1:
		return p.Kinds[0]
	case len(p.Kinds) > 1:
		return strings.Join(p.Kinds[:len(p.Kinds)-1], ", ") + " or " + p.Kinds[len(p.Kinds)-1]
	case p.Ticket:
		return "ticketing"
	default:
		return "matching"
	}
}

// IntegrationGap is one step parameter that cannot point at a real integration.
type IntegrationGap struct {
	TaskID    string
	Param     string
	ConfigKey string // the missing config, or "" when the parameter is empty
	Need      IntegrationParam
}

// ListAccountIntegrations lists the account's integrations, as a seam for tests.
var ListAccountIntegrations = toolcore.ListAllToolConfigs

// auxiliaryCheckTimeout bounds the extra runbook calls these checks make, so a slow runbook server
// delays an answer by at most this much instead of hanging it.
const auxiliaryCheckTimeout = 15 * time.Second

// IsValidationRejection reports whether err is the runbook server rejecting a definition (HTTP
// 400), as opposed to the request failing or the server erroring.
func IsValidationRejection(err error) bool {
	var statusErr *runbookStatusError
	return errors.As(err, &statusErr) && statusErr.status == http.StatusBadRequest
}

var loneConfigRef = regexp.MustCompile(`^\{\{\s*Configs\.([A-Za-z0-9_.-]+)\s*\}\}$`)

// IntegrationParamsByTaskType reads a GET /tasks response ({"tasks":[...]} or a bare array) into
// task type → integration-typed parameters. An unreadable response yields an empty map.
func IntegrationParamsByTaskType(registry []byte) map[string]map[string]IntegrationParam {
	var tasks []map[string]interface{}
	var wrapped struct {
		Tasks []map[string]interface{} `json:"tasks"`
	}
	if err := json.Unmarshal(registry, &wrapped); err == nil && len(wrapped.Tasks) > 0 {
		tasks = wrapped.Tasks
	} else {
		_ = json.Unmarshal(registry, &tasks)
	}

	out := map[string]map[string]IntegrationParam{}
	for _, t := range tasks {
		name, _ := t["name"].(string)
		inputSchema, _ := t["input_schema"].(map[string]interface{})
		if name == "" || inputSchema == nil {
			continue
		}
		if params := parseIntegrationParams(inputSchema); len(params) > 0 {
			out[name] = params
		}
	}
	return out
}

func parseIntegrationParams(inputSchema map[string]interface{}) map[string]IntegrationParam {
	params := map[string]IntegrationParam{}
	for name, raw := range inputSchema {
		prop, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		propType, _ := prop["type"].(string)
		if propType != "ticket" && propType != "integration" {
			continue
		}
		p := IntegrationParam{Ticket: propType == "ticket"}
		p.Required, _ = prop["required"].(bool)
		if subTypes, ok := prop["sub_types"].([]interface{}); ok {
			for _, st := range subTypes {
				if s, ok := st.(string); ok && s != "" {
					p.Kinds = append(p.Kinds, s)
				}
			}
		}
		if len(p.Kinds) == 0 {
			if st, _ := prop["sub_type"].(string); st != "" {
				p.Kinds = []string{st}
			}
		}
		params[name] = p
	}
	return params
}

// FindIntegrationGaps walks a workflow's steps (nested steps and hooks included) and returns the
// integration parameters that resolve to nothing. A step type with no known integration
// parameters is skipped, so an unreadable registry finds nothing rather than guessing.
func FindIntegrationGaps(workflow map[string]interface{}, paramsByType map[string]map[string]IntegrationParam, configExists func(key string) bool) []IntegrationGap {
	var tasks []interface{}
	def, _ := workflow["definition"].(map[string]interface{})
	if def != nil {
		tasks, _ = def["tasks"].([]interface{})
	}
	if tasks == nil {
		tasks, _ = workflow["tasks"].([]interface{})
	}

	var gaps []IntegrationGap
	checkStep := func(step map[string]interface{}, label string) {
		stepType, _ := step["type"].(string)
		needs := paramsByType[stepType]
		if len(needs) == 0 {
			return
		}
		params, _ := step["params"].(map[string]interface{})
		names := make([]string, 0, len(needs))
		for name := range needs {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			need := needs[name]
			value, _ := params[name].(string)
			value = strings.TrimSpace(value)
			switch {
			case value == "":
				if need.Required {
					gaps = append(gaps, IntegrationGap{TaskID: label, Param: name, Need: need})
				}
			case loneConfigRef.MatchString(value):
				key := loneConfigRef.FindStringSubmatch(value)[1]
				if !configExists(key) {
					gaps = append(gaps, IntegrationGap{TaskID: label, Param: name, ConfigKey: key, Need: need})
				}
			}
		}
	}
	checkHooks := func(hooks interface{}, owner string) {
		h, _ := hooks.(map[string]interface{})
		for _, phase := range []string{"success", "failure", "always"} {
			actions, _ := h[phase].([]interface{})
			for i, raw := range actions {
				if action, ok := raw.(map[string]interface{}); ok {
					checkStep(action, fmt.Sprintf("%s (%s hook %d)", owner, phase, i+1))
				}
			}
		}
	}
	var walk func(items []interface{})
	walk = func(items []interface{}) {
		for _, raw := range items {
			step, ok := raw.(map[string]interface{})
			if !ok {
				continue
			}
			id, _ := step["id"].(string)
			if id == "" {
				id = "<unnamed>"
			}
			checkStep(step, id)
			checkHooks(step["hooks"], id)
			if nested, ok := step["tasks"].([]interface{}); ok {
				walk(nested)
			}
			if params, ok := step["params"].(map[string]interface{}); ok {
				if nested, ok := params["tasks"].([]interface{}); ok {
					walk(nested)
				}
			}
		}
	}
	walk(tasks)
	if def != nil {
		checkHooks(def["hooks"], "automation")
	}
	return gaps
}

// IntegrationGapLines says, per gap, what the step needs and what the person can do about it. It
// names the account's integrations of the needed kind when there are some, and says there are
// none only when the list was actually read: a failed listing must not read as "none connected".
// The list comes from a cache, so it shapes the wording, never whether a gap is reported.
func IntegrationGapLines(ctx *security.RequestContext, accountId string, gaps []IntegrationGap) []string {
	byKind := map[string][]string{}
	configs, listErr := ListAccountIntegrations(ctx, accountId)
	if listErr != nil {
		ctx.GetLogger().Warn("workflow: could not list integrations to describe a missing one", "error", listErr)
	}
	for _, cfg := range configs {
		kind := strings.ToLower(cfg.Schema.ConfigType)
		byKind[kind] = append(byKind[kind], cfg.Name)
	}

	var lines []string
	for _, g := range gaps {
		var available []string
		for _, k := range g.Need.Kinds {
			available = append(available, byKind[k]...)
		}
		sort.Strings(available)

		what := fmt.Sprintf("Step `%s` needs a %s integration", g.TaskID, g.Need.KindLabel())
		if g.ConfigKey != "" {
			what += fmt.Sprintf(", but it points at the config `%s`, which does not exist", g.ConfigKey)
		} else {
			what += ", but none is set"
		}
		switch {
		case len(available) > 0:
			lines = append(lines, fmt.Sprintf("- %s. This account has: %s. Ask me again and name the one to use.", what, strings.Join(available, ", ")))
		case listErr != nil:
			lines = append(lines, fmt.Sprintf("- %s. Name the %s integration to use (connect one under Integrations if the account has none), then ask me again.", what, g.Need.KindLabel()))
		default:
			lines = append(lines, fmt.Sprintf("- %s, and this account has no %s integration connected. Connect one under Integrations, then ask me again.", what, g.Need.KindLabel()))
		}
	}
	return lines
}

// WorkflowIntegrationGaps checks a workflow against the task registry and the account's configs,
// both read from the runbook server. It returns nil when either cannot be read: "we could not
// check" must not read as "something is missing".
func WorkflowIntegrationGaps(ctx *security.RequestContext, accountId string, workflow map[string]interface{}) []IntegrationGap {
	tenantId := ctx.GetSecurityContext().GetTenantId()
	userId := ctx.GetSecurityContext().GetUserId()

	reqCtx, cancel := context.WithTimeout(ctx.GetContext(), auxiliaryCheckTimeout)
	defer cancel()

	registry, err := DoRunbookRequestWithContext(reqCtx, "GET", "tasks", nil, accountId, tenantId, userId)
	if err != nil {
		ctx.GetLogger().Warn("workflow: task registry unavailable, skipping the integration check", "error", err)
		return nil
	}
	paramsByType := IntegrationParamsByTaskType(registry)
	if len(paramsByType) == 0 {
		return nil
	}

	// configKeys stays nil when the configs cannot be read; every key then counts as present,
	// because "could not tell" must not be reported as "missing".
	var configKeys map[string]bool
	var fetched bool
	configExists := func(key string) bool {
		if !fetched {
			fetched = true
			resp, err := DoRunbookRequestWithContext(reqCtx, "GET", "configs", nil, accountId, tenantId, userId)
			var configs []struct {
				Key string `json:"key"`
			}
			if err == nil {
				err = json.Unmarshal(resp, &configs)
			}
			if err != nil {
				ctx.GetLogger().Warn("workflow: configs unavailable, skipping the integration check", "error", err)
			} else {
				configKeys = map[string]bool{}
				for _, c := range configs {
					configKeys[c.Key] = true
				}
			}
		}
		return configKeys == nil || configKeys[key]
	}
	return FindIntegrationGaps(workflow, paramsByType, configExists)
}
