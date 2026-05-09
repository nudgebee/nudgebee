package relay

import (
	"nudgebee/runbook/services/security"
	"testing"

	"github.com/stretchr/testify/assert"
)

func newTestRequestContext() *security.RequestContext {
	return security.NewRequestContextForTenantAccountAdmin("test-tenant", "test-user", []string{"test-account"})
}

func TestDetectSqlcmdError(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr string
	}{
		{
			name:    "sqlcmd not found",
			input:   "script.sh: line 1: sqlcmd: not found\n",
			wantErr: "sqlcmd execution failed",
		},
		{
			name:    "command not found",
			input:   "bash: sqlcmd: command not found",
			wantErr: "sqlcmd execution failed",
		},
		{
			name:    "permission denied",
			input:   "/usr/local/bin/sqlcmd: Permission denied",
			wantErr: "sqlcmd execution failed",
		},
		{
			name:    "sqlcmd connection error",
			input:   "Sqlcmd: Error: Microsoft ODBC Driver 17 for SQL Server : Login timeout expired.",
			wantErr: "sqlcmd error",
		},
		{
			name:    "SQL Server engine Msg error",
			input:   "Msg 208, Level 16, State 1, Server MY_SERVER, Line 1\nInvalid object name 'does_not_exist'.",
			wantErr: "sqlcmd error",
		},
		{
			name:    "sqlcmd lowercase prefix",
			input:   "sqlcmd: error: something went wrong",
			wantErr: "sqlcmd error",
		},
		{
			name:    "valid tab-separated output returns no error",
			input:   "id\tname\n1\talice",
			wantErr: "",
		},
		{
			name:    "empty output returns no error",
			input:   "",
			wantErr: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := detectSqlcmdError(tc.input)
			if tc.wantErr == "" {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
			}
		})
	}
}

func TestCleanSqlcmdOutput(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "strips dashes separator and affected-rows",
			input:    "id\tname\n--\t----\n1\talice\n2\tbob\n\n(2 rows affected)\n",
			expected: "id\tname\n1\talice\n2\tbob",
		},
		{
			name:     "strips single row affected",
			input:    "result\n------\n2\n\n(1 row affected)\n",
			expected: "result\n2",
		},
		{
			name:     "no decoration to strip",
			input:    "id\tname\n1\talice\n",
			expected: "id\tname\n1\talice",
		},
		{
			name:     "only headers and dashes",
			input:    "id\tname\n--\t----\n\n(0 rows affected)\n",
			expected: "id\tname",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := cleanSqlcmdOutput(tc.input)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestConvertCsvToJsonString(t *testing.T) {
	tests := []struct {
		name      string
		csvData   string
		separator rune
		expected  string
	}{
		{
			name:      "simple tab-separated (MySQL style)",
			csvData:   "id\tname\n1\talice\n2\tbob\n",
			separator: '\t',
			expected:  `[{"id":"1","name":"alice"},{"id":"2","name":"bob"}]`,
		},
		{
			name:      "simple comma-separated (PostgreSQL style)",
			csvData:   "id,name\n1,alice\n2,bob\n",
			separator: ',',
			expected:  `[{"id":"1","name":"alice"},{"id":"2","name":"bob"}]`,
		},
		{
			name:      "empty result set",
			csvData:   "id\tname\n",
			separator: '\t',
			expected:  "[]",
		},
		{
			name:      "BOM character in header",
			csvData:   "\xef\xbb\xbfid\tname\n1\talice\n",
			separator: '\t',
			expected:  `[{"id":"1","name":"alice"}]`,
		},
		{
			name:      "row length mismatch skips bad rows",
			csvData:   "id\tname\n1\talice\n2\n3\tcharlie\n",
			separator: '\t',
			expected:  `[{"id":"1","name":"alice"},{"id":"3","name":"charlie"}]`,
		},
		{
			name:      "MSSQL cleaned output",
			csvData:   "id\tname\n1\talice\n2\tbob",
			separator: '\t',
			expected:  `[{"id":"1","name":"alice"},{"id":"2","name":"bob"}]`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := newTestRequestContext()
			result := convertCsvToJsonString(ctx, tc.csvData, tc.separator)
			assert.JSONEq(t, tc.expected, result)
		})
	}
}

func TestDetectDBQueryError(t *testing.T) {
	tests := []struct {
		name    string
		module  RelayJob
		input   string
		wantErr string
	}{
		// Postgres
		{name: "postgres ERROR prefix", module: RelayJobPostgres, input: "ERROR:  relation \"foo\" does not exist\nLINE 1: select * from foo", wantErr: "postgres error"},
		{name: "postgres FATAL prefix", module: RelayJobPostgres, input: "FATAL:  password authentication failed for user", wantErr: "postgres error"},
		{name: "postgres psql prefix", module: RelayJobPostgres, input: "psql: error: connection to server failed", wantErr: "postgres error"},
		{name: "postgres valid output no error", module: RelayJobPostgres, input: "id,name\n1,alice\n2,bob\n", wantErr: ""},
		// MySQL
		{name: "mysql ERROR prefix", module: RelayJobMysql, input: "ERROR 1064 (42000): You have an error in your SQL syntax", wantErr: "mysql error"},
		{name: "mysql valid output no error", module: RelayJobMysql, input: "id\tname\n1\talice\n", wantErr: ""},
		// ClickHouse
		{name: "clickhouse Code prefix", module: RelayJobClickhouse, input: "Code: 62. DB::Exception: Syntax error", wantErr: "clickhouse error"},
		{name: "clickhouse DB::Exception", module: RelayJobClickhouse, input: "Received exception from server: DB::Exception: Unknown identifier", wantErr: "clickhouse error"},
		{name: "clickhouse valid output no error", module: RelayJobClickhouse, input: "result\n2\n", wantErr: ""},
		// Oracle
		{name: "oracle ORA- prefix", module: RelayJobOracle, input: "ORA-00942: table or view does not exist", wantErr: "oracle error"},
		{name: "oracle SP2- prefix", module: RelayJobOracle, input: "SP2-0734: unknown command beginning", wantErr: "oracle error"},
		{name: "oracle PLS- prefix", module: RelayJobOracle, input: "PLS-00201: identifier must be declared", wantErr: "oracle error"},
		{name: "oracle valid output no error", module: RelayJobOracle, input: "ID,NAME\n1,alice\n", wantErr: ""},
		// Empty input
		{name: "empty input no error", module: RelayJobPostgres, input: "", wantErr: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := detectDBQueryError(tc.module, tc.input)
			if tc.wantErr == "" {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
			}
		})
	}
}

// TestGetRelayCommandResponseData_SurfacesErrors ensures relay-side failures
// (ImagePullBackOff, status 500, success=false) are returned as errors instead
// of being silently swallowed as empty results.
func TestGetRelayCommandResponseData_SurfacesErrors(t *testing.T) {
	tests := []struct {
		name     string
		response map[string]any
		wantErr  string
	}{
		{
			name: "success=false with ImagePullBackOff message",
			response: map[string]any{
				"data": map[string]any{
					"success":     false,
					"status_code": float64(500),
					"error_code":  float64(4705),
					"msg":         "ImagePullBackOff error detected for pod nb-runbook-e6e750a8",
					"findings":    []any{},
				},
			},
			wantErr: "ImagePullBackOff",
		},
		{
			name: "status_code 500 without success flag",
			response: map[string]any{
				"data": map[string]any{
					"status_code": float64(500),
					"msg":         "internal server error",
					"findings":    []any{},
				},
			},
			wantErr: "internal server error",
		},
		{
			name: "success=false with no message",
			response: map[string]any{
				"data": map[string]any{
					"success":  false,
					"findings": []any{},
				},
			},
			wantErr: "relay reported failure",
		},
		{
			name: "healthy response with empty findings is not an error",
			response: map[string]any{
				"data": map[string]any{
					"success":  true,
					"findings": []any{},
				},
			},
			wantErr: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := getRelayCommandResponseData(tc.response)
			if tc.wantErr != "" {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, result)
			}
		})
	}
}

