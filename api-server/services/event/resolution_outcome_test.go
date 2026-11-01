package event

import (
	"go/ast"
	"go/parser"
	"go/token"
	"log/slog"
	"strings"
	"testing"

	"nudgebee/services/internal/database"
	"nudgebee/services/internal/database/models"
	"nudgebee/services/security"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/require"
)

// A successful attempt writes only its own row; any `UPDATE events` is unexpected -> sqlmock error.
func TestRecordResolutionStatusLeavesTheEventAlone(t *testing.T) {
	for _, status := range []models.RecommendationResolutionStatus{
		models.RecommendationResolutionStatusSuccess,
		models.RecommendationResolutionStatusFailed,
	} {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		dbms := &database.DatabaseManager{Db: sqlx.NewDb(db, "sqlmock")}

		mock.ExpectExec(`UPDATE event_resolution SET status`).
			WithArgs("res-1", status, sqlmock.AnyArg(), "applied", models.RecommendationResolutionStatusInProgress, lastCheckKeep).
			WillReturnResult(sqlmock.NewResult(0, 1))

		ctx := security.NewRequestContextForTenantAdmin("tenant-1", slog.Default(), nil, nil)
		// No type reference: the undo-state capture has no task to read, so the only statement
		// is the resolution's own update.
		err = recordResolutionStatus(ctx, dbms, models.EventResolution{Id: "res-1", EventId: "evt-1"}, status, "applied", lastCheckKeep)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet(), string(status))
		_ = db.Close()
	}
}

// An unknown last_pr_check_at mode is refused before any statement runs.
func TestRecordResolutionStatusRefusesAnUnexpectedLastCheck(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	ctx := security.NewRequestContextForTenantAdmin("tenant-1", slog.Default(), nil, nil)
	err = recordResolutionStatus(ctx, &database.DatabaseManager{Db: sqlx.NewDb(db, "sqlmock")}, models.EventResolution{Id: "res-1"},
		models.RecommendationResolutionStatusSuccess, "applied", "now(); DROP TABLE events")
	require.Error(t, err)
	require.NoError(t, mock.ExpectationsWereMet(), "no statement may run")
}

// Apply/settle paths once set events.status = RESOLVED on success. They need agents to run,
// so this pins their source: no `UPDATE events` in any of them.
func TestRemediationApplyPathsNeverWriteEventStatus(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "service.go", nil, 0)
	require.NoError(t, err)

	guarded := map[string]bool{"ApplyEventResolution": true, "UpdateResolutionStatus": true, "settleResolution": true, "recordResolutionStatus": true}
	found := map[string]bool{}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || !guarded[fn.Name.Name] {
			continue
		}
		found[fn.Name.Name] = true
		ast.Inspect(fn, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if ok && lit.Kind == token.STRING {
				sql := strings.ToLower(strings.Join(strings.Fields(lit.Value), " "))
				require.NotContains(t, sql, "update events ", "%s writes the event's status at %s", fn.Name.Name, fset.Position(lit.Pos()))
			}
			return true
		})
	}
	require.Len(t, found, len(guarded), "a guarded function was renamed; update this test")
}
