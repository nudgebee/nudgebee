package recommendation

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A recommendation row's status has two owners. The scanner owns Open and
// Archive; the user owns Dismissed/snoozed, InProgress and Closed. Every
// producer that re-scans has to respect that split in two places at once, and
// getting only one of them right still reopens dismissed findings:
//
//   - the archive step may tombstone Open rows, or rows whose object vanished
//     from the scan, but must not sweep a still-present row out of a user-owned
//     state on its way to being re-upserted;
//   - the upsert must never reclaim status with a bare EXCLUDED.status, because
//     by then the archive has already moved the row to Archive.
//
// This has been fixed three times in individual producers and regressed each
// time a new one was written, so these tests assert the invariant over the
// whole service tree rather than over the handful of call sites known today.
// A new producer that gets it wrong fails here without anyone remembering to
// add a case for it.
//
// The patterns are whitespace- and case-tolerant on purpose. Matching SQL
// literally would let a producer formatted even slightly differently slip
// through unexamined, which is worse than having no test: it would report
// success while checking nothing.

var (
	// recommendationConflictRe identifies an upsert against the recommendation
	// table: the tuple is that table's unique index, so any statement
	// conflicting on it is writing a recommendation row.
	recommendationConflictRe = regexp.MustCompile(
		`(?i)ON\s+CONFLICT\s*\(\s*rule_name\s*,\s*cloud_account_id\s*,\s*resource_id\s*,\s*category\s*,\s*account_object_id\s*\)`)

	// bareStatusAssignRe is the assignment that reopens dismissed findings.
	bareStatusAssignRe = regexp.MustCompile(`(?i)status\s*=\s*\(?\s*EXCLUDED\.status\s*\)?`)

	// archiveStatementRe finds an archive UPDATE written as a literal.
	archiveStatementRe = regexp.MustCompile(`(?i)UPDATE\s+recommendation\s+SET\s+status\s*=\s*'Archive'`)

	// sweepPredicateRe is the archive predicate that tombstones user-owned rows.
	sweepPredicateRe = regexp.MustCompile(`(?i)status\s*(!=|<>)\s*'Archive'`)
)

// statusGuard is the only legal way to assign status in a recommendation upsert.
const statusGuard = "status = CASE WHEN recommendation.status NOT IN ('Open', 'Archive')"

// These floors keep the tests honest. A static check that silently stops
// matching anything passes forever while verifying nothing, so assert that the
// walk still finds roughly the population of call sites we know exists. Raise
// them when producers are added; a failure here means either the regexes drifted
// or the code moved, and both are worth a look.
const (
	minUpsertSites      = 6
	minArchiveSites     = 5
	minFailedApplySites = 2
)

// goSourceFiles returns every non-test .go file under the services tree.
func goSourceFiles(t *testing.T) map[string]string {
	t.Helper()
	// Tests run with the package directory as CWD, so ".." is services/.
	root, err := filepath.Abs("..")
	require.NoError(t, err)

	files := map[string]string{}
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "vendor" || d.Name() == "node_modules" || strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = path
		}
		files[rel] = string(content)
		return nil
	})
	require.NoError(t, err)
	require.NotEmpty(t, files, "walked the services tree and found no Go sources")
	return files
}

// statementWindow returns the SQL statement starting at idx, bounded by the end
// of its raw string literal so a later statement in the same file cannot
// satisfy or trip an assertion meant for this one.
func statementWindow(content string, idx, size int) string {
	window := content[idx:min(idx+size, len(content))]
	if end := strings.Index(window, "`"); end > 0 {
		window = window[:end]
	}
	return window
}

// TestRecommendationUpsertsNeverReclaimStatus fails if any producer assigns
// status straight from EXCLUDED in a recommendation upsert. That is the exact
// statement that reopened dismissed findings: the archive step has already
// moved the row to Archive, so EXCLUDED.status lands it back on Open with
// is_dismissed stranded at true.
func TestRecommendationUpsertsNeverReclaimStatus(t *testing.T) {
	found := 0
	for name, content := range goSourceFiles(t) {
		for _, match := range recommendationConflictRe.FindAllStringIndex(content, -1) {
			found++
			window := statementWindow(content, match[0], 900)
			assert.NotRegexpf(t, bareStatusAssignRe, window,
				"%s: recommendation upsert assigns status from EXCLUDED, which reopens dismissed findings.\n"+
					"Use: %s\n                THEN recommendation.status ELSE EXCLUDED.status END", name, statusGuard)
		}
	}
	assert.GreaterOrEqualf(t, found, minUpsertSites,
		"only found %d recommendation upserts; the pattern has probably drifted and this test is no longer checking anything", found)
}

// TestRecommendationArchivesDoNotSweepUserOwnedStates fails if an archive
// statement selects rows by "anything not already archived". That predicate
// tombstones Dismissed/InProgress/Closed rows on the way to re-upserting them,
// which defeats the upsert's CASE guard: by the time the guard runs, the row
// reads as Archive and is treated as scanner-owned.
//
// Legal alternatives are archiving Open rows only, or archiving by the scan's
// keep-set so only rows whose object genuinely vanished are retired.
//
// A decommission path that deliberately retires everything an account owns is
// exempt, because nothing re-upserts those rows afterwards. It opts out with an
// "archive-all-statuses:" comment stating why, so the exemption is a decision
// on the record rather than a filename in an allow-list here.
func TestRecommendationArchivesDoNotSweepUserOwnedStates(t *testing.T) {
	const optOut = "archive-all-statuses:"

	found := 0
	for name, content := range goSourceFiles(t) {
		for _, match := range archiveStatementRe.FindAllStringIndex(content, -1) {
			found++
			// Look back far enough to catch the marker on the statement or on
			// the few lines introducing it.
			if strings.Contains(content[max(0, match[0]-400):match[0]], optOut) {
				continue
			}
			assert.NotRegexpf(t, sweepPredicateRe, statementWindow(content, match[0], 700),
				"%s: archive sweeps every non-archived row, including user-owned states.\n"+
					"Archive Open rows only, or archive by the scan's keep-set "+
					"(status NOT IN ('Archive', 'Closed') AND NOT (account_object_id = ANY($n))).", name)
		}
	}
	assert.GreaterOrEqualf(t, found, minArchiveSites,
		"only found %d recommendation archive statements; the pattern has probably drifted and this test is no longer checking anything", found)
}

// TestFailedApplyNeverDismissesARecommendation fails if any code maps a failed
// resolution outcome onto the Dismissed status.
//
// Dismissed means a person decided not to do this. A failed apply means the
// platform could not do it — the opposite claim about the same row, and a far
// more damaging one to record, because every guarded upsert now pins
// user-owned statuses and no re-scan can lift it. An IAM denial or a throttled
// call would delete a live savings opportunity permanently.
//
// The coordinator already answers this correctly in projectRecommendation
// (Failed on an InProgress row hands it back as Open, for retry) and refuses
// the transition outright in legalDismissal. This asserts nobody re-derives
// that mapping by hand next to it, which is how the api-server ended up with
// three copies of it.
func TestFailedApplyNeverDismissesARecommendation(t *testing.T) {
	failedCaseRe := regexp.MustCompile(`(?i)case\s+adapter\.RecommendationResolutionStatusFailed\s*:`)

	found := 0
	for name, content := range goSourceFiles(t) {
		for _, match := range failedCaseRe.FindAllStringIndex(content, -1) {
			found++
			assert.NotContainsf(t, caseBody(content, match[1]), "RecommendationStatusDismissed",
				"%s: a failed apply is recorded as Dismissed.\n"+
					"Dismissed is the user's decision not to act, and no re-scan can reopen it, so a "+
					"failed apply recorded this way deletes the recommendation for good.\n"+
					"Settle the resolution through coordinator.SettleResolution and let "+
					"projectRecommendation decide — it returns a failed attempt to Open for retry.", name)
		}
	}
	assert.GreaterOrEqualf(t, found, minFailedApplySites,
		"only found %d failed-outcome branches; the pattern has probably drifted and this test is no longer checking anything", found)
}

// caseBody returns the body of the switch case whose label ends at start,
// bounded by the next label at the same nesting or the close of the switch.
//
// Scanning the whole case rather than a fixed window is the point: a comment
// explaining the branch is enough to push an assignment outside any character
// budget, which would leave this test passing while checking nothing. The
// boundary is found by indentation rather than a hardcoded depth so a switch
// inside a closure or a goroutine is bounded just as tightly — guessing the
// depth would over-scan into unrelated code and report a failure against the
// wrong file.
func caseBody(content string, start int) string {
	indent := labelIndent(content, start)
	body := content[start:]

	for offset := 0; ; {
		nl := strings.IndexByte(body[offset:], '\n')
		if nl < 0 {
			return body
		}
		lineStart := offset + nl + 1
		line := body[lineStart:]
		if eol := strings.IndexByte(line, '\n'); eol >= 0 {
			line = line[:eol]
		}
		trimmed := strings.TrimLeft(line, " \t")
		// A line indented deeper than the label is inside this case.
		if trimmed != "" && len(line)-len(trimmed) <= len(indent) {
			if strings.HasPrefix(trimmed, "case ") || strings.HasPrefix(trimmed, "default:") || strings.HasPrefix(trimmed, "}") {
				return body[:lineStart]
			}
		}
		offset = lineStart
	}
}

// labelIndent returns the leading whitespace of the line containing pos.
func labelIndent(content string, pos int) string {
	lineStart := strings.LastIndexByte(content[:pos], '\n') + 1
	line := content[lineStart:pos]
	return line[:len(line)-len(strings.TrimLeft(line, " \t"))]
}
