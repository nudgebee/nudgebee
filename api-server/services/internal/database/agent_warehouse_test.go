package database

import (
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"nudgebee/services/config"
	"strings"
	"testing"

	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
)

func mockRelayServer() (*httptest.Server, error) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			_, _ = fmt.Fprintln(w, `{"message": "error"}`)
			w.WriteHeader(http.StatusInternalServerError)
		}
		stringBody := string(body)
		if strings.Contains(stringBody, "query_data") {
			responseData := `{
				"action": "response",
				"request_id": "1574f964-dbb2-48e8-b3db-0f3e03eb2658",
				"status_code": 200,
				"data": {
					"success": true,
					"data": {
						"data": [
							[
								1
							]
						],
						"columns": [
							"count"
						],
						"column_types": [
							"int"
						],
						"error": null
					},
					"request_id": "1574f964-dbb2-48e8-b3db-0f3e03eb2658"
				}
			}`
			roleBindingData := []byte(responseData)
			_, err = w.Write(roleBindingData)
			if err != nil {
				_, _ = fmt.Fprintln(w, `{"message": "error"}`)
				w.WriteHeader(http.StatusInternalServerError)
			}
		} else {
			_, _ = fmt.Fprintln(w, `{"message": "error"}`)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	config.Config.RelayServerEndpoint = ts.URL
	return ts, nil
}

func TestAgentDriver(t *testing.T) {
	ts, err := mockRelayServer()
	assert.Nil(t, err)
	defer ts.Close()

	agentDriver := newAgentWarehouseDriver()
	connector, err := agentDriver.OpenConnector("agent_warehouse")
	if err != nil {
		t.Fatal(err)
	}
	sqlxDb := sqlx.NewDb(sql.OpenDB(connector), "clickhouse")
	dummyAccountId := "dummy"
	rows, err := sqlxDb.Query("SELECT 1", dummyAccountId, "agent_warehouse_clickhouse")
	assert.Nil(t, err)
	assert.NotNil(t, rows)
	assert.Nil(t, rows.Err())
	cols, err := rows.Columns()
	assert.Nil(t, err)
	assert.Equal(t, 2, len(cols))
	colTypes, err := rows.ColumnTypes()
	assert.Nil(t, err)
	assert.Equal(t, 2, len(colTypes))

	for rows.Next() {
		var data int
		var accountId string
		err = rows.Scan(&data, &accountId)
		assert.Nil(t, err)
		assert.Equal(t, 1, data)
		assert.Equal(t, accountId, dummyAccountId)
	}

}
