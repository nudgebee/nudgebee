package observability

import (
	"encoding/json"
	"strings"
	"testing"

	"nudgebee/services/query"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Unit tests for the Hive integration SQL builders, response parsers, and
// hive_url parser. These do not require a live HiveServer2 or DB; the
// DynamicLabelMappingSource / GetMergedLabelMapping integration paths mirror
// the Pinot suite and can be added once a `hive` integration exists in the
// test DB.

func TestParseHiveHostPort(t *testing.T) {
	cases := []struct {
		in         string
		wantHost   string
		wantPort   int
		wantUseTLS bool
		wantErr    bool
	}{
		{"hiveserver2.hive.svc.cluster.local:10000", "hiveserver2.hive.svc.cluster.local", 10000, false, false},
		{"hive://hs2.example.com:10001/foo", "hs2.example.com", 10001, false, false},
		{"thrift://10.0.0.5:10000?retry=3", "10.0.0.5", 10000, false, false},
		{"http://localhost:10000", "localhost", 10000, false, false},
		{"https://hs2.internal:10000", "hs2.internal", 10000, true, false},
		{"", "", 0, false, true},
		{"hiveserver2.host", "", 0, false, true},
		{":10000", "", 0, false, true},
		{"hostonly:abc", "", 0, false, true},
	}
	for _, tc := range cases {
		host, port, useTLS, err := parseHiveHostPort(tc.in)
		if tc.wantErr {
			assert.Error(t, err, "input=%q", tc.in)
			continue
		}
		require.NoError(t, err, "input=%q", tc.in)
		assert.Equal(t, tc.wantHost, host, "input=%q", tc.in)
		assert.Equal(t, tc.wantPort, port, "input=%q", tc.in)
		assert.Equal(t, tc.wantUseTLS, useTLS, "input=%q", tc.in)
	}
}

func TestHiveQuoteIdent(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"log", "`log`"},
		{"time_ms", "`time_ms`"},
		{"kubernetes.namespace_name", "`kubernetes`.`namespace_name`"},
		{"with`tick", "`with``tick`"},
		{"a.b.c", "`a`.`b`.`c`"},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, hiveQuoteIdent(tc.in), "input=%q", tc.in)
	}
}

func TestHiveSafeColumnRef(t *testing.T) {
	assert.True(t, hiveSafeColumnRef("log"))
	assert.True(t, hiveSafeColumnRef("time_ms"))
	assert.True(t, hiveSafeColumnRef("kubernetes.namespace_name"))
	assert.False(t, hiveSafeColumnRef(""))
	assert.False(t, hiveSafeColumnRef("a;b"))
	assert.False(t, hiveSafeColumnRef("a b"))
	assert.False(t, hiveSafeColumnRef("a-b"))
	assert.False(t, hiveSafeColumnRef("0starts_with_digit"))
}

func TestHiveQualifiedTable(t *testing.T) {
	assert.Equal(t, "`default`.`k8s_logs`", hiveQualifiedTable("default", "k8s_logs"))
	assert.Equal(t, "`k8s_logs`", hiveQualifiedTable("", "k8s_logs"))
}

func TestHiveEscapeString(t *testing.T) {
	cases := []struct{ in, want string }{
		{"plain", "plain"},
		{"O'Brien", `O\'Brien`},
		{`back\slash`, `back\\slash`},
		{`mix\'ed`, `mix\\\'ed`},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, hiveEscapeString(tc.in), "input=%q", tc.in)
	}
}

func TestBuildHiveSQL_NumericTimestamp(t *testing.T) {
	// Numeric epoch-ms — startMs/endMs go into the BETWEEN verbatim (ScaleFactor=1).
	// LIMIT is scaled up by hiveLogOverfetchFactor so the client-side sort can
	// pick the actual newest N from a wider scan window.
	sql := buildHiveSQL("default", "k8s_logs", "time_ms", "", 1_700_000_000_000, 1_700_000_060_000,
		hiveTsMode{ScaleFactor: 1}, 500, nil)
	assert.Contains(t, sql, "FROM `default`.`k8s_logs`")
	assert.Contains(t, sql, "`time_ms` BETWEEN 1700000000000 AND 1700000060000")
	assert.Contains(t, sql, "LIMIT 5000", "500 × overfetch factor 10")
	assert.NotContains(t, sql, "ORDER BY", "ORDER BY forces MapReduce — must be sorted client-side")
	assert.NotContains(t, sql, "OFFSET", "Hive does not support OFFSET")
}

func TestBuildHiveSQL_OverfetchCappedAtCeiling(t *testing.T) {
	// limit × 10 would exceed hiveLogOverfetchCap (10_000) — should clamp.
	sql := buildHiveSQL("default", "k8s_logs", "time_ms", "", 1_700_000_000_000, 1_700_000_060_000,
		hiveTsMode{ScaleFactor: 1}, 5_000, nil)
	assert.Contains(t, sql, "LIMIT 10000", "capped at hiveLogOverfetchCap")
}

func TestBuildHiveSQL_OverfetchHonoursMinimum(t *testing.T) {
	// limit × 10 stays below cap, but we never go below the user's limit.
	sql := buildHiveSQL("default", "k8s_logs", "time_ms", "", 1_700_000_000_000, 1_700_000_060_000,
		hiveTsMode{ScaleFactor: 1}, 50, nil)
	assert.Contains(t, sql, "LIMIT 500", "50 × 10 < cap")
}

func TestBuildHiveSQL_NumericSecondsScaleFactor(t *testing.T) {
	// ScaleFactor=1000 → seconds. 1.7e12 ms / 1000 = 1.7e9 s
	sql := buildHiveSQL("default", "k8s_logs", "ts", "", 1_700_000_000_000, 1_700_000_060_000,
		hiveTsMode{ScaleFactor: 1_000}, 100, nil)
	assert.Contains(t, sql, "`ts` BETWEEN 1700000000 AND 1700000060")
}

func TestBuildHiveSQL_StringTimestamp(t *testing.T) {
	sql := buildHiveSQL("default", "k8s_logs", "time", "", 1_700_000_000_000, 1_700_000_060_000,
		hiveTsMode{IsString: true, GoFormat: "2006-01-02T15:04:05Z"}, 100, nil)
	assert.Contains(t, sql, "`time` BETWEEN '2023-11-14T22:13:20Z' AND '2023-11-14T22:14:20Z'")
}

func TestBuildHiveSQL_WithWhere(t *testing.T) {
	// sortFields is accepted but currently ignored — client-side sort
	// handles newest-first. ORDER BY in SQL would force MapReduce.
	sql := buildHiveSQL("default", "k8s_logs", "time_ms",
		"`log` LIKE '%error%'",
		1_700_000_000_000, 1_700_000_060_000,
		hiveTsMode{ScaleFactor: 1}, 50,
		[]SortField{{ColumnName: "time_ms", Order: "asc"}},
	)
	assert.Contains(t, sql, " AND (`log` LIKE '%error%')")
	assert.Contains(t, sql, "LIMIT 500", "50 × overfetch factor 10")
	assert.NotContains(t, sql, "ORDER BY")
}

// Verifies the parser truncates to `limit` after sorting newest-first, so
// the user gets the actual newest N rows even though buildHiveSQL overfetched.
func TestParseHiveResultBytes_TruncatesAfterSort(t *testing.T) {
	body := hiveQueryResponse{
		Columns: []string{"log", "time_ms"},
		Rows: [][]any{
			{"oldest", float64(1_700_000_000_000)},
			{"middle", float64(1_700_000_001_000)},
			{"newest", float64(1_700_000_002_000)},
		},
	}
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	logs, err := parseHiveResultBytes(raw, "time_ms", "log", "", getHiveTimestampConverter(nil, "time_ms", ""), 2)
	require.NoError(t, err)
	require.Len(t, logs, 2, "truncated to limit=2 after sort")
	assert.Equal(t, "newest", logs[0].Message)
	assert.Equal(t, "middle", logs[1].Message)
}

func TestSortLogsByTimestampDesc(t *testing.T) {
	logs := []OutputLog{
		{Timestamp: "2026-05-21T20:00:05.000000Z", Message: "second"},
		{Timestamp: "2026-05-21T20:00:09.000000Z", Message: "third"},
		{Timestamp: "2026-05-21T20:00:04.000000Z", Message: "first"},
		{Timestamp: "", Message: "no-ts"},
	}
	sortLogsByTimestampDesc(logs)
	got := []string{logs[0].Message, logs[1].Message, logs[2].Message, logs[3].Message}
	assert.Equal(t, []string{"third", "second", "first", "no-ts"}, got,
		"newest first; empty timestamps sink to bottom")
}

func TestBuildHiveWhereClause_Operators(t *testing.T) {
	cases := []struct {
		name string
		w    query.QueryWhereClause
		want string
	}{
		{
			"eq",
			query.QueryWhereClause{Binary: map[string]map[query.BinaryWhereClauseType]any{
				"log": {query.Eq: "boom"},
			}},
			"`log` = 'boom'",
		},
		{
			"eq_column_with_embedded_backtick_gets_doubled",
			query.QueryWhereClause{Binary: map[string]map[query.BinaryWhereClauseType]any{
				"col`with`tick": {query.Eq: 1.0},
			}},
			"`col``with``tick` = 1",
		},
		{
			"contains",
			query.QueryWhereClause{Binary: map[string]map[query.BinaryWhereClauseType]any{
				"log": {query.Contains: "panic"},
			}},
			"`log` LIKE '%panic%'",
		},
		{
			"regex_via_RLIKE",
			query.QueryWhereClause{Binary: map[string]map[query.BinaryWhereClauseType]any{
				"log": {query.Regex: "^ERR"},
			}},
			"`log` RLIKE '^ERR'",
		},
		{
			"is_null_true",
			query.QueryWhereClause{Binary: map[string]map[query.BinaryWhereClauseType]any{
				"stream": {query.IsNull: true},
			}},
			"`stream` IS NULL",
		},
		{
			"is_null_false_means_not_null",
			query.QueryWhereClause{Binary: map[string]map[query.BinaryWhereClauseType]any{
				"stream": {query.IsNull: false},
			}},
			"`stream` IS NOT NULL",
		},
		{
			"nested_struct_eq",
			query.QueryWhereClause{Binary: map[string]map[query.BinaryWhereClauseType]any{
				"kubernetes.namespace_name": {query.Eq: "default"},
			}},
			"`kubernetes`.`namespace_name` = 'default'",
		},
		{
			"in",
			query.QueryWhereClause{Binary: map[string]map[query.BinaryWhereClauseType]any{
				"stream": {query.In: []any{"stdout", "stderr"}},
			}},
			"`stream` IN ('stdout', 'stderr')",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := buildHiveWhereClause(tc.w)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// Microsecond columns use the hiveScaleMicros sentinel (negative). The
// converter must produce a real RFC3339Nano for microsecond inputs (not
// silently treat them as ms), and the time-filter helper must multiply
// rather than divide.
func TestHiveTimestampConverter_Microseconds(t *testing.T) {
	schema := &hiveSchemaResponse{Columns: []hiveColumnSpec{
		{Name: "ts_us", Type: "bigint"},
	}}
	conv := getHiveTimestampConverter(schema, "ts_us", "")
	// 1700000000123456 µs = 2023-11-14T22:13:20.123456Z
	got := conv(int64(1_700_000_000_123_456))
	assert.Equal(t, "2023-11-14T22:13:20.123456Z", got)
}

func TestHiveNumericTimeFilter_Microseconds(t *testing.T) {
	filter := hiveNumericTimeFilter("`ts_us`", 1_700_000_000_000, 1_700_000_060_000, hiveScaleMicros)
	assert.Equal(t,
		"`ts_us` BETWEEN 1700000000000000 AND 1700000060000000",
		filter, "ms must be multiplied by 1000 to land in microseconds")
}

func TestHiveNumericTimeFilter_FallbackOnInvalidSentinel(t *testing.T) {
	// sf=0 is the pre-fix legacy value. Make sure we don't silently divide
	// by zero — we fall back to ms.
	filter := hiveNumericTimeFilter("`ts`", 1_700_000_000_000, 1_700_000_060_000, 0)
	assert.Equal(t, "`ts` BETWEEN 1700000000000 AND 1700000060000", filter)
}

func TestResolveHiveTsMode_FromSchema(t *testing.T) {
	schema := &hiveSchemaResponse{Columns: []hiveColumnSpec{
		{Name: "time_ms", Type: "bigint"},
		{Name: "time", Type: "string"},
		{Name: "evt_ts", Type: "timestamp"},
		{Name: "epoch_s", Type: "bigint"},
		{Name: "ts", Type: "bigint"}, // unsuffixed — defaults to ms
		{Name: "ts_us", Type: "bigint"},
	}}

	// time_ms → numeric, ScaleFactor=1 (suffix _ms)
	m := resolveHiveTsMode(schema, "time_ms", "")
	assert.False(t, m.IsString)
	assert.Equal(t, int64(1), m.ScaleFactor)

	// time → string
	m = resolveHiveTsMode(schema, "time", "")
	assert.True(t, m.IsString)

	// evt_ts → string (timestamp type)
	m = resolveHiveTsMode(schema, "evt_ts", "")
	assert.True(t, m.IsString)

	// epoch_s → numeric, ScaleFactor=1000 (explicit _s suffix)
	m = resolveHiveTsMode(schema, "epoch_s", "")
	assert.False(t, m.IsString)
	assert.Equal(t, int64(1_000), m.ScaleFactor)

	// ts → numeric, ScaleFactor=1 (default for plain bigint is now ms,
	// matching fluent-bit / fluentd / OTel emitters)
	m = resolveHiveTsMode(schema, "ts", "")
	assert.False(t, m.IsString)
	assert.Equal(t, int64(1), m.ScaleFactor)

	// ts_us → microseconds sentinel
	m = resolveHiveTsMode(schema, "ts_us", "")
	assert.False(t, m.IsString)
	assert.Equal(t, hiveScaleMicros, m.ScaleFactor)

	// Missing column with hint → string mode
	m = resolveHiveTsMode(schema, "no_such_col", "2006-01-02")
	assert.True(t, m.IsString)
	assert.Equal(t, "2006-01-02", m.GoFormat)
}

func TestBuildHiveLogGroupSQL(t *testing.T) {
	cols := hiveLogGroupCols{
		Database:     "default",
		Table:        "k8s_logs",
		TsCol:        "time_ms",
		MsgCol:       "log",
		SevCol:       "level",
		NsCol:        "kubernetes.namespace_name",
		PodCol:       "kubernetes.pod_name",
		ContainerCol: "kubernetes.container_name",
	}
	sql := buildHiveLogGroupSQL(cols, hiveTsMode{ScaleFactor: 1},
		1_700_000_000_000, 1_700_000_060_000, "kube-system", "etcd", 50)
	assert.Contains(t, sql, "SELECT `log`, `kubernetes`.`namespace_name`, `kubernetes`.`pod_name`, `kubernetes`.`container_name`, `level`, COUNT(*) AS cnt")
	assert.Contains(t, sql, "FROM `default`.`k8s_logs`")
	assert.Contains(t, sql, "`time_ms` BETWEEN 1700000000000 AND 1700000060000")
	assert.Contains(t, sql, "LOWER(`level`) IN ('error', 'critical', 'fatal', 'err', 'crit')")
	assert.Contains(t, sql, "`kubernetes`.`namespace_name` = 'kube-system'")
	assert.Contains(t, sql, "`kubernetes`.`pod_name` LIKE 'etcd-%'")
	assert.Contains(t, sql, "GROUP BY `log`, `kubernetes`.`namespace_name`, `kubernetes`.`pod_name`, `kubernetes`.`container_name`, `level`")
	assert.Contains(t, sql, "ORDER BY cnt DESC LIMIT 50")
}

func TestParseHiveResult_Bytes(t *testing.T) {
	// "first" has earlier time_ms, "second" later — after parseHiveResultBytes
	// the slice is sorted newest-first, so second comes first.
	body := hiveQueryResponse{
		Columns: []string{"log", "time_ms", "level"},
		Rows: [][]any{
			{"first", float64(1_700_000_000_000), "INFO"},
			{"second", float64(1_700_000_001_000), "ERROR"},
		},
	}
	raw, err := json.Marshal(body)
	require.NoError(t, err)

	logs, err := parseHiveResultBytes(raw, "time_ms", "log", "level", getHiveTimestampConverter(nil, "time_ms", ""), 0)
	require.NoError(t, err)
	require.Len(t, logs, 2)

	assert.Equal(t, "second", logs[0].Message, "newest first after client-side sort")
	assert.Equal(t, "ERROR", logs[0].Severity)
	assert.True(t, strings.HasPrefix(logs[0].Timestamp, "2023-11-14T"),
		"timestamp should be epoch-ms formatted, got %q", logs[0].Timestamp)

	assert.Equal(t, "first", logs[1].Message)
	assert.Equal(t, "INFO", logs[1].Severity)

	// tsCol/msgCol/sevCol should be excluded from generic labels map.
	_, hasLogLabel := logs[0].Labels["log"]
	_, hasTimeLabel := logs[0].Labels["time_ms"]
	_, hasLevelLabel := logs[0].Labels["level"]
	assert.False(t, hasLogLabel)
	assert.False(t, hasTimeLabel)
	assert.False(t, hasLevelLabel)
}

func TestParseHiveResult_TableQualifiedColumns(t *testing.T) {
	// HiveServer2 sometimes returns column names as "table.col"; the parser
	// should still align tsCol/msgCol to the unqualified suffix.
	body := hiveQueryResponse{
		Columns: []string{"k8s_logs.log", "k8s_logs.time_ms"},
		Rows:    [][]any{{"hello", float64(1_700_000_000_000)}},
	}
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	logs, err := parseHiveResultBytes(raw, "time_ms", "log", "", getHiveTimestampConverter(nil, "time_ms", ""), 0)
	require.NoError(t, err)
	require.Len(t, logs, 1)
	assert.Equal(t, "hello", logs[0].Message)
	assert.NotEmpty(t, logs[0].Timestamp)
}

func TestParseHiveSchemaBytes(t *testing.T) {
	body := hiveSchemaResponse{Columns: []hiveColumnSpec{
		{Name: "log", Type: "string"},
		{Name: "time_ms", Type: "bigint"},
	}}
	raw, err := json.Marshal(body)
	require.NoError(t, err)

	schema, labels, err := parseHiveSchemaBytes(raw)
	require.NoError(t, err)
	require.NotNil(t, schema)
	require.Len(t, labels, 2)
	assert.Equal(t, "log", labels[0].Label)
	assert.Equal(t, "string", labels[0].Attributes["dataType"])
}

func TestParseHiveSchemaBytes_PartitionFlag(t *testing.T) {
	// hive_schema responses (relay-mode) carry the is_partition flag verbatim;
	// the parser must propagate it into OutputLogLabel.Attributes["isPartition"].
	body := hiveSchemaResponse{Columns: []hiveColumnSpec{
		{Name: "log", Type: "string"},
		{Name: "time_ms", Type: "bigint"},
		{Name: "year", Type: "string", IsPartition: true},
		{Name: "month", Type: "string", IsPartition: true},
	}}
	raw, err := json.Marshal(body)
	require.NoError(t, err)

	_, labels, err := parseHiveSchemaBytes(raw)
	require.NoError(t, err)
	require.Len(t, labels, 4)

	assert.Nil(t, labels[0].Attributes["isPartition"], "log is not a partition col")
	assert.Nil(t, labels[1].Attributes["isPartition"], "time_ms is not a partition col")
	assert.Equal(t, true, labels[2].Attributes["isPartition"], "year is a partition col")
	assert.Equal(t, true, labels[3].Attributes["isPartition"], "month is a partition col")
}

func TestParseHiveLabelValuesBytes_SkipsEmpty(t *testing.T) {
	body := hiveQueryResponse{Columns: []string{"stream"}, Rows: [][]any{
		{"stdout"}, {""}, {"stderr"}, {nil},
	}}
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	vals, err := parseHiveLabelValuesBytes(raw)
	require.NoError(t, err)
	got := make([]string, len(vals))
	for i, v := range vals {
		got[i] = v.Value
	}
	assert.Equal(t, []string{"stdout", "stderr"}, got)
}
