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

func TestClassifyTask(t *testing.T) {
	resID := uuidptr()
	link := strptr("https://jira/BROW-1")
	cases := []struct {
		name string
		in   model.AutoOptimizeTask
		want taskOutcome
	}{
		{"in-place applied", task(string(model.AutopilotTaskStatusComplete), nil, nil, "app", "Deployment", "api"), outcomeApplied},
		{"gitops pr", task(string(model.AutopilotTaskStatusComplete), resID, nil, "app", "Deployment", "web"), outcomePR},
		{"ticket", task(string(model.AutopilotTaskStatusComplete), resID, link, "app", "Deployment", "worker"), outcomeTicket},
		{"failed", task(string(model.AutopilotTaskStatusFailed), nil, nil, "app", "Deployment", "cron"), outcomeFailed},
		{"skipped", task(string(model.AutopilotTaskStatusSkipped), nil, nil, "app", "Deployment", "cache"), outcomeNoChange},
		{"dry-run", task(string(model.AutoOptimizeStatusDryrun), nil, nil, "app", "Deployment", "db"), outcomeNoChange},
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

	// Mixed outcomes → send, with each section present.
	mixed := []model.AutoOptimizeTask{
		task(string(model.AutopilotTaskStatusComplete), nil, nil, "app", "Deployment", "api"),                          // applied
		task(string(model.AutopilotTaskStatusComplete), uuidptr(), nil, "app", "Deployment", "web"),                    // pr
		task(string(model.AutopilotTaskStatusComplete), uuidptr(), strptr("https://jira/X-1"), "app", "Deploy", "wkr"), // ticket
		task(string(model.AutopilotTaskStatusFailed), nil, nil, "app", "Deployment", "cron"),                           // failed
		task(string(model.AutopilotTaskStatusSkipped), nil, nil, "app", "Deployment", "cache"),                         // skipped
	}
	body, has := buildCompletionSummary(ao, mixed, "slack")
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
