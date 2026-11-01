package playbooks

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBuildServiceEndpointsReport covers the three live states the enricher
// distinguishes: still broken (Critical), recovered (Info), and
// selector-now-matches-but-zero-addresses (High — scale-to-zero / not-ready).
func TestBuildServiceEndpointsReport(t *testing.T) {
	workloads := []workloadTemplate{
		{name: "web", labels: map[string]string{"app": "web"}},
		{name: "worker", labels: map[string]string{"app": "worker"}},
	}

	t.Run("still broken -> Critical insight + comparison table", func(t *testing.T) {
		text, insights := buildServiceEndpointsReport(
			"demo", "web-svc", map[string]string{"app": "wrong-label"}, 0, workloads, serviceAgeFacts{})
		assert.Contains(t, text, "0 addresses — still not routing traffic")
		assert.Contains(t, text, "app=wrong-label")
		assert.Contains(t, text, "| web | `app=web` | ✗ |")
		if assert.Len(t, insights, 1) {
			assert.Equal(t, "Critical", insights[0].Severity)
			assert.Contains(t, insights[0].Message, "matches none of the 2 workload pod template(s)")
		}
	})

	t.Run("recovered -> Info insight", func(t *testing.T) {
		text, insights := buildServiceEndpointsReport(
			"demo", "web-svc", map[string]string{"app": "web"}, 3, workloads, serviceAgeFacts{})
		assert.Contains(t, text, "3 address(es) — appears recovered")
		if assert.Len(t, insights, 1) {
			assert.Equal(t, "Info", insights[0].Severity)
			assert.Contains(t, insights[0].Message, "appears resolved")
		}
	})

	t.Run("selector matches template but zero addresses -> High insight", func(t *testing.T) {
		text, insights := buildServiceEndpointsReport(
			"demo", "web-svc", map[string]string{"app": "web"}, 0, workloads, serviceAgeFacts{})
		assert.Contains(t, text, "| web | `app=web` | ✓ |")
		if assert.Len(t, insights, 1) {
			assert.Equal(t, "High", insights[0].Severity)
			assert.Contains(t, insights[0].Message, "scaled to zero")
		}
	})

	t.Run("selector removed -> noted, no insight", func(t *testing.T) {
		text, insights := buildServiceEndpointsReport("demo", "web-svc", nil, 0, workloads, serviceAgeFacts{})
		assert.Contains(t, text, "selector was removed")
		assert.Empty(t, insights)
	})
}

// TestServiceEndpointsResponseShape locks the wire contract the frontend
// depends on: type=markdown with a top-level data string, and
// actual_action_name=text_enricher so TextEnricherDynamicCard renders it
// without any new frontend registration.
func TestServiceEndpointsResponseShape(t *testing.T) {
	resp := serviceEndpointsMarkdownResponse{
		Title:          "Service endpoint status (live)",
		Data:           "**Live check**",
		AdditionalInfo: map[string]any{"actual_action_name": "text_enricher"},
		Insight:        []PlaybookActionResponseInsight{{Message: "m", Severity: "Critical"}},
	}
	assert.Equal(t, "markdown", resp.GetFormatName())
	assert.Equal(t, "**Live check**", resp.GetData())
	assert.Equal(t, "text_enricher", resp.GetAdditionalInfo()["actual_action_name"])
}

func TestWorkloadTemplateHelpers(t *testing.T) {
	list := []any{
		map[string]any{
			"metadata": map[string]any{"name": "web"},
			"spec": map[string]any{
				"template": map[string]any{
					"metadata": map[string]any{"labels": map[string]any{"app": "web"}},
				},
			},
		},
		map[string]any{"metadata": map[string]any{}}, // nameless — skipped
	}
	ws := workloadTemplatesFromList(list, "demo")
	if assert.Len(t, ws, 1) {
		assert.Equal(t, "web", ws[0].name)
		assert.Equal(t, map[string]string{"app": "web"}, ws[0].labels)
	}

	assert.True(t, labelsContain(map[string]string{"app": "web", "tier": "fe"}, map[string]string{"app": "web"}))
	assert.False(t, labelsContain(map[string]string{"app": "web"}, map[string]string{"app": "other"}))
	assert.False(t, labelsContain(map[string]string{"app": "web"}, nil))
	// Empty-string selector value must not match a MISSING label key.
	assert.False(t, labelsContain(map[string]string{"app": "web"}, map[string]string{"foo": ""}))
	assert.True(t, labelsContain(map[string]string{"foo": ""}, map[string]string{"foo": ""}))

	ep := map[string]any{"subsets": []any{
		map[string]any{"addresses": []any{map[string]any{"ip": "10.0.0.1"}, map[string]any{"ip": "10.0.0.2"}}},
		map[string]any{}, // no addresses
	}}
	assert.Equal(t, 2, endpointsAddressCount(ep))

	// Selector with dotted user keys survives (snake conversion skips them).
	svc := map[string]any{"spec": map[string]any{"selector": map[string]any{"app.kubernetes.io/name": "web"}}}
	sel := serviceSelectorFromDict(svc)
	assert.Equal(t, "web", sel["app.kubernetes.io/name"])
	assert.True(t, strings.Contains(formatLabelMap(sel), "app.kubernetes.io/name=web"))
}

// Agents before 0.1.26-rc-2 ignore the namespace on the workload list call and
// return the whole cluster; the card must still show only the Service's own
// namespace.
func TestWorkloadTemplatesFromListKeepsOnlyNamespace(t *testing.T) {
	item := func(name, ns string) map[string]any {
		meta := map[string]any{"name": name}
		if ns != "" {
			meta["namespace"] = ns
		}
		return map[string]any{"metadata": meta}
	}
	list := []any{item("web", "demo"), item("cert-manager", "cert-manager"), item("coredns", "kube-system"), item("no-ns", "")}

	ws := workloadTemplatesFromList(list, "demo")
	names := make([]string, 0, len(ws))
	for _, w := range ws {
		names = append(names, w.name)
	}
	assert.Equal(t, []string{"web", "no-ns"}, names)
}

func TestServiceCreatedAt(t *testing.T) {
	want := time.Date(2023, 10, 25, 11, 42, 33, 0, time.UTC)
	for _, key := range []string{"creationTimestamp", "creation_timestamp"} {
		got, ok := serviceCreatedAt(map[string]any{"metadata": map[string]any{key: "2023-10-25T11:42:33Z"}})
		assert.True(t, ok, key)
		assert.Equal(t, want, got, key)
	}
	_, ok := serviceCreatedAt(map[string]any{"metadata": map[string]any{"creationTimestamp": "not-a-time"}})
	assert.False(t, ok)
	_, ok = serviceCreatedAt(map[string]any{})
	assert.False(t, ok)
}

// The facts must reach both the card and the insight: the insight is what the
// investigation reads, and without them a Service broken for months reads the
// same as one that broke a minute ago.
func TestServiceAgeFactsInReport(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	created := time.Date(2023, 10, 25, 11, 42, 33, 0, time.UTC)
	firstAlert := time.Date(2026, 7, 30, 18, 11, 20, 0, time.UTC)
	workloads := []workloadTemplate{{name: "web", labels: map[string]string{"app": "web"}}}
	selector := map[string]string{"app": "legacy-proxy", "release": "old"}

	t.Run("old Service with an alert on record for months", func(t *testing.T) {
		facts := serviceAgeFacts{now: now, createdAt: created, hasCreatedAt: true, alerts: &alertHistory{first: firstAlert}}
		text, insights := buildServiceEndpointsReport("demo", "legacy-proxy", selector, 0, workloads, facts)
		assert.Contains(t, text, "- Service created: Oct 25, 2023 (2 years ago)")
		assert.Contains(t, text, "- First alert for this Service on record: Jul 30, 2026 (2 months ago)")
		if assert.Len(t, insights, 1) {
			assert.Equal(t, "Critical", insights[0].Severity)
			assert.Contains(t, insights[0].Message, "matches none of the 1 workload pod template(s) in the namespace. ")
			assert.Contains(t, insights[0].Message, "The Service was created Oct 25, 2023 (2 years ago).")
			assert.Contains(t, insights[0].Message, "The first alert for it on record fired Jul 30, 2026 (2 months ago).")
		}
	})

	t.Run("no earlier alert on record", func(t *testing.T) {
		facts := serviceAgeFacts{now: now, alerts: &alertHistory{}}
		text, insights := buildServiceEndpointsReport("demo", "legacy-proxy", selector, 0, workloads, facts)
		assert.Contains(t, text, "- No earlier alert for this Service on record")
		assert.NotContains(t, text, "Service created")
		if assert.Len(t, insights, 1) {
			assert.True(t, strings.HasSuffix(insights[0].Message, ". No earlier alert for it is on record."), insights[0].Message)
		}
	})

	t.Run("history not read: no fact lines, message unchanged", func(t *testing.T) {
		text, insights := buildServiceEndpointsReport("demo", "legacy-proxy", selector, 0, workloads, serviceAgeFacts{now: now})
		assert.NotContains(t, text, "alert for this Service")
		if assert.Len(t, insights, 1) {
			assert.True(t, strings.HasSuffix(insights[0].Message, "in the namespace"), insights[0].Message)
		}
	})

	t.Run("scaled-to-zero insight carries the facts too", func(t *testing.T) {
		facts := serviceAgeFacts{now: now, alerts: &alertHistory{first: firstAlert}}
		_, insights := buildServiceEndpointsReport("demo", "legacy-proxy", map[string]string{"app": "web"}, 0, workloads, facts)
		if assert.Len(t, insights, 1) {
			assert.Equal(t, "High", insights[0].Severity)
			assert.Contains(t, insights[0].Message, "The first alert for it on record fired Jul 30, 2026")
		}
	})

	t.Run("recovered Service carries no age facts in the insight", func(t *testing.T) {
		facts := serviceAgeFacts{now: now, createdAt: created, hasCreatedAt: true, alerts: &alertHistory{first: firstAlert}}
		_, insights := buildServiceEndpointsReport("demo", "legacy-proxy", selector, 2, workloads, facts)
		if assert.Len(t, insights, 1) {
			assert.Equal(t, "Info", insights[0].Severity)
			assert.NotContains(t, insights[0].Message, "created")
		}
	})
}

// Execute can be called for any Service; only the alert's own Service may
// borrow the alert's fingerprint.
func TestOwnAlertFingerprint(t *testing.T) {
	ev := PlaybookEvent{AggregationKey: "service_no_endpoints", SubjectType: "service",
		SubjectName: "legacy-proxy", SubjectNamespace: "demo", Fingerprint: "fp1"}
	assert.Equal(t, "fp1", ownAlertFingerprint(ev, "legacy-proxy", "demo"))
	assert.Empty(t, ownAlertFingerprint(ev, "other", "demo"))
	assert.Empty(t, ownAlertFingerprint(ev, "legacy-proxy", "other-ns"))
	ev.AggregationKey = "report_crash_loop"
	assert.Empty(t, ownAlertFingerprint(ev, "legacy-proxy", "demo"))
}

func TestHumanAge(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	cases := map[time.Duration]string{
		10 * time.Minute:         "less than an hour",
		time.Hour:                "1 hour",
		30 * time.Hour:           "30 hours",
		3 * 24 * time.Hour:       "3 days",
		90 * 24 * time.Hour:      "3 months",
		3 * 365 * 24 * time.Hour: "3 years",
	}
	for ago, want := range cases {
		assert.Equal(t, want, humanAge(now, now.Add(-ago)), ago.String())
	}
}

func TestQueryAlertHistory(t *testing.T) {
	newDB := func(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock) {
		t.Helper()
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })
		return sqlx.NewDb(db, "postgres"), mock
	}
	first := time.Date(2026, 7, 30, 18, 11, 20, 0, time.UTC)

	t.Run("first alert on record", func(t *testing.T) {
		db, mock := newDB(t)
		mock.ExpectQuery(`SELECT min\(created_at\) FROM events WHERE fingerprint = \$1 AND cloud_account_id = \$2`).
			WithArgs("fp1", "acct").
			WillReturnRows(sqlmock.NewRows([]string{"min"}).AddRow(first))
		hist, err := queryAlertHistory(db, "acct", "fp1")
		require.NoError(t, err)
		assert.Equal(t, first, hist.first)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("no alert on record", func(t *testing.T) {
		db, mock := newDB(t)
		mock.ExpectQuery(`SELECT min\(created_at\) FROM events`).
			WillReturnRows(sqlmock.NewRows([]string{"min"}).AddRow(nil))
		hist, err := queryAlertHistory(db, "acct", "fp1")
		require.NoError(t, err)
		assert.True(t, hist.first.IsZero())
	})

	t.Run("query error is returned, not treated as no history", func(t *testing.T) {
		db, mock := newDB(t)
		mock.ExpectQuery(`SELECT min\(created_at\) FROM events`).WillReturnError(errors.New("boom"))
		_, err := queryAlertHistory(db, "acct", "fp1")
		assert.ErrorContains(t, err, "boom")
	})
}
