package integration_test

import (
	"context"
	"encoding/json"
	"fmt"

	"nudgebee/runbook/internal/model"

	"github.com/google/uuid"
)

// End-to-end coverage for the open-resolution check that decides whether an auto
// optimize run acts on a recommendation or skips it. This drives the real
// GenerateTasks path — recommendation selection, task generation, and the skip
// marking in services/optimizer/executor.go — against a real Postgres, rather
// than asserting on the DAO in isolation.
//
// It exists because the check has now been wrong twice in production without any
// test noticing (#34943, then #35523, which fixed api-server's copy of the
// predicate and left runbook-server's alone). Both times the symptom was the one
// reproduced below: a recommendation skipped every hour with "already has active
// resolutions", naming a pull request that had closed months earlier or had never
// been created at all. On dev that ran for five months on two workloads.

// seedOptimizeTarget creates the full chain an auto optimize needs — agent,
// rule, resource, recommendation — and returns the rule and recommendation ids.
// Each call uses its own account so scenarios cannot see each other's rows.
func (s *IntegrationTestSuite) seedOptimizeTarget(ctx context.Context, workload string) (aoID, recID uuid.UUID) {
	accountID := uuid.New()
	tenantID := uuid.New()

	_, err := s.testWorkflowDao.Db().ExecContext(ctx, `
		INSERT INTO agent (id, tenant, cloud_account_id, status, type)
		VALUES ($1, $2, $3, 'Connected', 'k8s')`, uuid.New(), tenantID, accountID)
	s.Require().NoError(err)

	aoID = uuid.New()
	err = s.testOptimizerDao.SaveAutoOptimize(ctx, model.AutoOptimize{
		ID:        aoID,
		AccountID: accountID,
		TenantID:  tenantID,
		Category:  "vertical_rightsize",
		Status:    model.AutoOptimizeStatusActive,
		Rule: map[string]interface{}{
			"scale_up": true,
			"cpu": map[string]interface{}{
				"algo":    "P99",
				"trigger": map[string]interface{}{"change_pct": 10, "max_change_pct": 500},
			},
		},
		ScheduleTime: "0 * * * *",
		CreatedBy:    uuid.New(),
		Name:         ptr("Auto optimize for deployment " + workload),
	})
	s.Require().NoError(err)

	resID := uuid.New()
	_, err = s.testWorkflowDao.Db().ExecContext(ctx, `
		INSERT INTO cloud_resourses (id, account, tenant, resourse_id, name, type, status, region, cloud_provider, meta)
		VALUES ($1, $2, $3, $4, $5, 'Deployment', 'Active', 'us-east-1', 'k8s', '{"cpu": "100m"}')`,
		resID, accountID, tenantID, fmt.Sprintf("default/Deployment/%s", workload), workload)
	s.Require().NoError(err)

	// category/rule_name must match what GetFullRecommendationsForOptimizerCategory
	// asks for on 'vertical_rightsize', or the recommendation is never selected and
	// the scenario silently proves nothing.
	recID = uuid.New()
	// Keyed by container name, each entry a per-resource recommendation — the
	// shape the vertical-rightsize generator actually reads. A flatter payload is
	// accepted by the database and then silently produces no task, which reads
	// exactly like the skip this test is trying to detect.
	recBytes, _ := json.Marshal(map[string]interface{}{
		workload: []interface{}{
			map[string]interface{}{
				"resource":    "cpu",
				"recommended": map[string]interface{}{"request": "150m"},
				"add_info":    map[string]interface{}{"cpu_percentile_99": "140m"},
			},
		},
	})
	_, err = s.testWorkflowDao.Db().ExecContext(ctx, `
		INSERT INTO recommendation
			(id, tenant_id, cloud_account_id, resource_id, recommendation, status, category,
			 rule_name, recommendation_action, is_dismissed)
		VALUES ($1, $2, $3, $4, $5, 'Open', 'RightSizing', 'pod_right_sizing', 'apply', false)`,
		recID, tenantID, accountID, resID, recBytes)
	s.Require().NoError(err)

	return aoID, recID
}

// seedResolution attaches one resolution row to a recommendation.
func (s *IntegrationTestSuite) seedResolution(ctx context.Context, recID uuid.UUID, status, refID, resolver string, lifecycle *string) {
	_, err := s.testWorkflowDao.Db().ExecContext(ctx, `
		INSERT INTO recommendation_resolution
			(id, recommendation_id, type, status, type_reference_id, resolver_type, resolver_id,
			 created_at, pr_lifecycle_state)
		VALUES ($1, $2, 'PullRequest', $3, $4, $5, $6, now() - interval '30 days', $7)`,
		uuid.New(), recID, status, refID, resolver, uuid.New().String(), lifecycle)
	s.Require().NoError(err)
}

// skipReason returns the generated task's skip reason, or "" when the run acted
// on the recommendation.
func (s *IntegrationTestSuite) skipReason(tasks []model.AutoOptimizeTask, recID uuid.UUID) string {
	for _, t := range tasks {
		if t.RecommendationID == nil || *t.RecommendationID != recID {
			continue
		}
		if t.Status != string(model.AutopilotTaskStatusSkipped) {
			return ""
		}
		if t.Reason == nil {
			return "skipped with no reason"
		}
		return *t.Reason
	}
	s.Failf("no task", "no task was generated for recommendation %s at all", recID)
	return ""
}

// TestOptimizerRunsWhenBlockingResolutionIsUnreleasable reproduces dev's
// workflow-server case: the recommendation carries a resolution pointing at a
// pull request a person raised, which closed months ago. The row is Failed, so
// api-server's guard cannot see it and the reconciler — which only ever selects
// InProgress — will never terminalise it. Nothing can release it, so blocking on
// it strands the recommendation permanently.
func (s *IntegrationTestSuite) TestOptimizerRunsWhenBlockingResolutionIsUnreleasable() {
	ctx := context.Background()
	aoID, recID := s.seedOptimizeTarget(ctx, "workflow-server")
	s.seedResolution(ctx, recID, "Failed", "https://github.com/acme/infra/pull/472", "User", nil)

	tasks, err := s.optimizerService.GenerateTasks(ctx, aoID)
	s.Require().NoError(err)

	s.Assert().Empty(s.skipReason(tasks, recID),
		"a pull request closed months ago must not keep the recommendation skipped forever")
}

// TestOptimizerRunsWhenCreationWasAbandoned reproduces dev's ml-k8s-server case:
// a resolution stuck at "creating a pull request" with no URL, which the
// lifecycle already marked unresolvable. Blocking on a row the system has given
// up on is a permanent skip.
func (s *IntegrationTestSuite) TestOptimizerRunsWhenCreationWasAbandoned() {
	ctx := context.Background()
	aoID, recID := s.seedOptimizeTarget(ctx, "ml-k8s-server")
	unresolvable := "unresolvable"
	s.seedResolution(ctx, recID, "InProgress", "", "User", &unresolvable)

	tasks, err := s.optimizerService.GenerateTasks(ctx, aoID)
	s.Require().NoError(err)

	s.Assert().Empty(s.skipReason(tasks, recID),
		"a creation the system already gave up on must not keep the recommendation skipped forever")
}

// TestOptimizerSkipsWhenForeignPRIsGenuinelyOpen is the other half, and the one
// that must not regress: a pull request a person raised that is genuinely still
// open. We do not rewrite a human's pull request, and raising a second one for
// the same recommendation is the duplicate #33523 exists to prevent — so this
// recommendation must still be skipped.
func (s *IntegrationTestSuite) TestOptimizerSkipsWhenForeignPRIsGenuinelyOpen() {
	ctx := context.Background()
	aoID, recID := s.seedOptimizeTarget(ctx, "billing-api")
	created := "created"
	s.seedResolution(ctx, recID, "InProgress", "https://github.com/acme/infra/pull/901", "User", &created)

	tasks, err := s.optimizerService.GenerateTasks(ctx, aoID)
	s.Require().NoError(err)

	s.Assert().Contains(s.skipReason(tasks, recID), "already has active resolutions",
		"an open pull request raised by a person must still block the recommendation")
}

// TestOptimizerRunsWhenItsOwnPRIsOpen is #34959's premise: the run must reach the
// api-server guard so that guard can rewrite the auto optimize's own open pull
// request in place when the numbers have moved. If the optimizer skips here, the
// value refresh is unreachable no matter how correct it is.
func (s *IntegrationTestSuite) TestOptimizerRunsWhenItsOwnPRIsOpen() {
	ctx := context.Background()
	aoID, recID := s.seedOptimizeTarget(ctx, "notifications")
	created := "created"
	s.seedResolution(ctx, recID, "InProgress", "https://github.com/acme/infra/pull/802", "AutoOptimize", &created)

	tasks, err := s.optimizerService.GenerateTasks(ctx, aoID)
	s.Require().NoError(err)

	s.Assert().Empty(s.skipReason(tasks, recID),
		"the run must proceed so the api-server guard can refresh its own open pull request (#34959)")
}

// TestOptimizerRunsRecommendationInProgressBehindItsOwnPR is the end-to-end half
// of #34959's last gap. Raising the pull request flips the recommendation to
// InProgress; on `status = 'Open'` alone the workload then produces no task at
// all on any later run, so the api-server's refresh guard is never reached and
// the pull request goes stale exactly as the ticket describes.
//
// Note what "no task at all" means here: not a Skipped task with a reason, which
// is what a below-threshold change produces — nothing, silently. That is why
// this asserts a task exists before asserting anything about it.
func (s *IntegrationTestSuite) TestOptimizerRunsRecommendationInProgressBehindItsOwnPR() {
	ctx := context.Background()
	aoID, recID := s.seedOptimizeTarget(ctx, "own-pr-open")

	created := "created"
	s.seedResolution(ctx, recID, "InProgress", "https://github.com/acme/infra/pull/950", "AutoOptimize", &created)
	_, err := s.testWorkflowDao.Db().ExecContext(ctx,
		`UPDATE recommendation SET status = 'InProgress' WHERE id = $1`, recID)
	s.Require().NoError(err)

	tasks, err := s.optimizerService.GenerateTasks(ctx, aoID)
	s.Require().NoError(err)

	var found bool
	for _, t := range tasks {
		if t.RecommendationID != nil && *t.RecommendationID == recID {
			found = true
		}
	}
	s.Assert().True(found,
		"a recommendation held InProgress only by the pull request this auto optimize raised "+
			"must still be recomputed, or the refresh guard is unreachable and the PR goes stale (#34959)")
}

// TestOptimizerSkipsRecommendationInProgressBehindSomeoneElse is the negative
// that keeps the widening narrow: InProgress behind work that is genuinely still
// running must stay excluded, or the run duplicates it.
func (s *IntegrationTestSuite) TestOptimizerSkipsRecommendationInProgressBehindSomeoneElse() {
	ctx := context.Background()
	aoID, recID := s.seedOptimizeTarget(ctx, "human-pr-open")

	created := "created"
	s.seedResolution(ctx, recID, "InProgress", "https://github.com/acme/infra/pull/901", "User", &created)
	_, err := s.testWorkflowDao.Db().ExecContext(ctx,
		`UPDATE recommendation SET status = 'InProgress' WHERE id = $1`, recID)
	s.Require().NoError(err)

	tasks, err := s.optimizerService.GenerateTasks(ctx, aoID)
	s.Require().NoError(err)

	for _, t := range tasks {
		if t.RecommendationID != nil && *t.RecommendationID == recID {
			s.Failf("unexpected task",
				"a recommendation InProgress behind a pull request someone else raised must not be "+
					"recomputed — we never rewrite a human's PR, so this is pointless churn")
		}
	}
}
