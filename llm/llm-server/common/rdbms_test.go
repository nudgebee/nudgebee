package common

import (
	"database/sql"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
)

func MockMetastore() (*sql.DB, sqlmock.Sqlmock, error) {
	db, mock, err := sqlmock.New()
	if err != nil {
		return nil, nil, err
	}
	RegisterDatabaseManagerHook(Metastore, func() (*DatabaseManager, error) {
		return &DatabaseManager{
			Db: sqlx.NewDb(db, "postgresql"),
		}, nil
	})
	return db, mock, nil
}

func TestMaskDBConnectionString(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "simple password",
			input:    "postgres://user:password@host:5432/db",
			expected: "postgres://user:xxxxx@host:5432/db",
		},
		{
			name:     "password with colon",
			input:    "postgres://user:p:ssword@host:5432/db",
			expected: "postgres://user:xxxxx@host:5432/db",
		},
		{
			name:     "password with special characters",
			input:    "postgres://user:p@ssword@host:5432/db",
			expected: "postgres://user:xxxxx@host:5432/db",
		},
		{
			name:     "malformed url fallback",
			input:    "postgres://user:p%ssword@host:5432/db",
			expected: "postgres://user:xxxxx@host:5432/db",
		},
		{
			name:     "malformed url fallback with at-sign in password",
			input:    "postgres://user:p%ss@word@host:5432/db",
			expected: "postgres://user:xxxxx@host:5432/db",
		},
		{
			name:     "keyword-value format",
			input:    "host=localhost port=5432 dbname=mydb user=postgres password=mypassword",
			expected: "host=localhost port=5432 dbname=mydb user=postgres password=xxxxx",
		},
		{
			name:     "keyword-value format with quoted password",
			input:    `host=localhost user=postgres password="my@password"`,
			expected: `host=localhost user=postgres password=xxxxx`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MaskDBConnectionString(tt.input)
			if got != tt.expected {
				t.Errorf("MaskDBConnectionString() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestRegisterDatabaseManagerHookReplacesCachedManager(t *testing.T) {
	const name DatabaseManagerType = "hook-replacement-test"
	t.Cleanup(func() { RegisterDatabaseManagerHook(name, nil) })
	first := &DatabaseManager{}
	second := &DatabaseManager{}
	RegisterDatabaseManagerHook(name, func() (*DatabaseManager, error) { return first, nil })
	got, err := GetDatabaseManager(name)
	if err != nil || got != first {
		t.Fatalf("first lookup = %p, %v; want %p", got, err, first)
	}
	RegisterDatabaseManagerHook(name, func() (*DatabaseManager, error) { return second, nil })
	got, err = GetDatabaseManager(name)
	if err != nil || got != second {
		t.Fatalf("replacement lookup = %p, %v; want %p", got, err, second)
	}
	RegisterDatabaseManagerHook(name, nil)
	// An unknown manager name cannot fall back to a real database connection.
	if _, err := GetDatabaseManager(name); err == nil {
		t.Fatal("removed hook must not return a cached manager")
	}
}
