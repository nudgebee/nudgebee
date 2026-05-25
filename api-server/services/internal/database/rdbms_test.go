package database

import (
	"fmt"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
)

func TestBuildInClause_SQLInjection(t *testing.T) {
	d := &DatabaseManager{}

	// Malicious input
	maliciousInput := "'; DROP TABLE users; --"

	// Safe behavior: single quotes should be escaped by doubling them
	result := d.BuildInClause(maliciousInput)

	// We expect the single quote to be replaced by two single quotes
	expectedSafe := fmt.Sprintf("'%s'", strings.ReplaceAll(maliciousInput, "'", "''"))
	assert.Equal(t, expectedSafe, result, "The output should be escaped to prevent SQL injection")
}

func TestBuildInClause_StandardBehavior(t *testing.T) {
	dm := &DatabaseManager{} // Db is nil

	// Case 1: Standard SQL injection attempt with single quote
	input1 := "'; DROP TABLE students; --"
	result1 := dm.BuildInClause(input1)
	// Expected: single quotes doubled
	assert.Equal(t, "'''; DROP TABLE students; --'", result1)

	// Case 2: Backslash injection (Vulnerable in ClickHouse if not escaped, but here Db is nil so we don't escape)
	input2 := `\`
	result2 := dm.BuildInClause(input2)
	// Expect: '\'' (no backslash escaping)
	assert.Equal(t, `'\'`, result2)
}

func TestBuildInClause_ClickHouse(t *testing.T) {
	// Create mock DB to set DriverName
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("an error '%s' was not expected when opening a stub database connection", err)
	}
	defer func() {
		_ = db.Close()
	}()
	sqlxDB := sqlx.NewDb(db, "clickhouse")

	dm := &DatabaseManager{
		Db: sqlxDB,
	}

	// Case: Backslash injection
	input := `\`
	result := dm.BuildInClause(input)

	// We WANT: '\\' (quotes added around, so '\\')
	// BuildInClause adds surrounding quotes.
	// Inside: \ -> \\
	// Then surrounds: '\\'
	assert.Equal(t, `'\\'`, result)

	// Complex injection attempt
	input2 := `\`
	input3 := `) OR 1=1 --`
	resultComplex := dm.BuildInClause(input2, input3)
	// Expected: '\\',') OR 1=1 --'
	// The first string is '\\'. ClickHouse sees literal backslash. String closed safely.
	// The second string is ') OR 1=1 --'. String closed safely.
	assert.Equal(t, `'\\',') OR 1=1 --'`, resultComplex)
}

func TestBuildInClause_Postgres(t *testing.T) {
	// Create mock DB to set DriverName
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("an error '%s' was not expected when opening a stub database connection", err)
	}
	defer func() {
		_ = db.Close()
	}()
	sqlxDB := sqlx.NewDb(db, "postgres")

	dm := &DatabaseManager{
		Db: sqlxDB,
	}

	// Case: Backslash injection
	input := `\`
	result := dm.BuildInClause(input)

	// Postgres doesn't need backslash escaping (assuming standard conforming strings)
	// So expect: '\''
	assert.Equal(t, `'\'`, result)
}
