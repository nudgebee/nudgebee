//go:build e2e

package core

import (
	"testing"

	"github.com/google/uuid"
)

// Identity for the memory integration suites.
//
// Derived rather than random, and deliberately not read from the environment.
//
// It must be a real UUID: these ids reach rag-server as its account_id and are
// written to llm_retrieval_trace.cloud_account_id, which is uuid NOT NULL. An
// invented id like "hybrid-it-tenant-<8 chars>" is rejected there, and because
// the trace write is fire-and-forget the only symptom is a swallowed error.
//
// It must also be stable: rag-server names the Qdrant collection after this
// value, so an id that changes per run leaves a new collection behind each
// time. t.Name() is fixed for a given test, so the derived id is too.
//
// It must not come from TEST_TENANT / TEST_USER / TEST_ACCOUNT. Those carry the
// shared dev identity for the agent e2e suites (agents/TESTING.md) and on the
// standard .env they point at the real dev tenant. These suites clean up with
// tenant-wide deletes and Erase, so sourcing them would wipe real memory.
//
// Scoped per test and per package: several of these suites clean up with
// tenant-wide deletes, so a shared id would let one test's cleanup remove rows
// another had seeded — across packages, because `go test ./ee/memory/...` runs
// them in parallel, and within one, should any of these ever call t.Parallel.
const testIDPackage = "agents-core"

func testTenantID(t *testing.T) string  { return derivedTestID(t, "tenant") }
func testUserID(t *testing.T) string    { return derivedTestID(t, "user") }
func testAccountID(t *testing.T) string { return derivedTestID(t, "account") }

// derivedTestID is deterministic: the same test always yields the same UUID.
func derivedTestID(t *testing.T, kind string) string {
	t.Helper()
	name := "nudgebee/memory-it/" + testIDPackage + "/" + kind + "/" + t.Name()
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(name)).String()
}
