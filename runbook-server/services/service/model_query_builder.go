package service

import (
	"fmt"
	"nudgebee/runbook/common"
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
					w, err := queryBuilderFixWhereClause(subClause.(map[string]any))
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
func BuildLogQueryBuilder(input string) (QueryBuilder, error) {

	queryObject := QueryBuilder{}

	//rewrite query
	serializedData := map[string]any{}
	err := common.UnmarshalJson([]byte(input), &serializedData)
	if err != nil {
		return queryObject, err
	}

	if serializedData["where"] != nil {
		whereMap, err := queryBuilderFixWhereClause(serializedData["where"].(map[string]any))
		if err != nil {
			return queryObject, err
		}
		err = common.DecodeMapToStruct(whereMap, &queryObject.Where)
		if err != nil {
			return queryObject, err
		}
	}

	if serializedData["start_time"] != nil {
		queryObject.StartTime = serializedData["start_time"].(string)
	}

	if serializedData["end_time"] != nil {
		queryObject.StartTime = serializedData["end_time"].(string)
	}

	if serializedData["time_range"] != nil {
		queryObject.TimeRange = serializedData["time_range"].(string)
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

	return queryObject, err

}

func BuildTraceQueryBuilder(input string) (TraceQueryBuilder, time.Time, time.Time, error) {

	queryObject := TraceQueryBuilder{}
	startTime := time.Now().UTC()
	endTime := startTime.Add(-1 * time.Hour)

	//rewrite query
	serializedData := map[string]any{}
	err := common.UnmarshalJson([]byte(input), &serializedData)
	if err != nil {
		return queryObject, startTime, endTime, err
	}

	if serializedData["where"] != nil {
		whereMap, err := queryBuilderFixWhereClause(serializedData["where"].(map[string]any))
		if err != nil {
			return queryObject, startTime, endTime, err
		}
		err = common.DecodeMapToStruct(whereMap, &queryObject.Where)
		if err != nil {
			return queryObject, startTime, endTime, err
		}
	}
	if serializedData["start_time"] != nil {
		startTime, err = time.Parse(time.RFC3339, serializedData["start_time"].(string))
		if err != nil {
			return queryObject, startTime, endTime, err
		}
	}

	if serializedData["end_time"] != nil {
		endTime, err = time.Parse(time.RFC3339, serializedData["end_time"].(string))
		if err != nil {
			return queryObject, startTime, endTime, err
		}
	}

	// if serializedData["time_range"] != nil {
	// 	queryObject.TimeRange = serializedData["time_range"].(string)
	// }

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
