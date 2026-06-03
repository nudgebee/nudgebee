package observability

import (
	"strings"
	"testing"
)

// Regression for the "every Log Group shows the same Last Time" bug: the SELECT must
// include MAX(<tsCol>) AS max_ts so per-group last-seen is fetched from Pinot. Without
// it the parser falls back to the query-window end time for every row.
func TestBuildPinotLogGroupSQL_IncludesMaxTs(t *testing.T) {
	cols := pinotLogGroupCols{
		Table:        "k8s_logs",
		TsCol:        "ts",
		MsgCol:       "log",
		NsCol:        "namespace",
		PodCol:       "pod",
		ContainerCol: "container",
	}
	sql := buildPinotLogGroupSQL(cols, pinotTsMode{ScaleFactor: 1}, 1_700_000_000_000, 1_700_000_060_000, "", "", 100)

	if !strings.Contains(sql, `MAX("ts") AS max_ts`) {
		t.Fatalf("expected SELECT to include MAX(\"ts\") AS max_ts, got: %s", sql)
	}
	// max_ts is an aggregate — it must NOT appear in the GROUP BY clause.
	groupBy := sql[strings.Index(sql, "GROUP BY"):]
	if strings.Contains(groupBy, "max_ts") {
		t.Fatalf("max_ts must not appear in GROUP BY clause, got: %s", groupBy)
	}
}

func TestParsePinotLogGroupBytes_UsesPerRowMaxTs_NumericMs(t *testing.T) {
	// tsCol values are epoch-ms LONG (ScaleFactor=1)
	data := []byte(`{
		"resultTable": {
			"dataSchema": {
				"columnNames": ["log", "namespace", "pod", "container", "cnt", "max_ts"],
				"columnDataTypes": ["STRING","STRING","STRING","STRING","LONG","LONG"]
			},
			"rows": [
				["msg-A", "argocd", "argocd-repo-server-0",          "main", 23, 1700000050000],
				["msg-B", "argocd", "argocd-application-controller-0","main",  5, 1700000010000]
			]
		}
	}`)
	cols := pinotLogGroupCols{Table: "k8s_logs", TsCol: "ts", MsgCol: "log", NsCol: "namespace", PodCol: "pod", ContainerCol: "container"}
	out, err := parsePinotLogGroupBytes(data, cols, pinotTsMode{ScaleFactor: 1}, 1_700_000_060_000)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if len(out.Groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(out.Groups))
	}
	// max_ts ms → unix seconds.
	if got := out.Groups[0].Timestamps[0]; got != 1_700_000_050 {
		t.Errorf("group[0] timestamp: expected 1700000050, got %d", got)
	}
	if got := out.Groups[1].Timestamps[0]; got != 1_700_000_010 {
		t.Errorf("group[1] timestamp: expected 1700000010, got %d", got)
	}
	// The two rows must NOT share the same timestamp.
	if out.Groups[0].Timestamps[0] == out.Groups[1].Timestamps[0] {
		t.Errorf("both groups got the same timestamp %d — bug not fixed", out.Groups[0].Timestamps[0])
	}
}

func TestParsePinotLogGroupBytes_UsesPerRowMaxTs_NumericSeconds(t *testing.T) {
	// tsCol values are epoch-s (ScaleFactor=1000)
	data := []byte(`{
		"resultTable": {
			"dataSchema": {
				"columnNames": ["log","namespace","pod","container","cnt","max_ts"],
				"columnDataTypes": ["STRING","STRING","STRING","STRING","LONG","LONG"]
			},
			"rows": [["m","ns","pod-0","c", 1, 1700000050]]
		}
	}`)
	cols := pinotLogGroupCols{Table: "t", TsCol: "ts", MsgCol: "log", NsCol: "namespace", PodCol: "pod", ContainerCol: "container"}
	out, err := parsePinotLogGroupBytes(data, cols, pinotTsMode{ScaleFactor: 1000}, 1_700_000_060_000)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if got := out.Groups[0].Timestamps[0]; got != 1_700_000_050 {
		t.Errorf("expected 1700000050 unix s, got %d", got)
	}
}

func TestParsePinotLogGroupBytes_UsesPerRowMaxTs_StringISO(t *testing.T) {
	// tsCol is a STRING ISO-8601 column.
	data := []byte(`{
		"resultTable": {
			"dataSchema": {
				"columnNames": ["log","namespace","pod","container","cnt","max_ts"],
				"columnDataTypes": ["STRING","STRING","STRING","STRING","LONG","STRING"]
			},
			"rows": [["m","ns","pod-0","c", 1, "2023-11-14T22:14:10Z"]]
		}
	}`)
	cols := pinotLogGroupCols{Table: "t", TsCol: "ts", MsgCol: "log", NsCol: "namespace", PodCol: "pod", ContainerCol: "container"}
	out, err := parsePinotLogGroupBytes(data, cols, pinotTsMode{IsString: true, GoFormat: "2006-01-02T15:04:05Z07:00"}, 1_700_000_060_000)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if got := out.Groups[0].Timestamps[0]; got != 1_700_000_050 {
		t.Errorf("expected ISO string parsed to 1700000050, got %d", got)
	}
}

func TestParsePinotLogGroupBytes_FallsBackToEndTime_WhenMaxTsMissing(t *testing.T) {
	// Defensive: if the agent build is older and the response omits max_ts,
	// the parser must fall back to endTime rather than 0 / now.
	data := []byte(`{
		"resultTable": {
			"dataSchema": {
				"columnNames": ["log","namespace","pod","container","cnt"],
				"columnDataTypes": ["STRING","STRING","STRING","STRING","LONG"]
			},
			"rows": [["m","ns","pod-0","c", 1]]
		}
	}`)
	cols := pinotLogGroupCols{Table: "t", TsCol: "ts", MsgCol: "log", NsCol: "namespace", PodCol: "pod", ContainerCol: "container"}
	out, err := parsePinotLogGroupBytes(data, cols, pinotTsMode{ScaleFactor: 1}, 1_700_000_060_000)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	// endTime was epoch-ms; fallback converts to seconds.
	if got := out.Groups[0].Timestamps[0]; got != 1_700_000_060 {
		t.Errorf("expected fallback to endTime 1700000060 s, got %d", got)
	}
}
