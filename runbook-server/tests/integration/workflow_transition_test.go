package integration_test

import (
	"context"
	"fmt"
	"net/http"
	"nudgebee/runbook/internal/model"
	"time"
)

func (s *IntegrationTestSuite) TestWorkflowStatusTransitions() {
	s.T().Log("Running TestWorkflowStatusTransitions...")

	// 1. Create a scheduled workflow. New workflows default to ACTIVE (DRAFT
	// status has been removed), so the schedule is registered immediately.
	workflow := s.loadWorkflowFromFile("testdata/test-scheduled-workflow.yaml")
	workflow.Name = "transition-test-workflow"

	createdWorkflow, _, err := s.createWorkflow(workflow)
	s.Require().NoError(err, "Failed to create workflow")

	// Fetch to verify status
	fetchedWorkflow := s.getWorkflow(createdWorkflow.ID)
	createdWorkflow.Status = fetchedWorkflow.Status
	s.Assert().Equal(model.WorkflowStatusActive, createdWorkflow.Status, "New workflow should default to ACTIVE status")

	// Verify Schedule EXISTS immediately for the freshly-created ACTIVE workflow
	scheduleID := "workflow-schedule-" + createdWorkflow.ID
	s.Require().Eventually(func() bool {
		_, err = s.temporalClient.ScheduleClient().GetHandle(context.Background(), scheduleID).Describe(context.Background())
		return err == nil
	}, 5*time.Second, 500*time.Millisecond, "Schedule should exist for newly-created ACTIVE workflow")

	// --- Transition: ACTIVE -> PAUSED ---
	s.T().Log("Transition: ACTIVE -> PAUSED")
	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/workflows/%s/pause", apiBaseURL, createdWorkflow.ID), nil)
	s.Require().NoError(err)
	s.addRequestHeaders(req)
	resp, err := http.DefaultClient.Do(req)
	s.Require().NoError(err)
	defer func() {
		if err := resp.Body.Close(); err != nil {
			s.T().Logf("Error closing response body: %v", err)
		}
	}()
	s.Assert().Equal(http.StatusOK, resp.StatusCode)

	// Verify status is PAUSED
	wf := s.getWorkflow(createdWorkflow.ID)
	s.Assert().Equal(model.WorkflowStatusPaused, wf.Status)

	// Verify Schedule is PAUSED
	desc, err := s.temporalClient.ScheduleClient().GetHandle(context.Background(), scheduleID).Describe(context.Background())
	s.Require().NoError(err)
	s.Assert().True(desc.Schedule.State.Paused, "Schedule should be paused")

	// --- Transition: PAUSED -> ACTIVE (Resume) ---
	s.T().Log("Transition: PAUSED -> ACTIVE")
	req, err = http.NewRequest(http.MethodPost, fmt.Sprintf("%s/workflows/%s/resume", apiBaseURL, createdWorkflow.ID), nil)
	s.Require().NoError(err)
	s.addRequestHeaders(req)
	resp, err = http.DefaultClient.Do(req)
	s.Require().NoError(err)
	defer func() {
		if err := resp.Body.Close(); err != nil {
			s.T().Logf("Error closing response body: %v", err)
		}
	}()
	s.Assert().Equal(http.StatusOK, resp.StatusCode)

	// Verify status is ACTIVE
	wf = s.getWorkflow(createdWorkflow.ID)
	s.Assert().Equal(model.WorkflowStatusActive, wf.Status)

	// Verify Schedule is UNPAUSED
	desc, err = s.temporalClient.ScheduleClient().GetHandle(context.Background(), scheduleID).Describe(context.Background())
	s.Require().NoError(err)
	s.Assert().False(desc.Schedule.State.Paused, "Schedule should be unpaused")

	// --- Transition: ACTIVE -> INACTIVE (Disable) ---
	s.T().Log("Transition: ACTIVE -> INACTIVE")
	wf.Status = model.WorkflowStatusInactive
	wf, err = s.updateWorkflow(wf)
	s.Require().NoError(err)
	s.Assert().Equal(model.WorkflowStatusInactive, wf.Status)

	// Verify Schedule DELETED
	s.Require().Eventually(func() bool {
		_, derr := s.temporalClient.ScheduleClient().GetHandle(context.Background(), scheduleID).Describe(context.Background())
		return derr != nil
	}, 5*time.Second, 500*time.Millisecond, "Schedule should be deleted when moving to INACTIVE")

	// --- Transition: INACTIVE -> ACTIVE (Enable) ---
	s.T().Log("Transition: INACTIVE -> ACTIVE")
	wf.Status = model.WorkflowStatusActive
	wf, err = s.updateWorkflow(wf)
	s.Require().NoError(err)
	s.Assert().Equal(model.WorkflowStatusActive, wf.Status)

	// Verify Schedule Re-created
	s.Require().Eventually(func() bool {
		_, err = s.temporalClient.ScheduleClient().GetHandle(context.Background(), scheduleID).Describe(context.Background())
		return err == nil
	}, 5*time.Second, 500*time.Millisecond, "Schedule should exist for ACTIVE workflow")

	// Cleanup
	s.deleteWorkflow(createdWorkflow.ID, false)
}
