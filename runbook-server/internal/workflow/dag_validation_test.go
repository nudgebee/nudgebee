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

	s.Run("should detect missing depends_on for task output references", func() {
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
		s.Error(err)
		s.Contains(err.Error(), "references Tasks['fetch-data']")
		s.Contains(err.Error(), "depends_on")
	})

	s.Run("should pass when depends_on matches task output references", func() {
		tasks := []model.Task{
			{ID: "fetch-data", Type: "scripting.run_script", Params: map[string]any{
				"script":   "echo hello",
				"language": "bash",
			}},
			{ID: "process-data", Type: "scripting.run_script", DependsOn: []string{"fetch-data"}, Params: map[string]any{
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

	s.Run("should detect missing depends_on in if condition", func() {
		tasks := []model.Task{
			{ID: "check", Type: "scripting.run_script", Params: map[string]any{
				"script": "echo true",
			}},
			{ID: "conditional", Type: "scripting.run_script",
				If: "{{ Tasks['check'].output.data == 'true' }}",
				Params: map[string]any{
					"script": "echo done",
				},
			},
		}
		err := ValidateDAG(tasks)
		s.Error(err)
		s.Contains(err.Error(), "references Tasks['check']")
	})

	s.Run("should detect missing depends_on inside foreach subtasks", func() {
		tasks := []model.Task{
			{ID: "init", Type: "scripting.run_script", Params: map[string]any{
				"script": "echo init",
			}},
			{ID: "loop", Type: "core.foreach", DependsOn: []string{"init"}, Params: map[string]any{
				"items": "{{ Tasks['init'].output.data }}",
				"tasks": []any{
					map[string]any{
						"id":   "step-a",
						"type": "scripting.run_script",
						"params": map[string]any{
							"script": "echo a",
						},
					},
					map[string]any{
						"id":   "step-b",
						"type": "scripting.run_script",
						"if":   "{{ Tasks['step-a'].output.data == 'a' }}",
						"params": map[string]any{
							"script": "echo b",
						},
					},
				},
			}},
		}
		err := ValidateDAG(tasks)
		s.Error(err)
		s.Contains(err.Error(), "in foreach task loop")
		s.Contains(err.Error(), "references Tasks['step-a']")
	})

	s.Run("should pass when foreach subtasks have correct depends_on", func() {
		tasks := []model.Task{
			{ID: "init", Type: "scripting.run_script", Params: map[string]any{
				"script": "echo init",
			}},
			{ID: "loop", Type: "core.foreach", DependsOn: []string{"init"}, Params: map[string]any{
				"items": "{{ Tasks['init'].output.data }}",
				"tasks": []any{
					map[string]any{
						"id":   "step-a",
						"type": "scripting.run_script",
						"params": map[string]any{
							"script": "echo a",
						},
					},
					map[string]any{
						"id":         "step-b",
						"type":       "scripting.run_script",
						"depends_on": []any{"step-a"},
						"if":         "{{ Tasks['step-a'].output.data == 'a' }}",
						"params": map[string]any{
							"script": "echo b",
						},
					},
				},
			}},
		}
		err := ValidateDAG(tasks)
		s.NoError(err)
	})

	s.Run("should ignore references to tasks outside current scope", func() {
		// Inside a foreach loop, params may reference {{ item.field }} or tasks from parent scope.
		// The validation only checks references to tasks within the same task list.
		tasks := []model.Task{
			{ID: "outer-task", Type: "scripting.run_script", Params: map[string]any{
				"script": "echo hello",
			}},
			{ID: "loop", Type: "core.foreach", DependsOn: []string{"outer-task"}, Params: map[string]any{
				"items": "{{ Tasks['outer-task'].output.data }}",
				"tasks": []any{
					map[string]any{
						"id":   "inner-step",
						"type": "scripting.run_script",
						"params": map[string]any{
							// References outer-task which is outside the loop's task list — should be ignored
							"env": map[string]any{
								"DATA": "{{ Tasks['outer-task'].output.data }}",
							},
							"script": "echo inner",
						},
					},
				},
			}},
		}
		err := ValidateDAG(tasks)
		s.NoError(err)
	})

	s.Run("should detect multiple missing dependencies", func() {
		tasks := []model.Task{
			{ID: "a", Type: "scripting.run_script", Params: map[string]any{"script": "echo a"}},
			{ID: "b", Type: "scripting.run_script", Params: map[string]any{"script": "echo b"}},
			{ID: "c", Type: "scripting.run_script", Params: map[string]any{
				"script": "echo c",
				"env": map[string]any{
					"A": "{{ Tasks['a'].output.data }}",
					"B": "{{ Tasks['b'].output.data }}",
				},
			}},
		}
		err := ValidateDAG(tasks)
		s.Error(err)
		// Should catch at least one of the missing deps
		s.Contains(err.Error(), "depends_on")
	})

	s.Run("should pass when referenced task is a transitive ancestor", func() {
		// Chain: task1 -> task2 -> task3. task3 references task1.
		// Even though task1 is not in task3.depends_on directly, topological
		// execution guarantees task1 has completed before task3 runs.
		tasks := []model.Task{
			{ID: "task1", Type: "scripting.run_script", Params: map[string]any{"script": "echo 1"}},
			{ID: "task2", Type: "scripting.run_script", DependsOn: []string{"task1"}, Params: map[string]any{
				"script": "echo 2",
			}},
			{ID: "task3", Type: "scripting.run_script", DependsOn: []string{"task2"}, Params: map[string]any{
				"script": "echo 3",
				"env": map[string]any{
					"FROM_TASK1": "{{ Tasks['task1'].output.data }}",
				},
			}},
		}
		err := ValidateDAG(tasks)
		s.NoError(err)
	})

	s.Run("should still error when sibling references sibling without edge", func() {
		// sib1 and sib2 both depend on root; sib2 references sib1 with no edge between them.
		// sib1 is not a transitive ancestor of sib2 — they can race in parallel execution.
		tasks := []model.Task{
			{ID: "root", Type: "scripting.run_script", Params: map[string]any{"script": "echo root"}},
			{ID: "sib1", Type: "scripting.run_script", DependsOn: []string{"root"}, Params: map[string]any{
				"script": "echo sib1",
			}},
			{ID: "sib2", Type: "scripting.run_script", DependsOn: []string{"root"}, Params: map[string]any{
				"script": "echo sib2",
				"env": map[string]any{
					"FROM_SIB1": "{{ Tasks['sib1'].output.data }}",
				},
			}},
		}
		err := ValidateDAG(tasks)
		s.Error(err)
		s.Contains(err.Error(), "references Tasks['sib1']")
	})

	s.Run("should detect missing depends_on in set_vars", func() {
		tasks := []model.Task{
			{ID: "source", Type: "scripting.run_script", Params: map[string]any{"script": "echo data"}},
			{ID: "consumer", Type: "scripting.run_script",
				SetVars: map[string]any{
					"result": map[string]any{
						"value": "{{ Tasks['source'].output.data }}",
					},
				},
				Params: map[string]any{"script": "echo done"},
			},
		}
		err := ValidateDAG(tasks)
		s.Error(err)
		s.Contains(err.Error(), "references Tasks['source']")
	})
}
