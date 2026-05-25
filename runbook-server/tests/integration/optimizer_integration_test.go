package integration_test

import (
	"context"
	"encoding/json"

	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/services/optimizer"
	"nudgebee/runbook/services/security"

	"github.com/google/uuid"
)

func (s *IntegrationTestSuite) TestOptimizerFlow() {
	ctx := context.Background()
	accountID := uuid.New()
	tenantID := uuid.New()
	userID := uuid.New()

	// 1. Setup Data
	// Create Agent
	_, err := s.testWorkflowDao.Db().ExecContext(ctx, `
		INSERT INTO agent (id, tenant, cloud_account_id, status, type)
		VALUES ($1, $2, $3, 'Connected', 'k8s')
	`, uuid.New(), tenantID, accountID)
	s.Require().NoError(err)

	// Create AutoOptimize Rule (Vertical Rightsize)
	rule := map[string]interface{}{
		"cpu": map[string]interface{}{
			"change_pct": 10,
		},
		"memory": map[string]interface{}{
			"change_pct": 10,
		},
		"scale_up": true,
	}

	aoID := uuid.New()
	ao := model.AutoOptimize{
		ID:           aoID,
		AccountID:    accountID,
		TenantID:     tenantID,
		Category:     "vertical_rightsize",
		Status:       model.AutoOptimizeStatusActive,
		Rule:         rule,
		ScheduleTime: "* * * * *",
		CreatedBy:    userID,
		Name:         ptr("Test Optimizer"),
	}
	err = s.testOptimizerDao.SaveAutoOptimize(ctx, ao)
	s.Require().NoError(err)

	// Create Resource
	resID := uuid.New()
	resIdent := "default/Deployment/nginx"
	_, err = s.testWorkflowDao.Db().ExecContext(ctx, `
		INSERT INTO cloud_resourses (id, account, tenant, resourse_id, name, type, status, region, cloud_provider)
		VALUES ($1, $2, $3, $4, 'nginx', 'Deployment', 'Active', 'us-east-1', 'k8s')
	`, resID, accountID, tenantID, resIdent)
	s.Require().NoError(err)

	// Create Recommendation
	recID := uuid.New()
	recData := map[string]interface{}{
		"cpu": map[string]interface{}{
			"recommended": "100m",
		},
	}
	recBytes, _ := json.Marshal(recData)
	_, err = s.testWorkflowDao.Db().ExecContext(ctx, `
		INSERT INTO recommendation (id, tenant_id, cloud_account_id, resource_id, recommendation, status, category, recommendation_action)
		VALUES ($1, $2, $3, $4, $5, 'Open', 'vertical_rightsize', 'apply')
	`, recID, tenantID, accountID, resID, recBytes)
	s.Require().NoError(err)

	// 2. Run GenerateTasks
	tasks, err := s.optimizerService.GenerateTasks(ctx, aoID)
	s.Require().NoError(err)

	// 3. Verify
	s.Assert().Len(tasks, 1)
	if len(tasks) > 0 {
		task := tasks[0]
		s.Assert().Equal(aoID, task.AutoPilotID)
		s.Assert().Equal("Vertical Rightsize Deployment default/nginx", task.Name)

		// Verify Meta (Payload)
		meta := task.Meta
		s.Assert().Equal("default", meta["namespace"])
		s.Assert().Equal("nginx", meta["name"])
		s.Assert().Equal("Deployment", meta["kind"])
		s.Assert().Equal(accountID.String(), meta["account_id"])

		updatedAO, _ := s.testOptimizerDao.GetAutoOptimize(ctx, aoID)
		s.Assert().Equal(string(model.AutopilotExecutionStatusInProgress), updatedAO.ExecutionStatus)
		s.Assert().NotNil(updatedAO.LastExecutedTime)
	}
}

func (s *IntegrationTestSuite) TestVerticalRightsizeSafety() {
	ctx := context.Background()
	accountID := uuid.New()
	tenantID := uuid.New()
	userID := uuid.New()

	// 1. Setup Data
	_, _ = s.testWorkflowDao.Db().ExecContext(ctx, `INSERT INTO agent (id, tenant, cloud_account_id, status, type) VALUES ($1, $2, $3, 'Connected', 'k8s')`, uuid.New(), tenantID, accountID)

	// Rule with Max Change % = 50
	rule := map[string]interface{}{
		"cpu": map[string]interface{}{
			"trigger": map[string]interface{}{
				"change_pct":     5,
				"max_change_pct": 50,
			},
		},
	}

	aoID := uuid.New()
	ao := model.AutoOptimize{
		ID:           aoID,
		AccountID:    accountID,
		TenantID:     tenantID,
		Category:     "vertical_rightsize",
		Status:       model.AutoOptimizeStatusActive,
		Rule:         rule,
		ScheduleTime: "* * * * *",
		CreatedBy:    userID,
		Name:         ptr("Safety Test"),
	}
	_ = s.testOptimizerDao.SaveAutoOptimize(ctx, ao)

	// Create Resource with 100m CPU
	resID := uuid.New()
	resIdent := "default/Deployment/nginx-safe"
	_, _ = s.testWorkflowDao.Db().ExecContext(ctx, `
		INSERT INTO cloud_resourses (id, account, tenant, resourse_id, name, type, status, region, cloud_provider, meta)
		VALUES ($1, $2, $3, $4, 'nginx-safe', 'Deployment', 'Active', 'us-east-1', 'k8s', '{"cpu": "100m"}')
	`, resID, accountID, tenantID, resIdent)

	// Create "Dangerous" Recommendation: 500m (+400% change)
	recData := map[string]interface{}{
		"nginx-container": []interface{}{
			map[string]interface{}{
				"resource": "cpu",
				"recommended": map[string]interface{}{
					"request": "500m",
				},
			},
		},
	}
	recBytes, _ := json.Marshal(recData)
	_, _ = s.testWorkflowDao.Db().ExecContext(ctx, `
		INSERT INTO recommendation (id, tenant_id, cloud_account_id, resource_id, recommendation, status, category, recommendation_action)
		VALUES ($1, $2, $3, $4, $5, 'Open', 'vertical_rightsize', 'apply')
	`, uuid.New(), tenantID, accountID, resID, recBytes)

	// 2. Generate Tasks
	tasks, err := s.optimizerService.GenerateTasks(ctx, aoID)
	s.Require().NoError(err)
	s.Assert().Len(tasks, 1)

	// 3. Execute Task Activity (Simulated)
	optimizerActivities := optimizer.NewActivities(s.optimizerService, s.testOptimizerDao)
	err = optimizerActivities.ExecuteTaskActivity(ctx, tasks[0].ID.String())
	s.Assert().NoError(err)

	// 4. Verify status in DB
	task, err := s.testOptimizerDao.GetAutoOptimizeTask(ctx, tasks[0].ID)
	s.Require().NoError(err)
	s.Assert().Equal(string(model.AutopilotTaskStatusSkipped), task.Status)
	s.Assert().Contains(*task.Reason, "CPU change rejected")
}

func (s *IntegrationTestSuite) TestConflictResolution() {
	ctx := context.Background()
	accountID := uuid.New()
	tenantID := uuid.New()
	sc := security.NewRequestContextForTenantAdmin(tenantID.String())

	// 1. Setup Active Vertical AO
	aoID := uuid.New()
	ao := model.AutoOptimize{
		ID:        aoID,
		AccountID: accountID,
		TenantID:  tenantID,
		Category:  "vertical_rightsize",
		Status:    model.AutoOptimizeStatusActive,
		Name:      ptr("Existing Vertical"),
	}
	_ = s.testOptimizerDao.SaveAutoOptimize(ctx, ao)

	// Add resource map for namespace 'default'
	_ = s.testOptimizerDao.SaveResourceFilters(ctx, []model.AutoOptimizeResourceMap{{
		ID:                 uuid.New(),
		AutoOptimizeID:     aoID,
		AccountID:          accountID,
		TenantID:           tenantID,
		AutoOptimizeType:   "vertical_rightsize",
		ResourceIdentifier: model.AutoOptimizeResourceFilter{Namespace: ptr("default")},
	}})

	// 2. Attempt to create conflicting Continuous AO
	req := model.AutoOptimizeRequestModel{
		Category:       "continuous_rightsize",
		Name:           "New Continuous",
		ResourceFilter: []model.AutoOptimizeResourceFilter{{Namespace: ptr("default")}},
		Schedule:       model.ScheduleConfig{Frequency: "* * * * *"},
	}

	_, err := s.optimizerService.CreateAutoOptimize(ctx, sc, accountID, req)
	s.Assert().Error(err)
	s.Assert().Contains(err.Error(), "conflicts with new category")
}

func ptr(s string) *string {
	return &s
}
