package workflow

import (
	"nudgebee/runbook/internal/model"
	"testing"

	"github.com/stretchr/testify/suite"
)

type DAGValidationTestSuite struct {
	suite.Suite
}

func (s *DAGValidationTestSuite) SetupSuite() {
}

func TestDAGValidationSuite(t *testing.T) {
	suite.Run(t, new(DAGValidationTestSuite))
}

func (s *DAGValidationTestSuite) TestValidateDAG() {
	s.Run("should return no error for a valid DAG", func() {
		tasks := []model.Task{
			{ID: "A", Type: "http_request"},
			{ID: "B", Type: "http_request", DependsOn: []string{"A"}},
			{ID: "C", Type: "http_request", DependsOn: []string{"A"}},
			{ID: "D", Type: "http_request", DependsOn: []string{"B", "C"}},
		}
		err := ValidateDAG(tasks)
		s.NoError(err)
	})

	s.Run("should detect a simple direct cycle", func() {
		tasks := []model.Task{
			{ID: "A", Type: "http_request", DependsOn: []string{"B"}},
			{ID: "B", Type: "http_request", DependsOn: []string{"A"}},
		}
		err := ValidateDAG(tasks)
		s.Error(err)
	})

	s.Run("should detect a longer indirect cycle", func() {
		tasks := []model.Task{
			{ID: "A", Type: "http_request", DependsOn: []string{"C"}},
			{ID: "B", Type: "http_request", DependsOn: []string{"A"}},
			{ID: "C", Type: "http_request", DependsOn: []string{"B"}},
		}
		err := ValidateDAG(tasks)
		s.Error(err)
	})

	s.Run("should handle multiple disconnected components", func() {
		tasks := []model.Task{
			{ID: "A", Type: "http_request"},
			{ID: "B", Type: "http_request"},
			{ID: "C", Type: "http_request", DependsOn: []string{"D"}},
			{ID: "D", Type: "http_request", DependsOn: []string{"C"}}, // Cycle in second component
		}
		err := ValidateDAG(tasks)
		s.Error(err)
	})

	s.Run("should detect cycle in nested tasks (group)", func() {
		tasks := []model.Task{
			{
				ID:   "group1",
				Type: "core.group",
				Tasks: []model.Task{
					{ID: "A", Type: "http_request", DependsOn: []string{"B"}},
					{ID: "B", Type: "http_request", DependsOn: []string{"A"}},
				},
			},
		}
		err := ValidateDAG(tasks)
		s.Error(err)
		s.Contains(err.Error(), "in group task group1")
	})

	s.Run("should validate valid nested tasks", func() {
		tasks := []model.Task{
			{
				ID:   "group1",
				Type: "core.group",
				Tasks: []model.Task{
					{ID: "A", Type: "http_request"},
					{ID: "B", Type: "http_request", DependsOn: []string{"A"}},
				},
			},
		}
		err := ValidateDAG(tasks)
		s.NoError(err)
	})

	s.Run("should accept Tasks[X] template ref without depends_on", func() {
		// The DAG validator no longer checks template-ref completeness.
		// Templates that reference a task whose output is not populated at
		// runtime fail clearly during execution; saving them is allowed.
		tasks := []model.Task{
			{ID: "fetch-data", Type: "scripting.run_script", Params: map[string]any{
				"script":   "echo hello",
				"language": "bash",
			}},
			{ID: "process-data", Type: "scripting.run_script", Params: map[string]any{
				"script":   "echo processing",
				"language": "python",
				"env": map[string]any{
					"INPUT": "{{ Tasks['fetch-data'].output.data | to_json }}",
				},
			}},
		}
		err := ValidateDAG(tasks)
		s.NoError(err)
	})

	s.Run("should detect non-existent depends_on target", func() {
		tasks := []model.Task{
			{ID: "A", Type: "http_request"},
			{ID: "B", Type: "http_request", DependsOn: []string{"missing"}},
		}
		err := ValidateDAG(tasks)
		s.Error(err)
		s.Contains(err.Error(), "depends on non-existent task")
	})
}
