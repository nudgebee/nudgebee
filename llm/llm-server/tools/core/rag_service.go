package core

import (
	"bytes"
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
	AccountID       string            `json:"account_id"`
	Query           string            `json:"query"`
	Module          string            `json:"module,omitempty"`
	CollectionName  string            `json:"collection_name,omitempty"`
	NumberOfResults int               `json:"k,omitempty"`
	ConversationID  string            `json:"conversation_id"`
	MessageID       string            `json:"message_id,omitempty"`
	UserID          string            `json:"user_id,omitempty"`
	AgentID         string            `json:"agent_id,omitempty"`
	TrackTokenUsage *bool             `json:"track_token_usage,omitempty"`
	MetadataFilter  map[string]string `json:"metadata_filter,omitempty"`
}

type RAGSearchResult struct {
	Document        string         `json:"document"`
	Metadata        map[string]any `json:"metadata"`
	SimilarityScore float32        `json:"similarity_score"`
}

type RAGSearchResults []RAGSearchResult

const ragUnexpectedResponseStatus = "rag: unexpected response status: %d"
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

func executeRAGCall(payload ragQueryRequest) (RAGSearchResults, error) {
	data, err := common.MarshalJson(payload)
	if err != nil {
		slog.Warn("rag: failed to marshal JSON payload", "error", err.Error())
		return nil, err
	}

	req, err := http.NewRequest("POST", getRAGServerURL()+"get_matching_doc", bytes.NewBuffer(data))
	if err != nil {
		slog.Warn("rag: failed to create request", "error", err.Error())
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	addRAGAuth(req)

	slog.Info("rag: sending request", "url", req.URL.String())

	resp, err := ragClient.Do(req)
	if err != nil {
		slog.Warn("rag: failed to send request", "error", err.Error())
		return nil, err
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			slog.Warn("rag: failed to close response body", "error", err.Error())
		}
	}()

	if resp.StatusCode != http.StatusOK {
		slog.Warn("rag: response status not OK", "status", resp.StatusCode, "body", resp.Body)
		return nil, fmt.Errorf(ragUnexpectedResponseStatus, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		slog.Warn("rag: failed to read response body", "error", err.Error())
		return nil, err
	}

	if len(body) == 0 {
		slog.Warn("rag: empty response body")
		return nil, fmt.Errorf("rag: empty response body")
	}
	if resp.StatusCode != http.StatusOK {
		slog.Warn("rag: unexpected response status", "status", resp.StatusCode, "body", string(body))
		return nil, fmt.Errorf(ragUnexpectedResponseStatus, resp.StatusCode)
	}
	// Parse the response JSON
	var response RAGSearchResults
	if err := common.UnmarshalJson(body, &response); err != nil {
		slog.Warn("rag: failed to parse response JSON", "error", err, "data", string(body))
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

	searchResults, err := executeRAGCall(payload)
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

func QueryRAG(userId, accountId, query, module string, numberOfResults int, conversationID string, messageId string, agentId string, trackTokenUsage bool, metadataFilter ...map[string]string) RAGSearchResults {
	var mf map[string]string
	if len(metadataFilter) > 0 {
		mf = metadataFilter[0]
	}
	return QueryRAGCollection(userId, accountId, query, module, "", numberOfResults, conversationID, messageId, agentId, trackTokenUsage, mf)
}

// Retrieves multiple documents from the RAG server.
// The optional metadataFilter parameter allows filtering results by metadata
// fields (e.g., map[string]string{"source": "confluence"}).
func QueryRAGCollection(userId, accountId, query, module, collectionName string, numberOfResults int, conversationID string, messageId string, agentId string, trackTokenUsage bool, metadataFilter ...map[string]string) RAGSearchResults {
	payload := ragQueryRequest{
		AccountID:       accountId,
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

	response, err := executeRAGCall(payload)
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
