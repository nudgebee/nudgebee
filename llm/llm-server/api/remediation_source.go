package api

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"maps"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/google/uuid"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
)

// Which runbook line a plan step follows (#42825). The agent names the document by the handle load_skills gave it
// and quotes the line; the server checks the quote against the page that turn loaded and stores the page's own line,
// title and link. Only the quote is the model's: a turn that did not read the document cannot cite it, and what a
// person sees comes from the document.

// PlanSource is the runbook line a step follows. An agent writes Handle and Step; the stored plan holds the
// document's title, its own line in Step, and its link when it has one.
type PlanSource struct {
	Handle   string `json:"handle,omitempty"`
	Document string `json:"document,omitempty"`
	Step     string `json:"step,omitempty"`
	Url      string `json:"url,omitempty"`
}

// planTurn is the conversation turn that wrote a plan: the turn whose loaded documents its steps may cite.
type planTurn struct {
	ConversationId string
	MessageId      string
}

// loadedDocument is one knowledge document a turn loaded, from its reference row.
type loadedDocument struct {
	Title   string
	Content string
	Url     string
}

const (
	// minSourceQuote is the shortest part of a line accepted as a quote of it; a whole line may be shorter.
	minSourceQuote = 12
	maxSourceStep  = 300
	// maxSourceSpan is the most lines one quote may run across: a step wrapped over lines, with its command below it.
	maxSourceSpan = 6
)

var (
	// sourceMarkup removes the inline markup of a document's line, which a step shows as plain text.
	sourceMarkup = strings.NewReplacer("**", "", "__", "", "`", "")
	// sourceSymbols removes what a quote may differ from its line by, other than spacing, case and entities.
	sourceSymbols = strings.NewReplacer("*", "", "_", "", "`", "", "#", "", ">", "")
	// Keep a document's title and link from ending the markdown link around them.
	sourceLinkText = strings.NewReplacer("[", "(", "]", ")")
	sourceLinkUrl  = strings.NewReplacer("(", "%28", ")", "%29")
)

// A read of a document numbers its lines ("14: Step 3 ..."); the number is not part of the document.
var sourceReadLinePrefix = regexp.MustCompile(`^\s*\d+:\s+`)

// loadedDocuments returns the documents the turn loaded, by handle. A var so tests can stub it.
var loadedDocuments = func(ctx context.Context, accountId string, turn planTurn) (map[string]loadedDocument, error) {
	if turn.ConversationId == "" || turn.MessageId == "" {
		return nil, nil
	}
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil, fmt.Errorf("remediation: reading the turn's documents: %w", err)
	}
	var rows []struct {
		ReferenceId string `db:"reference_id"`
		Metadata    []byte `db:"metadata"`
	}
	err = dbManager.Db.SelectContext(ctx, &rows, `SELECT reference_id, metadata FROM llm_conversation_references
		WHERE account_id = $1 AND conversation_id = $2 AND message_id = $3 AND reference_type = $4 AND metadata->>'handle' <> ''`,
		accountId, turn.ConversationId, turn.MessageId, string(core.AgentReferenceTypeKB))
	if err != nil {
		return nil, fmt.Errorf("remediation: reading the turn's documents: %w", err)
	}
	documents := make(map[string]loadedDocument, len(rows))
	for _, row := range rows {
		var metadata struct {
			Handle  string `json:"handle"`
			Name    string `json:"name"`
			Content string `json:"content"`
			Url     string `json:"url"`
		}
		if err := json.Unmarshal(row.Metadata, &metadata); err != nil {
			continue
		}
		documents[metadata.Handle] = loadedDocument{Title: metadata.Name, Content: metadata.Content,
			Url: firstNonEmpty(webLink(metadata.Url), webLink(row.ReferenceId), knowledgeBaseLink(accountId, row.ReferenceId))}
	}
	return documents, nil
}

// knowledgeBaseLink is the app's page for a knowledge base written in the app, which has no page elsewhere: its
// reference is the knowledge base's own id. "" for any other reference, or when the app's address is not configured.
func knowledgeBaseLink(accountId, referenceId string) string {
	base := strings.TrimRight(strings.TrimSpace(config.Config.BaseUrl), "/")
	if _, err := uuid.Parse(referenceId); err != nil || webLink(base) == "" {
		return ""
	}
	query := url.Values{"bcortex": {"knowledge-base"}, "kb": {referenceId}}
	if accountId != "" {
		query.Set("accountId", accountId)
	}
	return base + "/home?" + query.Encode()
}

// webLink returns link when a person's browser can open it as a page, else "".
func webLink(link string) string {
	parsed, err := url.Parse(strings.TrimSpace(link))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return ""
	}
	return parsed.String()
}

// resolvePlanSources replaces each step's source with what the turn's documents say, and clears one they do not back.
// A cleared source costs the step its label and nothing else, so it is a note in the trace, never a refusal.
func resolvePlanSources(ctx *security.RequestContext, accountId string, turn planTurn, plan *RemediationPlan) []planValidationNote {
	var notes []planValidationNote
	var documents map[string]loadedDocument
	read := false
	for i := range plan.Actions {
		a := &plan.Actions[i]
		if a.Source == nil {
			continue
		}
		source := *a.Source
		a.Source = nil
		if !read {
			read = true
			var err error
			if documents, err = loadedDocuments(ctx.GetContext(), accountId, turn); err != nil {
				ctx.GetLogger().Warn("remediation: step sources not checked", "error", err)
			}
		}
		document, line := quotedDocument(documents, strings.TrimSpace(source.Handle), source.Step)
		if line == "" {
			notes = append(notes, planValidationNote{Action: a.Action, Rule: "source_not_in_a_loaded_document", Detail: source.Step})
			continue
		}
		a.Source = &PlanSource{Document: document.Title, Step: line, Url: document.Url}
	}
	return notes
}

// quotedDocument is the document that holds the quoted line and the line, trying the named handle first. A turn's second
// load of a page has a handle of its own but no reference row of its own, so a handle that is not found is not a failed
// quote: the line stays the document's, whichever load showed it.
func quotedDocument(documents map[string]loadedDocument, handle, step string) (loadedDocument, string) {
	if document, ok := documents[handle]; ok {
		if line := sourceLine(document.Content, step); line != "" {
			return document, line
		}
	}
	for _, key := range slices.Sorted(maps.Keys(documents)) {
		if line := sourceLine(documents[key].Content, step); line != "" {
			return documents[key], line
		}
	}
	return loadedDocument{}, ""
}

// sourceLine is the line of content that step quotes, as the document writes it, or "" when no line does.
func sourceLine(content, step string) string {
	quote := sourceText(sourceReadLinePrefix.ReplaceAllString(oneLine(step, maxSourceStep*4), ""))
	if quote == "" {
		return ""
	}
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		text := sourceText(line)
		if text == quote || (len(quote) >= minSourceQuote && strings.Contains(text, quote)) {
			return truncate(sourceDisplay(line), maxSourceStep)
		}
	}
	if len(quote) < minSourceQuote {
		return ""
	}
	// A document wraps a step over lines and puts its command on a line of its own, and a quote of the step reads
	// them as one: the fewest lines, blank ones skipped, that hold the quote. The first end that does is the shortest.
	var kept, texts []string
	for _, line := range lines {
		if text := sourceText(line); text != "" {
			kept = append(kept, line)
			texts = append(texts, text)
		}
	}
	for end := 1; end < len(kept); end++ {
		for start := end - 1; start >= 0 && start > end-maxSourceSpan; start-- {
			if strings.Contains(strings.Join(texts[start:end+1], " "), quote) {
				shown := make([]string, 0, end+1-start)
				for _, line := range kept[start : end+1] {
					shown = append(shown, sourceDisplay(line))
				}
				return truncate(strings.Join(shown, " "), maxSourceStep)
			}
		}
	}
	return ""
}

// sourceDisplay is a document's line as a step shows it: plain text, without its heading or list marker.
func sourceDisplay(line string) string {
	return strings.TrimLeft(sourceMarkup.Replace(html.UnescapeString(strings.TrimSpace(line))), "#*-> ")
}

// sourceText is a line reduced to its words: a quote differs from its line by entities, markup, spacing and case.
func sourceText(line string) string {
	return strings.ToLower(strings.Join(strings.Fields(sourceSymbols.Replace(html.UnescapeString(line))), " "))
}

// sourceMarkdown is a step's source as one markdown line, or "" when it has none.
func sourceMarkdown(source *PlanSource) string {
	if source == nil {
		return ""
	}
	document := oneLine(source.Document, optionLineChars)
	if source.Url != "" {
		document = "[" + sourceLinkText.Replace(document) + "](" + sourceLinkUrl.Replace(source.Url) + ")"
	}
	return "From runbook " + document + ": " + oneLine(source.Step, optionLineChars)
}
