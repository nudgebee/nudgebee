package hiveclient

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFlattenColumns_Primitive(t *testing.T) {
	in := []ColumnSpec{{Name: "log", Type: "string"}}
	out := FlattenColumns(in)
	assert.Equal(t, in, out, "primitive column passes through unchanged")
}

func TestFlattenColumns_SimpleStruct(t *testing.T) {
	in := []ColumnSpec{
		{Name: "kubernetes", Type: "struct<pod_name:string,namespace_name:string>"},
	}
	out := FlattenColumns(in)
	names := make([]string, len(out))
	for i, c := range out {
		names[i] = c.Name
	}
	assert.Equal(t, []string{
		"kubernetes",
		"kubernetes.pod_name",
		"kubernetes.namespace_name",
	}, names)
	assert.Equal(t, "string", out[1].Type)
	assert.Equal(t, "string", out[2].Type)
}

func TestFlattenColumns_NestedStruct(t *testing.T) {
	in := []ColumnSpec{
		{Name: "k", Type: "struct<a:string,b:struct<c:int,d:string>>"},
	}
	out := FlattenColumns(in)
	names := make([]string, len(out))
	for i, c := range out {
		names[i] = c.Name
	}
	assert.Equal(t, []string{
		"k",
		"k.a",
		"k.b",
		"k.b.c",
		"k.b.d",
	}, names)
}

func TestFlattenColumns_ArrayOfStruct(t *testing.T) {
	in := []ColumnSpec{
		{Name: "events", Type: "array<struct<ts:bigint,msg:string>>"},
	}
	out := FlattenColumns(in)
	names := make([]string, len(out))
	for i, c := range out {
		names[i] = c.Name
	}
	assert.Equal(t, []string{
		"events",
		"events.ts",
		"events.msg",
	}, names)
}

func TestFlattenColumns_PrimitiveArrayAndMap_NoExpansion(t *testing.T) {
	in := []ColumnSpec{
		{Name: "tags", Type: "array<string>"},
		{Name: "props", Type: "map<string,int>"},
	}
	out := FlattenColumns(in)
	assert.Equal(t, in, out, "primitive arrays and maps are not expanded")
}

func TestFlattenColumns_Mixed(t *testing.T) {
	in := []ColumnSpec{
		{Name: "log", Type: "string"},
		{Name: "kubernetes", Type: "struct<pod_name:string,namespace_name:string>"},
		{Name: "time_ms", Type: "bigint"},
		{Name: "year", Type: "string", IsPartition: true},
	}
	out := FlattenColumns(in)
	names := make([]string, len(out))
	parts := make([]bool, len(out))
	for i, c := range out {
		names[i] = c.Name
		parts[i] = c.IsPartition
	}
	assert.Equal(t, []string{
		"log",
		"kubernetes",
		"kubernetes.pod_name",
		"kubernetes.namespace_name",
		"time_ms",
		"year",
	}, names)
	// Partition flag propagates to leaves of partition struct columns.
	assert.Equal(t, []bool{false, false, false, false, false, true}, parts)
}

func TestFlattenColumns_PartitionStructLeavesInheritFlag(t *testing.T) {
	in := []ColumnSpec{
		{Name: "p", Type: "struct<a:string>", IsPartition: true},
	}
	out := FlattenColumns(in)
	assert.True(t, out[0].IsPartition)
	assert.True(t, out[1].IsPartition, "leaf inherits parent partition flag")
}

// Pathological deeply-nested struct: ensure recursion stops at
// maxFlattenDepth (10) and doesn't blow the stack. We build a 30-level
// struct chain — way past the limit.
func TestFlattenColumns_DepthLimit(t *testing.T) {
	deepType := "int"
	for i := 0; i < 30; i++ {
		deepType = "struct<a:" + deepType + ">"
	}
	in := []ColumnSpec{{Name: "deep", Type: deepType}}
	out := FlattenColumns(in)
	// Top-level column + at most maxFlattenDepth child leaves emitted before
	// recursion bails. We don't assert an exact count because the helper
	// emits the named field at each level — what matters is that recursion
	// returns rather than diverging.
	assert.GreaterOrEqual(t, len(out), 2)
	assert.LessOrEqual(t, len(out), maxFlattenDepth*2+2, "recursion bounded")
	assert.Equal(t, "deep", out[0].Name)
}

func TestFlattenColumns_MalformedTypeFallback(t *testing.T) {
	// Unbalanced brackets — stripWrapper requires the trailing '>'. Without
	// it the type isn't expanded, but the column is still returned as-is.
	in := []ColumnSpec{{Name: "broken", Type: "struct<a:string"}}
	out := FlattenColumns(in)
	assert.Equal(t, in, out)
}

func TestSplitTopLevel_BracketAware(t *testing.T) {
	got := splitTopLevel("a:int,b:struct<c:int,d:int>,e:string", ',')
	assert.Equal(t, []string{
		"a:int",
		"b:struct<c:int,d:int>",
		"e:string",
	}, got)
}

func TestQuoteIdent(t *testing.T) {
	assert.Equal(t, "`log`", QuoteIdent("log"))
	assert.Equal(t, "`kubernetes`.`namespace_name`", QuoteIdent("kubernetes.namespace_name"))
	assert.Equal(t, "`a``b`", QuoteIdent("a`b"))
}

// Verifies FlattenColumns produces the exact suggestion list the Hive form
// should show for the existing k8s_logs table in the user's cluster.
// Shape pulled from the Hive metastore on 2026-05-21:
//
//	log         string
//	stream      string
//	time        string
//	kubernetes  struct<pod_name:string,namespace_name:string,pod_id:string,host:string,container_name:string,docker_id:string>
//	time_ms     bigint
//	year, month, day, hour  (partition cols, all string)
func TestFlattenColumns_K8sLogsRealShape(t *testing.T) {
	in := []ColumnSpec{
		{Name: "log", Type: "string"},
		{Name: "stream", Type: "string"},
		{Name: "time", Type: "string"},
		{Name: "kubernetes", Type: "struct<pod_name:string,namespace_name:string,pod_id:string,host:string,container_name:string,docker_id:string>"},
		{Name: "time_ms", Type: "bigint"},
		{Name: "year", Type: "string", IsPartition: true},
		{Name: "month", Type: "string", IsPartition: true},
		{Name: "day", Type: "string", IsPartition: true},
		{Name: "hour", Type: "string", IsPartition: true},
	}
	out := FlattenColumns(in)

	names := make([]string, len(out))
	for i, c := range out {
		names[i] = c.Name
	}
	assert.Equal(t, []string{
		"log",
		"stream",
		"time",
		"kubernetes",
		"kubernetes.pod_name",
		"kubernetes.namespace_name",
		"kubernetes.pod_id",
		"kubernetes.host",
		"kubernetes.container_name",
		"kubernetes.docker_id",
		"time_ms",
		"year",
		"month",
		"day",
		"hour",
	}, names)

	// Partition flag set only on year/month/day/hour and never bled onto
	// the regular columns or the kubernetes struct leaves.
	for _, c := range out {
		switch c.Name {
		case "year", "month", "day", "hour":
			assert.True(t, c.IsPartition, "%s should be partition", c.Name)
		default:
			assert.False(t, c.IsPartition, "%s should NOT be partition", c.Name)
		}
	}
}

// Verifies the DESCRIBE parser dedupes partition columns. Hive emits each
// partition column twice — first as a regular row, then again after the
// "# Partition Information" divider. We must end up with one entry per
// column with IsPartition=true on the partition keys.
func TestParseDescribeResult_DedupesPartitionColumns(t *testing.T) {
	r := &RawResult{
		Columns: []string{"col_name", "data_type", "comment"},
		Rows: [][]any{
			{"log", "string", ""},
			{"stream", "string", ""},
			{"time", "string", ""},
			{"kubernetes", "struct<pod_name:string,namespace_name:string>", ""},
			{"time_ms", "bigint", ""},
			// Partition columns appear first in the main column list...
			{"year", "string", ""},
			{"month", "string", ""},
			{"day", "string", ""},
			{"hour", "string", ""},
			// ...then again here.
			{"", "", ""},
			{"# Partition Information", "", ""},
			{"# col_name", "data_type", "comment"},
			{"year", "string", ""},
			{"month", "string", ""},
			{"day", "string", ""},
			{"hour", "string", ""},
		},
	}
	out := parseDescribeResult(r)

	names := make([]string, len(out))
	parts := make([]bool, len(out))
	for i, c := range out {
		names[i] = c.Name
		parts[i] = c.IsPartition
	}
	assert.Equal(t, []string{
		"log", "stream", "time", "kubernetes", "time_ms",
		"year", "month", "day", "hour",
	}, names, "no duplicates")
	assert.Equal(t, []bool{
		false, false, false, false, false,
		true, true, true, true,
	}, parts, "year/month/day/hour marked partition")
}

func TestParseDescribeResult_PartitionOnlyInPartitionSection(t *testing.T) {
	// Edge case: a partition column that doesn't appear in the main section
	// (rare but possible with custom Hive output). It should still be emitted.
	r := &RawResult{
		Columns: []string{"col_name", "data_type"},
		Rows: [][]any{
			{"log", "string"},
			{"# Partition Information", ""},
			{"# col_name", "data_type"},
			{"region", "string"},
		},
	}
	out := parseDescribeResult(r)
	require.Len(t, out, 2)
	assert.Equal(t, "log", out[0].Name)
	assert.False(t, out[0].IsPartition)
	assert.Equal(t, "region", out[1].Name)
	assert.True(t, out[1].IsPartition)
}

func TestParseHostPort(t *testing.T) {
	cases := []struct {
		in         string
		wantHost   string
		wantPort   int
		wantUseTLS bool
		wantErr    bool
	}{
		{"host:10000", "host", 10000, false, false},
		{"hive://h:10001/foo", "h", 10001, false, false},
		{"thrift://10.0.0.5:10000?retry=3", "10.0.0.5", 10000, false, false},
		{"http://localhost:10000", "localhost", 10000, false, false},
		{"https://hs2.internal:10000", "hs2.internal", 10000, true, false},
		{"hive+ssl://h:10001", "h", 10001, true, false},
		{"HTTPS://CAPS:10000", "CAPS", 10000, true, false},
		{"", "", 0, false, true},
		{"hostonly", "", 0, false, true},
		{":10000", "", 0, false, true},
		{"host:abc", "", 0, false, true},
	}
	for _, tc := range cases {
		host, port, useTLS, err := ParseHostPort(tc.in)
		if tc.wantErr {
			assert.Error(t, err, "input=%q", tc.in)
			continue
		}
		assert.NoError(t, err, "input=%q", tc.in)
		assert.Equal(t, tc.wantHost, host, "host for %q", tc.in)
		assert.Equal(t, tc.wantPort, port, "port for %q", tc.in)
		assert.Equal(t, tc.wantUseTLS, useTLS, "useTLS for %q", tc.in)
	}
}
