package logql2ch

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
)

// ClickHouse's match and replaceRegexpAll use RE2, as Go's regexp does, so the
// severity-less branch of ErrorLines can be checked here line by line.
func TestErrorLines_LinesWithoutASeverity(t *testing.T) {
	noError := regexp.MustCompile("(?-s)" + noErrorFields)
	words := regexp.MustCompile("(?-s)" + errorWords)
	isError := func(line string) bool { return words.MatchString(noError.ReplaceAllString(line, "")) }

	for _, line := range []string{
		"connection error: dial tcp 10.0.0.1:5432: connect: connection refused",
		`level=info msg="sync failed" err="context deadline exceeded"`,
		`{"msg":"request done","error":"timeout"}`,
		"panic: runtime error: index out of range",
		"java.lang.IllegalStateException: boom",
		"java.lang.OutOfMemoryError: Java heap space",
		"E0101 12:00:00.000000 1 reflector.go:138] failed to list",
		"FATAL could not open file",
		"request done err=<nil> but then a panic",
		// Messages that start with an error word, not fields reporting no error.
		`2026-10-19 10:00:00 UTC [42] ERROR:  null value in column "id" violates not-null constraint`,
		"ERROR: none of the pods are ready",
		"error: nil map assignment",
		"2 errors occurred while syncing",
		"Errored out while calling backend",
	} {
		assert.True(t, isError(line), line)
	}
	for _, line := range []string{
		`level=info msg="request done" err=<nil>`,
		`time=2026-10-19 msg=ok error=<nil> latency=3ms`,
		`level=info msg=ok err=null`,
		`{"msg":"request done","error":null}`,
		`{"msg":"request done","err": null}`,
		"Preferred node chosen for OrderResult",
		"writing to stderr",
	} {
		assert.False(t, isError(line), line)
	}
}

func TestErrorLines_SQL(t *testing.T) {
	sql := ErrorLines().sql
	assert.Contains(t, sql, "SeverityNumber >= 17 OR (SeverityNumber = 0 AND match(replaceRegexpAll(Body, '(?-s)")
	// The literal escapes each backslash for ClickHouse, as quote does for every literal.
	assert.Contains(t, sql, `\\berr(or)?`)
}
