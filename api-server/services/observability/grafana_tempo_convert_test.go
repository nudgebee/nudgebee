package observability

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const tempoSearchFixture = `{"traces":[{"traceID":"5b8efff798038103d269b633813fc60c","rootServiceName":"frontend",
 "spanSet":{"spans":[{"spanID":"eee19b7ec3c1b174","name":"GET /checkout","startTimeUnixNano":"1790626201857133800","durationNanos":"150000000","attributes":[]}]},
 "spanSets":[{"spans":[
  {"spanID":"eee19b7ec3c1b174","name":"GET /checkout","startTimeUnixNano":"1790626201857133800","durationNanos":"150000000","attributes":[
   {"key":"service.name","value":{"stringValue":"frontend"}},
   {"key":"k8s.namespace.name","value":{"stringValue":"demo"}},
   {"key":"k8s.pod.name","value":{"stringValue":"frontend-abc"}},
   {"key":"net.peer.name","value":{"stringValue":"payments"}},
   {"key":"peer.service","value":{"stringValue":"nil"}},
   {"key":"http.status_code","value":{"intValue":"503"}},
   {"key":"http.target","value":{"stringValue":"/checkout"}},
   {"key":"status","value":{"stringValue":"error"}},
   {"key":"kind","value":{"stringValue":"server"}},
   {"key":"span:parentID","value":{"stringValue":"0000000000000000"}}]},
  {"spanID":"ddba5d0cdcc241d7","name":"GET /charge","startTimeUnixNano":"1790626201859133800","durationNanos":"40000000","attributes":[
   {"key":"service.name","value":{"stringValue":"payments"}},
   {"key":"http.status_code","value":{"stringValue":"nil"}},
   {"key":"status","value":{"stringValue":"ok"}},
   {"key":"kind","value":{"stringValue":"client"}},
   {"key":"span:parentID","value":{"stringValue":"eee19b7ec3c1b174"}}]}
 ]}]}]}`

func TestTempoSearchToSpans(t *testing.T) {
	spans, err := tempoSearchToSpans([]byte(tempoSearchFixture))
	require.NoError(t, err)
	require.Len(t, spans, 2, "a span present in both spanSet and spanSets is returned once")

	root := spans[0]
	assert.Equal(t, "5b8efff798038103d269b633813fc60c", root.TraceID)
	assert.Equal(t, "eee19b7ec3c1b174", root.SpanID)
	assert.Equal(t, "", root.ParentSpanID, "an all-zero parent id means root")
	assert.Equal(t, "GET /checkout", root.SpanName)
	assert.Equal(t, "SPAN_KIND_SERVER", root.SpanKind)
	assert.Equal(t, "frontend", root.ServiceName)
	assert.Equal(t, "frontend", root.WorkloadName)
	assert.Equal(t, "demo", root.WorkloadNamespace)
	assert.Equal(t, "payments", root.DestinationWorkload)
	assert.Equal(t, "payments", root.DestinationName)
	assert.Equal(t, "503", root.HTTPStatusCode)
	assert.Equal(t, "STATUS_CODE_ERROR", root.StatusCode)
	assert.Equal(t, "/checkout", root.Resource)
	assert.Equal(t, int64(150000000), root.DurationNs)
	assert.Equal(t, "grafana_tempo", root.TraceSource)
	assert.Equal(t, "frontend-abc", root.ResourceAttributes["k8s.pod.name"])
	assert.NotContains(t, root.SpanAttributes, "peer.service", `"nil" is Tempo's missing-value marker`)
	assert.NotContains(t, root.SpanAttributes, "status")
	ms, err := ParseDateToMillis(root.Timestamp)
	require.NoError(t, err)
	assert.Equal(t, int64(1790626201857), ms)

	child := spans[1]
	assert.Equal(t, "eee19b7ec3c1b174", child.ParentSpanID)
	assert.Equal(t, "", child.HTTPStatusCode)
	assert.Equal(t, "STATUS_CODE_OK", child.StatusCode)
	assert.Equal(t, "SPAN_KIND_CLIENT", child.SpanKind)
}

const tempoLegacySpanSetFixture = `{"traces":[{"traceID":"5b8efff798038103d269b633813fc60c","rootServiceName":"frontend",
 "spanSet":{"spans":[{"spanID":"eee19b7ec3c1b174","name":"GET /checkout","startTimeUnixNano":"1790626201857133800","durationNanos":"150000000","attributes":[]}]},
 "spanSets":[{"spans":[
  {"spanID":"ddba5d0cdcc241d7","name":"GET /charge","startTimeUnixNano":"1790626201859133800","durationNanos":"40000000","attributes":[]}
 ]}]}]}`

func TestTempoSearchToSpans_MergesLegacySpanSet(t *testing.T) {
	spans, err := tempoSearchToSpans([]byte(tempoLegacySpanSetFixture))
	require.NoError(t, err)
	require.Len(t, spans, 2, "a span present only in the legacy spanSet must not be dropped")

	ids := []string{spans[0].SpanID, spans[1].SpanID}
	assert.Contains(t, ids, "ddba5d0cdcc241d7", "span present only in spanSets")
	assert.Contains(t, ids, "eee19b7ec3c1b174", "span present only in the legacy spanSet")
}

const tempoOTLPFixture = `{"trace":{"resourceSpans":[{"resource":{"attributes":[
  {"key":"service.name","value":{"stringValue":"payments"}},{"key":"k8s.namespace.name","value":{"stringValue":"demo"}}]},
 "scopeSpans":[{"scope":{},"spans":[{"traceId":"W47/95gDgQPSabYzgT/GDA==","spanId":"3bpdDNzCQdc=","parentSpanId":"7uGbfsPBsXQ=",
  "name":"GET /charge","kind":"SPAN_KIND_CLIENT","startTimeUnixNano":"1790626201859133800","endTimeUnixNano":"1790626201899133800",
  "attributes":[{"key":"http.status_code","value":{"intValue":"503"}}],
  "status":{"code":"STATUS_CODE_ERROR","message":"upstream returned 503"},
  "events":[{"name":"exception","timeUnixNano":"1790626201860000000","attributes":[{"key":"exception.message","value":{"stringValue":"boom"}}]}]}]}]}]}}`

func TestTempoOTLPToHeatmap(t *testing.T) {
	out, err := tempoOTLPToHeatmap([]byte(tempoOTLPFixture))
	require.NoError(t, err)
	require.Len(t, out, 1)
	s := out[0]
	assert.Equal(t, "5b8efff798038103d269b633813fc60c", s.TraceID, "trace-by-id ids are base64 and must be decoded to hex")
	assert.Equal(t, "ddba5d0cdcc241d7", s.SpanID)
	assert.Equal(t, "payments", s.ServiceName)
	assert.Equal(t, "demo", s.ResourceAttributes["k8s.namespace.name"])
	assert.Equal(t, "STATUS_CODE_ERROR", s.StatusCode)
	assert.Equal(t, int64(40000000), s.DurationNs)
	assert.Equal(t, "503", s.SpanAttributes["http.status_code"])
	assert.Equal(t, []string{"exception"}, s.EventsName)
	assert.Equal(t, "boom", s.EventsAttributes[0]["exception.message"])
}

func TestTempoOTLPToHeatmap_EmptyTrace(t *testing.T) {
	out, err := tempoOTLPToHeatmap([]byte(`{"trace":{},"metrics":{}}`))
	require.NoError(t, err)
	assert.Empty(t, out)
}

func TestParseTempoMetricSeries(t *testing.T) {
	body := `{"series":[
	 {"labels":[{"key":"resource.service.name","value":{"stringValue":"frontend"}},{"key":"p","value":{"doubleValue":0.95}}],"value":0.51},
	 {"labels":[{"key":"resource.service.name","value":{"stringValue":"ledger"}}],"value":36}]}`
	series, err := parseTempoMetricSeries([]byte(body))
	require.NoError(t, err)
	require.Len(t, series, 2)
	assert.Equal(t, "0.95", series[0].Labels["p"])
	assert.Equal(t, 0.51, series[0].Value)
	assert.Equal(t, "ledger", series[1].Labels["resource.service.name"])
	assert.Equal(t, 36.0, series[1].Value)
}

// Search drops leading zeros from ids (30-char trace ids are common live); trace-by-id
// decodes to the full width, so search ids are padded to match the waterfall and logs.
const tempoShortIDFixture = `{"traces":[{"traceID":"7ae0e7eb7a1ce404397ccd0f613f11",
 "spanSets":[{"spans":[{"spanID":"ba5d0cdcc241d7","startTimeUnixNano":"1790626201859133800","durationNanos":"40000000","attributes":[
   {"key":"name","value":{"stringValue":"GET /charge"}},
   {"key":"span:parentID","value":{"stringValue":"e19b7ec3c1b174"}}]}]}]}]}`

func TestTempoSearchToSpans_PadsIDs(t *testing.T) {
	spans, err := tempoSearchToSpans([]byte(tempoShortIDFixture))
	require.NoError(t, err)
	require.Len(t, spans, 1)
	assert.Equal(t, "007ae0e7eb7a1ce404397ccd0f613f11", spans[0].TraceID)
	assert.Equal(t, "00ba5d0cdcc241d7", spans[0].SpanID)
	assert.Equal(t, "00e19b7ec3c1b174", spans[0].ParentSpanID)
}

func TestTempoSearchToSpans_SpanNameFromSelectedAttribute(t *testing.T) {
	spans, err := tempoSearchToSpans([]byte(tempoShortIDFixture))
	require.NoError(t, err)
	require.Len(t, spans, 1)
	assert.Equal(t, "GET /charge", spans[0].SpanName, "a span without a top-level name falls back to the selected name attribute")
	assert.NotContains(t, spans[0].SpanAttributes, "name")
}
