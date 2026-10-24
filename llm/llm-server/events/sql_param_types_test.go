package events

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	sqlLiteral = regexp.MustCompile("(?s)`[^`]*`")
	sqlParam   = regexp.MustCompile(`\$(\d+)(::[a-z]+)?`)
)

// A statement parameter cast once as $N::uuid has the type uuid in the whole statement, so a bare $N elsewhere
// in it no longer compares with text: "operator does not exist: text = uuid". The statement fails on every call,
// and a caller that treats the error as "nothing found" silently loses the row (e.g. a run resolved with
// cause_key null because NewestPlanOfEvent ordered by `a.event_id::text = $2` after filtering on `$2::uuid`).
// Every statement in the remediation packages uses a uuid parameter either always cast or never.
func TestSQLParamsCastToUUIDEverywhereOrNowhere(t *testing.T) {
	var files []string
	for _, dir := range []string{".", "../api"} {
		matches, err := filepath.Glob(filepath.Join(dir, "*.go"))
		require.NoError(t, err)
		for _, f := range matches {
			if !strings.HasSuffix(f, "_test.go") {
				files = append(files, f)
			}
		}
	}
	require.NotEmpty(t, files)
	var mixed []string
	for _, f := range files {
		src, err := os.ReadFile(f)
		require.NoError(t, err)
		for _, lit := range sqlLiteral.FindAllString(string(src), -1) {
			casts := map[string]map[string]bool{}
			for _, m := range sqlParam.FindAllStringSubmatch(lit, -1) {
				if casts[m[1]] == nil {
					casts[m[1]] = map[string]bool{}
				}
				casts[m[1]][m[2]] = true
			}
			for n, seen := range casts {
				if seen["::uuid"] && seen[""] {
					mixed = append(mixed, f+": $"+n+" in "+firstLine(lit))
				}
			}
		}
	}
	sort.Strings(mixed)
	assert.Empty(t, mixed, "cast these parameters to ::uuid at every use (or at none)")
}

func firstLine(s string) string {
	s = strings.TrimSpace(strings.Trim(s, "`"))
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 100 {
		s = s[:100]
	}
	return s
}
