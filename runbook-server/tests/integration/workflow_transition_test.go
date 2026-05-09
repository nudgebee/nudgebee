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

	// 1. Create a scheduled workflow (Starts in DRAFT)
	workflow := s.loadWorkflowFromFile("testdata/test-scheduled-workflow.yaml")
	workflow.Name = "transition-test-workflow"

	// Create only (default status DRAFT)
	createdWorkflow, _, err := s.createWorkflow(workflow)
	s.Require().NoError(err, "Failed to create workflow")

	// Fetch to verify status
	fetchedWorkflow := s.getWorkflow(createdWorkflow.ID)
	createdWorkflow.Status = fetchedWorkflow.Status
	s.Assert().Equal(model.WorkflowStatusDraft, createdWorkflow.Status, "New workflow should be in DRAFT status")

	// Verify Schedule does NOT exist
	scheduleID := "workflow-schedule-" + createdWorkflow.ID
	_, err = s.temporalClient.ScheduleClient().GetHandle(context.Background(), scheduleID).Describe(context.Background())
	s.Assert().Error(err, "Schedule should not exist for DRAFT workflow")

	// --- Transition: DRAFT -> ACTIVE (Publish) ---
	s.T().Log("Transition: DRAFT -> ACTIVE")
	createdWorkflow.Status = model.WorkflowStatusActive
	createdWorkflow, err = s.updateWorkflow(createdWorkflow)
	s.Require().NoError(err)
	s.Assert().Equal(model.WorkflowStatusActive, createdWorkflow.Status)

	// Verify Schedule EXISTS
	s.Require().Eventually(func() bool {
		_, err = s.temporalClient.ScheduleClient().GetHandle(context.Background(), scheduleID).Describe(context.Background())
		return err == nil
	}, 5*time.Second, 500*time.Millisecond, "Schedule should exist for ACTIVE workflow")

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

	// --- Transition: ACTIVE -> DRAFT (Unpublish) ---
	s.T().Log("Transition: ACTIVE -> DRAFT")
	wf.Status = model.WorkflowStatusDraft
	wf, err = s.updateWorkflow(wf)
	s.Require().NoError(err)
	s.Assert().Equal(model.WorkflowStatusDraft, wf.Status)

	// Verify Schedule DELETED
	_, err = s.temporalClient.ScheduleClient().GetHandle(context.Background(), scheduleID).Describe(context.Background())
	s.Assert().Error(err, "Schedule should be deleted when moving to DRAFT")

	// --- Transition: DRAFT -> PAUSED (Should Fail) ---
	s.T().Log("Transition: DRAFT -> PAUSED (Should Fail)")
	req, err = http.NewRequest(http.MethodPost, fmt.Sprintf("%s/workflows/%s/pause", apiBaseURL, createdWorkflow.ID), nil)
	s.Require().NoError(err)
	s.addRequestHeaders(req)
	resp, err = http.DefaultClient.Do(req)
	s.Require().NoError(err)
	defer func() {
		if err := resp.Body.Close(); err != nil {
			s.T().Logf("Error closing response body: %v", err)
		}
	}()
	s.Assert().Equal(http.StatusInternalServerError, resp.StatusCode)

	// Verify status remains DRAFT
	wf = s.getWorkflow(createdWorkflow.ID)
	s.Assert().Equal(model.WorkflowStatusDraft, wf.Status)

	// --- Transition: DRAFT -> INACTIVE (Disable) ---
	s.T().Log("Transition: DRAFT -> INACTIVE")
	// Using UpdateWorkflowStatus endpoint directly if exposed, or UpdateWorkflow
	// Assuming UpdateWorkflow handles status changes.
	wf.Status = model.WorkflowStatusInactive
	wf, err = s.updateWorkflow(wf)
	s.Require().NoError(err)
	s.Assert().Equal(model.WorkflowStatusInactive, wf.Status)

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
