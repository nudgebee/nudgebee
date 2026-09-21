package core

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

type ragQueryRequest struct {
	KnowledgeExcerptBytes int    `json:"knowledge_excerpt_bytes,omitempty"`
	AccountID             string `json:"account_id"`
	TenantID              string `json:"tenant_id,omitempty"`
	Query                 string `json:"query"`
	Module                string `json:"module,omitempty"`
	CollectionName        string `json:"collection_name,omitempty"`
	NumberOfResults       int    `json:"k,omitempty"`
	ConversationID        string `json:"conversation_id"`
	MessageID             string `json:"message_id,omitempty"`
	UserID                string `json:"user_id,omitempty"`
	AgentID               string `json:"agent_id,omitempty"`
	TrackTokenUsage       *bool  `json:"track_token_usage,omitempty"`
	// RestrictToCollection turns CollectionName from "also search this" into
	// "search ONLY this". rag-server intersects it with the collections the
	// account could already search, so it narrows and never widens. Used by the
	// KB retrieval probe when scoped to a single knowledge base.
	RestrictToCollection bool `json:"restrict_to_collection,omitempty"`
	// MetadataFilter accepts scalars (equality), []any (IN — Qdrant MatchAny),
	// or map[string]any with the range keys {gte, gt, lte, lt} (Qdrant Range).
	// The rag-server dispatcher decides by value shape — see
	// llm/rag-server/rag/search/filters.py::build_field_condition.
	MetadataFilter map[string]any `json:"metadata_filter,omitempty"`
	// UseReranking requests server-side LLM reranking for THIS call only.
	// Omitted (nil) defers to rag-server's RAG_RERANKING_ENABLED default, so
	// existing callers keep today's behavior. The reranker reads query + doc
	// text, re-weights scores (0.4·cosine + 0.6·LLM) and drops docs below
	// RANKING_THRESHOLD — cosine near-ties (observed 0.002 between sibling
	// runbooks) become decisively ordered, at the cost of one extra LLM call
	// inside retrieval.
	UseReranking *bool `json:"use_reranking,omitempty"`
}

type RAGSearchResult struct {
	Document        string         `json:"document"`
	Metadata        map[string]any `json:"metadata"`
	SimilarityScore float32        `json:"similarity_score"`
}

type RAGSearchResults []RAGSearchResult

const ragUnexpectedResponseStatus = "rag: unexpected response status: %d"

// ragErrorBodyMaxBytes caps the RAG error-response body we read into the
// error message. rag-server's error payloads are short JSON blobs (a
// handful of bytes), so 8KB is generous while bounding worst-case memory
// against a misbehaving upstream returning a huge / streaming body.
const ragErrorBodyMaxBytes = 8 * 1024
const ragAgentNameRequired = "rag: agentName is required"
const ragUnauthorized = "auth: unauthorized"
const ragFailedToGetDatabaseManager = "rag: failed to get database manager"

var ragClient = &http.Client{
	Transport: &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   2 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ResponseHeaderTimeout: 120 * time.Second,
	},
}

func getRAGServerURL() string {
	ragServerUrl := config.Config.RAGServerUrl
	if !strings.HasSuffix(ragServerUrl, "/") {
		ragServerUrl += "/"
	}
	return ragServerUrl
}

// addRAGAuth attaches the per-service bearer token used by the
// rag-server middleware. Same X-ACTION-TOKEN header convention as
// every other backend (so callers don't have to special-case
// rag-server), but the *value* is rag-server-specific so a leak of
// one backend's token doesn't open the others.
func addRAGAuth(req *http.Request) {
	if token := config.Config.RAGServerToken; token != "" {
		req.Header.Set("X-ACTION-TOKEN", token)
	}
}

func executeRAGCall(ctx context.Context, payload ragQueryRequest) (RAGSearchResults, error) {
	// Stamp trace_id/span_id from the caller's context so RAG round-trips
	// correlate with the conversation's other log lines in Loki.
	logger := common.TraceLogger(ctx)
	if payload.Module == "knowledge_base" && config.Config.LlmServerKnowledgeWorkspaceEnabled {
		payload.KnowledgeExcerptBytes = KnowledgeExcerptBytes
	}
	data, err := common.MarshalJson(payload)
	if err != nil {
		logger.Warn("rag: failed to marshal JSON payload", "error", err.Error())
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", getRAGServerURL()+"get_matching_doc", bytes.NewBuffer(data))
	if err != nil {
		logger.Warn("rag: failed to create request", "error", err.Error())
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	addRAGAuth(req)

	logger.Info("rag: sending request", "url", req.URL.String())

	resp, err := ragClient.Do(req)
	if err != nil {
		logger.Warn("rag: failed to send request", "error", err.Error())
		return nil, err
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			logger.Warn("rag: failed to close response body", "error", err.Error())
		}
	}()

	if resp.StatusCode != http.StatusOK {
		// Bounded read: rag-server error payloads are short JSON blobs, and
		// logging the raw resp.Body would only print the ReadCloser's pointer.
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, ragErrorBodyMaxBytes))
		logger.Warn("rag: response status not OK", "status", resp.StatusCode, "body", string(errBody))
		return nil, fmt.Errorf(ragUnexpectedResponseStatus, resp.StatusCode)
	}

	var reader io.Reader = resp.Body
	if payload.Module == "knowledge_base" {
		reader = io.LimitReader(resp.Body, 1024*1024+1)
	}
	body, err := io.ReadAll(reader)
	if payload.Module == "knowledge_base" && len(body) > 1024*1024 {
		return nil, fmt.Errorf("knowledge discovery response exceeds limit; update RAG server or narrow search")
	}
	if err != nil {
		logger.Warn("rag: failed to read response body", "error", err.Error())
		return nil, err
	}

	if len(body) == 0 {
		logger.Warn("rag: empty response body")
		return nil, fmt.Errorf("rag: empty response body")
	}
	// Parse the response JSON
	var response RAGSearchResults
	if err := common.UnmarshalJson(body, &response); err != nil {
		logger.Warn("rag: failed to parse response JSON", "error", err, "data", string(body))
		return nil, err
	}
	return response, nil
}

// Retrieves a single document from the RAG server
func GetRAG(userId, accountId, query, module, conversationID, messageId, agentId string, trackTokenUsage bool) RAGSearchResult {
	payload := ragQueryRequest{
		AccountID:       accountId,
		Query:           query,
		Module:          module,
		NumberOfResults: 1,
		ConversationID:  conversationID,
		MessageID:       messageId,
		AgentID:         agentId,
		TrackTokenUsage: &trackTokenUsage,
		UserID:          userId,
	}

	searchResults, err := executeRAGCall(context.Background(), payload)
	if err != nil {
		return RAGSearchResult{}
	}
	if len(searchResults) == 0 {
		return RAGSearchResult{}
	}
	// Return the first document
	response := searchResults[0]

	if response.Document != "" {
		return response
	}
	return RAGSearchResult{}
}

func QueryRAG(userId, accountId, query, module string, numberOfResults int, conversationID string, messageId string, agentId string, trackTokenUsage bool, metadataFilter ...map[string]any) RAGSearchResults {
	var mf map[string]any
	if len(metadataFilter) > 0 {
		mf = metadataFilter[0]
	}
	// Non-context caller: use background. Callers that need cancellation
	// should invoke QueryRAGCollection directly with their own ctx.
	return QueryRAGCollection(context.Background(), userId, accountId, "", query, module, "", numberOfResults, conversationID, messageId, agentId, trackTokenUsage, mf)
}

// QueryRAGReranked is QueryRAG with server-side LLM reranking requested for
// this call only — the global RAG_RERANKING_ENABLED default stays untouched
// for every other caller. Used by the KB pre-step, where ordering decides
// which page wins the prompt budget: cosine alone ranked two sibling runbooks
// 0.002 apart and put the wrong one first. Reranking adds an LLM call inside
// retrieval, so callers must budget a longer timeout than a plain search.
func QueryRAGReranked(userId, accountId, query, module string, numberOfResults int, conversationID string, messageId string, agentId string, trackTokenUsage bool) RAGSearchResults {
	return QueryRAGRerankedContext(context.Background(), userId, accountId, query, module, numberOfResults, conversationID, messageId, agentId, trackTokenUsage)
}

// QueryRAGRerankedContext cancels the HTTP request when automatic discovery expires.
func QueryRAGRerankedContext(ctx context.Context, userId, accountId, query, module string, numberOfResults int, conversationID string, messageId string, agentId string, trackTokenUsage bool) RAGSearchResults {
	yes := true
	payload := ragQueryRequest{
		AccountID:       accountId,
		Query:           query,
		Module:          module,
		NumberOfResults: numberOfResults,
		ConversationID:  conversationID,
		MessageID:       messageId,
		AgentID:         agentId,
		TrackTokenUsage: &trackTokenUsage,
		UserID:          userId,
		UseReranking:    &yes,
	}
	response, err := executeRAGCall(ctx, payload)
	if err != nil {
		return RAGSearchResults{}
	}
	return response
}

// QueryRAGCollectionReranked is QueryRAGReranked narrowed to ONE collection.
// Used by the knowledge-base retrieval probe when the operator scopes a test to
// a single KB: same reranking as the account-wide search, so the scores shown
// in the two modes mean the same thing.
//
// Narrowing is enforced by rag-server, which intersects collectionName with the
// set this account/tenant could already search — naming a collection outside
// that set returns nothing rather than reading it.
func QueryRAGCollectionReranked(ctx context.Context, userId, accountId, query, module, collectionName string, numberOfResults int, conversationID string, messageId string, agentId string, trackTokenUsage bool) RAGSearchResults {
	yes := true
	payload := ragQueryRequest{
		AccountID:            accountId,
		Query:                query,
		Module:               module,
		CollectionName:       collectionName,
		RestrictToCollection: true,
		NumberOfResults:      numberOfResults,
		ConversationID:       conversationID,
		MessageID:            messageId,
		AgentID:              agentId,
		TrackTokenUsage:      &trackTokenUsage,
		UserID:               userId,
		UseReranking:         &yes,
	}
	response, err := executeRAGCall(ctx, payload)
	if err != nil {
		return RAGSearchResults{}
	}
	return response
}

// Retrieves multiple documents from the RAG server.
// The optional metadataFilter parameter allows filtering results by metadata
// fields. Values can be scalars (equality), []any (IN), or a map with the
// range keys gte/gt/lte/lt (Range). See rag_service.ragQueryRequest.MetadataFilter.
//
// ctx is threaded through to http.NewRequestWithContext so caller
// cancellations (e.g. compose's timeout goroutine) actually abort the
// in-flight HTTP request instead of leaking the goroutine until rag-server
// eventually responds. Pass context.Background() when cancellation isn't
// available.
//
// tenantId is optional. When set, rag-server skips its own
// account→tenant Postgres lookup (a ~2s cache-miss path in dev). Callers
// that already know the tenant scope (e.g. memory-v2 compose, which
// sends the tenant UUID as account_id) should pass it.
func QueryRAGCollection(ctx context.Context, userId, accountId, tenantId, query, module, collectionName string, numberOfResults int, conversationID string, messageId string, agentId string, trackTokenUsage bool, metadataFilter ...map[string]any) RAGSearchResults {
	payload := ragQueryRequest{
		AccountID:       accountId,
		TenantID:        tenantId,
		Query:           query,
		Module:          module,
		CollectionName:  collectionName,
		NumberOfResults: numberOfResults,
		ConversationID:  conversationID,
		MessageID:       messageId,
		AgentID:         agentId,
		TrackTokenUsage: &trackTokenUsage,
		UserID:          userId,
	}
	if len(metadataFilter) > 0 && metadataFilter[0] != nil {
		payload.MetadataFilter = metadataFilter[0]
	}

	response, err := executeRAGCall(ctx, payload)
	if err != nil {
		return RAGSearchResults{}
	}
	return response
}

type ragServerCreateRequest struct {
	AccountID string         `json:"account_id"`
	Module    string         `json:"module"`
	Data      string         `json:"data"`
	Format    string         `json:"format,omitempty"`
	ID        string         `json:"id,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

type AgentRag struct {
	AccountId    string `json:"account_id" db:"account_id"`
	AgentId      string `json:"agent_id" db:"agent_id"`
	DataFilename string `json:"data_filename" db:"data_filename"`
	DataFormat   string `json:"data_format" db:"data_format"`
	CreatedBy    string `json:"created_by" db:"created_by"`
	CreatedAt    string `json:"created_at" db:"created_at"`
	UpdatedAt    string `json:"updated_at" db:"updated_at"`
}

func DeleteAgentRags(sc *security.RequestContext, accountId, agent string) error {
	if accountId == "" {
		return errors.New("rags: accountId is required")
	}

	if agent == "" {
		return errors.New(ragAgentNameRequired)
	}

	// validate if user has access
	if !sc.GetSecurityContext().HasAccountAccess(accountId, security.SecurityAccessTypeCreate) {
		slog.Error("rag: failed to get account access")
		return errors.New(ragUnauthorized)
	}

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		slog.Error(ragFailedToGetDatabaseManager, "error", err)
		return err
	}

	_, err = dbms.Db.Exec("delete from llm_rags where account_id = $1 and agent_id = $2", accountId, agent)

	if err != nil {
		slog.Error("rag: failed to delete agent rag", "error", err)
		return err
	}

	return nil
}

func ListAgentRags(sc *security.RequestContext, accountId, agent string) ([]AgentRag, error) {
	if accountId == "" {
		return []AgentRag{}, errors.New("rag: accountId is required")
	}

	if agent == "" {
		return []AgentRag{}, errors.New(ragAgentNameRequired)
	}

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		slog.Error(ragFailedToGetDatabaseManager, "error", err)
		return []AgentRag{}, err
	}
	// validate if user has access
	if !sc.GetSecurityContext().HasAccountAccess(accountId, security.SecurityAccessTypeRead) {
		slog.Error("rag: failed to get account access")
		return []AgentRag{}, errors.New(ragUnauthorized)
	}

	rows, err := dbms.Db.Queryx("select account_id, agent_id, data_filename, data_format, created_by, created_at, updated_at from llm_rags where account_id = $1 and agent_id = $2", accountId, agent)

	if err != nil {
		slog.Error("rag: failed to get agent rags", "error", err)
		return []AgentRag{}, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Error("rag: failed to close rows", "error", err)
		}
	}()
	agentRags := make([]AgentRag, 0)
	for rows.Next() {
		agentRag := AgentRag{}
		err = rows.StructScan(&agentRag)
		if err != nil {
			slog.Error("rag: failed to scan agent rag", "error", err)
			continue
		}
		agentRags = append(agentRags, agentRag)
	}

	return agentRags, nil
}

func CreateAgentRag(sc *security.RequestContext, accountId, agent, data string, format string, fileName string) (AgentRag, error) {

	if len(data) == 0 {
		return AgentRag{}, errors.New("rag: data is required")
	}

	if accountId == "" {
		return AgentRag{}, errors.New("rag: accountId is required")
	}

	if agent == "" {
		return AgentRag{}, errors.New(ragAgentNameRequired)
	}

	if len(data) > 1000000 {
		return AgentRag{}, errors.New("rag: data is too large")
	}

	payloadType := ""

	payload := ragServerCreateRequest{
		AccountID: accountId,
		Module:    agent,
		Data:      data,
		Format:    payloadType,
	}

	if format != "" {
		payload.Format = format
	}

	if payload.Format == "" {

		if strings.HasPrefix(data, "[") && strings.HasSuffix(data, "]") {
			payload.Format = "json"
		}
		if strings.HasPrefix(data, "<") && strings.HasSuffix(data, ">") {
			payload.Format = "xml"
		}
	}

	if payload.Format == "" {
		payload.Format = "text"
	}

	if payload.Format != "json" && payload.Format != "xml" && payload.Format != "csv" && payload.Format != "text" {
		return AgentRag{}, errors.New("rag: invalid format, supported formats are json, xml, csv, text")
	}

	dataArr, err := common.MarshalJson(payload)
	if err != nil {
		slog.Warn("rag: failed to marshal JSON payload", "error", err)
		return AgentRag{}, err
	}

	req, err := http.NewRequest("POST", getRAGServerURL()+"load_account_module_docs", bytes.NewBuffer(dataArr))
	if err != nil {
		slog.Warn("rag: failed to create request", "error", err)
		return AgentRag{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	addRAGAuth(req)

	client := ragClient
	resp, err := client.Do(req)
	if err != nil {
		slog.Warn("rag: failed to send request", "error", err)
		return AgentRag{}, err
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			slog.Warn("rag: failed to close response body", "error", err.Error())
		}
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		slog.Warn("rag: failed to read response body", "error", err)
		return AgentRag{}, err
	}

	if resp.StatusCode != http.StatusOK {
		slog.Warn("rag: unexpected response status", "status", resp.StatusCode, "body", string(body))
		return AgentRag{}, fmt.Errorf(ragUnexpectedResponseStatus, resp.StatusCode)
	}

	response := map[string]string{}
	if err := common.UnmarshalJson(body, &response); err != nil {
		slog.Warn("rag: failed to parse response JSON", "error", err, "data", string(body))
		return AgentRag{}, err
	}

	// store data to db
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		slog.Error(ragFailedToGetDatabaseManager, "error", err)
		return AgentRag{}, err
	}

	// validate if user has access
	if !sc.GetSecurityContext().HasAccountAccess(accountId, security.SecurityAccessTypeCreate) {
		slog.Error("agent: failed to get account access", "error", err)
		return AgentRag{}, errors.New(ragUnauthorized)
	}

	// default values
	if fileName == "" {
		fileName = fmt.Sprintf("rag_%s", uuid.New().String())
	}

	nullableCreatedBy := sql.NullString{String: sc.GetSecurityContext().GetUserId(), Valid: sc.GetSecurityContext().GetUserId() != ""}
	createdAt := time.Now()
	updatedAt := time.Now()
	_, err = dbms.DoInTransaction(func(tx *sqlx.Tx) (any, error) {
		_, err = tx.Exec("insert into llm_rags (tenant_id, account_id, agent_id, data_filename, data_format, created_by, created_at, updated_at, data) values ($1, $2, $3, $4, $5, $6, $7, $8, $9)",
			sc.GetSecurityContext().GetTenantId(), accountId, agent, fileName, payload.Format, nullableCreatedBy, createdAt, updatedAt, payload.Data)
		if err != nil {
			slog.Error("rag: failed to insert agent rag", "error", err)
			return nil, err
		}

		return nil, nil
	})
	if err != nil {
		return AgentRag{}, err
	}
	return AgentRag{
		AccountId:    accountId,
		AgentId:      agent,
		DataFilename: fileName,
		DataFormat:   payload.Format,
		CreatedBy:    sc.GetSecurityContext().GetUserId(),
		CreatedAt:    createdAt.Format(time.RFC3339),
		UpdatedAt:    updatedAt.Format(time.RFC3339),
	}, nil

}

func AddMemoryToRAG(accountId, content, id, memoryType string) error {
	slog.Info("rag: AddMemoryToRAG called",
		"accountId", accountId,
		"id", id,
		"memoryType", memoryType,
		"content_len", len(content),
		"content_preview", content[:min(100, len(content))])

	if accountId == "" || content == "" {
		slog.Warn("rag: AddMemoryToRAG validation failed", "accountId_empty", accountId == "", "content_empty", content == "")
		return errors.New("rag: accountId and content are required")
	}

	metadata := map[string]any{
		"_id": id,
	}
	if memoryType != "" {
		metadata["memory_type"] = memoryType
	}

	payload := ragServerCreateRequest{
		AccountID: accountId,
		Module:    "long_term_memory",
		Data:      content,
		Format:    "text",
		ID:        id,
		Metadata:  metadata,
	}

	dataArr, err := common.MarshalJson(payload)
	if err != nil {
		slog.Warn("rag: failed to marshal JSON payload", "error", err)
		return err
	}

	ragURL := getRAGServerURL() + "load_account_module_docs"
	slog.Info("rag: sending request to RAG server", "url", ragURL)

	req, err := http.NewRequest("POST", ragURL, bytes.NewBuffer(dataArr))
	if err != nil {
		slog.Warn("rag: failed to create request", "error", err)
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	addRAGAuth(req)

	client := ragClient
	resp, err := client.Do(req)
	if err != nil {
		slog.Warn("rag: failed to send request", "error", err, "url", ragURL)
		return err
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			slog.Warn("rag: failed to close response body", "error", err.Error())
		}
	}()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		slog.Warn("rag: unexpected response status", "status", resp.StatusCode, "body", string(body), "url", ragURL)
		return fmt.Errorf(ragUnexpectedResponseStatus, resp.StatusCode)
	}

	slog.Info("rag: successfully added memory to RAG", "id", id, "accountId", accountId)
	return nil
}

// IngestDocumentToRAG is the generic ingest counterpart to QueryRAG —
// upserts a single (id, text, metadata) triple into rag-server on the
// {accountId, module} collection. Used by the memory-v2 RAG projector
// (memory/rag_projector.go) which needs a module-parameterized ingest
// so per-layer collections (memory_patterns, memory_decisions, etc.)
// share the same client.
//
// Failure is a plain error: callers decide whether to retry (the outbox
// worker does) or fail-loud (the Slice 2 Erase fanout will).
func IngestDocumentToRAG(ctx context.Context, accountId, module, id, text string, metadata map[string]any) error {
	if accountId == "" || module == "" || id == "" {
		return errors.New("rag: accountId, module, id are required")
	}
	if text == "" {
		return errors.New("rag: text is required")
	}
	payload := ragServerCreateRequest{
		AccountID: accountId,
		Module:    module,
		Data:      text,
		Format:    "text",
		ID:        id,
		Metadata:  metadata,
	}
	dataArr, err := common.MarshalJson(payload)
	if err != nil {
		return fmt.Errorf("rag: IngestDocumentToRAG: marshal: %w", err)
	}
	ragURL := getRAGServerURL() + "load_account_module_docs"
	req, err := http.NewRequestWithContext(ctx, "POST", ragURL, bytes.NewBuffer(dataArr))
	if err != nil {
		return fmt.Errorf("rag: IngestDocumentToRAG: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	addRAGAuth(req)
	resp, err := ragClient.Do(req)
	if err != nil {
		return fmt.Errorf("rag: IngestDocumentToRAG: send request: %w", err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			slog.Warn("rag: IngestDocumentToRAG: close body", "error", cerr.Error())
		}
	}()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, ragErrorBodyMaxBytes))
		return fmt.Errorf("rag: IngestDocumentToRAG: %w",
			fmt.Errorf(ragUnexpectedResponseStatus+" body=%s", resp.StatusCode, string(body)))
	}
	return nil
}

// DeleteDocumentFromRAG is the module-parameterized delete counterpart to
// IngestDocumentToRAG. Symmetric shape lets the memory-v2 delete-fanout
// (TTL sweeper + Erase, Slice 2 of memory-rag-integration) hit the same
// (accountId, module) namespace it wrote via IngestDocumentToRAG. The
// legacy DeleteMemoryFromRAG stays around for long-term-memory callers;
// new callers should prefer this one.
//
// Return semantics: any non-2xx from rag-server surfaces as an error. The
// Erase caller uses this to gate the follow-on Postgres delete
// (fail-loud) — a silent-success on a hosed rag-server would leave
// orphan tombstone tasks for the sweeper to retry indefinitely.
func DeleteDocumentFromRAG(ctx context.Context, accountID, module, id string) error {
	if accountID == "" || module == "" || id == "" {
		return errors.New("rag: accountID, module, id are required")
	}
	payload := ragServerCreateRequest{
		AccountID: accountID,
		Module:    module,
		ID:        id,
	}
	dataArr, err := common.MarshalJson(payload)
	if err != nil {
		return fmt.Errorf("rag: DeleteDocumentFromRAG: marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, "POST", getRAGServerURL()+"delete_account_module_docs", bytes.NewBuffer(dataArr))
	if err != nil {
		return fmt.Errorf("rag: DeleteDocumentFromRAG: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	addRAGAuth(req)
	resp, err := ragClient.Do(req)
	if err != nil {
		return fmt.Errorf("rag: DeleteDocumentFromRAG: send request: %w", err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			slog.Warn("rag: DeleteDocumentFromRAG: close body", "error", cerr.Error())
		}
	}()
	// Idempotency: treat 404 as success. A missing doc in rag-server
	// means the desired end state is already reached, which is what
	// GDPR Erase (fail-loud on any real error) and the TTL tombstone
	// sweeper (retry-until-gone) both actually want. Returning an
	// error on 404 would abort the follow-on Postgres delete in Erase
	// and pin the sweeper in an infinite retry loop for any row that
	// never got projected in the first place.
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, ragErrorBodyMaxBytes))
		return fmt.Errorf("rag: DeleteDocumentFromRAG: %w",
			fmt.Errorf(ragUnexpectedResponseStatus+" body=%s", resp.StatusCode, string(body)))
	}
	return nil
}

func DeleteMemoryFromRAG(accountId, id string) error {
	if accountId == "" || id == "" {
		return errors.New("rag: accountId and id are required")
	}

	payload := ragServerCreateRequest{
		AccountID: accountId,
		Module:    "long_term_memory",
		ID:        id,
	}

	dataArr, err := common.MarshalJson(payload)
	if err != nil {
		slog.Warn("rag: failed to marshal JSON payload", "error", err)
		return err
	}

	// Assuming the endpoint is delete_account_module_docs
	req, err := http.NewRequest("POST", getRAGServerURL()+"delete_account_module_docs", bytes.NewBuffer(dataArr))
	if err != nil {
		slog.Warn("rag: failed to create request", "error", err)
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	addRAGAuth(req)

	client := ragClient
	resp, err := client.Do(req)
	if err != nil {
		slog.Warn("rag: failed to send request", "error", err)
		return err
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			slog.Warn("rag: failed to close response body", "error", err.Error())
		}
	}()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		slog.Warn("rag: unexpected response status", "status", resp.StatusCode, "body", string(body))
		return fmt.Errorf(ragUnexpectedResponseStatus, resp.StatusCode)
	}

	return nil
}
