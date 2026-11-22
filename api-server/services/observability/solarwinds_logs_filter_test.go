package observability

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"testing"

	"nudgebee/services/query"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// resetSWPodNamespaceCache empties the process-wide pod → namespace cache, which the tests
// below would otherwise share (they all use the same fake token).
func resetSWPodNamespaceCache() {
	swPodNamespaceCache.Lock()
	swPodNamespaceCache.entries = map[string]swPodNamespaceEntry{}
	swPodNamespaceCache.Unlock()
}

// swFilterTestRows serves /v1/logs with one row per pod (program = the pod's first word)
// and /v1/entities with the given pod namespaces. It records the search filter sent.
func swFilterTestRows(t *testing.T, pods []string, namespaces map[string]string) (*string, func()) {
	t.Helper()
	restoreConfigs := stubSWConfigs()
	resetSWPodNamespaceCache()
	orig := solarWindsDoGET
	var filter string
	solarWindsDoGET = func(apiToken, baseURL, path string, params map[string]string) ([]byte, int, error) {
		if path == swTestEntitiesP {
			var entities []map[string]any
			for pod, ns := range namespaces {
				if strings.Contains(params["search"], `"`+pod+`"`) {
					entities = append(entities, map[string]any{"name": pod, "attributes": map[string]any{"namespaceName": ns}})
				}
			}
			body, _ := json.Marshal(map[string]any{"entities": entities})
			return body, http.StatusOK, nil
		}
		filter = params["filter"]
		logs := make([]map[string]any, 0, len(pods))
		for _, pod := range pods {
			logs = append(logs, map[string]any{
				"time": "2026-10-07T18:20:00Z", "hostname": pod, "program": strings.SplitN(pod, "-", 2)[0],
				"message": "msg from " + pod, "severity": "INFO",
			})
		}
		body, _ := json.Marshal(map[string]any{"logs": logs})
		return body, http.StatusOK, nil
	}
	return &filter, func() { solarWindsDoGET = orig; restoreConfigs() }
}

func swQueryHosts(t *testing.T, where query.QueryWhereClause) []string {
	t.Helper()
	out, err := (&SolarWindsLogSource{}).QueryLogs(mockRequestContext(), FetchLogRequest{
		AccountId:    "acc-1",
		QueryRequest: LogsQueryBuilderRequest{Where: where},
	})
	require.NoError(t, err)
	hosts := make([]string, 0, len(out))
	for _, l := range out {
		hosts = append(hosts, l.Labels["hostname"].(string))
	}
	sort.Strings(hosts)
	return hosts
}

func swBin(field string, op query.BinaryWhereClauseType, val any) query.QueryWhereClause {
	return query.QueryWhereClause{Binary: query.BinaryWhereClause{field: {op: val}}}
}

// _like used to drop its anchors: "canary%" searched host:canary and returned every
// loki-canary pod; "%-0" returned 87 hosts. The search stays a superset and the rows are
// checked against the pattern.
func TestQueryLogs_LikeAnchorsCheckedOnRows(t *testing.T) {
	pods := []string{"canary-web-0", "loki-canary-4xwzv", "loki-0", "web-0-x"}
	filter, restore := swFilterTestRows(t, pods, nil)
	defer restore()

	assert.Equal(t, []string{"canary-web-0"}, swQueryHosts(t, swBin("hostname", query.Like, "canary%")))
	assert.Equal(t, "host:canary", *filter)
	assert.Equal(t, []string{"canary-web-0", "loki-0"}, swQueryHosts(t, swBin("hostname", query.Like, "%-0")))
	assert.Equal(t, []string{"loki-canary-4xwzv"}, swQueryHosts(t, swBin("hostname", query.ILike, "LOKI-C%")))
	assert.Equal(t, []string{"loki-0", "web-0-x"}, swQueryHosts(t, swBin("hostname", query.NLike, "%canary-%")))
}

// _contains is case-sensitive; SolarWinds' term is not.
func TestQueryLogs_ContainsCaseCheckedOnRows(t *testing.T) {
	_, restore := swFilterTestRows(t, []string{"loki-0", "LOKI-1"}, nil)
	defer restore()

	assert.Equal(t, []string{"LOKI-1"}, swQueryHosts(t, swBin("hostname", query.Contains, "LOKI")))
	assert.Equal(t, []string{"LOKI-1", "loki-0"}, swQueryHosts(t, swBin("hostname", query.IContains, "LOKI")))
}

// A case-sensitive or anchored pattern on a field the rows do not carry cannot be checked,
// so it is refused instead of answered as a case-insensitive substring.
func TestBuildSWBinaryClause_UncheckablePatternsRefused(t *testing.T) {
	for _, tc := range []struct {
		op  query.BinaryWhereClauseType
		val string
	}{
		{query.Contains, "nudge"},
		{query.Like, "%nudge%"},
		{query.ILike, "nudge%"},
		{query.ILike, "nudgebee"},
		{query.NLike, "%nudge%"},
	} {
		_, err := buildSWBinaryClause("k8s.namespace.name", tc.op, tc.val)
		assert.Error(t, err, "%s %s", tc.op, tc.val)
	}
	for _, tc := range []struct {
		op  query.BinaryWhereClauseType
		val string
	}{
		{query.IContains, "nudge"},
		{query.NIContains, "nudge"},
		{query.ILike, "%nudge%"},
	} {
		result, err := buildSWBinaryClause("k8s.namespace.name", tc.op, tc.val)
		assert.NoError(t, err, "%s %s", tc.op, tc.val)
		assert.Contains(t, result, "k8s.namespace.name:nudge")
	}
}

// k8s.namespace.name:"nudgebee" also returns nudgebee-agent-dev pods (live: 58 hosts, 22
// really in nudgebee). The rows are checked against each pod's namespace.
func TestQueryLogs_NamespaceEqualityIsExact(t *testing.T) {
	pods := []string{"api-7d9f8c6b5-abcde", "agent-6c4b9d7f8-fghjk", "node-ip-10-0-0-1"}
	namespaces := map[string]string{"api-7d9f8c6b5-abcde": "shop", "agent-6c4b9d7f8-fghjk": "shop-agent"}
	filter, restore := swFilterTestRows(t, pods, namespaces)
	defer restore()

	// node-ip-10-0-0-1 has no pod entity: unknown, so the search's answer stands.
	assert.Equal(t, []string{"api-7d9f8c6b5-abcde", "node-ip-10-0-0-1"}, swQueryHosts(t, swBin("k8s.namespace.name", query.Eq, "shop")))
	assert.Equal(t, `k8s.namespace.name:"shop"`, *filter)

	// Not-equal must not over-exclude: the search sends no namespace exclusion at all.
	assert.Equal(t, []string{"agent-6c4b9d7f8-fghjk", "node-ip-10-0-0-1"}, swQueryHosts(t, swBin("k8s.namespace.name", query.Nq, "shop")))
	assert.Equal(t, "", *filter)
	assert.Equal(t, []string{"agent-6c4b9d7f8-fghjk", "node-ip-10-0-0-1"},
		swQueryHosts(t, swBin("k8s.namespace.name", query.NotIn, []any{"shop", "other"})))
}

// workload "loki" also matched loki-canary and loki-gateway pods; ownership is read from
// the pod name instead.
func TestQueryLogs_WorkloadEqualityIsExact(t *testing.T) {
	pods := []string{"loki-0", "loki-canary-4xwzv", "loki-gateway-75d5f46c-c56xr", "loki-7d9f8c6b5-bcdfg"}
	filter, restore := swFilterTestRows(t, pods, nil)
	defer restore()

	assert.Equal(t, []string{"loki-0", "loki-7d9f8c6b5-bcdfg"}, swQueryHosts(t, swBin(swMapField("workload"), query.Eq, "loki")))
	assert.Contains(t, *filter, `k8s.statefulset.name:"loki"`)
	assert.Equal(t, []string{"loki-canary-4xwzv", "loki-gateway-75d5f46c-c56xr"}, swQueryHosts(t, swBin(swMapField("app"), query.Nq, "loki")))
	assert.Equal(t, "", *filter)
	assert.Equal(t, []string{"loki-canary-4xwzv"}, swQueryHosts(t, swBin("k8s.daemonset.name", query.Eq, "loki-canary")))
}

func TestSWPodOwnedBy(t *testing.T) {
	cases := []struct {
		pod, workload, field string
		want                 bool
	}{
		{"loki-0", "loki", swWorkloadField, true},
		{"loki-canary-4xwzv", "loki", swWorkloadField, false},
		{"loki-canary-4xwzv", "loki-canary", swWorkloadField, true},
		{"loki-gateway-75d5f46c-c56xr", "loki", swWorkloadField, false},
		{"loki-gateway-75d5f46c-c56xr", "loki-gateway", swWorkloadField, true},
		{"services-server-6997c4cb96-7m7xl", "services-server", "k8s.deployment.name", true},
		{"services-server-6997c4cb96-7m7xl", "services-server", "k8s.statefulset.name", false},
		{"calico-node-678m7", "calico-node", "k8s.daemonset.name", true},
		{"loki-0", "loki", "k8s.deployment.name", false},
		{"loki", "loki", swWorkloadField, false},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, swPodOwnedBy(tc.pod, tc.workload, tc.field), "%s / %s / %s", tc.pod, tc.workload, tc.field)
	}
}

// An OR across a checkable and an uncheckable condition keeps rows the search matched on
// the uncheckable side.
func TestQueryLogs_UncheckableConditionsDeferToSearch(t *testing.T) {
	_, restore := swFilterTestRows(t, []string{"web-0", "db-0"}, nil)
	defer restore()

	where := query.QueryWhereClause{Or: []query.QueryWhereClause{
		swBin("k8s.node.name", query.Eq, "node-a"),
		swBin("hostname", query.Eq, "web-0"),
	}}
	assert.Equal(t, []string{"db-0", "web-0"}, swQueryHosts(t, where))
}

// trace_id is refused by the builder, so it must not be advertised as filterable.
func TestSolarWindsLogs_TraceIDNotAdvertised(t *testing.T) {
	src := &SolarWindsLogSource{}
	assert.NotContains(t, canonicalLogFieldNamesFor(src, src.GetLabelMapping()), "trace_id")
	labels := buildLogLabels(resolvableCanonicalLogFields(src, src.GetLabelMapping()), src.GetLabelMapping(), nil)
	for _, l := range labels {
		assert.NotEqual(t, "trace_id", l.Label)
	}
}

// "not contains 100%" must not search -(message:100): that also drops "took 100 ms".
func TestBuildSWBinaryClause_WildcardInContainsValueIsNotExact(t *testing.T) {
	result, err := buildSWBinaryClause("message", query.NIContains, "100%")
	require.NoError(t, err)
	assert.Equal(t, "", result)
	result, err = buildSWBinaryClause("message", query.NIContains, "timeout")
	require.NoError(t, err)
	assert.Equal(t, "-(message:timeout)", result)
}

// A pod name SolarWinds knows in two namespaces says nothing about which one a row is
// from, so it is left unknown rather than resolved to whichever came last.
func TestQuerySWPodNamespacesFor_AmbiguousNameIsUnknown(t *testing.T) {
	orig := solarWindsDoGET
	defer func() { solarWindsDoGET = orig }()
	solarWindsDoGET = func(apiToken, baseURL, path string, params map[string]string) ([]byte, int, error) {
		return []byte(`{"entities":[
			{"name":"redis-0","attributes":{"namespaceName":"a"}},
			{"name":"redis-0","attributes":{"namespaceName":"b"}},
			{"name":"web-0","attributes":{"namespaceName":"a"}}]}`), http.StatusOK, nil
	}
	got, err := querySWPodNamespacesFor("token", swTestBaseURL, []string{"redis-0", "web-0"})
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"web-0": "a"}, got)
}
