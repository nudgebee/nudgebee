package tools

import (
	"encoding/json"
	"fmt"
	"html"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/tools/core"
	"strings"
	"time"

	"github.com/lib/pq"
)

const LoadSkillsToolName = "load_skills"
const SearchSkillsToolName = "search_skills"

// skillData holds cached skill content.
type skillData struct {
	ID          string  `json:"id"`
	Data        string  `json:"data"`
	Description string  `json:"description"`
	KBType      string  `json:"kb_type"`
	KBSource    *string `json:"kb_source"`
}

const ragSkillTopK = 5
const ragSkillTimeout = 10 * time.Second

func init() {
	core.RegisterNBToolFactory(LoadSkillsToolName, func(accountId string) (core.NBTool, error) {
		return LoadSkillsTool{}, nil
	})
	core.RegisterNBToolFactory(SearchSkillsToolName, func(accountId string) (core.NBTool, error) {
		return SearchSkillsTool{}, nil
	})
	// Cache namespace is initialized in tools/core so knowledgebase_service.go can invalidate it.
}

type LoadSkillsTool struct{}

func (m LoadSkillsTool) Name() string {
	return LoadSkillsToolName
}

func (m LoadSkillsTool) Description() string {
	return `Loads the content of one or more skills or knowledge bases by name. You MUST use the 'skill_name' parameter. If loading multiple skills, provide their names as a single comma-separated string.`
}

func (m LoadSkillsTool) GetType() core.NBToolType {
	return core.NBToolTypeTool
}

func (m LoadSkillsTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"skill_name": {
				Type:        core.ToolSchemaTypeString,
				Description: "The name of the skill to load (exact match required). For multiple skills, use a comma-separated list (e.g., 'skill1, skill2'). Do NOT pass an array or list.",
			},
		},
		Required: []string{"skill_name"},
	}
}

func (m LoadSkillsTool) Call(ctx core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	skillName := m.ParseSkillName(input)
	ctx.Ctx.GetLogger().Info("tool: load_skills called", "skill_name", skillName)

	if skillName == "" {
		common.MetricsToolOperationsTotal(m.Name(), "error", ctx.AccountId)
		return core.NBToolResponse{
			Status: core.NBToolResponseStatusError,
			Data:   "skill_name is required",
		}, nil
	}

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		common.MetricsToolOperationsTotal(m.Name(), "error", ctx.AccountId)
		return core.NBToolResponse{Status: core.NBToolResponseStatusError}, err
	}

	// Parse and deduplicate requested skill names.
	requestedNames := m.parseSkillNames(skillName)
	if len(requestedNames) == 0 {
		common.MetricsToolOperationsTotal(m.Name(), "error", ctx.AccountId)
		return core.NBToolResponse{
			Status: core.NBToolResponseStatusError,
			Data:   "skill_name is required",
		}, nil
	}

	// Fetch skills: check cache first, then single batch DB query for misses,
	// then fuzzy fallback for any still-missing names.
	results, missingNames := m.fetchSkillsBatch(ctx, dbms, requestedNames)

	if len(missingNames) > 0 {
		fuzzyResults := m.fetchSkillsFuzzy(ctx, dbms, missingNames)
		for k, v := range fuzzyResults {
			results[k] = v
		}
		var stillMissing []string
		for _, n := range missingNames {
			if _, found := results[strings.ToLower(n)]; !found {
				stillMissing = append(stillMissing, n)
			}
		}
		missingNames = stillMissing
	}

	// For names still not found in DB (e.g. RAG-sourced integration content
	// the LLM saw in skill-list previews), search RAG directly in parallel.
	// Gated by feature flag to control rollout.
	if len(missingNames) > 0 && config.Config.LlmServerIntegrationKBEnabled {
		type ragResult struct {
			name    string
			content string
		}
		ragCh := make(chan ragResult, len(missingNames))
		for _, name := range missingNames {
			go func(n string) {
				ragCh <- ragResult{name: n, content: searchKBsViaRAG(ctx, n, ragSkillTopK)}
			}(name)
		}
		for range len(missingNames) {
			r := <-ragCh
			if r.content != "" {
				lower := strings.ToLower(r.name)
				results[lower] = skillData{
					Data:        r.content,
					Description: "Retrieved from knowledge base",
					KBType:      "integration",
				}
				ctx.Ctx.GetLogger().Info("tool: load_skills resolved via RAG fallback", "name", r.name)
			}
		}
		// Recalculate missing names.
		var stillMissing []string
		for _, n := range missingNames {
			if _, found := results[strings.ToLower(n)]; !found {
				stillMissing = append(stillMissing, n)
			}
		}
		missingNames = stillMissing
	}

	// For integration-type skills with empty data, fall back to RAG retrieval.
	enrichIntegrationSkillsFromRAG(ctx, results)

	// Build the aggregated response.
	maxSkillContentLength := config.Config.LlmServerMaxSkillContentLength
	var aggregatedOutput strings.Builder
	var skillRefs []core.NBToolResponseReference
	loadedCount := 0

	for _, name := range requestedNames {
		skill, ok := results[strings.ToLower(name)]
		if !ok {
			continue
		}

		data := skill.Data
		totalSize := len(data)
		isTruncated := totalSize > maxSkillContentLength
		if isTruncated {
			data = data[:maxSkillContentLength]
		}

		if loadedCount > 0 {
			aggregatedOutput.WriteString("\n\n---\n\n")
		}
		fmt.Fprintf(&aggregatedOutput,
			"<skill>\n<name>%s</name>\n<description>%s</description>\n<content>\n%s\n</content>",
			html.EscapeString(name),
			html.EscapeString(skill.Description),
			html.EscapeString(data))
		if isTruncated {
			fmt.Fprintf(&aggregatedOutput,
				"\n<truncated total_bytes=\"%d\" shown_bytes=\"%d\">Content was truncated. Request a specific section if you need more.</truncated>",
				totalSize, maxSkillContentLength)
		}
		aggregatedOutput.WriteString("\n</skill>")
		skillRefs = append(skillRefs, core.NBToolResponseReference{
			Text:        name,
			Type:        "skill",
			Url:         skill.ID,
			Description: skill.Description,
		})
		loadedCount++
	}

	if loadedCount == 0 {
		common.MetricsToolOperationsTotal(m.Name(), "not_found", ctx.AccountId)
		errorMsg := fmt.Sprintf("Skills '%s' not found or not available for this agent.", skillName)
		if available := m.listAvailableSkills(ctx, dbms); len(available) > 0 {
			errorMsg += fmt.Sprintf(" Available skills for this account/agent are: %s", strings.Join(available, ", "))
		} else {
			errorMsg += " No skills are currently mapped to this agent."
		}
		return core.NBToolResponse{
			Status: core.NBToolResponseStatusError,
			Data:   errorMsg,
		}, nil
	}

	if len(missingNames) > 0 {
		fmt.Fprintf(&aggregatedOutput, "\n\n<note>The following requested skills were not found: %s</note>",
			strings.Join(missingNames, ", "))
	}

	ctx.Ctx.GetLogger().Info("tool: load_skills success", "loaded_count", loadedCount, "missing_count", len(missingNames))
	common.MetricsToolOperationsTotal(m.Name(), "success", ctx.AccountId)
	return core.NBToolResponse{
		Status:     core.NBToolResponseStatusSuccess,
		Data:       aggregatedOutput.String(),
		Type:       core.NBToolResponseTypeText,
		References: skillRefs,
	}, nil
}

// parseSkillNames splits a comma-separated skill name string and returns
// a deduplicated, trimmed slice preserving original casing.
func (m LoadSkillsTool) parseSkillNames(skillName string) []string {
	seen := make(map[string]bool)
	var names []string
	for _, n := range strings.Split(skillName, ",") {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		lower := strings.ToLower(n)
		if !seen[lower] {
			seen[lower] = true
			names = append(names, n)
		}
	}
	return names
}

// fetchSkillsBatch checks cache for each skill, then does a single batch DB query
// for all cache misses. Returns a map of lowerName→skillData and original-casing
// names that were not found.
func (m LoadSkillsTool) fetchSkillsBatch(ctx core.NbToolContext, dbms *common.DatabaseManager, names []string) (map[string]skillData, []string) {
	results := make(map[string]skillData, len(names))
	// cacheMissNames preserves original casing for correct "not found" reporting.
	var cacheMissNames []string
	var cacheMissLower []string

	for _, name := range names {
		lower := strings.ToLower(name)
		cacheKey := fmt.Sprintf("skill:%s:%s", ctx.AccountId, lower)
		if raw, ok := common.CacheGet(core.CacheNamespaceLlmSkillContent, cacheKey); ok {
			var cached skillData
			if err := json.Unmarshal(raw, &cached); err == nil {
				results[lower] = cached
				continue
			}
		}
		cacheMissNames = append(cacheMissNames, name)
		cacheMissLower = append(cacheMissLower, lower)
	}

	if len(cacheMissLower) == 0 {
		return results, nil
	}

	// Single batch query for all cache misses using ANY($2::text[]).
	query := `
		SELECT kb.id, kb.name, kb.data, COALESCE(kb.description, ''),
		       COALESCE(kb.kb_type, 'manual'), kb.kb_source
		FROM llm_knowledgebases kb
		INNER JOIN llm_kb_agent_mappings m ON kb.id = m.kb_id
		WHERE kb.account_id = $1
		  AND LOWER(kb.name) = ANY($2::text[])
		  AND kb.status = 'active'`

	rows, err := dbms.Db.Query(query, ctx.AccountId, pq.Array(cacheMissLower))
	if err != nil {
		ctx.Ctx.GetLogger().Error("tool: load_skills batch query error", "error", err)
		return results, cacheMissNames
	}
	defer func() {
		if err := rows.Close(); err != nil {
			ctx.Ctx.GetLogger().Warn("tool: load_skills failed to close rows", "error", err)
		}
	}()

	foundInDB := make(map[string]bool)
	for rows.Next() {
		var id, name, data, description, kbType string
		var kbSource *string
		if err := rows.Scan(&id, &name, &data, &description, &kbType, &kbSource); err != nil {
			continue
		}
		lower := strings.ToLower(name)
		sd := skillData{ID: id, Data: data, Description: description, KBType: kbType, KBSource: kbSource}
		results[lower] = sd
		foundInDB[lower] = true

		// Cache the freshly fetched skill content.
		if raw, err := json.Marshal(sd); err == nil {
			cacheKey := fmt.Sprintf("skill:%s:%s", ctx.AccountId, lower)
			_ = common.CacheSet(core.CacheNamespaceLlmSkillContent, cacheKey, raw)
		}
	}
	if err := rows.Err(); err != nil {
		ctx.Ctx.GetLogger().Error("tool: load_skills batch rows error", "error", err)
	}

	// Collect original-casing names still not found after DB query.
	var stillMissing []string
	for i, lower := range cacheMissLower {
		if !foundInDB[lower] {
			stillMissing = append(stillMissing, cacheMissNames[i])
		}
	}

	return results, stillMissing
}

// fetchSkillsFuzzy attempts ILIKE substring matching for names that weren't
// found by the exact-match batch query. missingNames preserves original casing.
// Returns a map of lowerRequestedName→skillData.
func (m LoadSkillsTool) fetchSkillsFuzzy(ctx core.NbToolContext, dbms *common.DatabaseManager, missingNames []string) map[string]skillData {
	results := make(map[string]skillData)
	if len(missingNames) == 0 {
		return results
	}

	// Build ILIKE patterns from lowercased names.
	patterns := make([]string, 0, len(missingNames))
	for _, n := range missingNames {
		pattern := "%" + strings.ToLower(strings.TrimSpace(n)) + "%"
		patterns = append(patterns, pattern)
	}

	query := `
		SELECT kb.id, kb.name, kb.data, COALESCE(kb.description, ''),
		       COALESCE(kb.kb_type, 'manual'), kb.kb_source
		FROM llm_knowledgebases kb
		INNER JOIN llm_kb_agent_mappings m ON kb.id = m.kb_id
		WHERE kb.account_id = $1
		  AND LOWER(kb.name) LIKE ANY($2::text[])
		  AND kb.status = 'active'`

	rows, err := dbms.Db.Query(query, ctx.AccountId, pq.Array(patterns))
	if err != nil {
		ctx.Ctx.GetLogger().Error("tool: load_skills fuzzy query error", "error", err)
		return results
	}
	defer func() {
		if err := rows.Close(); err != nil {
			ctx.Ctx.GetLogger().Warn("tool: load_skills failed to close fuzzy rows", "error", err)
		}
	}()

	for rows.Next() {
		var id, foundName, data, description, kbType string
		var kbSource *string
		if err := rows.Scan(&id, &foundName, &data, &description, &kbType, &kbSource); err != nil {
			continue
		}
		lowerFound := strings.ToLower(foundName)

		// Attribute the result to the first requested name whose pattern matches.
		for _, n := range missingNames {
			if strings.Contains(lowerFound, strings.ToLower(n)) {
				key := strings.ToLower(n)
				if _, already := results[key]; !already {
					results[key] = skillData{ID: id, Data: data, Description: description, KBType: kbType, KBSource: kbSource}
					ctx.Ctx.GetLogger().Info("tool: load_skills fuzzy match", "requested", n, "found", foundName)
				}
				break
			}
		}
	}
	if err := rows.Err(); err != nil {
		ctx.Ctx.GetLogger().Error("tool: load_skills fuzzy rows error", "error", err)
	}

	return results
}

// listAvailableSkills returns names of all active skills accessible to the account.
func (m LoadSkillsTool) listAvailableSkills(ctx core.NbToolContext, dbms *common.DatabaseManager) []string {
	rows, err := dbms.Db.Query(`
		SELECT DISTINCT kb.name
		FROM llm_knowledgebases kb
		INNER JOIN llm_kb_agent_mappings m ON kb.id = m.kb_id
		WHERE kb.account_id = $1 AND kb.status = 'active'
		ORDER BY kb.name ASC`, ctx.AccountId)
	if err != nil || rows == nil {
		return nil
	}
	defer func() {
		if err := rows.Close(); err != nil {
			ctx.Ctx.GetLogger().Warn("tool: load_skills failed to close available-skills rows", "error", err)
		}
	}()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err == nil {
			names = append(names, name)
		}
	}
	return names
}

// ---------------------------------------------------------------------------
// Shared RAG search
// ---------------------------------------------------------------------------

// ragKBModule is the module tag used by the RAG server for all knowledgebase
// collections. A single QueryRAG call with this module searches across every
// KB collection for the account.
const ragKBModule = "knowledge_base"

// searchKBsViaRAG queries the RAG server's /get_matching_doc endpoint with
// module "knowledge_base", which searches all KB collections for the account.
// An optional metadataFilter (e.g. {"source": "confluence"}) narrows results.
// Returns aggregated content with source URLs, or empty string if no results.
func searchKBsViaRAG(ctx core.NbToolContext, query string, topK int, metadataFilter ...map[string]string) string {
	type result struct {
		content string
	}
	ch := make(chan result, 1)

	go func() {
		var mf map[string]string
		if len(metadataFilter) > 0 {
			mf = metadataFilter[0]
		}
		ragStart := time.Now()
		ragDocs := core.QueryRAG(
			ctx.UserId, ctx.AccountId, query, ragKBModule,
			topK, ctx.ConversationId, ctx.MessageId, "", false, mf,
		)
		ctx.Ctx.GetLogger().Info("tool: searchKBsViaRAG complete",
			"duration_ms", time.Since(ragStart).Milliseconds(),
			"result_count", len(ragDocs),
			"metadata_filter", mf)
		if len(ragDocs) == 0 {
			ch <- result{}
			return
		}
		maxLen := config.Config.LlmServerMaxSkillContentLength
		if maxLen <= 0 {
			maxLen = 5000
		}
		// Per-doc cap: split the budget across results so one huge doc
		// doesn't starve the others.
		perDocCap := maxLen / len(ragDocs)
		perDocCap = max(perDocCap, 500)

		var sb strings.Builder
		for i, doc := range ragDocs {
			if i > 0 {
				sb.WriteString("\n\n---\n\n")
			}
			content := doc.Document
			if len(content) > perDocCap {
				content = content[:perDocCap] + "\n[truncated]"
			}
			sb.WriteString(content)
			if url, ok := doc.Metadata["url"].(string); ok && url != "" {
				sb.WriteString("\nSource: ")
				sb.WriteString(url)
			}
			// Stop if aggregate size already exceeds the budget.
			if sb.Len() >= maxLen {
				break
			}
		}
		ch <- result{content: sb.String()}
	}()

	select {
	case r := <-ch:
		return r.content
	case <-time.After(ragSkillTimeout):
		ctx.Ctx.GetLogger().Warn("tool: searchKBsViaRAG timed out", "timeout", ragSkillTimeout)
		return ""
	}
}

// enrichIntegrationSkillsFromRAG fetches content from the RAG server for integration-type
// skills (e.g. Confluence, ServiceNow) whose data is stored externally, not in the DB.
// A single RAG call with module "knowledge_base" searches all KB collections.
func enrichIntegrationSkillsFromRAG(ctx core.NbToolContext, results map[string]skillData) {
	// Collect keys of integration skills that need enrichment and track sources.
	var needsEnrichment []string
	query := strings.TrimSpace(ctx.Query)
	sources := make(map[string]bool)
	for key, skill := range results {
		if skill.KBType != "integration" || strings.TrimSpace(skill.Data) != "" {
			continue
		}
		needsEnrichment = append(needsEnrichment, key)
		if skill.KBSource != nil && *skill.KBSource != "" {
			sources[*skill.KBSource] = true
		}
		if query == "" {
			query = key // fallback query if ctx.Query is empty
		}
	}

	if len(needsEnrichment) == 0 {
		return
	}

	// If all integration skills share the same source, filter RAG by it.
	var metadataFilter map[string]string
	if len(sources) == 1 {
		for src := range sources {
			metadataFilter = map[string]string{"source": src}
		}
	}

	ctx.Ctx.GetLogger().Info("tool: enriching integration skills from RAG",
		"skills", needsEnrichment, "query", query, "source_filter", metadataFilter)

	content := searchKBsViaRAG(ctx, query, ragSkillTopK, metadataFilter)
	if content == "" {
		ctx.Ctx.GetLogger().Warn("tool: RAG returned no results for integration skills",
			"skills", needsEnrichment)
		return
	}

	// Apply RAG content to all integration skills and update cache.
	for _, key := range needsEnrichment {
		skill := results[key]
		skill.Data = content
		results[key] = skill

		if raw, err := json.Marshal(skill); err == nil {
			cacheKey := fmt.Sprintf("skill:%s:%s", ctx.AccountId, key)
			_ = common.CacheSet(core.CacheNamespaceLlmSkillContent, cacheKey, raw)
		}
		ctx.Ctx.GetLogger().Info("tool: enriched integration skill from RAG",
			"skill", key, "content_len", len(content))
	}
}

// ---------------------------------------------------------------------------
// SearchSkillsTool — semantic search across all skill sources (DB + RAG)
// ---------------------------------------------------------------------------

// SearchSkillsTool searches across manual and integration knowledge bases by query.
type SearchSkillsTool struct{}

func (s SearchSkillsTool) Name() string { return SearchSkillsToolName }

func (s SearchSkillsTool) Description() string {
	return `Searches knowledge bases and skills by a natural language query. Returns relevant snippets from both manual (DB-stored) and external (Confluence, ServiceNow) knowledge bases. Use this when you need to find information across skills without knowing the exact skill name.`
}

func (s SearchSkillsTool) GetType() core.NBToolType { return core.NBToolTypeTool }

func (s SearchSkillsTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"query": {
				Type:        core.ToolSchemaTypeString,
				Description: "The search query describing what information you need.",
			},
		},
		Required: []string{"query"},
	}
}

func (s SearchSkillsTool) Call(ctx core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	query := ""
	if val, ok := input.Arguments["query"]; ok {
		if q, ok := val.(string); ok {
			query = strings.TrimSpace(q)
		}
	}
	if query == "" && input.Command != "" {
		query = strings.TrimSpace(input.Command)
	}
	if query == "" {
		common.MetricsToolOperationsTotal(s.Name(), "error", ctx.AccountId)
		return core.NBToolResponse{
			Status: core.NBToolResponseStatusError,
			Data:   "query is required",
		}, nil
	}

	ctx.Ctx.GetLogger().Info("tool: search_skills called", "query", query)

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		common.MetricsToolOperationsTotal(s.Name(), "error", ctx.AccountId)
		return core.NBToolResponse{Status: core.NBToolResponseStatusError}, err
	}

	// Run manual DB search and integration RAG search in parallel,
	// bounded by an overall 10s timeout.
	type searchOutput struct {
		results []string
		refs    []core.NBToolResponseReference
	}
	manualCh := make(chan searchOutput, 1)
	ragCh := make(chan searchOutput, 1)

	// 1. Manual KBs — fuzzy name/description match (DB).
	go func() {
		var out searchOutput
		for _, mr := range s.searchManualKBs(ctx, dbms, query) {
			out.results = append(out.results, fmt.Sprintf("<result system=\"internal\" source=\"manual\" name=\"%s\">\n%s\n</result>",
				html.EscapeString(mr.name), html.EscapeString(mr.snippet)))
			out.refs = append(out.refs, core.NBToolResponseReference{
				Text: mr.name, Type: "skill", Url: mr.id, Description: mr.description,
			})
		}
		manualCh <- out
	}()

	// 2. Integration KBs — single RAG search with module "knowledge_base".
	go func() {
		var out searchOutput
		content := searchKBsViaRAG(ctx, query, ragSkillTopK)
		if content != "" {
			out.results = append(out.results, fmt.Sprintf("<result system=\"rag\" source=\"knowledge_base\">\n%s\n</result>",
				html.EscapeString(content)))
		}
		ragCh <- out
	}()

	// Collect with overall timeout.
	var manualOut, ragOut searchOutput
	deadline := time.After(ragSkillTimeout)
collect:
	for range 2 {
		select {
		case manualOut = <-manualCh:
		case ragOut = <-ragCh:
		case <-deadline:
			ctx.Ctx.GetLogger().Warn("tool: search_skills overall timeout reached")
			break collect
		}
	}

	var finalResults []string
	var finalRefs []core.NBToolResponseReference
	finalResults = append(finalResults, manualOut.results...)
	finalRefs = append(finalRefs, manualOut.refs...)

	// RAG results are content-based (not name-based), so they complement
	// rather than duplicate the manual results — append all.
	finalResults = append(finalResults, ragOut.results...)

	if len(finalResults) == 0 {
		common.MetricsToolOperationsTotal(s.Name(), "not_found", ctx.AccountId)
		return core.NBToolResponse{
			Status: core.NBToolResponseStatusSuccess,
			Data:   "No matching skills or knowledge base entries found for the given query.",
			Type:   core.NBToolResponseTypeText,
		}, nil
	}

	ctx.Ctx.GetLogger().Info("tool: search_skills success",
		"manual_results", len(manualOut.results), "rag_results", len(ragOut.results))
	common.MetricsToolOperationsTotal(s.Name(), "success", ctx.AccountId)
	return core.NBToolResponse{
		Status:     core.NBToolResponseStatusSuccess,
		Data:       strings.Join(finalResults, "\n\n---\n\n"),
		Type:       core.NBToolResponseTypeText,
		References: finalRefs,
	}, nil
}

type manualSearchResult struct {
	id          string
	name        string
	description string
	snippet     string
}

// searchManualKBs does a fuzzy ILIKE search on manual KB names and descriptions.
// The query is tokenized (lowercased, stop words removed) and each token must
// appear in either the name or description.
func (s SearchSkillsTool) searchManualKBs(ctx core.NbToolContext, dbms *common.DatabaseManager, query string) []manualSearchResult {
	words := core.TokenizeForSkillSelection(query)
	if len(words) == 0 {
		return nil
	}

	// Build per-word conditions: each word must match name OR description.
	// Parameters: $1 = account_id, $2..$N = word patterns.
	var conditions []string
	args := []any{ctx.AccountId}
	for i, word := range words {
		paramIdx := i + 2 // $2, $3, ...
		conditions = append(conditions, fmt.Sprintf(
			"(LOWER(kb.name) LIKE $%d OR LOWER(COALESCE(kb.description, '')) LIKE $%d)",
			paramIdx, paramIdx))
		args = append(args, "%"+word+"%")
	}

	sqlQuery := fmt.Sprintf(`
		SELECT kb.id, kb.name, COALESCE(kb.description, ''),
		       LEFT(kb.data, 500)
		FROM llm_knowledgebases kb
		WHERE kb.account_id = $1
		  AND kb.status = 'active'
		  AND COALESCE(kb.kb_type, 'manual') = 'manual'
		  AND %s
		LIMIT 5`, strings.Join(conditions, " AND "))

	rows, err := dbms.Db.Query(sqlQuery, args...)
	if err != nil {
		ctx.Ctx.GetLogger().Error("tool: search_skills manual query error", "error", err)
		return nil
	}
	defer func() {
		if err := rows.Close(); err != nil {
			ctx.Ctx.GetLogger().Warn("tool: search_skills failed to close rows", "error", err)
		}
	}()

	var out []manualSearchResult
	for rows.Next() {
		var id, name, description, snippet string
		if err := rows.Scan(&id, &name, &description, &snippet); err != nil {
			continue
		}
		out = append(out, manualSearchResult{id: id, name: name, description: description, snippet: snippet})
	}
	return out
}


func (m LoadSkillsTool) ParseSkillName(input core.NBToolCallRequest) string {
	var skillName string

	// Helper to extract string or join slice
	extractName := func(val any) string {
		if s, ok := val.(string); ok {
			return s
		}

		var inputSlice []any
		if slice, ok := val.([]any); ok {
			inputSlice = slice
		} else if sSlice, ok := val.([]string); ok {
			for _, s := range sSlice {
				inputSlice = append(inputSlice, s)
			}
		}

		if len(inputSlice) > 0 {
			var names []string
			for _, item := range inputSlice {
				if s, ok := item.(string); ok {
					trimmed := strings.TrimSpace(s)
					if trimmed != "" {
						names = append(names, trimmed)
					}
				}
			}
			return strings.Join(names, ",")
		}
		return ""
	}

	if val, ok := input.Arguments["skill_name"]; ok {
		skillName = extractName(val)
	} else if val, ok := input.Arguments["skill_names"]; ok {
		skillName = extractName(val)
	} else if val, ok := input.Arguments["skills"]; ok {
		skillName = extractName(val)
	}

	// Fallback 1: check if the argument was passed as "value" or single unnamed arg (some LLMs do this)
	if skillName == "" && len(input.Arguments) > 0 {
		for _, v := range input.Arguments {
			skillName = extractName(v)
			if skillName != "" {
				break
			}
		}
	}

	// Fallback 2: Check input.Command (for ReWoo or natural language calls)
	if skillName == "" && input.Command != "" {
		skillName = input.Command
		// Handle common prefixes like "skill_name:" or "skill_name="
		if strings.Contains(skillName, ":") {
			parts := strings.SplitN(skillName, ":", 2)
			if len(parts) == 2 && (strings.Contains(strings.ToLower(parts[0]), "skill") || strings.Contains(strings.ToLower(parts[0]), "name") || strings.Contains(strings.ToLower(parts[0]), "guide")) {
				skillName = parts[1]
			}
		} else if strings.Contains(skillName, "=") {
			parts := strings.SplitN(skillName, "=", 2)
			if len(parts) == 2 && (strings.Contains(strings.ToLower(parts[0]), "skill") || strings.Contains(strings.ToLower(parts[0]), "name")) {
				skillName = parts[1]
			}
		}

		// If it still looks like a sentence, try to extract the quoted part
		if strings.Contains(skillName, "'") || strings.Contains(skillName, "\"") {
			var firstIdx, lastIdx int
			if strings.Contains(skillName, "'") {
				firstIdx = strings.Index(skillName, "'")
				lastIdx = strings.LastIndex(skillName, "'")
			} else {
				firstIdx = strings.Index(skillName, "\"")
				lastIdx = strings.LastIndex(skillName, "\"")
			}

			if firstIdx != -1 && lastIdx != -1 && firstIdx != lastIdx {
				skillName = skillName[firstIdx+1 : lastIdx]
			}
		}

		// Heuristic: If it starts with "Load the skill named" or similar, strip it
		fillerPhrases := []string{
			"load the skill named",
			"load skill guides",
			"load skill guide",
			"load skill",
			"get skill",
			"show skill",
			"using the skill",
			"named",
		}
		lowerSkill := strings.ToLower(skillName)
		for _, phrase := range fillerPhrases {
			if strings.HasPrefix(lowerSkill, phrase) {
				skillName = strings.TrimSpace(skillName[len(phrase):])
				skillName = strings.TrimSuffix(skillName, ".") // Remove trailing period
				break
			}
		}
	}

	return strings.TrimSpace(skillName)
}
