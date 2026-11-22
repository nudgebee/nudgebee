package api

import (
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/security"
)

// incidentDB is an isolated schema on a disposable PostgreSQL database holding the columns the
// approval reads use.
func incidentDB(t *testing.T) *sqlx.DB {
	t.Helper()
	dsn := os.Getenv("REMEDIATION_LOOP_TEST_DB_URL")
	if dsn == "" {
		t.Skip("set REMEDIATION_LOOP_TEST_DB_URL to a disposable PostgreSQL database")
	}
	admin, err := sqlx.Connect("postgres", dsn)
	require.NoError(t, err)
	schema := "loop_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = admin.Exec("CREATE SCHEMA " + schema)
	require.NoError(t, err)
	scoped, err := url.Parse(dsn)
	require.NoError(t, err)
	q := scoped.Query()
	q.Set("search_path", schema)
	scoped.RawQuery = q.Encode()
	db, err := sqlx.Connect("postgres", scoped.String())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close(); _, _ = admin.Exec("DROP SCHEMA " + schema + " CASCADE"); _ = admin.Close() })
	_, err = db.Exec(`
		CREATE TABLE events (id uuid PRIMARY KEY, cloud_account_id uuid NOT NULL, fingerprint text, nb_status text, created_at timestamp NOT NULL);
		CREATE TABLE llm_conversations (id uuid PRIMARY KEY, account_id uuid NOT NULL, session_id text NOT NULL, status text NOT NULL DEFAULT 'WAITING');
		CREATE TABLE llm_conversation_messages (id uuid PRIMARY KEY, conversation_id uuid NOT NULL, message_type text, status text,
			message_config text, message_context text, response text, created_at timestamp NOT NULL, updated_at timestamp NOT NULL,
			user_id uuid, metadata jsonb);
		CREATE TABLE llm_conversation_agent (id uuid PRIMARY KEY, account_id uuid NOT NULL, followup_message_id uuid, message_id uuid,
			status text, created_at timestamp NOT NULL)`)
	require.NoError(t, err)
	return db
}

type incidentFixture struct {
	t            *testing.T
	db           *sqlx.DB
	account      string
	conversation string
}

const incidentFingerprint = "fp-image-pull"

func (f *incidentFixture) event(status string, at time.Time) string {
	id := uuid.NewString()
	_, err := f.db.Exec(`INSERT INTO events (id, cloud_account_id, fingerprint, nb_status, created_at) VALUES ($1, $2, $3, $4, $5)`,
		id, f.account, incidentFingerprint, status, at)
	require.NoError(f.t, err)
	return id
}

// confirmation stores a run_step confirmation asked at asked, answered with response at answered
// (status WAITING when response is "").
func (f *incidentFixture) confirmation(asked, answered time.Time, response string) string {
	id := uuid.NewString()
	status := "WAITING"
	if response != "" {
		status = "COMPLETED"
	}
	config := `{"confirmationKey":"run_step:pv-1:1:apply","followupOptions":["yes","no"],"followupType":"tool_confirmation",` +
		`"question":"Approve this change?\nCommand - kubectl rollout undo deploy/api -n demo","toolName":"run_step"}`
	_, err := f.db.Exec(`INSERT INTO llm_conversation_messages (id, conversation_id, message_type, status, message_config, message_context, response, created_at, updated_at)
		VALUES ($1, $2, 'followup', $3, $4, '{}', $5, $6, $7)`, id, f.conversation, status, config, response, asked, answered)
	require.NoError(f.t, err)
	return id
}

func newIncidentFixture(t *testing.T) *incidentFixture {
	db := incidentDB(t)
	f := &incidentFixture{t: t, db: db, account: uuid.NewString(), conversation: uuid.NewString()}
	_, err := db.Exec(`INSERT INTO llm_conversations (id, account_id, session_id) VALUES ($1, $2, $3)`, f.conversation, f.account, "event-"+incidentFingerprint)
	require.NoError(t, err)
	return f
}

// The alert's conversation spans every incident: an earlier incident's denials and waiting approvals
// stay on that incident, and the new incident shows only its own. Duplicates do not start an incident.
func TestApprovalsBelongToTheIncidentTheyWereAskedIn(t *testing.T) {
	f := newIncidentFixture(t)
	now := time.Now().UTC().Truncate(time.Second)
	ctx := security.NewRequestContextForSuperAdmin()

	// The earlier incident and its duplicates, resolved; denied once, and an approval left waiting.
	old := f.event("RESOLVED", now.Add(-6*time.Hour))
	f.event("DUPLICATE", now.Add(-5*time.Hour))
	oldDenial := f.confirmation(now.Add(-5*time.Hour), now.Add(-5*time.Hour+time.Minute), "no")
	oldWaiting := f.confirmation(now.Add(-4*time.Hour), now.Add(-4*time.Hour), "")

	// The new incident, a duplicate of it, and its own denial.
	f.event("DUPLICATE", now.Add(-2*time.Hour-time.Second))
	current := f.event("OPEN", now.Add(-2*time.Hour))
	f.event("DUPLICATE", now.Add(-90*time.Minute))
	newDenial := f.confirmation(now.Add(-time.Hour), now.Add(-time.Hour+time.Minute), "No")
	f.confirmation(now.Add(-50*time.Minute), now.Add(-49*time.Minute), "yes")

	ids := func(rows []deniedConfirmation) []string {
		var out []string
		for _, r := range rows {
			out = append(out, r.MessageId)
		}
		return out
	}
	denied, err := deniedConfirmationsIn(ctx, f.db, f.account, "event-"+incidentFingerprint, current)
	require.NoError(t, err)
	assert.Equal(t, []string{newDenial}, ids(denied), "the new incident shows only its own denial")

	denied, err = deniedConfirmationsIn(ctx, f.db, f.account, "event-"+incidentFingerprint, old)
	require.NoError(t, err)
	assert.Equal(t, []string{oldDenial}, ids(denied), "the earlier incident keeps its denial, and not the new one's")

	waiting, err := waitingConfirmationsIn(ctx, f.db, f.account, "event-"+incidentFingerprint, current)
	require.NoError(t, err)
	assert.Empty(t, waiting, "an approval waiting from the earlier incident is not the new incident's")

	waiting, err = waitingConfirmationsIn(ctx, f.db, f.account, "event-"+incidentFingerprint, old)
	require.NoError(t, err)
	require.Len(t, waiting, 1)
	assert.Equal(t, oldWaiting, waiting[0].MessageId)

	// An approval left WAITING in a conversation whose turn failed can never be answered: it is not offered.
	_, err = f.db.Exec(`UPDATE llm_conversations SET status = 'FAILED' WHERE id = $1`, f.conversation)
	require.NoError(t, err)
	waiting, err = waitingConfirmationsIn(ctx, f.db, f.account, "event-"+incidentFingerprint, old)
	require.NoError(t, err)
	assert.Empty(t, waiting, "a dead approval is not offered")
	_, err = f.db.Exec(`UPDATE llm_conversations SET status = 'WAITING' WHERE id = $1`, f.conversation)
	require.NoError(t, err)

	// An event that is not this account's is no incident at all.
	waiting, err = waitingConfirmationsIn(ctx, f.db, uuid.NewString(), "event-"+incidentFingerprint, current)
	require.NoError(t, err)
	assert.Empty(t, waiting)
}

// Lapsed and superseded approvals (#40734): NudgeBee closed them unanswered and nothing ran. A lapsed one stays
// on the incident it was asked in; a superseded one was closed because the new incident started, so the new
// incident's timeline shows it (at the time it was asked), with who asked it.
func TestClosedApprovalsOnTheTimeline(t *testing.T) {
	f := newIncidentFixture(t)
	now := time.Now().UTC().Truncate(time.Second)
	ctx := security.NewRequestContextForSuperAdmin()
	old := f.event("RESOLVED", now.Add(-6*time.Hour))
	current := f.event("OPEN", now.Add(-2*time.Hour))
	person := uuid.NewString()
	closeAs := func(asked, closed time.Time, reason string) string {
		id := f.confirmation(asked, asked, "")
		turn := uuid.NewString()
		_, err := f.db.Exec(`INSERT INTO llm_conversation_messages (id, conversation_id, message_type, status, message_config, created_at, updated_at, user_id)
			VALUES ($1, $2, 'generation', 'TERMINATED', '{}', $3, $3, $4)`, turn, f.conversation, asked, person)
		require.NoError(t, err)
		_, err = f.db.Exec(`INSERT INTO llm_conversation_agent (id, account_id, followup_message_id, message_id, status, created_at) VALUES (gen_random_uuid(), $1, $2, $3, 'terminated', $4)`,
			f.account, id, turn, asked)
		require.NoError(t, err)
		_, err = f.db.Exec(`UPDATE llm_conversation_messages SET status = 'TERMINATED', response = 'closed', updated_at = $2,
			metadata = jsonb_build_object('closed_reason', $3::text) WHERE id = $1`, id, closed, reason)
		require.NoError(t, err)
		return id
	}
	superseded := closeAs(now.Add(-5*time.Hour), now.Add(-2*time.Hour+time.Minute), "superseded")
	lapsed := closeAs(now.Add(-90*time.Minute), now.Add(-30*time.Minute), "lapsed")
	stopped := f.confirmation(now.Add(-80*time.Minute), now.Add(-80*time.Minute), "")
	_, err := f.db.Exec(`UPDATE llm_conversation_messages SET status = 'TERMINATED' WHERE id = $1`, stopped)
	require.NoError(t, err)

	rows, err := deniedConfirmationsIn(ctx, f.db, f.account, "event-"+incidentFingerprint, current)
	require.NoError(t, err)
	got := deniedApprovalsOf(f.account, current, rows)
	require.Len(t, got, 2, "a person's Stop is not a denial")
	byId := map[string]deniedApproval{}
	for _, d := range got {
		byId[d.MessageId] = d
	}
	assert.Equal(t, "superseded", byId[superseded].Reason)
	assert.Equal(t, now.Add(-5*time.Hour), byId[superseded].AskedAt, "shown at the time it was asked: under the earlier episode")
	assert.Equal(t, person, byId[superseded].AskedBy)
	assert.Equal(t, "lapsed", byId[lapsed].Reason)
	assert.Empty(t, byId[lapsed].DeniedBy, "nobody denied it")

	rows, err = deniedConfirmationsIn(ctx, f.db, f.account, "event-"+incidentFingerprint, old)
	require.NoError(t, err)
	assert.Empty(t, rows, "the earlier incident does not list what the new one closed")
}
