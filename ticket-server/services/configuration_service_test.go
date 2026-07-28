package services

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"nudgebee/tickets-server/models"
	ticketmgr "nudgebee/tickets-server/services/ticket"

	"github.com/gin-gonic/gin"
	"github.com/lib/pq"
)

// fakeManager is a no-op TicketManager that counts GetCreateMeta calls so we can
// assert fetchCreateMetaCached serves the second call from cache.
type fakeManager struct{ calls int }

func (f *fakeManager) GetCreateMeta(_ *gin.Context, _ models.TicketConfigurations, _ string) (interface{}, error) {
	f.calls++
	return map[string]interface{}{"data": []interface{}{map[string]interface{}{"name": "Fake"}}}, nil
}

// Unused interface methods.
func (f *fakeManager) Create(_ *gin.Context, _ models.TicketConfigurations, t models.Ticket) (models.Ticket, error) {
	return t, nil
}
func (f *fakeManager) AddComment(_ *gin.Context, _ models.TicketConfigurations, _ models.Ticket) error {
	return nil
}
func (f *fakeManager) GetComments(_ *gin.Context, _ models.TicketConfigurations, _ string) ([]models.Comments, error) {
	return nil, nil
}
func (f *fakeManager) Get(_ *gin.Context, _ models.TicketConfigurations, _ string) (*models.Ticket, error) {
	return nil, nil
}
func (f *fakeManager) Update(_ *gin.Context, _ models.TicketConfigurations, _ string, _ models.UpdateFields) error {
	return nil
}
func (f *fakeManager) Transition(_ *gin.Context, _ models.TicketConfigurations, _ string, _ string) error {
	return nil
}
func (f *fakeManager) List(_ *gin.Context, _ models.TicketConfigurations, _ models.ListParams) (*models.ListResult, error) {
	return nil, nil
}

func TestFetchCreateMetaCached(t *testing.T) {
	fake := &fakeManager{}
	const tool = "faketool_cachetest"
	ticketmgr.RegisterTicketManager(tool, fake)

	cfg := models.TicketConfigurations{ID: "cfg-cache-1", Tool: tool}
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())

	// fetchCreateMetaCached writes to a package-level cache keyed by cfg.ID, which
	// survives across repeated runs in the same process (e.g. `go test -count=N`).
	// Clear this key up front (and on cleanup) so the first fetch is a guaranteed
	// miss and the test stays hermetic regardless of run order or count.
	invalidateCreateMetaCache(cfg.ID)
	t.Cleanup(func() { invalidateCreateMetaCache(cfg.ID) })

	// First call: miss -> registry invoked, result cached.
	if _, err := fetchCreateMetaCached(ctx, cfg, "PROJ"); err != nil {
		t.Fatalf("first fetch errored: %v", err)
	}
	// Second call (same key): hit -> registry NOT invoked again.
	if _, err := fetchCreateMetaCached(ctx, cfg, "PROJ"); err != nil {
		t.Fatalf("second fetch errored: %v", err)
	}
	if fake.calls != 1 {
		t.Errorf("GetCreateMeta called %d times, want 1 (second should be cached)", fake.calls)
	}

	// Invalidation drops the entry; the next fetch hits the registry again.
	invalidateCreateMetaCache(cfg.ID)
	if _, err := fetchCreateMetaCached(ctx, cfg, "PROJ"); err != nil {
		t.Fatalf("post-invalidation fetch errored: %v", err)
	}
	if fake.calls != 2 {
		t.Errorf("GetCreateMeta called %d times after invalidation, want 2", fake.calls)
	}

	// Unknown tool surfaces a clear error rather than caching nonsense.
	if _, err := fetchCreateMetaCached(ctx, models.TicketConfigurations{ID: "x", Tool: "nope_tool"}, "P"); err == nil {
		t.Error("expected error for unregistered tool")
	}
}

// TestShouldRehydrateCredentials pins the id gate: a create (no id) must not
// rehydrate, so a token config can't inherit a same-named App's auth_type (#33173).
func TestShouldRehydrateCredentials(t *testing.T) {
	cases := []struct {
		name     string
		id       string
		password string
		authType string
		want     bool
	}{
		{"create with token, no auth_type (the reported bug) -> no rehydrate", "", "ghp_pat", "", false},
		{"create with token and auth_type -> no rehydrate", "", "ghp_pat", "token", false},
		{"create with everything blank, no id -> no rehydrate", "", "", "", false},
		{"edit, password omitted -> rehydrate", "cfg-1", "", "token", true},
		{"edit, auth_type omitted -> rehydrate", "cfg-1", "ghp_pat", "", true},
		{"edit, both omitted -> rehydrate", "cfg-1", "", "", true},
		{"edit, both present -> no rehydrate", "cfg-1", "ghp_pat", "application", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ShouldRehydrateCredentials(tc.id, tc.password, tc.authType); got != tc.want {
				t.Errorf("ShouldRehydrateCredentials(%q, %q, %q) = %v, want %v", tc.id, tc.password, tc.authType, got, tc.want)
			}
		})
	}
}

// TestDuplicateNameError: a 23505 becomes a friendly name-bearing message; every
// other error passes through as nil.
func TestDuplicateNameError(t *testing.T) {
	if err := duplicateNameError(&pq.Error{Code: "23505"}, "acme", "github"); err == nil {
		t.Fatal("expected a friendly error for a 23505 unique violation, got nil")
	} else if !strings.Contains(err.Error(), "acme") || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("friendly error missing name/context: %q", err.Error())
	}

	if err := duplicateNameError(&pq.Error{Code: "23503"}, "acme", "github"); err != nil {
		t.Errorf("non-23505 pq error should pass through as nil, got %v", err)
	}
	if err := duplicateNameError(errors.New("connection refused"), "acme", "github"); err != nil {
		t.Errorf("non-pq error should pass through as nil, got %v", err)
	}
	if err := duplicateNameError(nil, "acme", "github"); err != nil {
		t.Errorf("nil error should pass through as nil, got %v", err)
	}
}
