package query

import (
	"fmt"
	"nudgebee/services/cloud"
	"nudgebee/services/common"
	"nudgebee/services/security"
	"strconv"
	"strings"
	"time"

	"github.com/samber/lo"
)

var statsNameMap = map[string]string{
	"avg": "Average",
	"min": "Minimum",
	"max": "Maximum",
}

func convertFilterValueToStrings(value any) ([]string, error) {
	switch values := value.(type) {
	case string:
		return []string{values}, nil
	case []string:
		return values, nil
	case []any:
		result := make([]string, len(values))
		for i, value := range values {
			stringValue, ok := value.(string)
			if !ok {
				return nil, fmt.Errorf("value at index %d must be a string, got %T", i, value)
			}
			result[i] = stringValue
		}
		return result, nil
	default:
		return nil, fmt.Errorf("expected string, []string, or []any, got %T", value)
	}
}

func processAgentMetrices(query QueryRequest, context *security.RequestContext, startTime time.Time) (QueryResponse, error) {
	binary := query.Where.Binary
	if binary != nil && binary["account_id"] != nil && binary["account_id"][Eq] != nil {
		accountId := binary["account_id"][Eq]
		delete(binary, "account_id")
		if !context.GetSecurityContext().HasAccountAccess(accountId.(string), security.SecurityAccessTypeRead) {
			return QueryResponse{
				Errors: []common.Error{
					common.ErrorUnauthorized("Access Denied"),
				},
				ExecutionTimeMs: time.Since(startTime).Milliseconds(),
			}, nil
		}
		resourceIds := []string{}
		if binary["resource_id"] != nil && binary["resource_id"][In] != nil {
			var err error
			resourceIds, err = convertFilterValueToStrings(binary["resource_id"][In])
			if err != nil {
				return QueryResponse{
					Errors: []common.Error{
						common.ErrorBadRequest(fmt.Sprintf("invalid resource_id _in filter: %v", err)),
					},
					ExecutionTimeMs: time.Since(startTime).Milliseconds(),
				}, nil
			}
			delete(binary, "resource_id")
		} else if binary["resource_id"] != nil && binary["resource_id"][Eq] != nil {
			var err error
			resourceIds, err = convertFilterValueToStrings(binary["resource_id"][Eq])
			if err != nil {
				return QueryResponse{
					Errors: []common.Error{
						common.ErrorBadRequest(fmt.Sprintf("invalid resource_id _eq filter: %v", err)),
					},
					ExecutionTimeMs: time.Since(startTime).Milliseconds(),
				}, nil
			}
			delete(binary, "resource_id")
		}

		resourceType := ""
		if binary["resource_type"][Eq] != nil {
			resourceType = binary["resource_type"][Eq].(string)
			delete(binary, "resource_type")
		}

		endDate := time.Now()
		startDate := endDate.Add(-time.Hour * 24)
		if binary["timestamp"] != nil && binary["timestamp"][Gt] != nil {
			if ts, ok := binary["timestamp"]["_gt"].(string); ok {
				parsedDate, err := time.Parse(time.RFC3339, ts)
				if err == nil {
					startDate = parsedDate
				}
			}
		} else if binary["timestamp"] != nil && binary["timestamp"][Gte] != nil {
			if ts, ok := binary["timestamp"]["_gte"].(string); ok {
				parsedDate, err := time.Parse(time.RFC3339, ts)
				if err == nil {
					startDate = parsedDate
				}
			}
		}
		if binary["timestamp"] != nil && binary["timestamp"][Lt] != nil {
			if ts, ok := binary["timestamp"]["_lt"].(string); ok {
				parsedDate, err := time.Parse(time.RFC3339, ts)
				if err == nil {
					endDate = parsedDate
				}
			}
		} else if binary["timestamp"] != nil && binary["timestamp"][Lte] != nil {
			if ts, ok := binary["timestamp"]["_lte"].(string); ok {
				parsedDate, err := time.Parse(time.RFC3339, ts)
				if err == nil {
					endDate = parsedDate
				}
			}
		}

		metrics := []string{}
		if binary["metric"] != nil && binary["metric"][In] != nil {
			var err error
			metrics, err = convertFilterValueToStrings(binary["metric"][In])
			if err != nil {
				return QueryResponse{
					Errors: []common.Error{
						common.ErrorBadRequest(fmt.Sprintf("invalid metric _in filter: %v", err)),
					},
					ExecutionTimeMs: time.Since(startTime).Milliseconds(),
				}, nil
			}
			delete(binary, "metric")
		} else if binary["metric"] != nil && binary["metric"][Eq] != nil {
			var err error
			metrics, err = convertFilterValueToStrings(binary["metric"][Eq])
			if err != nil {
				return QueryResponse{
					Errors: []common.Error{
						common.ErrorBadRequest(fmt.Sprintf("invalid metric _eq filter: %v", err)),
					},
					ExecutionTimeMs: time.Since(startTime).Milliseconds(),
				}, nil
			}
			delete(binary, "metric")
		}

		regionName := "us-east-1"
		if binary["region_name"] != nil && binary["region_name"][Eq] != nil {
			regionName = binary["region_name"][Eq].(string)
		}

		serviceName := ""
		if binary["service_name"] != nil && binary["service_name"][Eq] != nil {
			serviceName = binary["service_name"][Eq].(string)
		}

		if serviceName == "" {
			return QueryResponse{
				Errors: []common.Error{
					common.ErrorBadRequest("service_name is required"),
				},
				ExecutionTimeMs: time.Since(startTime).Milliseconds(),
			}, nil
		}

		var err error
		periodUnit := "hour"
		period := 1
		timestampColumn := lo.Filter(query.Columns, func(column QueryColumn, i int) bool {
			return column.Name == "timestamp"
		})
		if len(timestampColumn) > 0 {
			timestamp := timestampColumn[0]
			if timestamp.Expr == "date_unit" && len(timestamp.Args) > 0 {
				periodUnit = timestamp.Args[0]
			}
			if timestamp.Expr == "date_unit" && len(timestamp.Args) > 1 {
				period, err = strconv.Atoi(timestamp.Args[1])
				if err == nil {
					period = 1
				}
			}
		}

		stats := []string{}
		for _, col := range query.Columns {
			if strings.HasSuffix(col.Name, "_value") {
				statsName := strings.Split(col.Name, "_value")[0]
				if updatedName, ok := statsNameMap[statsName]; ok {
					statsName = updatedName
				}
				stats = append(stats, statsName)
			}
		}

		periodDuration := time.Duration(1 * time.Hour)

		switch periodUnit {
		case "month", "M":
			periodDuration = time.Duration(period) * time.Hour * 24 * 30
		case "day", "D":
			periodDuration = time.Duration(period) * time.Hour * 24
		case "hour", "h":
			periodDuration = time.Duration(period) * time.Hour
		case "minute", "m":
			periodDuration = time.Duration(period) * time.Minute
		case "second", "s":
			periodDuration = time.Duration(period) * time.Second
		}

		resp, err := cloud.QueryMetrics(context, cloud.QueryMetricsRequest{
			AccountId: accountId.(string),
			Query: cloud.MetricsQuery{
				StartDate:    &startDate,
				EndDate:      &endDate,
				Region:       regionName,
				ServiceName:  serviceName,
				ResourceIds:  resourceIds,
				MetricNames:  metrics,
				ResourceType: resourceType,
				Statistics:   stats,
				Step:         periodDuration,
			},
		})

		if err != nil {
			return QueryResponse{
				Errors: []common.Error{
					common.ErrorInternal("Error while fetching metrics"),
				},
				ExecutionTimeMs: time.Since(startTime).Milliseconds(),
			}, nil
		}

		rows := []QueryRow{}
		for _, item := range resp.Items {
			stat := strings.ToLower(item.Statistics)
			switch stat {
			case "average":
				stat = "avg"
			case "maximum":
				stat = "max"
			case "minimum":
				stat = "min"
			}
			valueName := stat + "_value"
			values := item.Values
			for i, timestamp := range item.Timestamps {
				row := QueryRow{
					"timestamp":     timestamp,
					"metric":        item.Name,
					"tenant_id":     context.GetSecurityContext().GetTenantId(),
					"account_id":    accountId,
					valueName:       values[i],
					"resource_id":   item.ResourceId,
					"resource_type": resourceType,
					"region_name":   regionName,
					"service_name":  serviceName,
				}
				rows = append(rows, row)
			}

		}
		return QueryResponse{
			Rows:            rows,
			ExecutionTimeMs: time.Since(startTime).Milliseconds(),
		}, nil

	} else {
		return QueryResponse{
			Errors: []common.Error{
				common.ErrorBadRequest("account_id is required"),
			},
			ExecutionTimeMs: time.Since(startTime).Milliseconds(),
		}, nil

	}
}
