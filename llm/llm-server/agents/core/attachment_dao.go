package core

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"nudgebee/llm/common"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// ConversationAttachment represents an attachment joined with its ref for a specific message.
type ConversationAttachment struct {
	ID             uuid.UUID `json:"id" db:"id"`
	MessageID      uuid.UUID `json:"message_id" db:"message_id"`
	ConversationID uuid.UUID `json:"conversation_id" db:"conversation_id"`
	AccountID      uuid.UUID `json:"account_id" db:"account_id"`
	ContentHash    string    `json:"content_hash" db:"content_hash"`
	MIMEType       string    `json:"mime_type" db:"mime_type"`
	SizeBytes      int       `json:"size_bytes" db:"size_bytes"`
	Data           string    `json:"data,omitempty" db:"data"`
	SourceURL      *string   `json:"source_url,omitempty" db:"source_url"`
	Description    *string   `json:"description,omitempty" db:"description"`
	CreatedAt      time.Time `json:"created_at" db:"created_at"`
}

// AttachmentDescription is a lightweight projection for history replay.
type AttachmentDescription struct {
	ID          uuid.UUID `json:"id" db:"id"`
	MessageID   uuid.UUID `json:"message_id" db:"message_id"`
	MIMEType    string    `json:"mime_type" db:"mime_type"`
	Description *string   `json:"description,omitempty" db:"description"`
}

// IAttachmentDAO defines the interface for attachment persistence operations.
type IAttachmentDAO interface {
	SaveAttachments(messageID, conversationID, accountID string, images []ImageAttachment) ([]uuid.UUID, error)
	LoadAttachments(messageID, accountID string) ([]ConversationAttachment, error)
	LoadAttachmentDescriptions(messageIDs []string, accountID string) (map[string][]AttachmentDescription, error)
	UpdateAttachmentDescription(attachmentID, accountID, description string) error
	PurgeExpiredAttachments(retentionDays int) (int64, error)
}

// AttachmentDAO implements IAttachmentDAO using PostgreSQL.
type AttachmentDAO struct {
	dbManager *common.DatabaseManager
}

var (
	attachmentDAO      IAttachmentDAO
	attachmentDAOMutex sync.Mutex
)

// GetAttachmentDAO returns the singleton AttachmentDAO instance.
func GetAttachmentDAO() IAttachmentDAO {
	attachmentDAOMutex.Lock()
	defer attachmentDAOMutex.Unlock()

	if attachmentDAO != nil {
		return attachmentDAO
	}

	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		slog.Error("attachment: unable to get database manager", "error", err)
		return nil
	}

	attachmentDAO = &AttachmentDAO{
		dbManager: dbManager,
	}

	return attachmentDAO
}

// SetAttachmentDAO replaces the singleton (for testing).
func SetAttachmentDAO(dao IAttachmentDAO) {
	attachmentDAOMutex.Lock()
	defer attachmentDAOMutex.Unlock()
	attachmentDAO = dao
}

// computeContentHash returns a hex-encoded SHA-256 hash of the given data.
func computeContentHash(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// SaveAttachments persists image attachments and links them to the message via the refs table.
// Deduplicates image data by (account_id, content_hash). Every message that references
// the same image gets its own row in llm_conversation_attachment_refs.
func (dao *AttachmentDAO) SaveAttachments(messageID, conversationID, accountID string, images []ImageAttachment) ([]uuid.UUID, error) {
	if len(images) == 0 {
		return nil, nil
	}

	start := time.Now()

	// Upsert returns the existing id on conflict so we always get the attachment id
	upsertQuery := `
		INSERT INTO llm_conversation_attachments
			(id, account_id, content_hash, mime_type, size_bytes, data, source_url)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (account_id, content_hash) DO UPDATE SET last_used_at = now()
		RETURNING id`

	refQuery := `
		INSERT INTO llm_conversation_attachment_refs
			(attachment_id, message_id, conversation_id)
		VALUES ($1, $2, $3)
		ON CONFLICT (attachment_id, message_id) DO NOTHING`

	tx, err := dao.dbManager.Db.Begin()
	if err != nil {
		return nil, fmt.Errorf("attachment: failed to begin transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	var ids []uuid.UUID

	for _, img := range images {
		id := common.GenerateUUID()

		var data string
		var sourceURL *string
		var contentHash string
		var sizeBytes int

		if img.Data != "" {
			data = stripDataURIPrefix(img.Data)
			if decoded, err := decodeBase64(data); err == nil {
				sizeBytes = len(decoded)
			} else {
				sizeBytes = len(data) // fallback for non-standard encoding
			}
			contentHash = computeContentHash([]byte(data))
		} else if img.URL != "" {
			url := img.URL
			sourceURL = &url
			contentHash = computeContentHash([]byte(img.URL))
		}

		// Upsert attachment — always returns the attachment id (new or existing)
		var attachmentID uuid.UUID
		err = tx.QueryRow(upsertQuery,
			id, accountID, contentHash, img.MIMEType, sizeBytes, data, sourceURL,
		).Scan(&attachmentID)
		if err != nil {
			slog.Error("attachment: failed to upsert attachment",
				"error", err,
				"message_id", messageID,
			)
			return nil, fmt.Errorf("attachment: failed to upsert: %w", err)
		}

		// Link attachment to this message
		_, err = tx.Exec(refQuery, attachmentID, messageID, conversationID)
		if err != nil {
			slog.Error("attachment: failed to insert ref",
				"error", err,
				"attachment_id", attachmentID,
				"message_id", messageID,
			)
			return nil, fmt.Errorf("attachment: failed to insert ref: %w", err)
		}

		ids = append(ids, attachmentID)
	}

	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("attachment: failed to commit transaction: %w", err)
	}

	slog.Info("attachment: saved attachments",
		"message_id", messageID,
		"count", len(ids),
		"duration_ms", time.Since(start).Milliseconds(),
	)

	return ids, nil
}

// LoadAttachments retrieves all attachments linked to a given message via the refs table.
func (dao *AttachmentDAO) LoadAttachments(messageID, accountID string) ([]ConversationAttachment, error) {
	query := `
		SELECT a.id, r.message_id, r.conversation_id, a.account_id, a.content_hash,
		       a.mime_type, a.size_bytes, a.data, a.source_url, a.description, a.created_at
		FROM llm_conversation_attachment_refs r
		JOIN llm_conversation_attachments a ON a.id = r.attachment_id
		WHERE r.message_id = $1 AND a.account_id = $2
		ORDER BY a.created_at ASC`

	var attachments []ConversationAttachment
	err := dao.dbManager.QueryAndScan(&attachments, query, messageID, accountID)
	if err != nil {
		return nil, fmt.Errorf("attachment: failed to load attachments: %w", err)
	}

	return attachments, nil
}

// LoadAttachmentDescriptions retrieves lightweight attachment metadata (id, message_id, mime_type, description)
// for the given message IDs via the refs table. Returns a map keyed by message_id string.
func (dao *AttachmentDAO) LoadAttachmentDescriptions(messageIDs []string, accountID string) (map[string][]AttachmentDescription, error) {
	if len(messageIDs) == 0 {
		return nil, nil
	}

	query := `
		SELECT a.id, r.message_id, a.mime_type, a.description
		FROM llm_conversation_attachment_refs r
		JOIN llm_conversation_attachments a ON a.id = r.attachment_id
		WHERE a.account_id = $1 AND r.message_id = ANY($2::uuid[])
		ORDER BY a.created_at ASC`

	var descriptions []AttachmentDescription
	err := dao.dbManager.QueryAndScan(&descriptions, query, accountID, pq.Array(messageIDs))
	if err != nil {
		return nil, fmt.Errorf("attachment: failed to load descriptions: %w", err)
	}

	result := make(map[string][]AttachmentDescription, len(messageIDs))
	for _, d := range descriptions {
		key := d.MessageID.String()
		result[key] = append(result[key], d)
	}

	return result, nil
}

// UpdateAttachmentDescription sets the description for a specific attachment, scoped by account.
func (dao *AttachmentDAO) UpdateAttachmentDescription(attachmentID, accountID, description string) error {
	query := `
		UPDATE llm_conversation_attachments
		SET description = $2
		WHERE id = $1 AND account_id = $3`

	_, err := dao.dbManager.Db.Exec(query, attachmentID, description, accountID)
	if err != nil {
		return fmt.Errorf("attachment: failed to update description: %w", err)
	}

	return nil
}

// PurgeExpiredAttachments nullifies the data column for attachments older than retentionDays.
// Preserves the row, description, and metadata for history context.
func (dao *AttachmentDAO) PurgeExpiredAttachments(retentionDays int) (int64, error) {
	query := `
		UPDATE llm_conversation_attachments
		SET data = NULL
		WHERE last_used_at < NOW() - ($1::int * INTERVAL '1 day')
		  AND data IS NOT NULL`

	result, err := dao.dbManager.Db.Exec(query, retentionDays)
	if err != nil {
		return 0, fmt.Errorf("attachment: failed to purge expired attachments: %w", err)
	}

	count, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("attachment: failed to get purge count: %w", err)
	}

	return count, nil
}
