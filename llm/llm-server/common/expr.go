package common

import (
	"fmt"

	"github.com/expr-lang/expr"
)

func EvaluateExpression(contextData map[string]any, conditionToEvaluate string) (any, error) {
	// Provide the contextData as the environment for the expression
	// expr.Env(contextData) allows the expression to access keys from the map as variables.
	program, err := expr.Compile(conditionToEvaluate, expr.Env(contextData))
	if err != nil {
		return nil, fmt.Errorf("error compiling expression: %w", err)
	}

	result, err := expr.Run(program, contextData)
	if err != nil {
		return nil, fmt.Errorf("error running expression: %w", err)
	}
	return result, nil
}
