package k8s_upgrade

import (
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
)

func scopeDB(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return sqlx.NewDb(db, "postgres"), mock
}

func TestVerifyPlanScopeForeignIDsAreNotFound(t *testing.T) {
	cases := []struct {
		name                    string
		plan, step, task, query string
	}{
		{"plan", "p1", "", "", "upgrade_plan p"},
		{"step", "", "s1", "", "upgrade_plan_steps"},
		{"task", "", "", "t1", "upgrade_plan_tasks"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, mock := scopeDB(t)
			mock.ExpectQuery(tc.query).WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
			err := verifyPlanScope(db, "tenant", "account", tc.plan, tc.step, tc.task)
			if !errors.Is(err, ErrNotFound) {
				t.Fatalf("err = %v, want ErrNotFound", err)
			}
		})
	}
}

func TestVerifyPlanScopeOwnedIDsPass(t *testing.T) {
	db, mock := scopeDB(t)
	for i := 0; i < 3; i++ {
		mock.ExpectQuery("EXISTS").WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	}
	if err := verifyPlanScope(db, "tenant", "account", "p1", "s1", "t1"); err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyPlanScopeNoIDsSkipsQueries(t *testing.T) {
	db, _ := scopeDB(t)
	if err := verifyPlanScope(db, "tenant", "account", "", "", ""); err != nil {
		t.Fatal(err)
	}
}
