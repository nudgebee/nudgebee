package kql

import (
	"encoding/json"
	"fmt"
	"strings"
)

// KqlSignozConverter translates KQL AST to SigNoz query.
type KqlSignozConverter struct{}

// Translate converts a KQLQuery AST into a SigNoz query string.
func (c *KqlSignozConverter) Translate(ast KQLQuery) (string, error) {
	query := SigNozQuery{
		QueryType: "list", // Default to list query
		Filters:   []SigNozFilter{},
		Aggregate: SigNozAggregate{
			Functions: []SigNozAggregateFunction{},
			GroupBy:   []string{},
		},
	}

	// Handle the source of the query
	if ast.Source != nil {
		if ast.Source.TableName != nil {
			// SigNoz doesn't have a direct "table name" concept like Loki's app label.
			// We can map this to a serviceName filter if applicable, or ignore if not.
			// For now, let's assume it's a filter on 'serviceName'
			query.Filters = append(query.Filters, SigNozFilter{
				Key:      "serviceName",
				Operator: "=",
				Value:    *ast.Source.TableName,
			})
		} else if ast.Source.Search != nil {
			searchVal, err := c.translateArithmeticExpression(ast.Source.Search.Expression.Left.Left.Predicate.Left)
			if err != nil {
				return "", err
			}
			query.Filters = append(query.Filters, SigNozFilter{
				Key:      "body", // Assuming search applies to the log body
				Operator: "contains",
				Value:    strings.Trim(searchVal, `"`),
			})
		}
	}

	// Process each pipe operation
	for _, op := range ast.Operations {
		if op.Operator.Where != nil {
			whereFilter, err := c.translateExpression(op.Operator.Where.Expression)
			if err != nil {
				return "", err
			}
			query.Filters = append(query.Filters, whereFilter)
		} else if op.Operator.Project != nil {
			// SigNoz doesn't have a direct "project" equivalent in its query API for logs.
			// This might need to be handled post-query or by selecting specific fields in the UI.
			// For now, we'll ignore it or return an error if strict.
			return "", fmt.Errorf("project operator not directly supported in SigNoz query API")
		} else if op.Operator.Take != nil {
			query.Limit = op.Operator.Take.Count
		} else if op.Operator.Summarize != nil {
			if len(op.Operator.Summarize.Aggregations) == 1 {
				agg := op.Operator.Summarize.Aggregations[0]
				if agg.Function.Name == "count" {
					query.QueryType = "aggregate"
					query.Aggregate.Functions = append(query.Aggregate.Functions, SigNozAggregateFunction{
						Name: "count",
						Key:  "body", // Count of logs
					})
					if op.Operator.Summarize.ByClause != nil {
						for _, col := range op.Operator.Summarize.ByClause.Columns {
							colName, err := c.translatePrimary(col)
							if err != nil {
								return "", err
							}
							query.Aggregate.GroupBy = append(query.Aggregate.GroupBy, colName)
						}
					}
				} else {
					return "", fmt.Errorf("unsupported summarize aggregation: %s", agg.Function.Name)
				}
			} else {
				return "", fmt.Errorf("only single summarize aggregation is supported")
			}
		} else if op.Operator.ParseRegex != nil {
			// SigNoz has a concept of "parse" but it's usually done at ingest time or via UI.
			// Direct regex parsing in the query might not be straightforward.
			return "", fmt.Errorf("parse regex operator not directly supported in SigNoz query API")
		} else if op.Operator.Parse != nil {
			return "", fmt.Errorf("parse operator not directly supported in SigNoz query API")
		} else {
			return "", fmt.Errorf("unsupported operator")
		}
	}

	jsonQuery, err := json.Marshal(query)
	if err != nil {
		return "", err
	}

	return string(jsonQuery), nil
}

// SigNozQuery represents the structure of a SigNoz logs query.
type SigNozQuery struct {
	QueryType string          `json:"queryType"`
	Filters   []SigNozFilter  `json:"filters"`
	Limit     int             `json:"limit,omitempty"`
	Aggregate SigNozAggregate `json:"aggregate"`
}

// SigNozFilter represents a filter in a SigNoz query.
type SigNozFilter struct {
	Key       string         `json:"key,omitempty"`
	Operator  string         `json:"op,omitempty"`
	Value     string         `json:"value,omitempty"`
	LogicalOp string         `json:"logicalOp,omitempty"`
	Items     []SigNozFilter `json:"items,omitempty"`
}

// SigNozAggregate represents the aggregation part of a SigNoz query.
type SigNozAggregate struct {
	Functions []SigNozAggregateFunction `json:"functions"`
	GroupBy   []string                  `json:"groupBy"`
}

// SigNozAggregateFunction represents an aggregation function in SigNoz.
type SigNozAggregateFunction struct {
	Name string `json:"name"`
	Key  string `json:"key"`
}

// translateExpression converts a KQL expression to a SigNoz filter.
func (c *KqlSignozConverter) translateExpression(expr *Expression) (SigNozFilter, error) {
	if expr == nil {
		return SigNozFilter{}, nil
	}

	filters := []SigNozFilter{}

	// Translate the left part of the expression
	leftFilter, err := c.translateAndTerm(expr.Left)
	if err != nil {
		return SigNozFilter{}, err
	}
	if leftFilter.Key != "" || leftFilter.LogicalOp != "" || len(leftFilter.Items) > 0 {
		filters = append(filters, leftFilter)
	}

	// Translate the right parts (OR conditions)
	for _, orTerm := range expr.Right {
		rightFilter, err := c.translateAndTerm(orTerm.Right)
		if err != nil {
			return SigNozFilter{}, err
		}
		if rightFilter.Key != "" || rightFilter.LogicalOp != "" || len(rightFilter.Items) > 0 {
			filters = append(filters, rightFilter)
		}
	}

	if len(filters) == 0 {
		return SigNozFilter{}, nil
	} else if len(filters) == 1 {
		return filters[0], nil // If only one filter, return it directly
	} else {
		return SigNozFilter{
			LogicalOp: "OR",
			Items:     filters,
		}, nil
	}
}

// translateAndTerm translates an AND term to a SigNoz filter.
func (c *KqlSignozConverter) translateAndTerm(term *AndTerm) (SigNozFilter, error) {
	if term == nil {
		return SigNozFilter{}, nil
	}

	filters := []SigNozFilter{}

	// Translate the left part of the term
	leftFilter, err := c.translateComparison(term.Left)
	if err != nil {
		return SigNozFilter{}, err
	}
	// Only append if the filter is not empty (e.g., from a nil comparison)
	if leftFilter.Key != "" || leftFilter.LogicalOp != "" || len(leftFilter.Items) > 0 {
		filters = append(filters, leftFilter)
	}

	// Translate the right parts (AND conditions)
	for _, andTerm := range term.Right {
		rightFilter, err := c.translateComparison(andTerm.Right)
		if err != nil {
			return SigNozFilter{}, err
		}
		if rightFilter.Key != "" || rightFilter.LogicalOp != "" || len(rightFilter.Items) > 0 {
			filters = append(filters, rightFilter)
		}
	}

	if len(filters) == 0 {
		return SigNozFilter{}, nil
	} else if len(filters) == 1 {
		return filters[0], nil // If only one filter, return it directly
	} else {
		return SigNozFilter{
			LogicalOp: "AND",
			Items:     filters,
		}, nil
	}
}

// translateComparison translates a comparison to a SigNoz filter.
func (c *KqlSignozConverter) translateComparison(comp *Comparison) (SigNozFilter, error) {
	if comp == nil {
		return SigNozFilter{}, nil
	}

	if comp.SubExpression != nil {
		// A sub-expression is essentially a nested filter group
		filter, err := c.translateExpression(comp.SubExpression)
		if err != nil {
			return SigNozFilter{}, err
		}
		return filter, nil
	}

	if comp.Predicate != nil {
		return c.translatePredicate(comp.Predicate)
	}

	return SigNozFilter{}, fmt.Errorf("unsupported comparison")
}

// translatePredicate translates a predicate to a SigNoz filter.
func (c *KqlSignozConverter) translatePredicate(pred *Predicate) (SigNozFilter, error) {
	if pred == nil {
		return SigNozFilter{}, fmt.Errorf("predicate is nil")
	}

	key, err := c.translateArithmeticExpression(pred.Left)
	if err != nil {
		return SigNozFilter{}, err
	}

	if pred.Op == nil {
		// If no operator, assume it's a boolean check for existence
		return SigNozFilter{
			Key:      key,
			Operator: "exists",
			Value:    "",
		}, nil
	}

	if pred.Op.Binary != nil {
		value, err := c.translateArithmeticExpression(pred.Op.Binary.Right)
		if err != nil {
			return SigNozFilter{}, err
		}

		op := ""
		switch pred.Op.Binary.Operator {
		case "==":
			op = "="
		case "!=":
			op = "!="
		case ">":
			op = ">"
		case ">=":
			op = ">="
		case "<":
			op = "<"
		case "<=":
			op = "<="
		case "contains":
			op = "contains"
		case "!contains":
			op = "notContains"
		case "has": // Assuming 'has' is similar to contains for a single value
			op = "contains"
		case "!has":
			op = "notContains"
		case "matches regex":
			op = "regex"
		// Add more mappings as needed
		case "startswith":
			op = "regex"
			value = "^" + strings.Trim(value, `"`)
		case "!startswith":
			op = "notRegex"
			value = "^" + strings.Trim(value, `"`)
		case "endswith":
			op = "regex"
			value = strings.Trim(value, `"`) + "$"
		case "!endswith":
			op = "notRegex"
			value = strings.Trim(value, `"`) + "$"
		case "contains_cs":
			op = "regex" // SigNoz regex is case-sensitive by default for exact matches, or can use flags
			// Note: SigNoz 'contains' is case-insensitive. Using regex for case-sensitive contains.
			value = strings.Trim(value, `"`) // Value is already a string literal
		default:
			return SigNozFilter{}, fmt.Errorf("unsupported binary operator: %s", pred.Op.Binary.Operator)
		}

		return SigNozFilter{
			Key:      key,
			Operator: op,
			Value:    strings.Trim(value, `"`), // Remove quotes from string literals
		}, nil
	}

	if pred.Op.In != nil {
		var values []string
		for _, val := range pred.Op.In.Values {
			translatedVal, err := c.translateArithmeticExpression(val)
			if err != nil {
				return SigNozFilter{}, err
			}
			values = append(values, strings.Trim(translatedVal, `"`))
		}
		op := "in"
		if strings.HasPrefix(pred.Op.In.Operator, "!") { // Check for '!in'
			op = "notIn"
		}
		return SigNozFilter{
			Key:      key,
			Operator: op,
			Value:    strings.Join(values, ","),
		}, nil
	}

	return SigNozFilter{}, fmt.Errorf("unsupported predicate operation")
}

// translateArithmeticExpression translates an arithmetic expression.
func (c *KqlSignozConverter) translateArithmeticExpression(expr *ArithmeticExpression) (string, error) {
	if expr == nil {
		return "", nil
	}
	translatedTerm, err := c.translateArithmeticTerm(expr.Left)
	if err != nil {
		return "", err
	}
	return translatedTerm, nil
}

func (c *KqlSignozConverter) translateArithmeticTerm(term *ArithmeticTerm) (string, error) {
	if term == nil {
		return "", nil
	}
	translatedPrimary, err := c.translatePrimary(term.Left)
	if err != nil {
		return "", err
	}
	return translatedPrimary, nil
}

// translatePrimary translates a primary value (like a column name or literal).
func (c *KqlSignozConverter) translatePrimary(primary *Primary) (string, error) {
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
			return *primary.Literal.String, nil
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

func (c *KqlSignozConverter) translateColumnPath(path *ColumnPath) (string, error) {
	if path == nil {
		return "", nil
	}
	// SigNoz typically uses dot notation for nested fields in filters.
	// KQL's ColumnPath with accessors maps well to this.
	var parts []string
	parts = append(parts, path.Base)
	for _, accessor := range path.Accessors {
		if accessor.DotProperty != nil {
			parts = append(parts, *accessor.DotProperty)
		} else if accessor.BracketAccess != nil {
			return "", fmt.Errorf("bracket access not supported for SigNoz column paths")
		}
	}
	return strings.Join(parts, "."), nil
}
