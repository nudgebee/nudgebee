package tools

import (
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/tools/core"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/playwright-community/playwright-go"
)

// validateURL checks if the URL is safe to crawl (prevents SSRF)
func validateURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("invalid scheme: %s", u.Scheme)
	}

	host, _, err := net.SplitHostPort(u.Host)
	if err != nil {
		host = u.Host
	}

	// Resolve IP address
	ips, err := net.LookupIP(host)
	if err != nil {
		return nil // Could be a valid external domain that doesn't resolve in this env
	}

	for _, ip := range ips {
		if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsPrivate() {
			return fmt.Errorf("URL resolves to a restricted IP: %s", ip.String())
		}
	}

	return nil
}

var userAgents = []string{
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/109.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/109.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/108.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/108.0.0.0 Safari/537.36",
	"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/108.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/16.1 Safari/605.1.15",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 13_1) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/16.1 Safari/605.1.15",
}

// maxCrawlContentChars is the maximum character limit for crawled page content.
// 500KB is usually more than enough for any single page documentation.
const maxCrawlContentChars = 500000

// botBlockSignals are phrases found in the first 500 runes of bot-detection challenge
// pages (Cloudflare, DDoS-Guard, PerimeterX, etc.). Used to trigger the Serper scrape
// fallback before surfacing useless content to the caller.
var botBlockSignals = []string{
	"enable javascript", "checking your browser", "just a moment",
	"ddos protection", "verify you are human", "captcha",
	"bot detection", "please wait while", "attention required",
}

// isBotBlockPage returns true when the extracted page text looks like a bot-detection
// challenge. It only scans the first 500 rune characters to avoid false positives on
// legitimate pages that discuss captcha or bot detection in their content body.
func isBotBlockPage(content string) bool {
	trimmed := strings.TrimSpace(content)
	scanContent := trimmed
	count := 0
	for i := range trimmed {
		if count == 500 {
			scanContent = trimmed[:i]
			break
		}
		count++
	}
	lower := strings.ToLower(scanContent)
	for _, phrase := range botBlockSignals {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

var viewports = []playwright.Size{
	{Width: 1920, Height: 1080},
	{Width: 1366, Height: 768},
	{Width: 1536, Height: 864},
}

// getRandomElement returns a random element from a slice of strings
func getRandomElement(slice []string) string {
	return slice[rand.Intn(len(slice))]
}

// getRandomViewport returns a random viewport from the viewports slice
func getRandomViewport() playwright.Size {
	return viewports[rand.Intn(len(viewports))]
}

const ToolExecuteSearchCommand = "search_execute"

func init() {
	core.RegisterNBToolFactory(ToolExecuteSearchCommand, func(accountId string) (core.NBTool, error) {
		return SearchExecuteTool{}, nil
	})
}

type SearchExecuteTool struct {
}

func (m SearchExecuteTool) Name() string {
	return ToolExecuteSearchCommand
}

func (m SearchExecuteTool) GetType() core.NBToolType {
	return core.NBToolTypeTool
}

func (m SearchExecuteTool) Description() string {
	return `Searches google for relvent links for a given user query.

		**Usage:**

		* **Prioritize this tool:** Whenever you want to get website links related to a user query. 
		* **Input:** Provide a valid, search query.
		* **Output:** Link & descripts for user search.
		* Ensure the search query is concise & clear.

		**Examples:**

		* 'release notes for kubernetes'
		* 'helm command for deployment'

		`
}

func (m SearchExecuteTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"command": {
				Type:        core.ToolSchemaTypeString,
				Description: "search to perform",
			},
		},
		Required: []string{"command"},
	}
}

func (m SearchExecuteTool) searchGoogle(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) ([]map[string]string, error) {
	googleUrl := fmt.Sprintf("https://www.google.com/search?q=%s", url.QueryEscape(input.Command))

	crawlTool := CrawlExecuteTool{}
	resp, err := crawlTool.Call(nbRequestContext, core.NBToolCallRequest{
		Command: googleUrl,
		Arguments: map[string]any{
			"response_type": "html",
		},
	})
	if err != nil {
		return nil, err
	}

	// Parse the HTML content
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(resp.Data))
	if err != nil {
		return nil, err
	}

	// Extract the links from the search results
	links := []map[string]string{}
	doc.Find("#main a").Each(func(i int, s *goquery.Selection) {
		link, exists := s.Attr("href")
		if exists && strings.HasPrefix(link, "/url?") && strings.Contains(link, "url=") {
			link = strings.TrimPrefix(link, "/url?")
			parameters := strings.Split(link, "&")
			webUrl := ""
			for _, param := range parameters {
				if strings.HasPrefix(param, "url=") {
					webUrl = param[4:]
					break
				}
			}
			if webUrl == "" || strings.HasPrefix(webUrl, "/") || strings.Contains(webUrl, "google.com") {
				return
			}

			webUrl, err = url.QueryUnescape(webUrl)
			if err != nil {
				return
			}

			links = append(links, map[string]string{
				"url":  webUrl,
				"desc": s.Text(),
			})
		}
	})

	return links, nil
}

// searchDuckDuckGo performs a search on DuckDuckGo with the given query and extracts search results
func (m SearchExecuteTool) searchDuckDuckGo(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) ([]map[string]string, error) {
	// Construct the DuckDuckGo search URL with the properly escaped query
	duckduckUrl := fmt.Sprintf("https://duckduckgo.com/?q=%s&kp=1", url.QueryEscape(input.Command))
	//kp=1 is to set safe search to strict
	// Create a web crawler tool instance to fetch the search results page
	crawlTool := CrawlExecuteTool{}
	resp, err := crawlTool.Call(nbRequestContext, core.NBToolCallRequest{
		Command: duckduckUrl,
		Arguments: map[string]any{
			"response_type": "html",
		},
	})
	if err != nil {
		return nil, err
	}

	// Parse the HTML content using goquery for DOM manipulation
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(resp.Data))
	if err != nil {
		return nil, err
	}

	// Extract the links from the search results
	links := []map[string]string{}
	// Find all anchor tags within article elements in the search results

	doc.Find("article a").Each(func(i int, s *goquery.Selection) {
		// Extract the href attribute (URL) from the anchor tag
		link, exists := s.Attr("href")
		if exists {
			// Store both the URL and the description text in our results collection
			links = append(links, map[string]string{
				"url":  link,
				"desc": s.Text(),
			})
		}
	})

	return links, nil
}

func (m SearchExecuteTool) searchBrave(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) ([]map[string]string, error) {
	// Construct the Brave search URL with the properly escaped query
	braveUrl := fmt.Sprintf("https://search.brave.com/search?q=%s&safesearch=strict", url.QueryEscape(input.Command))
	//count=<number> doesnt work in a brave search
	// Create a web crawler tool instance to fetch the search results page
	crawlTool := CrawlExecuteTool{}
	resp, err := crawlTool.Call(nbRequestContext, core.NBToolCallRequest{
		Command: braveUrl,
		Arguments: map[string]any{
			"response_type": "html",
		},
	})
	if err != nil {
		return nil, err
	}

	nbRequestContext.Ctx.GetLogger().Info("search: searchBrave", "resp", resp.Data)
	// Parse the HTML content using goquery for DOM manipulation
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(resp.Data))
	if err != nil {
		return nil, err
	}

	// Extract the links from the search results
	links := []map[string]string{}
	// Find all anchor tags within article elements in the search results

	doc.Find(".snippet > a").Each(func(i int, s *goquery.Selection) {
		// Extract the href attribute (URL) from the anchor tag
		link, exists := s.Attr("href")
		if exists {
			// Store both the URL and the description text in our results collection
			links = append(links, map[string]string{
				"url":  link,
				"desc": s.Text(),
			})
		}
	})

	if len(links) > 5 {
		links = links[:5]
	}

	// we are not able to get links then so return full body and let LLM return the links
	if len(links) == 0 {
		nbRequestContext.Ctx.GetLogger().Error("search: no links found", "query", input.Command, "data", resp.Data)
		links = append(links, map[string]string{
			"url":   braveUrl,
			"_body": resp.Data,
		})
	}

	//limit the links to top 5
	return links, nil
}

const SerperApiUrl = "https://google.serper.dev/search"

type SerperResponse struct {
	Organic []struct {
		Title   string `json:"title"`
		Link    string `json:"link"`
		Snippet string `json:"snippet"`
	} `json:"organic"`
}

// searchSerper performs a search using the Serper API
func (m SearchExecuteTool) searchSerper(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) ([]map[string]string, error) {
	serperAPIKey := config.Config.LlmServerSerperApiKey
	if serperAPIKey == "" {
		return nil, fmt.Errorf("SERPER_API_KEY is not configured in appConfig")
	}

	headers := map[string]string{
		"X-API-KEY":    serperAPIKey,
		"Content-Type": "application/json",
	}

	// Use common.HttpPost with HttpOption functions
	resp, err := common.HttpPost(
		SerperApiUrl,
		common.HttpWithHeaders(headers),
		common.HttpWithJsonBody(map[string]string{"q": input.Command}),
		common.HttpWithTimeout(10*time.Second), // Default timeout, can be made configurable
	)
	if err != nil {
		return nil, fmt.Errorf("failed to send HTTP request to Serper API: %w", err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			nbRequestContext.Ctx.GetLogger().Error("search: failed to close response body", "error", cerr)
		}
	}() // Ensure body is closed

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("search: failed to read Serper API response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("search: serper API returned non-OK status: %d, body: %s", resp.StatusCode, string(bodyBytes))
	}

	var serperResponse SerperResponse
	err = json.Unmarshal(bodyBytes, &serperResponse)
	if err != nil {
		return nil, fmt.Errorf("search: failed to unmarshal Serper API response: %w", err)
	}

	links := []map[string]string{}
	for _, result := range serperResponse.Organic {
		links = append(links, map[string]string{
			"url":  result.Link,
			"desc": result.Title + " - " + result.Snippet, // Combine title and snippet for description
		})
	}

	return links, nil
}

func (m SearchExecuteTool) Call(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	nbRequestContext.Ctx.GetLogger().Info("search: executing tool call", "query", input.Command)
	input.Command = strings.TrimSpace(input.Command)
	var links []map[string]string
	var err error

	provider := config.Config.LlmServerSearchAgentProvider
	if providerAny, ok := input.Arguments["provider"]; ok {
		provider, _ = providerAny.(string)
	}

	switch provider {
	case "google":
		links, err = m.searchGoogle(nbRequestContext, input)
	case "duckduckgo":
		links, err = m.searchDuckDuckGo(nbRequestContext, input)
	case "serper":
		links, err = m.searchSerper(nbRequestContext, input)
	case "jina":
		links, err = searchViaJina(nbRequestContext, input.Command)
	default:
		links, err = m.searchBrave(nbRequestContext, input)
	}

	// If the primary provider failed and Jina is configured, fall back to it
	if err != nil && provider != "jina" && config.Config.LlmServerJinaApiKey != "" {
		nbRequestContext.Ctx.GetLogger().Warn("search: primary provider failed, falling back to Jina Search", "provider", provider, "error", err)
		if jinaLinks, jinaErr := searchViaJina(nbRequestContext, input.Command); jinaErr == nil {
			links, err = jinaLinks, nil
		}
	}

	if err != nil {
		return core.NBToolResponse{
			Data: "",
			Type: core.NBToolResponseTypeText,
		}, err
	}
	linksBytes, err := common.MarshalJson(links)
	if err != nil {
		return core.NBToolResponse{
			Data: "",
			Type: core.NBToolResponseTypeText,
		}, err
	}

	return core.NBToolResponse{
		Data: string(linksBytes),
		Type: core.NBToolResponseTypeJson,
	}, nil
}

const ToolExecuteCrawlCommand = "crawl_execute"

func init() {
	core.RegisterNBToolFactory(ToolExecuteCrawlCommand, func(accountId string) (core.NBTool, error) {
		return CrawlExecuteTool{}, nil
	})
}

type CrawlExecuteTool struct {
}

func (m CrawlExecuteTool) Name() string {
	return ToolExecuteCrawlCommand
}

func (m CrawlExecuteTool) GetType() core.NBToolType {
	return core.NBToolTypeTool
}

func (m CrawlExecuteTool) Description() string {
	return `Crawls website and returns content in text format

		**Usage:**

		* **Input:** Provide a valid url to crawl.
		* **Output:** Crawled content in text format.
		* Ensure that URL is correctlu formatted.

		**Examples:**

		* 'https://timesofindia.com's

		`
}

func (m CrawlExecuteTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"command": {
				Type:        core.ToolSchemaTypeString,
				Description: "url to crawl",
			},
		},
		Required: []string{"command"},
	}
}

// Call executes the web crawling functionality using Playwright to fetch content from a specified URL
func (m CrawlExecuteTool) Call(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	nbRequestContext.Ctx.GetLogger().Info("crawl: executing tool call", "query", input.Command)
	command := strings.TrimSpace(input.Command)

	if err := validateURL(command); err != nil {
		return core.NBToolResponse{}, fmt.Errorf("crawl: security validation failed: %w", err)
	}

	// Initialize Playwright automation framework
	pw, err := playwright.Run()
	if err != nil {
		// Playwright driver missing or version mismatch — try Jina Reader before giving up.
		// This commonly happens when the base image has a different driver version than go.mod.
		nbRequestContext.Ctx.GetLogger().Warn("crawl: playwright failed to launch, trying Jina Reader fallback", "url", command, "error", err.Error())
		return crawlViaJina(nbRequestContext, command)
	}
	// Ensure Playwright stops after execution completes
	defer func() {
		err := pw.Stop()
		if err != nil {
			nbRequestContext.Ctx.GetLogger().Error("crawl: unable to stop playwright", "error", err.Error())
		}
	}()

	// Launch headless Chromium browser with arguments to avoid bot detection
	browser, err := pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{
		Headless: playwright.Bool(true),
		Args: []string{
			"--disable-blink-features=AutomationControlled",
			"--disable-infobars",
			"--disable-popup-blocking",
			"--disable-notifications",
		},
	})
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("crawl: unable to launch chromium", "error", err.Error())
		return core.NBToolResponse{}, err
	}
	defer func() {
		if err := browser.Close(); err != nil {
			nbRequestContext.Ctx.GetLogger().Error("crawl: unable to close browser", "error", err.Error())
		}
	}()

	// Create a new browser context for the operation
	randomUserAgent := getRandomElement(userAgents)
	randomViewport := getRandomViewport()
	context, err := browser.NewContext(playwright.BrowserNewContextOptions{
		UserAgent:         playwright.String(randomUserAgent),
		Viewport:          &randomViewport,
		Locale:            playwright.String("en-US"),
		TimezoneId:        playwright.String("America/New_York"),
		JavaScriptEnabled: playwright.Bool(true),
		BypassCSP:         playwright.Bool(true),
	})

	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("crawl: unable to create browser context", "error", err.Error())
		return core.NBToolResponse{}, err
	}

	// Create a new page in the browser
	page, err := context.NewPage()
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("crawl: unable to create browser page", "error", err.Error())
		return core.NBToolResponse{}, err
	}

	// Add an init script to hide the webdriver flag
	err = page.AddInitScript(playwright.Script{
		Content: playwright.String(`
			Object.defineProperty(navigator, 'webdriver', {get: () => undefined});
			
			// Spoof navigator.plugins
			Object.defineProperty(navigator, 'plugins', {
				get: () => [
					{ name: 'Chrome PDF Plugin', filename: 'internal-pdf-viewer', description: 'Portable Document Format' },
					{ name: 'Chrome PDF Viewer', filename: 'mhjfbmdgcfjbbpaeojofohoefgiehjai', description: '' },
					{ name: 'Native Client', filename: 'internal-nacl-plugin', description: '' },
				],
			});

			// Spoof navigator.permissions
			const originalQuery = window.navigator.permissions.query;
			window.navigator.permissions.query = (parameters) => (
				parameters.name === 'notifications'
					? Promise.resolve({ state: Notification.permission })
					: originalQuery(parameters)
			);

			// Spoof WebGL renderer
			try {
				const getParameter = WebGLRenderingContext.prototype.getParameter;
				WebGLRenderingContext.prototype.getParameter = function(parameter) {
					if (parameter === 37445) { // VENDOR
						return 'Intel Open Source Technology Center';
					}
					if (parameter === 37446) { // RENDERER
						return 'Mesa DRI Intel(R) Ivybridge Mobile';
					}
					return getParameter.apply(this, arguments);
				};
			} catch (e) {}
		`),
	})
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("crawl: unable to add init script", "error", err.Error())
		return core.NBToolResponse{}, err
	}

	resp, err := page.Goto(command)
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("crawl: unable to navigate to url", "error", err.Error(), "url", command)
		return core.NBToolResponse{}, err
	}

	// Check HTTP response status — Playwright treats 4xx/5xx as successful navigations,
	// so we must explicitly reject them to avoid returning error page content as results.
	if resp != nil {
		status := resp.Status()
		if status >= 400 {
			nbRequestContext.Ctx.GetLogger().Warn("crawl: HTTP error response from url", "status", status, "url", command)

			// Try Jina Reader as fallback for HTTP error responses
			nbRequestContext.Ctx.GetLogger().Info("crawl: attempting Jina Reader fallback after HTTP error", "url", command)
			jinaResp, jinaErr := crawlViaJina(nbRequestContext, command)
			if jinaErr == nil {
				return jinaResp, nil
			}
			nbRequestContext.Ctx.GetLogger().Warn("crawl: Jina Reader fallback also failed", "url", command, "error", jinaErr)

			return core.NBToolResponse{}, fmt.Errorf("crawl: HTTP %d error from %s — site blocked access or page not found", status, command)
		}
	}

	// Extract content based on the requested response type (HTML or text)
	responseType := "text"
	if responseTypeAny, ok := input.Arguments["response_type"]; ok {
		responseType, _ = responseTypeAny.(string)
	}

	var body string
	outputResponseType := core.NBToolResponseTypeText
	if responseType == "html" {
		body, err = page.Content()
		if err != nil {
			nbRequestContext.Ctx.GetLogger().Error("crawl: unable to get page content", "error", err.Error(), "url", command)
			return core.NBToolResponse{}, fmt.Errorf("crawl: unable to get page content: %w", err)
		}
	} else {
		// Smart content extraction: try to find main article or content tags first
		var innerText string
		contentLocators := []string{"article", "main", ".content", ".post", "#content"}
		for _, selector := range contentLocators {
			loc := page.Locator(selector)
			count, _ := loc.Count()
			if count > 0 {
				innerText, err = loc.First().InnerText(playwright.LocatorInnerTextOptions{Timeout: playwright.Float(5000)})
				if err == nil && len(strings.TrimSpace(innerText)) > 500 {
					nbRequestContext.Ctx.GetLogger().Info("crawl: extracted content using selector", "selector", selector)
					break
				}
			}
		}

		if innerText == "" {
			innerText, err = page.Locator("body").InnerText(playwright.LocatorInnerTextOptions{Timeout: playwright.Float(30000)})
			if err != nil {
				return core.NBToolResponse{}, fmt.Errorf("crawl: could not get text content: %w", err)
			}
		}

		// Bot-block detection: Cloudflare and similar services return HTTP 200 with a
		// challenge page when the request originates from a datacenter IP (AWS/GCP).
		// If we detect one, attempt Jina Reader as a fallback before giving up.
		if isBotBlockPage(innerText) {
			nbRequestContext.Ctx.GetLogger().Info("crawl: bot-block challenge detected, trying Jina Reader fallback", "url", command)
			jinaResp, jinaErr := crawlViaJina(nbRequestContext, command)
			if jinaErr == nil {
				return jinaResp, nil
			}
			nbRequestContext.Ctx.GetLogger().Warn("crawl: Jina Reader fallback also failed after bot-block", "url", command, "error", jinaErr)
		}

		// Truncate extremely large responses to prevent OOM/timeouts in later stages
		maxChars := maxCrawlContentChars
		if len(innerText) > maxChars {
			innerText = innerText[:maxChars] + "\n\n[... content truncated at 500KB by crawler ...]"
		}

		jsonBodyMap := map[string]string{
			"url":     command,
			"content": innerText,
		}
		data, err := common.MarshalJson(jsonBodyMap)
		if err != nil {
			nbRequestContext.Ctx.GetLogger().Error("crawl: unable to serialize request", "error", err)
			outputResponseType = core.NBToolResponseTypeText
			body = innerText
		} else {
			body = string(data)
			outputResponseType = core.NBToolResponseTypeJson
		}
	}

	// Return the crawled content
	return core.NBToolResponse{
		Data: body,
		Type: outputResponseType,
		AdditionalDetails: map[string]any{
			"reference": command,
		},
		References: []core.NBToolResponseReference{
			{
				Text: command,
				Url:  command,
			},
		},
	}, nil
}

const jinaReaderBaseURL = "https://r.jina.ai/"
const jinaSearchBaseURL = "https://s.jina.ai/"

// crawlViaJina uses Jina Reader (r.jina.ai) to fetch and convert a page to clean
// markdown. It is used as a fallback when Playwright is blocked by bot-detection.
// An optional API key (jina_api_key) unlocks higher rate limits but is not required.
func crawlViaJina(nbRequestContext core.NbToolContext, targetUrl string) (core.NBToolResponse, error) {
	headers := map[string]string{
		"Accept": "text/plain",
	}
	if key := config.Config.LlmServerJinaApiKey; key != "" {
		headers["Authorization"] = "Bearer " + key
	}

	resp, err := common.HttpGet(
		jinaReaderBaseURL+targetUrl,
		common.HttpWithHeaders(headers),
		common.HttpWithTimeout(30*time.Second),
	)
	if err != nil {
		return core.NBToolResponse{}, fmt.Errorf("crawl: Jina Reader request failed: %w", err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			nbRequestContext.Ctx.GetLogger().Error("crawl: failed to close Jina response body", "error", cerr)
		}
	}()

	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return core.NBToolResponse{}, fmt.Errorf("crawl: failed to read Jina Reader response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return core.NBToolResponse{}, fmt.Errorf("crawl: Jina Reader returned status %d for %s", resp.StatusCode, targetUrl)
	}

	content := strings.TrimSpace(string(bodyBytes))
	if content == "" {
		return core.NBToolResponse{}, fmt.Errorf("crawl: Jina Reader returned empty content for %s", targetUrl)
	}

	nbRequestContext.Ctx.GetLogger().Info("crawl: successfully fetched content via Jina Reader fallback", "url", targetUrl)

	if len(content) > maxCrawlContentChars {
		content = content[:maxCrawlContentChars] + "\n\n[... content truncated at 500KB by crawler ...]"
	}

	jsonBodyMap := map[string]string{
		"url":     targetUrl,
		"content": content,
	}
	data, err := common.MarshalJson(jsonBodyMap)
	if err != nil {
		return core.NBToolResponse{Data: content, Type: core.NBToolResponseTypeText}, nil
	}

	return core.NBToolResponse{
		Data: string(data),
		Type: core.NBToolResponseTypeJson,
		AdditionalDetails: map[string]any{
			"reference": targetUrl,
			"source":    "jina_reader",
		},
		References: []core.NBToolResponseReference{
			{Text: targetUrl, Url: targetUrl},
		},
	}, nil
}

// searchViaJina uses Jina Search (s.jina.ai) to perform a web search and return
// results as clean markdown. Used as a fallback when Serper is not configured.
func searchViaJina(nbRequestContext core.NbToolContext, query string) ([]map[string]string, error) {
	headers := map[string]string{
		"Accept": "application/json",
	}
	if key := config.Config.LlmServerJinaApiKey; key != "" {
		headers["Authorization"] = "Bearer " + key
	}

	resp, err := common.HttpGet(
		jinaSearchBaseURL+url.QueryEscape(query),
		common.HttpWithHeaders(headers),
		common.HttpWithTimeout(15*time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("search: Jina Search request failed: %w", err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			nbRequestContext.Ctx.GetLogger().Error("search: failed to close Jina Search response body", "error", cerr)
		}
	}()

	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	if err != nil {
		return nil, fmt.Errorf("search: failed to read Jina Search response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("search: Jina Search returned status %d", resp.StatusCode)
	}

	// Jina Search returns JSON with a "data" array of results containing url and title.
	var jinaResp struct {
		Data []struct {
			URL         string `json:"url"`
			Title       string `json:"title"`
			Description string `json:"description"`
		} `json:"data"`
	}
	if err := json.Unmarshal(bodyBytes, &jinaResp); err != nil || len(jinaResp.Data) == 0 {
		// Fall back: treat the whole response as a single text block
		return []map[string]string{{"url": jinaSearchBaseURL + url.PathEscape(query), "_body": string(bodyBytes)}}, nil
	}

	links := make([]map[string]string, 0, len(jinaResp.Data))
	for _, r := range jinaResp.Data {
		links = append(links, map[string]string{
			"url":  r.URL,
			"desc": r.Title + " - " + r.Description,
		})
	}
	return links, nil
}
