package query

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"nudgebee/services/common"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
	"slices"
	"time"

	"github.com/samber/lo"
)

func ExecuteQuery(context *security.RequestContext, query QueryRequest) (QueryResponse, error) {
	startTime := time.Now()
	var sourceType database.DatabaseManagerType
	var tableDef TableDefinition
	queryArgs := []any{}
	var requestedAccountId string

	if tableDef1, ok := GetTableMetadata(query.Table); ok {
		sourceType = tableDef1.Source
		tableDef = tableDef1
	} else {
		return QueryResponse{
			Errors: []common.Error{
				common.ErrorBadRequest(fmt.Sprintf("table %s not found", query.Table)),
			},
			ExecutionTimeMs: time.Since(startTime).Milliseconds(),
		}, nil
	}

	if tableDef.Source == database.AgentMetrices {
		return processAgentMetrices(query, context, startTime)
	}

	if tableDef.SourceGenerator != nil {
		binary := query.Where.Binary
		accountIdColumnName := tableDef.AccountIdColumnName
		if binary != nil && binary[accountIdColumnName] != nil && binary[accountIdColumnName][Eq] != nil {
			account_id := binary[accountIdColumnName][Eq]
			if account_id == "" {
				return QueryResponse{
					Errors: []common.Error{
						common.ErrorBadRequest("account_id is required"),
					},
					ExecutionTimeMs: time.Since(startTime).Milliseconds(),
				}, nil
			}
			sourceDef := tableDef.SourceGenerator(context, account_id.(string), query)
			tableDef.Source = sourceDef
			sourceType = sourceDef
		}
	}
	// add security context restrictions
	if context.GetSecurityContext().GetTenantId() != "" && tableDef.Source != database.AgentWarehouse && tableDef.Source != database.AgentWarehouseBigQuery && tableDef.Source != database.AgentWarehouseChronosphere {
		columnName := tableDef.TenantIdColumnName
		if columnName != "" {
			query.Where.And = append(query.Where.And, QueryWhereClause{
				Binary: map[string]map[BinaryWhereClauseType]any{
					columnName: {
						Eq: context.GetSecurityContext().GetTenantId(),
					},
				},
			})

			if !context.GetSecurityContext().IsSuperAdmin() && !context.GetSecurityContext().IsTenantReadAdmin() && !context.GetSecurityContext().IsTenantAdmin() {
				// add account read restrictions
				accountIdColumnName := tableDef.AccountIdColumnName
				if accountIdColumnName == "" {
					return QueryResponse{
						Errors: []common.Error{
							common.ErrorBadRequest(fmt.Sprintf("account id column not found in table %s", query.Table)),
						},
						ExecutionTimeMs: time.Since(startTime).Milliseconds(),
					}, nil
				}

				if slices.Contains(context.GetSecurityContext().GetRoles(), security.AUTH_ACCOUNT_ADMIN_ROLE) || slices.Contains(context.GetSecurityContext().GetRoles(), security.AUTH_ACCOUNT_READ_ADMIN_ROLE) {
					accountIds := context.GetSecurityContext().ListAccountIds()
					query.Where.And = append(query.Where.And, QueryWhereClause{
						Binary: map[string]map[BinaryWhereClauseType]any{
							accountIdColumnName: {
								In: accountIds,
							},
						},
					})
				} else if slices.Contains(context.GetSecurityContext().GetRoles(), security.AUTH_K8S_NAMESPACE_ADMIN_ROLE) || slices.Contains(context.GetSecurityContext().GetRoles(), security.AUTH_K8S_NAMESPACE_READ_ADMIN_ROLE) {
					accountIds := context.GetSecurityContext().ListAccountIds()
					query.Where.And = append(query.Where.And, QueryWhereClause{
						Binary: map[string]map[BinaryWhereClauseType]any{
							accountIdColumnName: {
								In: accountIds,
							},
						},
					})

					queryWithNamespace, queryResponse, err := updateQueryWithNamespace(context, tableDef, query, context, startTime, accountIdColumnName, accountIds)
					if err != nil {
						return queryResponse, err
					}
					query = queryWithNamespace
				} else {
					return QueryResponse{
						Errors: []common.Error{
							common.ErrorUnauthorized("Access Denied"),
						},
						ExecutionTimeMs: time.Since(startTime).Milliseconds(),
					}, nil
				}
			}
		}
	} else if tableDef.Source == database.AgentWarehouse || tableDef.Source == database.AgentWarehouseBigQuery {
		binary := query.Where.Binary
		accountIdColumnName := tableDef.AccountIdColumnName
		if binary != nil && binary[accountIdColumnName] != nil && binary[accountIdColumnName][Eq] != nil {
			account_id := binary[accountIdColumnName][Eq]
			delete(binary, accountIdColumnName)
			query.Columns = lo.Filter(query.Columns, func(column QueryColumn, i int) bool {
				return column.Name != accountIdColumnName
			})
			if !context.GetSecurityContext().HasAccountAccess(account_id.(string), security.SecurityAccessTypeRead) {
				return QueryResponse{
					Errors: []common.Error{
						common.ErrorUnauthorized("Access Denied"),
					},
					ExecutionTimeMs: time.Since(startTime).Milliseconds(),
				}, nil
			}
			queryArgs = append(queryArgs, account_id)
			queryArgs = append(queryArgs, tableDef.Source)

			if slices.Contains(context.GetSecurityContext().GetRoles(), security.AUTH_K8S_NAMESPACE_ADMIN_ROLE) || slices.Contains(context.GetSecurityContext().GetRoles(), security.AUTH_K8S_NAMESPACE_READ_ADMIN_ROLE) {
				queryWithNamespace, queryResponse, err := updateQueryWithNamespace(context, tableDef, query, context, startTime, accountIdColumnName, []string{account_id.(string)})
				if err != nil {
					return queryResponse, err
				}
				query = queryWithNamespace
			}
			requestedAccountId = account_id.(string)
		} else {
			return QueryResponse{
				Errors: []common.Error{
					common.ErrorBadRequest("account_id is required"),
				},
				ExecutionTimeMs: time.Since(startTime).Milliseconds(),
			}, nil

		}
	}

	// Handle Chronosphere differently - bypass SQL generation
	if sourceType == database.AgentWarehouseChronosphere {
		// Extract account_id for Chronosphere specifically
		binary := query.Where.Binary
		accountIdColumnName := tableDef.AccountIdColumnName
		if binary != nil && binary[accountIdColumnName] != nil && binary[accountIdColumnName][Eq] != nil {
			account_id := binary[accountIdColumnName][Eq]
			if !context.GetSecurityContext().HasAccountAccess(account_id.(string), security.SecurityAccessTypeRead) {
				return QueryResponse{
					Errors: []common.Error{
						common.ErrorUnauthorized("Access Denied"),
					},
					ExecutionTimeMs: time.Since(startTime).Milliseconds(),
				}, nil
			}
			requestedAccountId = account_id.(string)
		} else {
			return QueryResponse{
				Errors: []common.Error{
					common.ErrorBadRequest("account_id is required"),
				},
				ExecutionTimeMs: time.Since(startTime).Milliseconds(),
			}, nil
		}
		return executeChronosphereQuery(context, requestedAccountId, query, tableDef, startTime)
	}

	queryText, err := GenerateSqlQuery(context, requestedAccountId, query, tableDef)
	if err != nil {
		return QueryResponse{
			Errors: []common.Error{
				common.ErrorInternal(err.Error()),
			},
			ExecutionTimeMs: time.Since(startTime).Milliseconds(),
		}, nil
	}

	context.GetLogger().Info(fmt.Sprintf("Query To Execute %s", queryText))

	rowsMap, err := executeSqlQuery(sourceType, queryText, queryArgs, query.Limit)
	if err != nil {
		context.GetLogger().Error(err.Error())
		return QueryResponse{
			Errors: []common.Error{
				common.ErrorInternal("Unable to execute query"),
			},
			ExecutionTimeMs: time.Since(startTime).Milliseconds(),
		}, nil
	}
	return QueryResponse{
		Rows:            rowsMap,
		ExecutionTimeMs: time.Since(startTime).Milliseconds(),
	}, nil
}

// executeChronosphereQuery handles Chronosphere traces queries using the agent warehouse driver
func executeChronosphereQuery(context *security.RequestContext, accountId string, query QueryRequest, tableDef TableDefinition, startTime time.Time) (QueryResponse, error) {
	// Extract Chronosphere parameters from the query
	chronosphereParams := ExtractChronosphereParams(query)

	// Convert parameters to JSON query for the agent warehouse driver
	paramsJSON, err := json.Marshal(chronosphereParams)
	if err != nil {
		return QueryResponse{
			Errors: []common.Error{
				common.ErrorInternal(fmt.Sprintf("Failed to marshal Chronosphere parameters: %v", err)),
			},
			ExecutionTimeMs: time.Since(startTime).Milliseconds(),
		}, nil
	}

	context.GetLogger().Info(fmt.Sprintf("Chronosphere Query Parameters: %s", string(paramsJSON)))

	// Use the agent warehouse SQL driver directly
	rowsMap, err := executeChronosphereAgentWarehouseQuery(string(paramsJSON), accountId)
	if err != nil {
		context.GetLogger().Error(fmt.Sprintf("Chronosphere query failed: %v", err))
		return QueryResponse{
			Errors: []common.Error{
				common.ErrorInternal(err.Error()),
			},
			ExecutionTimeMs: time.Since(startTime).Milliseconds(),
		}, nil
	}

	return QueryResponse{
		Rows:            rowsMap,
		ExecutionTimeMs: time.Since(startTime).Milliseconds(),
	}, nil
}

// executeChronosphereAgentWarehouseQuery executes a Chronosphere query using the agent warehouse SQL driver
func executeChronosphereAgentWarehouseQuery(paramsJSON, accountId string) ([]QueryRow, error) {
	// Open connection using the agent warehouse driver
	db, err := sql.Open("agent_warehouse", "agent_warehouse")
	if err != nil {
		return nil, fmt.Errorf("failed to open agent warehouse connection: %v", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Error("Failed to close database connection", "error", err)
		}
	}()

	// Execute the query - the driver expects: accountId, provider as args, query as SQL
	// But the query content goes as the SQL parameter, and accountId/provider as arguments
	rows, err := db.Query(paramsJSON, accountId, "agent_warehouse_chronosphere")
	if err != nil {
		return nil, fmt.Errorf("failed to execute Chronosphere query: %v", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Error("Failed to close database rows", "error", err)
		}
	}()

	// Get column names
	columns, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("failed to get columns: %v", err)
	}

	var queryRows []QueryRow

	for rows.Next() {
		// Create slice to hold row values
		values := make([]any, len(columns))
		scanArgs := make([]any, len(columns))
		for i := range values {
			scanArgs[i] = &values[i]
		}

		// Scan the row
		if err := rows.Scan(scanArgs...); err != nil {
			return nil, fmt.Errorf("failed to scan row: %v", err)
		}

		// Convert to QueryRow format
		queryRow := QueryRow{}
		for i, col := range columns {
			if values[i] != nil {
				queryRow[col] = values[i]
			}
		}

		queryRows = append(queryRows, queryRow)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("row iteration error: %v", err)
	}

	return queryRows, nil
}

func getAccountIdFromQuery(ctx *security.RequestContext, query QueryRequest, columnName string) string {
	if query.Where.Binary != nil && query.Where.Binary[columnName] != nil && query.Where.Binary[columnName][Eq] != nil {
		return query.Where.Binary[columnName][Eq].(string)
	}
	if query.Where.And != nil {
		for _, clause := range query.Where.And {
			if clause.Binary != nil && clause.Binary[columnName] != nil && clause.Binary[columnName][Eq] != nil {
				return clause.Binary[columnName][Eq].(string)
			}
			if clause.And != nil {
				for _, innerClause := range clause.And {
					if innerClause.Binary != nil && innerClause.Binary[columnName] != nil && innerClause.Binary[columnName][Eq] != nil {
						return innerClause.Binary[columnName][Eq].(string)
					}
				}
			}
		}
	}
	return ""
}

func updateQueryWithNamespace(ctx *security.RequestContext, tableDef TableDefinition, query QueryRequest, context *security.RequestContext, startTime time.Time, accountIdColumnName string, accountIds []string) (QueryRequest, QueryResponse, error) {
	namespaceColumn := tableDef.NamespaceColumnName
	if namespaceColumn != "" {
		accountId := getAccountIdFromQuery(ctx, query, accountIdColumnName)
		accountsToCheck := accountIds
		if accountId != "" {
			accountsToCheck = []string{accountId}
		}
		namespacesToFilter := []string{}
		for _, account := range accountsToCheck {
			namespaces, err := context.GetSecurityContext().ListK8sObjectNames(account, security.K8sObjectNamespaces, security.K8sRbacPermissionTypeList)
			if err != nil {
				return query, QueryResponse{
					Errors: []common.Error{
						common.ErrorUnauthorized("Access Denied"),
					},
					ExecutionTimeMs: time.Since(startTime).Milliseconds(),
				}, common.ErrorUnauthorized("Access Denied")
			}
			namespacesToFilter = append(namespacesToFilter, namespaces...)
		}

		query.Where.And = append(query.Where.And, QueryWhereClause{
			Binary: map[string]map[BinaryWhereClauseType]any{
				namespaceColumn: {
					In: namespacesToFilter,
				},
			},
		})
	}
	return query, QueryResponse{}, nil
}
