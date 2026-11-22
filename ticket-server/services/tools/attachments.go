package tools

import "fmt"

// attachmentMarker replaces a stripped inline attachment reference in place,
// so the frontend (TicketListTable.jsx's ATTACHMENT_MARKER_RE) can split the
// comment text back apart and render the attachment where it actually was,
// rather than always after the full comment text. Shared by every provider
// that strips an inline reference out of the comment body and surfaces it as
// a models.CommentAttachment instead (GitHub's extractGithubUserAttachments,
// Jira's fetchCommentsFromJira).
func attachmentMarker(id string) string {
	return fmt.Sprintf("[[nb-attachment:%s]]", id)
}
