package kql

import (
	"fmt"
	"strings"
)

type KqlLokiConverter struct{}

// translates kql ast to loki query
// thrrow error for not supported features
func (c *KqlLokiConverter) Translate(ast KQLQuery) (string, error) {
	var streamSelector, filter, parser, metricQuery string

	// Detect if we need to automatically add the json operator
	autoJSON := c.hasNestedFieldAccess(ast)

	// Handle the source of the query
	if ast.Source != nil {
		if ast.Source.TableName != nil {
			streamSelector = fmt.Sprintf(`{app="%s"}`, *ast.Source.TableName)
		} else if ast.Source.Search != nil {
			searchExpr, err := c.translateExpression(ast.Source.Search.Expression)
			if err != nil {
				return "", err
			}
			filter = searchExpr
		}
	}

	// Process each pipe operation
	for _, op := range ast.Operations {
		if op.Operator.Where != nil {
			whereExpr, err := c.translateExpression(op.Operator.Where.Expression)
			if err != nil {
				return "", err
			}
			filter = fmt.Sprintf("%s |~ `%s`", filter, whereExpr)
		} else if op.Operator.Project != nil {
			var columns []string
			for _, col := range op.Operator.Project.Columns {
				colName, err := c.translateArithmeticExpression(col.Expression)
				if err != nil {
					return "", err
				}
				columns = append(columns, colName)
			}
			parser = fmt.Sprintf(` | line_format "{{%s}}"`, strings.Join(columns, " "))
		} else if op.Operator.Take != nil {
			filter = fmt.Sprintf("%s | limit %d", filter, op.Operator.Take.Count)
		} else if op.Operator.Summarize != nil {
			// For now, we only support count() aggregation
			if len(op.Operator.Summarize.Aggregations) == 1 && op.Operator.Summarize.Aggregations[0].Function.Name == "count" {
				if op.Operator.Summarize.ByClause != nil {
					var byColumns []string
					for _, col := range op.Operator.Summarize.ByClause.Columns {
						colName, err := c.translatePrimary(col)
						if err != nil {
							return "", err
						}
						byColumns = append(byColumns, colName)
					}
					metricQuery = fmt.Sprintf(`sum by (%s) (count_over_time(%s[5m]))`, strings.Join(byColumns, ","), streamSelector)
				} else {
					metricQuery = fmt.Sprintf(`count_over_time(%s[5m])`, streamSelector)
				}
			} else {
				return "", fmt.Errorf("unsupported summarize aggregation")
			}
		} else if op.Operator.ParseRegex != nil {
			filter = fmt.Sprintf("%s | regexp \"%s\"", filter, op.Operator.ParseRegex.Pattern)
		} else if op.Operator.Parse != nil {
			return "", fmt.Errorf("unsupported parse operator format")
		} else {
			return "", fmt.Errorf("unsupported operator")
		}
	}

	if autoJSON {
		filter = " | json" + filter
	}

	if metricQuery != "" {
		return fmt.Sprintf("%s | %s", streamSelector, metricQuery), nil
	}

	return fmt.Sprintf("%s%s%s", streamSelector, filter, parser), nil
}

// hasNestedFieldAccess walks the AST to detect if there is any nested field access.
func (c *KqlLokiConverter) hasNestedFieldAccess(ast KQLQuery) bool {
	for _, op := range ast.Operations {
		if op.Operator.Where != nil {
			if c.expressionHasNestedFieldAccess(op.Operator.Where.Expression) {
				return true
			}
		}
		if op.Operator.Project != nil {
			for _, col := range op.Operator.Project.Columns {
				if c.arithmeticExpressionHasNestedFieldAccess(col.Expression) {
					return true
				}
			}
		}
	}
	return false
}

func (c *KqlLokiConverter) expressionHasNestedFieldAccess(expr *Expression) bool {
	if expr == nil {
		return false
	}
	if c.andTermHasNestedFieldAccess(expr.Left) {
		return true
	}
	for _, orTerm := range expr.Right {
		if c.andTermHasNestedFieldAccess(orTerm.Right) {
			return true
		}
	}
	return false
}

func (c *KqlLokiConverter) andTermHasNestedFieldAccess(term *AndTerm) bool {
	if term == nil {
		return false
	}
	if c.comparisonHasNestedFieldAccess(term.Left) {
		return true
	}
	for _, andTerm := range term.Right {
		if c.comparisonHasNestedFieldAccess(andTerm.Right) {
			return true
		}
	}
	return false
}

func (c *KqlLokiConverter) comparisonHasNestedFieldAccess(comp *Comparison) bool {
	if comp == nil {
		return false
	}
	if comp.SubExpression != nil {
		return c.expressionHasNestedFieldAccess(comp.SubExpression)
	}
	if comp.Predicate != nil {
		return c.predicateHasNestedFieldAccess(comp.Predicate)
	}
	return false
}

func (c *KqlLokiConverter) predicateHasNestedFieldAccess(pred *Predicate) bool {
	if pred == nil {
		return false
	}
	if c.arithmeticExpressionHasNestedFieldAccess(pred.Left) {
		return true
	}
	if pred.Op != nil && pred.Op.Binary != nil {
		return c.arithmeticExpressionHasNestedFieldAccess(pred.Op.Binary.Right)
	}
	return false
}

func (c *KqlLokiConverter) arithmeticExpressionHasNestedFieldAccess(expr *ArithmeticExpression) bool {
	if expr == nil {
		return false
	}
	if c.arithmeticTermHasNestedFieldAccess(expr.Left) {
		return true
	}
	for _, term := range expr.Right {
		if c.arithmeticTermHasNestedFieldAccess(term.Term) {
			return true
		}
	}
	return false
}

func (c *KqlLokiConverter) arithmeticTermHasNestedFieldAccess(term *ArithmeticTerm) bool {
	if term == nil {
		return false
	}
	if c.primaryHasNestedFieldAccess(term.Left) {
		return true
	}
	for _, factor := range term.Right {
		if c.primaryHasNestedFieldAccess(factor.Factor) {
			return true
		}
	}
	return false
}

func (c *KqlLokiConverter) primaryHasNestedFieldAccess(primary *Primary) bool {
	if primary == nil {
		return false
	}
	if primary.ColumnPath != nil && len(primary.ColumnPath.Accessors) > 0 {
		return true
	}
	return false
}

// translateExpression converts a KQL expression to a Loki log filter expression.
func (c *KqlLokiConverter) translateExpression(expr *Expression) (string, error) {
	if expr == nil {
		return "", nil
	}

	// Translate the left part of the expression
	left, err := c.translateAndTerm(expr.Left)
	if err != nil {
		return "", err
	}

	// Translate the right parts (OR conditions)
	for _, orTerm := range expr.Right {
		right, err := c.translateAndTerm(orTerm.Right)
		if err != nil {
			return "", err
		}
		left = fmt.Sprintf("%s or %s", left, right)
	}

	return left, nil
}

// translateAndTerm translates an AND term to its Loki equivalent.
func (c *KqlLokiConverter) translateAndTerm(term *AndTerm) (string, error) {
	if term == nil {
		return "", nil
	}

	// Translate the left part of the term
	left, err := c.translateComparison(term.Left)
	if err != nil {
		return "", err
	}

	// Translate the right parts (AND conditions)
	for _, andTerm := range term.Right {
		right, err := c.translateComparison(andTerm.Right)
		if err != nil {
			return "", err
		}
		left = fmt.Sprintf("%s and %s", left, right)
	}

	return left, nil
}

// translateComparison translates a comparison to its Loki equivalent.
func (c *KqlLokiConverter) translateComparison(comp *Comparison) (string, error) {
	if comp == nil {
		return "", nil
	}

	if comp.SubExpression != nil {
		return c.translateExpression(comp.SubExpression)
	}

	if comp.Predicate != nil {
		return c.translatePredicate(comp.Predicate)
	}

	return "", fmt.Errorf("unsupported comparison")
}

// translatePredicate translates a predicate to its Loki equivalent.
func (c *KqlLokiConverter) translatePredicate(pred *Predicate) (string, error) {
	if pred == nil {
		return "", nil
	}

	left, err := c.translateArithmeticExpression(pred.Left)
	if err != nil {
		return "", err
	}

	if pred.Op == nil {
		// This could be a boolean check like "where my_field"
		return left, nil
	}

	if pred.Op.Binary != nil {
		right, err := c.translateArithmeticExpression(pred.Op.Binary.Right)
		if err != nil {
			return "", err
		}
		// Note: Loki operators might differ from KQL. This is a simple mapping.
		return fmt.Sprintf("%s %s %s", left, pred.Op.Binary.Operator, right), nil
	}

	if pred.Op.In != nil {
		var values []string
		for _, val := range pred.Op.In.Values {
			translatedVal, err := c.translateArithmeticExpression(val)
			if err != nil {
				return "", err
			}
			// Remove quotes from the translated value for the regex
			values = append(values, strings.Trim(translatedVal, `"`))
		}
		return fmt.Sprintf(`%s=~"%s"`, left, strings.Join(values, "|")), nil
	}

	return "", fmt.Errorf("unsupported predicate operation")
}

// translateArithmeticExpression translates an arithmetic expression.
// For now, we'll keep it simple and assume it's a primary value.
func (c *KqlLokiConverter) translateArithmeticExpression(expr *ArithmeticExpression) (string, error) {
	if expr == nil {
		return "", nil
	}
	// This is a simplification. A full implementation would handle arithmetic.
	translatedTerm, err := c.translateArithmeticTerm(expr.Left)
	if err != nil {
		return "", err
	}
	return translatedTerm, nil
}

func (c *KqlLokiConverter) translateArithmeticTerm(term *ArithmeticTerm) (string, error) {
	if term == nil {
		return "", nil
	}
	// This is a simplification. A full implementation would handle arithmetic.
	translatedPrimary, err := c.translatePrimary(term.Left)
	if err != nil {
		return "", err
	}
	return translatedPrimary, nil
}

// translatePrimary translates a primary value (like a column name or literal).
func (c *KqlLokiConverter) translatePrimary(primary *Primary) (string, error) {
	if primary == nil {
		return "", nil
	}

	if primary.ColumnPath != nil {
		translatedPath, err := c.translateColumnPath(primary.ColumnPath)
		if err != nil {
			return "", err
		}
		return translatedPath, nil
	}

	if primary.Literal != nil {
		if primary.Literal.String != nil {
			return fmt.Sprintf(`%q`, *primary.Literal.String), nil
		}
		if primary.Literal.Numeric != nil {
			return fmt.Sprintf("%g", primary.Literal.Numeric.Number), nil
		}
		if primary.Literal.Identifier != nil {
			return *primary.Literal.Identifier, nil
		}
	}

	return "", fmt.Errorf("unsupported primary type")
}

func (c *KqlLokiConverter) translateColumnPath(path *ColumnPath) (string, error) {
	if path == nil {
		return "", nil
	}
	var parts []string
	parts = append(parts, path.Base)
	for _, accessor := range path.Accessors {
		if accessor.DotProperty != nil {
			parts = append(parts, *accessor.DotProperty)
		} else if accessor.BracketAccess != nil {
			return "", fmt.Errorf("bracket access not supported")
		}
	}
	return strings.Join(parts, "_"), nil
}
