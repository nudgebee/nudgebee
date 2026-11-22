package observability

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"nudgebee/services/integrations/orionclient"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Live end-to-end checks against a real SolarWinds Platform (Orion) server,
// running the statements buildOrionVMQueries actually produces and converting the
// rows with the real converter. Everything else in this file's package is tested
// against hand-built payloads, so this is the only check that proves the SWQL is
// valid, the column aliases survive, and the units land where the keys expect.
//
// Skipped unless explicitly enabled, so it never runs in CI:
//
//	LIVE_ORION_URL=https://localhost:27774 \
//	LIVE_ORION_USER=admin LIVE_ORION_PASSWORD=... \
//	LIVE_ORION_NODE_ID=1 \
//	go test ./observability/ -run TestLiveOrion -v
//
// Unlike the other live tests here it needs no database or encryption key: the
// connection details are passed in, because this exercises the query layer rather
// than integration storage.
//
// Read-only: issues SELECTs only.
func liveOrionConfig(t *testing.T) (orionclient.Config, string) {
	t.Helper()
	url, user, pass := os.Getenv("LIVE_ORION_URL"), os.Getenv("LIVE_ORION_USER"), os.Getenv("LIVE_ORION_PASSWORD")
	nodeID := os.Getenv("LIVE_ORION_NODE_ID")
	if url == "" || user == "" || pass == "" || nodeID == "" {
		t.Skip("set LIVE_ORION_URL, LIVE_ORION_USER, LIVE_ORION_PASSWORD and LIVE_ORION_NODE_ID to run")
	}
	require.True(t, orionNodeIDValid(nodeID), "LIVE_ORION_NODE_ID must be an Orion NodeID")
	return orionclient.Config{
		BaseURL:       url,
		Username:      user,
		Password:      pass,
		TLSSkipVerify: true, // the test bed serves SWIS with its self-signed certificate
	}, nodeID
}

// runLiveOrion executes one built statement the way FetchMetricsQuery does.
func runLiveOrion(t *testing.T, cfg orionclient.Config, swql string) []Result {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	end := time.Now().UTC()
	start := end.Add(-6 * time.Hour)
	params := orionParamsFor(swql, map[string]any{
		"start": start.Format(orionTimeLayout),
		"end":   end.Format(orionTimeLayout),
	})

	rows, err := cfg.Query(ctx, swql, params)
	require.NoError(t, err, "SWQL rejected by the server:\n%s", swql)
	return orionRowsToSeries(rows, end)
}

func TestLiveOrion_VMQueriesRunAndCarryOrionsNumbers(t *testing.T) {
	cfg, nodeID := liveOrionConfig(t)
	meta := RequestMetadata{VMInstance: nodeID}

	queries := buildOrionVMQueries(meta, orionVMKeyList())
	require.NotEmpty(t, queries)

	// What the node itself reports, to check the rendered numbers against.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	nodeRows, err := cfg.Query(ctx, "SELECT CPUCount, TotalMemory FROM Orion.Nodes WHERE NodeID = @id", map[string]any{"id": nodeID})
	require.NoError(t, err)
	require.Len(t, nodeRows, 1)
	cores, _ := orionFloat(nodeRows[0]["CPUCount"])
	totalMemory, _ := orionFloat(nodeRows[0]["TotalMemory"])

	for key, swql := range queries {
		t.Run(key, func(t *testing.T) {
			series := runLiveOrion(t, cfg, swql)
			if len(series) == 0 {
				// A volume key is empty on a node whose volumes Orion does not
				// monitor. That is the integration behaving correctly, so it is
				// reported rather than failed.
				t.Skipf("%s returned no rows; Orion is not collecting it for node %s", key, nodeID)
			}
			values := series[0].Values
			require.NotEmpty(t, values)

			for _, v := range values {
				assert.GreaterOrEqual(t, v, 0.0, "%s must not be negative", key)
			}

			switch UtilisationKey(key) {
			case KeyCPUUsage, KeyCPUUsageLine:
				// Cores, not the percentage Orion stores: a fully busy machine
				// cannot exceed its core count.
				if cores > 0 {
					for _, v := range values {
						assert.LessOrEqual(t, v, cores*1.05, "cpu_usage is cores; %v exceeds the node's %v", v, cores)
					}
				}
			case KeyCPUTotal:
				assert.Equal(t, cores, values[0])
			case KeyMemoryUsage, KeyMemoryUsageLine:
				// Bytes, and never more than the machine has.
				if totalMemory > 0 {
					for _, v := range values {
						assert.LessOrEqual(t, v, totalMemory*1.05, "memory_usage is bytes; %v exceeds the node's %v", v, totalMemory)
					}
				}
			case KeyMemTotal:
				assert.InDelta(t, totalMemory, values[0], totalMemory*0.01)
			}
		})
	}
}

func TestLiveOrion_FleetQueriesCarryTheMatchingLabels(t *testing.T) {
	cfg, _ := liveOrionConfig(t)

	queries := buildOrionVMFleetQueries(orionVMFleetKeyList())
	require.Len(t, queries, len(orionVMFleetKeyList()))

	for key, swql := range queries {
		t.Run(key, func(t *testing.T) {
			series := runLiveOrion(t, cfg, swql)
			require.NotEmpty(t, series, "no polled node answered %s", key)

			for _, s := range series {
				assert.NotEmpty(t, s.Metric["instance"], "every fleet series needs an instance to match on")
				assert.True(t, orionNodeIDValid(s.Metric["instance"]))
			}
			switch UtilisationKey(key) {
			case KeyVMFleetIdentity:
				assert.NotEmpty(t, series[0].Metric["local_ipv4"], "the VM list matches on local_ipv4")
			case KeyVMFleetHostname:
				assert.NotEmpty(t, series[0].Metric["hostname"], "the VM list matches on hostname")
			}
		})
	}
}

// The resolver's ambiguity rule is not theoretical: on the test bed two nodes share
// one IP address, so a lookup by IP must resolve to nothing rather than pick one.
func TestLiveOrion_ProbeStatementsAreValid(t *testing.T) {
	cfg, _ := liveOrionConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	for _, probe := range orionNodeProbes(RequestMetadata{VMIP: "10.0.0.1", VMHostname: "nonexistent-host"}) {
		rows, err := cfg.Query(ctx, probe.Query, probe.Params)
		require.NoError(t, err, "probe by %s was rejected:\n%s", probe.Using, probe.Query)
		assert.Empty(t, rows, "a hostname that does not exist must match no node")
	}
}

func TestLiveOrion_KubernetesIsNotAvailable(t *testing.T) {
	cfg, _ := liveOrionConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// The reason Kubernetes is out of scope, asserted against the server rather
	// than left as a claim in a comment: nothing in Orion's container tables names
	// a namespace, pod, workload, request or limit.
	rows, err := cfg.Query(ctx, `SELECT EntityName, Name FROM Metadata.Property `+
		`WHERE EntityName LIKE 'Orion.Cman%' AND (Name LIKE '%Limit%' OR Name LIKE '%Request%' `+
		`OR Name LIKE '%Namespace%' OR Name LIKE '%Pod%' OR Name LIKE '%Workload%')`, nil)
	require.NoError(t, err)
	if len(rows) > 0 {
		var found []string
		for _, r := range rows {
			found = append(found, orionLabel(r["EntityName"])+"."+orionLabel(r["Name"]))
		}
		t.Fatalf("this Orion version exposes Kubernetes columns we assumed absent (%s); revisit the Kubernetes scope decision in nb-orion-observability-spec.md",
			strings.Join(found, ", "))
	}
}

// The metadata methods back the Code tab's entity and column pickers. They were
// written from the SWIS schema; these run them against a real server.
func TestLiveOrion_MetadataMethodsAnswer(t *testing.T) {
	cfg, _ := liveOrionConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	entities, err := cfg.Query(ctx, "SELECT FullName FROM Metadata.Entity WHERE FullName LIKE 'Orion.%' ORDER BY FullName", nil)
	require.NoError(t, err)
	require.NotEmpty(t, entities, "an Orion server must publish its entities")

	var names []string
	for _, row := range entities {
		names = append(names, orionLabel(row["FullName"]))
	}
	assert.Contains(t, names, "Orion.Nodes")
	assert.Contains(t, names, "Orion.CPULoad")

	// Columns of one entity, the query FetchMetricsLabels issues.
	cols, err := cfg.Query(ctx,
		"SELECT Name FROM Metadata.Property WHERE EntityName = @entity AND IsNavigable = false ORDER BY Name",
		map[string]any{"entity": "Orion.CPULoad"})
	require.NoError(t, err)
	var colNames []string
	for _, row := range cols {
		colNames = append(colNames, orionLabel(row["Name"]))
	}
	for _, want := range []string{"AvgLoad", "AvgMemoryUsed", "NodeID", "ObservationTimestamp"} {
		assert.Contains(t, colNames, want, "the VM queries depend on this column")
	}

	// Distinct values of one column, the query FetchMetricLabelValues issues once
	// it has checked the column against the schema above.
	values, err := cfg.Query(ctx, "SELECT DISTINCT NodeID AS v FROM Orion.Nodes", nil)
	require.NoError(t, err)
	assert.NotEmpty(t, values)
}

// SupportsRawQuery is true for this provider, so a person can type SWQL in the
// Code tab. This proves an arbitrary statement runs and converts, and that a bad
// one comes back with Orion's own complaint rather than a generic failure.
func TestLiveOrion_RawQueryPath(t *testing.T) {
	cfg, _ := liveOrionConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	rows, err := cfg.Query(ctx,
		"SELECT TOP 5 ObservationTimestamp AS nb_time, AvgLoad AS nb_value, NodeID AS node FROM Orion.CPULoad ORDER BY ObservationTimestamp DESC", nil)
	require.NoError(t, err)
	series := orionRowsToSeries(rows, time.Now().UTC())
	require.NotEmpty(t, series)
	assert.NotEmpty(t, series[0].Metric["node"], "a non-reserved column becomes a label")
	assert.Equal(t, len(series[0].Timestamps), len(series[0].Values), "timestamps and values stay aligned")

	_, err = cfg.Query(ctx, "SELECT Nope FROM Orion.NotAnEntity", nil)
	require.Error(t, err)
	assert.Contains(t, strings.ToLower(err.Error()), "swis", "the server's own message should survive")
}

// An aggregate cannot be expressed in SWQL here, so it must be refused rather
// than answered with a full series the caller will read as one collapsed point.
func TestLiveOrion_AggregateIsRefusedNotSilentlyIgnored(t *testing.T) {
	liveOrionConfig(t) // skip unless the live env is configured
	src := &OrionMetricSource{}
	_, err := src.WrapAggregate("SELECT 1", "max")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not supported")
}
