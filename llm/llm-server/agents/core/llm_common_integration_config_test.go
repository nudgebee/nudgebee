package core

import (
	"testing"

	"nudgebee/llm/common"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
)

// TestExecLLMIntegrationConfigQuery_NullableColumns guards the regression where
// a NULL value in `is_encrypted` (or in `name`/`value`) caused
// `sql: Scan error on column index 3, name "is_encrypted"` and broke LLM config
// resolution for the affected tenant.
func TestExecLLMIntegrationConfigQuery_NullableColumns(t *testing.T) {
	cols := []string{"id", "name", "value", "is_encrypted"}
	plain := "abc-key-123"

	cases := []struct {
		name string
		rows *sqlmock.Rows
		want map[string]string
	}{
		{
			name: "is_encrypted NULL is treated as not encrypted",
			rows: sqlmock.NewRows(cols).AddRow("11111111-1111-1111-1111-111111111111", "api_key", plain, nil),
			want: map[string]string{"api_key": plain},
		},
		{
			name: "is_encrypted false reads value as plaintext",
			rows: sqlmock.NewRows(cols).AddRow("22222222-2222-2222-2222-222222222222", "api_key", plain, false),
			want: map[string]string{"api_key": plain},
		},
		{
			name: "value NULL becomes empty string",
			rows: sqlmock.NewRows(cols).AddRow("33333333-3333-3333-3333-333333333333", "api_key", nil, false),
			want: map[string]string{"api_key": ""},
		},
		{
			name: "row with NULL name is skipped",
			rows: sqlmock.NewRows(cols).
				AddRow("44444444-4444-4444-4444-444444444444", nil, plain, false).
				AddRow("55555555-5555-5555-5555-555555555555", "api_key", plain, nil),
			want: map[string]string{"api_key": plain},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			assert.NoError(t, err)
			defer func() { _ = db.Close() }()

			dbm := &common.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}
			mock.ExpectQuery("SELECT").WillReturnRows(tc.rows)

			got, err := execLLMIntegrationConfigQuery(nil, dbm,
				`SELECT id, name, value, is_encrypted FROM t WHERE tenant_id = :tenant_id`,
				map[string]any{"tenant_id": "tenant-x"}, "test-id")
			assert.NoError(t, err)
			assert.Equal(t, tc.want, got)
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
