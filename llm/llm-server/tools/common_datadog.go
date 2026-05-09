package tools

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"nudgebee/llm/common"
	"nudgebee/llm/tools/core"
	"regexp"
	"strings"
	"time"
)

// cliTimeFlagRE matches CLI-style time flags the LLM sometimes appends to Datadog queries
// (e.g. `--start-time 2026-04-28T03:44:44Z`, `--from=now-1h`). Datadog's metric/log query
// parsers reject unknown tokens with `Rule 'scope_expr' didn't match`, so the values must
// be hoisted into Arguments and stripped from the query string.
var cliTimeFlagRE = regexp.MustCompile(`\s*--(start[-_]time|end[-_]time|from|to)[=\s]+("[^"]*"|'[^']*'|\S+)`)

// stripCLITimeFlags removes CLI-style time flags from a Datadog query string and hoists
// their values into args (preserving any pre-existing keys). Returns the cleaned query.
// Shared across DatadogLogExecuteTool and DatadogMetricsExecuteTool.
func stripCLITimeFlags(query string, args map[string]any) string {
	matches := cliTimeFlagRE.FindAllStringSubmatch(query, -1)
	if len(matches) == 0 {
		return query
	}
	if args != nil {
		for _, m := range matches {
			flag := strings.ReplaceAll(m[1], "-", "_")
			val := strings.Trim(m[2], `"'`)
			switch flag {
			case "from":
				flag = "start_time"
			case "to":
				flag = "end_time"
			}
			if _, exists := args[flag]; !exists {
				args[flag] = val
			}
		}
	}
	return strings.TrimSpace(cliTimeFlagRE.ReplaceAllString(query, ""))
}

// normalizeDatadogSite cleans up common site URL formatting issues coming from integration
// configuration. Strips protocol prefixes, trailing slashes, and auto-corrects bare Datadog
// domains (e.g. "datadoghq.com") to their app-prefixed API-serving equivalents — bare
// marketing domains return HTML and break the JSON unmarshal in doDatadogRequest.
// Mirrors api-server/services/integrations/datadog.go:NormalizeDatadogSite so the same
// site value works whether routed through api-server or directly from llm-server tools.
func normalizeDatadogSite(site string) string {
	site = strings.TrimSpace(site)
	site = strings.TrimPrefix(site, "https://")
	site = strings.TrimPrefix(site, "http://")
	site = strings.TrimRight(site, "/")
	site = strings.TrimPrefix(site, "www.")

	switch site {
	case "datadoghq.com":
		return "app.datadoghq.com"
	case "datadoghq.eu":
		return "app.datadoghq.eu"
	case "ddog-gov.com":
		return "app.ddog-gov.com"
	}
	return site
}

// unwrapJSONQuery handles the case where the LLM packs the query into a JSON blob like
// `{"query":"...","start_time":"..."}`. Returns the extracted query string if parseable,
// otherwise the original. Looks for `query`, `command`, or `q` keys.
func unwrapJSONQuery(query string) string {
	if !strings.HasPrefix(query, "{") {
		return query
	}
	var parsed map[string]string
	if err := common.UnmarshalJson([]byte(query), &parsed); err != nil {
		return query
	}
	for _, key := range []string{"command", "query", "q"} {
		if v, ok := parsed[key]; ok && v != "" {
			return strings.TrimSpace(v)
		}
	}
	return query
}

func getDataDogConfigs(ctx core.NbToolContext) (string, string, string, error) {
	var err error
	apiKey := ""
	appKey := ""
	site := "api.datadoghq.com"

	for _, cfgValue := range ctx.ToolConfig.Values {
		if strings.EqualFold(cfgValue.Name, "site") && cfgValue.Value != "" {
			site = cfgValue.Value
		} else if strings.EqualFold(cfgValue.Name, "api_key") && cfgValue.Value != "" {
			apiKey = cfgValue.Value
			if cfgValue.IsEncrypted {
				apiKey, err = common.Decrypt(cfgValue.Value)
				if err != nil {
					ctx.Ctx.GetLogger().Error("datadog: unable to decrypt api_key", "error", err)
					return "", "", "", err
				}
			}
		} else if strings.EqualFold(cfgValue.Name, "app_key") && cfgValue.Value != "" {
			appKey = cfgValue.Value
			if cfgValue.IsEncrypted {
				appKey, err = common.Decrypt(cfgValue.Value)
				if err != nil {
					ctx.Ctx.GetLogger().Error("datadog: unable to decrypt app_key", "error", err)
					return "", "", "", err
				}
			}
		}
	}
	if apiKey == "" || appKey == "" { // Check after potential overrides
		return "", "", "", errors.New("datadog: api_key and app_key are required. Please check environment variables (DD_API_KEY, DD_APP_KEY) or tool configuration")
	}
	return apiKey, appKey, normalizeDatadogSite(site), nil
}

// doDatadogRequest is a helper function to perform HTTP requests to Datadog APIs
// and parse the JSON response.
func doDatadogRequest(req *http.Request, apiKey, appKey string) (map[string]any, error) {
	client := &http.Client{Timeout: 30 * time.Second}

	req.Header.Set("DD-API-KEY", apiKey)
	req.Header.Set("DD-APPLICATION-KEY", appKey)
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			fmt.Printf("Error closing response body: %v\n", err)
		}
	}()

	body, bodyReadErr := io.ReadAll(resp.Body)
	if bodyReadErr != nil {
		return nil, fmt.Errorf("failed to read datadog response body: %w", bodyReadErr)
	}

	if resp.StatusCode != http.StatusOK {
		// Try to unmarshal into DatadogErrorResponse for better error messages
		var ddError struct {
			Errors []string `json:"errors"`
		}
		if common.UnmarshalJson(body, &ddError) == nil && len(ddError.Errors) > 0 {
			return nil, fmt.Errorf("datadog api error: %s", strings.Join(ddError.Errors, "; "))
		}
		return nil, fmt.Errorf("datadog api error (status %d): %s", resp.StatusCode, string(body))
	}

	var result map[string]any
	if unmarshalErr := common.UnmarshalJson(body, &result); unmarshalErr != nil {
		return nil, fmt.Errorf("failed to unmarshal datadog response JSON: %w. Response body: %s", unmarshalErr, string(body))
	}
	return result, nil
}
