package adapter

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"nudgebee/services/internal/database"
	"nudgebee/services/internal/database/models"
	"nudgebee/services/security"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

// TestParseAddressedComments covers the agent-response parser (#36865). It is
// deliberately lenient — this is a bookkeeping record, never a correctness
// gate, so malformed input must degrade to "record less" and never fail an
// otherwise-successful followup run.
func TestParseAddressedComments(t *testing.T) {
	t.Run("absent field yields nil", func(t *testing.T) {
		require.Nil(t, parseAddressedComments(nil))
	})

	t.Run("wrong shape yields nil", func(t *testing.T) {
		require.Nil(t, parseAddressedComments("not an array"))
		require.Nil(t, parseAddressedComments(map[string]any{"source": "inline"}))
	})

	t.Run("parses a well-formed entry", func(t *testing.T) {
		got := parseAddressedComments([]any{
			map[string]any{
				"source":       "inline",
				"comment_id":   float64(2145),
				"action":       "fixed",
				"addressed_at": "2026-08-24T10:00:00Z",
			},
		})
		require.Len(t, got, 1)
		require.Equal(t, "inline", got[0].Source)
		require.Equal(t, int64(2145), got[0].CommentID)
		require.Equal(t, "fixed", got[0].Action)
		require.Equal(t, 2026, got[0].AddressedAt.Year())
	})

	t.Run("drops half-identified entries but keeps the rest", func(t *testing.T) {
		got := parseAddressedComments([]any{
			map[string]any{"comment_id": float64(1), "action": "fixed"},                        // no source
			map[string]any{"source": "inline", "action": "fixed"},                              // no id
			map[string]any{"source": "inline", "comment_id": float64(2)},                       // no action
			map[string]any{"source": "inline", "comment_id": float64(0), "action": "fixed"},    // zero id
			map[string]any{"source": "issue_comment", "comment_id": float64(9), "action": "x"}, // good
		})
		require.Len(t, got, 1, "only the fully-identified entry survives")
		require.Equal(t, int64(9), got[0].CommentID)
	})

	t.Run("missing timestamp is stamped so newest-wins still has a key", func(t *testing.T) {
		got := parseAddressedComments([]any{
			map[string]any{"source": "inline", "comment_id": float64(5), "action": "fixed"},
		})
		require.Len(t, got, 1)
		require.False(t, got[0].AddressedAt.IsZero(), "a zero time would break the merge ordering")
	})

	t.Run("comment_id as a string is accepted, not just float64", func(t *testing.T) {
		got := parseAddressedComments([]any{
			map[string]any{"source": "inline", "comment_id": "2145", "action": "fixed"},
		})
		require.Len(t, got, 1, "a stringified id must not be silently dropped")
		require.Equal(t, int64(2145), got[0].CommentID)
	})

	t.Run("an unparseable string comment_id is dropped, not zero", func(t *testing.T) {
		got := parseAddressedComments([]any{
			map[string]any{"source": "inline", "comment_id": "not-a-number", "action": "fixed"},
		})
		require.Empty(t, got)
	})

	t.Run("addressed_at that is not a string falls back to now, not the stringified value", func(t *testing.T) {
		got := parseAddressedComments([]any{
			map[string]any{"source": "inline", "comment_id": float64(1), "action": "fixed", "addressed_at": float64(123)},
		})
		require.Len(t, got, 1)
		require.False(t, got[0].AddressedAt.IsZero())
		require.WithinDuration(t, time.Now(), got[0].AddressedAt, time.Minute)
	})
}

// TestAddressedCommentsJSON pins the jsonb argument shape. An empty record must
// produce '[]', never nil — a NULL bind would make the merge expression NULL
// and wipe the column.
func TestAddressedCommentsJSON(t *testing.T) {
	require.Equal(t, "[]", string(followupOutcomeSuccess.addressedCommentsJSON()),
		"no record must serialize to an empty array, not null")

	o := followupOutcomeSuccess
	o.addressed = []models.AddressedComment{{
		Source: "inline", CommentID: 7, Action: "fixed",
		AddressedAt: time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC),
	}}
	var round []models.AddressedComment
	require.NoError(t, json.Unmarshal(o.addressedCommentsJSON(), &round))
	require.Len(t, round, 1)
	require.Equal(t, int64(7), round[0].CommentID)
}

// TestClassifyFollowupOutcomeCarriesAddressedComments checks the field is read
// on both a success and a no_op — a run can answer comments and still finish as
// a no_op (every reply skipped as unverified), and that record still matters.
// An older agent that never emits the field must stay a plain no-op.
func TestClassifyFollowupOutcomeCarriesAddressedComments(t *testing.T) {
	withComments := `{"execution_status":"%s","addressed_comments":[
		{"source":"inline","comment_id":11,"action":"fixed","addressed_at":"2026-08-24T10:00:00Z"}]}`

	for _, status := range []string{"success", "no_op"} {
		t.Run(status, func(t *testing.T) {
			outcome := classifyFollowupOutcome([]string{fmt.Sprintf(withComments, status)})
			require.Equal(t, status, outcome.name)
			require.Len(t, outcome.addressed, 1)
			require.Equal(t, int64(11), outcome.addressed[0].CommentID)
		})
	}

	t.Run("older agent omitting the field", func(t *testing.T) {
		outcome := classifyFollowupOutcome([]string{`{"execution_status":"success"}`})
		require.Empty(t, outcome.addressed)
		require.Equal(t, "[]", string(outcome.addressedCommentsJSON()))
	})
}

// TestBuildPRFollowupChatRequestAddressedComments checks the set rides on the
// payload as a JSON array (not a quoted string), and is omitted when there is
// no history so the request is unchanged for a fresh PR.
func TestBuildPRFollowupChatRequestAddressedComments(t *testing.T) {
	meta := prMetadata{PRURL: "https://github.com/acme/infra/pull/1", RepoURL: "https://github.com/acme/infra", Branch: "b"}

	t.Run("omitted when empty", func(t *testing.T) {
		for _, empty := range []json.RawMessage{
			nil, json.RawMessage("[]"), json.RawMessage("null"),
			// Whitespace-padded trivial values must be recognized too — the
			// comparison shouldn't depend on the driver always returning
			// compact jsonb.
			json.RawMessage("  []  "), json.RawMessage("\n[]\n"), json.RawMessage(" null "),
		} {
			req := buildPRFollowupChatRequest(meta, "tok", "prompt", empty)
			require.NotContainsf(t, req.Query, "addressed_comments",
				"a fresh PR's payload must be unchanged for %q", string(empty))
		}
	})

	t.Run("embedded as an array when present", func(t *testing.T) {
		raw := json.RawMessage(`[{"source":"inline","comment_id":3,"action":"fixed"}]`)
		req := buildPRFollowupChatRequest(meta, "tok", "prompt", raw)
		require.Contains(t, req.Query, "addressed_comments")
		// As an array, not a JSON-quoted string — a quoted string would arrive at
		// code-analysis as an unparseable scalar.
		require.Contains(t, req.Query, `"addressed_comments":[{`)
	})
}

// addressedTestManager builds a throwaway schema holding just enough of
// pr_followup to exercise the real merge SQL. Named _DB$ so the services-server
// DB job's `-run '_DB$'` filter picks it up, like every other database-backed
// test in this package.
func addressedTestManager(t *testing.T) *database.DatabaseManager {
	t.Helper()

	dsn := os.Getenv("APP_DATABASE_URL")
	if dsn == "" {
		dsn = os.Getenv("TEST_POSTGRES_DSN")
	}
	if dsn == "" {
		if os.Getenv("REQUIRE_DB_TESTS") == "true" {
			t.Fatal("REQUIRE_DB_TESTS is set but neither APP_DATABASE_URL nor TEST_POSTGRES_DSN is: " +
				"this suite must not be skipped in the job that exists to run it")
		}
		t.Skip("no database configured; export APP_DATABASE_URL to run this suite")
	}

	db, err := sqlx.Connect("postgres", dsn)
	require.NoError(t, err)
	// One connection, so SET search_path below holds for every later statement.
	db.SetMaxOpenConns(1)

	schema := fmt.Sprintf("addressed_comments_test_%d", os.Getpid())
	_, err = db.Exec(fmt.Sprintf(`DROP SCHEMA IF EXISTS %s CASCADE; CREATE SCHEMA %s; SET search_path TO %s`,
		schema, schema, schema))
	require.NoError(t, err)

	// Mirrors the real table's shape for the columns this SQL touches.
	_, err = db.Exec(`CREATE TABLE pr_followup (
		id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
		pr_url text NOT NULL,
		tenant_id text NOT NULL,
		pr_lifecycle_state text NOT NULL DEFAULT 'created',
		pr_iteration_count integer NOT NULL DEFAULT 0,
		last_pr_check_at timestamptz,
		pr_followup_pending boolean NOT NULL DEFAULT false,
		status_message text,
		addressed_comments jsonb NOT NULL DEFAULT '[]'::jsonb,
		created_at timestamptz NOT NULL DEFAULT now(),
		updated_at timestamptz NOT NULL DEFAULT now(),
		CONSTRAINT pr_followup_url_unique UNIQUE (pr_url)
	)`)
	require.NoError(t, err)

	var current string
	require.NoError(t, db.QueryRow(`SHOW search_path`).Scan(&current))
	require.Contains(t, current, schema, "search_path must point at the throwaway schema")

	t.Cleanup(func() {
		_, _ = db.Exec(fmt.Sprintf(`DROP SCHEMA IF EXISTS %s CASCADE`, schema))
		_ = db.Close()
	})
	return &database.DatabaseManager{Db: db}
}

func readAddressed(t *testing.T, dbms *database.DatabaseManager, id string) []models.AddressedComment {
	t.Helper()
	var raw []byte
	require.NoError(t, dbms.Db.QueryRow(`SELECT addressed_comments FROM pr_followup WHERE id=$1`, id).Scan(&raw))
	var out []models.AddressedComment
	require.NoError(t, json.Unmarshal(raw, &out))
	return out
}

func seedFollowupRow(t *testing.T, dbms *database.DatabaseManager, prURL, state string) string {
	t.Helper()
	var id string
	require.NoError(t, dbms.Db.QueryRow(
		`INSERT INTO pr_followup (pr_url, tenant_id, pr_lifecycle_state, last_pr_check_at)
		 VALUES ($1, 'tenant-a', $2, now()) RETURNING id`, prURL, state).Scan(&id))
	return id
}

func addressedEntry(source string, id int64, action string, at time.Time) models.AddressedComment {
	return models.AddressedComment{Source: source, CommentID: id, Action: action, AddressedAt: at}
}

// TestApplyFollowupOutcomeMergesAddressedComments_DB exercises the real merge
// SQL against real Postgres. The union semantics are the whole point: they must
// accumulate across runs, dedupe by (source, comment_id) with the newest
// winning, and never clobber an existing record with an empty one.
func TestApplyFollowupOutcomeMergesAddressedComments_DB(t *testing.T) {
	dbms := addressedTestManager(t)
	ctx := security.NewRequestContextForSuperAdmin(nil, nil, nil)
	base := time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC)

	t.Run("first run populates an empty column", func(t *testing.T) {
		id := seedFollowupRow(t, dbms, "https://github.com/acme/infra/pull/1", "addressing")
		o := followupOutcomeSuccess
		o.addressed = []models.AddressedComment{addressedEntry("inline", 1, "fixed", base)}

		applyFollowupOutcome(ctx, dbms, "pr_followup", id, o)

		got := readAddressed(t, dbms, id)
		require.Len(t, got, 1)
		require.Equal(t, int64(1), got[0].CommentID)
	})

	t.Run("later runs accumulate rather than replace", func(t *testing.T) {
		id := seedFollowupRow(t, dbms, "https://github.com/acme/infra/pull/2", "addressing")

		o1 := followupOutcomeSuccess
		o1.addressed = []models.AddressedComment{addressedEntry("inline", 1, "fixed", base)}
		applyFollowupOutcome(ctx, dbms, "pr_followup", id, o1)

		o2 := followupOutcomeSuccess
		o2.addressed = []models.AddressedComment{addressedEntry("issue_comment", 2, "wont_fix", base.Add(time.Hour))}
		applyFollowupOutcome(ctx, dbms, "pr_followup", id, o2)

		got := readAddressed(t, dbms, id)
		require.Len(t, got, 2, "the first run's record must survive the second run")
	})

	t.Run("same comment answered again updates, does not duplicate", func(t *testing.T) {
		id := seedFollowupRow(t, dbms, "https://github.com/acme/infra/pull/3", "addressing")

		o1 := followupOutcomeSuccess
		o1.addressed = []models.AddressedComment{addressedEntry("inline", 1, "acknowledged", base)}
		applyFollowupOutcome(ctx, dbms, "pr_followup", id, o1)

		o2 := followupOutcomeSuccess
		o2.addressed = []models.AddressedComment{addressedEntry("inline", 1, "fixed", base.Add(time.Hour))}
		applyFollowupOutcome(ctx, dbms, "pr_followup", id, o2)

		got := readAddressed(t, dbms, id)
		require.Len(t, got, 1, "same (source, comment_id) must collapse to one entry")
		require.Equal(t, "fixed", got[0].Action, "the newer answer must win")
	})

	t.Run("same id in different sources are distinct entries", func(t *testing.T) {
		// GitHub issue comments and review submissions are separate id spaces,
		// so the same number can legitimately name two different comments.
		id := seedFollowupRow(t, dbms, "https://github.com/acme/infra/pull/4", "addressing")

		o := followupOutcomeSuccess
		o.addressed = []models.AddressedComment{
			addressedEntry("inline", 42, "fixed", base),
			addressedEntry("issue_comment", 42, "wont_fix", base),
			addressedEntry("review_body", 42, "acknowledged", base),
		}
		applyFollowupOutcome(ctx, dbms, "pr_followup", id, o)

		require.Len(t, readAddressed(t, dbms, id), 3)
	})

	t.Run("a run recording nothing leaves the column intact", func(t *testing.T) {
		id := seedFollowupRow(t, dbms, "https://github.com/acme/infra/pull/5", "addressing")

		o1 := followupOutcomeSuccess
		o1.addressed = []models.AddressedComment{addressedEntry("inline", 1, "fixed", base)}
		applyFollowupOutcome(ctx, dbms, "pr_followup", id, o1)

		// A no_op run that answered nothing must not wipe the history.
		applyFollowupOutcome(ctx, dbms, "pr_followup", id, followupOutcomeNoOp)

		require.Len(t, readAddressed(t, dbms, id), 1,
			"an empty record must be a no-op, not a reset")
	})

	t.Run("a terminal row is not modified", func(t *testing.T) {
		// Mirrors the existing terminal guard on state and counter: a PR that
		// merged or closed mid-run must not be written back to.
		for _, state := range []string{"merged", "closed", "unresolvable"} {
			id := seedFollowupRow(t, dbms, "https://github.com/acme/infra/pull/t-"+state, state)
			o := followupOutcomeSuccess
			o.addressed = []models.AddressedComment{addressedEntry("inline", 1, "fixed", base)}

			applyFollowupOutcome(ctx, dbms, "pr_followup", id, o)

			require.Empty(t, readAddressed(t, dbms, id),
				"a %s row must not accept new addressed comments", state)
		}
	})
}

// TestFindOrCreatePRFollowupReturnsAddressedComments_DB checks the read side:
// the set rides on the existing find-or-create RETURNING rather than costing a
// second round trip, and a freshly-created row reports an empty array.
func TestFindOrCreatePRFollowupReturnsAddressedComments_DB(t *testing.T) {
	dbms := addressedTestManager(t)

	t.Run("new row starts empty", func(t *testing.T) {
		_, addressed, err := findOrCreatePRFollowupInTable(dbms, "pr_followup",
			"https://github.com/acme/infra/pull/10", "tenant-a", time.Time{})
		require.NoError(t, err)
		require.JSONEq(t, "[]", string(addressed))
	})

	t.Run("existing row returns what was recorded", func(t *testing.T) {
		ctx := security.NewRequestContextForSuperAdmin(nil, nil, nil)
		prURL := "https://github.com/acme/infra/pull/11"

		id, _, err := findOrCreatePRFollowupInTable(dbms, "pr_followup", prURL, "tenant-a", time.Time{})
		require.NoError(t, err)

		o := followupOutcomeSuccess
		o.addressed = []models.AddressedComment{
			addressedEntry("inline", 7, "fixed", time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC)),
		}
		applyFollowupOutcome(ctx, dbms, "pr_followup", id, o)

		// Second find-or-create on the same URL hits the ON CONFLICT path.
		gotID, addressed, err := findOrCreatePRFollowupInTable(dbms, "pr_followup", prURL, "tenant-a", time.Time{})
		require.NoError(t, err)
		require.Equal(t, id, gotID)

		var entries []models.AddressedComment
		require.NoError(t, json.Unmarshal(addressed, &entries))
		require.Len(t, entries, 1)
		require.Equal(t, int64(7), entries[0].CommentID)
	})
}
