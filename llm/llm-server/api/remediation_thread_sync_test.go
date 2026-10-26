package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/config"
	"nudgebee/llm/events"
	"nudgebee/llm/security"
)

// --- drawing: one state at a time -----------------------------------------------------------------------------

var threadAt = time.Date(2026, 10, 24, 14, 2, 0, 0, time.UTC)

func threadPlan() RemediationPlan {
	return RemediationPlan{Version: 2, RootCause: "bad image tag", Summary: "roll back", Revision: &PlanRevision{PreviousVersion: "pv-1"},
		Actions: []RemediationAction{
			{Action: "Roll back", Title: "Roll back the image", Confidence: 90, ExecuteCommand: "kubectl rollout undo deployment/web -n shop",
				RollbackCommand: "kubectl rollout undo deployment/web -n shop", Delivery: DeliveryRun},
			{Action: "Pin the tag", RequiredChange: &RequiredChange{Where: "code", Target: "chart/values.yaml", Change: "pin the tag", Repo: "example/web"}},
			{Action: "Raise the quota", RequiredChange: &RequiredChange{Where: "infrastructure", Target: "the project quota", Change: "raise it"}},
		}}
}

func threadView() alertThreadView {
	return alertThreadView{TenantId: "t", AccountId: "a", PlanEventId: "e", MainFingerprint: "fp-main", Threads: []string{"fp-main"},
		Seq: 5, HasPlan: true, PlanVersion: "pv-2", Plan: threadPlan(), Loop: &remediationLoopState{PlanStatus: PlanStatusReady,
			OptionRepos: []optionRepo{{Option: 2, Repo: "https://github.com/example/web", Known: repoKnownWorkload}}},
		Names: map[string]string{"u-1": "Asha", "u-2": "Ravi"}}
}

func threadRun(id, status string, at time.Time, data string) alertThreadRun {
	r := alertThreadRun{Id: id, Type: "CommandExecution", Status: status, CreatedAt: at, RawData: []byte(data)}
	_ = json.Unmarshal(r.RawData, &r.Data)
	return r
}

func optionsOf(t *testing.T, view alertThreadView) threadPost {
	t.Helper()
	for _, p := range alertThreadPosts(view) {
		if p.Key == optionsKey(view.PlanEventId, view.PlanVersion) {
			return p
		}
	}
	t.Fatal("no options message")
	return threadPost{}
}

func kinds(buttons []threadButton) []string {
	out := []string{}
	for _, b := range buttons {
		out = append(out, b.Kind)
	}
	return out
}

// allKinds is the kinds of a message's buttons: those beside its sections, then those below.
func allKinds(post threadPost) []string {
	out := []string{}
	for _, s := range post.Sections {
		if s.Button != nil {
			out = append(out, s.Button.Kind)
		}
	}
	return append(out, kinds(post.Buttons)...)
}

// Picking: one message with every option, a button each by how it is carried out, and Other fixes…; it names the plan
// for the clicks, and is edited in place.
func TestThreadOptionsWhilePicking(t *testing.T) {
	view := threadView()
	posts := alertThreadPosts(view)
	require.Len(t, posts, 2, "the replaced plan's message, then the current one; no verdict")
	assert.Equal(t, threadPost{Key: "options:e:pv-1", Edit: true, OnlyIfPosted: true, Text: "### Fix options (replaced)\n_Replaced by newer fix options below._"}, posts[0],
		"the older plan's options lose their buttons, and are never posted fresh")
	options := posts[1]
	assert.Equal(t, "options:e:pv-2", options.Key)
	assert.True(t, options.Edit)
	assert.Equal(t, "remediation-plan:t:a:e:pv-2", options.Token)
	assert.Equal(t, "### Fix options · v2\n**Root cause:** bad image tag", options.Text, "the cause in one line; the options are sections")
	require.Len(t, options.Sections, 3, "one section per option")
	assert.Equal(t, "**1 · Roll back the image** · _fix_\n`kubectl rollout undo deployment/web -n shop`", options.Sections[0].Text)
	assert.Equal(t, "**2 · Pin the tag** · _code_\npin the tag in chart/values.yaml (example/web)", options.Sections[1].Text)
	assert.Equal(t, "**3 · Raise the quota** · _infrastructure change_\nraise it in the project quota", options.Sections[2].Text)
	assert.Equal(t, threadButton{Kind: "pick", Label: "Use this fix", Option: 1, Style: "primary"}, *options.Sections[0].Button)
	assert.Equal(t, threadButton{Kind: "pr", Label: "Raise a PR", Option: 2}, *options.Sections[1].Button)
	assert.Equal(t, threadButton{Kind: "manual", Label: "Mark done", Option: 3}, *options.Sections[2].Button,
		"an infrastructure change with no repository named is made outside one")
	assert.Equal(t, []string{"other"}, kinds(options.Buttons), "Other fixes… stays below the options")
	// The same code fix with its repository not known for the workload: the person chooses it on the tab.
	view.Loop.OptionRepos = nil
	choose := *optionsOf(t, view).Sections[1].Button
	assert.Equal(t, "link", choose.Kind)
	assert.Equal(t, "Choose the repo in NudgeBee", choose.Label)
	assert.Contains(t, options.Status, "Pick a fix")
	assert.NotContains(t, options.Text, "remediation:", "no per-step Run token any more")
}

// An option names what it is and what it gives up in one short line: a mitigation buys time, its trade-off said; a
// long command or cause is cut, never shown behind Slack's "Show more"; a deployment-source change says so.
func TestThreadOptionLines(t *testing.T) {
	view := threadView()
	view.Loop.OptionRepos = nil // the chart's repository is not known for the workload
	view.Plan.RootCause = "The kafka container's memory limit is too low.\nIt is set to 512Mi while the broker needs 1.2Gi."
	view.Plan.Actions = []RemediationAction{
		{Title: "Increase Kafka container memory", Kind: RemediationKindMitigation, Delivery: DeliveryRun,
			ExecuteCommand: "kubectl set resources deployment/kafka -n demo --limits=memory=2Gi " + strings.Repeat("--x=y ", 60),
			TradeOff:       "Argo CD reverts it on the next sync"},
		{Title: "Raise the limit in the chart", Kind: RemediationKindFix,
			RequiredChange: &RequiredChange{Where: "deployment_source", Target: "charts/kafka/values.yaml", Change: "set resources.limits.memory to 2Gi"}},
	}
	options := optionsOf(t, view)
	assert.Equal(t, "### Fix options · v2\n**Root cause:** The kafka container's memory limit is too low.", options.Text)
	require.Len(t, options.Sections, 2)
	first := options.Sections[0].Text
	assert.True(t, strings.HasPrefix(first, "**1 · Increase Kafka container memory** · _buys time_\n`kubectl set resources deployment/kafka"), first)
	assert.True(t, strings.HasSuffix(first, " · Heads-up: Argo CD reverts it on the next sync"), first)
	assert.Less(t, len(first), 500, "one short line")
	assert.Equal(t, "**2 · Raise the limit in the chart** · _change in the deployment source_\nset resources.limits.memory to 2Gi in charts/kafka/values.yaml", options.Sections[1].Text)
	assert.Equal(t, "Choose the repo in NudgeBee", options.Sections[1].Button.Label, "a chart change with no repository known: chosen on the tab")
}

// Not picking: the options show without buttons beside them.
func TestThreadOptionsWithoutPicking(t *testing.T) {
	view := threadView()
	view.Loop.ConversationBusy = true
	options := optionsOf(t, view)
	require.Len(t, options.Sections, 3)
	for _, s := range options.Sections {
		assert.Nil(t, s.Button)
	}
}

// Every state the tab knows is a footer line, and buttons show only while picking.
func TestThreadOptionsFooterPerState(t *testing.T) {
	asked := threadAt.Add(-time.Hour)
	swap(t, &config.Config.BaseUrl, "https://app.example.com")
	cases := []struct {
		name    string
		change  func(v *alertThreadView)
		status  []string
		buttons []string
	}{
		{"waiting in Slack", func(v *alertThreadView) {
			v.Loop.PendingApprovals = []pendingApproval{{Origin: ApprovalOriginSlack, CreatedAt: asked, ThreadUrl: "https://x.slack.com/archives/C1/p1"}}
		}, []string{"**Waiting for approval in Slack**, asked at 13:02 UTC", "<https://x.slack.com/archives/C1/p1|open>"}, []string{}},
		{"waiting on the tab", func(v *alertThreadView) {
			v.Loop.PendingApprovals = []pendingApproval{{Origin: ApprovalOriginWeb, CreatedAt: asked}}
		}, []string{"**Waiting for approval on the Remediation tab**", "<https://app.example.com/investigate?id=e|open>", "It is answered there."}, []string{}},
		{"preparing", func(v *alertThreadView) { v.Loop.PlanStatus = PlanStatusPreparing }, []string{"being prepared"}, []string{}},
		{"stale", func(v *alertThreadView) {
			v.Loop.Relevance = &planRelevance{Stale: true, StaleReason: "the alert fired again with a new cause"}
		}, []string{"**These fixes are out of date**: the alert fired again with a new cause."}, []string{}},
		{"conversation busy (a pick just sent)", func(v *alertThreadView) { v.Loop.ConversationBusy = true }, []string{"Nubi is working"}, []string{}},
		{"approved in Slack, running", func(v *alertThreadView) {
			v.Runs = []alertThreadRun{threadRun("r1", "InProgress", threadAt, `{"option":1,"plan_version":"pv-2","approved_by":"u-1","approved_from":"slack","approval_note":"after the freeze"}`)}
		}, []string{"**Option 1 · Roll back the image:** approved by Asha in Slack at 14:02 UTC: “after the freeze” · running"}, []string{}},
		{"approved on the tab, verifying", func(v *alertThreadView) {
			v.Runs = []alertThreadRun{threadRun("r1", "Success", threadAt, `{"option":1,"plan_version":"pv-2","approved_by":"u-2","approved_from":"web","followup":{"hold_until":"2026-10-24T14:40:00Z"}}`)}
		}, []string{"approved by Ravi on the tab at 14:02 UTC · verifying until 14:40 UTC"}, []string{}},
		{"likely fixed", func(v *alertThreadView) {
			v.Runs = []alertThreadRun{threadRun("r1", "Success", threadAt, `{"option":1,"plan_version":"pv-2","approved_by":"u-1","followup":{"verdict":"fixed","verdict_reason":"cleared_after_change_and_held","done_at":"x"}}`)}
		}, []string{"likely fixed: the alert cleared after the change"}, []string{}},
		{"not fixed: a person picks again", func(v *alertThreadView) {
			v.Runs = []alertThreadRun{threadRun("r1", "Success", threadAt, `{"option":1,"plan_version":"pv-2","followup":{"verdict":"not_fixed","verdict_reason":"still_firing","done_at":"x"}}`)}
		}, []string{"not fixed: the alert was still firing at the re-check"}, []string{"other"}},
		{"came back", func(v *alertThreadView) {
			v.Runs = []alertThreadRun{threadRun("r1", "Success", threadAt, `{"option":1,"plan_version":"pv-2","followup":{"verdict":"fixed","done_at":"x"}}`)}
			v.Loop.RefireAfterRun = &refireAfterRun{RunId: "r1", FiredAt: threadAt.Add(30 * time.Minute), MinutesAfter: 30}
		}, []string{"**The alert came back** at 14:32 UTC, 30 min after the fix."}, []string{}},
		{"resolved", func(v *alertThreadView) {
			v.NbStatus, v.ResolvedAt = "RESOLVED", &threadAt
			v.ResolvedBy = alertResolvedBy{RunId: "r1", ConfirmedByName: "Asha"}
		}, []string{"**Resolved** by Asha at 14:02 UTC."}, []string{}},
		// e2e of PR 2: "I made this change" records its step, not its option.
		{"made by a person (a manual record names its step)", func(v *alertThreadView) {
			v.Runs = []alertThreadRun{threadRun("r1", "Success", threadAt, `{"step":"2:apply","slot":"manual","manual":true,"source":"slack","plan_version":"pv-2","approved_by":"u-1"}`)}
		}, []string{"**Option 3 · Raise the quota:** made by Asha at 14:02 UTC · checking whether it worked"}, []string{}},
		{"a run on an older plan does not count", func(v *alertThreadView) {
			v.Runs = []alertThreadRun{threadRun("r0", "Success", threadAt, `{"option":1,"plan_version":"pv-1"}`)}
		}, []string{"Pick a fix"}, []string{"pick", "pr", "manual", "other"}},
		{"a declined or undone step does not count", func(v *alertThreadView) {
			v.Runs = []alertThreadRun{threadRun("r0", "Declined", threadAt, `{"option":1,"plan_version":"pv-2"}`),
				threadRun("r1", "Success", threadAt, `{"option":1,"plan_version":"pv-2","slot":"rollback"}`)}
		}, []string{"Pick a fix"}, []string{"pick", "pr", "manual", "other"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			view := threadView()
			c.change(&view)
			options := optionsOf(t, view)
			for _, s := range c.status {
				assert.Contains(t, options.Status, s)
			}
			assert.Equal(t, c.buttons, allKinds(options))
		})
	}
}

// A run judged likely fixed gets its own message with Resolve while it can be confirmed; resolved, back, or replaced by
// a later change, the button goes and the footer says why.
func TestThreadVerdictMessage(t *testing.T) {
	fixed := threadRun("r1", "Success", threadAt, `{"option":1,"plan_version":"pv-2","followup":{"verdict":"fixed","verdict_reason":"cleared_after_change_and_held","done_at":"x"}}`)
	verdictOf := func(view alertThreadView) threadPost {
		for _, p := range alertThreadPosts(view) {
			if p.Key == "verdict:r1" {
				return p
			}
		}
		t.Fatal("no verdict message")
		return threadPost{}
	}
	view := threadView()
	view.Runs = []alertThreadRun{fixed}
	v := verdictOf(view)
	assert.Equal(t, "remediation-run:t:a:e:r1", v.Token)
	assert.True(t, v.Edit)
	assert.Equal(t, "**Likely fixed:** the alert cleared after the change and was still clear at the re-check. Resolve to close the event in your name.", v.Text)
	assert.Equal(t, []threadButton{{Kind: "resolve", Label: "Resolve", RunId: "r1", Style: "primary"}}, v.Buttons)

	resolved := view
	resolved.NbStatus, resolved.ResolvedAt, resolved.ResolvedBy = "RESOLVED", &threadAt, alertResolvedBy{RunId: "r1", ConfirmedByName: "Asha"}
	v = verdictOf(resolved)
	assert.Empty(t, v.Buttons)
	assert.Equal(t, "**Resolved** by Asha at 14:02 UTC.", v.Status, "resolved on the tab shows here too")

	back := view
	back.Loop = &remediationLoopState{RefireAfterRun: &refireAfterRun{RunId: "r1", FiredAt: threadAt.Add(time.Hour)}}
	v = verdictOf(back)
	assert.Empty(t, v.Buttons)
	assert.Contains(t, v.Status, "came back")

	later := view
	later.Runs = []alertThreadRun{fixed, threadRun("r2", "Success", threadAt.Add(time.Hour), `{"option":1,"plan_version":"pv-2"}`)}
	v = verdictOf(later)
	assert.Empty(t, v.Buttons)
	assert.Equal(t, "A later change replaced this one.", v.Status)

	cleared := view
	cleared.Runs = []alertThreadRun{threadRun("r1", "Success", threadAt, `{"followup":{"verdict":"cleared_before_change","verdict_reason":"cleared_before_change"}}`)}
	assert.True(t, strings.HasPrefix(verdictOf(cleared).Text, "**Cleared before the change:**"))

	notFixed := view
	notFixed.Runs = []alertThreadRun{threadRun("r1", "Success", threadAt, `{"followup":{"verdict":"not_fixed"}}`)}
	for _, p := range alertThreadPosts(notFixed) {
		assert.NotEqual(t, "verdict:r1", p.Key, "only a likely fix gets a verdict message")
	}
}

// The episode: the same state goes into the main alert's thread and each duplicate's, keyed per thread, with the state
// number; a duplicate's names its main alert. A one-off report carries no state number.
func TestAlertThreadSendsFanOutToTheEpisode(t *testing.T) {
	view := threadView()
	view.Threads = []string{"fp-main", "fp-dup-1", "fp-dup-2"}
	posts := alertThreadPosts(view)
	sends := alertThreadSends(view, posts, true)
	require.Len(t, sends, 3)
	for i, s := range sends {
		assert.Equal(t, view.Threads[i], s.Fingerprint)
		assert.Equal(t, int64(5), s.StateSeq)
		assert.Equal(t, "e", s.EventId, "the plan's event, for every thread")
		assert.Equal(t, posts, s.Posts, "the same messages; notifications-server keys them per thread")
	}
	assert.Empty(t, sends[0].MainFingerprint)
	assert.Equal(t, "fp-main", sends[1].MainFingerprint)
	assert.Zero(t, alertThreadSends(view, posts, false)[0].StateSeq)
}

// --- reading: the database ---------------------------------------------------------------------------------------

type threadDBFixture struct {
	t       *testing.T
	db      *sqlx.DB
	account string
	tenant  string
	main    string
}

func newThreadDBFixture(t *testing.T) *threadDBFixture {
	t.Helper()
	db := remediationScratchDB(t, "thread", `CREATE TABLE event_resolution (LIKE public.event_resolution INCLUDING ALL);
		CREATE TABLE events (id uuid PRIMARY KEY, cloud_account_id uuid NOT NULL, fingerprint text, nb_status text,
			nb_status_changed_at timestamp, labels jsonb, created_at timestamp NOT NULL DEFAULT LOCALTIMESTAMP);
		CREATE TABLE cloud_accounts (id uuid PRIMARY KEY, tenant uuid);
		CREATE SEQUENCE remediation_thread_state_seq`)
	f := &threadDBFixture{t: t, db: db, account: uuid.NewString(), tenant: uuid.NewString(), main: uuid.NewString()}
	_, err := db.Exec(`INSERT INTO cloud_accounts VALUES ($1, $2)`, f.account, f.tenant)
	require.NoError(t, err)
	f.event(f.main, "fp-main", "", "{}", -time.Hour)
	return f
}

func (f *threadDBFixture) event(id, fingerprint, nbStatus, labels string, ago time.Duration) {
	f.t.Helper()
	_, err := f.db.Exec(`INSERT INTO events (id, cloud_account_id, fingerprint, nb_status, labels, created_at) VALUES ($1, $2, $3, NULLIF($4, ''), $5::jsonb, LOCALTIMESTAMP + $6::interval)`,
		id, f.account, fingerprint, nbStatus, labels, fmt.Sprintf("%d seconds", int(ago.Seconds())))
	require.NoError(f.t, err)
}

func (f *threadDBFixture) run(data string) string {
	f.t.Helper()
	id := uuid.NewString()
	_, err := f.db.Exec(`INSERT INTO event_resolution (id, event_id, type, data, status, type_reference_id, resolver_type, resolver_id)
		VALUES ($1, $2, 'CommandExecution', $3, 'Success', 'kubectl', 'User', 'u-1')`, id, f.main, data)
	require.NoError(f.t, err)
	return id
}

// The duplicates' threads: followers marked duplicates of the main event with an alert of their own, once each; not
// the main alert's own fingerprint, not an event that is no follower.
func TestEpisodeFollowerThreads_DB(t *testing.T) {
	f := newThreadDBFixture(t)
	dupOf := `{"nb_duplicate_of":"` + f.main + `"}`
	f.event(uuid.NewString(), "fp-dup-1", "DUPLICATE", dupOf, 0)
	f.event(uuid.NewString(), "fp-dup-1", "DUPLICATE", dupOf, time.Minute)
	f.event(uuid.NewString(), "fp-dup-2", "SUPPRESSED", dupOf, 2*time.Minute)
	f.event(uuid.NewString(), "fp-main", "DUPLICATE", dupOf, 0)
	f.event(uuid.NewString(), "fp-other", "", "{}", 0)
	f.event(uuid.NewString(), "fp-open", "OPEN", dupOf, 0)
	got, err := episodeFollowerThreads(context.Background(), f.db, f.account, f.main)
	require.NoError(t, err)
	assert.Equal(t, []string{"fp-dup-2", "fp-dup-1"}, got)
}

// Each sync takes a higher state number, before it reads anything.
func TestNextThreadStateSeq_DB(t *testing.T) {
	f := newThreadDBFixture(t)
	a, err := nextThreadStateSeq(context.Background(), f.db)
	require.NoError(t, err)
	b, err := nextThreadStateSeq(context.Background(), f.db)
	require.NoError(t, err)
	assert.Greater(t, b, a)

	var order []string
	swap(t, &resolveThreadPlanEvent, func(*security.RequestContext, string, string) (string, *events.EventInfo, error) {
		return f.main, &events.EventInfo{ID: f.main, Fingerprint: "fp-main"}, nil
	})
	swap(t, &nextThreadStateSeq, func(ctx context.Context, db *sqlx.DB) (int64, error) {
		order = append(order, "seq")
		return 9, nil
	})
	swap(t, &readThreadRuns, func(ctx context.Context, db *sqlx.DB, eventId string) ([]alertThreadRun, error) {
		order = append(order, "runs")
		return nil, nil
	})
	swap(t, &readThreadLoop, func(*security.RequestContext, string, string, *events.EventInfo, RemediationPlan) *remediationLoopState {
		order = append(order, "loop")
		return nil
	})
	stubSlackPlan(t, "pv-1", true)
	view, err := readAlertThreadView(security.NewRequestContextForSuperAdmin(), f.db, f.account, f.main)
	require.NoError(t, err)
	assert.Equal(t, []string{"seq", "loop", "runs"}, order, "the number is taken first: a higher number has read every change before it")
	assert.Equal(t, int64(9), view.Seq)
	assert.Equal(t, f.tenant, view.TenantId)
}

// The end to end of one sync, against the database with the plan and loop read stubbed: one send per thread of the
// episode, each with the options and the verdict, in state order; a second sync is a higher number.
func TestSyncAlertThread_DB(t *testing.T) {
	f := newThreadDBFixture(t)
	f.event(uuid.NewString(), "fp-dup", "DUPLICATE", `{"nb_duplicate_of":"`+f.main+`"}`, 0)
	run := f.run(`{"option":1,"plan_version":"pv-1","followup":{"verdict":"fixed","verdict_reason":"cleared_after_change_and_held","done_at":"x"}}`)
	withMetastore(t, f.db)
	slackOn(t, f.account, true)
	stubSlackPlan(t, "pv-1", true)
	swap(t, &resolveThreadPlanEvent, func(*security.RequestContext, string, string) (string, *events.EventInfo, error) {
		return f.main, &events.EventInfo{ID: f.main, Fingerprint: "fp-main"}, nil
	})
	swap(t, &readThreadLoop, func(*security.RequestContext, string, string, *events.EventInfo, RemediationPlan) *remediationLoopState {
		return &remediationLoopState{PlanStatus: PlanStatusReady}
	})
	swap(t, &userDisplayNames, func(*security.RequestContext, []string) (map[string]string, error) { return map[string]string{}, nil })
	c := captureSlackSends(t)

	syncAlertThread(security.NewRequestContextForSuperAdmin(), f.account, f.main)
	c.wait(t)
	c.wait(t)
	threadPostsInFlight.Wait()
	require.Equal(t, 2, c.count())
	assert.Equal(t, []string{"fp-main", "fp-dup"}, []string{c.calls[0].Fingerprint, c.calls[1].Fingerprint})
	assert.Equal(t, "fp-main", c.calls[1].MainFingerprint)
	keys := []string{}
	for _, p := range c.calls[0].Posts {
		keys = append(keys, p.Key)
	}
	assert.Equal(t, []string{"options:" + f.main + ":pv-1", "verdict:" + run}, keys)
	first := c.calls[0].StateSeq

	syncAlertThread(security.NewRequestContextForSuperAdmin(), f.account, f.main)
	threadPostsInFlight.Wait()
	require.Equal(t, 4, c.count())
	assert.Greater(t, c.calls[2].StateSeq, first)

	// The loop off for the account: nothing is drawn.
	slackOn(t, f.account, false)
	syncAlertThread(security.NewRequestContextForSuperAdmin(), f.account, f.main)
	threadPostsInFlight.Wait()
	assert.Equal(t, 4, c.count())
}

// A resolve on the tab reaches the thread on the tick, once.
func TestSyncResolvedThreads_DB(t *testing.T) {
	f := newThreadDBFixture(t)
	run := f.run(`{"option":1,"followup":{"verdict":"fixed","done_at":"x"}}`)
	_, err := f.db.Exec(`UPDATE event_resolution SET created_at = LOCALTIMESTAMP - interval '10 minutes' WHERE id = $1`, run)
	require.NoError(t, err)
	_, err = f.db.Exec(`UPDATE events SET nb_status = 'RESOLVED', nb_status_changed_at = LOCALTIMESTAMP - interval '5 minutes' WHERE id = $1`, f.main)
	require.NoError(t, err)
	slackOn(t, f.account, true)
	var synced []string
	var mu sync.Mutex
	swap(t, &syncAlertThread, func(_ *security.RequestContext, accountId, eventId string) {
		mu.Lock()
		defer mu.Unlock()
		synced = append(synced, eventId)
	})
	syncResolvedThreads(f.db)
	syncResolvedThreads(f.db)
	assert.Equal(t, []string{f.main}, synced, "drawn once")
	var at string
	require.NoError(t, f.db.Get(&at, `SELECT data->>'thread_synced_at' FROM event_resolution WHERE id = $1`, run))
	assert.NotEmpty(t, at)
}

// --- hooks --------------------------------------------------------------------------------------------------------

func captureSyncs(t *testing.T) *[]string {
	t.Helper()
	var mu sync.Mutex
	got := []string{}
	swap(t, &syncAlertThread, func(_ *security.RequestContext, accountId, eventId string) {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, accountId+"/"+eventId)
	})
	return &got
}

// A turn of an alert's conversation (an approval raised, answered, or its run done) draws its thread; any other
// conversation, or the loop off, draws nothing.
func TestAfterConversationTurnDrawsTheAlertThread(t *testing.T) {
	got := captureSyncs(t)
	swap(t, &newestEventOfAlert, func(_ *security.RequestContext, accountId, alert string) (string, error) {
		return "evt-of-" + alert, nil
	})
	slackOn(t, "acct", true)
	ctx := security.NewRequestContextForSuperAdmin()
	for _, session := range []string{"event-fp-1", "event-rca-fp-1", "C1-1700000000.0001", "event-"} {
		core.AfterConversationTurn(ctx, core.NBAgentRequest{AccountId: "acct", SessionId: session})
	}
	assert.Equal(t, []string{"acct/evt-of-fp-1"}, *got)
	slackOn(t, "acct", false)
	core.AfterConversationTurn(ctx, core.NBAgentRequest{AccountId: "acct", SessionId: "event-fp-1"})
	assert.Len(t, *got, 1)

	slackOn(t, "acct", true)
	swap(t, &sessionOfConversation, func(string) string { return "event-fp-2" })
	core.AfterWaitingClosed(ctx, "acct", "conv-1")
	assert.Equal(t, []string{"acct/evt-of-fp-1", "acct/evt-of-fp-2"}, *got, "a lapsed approval draws it too")
}

// "Likely fixed" is the verdict message, drawn by the sync: it is not posted as a report as well.
func TestLikelyFixedIsTheVerdictMessageNotAReport(t *testing.T) {
	got := captureSyncs(t)
	c := captureSlackSends(t)
	slackOn(t, "acct", true)
	postFollowupToThread(followupRun{Id: "r1", AccountId: "acct", EventId: "e1"}, map[string]any{"outcome": followupLikelyFixed, "summary": "x"}, "")
	postFollowupToThread(followupRun{Id: "r1", AccountId: "acct", EventId: "e1"}, map[string]any{"outcome": followupSuperseded}, "")
	threadPostsInFlight.Wait()
	assert.Equal(t, []string{"acct/e1", "acct/e1"}, *got)
	assert.Zero(t, c.count(), "no report post")
}

// A step's approval card learns whether the investigation is newer than the plan; nothing for another conversation
// or with the loop off.
func TestRemediationApprovalDetails(t *testing.T) {
	slackOn(t, "acct", true)
	swap(t, &newestEventOfAlert, func(*security.RequestContext, string, string) (string, error) { return "e1", nil })
	swap(t, &resolveThreadPlanEvent, func(*security.RequestContext, string, string) (string, *events.EventInfo, error) {
		return "e1", &events.EventInfo{ID: "e1"}, nil
	})
	stubSlackPlan(t, "pv-1", true)
	newer := true
	swap(t, &readThreadLoop, func(*security.RequestContext, string, string, *events.EventInfo, RemediationPlan) *remediationLoopState {
		if newer {
			return &remediationLoopState{InvestigationAfterPlan: &investigationAfterPlan{At: threadAt}}
		}
		return &remediationLoopState{}
	})
	ctx := security.NewRequestContextForSuperAdmin()
	request := core.NBAgentRequest{AccountId: "acct", SessionId: "event-fp"}
	assert.Equal(t, map[string]any{"recheck": true}, remediationApprovalDetails(ctx, request, core.FollowupRequest{}))
	newer = false
	assert.Equal(t, map[string]any{"recheck": false}, remediationApprovalDetails(ctx, request, core.FollowupRequest{}))
	assert.Nil(t, remediationApprovalDetails(ctx, core.NBAgentRequest{AccountId: "acct", SessionId: "C1-1"}, core.FollowupRequest{}))
	slackOn(t, "acct", false)
	assert.Nil(t, remediationApprovalDetails(ctx, request, core.FollowupRequest{}))
}
