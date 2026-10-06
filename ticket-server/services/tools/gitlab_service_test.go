package tools

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"nudgebee/tickets-server/models"
	"nudgebee/tickets-server/services/ticket"

	"github.com/gin-gonic/gin"
)

// A pasted-image comment produces GitLab's markdown image syntax for a
// project-relative upload, plus GitLab's own (non-standard) sizing suffix.
// rewriteGitLabUploadImages must turn it into a link in place, using GitLab's
// global /-/project/<numeric id>/uploads/... form (verified against the real
// rendered <img> src on the issue page - the namespaced <group>/<project>
// form 404s even when authenticated). The upload can't be fetched
// server-side: every gitlab.com upload route is behind Cloudflare bot
// protection that blocks Go's net/http client specifically (TLS
// fingerprinting, not headers - a plain curl to the same URL passes through).
func TestRewriteGitLabUploadImages(t *testing.T) {
	body := "First Line\n\n![image.png](/uploads/f84367d7e3833600fa27f72d55c83431/image.png){width=720 height=218}\n\nNext Line"

	got := rewriteGitLabUploadImages(body, "https://gitlab.com", 85540573)

	want := "First Line\n\n[\U0001F4CE image.png (view on GitLab)](https://gitlab.com/-/project/85540573/uploads/f84367d7e3833600fa27f72d55c83431/image.png)\n\nNext Line"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// A comment with no image reference must pass through unchanged.
func TestRewriteGitLabUploadImages_LeavesPlainTextAlone(t *testing.T) {
	body := "just a normal comment, no image here"
	got := rewriteGitLabUploadImages(body, "https://gitlab.com", 85540573)
	if got != body {
		t.Fatalf("expected plain text to pass through unchanged, got %q", got)
	}
}

// A deleted GitLab issue (404) must surface as ticket.ErrNotFound, not a
// generic error — the ticket-status sync job relies on that to skip it
// without counting toward its consecutive-failure abort guard.
func TestGitLabServiceGet_NotFoundIsErrNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"404 Not found"}`))
	}))
	defer srv.Close()

	cfg := models.TicketConfigurations{
		Password: "token",
		URL:      srv.URL,
		Projects: []models.Project{{Key: "group/project"}},
	}
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())

	svc := &GitLabService{}
	_, err := svc.Get(ctx, cfg, "99")
	if !errors.Is(err, ticket.ErrNotFound) {
		t.Fatalf("err = %v, want it to wrap ticket.ErrNotFound", err)
	}
}

// GitLab's notes endpoint interleaves real comments with system-generated
// notes (label changes, status transitions, ...). Only the former should
// reach Nudgebee's comment thread.
func TestGitLabServiceGetComments_FiltersSystemNotes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{"id":1,"body":"changed the description","system":true,"author":{"username":"bot"},"created_at":"2026-01-01T00:00:00Z"},
			{"id":2,"body":"Looks good to me","system":false,"author":{"username":"alice"},"created_at":"2026-01-02T00:00:00Z","updated_at":"2026-01-02T00:00:00Z"}
		]`))
	}))
	defer srv.Close()

	cfg := models.TicketConfigurations{
		Password: "token",
		URL:      srv.URL,
		Projects: []models.Project{{Key: "group/project"}},
	}
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())

	svc := &GitLabService{}
	comments, err := svc.GetComments(ctx, cfg, "99")
	if err != nil {
		t.Fatalf("GetComments() error = %v", err)
	}
	if len(comments) != 1 {
		t.Fatalf("len(comments) = %d, want 1 (system note must be filtered out); got %+v", len(comments), comments)
	}
	if comments[0].Author != "alice" || comments[0].Comment != "Looks good to me" {
		t.Fatalf("comments[0] = %+v, want author=alice comment=\"Looks good to me\"", comments[0])
	}
}
