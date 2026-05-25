package query

import (
	"testing"
)

func TestGenerateKqlColumnExpression(t *testing.T) {
	tableDef := TableDefinition{
		Def: "MyTable",
		Columns: map[string]ColumnDefinition{
			"ts":     {Type: "datetime"},
			"name":   {Type: "string"},
			"age":    {Type: "integer"},
			"weight": {Type: "float"},
			"props":  {Type: "json"},
		},
	}

	tests := []struct {
		name     string
		colDef   ColumnDefinition
		queryCol QueryColumn
		expected string
	}{
		{
			name:     "datetime bin day",
			colDef:   tableDef.Columns["ts"],
			queryCol: QueryColumn{Name: "ts", Expr: "date_unit", Args: []string{"day"}},
			expected: "bin(ts, 1d)",
		},
		{
			name:     "datetime bin hour",
			colDef:   tableDef.Columns["ts"],
			queryCol: QueryColumn{Name: "ts", Expr: "date_unit", Args: []string{"hour"}},
			expected: "bin(ts, 1h)",
		},
		{
			name:     "string",
			colDef:   tableDef.Columns["name"],
			queryCol: QueryColumn{Name: "name"},
			expected: "tostring(name)",
		},
		{
			name:     "json",
			colDef:   tableDef.Columns["props"],
			queryCol: QueryColumn{Name: "props"},
			expected: "tostring(props)",
		},
		{
			name:     "float",
			colDef:   tableDef.Columns["weight"],
			queryCol: QueryColumn{Name: "weight"},
			expected: "todouble(weight)",
		},
		{
			name:     "integer",
			colDef:   tableDef.Columns["age"],
			queryCol: QueryColumn{Name: "age"},
			expected: "toint(age)",
		},
	}

	for _, tt := range tests {
		KqlGenerator := KqlGenerator{}
		got := KqlGenerator.generateKqlColumnExpression(tt.colDef, tt.queryCol, tableDef, false)
		if got != tt.expected {
			t.Errorf("%s: got %s, want %s", tt.name, got, tt.expected)
		}
	}
}

func TestGenerateKqlSelectClause(t *testing.T) {
	KqlGenerator := KqlGenerator{}
	tableDef := TableDefinition{
		Def: "MyTable",
		Columns: map[string]ColumnDefinition{
			"id":   {Type: "integer"},
			"name": {Type: "string"},
		},
	}

	req := QueryRequest{
		Columns: []QueryColumn{
			{Name: "id"},
			{Name: "name"},
		},
	}

	got, err := KqlGenerator.generateKqlSelectClause(req, tableDef)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "project id = toint(id), name = tostring(name)"
	if got != want {
		t.Errorf("SelectClause: got %s, want %s", got, want)
	}
}

func TestGenerateKqlWhereClause(t *testing.T) {
	KqlGenerator := KqlGenerator{}
	tableDef := TableDefinition{
		Def: "MyTable",
		Columns: map[string]ColumnDefinition{
			"name":   {Type: "string"},
			"age":    {Type: "integer"},
			"gender": {Type: "string"},
		},
	}

	where := QueryWhereClause{
		Binary: map[string]map[BinaryWhereClauseType]any{
			"name": {
				Like: "abc",
			},
			"age": {
				Eq: 30,
				Nq: 40,
				In: []string{"20", "25"},
			},
			"gender": {
				In: []string{},
			},
		},
	}

	got, err := KqlGenerator.generateKqlWhereClause(where, tableDef)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "where name contains 'abc' and age == '30' and age != '40' and age in ('20','25')"
	if got != want {
		t.Errorf("WhereClause: got %s, want %s", got, want)
	}
}

// func TestGenerateKqlGroupClause(t *testing.T) {
// 	KqlGenerator := KqlGenerator{}
// 	tableDef := TableDefinition{
// 		Def: "MyTable",
// 	}
// 	req := QueryRequest{
// 		GroupBy: []string{"name", "age"},
// 	}

// 	got, err := KqlGenerator.generateKqlGroupClause(req, tableDef)
// 	if err != nil {
// 		t.Fatalf("unexpected error: %v", err)
// 	}
// 	want := "summarize count() by name, age"
// 	if got != want {
// 		t.Errorf("GroupClause: got %s, want %s", got, want)
// 	}
// }

func TestGenerateKqlOrderClause(t *testing.T) {
	KqlGenerator := KqlGenerator{}
	req := QueryRequest{
		OrderBy: []QueryOrderBy{
			{Column: "name", Order: Asc},
			{Column: "age", Order: Desc},
		},
	}

	got := KqlGenerator.generateKqlOrderClause(req, TableDefinition{})
	want := "order by name asc, age desc"
	if got != want {
		t.Errorf("OrderClause: got %s, want %s", got, want)
	}
}

func TestGenerateKqlQuery(t *testing.T) {
	KqlGenerator := KqlGenerator{}
	tableDef := TableDefinition{
		Def: "MyTable",
		Columns: map[string]ColumnDefinition{
			"id":   {Type: "integer"},
			"name": {Type: "string"},
		},
	}

	req := QueryRequest{
		Columns: []QueryColumn{{Name: "id"}, {Name: "name"}},
		Where: QueryWhereClause{
			Binary: map[string]map[BinaryWhereClauseType]any{
				"id": {
					Eq: 10,
				},
			},
		},
		GroupBy: []string{"name"},
		OrderBy: []QueryOrderBy{{Column: "id", Order: Desc}},
	}

	got, err := KqlGenerator.GenerateKqlQuery(req, tableDef)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := "MyTable | where id == '10' | summarize count() by name | project id = toint(id), name = tostring(name) | order by id desc"

	if got != want {
		t.Errorf("FullQuery: got %s, want %s", got, want)
	}
}
