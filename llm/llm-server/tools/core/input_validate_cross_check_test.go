//go:build cross_check

// Cross-check harness: replays sampled prod tool calls through ValidateToolInput
// and prints a per-tool safe-to-allowlist verdict. Never runs in normal `go test`.
//
//	CROSS_CHECK_DB_URL + CROSS_CHECK_ACCOUNT_ID required; optional CROSS_CHECK_{DAYS,LIMIT,REPORT}.
//	go test -tags=cross_check -run TestCrossCheckTools ./tools/core/
package core_test

import (
	"database/sql"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"

	_ "nudgebee/llm/tools" // side-effect: register all system tool factories
	toolcore "nudgebee/llm/tools/core"

	_ "github.com/lib/pq"
)

const (
	defaultDays  = 30
	defaultLimit = 5000
)

type sampleRow struct {
	ToolName   string
	Status     string
	Parameters string
}

type bucket struct {
	validator string // "pass" | "fail" | "skip-no-schema"
	prod      string // success | fail | error | other
}

type toolStats struct {
	name             string
	totalSampled     int
	tested           int
	skippedNoSchema  int
	bySituation      map[bucket]int
	exampleFP        []string // input strings — validator-fail × prod-success
	exampleTrueCatch []string // input strings — validator-fail × prod-fail/error
	exampleFPMsg     []string // validator messages for FP cases
}

func newToolStats(name string) *toolStats {
	return &toolStats{name: name, bySituation: map[bucket]int{}}
}

func TestCrossCheckTools(t *testing.T) {
	dbURL := os.Getenv("CROSS_CHECK_DB_URL")
	if dbURL == "" {
		t.Skip("CROSS_CHECK_DB_URL not set; skipping cross-check harness")
	}
	accountID := os.Getenv("CROSS_CHECK_ACCOUNT_ID")
	if accountID == "" {
		t.Skip("CROSS_CHECK_ACCOUNT_ID not set; skipping cross-check harness")
	}
	days := envIntOrDefault("CROSS_CHECK_DAYS", defaultDays)
	limit := envIntOrDefault("CROSS_CHECK_LIMIT", defaultLimit)
	reportPath := os.Getenv("CROSS_CHECK_REPORT")

	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		t.Fatalf("ping db: %v", err)
	}

	rows, err := db.Query(`
        SELECT tool_name, status, COALESCE(parameters, '') AS parameters
        FROM llm_conversation_tool_calls
        WHERE created_at > now() - ($1 || ' days')::interval
          AND tool_type = 'tool'
          AND tool_name IS NOT NULL
          AND tool_name <> ''
          AND parameters IS NOT NULL
          AND parameters LIKE '{%'
          AND status IN ('success', 'fail', 'error')
        ORDER BY random()
        LIMIT $2
    `, strconv.Itoa(days), limit)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()

	var samples []sampleRow
	for rows.Next() {
		var r sampleRow
		if err := rows.Scan(&r.ToolName, &r.Status, &r.Parameters); err != nil {
			t.Fatalf("scan: %v", err)
		}
		samples = append(samples, r)
	}
	t.Logf("loaded %d JSON-shaped leaf-tool calls from last %d days", len(samples), days)

	// Aggregate per tool
	stats := map[string]*toolStats{}
	unregistered := map[string]int{}

	for _, r := range samples {
		tool, ok := toolcore.GetNBTool(accountID, r.ToolName)
		if !ok || tool == nil {
			unregistered[r.ToolName]++
			continue
		}

		s := stats[r.ToolName]
		if s == nil {
			s = newToolStats(r.ToolName)
			stats[r.ToolName] = s
		}
		s.totalSampled++

		schema := tool.InputSchema()
		if len(schema.Properties) == 0 && len(schema.Required) == 0 {
			s.bySituation[bucket{"skip-no-schema", normalizeStatus(r.Status)}]++
			s.skippedNoSchema++
			continue
		}

		s.tested++
		var verdict, msg string
		if v := toolcore.ValidateToolInput(tool, r.Parameters); v != nil {
			verdict = "fail"
			msg = *v
		} else {
			verdict = "pass"
		}
		key := bucket{verdict, normalizeStatus(r.Status)}
		s.bySituation[key]++

		if verdict == "fail" {
			if r.Status == "success" && len(s.exampleFP) < 2 {
				s.exampleFP = append(s.exampleFP, truncate(r.Parameters, 140))
				s.exampleFPMsg = append(s.exampleFPMsg, firstLine(msg))
			} else if r.Status != "success" && len(s.exampleTrueCatch) < 2 {
				s.exampleTrueCatch = append(s.exampleTrueCatch, truncate(r.Parameters, 140))
			}
		}
	}

	// Verdict per tool
	type toolReport struct {
		name      string
		stats     *toolStats
		verdict   string
		falsePos  int
		trueCatch int
	}
	reports := make([]toolReport, 0, len(stats))
	for name, s := range stats {
		fp := s.bySituation[bucket{"fail", "success"}]
		tc := s.bySituation[bucket{"fail", "fail"}] + s.bySituation[bucket{"fail", "error"}]
		var verdict string
		switch {
		case s.tested == 0:
			verdict = "no-data" // only schema-skipped samples observed
		case fp > 0:
			verdict = "needs-schema-fix"
		case tc > 0:
			verdict = "safe-and-helpful"
		default:
			verdict = "safe-no-effect"
		}
		reports = append(reports, toolReport{name: name, stats: s, verdict: verdict, falsePos: fp, trueCatch: tc})
	}
	sort.Slice(reports, func(i, j int) bool {
		// surface needs-schema-fix first, then helpful, then no-effect
		order := map[string]int{"needs-schema-fix": 0, "safe-and-helpful": 1, "safe-no-effect": 2, "no-data": 3}
		if order[reports[i].verdict] != order[reports[j].verdict] {
			return order[reports[i].verdict] < order[reports[j].verdict]
		}
		// within bucket, larger sample first
		return reports[i].stats.totalSampled > reports[j].stats.totalSampled
	})

	// Render report
	var b strings.Builder
	fmt.Fprintf(&b, "# Tool input validator — cross-check report\n\n")
	fmt.Fprintf(&b, "Sampled %d JSON-shaped leaf-tool calls from the last %d days.\n", len(samples), days)
	fmt.Fprintf(&b, "Account context: `%s`. Limit: %d.\n\n", accountID, limit)

	fmt.Fprintf(&b, "## Verdict summary\n\n")
	fmt.Fprintf(&b, "| Tool | Verdict | Sampled | Tested | False+ | True catches | Schema-skipped |\n")
	fmt.Fprintf(&b, "|---|---|---|---|---|---|---|\n")
	for _, r := range reports {
		fmt.Fprintf(&b, "| `%s` | **%s** | %d | %d | %d | %d | %d |\n",
			r.name, r.verdict, r.stats.totalSampled, r.stats.tested, r.falsePos, r.trueCatch, r.stats.skippedNoSchema)
	}

	if len(unregistered) > 0 {
		fmt.Fprintf(&b, "\n## Tool names seen in DB but not registered (custom tools or factory errors)\n\n")
		names := make([]string, 0, len(unregistered))
		for n := range unregistered {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			fmt.Fprintf(&b, "- `%s` (%d samples)\n", n, unregistered[n])
		}
	}

	fmt.Fprintf(&b, "\n## Examples per tool\n\n")
	for _, r := range reports {
		if len(r.stats.exampleFP) == 0 && len(r.stats.exampleTrueCatch) == 0 {
			continue
		}
		fmt.Fprintf(&b, "### `%s` (%s)\n\n", r.name, r.verdict)
		if len(r.stats.exampleFP) > 0 {
			fmt.Fprintf(&b, "**False positives** — validator rejected, prod succeeded:\n\n")
			for i, ex := range r.stats.exampleFP {
				fmt.Fprintf(&b, "- input: `%s`\n", ex)
				if i < len(r.stats.exampleFPMsg) {
					fmt.Fprintf(&b, "  - validator says: %s\n", r.stats.exampleFPMsg[i])
				}
			}
			fmt.Fprintln(&b)
		}
		if len(r.stats.exampleTrueCatch) > 0 {
			fmt.Fprintf(&b, "**True catches** — validator rejected, prod failed too:\n\n")
			for _, ex := range r.stats.exampleTrueCatch {
				fmt.Fprintf(&b, "- input: `%s`\n", ex)
			}
			fmt.Fprintln(&b)
		}
	}

	// Always log to stdout via t.Log so the report appears in the test output.
	for _, line := range strings.Split(b.String(), "\n") {
		t.Log(line)
	}

	if reportPath != "" {
		if err := os.WriteFile(reportPath, []byte(b.String()), 0o644); err != nil {
			t.Logf("failed to write report file: %v", err)
		} else {
			t.Logf("report written to %s", reportPath)
		}
	}

	// Discovery harness: assert nothing, just surface the report.
}

func normalizeStatus(s string) string {
	switch s {
	case "success", "fail", "error":
		return s
	default:
		return "other"
	}
}

func envIntOrDefault(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func truncate(s string, n int) string {
	var count int
	for i := range s {
		if count == n {
			return s[:i] + "..."
		}
		count++
	}
	return s
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
