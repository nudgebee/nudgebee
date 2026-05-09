package tools

import (
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"nudgebee/llm/common"
	_ "nudgebee/llm/security"
	"nudgebee/llm/tools/core"
	"strings"

	"github.com/samber/lo"
	"github.com/tmc/langchaingo/schema"
)

func init() {
	core.RegisterNBToolFactory(SearchDocsToolName, func(accountId string) (core.NBTool, error) {
		return DocsAgentTool{}, nil
	})
}

const SearchDocsToolName = "search_docs"

type DocsAgentTool struct{}

func (m DocsAgentTool) Name() string {
	return SearchDocsToolName
}

func (m DocsAgentTool) GetType() core.NBToolType {
	return core.NBToolTypeTool
}

func (m DocsAgentTool) Description() string {
	return `Executes a query to search Documentations and other related sources.`
}

func (m DocsAgentTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"command": {
				Type:        core.ToolSchemaTypeString,
				Description: "Search string",
			},
		},
		Required: []string{"command"},
	}
}

func (m DocsAgentTool) Call(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	query := strings.TrimSpace(input.Command)
	response, refs, err := searchDocumentation(nbRequestContext, query)
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("searchDocs: search failed", "error", err.Error())
		return core.NBToolResponse{}, err
	}

	resp := core.NBToolResponse{
		Data: response,
		Type: core.NBToolResponseTypeText,
	}

	if len(refs) > 0 {
		resp.References = lo.Map(refs, func(ref string, _ int) core.NBToolResponseReference {
			return core.NBToolResponseReference{
				Text: ref,
				Url:  ref,
				Type: "external",
			}
		})
	}

	return resp, nil
}

func searchDocumentation(nbRequestContext core.NbToolContext, query string) (string, []string, error) {
	if strings.TrimSpace(query) == "" {
		return "", nil, fmt.Errorf("query cannot be empty")
	}

	var matchingDocs []schema.Document

	// Try fetching from RAG. All indexed doc sources (user KBs, Confluence,
	// ServiceNow, Nudgebee product docs) are tagged ``module="knowledge_base"``
	// in rag-server, so this one call covers every searchable source. Falls
	// back to live Confluence below when the index has zero hits.
	var refs []string
	document := core.QueryRAG(nbRequestContext.UserId, nbRequestContext.AccountId, query, "knowledge_base", 3, nbRequestContext.ConversationId, nbRequestContext.MessageId, nbRequestContext.ParentAgentId, true)
	if len(document) > 0 {
		for _, doc := range document {
			matchingDocs = append(matchingDocs, schema.Document{
				PageContent: doc.Document,
				Metadata:    doc.Metadata,
				Score:       doc.SimilarityScore,
			})
			if url, ok := doc.Metadata["url"]; ok {
				refs = append(refs, url.(string))
			}
		}
	} else if _, cfgErr := getConfluenceIntegrationConfig(nbRequestContext.AccountId); cfgErr == nil {
		// Fallback to Confluence only if the integration is configured
		wikiPages, baseUrl, err := searchConfluence(query, nbRequestContext.AccountId)
		if err != nil {
			slog.Error("Could not connect to confluence", "error", err.Error())
			return "", nil, fmt.Errorf("could not connect to Confluence, please check your integration")
		}
		if len(wikiPages.Results) == 0 {
			return "No documentation found for your query. Please try a different search term.", nil, nil
		}

		baseUrl = strings.TrimSuffix(baseUrl, "/")

		for _, doc := range wikiPages.Results {
			matchingDocs = append(matchingDocs, schema.Document{
				PageContent: doc.Body.View.Value,
				Metadata: map[string]any{
					"id":  doc.ID,
					"url": baseUrl + "/wiki" + doc.Links.WebUi,
				},
				Score: 0.0,
			})
			refs = append(refs, baseUrl+"/wiki"+doc.Links.WebUi)
		}
	}

	if len(matchingDocs) == 0 {
		return "No documentation found for your query. Please try a different search term.", nil, nil
	}

	matchingDocBytes, err := common.MarshalJson(matchingDocs)
	if err != nil {
		slog.Error("unable to marshal matchingDocs", "error", err.Error())
		return "", nil, fmt.Errorf("unable to match docs, please try a different search term")
	}

	return string(matchingDocBytes), refs, nil
}

type WikiPage struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Body  struct {
		View struct {
			Value string `json:"value"`
		}
	} `json:"body"`
	Links struct {
		Self   string `json:"self"`
		TinyUi string `json:"tinyui"`
		EditUi string `json:"editui"`
		WebUi  string `json:"webui"`
		EditV2 string `json:"edituiv2"`
	} `json:"_links"`
}

type ConfluenceSearchResponse struct {
	Results []WikiPage `json:"results"`
}

func searchConfluence(query string, accountId string) (ConfluenceSearchResponse, string, error) {
	configs, err := getConfluenceIntegrationConfig(accountId)
	if err != nil {
		return ConfluenceSearchResponse{}, "", err
	}

	confluenceBaseURL := strings.TrimSuffix(configs["host"], "/")
	email := configs["username"]
	apiToken := configs["token"]
	auth := base64.StdEncoding.EncodeToString([]byte(email + ":" + apiToken))

	escapedQuery := strings.ReplaceAll(url.QueryEscape(strings.ReplaceAll(query, `"`, `\"`)), "+", "%20")
	searchUrl := fmt.Sprintf("%s/wiki/rest/api/content/search?cql=text~%%22%s%%22&expand=body.view", confluenceBaseURL, escapedQuery)

	req, _ := http.NewRequest("GET", searchUrl, nil)
	req.Header.Set("Authorization", "Basic "+auth)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return ConfluenceSearchResponse{}, "", err
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			fmt.Printf("Error closing response body: %v\n", err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return ConfluenceSearchResponse{}, "", fmt.Errorf("confluence API error: %s", body)
	}

	body, _ := io.ReadAll(resp.Body)
	var response ConfluenceSearchResponse
	if err := common.UnmarshalJson(body, &response); err != nil {
		return ConfluenceSearchResponse{}, "", fmt.Errorf("failed to unmarshal response: %w", err)
	}
	return response, confluenceBaseURL, nil
}

func getConfluenceIntegrationConfig(accountId string) (map[string]string, error) {
	query := `SELECT i.id, icv.name, icv.value FROM integrations i JOIN integrations_cloud_accounts ia ON i.id = ia.integration_id JOIN integration_config_values icv ON i.id = icv.integration_id WHERE i."type" = 'confluence' AND ia.cloud_account_id = :ac_id`
	err := sqlValidateReadOnly(query, "")
	if err != nil {
		return nil, err
	}

	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil, err
	}

	params := map[string]any{
		"ac_id": accountId,
	}

	rows, err := dbManager.Db.NamedQuery(query, params)
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
		if _, ok := allconfig[id]; !ok {
			allconfig[id] = make(map[string]string)
		}
		allconfig[id][name] = value
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	requiredKeys := []string{"host", "username", "token"}
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

	return nil, fmt.Errorf("unable to find confluence integration config for account: %s", accountId)
}
