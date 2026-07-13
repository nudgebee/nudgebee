package tools

import (
	"context"
	"encoding/base64"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"nudgebee/llm/common"
)

// Runbook resolution turns a `runbook_url` alert label into runbook *content*
// for the automatic event-RCA prompt. Prometheus/Alertmanager and NewRelic
// alerts carry this label (see api-server prometheus_alertmanager_wehbook.go /
// newrelic_webhook.go); historically the analyzer only consulted the manual
// `knowledge_base` DB table keyed on the aggregation key, so the runbook the
// operator actually attached to the alert never reached RCA. ResolveRunbook
// closes that gap with three source routes chosen by the URL shape.
const (
	// runbookFetchTimeout bounds the synchronous fetch on the RCA prompt-build
	// path — a slow runbook host must not stall event analysis. On timeout the
	// caller falls back to the DB knowledge_base.
	runbookFetchTimeout = 10 * time.Second
	// runbookMaxReadBytes caps how much of the response body we read before
	// stripping, guarding against a pathologically large page.
	runbookMaxReadBytes = 512 * 1024
	// runbookMaxContentChars caps the stripped text injected into the prompt so
	// a long runbook can't blow the token budget.
	runbookMaxContentChars = 8000
)

var (
	// Confluence Cloud page URLs embed the numeric page id after /pages/
	// (…/wiki/spaces/SPACE/pages/12345/Title) or as a ?pageId= query param.
	confluencePageIDRe = regexp.MustCompile(`/pages/(\d+)`)
	confluencePageQPRe = regexp.MustCompile(`[?&]pageId=(\d+)`)
	// ServiceNow KB deep links carry the article number (KB0010001) or the
	// record sys_id in a query param.
	serviceNowNumberRe = regexp.MustCompile(`(?i)(?:sysparm_article|number)=(KB\d+)`)
	serviceNowSysIDRe  = regexp.MustCompile(`(?i)(?:sys_id|kb)=([0-9a-f]{32})`)
	// HTML cleanup for fetched runbook bodies.
	runbookScriptStyleRe = regexp.MustCompile(`(?is)<(script|style)\b[^>]*>.*?</(script|style)>`)
	runbookHTMLTagRe     = regexp.MustCompile(`(?s)<[^>]+>`)
	runbookBlankLinesRe  = regexp.MustCompile(`\n[ \t]*\n(?:[ \t]*\n)+`)
	runbookTitleRe       = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
)

// RunbookRef is a resolved runbook: its cleaned body plus the metadata needed
// to cite it back to the user (title, source URL, and which integration served
// it). Source is one of "confluence", "servicenow", "public".
type RunbookRef struct {
	Text   string
	Title  string
	URL    string
	Source string
}

// ResolveRunbook fetches runbook content for rawURL, authenticating against the
// account's Confluence or ServiceNow integration when the URL points at one and
// falling back to an unauthenticated GET for public runbooks. It returns the
// cleaned, length-capped body plus citation metadata, or (zero, err) on any
// failure so the caller can fall back to the DB knowledge_base. It never panics
// and never blocks longer than runbookFetchTimeout on the network.
func ResolveRunbook(accountId, rawURL string) (RunbookRef, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return RunbookRef{}, fmt.Errorf("runbook: empty url")
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return RunbookRef{}, fmt.Errorf("runbook: parse url %q: %w", rawURL, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return RunbookRef{}, fmt.Errorf("runbook: unsupported url scheme %q", u.Scheme)
	}
	if u.Host == "" {
		return RunbookRef{}, fmt.Errorf("runbook: empty host in url %q", rawURL)
	}
	switch classifyRunbookSource(strings.ToLower(u.Host), u.Path, rawURL) {
	case "confluence":
		return fetchConfluenceRunbook(accountId, rawURL)
	case "servicenow":
		return fetchServiceNowRunbook(accountId, rawURL)
	default:
		return fetchPublicRunbook(rawURL)
	}
}

// classifyRunbookSource decides which fetch path a runbook_url takes. It routes
// to an integration only when the URL is unambiguously that integration: an
// Atlassian host, or a generic /wiki/ path that actually carries a Confluence
// page id (likewise a ServiceNow host, or a servicenow path carrying an article
// ref). Public wikis/blogs that merely contain "/wiki/" or "servicenow" in the
// URL stay on the public path instead of failing against an integration that
// can't resolve them.
func classifyRunbookSource(host, path, rawURL string) string {
	hasConfluencePageID := confluencePageIDRe.MatchString(rawURL) || confluencePageQPRe.MatchString(rawURL)
	if strings.Contains(host, "atlassian.net") || (strings.Contains(path, "/wiki/") && hasConfluencePageID) {
		return "confluence"
	}
	hasServiceNowRef := serviceNowNumberRe.MatchString(rawURL) || serviceNowSysIDRe.MatchString(rawURL)
	if strings.Contains(host, "service-now.com") || (strings.Contains(host, "servicenow") && hasServiceNowRef) {
		return "servicenow"
	}
	return "public"
}

// fetchPublicRunbook fetches a public runbook page (e.g. the prometheus-operator
// runbooks) with no credentials. The runbook_url host is attacker-controllable
// (it comes straight from an alert label), so this goes through the shared
// SSRF-guarded fetcher, which blocks loopback/private/link-local/metadata
// destinations at dial time (closing the DNS-rebinding window) and restricts
// the response to text-like content types. The Confluence/ServiceNow paths do
// not need this: they fetch the account's *configured* integration host, not
// the raw URL.
func fetchPublicRunbook(rawURL string) (RunbookRef, error) {
	_, data, err := common.FetchImageSafely(context.Background(), rawURL, common.SafeFetchOptions{
		MaxSizeBytes:        runbookMaxReadBytes,
		Timeout:             runbookFetchTimeout,
		AllowedMIMEPrefixes: []string{"text/", "application/json", "application/xhtml"},
	})
	if err != nil {
		return RunbookRef{}, fmt.Errorf("runbook: fetch %q: %w", rawURL, err)
	}
	text := htmlToRunbookText(string(data))
	if strings.TrimSpace(text) == "" {
		return RunbookRef{}, fmt.Errorf("runbook: %q returned no text", rawURL)
	}
	return RunbookRef{Text: text, Title: htmlTitle(string(data)), URL: rawURL, Source: "public"}, nil
}

// fetchConfluenceRunbook resolves a Confluence page id from the URL and pulls
// its rendered body via the Confluence REST content API, authenticated with the
// account's Confluence integration credentials.
func fetchConfluenceRunbook(accountId, rawURL string) (RunbookRef, error) {
	pageID := ""
	if m := confluencePageIDRe.FindStringSubmatch(rawURL); m != nil {
		pageID = m[1]
	} else if m := confluencePageQPRe.FindStringSubmatch(rawURL); m != nil {
		pageID = m[1]
	}
	if pageID == "" {
		return RunbookRef{}, fmt.Errorf("runbook: no confluence page id in %q", rawURL)
	}

	configs, err := getConfluenceIntegrationConfig(accountId)
	if err != nil {
		return RunbookRef{}, fmt.Errorf("runbook: confluence config: %w", err)
	}
	base := strings.TrimSuffix(configs["host"], "/")
	auth := base64.StdEncoding.EncodeToString([]byte(configs["username"] + ":" + configs["token"]))
	apiURL := fmt.Sprintf("%s/wiki/rest/api/content/%s?expand=body.view", base, pageID)

	resp, err := common.HttpGet(apiURL,
		common.HttpWithTimeout(runbookFetchTimeout),
		common.HttpWithHeaders(map[string]string{
			"Authorization": "Basic " + auth,
			"Content-Type":  "application/json",
		}),
	)
	if err != nil {
		return RunbookRef{}, fmt.Errorf("runbook: confluence GET: %w", err)
	}
	defer closeBody(resp)
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, runbookMaxReadBytes))
		return RunbookRef{}, fmt.Errorf("runbook: confluence API %d: %s", resp.StatusCode, common.SanitizeErrorMessage(string(body)))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, runbookMaxReadBytes))
	if err != nil {
		return RunbookRef{}, fmt.Errorf("runbook: confluence read: %w", err)
	}
	var content struct {
		Title string `json:"title"`
		Body  struct {
			View struct {
				Value string `json:"value"`
			} `json:"view"`
		} `json:"body"`
	}
	if err := common.UnmarshalJson(body, &content); err != nil {
		return RunbookRef{}, fmt.Errorf("runbook: confluence unmarshal: %w", err)
	}
	text := htmlToRunbookText(content.Body.View.Value)
	if content.Title != "" {
		text = content.Title + "\n\n" + text
	}
	if strings.TrimSpace(text) == "" {
		return RunbookRef{}, fmt.Errorf("runbook: confluence page %s empty", pageID)
	}
	return RunbookRef{Text: text, Title: content.Title, URL: rawURL, Source: "confluence"}, nil
}

// fetchServiceNowRunbook resolves a KB article number / sys_id from the URL and
// pulls the article body via the ServiceNow Table API (kb_knowledge),
// authenticated with the account's ServiceNow integration credentials.
func fetchServiceNowRunbook(accountId, rawURL string) (RunbookRef, error) {
	var query string
	if m := serviceNowNumberRe.FindStringSubmatch(rawURL); m != nil {
		query = "number=" + m[1]
	} else if m := serviceNowSysIDRe.FindStringSubmatch(rawURL); m != nil {
		query = "sys_id=" + m[1]
	} else {
		return RunbookRef{}, fmt.Errorf("runbook: no servicenow article ref in %q", rawURL)
	}

	configs, err := getServiceNowIntegrationConfig(accountId)
	if err != nil {
		return RunbookRef{}, fmt.Errorf("runbook: servicenow config: %w", err)
	}
	instance := strings.TrimSuffix(configs["url"], "/")
	if !strings.HasPrefix(instance, "http") {
		instance = "https://" + instance
	}
	auth := base64.StdEncoding.EncodeToString([]byte(configs["username"] + ":" + configs["password"]))
	apiURL := fmt.Sprintf("%s/api/now/table/kb_knowledge", instance)

	resp, err := common.HttpGet(apiURL,
		common.HttpWithTimeout(runbookFetchTimeout),
		common.HttpWithQueryParams(map[string]string{
			"sysparm_query":  query,
			"sysparm_fields": "number,short_description,text",
			"sysparm_limit":  "1",
		}),
		common.HttpWithHeaders(map[string]string{
			"Authorization": "Basic " + auth,
			"Accept":        "application/json",
		}),
	)
	if err != nil {
		return RunbookRef{}, fmt.Errorf("runbook: servicenow GET: %w", err)
	}
	defer closeBody(resp)
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, runbookMaxReadBytes))
		return RunbookRef{}, fmt.Errorf("runbook: servicenow API %d: %s", resp.StatusCode, common.SanitizeErrorMessage(string(body)))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, runbookMaxReadBytes))
	if err != nil {
		return RunbookRef{}, fmt.Errorf("runbook: servicenow read: %w", err)
	}
	var snResp struct {
		Result []struct {
			Number           string `json:"number"`
			ShortDescription string `json:"short_description"`
			Text             string `json:"text"`
		} `json:"result"`
	}
	if err := common.UnmarshalJson(body, &snResp); err != nil {
		return RunbookRef{}, fmt.Errorf("runbook: servicenow unmarshal: %w", err)
	}
	if len(snResp.Result) == 0 {
		return RunbookRef{}, fmt.Errorf("runbook: servicenow article not found for %q", query)
	}
	article := snResp.Result[0]
	text := htmlToRunbookText(article.Text)
	if article.ShortDescription != "" {
		text = article.ShortDescription + "\n\n" + text
	}
	if strings.TrimSpace(text) == "" {
		return RunbookRef{}, fmt.Errorf("runbook: servicenow article %s empty", article.Number)
	}
	return RunbookRef{Text: text, Title: article.ShortDescription, URL: rawURL, Source: "servicenow"}, nil
}

// getServiceNowIntegrationConfig mirrors getConfluenceIntegrationConfig for the
// ServiceNow integration: url / username / password, with the password
// decrypted best-effort (legacy plaintext rows fall through unchanged).
func getServiceNowIntegrationConfig(accountId string) (map[string]string, error) {
	query := `SELECT i.id, icv.name, icv.value FROM integrations i JOIN integrations_cloud_accounts ia ON i.id = ia.integration_id JOIN integration_config_values icv ON i.id = icv.integration_id WHERE i."type" = 'servicenow' AND ia.cloud_account_id = :ac_id`
	if err := sqlValidateReadOnly(query, ""); err != nil {
		return nil, err
	}

	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil, err
	}

	rows, err := dbManager.Db.NamedQuery(query, map[string]any{"ac_id": accountId})
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			fmt.Printf("Error closing rows: %v\n", err)
		}
	}()

	allconfig := make(map[string]map[string]string)
	for rows.Next() {
		var id, name, value string
		if err := rows.Scan(&id, &name, &value); err != nil {
			return nil, err
		}
		if name == "password" {
			if dec, derr := common.Decrypt(value); derr == nil {
				value = dec
			}
		}
		if _, ok := allconfig[id]; !ok {
			allconfig[id] = make(map[string]string)
		}
		allconfig[id][name] = value
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	requiredKeys := []string{"url", "username", "password"}
	for _, config := range allconfig {
		allKeysFound := true
		for _, key := range requiredKeys {
			if _, ok := config[key]; !ok {
				allKeysFound = false
				break
			}
		}
		if allKeysFound {
			return config, nil
		}
	}
	return nil, fmt.Errorf("unable to find servicenow integration config for account: %s", accountId)
}

// htmlToRunbookText reduces an HTML (or plain-text) runbook body to prompt-safe
// text: drops script/style blocks, strips tags (which also neutralises
// prompt-injection markup like </instructions>), unescapes entities, collapses
// blank-line runs, and caps the length.
func htmlToRunbookText(s string) string {
	// Unescape entities FIRST, then strip tags. Doing it the other way round
	// lets an attacker smuggle prompt-injection markup past the tag stripper by
	// HTML-encoding it (e.g. `&lt;instructions&gt;` survives the stripper and is
	// then unescaped into a live `<instructions>` tag). Unescaping first means
	// any `<...>` an entity decodes into is caught by the tag stripper. The
	// trade-off — literal placeholders like `<name>` written as `&lt;name&gt;`
	// are also stripped — is acceptable for external content bound for a prompt.
	s = html.UnescapeString(s)
	s = runbookScriptStyleRe.ReplaceAllString(s, " ")
	s = runbookHTMLTagRe.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = runbookBlankLinesRe.ReplaceAllString(s, "\n\n")
	s = strings.TrimSpace(s)
	return truncateForError(s, runbookMaxContentChars)
}

// htmlTitle extracts the <title> text from an HTML document, or "" if absent.
// Used to label public runbook pages that have no structured title field.
func htmlTitle(s string) string {
	m := runbookTitleRe.FindStringSubmatch(s)
	if m == nil {
		return ""
	}
	return strings.TrimSpace(html.UnescapeString(m[1]))
}

func closeBody(resp *http.Response) {
	if resp != nil && resp.Body != nil {
		if err := resp.Body.Close(); err != nil {
			fmt.Printf("Error closing response body: %v\n", err)
		}
	}
}
