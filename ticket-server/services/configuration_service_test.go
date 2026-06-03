package services

import (
	"net/http/httptest"
	"testing"

	"nudgebee/tickets-server/models"
	ticketmgr "nudgebee/tickets-server/services/ticket"

	"github.com/gin-gonic/gin"
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
