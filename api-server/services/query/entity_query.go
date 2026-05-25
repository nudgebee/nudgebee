package query

import "nudgebee/services/common"

type BinaryWhereClauseType string

const (
	Nq       BinaryWhereClauseType = "_neq"
	Eq       BinaryWhereClauseType = "_eq"
	Lt       BinaryWhereClauseType = "_lt"
	Gt       BinaryWhereClauseType = "_gt"
	Lte      BinaryWhereClauseType = "_lte"
	Gte      BinaryWhereClauseType = "_gte"
	In       BinaryWhereClauseType = "_in"
	NotIn    BinaryWhereClauseType = "_not_in"
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

	// Case-insensitive operators
	IContains  BinaryWhereClauseType = "_icontains"  // Case-insensitive substring match
	NIContains BinaryWhereClauseType = "_nicontains" // Negation of IContains

	// Raw regex operators (user provides regex, not SQL LIKE pattern)
	Regex  BinaryWhereClauseType = "_regex"  // Raw regex pattern matching
	NRegex BinaryWhereClauseType = "_nregex" // Negation of Regex
)

type BinaryWhereClause map[string]map[BinaryWhereClauseType]any

type QueryWhereClause struct {
	Binary BinaryWhereClause  `json:"_binary,omitempty" mapstructure:"_binary,omitempty"`
	And    []QueryWhereClause `json:"_and,omitempty" mapstructure:"_and,omitempty"`
	Or     []QueryWhereClause `json:"_or,omitempty" mapstructure:"_or,omitempty"`
	Not    *QueryWhereClause  `json:"_not,omitempty" mapstructure:"_not,omitempty"`
}

// hasFilters returns true if the where clause contains any filter conditions.
func hasFilters(w QueryWhereClause) bool {
	return len(w.Binary) > 0 || len(w.And) > 0 || len(w.Or) > 0 || w.Not != nil
}

type QuerySortOrder string

const (
	Asc            QuerySortOrder = "asc"
	Desc           QuerySortOrder = "desc"
	DescNullsLast  QuerySortOrder = "desc_nulls_last"
	DescNullsFirst QuerySortOrder = "desc_nulls_first"
	AscNullsLast   QuerySortOrder = "asc_nulls_last"
	AscNullsFirst  QuerySortOrder = "asc_nulls_first"
)

type QueryOrderBy struct {
	Column string         `json:"column,omitempty" mapstructure:"column,omitempty"`
	Order  QuerySortOrder `json:"order,omitempty" mapstructure:"order,omitempty"`
}

type QueryColumn struct {
	Name string   `json:"name,omitempty" mapstructure:"name,omitempty"`
	Expr string   `json:"expr,omitempty" mapstructure:"expr,omitempty"`
	Args []string `json:"args,omitempty" mapstructure:"args,omitempty"`
}

type QueryRequest struct {
	Table   string           `json:"table,omitempty"`
	Columns []QueryColumn    `json:"columns,omitempty" mapstructure:"columns,omitempty"`
	Where   QueryWhereClause `json:"where,omitempty" mapstructure:"where,omitempty"`
	GroupBy []string         `json:"group_by,omitempty" mapstructure:"group_by,omitempty"`
	Having  QueryWhereClause `json:"having,omitempty" mapstructure:"having,omitempty"`
	Limit   int              `json:"limit,omitempty" mapstructure:"limit,omitempty"`
	Offset  int              `json:"offset,omitempty" mapstructure:"offset,omitempty"`
	OrderBy []QueryOrderBy   `json:"order_by,omitempty" mapstructure:"order_by,omitempty"`
}

type QueryRow map[string]any
type QueryResponse struct {
	Rows            []QueryRow     `json:"rows"`
	ExecutionTimeMs int64          `json:"execution_time,omitempty"`
	Errors          []common.Error `json:"errors,omitempty"`
}
