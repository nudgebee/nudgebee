package core

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"nudgebee/llm/audit"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// GlobalContext represents an account-scoped global context file
type GlobalContext struct {
	Id            string    `json:"id" db:"id"`
	TenantId      string    `json:"tenant_id" db:"tenant_id"`
	AccountId     string    `json:"account_id" db:"account_id"`
	Name          string    `json:"name" db:"name"`
	Description   string    `json:"description,omitempty" db:"description"`
	Data          string    `json:"data,omitempty" db:"data"` // Omit in list responses
	DataFormat    string    `json:"data_format" db:"data_format"`
	DataFilename  string    `json:"data_filename" db:"data_filename"`
	DataSizeBytes int64     `json:"data_size_bytes" db:"data_size_bytes"`
	Status        string    `json:"status" db:"status"`
	CreatedBy     string    `json:"created_by,omitempty" db:"created_by"` // Display name, not UUID
	UpdatedBy     string    `json:"updated_by,omitempty" db:"updated_by"` // Display name, not UUID
	CreatedAt     time.Time `json:"created_at" db:"created_at"`
	UpdatedAt     time.Time `json:"updated_at" db:"updated_at"`
}

// GlobalContextStatus represents the processing status
type GlobalContextStatus string

const (
	GCStatusActive     GlobalContextStatus = "active"
	GCStatusProcessing GlobalContextStatus = "processing"
	GCStatusError      GlobalContextStatus = "error"
	GCStatusArchived   GlobalContextStatus = "archived"
)

// GlobalContextFormat represents supported file formats
type GlobalContextFormat string

const (
	GCFormatJSON GlobalContextFormat = "json"
	GCFormatXML  GlobalContextFormat = "xml"
	GCFormatCSV  GlobalContextFormat = "csv"
	GCFormatText GlobalContextFormat = "text"
)

// Error constants
const (
	errGCTenantIDRequired        = "tenant ID is required"
	errGCAccountIDRequired       = "account ID is required"
	errGCNameRequired            = "please provide a name for the global context"
	errGCNameInvalid             = "global context name contains invalid characters. Use only letters, numbers, spaces, hyphens, and underscores"
	errGCIDRequired              = "global context ID is required"
	errGCDataRequired            = "please provide content for the global context"
	errGCFormatRequired          = "file format is required"
	errGCFormatInvalid           = "unsupported file format. Please use JSON, XML, CSV, or Text"
	errGCFilenameRequired        = "filename is required"
	errGCTokenLimitExceeded      = "content exceeds the maximum limit of 1024 tokens. Please reduce the content size"
	errGCAlreadyExistsForAccount = "this account already has a global context. Only one global context is allowed per account"
	errGCAlreadyExists           = "a global context with this name already exists"
	errGCNotFound                = "global context not found"
	errGCUnauthorized            = "you don't have permission to access this global context. Admin access required"
)

// Log messages
const (
	logGCFailedDBManager = "gc: failed to get database manager"
	logGCFailedQuery     = "gc: failed to execute query"
)

// estimateTokenCount provides a simple token count estimation
// Uses word-based approximation: tokens ≈ words * 1.3
// This is a conservative estimate that works well for most text
func estimateTokenCount(text string) int {
	// Count words by splitting on whitespace
	wordCount := 0
	inWord := false

	for _, r := range text {
		if unicode.IsSpace(r) {
			inWord = false
		} else if !inWord {
			wordCount++
			inWord = true
		}
	}

	// Apply 1.3x multiplier for token estimation
	// Round up to be conservative
	return int(float64(wordCount) * 1.3)
}

// ValidateGCFormat checks if the format is supported
func ValidateGCFormat(format string) error {
	validFormats := map[string]bool{
		string(GCFormatJSON): true,
		string(GCFormatXML):  true,
		string(GCFormatCSV):  true,
		string(GCFormatText): true,
	}

	if !validFormats[format] {
		return errors.New(errGCFormatInvalid)
	}
	return nil
}

// CreateGlobalContext creates a new global context
func CreateGlobalContext(sc *security.RequestContext, tenantId string, gc GlobalContext) (GlobalContext, error) {
	// Validation
	if tenantId == "" {
		tenantId = sc.GetSecurityContext().GetTenantId()
		if tenantId == "" {
			return GlobalContext{}, errors.New(errGCTenantIDRequired)
		}
	}

	if gc.AccountId == "" {
		return GlobalContext{}, errors.New(errGCAccountIDRequired)
	}

	gc.Name = strings.TrimSpace(gc.Name)
	if gc.Name == "" {
		return GlobalContext{}, errors.New(errGCNameRequired)
	}

	if !common.IsValidName(gc.Name) {
		return GlobalContext{}, errors.New(errGCNameInvalid)
	}

	if gc.Data == "" {
		return GlobalContext{}, errors.New(errGCDataRequired)
	}

	if gc.DataFormat == "" {
		return GlobalContext{}, errors.New(errGCFormatRequired)
	}

	if err := ValidateGCFormat(gc.DataFormat); err != nil {
		return GlobalContext{}, err
	}

	if gc.DataFilename == "" {
		return GlobalContext{}, errors.New(errGCFilenameRequired)
	}

	// Calculate data size for informational purposes (stored but not validated)
	gc.DataSizeBytes = int64(len(gc.Data))

	// Estimate token count and validate against limit (1024 tokens max)
	tokenCount := estimateTokenCount(gc.Data)
	if tokenCount > config.Config.LlmServerMaxGCTokens {
		return GlobalContext{}, fmt.Errorf("%s: estimated %d tokens (max: %d)", errGCTokenLimitExceeded, tokenCount, config.Config.LlmServerMaxGCTokens)
	}

	// Check if user has account-level create access
	if !sc.GetSecurityContext().HasAccountAccess(gc.AccountId, security.SecurityAccessTypeCreate) {
		slog.Error("gc: user does not have account access", "account_id", gc.AccountId)
		return GlobalContext{}, errors.New(errGCUnauthorized)
	}

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		slog.Error(logGCFailedDBManager, "error", err)
		return GlobalContext{}, err
	}

	// Set defaults
	gc.Id = uuid.NewString()
	gc.TenantId = tenantId
	gc.Status = string(GCStatusActive)
	gc.CreatedBy = sc.GetSecurityContext().GetUserId()
	gc.UpdatedBy = sc.GetSecurityContext().GetUserId()

	nullableCreatedBy := sql.NullString{String: gc.CreatedBy, Valid: gc.CreatedBy != ""}
	nullableUpdatedBy := sql.NullString{String: gc.UpdatedBy, Valid: gc.UpdatedBy != ""}
	nullableDescription := sql.NullString{String: gc.Description, Valid: gc.Description != ""}

	// Insert into database
	gcAny, err := dbms.DoInTransaction(func(tx *sqlx.Tx) (any, error) {
		// Check if ANY GC already exists for this account (only one allowed per account)
		// This check is inside the transaction to prevent race conditions
		var exists bool
		err = tx.Get(&exists, "SELECT EXISTS(SELECT 1 FROM llm_global_contexts WHERE account_id = $1)", gc.AccountId)
		if err != nil {
			slog.Error("gc: failed to check existence", "error", err)
			return GlobalContext{}, err
		}
		if exists {
			return GlobalContext{}, errors.New(errGCAlreadyExistsForAccount)
		}

		_, err = tx.Exec(`
			INSERT INTO llm_global_contexts
			(id, tenant_id, account_id, name, description, data, data_format, data_filename, data_size_bytes, status, created_by, updated_by, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, NOW(), NOW())`,
			gc.Id, gc.TenantId, gc.AccountId, gc.Name, nullableDescription, gc.Data,
			gc.DataFormat, gc.DataFilename, gc.DataSizeBytes, gc.Status, nullableCreatedBy, nullableUpdatedBy)
		if err != nil {
			slog.Error("gc: failed to insert", "error", err)
			return GlobalContext{}, err
		}

		// Fetch the created GC with usernames
		var createdGC GlobalContext
		err = tx.Get(&createdGC, `
			SELECT gc.id, gc.tenant_id, gc.account_id, gc.name,
			       COALESCE(gc.description, '') as description,
			       gc.data, gc.data_format, gc.data_filename, gc.data_size_bytes,
			       gc.status,
			       COALESCE(cu.display_name, '') as created_by,
			       COALESCE(uu.display_name, '') as updated_by,
			       gc.created_at, gc.updated_at
			FROM llm_global_contexts gc
			LEFT JOIN users cu ON gc.created_by = cu.id
			LEFT JOIN users uu ON gc.updated_by = uu.id
			WHERE gc.id = $1`, gc.Id)
		if err != nil {
			slog.Error("gc: failed to fetch created GC", "error", err)
			return GlobalContext{}, err
		}
		return createdGC, nil
	})

	if err != nil {
		return GlobalContext{}, err
	}

	createdGC := gcAny.(GlobalContext)

	// Create audit entry
	auditReq := &audit.AuditRequest{
		Audits: []audit.Audit{
			{
				AccountId:     gc.AccountId,
				EventTime:     time.Now().UTC(),
				EventCategory: "global_context",
				EventType:     "GC_CREATE",
				EventState:    createdGC,
				EventActor:    audit.EventActor(sc.GetSecurityContext().GetUserId()),
				EventTarget:   createdGC.Id,
				EventAction:   audit.EventActionCreate,
				EventStatus:   audit.EventStatusSuccess,
				TransactionId: sc.GetTraceId(),
				EventAttr:     map[string]any{"gc_id": createdGC.Id, "gc_name": createdGC.Name, "account_id": gc.AccountId},
				TenantId:      tenantId,
				UserId:        sc.GetSecurityContext().GetUserId(),
			},
		},
	}
	auditErr := audit.CreateAudit(sc, auditReq)
	if auditErr != nil {
		slog.Error("gc: failed to create audit entry", "error", auditErr, "gc_id", createdGC.Id)
	}

	// Don't return data in response
	createdGC.Data = ""
	return createdGC, nil
}

// GetGlobalContext retrieves a single global context by ID with full data
func GetGlobalContext(sc *security.RequestContext, tenantId, accountId, gcId string) (GlobalContext, error) {
	if tenantId == "" {
		tenantId = sc.GetSecurityContext().GetTenantId()
		if tenantId == "" {
			return GlobalContext{}, errors.New(errGCTenantIDRequired)
		}
	}
	if accountId == "" {
		return GlobalContext{}, errors.New(errGCAccountIDRequired)
	}
	if gcId == "" {
		return GlobalContext{}, errors.New(errGCIDRequired)
	}

	// Check if user has account-level read access
	if !sc.GetSecurityContext().HasAccountAccess(accountId, security.SecurityAccessTypeRead) {
		return GlobalContext{}, errors.New(errGCUnauthorized)
	}

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		slog.Error(logGCFailedDBManager, "error", err)
		return GlobalContext{}, err
	}

	var gc GlobalContext
	err = dbms.Db.Get(&gc, `
		SELECT gc.id, gc.tenant_id, gc.account_id, gc.name,
		       COALESCE(gc.description, '') as description,
		       gc.data, gc.data_format, gc.data_filename, gc.data_size_bytes,
		       gc.status,
		       COALESCE(cu.display_name, '') as created_by,
		       COALESCE(uu.display_name, '') as updated_by,
		       gc.created_at, gc.updated_at
		FROM llm_global_contexts gc
		LEFT JOIN users cu ON gc.created_by = cu.id
		LEFT JOIN users uu ON gc.updated_by = uu.id
		WHERE gc.id = $1 AND gc.account_id = $2`, gcId, accountId)
	if err != nil {
		if err == sql.ErrNoRows {
			return GlobalContext{}, errors.New(errGCNotFound)
		}
		slog.Error(logGCFailedQuery, "error", err, "gc_id", gcId)
		return GlobalContext{}, err
	}

	return gc, nil
}

// ListGlobalContexts retrieves all global contexts for an account (without data)
func ListGlobalContexts(sc *security.RequestContext, tenantId, accountId string) ([]GlobalContext, error) {
	if tenantId == "" {
		tenantId = sc.GetSecurityContext().GetTenantId()
		if tenantId == "" {
			return nil, errors.New(errGCTenantIDRequired)
		}
	}
	if accountId == "" {
		return nil, errors.New(errGCAccountIDRequired)
	}

	// Check if user has account-level read access
	if !sc.GetSecurityContext().HasAccountAccess(accountId, security.SecurityAccessTypeRead) {
		return nil, errors.New(errGCUnauthorized)
	}

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		slog.Error(logGCFailedDBManager, "error", err)
		return nil, err
	}

	var gcs []GlobalContext
	// Note: Excluding data field for performance - only fetch it in GetGlobalContext
	err = dbms.Db.Select(&gcs, `
		SELECT gc.id, gc.tenant_id, gc.account_id, gc.name,
		       COALESCE(gc.description, '') as description,
		       gc.data_format, gc.data_filename,
		       gc.data_size_bytes, gc.status,
		       COALESCE(cu.display_name, '') as created_by,
		       COALESCE(uu.display_name, '') as updated_by,
		       gc.created_at, gc.updated_at
		FROM llm_global_contexts gc
		LEFT JOIN users cu ON gc.created_by = cu.id
		LEFT JOIN users uu ON gc.updated_by = uu.id
		WHERE gc.account_id = $1
		ORDER BY gc.created_at DESC`, accountId)
	if err != nil {
		slog.Error(logGCFailedQuery, "error", err, "account_id", accountId)
		return nil, err
	}

	return gcs, nil
}

// UpdateGlobalContext updates an existing global context
func UpdateGlobalContext(sc *security.RequestContext, tenantId, accountId, gcId string, updates GlobalContext) error {
	if tenantId == "" {
		tenantId = sc.GetSecurityContext().GetTenantId()
		if tenantId == "" {
			return errors.New(errGCTenantIDRequired)
		}
	}
	if accountId == "" {
		return errors.New(errGCAccountIDRequired)
	}
	if gcId == "" {
		return errors.New(errGCIDRequired)
	}

	// Check if user has account-level update access
	if !sc.GetSecurityContext().HasAccountAccess(accountId, security.SecurityAccessTypeUpdate) {
		return errors.New(errGCUnauthorized)
	}

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		slog.Error(logGCFailedDBManager, "error", err)
		return err
	}

	// Check if GC exists
	var existingGC GlobalContext
	err = dbms.Db.Get(&existingGC, `
		SELECT gc.id, gc.tenant_id, gc.account_id, gc.name,
		       COALESCE(gc.description, '') as description,
		       gc.data, gc.data_format, gc.data_filename, gc.data_size_bytes,
		       gc.status,
		       gc.created_at, gc.updated_at,
		       COALESCE(cu.display_name, '') as created_by,
		       COALESCE(uu.display_name, '') as updated_by
		FROM llm_global_contexts gc
		LEFT JOIN users cu ON gc.created_by = cu.id
		LEFT JOIN users uu ON gc.updated_by = uu.id
		WHERE gc.id = $1 AND gc.account_id = $2`, gcId, accountId)
	if err != nil {
		if err == sql.ErrNoRows {
			return errors.New(errGCNotFound)
		}
		return err
	}

	// Validate name if being changed
	if updates.Name != "" && updates.Name != existingGC.Name {
		updates.Name = strings.TrimSpace(updates.Name)
		if !common.IsValidName(updates.Name) {
			return errors.New(errGCNameInvalid)
		}
	} else {
		updates.Name = existingGC.Name
	}

	// Handle data updates
	if updates.Data != "" && updates.Data != existingGC.Data {
		if updates.DataFormat != "" {
			if err := ValidateGCFormat(updates.DataFormat); err != nil {
				return err
			}
		} else {
			updates.DataFormat = existingGC.DataFormat
		}

		// Calculate data size for informational purposes (stored but not validated)
		updates.DataSizeBytes = int64(len(updates.Data))

		// Estimate token count and validate against limit (1024 tokens max)
		tokenCount := estimateTokenCount(updates.Data)
		if tokenCount > config.Config.LlmServerMaxGCTokens {
			return fmt.Errorf("%s: estimated %d tokens (max: %d)", errGCTokenLimitExceeded, tokenCount, config.Config.LlmServerMaxGCTokens)
		}
	} else {
		updates.Data = existingGC.Data
		updates.DataFormat = existingGC.DataFormat
		updates.DataFilename = existingGC.DataFilename
		updates.DataSizeBytes = existingGC.DataSizeBytes
	}

	if updates.Description == "" {
		updates.Description = existingGC.Description
	}

	updates.UpdatedBy = sc.GetSecurityContext().GetUserId()
	nullableUpdatedBy := sql.NullString{String: updates.UpdatedBy, Valid: updates.UpdatedBy != ""}
	nullableDescription := sql.NullString{String: updates.Description, Valid: updates.Description != ""}

	// Update database
	_, err = dbms.DoInTransaction(func(tx *sqlx.Tx) (any, error) {
		_, err = tx.Exec(`
			UPDATE llm_global_contexts
			SET name = $1, description = $2, data = $3, data_format = $4, data_filename = $5,
			    data_size_bytes = $6, updated_by = $7, updated_at = NOW()
			WHERE id = $8 AND account_id = $9`,
			updates.Name, nullableDescription, updates.Data, updates.DataFormat, updates.DataFilename,
			updates.DataSizeBytes, nullableUpdatedBy, gcId, accountId)
		if err != nil {
			slog.Error("gc: failed to update", "error", err, "gc_id", gcId)
			return nil, err
		}
		return nil, nil
	})

	if err != nil {
		return err
	}

	// Create audit entry
	auditReq := &audit.AuditRequest{
		Audits: []audit.Audit{
			{
				AccountId:     accountId,
				EventTime:     time.Now().UTC(),
				EventCategory: "global_context",
				EventType:     "GC_UPDATE",
				EventState:    updates,
				EventActor:    audit.EventActor(sc.GetSecurityContext().GetUserId()),
				EventTarget:   gcId,
				EventAction:   audit.EventActionUpdate,
				EventStatus:   audit.EventStatusSuccess,
				TransactionId: sc.GetTraceId(),
				EventAttr:     map[string]any{"gc_id": gcId, "gc_name": updates.Name, "account_id": accountId},
				TenantId:      tenantId,
				UserId:        sc.GetSecurityContext().GetUserId(),
			},
		},
	}
	auditErr := audit.CreateAudit(sc, auditReq)
	if auditErr != nil {
		slog.Error("gc: failed to create audit entry", "error", auditErr, "gc_id", gcId)
	}

	return nil
}

// DeleteGlobalContext deletes a global context
func DeleteGlobalContext(sc *security.RequestContext, tenantId, accountId, gcId string) error {
	if tenantId == "" {
		tenantId = sc.GetSecurityContext().GetTenantId()
		if tenantId == "" {
			return errors.New(errGCTenantIDRequired)
		}
	}
	if accountId == "" {
		return errors.New(errGCAccountIDRequired)
	}
	if gcId == "" {
		return errors.New(errGCIDRequired)
	}

	// Check if user has account-level delete access
	if !sc.GetSecurityContext().HasAccountAccess(accountId, security.SecurityAccessTypeDelete) {
		return errors.New(errGCUnauthorized)
	}

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		slog.Error(logGCFailedDBManager, "error", err)
		return err
	}

	// Get GC details for audit before deletion
	var gc GlobalContext
	err = dbms.Db.Get(&gc, "SELECT id, name, tenant_id, account_id FROM llm_global_contexts WHERE id = $1 AND account_id = $2", gcId, accountId)
	if err != nil {
		if err == sql.ErrNoRows {
			return errors.New(errGCNotFound)
		}
		return err
	}

	// Delete from database
	_, err = dbms.DoInTransaction(func(tx *sqlx.Tx) (any, error) {
		result, err := tx.Exec("DELETE FROM llm_global_contexts WHERE id = $1 AND account_id = $2", gcId, accountId)
		if err != nil {
			slog.Error("gc: failed to delete", "error", err, "gc_id", gcId)
			return nil, err
		}
		rowsAffected, _ := result.RowsAffected()
		if rowsAffected == 0 {
			return nil, errors.New(errGCNotFound)
		}
		return nil, nil
	})

	if err != nil {
		return err
	}

	// Create audit entry
	auditReq := &audit.AuditRequest{
		Audits: []audit.Audit{
			{
				AccountId:     accountId,
				EventTime:     time.Now().UTC(),
				EventCategory: "global_context",
				EventType:     "GC_DELETE",
				EventState:    gc,
				EventActor:    audit.EventActor(sc.GetSecurityContext().GetUserId()),
				EventTarget:   gcId,
				EventAction:   audit.EventActionDelete,
				EventStatus:   audit.EventStatusSuccess,
				TransactionId: sc.GetTraceId(),
				EventAttr:     map[string]any{"gc_id": gcId, "gc_name": gc.Name, "account_id": accountId},
				TenantId:      tenantId,
				UserId:        sc.GetSecurityContext().GetUserId(),
			},
		},
	}
	auditErr := audit.CreateAudit(sc, auditReq)
	if auditErr != nil {
		slog.Error("gc: failed to create audit entry", "error", auditErr, "gc_id", gcId)
	}

	return nil
}
