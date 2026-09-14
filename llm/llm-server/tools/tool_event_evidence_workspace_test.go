package tools

import (
	"errors"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/workspace"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockWorkspaceManager stubs only SaveFile — every other WorkspaceManager
// method is picked up (as a nil-valued no-op) via the embedded interface,
// since these tests never call them.
type mockWorkspaceManager struct {
	workspace.WorkspaceManager
	saveFileFunc func(ctx *security.RequestContext, accountId, conversationId, path, content string) error
}

func (m *mockWorkspaceManager) SaveFile(ctx *security.RequestContext, accountId, conversationId, path, content string) error {
	return m.saveFileFunc(ctx, accountId, conversationId, path, content)
}

// TestSaveEvidenceToWorkspaceIfLarge covers the reproduction from #35582:
// get_event_evidence's raw response previously entered the scratchpad with no
// workspace reference, so oversized evidence (a real 1.88MB example) was
// silently dropped by the per-observation truncation cap with no recovery
// handle. This exercises the fix in isolation from the DB-backed Call() path.
func TestSaveEvidenceToWorkspaceIfLarge(t *testing.T) {
	sc := security.NewRequestContextForSuperAdmin()
	threshold := config.Config.LlmServerEventEvidenceOverflowThreshold

	t.Run("small data passes through unchanged, no save attempted", func(t *testing.T) {
		wm := &mockWorkspaceManager{
			saveFileFunc: func(ctx *security.RequestContext, accountId, conversationId, path, content string) error {
				t.Fatal("SaveFile should not be called for data under the threshold")
				return nil
			},
		}
		small := strings.Repeat("a", threshold)
		data, references := saveEvidenceToWorkspaceIfLarge(sc, wm, "acct-1", "conv-1", "event_x", small)
		assert.Equal(t, small, data)
		assert.Nil(t, references)
	})

	t.Run("oversized data under the preview cap is saved and the preview keeps the full body", func(t *testing.T) {
		var savedPath, savedContent string
		wm := &mockWorkspaceManager{
			saveFileFunc: func(ctx *security.RequestContext, accountId, conversationId, path, content string) error {
				savedPath = path
				savedContent = content
				return nil
			},
		}
		// Over the save trigger but well under evidencePreviewBytes: this is
		// the 2KB-64KB band that used to reach the model intact (under the
		// scratchpad's per-observation cap) and must still arrive whole in
		// the preview instead of being cut to the save threshold.
		large := strings.Repeat("b", threshold+500)
		data, references := saveEvidenceToWorkspaceIfLarge(sc, wm, "acct-1", "conv-1", "event_x_evidence", large)

		require.Equal(t, large, savedContent, "the full body, not a truncated version, must be saved")
		require.Len(t, references, 1)
		assert.Equal(t, "file", references[0].Type)
		assert.Equal(t, "event_x_evidence.json", savedPath, "filename must be deterministic (no timestamp suffix) so repeat calls on the same event don't write duplicate blobs")
		assert.Equal(t, savedPath, references[0].Url)
		assert.Contains(t, data, "Output large (2500 bytes)")
		assert.Contains(t, data, large, "preview must carry the full body when it's under evidencePreviewBytes, not just the save-trigger threshold")
	})

	t.Run("data beyond the preview cap is truncated in the preview", func(t *testing.T) {
		wm := &mockWorkspaceManager{
			saveFileFunc: func(ctx *security.RequestContext, accountId, conversationId, path, content string) error {
				return nil
			},
		}
		large := strings.Repeat("d", evidencePreviewBytes+500)
		data, references := saveEvidenceToWorkspaceIfLarge(sc, wm, "acct-1", "conv-1", "event_x", large)
		require.Len(t, references, 1)
		assert.NotContains(t, data, strings.Repeat("d", evidencePreviewBytes+1), "preview must be capped at evidencePreviewBytes")
	})

	t.Run("save failure degrades to returning the raw data, not an error", func(t *testing.T) {
		wm := &mockWorkspaceManager{
			saveFileFunc: func(ctx *security.RequestContext, accountId, conversationId, path, content string) error {
				return errors.New("workspace unreachable")
			},
		}
		large := strings.Repeat("c", threshold+1)
		data, references := saveEvidenceToWorkspaceIfLarge(sc, wm, "acct-1", "conv-1", "event_x", large)
		assert.Equal(t, large, data)
		assert.Nil(t, references)
	})

	t.Run("preview truncation does not split a multi-byte UTF-8 rune", func(t *testing.T) {
		wm := &mockWorkspaceManager{
			saveFileFunc: func(ctx *security.RequestContext, accountId, conversationId, path, content string) error {
				return nil
			},
		}
		// A multi-byte rune ('€', 3 bytes) straddling the preview-cap boundary.
		large := strings.Repeat("a", evidencePreviewBytes-1) + "€€€€€€"
		data, _ := saveEvidenceToWorkspaceIfLarge(sc, wm, "acct-1", "conv-1", "event_x", large)
		previewStart := strings.Index(data, "Preview: ") + len("Preview: ")
		assert.True(t, len(data) >= previewStart)
		assert.True(t, strings.Contains(data[previewStart:], "€") || !strings.ContainsAny(data[previewStart:], "�"), "truncated preview must not contain the UTF-8 replacement character")
	})
}

// TestTruncateUTF8Safe_NonPositiveMaxBytes guards the negative-maxBytes panic
// (s[:maxBytes] with a negative index) flagged in PR review — reachable if a
// future caller passes a zero/negative bound directly to the helper.
func TestTruncateUTF8Safe_NonPositiveMaxBytes(t *testing.T) {
	assert.Equal(t, "", truncateUTF8Safe("hello", -1))
	assert.Equal(t, "", truncateUTF8Safe("hello", 0))
}
