package services_server

import (
	"testing"

	"nudgebee/llm/common"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetVMServiceCodeLink(t *testing.T) {
	const container = "/system.slice/nginx.service"
	repoKey := AnnotationGitRepo + "/" + container
	branchKey := AnnotationGitBranch + "/" + container

	tests := []struct {
		name string
		rows [][]string
		want VMServiceCodeLink
	}{
		{name: "repo and branch", rows: [][]string{{repoKey, " https://github.com/org/web "}, {branchKey, "release"}}, want: VMServiceCodeLink{Repo: "https://github.com/org/web", Branch: "release"}},
		{name: "repo only", rows: [][]string{{repoKey, "https://github.com/org/web"}}, want: VMServiceCodeLink{Repo: "https://github.com/org/web"}},
		{name: "cleared link is unset", rows: [][]string{{repoKey, ""}, {branchKey, ""}}, want: VMServiceCodeLink{}},
		{name: "no link", rows: nil, want: VMServiceCodeLink{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			rows := sqlmock.NewRows([]string{"name", "value"})
			for _, r := range tt.rows {
				rows.AddRow(r[0], r[1])
			}
			mock.ExpectQuery("FROM events e").WithArgs("evt-1", repoKey, branchKey).WillReturnRows(rows)

			got, err := GetVMServiceCodeLink(&common.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}, "evt-1", container)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestListVMServiceCodeLinks(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	web, api := "/system.slice/web.service", "/docker/api"
	mock.ExpectQuery("FROM cloud_resource_attributes cra").WillReturnRows(sqlmock.NewRows([]string{"vm", "service", "repo", "branch"}).
		AddRow("vm-a", api, "https://gitlab.com/org/api", "release").
		AddRow("vm-a", web, " https://github.com/org/web ", "").
		AddRow("vm-b", web, " ", "main"))

	got, err := ListVMServiceCodeLinks(&common.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}, "acc-1")
	require.NoError(t, err)
	assert.Equal(t, []VMServiceLink{
		{VM: "vm-a", Service: api, VMServiceCodeLink: VMServiceCodeLink{Repo: "https://gitlab.com/org/api", Branch: "release"}},
		{VM: "vm-a", Service: web, VMServiceCodeLink: VMServiceCodeLink{Repo: "https://github.com/org/web"}},
	}, got, "a blank repo is not listed")
}
