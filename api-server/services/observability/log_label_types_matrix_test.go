package observability

import (
	"fmt"
	"strings"
	"testing"

	"nudgebee/services/query"
	"nudgebee/services/security"
)

// This file exhaustively crosses every operator a provider advertises with every
// normalized data type, for Pinot and Loki. It exists because the ES PPL formatter was
// found quoting int64 only after Pinot and Hive had already been fixed — a gap that a
// per-case test could not surface but a full cross-product does.
//
// Each cell asserts two independent things:
//
//	1. VALIDATION — does applyLabelDataTypes accept the (operator, type) pair, and does
//	   that verdict match query.OperatorAppliesToType? The two must never disagree, or
//	   the UI menu and the backend guard drift apart.
//	2. RENDERING — for every accepted pair, does the provider's own query builder
//	   produce a query without erroring, with values rendered per the column's type?

// typeFixtures gives a representative value per normalized data type, in the shape the
// UI actually sends it: always a string.
var typeFixtures = []struct {
	dataType   string
	attributes map[string]any // what the provider reports for such a column
	value      string
	listValue  []any
}{
	{query.LabelTypeString, map[string]any{"dataType": "STRING"}, "api-xyz", []any{"a", "b"}},
	{query.LabelTypeNumber, map[string]any{"dataType": "INT"}, "500", []any{"1", "2"}},
	{query.LabelTypeBool, map[string]any{"dataType": "BOOLEAN"}, "true", []any{"true", "false"}},
	{query.LabelTypeTimestamp, map[string]any{"dataType": "LONG", "fieldType": "dateTime"}, "1786230607140", []any{"1", "2"}},
	{query.LabelTypeUnknown, map[string]any{}, "anything", []any{"a", "b"}},
}

// operatorFixture supplies the value shape each operator expects.
func operatorFixture(op string, scalar string, list []any) any {
	switch query.BinaryWhereClauseType(op) {
	case query.In, query.NotIn, query.Between:
		return list
	case query.IsNull, query.HasKey:
		return true
	default:
		return scalar
	}
}

// TestOperatorDataTypeMatrix crosses provider × operator × data type.
func TestOperatorDataTypeMatrix(t *testing.T) {
	providers := []struct {
		name string
		// render turns a validated where clause into the provider's own query, so a
		// renderer that mishandles a coerced value shows up as an error or a quoted
		// literal rather than passing silently.
		render func(query.QueryWhereClause) (string, error)
	}{
		{
			name:   "pinot",
			render: buildPinotWhereClause,
		},
		{
			name: "loki",
			render: func(w query.QueryWhereClause) (string, error) {
				s := &LokiSource{}
				return s.BuildLokiQuery(LogsQueryBuilderRequest{Where: w})
			},
		},
	}

	// Built once: constructing a request context costs ~880ms, while the code under
	// test takes ~250µs. Per-cell construction made this test 100s instead of <1s.
	ctx := labelTypeTestContext()

	for _, provider := range providers {
		source := providerSourceFor(t, provider.name)
		for _, op := range source.GetSupportedOperators() {
			for _, fixture := range typeFixtures {
				name := fmt.Sprintf("%s/%s/%s", provider.name, strings.TrimPrefix(op, "_"), fixture.dataType)
				t.Run(name, func(t *testing.T) {
					assertMatrixCell(t, ctx, provider.name, provider.render, source, op, fixture.dataType, fixture.attributes,
						operatorFixture(op, fixture.value, fixture.listValue))
				})
			}
		}
	}
}

// providerSourceFor returns the real LogSource so the operator list under test is the
// one the provider actually advertises, not a copy that could drift.
func providerSourceFor(t *testing.T, name string) LogSource {
	t.Helper()
	switch name {
	case "pinot":
		return &PinotSource{}
	case "loki":
		return &LokiSource{}
	default:
		t.Fatalf("unknown provider %q", name)
		return nil
	}
}

func assertMatrixCell(
	t *testing.T,
	ctx *security.RequestContext,
	providerName string,
	render func(query.QueryWhereClause) (string, error),
	source LogSource,
	op string,
	dataType string,
	attributes map[string]any,
	value any,
) {
	t.Helper()

	const field = "col"
	stub := &stubLogSource{
		labels:    []OutputLogLabel{{Label: field, Attributes: attributes}},
		operators: source.GetSupportedOperators(),
	}
	request := FetchLogRequest{
		// Unique per cell so the 10-minute type cache never leaks across cells.
		AccountId: fmt.Sprintf("acct-matrix-%s-%s-%s", providerName, op, dataType),
		QueryRequest: LogsQueryBuilderRequest{
			Where: query.QueryWhereClause{
				Binary: query.BinaryWhereClause{field: {query.BinaryWhereClauseType(op): value}},
			},
		},
	}

	err := applyLabelDataTypes(ctx, stub, &request)

	// 1. The verdict must match the shared matrix exactly. Anything else means the
	//    backend guard and the UI menu would disagree for this cell.
	wantAllowed := query.OperatorAppliesToType(op, dataType)
	gotAllowed := err == nil
	if gotAllowed != wantAllowed {
		t.Fatalf("validation allowed=%v, matrix says %v (err=%v)", gotAllowed, wantAllowed, err)
	}
	if !wantAllowed {
		// Rejections must name the operator, the label and the type — this message is
		// what an API caller (or the LLM agent) rewrites its query from.
		for _, want := range []string{field, dataType} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("rejection %q does not mention %q", err.Error(), want)
			}
		}
		return
	}

	// 2. An accepted pair must actually render. A renderer lacking a case for a coerced
	//    type either errors here or silently re-quotes the literal.
	rendered, renderErr := render(request.QueryRequest.Where)
	if renderErr != nil {
		t.Fatalf("validation accepted the pair but %s could not render it: %v", providerName, renderErr)
	}
	if strings.TrimSpace(rendered) == "" {
		t.Fatalf("validation accepted the pair but %s rendered an empty query", providerName)
	}

	assertValueRendering(t, providerName, rendered, dataType, op)
}

// assertValueRendering checks the literal is written the way the column's type requires.
// Only Pinot is checked: LogQL has no typed literals — every Loki label value is a quoted
// string by design, so "bare vs quoted" is not a meaningful assertion there.
func assertValueRendering(t *testing.T, providerName, rendered, dataType, op string) {
	t.Helper()
	if providerName != "pinot" {
		return
	}
	switch query.BinaryWhereClauseType(op) {
	case query.Eq, query.Nq, query.Lt, query.Lte, query.Gt, query.Gte:
	default:
		return // only scalar comparisons carry a coercible literal
	}

	switch dataType {
	case query.LabelTypeNumber, query.LabelTypeTimestamp:
		if strings.Contains(rendered, "'") {
			t.Errorf("numeric column rendered a quoted literal (the bug this fixes): %s", rendered)
		}
	case query.LabelTypeBool:
		if strings.Contains(rendered, "'") {
			t.Errorf("boolean column rendered a quoted literal: %s", rendered)
		}
	case query.LabelTypeString, query.LabelTypeUnknown:
		if !strings.Contains(rendered, "'") {
			t.Errorf("string/unknown column must stay quoted, got: %s", rendered)
		}
	}
}
