package core

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"nudgebee/llm/audit"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

// Worker pool for async KB embedding generation
var kbEmbeddingWorkerPool *common.WorkerPool

const CacheNamespaceLlmKbMapping = "llm_kb_mapping"

// CacheNamespaceLlmSkillContent caches individual skill content fetched by load_skills.
// Declared here (in tools/core) so KB update/delete paths can invalidate it.
const CacheNamespaceLlmSkillContent = "llm_skill_content"

func init() {
	workerCount := 3 // Default worker count for KB embeddings
	if config.Config.AsyncApiWorkerCount > 0 {
		workerCount = config.Config.AsyncApiWorkerCount
	}
	kbEmbeddingWorkerPool = common.NewWorkerPool("kb_embedding", workerCount, workerCount*10)
	common.CacheCreateNamespace(CacheNamespaceLlmKbMapping, common.CacheNamespaceWithExpiration(30*time.Minute))
	common.CacheCreateNamespace(CacheNamespaceLlmSkillContent, common.CacheNamespaceWithExpiration(15*time.Minute))
}

// Knowledgebase represents an account-scoped knowledge base
type Knowledgebase struct {
	Id            string     `json:"id" db:"id"`
	TenantId      string     `json:"tenant_id" db:"tenant_id"`
	AccountId     string     `json:"account_id" db:"account_id"`
	Name          string     `json:"name" db:"name"`
	Description   string     `json:"description,omitempty" db:"description"`
	Data          string     `json:"data,omitempty" db:"data"` // Omit in list responses
	DataFormat    string     `json:"data_format" db:"data_format"`
	DataFilename  string     `json:"data_filename" db:"data_filename"`
	DataSizeBytes int64      `json:"data_size_bytes" db:"data_size_bytes"`
	Status        string     `json:"status" db:"status"`
	KBType        string     `json:"kb_type" db:"kb_type"`                         // Type: manual or integration
	KBSource      *string    `json:"kb_source,omitempty" db:"kb_source"`           // Source: confluence, servicenow (null for manual)
	IntegrationId *string    `json:"integration_id,omitempty" db:"integration_id"` // Link to integrations table (null for manual)
	CreatedBy     string     `json:"created_by,omitempty" db:"created_by"`         // Display name, not UUID
	UpdatedBy     string     `json:"updated_by,omitempty" db:"updated_by"`         // Display name, not UUID
	CreatedAt     time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at" db:"updated_at"`
	DocumentCount *int       `json:"document_count,omitempty" db:"document_count"`
	LastLoadedAt  *time.Time `json:"last_loaded_at,omitempty" db:"last_loaded_at"`
}

// KBLoadHistoryEntry represents a single load history record from rag_embedding_token_usage.
type KBLoadHistoryEntry struct {
	Id                    string    `json:"id" db:"id"`
	DocumentCount         int       `json:"document_count" db:"document_count"`
	ExpectedDocumentCount *int      `json:"expected_document_count,omitempty" db:"expected_document_count"`
	TotalTokens           int64     `json:"total_tokens" db:"total_tokens"`
	EmbeddingProvider     *string   `json:"embedding_provider,omitempty" db:"embedding_provider"`
	EmbeddingModel        *string   `json:"embedding_model,omitempty" db:"embedding_model"`
	RequestStatus         string    `json:"request_status" db:"request_status"`
	ErrorMessage          *string   `json:"error_message,omitempty" db:"error_message"`
	TriggerType           *string   `json:"trigger_type,omitempty" db:"trigger_type"`
	TriggeredBy           *string   `json:"triggered_by,omitempty" db:"triggered_by"`
	LoadDurationSeconds   *float64  `json:"load_duration_seconds,omitempty" db:"load_duration_seconds"`
	CreatedAt             time.Time `json:"created_at" db:"created_at"`
}

// KBAgentMapping represents a many-to-many mapping between KB and agents
type KBAgentMapping struct {
	KbId      string    `json:"kb_id" db:"kb_id"`
	AgentId   string    `json:"agent_id" db:"agent_id"`
	AccountId string    `json:"account_id" db:"account_id"`
	CreatedBy string    `json:"created_by,omitempty" db:"created_by"` // Display name, not UUID
	UpdatedBy string    `json:"updated_by,omitempty" db:"updated_by"` // Display name, not UUID
	CreatedAt time.Time `json:"created_at" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
}

// KnowledgebaseStatus represents the processing status
type KnowledgebaseStatus string

const (
	KBStatusActive     KnowledgebaseStatus = "active"
	KBStatusProcessing KnowledgebaseStatus = "processing"
	KBStatusError      KnowledgebaseStatus = "error"
	KBStatusArchived   KnowledgebaseStatus = "archived"
)

// Knowledgebase types — manual (user-created) vs integration (synced from an
// external system such as Confluence or ServiceNow).
const (
	KBTypeManual      = "manual"
	KBTypeIntegration = "integration"
)

// KnowledgebaseFormat represents supported file formats
type KnowledgebaseFormat string

const (
	KBFormatJSON KnowledgebaseFormat = "json"
	KBFormatXML  KnowledgebaseFormat = "xml"
	KBFormatCSV  KnowledgebaseFormat = "csv"
	KBFormatText KnowledgebaseFormat = "text"
	KBFormatPDF  KnowledgebaseFormat = "pdf"
)

// Error constants
const (
	errKBAccountIDRequired = "account ID is required"
	errKBNameRequired      = "please provide a name for the knowledgebase"
	errKBNameInvalid       = "knowledgebase name must start with a letter and contain only letters, numbers, spaces, hyphens, underscores, or colons (3-100 characters)"
	errKBIDRequired        = "knowledgebase ID is required"
	errKBDataRequired      = "please provide content for the knowledgebase"
	errKBFormatRequired    = "file format is required"
	errKBFormatInvalid     = "unsupported file format. Please use JSON, XML, CSV, Text, or PDF"
	errKBFilenameRequired  = "filename is required"
	errKBSizeTooLarge      = "file size exceeds the maximum limit of 10MB"
	errKBAlreadyExists     = "a knowledgebase with this name already exists in your account"
	errKBNotFound          = "knowledgebase not found"
	errKBUnauthorized      = "you don't have permission to access this knowledgebase"
	errKBIntegrationDelete = "integration knowledge bases are managed by the integration; disable the integration to remove it"
)

// Log messages
const (
	logKBFailedDBManager = "kb: failed to get database manager"
	logKBFailedQuery     = "kb: failed to execute query"
	logKBFailedScan      = "kb: failed to scan row"
)

const (
	maxKBSizeBytes = 10 * 1024 * 1024 // 10MB
)

// ValidateFormat checks if the format is supported
func ValidateKBFormat(format string) error {
	validFormats := map[string]bool{
		string(KBFormatJSON): true,
		string(KBFormatXML):  true,
		string(KBFormatCSV):  true,
		string(KBFormatText): true,
		string(KBFormatPDF):  true,
	}

	if !validFormats[format] {
		return errors.New(errKBFormatInvalid)
	}
	return nil
}

// CreateKnowledgebase creates a new knowledge base with vector embeddings
func CreateKnowledgebase(sc *security.RequestContext, accountId string, kb Knowledgebase) (Knowledgebase, error) {
	// Validation
	if accountId == "" {
		return Knowledgebase{}, errors.New(errKBAccountIDRequired)
	}

	kb.Name = strings.TrimSpace(kb.Name)
	if kb.Name == "" {
		return Knowledgebase{}, errors.New(errKBNameRequired)
	}

	if !common.IsValidKBName(kb.Name) {
		return Knowledgebase{}, errors.New(errKBNameInvalid)
	}

	if kb.Data == "" {
		return Knowledgebase{}, errors.New(errKBDataRequired)
	}

	if kb.DataFormat == "" {
		return Knowledgebase{}, errors.New(errKBFormatRequired)
	}

	if err := ValidateKBFormat(kb.DataFormat); err != nil {
		return Knowledgebase{}, err
	}

	if kb.DataFilename == "" {
		return Knowledgebase{}, errors.New(errKBFilenameRequired)
	}

	kb.DataSizeBytes = int64(len(kb.Data))
	if kb.DataSizeBytes > maxKBSizeBytes {
		return Knowledgebase{}, fmt.Errorf("%s: %d bytes", errKBSizeTooLarge, kb.DataSizeBytes)
	}

	tenantId := sc.GetSecurityContext().GetTenantId()
	if tenantId == "" {
		return Knowledgebase{}, errors.New("auth: tenantId is required")
	}

	// Check access
	if !sc.GetSecurityContext().HasAccountAccess(accountId, security.SecurityAccessTypeCreate) {
		slog.Error("kb: failed to get account access")
		return Knowledgebase{}, errors.New(errKBUnauthorized)
	}

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		slog.Error(logKBFailedDBManager, "error", err)
		return Knowledgebase{}, err
	}

	// Check if KB with same name already exists
	var exists bool
	err = dbms.Db.Get(&exists, "SELECT EXISTS(SELECT 1 FROM llm_knowledgebases WHERE account_id = $1 AND name = $2)", accountId, kb.Name)
	if err != nil {
		slog.Error("kb: failed to check existence", "error", err)
		return Knowledgebase{}, err
	}
	if exists {
		return Knowledgebase{}, errors.New(errKBAlreadyExists)
	}

	// Set defaults
	kb.Id = uuid.NewString()
	kb.TenantId = tenantId
	kb.AccountId = accountId
	kb.Status = string(KBStatusProcessing) // Set to processing initially
	kb.CreatedBy = sc.GetSecurityContext().GetUserId()
	kb.UpdatedBy = sc.GetSecurityContext().GetUserId()

	nullableCreatedBy := sql.NullString{String: kb.CreatedBy, Valid: kb.CreatedBy != ""}
	nullableUpdatedBy := sql.NullString{String: kb.UpdatedBy, Valid: kb.UpdatedBy != ""}
	nullableDescription := sql.NullString{String: kb.Description, Valid: kb.Description != ""}

	// Insert into database
	kbAny, err := dbms.DoInTransaction(func(tx *sqlx.Tx) (any, error) {
		_, err = tx.Exec(`
			INSERT INTO llm_knowledgebases
			(id, tenant_id, account_id, name, description, data, data_format, data_filename, data_size_bytes, status, kb_type, kb_source, created_by, updated_by, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, NOW(), NOW())`,
			kb.Id, kb.TenantId, kb.AccountId, kb.Name, nullableDescription, kb.Data,
			kb.DataFormat, kb.DataFilename, kb.DataSizeBytes, kb.Status, "manual", nil, nullableCreatedBy, nullableUpdatedBy)
		if err != nil {
			slog.Error("kb: failed to insert", "error", err)
			return Knowledgebase{}, err
		}

		// Fetch the created KB with usernames
		var createdKB Knowledgebase
		err = tx.Get(&createdKB, `
			SELECT kb.id, kb.tenant_id, kb.account_id, kb.name,
			       COALESCE(kb.description, '') as description,
			       kb.data, kb.data_format, kb.data_filename, kb.data_size_bytes,
			       kb.status, kb.kb_type, kb.kb_source, kb.integration_id,
			       COALESCE(cu.display_name, '') as created_by,
			       COALESCE(uu.display_name, '') as updated_by,
			       kb.created_at, kb.updated_at,
			       kb.document_count, kb.last_loaded_at
			FROM llm_knowledgebases kb
			LEFT JOIN users cu ON kb.created_by = cu.id
			LEFT JOIN users uu ON kb.updated_by = uu.id
			WHERE kb.id = $1`, kb.Id)
		if err != nil {
			slog.Error("kb: failed to fetch created KB", "error", err)
			return Knowledgebase{}, err
		}
		return createdKB, nil
	})

	if err != nil {
		return Knowledgebase{}, err
	}

	createdKB := kbAny.(Knowledgebase)

	// Submit async embedding generation to worker pool
	submissionCtx, cancel := context.WithTimeout(context.Background(), time.Duration(config.Config.AsyncOperationTimeoutSeconds)*time.Second)
	defer cancel()
	_ = kbEmbeddingWorkerPool.Submit(submissionCtx, func() {
		processKBEmbeddingsAsync(sc, accountId, createdKB.Id, kb.Data, kb.DataFormat)
	})

	// Create audit entry
	auditReq := &audit.AuditRequest{
		Audits: []audit.Audit{
			{
				AccountId:     accountId,
				EventTime:     time.Now().UTC(),
				EventCategory: "knowledgebase",
				EventType:     "KB_CREATE",
				EventState:    createdKB,
				EventActor:    audit.EventActor(sc.GetSecurityContext().GetUserId()),
				EventTarget:   createdKB.Id,
				EventAction:   audit.EventActionCreate,
				EventStatus:   audit.EventStatusSuccess,
				TransactionId: sc.GetTraceId(),
				EventAttr:     map[string]any{"kb_id": createdKB.Id, "kb_name": createdKB.Name},
				TenantId:      tenantId,
				UserId:        sc.GetSecurityContext().GetUserId(),
			},
		},
	}
	auditErr := audit.CreateAudit(sc, auditReq)
	if auditErr != nil {
		slog.Error("kb: failed to create audit entry", "error", auditErr, "kb_id", createdKB.Id)
	}

	// Don't return data in response
	createdKB.Data = ""
	return createdKB, nil
}

// GetKnowledgebase retrieves a single knowledge base by ID
func GetKnowledgebase(sc *security.RequestContext, accountId, kbId string) (Knowledgebase, error) {
	if accountId == "" {
		return Knowledgebase{}, errors.New(errKBAccountIDRequired)
	}
	if kbId == "" {
		return Knowledgebase{}, errors.New(errKBIDRequired)
	}

	// Check access
	if !sc.GetSecurityContext().HasAccountAccess(accountId, security.SecurityAccessTypeRead) {
		return Knowledgebase{}, errors.New(errKBUnauthorized)
	}

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		slog.Error(logKBFailedDBManager, "error", err)
		return Knowledgebase{}, err
	}

	var kb Knowledgebase
	err = dbms.Db.Get(&kb, `
		SELECT kb.id, kb.tenant_id, kb.account_id, kb.name,
		       COALESCE(kb.description, '') as description,
		       kb.data, kb.data_format, kb.data_filename, kb.data_size_bytes,
		       kb.status, kb.kb_type, kb.kb_source, kb.integration_id,
		       kb.created_at, kb.updated_at,
		       kb.document_count, kb.last_loaded_at,
		       COALESCE(cu.display_name, '') as created_by,
		       COALESCE(uu.display_name, '') as updated_by
		FROM llm_knowledgebases kb
		LEFT JOIN users cu ON kb.created_by = cu.id
		LEFT JOIN users uu ON kb.updated_by = uu.id
		WHERE kb.id = $1 AND kb.account_id = $2`, kbId, accountId)
	if err != nil {
		if err == sql.ErrNoRows {
			return Knowledgebase{}, errors.New(errKBNotFound)
		}
		slog.Error(logKBFailedQuery, "error", err, "kb_id", kbId)
		return Knowledgebase{}, err
	}

	return kb, nil
}

// ListKnowledgebases retrieves all knowledge bases for an account (without data field)
func ListKnowledgebases(sc *security.RequestContext, accountId string) ([]Knowledgebase, error) {
	if accountId == "" {
		return nil, errors.New(errKBAccountIDRequired)
	}

	// Check access
	if !sc.GetSecurityContext().HasAccountAccess(accountId, security.SecurityAccessTypeRead) {
		return nil, errors.New(errKBUnauthorized)
	}

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		slog.Error(logKBFailedDBManager, "error", err)
		return nil, err
	}

	var kbs []Knowledgebase
	err = dbms.Db.Select(&kbs, `
		SELECT kb.id, kb.tenant_id, kb.account_id, kb.name,
		       COALESCE(kb.description, '') as description,
		       kb.data_format, kb.data_filename,
		       kb.data_size_bytes, kb.status,
		       kb.kb_type, kb.kb_source, kb.integration_id,
		       kb.created_at, kb.updated_at,
		       kb.document_count, kb.last_loaded_at,
		       COALESCE(cu.display_name, '') as created_by,
		       COALESCE(uu.display_name, '') as updated_by
		FROM llm_knowledgebases kb
		LEFT JOIN users cu ON kb.created_by = cu.id
		LEFT JOIN users uu ON kb.updated_by = uu.id
		WHERE kb.account_id = $1
		ORDER BY kb.created_at DESC`, accountId)
	if err != nil {
		slog.Error(logKBFailedQuery, "error", err, "account_id", accountId)
		return nil, err
	}

	return kbs, nil
}

// UpdateKnowledgebase updates an existing knowledge base
func UpdateKnowledgebase(sc *security.RequestContext, accountId, kbId string, updates Knowledgebase) error {
	if accountId == "" {
		return errors.New(errKBAccountIDRequired)
	}
	if kbId == "" {
		return errors.New(errKBIDRequired)
	}

	// Check access
	if !sc.GetSecurityContext().HasAccountAccess(accountId, security.SecurityAccessTypeUpdate) {
		return errors.New(errKBUnauthorized)
	}

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		slog.Error(logKBFailedDBManager, "error", err)
		return err
	}

	// Check if KB exists
	var existingKB Knowledgebase
	err = dbms.Db.Get(&existingKB, `
		SELECT kb.id, kb.tenant_id, kb.account_id, kb.name,
		       COALESCE(kb.description, '') as description,
		       kb.data, kb.data_format, kb.data_filename, kb.data_size_bytes,
		       kb.status,
		       kb.created_at, kb.updated_at,
		       kb.document_count, kb.last_loaded_at,
		       COALESCE(cu.display_name, '') as created_by,
		       COALESCE(uu.display_name, '') as updated_by
		FROM llm_knowledgebases kb
		LEFT JOIN users cu ON kb.created_by = cu.id
		LEFT JOIN users uu ON kb.updated_by = uu.id
		WHERE kb.id = $1 AND kb.account_id = $2`, kbId, accountId)
	if err != nil {
		if err == sql.ErrNoRows {
			return errors.New(errKBNotFound)
		}
		return err
	}

	// Check if name is being changed and conflicts with another KB
	if updates.Name != "" && updates.Name != existingKB.Name {
		updates.Name = strings.TrimSpace(updates.Name)
		if !common.IsValidKBName(updates.Name) {
			return errors.New(errKBNameInvalid)
		}

		var nameExists bool
		err = dbms.Db.Get(&nameExists, "SELECT EXISTS(SELECT 1 FROM llm_knowledgebases WHERE account_id = $1 AND name = $2 AND id != $3)", accountId, updates.Name, kbId)
		if err != nil {
			return err
		}
		if nameExists {
			return errors.New(errKBAlreadyExists)
		}
	} else {
		updates.Name = existingKB.Name // Keep existing name
	}

	// Determine if data is being updated
	dataChanged := updates.Data != "" && updates.Data != existingKB.Data

	if dataChanged {
		// Validate format if provided
		if updates.DataFormat != "" {
			if err := ValidateKBFormat(updates.DataFormat); err != nil {
				return err
			}
		} else {
			updates.DataFormat = existingKB.DataFormat
		}

		updates.DataSizeBytes = int64(len(updates.Data))
		if updates.DataSizeBytes > maxKBSizeBytes {
			return fmt.Errorf("%s: %d bytes", errKBSizeTooLarge, updates.DataSizeBytes)
		}
	} else {
		// Keep existing data fields
		updates.Data = existingKB.Data
		updates.DataFormat = existingKB.DataFormat
		updates.DataFilename = existingKB.DataFilename
		updates.DataSizeBytes = existingKB.DataSizeBytes
	}

	if updates.Description == "" {
		updates.Description = existingKB.Description
	}

	updates.UpdatedBy = sc.GetSecurityContext().GetUserId()
	nullableUpdatedBy := sql.NullString{String: updates.UpdatedBy, Valid: updates.UpdatedBy != ""}
	nullableDescription := sql.NullString{String: updates.Description, Valid: updates.Description != ""}

	// If data changed, set status to processing
	if dataChanged {
		updates.Status = string(KBStatusProcessing)
	}

	nullableStatus := sql.NullString{String: updates.Status, Valid: updates.Status != ""}

	// Update database
	_, err = dbms.DoInTransaction(func(tx *sqlx.Tx) (any, error) {
		query := `
			UPDATE llm_knowledgebases
			SET name = $1, description = $2, data = $3, data_format = $4, data_filename = $5,
			    data_size_bytes = $6, updated_by = $7, updated_at = NOW()`
		args := []any{
			updates.Name, nullableDescription, updates.Data, updates.DataFormat, updates.DataFilename,
			updates.DataSizeBytes, nullableUpdatedBy,
		}

		// Only update status if data changed
		if dataChanged {
			query += `, status = $8 WHERE id = $9 AND account_id = $10`
			args = append(args, nullableStatus, kbId, accountId)
		} else {
			query += ` WHERE id = $8 AND account_id = $9`
			args = append(args, kbId, accountId)
		}

		_, err = tx.Exec(query, args...)
		if err != nil {
			slog.Error("kb: failed to update", "error", err, "kb_id", kbId)
			return nil, err
		}
		return nil, nil
	})

	if err != nil {
		return err
	}

	// If data changed, update embeddings asynchronously and invalidate skill content cache.
	if dataChanged {
		submissionCtx, cancel := context.WithTimeout(context.Background(), time.Duration(config.Config.AsyncOperationTimeoutSeconds)*time.Second)
		defer cancel()
		_ = kbEmbeddingWorkerPool.Submit(submissionCtx, func() {
			processKBUpdateEmbeddingsAsync(sc, accountId, kbId, updates.Data, updates.DataFormat, "user_update")
		})
		// Invalidate cached skill content so the updated content is served immediately.
		skillCacheKey := fmt.Sprintf("skill:%s:%s", accountId, strings.ToLower(updates.Name))
		if err := common.CacheDelete(CacheNamespaceLlmSkillContent, skillCacheKey); err != nil {
			slog.Warn("kb: failed to invalidate skill content cache", "error", err, "key", skillCacheKey)
		}
	}

	// Create audit entry
	auditReq := &audit.AuditRequest{
		Audits: []audit.Audit{
			{
				AccountId:     accountId,
				EventTime:     time.Now().UTC(),
				EventCategory: "knowledgebase",
				EventType:     "KB_UPDATE",
				EventState:    updates,
				EventActor:    audit.EventActor(sc.GetSecurityContext().GetUserId()),
				EventTarget:   kbId,
				EventAction:   audit.EventActionUpdate,
				EventStatus:   audit.EventStatusSuccess,
				TransactionId: sc.GetTraceId(),
				EventAttr:     map[string]any{"kb_id": kbId, "kb_name": updates.Name, "data_changed": dataChanged},
				TenantId:      sc.GetSecurityContext().GetTenantId(),
				UserId:        sc.GetSecurityContext().GetUserId(),
			},
		},
	}
	auditErr := audit.CreateAudit(sc, auditReq)
	if auditErr != nil {
		slog.Error("kb: failed to create audit entry", "error", auditErr, "kb_id", kbId)
	}

	return nil
}

// DeleteKnowledgebase deletes a knowledge base and its vector collection
func DeleteKnowledgebase(sc *security.RequestContext, accountId, kbId string) error {
	if accountId == "" {
		return errors.New(errKBAccountIDRequired)
	}
	if kbId == "" {
		return errors.New(errKBIDRequired)
	}

	// Check access
	if !sc.GetSecurityContext().HasAccountAccess(accountId, security.SecurityAccessTypeDelete) {
		return errors.New(errKBUnauthorized)
	}

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		slog.Error(logKBFailedDBManager, "error", err)
		return err
	}

	// Get KB details for audit before deletion
	var kb Knowledgebase
	err = dbms.Db.Get(&kb, "SELECT id, name, account_id, tenant_id, kb_type, status, integration_id FROM llm_knowledgebases WHERE id = $1 AND account_id = $2", kbId, accountId)
	if err != nil {
		if err == sql.ErrNoRows {
			return errors.New(errKBNotFound)
		}
		return err
	}

	// Integration KBs are managed by the integration sync — only an archived
	// one (its integration has been disabled) may be deleted, for cleanup.
	if kb.KBType == KBTypeIntegration && kb.Status != string(KBStatusArchived) {
		return errors.New(errKBIntegrationDelete)
	}

	// Delete the vector collection first. Manual KBs own a kb_<id> collection;
	// an archived integration KB's documents live in the shared
	// <integration_id>_knowledge_base collection. Continue with DB deletion
	// even if vector deletion fails.
	if kb.KBType == KBTypeIntegration {
		// Every cloud account in a tenant gets its own KB row for the same
		// integration, all backed by the one shared <integration_id>_knowledge_base
		// collection. Only drop the collection when this is the last row, so
		// deleting one never strands the siblings. Keep it on a count error.
		siblings := 1
		if kb.IntegrationId != nil {
			if err := dbms.Db.Get(&siblings,
				"SELECT COUNT(*) FROM llm_knowledgebases WHERE integration_id = $1 AND id != $2",
				*kb.IntegrationId, kbId); err != nil {
				slog.Error("kb: failed to count sibling integration KBs", "error", err, "kb_id", kbId)
				siblings = 1
			}
		}
		if siblings == 0 {
			if err := deleteRAGCollection(kbCollectionName(kb.KBType, kb.IntegrationId, kbId)); err != nil {
				slog.Error("kb: failed to delete integration vector collection", "error", err, "kb_id", kbId)
			}
		} else {
			slog.Info("kb: keeping shared integration collection still referenced by other KB rows",
				"kb_id", kbId, "siblings", siblings)
		}
	} else if err := deleteKBVectorCollection(accountId, kbId); err != nil {
		slog.Error("kb: failed to delete vector collection", "error", err, "kb_id", kbId)
	}

	// Delete from database
	_, err = dbms.DoInTransaction(func(tx *sqlx.Tx) (any, error) {
		result, err := tx.Exec("DELETE FROM llm_knowledgebases WHERE id = $1 AND account_id = $2", kbId, accountId)
		if err != nil {
			slog.Error("kb: failed to delete", "error", err, "kb_id", kbId)
			return nil, err
		}
		rowsAffected, _ := result.RowsAffected()
		if rowsAffected == 0 {
			return nil, errors.New(errKBNotFound)
		}
		return nil, nil
	})

	if err != nil {
		return err
	}

	// Invalidate caches
	if err := common.CacheClear(CacheNamespaceLlmKbMapping); err != nil {
		slog.Error("kb: failed to clear KB mapping cache", "error", err)
	}
	skillCacheKey := fmt.Sprintf("skill:%s:%s", accountId, strings.ToLower(kb.Name))
	if err := common.CacheDelete(CacheNamespaceLlmSkillContent, skillCacheKey); err != nil {
		slog.Warn("kb: failed to invalidate skill content cache", "error", err, "key", skillCacheKey)
	}

	// Create audit entry
	auditReq := &audit.AuditRequest{
		Audits: []audit.Audit{
			{
				AccountId:     accountId,
				EventTime:     time.Now().UTC(),
				EventCategory: "knowledgebase",
				EventType:     "KB_DELETE",
				EventState:    kb,
				EventActor:    audit.EventActor(sc.GetSecurityContext().GetUserId()),
				EventTarget:   kbId,
				EventAction:   audit.EventActionDelete,
				EventStatus:   audit.EventStatusSuccess,
				TransactionId: sc.GetTraceId(),
				EventAttr:     map[string]any{"kb_id": kbId, "kb_name": kb.Name},
				TenantId:      sc.GetSecurityContext().GetTenantId(),
				UserId:        sc.GetSecurityContext().GetUserId(),
			},
		},
	}
	auditErr := audit.CreateAudit(sc, auditReq)
	if auditErr != nil {
		slog.Error("kb: failed to create audit entry", "error", auditErr, "kb_id", kbId)
	}

	return nil
}

// Helper function to update KB status
func updateKBStatus(dbms *common.DatabaseManager, kbId string, status string) error {
	_, err := dbms.Db.Exec("UPDATE llm_knowledgebases SET status = $1, updated_at = NOW() WHERE id = $2", status, kbId)
	return err
}

// kbCollectionName returns the rag-server vector collection name for a KB.
// Manual KBs are stored as kb_<id>; integration KBs share a per-integration
// collection named <integration_id>_knowledge_base.
func kbCollectionName(kbType string, integrationID *string, kbId string) string {
	if kbType == KBTypeIntegration && integrationID != nil && *integrationID != "" {
		return *integrationID + "_knowledge_base"
	}
	return "kb_" + kbId
}

// GetKBLoadHistory returns the load history for a knowledge base by querying rag_embedding_token_usage.
func GetKBLoadHistory(sc *security.RequestContext, accountId, kbId string) ([]KBLoadHistoryEntry, error) {
	if accountId == "" || kbId == "" {
		return nil, errors.New(errKBAccountIDRequired)
	}
	if !sc.GetSecurityContext().HasAccountAccess(accountId, security.SecurityAccessTypeRead) {
		return nil, errors.New(errKBUnauthorized)
	}

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil, fmt.Errorf("kb: failed to get database manager: %w", err)
	}

	// Resolve the rag-server collection name: manual KBs use kb_<id>, integration
	// KBs store their load events under <integration_id>_knowledge_base.
	var kbMeta struct {
		KBType        string  `db:"kb_type"`
		IntegrationID *string `db:"integration_id"`
	}
	err = dbms.Db.Get(&kbMeta, "SELECT kb_type, integration_id FROM llm_knowledgebases WHERE id = $1 AND account_id = $2", kbId, accountId)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, errors.New(errKBNotFound)
		}
		return nil, fmt.Errorf("kb: failed to fetch knowledgebase: %w", err)
	}
	collectionName := kbCollectionName(kbMeta.KBType, kbMeta.IntegrationID, kbId)

	// Filter by collection_name only: it is globally unique for both kb_<id>
	// and <integration_id>_knowledge_base, and integration-scrape rows are
	// written with account_id='global' (tenant-scoped collections), so an
	// account_id filter would hide them. triggered_by stores a user ID (or
	// "system") — resolve it to a display name.
	var entries []KBLoadHistoryEntry
	err = dbms.Db.Select(&entries, `
		SELECT r.id::text, r.document_count, r.expected_document_count, r.total_tokens,
		       r.embedding_provider, r.embedding_model, r.request_status, r.error_message,
		       r.trigger_type, COALESCE(tu.display_name, r.triggered_by) AS triggered_by,
		       r.load_duration_seconds, r.created_at
		FROM rag_embedding_token_usage r
		LEFT JOIN users tu ON tu.id::text = r.triggered_by
		WHERE r.collection_name = $1
		  AND r.operation_type = 'batch_embedding'
		ORDER BY r.created_at DESC
		LIMIT 50`, collectionName)
	if err != nil {
		slog.Error("kb: failed to query load history", "error", err, "kb_id", kbId)
		return nil, fmt.Errorf("kb: failed to query load history: %w", err)
	}
	if entries == nil {
		entries = []KBLoadHistoryEntry{}
	}
	return entries, nil
}

// RetriggerKnowledgebase re-triggers embedding generation for a KB.
func RetriggerKnowledgebase(sc *security.RequestContext, accountId, kbId string) error {
	if accountId == "" {
		return errors.New(errKBAccountIDRequired)
	}
	if kbId == "" {
		return errors.New(errKBIDRequired)
	}
	if !sc.GetSecurityContext().HasAccountAccess(accountId, security.SecurityAccessTypeUpdate) {
		return errors.New(errKBUnauthorized)
	}

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return fmt.Errorf("kb: failed to get database manager: %w", err)
	}

	// Fetch existing KB
	var existingKB struct {
		Data          string  `db:"data"`
		DataFormat    string  `db:"data_format"`
		Status        string  `db:"status"`
		KBType        string  `db:"kb_type"`
		KBSource      *string `db:"kb_source"`
		IntegrationID *string `db:"integration_id"`
	}
	err = dbms.Db.Get(&existingKB, "SELECT data, data_format, status, kb_type, kb_source, integration_id FROM llm_knowledgebases WHERE id = $1 AND account_id = $2", kbId, accountId)
	if err != nil {
		if err == sql.ErrNoRows {
			return errors.New(errKBNotFound)
		}
		return fmt.Errorf("kb: failed to fetch knowledgebase: %w", err)
	}

	if existingKB.Status == string(KBStatusProcessing) {
		return errors.New("knowledgebase is currently being processed")
	}

	// Set status to processing. The terminal active/error flip is owned by the
	// rag-server once embedding completes.
	if err := updateKBStatus(dbms, kbId, string(KBStatusProcessing)); err != nil {
		return fmt.Errorf("kb: failed to update status to processing: %w", err)
	}

	if existingKB.KBType == KBTypeIntegration {
		// Integration KB: re-scrape the source integration. The `data` column
		// is empty for integration KBs — their content lives in the
		// integration's own vector collection.
		if existingKB.IntegrationID == nil || existingKB.KBSource == nil {
			_ = updateKBStatus(dbms, kbId, string(KBStatusError))
			return errors.New("kb: integration knowledgebase is missing its integration reference")
		}
		retriggerCtx, cancel := context.WithTimeout(context.Background(), time.Duration(config.Config.AsyncOperationTimeoutSeconds)*time.Second)
		err := triggerIntegrationKBSync(retriggerCtx, accountId, *existingKB.IntegrationID, *existingKB.KBSource, sc.GetSecurityContext().GetUserId())
		cancel()
		if err != nil {
			_ = updateKBStatus(dbms, kbId, string(KBStatusError))
			return fmt.Errorf("kb: failed to trigger integration sync: %w", err)
		}
	} else {
		// Manual KB: re-embed the stored data (delete old → create new).
		submissionCtx, cancel := context.WithTimeout(context.Background(), time.Duration(config.Config.AsyncOperationTimeoutSeconds)*time.Second)
		defer cancel()
		if err := kbEmbeddingWorkerPool.Submit(submissionCtx, func() {
			processKBUpdateEmbeddingsAsync(sc, accountId, kbId, existingKB.Data, existingKB.DataFormat, "user_retrigger")
		}); err != nil {
			_ = updateKBStatus(dbms, kbId, string(KBStatusError))
			return fmt.Errorf("kb: failed to submit retrigger task: %w", err)
		}
	}

	// Audit
	auditReq := &audit.AuditRequest{
		Audits: []audit.Audit{
			{
				AccountId:     accountId,
				EventTime:     time.Now().UTC(),
				EventCategory: "knowledgebase",
				EventType:     "KB_RETRIGGER",
				EventActor:    audit.EventActor(sc.GetSecurityContext().GetUserId()),
				EventTarget:   kbId,
				EventAction:   audit.EventActionUpdate,
				EventStatus:   audit.EventStatusSuccess,
				TransactionId: sc.GetTraceId(),
				EventAttr:     map[string]any{"kb_id": kbId},
				TenantId:      sc.GetSecurityContext().GetTenantId(),
				UserId:        sc.GetSecurityContext().GetUserId(),
			},
		},
	}
	_ = audit.CreateAudit(sc, auditReq)

	return nil
}

// processKBEmbeddingsAsync handles embedding generation asynchronously
func processKBEmbeddingsAsync(sc *security.RequestContext, accountId, kbId, data, format string) {
	slog.Info("kb: starting async embedding generation", "kb_id", kbId, "account_id", accountId)

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		slog.Error("kb: async - failed to get database manager", "error", err, "kb_id", kbId)
		return
	}

	// Call RAG server to create vector embeddings
	triggeredBy := sc.GetSecurityContext().GetUserId()
	docCount, err := createKBVectorCollection(accountId, kbId, data, format, triggeredBy, "user_create")
	if err != nil {
		slog.Error("kb: async - failed to create vector collection", "error", err, "kb_id", kbId)
		// Update status to error
		_ = updateKBStatus(dbms, kbId, string(KBStatusError))

		// Create audit entry for failure
		auditReq := &audit.AuditRequest{
			Audits: []audit.Audit{
				{
					AccountId:     accountId,
					EventTime:     time.Now().UTC(),
					EventCategory: "knowledgebase",
					EventType:     "KB_EMBEDDING_ERROR",
					EventActor:    audit.EventActor(sc.GetSecurityContext().GetUserId()),
					EventTarget:   kbId,
					EventAction:   audit.EventActionUpdate,
					EventStatus:   audit.EventStatusFailure,
					TransactionId: sc.GetTraceId(),
					EventAttr:     map[string]any{"kb_id": kbId, "error": err.Error()},
					TenantId:      sc.GetSecurityContext().GetTenantId(),
					UserId:        sc.GetSecurityContext().GetUserId(),
				},
			},
		}
		_ = audit.CreateAudit(sc, auditReq)
		return
	}

	// The rag-server owns the terminal status flip: it sets the KB to active
	// (or error) and updates document_count/last_loaded_at when embedding
	// completes. llm-server only sets error above when the call itself fails.
	slog.Info("kb: async embedding generation completed successfully", "kb_id", kbId, "document_count", docCount)

	// Create audit entry for success
	auditReq := &audit.AuditRequest{
		Audits: []audit.Audit{
			{
				AccountId:     accountId,
				EventTime:     time.Now().UTC(),
				EventCategory: "knowledgebase",
				EventType:     "KB_EMBEDDING_READY",
				EventActor:    audit.EventActor(sc.GetSecurityContext().GetUserId()),
				EventTarget:   kbId,
				EventAction:   audit.EventActionUpdate,
				EventStatus:   audit.EventStatusSuccess,
				TransactionId: sc.GetTraceId(),
				EventAttr:     map[string]any{"kb_id": kbId},
				TenantId:      sc.GetSecurityContext().GetTenantId(),
				UserId:        sc.GetSecurityContext().GetUserId(),
			},
		},
	}
	_ = audit.CreateAudit(sc, auditReq)
}

// processKBUpdateEmbeddingsAsync handles embedding update asynchronously
func processKBUpdateEmbeddingsAsync(sc *security.RequestContext, accountId, kbId, data, format, triggerType string) {
	slog.Info("kb: starting async embedding update", "kb_id", kbId, "account_id", accountId)

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		slog.Error("kb: async update - failed to get database manager", "error", err, "kb_id", kbId)
		return
	}

	// Delete old vector collection first
	err = deleteKBVectorCollection(accountId, kbId)
	if err != nil {
		slog.Error("kb: async update - failed to delete old vector collection", "error", err, "kb_id", kbId)
		// Continue with creation even if deletion fails
	}

	// Create new vector collection
	triggeredBy := sc.GetSecurityContext().GetUserId()
	docCount, err := createKBVectorCollection(accountId, kbId, data, format, triggeredBy, triggerType)
	if err != nil {
		slog.Error("kb: async update - failed to create vector collection", "error", err, "kb_id", kbId)
		_ = updateKBStatus(dbms, kbId, string(KBStatusError))

		auditReq := &audit.AuditRequest{
			Audits: []audit.Audit{
				{
					AccountId:     accountId,
					EventTime:     time.Now().UTC(),
					EventCategory: "knowledgebase",
					EventType:     "KB_EMBEDDING_UPDATE_ERROR",
					EventActor:    audit.EventActor(sc.GetSecurityContext().GetUserId()),
					EventTarget:   kbId,
					EventAction:   audit.EventActionUpdate,
					EventStatus:   audit.EventStatusFailure,
					TransactionId: sc.GetTraceId(),
					EventAttr:     map[string]any{"kb_id": kbId, "error": err.Error()},
					TenantId:      sc.GetSecurityContext().GetTenantId(),
					UserId:        sc.GetSecurityContext().GetUserId(),
				},
			},
		}
		_ = audit.CreateAudit(sc, auditReq)
		return
	}

	// The rag-server owns the terminal status flip when embedding completes.
	slog.Info("kb: async embedding update completed successfully", "kb_id", kbId, "document_count", docCount)

	// Create audit entry for success
	auditReq := &audit.AuditRequest{
		Audits: []audit.Audit{
			{
				AccountId:     accountId,
				EventTime:     time.Now().UTC(),
				EventCategory: "knowledgebase",
				EventType:     "KB_EMBEDDING_UPDATED",
				EventActor:    audit.EventActor(sc.GetSecurityContext().GetUserId()),
				EventTarget:   kbId,
				EventAction:   audit.EventActionUpdate,
				EventStatus:   audit.EventStatusSuccess,
				TransactionId: sc.GetTraceId(),
				EventAttr:     map[string]any{"kb_id": kbId},
				TenantId:      sc.GetSecurityContext().GetTenantId(),
				UserId:        sc.GetSecurityContext().GetUserId(),
			},
		},
	}
	_ = audit.CreateAudit(sc, auditReq)
}

// Helper function to create vector collection via RAG server
func createKBVectorCollection(accountId, kbId, data, format, triggeredBy, triggerType string) (int, error) {
	ragServerURL := config.Config.RAGServerUrl
	if ragServerURL == "" {
		return 0, errors.New("failed to process knowledgebase for semantic search. Service not configured")
	}

	if triggeredBy == "" {
		triggeredBy = "system"
	}
	if triggerType == "" {
		triggerType = "user_create"
	}

	// When the stored format is "text", wrap the content in a JSON array so the
	// RAG server indexes it as a single document instead of splitting every
	// newline into a separate Qdrant point. The JSONLoader on the RAG side
	// iterates ".[]" and keeps each element intact — a string element is
	// stored directly as page_content without further splitting.
	ragData := data
	ragFormat := format
	if format == "text" {
		wrapped, jsonErr := common.MarshalJson([]string{data})
		if jsonErr == nil {
			ragData = string(wrapped)
			ragFormat = "json"
		}
		// If marshalling somehow fails, fall through with original text format.
	}

	payload := map[string]any{
		"account_id":   accountId,
		"kb_id":        kbId,
		"data":         ragData,
		"format":       ragFormat,
		"triggered_by": triggeredBy,
		"trigger_type": triggerType,
	}

	payloadBytes, err := common.MarshalJson(payload)
	if err != nil {
		return 0, fmt.Errorf("failed to prepare knowledgebase data: %w", err)
	}

	req, err := http.NewRequest("POST", ragServerURL+"/api/v1/kb/create", bytes.NewBuffer(payloadBytes))
	if err != nil {
		return 0, fmt.Errorf("failed to prepare search indexing request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	addRAGAuth(req)

	client := common.HttpClient()
	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("failed to process knowledgebase for semantic search: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			slog.Warn("kb: failed to close response body", "error", closeErr)
		}
	}()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20)) // 10 MB limit
	if err != nil {
		return 0, fmt.Errorf("failed to read RAG server response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("failed to process knowledgebase for semantic search (status %d): %s", resp.StatusCode, string(body))
	}

	// Parse document_count from RAG server response: {"status":"success","collection":"...","document_count":N}
	var ragResp struct {
		DocumentCount int `json:"document_count"`
	}
	if err := common.UnmarshalJson(body, &ragResp); err != nil {
		slog.Warn("kb: failed to parse RAG server response for document_count", "error", err)
	}

	return ragResp.DocumentCount, nil
}

// Helper function to delete vector collection via RAG server
func deleteKBVectorCollection(accountId, kbId string) error {
	ragServerURL := config.Config.RAGServerUrl
	if ragServerURL == "" {
		return errors.New("failed to remove knowledgebase from search index. Service not configured")
	}

	url := fmt.Sprintf("%s/api/v1/kb/%s/%s", ragServerURL, accountId, kbId)

	req, err := http.NewRequest("DELETE", url, nil)
	if err != nil {
		return fmt.Errorf("failed to prepare delete request: %w", err)
	}
	addRAGAuth(req)

	client := common.HttpClient()
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to remove knowledgebase from search index: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			slog.Warn("kb: failed to close response body", "error", closeErr)
		}
	}()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to remove knowledgebase from search index (status %d): %s", resp.StatusCode, string(body))
	}

	return nil
}

// triggerIntegrationKBSync asks the rag-server to re-scrape a single
// integration's knowledge base. The rag-server runs the scrape asynchronously
// and owns the KB's terminal active/error status flip. A 200 response — which
// includes the "already syncing" case — is treated as success.
func triggerIntegrationKBSync(ctx context.Context, accountId, integrationID, kbSource, triggeredBy string) error {
	ragServerURL := config.Config.RAGServerUrl
	if ragServerURL == "" {
		return errors.New("kb: rag-server is not configured")
	}
	if triggeredBy == "" {
		triggeredBy = "system"
	}

	payload := map[string]any{
		"account_id":     accountId,
		"integration_id": integrationID,
		"kb_source":      kbSource,
		"triggered_by":   triggeredBy,
	}
	payloadBytes, err := common.MarshalJson(payload)
	if err != nil {
		return fmt.Errorf("kb: failed to prepare integration sync request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", ragServerURL+"/api/v1/kb/retrigger_integration", bytes.NewBuffer(payloadBytes))
	if err != nil {
		return fmt.Errorf("kb: failed to prepare integration sync request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	addRAGAuth(req)

	resp, err := common.HttpClient().Do(req)
	if err != nil {
		return fmt.Errorf("kb: failed to reach rag-server for integration sync: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			slog.Warn("kb: failed to close response body", "error", closeErr)
		}
	}()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("kb: integration sync request failed (status %d): %s", resp.StatusCode, string(body))
	}
	return nil
}

// deleteRAGCollection removes a vector collection from the rag-server by name.
func deleteRAGCollection(collectionName string) error {
	ragServerURL := config.Config.RAGServerUrl
	if ragServerURL == "" {
		return errors.New("failed to remove knowledgebase from search index. Service not configured")
	}

	url := fmt.Sprintf("%s/api/v1/collections/%s", ragServerURL, collectionName)
	req, err := http.NewRequest("DELETE", url, nil)
	if err != nil {
		return fmt.Errorf("failed to prepare delete request: %w", err)
	}
	addRAGAuth(req)

	resp, err := common.HttpClient().Do(req)
	if err != nil {
		return fmt.Errorf("failed to remove collection from search index: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			slog.Warn("kb: failed to close response body", "error", closeErr)
		}
	}()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to remove collection from search index (status %d): %s", resp.StatusCode, string(body))
	}
	return nil
}

// ListAgentKBs returns all knowledge bases mapped to a specific agent
func ListAgentKBs(sc *security.RequestContext, accountId, agentId string) ([]Knowledgebase, error) {
	if accountId == "" {
		return nil, errors.New("account ID is required")
	}

	if agentId == "" {
		return nil, errors.New("agent ID is required")
	}

	// Validate user has read access to account
	if !sc.GetSecurityContext().HasAccountAccess(accountId, security.SecurityAccessTypeRead) {
		slog.Error("kb: user does not have read access to account", "account_id", accountId)
		return nil, errors.New("you don't have permission to access this resource")
	}

	// Check cache
	cacheKey := fmt.Sprintf("kb_mapping:%s:%s", accountId, agentId)
	if data, ok := common.CacheGet(CacheNamespaceLlmKbMapping, cacheKey); ok {
		var cachedKbs []Knowledgebase
		if err := json.Unmarshal(data, &cachedKbs); err == nil {
			return cachedKbs, nil
		}
	}

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		slog.Error("kb: failed to get database manager", "error", err)
		return nil, err
	}

	var kbs []Knowledgebase
	// Join with mapping table to get KBs for this agent
	// Note: Excluding data field for performance
	err = dbms.Db.Select(&kbs, `
		SELECT kb.id, kb.tenant_id, kb.account_id, kb.name,
		       COALESCE(kb.description, '') as description,
		       kb.data_format,
		       kb.data_filename, kb.data_size_bytes, kb.status,
		       COALESCE(kb.kb_type, 'manual') as kb_type,
		       kb.kb_source,
		       kb.created_at, kb.updated_at,
		       kb.document_count, kb.last_loaded_at,
		       COALESCE(cu.display_name, '') as created_by,
		       COALESCE(uu.display_name, '') as updated_by
		FROM llm_knowledgebases kb
		INNER JOIN llm_kb_agent_mappings m ON kb.id = m.kb_id
		LEFT JOIN users cu ON kb.created_by = cu.id
		LEFT JOIN users uu ON kb.updated_by = uu.id
		WHERE m.account_id = $1 AND m.agent_id = $2
		ORDER BY kb.created_at DESC`, accountId, agentId)

	if err != nil {
		slog.Error("kb: failed to list agent KBs", "error", err, "account_id", accountId, "agent_id", agentId)
		return nil, err
	}

	// Set cache
	if jsonData, err := json.Marshal(kbs); err == nil {
		if err := common.CacheSet(CacheNamespaceLlmKbMapping, cacheKey, jsonData); err != nil {
			slog.Error("kb: failed to set cache", "error", err, "key", cacheKey)
		}
	}

	return kbs, nil
}

// ListActiveAgentSkillCandidates returns lightweight (id, name, description) rows
// for every active KB mapped to any of the supplied agent names. The caller uses
// these for question-aware skill selection (see SelectRelevantSkills) without
// paying the cost of fetching the full skill bodies.
func ListActiveAgentSkillCandidates(sc *security.RequestContext, accountId string, agentNames []string) ([]SkillCandidate, error) {
	if accountId == "" || len(agentNames) == 0 {
		return nil, nil
	}
	if !sc.GetSecurityContext().HasAccountAccess(accountId, security.SecurityAccessTypeRead) {
		return nil, errors.New("you don't have permission to access this resource")
	}
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil, fmt.Errorf("ListActiveAgentSkillCandidates: failed to get database manager: %w", err)
	}
	var out []SkillCandidate
	err = dbms.Db.Select(&out, `
		SELECT DISTINCT ON (kb.id)
		       kb.id,
		       kb.name,
		       COALESCE(kb.description, '') as description
		FROM llm_knowledgebases kb
		INNER JOIN llm_kb_agent_mappings m ON kb.id = m.kb_id
		WHERE m.account_id = $1
		  AND m.agent_id = ANY($2::text[])
		  AND kb.status = 'active'
		ORDER BY kb.id, kb.name ASC`, accountId, pq.Array(agentNames))
	if err != nil {
		return nil, fmt.Errorf("ListActiveAgentSkillCandidates: query failed: %w", err)
	}
	return out, nil
}

// LoadActiveAgentSkillContents fetches the full content of every active knowledge
// base mapped to the supplied agent name chain and renders it as a single
// `<skills>...</skills>` block suitable for direct injection into an LLM prompt.
// It also returns one NBToolResponseReference per active skill so callers can
// surface "skills used by this agent" entries in the UI alongside other tool
// references.
//
// agentNames[0] is the agent's own name and is always loaded. agentNames[1:]
// are inherited ancestor names — set by custom-planner delegators (metrics,
// traces, logs, logs_default) so a delegated sub-agent can see KBs the user
// mapped to an upstream custom-planner parent.
//
// restrictToIds carries the top-level question-aware selection, mirroring
// injectKBContext:
//
//   - nil            → selection disabled (legacy "every active mapped skill")
//   - non-nil (any)  → selection ran at top-level. Own-name KBs are ALWAYS
//     loaded regardless — a sub-agent's scoped expertise must
//     never be hidden by an upstream parent's filter. Only
//     *inherited* KBs are filtered to the restriction.
//
// This is intended for agents whose planner type is AgentPlannerTypeCustom — those
// agents implement their own Execute() and bypass the executor's systemMessage path,
// so the lazy load_skills tool flow used by ReAct/ReWoo planners never reaches them.
// For such agents we eagerly inline the skill content (instead of just names and
// descriptions). Returns "" / nil when no active skills match.
// escapeCDATA makes a string safe to inline inside a `<![CDATA[...]]>` section
// by splitting any literal `]]>` across two CDATA sections. The sequence
// `]]>` is the only reserved marker inside CDATA — it's replaced with
// `]]]]><![CDATA[>` so the first `]]` stays inside the original section,
// the section closes, and a new CDATA section reopens to carry the trailing
// `>`. The concatenated visible text is identical to the input.
//
// Call this on user-authored content (skill bodies, tool outputs) before
// emitting it inside a CDATA wrapper — the LLM is not a strict XML parser
// but the repo convention is to keep our XML-like framing unambiguous.
func escapeCDATA(s string) string {
	if !strings.Contains(s, "]]>") {
		return s
	}
	return strings.ReplaceAll(s, "]]>", "]]]]><![CDATA[>")
}

// agentSkillRow is the full skill record (metadata + body) rendered into a
// `<skills>` block. Shared by every loader below so the framing stays identical.
type agentSkillRow struct {
	ID          string `db:"id"`
	Name        string `db:"name"`
	Description string `db:"description"`
	Data        string `db:"data"`
}

// renderSkillsBlock renders skill rows into the canonical `<skills>...</skills>`
// block and a parallel set of UI references. Returns "" / nil for no rows.
func renderSkillsBlock(rows []agentSkillRow) (string, []NBToolResponseReference) {
	if len(rows) == 0 {
		return "", nil
	}
	var sb strings.Builder
	sb.WriteString("<skills>\n")
	sb.WriteString("The following skills contain expert guidance mapped to this agent. ")
	sb.WriteString("Apply them to the current task where relevant.\n\n")
	references := make([]NBToolResponseReference, 0, len(rows))
	for _, r := range rows {
		fmt.Fprintf(&sb, "<skill name=%q>\n", r.Name)
		if r.Description != "" {
			fmt.Fprintf(&sb, "Description: %s\n", r.Description)
		}
		// Wrap the raw skill body in a CDATA section. Skill content is authored by
		// users and can contain characters that conflict with our XML-like wrapper
		// (including a literal `</skill>` in a skill that teaches XML). The LLM is
		// not a strict XML parser, but CDATA keeps the block structure unambiguous
		// and cheaply defends against malformed framing. The body itself may still
		// contain a literal `]]>` (e.g. a skill that teaches XML/XSD or embeds
		// CDATA examples) which would prematurely close the section — escapeCDATA
		// handles that via the standard split-the-marker trick.
		sb.WriteString("<![CDATA[\n")
		sb.WriteString(escapeCDATA(r.Data))
		sb.WriteString("\n]]>\n</skill>\n")

		references = append(references, NBToolResponseReference{
			Text:        r.Name,
			Url:         r.ID,
			Type:        "skill",
			Description: r.Description,
		})
	}
	sb.WriteString("</skills>")
	return sb.String(), references
}

// LoadAgentSkillContentsByIDs renders the `<skills>` block for an explicit set of
// active KB IDs (account-scoped), in id order. Unlike LoadActiveAgentSkillContents
// — which always loads an agent's own-name skills regardless of the question-aware
// selection — this loader honours the id set verbatim, narrowing own and inherited
// skills alike. It backs the code-analysis forward path: that service is stateless
// and receives skill bodies up front over HTTP, so (unlike the in-process ReAct/
// ReWoo planners, which can lazily load_skills mid-loop) the forwarded set must be
// pre-narrowed to exactly the query-relevant skills. An empty id set returns
// "" / nil — "nothing relevant to the question" forwards no skills.
func LoadAgentSkillContentsByIDs(sc *security.RequestContext, accountId string, ids []string) (string, []NBToolResponseReference, error) {
	if accountId == "" || len(ids) == 0 {
		return "", nil, nil
	}
	if !sc.GetSecurityContext().HasAccountAccess(accountId, security.SecurityAccessTypeRead) {
		return "", nil, errors.New("you don't have permission to access this resource")
	}
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return "", nil, fmt.Errorf("LoadAgentSkillContentsByIDs: failed to get database manager: %w", err)
	}
	var rows []agentSkillRow
	// account_id scoping is defense-in-depth: ids already come from an
	// account+agent-mapping-scoped candidate query, but filtering here too means a
	// spoofed id can never pull another tenant's skill body.
	err = dbms.Db.Select(&rows, `
		SELECT DISTINCT ON (kb.id)
		       kb.id,
		       kb.name,
		       COALESCE(kb.description, '') as description,
		       COALESCE(kb.data, '')        as data
		FROM llm_knowledgebases kb
		WHERE kb.account_id = $1
		  AND kb.id = ANY($2::text[])
		  AND kb.status = 'active'
		ORDER BY kb.id, kb.name ASC`, accountId, pq.Array(ids))
	if err != nil {
		return "", nil, fmt.Errorf("LoadAgentSkillContentsByIDs: query failed: %w", err)
	}
	block, refs := renderSkillsBlock(rows)
	return block, refs, nil
}

func LoadActiveAgentSkillContents(sc *security.RequestContext, accountId string, agentNames []string, restrictToIds []string) (string, []NBToolResponseReference, error) {
	if accountId == "" || len(agentNames) == 0 {
		return "", nil, nil
	}

	if !sc.GetSecurityContext().HasAccountAccess(accountId, security.SecurityAccessTypeRead) {
		return "", nil, errors.New("you don't have permission to access this resource")
	}

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return "", nil, fmt.Errorf("LoadActiveAgentSkillContents: failed to get database manager: %w", err)
	}

	// Own name (first entry) is ALWAYS loaded regardless of the top-level
	// selection — a sub-agent's scoped expertise must not be hidden by an upstream
	// parent's filter. Inherited ancestor names are only honoured when they pass
	// the selection. This mirrors the own/inherited split inside injectKBContext.
	ownName := agentNames[0]
	inheritedNames := agentNames[1:]

	selectionActive := restrictToIds != nil

	var rows []agentSkillRow
	switch {
	case !selectionActive:
		// Legacy show-all: every KB mapped to the full name set is loaded.
		err = dbms.Db.Select(&rows, `
			SELECT DISTINCT ON (kb.id)
			       kb.id,
			       kb.name,
			       COALESCE(kb.description, '') as description,
			       COALESCE(kb.data, '')        as data
			FROM llm_knowledgebases kb
			INNER JOIN llm_kb_agent_mappings m ON kb.id = m.kb_id
			WHERE m.account_id = $1
			  AND m.agent_id = ANY($2::text[])
			  AND kb.status = 'active'
			ORDER BY kb.id, kb.name ASC`, accountId, pq.Array(agentNames))

	case len(inheritedNames) == 0:
		// Selection active but no inheritance — own-name skills are always loaded.
		// The selection does not narrow own-name skills (they are the agent's own
		// scoped expertise). Callers that want selection to apply to a single-agent
		// case should set InheritSkillsFromAgents so that inheritance mode kicks in.
		err = dbms.Db.Select(&rows, `
			SELECT DISTINCT ON (kb.id)
			       kb.id,
			       kb.name,
			       COALESCE(kb.description, '') as description,
			       COALESCE(kb.data, '')        as data
			FROM llm_knowledgebases kb
			INNER JOIN llm_kb_agent_mappings m ON kb.id = m.kb_id
			WHERE m.account_id = $1
			  AND m.agent_id = $2
			  AND kb.status = 'active'
			ORDER BY kb.id, kb.name ASC`, accountId, ownName)

	case len(restrictToIds) == 0:
		// Selection ran but chose nothing. Sub-agent's own-name skills must still
		// load — inheritance is just hidden.
		err = dbms.Db.Select(&rows, `
			SELECT DISTINCT ON (kb.id)
			       kb.id,
			       kb.name,
			       COALESCE(kb.description, '') as description,
			       COALESCE(kb.data, '')        as data
			FROM llm_knowledgebases kb
			INNER JOIN llm_kb_agent_mappings m ON kb.id = m.kb_id
			WHERE m.account_id = $1
			  AND m.agent_id = $2
			  AND kb.status = 'active'
			ORDER BY kb.id, kb.name ASC`, accountId, ownName)

	default:
		// Selection active with both an own name and inherited names. Union via a
		// single SELECT: own-name KBs always, inherited-name KBs only when they
		// are in the selection set.
		err = dbms.Db.Select(&rows, `
			SELECT DISTINCT ON (kb.id)
			       kb.id,
			       kb.name,
			       COALESCE(kb.description, '') as description,
			       COALESCE(kb.data, '')        as data
			FROM llm_knowledgebases kb
			INNER JOIN llm_kb_agent_mappings m ON kb.id = m.kb_id
			WHERE m.account_id = $1
			  AND kb.status = 'active'
			  AND (
			      m.agent_id = $2
			      OR (m.agent_id = ANY($3::text[]) AND kb.id = ANY($4::text[]))
			  )
			ORDER BY kb.id, kb.name ASC`, accountId, ownName, pq.Array(inheritedNames), pq.Array(restrictToIds))
	}
	if err != nil {
		return "", nil, fmt.Errorf("LoadActiveAgentSkillContents: query failed: %w", err)
	}

	if len(rows) == 0 {
		return "", nil, nil
	}

	block, references := renderSkillsBlock(rows)
	return block, references, nil
}

// MapKBToAgent maps a knowledge base to an agent (creates junction table entry)
func MapKBToAgent(sc *security.RequestContext, accountId, kbId, agentId string) (*KBAgentMapping, error) {
	if accountId == "" {
		return nil, errors.New("account ID is required")
	}

	if kbId == "" {
		return nil, errors.New("knowledgebase ID is required")
	}

	if agentId == "" {
		return nil, errors.New("agent ID is required")
	}

	// Validate user has create/update access to account
	if !sc.GetSecurityContext().HasAccountAccess(accountId, security.SecurityAccessTypeCreate) {
		slog.Error("kb: user does not have create access to account", "account_id", accountId)
		return nil, errors.New("you don't have permission to access this resource")
	}

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		slog.Error("kb: failed to get database manager", "error", err)
		return nil, err
	}

	// First verify the KB exists and belongs to this account
	var kbExists bool
	err = dbms.Db.Get(&kbExists, `
		SELECT EXISTS(SELECT 1 FROM llm_knowledgebases WHERE id = $1 AND account_id = $2)`, kbId, accountId)
	if err != nil {
		slog.Error("kb: failed to check KB existence", "error", err)
		return nil, err
	}
	if !kbExists {
		return nil, errors.New("Knowledgebase not found or access denied")
	}

	// Insert mapping (or update if already exists)
	result, err := dbms.DoInTransaction(func(tx *sqlx.Tx) (any, error) {
		userId := sc.GetSecurityContext().GetUserId()
		_, err := tx.Exec(`
			INSERT INTO llm_kb_agent_mappings (kb_id, agent_id, account_id, created_by, updated_by, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $4, NOW(), NOW())
			ON CONFLICT (kb_id, agent_id) DO UPDATE
			SET updated_at = NOW(), updated_by = $5`,
			kbId, agentId, accountId, userId, userId)

		if err != nil {
			slog.Error("kb: failed to create agent mapping", "error", err, "kb_id", kbId, "agent_id", agentId)
			return nil, err
		}

		// Fetch the mapping
		var mapping KBAgentMapping
		err = tx.Get(&mapping, `
			SELECT m.kb_id, m.agent_id, m.account_id, m.created_at, m.updated_at,
			       COALESCE(cu.display_name, '') as created_by,
			       COALESCE(uu.display_name, '') as updated_by
			FROM llm_kb_agent_mappings m
			LEFT JOIN users cu ON m.created_by = cu.id
			LEFT JOIN users uu ON m.updated_by = uu.id
			WHERE m.kb_id = $1 AND m.agent_id = $2`, kbId, agentId)

		if err != nil {
			slog.Error("kb: failed to fetch mapping", "error", err, "kb_id", kbId, "agent_id", agentId)
			return nil, err
		}

		return mapping, nil
	})

	if err != nil {
		return nil, err
	}

	mapping := result.(KBAgentMapping)

	// Invalidate cache
	cacheKey := fmt.Sprintf("kb_mapping:%s:%s", accountId, agentId)
	if err := common.CacheDelete(CacheNamespaceLlmKbMapping, cacheKey); err != nil {
		slog.Error("kb: failed to invalidate cache", "error", err, "key", cacheKey)
	}

	// Create audit log
	go func() {
		auditReq := &audit.AuditRequest{
			Audits: []audit.Audit{
				{
					AccountId:     accountId,
					EventTime:     time.Now().UTC(),
					EventCategory: "knowledgebase",
					EventType:     "KB_MAP_TO_AGENT",
					EventState:    mapping,
					EventActor:    audit.EventActor(sc.GetSecurityContext().GetUserId()),
					EventTarget:   kbId,
					EventAction:   audit.EventActionUpdate,
					EventStatus:   audit.EventStatusSuccess,
					TransactionId: sc.GetTraceId(),
					EventAttr:     map[string]any{"kb_id": kbId, "agent_id": agentId},
					TenantId:      sc.GetSecurityContext().GetTenantId(),
					UserId:        sc.GetSecurityContext().GetUserId(),
				},
			},
		}
		_ = audit.CreateAudit(sc, auditReq)
	}()

	slog.Info("kb: successfully mapped KB to agent", "kb_id", kbId, "agent_id", agentId, "account_id", accountId)

	return &mapping, nil
}

// UnmapKBFromAgent removes a specific agent mapping from a knowledge base
func UnmapKBFromAgent(sc *security.RequestContext, accountId, kbId, agentId string) error {
	if accountId == "" {
		return errors.New("account ID is required")
	}

	if kbId == "" {
		return errors.New("knowledgebase ID is required")
	}

	if agentId == "" {
		return errors.New("agent ID is required")
	}

	// Validate user has create/update access to account
	if !sc.GetSecurityContext().HasAccountAccess(accountId, security.SecurityAccessTypeCreate) {
		slog.Error("kb: user does not have create access to account", "account_id", accountId)
		return errors.New("you don't have permission to access this resource")
	}

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		slog.Error("kb: failed to get database manager", "error", err)
		return err
	}

	// Delete the mapping
	_, err = dbms.DoInTransaction(func(tx *sqlx.Tx) (any, error) {
		result, err := tx.Exec(`
			DELETE FROM llm_kb_agent_mappings
			WHERE kb_id = $1 AND agent_id = $2 AND account_id = $3`,
			kbId, agentId, accountId)

		if err != nil {
			slog.Error("kb: failed to delete agent mapping", "error", err, "kb_id", kbId, "agent_id", agentId)
			return nil, err
		}

		rowsAffected, _ := result.RowsAffected()
		if rowsAffected == 0 {
			return nil, errors.New("Knowledgebase mapping not found")
		}

		return nil, nil
	})

	if err != nil {
		return err
	}

	// Invalidate cache
	cacheKey := fmt.Sprintf("kb_mapping:%s:%s", accountId, agentId)
	if err := common.CacheDelete(CacheNamespaceLlmKbMapping, cacheKey); err != nil {
		slog.Error("kb: failed to invalidate cache", "error", err, "key", cacheKey)
	}

	// Create audit log
	go func() {
		auditReq := &audit.AuditRequest{
			Audits: []audit.Audit{
				{
					AccountId:     accountId,
					EventTime:     time.Now().UTC(),
					EventCategory: "knowledgebase",
					EventType:     "KB_UNMAP_FROM_AGENT",
					EventState:    map[string]any{"kb_id": kbId, "agent_id": agentId},
					EventActor:    audit.EventActor(sc.GetSecurityContext().GetUserId()),
					EventTarget:   kbId,
					EventAction:   audit.EventActionDelete,
					EventStatus:   audit.EventStatusSuccess,
					TransactionId: sc.GetTraceId(),
					EventAttr:     map[string]any{"kb_id": kbId, "agent_id": agentId},
					TenantId:      sc.GetSecurityContext().GetTenantId(),
					UserId:        sc.GetSecurityContext().GetUserId(),
				},
			},
		}
		_ = audit.CreateAudit(sc, auditReq)
	}()

	slog.Info("kb: successfully unmapped KB from agent", "kb_id", kbId, "agent_id", agentId, "account_id", accountId)

	return nil
}

// AgentKBSummary represents an agent with its KB mapping count
type AgentKBSummary struct {
	AgentId string `json:"agent_id" db:"agent_id"`
	KBCount int    `json:"kb_count" db:"kb_count"`
}

// ListAgentsWithKBCounts returns all agents and their KB mapping counts for an account
func ListAgentsWithKBCounts(sc *security.RequestContext, accountId string) ([]AgentKBSummary, error) {
	if accountId == "" {
		return nil, errors.New("account ID is required")
	}

	// Validate user has read access to account
	if !sc.GetSecurityContext().HasAccountAccess(accountId, security.SecurityAccessTypeRead) {
		slog.Error("kb: user does not have read access to account", "account_id", accountId)
		return nil, errors.New("you don't have permission to access this resource")
	}

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		slog.Error("kb: failed to get database manager", "error", err)
		return nil, err
	}

	var agentSummaries []AgentKBSummary
	err = dbms.Db.Select(&agentSummaries, `
		SELECT agent_id, COUNT(*) as kb_count
		FROM llm_kb_agent_mappings
		WHERE account_id = $1
		GROUP BY agent_id
		ORDER BY agent_id`, accountId)

	if err != nil {
		slog.Error("kb: failed to query agent KB counts", "error", err, "account_id", accountId)
		return nil, err
	}

	return agentSummaries, nil
}

// KBAgentMappingDetail represents a KB-agent mapping with full details
type KBAgentMappingDetail struct {
	KbId      string    `json:"kb_id" db:"kb_id"`
	KbName    string    `json:"kb_name" db:"kb_name"`
	AgentId   string    `json:"agent_id" db:"agent_id"`
	AccountId string    `json:"account_id" db:"account_id"`
	CreatedBy string    `json:"created_by,omitempty" db:"created_by"` // Display name, not UUID
	UpdatedBy string    `json:"updated_by,omitempty" db:"updated_by"` // Display name, not UUID
	CreatedAt time.Time `json:"created_at" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
}

// ListKBAgentMappings returns all KB-agent mappings for an account
func ListKBAgentMappings(sc *security.RequestContext, accountId string) ([]KBAgentMappingDetail, error) {
	if accountId == "" {
		return nil, errors.New("account ID is required")
	}

	// Validate user has read access to account
	if !sc.GetSecurityContext().HasAccountAccess(accountId, security.SecurityAccessTypeRead) {
		slog.Error("kb: user does not have read access to account", "account_id", accountId)
		return nil, errors.New("you don't have permission to access this resource")
	}

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		slog.Error("kb: failed to get database manager", "error", err)
		return nil, err
	}

	var mappings []KBAgentMappingDetail
	err = dbms.Db.Select(&mappings, `
		SELECT m.kb_id, kb.name as kb_name, m.agent_id, m.account_id,
		       m.created_at, m.updated_at,
		       COALESCE(cu.display_name, '') as created_by,
		       COALESCE(uu.display_name, '') as updated_by
		FROM llm_kb_agent_mappings m
		INNER JOIN llm_knowledgebases kb ON m.kb_id = kb.id
		LEFT JOIN users cu ON m.created_by = cu.id
		LEFT JOIN users uu ON m.updated_by = uu.id
		WHERE m.account_id = $1
		ORDER BY kb.name, m.agent_id`, accountId)

	if err != nil {
		slog.Error("kb: failed to query KB-agent mappings", "error", err, "account_id", accountId)
		return nil, err
	}

	return mappings, nil
}

// ListKBAgents returns all agents that a specific KB is mapped to
func ListKBAgents(sc *security.RequestContext, accountId, kbId string) ([]string, error) {
	if accountId == "" {
		return nil, errors.New("account ID is required")
	}

	if kbId == "" {
		return nil, errors.New("knowledgebase ID is required")
	}

	// Validate user has read access to account
	if !sc.GetSecurityContext().HasAccountAccess(accountId, security.SecurityAccessTypeRead) {
		slog.Error("kb: user does not have read access to account", "account_id", accountId)
		return nil, errors.New("you don't have permission to access this resource")
	}

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		slog.Error("kb: failed to get database manager", "error", err)
		return nil, err
	}

	// First verify the KB exists and belongs to this account
	var kbExists bool
	err = dbms.Db.Get(&kbExists, `
		SELECT EXISTS(SELECT 1 FROM llm_knowledgebases WHERE id = $1 AND account_id = $2)`, kbId, accountId)
	if err != nil {
		slog.Error("kb: failed to check KB existence", "error", err)
		return nil, err
	}
	if !kbExists {
		return nil, errors.New("Knowledgebase not found or access denied")
	}

	var agentIds []string
	err = dbms.Db.Select(&agentIds, `
		SELECT agent_id
		FROM llm_kb_agent_mappings
		WHERE kb_id = $1 AND account_id = $2
		ORDER BY agent_id`, kbId, accountId)

	if err != nil {
		slog.Error("kb: failed to query KB agents", "error", err, "kb_id", kbId)
		return nil, err
	}

	return agentIds, nil
}
