package relay

import (
	"database/sql"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
)

const testAccountID = "00666e9f-3774-4f5d-b86c-60b201ae18c5"

// expectedAgentQuery is the lookup SQL, as a regexp for sqlmock. Pinning the
// text keeps the three properties this gate depends on from being edited away:
// counting CONNECTED rows (not reading one row's status, which over two rows
// would pick one nondeterministically), and lower(type) / upper(status) (both
// columns are free-form text, so "K8s" or "Connected" must not read as absent).
const expectedAgentQuery = `select count\(\*\) from agent where upper\(status\) = \$1 and lower\(type\) = \$2 and cloud_account_id = \$3`

func mockDb(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return sqlx.NewDb(db, "sqlmock"), mock
}

// TestAgentConnectedInDb pins the policy: an account is connected iff it has at
// least one CONNECTED k8s row, and a failed lookup fails open so a DB blip
// cannot silently strip an account of its data.
func TestAgentConnectedInDb(t *testing.T) {
	t.Run("no connected k8s agent", func(t *testing.T) {
		db, mock := mockDb(t)
		mock.ExpectQuery(expectedAgentQuery).
			WithArgs(agentStatusConnected, k8sAgentType, testAccountID).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))

		if agentConnectedInDb(db, testAccountID) {
			t.Error("agentConnectedInDb() = true on a zero count, want false")
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Error(err)
		}
	})

	t.Run("connected k8s agent", func(t *testing.T) {
		db, mock := mockDb(t)
		mock.ExpectQuery(expectedAgentQuery).
			WithArgs(agentStatusConnected, k8sAgentType, testAccountID).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

		if !agentConnectedInDb(db, testAccountID) {
			t.Error("agentConnectedInDb() = false on a non-zero count, want true")
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Error(err)
		}
	})

	t.Run("multiple connected rows still counts as connected", func(t *testing.T) {
		db, mock := mockDb(t)
		mock.ExpectQuery(expectedAgentQuery).
			WithArgs(agentStatusConnected, k8sAgentType, testAccountID).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))

		if !agentConnectedInDb(db, testAccountID) {
			t.Error("agentConnectedInDb() = false with 2 connected rows, want true")
		}
	})

	t.Run("db error fails open", func(t *testing.T) {
		db, mock := mockDb(t)
		mock.ExpectQuery(expectedAgentQuery).
			WithArgs(agentStatusConnected, k8sAgentType, testAccountID).
			WillReturnError(sql.ErrConnDone)

		if !agentConnectedInDb(db, testAccountID) {
			t.Error("agentConnectedInDb() = false on a db error, want true (fail open)")
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Error(err)
		}
	})
}

// TestAgentConnectedEmptyAccount pins the one short-circuit this gate has, and
// pins it to api-server's: an empty account is a definitive no. A malformed but
// non-empty id must NOT short-circuit — it falls through to the query and fails
// open there, so an unexpected id surfaces as a real relay error instead of a
// silent "agent not connected" skip.
func TestAgentConnectedEmptyAccount(t *testing.T) {
	if AgentConnected("") {
		t.Error(`AgentConnected("") = true, want false`)
	}
}

// TestAgentConnectedUsesCache asserts a cached answer is returned without a
// database manager — the lookup would fail open to true, so reading false back
// proves the cache was consulted.
func TestAgentConnectedUsesCache(t *testing.T) {
	const accountID = "21a3b9a9-8d4e-4a8f-9744-dcd61d828344" // valid uuid: the guard must not short-circuit before the cache

	agentStatusCache.Lock()
	agentStatusCache.entries[accountID] = agentStatusCacheEntry{
		connected: false,
		expiresAt: time.Now().Add(time.Minute),
	}
	agentStatusCache.Unlock()

	t.Cleanup(func() {
		agentStatusCache.Lock()
		delete(agentStatusCache.entries, accountID)
		agentStatusCache.Unlock()
	})

	if AgentConnected(accountID) {
		t.Error("AgentConnected() = true on a cached false entry, want false")
	}
}

// TestAgentNotConnectedCacheTTLUnderRelayNegativeTTL keeps this gate from
// outliving the relay server's own negative cache (2m, wsNotConnectedCacheTTL in
// relay-server/pkg/db/db_store.go). Staying under it means skipping a call here
// can never delay a reconnect longer than making the call would have.
func TestAgentNotConnectedCacheTTLUnderRelayNegativeTTL(t *testing.T) {
	const relayNotConnectedCacheTTL = 2 * time.Minute
	if agentNotConnectedCacheTTL >= relayNotConnectedCacheTTL {
		t.Errorf("agentNotConnectedCacheTTL = %s, must stay under the relay's %s negative cache",
			agentNotConnectedCacheTTL, relayNotConnectedCacheTTL)
	}
}

// TestExecuteSkipsWhenAgentNotConnected is the regression test for the bug this
// file exists to fix: llm-server's relay client used to POST /request for every
// account, so an account whose agent was gone took a 503 on every tool call and
// the planner retried it. A cached "not connected" entry must make Execute
// return before any HTTP call — asserted by the error, since an attempted call
// would fail with a transport/config error instead.
func TestExecuteSkipsWhenAgentNotConnected(t *testing.T) {
	const accountID = "7c1f3a52-9d0e-4b6a-8f21-3ec4a5d7b018" // valid uuid: proves the cache, not the uuid guard, caused the skip

	agentStatusCache.Lock()
	agentStatusCache.entries[accountID] = agentStatusCacheEntry{
		connected: false,
		expiresAt: time.Now().Add(time.Minute),
	}
	agentStatusCache.Unlock()

	t.Cleanup(func() {
		agentStatusCache.Lock()
		delete(agentStatusCache.entries, accountID)
		agentStatusCache.Unlock()
	})

	// AgentType empty is the k8s default the relay applies to a missing
	// X-NB-Agent-Type header, and is the path every kubectl/logs tool takes.
	_, err := Execute(t.Context(), ActionExecuteBody{
		AccountID:  accountID,
		ActionName: "kubectl_command_executor",
	})
	if err == nil {
		t.Fatal("Execute() = nil error for a disconnected agent, want it to skip the relay call")
	}
	if err.Error() != errMsgAgentNotConnected {
		t.Errorf("Execute() error = %q, want %q (an HTTP failure means the guard did not fire)",
			err.Error(), errMsgAgentNotConnected)
	}
}

// TestExecuteSkipIsCaseInsensitiveOnAgentType pins that a differently-cased
// agent type still gates. AgentType is not a constant at the call sites — it is
// read from tool config (getConfigValue(..., "agent_type")), so a config
// written "K8s" would otherwise slip past the guard and take the 503 this
// change exists to avoid.
func TestExecuteSkipIsCaseInsensitiveOnAgentType(t *testing.T) {
	const accountID = "3f8c1d64-52ab-4e97-b1d3-08a6f27c945e"

	agentStatusCache.Lock()
	agentStatusCache.entries[accountID] = agentStatusCacheEntry{
		connected: false,
		expiresAt: time.Now().Add(time.Minute),
	}
	agentStatusCache.Unlock()

	t.Cleanup(func() {
		agentStatusCache.Lock()
		delete(agentStatusCache.entries, accountID)
		agentStatusCache.Unlock()
	})

	for _, agentType := range []string{"K8S", "K8s", "k8s"} {
		t.Run(agentType, func(t *testing.T) {
			_, err := Execute(t.Context(), ActionExecuteBody{
				AccountID:  accountID,
				ActionName: "kubectl_command_executor",
				AgentType:  agentType,
			})
			if err == nil || err.Error() != errMsgAgentNotConnected {
				t.Errorf("Execute(AgentType=%q) error = %v, want %q", agentType, err, errMsgAgentNotConnected)
			}
		})
	}
}

// TestExecuteSkipGatesOnlyK8sAgent asserts a non-k8s agent type is left alone:
// proxy and the cloud-sync rows are separate registrations that the k8s
// connectivity lookup says nothing about, so gating them on it would break
// accounts whose k8s agent is simply absent by design.
func TestExecuteSkipGatesOnlyK8sAgent(t *testing.T) {
	const accountID = "b4e2d9c1-6a73-4f58-9c02-1d8e5f3a7b64" // valid uuid: proves AgentType, not the uuid guard, decided this

	agentStatusCache.Lock()
	agentStatusCache.entries[accountID] = agentStatusCacheEntry{
		connected: false,
		expiresAt: time.Now().Add(time.Minute),
	}
	agentStatusCache.Unlock()

	t.Cleanup(func() {
		agentStatusCache.Lock()
		delete(agentStatusCache.entries, accountID)
		agentStatusCache.Unlock()
	})

	_, err := Execute(t.Context(), ActionExecuteBody{
		AccountID:  accountID,
		ActionName: "db_query",
		AgentType:  "proxy",
	})
	// It should get past the guard and fail on the HTTP call instead (no relay
	// endpoint configured in tests) — any error is fine except the guard's.
	if err != nil && err.Error() == errMsgAgentNotConnected {
		t.Error("Execute() gated a proxy-agent call on k8s connectivity, want it to proceed")
	}
}

// TestExecuteNoAgentSkipsGate: an action the relay serves without any agent
// (direct-mode MCP) must not be refused because the account's k8s agent is
// disconnected or absent.
func TestExecuteNoAgentSkipsGate(t *testing.T) {
	const accountID = "0d7a6e3b-1c24-4f89-a5b2-9e61c3f8d470" // valid uuid: proves NoAgent, not the uuid guard, decided this

	agentStatusCache.Lock()
	agentStatusCache.entries[accountID] = agentStatusCacheEntry{
		connected: false,
		expiresAt: time.Now().Add(time.Minute),
	}
	agentStatusCache.Unlock()

	t.Cleanup(func() {
		agentStatusCache.Lock()
		delete(agentStatusCache.entries, accountID)
		agentStatusCache.Unlock()
	})

	_, err := Execute(t.Context(), ActionExecuteBody{
		AccountID:  accountID,
		ActionName: "mcp_request",
		NoAgent:    true,
	})
	if err != nil && err.Error() == errMsgAgentNotConnected {
		t.Error("Execute() gated a NoAgent call on k8s connectivity, want it to proceed")
	}
}
