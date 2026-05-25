package core

import (
	"fmt"
	"nudgebee/llm/common"
	"reflect"
	"strconv"
	"time"
)

type QueryBuilder struct {
	Where     QueryWhereClause `json:"where,omitempty"`
	StartTime string           `json:"start_time,omitempty"`
	EndTime   string           `json:"end_time,omitempty"`
	TimeRange string           `json:"time_range,omitempty"`
	Limit     int              `json:"limit,omitempty"`
	Offset    int              `json:"offset,omitempty"`
	Index     string           `json:"index,omitempty"`
}

type QueryWhereClause struct {
	Binary BinaryWhereClause  `json:"_binary,omitempty" mapstructure:"_binary,omitempty"`
	And    []QueryWhereClause `json:"_and,omitempty" mapstructure:"_and,omitempty"`
	Or     []QueryWhereClause `json:"_or,omitempty" mapstructure:"_or,omitempty"`
	Not    *QueryWhereClause  `json:"_not,omitempty" mapstructure:"_not,omitempty"`
}

type BinaryWhereClause map[string]map[BinaryWhereClauseType]any
type BinaryWhereClauseType string

const (
	Nq       BinaryWhereClauseType = "_neq"
	Eq       BinaryWhereClauseType = "_eq"
	Lt       BinaryWhereClauseType = "_lt"
	Gt       BinaryWhereClauseType = "_gt"
	Lte      BinaryWhereClauseType = "_lte"
	Gte      BinaryWhereClauseType = "_gte"
	In       BinaryWhereClauseType = "_in"
	NotIn    BinaryWhereClauseType = "_nin"
	Like     BinaryWhereClauseType = "_like"
	Between  BinaryWhereClauseType = "_between"
	Contains BinaryWhereClauseType = "_contains"
	ILike    BinaryWhereClauseType = "_ilike"
	HasKey   BinaryWhereClauseType = "_has_key"
	IsNull   BinaryWhereClauseType = "_is_null"

	NqF    BinaryWhereClauseType = "_neq_f"
	EqF    BinaryWhereClauseType = "_eq_f"
	LtF    BinaryWhereClauseType = "_lt_f"
	GtF    BinaryWhereClauseType = "_gt_f"
	LteF   BinaryWhereClauseType = "_lte_f"
	GteF   BinaryWhereClauseType = "_gte_f"
	LikeF  BinaryWhereClauseType = "_like_f"
	ILikeF BinaryWhereClauseType = "_ilike_f"
	NLike  BinaryWhereClauseType = "_nlike"
)

func queryBuilderFixWhereClause(whereMap map[string]any) (map[string]any, error) {
	whereMap2 := make(map[string]any)
	binaryMap := make(map[string]any)
	for k, v := range whereMap {
		if k == "_and" || k == "_or" || k == "_not" {
			compositeClause := []any{}
			if reflect.TypeOf(v).Kind() == reflect.Slice {
				for _, subClause := range v.([]any) {
					subMap, ok := subClause.(map[string]any)
					if !ok {
						continue // Skip non-map elements in composite clause
					}
					w, err := queryBuilderFixWhereClause(subMap)
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
func BuildLogQueryBuilder(nbRequestContext NbToolContext, input string) (QueryBuilder, error) {

	queryObject := QueryBuilder{}

	//rewrite query
	serializedData := map[string]any{}
	err := common.UnmarshalJson([]byte(input), &serializedData)
	if err != nil {
		return queryObject, err
	}

	// Unwrap "command" key: LLM sometimes nests the query inside
	// {"command": {"where": ...}, "start_time": "..."}.
	// Merge command contents into serializedData so "where" is found at the top level.
	if cmdVal, cmdOk := serializedData["command"]; cmdOk {
		if _, whereOk := serializedData["where"]; !whereOk {
			if cmdMap, ok := cmdVal.(map[string]any); ok {
				for k, v := range cmdMap {
					if _, exists := serializedData[k]; !exists {
						serializedData[k] = v
					}
				}
				delete(serializedData, "command")
			}
		}
	}

	if v := serializedData["where"]; v != nil {
		whereMap, ok := v.(map[string]any)
		if ok {
			fixedWhere, err := queryBuilderFixWhereClause(whereMap)
			if err != nil {
				return queryObject, err
			}
			err = common.DecodeMapToStruct(fixedWhere, &queryObject.Where)
			if err != nil {
				return queryObject, err
			}
		}
	}

	if v := serializedData["start_time"]; v != nil {
		if s, ok := v.(string); ok {
			queryObject.StartTime = s
		}
	}

	if v := serializedData["end_time"]; v != nil {
		if s, ok := v.(string); ok {
			queryObject.EndTime = s
		}
	}

	if serializedData["range"] != nil {
		switch rangeValue := serializedData["range"].(type) {
		case string:
			queryObject.TimeRange = rangeValue
		case map[string]any:
			if last, ok := rangeValue["_last"]; ok {
				if lastStr, ok := last.(string); ok {
					queryObject.TimeRange = lastStr
				}
			}
		}
	} else if serializedData["time_range"] != nil {
		switch rangeValue := serializedData["time_range"].(type) {
		case string:
			queryObject.TimeRange = rangeValue
		case map[string]any:
			if last, ok := rangeValue["_last"]; ok {
				if lastStr, ok := last.(string); ok {
					queryObject.TimeRange = lastStr
				}
			}
		}
	}

	if serializedData["limit"] != nil {
		switch limitValue := serializedData["limit"].(type) {
		case string:
			queryObject.Limit, err = strconv.Atoi(limitValue)
			if err != nil {
				return queryObject, err
			}
		case float64:
			queryObject.Limit = int(limitValue)
		case int:
			queryObject.Limit = limitValue
		case int64:
			queryObject.Limit = int(limitValue)
		}
	}

	if serializedData["offset"] != nil {
		switch offsetValue := serializedData["offset"].(type) {
		case string:
			queryObject.Offset, err = strconv.Atoi(offsetValue)
			if err != nil {
				return queryObject, err
			}
		case float64:
			queryObject.Offset = int(offsetValue)
		case int:
			queryObject.Offset = offsetValue
		case int64:
			queryObject.Offset = int(offsetValue)
		}
	}

	if s, ok := serializedData["index"].(string); ok && s != "" {
		queryObject.Index = s
	}

	return queryObject, err

}

func BuildTraceQueryBuilder(nbRequestContext NbToolContext, input string) (TraceQueryBuilder, time.Time, time.Time, error) {

	queryObject := TraceQueryBuilder{}
	endTime := time.Now().UTC()
	startTime := endTime.Add(-1 * time.Hour)

	//rewrite query
	serializedData := map[string]any{}
	err := common.UnmarshalJson([]byte(input), &serializedData)
	if err != nil {
		return queryObject, startTime, endTime, err
	}

	if v := serializedData["where"]; v != nil {
		whereMap, ok := v.(map[string]any)
		if ok {
			fixedWhere, err := queryBuilderFixWhereClause(whereMap)
			if err != nil {
				return queryObject, startTime, endTime, err
			}
			err = common.DecodeMapToStruct(fixedWhere, &queryObject.Where)
			if err != nil {
				return queryObject, startTime, endTime, err
			}
		}
	}
	if v := serializedData["start_time"]; v != nil {
		if s, ok := v.(string); ok {
			startTime, err = time.Parse(time.RFC3339, s)
			if err != nil {
				return queryObject, startTime, endTime, err
			}
		}
	}

	if v := serializedData["end_time"]; v != nil {
		if s, ok := v.(string); ok {
			endTime, err = time.Parse(time.RFC3339, s)
			if err != nil {
				return queryObject, startTime, endTime, err
			}
		}
	}

	if serializedData["limit"] != nil {
		switch limitValue := serializedData["limit"].(type) {
		case string:
			queryObject.Limit, err = strconv.Atoi(limitValue)
			if err != nil {
				return queryObject, startTime, endTime, err
			}
		case float64:
			queryObject.Limit = int(limitValue)
		case int:
			queryObject.Limit = limitValue
		case int64:
			queryObject.Limit = int(limitValue)
		}
	}

	if serializedData["offset"] != nil {
		switch offsetValue := serializedData["offset"].(type) {
		case string:
			queryObject.Offset, err = strconv.Atoi(offsetValue)
			if err != nil {
				return queryObject, startTime, endTime, err
			}
		case float64:
			queryObject.Offset = int(offsetValue)
		case int:
			queryObject.Offset = offsetValue
		case int64:
			queryObject.Offset = int(offsetValue)
		}
	}

	return queryObject, startTime, endTime, err

}
