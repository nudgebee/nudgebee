package cloud

import (
	"encoding/json"
	"errors"
	"log/slog"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"nudgebee/services/eventrule/playbooks"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/require"
)

// fakeCollector stands in for the cloud collector and records every command
// that actually reaches it.
type fakeCollector struct {
	mu       sync.Mutex
	commands []string
}

func newFakeCollector(t *testing.T, respond func(command string) (string, error)) *fakeCollector {
	t.Helper()
	fc := &fakeCollector{}
	prev := staticConfigCliExec
	staticConfigCliExec = func(_ *security.RequestContext, req CloudExecuteCliCommandRequest) (map[string]any, error) {
		fc.mu.Lock()
		fc.commands = append(fc.commands, req.Command)
		fc.mu.Unlock()
		data, err := respond(req.Command)
		if err != nil {
			return map[string]any{}, err
		}
		return map[string]any{"data": data}, nil
	}
	t.Cleanup(func() { staticConfigCliExec = prev })
	return fc
}

func (fc *fakeCollector) calls(command string) int {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	n := 0
	for _, c := range fc.commands {
		if c == command {
			n++
		}
	}
	return n
}

// newStaticCliMetastore installs a mock metastore: building a tenant-admin
// context looks up the tenant's accounts, and the actions build one per call.
func newStaticCliMetastore(t *testing.T) sqlmock.Sqlmock {
	t.Helper()
	rawDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) {
		return &database.DatabaseManager{Db: sqlx.NewDb(rawDB, "postgresql")}, nil
	})
	t.Cleanup(func() {
		database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) {
			return nil, errors.New("no metastore in unit tests")
		})
		_ = rawDB.Close()
	})
	return mock
}

func expectTenantAccountLookup(mock sqlmock.Sqlmock) {
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id FROM cloud_accounts WHERE tenant = $1")).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("acct"))
}

// staticCliRun returns a suffix unique to one invocation of one test. The cache
// is package-level and outlives a test, so fixed tenant names would turn a
// second run in the same process (-count=2) into cache hits.
func staticCliRun(t *testing.T) string {
	return t.Name() + "-" + strconv.FormatInt(time.Now().UnixNano(), 36)
}

func staticCliCtx(mock sqlmock.Sqlmock, tenant string) *security.RequestContext {
	expectTenantAccountLookup(mock)
	return security.NewRequestContextForTenantAdmin(tenant, slog.Default(), nil, nil)
}

func TestExecuteStaticConfigCli_RepeatWithinWindowIsNotFetchedAgain(t *testing.T) {
	mock := newStaticCliMetastore(t)
	run := staticCliRun(t)
	const cmd = "az resource show --ids '/subscriptions/s1/rules/repeat' --output json"
	fc := newFakeCollector(t, func(string) (string, error) { return `{"name":"repeat"}`, nil })
	req := CloudExecuteCliCommandRequest{AccountID: "acct-repeat", Command: cmd}

	first, err := executeStaticConfigCli(staticCliCtx(mock, "tenant-repeat-"+run), req)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	second, err := executeStaticConfigCli(staticCliCtx(mock, "tenant-repeat-"+run), req)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}

	if got := fc.calls(cmd); got != 1 {
		t.Fatalf("collector was called %d times, want 1", got)
	}
	if first["data"] != second["data"] {
		t.Fatalf("cached output differs: first=%v second=%v", first["data"], second["data"])
	}
}

func TestExecuteStaticConfigCli_KeyedByTenantAccountAndCommand(t *testing.T) {
	mock := newStaticCliMetastore(t)
	run := staticCliRun(t)
	const cmd = "az resource show --ids '/subscriptions/s1/rules/keyed' --output json"
	const otherCmd = "az resource show --ids '/subscriptions/s1/rules/keyed-other' --output json"
	fc := newFakeCollector(t, func(string) (string, error) { return `{"name":"keyed"}`, nil })

	calls := []struct {
		tenant, account, command string
	}{
		{"tenant-a-" + run, "acct-a", cmd},
		{"tenant-b-" + run, "acct-a", cmd},
		{"tenant-a-" + run, "acct-b", cmd},
		{"tenant-a-" + run, "acct-a", otherCmd},
	}
	for _, c := range calls {
		if _, err := executeStaticConfigCli(staticCliCtx(mock, c.tenant), CloudExecuteCliCommandRequest{AccountID: c.account, Command: c.command}); err != nil {
			t.Fatalf("call %+v: %v", c, err)
		}
	}

	if got := fc.calls(cmd) + fc.calls(otherCmd); got != len(calls) {
		t.Fatalf("collector was called %d times, want %d (every call differs in tenant, account or command)", got, len(calls))
	}
}

func TestExecuteStaticConfigCli_FailureIsRetriedOnNextCall(t *testing.T) {
	mock := newStaticCliMetastore(t)
	run := staticCliRun(t)
	const cmd = "az resource show --ids '/subscriptions/s1/rules/failing' --output json"
	failure := errors.New("cloud collector returned status 500")
	fc := newFakeCollector(t, func(string) (string, error) { return `{"name":"failing"}`, failure })
	req := CloudExecuteCliCommandRequest{AccountID: "acct-failing", Command: cmd}

	if _, err := executeStaticConfigCli(staticCliCtx(mock, "tenant-failing-"+run), req); err == nil {
		t.Fatal("expected an error from a failing command")
	}
	failure = nil
	if _, err := executeStaticConfigCli(staticCliCtx(mock, "tenant-failing-"+run), req); err != nil {
		t.Fatalf("call after recovery: %v", err)
	}

	if got := fc.calls(cmd); got != 2 {
		t.Fatalf("collector was called %d times, want 2 (the failure must not be cached)", got)
	}
}

func TestExecuteStaticConfigCli_EmptyOrOversizedOutputIsNotCached(t *testing.T) {
	mock := newStaticCliMetastore(t)
	run := staticCliRun(t)
	const emptyCmd = "az rest --method get --url 'https://example.com/empty' --output json"
	const bigCmd = "az rest --method get --url 'https://example.com/big' --output json"
	big := strings.Repeat("x", staticConfigCliMaxBytes+1)
	fc := newFakeCollector(t, func(command string) (string, error) {
		if command == bigCmd {
			return big, nil
		}
		return "  ", nil
	})

	for _, cmd := range []string{emptyCmd, bigCmd} {
		req := CloudExecuteCliCommandRequest{AccountID: "acct-uncached", Command: cmd}
		for i := 0; i < 2; i++ {
			if _, err := executeStaticConfigCli(staticCliCtx(mock, "tenant-uncached-"+run), req); err != nil {
				t.Fatalf("%s call %d: %v", cmd, i, err)
			}
		}
		if got := fc.calls(cmd); got != 2 {
			t.Fatalf("%s: collector was called %d times, want 2", cmd, got)
		}
	}
}

func TestExecuteStaticConfigCli_FetchedAgainAfterWindow(t *testing.T) {
	mock := newStaticCliMetastore(t)
	run := staticCliRun(t)
	const cmd = "az resource show --ids '/subscriptions/s1/rules/expiring' --output json"
	fc := newFakeCollector(t, func(string) (string, error) { return `{"name":"expiring"}`, nil })
	req := CloudExecuteCliCommandRequest{AccountID: "acct-expiring", Command: cmd}

	now := time.Now()
	staticConfigCliNow = func() time.Time { return now }
	t.Cleanup(func() { staticConfigCliNow = time.Now })

	if _, err := executeStaticConfigCli(staticCliCtx(mock, "tenant-expiring-"+run), req); err != nil {
		t.Fatalf("first call: %v", err)
	}
	now = now.Add(staticConfigCliTTL - time.Second)
	if _, err := executeStaticConfigCli(staticCliCtx(mock, "tenant-expiring-"+run), req); err != nil {
		t.Fatalf("call inside window: %v", err)
	}
	if got := fc.calls(cmd); got != 1 {
		t.Fatalf("inside the window the collector was called %d times, want 1", got)
	}

	now = now.Add(2 * time.Second)
	if _, err := executeStaticConfigCli(staticCliCtx(mock, "tenant-expiring-"+run), req); err != nil {
		t.Fatalf("call after window: %v", err)
	}
	if got := fc.calls(cmd); got != 2 {
		t.Fatalf("after the window the collector was called %d times, want 2", got)
	}
}

// A second firing of the same alert must get the same card and the same labels
// as the first — other actions gate on those labels — without asking the cloud
// for the rule again.
func TestAzureAlertRuleAction_SecondFiringReusesRuleDefinition(t *testing.T) {
	const ruleID = "/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/rg/providers/Microsoft.Insights/scheduledQueryRules/second-firing"
	const workspace = "/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/rg/providers/Microsoft.OperationalInsights/workspaces/ws"
	rule := map[string]any{
		"name": "second-firing",
		"type": "Microsoft.Insights/scheduledQueryRules",
		"properties": map[string]any{
			"enabled": true,
			"scopes":  []any{workspace},
			"criteria": map[string]any{
				"allOf": []any{map[string]any{
					"query":           "Heartbeat | take 1",
					"operator":        "GreaterThan",
					"threshold":       0,
					"timeAggregation": "Count",
				}},
			},
		},
	}
	ruleJSON, err := json.Marshal(rule)
	if err != nil {
		t.Fatal(err)
	}
	fc := newFakeCollector(t, func(string) (string, error) { return string(ruleJSON), nil })

	tenant := "tenant-rule-" + staticCliRun(t)
	firing := func() playbooks.PlaybookActionContext {
		return playbooks.NewPlaybookActionContext(tenant, "acct-rule", slog.Default(), playbooks.PlaybookEvent{
			Source: "Azure_Monitor_Alert",
			Labels: map[string]string{"azure_alert_rule": ruleID},
		})
	}

	// The action builds its own tenant-admin context for each firing.
	mock := newStaticCliMetastore(t)
	expectTenantAccountLookup(mock)
	expectTenantAccountLookup(mock)

	action := &cloudAzureAlertRuleAction{}
	first, err := action.AutoExecute(firing())
	if err != nil {
		t.Fatalf("first firing: %v", err)
	}
	second, err := action.AutoExecute(firing())
	if err != nil {
		t.Fatalf("second firing: %v", err)
	}

	cmd := "az resource show --ids '" + ruleID + "' --output json"
	if got := fc.calls(cmd); got != 1 {
		t.Fatalf("rule definition was fetched %d times, want 1", got)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("second firing's card differs from the first:\nfirst=%+v\nsecond=%+v", first, second)
	}
	labels := second.(playbooks.PlaybookActionResponseLabelExtractor).ExtractLabels()
	if labels["azure_alert_kql_query"] != "Heartbeat | take 1" || labels["azure_alert_kql_workspace_scope"] != workspace {
		t.Fatalf("second firing lost the labels other actions gate on: %v", labels)
	}
}
