package common

import (
	"database/sql"

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
