package core

import (
	"testing"
	"time"

	"nudgebee/llm/common"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupAttachmentDAOMock(t *testing.T) (*AttachmentDAO, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	dao := &AttachmentDAO{
		dbManager: &common.DatabaseManager{
			Db: sqlx.NewDb(db, "postgresql"),
		},
	}
	return dao, mock
}

func TestSaveAttachments_WithBase64Data(t *testing.T) {
	dao, mock := setupAttachmentDAOMock(t)

	messageID := uuid.New().String()
	conversationID := uuid.New().String()
	accountID := uuid.New().String()
	insertedID := uuid.New()

	images := []ImageAttachment{
		{Data: "iVBORw0KGgo=", MIMEType: "image/png"},
	}

	mock.ExpectBegin()

	// Expect upsert into attachments — sizeBytes is decoded length (8 bytes for PNG header)
	rows := sqlmock.NewRows([]string{"id"}).AddRow(insertedID)
	mock.ExpectQuery(`INSERT INTO llm_conversation_attachments`).
		WithArgs(
			sqlmock.AnyArg(), // id
			accountID,
			sqlmock.AnyArg(), // content_hash
			"image/png",
			8, // decoded size of "iVBORw0KGgo="
			"iVBORw0KGgo=",
			nil, // source_url
		).
		WillReturnRows(rows)

	// Expect ref insert
	mock.ExpectExec(`INSERT INTO llm_conversation_attachment_refs`).
		WithArgs(insertedID, messageID, conversationID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	mock.ExpectCommit()

	ids, err := dao.SaveAttachments(messageID, conversationID, accountID, images)
	assert.NoError(t, err)
	assert.Len(t, ids, 1)
	assert.Equal(t, insertedID, ids[0])
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSaveAttachments_WithURL(t *testing.T) {
	dao, mock := setupAttachmentDAOMock(t)

	messageID := uuid.New().String()
	conversationID := uuid.New().String()
	accountID := uuid.New().String()
	insertedID := uuid.New()

	imageURL := "https://example.com/image.png"
	images := []ImageAttachment{
		{URL: imageURL, MIMEType: "image/png"},
	}

	mock.ExpectBegin()

	rows := sqlmock.NewRows([]string{"id"}).AddRow(insertedID)
	mock.ExpectQuery(`INSERT INTO llm_conversation_attachments`).
		WithArgs(
			sqlmock.AnyArg(), // id
			accountID,
			sqlmock.AnyArg(), // content_hash
			"image/png",
			0,                // size_bytes is 0 for URL
			sqlmock.AnyArg(), // data (nil []byte)
			&imageURL,
		).
		WillReturnRows(rows)

	mock.ExpectExec(`INSERT INTO llm_conversation_attachment_refs`).
		WithArgs(insertedID, messageID, conversationID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	mock.ExpectCommit()

	ids, err := dao.SaveAttachments(messageID, conversationID, accountID, images)
	assert.NoError(t, err)
	assert.Len(t, ids, 1)
	assert.Equal(t, insertedID, ids[0])
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSaveAttachments_EmptyImages(t *testing.T) {
	dao, _ := setupAttachmentDAOMock(t)

	ids, err := dao.SaveAttachments("msg-1", "conv-1", "acc-1", nil)
	assert.NoError(t, err)
	assert.Nil(t, ids)

	ids, err = dao.SaveAttachments("msg-1", "conv-1", "acc-1", []ImageAttachment{})
	assert.NoError(t, err)
	assert.Nil(t, ids)
}

func TestSaveAttachments_DuplicateImage_NewRef(t *testing.T) {
	dao, mock := setupAttachmentDAOMock(t)

	messageID := uuid.New().String()
	conversationID := uuid.New().String()
	accountID := uuid.New().String()
	existingID := uuid.New()

	images := []ImageAttachment{
		{Data: "duplicate-data", MIMEType: "image/jpeg"},
	}

	mock.ExpectBegin()

	// Upsert returns existing attachment id (dedup hit)
	rows := sqlmock.NewRows([]string{"id"}).AddRow(existingID)
	mock.ExpectQuery(`INSERT INTO llm_conversation_attachments`).
		WithArgs(
			sqlmock.AnyArg(),
			accountID,
			sqlmock.AnyArg(),
			"image/jpeg",
			10, // "duplicate-data" decodes to 10 bytes via RawURLEncoding
			"duplicate-data",
			nil,
		).
		WillReturnRows(rows)

	// Ref is still inserted, linking existing attachment to new message
	mock.ExpectExec(`INSERT INTO llm_conversation_attachment_refs`).
		WithArgs(existingID, messageID, conversationID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	mock.ExpectCommit()

	ids, err := dao.SaveAttachments(messageID, conversationID, accountID, images)
	assert.NoError(t, err)
	assert.Len(t, ids, 1)
	assert.Equal(t, existingID, ids[0])
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSaveAttachments_MultipleImages(t *testing.T) {
	dao, mock := setupAttachmentDAOMock(t)

	messageID := uuid.New().String()
	conversationID := uuid.New().String()
	accountID := uuid.New().String()
	id1 := uuid.New()
	id2 := uuid.New()

	images := []ImageAttachment{
		{Data: "image-data-1", MIMEType: "image/png"},
		{URL: "https://example.com/img.jpg", MIMEType: "image/jpeg"},
	}

	mock.ExpectBegin()

	// First image (base64)
	mock.ExpectQuery(`INSERT INTO llm_conversation_attachments`).
		WithArgs(
			sqlmock.AnyArg(), accountID,
			sqlmock.AnyArg(), "image/png", 9, // "image-data-1" decodes to 9 bytes via RawURLEncoding
			"image-data-1", nil,
		).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(id1))

	mock.ExpectExec(`INSERT INTO llm_conversation_attachment_refs`).
		WithArgs(id1, messageID, conversationID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	// Second image (URL)
	url := "https://example.com/img.jpg"
	mock.ExpectQuery(`INSERT INTO llm_conversation_attachments`).
		WithArgs(
			sqlmock.AnyArg(), accountID,
			sqlmock.AnyArg(), "image/jpeg", 0,
			sqlmock.AnyArg(), &url,
		).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(id2))

	mock.ExpectExec(`INSERT INTO llm_conversation_attachment_refs`).
		WithArgs(id2, messageID, conversationID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	mock.ExpectCommit()

	ids, err := dao.SaveAttachments(messageID, conversationID, accountID, images)
	assert.NoError(t, err)
	assert.Len(t, ids, 2)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestLoadAttachments(t *testing.T) {
	dao, mock := setupAttachmentDAOMock(t)

	messageID := uuid.New().String()
	accountID := uuid.New().String()
	attachID := uuid.New()
	msgUUID := uuid.MustParse(messageID)
	convUUID := uuid.New()
	accUUID := uuid.MustParse(accountID)

	rows := sqlmock.NewRows([]string{
		"id", "message_id", "conversation_id", "account_id", "content_hash",
		"mime_type", "size_bytes", "data", "source_url", "description", "created_at",
	}).AddRow(
		attachID, msgUUID, convUUID, accUUID, "abc123",
		"image/png", 1024, "data", nil, nil, time.Now(),
	)

	mock.ExpectQuery(`SELECT .+ FROM llm_conversation_attachment_refs`).
		WithArgs(messageID, accountID).
		WillReturnRows(rows)

	attachments, err := dao.LoadAttachments(messageID, accountID)
	assert.NoError(t, err)
	assert.Len(t, attachments, 1)
	assert.Equal(t, attachID, attachments[0].ID)
	assert.Equal(t, "image/png", attachments[0].MIMEType)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestLoadAttachmentDescriptions(t *testing.T) {
	dao, mock := setupAttachmentDAOMock(t)

	accountID := uuid.New().String()
	msgID1 := uuid.New()
	msgID2 := uuid.New()
	messageIDs := []string{msgID1.String(), msgID2.String()}
	desc := "A screenshot of a Kubernetes dashboard"

	rows := sqlmock.NewRows([]string{"id", "message_id", "mime_type", "description"}).
		AddRow(uuid.New(), msgID1, "image/png", &desc).
		AddRow(uuid.New(), msgID2, "image/jpeg", nil)

	mock.ExpectQuery(`SELECT .+ FROM llm_conversation_attachment_refs`).
		WithArgs(accountID, sqlmock.AnyArg()).
		WillReturnRows(rows)

	result, err := dao.LoadAttachmentDescriptions(messageIDs, accountID)
	assert.NoError(t, err)
	assert.Len(t, result, 2)
	assert.Len(t, result[msgID1.String()], 1)
	assert.Equal(t, &desc, result[msgID1.String()][0].Description)
	assert.Nil(t, result[msgID2.String()][0].Description)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestLoadAttachmentDescriptions_Empty(t *testing.T) {
	dao, _ := setupAttachmentDAOMock(t)

	result, err := dao.LoadAttachmentDescriptions(nil, "acc-1")
	assert.NoError(t, err)
	assert.Nil(t, result)

	result, err = dao.LoadAttachmentDescriptions([]string{}, "acc-1")
	assert.NoError(t, err)
	assert.Nil(t, result)
}

func TestUpdateAttachmentDescription(t *testing.T) {
	dao, mock := setupAttachmentDAOMock(t)

	attachmentID := uuid.New().String()
	accountID := uuid.New().String()
	description := "A screenshot showing pod crash loop"

	mock.ExpectExec(`UPDATE llm_conversation_attachments`).
		WithArgs(attachmentID, description, accountID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := dao.UpdateAttachmentDescription(attachmentID, accountID, description)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestComputeContentHash(t *testing.T) {
	// Same input should produce same hash
	hash1 := computeContentHash([]byte("test-data"))
	hash2 := computeContentHash([]byte("test-data"))
	assert.Equal(t, hash1, hash2)

	// Different input should produce different hash
	hash3 := computeContentHash([]byte("other-data"))
	assert.NotEqual(t, hash1, hash3)

	// Hash should be hex-encoded SHA-256 (64 chars)
	assert.Len(t, hash1, 64)
}

func TestPurgeExpiredAttachments(t *testing.T) {
	dao, mock := setupAttachmentDAOMock(t)

	mock.ExpectExec(`UPDATE llm_conversation_attachments`).
		WithArgs(7).
		WillReturnResult(sqlmock.NewResult(0, 3))

	count, err := dao.PurgeExpiredAttachments(7)
	assert.NoError(t, err)
	assert.Equal(t, int64(3), count)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPurgeExpiredAttachments_NoneExpired(t *testing.T) {
	dao, mock := setupAttachmentDAOMock(t)

	mock.ExpectExec(`UPDATE llm_conversation_attachments`).
		WithArgs(30).
		WillReturnResult(sqlmock.NewResult(0, 0))

	count, err := dao.PurgeExpiredAttachments(30)
	assert.NoError(t, err)
	assert.Equal(t, int64(0), count)
	assert.NoError(t, mock.ExpectationsWereMet())
}
