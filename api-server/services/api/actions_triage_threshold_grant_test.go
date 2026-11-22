package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"nudgebee/services/internal/database"
	"nudgebee/services/security"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Applying or reverting a threshold suggestion is classified events:Execute.
// The handlers used to admit a custom role only through an events:Write grant,
// so the grant the role editor offers for these actions was refused. They now
// take the action's key; Write and Execute are separate grants.
func TestThresholdApplyHandlersHonourTheActionsGrant(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handlers := map[string]func(*ActionRequest, *gin.Context, *security.RequestContext){
		"event_apply_threshold_suggestion":  handleEventApplyThresholdSuggestion,
		"event_revert_threshold_suggestion": handleEventRevertThresholdSuggestion,
	}
	const key = "events:Execute"
	person := func(extra string) string {
		return `{"TenantId":"tenant-1","UserId":"user-2","AccountIds":["` + triageOwnAccount + `"]` + extra + `}`
	}

	for action, handler := range handlers {
		t.Run(action, func(t *testing.T) {
			status := func(caller, key string) int {
				db, _, err := sqlmock.New() // no expectations: a call let through fails on its first read
				require.NoError(t, err)
				t.Cleanup(func() { _ = db.Close() })
				database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) {
					return &database.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}, nil
				})
				t.Cleanup(func() { database.RegisterDatabaseManagerHook(database.Metastore, nil) })
				var sc security.SecurityContext
				require.NoError(t, json.Unmarshal([]byte(caller), &sc))
				ctx := security.NewRequestContext(context.Background(), &sc, slog.Default(), nil, nil)
				ctx.SetActionPermission(key)
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
				handler(&ActionRequest{Input: map[string]any{
					"alert_rule_key": "k", "cloud_account_id": triageOwnAccount, "method": "direct",
				}}, c, ctx)
				return rec.Code
			}

			assert.NotEqual(t, http.StatusForbidden, status(person(`,"Roles":[],"CustomPermissions":{"events:Execute":true}`), key), "tenant-wide grant for the key")
			assert.NotEqual(t, http.StatusForbidden, status(person(`,"Roles":[],"ScopedCustomPermissions":{"`+triageOwnAccount+`":{"events:Execute":true}}`), key), "grant bound to the account")
			assert.Equal(t, http.StatusForbidden, status(person(`,"Roles":[]`), key), "no grant")
			assert.Equal(t, http.StatusForbidden, status(person(`,"Roles":[],"CustomPermissions":{"events:Write":true}`), key), "a Write grant is not Execute")
			assert.Equal(t, http.StatusForbidden, status(person(`,"Roles":[],"CustomPermissions":{"events:Execute":true}`), ""), "no key: built-in roles only")
			assert.NotEqual(t, http.StatusForbidden, status(triageAccountAdmin, key), "built-in account admin")
		})
	}
}
