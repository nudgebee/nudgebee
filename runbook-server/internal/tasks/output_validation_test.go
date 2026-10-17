package tasks

import (
	"errors"
	"testing"

	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/internal/tasks/types"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
)

func boolPtr(b bool) *bool {
	return &b
}

func TestParseTaskExpectedOutput(t *testing.T) {
	t.Run("valid map parsing", func(t *testing.T) {
		input := map[string]any{
			"type":        "json",
			"allow_empty": false,
			"required":    []any{"status", "count"},
		}
		expected := model.ParseTaskExpectedOutput(input)
		require.NotNil(t, expected)
		assert.Equal(t, "json", expected.Type)
		require.NotNil(t, expected.AllowEmpty)
		assert.False(t, *expected.AllowEmpty)
		assert.Equal(t, []string{"status", "count"}, expected.Required)
	})

	t.Run("nil and empty input", func(t *testing.T) {
		expected := model.ParseTaskExpectedOutput(nil)
		assert.Nil(t, expected)

		expected = model.ParseTaskExpectedOutput(map[string]any{})
		assert.Nil(t, expected)
	})
}

func TestTaskValidationError_RetryClassification(t *testing.T) {
	// Transient errors: ERR_EMPTY_OUTPUT
	emptyErr := &TaskValidationError{
		Code:   ErrCodeEmptyOutput,
		Reason: "empty output",
	}
	assert.True(t, emptyErr.IsRetryable(), "ERR_EMPTY_OUTPUT must be classified as retryable")
	temporalErr := emptyErr.ToTemporalError()
	require.Error(t, temporalErr)
	var appErr *temporal.ApplicationError
	require.True(t, errors.As(temporalErr, &appErr))
	assert.False(t, appErr.NonRetryable(), "ERR_EMPTY_OUTPUT must NOT be marked non-retryable")
	assert.Equal(t, ErrCodeEmptyOutput, appErr.Type())

	// Deterministic errors: ERR_MALFORMED_OUTPUT
	malformedErr := &TaskValidationError{
		Code:   ErrCodeMalformedOutput,
		Reason: "invalid JSON syntax",
	}
	assert.False(t, malformedErr.IsRetryable(), "ERR_MALFORMED_OUTPUT must be non-retryable")
	temporalErr = malformedErr.ToTemporalError()
	require.Error(t, temporalErr)
	require.True(t, errors.As(temporalErr, &appErr))
	assert.True(t, appErr.NonRetryable(), "ERR_MALFORMED_OUTPUT must be marked non-retryable")
	assert.Equal(t, ErrCodeMalformedOutput, appErr.Type())

	// Deterministic errors: ERR_TYPE_MISMATCH
	typeErr := &TaskValidationError{
		Code:   ErrCodeTypeMismatch,
		Reason: "expected object, got int",
	}
	assert.False(t, typeErr.IsRetryable(), "ERR_TYPE_MISMATCH must be non-retryable")
	temporalErr = typeErr.ToTemporalError()
	require.Error(t, temporalErr)
	require.True(t, errors.As(temporalErr, &appErr))
	assert.True(t, appErr.NonRetryable(), "ERR_TYPE_MISMATCH must be marked non-retryable")
	assert.Equal(t, ErrCodeTypeMismatch, appErr.Type())

	// Deterministic errors: ERR_MISSING_REQUIRED_FIELD
	reqErr := &TaskValidationError{
		Code:   ErrCodeMissingRequiredField,
		Reason: "missing required key cluster_id",
	}
	assert.False(t, reqErr.IsRetryable(), "ERR_MISSING_REQUIRED_FIELD must be non-retryable")
	temporalErr = reqErr.ToTemporalError()
	require.Error(t, temporalErr)
	require.True(t, errors.As(temporalErr, &appErr))
	assert.True(t, appErr.NonRetryable(), "ERR_MISSING_REQUIRED_FIELD must be marked non-retryable")
	assert.Equal(t, ErrCodeMissingRequiredField, appErr.Type())
}

type mockAdapterTask struct {
	name   string
	output any
	err    error
}

func (m *mockAdapterTask) GetName() string        { return m.name }
func (m *mockAdapterTask) GetDescription() string { return "" }
func (m *mockAdapterTask) GetDisplayName() string { return m.name }
func (m *mockAdapterTask) Execute(ctx types.TaskContext, params map[string]any) (any, error) {
	return m.output, m.err
}
func (m *mockAdapterTask) InputSchema() *types.Schema  { return nil }
func (m *mockAdapterTask) OutputSchema() *types.Schema { return nil }

func TestTaskWrapper_OriginalFailureModeRegression(t *testing.T) {
	ts := &testsuite.WorkflowTestSuite{}

	// Simulate scripting.run_script where process exits with code 0 and stdout is empty
	mockTask := &mockAdapterTask{
		name:   "scripting.run_script",
		output: map[string]any{"data": "", "exit_code": 0},
	}
	tw := &TaskWrapper{Task: mockTask}

	// OLD BEHAVIOR: Without expected_output, TaskWrapper succeeds despite unusable empty output
	envOld := ts.NewTestActivityEnvironment()
	envOld.RegisterActivity(tw.Execute)
	paramsOld := map[string]any{}
	val, err := envOld.ExecuteActivity(tw.Execute, paramsOld)
	require.NoError(t, err, "OLD BEHAVIOR: activity returned success despite empty stdout")
	var resOld map[string]any
	require.NoError(t, val.Get(&resOld))
	assert.Equal(t, "", resOld["data"])

	// NEW BEHAVIOR: With expected_output requiring non-empty string, TaskWrapper detects contract violation
	envNew := ts.NewTestActivityEnvironment()
	envNew.RegisterActivity(tw.Execute)
	f := false
	paramsNew := map[string]any{
		ParamExpectedOutput: &model.TaskExpectedOutput{
			Type:       "string",
			AllowEmpty: &f,
		},
	}
	_, err = envNew.ExecuteActivity(tw.Execute, paramsNew)
	require.Error(t, err, "NEW BEHAVIOR: activity must return error on empty stdout when allow_empty=false")
	var appErr *temporal.ApplicationError
	require.True(t, errors.As(err, &appErr))
	assert.Equal(t, ErrCodeEmptyOutput, appErr.Type())
	assert.False(t, appErr.NonRetryable(), "ERR_EMPTY_OUTPUT must be retryable for Temporal")

	// DETERMINISTIC CONTRACT FAILURE: Malformed JSON must be non-retryable
	mockMalformed := &mockAdapterTask{
		name:   "scripting.run_script",
		output: map[string]any{"data": `{"unclosed":`},
	}
	twMalformed := &TaskWrapper{Task: mockMalformed}
	envMalformed := ts.NewTestActivityEnvironment()
	envMalformed.RegisterActivity(twMalformed.Execute)
	paramsMalformed := map[string]any{
		ParamExpectedOutput: &model.TaskExpectedOutput{
			Type: "json",
		},
	}
	_, err = envMalformed.ExecuteActivity(twMalformed.Execute, paramsMalformed)
	require.Error(t, err)
	require.True(t, errors.As(err, &appErr))
	assert.Equal(t, ErrCodeMalformedOutput, appErr.Type())
	assert.True(t, appErr.NonRetryable(), "ERR_MALFORMED_OUTPUT must be non-retryable for Temporal")

	// DETERMINISTIC CONTRACT FAILURE: Missing required field must be non-retryable
	mockMissing := &mockAdapterTask{
		name:   "integrations.http",
		output: map[string]any{"body": `{"status":"ok"}`},
	}
	twMissing := &TaskWrapper{Task: mockMissing}
	envMissing := ts.NewTestActivityEnvironment()
	envMissing.RegisterActivity(twMissing.Execute)
	paramsMissing := map[string]any{
		ParamExpectedOutput: &model.TaskExpectedOutput{
			Type:     "json",
			Required: []string{"cluster_id"},
		},
	}
	_, err = envMissing.ExecuteActivity(twMissing.Execute, paramsMissing)
	require.Error(t, err)
	require.True(t, errors.As(err, &appErr))
	assert.Equal(t, ErrCodeMissingRequiredField, appErr.Type())
	assert.True(t, appErr.NonRetryable(), "ERR_MISSING_REQUIRED_FIELD must be non-retryable for Temporal")
}

// TestValidationContractMatrix_18Scenarios exercises the complete 18-case contract matrix
func TestValidationContractMatrix_18Scenarios(t *testing.T) {
	trueVal := true
	falseVal := false

	tests := []struct {
		name        string
		input       any
		expected    *model.TaskExpectedOutput
		taskType    string
		expectErr   bool
		errCode     string
		validateOut func(t *testing.T, out any)
	}{
		{
			name:      "1. nil expected_output preserves input exactly",
			input:     "raw-value",
			expected:  nil,
			taskType:  "core.print",
			expectErr: false,
			validateOut: func(t *testing.T, out any) {
				assert.Equal(t, "raw-value", out)
			},
		},
		{
			name:      "2. empty expected_output defaults to allow_empty=true",
			input:     "",
			expected:  &model.TaskExpectedOutput{},
			taskType:  "scripting.run_script",
			expectErr: false,
			validateOut: func(t *testing.T, out any) {
				assert.Equal(t, "", out)
			},
		},
		{
			name:  "3. allow_empty=true permits empty string",
			input: "",
			expected: &model.TaskExpectedOutput{
				Type:       "string",
				AllowEmpty: &trueVal,
			},
			taskType:  "scripting.run_script",
			expectErr: false,
		},
		{
			name:  "4. allow_empty=false rejects empty string",
			input: "",
			expected: &model.TaskExpectedOutput{
				Type:       "string",
				AllowEmpty: &falseVal,
			},
			taskType:  "scripting.run_script",
			expectErr: true,
			errCode:   ErrCodeEmptyOutput,
		},
		{
			name:  "5. valid string passes",
			input: "cluster healthy",
			expected: &model.TaskExpectedOutput{
				Type: "string",
			},
			taskType:  "scripting.run_script",
			expectErr: false,
		},
		{
			name:  "6. valid JSON parses and normalizes",
			input: `{"status":"ok","nodes":4}`,
			expected: &model.TaskExpectedOutput{
				Type: "json",
			},
			taskType:  "integrations.http",
			expectErr: false,
			validateOut: func(t *testing.T, out any) {
				m, ok := out.(map[string]any)
				require.True(t, ok)
				assert.Equal(t, "ok", m["status"])
			},
		},
		{
			name:  "7. malformed JSON returns non-retryable error",
			input: `{"status": "ok", broken...`,
			expected: &model.TaskExpectedOutput{
				Type: "json",
			},
			taskType:  "integrations.http",
			expectErr: true,
			errCode:   ErrCodeMalformedOutput,
		},
		{
			name:  "8. valid object parses correctly",
			input: `{"cluster_id":"c-123"}`,
			expected: &model.TaskExpectedOutput{
				Type: "object",
			},
			taskType:  "k8s.cli",
			expectErr: false,
			validateOut: func(t *testing.T, out any) {
				m, ok := out.(map[string]any)
				require.True(t, ok)
				assert.Equal(t, "c-123", m["cluster_id"])
			},
		},
		{
			name:  "9. valid array parses correctly",
			input: `["node-1", "node-2"]`,
			expected: &model.TaskExpectedOutput{
				Type: "array",
			},
			taskType:  "k8s.cli",
			expectErr: false,
			validateOut: func(t *testing.T, out any) {
				s, ok := out.([]any)
				require.True(t, ok)
				assert.Len(t, s, 2)
			},
		},
		{
			name:  "10. wrong type (expected array, got object) returns ERR_TYPE_MISMATCH",
			input: map[string]any{"not": "an-array"},
			expected: &model.TaskExpectedOutput{
				Type: "array",
			},
			taskType:  "k8s.cli",
			expectErr: true,
			errCode:   ErrCodeTypeMismatch,
		},
		{
			name:  "11. required field present passes",
			input: map[string]any{"token": "tok_123", "expires": 3600},
			expected: &model.TaskExpectedOutput{
				Type:     "object",
				Required: []string{"token"},
			},
			taskType:  "integrations.http",
			expectErr: false,
		},
		{
			name:  "12. required field missing returns ERR_MISSING_REQUIRED_FIELD",
			input: map[string]any{"expires": 3600},
			expected: &model.TaskExpectedOutput{
				Type:     "object",
				Required: []string{"token"},
			},
			taskType:  "integrations.http",
			expectErr: true,
			errCode:   ErrCodeMissingRequiredField,
		},
		{
			name:  "13. wrapped payload in data key is unpacked and validated",
			input: map[string]any{"data": `{"result":"valid"}`},
			expected: &model.TaskExpectedOutput{
				Type: "object",
			},
			taskType:  "scripting.run_script",
			expectErr: false,
		},
		{
			name:  "14. normalized JSON payload unpacks string JSON into map inside wrapper",
			input: map[string]any{"body": `{"tier":"premium"}`},
			expected: &model.TaskExpectedOutput{
				Type: "json",
			},
			taskType:  "integrations.http",
			expectErr: false,
			validateOut: func(t *testing.T, out any) {
				m, ok := out.(map[string]any)
				require.True(t, ok)
				inner, ok := m["body"].(map[string]any)
				require.True(t, ok)
				assert.Equal(t, "premium", inner["tier"])
			},
		},
		{
			name:  "15. nil payload with allow_empty=false returns ERR_EMPTY_OUTPUT",
			input: nil,
			expected: &model.TaskExpectedOutput{
				Type:       "string",
				AllowEmpty: &falseVal,
			},
			taskType:  "scripting.run_script",
			expectErr: true,
			errCode:   ErrCodeEmptyOutput,
		},
		{
			name:  "16. whitespace-only string with allow_empty=false returns ERR_EMPTY_OUTPUT",
			input: "   \t\n  ",
			expected: &model.TaskExpectedOutput{
				Type:       "string",
				AllowEmpty: &falseVal,
			},
			taskType:  "scripting.run_script",
			expectErr: true,
			errCode:   ErrCodeEmptyOutput,
		},
		{
			name:  "17. legitimate empty business result (0-row SQL query) passes with allow_empty=true",
			input: map[string]any{"rows": []any{}, "rowCount": 0},
			expected: &model.TaskExpectedOutput{
				Type:       "object",
				AllowEmpty: &trueVal,
			},
			taskType:  "dbms.rdbms",
			expectErr: false,
			validateOut: func(t *testing.T, out any) {
				m, ok := out.(map[string]any)
				require.True(t, ok)
				assert.Equal(t, 0, m["rowCount"])
			},
		},
		{
			name:      "18. existing task behavior without new contract is completely untouched",
			input:     map[string]any{"exit_code": 0, "stdout": ""},
			expected:  nil,
			taskType:  "scripting.run_script",
			expectErr: false,
			validateOut: func(t *testing.T, out any) {
				m, ok := out.(map[string]any)
				require.True(t, ok)
				assert.Equal(t, "", m["stdout"])
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, valErr := ValidateTaskOutput(tt.input, tt.expected, tt.taskType)
			if tt.expectErr {
				require.NotNil(t, valErr)
				if tt.errCode != "" {
					assert.Equal(t, tt.errCode, valErr.Code)
				}
			} else {
				require.Nil(t, valErr)
				if tt.validateOut != nil {
					tt.validateOut(t, out)
				}
			}
		})
	}
}

type TestItem struct {
	Name string `json:"name"`
}

type TestResult struct {
	Items []TestItem `json:"items"`
	ID    string     `json:"id"`
}

type TestCustomMap map[string]string
type TestCustomSlice []string

func TestValidateTaskOutput_CustomTypesAndRequiredFields(t *testing.T) {
	// A. map[string]any
	t.Run("map[string]any with required fields", func(t *testing.T) {
		input := map[string]any{
			"id":    "item-123",
			"items": []any{"a", "b"},
		}
		expected := &model.TaskExpectedOutput{
			Type:     "object",
			Required: []string{"id", "items"},
		}
		out, valErr := ValidateTaskOutput(input, expected, "test.task")
		require.Nil(t, valErr)
		m, ok := out.(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "item-123", m["id"])
	})

	// B. map[string]string
	t.Run("map[string]string normalized to map[string]any with required fields", func(t *testing.T) {
		input := map[string]string{
			"id":   "str-456",
			"name": "cluster-prod",
		}
		expected := &model.TaskExpectedOutput{
			Type:     "object",
			Required: []string{"id", "name"},
		}
		out, valErr := ValidateTaskOutput(input, expected, "test.task")
		require.Nil(t, valErr)
		m, ok := out.(map[string]any)
		require.True(t, ok, "downstream output must be normalized to map[string]any")
		assert.Equal(t, "str-456", m["id"])
		assert.Equal(t, "cluster-prod", m["name"])
	})

	// C. custom map type
	t.Run("custom map type normalized with required fields", func(t *testing.T) {
		input := TestCustomMap{
			"id":   "cust-789",
			"role": "leader",
		}
		expected := &model.TaskExpectedOutput{
			Type:     "object",
			Required: []string{"id", "role"},
		}
		out, valErr := ValidateTaskOutput(input, expected, "test.task")
		require.Nil(t, valErr)
		m, ok := out.(map[string]any)
		require.True(t, ok, "custom map must be normalized to map[string]any")
		assert.Equal(t, "cust-789", m["id"])
		assert.Equal(t, "leader", m["role"])
	})

	// D. struct
	t.Run("struct normalized to map[string]any with required fields", func(t *testing.T) {
		input := TestResult{
			Items: []TestItem{{Name: "node-1"}, {Name: "node-2"}},
			ID:    "res-999",
		}
		expected := &model.TaskExpectedOutput{
			Type:     "object",
			Required: []string{"items", "id"},
		}
		out, valErr := ValidateTaskOutput(input, expected, "test.task")
		require.Nil(t, valErr)
		m, ok := out.(map[string]any)
		require.True(t, ok, "struct must be normalized to map[string]any")
		assert.Equal(t, "res-999", m["id"])
		items, ok := m["items"].([]any)
		require.True(t, ok, "nested slice in struct must be normalized to []any")
		assert.Len(t, items, 2)
	})

	// E. struct with type: json
	t.Run("struct with type: json normalized and checked", func(t *testing.T) {
		input := TestResult{
			Items: []TestItem{{Name: "node-1"}},
			ID:    "res-888",
		}
		expected := &model.TaskExpectedOutput{
			Type:     "json",
			Required: []string{"items", "id"},
		}
		out, valErr := ValidateTaskOutput(input, expected, "test.task")
		require.Nil(t, valErr)
		m, ok := out.(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "res-888", m["id"])
	})

	// F. wrapped payload with struct
	t.Run("wrapped payload containing struct preserves envelope and normalizes inner", func(t *testing.T) {
		input := map[string]any{
			"data": TestResult{
				Items: []TestItem{{Name: "worker-1"}},
				ID:    "res-wrapped",
			},
			"exit_code": 0,
		}
		expected := &model.TaskExpectedOutput{
			Type:     "object",
			Required: []string{"items", "id"},
		}
		out, valErr := ValidateTaskOutput(input, expected, "test.task")
		require.Nil(t, valErr)
		resMap, ok := out.(map[string]any)
		require.True(t, ok)
		assert.Equal(t, 0, resMap["exit_code"])
		inner, ok := resMap["data"].(map[string]any)
		require.True(t, ok, "wrapped struct must be normalized to map[string]any inside data envelope")
		assert.Equal(t, "res-wrapped", inner["id"])
	})

	// Missing required field on struct
	t.Run("struct missing required field returns ERR_MISSING_REQUIRED_FIELD", func(t *testing.T) {
		input := TestResult{
			Items: []TestItem{},
			ID:    "res-incomplete",
		}
		expected := &model.TaskExpectedOutput{
			Type:     "object",
			Required: []string{"id", "nonexistent_field"},
		}
		_, valErr := ValidateTaskOutput(input, expected, "test.task")
		require.NotNil(t, valErr)
		assert.Equal(t, ErrCodeMissingRequiredField, valErr.Code)
		assert.False(t, valErr.IsRetryable())
	})

	// Struct passed where array was expected
	t.Run("struct with type array returns ERR_TYPE_MISMATCH", func(t *testing.T) {
		input := TestResult{ID: "not-an-array"}
		expected := &model.TaskExpectedOutput{Type: "array"}
		_, valErr := ValidateTaskOutput(input, expected, "test.task")
		require.NotNil(t, valErr)
		assert.Equal(t, ErrCodeTypeMismatch, valErr.Code)
		assert.False(t, valErr.IsRetryable())
	})
}

func TestValidateTaskOutput_ArrayNormalization(t *testing.T) {
	// []string -> []any
	t.Run("[]string normalized to []any", func(t *testing.T) {
		input := []string{"us-east-1", "us-west-2", "eu-west-1"}
		expected := &model.TaskExpectedOutput{Type: "array"}
		out, valErr := ValidateTaskOutput(input, expected, "test.task")
		require.Nil(t, valErr)
		arr, ok := out.([]any)
		require.True(t, ok, "output must be []any")
		assert.Equal(t, []any{"us-east-1", "us-west-2", "eu-west-1"}, arr)
	})

	// []int -> []any
	t.Run("[]int normalized to []any", func(t *testing.T) {
		input := []int{200, 201, 204}
		expected := &model.TaskExpectedOutput{Type: "array"}
		out, valErr := ValidateTaskOutput(input, expected, "test.task")
		require.Nil(t, valErr)
		arr, ok := out.([]any)
		require.True(t, ok, "output must be []any")
		assert.Equal(t, []any{float64(200), float64(201), float64(204)}, arr)
	})

	// []struct -> []any
	t.Run("[]struct normalized to []any with inner maps", func(t *testing.T) {
		input := []TestItem{{Name: "alpha"}, {Name: "beta"}}
		expected := &model.TaskExpectedOutput{Type: "array"}
		out, valErr := ValidateTaskOutput(input, expected, "test.task")
		require.Nil(t, valErr)
		arr, ok := out.([]any)
		require.True(t, ok, "output must be []any")
		require.Len(t, arr, 2)
		first, ok := arr[0].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "alpha", first["name"])
	})

	// custom slice type -> []any
	t.Run("custom slice type normalized to []any", func(t *testing.T) {
		input := TestCustomSlice{"prod-1", "prod-2"}
		expected := &model.TaskExpectedOutput{Type: "array"}
		out, valErr := ValidateTaskOutput(input, expected, "test.task")
		require.Nil(t, valErr)
		arr, ok := out.([]any)
		require.True(t, ok, "output must be []any")
		assert.Equal(t, []any{"prod-1", "prod-2"}, arr)
	})

	// wrapped array in data key
	t.Run("wrapped []string in data envelope normalizes to []any inside data", func(t *testing.T) {
		input := map[string]any{
			"data":      []string{"k8s-master-1", "k8s-master-2"},
			"exit_code": 0,
		}
		expected := &model.TaskExpectedOutput{Type: "array"}
		out, valErr := ValidateTaskOutput(input, expected, "test.task")
		require.Nil(t, valErr)
		m, ok := out.(map[string]any)
		require.True(t, ok)
		assert.Equal(t, 0, m["exit_code"])
		arr, ok := m["data"].([]any)
		require.True(t, ok, "wrapped data slice must be normalized to []any")
		assert.Equal(t, []any{"k8s-master-1", "k8s-master-2"}, arr)
	})

	// array expected but map provided -> ERR_TYPE_MISMATCH
	t.Run("map provided when array expected returns ERR_TYPE_MISMATCH", func(t *testing.T) {
		input := map[string]any{"not": "an array"}
		expected := &model.TaskExpectedOutput{Type: "array"}
		_, valErr := ValidateTaskOutput(input, expected, "test.task")
		require.NotNil(t, valErr)
		assert.Equal(t, ErrCodeTypeMismatch, valErr.Code)
		assert.False(t, valErr.IsRetryable())
	})
}

func BenchmarkValidateTaskOutput(b *testing.B) {
	b.Run("NoValidation", func(b *testing.B) {
		input := map[string]any{"status": "healthy", "nodes": 5}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = ValidateTaskOutput(input, nil, "bench.task")
		}
	})

	b.Run("StringValidation", func(b *testing.B) {
		input := "healthy status check output"
		expected := &model.TaskExpectedOutput{Type: "string"}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = ValidateTaskOutput(input, expected, "bench.task")
		}
	})

	b.Run("JSONParsingAndNormalization", func(b *testing.B) {
		input := `{"cluster_id":"c-999","region":"us-east-1","nodes":12,"ready":true}`
		expected := &model.TaskExpectedOutput{Type: "json"}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = ValidateTaskOutput(input, expected, "bench.task")
		}
	})

	b.Run("RequiredFieldValidation", func(b *testing.B) {
		input := map[string]any{
			"cluster_id": "c-999",
			"region":     "us-east-1",
			"nodes":      12,
			"ready":      true,
		}
		expected := &model.TaskExpectedOutput{
			Type:     "object",
			Required: []string{"cluster_id", "region", "nodes"},
		}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = ValidateTaskOutput(input, expected, "bench.task")
		}
	})
}
