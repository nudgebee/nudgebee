package tools

import (
	"context"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
)

// lib/pq returns NUMERIC cells as []byte; the scan path must keep their
// decimal text and leave real bytea alone.
func TestKeepNumericText(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	cols := []*sqlmock.Column{sqlmock.NewColumn("estimated_saving").OfType("NUMERIC", nil), sqlmock.NewColumn("blob").OfType("BYTEA", nil)}
	mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRowsWithColumnDefinition(cols...))
	rows, err := db.Query("SELECT 1")
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	colTypes, err := rows.ColumnTypes()
	require.NoError(t, err)
	require.NoError(t, rows.Err())

	numericCols := numericColumnNames(colTypes)
	require.Equal(t, []string{"estimated_saving"}, numericCols)

	row := map[string]any{"estimated_saving": []byte("58.47"), "blob": []byte{0xff, 0x00}, "n": 3}
	keepNumericText(row, numericCols)
	require.Equal(t, "58.47", row["estimated_saving"])
	require.Equal(t, []byte{0xff, 0x00}, row["blob"], "bytea is not a numeric column and stays bytes")
	require.Equal(t, 3, row["n"])

	null := map[string]any{"estimated_saving": nil}
	keepNumericText(null, numericCols)
	require.Nil(t, null["estimated_saving"], "NULL stays nil")
}

// Regression through the real path (Queryx → ColumnTypes → MapScan →
// keepNumericText → common.MarshalJson): a NUMERIC cell must serialise as its
// decimal text, not base64, and a BYTEA cell must stay binary.
func TestSqlToolCallSerialisesNumericAsDecimalText(t *testing.T) {
	db, mock := mockMetastore(t)
	defer func() { _ = db.Close() }()
	cols := []*sqlmock.Column{
		sqlmock.NewColumn("id").OfType("TEXT", ""),
		sqlmock.NewColumn("estimated_saving").OfType("numeric", nil),
		sqlmock.NewColumn("blob").OfType("BYTEA", nil),
	}
	expectToolQuery(mock, "select").WillReturnRows(sqlmock.NewRowsWithColumnDefinition(cols...).AddRow("rec-1", []byte("58.47"), []byte{0xff, 0x00}))

	nbCtx := core.NbToolContext{AccountId: "550e8400-e29b-41d4-a716-446655440000", Ctx: security.NewRequestContext(context.Background(), nil, nil, nil, nil)}
	resp, data, err := sqlToolCall(nbCtx, "SELECT id, estimated_saving, blob FROM events", "events", "", 0, nil)
	require.NoError(t, err)
	require.Len(t, data, 1)
	require.Equal(t, "58.47", data[0]["estimated_saving"])
	require.Equal(t, []byte{0xff, 0x00}, data[0]["blob"])
	require.Contains(t, resp.Data, `"estimated_saving":"58.47"`, "decimal text, not base64 (\"NTguNDc=\")")
	require.Contains(t, resp.Data, `"blob":"/wA="`, "bytea is still base64, untouched")
	require.NoError(t, mock.ExpectationsWereMet())
}
