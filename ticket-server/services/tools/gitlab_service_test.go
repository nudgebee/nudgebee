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
