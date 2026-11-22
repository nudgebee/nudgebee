package logql2ch

// errorWords make a line without a severity an error line: whole words only, so
// "Preferred" or "stderr" do not count, class names ending in Error or Exception
// (IllegalStateException), and glog's leading "E0101".
const errorWords = `(?i)\b(err|errors|errored|fatal|panic|critical|crit|\w*error|\w*exception)\b|^E[0-9]{4}`

// noErrorFields are logfmt and JSON fields that report no error: err=<nil>,
// error=nil, "error":null. They are dropped before errorWords is matched, so a line
// whose only error word sits in one of them is not an error line. Only field syntax
// counts: "ERROR: none of the pods are ready" is an error message, not a field.
const noErrorFields = `(?i)\berr(or)?=(<nil>|(nil|null|none)\b)|"err(or)?"\s*:\s*null\b`

// ErrorLines matches the lines log grouping treats as errors: a record whose severity
// is error or above, or a line without a severity whose text keeps an error word once
// the fields reporting no error are dropped. The level label's own text match is
// looser (it reads err=<nil> as an error), which suits a filter a user writes but
// fills a fixed-size fetch with lines that are not errors.
func ErrorLines() Cond {
	return Cond{sql: "(SeverityNumber >= 17 OR (SeverityNumber = 0 AND match(replaceRegexpAll(Body, " +
		quote("(?-s)"+noErrorFields) + ", ''), " + quote("(?-s)"+errorWords) + ")))"}
}
