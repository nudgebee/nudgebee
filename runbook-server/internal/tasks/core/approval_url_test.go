package core

import (
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The approval email link must target the canonical frontend route
// (app/src/pages/automation/approvals/[token].tsx), not the legacy /workflow path.
func TestBuildApprovalURL_UsesAutomationRouteAndKeepsSignature(t *testing.T) {
	link := buildApprovalURL("https://app.example.com/", "tok 1", "approved", "a@b.c", "key")

	u, err := url.Parse(link)
	require.NoError(t, err)
	assert.Equal(t, "/automation/approvals/tok 1", u.Path)
	assert.False(t, strings.Contains(link, "//automation"), "trailing slash on base must be trimmed: %s", link)
	assert.Equal(t, "approved", u.Query().Get("status"))
	assert.Equal(t, "a@b.c", u.Query().Get("approver"))
	assert.Equal(t, approvalSignature("tok 1", "approved", "a@b.c", "key"), u.Query().Get("sig"))
}

// A link's own signature verifies; changing the decision, the approver or the
// key does not, and with no key nothing does.
func TestVerifyApprovalSignature(t *testing.T) {
	q, err := url.Parse(buildApprovalURL("https://app.example.com", "tok", "approve", "a@b.c", "key"))
	require.NoError(t, err)
	sig := q.Query().Get("sig")

	assert.True(t, VerifyApprovalSignature("tok", "approve", "a@b.c", sig, "key"))
	assert.False(t, VerifyApprovalSignature("tok", "reject", "a@b.c", sig, "key"))
	assert.False(t, VerifyApprovalSignature("tok", "approve", "x@y.z", sig, "key"))
	assert.False(t, VerifyApprovalSignature("other", "approve", "a@b.c", sig, "key"))
	assert.False(t, VerifyApprovalSignature("tok", "approve", "a@b.c", sig, "other-key"))
	assert.False(t, VerifyApprovalSignature("tok", "approve", "a@b.c", "", "key"))
	assert.False(t, VerifyApprovalSignature("tok", "approve", "a@b.c", approvalSignature("tok", "approve", "a@b.c", ""), ""))
}
