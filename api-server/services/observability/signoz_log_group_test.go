package observability

import (
	"strings"
	"testing"

	"nudgebee/services/query"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// signozGroupSeries is one log-group series as SigNoz returns it, labelled with
// whatever keys the query grouped by.
func signozGroupSeries(nsKey, podKey string) map[string]any {
	return map[string]any{
		"labels": map[string]any{
			"severity_text": "ERROR",
			nsKey:           "payments",
			podKey:          "checkout-7d4b9c6f8-xk2p9",
		},
		"points": []any{map[string]any{"timestamp": float64(1700000000000), "value": float64(4)}},
	}
}

func signozGroupResult(series map[string]any) map[string]any {
	return map[string]any{"result": []any{map[string]any{"queryName": "A", "series": []any{series}}}}
}

// Current SigNoz labels series with the dotted OTel keys we now group by; a
// legacy response labelled with the underscored keys must parse identically.
func TestSignozLogGroupParse_DottedAndLegacyKeysAgree(t *testing.T) {
	agent := &SignozSource{}
	for name, keys := range map[string][2]string{
		"current (dotted)":     {signozNamespaceKey, signozPodKey},
		"legacy (underscored)": {signozLegacyNamespaceKey, signozLegacyPodKey},
	} {
		t.Run(name, func(t *testing.T) {
			series := signozGroupSeries(keys[0], keys[1])

			saas, err := parseSignozSaasLogGroupResponse(map[string]any{"data": signozGroupResult(series)}, 1700000000000)
			require.NoError(t, err)
			require.Len(t, saas.Groups, 1)

			relayed, err := agent.parseSignozLogGroupResponse(map[string]any{"data": map[string]any{"data": signozGroupResult(series)}}, 1700000000000)
			require.NoError(t, err)
			require.Len(t, relayed.Groups, 1)

			for _, g := range []LogGroup{saas.Groups[0], relayed.Groups[0]} {
				assert.Equal(t, "payments", g.Namespace)
				assert.Equal(t, "ERROR", g.Level)
				assert.NotEmpty(t, g.Workload)
				assert.Equal(t, "/k8s/payments/"+g.Workload, g.ContainerID)
			}
		})
	}
}

// Filtering by the underscored key matches no row on current SigNoz, which
// empties the whole log-group result — both builders must use the dotted keys.
func TestSignozLogGroupFilters_UseDottedKeys(t *testing.T) {
	req := FetchLogGroupRequest{Request: map[string]any{"selectedNamespace": "payments", "selectedWorkload": "checkout"}}

	for name, filters := range map[string][]map[string]any{
		"user source":  buildSignozSaasLogGroupFilters(req),
		"agent source": (&SignozSource{}).buildLogGroupFilters(req),
	} {
		t.Run(name, func(t *testing.T) {
			keys := map[string]any{}
			for _, f := range filters {
				if k, ok := f["key"].(map[string]string); ok {
					keys[k["key"]] = f["value"]
				}
			}
			assert.Equal(t, "payments", keys[signozNamespaceKey])
			assert.Equal(t, "checkout", keys[signozPodKey])
			assert.NotContains(t, keys, signozLegacyNamespaceKey)
			assert.NotContains(t, keys, signozLegacyPodKey)

			// Typed as resource attributes: untyped, SigNoz resolved k8s.namespace.name
			// to the log attribute k8s event logs carry, and a namespace-filtered count
			// over a namespace with thousands of matching rows came back 0 (v0.145).
			for _, f := range filters[1:] {
				k := f["key"].(map[string]string)
				assert.Equal(t, "resource", k["type"], k["key"])
				assert.Equal(t, "string", k["dataType"], k["key"])
			}
		})
	}
}

// The series shape SigNoz v0.145 returned for the log-group query (captured live):
// points under `values`, each value a numeric STRING. Reading `points` as floats
// gave every group a count of 0, which the Log Groups page then hid.
func TestSignozSeriesPoints_ValuesWithStringNumbers(t *testing.T) {
	series := map[string]any{
		"labels": map[string]any{"k8s.pod.name": "gateway-6c7d8e9f0-zz9yy"},
		"values": []any{
			map[string]any{"timestamp": float64(1791453600000), "value": "1"},
			map[string]any{"timestamp": float64(1791457200000), "value": "2"},
		},
	}
	// Hourly bucket starts 10:00 and 11:00 (ms) for a query ending 11:13:20.
	ts, vals := signozSeriesPoints(series, 1791458000000)
	assert.Equal(t, []int64{1791457200, 1791458000}, ts,
		"Unix seconds, at each bucket's END (11:00), and the last one capped at the query end")
	assert.Equal(t, []float64{1, 2}, vals)

	// Numeric values and the legacy `points` key are read the same way; with no end
	// time the bucket end is not capped.
	ts, vals = signozSeriesPoints(map[string]any{"points": []any{map[string]any{"timestamp": float64(1791453600000), "value": float64(3)}}}, 0)
	assert.Equal(t, []int64{1791457200}, ts)
	assert.Equal(t, []float64{3}, vals)

	// A non-numeric value is skipped rather than counted as 0.
	_, vals = signozSeriesPoints(map[string]any{"values": []any{map[string]any{"timestamp": float64(1791453600000), "value": "n/a"}}}, 0)
	assert.Empty(t, vals)
}

func TestSignozLogGroupParse_CountsFromValuesShape(t *testing.T) {
	series := map[string]any{
		"labels": map[string]any{"severity_text": "ERROR", signozNamespaceKey: "payments", signozPodKey: "checkout-7d4b9c6f8-xk2p9"},
		"values": []any{
			map[string]any{"timestamp": float64(1791453600000), "value": "1"},
			map[string]any{"timestamp": float64(1791457200000), "value": "1"},
		},
	}
	saas, err := parseSignozSaasLogGroupResponse(map[string]any{"data": signozGroupResult(series)}, 1791458000000)
	require.NoError(t, err)
	relayed, err := (&SignozSource{}).parseSignozLogGroupResponse(map[string]any{"data": map[string]any{"data": signozGroupResult(series)}}, 1791458000000)
	require.NoError(t, err)

	for _, out := range []LogGroupOutput{saas, relayed} {
		require.Len(t, out.Groups, 1)
		assert.Equal(t, int64(2), out.Groups[0].Count)
		assert.Equal(t, []float64{1, 1}, out.Groups[0].Values)
		assert.Equal(t, []int64{1791457200, 1791458000}, out.Groups[0].Timestamps, "Unix seconds at bucket ends, last capped at the query end")
	}
}

// A canonical label is looked up under its SigNoz attribute as a resource attribute
// (the caller's tag type described the canonical label); a native key passes as-is.
func TestSignozLabelValueParams(t *testing.T) {
	key, tagType := signozLabelValueParams("service", "tag")
	assert.Equal(t, "service.name", key)
	assert.Equal(t, "resource", tagType)

	key, tagType = signozLabelValueParams("level", "tag")
	assert.Equal(t, "severity_text", key)
	assert.Equal(t, "resource", tagType)

	key, tagType = signozLabelValueParams("http.method", "tag")
	assert.Equal(t, "http.method", key)
	assert.Equal(t, "tag", tagType)
}

// Points missing a timestamp or value, or carrying NaN/Inf, are skipped: a 0
// timestamp would plot at 1970 and int64(NaN) is a garbage count.
func TestSignozSeriesPoints_SkipsUnusablePoints(t *testing.T) {
	ts, vals := signozSeriesPoints(map[string]any{"values": []any{
		map[string]any{"value": "3"},
		map[string]any{"timestamp": float64(1791446400000), "value": "NaN"},
		map[string]any{"timestamp": float64(1791450000000), "value": "+Inf"},
		map[string]any{"timestamp": float64(1791453600000), "value": "2"},
	}}, 0)
	assert.Equal(t, []int64{1791457200}, ts)
	assert.Equal(t, []float64{2}, vals)
}

// Several canonical names read the same SigNoz key (app and workload both read
// k8s.deployment.name). Every one of them must be aliased on every log — the old
// single-valued reverse map picked one at random per request.
func TestNormalizeOutputLogLabels_AliasesEveryCanonicalSharingAKey(t *testing.T) {
	for i := 0; i < 20; i++ {
		logs := []OutputLog{{Labels: map[string]any{"k8s.deployment.name": "checkout", "host.name": "n1", "severity_text": "ERROR"}}}
		normalizeOutputLogLabels(logs, signozLogLabelMapping)
		for _, canonical := range []string{"app", "workload"} {
			assert.Equal(t, "checkout", logs[0].Labels[canonical], "canonical %q", canonical)
		}
		assert.Equal(t, "n1", logs[0].Labels["host"])
		assert.Equal(t, "n1", logs[0].Labels["hostname"])
		assert.Equal(t, "ERROR", logs[0].Labels["level"])
		assert.Equal(t, "ERROR", logs[0].Labels["severity"])
	}
}

// The Log Groups page multiplies every timestamp by 1000, so FetchLogGroup enforces
// Unix seconds for every provider: whatever precision a parser emits, the page gets
// seconds — a slipped unit no longer reaches the screen as 1970 (or year 58000).
func TestLogGroupTimestampSeconds(t *testing.T) {
	const sec = int64(1791457200)
	for name, in := range map[string]int64{
		"seconds":      sec,
		"milliseconds": sec * 1e3,
		"microseconds": sec * 1e6,
		"nanoseconds":  sec * 1e9,
	} {
		assert.Equal(t, sec, logGroupTimestampSeconds(in), name)
	}

	out := LogGroupOutput{Groups: []LogGroup{{Timestamps: []int64{sec * 1e3, sec}}, {Timestamps: []int64{sec * 1e9}}}}
	normalizeLogGroupTimestamps(&out)
	assert.Equal(t, []int64{sec, sec}, out.Groups[0].Timestamps)
	assert.Equal(t, []int64{sec}, out.Groups[1].Timestamps)
}

// Nudgebee shows SigNoz levels lowercased and folded (WARNING -> warn), so the
// filters Nubi and the UI build use those spellings; SigNoz compares severity_text
// case-sensitively against what the shipper stored ("ERROR"). An equality filter
// must match every stored spelling of the level it names.
func TestSignozSeverityFilter(t *testing.T) {
	op, v := signozSeverityFilter(Eq, "error")
	assert.Equal(t, In, op)
	assert.ElementsMatch(t, []any{"error", "ERROR", "Error"}, v)

	op, v = signozSeverityFilter(Eq, "warn")
	assert.Equal(t, In, op)
	assert.ElementsMatch(t, []any{"warn", "WARN", "Warn", "warning", "WARNING", "Warning"}, v)

	op, v = signozSeverityFilter(Nq, "fatal")
	assert.Equal(t, NotIn, op)
	assert.Contains(t, v, "CRITICAL")

	op, v = signozSeverityFilter(In, []any{"info", "ERROR"})
	assert.Equal(t, In, op)
	assert.Subset(t, v, []any{"INFO", "INFORMATION", "error", "ERROR"})

	// Other operators pass through untouched.
	op, v = signozSeverityFilter(Contains, "err")
	assert.Equal(t, Contains, op)
	assert.Equal(t, "err", v)
}

func TestFlattenSignozQuery_SeverityIsCaseInsensitive(t *testing.T) {
	clauses, err := FlattenSignozQuery(query.QueryWhereClause{Binary: query.BinaryWhereClause{signozSeverityKey: {Eq: "error"}}})
	require.NoError(t, err)
	require.Len(t, clauses, 1)
	assert.Equal(t, "in", clauses[0].Op)
	assert.ElementsMatch(t, []any{"error", "ERROR", "Error"}, clauses[0].Value)
}

// The review regressions: an empty or non-string level must reach SigNoz exactly
// as before (it used to panic / fail flattening), and an explicit stored name is
// not widened to its whole display group.
func TestSignozSeverityFilter_EdgeCases(t *testing.T) {
	op, v := signozSeverityFilter(Eq, "")
	assert.Equal(t, Eq, op)
	assert.Equal(t, "", v)

	op, v = signozSeverityFilter(Eq, 17)
	assert.Equal(t, Eq, op)
	assert.Equal(t, 17, v)

	op, v = signozSeverityFilter(Eq, "trace")
	assert.Equal(t, In, op)
	assert.ElementsMatch(t, []any{"trace", "TRACE", "Trace"}, v, "trace stays trace; only displayed names widen")

	op, v = signozSeverityFilter(In, []string{"error"})
	assert.Equal(t, In, op)
	assert.ElementsMatch(t, []any{"error", "ERROR", "Error"}, v)
}

// signozSeverityGroups must agree with what mapSeverity displays, or the filter
// and the screen drift apart again.
func TestSignozSeverityGroupsMatchDisplay(t *testing.T) {
	for displayed, stored := range signozSeverityGroups {
		for _, name := range stored {
			assert.Equal(t, displayed, (&SignozSource{}).mapSeverity(strings.ToUpper(name), 0), "agent source: %q", name)
			assert.Equal(t, displayed, (&SignozSaasSource{}).mapSeverity(strings.ToUpper(name), 0), "user source: %q", name)
		}
	}
}

func TestSignozLogGroupFilters_CountEveryErrorSpelling(t *testing.T) {
	for name, filters := range map[string][]map[string]any{
		"user source":  buildSignozSaasLogGroupFilters(FetchLogGroupRequest{}),
		"agent source": (&SignozSource{}).buildLogGroupFilters(FetchLogGroupRequest{}),
	} {
		assert.Subset(t, filters[0]["value"], []any{"ERROR", "Error", "error", "FATAL", "Critical"}, name)
	}
}

func TestSignozSeverityFilter_StringSliceAndUnicode(t *testing.T) {
	// A []string list with an unusable element is returned as []any, which is the
	// only list type FlattenSignozQuery accepts.
	op, v := signozSeverityFilter(In, []string{"error", ""})
	assert.Equal(t, In, op)
	assert.Equal(t, []any{"error", ""}, v)

	// Title-casing a non-display value is rune-aware.
	_, v = signozSeverityFilter(Eq, "élevé")
	assert.Contains(t, v, "Élevé")
}
