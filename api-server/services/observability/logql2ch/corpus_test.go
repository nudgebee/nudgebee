package logql2ch

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestCorpus_WellFormed(t *testing.T) {
	entries := loadCorpus(t)
	seen := map[string]bool{}
	covered := map[string]bool{}
	for _, e := range entries {
		if e.ID == "" || seen[e.ID] {
			t.Errorf("missing or duplicate id %q", e.ID)
		}
		seen[e.ID] = true
		if e.LogQL == "" || e.Source == "" || len(e.Features) == 0 {
			t.Errorf("%s: logql, source and features are required", e.ID)
		}
		for _, f := range e.Features {
			covered[f] = true
		}
		if e.Parity != "" && !strings.HasPrefix(e.Parity, "known-diff: ") {
			t.Errorf("%s: parity must be empty or start with \"known-diff: \"", e.ID)
		}
		knownDiff := strings.HasPrefix(e.Parity, "known-diff: ")
		if knownDiff && (e.ExpectLoki == nil || e.ExpectCH == nil) {
			t.Errorf("%s: a known-diff entry must pin expect_loki and expect_ch", e.ID)
		}
		if !knownDiff && (e.ExpectLoki != nil || e.ExpectCH != nil) {
			t.Errorf("%s: only a known-diff entry may set expect_loki or expect_ch", e.ID)
		}
		switch e.Status {
		case "v1":
			if e.Where == "" || e.Error != "" {
				t.Errorf("%s: a v1 entry needs where and no error", e.ID)
			}
			switch e.Kind {
			case "logs":
				if e.Series != "" || e.Agg != "" || e.Step != "" {
					t.Errorf("%s: a logs entry has no series, agg or step", e.ID)
				}
				if e.Direction != "" && e.Direction != "forward" && e.Direction != "backward" {
					t.Errorf("%s: direction must be forward or backward", e.ID)
				}
			case "metric":
				if e.Series == "" || (e.Agg != "count" && e.Agg != "rate") {
					t.Errorf("%s: a metric entry needs series and agg count|rate", e.ID)
				}
				// metricSQL divides by the step in whole seconds, so a
				// sub-second step would divide by zero.
				if d, err := time.ParseDuration(e.Step); err != nil {
					t.Errorf("%s: step %q is not a duration", e.ID, e.Step)
				} else if d < time.Second || d%time.Second != 0 {
					t.Errorf("%s: step %q must be a whole number of seconds, at least 1s", e.ID, e.Step)
				}
			default:
				t.Errorf("%s: kind must be logs or metric", e.ID)
			}
		case "unsupported":
			if e.Error == "" || e.Where != "" || e.Kind != "" {
				t.Errorf("%s: an unsupported entry needs error and no where/kind", e.ID)
			}
		default:
			t.Errorf("%s: status must be v1 or unsupported", e.ID)
		}
	}
	for _, f := range requiredFeatures {
		if !covered[f] {
			t.Errorf("no corpus entry covers feature %q", f)
		}
	}
	for f := range covered {
		if !slices.Contains(requiredFeatures, f) {
			t.Errorf("unknown feature tag %q: add it to requiredFeatures", f)
		}
	}
}

func TestAliases(t *testing.T) {
	aliases := loadAliases(t)
	for _, label := range []string{"namespace", "pod", "container", "app", "job", "node_name", "stream", "filename", "service_name", "level"} {
		if aliases[label] == "" {
			t.Errorf("alias map lacks %q", label)
		}
	}
	// The unknown-label entry carries the exact message the translator returns.
	for _, e := range loadCorpus(t) {
		if slices.Contains(e.Features, "label-unknown") {
			if want := unknownLabelError("instance").Error(); e.Error != want {
				t.Errorf("%s: error\n %q\nwant\n %q", e.ID, e.Error, want)
			}
		}
	}
}

// corpusEntry is one LogQL form Nudgebee emits (see testdata/corpus.yaml).
type corpusEntry struct {
	ID        string   `yaml:"id"`
	Source    string   `yaml:"source"`   // where the LogQL comes from (file:line)
	Features  []string `yaml:"features"` // tags from requiredFeatures
	LogQL     string   `yaml:"logql"`
	Status    string   `yaml:"status"`    // v1 | unsupported
	Kind      string   `yaml:"kind"`      // v1: logs | metric
	Where     string   `yaml:"where"`     // v1: SQL conditions ANDed after the time bounds
	Direction string   `yaml:"direction"` // logs: backward (default) | forward
	Limit     int      `yaml:"limit"`     // logs: default 5000 (api-server's cap)
	Series    string   `yaml:"series"`    // metric: SQL rendering the Loki label set
	Agg       string   `yaml:"agg"`       // metric: count | rate
	Step      string   `yaml:"step"`      // metric: range and step (must be equal)
	Error     string   `yaml:"error"`     // unsupported: the translator's message
	Parity    string   `yaml:"parity"`    // "" | "known-diff: <why>"
	// A known-diff entry pins the size of the difference: how many lines
	// (or metric points) Loki and ClickHouse each return.
	ExpectLoki *int   `yaml:"expect_loki"`
	ExpectCH   *int   `yaml:"expect_ch"`
	Notes      string `yaml:"notes"`
}

func (e corpusEntry) limit() int {
	if e.Limit > 0 {
		return e.Limit
	}
	return 5000
}

// requiredFeatures is every LogQL feature the inventory found (plus the
// unsupported ones the translator must refuse). Each needs a corpus entry.
var requiredFeatures = []string{
	"selector-eq", "selector-neq", "selector-re", "selector-nre",
	"re-alternation", "re-case-insensitive", "re-anchored", "backtick-string", "string-escape",
	"line-contains", "line-not-contains", "line-re", "line-not-re", "limit-direction",
	"label-alias-app", "label-alias-job", "label-alias-node_name", "label-alias-stream",
	"label-alias-filename", "label-alias-service_name", "label-alias-level", "label-unknown",
	"parser-json", "parser-logfmt", "label-filter-string", "label-filter-numeric",
	"count_over_time", "rate", "sum", "sum-by", "metric-stream-labels",
	"line_format", "label_format", "pattern", "regexp", "unwrap", "topk", "quantile_over_time",
	"offset", "range-ne-step", "metric-comparison", "bytes_over_time", "absent_over_time", "invalid-syntax",
}

func loadCorpus(t testing.TB) []corpusEntry {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "corpus.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		Entries []corpusEntry `yaml:"entries"`
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		t.Fatalf("corpus.yaml: %v", err)
	}
	return c.Entries
}

func loadAliases(t *testing.T) map[string]string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "label_aliases.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var a struct {
		Aliases map[string]string `yaml:"aliases"`
	}
	if err := yaml.Unmarshal(b, &a); err != nil {
		t.Fatalf("label_aliases.yaml: %v", err)
	}
	return a.Aliases
}
