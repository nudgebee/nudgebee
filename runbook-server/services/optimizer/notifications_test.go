package optimizer

import (
	"strings"
	"testing"

	"nudgebee/runbook/internal/model"

	"github.com/google/uuid"
)

func strptr(s string) *string { return &s }

func uuidptr() *uuid.UUID { id := uuid.New(); return &id }

func task(status string, resolutionID *uuid.UUID, ticketLink *string, ns, kind, name string) model.AutoOptimizeTask {
	return model.AutoOptimizeTask{
		ID:               uuid.New(),
		RecommendationID: uuidptr(),
		Status:           status,
		Reason:           strptr("CPU 500m → 250m"),
		ResourceFilter:   model.AutoOptimizeResourceFilter{Namespace: &ns, Type: &kind, Name: &name},
		Attributes:       model.AutoOptimizeTaskAttributes{ResolutionID: resolutionID, TicketLink: ticketLink},
	}
}

// withPRAction stamps the api-server's verdict on what the apply did to the PR.
func withPRAction(t model.AutoOptimizeTask, action string) model.AutoOptimizeTask {
	t.Attributes.PRAction = action
	return t
}

func TestClassifyTask(t *testing.T) {
	resID := uuidptr()
	link := strptr("https://jira/BROW-1")
	complete := string(model.AutopilotTaskStatusComplete)
	cases := []struct {
		name string
		in   model.AutoOptimizeTask
		want taskOutcome
	}{
		{"in-place applied", task(complete, nil, nil, "app", "Deployment", "api"), outcomeApplied},
		{"gitops pr", task(complete, resID, nil, "app", "Deployment", "web"), outcomePR},
		{"ticket", task(complete, resID, link, "app", "Deployment", "worker"), outcomeTicket},
		{"failed", task(string(model.AutopilotTaskStatusFailed), nil, nil, "app", "Deployment", "cron"), outcomeFailed},
		{"skipped", task(string(model.AutopilotTaskStatusSkipped), nil, nil, "app", "Deployment", "cache"), outcomeNoChange},
		{"dry-run", task(string(model.AutoOptimizeStatusDryrun), nil, nil, "app", "Deployment", "db"), outcomeNoChange},
		// The open-PR guard hands back the same resolution every run; that is not a
		// change and must not be reported as one.
		{"pr left untouched", withPRAction(task(complete, resID, nil, "app", "Deployment", "web"), model.PRActionUnchanged), outcomePRUnchanged},
		{"pr rewritten with new values", withPRAction(task(complete, resID, nil, "app", "Deployment", "web"), "refreshed"), outcomePR},
		{"pr newly raised", withPRAction(task(complete, resID, nil, "app", "Deployment", "web"), "created"), outcomePR},
		// An api-server that predates the field says nothing; keep the old reading.
		{"no pr action reported", task(complete, resID, nil, "app", "Deployment", "web"), outcomePR},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := classifyTask(c.in); got != c.want {
				t.Fatalf("classifyTask = %v, want %v", got, c.want)
			}
		})
	}
}

func TestBuildCompletionSummary_ChangeGate(t *testing.T) {
	name := "Auto optimize for deployment app-dev"
	ao := &model.AutoOptimize{ID: uuid.New(), AccountID: uuid.New(), Name: &name}

	// All skipped/dry-run → no notification.
	noChange := []model.AutoOptimizeTask{
		task(string(model.AutopilotTaskStatusSkipped), nil, nil, "app", "Deployment", "a"),
		task(string(model.AutoOptimizeStatusDryrun), nil, nil, "app", "Deployment", "b"),
	}
	if body, has := buildCompletionSummary(ao, noChange, "slack"); has || body != "" {
		t.Fatalf("expected no-change summary to be suppressed, got has=%v body=%q", has, body)
	}

	// The hourly-noise case: every task completed, but each was handed back a pull
	// request that was already open. Nothing changed, so nothing is sent.
	allReused := []model.AutoOptimizeTask{
		withPRAction(task(string(model.AutopilotTaskStatusComplete), uuidptr(), nil, "app", "Deployment", "a"), model.PRActionUnchanged),
		withPRAction(task(string(model.AutopilotTaskStatusComplete), uuidptr(), nil, "app", "Deployment", "b"), model.PRActionUnchanged),
		task(string(model.AutopilotTaskStatusSkipped), nil, nil, "app", "Deployment", "c"),
	}
	if body, has := buildCompletionSummary(ao, allReused, "slack"); has || body != "" {
		t.Fatalf("expected a run that only reused open PRs to be suppressed, got has=%v body=%q", has, body)
	}

	// But when something else did change, the reused ones are still worth naming.
	someReused := []model.AutoOptimizeTask{
		task(string(model.AutopilotTaskStatusComplete), nil, nil, "app", "Deployment", "api"),
		withPRAction(task(string(model.AutopilotTaskStatusComplete), uuidptr(), nil, "app", "Deployment", "a"), model.PRActionUnchanged),
	}
	body, has := buildCompletionSummary(ao, someReused, "slack")
	if !has {
		t.Fatal("expected summary to be sent when a change accompanies the reused PRs")
	}
	if !strings.Contains(body, "1 already have an open pull request.") {
		t.Errorf("summary missing the reused-PR line\n---\n%s", body)
	}
	if strings.Contains(body, "Pull requests in progress") {
		t.Errorf("reused PR must not be reported as one this run raised\n---\n%s", body)
	}

	// Mixed outcomes → send, with each section present.
	mixed := []model.AutoOptimizeTask{
		task(string(model.AutopilotTaskStatusComplete), nil, nil, "app", "Deployment", "api"),                          // applied
		task(string(model.AutopilotTaskStatusComplete), uuidptr(), nil, "app", "Deployment", "web"),                    // pr
		task(string(model.AutopilotTaskStatusComplete), uuidptr(), strptr("https://jira/X-1"), "app", "Deploy", "wkr"), // ticket
		task(string(model.AutopilotTaskStatusFailed), nil, nil, "app", "Deployment", "cron"),                           // failed
		task(string(model.AutopilotTaskStatusSkipped), nil, nil, "app", "Deployment", "cache"),                         // skipped
	}
	body, has = buildCompletionSummary(ao, mixed, "slack")
	if !has {
		t.Fatal("expected change summary to be sent")
	}
	for _, want := range []string{"Applied in-place (1)", "Pull requests in progress (1)", "Tickets created (1)", "Failed (1)", "1 skipped.", "https://jira/X-1", "View ticket", "app/Deployment/api", "View in Nudgebee"} {
		if !strings.Contains(body, want) {
			t.Errorf("summary missing %q\n---\n%s", want, body)
		}
	}
}

func TestBuildPRsReadySummary(t *testing.T) {
	name := "AO"
	ao := &model.AutoOptimize{ID: uuid.New(), AccountID: uuid.New(), Name: &name}

	prTask := task(string(model.AutopilotTaskStatusComplete), uuidptr(), nil, "app", "Deployment", "web")
	failTask := task(string(model.AutopilotTaskStatusComplete), uuidptr(), nil, "app", "Deployment", "api")
	tasks := []model.AutoOptimizeTask{prTask, failTask}

	// Nothing settled yet → empty body.
	if body := buildPRsReadySummary(ao, tasks, map[uuid.UUID][]model.RecommendationResolution{}, "slack"); body != "" {
		t.Fatalf("expected empty body when no PR settled, got %q", body)
	}

	resolutions := map[uuid.UUID][]model.RecommendationResolution{
		*prTask.RecommendationID:   {{Status: string(model.RecommendationResolutionStatusInProgress), TypeReferenceID: "https://github.com/o/r/pull/7"}},
		*failTask.RecommendationID: {{Status: string(model.RecommendationResolutionStatusFailed), StatusMessage: strptr("clone failed")}},
	}
	body := buildPRsReadySummary(ao, tasks, resolutions, "slack")
	for _, want := range []string{"Pull requests created (1)", "https://github.com/o/r/pull/7", "PR #7", "PR creation failed (1)", "clone failed"} {
		if !strings.Contains(body, want) {
			t.Errorf("PRs-ready summary missing %q\n---\n%s", want, body)
		}
	}
}

func TestResolutionSettled(t *testing.T) {
	if resolutionSettled([]model.RecommendationResolution{{Status: string(model.RecommendationResolutionStatusInProgress), TypeReferenceID: ""}}) {
		t.Error("InProgress with no PR URL should not be settled")
	}
	if !resolutionSettled([]model.RecommendationResolution{{Status: string(model.RecommendationResolutionStatusInProgress), TypeReferenceID: "url"}}) {
		t.Error("InProgress with PR URL should be settled")
	}
	if !resolutionSettled([]model.RecommendationResolution{{Status: string(model.RecommendationResolutionStatusFailed)}}) {
		t.Error("Failed should be settled")
	}
}
