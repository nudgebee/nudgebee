package observability

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"nudgebee/services/query"
	"nudgebee/services/security"
)

// TestNormalizeLabelDataType covers every provider vocabulary the log path can
// return. The attribute shapes are copied from what each source actually emits —
// see pinot.parsePinotSchemaBytes, hive_saas.QueryLabels,
// signoz_saas.convertSigNozLogLabels, observe.QueryLabels,
// azure_app_insights.QueryLabels and the two ES field shapes.
func TestNormalizeLabelDataType(t *testing.T) {
	tests := []struct {
		name  string
		attrs map[string]any
		want  string
	}{
		// Pinot — {"dataType": ..., "fieldType": ...}
		{"pinot STRING", map[string]any{"dataType": "STRING", "fieldType": "dimension"}, query.LabelTypeString},
		{"pinot INT", map[string]any{"dataType": "INT", "fieldType": "metric"}, query.LabelTypeNumber},
		{"pinot LONG", map[string]any{"dataType": "LONG"}, query.LabelTypeNumber},
		{"pinot DOUBLE", map[string]any{"dataType": "DOUBLE"}, query.LabelTypeNumber},
		{"pinot BOOLEAN", map[string]any{"dataType": "BOOLEAN"}, query.LabelTypeBool},
		{"pinot TIMESTAMP", map[string]any{"dataType": "TIMESTAMP", "fieldType": "dateTime"}, query.LabelTypeTimestamp},
		// Pinot stores epoch-millis time columns as LONG and distinguishes them only
		// by fieldType. Reading dataType alone would report a date column as a plain
		// number — which is what the ts column on a real Pinot table looks like.
		{"pinot epoch-millis time column is a timestamp, not a number", map[string]any{"dataType": "LONG", "fieldType": "dateTime", "format": "1:MILLISECONDS:EPOCH"}, query.LabelTypeTimestamp},
		{"pinot LONG metric stays a number", map[string]any{"dataType": "LONG", "fieldType": "metric"}, query.LabelTypeNumber},

		// Hive — {"dataType": ..., optional "isPartition"}
		{"hive string", map[string]any{"dataType": "string"}, query.LabelTypeString},
		{"hive bigint", map[string]any{"dataType": "bigint"}, query.LabelTypeNumber},
		{"hive timestamp", map[string]any{"dataType": "timestamp", "isPartition": true}, query.LabelTypeTimestamp},
		{"hive parameterized varchar", map[string]any{"dataType": "varchar(255)"}, query.LabelTypeString},
		{"hive parameterized decimal", map[string]any{"dataType": "decimal(10,2)"}, query.LabelTypeNumber},

		// Signoz — sets BOTH dataType (the type) and type (tag/resource scope).
		// dataType must win; "resource" is not a data type.
		{"signoz string tag", map[string]any{"dataType": "string", "type": "tag", "isColumn": false}, query.LabelTypeString},
		{"signoz int64 resource", map[string]any{"dataType": "int64", "type": "resource"}, query.LabelTypeNumber},
		{"signoz float64", map[string]any{"dataType": "float64", "type": "tag"}, query.LabelTypeNumber},
		{"signoz bool", map[string]any{"dataType": "bool", "type": "tag"}, query.LabelTypeBool},

		// Elasticsearch — SaaS _mapping shape {"type": "long"}
		{"es mapping long", map[string]any{"type": "long"}, query.LabelTypeNumber},
		{"es mapping keyword", map[string]any{"type": "keyword"}, query.LabelTypeString},
		{"es mapping date", map[string]any{"type": "date"}, query.LabelTypeTimestamp},
		{"es mapping boolean", map[string]any{"type": "boolean"}, query.LabelTypeBool},

		// Elasticsearch — agent _field_caps shape, where the type IS the key
		{"es field_caps text", map[string]any{"text": map[string]any{"searchable": true}}, query.LabelTypeString},
		{"es field_caps long", map[string]any{"long": map[string]any{"aggregatable": true}}, query.LabelTypeNumber},

		// Azure App Insights / Observe
		{"azure int", map[string]any{"type": "int"}, query.LabelTypeNumber},
		{"azure datetime", map[string]any{"type": "datetime"}, query.LabelTypeTimestamp},
		{"observe datatype", map[string]any{"datatype": "string"}, query.LabelTypeString},

		// Fail-open cases: these must never narrow anything.
		{"untyped provider (loki/splunk/loggly/dynatrace)", map[string]any{}, query.LabelTypeUnknown},
		{"nil attributes", nil, query.LabelTypeUnknown},
		{"unrecognized vocabulary", map[string]any{"dataType": "SOME_FUTURE_TYPE"}, query.LabelTypeUnknown},
		{"structured column is not narrowed", map[string]any{"dataType": "json"}, query.LabelTypeUnknown},
		{"empty type string", map[string]any{"dataType": ""}, query.LabelTypeUnknown},
		{"scope-only attribute is not a data type", map[string]any{"type": "resource"}, query.LabelTypeUnknown},

		// The _field_caps fallback keys off the ATTRIBUTE NAME, so it must only fire
		// for that shape (type name → detail object). A flat attribute that merely
		// happens to be named after a type would otherwise mistype the label and
		// withhold operators that are actually valid.
		{"flat string attr named after a type is not field_caps", map[string]any{"real": "yes"}, query.LabelTypeUnknown},
		{"flat bool attr named after a type is not field_caps", map[string]any{"double": true}, query.LabelTypeUnknown},
		{"flat numeric attr named after a type is not field_caps", map[string]any{"long": 3}, query.LabelTypeUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeLabelDataType(tt.attrs); got != tt.want {
				t.Errorf("normalizeLabelDataType(%v) = %q, want %q", tt.attrs, got, tt.want)
			}
		})
	}
}

// TestLabelsFromIndexFields covers the Elasticsearch fetch_index path. ES is the
// only provider whose per-field types live behind QueryIndexFields rather than
// QueryLabels (which returns index NAMES), so if this conversion skipped
// normalization, ES would silently get no type-aware operators at all.
func TestLabelsFromIndexFields(t *testing.T) {
	labels := LabelsFromIndexFields([]OutputLogLabelFields{
		{Field: "kubernetes.pod_name", Attributes: map[string]any{"type": "keyword"}},
		{Field: "http.status_code", Attributes: map[string]any{"type": "long"}},
		{Field: "@timestamp", Attributes: map[string]any{"type": "date"}},
		// Agent _field_caps shape, where the type is the key.
		{Field: "message", Attributes: map[string]any{"text": map[string]any{"searchable": true}}},
		{Field: "untyped", Attributes: map[string]any{}},
	})

	want := []struct {
		label    string
		dataType string
	}{
		{"kubernetes.pod_name", query.LabelTypeString},
		{"http.status_code", query.LabelTypeNumber},
		{"@timestamp", query.LabelTypeTimestamp},
		{"message", query.LabelTypeString},
		{"untyped", query.LabelTypeUnknown},
	}
	if len(labels) != len(want) {
		t.Fatalf("expected %d labels, got %d", len(want), len(labels))
	}
	for i, w := range want {
		if labels[i].Label != w.label {
			t.Errorf("label[%d] = %q, want %q", i, labels[i].Label, w.label)
		}
		if labels[i].DataType != w.dataType {
			t.Errorf("label %q has DataType %q, want %q", labels[i].Label, labels[i].DataType, w.dataType)
		}
		// Raw attributes must survive: the UI still forwards provider-specific keys
		// from them when fetching that label's values.
		if labels[i].Attributes == nil {
			t.Errorf("label %q lost its raw Attributes", labels[i].Label)
		}
	}
}

// stubLogSource is a minimal LogSource for validator tests. Only QueryLabels and
// GetSupportedOperators are exercised; the rest satisfy the interface.
type stubLogSource struct {
	labels     []OutputLogLabel
	labelsErr  error
	operators  []string
	labelCalls int
}

func (s *stubLogSource) QueryLogs(*security.RequestContext, FetchLogRequest) ([]OutputLog, error) {
	return nil, nil
}
func (s *stubLogSource) QueryLabels(_ *security.RequestContext, _ FetchLogLabelRequest) ([]OutputLogLabel, error) {
	s.labelCalls++
	return s.labels, s.labelsErr
}
func (s *stubLogSource) QueryLabelValues(*security.RequestContext, FetchLogLabelValuesRequest) ([]OutputLogLabelValue, error) {
	return nil, nil
}
func (s *stubLogSource) GetQuery(*security.RequestContext, FetchLogRequest) (string, error) {
	return "", nil
}
func (s *stubLogSource) GetLabelMapping() map[string]string { return map[string]string{} }
func (s *stubLogSource) GetSupportedOperators() []string {
	if s.operators != nil {
		return s.operators
	}
	// The set Pinot/Hive advertise — the providers this bug reproduces on.
	return []string{"_eq", "_neq", "_contains", "_regex", "_nregex", "_is_null", "_gt", "_lt", "_gte", "_lte", "_like", "_nlike"}
}

// stubOverrideSource is a stubLogSource that also declares an operator↔type override.
type stubOverrideSource struct {
	stubLogSource
	overrides map[string][]string
}

func (s *stubOverrideSource) GetOperatorDataTypes() map[string][]string { return s.overrides }

func labelTypeTestContext() *security.RequestContext {
	return security.NewRequestContextForTenantAdmin("test-tenant", nil, nil, nil)
}

// typedLabels is the label set used across validator tests: one string column, one
// numeric column, one column the normalizer cannot type.
func typedLabels() []OutputLogLabel {
	return []OutputLogLabel{
		{Label: "pod", Attributes: map[string]any{"dataType": "STRING"}},
		{Label: "status_code", Attributes: map[string]any{"dataType": "INT"}},
		{Label: "payload", Attributes: map[string]any{"dataType": "SOME_FUTURE_TYPE"}},
	}
}

func whereWith(field, op string, value any) FetchLogRequest {
	return FetchLogRequest{
		AccountId: "acct-validator-test",
		QueryRequest: LogsQueryBuilderRequest{
			Where: query.QueryWhereClause{
				Binary: query.BinaryWhereClause{
					field: {query.BinaryWhereClauseType(op): value},
				},
			},
		},
	}
}

func TestValidateOperatorDataTypes(t *testing.T) {
	tests := []struct {
		name        string
		request     FetchLogRequest
		wantErr     bool
		wantContain []string
	}{
		{
			name:        "regex on a numeric column is rejected — the reported bug",
			request:     whereWith("status_code", "_regex", "5.."),
			wantErr:     true,
			wantContain: []string{"=~", "status_code", "number"},
		},
		{
			name:        "contains on a numeric column is rejected — same failure as regex",
			request:     whereWith("status_code", "_contains", "5"),
			wantErr:     true,
			wantContain: []string{"contains", "status_code", "number"},
		},
		{
			name:    "regex on a string column is allowed",
			request: whereWith("pod", "_regex", "api-.*"),
		},
		{
			name:    "equality on a numeric column is allowed",
			request: whereWith("status_code", "_eq", "500"),
		},
		{
			name:    "ordering on a numeric column is allowed",
			request: whereWith("status_code", "_gt", "499"),
		},
		{
			name:    "regex on an untypeable column fails open",
			request: whereWith("payload", "_regex", "5.."),
		},
		{
			name:    "regex on a label the provider never reported fails open",
			request: whereWith("not_a_known_label", "_regex", "5.."),
		},
		{
			name:    "line-content filters are never type-checked",
			request: whereWith("message", "_regex", "5.."),
		},
		{
			name:    "empty where clause is allowed",
			request: FetchLogRequest{AccountId: "acct-validator-test"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := &stubLogSource{labels: typedLabels()}
			// Distinct account per case so the 10-minute type cache cannot leak
			// between subtests.
			tt.request.AccountId = "acct-" + strings.ReplaceAll(tt.name, " ", "-")

			err := applyLabelDataTypes(labelTypeTestContext(), source, &tt.request)

			if !tt.wantErr {
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected an error, got nil")
			}
			assertActionableTypeError(t, err, tt.wantContain)
		})
	}
}

// assertActionableTypeError checks the rejection message names the offending pair
// AND suggests only operators that actually work for that type — the caller (notably
// the LLM agent) rewrites its query straight from this list, so a suggestion that is
// itself invalid would send it into a second failed attempt.
func assertActionableTypeError(t *testing.T, err error, wantContain []string) {
	t.Helper()
	for _, want := range wantContain {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err.Error(), want)
		}
	}

	_, suggestions, found := strings.Cut(err.Error(), "valid operators:")
	if !found {
		t.Fatalf("error %q does not suggest valid operators", err.Error())
	}
	// These are string-only, so they must never be offered for a numeric label.
	for _, banned := range []string{"contains", "LIKE", "=~", "!~"} {
		if strings.Contains(suggestions, banned) {
			t.Errorf("error suggests %q, which is invalid for a numeric label: %q", banned, err.Error())
		}
	}
}

// TestValidateOperatorDataTypesNestedClause proves the walk reaches conditions
// nested under _and / _or / _not, not just the top-level binary map.
func TestValidateOperatorDataTypesNestedClause(t *testing.T) {
	source := &stubLogSource{labels: typedLabels()}
	request := FetchLogRequest{
		AccountId: "acct-nested",
		QueryRequest: LogsQueryBuilderRequest{
			Where: query.QueryWhereClause{
				And: []query.QueryWhereClause{
					{Binary: query.BinaryWhereClause{"pod": {query.Eq: "api"}}},
					{Or: []query.QueryWhereClause{
						{Binary: query.BinaryWhereClause{"status_code": {query.Regex: "5.."}}},
					}},
				},
			},
		},
	}

	err := applyLabelDataTypes(labelTypeTestContext(), source, &request)
	if err == nil {
		t.Fatal("expected the nested regex-on-number condition to be rejected")
	}
	if !strings.Contains(err.Error(), "status_code") {
		t.Errorf("error %q does not name the offending nested field", err.Error())
	}
}

// TestValidateOperatorDataTypesFailsOpenOnDiscoveryError is the safety property: a
// label-discovery failure must never block a log query that would otherwise run.
func TestValidateOperatorDataTypesFailsOpenOnDiscoveryError(t *testing.T) {
	for _, tc := range []struct {
		name   string
		source *stubLogSource
	}{
		{"discovery errors", &stubLogSource{labelsErr: errors.New("backend unreachable")}},
		{"provider reports no labels", &stubLogSource{labels: []OutputLogLabel{}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := whereWith("status_code", "_regex", "5..")
			request.AccountId = "acct-failopen-" + tc.name
			if err := applyLabelDataTypes(labelTypeTestContext(), tc.source, &request); err != nil {
				t.Errorf("expected fail-open (nil), got %v", err)
			}
		})
	}
}

// TestDiscoveryHotPath pins exactly when label discovery is paid for. Two things can
// require it — a type-restricted operator (which might be invalid) or a string value a
// numeric column would need rendered natively — and a clause with neither must cost
// nothing, since that is the shape of the overwhelming majority of log queries.
//
// Note the second case: an all-equality clause is NOT automatically free. `status_code
// != "200"` needs the type in order to emit a bare 200 instead of '200', and that is the
// whole point of the coercion — so discovery there is correct, not a regression.
func TestDiscoveryHotPath(t *testing.T) {
	tests := []struct {
		name          string
		where         query.QueryWhereClause
		wantLabelCall bool
	}{
		{
			name: "equality on non-numeric values needs nothing",
			where: query.QueryWhereClause{
				And: []query.QueryWhereClause{
					{Binary: query.BinaryWhereClause{"pod": {query.Eq: "api-xyz"}}},
					{Binary: query.BinaryWhereClause{"namespace": {query.Nq: "kube-system"}}},
				},
			},
			wantLabelCall: false,
		},
		{
			name: "line-body filter is never type-checked, even with a restricted operator",
			where: query.QueryWhereClause{
				Binary: query.BinaryWhereClause{"message": {query.Contains: "error"}},
			},
			wantLabelCall: false,
		},
		{
			name: "type-restricted operator on a label requires the type",
			where: query.QueryWhereClause{
				Binary: query.BinaryWhereClause{"pod": {query.Regex: "api-.*"}},
			},
			wantLabelCall: true,
		},
		{
			name: "numeric-looking value requires the type so it can be coerced",
			where: query.QueryWhereClause{
				Binary: query.BinaryWhereClause{"status_code": {query.Nq: "200"}},
			},
			wantLabelCall: true,
		},
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := &stubLogSource{labels: typedLabels()}
			request := FetchLogRequest{
				// Distinct account per case so the type cache cannot mask a call.
				AccountId:    fmt.Sprintf("acct-hotpath-%d", i),
				QueryRequest: LogsQueryBuilderRequest{Where: tt.where},
			}

			if err := applyLabelDataTypes(labelTypeTestContext(), source, &request); err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if got := source.labelCalls > 0; got != tt.wantLabelCall {
				t.Errorf("QueryLabels called = %v (%d times), want %v", got, source.labelCalls, tt.wantLabelCall)
			}
		})
	}
}

// TestValidateOperatorDataTypesSkipsDiscoveryForLineFilters is the other half of the
// hot-path guard. `message contains "error"` is one of the most common log queries
// there is, and _contains IS type-restricted — but message is a line-body filter that
// is never type-checked, so the clause still cannot fail and must not pay for label
// discovery.
func TestValidateOperatorDataTypesSkipsDiscoveryForLineFilters(t *testing.T) {
	source := &stubLogSource{labels: typedLabels()}
	request := whereWith("message", "_contains", "error")
	request.AccountId = "acct-line-filter-hotpath"

	if err := applyLabelDataTypes(labelTypeTestContext(), source, &request); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if source.labelCalls != 0 {
		t.Errorf("line-body filter triggered %d QueryLabels call(s); it must trigger none", source.labelCalls)
	}
}

// TestUntypedProviderIsUnaffected is the regression test for the providers that
// report no type at all (Loki, Splunk, Loggly, Dynatrace): every operator they
// advertise must still pass, so their behaviour is identical to before this change.
func TestUntypedProviderIsUnaffected(t *testing.T) {
	lokiOperators := []string{"_eq", "_neq", "_contains", "_like", "_ilike", "_nlike", "_icontains", "_nicontains", "_regex", "_nregex"}
	for i, op := range lokiOperators {
		source := &stubLogSource{
			// The shape Loki/Splunk/Loggly/Dynatrace emit: bare names, no attributes.
			labels:    []OutputLogLabel{{Label: "pod", Attributes: map[string]any{}}, {Label: "status_code", Attributes: map[string]any{}}},
			operators: lokiOperators,
		}
		request := whereWith("status_code", op, "5..")
		request.AccountId = "acct-untyped-" + string(rune('a'+i))
		if err := applyLabelDataTypes(labelTypeTestContext(), source, &request); err != nil {
			t.Errorf("operator %q on an untyped label was rejected (%v); untyped providers must be unaffected", op, err)
		}
	}
}

// TestEffectiveDataTypesOverride covers the per-provider override: a declared token
// wins over the catalog, an undeclared one keeps the default, and a source without
// the interface is untouched.
func TestEffectiveDataTypesOverride(t *testing.T) {
	plain := &stubLogSource{}
	if got := effectiveDataTypes("_regex", plain); len(got) != 1 || got[0] != query.LabelTypeString {
		t.Errorf("source without override should get the catalog default, got %v", got)
	}

	override := &stubOverrideSource{
		overrides: map[string][]string{
			// A backend that casts numbers for regex, e.g. an implicitly-casting engine.
			"_regex": {query.LabelTypeString, query.LabelTypeNumber},
		},
	}
	if !query.DataTypesApplyToType(effectiveDataTypes("_regex", override), query.LabelTypeNumber) {
		t.Error("overridden _regex should accept number")
	}
	if query.DataTypesApplyToType(effectiveDataTypes("_contains", override), query.LabelTypeNumber) {
		t.Error("_contains was not overridden and must keep the catalog default (string-only)")
	}
}

// TestValidateOperatorDataTypesHonoursOverride proves the override actually reaches
// the validator, not just the descriptor path.
func TestValidateOperatorDataTypesHonoursOverride(t *testing.T) {
	source := &stubOverrideSource{
		stubLogSource: stubLogSource{labels: typedLabels()},
		overrides:     map[string][]string{"_regex": {query.LabelTypeString, query.LabelTypeNumber}},
	}
	request := whereWith("status_code", "_regex", "5..")
	request.AccountId = "acct-override-validator"

	if err := applyLabelDataTypes(labelTypeTestContext(), source, &request); err != nil {
		t.Errorf("provider declared _regex valid for numbers, so this must be allowed; got %v", err)
	}
}

// TestApplyOperatorDataTypeOverrides checks the descriptor path the UI reads, and
// that it does not mutate the shared catalog descriptors.
func TestApplyOperatorDataTypeOverrides(t *testing.T) {
	descriptors := query.DescribeOperators([]string{"_eq", "_regex", "_contains"})

	unchanged := applyOperatorDataTypeOverrides(descriptors, &stubLogSource{})
	if len(unchanged) != len(descriptors) {
		t.Fatalf("expected %d descriptors, got %d", len(descriptors), len(unchanged))
	}

	source := &stubOverrideSource{overrides: map[string][]string{"_regex": {query.LabelTypeString, query.LabelTypeNumber}}}
	got := applyOperatorDataTypeOverrides(descriptors, source)

	for _, d := range got {
		switch d.Token {
		case "_regex":
			if !query.DataTypesApplyToType(d.ApplicableDataTypes, query.LabelTypeNumber) {
				t.Errorf("_regex descriptor should carry the override, got %v", d.ApplicableDataTypes)
			}
		case "_contains":
			if query.DataTypesApplyToType(d.ApplicableDataTypes, query.LabelTypeNumber) {
				t.Errorf("_contains descriptor should keep the catalog default, got %v", d.ApplicableDataTypes)
			}
		}
	}

	// The catalog is process-global; an override must not leak into it.
	if query.OperatorAppliesToType("_regex", query.LabelTypeNumber) {
		t.Error("applying an override mutated the shared OperatorCatalog")
	}
}

// TestNormalizeLabelDataTypeStringBackedTimeColumn pins the one case where the
// fieldType=dateTime promotion must NOT fire. Pinot supports string-backed time
// columns (tsMode.IsString in buildPinotSQL), and on those `LIKE '%2026-08%'` is a
// legitimate query — promoting the label to "timestamp" would withhold the pattern
// operators that actually work on it.
func TestNormalizeLabelDataTypeStringBackedTimeColumn(t *testing.T) {
	attrs := map[string]any{"dataType": "STRING", "fieldType": "dateTime", "format": "1:DAYS:SIMPLE_DATE_FORMAT:yyyy-MM-dd"}
	if got := normalizeLabelDataType(attrs); got != query.LabelTypeString {
		t.Fatalf("string-backed time column = %q, want %q", got, query.LabelTypeString)
	}
	// And therefore the pattern operators stay available on it.
	if !query.OperatorAppliesToType("_contains", query.LabelTypeString) {
		t.Error("_contains must remain valid for a string-backed time column")
	}
}

// TestCoerceWhereValuesByDataType is the value axis: the UI always sends chip values as
// strings, so a numeric column would be compared against a quoted string and the provider
// rejects the query. Each case asserts the SQL Pinot actually receives.
func TestCoerceWhereValuesByDataType(t *testing.T) {
	labelTypes := map[string]string{
		"timestamp":   query.LabelTypeTimestamp,
		"status_code": query.LabelTypeNumber,
		"pod":         query.LabelTypeString,
		"is_error":    query.LabelTypeBool,
		"nanos":       query.LabelTypeNumber,
	}

	tests := []struct {
		name    string
		field   string
		op      query.BinaryWhereClauseType
		value   any
		wantSQL string
	}{
		{
			// The exact payload from the bug report. Pinot rejects a string literal
			// compared against a LONG column.
			name:  "epoch millis in exponential form becomes a bare integer",
			field: "timestamp", op: query.Gt, value: "1.78623060714e+12",
			wantSQL: `"timestamp" > 1786230607140`,
		},
		{
			name:  "numeric column gets a bare literal",
			field: "status_code", op: query.Eq, value: "500",
			wantSQL: `"status_code" = 500`,
		},
		{
			name:  "string column keeps its quotes",
			field: "pod", op: query.Eq, value: "api-xyz",
			wantSQL: `"pod" = 'api-xyz'`,
		},
		{
			name:  "boolean column gets a bare literal",
			field: "is_error", op: query.Eq, value: "true",
			wantSQL: `"is_error" = true`,
		},
		{
			// float64 only holds integers exactly below 2^53, so routing through it
			// would round this. ParseInt runs first precisely to avoid that.
			name:  "integer beyond 2^53 keeps full precision",
			field: "nanos", op: query.Gt, value: "1786230607140000000",
			wantSQL: `"nanos" > 1786230607140000000`,
		},
		{
			// Fail open: a value that is not a number is left exactly as sent rather
			// than being mangled or rejected.
			name:  "unparseable value on a numeric column is left alone",
			field: "status_code", op: query.Eq, value: "not-a-number",
			wantSQL: `"status_code" = 'not-a-number'`,
		},
		{
			name:  "a label with no known type is left alone",
			field: "mystery", op: query.Eq, value: "500",
			wantSQL: `"mystery" = '500'`,
		},
		{
			// Pattern operators are string operations; their operand must stay a string
			// even when the column somehow types as numeric.
			name:  "pattern operators are never coerced",
			field: "status_code", op: query.Contains, value: "500",
			wantSQL: `"status_code" LIKE '%500%'`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			where := query.QueryWhereClause{
				Binary: query.BinaryWhereClause{tt.field: {tt.op: tt.value}},
			}
			got, err := buildPinotWhereClause(coerceWhereValuesByDataType(where, labelTypes))
			if err != nil {
				t.Fatalf("buildPinotWhereClause: %v", err)
			}
			if got != tt.wantSQL {
				t.Errorf("SQL = %s, want %s", got, tt.wantSQL)
			}
		})
	}
}

// TestCoerceWhereValuesByDataTypeNested proves the rewrite reaches conditions nested under
// _and / _or / _not, and that it does not mutate the caller's clause.
func TestCoerceWhereValuesByDataTypeNested(t *testing.T) {
	labelTypes := map[string]string{"status_code": query.LabelTypeNumber}
	original := query.QueryWhereClause{
		And: []query.QueryWhereClause{
			{Or: []query.QueryWhereClause{
				{Binary: query.BinaryWhereClause{"status_code": {query.Gte: "500"}}},
			}},
		},
	}

	got, err := buildPinotWhereClause(coerceWhereValuesByDataType(original, labelTypes))
	if err != nil {
		t.Fatalf("buildPinotWhereClause: %v", err)
	}
	if !strings.Contains(got, `"status_code" >= 500`) {
		t.Errorf("nested condition was not coerced: %s", got)
	}

	// The caller's clause must be untouched — callers reuse it (e.g. the empty-result
	// diagnosis snapshots values before the query runs).
	if original.And[0].Or[0].Binary["status_code"][query.Gte] != "500" {
		t.Error("coercion mutated the caller's where clause instead of returning a copy")
	}
}

// TestApplyLabelDataTypesCoercesInPlace checks the wiring: FetchLogs relies on
// applyLabelDataTypes rewriting the request it is handed.
func TestApplyLabelDataTypesCoercesInPlace(t *testing.T) {
	source := &stubLogSource{labels: []OutputLogLabel{
		{Label: "status_code", Attributes: map[string]any{"dataType": "INT"}},
	}}
	request := whereWith("status_code", "_gt", "499")
	request.AccountId = "acct-coerce-inplace"

	if err := applyLabelDataTypes(labelTypeTestContext(), source, &request); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := request.QueryRequest.Where.Binary["status_code"][query.Gt]
	if got != int64(499) {
		t.Errorf("value = %#v (%T), want int64(499) — the request must be rewritten in place", got, got)
	}
}

// TestApplyLabelDataTypesSkipsDiscoveryForNonNumericValues guards the hot path against the
// coercion work: a filter whose values cannot possibly need coercion must not trigger
// label discovery, or every ordinary `pod = "api-xyz"` query would pay for a round-trip.
func TestApplyLabelDataTypesSkipsDiscoveryForNonNumericValues(t *testing.T) {
	source := &stubLogSource{labels: typedLabels()}
	request := whereWith("pod", "_eq", "api-xyz")
	request.AccountId = "acct-hotpath-values"

	if err := applyLabelDataTypes(labelTypeTestContext(), source, &request); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if source.labelCalls != 0 {
		t.Errorf("a non-numeric equality filter triggered %d QueryLabels call(s); it must trigger none", source.labelCalls)
	}
}

// TestSQLFormattersRenderCoercedTypesBare guards every SQL-family value formatter
// against the bug class that broke Pinot: coercion emits int64 / float64 / bool, and a
// formatter that lacks a case for one of them falls through to its quoted `default:`
// branch — silently re-quoting the literal and reproducing the exact failure coercion
// exists to prevent. This was found in pplFormatValue after it had already been fixed
// in pinotFormatValue and hiveFormatValue.
func TestSQLFormattersRenderCoercedTypesBare(t *testing.T) {
	formatters := map[string]func(any) string{
		"pinotFormatValue": pinotFormatValue,
		"hiveFormatValue":  hiveFormatValue,
		"pplFormatValue":   pplFormatValue,
	}
	// Every Go type coerceLabelValue can produce.
	cases := []struct {
		value any
		want  string
	}{
		{int64(1786230607140), "1786230607140"},
		{int64(1786230607140000000), "1786230607140000000"}, // beyond 2^53
		{float64(500), "500"},
		{true, "true"},
		{false, "false"},
	}

	for name, format := range formatters {
		for _, tc := range cases {
			t.Run(fmt.Sprintf("%s/%v", name, tc.value), func(t *testing.T) {
				if got := format(tc.value); got != tc.want {
					t.Errorf("%s(%#v) = %s, want %s (a quoted result means the type fell through to default:)",
						name, tc.value, got, tc.want)
				}
			})
		}
		// Strings must still be quoted — coercion deliberately leaves them alone.
		t.Run(name+"/string stays quoted", func(t *testing.T) {
			if got := format("api-xyz"); got != "'api-xyz'" {
				t.Errorf("%s(\"api-xyz\") = %s, want 'api-xyz'", name, got)
			}
		})
	}
}
