package agents

import (
	"log/slog"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
	"slices"
	"strings"
)

func init() {
	// This describes the 'elastic_search_query' agent when it is used as a tool by another agent (e.g., elastic_search agent).
	toolDescription := `Generates an Elasticsearch query string from a natural language question. Input should be a natural language request for an Elasticsearch query.`
	toolInput := "Provide elasticsearch question in natural language"
	toolOutput := "The tool will return the elasticsearch query retrieved from your question"

	core.RegisterNBAgentFactoryAsTool(ElasticSearchQueryAgentName, func(accountId string) (core.NBAgent, error) {
		return ElasticSearchQueryAgent{
			AccountId: accountId,
		}, nil
	}, toolDescription, toolInput, toolOutput)
}

const ElasticSearchQueryAgentName = "elastic_search_query"

type ElasticSearchQueryAgent struct {
	AccountId   string
	logProvider string
}

func (p ElasticSearchQueryAgent) GetName() string {
	return ElasticSearchQueryAgentName
}

func (a ElasticSearchQueryAgent) GetNameAliases() []string {
	return []string{"Elastic Search Query"}
}

func (p ElasticSearchQueryAgent) GetDescription() string {
	return `Generates Elasticsearch/Opensearch query based on natural language question.`
}

func (l ElasticSearchQueryAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {

	instructions := []string{
		"**GOAL:** Only Generate Query, Cannot Execute Query.",
		"**Analyze the Question:** Carefully analyze the user's request to understand the specific log information they need.",
		"**Generate Elasticsearch Query:** Construct a valid Elasticsearch query based on the user's request.",
		"**Term Queries:**",
		"   - For exact matches, use `term` queries with `case_insensitive` set to `true` and always use `.keyword` with the field name (e.g., `{\"term\": {\"label.keyword\": {\"value\":\"value\",\"case_insensitive\":true}}}`).",
		"**Regexp Queries:**",
		"   - For broader matches (pod names, application names, etc.), use `regexp` queries (e.g., `{\"regexp\": {\"label\": {\"value\": \"value.*\", \"case_insensitive\": true}}}`).",
		"**Time Range Filtering:**",
		"   - If needed, add a time range filter using `range` on the `@timestamp` field. Use a default time range of the last 24 hours (e.g., `{\"range\": {\"@timestamp\": {\"gte\": \"now-24h\"}}}`).",
		"**Error Handling:**",
		"   - On any error, return the response in JSON format: `{\"error\": \"error message\"}`.",
		"**Allowed Fields:**",
		"   - Only use the following fields in your queries: `[\"@timestamp\", \"message\", \"kubernetes.pod_name.keyword\", \"kubernetes.namespace_name.keyword\", \"kubernetes.container_name.keyword\"]`.",
	}
	constraints := []string{
		"Only return the valid Elasticsearch query.",
		"Do not add any formatting or additional text in the response.",
	}
	examples := []core.NBAgentPromptExample{
		{
			Question: "Show me logs for the \"my-app\" application of the last 24 hours.",
			Answer: `{
  "query": {
    "bool": {
      "must": [
        {
          "regexp": {
            "kubernetes.pod_name.keyword": {
              "case_insensitive": true,
              "value": "my-app.*"
            }
          }
        },
        {
          "range": {
            "@timestamp": {
              "gte": "now-24h"
            }
          }
        }
      ]
    }
  }
}`,
		},
		{
			Question: "nananan",
			Answer:   `{"error": "Looks like not valid question, please ask a valid question."}`,
		},
	}

	return core.NBAgentPrompt{
		Role:         "an SRE expert in ElasticSearch log analysis and Kubernetes",
		Instructions: instructions,
		Constraints:  constraints,
		Examples:     examples,
		OutputFormat: `Respond strictly with the valid JSON Elasticsearch query. Do not include any additional text or tags.`,
		Rag: core.NBAgentPromptRag{
			Module:      "elastic_search",
			Format:      core.NBAgentPromptRagFormatJson,
			QuestionKey: "question",
			AnswerKey:   "elastic_search_query",
		},
	}
}

func (p ElasticSearchQueryAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	return []toolcore.NBTool{}
}

func (l ElasticSearchQueryAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeTool
}

func (l ElasticSearchQueryAgent) updateQueryForProvider(output string) string {

	if l.logProvider == "" {
		dbms, err := common.GetDatabaseManager(common.Metastore)
		if err != nil {
			slog.Error("unable to fetch dbms, using original query", "error", err)
			return output
		}
		rows, err := dbms.Db.Queryx("select connection_status::text from agent where cloud_account_id = $1", l.AccountId)
		if err != nil {
			slog.Error("unable to query dbms, using original query", "error", err)
			return output
		}
		defer func() {
			if err := rows.Close(); err != nil {
				slog.Error("elastic_search_query: failed to close rows", "error", err)
			}
		}()

		provider := "es"
		for rows.Next() {
			var connectionStatusString *string
			err := rows.Scan(&connectionStatusString)
			if err != nil {
				slog.Error("unable to scan rows", "error", err)
				break
			}
			connectionStatus := map[string]any{}
			if connectionStatusString != nil {
				err = common.UnmarshalJson([]byte(*connectionStatusString), &connectionStatus)
				if err != nil {
					slog.Error("unable to unmarshal rows", "error", err)
					break
				}
			}
			logsUrl := connectionStatus["logProviderUrl"]
			if logsUrl != nil {
				logsUrlStr := logsUrl.(string)
				if strings.Contains(logsUrlStr, "logz.io") {
					provider = "logz.io"
					break
				}
			}

		}
		l.logProvider = provider
	}

	// for logz, keyword is not applicable
	if l.logProvider == "logz.io" {
		output = strings.ReplaceAll(output, ".keyword", "")
	}

	return output
}

func (l ElasticSearchQueryAgent) applyAliases(query any, queryAliases [][]string) any {
	switch v := query.(type) {
	case []any:
		for i, item := range v {
			v[i] = l.applyAliases(item, queryAliases)
		}
		return v
	case map[string]any:
		if boolVal, ok := v["bool"]; ok {
			if boolMap, ok := boolVal.(map[string]any); ok {
				for _, key := range []string{"must", "should", "filter", "must_not"} {
					if val, ok := boolMap[key]; ok {
						boolMap[key] = l.applyAliases(val, queryAliases)
					}
				}
			}
		}

		for _, queryKey := range []string{"regexp", "match", "term"} {
			if val, ok := v[queryKey]; ok {
				if subQuery, ok := val.(map[string]any); ok {
					expandedClauses := []any{}
					remainingFields := map[string]any{}
					hasAnyAlias := false

					for k, valVal := range subQuery {
						foundAlias := false
						for _, aliases := range queryAliases {
							if slices.Contains(aliases, k) {
								shouldQuery := []map[string]any{}
								for _, alias := range aliases {
									shouldQuery = append(shouldQuery, map[string]any{
										queryKey: map[string]any{
											alias: valVal,
										},
									})
								}
								expandedClauses = append(expandedClauses, map[string]any{
									"bool": map[string]any{
										"should": shouldQuery,
									},
								})
								hasAnyAlias = true
								foundAlias = true
								break
							}
						}
						if !foundAlias {
							remainingFields[k] = valVal
						}
					}

					if hasAnyAlias {
						// If there were other fields not expanded, add them back
						if len(remainingFields) > 0 {
							expandedClauses = append(expandedClauses, map[string]any{
								queryKey: remainingFields,
							})
						}

						// If we only have one clause, we can return it directly
						if len(expandedClauses) == 1 {
							delete(v, queryKey)
							// If v now has other keys, we should ideally wrap everything in a must,
							// but usually query clauses are single-keyed maps.
							// For simplicity and matching common ES patterns:
							if len(v) == 0 {
								return expandedClauses[0]
							}
							v["bool"] = map[string]any{
								"must": expandedClauses,
							}
						} else {
							delete(v, queryKey)
							v["bool"] = map[string]any{
								"must": expandedClauses,
							}
						}
					}
				}
			}
		}
		// Recurse into other map values
		for k, val := range v {
			if k != "bool" && k != "regexp" && k != "match" && k != "term" {
				v[k] = l.applyAliases(val, queryAliases)
			}
		}
		return v
	}
	return query
}

func (l ElasticSearchQueryAgent) UpdateExecutorLlmResponse(actions []core.NBAgentPlannerToolAction, finished *core.NBAgentPlannerFinishAction, err error) ([]core.NBAgentPlannerToolAction, *core.NBAgentPlannerFinishAction, error) {
	// update query to include should queries for message >  msg, log, message & levelname > level, level_name, levelname

	queryAliases := [][]string{
		{"msg", "log", "message"},
		{"level", "level_name", "levelname"},
		{"kubernetes.pod_name.keyword", "kubernetes.pod_name", "pod_name", "pod"},
		{"kubernetes.namespace_name.keyword", "kubernetes.namespace_name", "namespace_name", "namespace"},
		{"kubernetes.container_name.keyword", "kubernetes.container_name", "container_name", "container"},
	}

	if finished != nil {
		output := finished.Data
		if output != "" {

			output = l.updateQueryForProvider(output)

			esQuery := map[string]any{}
			err1 := common.UnmarshalJson([]byte(output), &esQuery)
			if err1 != nil {
				return actions, finished, err
			}
			if esQuery["query"] != nil {
				esQuery["query"] = l.applyAliases(esQuery["query"], queryAliases)
			}

			//serialize and return response
			serializedJson, err1 := common.MarshalJson(esQuery)
			if err1 != nil {
				return actions, finished, err
			}
			finished.Data = string(serializedJson)
			finished.Log = string(serializedJson)
		}
	}
	return actions, finished, err
}
