package recommendation

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"

	"nudgebee/services/common"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	grantTestAccount      = "11111111-1111-1111-1111-111111111111"
	grantTestOtherAccount = "33333333-3333-3333-3333-333333333333" // another tenant's
)

// The recommendation writes used to accept only a built-in role, so a person
// whose access comes from custom roles was refused from the UI and from the
// AI's tools alike. They now take the action's grant key (the gateway's
// classification, or the key llm-server sends for a change the AI makes), and
// with no key they are exactly the built-in check.
func TestRecommendationWritesHonourTheActionsGrant(t *testing.T) {
	db, _, err := sqlmock.New() // no expectations: a call let through fails on its first query
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) {
		return &database.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}, nil
	})
	t.Cleanup(func() { database.RegisterDatabaseManagerHook(database.Metastore, nil) })

	calls := map[string]func(ctx *security.RequestContext, account string) error{
		"recommendations_apply": func(ctx *security.RequestContext, account string) error {
			_, err := ApplyRecommendation(ctx, RecommendationApplyRequest{AccountId: account, RecommendationId: "r1"})
			return err
		},
		"recommendation_job_create": func(ctx *security.RequestContext, account string) error {
			_, err := CreateRecommendationJob(ctx, RecommendationJobCreateRequest{AccountId: account, JobName: "cis"})
			return err
		},
		"recommendations_update_dismissal": func(ctx *security.RequestContext, account string) error {
			_, err := UpdateRecommendationDismissal(ctx, RecommendationDismissalRequest{AccountId: account, RecommendationId: "r1", Dismissed: true})
			return err
		},
		"recommendations_create_ticket_resolution": func(ctx *security.RequestContext, account string) error {
			_, err := RecordTicketResolution(ctx, RecommendationTicketResolutionRequest{AccountId: account, RecommendationId: "r1", TicketId: "t1"})
			return err
		},
	}

	person := func(t *testing.T, grants map[string]bool, key string) *security.RequestContext {
		t.Helper()
		raw, err := json.Marshal(map[string]any{
			"TenantId": "tenant-1", "UserId": "user-1", "AccountIds": []string{grantTestAccount}, "Roles": []string{},
			"CustomPermissions": grants,
		})
		require.NoError(t, err)
		var sc security.SecurityContext
		require.NoError(t, json.Unmarshal(raw, &sc))
		ctx := security.NewRequestContext(context.Background(), &sc, slog.Default(), nil, nil)
		ctx.SetActionPermission(key)
		return ctx
	}
	denied := func(err error) bool {
		var e common.Error
		return errors.As(err, &e) && e.Code == 403
	}

	for action, call := range calls {
		t.Run(action, func(t *testing.T) {
			execute := map[string]bool{"recommendations:Execute": true}
			assert.False(t, denied(call(person(t, execute, "recommendations:Execute"), grantTestAccount)), "grant for the key")
			assert.True(t, denied(call(person(t, execute, ""), grantTestAccount)), "no key: built-in roles only")
			assert.True(t, denied(call(person(t, execute, "security:Execute"), grantTestAccount)), "a grant for another module")
			assert.True(t, denied(call(person(t, execute, "recommendations:Read"), grantTestAccount)), "a read key never authorizes a write")
			assert.True(t, denied(call(person(t, execute, "recommendations:Execute"), grantTestOtherAccount)), "another tenant's account")
		})
	}
}
