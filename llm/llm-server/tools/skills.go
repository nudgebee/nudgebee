package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/tools/core"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/lib/pq"
)

const LoadSkillsToolName = "load_skills"
const SearchSkillsToolName = "search_skills"

// skillData holds bounded content for one resolved skill.
type skillData struct {
	Purpose       core.KnowledgeContentPurpose `json:"purpose,omitempty"`
	ExcerptOnly   bool                         `json:"excerpt_only,omitempty"`
	ID            string                       `json:"id"`
	Data          string                       `json:"data"`
	Description   string                       `json:"description"`
	KBType        string                       `json:"kb_type"`
	KBSource      *string                      `json:"kb_source"`
	IntegrationID *string                      `json:"integration_id,omitempty"`
	ReferenceType string                       `json:"reference_type,omitempty"`
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

var _ core.SchemaValidationInputNormalizer = LoadSkillsTool{}

func (m LoadSkillsTool) Name() string {
	return LoadSkillsToolName
}

func (m LoadSkillsTool) Description() string {
	if config.Config.LlmServerKnowledgeWorkspaceEnabled {
		return `Loads one exact account skill or discovered candidate ID. Large indexed documents are saved to the workspace. Use keyword or start_line to read bounded sections through this tool. Integration names return document candidates for selection. Unknown names are never substituted.`
	}
	return `Loads discovered knowledge candidates by candidate id, or active account knowledge bases by exact name. Unknown names are not searched or substituted; use search_skills for discovery. You MUST use the 'skill_name' parameter. For multiple entries, provide ids or names as a single comma-separated string.`
}

func (m LoadSkillsTool) GetType() core.NBToolType {
	return core.NBToolTypeTool
}

func (m LoadSkillsTool) InputSchema() core.ToolSchema {
	// One canonical name in the schema: 'skill_name', mandatory. Call() still
	// silently accepts 'skill_names' and 'skills' as legacy compat (see
	// ParseSkillName), but the LLM-facing contract only teaches the one shape
	// to prevent alias sprawl across every tool. Once DB shows zero
	// skill_names/skills usage the fallback drops in a followup.
	schema := core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"keyword":    {Type: core.ToolSchemaTypeString, Description: "Find matching lines in one selected knowledge document. Bounded output; refine the keyword for more specific matches."},
			"start_line": {Type: core.ToolSchemaTypeInteger, Description: "Read one document starting at this 1-based line number."},
			"skill_name": {
				Type:        core.ToolSchemaTypeString,
				Description: "The candidate id shown in <skill-lists> (preferred), or an exact account skill name. For multiple entries, use a comma-separated string. Do NOT pass an array or list.",
			},
		},
		Required: []string{"skill_name"},
	}
	if config.Config.LlmServerKnowledgeWorkspaceEnabled {
		schema.Properties["skill_name"] = core.ToolSchemaProperty{Type: core.ToolSchemaTypeString, Description: "One discovered candidate ID or exact account skill name."}
	} else {
		delete(schema.Properties, "keyword")
		delete(schema.Properties, "start_line")
	}
	return schema
}

// NormalizeInputForSchemaValidation maps historical aliases onto the one
// canonical field exposed to the LLM. Call receives the original input and
// continues to parse the aliases through ParseSkillName.
func (m LoadSkillsTool) NormalizeInputForSchemaValidation(input string) string {
	trimmed := strings.TrimSpace(input)
	if !strings.HasPrefix(trimmed, "{") {
		return input
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil || parsed == nil {
		return input
	}
	if _, exists := parsed["skill_name"]; exists {
		return input
	}
	for _, alias := range []string{"skill_names", "skills"} {
		value, exists := parsed[alias]
		if !exists {
			continue
		}
		name := extractSkillName(value)
		if name == "" {
			continue
		}
		parsed["skill_name"] = name
		normalized, err := json.Marshal(parsed)
		if err != nil {
			return input
		}
		return string(normalized)
	}
	return input
}

func extractSkillName(val any) string {
	var names []string
	switch value := val.(type) {
	case string:
		return strings.TrimSpace(value)
	case []any:
		for _, item := range value {
			if s, ok := item.(string); ok {
				if trimmed := strings.TrimSpace(s); trimmed != "" {
					names = append(names, trimmed)
				}
			}
		}
	case []string:
		for _, s := range value {
			if trimmed := strings.TrimSpace(s); trimmed != "" {
				names = append(names, trimmed)
			}
		}
	default:
		return ""
	}
	return strings.Join(names, ",")
}

func (m LoadSkillsTool) Call(ctx core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	skillName := m.ParseSkillName(input)
	if config.Config.LlmServerKnowledgeWorkspaceEnabled {
		return m.callWorkspaceKnowledge(ctx, input, skillName)
	}
	ctx.Ctx.GetLogger().Info("tool: load_skills called", "skill_name", skillName)

	if skillName == "" {
		common.MetricsToolOperationsTotal(core.ToolImplTypeBuiltin, m.Name(), "error", ctx.AccountId)
		return core.NBToolResponse{
			Status: core.NBToolResponseStatusError,
			Data:   "skill_name is required",
		}, nil
	}

	// Parse and deduplicate requested skill names.
	requestedNames := m.parseSkillNames(skillName)
	if len(requestedNames) == 0 {
		common.MetricsToolOperationsTotal(core.ToolImplTypeBuiltin, m.Name(), "error", ctx.AccountId)
		return core.NBToolResponse{
			Status: core.NBToolResponseStatusError,
			Data:   "skill_name is required",
		}, nil
	}

	manualCandidates := make(map[string]string)
	// Question-relevant candidates are cached per turn by the account-wide
	// discovery step. Resolve them before touching the DB: a candidate may be an
	// individual Confluence/ServiceNow article with no standalone KB row.
	results := make(map[string]skillData, len(requestedNames))
	var dbNames []string
	for _, name := range requestedNames {
		if strings.HasPrefix(strings.ToLower(name), "knowledge:") {
			if candidate, ok := core.LoadKnowledgeCandidate(ctx.AccountId, ctx.ConversationId, ctx.MessageId, name); ok {
				if candidate.KBID != "" {
					manualCandidates[strings.ToLower(name)] = candidate.KBID
					continue
				}
				description := candidate.Source
				if candidate.URL != "" {
					description += " — " + candidate.URL
				}
				referenceID := candidate.ReferenceID
				if referenceID == "" {
					referenceID = candidate.ID
				}
				results[strings.ToLower(name)] = skillData{
					ID:            referenceID,
					Data:          candidate.Content,
					Description:   description,
					KBType:        "retrieved",
					ReferenceType: "knowledge_base",
					ExcerptOnly:   candidate.ExcerptOnly,
				}
			}
			// Candidate IDs are turn-scoped and never name DB rows. An expired or
			// cross-turn ID is reported as missing below without a pointless DB/RAG
			// lookup.
			continue
		}
		dbNames = append(dbNames, name)
	}

	if len(manualCandidates) > 0 {
		docs := make(core.RAGSearchResults, 0, len(manualCandidates))
		for _, id := range manualCandidates {
			docs = append(docs, core.RAGSearchResult{Metadata: map[string]any{"collection": "kb_" + id}})
		}
		resolved := core.ResolveManualKnowledge(ctx.Ctx, ctx.AccountId, docs)
		for _, doc := range resolved {
			id, _ := doc.Metadata["kb_id"].(string)
			title, _ := doc.Metadata["kb_name"].(string)
			for alias, kbID := range manualCandidates {
				if kbID == id {
					results[alias] = skillData{Purpose: core.KnowledgeDocumentPurpose(doc), ID: id, ExcerptOnly: len(doc.Document) > core.KnowledgeExcerptBytes, Data: common.TruncateHead(doc.Document, core.KnowledgeExcerptBytes), Description: title, KBType: "manual", ReferenceType: "skill"}
				}
			}
		}
	}
	var dbms *common.DatabaseManager
	var err error
	if len(dbNames) > 0 {
		dbms, err = common.GetDatabaseManager(common.Metastore)
		if err != nil {
			common.MetricsToolOperationsTotal(core.ToolImplTypeBuiltin, m.Name(), "error", ctx.AccountId)
			return core.NBToolResponse{Status: core.NBToolResponseStatusError}, err
		}
	}

	// Loading resolves identities, not search queries. Unknown names must not be
	// replaced with substring matches or nearest-neighbour RAG documents.
	if len(dbNames) > 0 {
		dbResults, _ := m.fetchSkillsBatch(ctx, dbms, dbNames)
		for k, v := range dbResults {
			results[k] = v
		}
	}
	// Integration results are query-dependent and never enter the name cache.
	enrichIntegrationSkillsFromRAG(ctx, results)
	var missingNames []string
	for _, name := range requestedNames {
		if _, found := results[strings.ToLower(name)]; !found {
			missingNames = append(missingNames, name)
		}
	}

	// Build the aggregated response.
	maxSkillContentLength := config.Config.LlmServerMaxSkillContentLength
	if maxSkillContentLength < utf8.UTFMax {
		maxSkillContentLength = 5000
	}
	var aggregatedOutput strings.Builder
	var skillRefs []core.NBToolResponseReference
	loadedCount := 0
	seenBodies := make(map[string]bool)

	for _, name := range requestedNames {
		skill, ok := results[strings.ToLower(name)]
		if !ok {
			continue
		}

		identity := skill.ID + "\x00" + skill.Data
		if seenBodies[identity] {
			continue
		}
		seenBodies[identity] = true
		data := common.TruncateHead(skill.Data, maxSkillContentLength)
		skill.ExcerptOnly = skill.ExcerptOnly || len(data) < len(skill.Data)

		if loadedCount > 0 {
			aggregatedOutput.WriteString("\n\n---\n\n")
		}
		fmt.Fprintf(&aggregatedOutput,
			"<skill>\n<name>%s</name>\n<description>%s</description>\n<guidance>%s</guidance>\n<content>\n%s\n</content>",
			html.EscapeString(name),
			html.EscapeString(skill.Description),
			html.EscapeString(core.KnowledgePurposeGuidance(skill.Purpose)),
			html.EscapeString(data))
		if skill.ExcerptOnly {
			aggregatedOutput.WriteString("\n<note>Only a bounded excerpt is loaded. The full document is not available through this legacy read path; refine search_skills or enable workspace knowledge reads.</note>")
		}
		aggregatedOutput.WriteString("\n</skill>")
		referenceType := skill.ReferenceType
		if referenceType == "" {
			referenceType = "skill"
		}
		skillRefs = append(skillRefs, core.NBToolResponseReference{
			Text:        name,
			Type:        referenceType,
			Url:         skill.ID,
			Description: skill.Description,
		})
		loadedCount++
	}

	if loadedCount == 0 {
		common.MetricsToolOperationsTotal(core.ToolImplTypeBuiltin, m.Name(), "not_found", ctx.AccountId)
		errorMsg := fmt.Sprintf("Knowledge '%s' was not found. Use search_skills to discover knowledge, then use load_skills with a candidate ID or an exact knowledge base name.", skillName)
		if dbms == nil {
			errorMsg += " The knowledge candidate may have expired; run discovery again."
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
	common.MetricsToolOperationsTotal(core.ToolImplTypeBuiltin, m.Name(), "success", ctx.AccountId)
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

// fetchSkillsBatch resolves exact names against live account rows on every load.
// Discovery handles provide reuse; a name cache must not bypass enablement checks.
func (m LoadSkillsTool) fetchSkillsBatch(ctx core.NbToolContext, dbms *common.DatabaseManager, names []string) (map[string]skillData, []string) {
	results := make(map[string]skillData, len(names))
	// queryNames preserves original casing for correct "not found" reporting.
	var queryNames []string
	var queryLower []string

	for _, name := range names {
		queryNames = append(queryNames, name)
		queryLower = append(queryLower, strings.ToLower(strings.TrimSpace(name)))
	}

	if len(queryLower) == 0 {
		return results, nil
	}

	// Resolve the requested names in one account-scoped batch query.
	query := `
		SELECT kb.id, kb.name, LEFT(kb.data, 4097), COALESCE(kb.description, ''),
		       COALESCE(kb.kb_type, 'manual'), kb.kb_source, kb.integration_id, COALESCE(kb.note_category, '')
		FROM llm_knowledgebases kb
		WHERE kb.account_id = $1
		  AND LOWER(BTRIM(kb.name)) = ANY($2::text[])
		  AND kb.status = 'active'
		  AND kb.enabled`

	rows, err := dbms.Db.Query(query, ctx.AccountId, pq.Array(queryLower))
	if err != nil {
		ctx.Ctx.GetLogger().Error("tool: load_skills batch query error", "error", err)
		return results, queryNames
	}
	defer func() {
		if err := rows.Close(); err != nil {
			ctx.Ctx.GetLogger().Warn("tool: load_skills failed to close rows", "error", err)
		}
	}()

	foundInDB := make(map[string]bool)
	for rows.Next() {
		var id, name, data, description, kbType, category string
		var kbSource, integrationID *string
		if err := rows.Scan(&id, &name, &data, &description, &kbType, &kbSource, &integrationID, &category); err != nil {
			continue
		}
		lower := strings.ToLower(strings.TrimSpace(name))
		sd := skillData{Purpose: core.KnowledgePurpose(kbType, category), ID: id, ExcerptOnly: len(data) > core.KnowledgeExcerptBytes, Data: common.TruncateHead(data, core.KnowledgeExcerptBytes), Description: description, KBType: kbType, KBSource: kbSource, IntegrationID: integrationID}
		results[lower] = sd
		foundInDB[lower] = true

	}
	if err := rows.Err(); err != nil {
		ctx.Ctx.GetLogger().Error("tool: load_skills batch rows error", "error", err)
	}

	// Collect original-casing names still not found after DB query.
	var stillMissing []string
	for i, lower := range queryLower {
		if !foundInDB[lower] {
			stillMissing = append(stillMissing, queryNames[i])
		}
	}

	return results, stillMissing
}

// ---------------------------------------------------------------------------
// Shared RAG search
// ---------------------------------------------------------------------------

// cacheSearchKnowledgeCandidates exposes exact, turn-scoped identities instead
// of asking the loader to search again using a guessed article title.
func cacheSearchKnowledgeCandidates(ctx core.NbToolContext, docs core.RAGSearchResults) []string {
	var results []string
	for _, doc := range docs {
		content := strings.TrimSpace(doc.Document)
		if content == "" || len(results) >= ragSkillTopK {
			continue
		}
		title, _ := doc.Metadata["title"].(string)
		url, _ := doc.Metadata["url"].(string)
		source, _ := doc.Metadata["source"].(string)
		if title == "" {
			title = strings.SplitN(content, "\n", 2)[0]
		}
		if source == "" {
			source = "knowledge_base"
		}
		// Include content so separate chunks from one article cannot overwrite
		// each other or a candidate created by automatic discovery.
		identity := core.KnowledgeDocumentIdentity(doc)
		if identity == "" {
			identity = url + "\x00" + content
		}
		id := core.NewKnowledgeCandidateID(ctx.AccountId, ctx.ConversationId, ctx.MessageId, "search:"+identity)
		candidate := core.KnowledgeCandidate{
			ID: id, ReferenceID: url, Title: truncateRunesExact(title, 240),
			Source: source, URL: url,
			Content: content,
			Snippet: truncateRunesExact(content, 240),
		}
		core.SetKnowledgeDocumentHandle(&candidate, doc)
		candidate.KBID = core.ManualKnowledgeID(doc)
		if candidate.KBID != "" {
			candidate.Content = ""
			candidate.ReferenceID = candidate.KBID
		}
		if err := core.StoreKnowledgeCandidate(ctx.AccountId, ctx.ConversationId, ctx.MessageId, candidate); err != nil {
			ctx.Ctx.GetLogger().Warn("search_skills: unable to cache candidate", "error", err)
			continue
		}
		results = append(results, fmt.Sprintf("<result system=\"rag\" source=\"%s\" id=\"%s\" title=\"%s\">\n%s\nUse load_skills with skill_name=%s to read this candidate.\n</result>",
			html.EscapeString(source), id, html.EscapeString(candidate.Title), html.EscapeString(core.KnowledgePurposeGuidance(candidate.Purpose)+"\n"+candidate.Snippet), id))
	}
	return results
}

// truncateRunes caps display metadata by Unicode code points without allocating
// a full []rune copy. Content budgets use common.TruncateHead instead because
// their configured limit is byte-based.
func truncateRunesExact(s string, limit int) string {
	if limit <= 0 {
		return ""
	}
	count := 0
	for i := range s {
		if count == limit {
			return s[:i]
		}
		count++
	}
	return s
}

// ragKBModule is the module tag used by the RAG server for all knowledgebase
// collections. A single QueryRAG call with this module searches across every
// KB collection for the account.
const ragKBModule = "knowledge_base"

// enrichIntegrationSkillsFromRAG searches each resolved collection independently.
// One deadline bounds the batch; no query-dependent content is cached by KB name.
func enrichIntegrationSkillsFromRAG(ctx core.NbToolContext, results map[string]skillData) {
	retrievalCtx, cancel := context.WithTimeout(ctx.Ctx.GetContext(), ragSkillTimeout)
	defer cancel()
	for key, skill := range results {
		if skill.KBType != "integration" || strings.TrimSpace(skill.Data) != "" {
			continue
		}
		collection := core.KnowledgebaseCollectionName(core.Knowledgebase{Id: skill.ID, KBType: skill.KBType, IntegrationId: skill.IntegrationID})
		query := strings.TrimSpace(ctx.Query)
		if query == "" {
			query = key
		}
		docs := core.QueryRAGCollectionReranked(retrievalCtx, ctx.UserId, ctx.AccountId, query, ragKBModule, collection, ragSkillTopK, ctx.ConversationId, ctx.MessageId, "", false)
		var content strings.Builder
		for _, doc := range docs {
			// Defend against a mis-scoped response from an incompatible server.
			if got, _ := doc.Metadata["collection"].(string); got != collection {
				continue
			}
			if strings.TrimSpace(doc.Document) == "" {
				continue
			}
			if content.Len() > 0 {
				content.WriteString("\n\n---\n\n")
			}
			content.WriteString(doc.Document)
			if url, _ := doc.Metadata["url"].(string); url != "" {
				content.WriteString("\nSource: " + url)
			}
		}
		if content.Len() == 0 {
			delete(results, key)
			continue
		}
		skill.ExcerptOnly = content.Len() > core.KnowledgeExcerptBytes
		skill.Data = common.TruncateHead(content.String(), core.KnowledgeExcerptBytes)
		results[key] = skill
	}
}

// ---------------------------------------------------------------------------
// SearchSkillsTool — semantic search across all skill sources (DB + RAG)
// ---------------------------------------------------------------------------

// SearchSkillsTool searches across manual and integration knowledge bases by query.
type SearchSkillsTool struct{}

func (s SearchSkillsTool) Name() string { return SearchSkillsToolName }

func (s SearchSkillsTool) Description() string {
	return `Searches knowledge bases and skills by a natural language query. Returns candidate snippets from manual (DB-stored) and external (Confluence, ServiceNow) knowledge bases. Use load_skills with the returned candidate id or exact manual KB name to read a selected result. Candidate ids are valid only in the current turn. Search matches may be weak; select only results relevant to the task.`
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
		common.MetricsToolOperationsTotal(core.ToolImplTypeBuiltin, s.Name(), "error", ctx.AccountId)
		return core.NBToolResponse{
			Status: core.NBToolResponseStatusError,
			Data:   "query is required",
		}, nil
	}

	ctx.Ctx.GetLogger().Info("tool: search_skills called", "query", query)

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		common.MetricsToolOperationsTotal(core.ToolImplTypeBuiltin, s.Name(), "error", ctx.AccountId)
		return core.NBToolResponse{Status: core.NBToolResponseStatusError}, err
	}

	// Run manual DB search and integration RAG search in parallel,
	// bounded by an overall 10s timeout.
	type searchOutput struct {
		results []string
		refs    []core.NBToolResponseReference
		docs    core.RAGSearchResults
	}
	manualCh := make(chan searchOutput, 1)
	ragCh := make(chan searchOutput, 1)

	// 1. Manual KBs — fuzzy name/description match (DB).
	go func() {
		var out searchOutput
		for _, mr := range s.searchManualKBs(ctx, dbms, query) {
			out.results = append(out.results, fmt.Sprintf("<result system=\"internal\" source=\"manual\" name=\"%s\">\n%s\n</result>",
				html.EscapeString(mr.name), html.EscapeString(core.KnowledgePurposeGuidance(core.KnowledgePurpose("manual", mr.category))+"\n"+mr.snippet)))
			out.refs = append(out.refs, core.NBToolResponseReference{
				Text: mr.name, Type: "skill", Url: mr.id, Description: mr.description,
			})
		}
		manualCh <- out
	}()

	// 2. Integration KBs — single RAG search with module "knowledge_base".
	go func() {
		var out searchOutput
		docs := core.QueryRAG(ctx.UserId, ctx.AccountId, query, ragKBModule,
			ragSkillTopK, ctx.ConversationId, ctx.MessageId, "", false)
		out.docs = docs
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

	// Resolve manual vector hits by ID before formatting, so lexical and vector
	// search cannot offer the same KB twice under unrelated identities.
	docs := core.ResolveManualKnowledge(ctx.Ctx, ctx.AccountId, ragOut.docs)
	seenManual := make(map[string]bool)
	for _, ref := range manualOut.refs {
		seenManual[ref.Url] = true
	}
	filtered := make(core.RAGSearchResults, 0, len(docs))
	for _, doc := range docs {
		id, _ := doc.Metadata["kb_id"].(string)
		if id != "" && seenManual[id] {
			continue
		}
		filtered = append(filtered, doc)
	}
	ragOut.results = cacheSearchKnowledgeCandidates(ctx, filtered)
	finalResults = append(finalResults, ragOut.results...)

	if len(finalResults) == 0 {
		common.MetricsToolOperationsTotal(core.ToolImplTypeBuiltin, s.Name(), "not_found", ctx.AccountId)
		return core.NBToolResponse{
			Status: core.NBToolResponseStatusSuccess,
			Data:   "No matching skills or knowledge base entries found for the given query.",
			Type:   core.NBToolResponseTypeText,
		}, nil
	}

	ctx.Ctx.GetLogger().Info("tool: search_skills success",
		"manual_results", len(manualOut.results), "rag_results", len(ragOut.results))
	common.MetricsToolOperationsTotal(core.ToolImplTypeBuiltin, s.Name(), "success", ctx.AccountId)
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
	category    string
}

// searchManualKBs matches query terms against manual KB names, descriptions and tags.
// The query is tokenized (lowercased, stop words removed) and each token must
// appear in the name, description or tags.
func (s SearchSkillsTool) searchManualKBs(ctx core.NbToolContext, dbms *common.DatabaseManager, query string) []manualSearchResult {
	words := core.TokenizeForSkillSelection(query)
	if len(words) == 0 {
		return nil
	}

	// Build per-word conditions: each word may match name, description or tags.
	// Parameters: $1 = account_id, $2..$N = word patterns.
	var conditions []string
	args := []any{ctx.AccountId}
	for i, word := range words {
		paramIdx := i + 2 // $2, $3, ...
		conditions = append(conditions, fmt.Sprintf(
			"(LOWER(kb.name) LIKE $%d OR LOWER(COALESCE(kb.description, '')) LIKE $%d OR LOWER(array_to_string(kb.context_tags, ' ')) LIKE $%d)",
			paramIdx, paramIdx, paramIdx))
		args = append(args, "%"+word+"%")
	}

	sqlQuery := fmt.Sprintf(`
		SELECT kb.id, kb.name, COALESCE(kb.description, ''),
		       LEFT(kb.data, 500), COALESCE(kb.note_category, '')
		FROM llm_knowledgebases kb
		WHERE kb.account_id = $1
		  AND kb.status = 'active'
		  AND kb.enabled
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
		var id, name, description, snippet, category string
		if err := rows.Scan(&id, &name, &description, &snippet, &category); err != nil {
			continue
		}
		out = append(out, manualSearchResult{id: id, name: name, description: description, snippet: snippet, category: category})
	}
	if err := rows.Err(); err != nil {
		ctx.Ctx.GetLogger().Error("tool: search_skills error iterating rows", "error", err)
	}
	return out
}

func (m LoadSkillsTool) ParseSkillName(input core.NBToolCallRequest) string {
	var skillName string

	if val, ok := input.Arguments["skill_name"]; ok {
		skillName = extractSkillName(val)
	} else if val, ok := input.Arguments["skill_names"]; ok {
		// Legacy alias: schema-side dropped 2026-07-10 (PR #31271). Kept in
		// Call() as a silent compat shim so historical LLM shapes still work
		// during the transition. Remove once DB shows zero calls landing on
		// this branch — track via metrics or a periodic sweep of
		// llm_conversation_tool_calls.parameters. Target: 2026-08-01.
		skillName = extractSkillName(val)
	} else if val, ok := input.Arguments["skills"]; ok {
		// Legacy alias — same rationale as 'skill_names' above.
		skillName = extractSkillName(val)
	}

	// Fallback 1: check if the argument was passed as "value" or single unnamed arg (some LLMs do this)
	if skillName == "" && len(input.Arguments) > 0 {
		for _, v := range input.Arguments {
			skillName = extractSkillName(v)
			if skillName != "" {
				break
			}
		}
	}

	// Fallback 2: Check input.Command (for natural language calls)
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
