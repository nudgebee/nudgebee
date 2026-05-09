package api

import (
	"fmt"
	"log/slog"
	"nudgebee/services/common"
	"nudgebee/services/query"
	"reflect"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/graphql-go/graphql/language/ast"
	"github.com/graphql-go/graphql/language/parser"
	"github.com/graphql-go/graphql/language/source"
	"github.com/samber/lo"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

func generateColumnFromAstField(f *ast.Field) query.QueryColumn {
	expr := ""
	args := []string{}
	if len(f.Arguments) > 0 {
		for _, arg := range f.Arguments {
			expr = arg.Name.Value
			args = append(args, arg.Value.GetValue().(string))
		}
	}
	return query.QueryColumn{Name: f.Name.Value, Expr: expr, Args: args}
}

func parseHasuraSelectColumns(hasuraPayload *HasuraActionRequest) ([]query.QueryColumn, error) {
	var cols []query.QueryColumn
	gqlQuery := hasuraPayload.RequestQuery
	if gqlQuery == "" {
		return cols, nil
	}

	//parse the query to get the o/p fields
	parsedQuery, err := parser.Parse(parser.ParseParams{
		Source: &source.Source{
			Body: []byte(gqlQuery),
			Name: "GraphQL",
		},
	})
	if err != nil {
		return cols, err
	}
	//get the selection set
	actionNameToParser := hasuraPayload.Action.Name
	// for now only check first definition
	selectionSet := parsedQuery.Definitions[0].(*ast.OperationDefinition).SelectionSet.Selections
	//get the selection set fields
	selectionSetIndex := 0
	for i, selection := range selectionSet {
		if selection.((*ast.Field)).Name.Value == actionNameToParser {
			selectionSetIndex = i
			break
		}
	}
	fields := selectionSet[selectionSetIndex].(*ast.Field).SelectionSet.Selections

	for _, field := range fields {
		if f, ok := field.(*ast.Field); ok {
			if f.SelectionSet != nil {
				//get the nested fields
				nestedFields := f.SelectionSet.Selections
				for _, nestedField := range nestedFields {
					if nf, ok := nestedField.(*ast.Field); ok {
						cols = append(cols, generateColumnFromAstField(nf))
					}
				}
			} else {
				cols = append(cols, generateColumnFromAstField(f))
			}
		}
	}
	return cols, nil
}

func fixWhereClause(whereMap map[string]any) (map[string]any, error) {
	whereMap2 := make(map[string]any)
	binaryMap := make(map[string]any)
	for k, v := range whereMap {
		if k == "_and" || k == "_or" || k == "_not" {
			compositeClause := []any{}
			if reflect.TypeOf(v).Kind() == reflect.Slice {
				for _, subClause := range v.([]any) {
					w, err := fixWhereClause(subClause.(map[string]any))
					if err != nil {
						return nil, err
					}
					compositeClause = append(compositeClause, w)
				}
				whereMap2[k] = compositeClause
			} else {
				return nil, fmt.Errorf("invalid type for %s", k)
			}
		} else {
			binaryMap[k] = v
		}
	}
	whereMap2["_binary"] = binaryMap
	return whereMap2, nil
}

func handleHasuraQueryAction(hasuraPayload *HasuraActionRequest, c *gin.Context, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	t0 := time.Now()
	queryMap := hasuraPayload.Input
	if queryMap["where"] != nil {
		whereMap := queryMap["where"].(map[string]any)
		w, err := fixWhereClause(whereMap)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest("unable to parse where clause - "+err.Error()))
			return
		}
		queryMap["where"] = w
	}
	if queryMap["having"] != nil {
		havingMap := queryMap["having"].(map[string]any)
		h, err := fixWhereClause(havingMap)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest("unable to parse having clause - "+err.Error()))
			return
		}
		queryMap["having"] = h
	}
	if queryMap["columns"] == nil || len(queryMap["columns"].([]any)) == 0 {
		colsFromGql, err := parseHasuraSelectColumns(hasuraPayload)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest("unable to parse graphql - "+err.Error()))
			return
		}

		queryMap["columns"] = colsFromGql
	} else if queryMap["columns"] != nil {
		cols := queryMap["columns"].([]any)
		queryMap["columns"] = lo.Map(cols, func(item any, index int) query.QueryColumn { return query.QueryColumn{Name: item.(string)} })
	}
	if queryMap["column_transformations"] != nil {
		transformations := queryMap["column_transformations"].([]any)
		for _, transformation := range transformations {
			transformationMap := transformation.(map[string]any)
			if transformationMap["name"] == nil || transformationMap["expr"] == nil {
				c.JSON(400, common.ErrorHasuraActionBadRequest("missing name/expr in column transformation"))
				return
			}
			name := transformationMap["name"].(string)
			expr := transformationMap["expr"].(string)
			args := []string{}

			if transformationMap["args"] != nil {
				for _, arg := range transformationMap["args"].([]any) {
					args = append(args, arg.(string))
				}
			}

			for i, col := range queryMap["columns"].([]query.QueryColumn) {
				if col.Name == name {
					col.Expr = expr
					col.Args = args
					queryMap["columns"].([]query.QueryColumn)[i] = col
					break
				}
			}

		}
	}

	var queryRequest query.QueryRequest
	err := common.UnmarshalMapToStruct(queryMap, &queryRequest)
	if err != nil {
		c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
		return
	}

	queryRequest.Table = hasuraPayload.Action.Name
	q, _ := common.MarshalJson(queryRequest)
	logger.Info(fmt.Sprintf("Executing Query %v", string(q)))
	q, _ = common.MarshalJson(queryMap)
	logger.Info(fmt.Sprintf("Executing QueryMap %v", string(q)))
	ctx, err := buildContextFromHasuraPayload(c, hasuraPayload, tracer, meter, logger)

	if err != nil {
		c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
		return
	}
	parseTime := time.Since(t0)
	tExec := time.Now()
	resp, err := query.ExecuteQuery(ctx, queryRequest)
	execTime := time.Since(tExec)
	totalTime := time.Since(t0)

	ctx.GetLogger().Info("query execution time",
		"table", queryRequest.Table,
		"parse_ms", parseTime.Milliseconds(),
		"exec_ms", execTime.Milliseconds(),
		"total_ms", totalTime.Milliseconds(),
	)

	if err != nil {
		c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
		return
	}

	if len(resp.Errors) > 0 {
		c.JSON(400, common.ErrorHasuraActionBadRequest(fmt.Sprintf("query execution errors: %v", resp.Errors)))
		return
	}

	c.JSON(200, resp)
}
