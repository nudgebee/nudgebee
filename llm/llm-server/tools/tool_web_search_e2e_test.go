//go:build e2e

package tools

import (
	"encoding/json"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"
)

func TestJinaSearch(t *testing.T) {
	key := os.Getenv("JINA_API_KEY")
	if key == "" {
		t.Skip("Skipping: JINA_API_KEY not set (Jina Search requires an API key)")
	}
	config.Config.LlmServerJinaApiKey = key

	toolCtx := newTestToolContext(t, SearchExecuteTool{}, "kubernetes release notes")
	links, err := searchViaJina(toolCtx, "kubernetes release notes")

	require.Nil(t, err)
	require.NotEmpty(t, links, "expected at least one search result from Jina")

	for _, link := range links {
		assert.NotEmpty(t, link["url"], "each result should have a url")
	}

	t.Logf("Jina search returned %d results, first URL: %s", len(links), links[0]["url"])
}

func TestJinaCrawl(t *testing.T) {
	if key := os.Getenv("JINA_API_KEY"); key != "" {
		config.Config.LlmServerJinaApiKey = key
	} else {
		// The free tier needs outbound network and is aggressively rate-limited
		// (HTTP 403 from CI/sandbox egress), so gate on a key for determinism.
		t.Skip("Skipping: JINA_API_KEY not set (Jina Reader free tier is rate-limited / needs network)")
	}

	targetURL := "https://kubernetes.io/docs/concepts/overview/what-is-kubernetes/"
	toolCtx := newTestToolContext(t, CrawlExecuteTool{}, targetURL)

	resp, err := crawlViaJina(toolCtx, targetURL)

	require.Nil(t, err)
	require.NotEmpty(t, resp.Data)
	assert.Equal(t, core.NBToolResponseTypeJson, resp.Type)

	var parsed struct {
		URL     string `json:"url"`
		Content string `json:"content"`
	}
	require.Nil(t, json.Unmarshal([]byte(resp.Data), &parsed))
	assert.NotEmpty(t, parsed.Content, "crawled content should not be empty")
	assert.Greater(t, len(parsed.Content), 100, "crawled content should have meaningful length")

	t.Logf("Jina crawl returned %d chars of content for %s", len(parsed.Content), targetURL)
}

func TestJinaSearchAndCrawl(t *testing.T) {
	key := os.Getenv("JINA_API_KEY")
	if key == "" {
		t.Skip("Skipping: JINA_API_KEY not set (Jina Search requires an API key)")
	}
	config.Config.LlmServerJinaApiKey = key

	query := "helm chart deployment tutorial"
	toolCtx := newTestToolContext(t, SearchExecuteTool{}, query)

	// Step 1: search
	links, err := searchViaJina(toolCtx, query)
	require.Nil(t, err, "Jina search should not fail")
	require.NotEmpty(t, links, "Jina search should return at least one result")

	firstURL := links[0]["url"]
	require.NotEmpty(t, firstURL, "first result must have a URL")
	t.Logf("Jina search: first result URL = %s", firstURL)

	// Step 2: crawl the first result
	crawlCtx := newTestToolContext(t, CrawlExecuteTool{}, firstURL)
	resp, err := crawlViaJina(crawlCtx, firstURL)
	require.Nil(t, err, "Jina crawl of first search result should not fail")
	require.NotEmpty(t, resp.Data)

	var parsed struct {
		Content string `json:"content"`
	}
	if json.Unmarshal([]byte(resp.Data), &parsed) == nil {
		t.Logf("Jina crawl: got %d chars of content from %s", len(parsed.Content), firstURL)
	}
}

func TestSearchExecuteTool(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("requires a configured web-search provider and outbound network; set TEST_ACCOUNT to run")
	}

	tool := SearchExecuteTool{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-prom-chain-1",
				Query:     `Release notes of Helm`,
				AccountId: os.Getenv("TEST_LOKICHAIN_ACCOUNT"),
				UserId:    os.Getenv("TEST_LOKICHAIN_USER"),
			},
		}
	for _, tc := range testCases {

		ntc := core.NewNbToolContext(sc, tool, tc.AccountId, tc.UserId, uuid.NewString(), uuid.NewString(), uuid.NewString(), tc.Query, []llms.MessageContent{}, "", core.NBQueryConfig{}, "")
		resp, err := tool.Call(ntc, core.NBToolCallRequest{
			Command: tc.Query,
		})
		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}
}

func TestSearchExecuteTool2(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("requires a configured web-search provider and outbound network; set TEST_ACCOUNT to run")
	}

	tool := SearchExecuteTool{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-prom-chain-1",
				Query:     `Release notes of Helm`,
				AccountId: os.Getenv("TEST_LOKICHAIN_ACCOUNT"),
				UserId:    os.Getenv("TEST_LOKICHAIN_USER"),
			},
		}
	for _, tc := range testCases {

		ntc := core.NewNbToolContext(sc, tool, tc.AccountId, tc.UserId, uuid.NewString(), uuid.NewString(), uuid.NewString(), tc.Query, []llms.MessageContent{}, "", core.NBQueryConfig{}, "")
		resp, err := tool.Call(ntc, core.NBToolCallRequest{
			Command: tc.Query,
		})
		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}
}

func TestCrawlExecuteTool(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("requires outbound network for crawling; set TEST_ACCOUNT to run")
	}

	tool := CrawlExecuteTool{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-prom-chain-1",
				AccountId: os.Getenv("TEST_ESCHAIN_ACCOUNT"),
				UserId:    os.Getenv("TEST_K8SCHAIN_USER"),
				Query:     `https://hub.docker.com/_/redis/tags`,
			},
		}
	for _, tc := range testCases {

		ntc := core.NewNbToolContext(sc, tool, tc.AccountId, tc.UserId, uuid.NewString(), uuid.NewString(), uuid.NewString(), tc.Query, []llms.MessageContent{}, "", core.NBQueryConfig{}, "")
		resp, err := tool.Call(ntc, core.NBToolCallRequest{
			Command: tc.Query,
		})
		t.Error(resp.Data)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}
}

func TestCrawlExecuteTool2(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("requires outbound network for crawling; set TEST_ACCOUNT to run")
	}

	tool := CrawlExecuteTool{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-prom-chain-1",
				AccountId: os.Getenv("TEST_ESCHAIN_ACCOUNT"),
				UserId:    os.Getenv("TEST_K8SCHAIN_USER"),
				Query:     `https://hub.docker.com/r/arm32v6/redis/`,
			},
		}
	for _, tc := range testCases {

		ntc := core.NewNbToolContext(sc, tool, tc.AccountId, tc.UserId, uuid.NewString(), uuid.NewString(), uuid.NewString(), tc.Query, []llms.MessageContent{}, "", core.NBQueryConfig{}, "")
		resp, err := tool.Call(ntc, core.NBToolCallRequest{
			Command: tc.Query,
		})
		t.Error(resp.Data)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}
}

// newTestToolContext creates a minimal NbToolContext for tool-level tests.
func newTestToolContext(t *testing.T, tool core.NBTool, query string) core.NbToolContext {
	t.Helper()
	sc := security.NewRequestContextForSuperAdmin()
	accountId := os.Getenv("TEST_ACCOUNT")
	if accountId == "" {
		accountId = "test-account"
	}
	return core.NewNbToolContext(sc, tool, accountId, "test-user", uuid.NewString(), uuid.NewString(), uuid.NewString(), query, []llms.MessageContent{}, "", core.NBQueryConfig{}, "")
}
