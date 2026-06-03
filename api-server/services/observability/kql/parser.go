package kql

import (
	"github.com/alecthomas/participle/v2"
	"github.com/alecthomas/participle/v2/lexer"
)

var kqlLexer = lexer.Must(lexer.New(lexer.Rules{
	"Root": []lexer.Rule{
		{Name: "whitespace", Pattern: `\s+`},
		{Name: "Keyword", Pattern: `\b(where|project|select|order|by|sort|take|limit|summarize|extend|distinct|union|search|in|with|case|project_away|project_rename)\b`},
		{Name: "Ident", Pattern: `[a-zA-Z_][a-zA-Z0-9_]*`},
		{Name: "String", Pattern: `"(?:\\.|[^"])*"|'(?:\\.|[^'])*'`},
		{Name: "Number", Pattern: `\d+(?:\.\d+)?`},
		// CHANGED: Added negated word operators like !has, !contains, etc.
		// The order is important to ensure longer tokens (like "!has") are matched before shorter ones.
		{Name: "Operator", Pattern: `!has|!contains|!startswith|!endswith|!in|==|!=|>=|<=|&&|\|\|`},
		{Name: "Punct", Pattern: `[|()\[\],.=<>+*/-]`},
	},
}))

// KQLQuery now supports starting with a table name or a subquery.
type KQLQuery struct {
	Source     *QuerySource     `parser:"@@"`
	Operations []*PipeOperation `parser:"@@*"`
}

// QuerySource represents the start of a query.
type QuerySource struct {
	TableName *string         `parser:" @Ident"`
	Search    *SearchOperator `parser:"| 'search' @@"`
	SubQuery  *KQLQuery       `parser:"| '(' @@ ')'"`
}

// PipeOperation represents a single operation preceded by a pipe "|".
type PipeOperation struct {
	Pipe     string           `parser:"@'|'"`
	Operator *TabularOperator `parser:"@@"`
}

// TabularOperator is a union type for all possible operators.
// The dispatch logic (matching keywords) is now handled here to resolve ambiguity.
type TabularOperator struct {
	ProjectAway   *ProjectAwayOperator   `parser:"  'project_away' @@"`
	ProjectRename *ProjectRenameOperator `parser:"| 'project_rename' @@"`
	Project       *ProjectOperator       `parser:"| ('project' | 'select') @@"`
	Where         *WhereOperator         `parser:"| 'where' @@"`
	Order         *OrderOperator         `parser:"| ('order' | 'sort') 'by' @@"`
	Take          *TakeOperator          `parser:"| ('take' | 'limit') @@"`
	Summarize     *SummarizeOperator     `parser:"| 'summarize' @@"`
	Extend        *ExtendOperator        `parser:"| 'extend' @@"`
	Count         *CountOperator         `parser:"| 'count'"`
	Distinct      *DistinctOperator      `parser:"| 'distinct' @@"`
	Union         *UnionOperator         `parser:"| 'union' @@"`
	Search        *SearchOperator        `parser:"| 'search' @@"`
	ParseRegex    *ParseRegexOperator    `parser:"| 'parse' @@"`
	Parse         *ParseOperator         `parser:"| 'parse' @@"`
}

// WhereOperator represents a 'where' clause.
type WhereOperator struct {
	Expression *Expression `parser:"@@"`
}

// ProjectOperator supports calculated columns and renaming.
type ProjectOperator struct {
	Columns []*ProjectColumn `parser:"@@ (',' @@)*"`
}

// ProjectColumn can be a simple column, a renamed column, or a calculated column.
type ProjectColumn struct {
	NewName    *string               `parser:"( @Ident '=' )?"`
	Expression *ArithmeticExpression `parser:"@@"`
}

// ExtendOperator adds new calculated columns to the result set.
type ExtendOperator struct {
	Columns []*ProjectColumn `parser:"@@ (',' @@)*"`
}

// ProjectAwayOperator removes columns from the result set.
type ProjectAwayOperator struct {
	Columns []string `parser:"@Ident (',' @Ident)*"`
}

// ProjectRenameOperator renames columns.
type ProjectRenameOperator struct {
	Renames []*RenameMapping `parser:"@@ (',' @@)*"`
}

// RenameMapping represents a "NewName = OldName" pair.
type RenameMapping struct {
	NewName string `parser:"@Ident"`
	OldName string `parser:"'=' @Ident"`
}

// OrderOperator now supports multiple sort columns.
type OrderOperator struct {
	Columns []*SortColumn `parser:"@@ (',' @@)*"`
}

// SortColumn represents a single column to sort by, with an optional direction.
type SortColumn struct {
	Column    string  `parser:"@Ident"`
	Direction *string `parser:"@('asc' | 'desc')?"`
}

// TakeOperator represents a 'take' or 'limit' clause.
type TakeOperator struct {
	Count int `parser:"@Number"`
}

// SummarizeOperator represents a 'summarize' clause for aggregations.
type SummarizeOperator struct {
	Aggregations []*Aggregation `parser:"@@ (',' @@)*"`
	ByClause     *SummarizeBy   `parser:"@@?"` // Optional 'by' clause
}

// Aggregation can be a simple function call or a renamed aggregation.
type Aggregation struct {
	NewName  *string       `parser:"( @Ident '=' )?"`
	Function *FunctionCall `parser:"@@"`
}

// SummarizeBy represents the 'by' part of a summarize statement.
type SummarizeBy struct {
	Keyword string     `parser:"@'by'"`
	Columns []*Primary `parser:"@@ (',' @@)*"`
}

// CountOperator is a shorthand for 'summarize count()'.
type CountOperator struct{}

// DistinctOperator returns the unique combinations of the provided columns.
type DistinctOperator struct {
	Columns []string `parser:"@Ident (',' @Ident)*"`
}

// UnionOperator combines multiple tables.
type UnionOperator struct {
	Tables []*QuerySource `parser:"@@ (',' @@)*"`
}

// SearchOperator finds strings in one or more tables.
type SearchOperator struct {
	InClause   *SearchInClause `parser:"@@?"`
	Expression *Expression     `parser:"@@"`
}

// SearchInClause specifies the tables to search in.
type SearchInClause struct {
	Keyword string   `parser:"@'in'"`
	Tables  []string `parser:"'(' @Ident (',' @Ident)* ')'"`
}

// ParseOperator extracts structured data from a string.
type ParseRegexOperator struct {
	Kind         string `parser:"'kind'"`
	Equals       string `parser:"'='"`
	RegexKeyword string `parser:"'regex'"`
	Pattern      string `parser:"@String"`
}

// ParseOperator extracts structured data from a string.
type ParseOperator struct {
	SourceColumn *Primary         `parser:"@@"`
	WithKeyword  string           `parser:"@'with'"`
	Fragments    []*ParseFragment `parser:"@@+"`
}

// ParseFragment is a piece of a parse pattern, either a literal, a wildcard, or a capture.
type ParseFragment struct {
	Literal  *string       `parser:"@String"`
	Wildcard *string       `parser:"| @'*'"`
	Capture  *ParseCapture `parser:"| @@"`
}

// ParseCapture defines a new column to capture data into.
type ParseCapture struct {
	Name string  `parser:"@Ident"`
	Type *string `parser:"(':' @Ident)?"`
}

// -- Expression Grammar (Refactored for Robustness) --

// Expression is a series of terms linked by OR.
type Expression struct {
	Left  *AndTerm `parser:"@@"`
	Right []*OpOr  `parser:"@@*"`
}

type OpOr struct {
	Operator string   `parser:"@('or' | '||')"`
	Right    *AndTerm `parser:"@@"`
}

// AndTerm is a series of comparisons linked by AND.
type AndTerm struct {
	Left  *Comparison `parser:"@@"`
	Right []*OpAnd    `parser:"@@*"`
}

type OpAnd struct {
	Operator string      `parser:"@('and' | '&&')"`
	Right    *Comparison `parser:"@@"`
}

// A Comparison can be a parenthesized expression or a predicate.
// By putting SubExpression first, we resolve the ambiguity with parentheses.
type Comparison struct {
	SubExpression *Expression `parser:"'(' @@ ')'"`
	Predicate     *Predicate  `parser:"| @@"`
}

// A Predicate is the actual comparison operation.
type Predicate struct {
	Left *ArithmeticExpression `parser:"@@"`
	Op   *PredicateOp          `parser:"@@?"`
}

type PredicateOp struct {
	Binary *BinaryOp `parser:"@@"`
	In     *InOp     `parser:"| @@"`
}

// BinaryOp is a standard check like "Op Right".
type BinaryOp struct {
	Operator string                `parser:"@('==' | '!=' | '>=' | '<=' | '>' | '<' | 'contains' | '!contains' | 'contains_cs' | 'startswith' | '!startswith' | 'endswith' | '!endswith' | 'has' | '!has' | 'matches' 'regex')"`
	Right    *ArithmeticExpression `parser:"@@"`
}

// InOp checks if a value is within a set of literals.
type InOp struct {
	Operator string                  `parser:"@('!in' | 'in')"`
	Values   []*ArithmeticExpression `parser:"'(' @@ (',' @@ )* ')'"`
}

// -- Arithmetic Expression Grammar --

// ArithmeticExpression is a series of factors linked by "+" or "-".
type ArithmeticExpression struct {
	Left  *ArithmeticTerm     `parser:"@@"`
	Right []*OpArithmeticTerm `parser:"@@*"`
}

type OpArithmeticTerm struct {
	Operator string          `parser:"@('+' | '-')"`
	Term     *ArithmeticTerm `parser:"@@"`
}

// ArithmeticTerm is a series of primaries linked by "*" or "/".
type ArithmeticTerm struct {
	Left  *Primary              `parser:"@@"`
	Right []*OpArithmeticFactor `parser:"@@*"`
}

type OpArithmeticFactor struct {
	Operator string   `parser:"@('*' | '/')"`
	Factor   *Primary `parser:"@@"`
}

// A Primary is the base unit in an expression. It can be a sub-expression,
// a function call, a column path, or a literal value.
type Primary struct {
	SubExpression *ArithmeticExpression `parser:" '(' @@ ')'"`
	CaseFunction  *CaseFunction         `parser:"| @@"`
	FunctionCall  *FunctionCall         `parser:"| @@"`
	ColumnPath    *ColumnPath           `parser:"| @@"`
	Literal       *Value                `parser:"| @@"`
}

// ColumnPath represents a reference to a column, potentially with JSON accessors.
type ColumnPath struct {
	Base      string              `parser:"@Ident"`
	Accessors []*PropertyAccessor `parser:"@@*"`
}

// PropertyAccessor is a single step in a path, either dot or bracket notation.
type PropertyAccessor struct {
	DotProperty   *string `parser:"'.' @Ident"`
	BracketAccess *Value  `parser:"| '[' @@ ']'"`
}

// A FunctionCall has a name and a list of arguments, which can be expressions.
type FunctionCall struct {
	Name string                  `parser:"@Ident"`
	Args []*ArithmeticExpression `parser:"'(' ( @@ ( ',' @@ )* )? ')'"`
}

// CaseFunction represents the case(...) conditional function.
type CaseFunction struct {
	Keyword    string                `parser:"@'case' '('"`
	Conditions []*CasePair           `parser:"(@@)*"`
	Else       *ArithmeticExpression `parser:"@@ ')'"`
}

// CasePair is a single condition-result pair in a case function.
type CasePair struct {
	Condition *Expression           `parser:"@@ ','"`
	Result    *ArithmeticExpression `parser:"@@ ','"`
}

// A Value represents a literal value in the query.
type Value struct {
	String     *string       `parser:"@String"`
	Numeric    *NumericValue `parser:"| @@"`
	Identifier *string       `parser:"| @Ident"` // For literals like 'true', 'false', 'null'
}

// NumericValue represents either a standalone number or a timespan.
type NumericValue struct {
	Number float64 `parser:"@Number"`
	Unit   *string `parser:"@('d' | 'h' | 'm' | 's')?"`
}

func Parse(kql string) (*KQLQuery, error) {
	parser, err := participle.Build[KQLQuery](
		participle.Lexer(kqlLexer),
		participle.Unquote("String"), // Automatically unquote captured strings
	)
	if err != nil {
		return nil, err
	}
	return parser.ParseString("", kql)
}
