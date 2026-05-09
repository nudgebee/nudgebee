package integration_test

import (
	"context"
	"nudgebee/runbook/config"
	"nudgebee/runbook/internal/model"
	"testing"

	"github.com/stretchr/testify/suite"
)

type WorkflowCompressionTestSuite struct {
	IntegrationTestSuite
}

func (s *WorkflowCompressionTestSuite) TestLargePayloadCompression() {
	workflowDef := s.loadWorkflowFromFile("testdata/test-compression-workflow.yaml")
	wf, _, err := s.createAndActivateWorkflow(workflowDef)
	s.Require().NoError(err)

	runID, err := s.executeWorkflow(wf.ID, nil)
	s.Require().NoError(err)
	s.Require().NotEmpty(runID)

	details := s.waitForWorkflowCompletion(wf.ID, runID)
	if details.Status == model.WorkflowExecutionStatusFailed {
		s.T().Logf("Workflow failed with message: %s", details.Error)
		// Retrieve full history to debug
		historyIter := s.temporalClient.GetWorkflowHistory(context.Background(), wf.ID, runID, false, 0)
		for historyIter.HasNext() {
			event, err := historyIter.Next()
			if err == nil {
				s.T().Logf("History Event: %s", event.GetEventType())
				if event.GetWorkflowExecutionFailedEventAttributes() != nil {
					s.T().Logf("Failure Attributes: %v", event.GetWorkflowExecutionFailedEventAttributes())
				}
			}
		}
	}
	s.Require().Equal(model.WorkflowExecutionStatusCompleted, details.Status)
}

func TestWorkflowCompressionTestSuite(t *testing.T) {
	// Check if the integration tests are enabled
	if config.Config.RunIntegrationTests != "true" {
		t.Skip("Skipping integration tests. Set RUN_INTEGRATION_TESTS=true to enable.")
	}
	suite.Run(t, new(WorkflowCompressionTestSuite))
}
