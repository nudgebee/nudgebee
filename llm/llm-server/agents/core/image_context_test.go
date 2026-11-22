package core

import (
	"context"
	"encoding/base64"
	"log/slog"
	"testing"

	"nudgebee/llm/attachments"
	"nudgebee/llm/security"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Minimal valid file headers for MIME detection
var (
	// PNG: 8-byte magic header
	pngHeader = []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
)

func pngBase64() string { return base64.StdEncoding.EncodeToString(pngHeader) }

func TestImageDescriptionPrompt_NotEmpty(t *testing.T) {
	assert.NotEmpty(t, imageDescriptionPrompt)
	assert.Contains(t, imageDescriptionPrompt, "image description")
}

func TestImageDescriptionPrompt_HasGuidance(t *testing.T) {
	// Prompt should guide the LLM to produce concise, technical descriptions
	assert.Contains(t, imageDescriptionPrompt, "concise")
	assert.Contains(t, imageDescriptionPrompt, "200 characters")
}

func TestGenerateImageDescriptionsAsync_NoImages(t *testing.T) {
	// Should be a no-op when there are no images
	request := NBAgentRequest{
		Images: nil,
	}
	// Should not panic
	GenerateImageDescriptionsAsync(nil, request)
}

func TestGenerateImageDescriptionsAsync_NoDAO(t *testing.T) {
	// Should be a no-op when DAO is nil
	old := attachments.SetAttachmentDAO(nil)
	defer attachments.SetAttachmentDAO(old)

	request := NBAgentRequest{
		Images: []attachments.ImageAttachment{{Data: pngBase64(), MIMEType: "image/png"}},
	}
	// Should not panic even with nil DAO
	GenerateImageDescriptionsAsync(nil, request)
}

// --- parseImageContextResponse ---

func TestParseImageContextResponse_TaggedFormat(t *testing.T) {
	content := "<summary>\nPod foo-bar is CrashLoopBackOff.\n</summary>\n" +
		"<descriptions>\n1. Screenshot of pod status showing CrashLoopBackOff.\n" +
		"2. Grafana graph showing a CPU spike at 14:02.\n</descriptions>"

	summary, descs := parseImageContextResponse(content)
	assert.Equal(t, "Pod foo-bar is CrashLoopBackOff.", summary)
	assert.Equal(t, []string{
		"Screenshot of pod status showing CrashLoopBackOff.",
		"Grafana graph showing a CPU spike at 14:02.",
	}, descs)
}

func TestParseImageContextResponse_NoTags_FallsBackToWholeContent(t *testing.T) {
	content := "  Just a plain untagged response.  "
	summary, descs := parseImageContextResponse(content)
	assert.Equal(t, "Just a plain untagged response.", summary)
	assert.Nil(t, descs)
}

func TestParseImageContextResponse_SummaryOnly_NoDescriptions(t *testing.T) {
	content := "<summary>Only a summary here.</summary>"
	summary, descs := parseImageContextResponse(content)
	assert.Equal(t, "Only a summary here.", summary)
	assert.Nil(t, descs)
}

func TestParseImageContextResponse_CaseInsensitiveTags(t *testing.T) {
	content := "<SUMMARY>Upper case tags.</SUMMARY>\n<Descriptions>\n1. one image.\n</Descriptions>"
	summary, descs := parseImageContextResponse(content)
	assert.Equal(t, "Upper case tags.", summary)
	assert.Equal(t, []string{"one image."}, descs)
}

// --- ContentHashForImage ---

// --- saveImageDescriptions ---

// mockAttachmentDAO is a minimal hand-written IAttachmentDAO for testing
// saveImageDescriptions without a real database.
type mockAttachmentDAO struct {
	attachments []attachments.ConversationAttachment
	updated     map[string]string
	loadErr     error
	updateErr   error
}

func (m *mockAttachmentDAO) SaveAttachments(messageID, conversationID, accountID string, images []attachments.ImageAttachment) ([]uuid.UUID, error) {
	return nil, nil
}
func (m *mockAttachmentDAO) LoadAttachments(messageID, accountID string) ([]attachments.ConversationAttachment, error) {
	return m.attachments, m.loadErr
}
func (m *mockAttachmentDAO) LoadAttachmentDescriptions(messageIDs []string, accountID string) (map[string][]attachments.AttachmentDescription, error) {
	return nil, nil
}
func (m *mockAttachmentDAO) UpdateAttachmentDescription(attachmentID, accountID, description string) error {
	if m.updateErr != nil {
		return m.updateErr
	}
	if m.updated == nil {
		m.updated = make(map[string]string)
	}
	m.updated[attachmentID] = description
	return nil
}
func (m *mockAttachmentDAO) PurgeExpiredAttachments(retentionDays int) (int64, error) {
	return 0, nil
}

func testRequestContext() *security.RequestContext {
	return security.NewRequestContext(context.Background(), nil, slog.Default(), nil, nil)
}

func TestSaveImageDescriptions_WritesMatchingAttachmentsByContentHash(t *testing.T) {
	old := attachments.SetAttachmentDAO(nil)
	defer attachments.SetAttachmentDAO(old)

	img1 := attachments.ImageAttachment{Data: pngBase64(), MIMEType: "image/png"}
	img2 := attachments.ImageAttachment{URL: "https://example.com/img.png", MIMEType: "image/png"}
	att1ID, att2ID := uuid.New(), uuid.New()

	mock := &mockAttachmentDAO{
		attachments: []attachments.ConversationAttachment{
			{ID: att1ID, ContentHash: attachments.ContentHashForImage(img1)},
			{ID: att2ID, ContentHash: attachments.ContentHashForImage(img2)},
		},
	}
	attachments.SetAttachmentDAO(mock)

	request := NBAgentRequest{MessageId: "msg-1", AccountId: "acc-1", Images: []attachments.ImageAttachment{img1, img2}}
	saveImageDescriptions(testRequestContext(), request, []string{"first image desc", "second image desc"})

	require.Len(t, mock.updated, 2)
	assert.Equal(t, "first image desc", mock.updated[att1ID.String()])
	assert.Equal(t, "second image desc", mock.updated[att2ID.String()])
}

func TestSaveImageDescriptions_CountMismatch_NoWrites(t *testing.T) {
	old := attachments.SetAttachmentDAO(nil)
	defer attachments.SetAttachmentDAO(old)

	img1 := attachments.ImageAttachment{Data: pngBase64(), MIMEType: "image/png"}
	mock := &mockAttachmentDAO{
		attachments: []attachments.ConversationAttachment{{ID: uuid.New(), ContentHash: attachments.ContentHashForImage(img1)}},
	}
	attachments.SetAttachmentDAO(mock)

	request := NBAgentRequest{MessageId: "msg-1", AccountId: "acc-1", Images: []attachments.ImageAttachment{img1}}
	// Two descriptions for one image — mismatch, must defer to async fallback.
	saveImageDescriptions(testRequestContext(), request, []string{"a", "b"})

	assert.Empty(t, mock.updated)
}

func TestSaveImageDescriptions_SkipsAttachmentThatAlreadyHasDescription(t *testing.T) {
	old := attachments.SetAttachmentDAO(nil)
	defer attachments.SetAttachmentDAO(old)

	img1 := attachments.ImageAttachment{Data: pngBase64(), MIMEType: "image/png"}
	existing := "already described"
	mock := &mockAttachmentDAO{
		attachments: []attachments.ConversationAttachment{
			{ID: uuid.New(), ContentHash: attachments.ContentHashForImage(img1), Description: &existing},
		},
	}
	attachments.SetAttachmentDAO(mock)

	request := NBAgentRequest{MessageId: "msg-1", AccountId: "acc-1", Images: []attachments.ImageAttachment{img1}}
	saveImageDescriptions(testRequestContext(), request, []string{"new description"})

	assert.Empty(t, mock.updated, "must not overwrite an existing description")
}
