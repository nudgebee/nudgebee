package query

import (
	"strings"
	"testing"
)

// TestOperatorCatalogCoversAllTokens fails if a BinaryWhereClauseType constant
// is declared in entity_query.go but has no entry in OperatorCatalog. This is
// one half of the catalog drift guard — the other half lives in
// api-server/services/observability/operator_catalog_coverage_test.go and
// checks every *Source.GetSupportedOperators() token.
func TestOperatorCatalogCoversAllTokens(t *testing.T) {
	declared := []BinaryWhereClauseType{
		Eq, Nq, Lt, Lte, Gt, Gte, In, NotIn,
		Like, ILike, NLike,
		Contains, IContains, NIContains,
		Regex, NRegex,
		HasKey, IsNull, Between,
		EqF, NqF, LtF, LteF, GtF, GteF, LikeF, ILikeF,
	}
	for _, tok := range declared {
		if _, ok := OperatorCatalog[string(tok)]; !ok {
			t.Errorf("BinaryWhereClauseType %q has no OperatorCatalog entry", tok)
		}
	}
}

// TestOperatorCatalogLabelsPopulated asserts every descriptor carries a
// non-empty label for each kind it claims. A "chip" descriptor with no
// chip_label (or "line" with no line_label) will render the raw token in the
// UI dropdown — the failure mode that motivated issue #29227. Catching it
// here blocks the PR before the UX regression ships.
func TestOperatorCatalogLabelsPopulated(t *testing.T) {
	for token, desc := range OperatorCatalog {
		validateCatalogEntry(t, token, desc)
	}
}

func validateCatalogEntry(t *testing.T, token string, desc OperatorDescriptor) {
	t.Helper()
	if token == "" {
		t.Errorf("OperatorCatalog has empty token key for %+v", desc)
	}
	if !strings.HasPrefix(token, "_") {
		t.Errorf("token %q does not start with '_' — all operator tokens must follow the _<op> convention", token)
	}
	if desc.Token != token {
		t.Errorf("descriptor Token %q does not match catalog key %q", desc.Token, token)
	}
	if len(desc.Kinds) == 0 {
		t.Errorf("descriptor for %q has no Kinds — must contain at least one of chip/line", token)
	}
	for _, k := range desc.Kinds {
		validateCatalogKind(t, token, desc, k)
	}
	if len(desc.ApplicableDataTypes) == 0 {
		t.Errorf("descriptor for %q declares no ApplicableDataTypes — every operator must state the label data types it accepts", token)
	}
	for _, dt := range desc.ApplicableDataTypes {
		switch dt {
		case LabelTypeString, LabelTypeNumber, LabelTypeBool, LabelTypeTimestamp:
		default:
			t.Errorf("descriptor for %q declares unknown data type %q", token, dt)
		}
	}
}

// TestOperatorCatalogFallbackIsPermissive enforces the invariant that makes an
// undetermined label type safe: every operator must accept LabelTypeString.
//
// String is the permissive type and the behavioural twin of the LabelTypeUnknown
// fallback, so as long as this holds, a label whose type could not be determined can
// never have an operator withheld or a query rejected. Tightening the string row
// without realising it is also the fallback path would silently narrow every untyped
// provider (Loki, Splunk, Loggly, Dynatrace) — this test blocks that.
func TestOperatorCatalogFallbackIsPermissive(t *testing.T) {
	for token, desc := range OperatorCatalog {
		if !DataTypesApplyToType(desc.ApplicableDataTypes, LabelTypeString) {
			t.Errorf("operator %q excludes %q — string is the permissive fallback for undetermined types and must always be accepted", token, LabelTypeString)
		}
		if !OperatorAppliesToType(token, LabelTypeUnknown) {
			t.Errorf("operator %q rejects %q — an undetermined label type must never be narrowed", token, LabelTypeUnknown)
		}
	}
}

// TestOperatorAppliesToType covers the type axis and its fail-open rules.
func TestOperatorAppliesToType(t *testing.T) {
	tests := []struct {
		name     string
		token    string
		dataType string
		want     bool
	}{
		{"regex on string is valid", string(Regex), LabelTypeString, true},
		{"regex on number is rejected — the reported bug", string(Regex), LabelTypeNumber, false},
		{"contains on number is rejected — same failure as regex", string(Contains), LabelTypeNumber, false},
		{"like on timestamp is rejected", string(Like), LabelTypeTimestamp, false},
		{"eq on number is valid", string(Eq), LabelTypeNumber, true},
		{"eq on bool is valid", string(Eq), LabelTypeBool, true},
		{"neq on bool is valid", string(Nq), LabelTypeBool, true},
		{"gt on number is valid", string(Gt), LabelTypeNumber, true},
		{"gt on timestamp is valid", string(Gt), LabelTypeTimestamp, true},
		{"gt on bool is rejected", string(Gt), LabelTypeBool, false},
		{"unknown type fails open", string(Regex), LabelTypeUnknown, true},
		{"empty type fails open", string(Regex), "", true},
		{"unknown token fails open", "_bogus_token", LabelTypeNumber, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := OperatorAppliesToType(tt.token, tt.dataType); got != tt.want {
				t.Errorf("OperatorAppliesToType(%q, %q) = %v, want %v", tt.token, tt.dataType, got, tt.want)
			}
		})
	}
}

// TestIsTypeRestrictedOperator guards the hot-path guard: only operators that can
// actually fail type validation may report as restricted, otherwise every all-`_eq`
// log query would pay for label-type discovery it can never need.
func TestIsTypeRestrictedOperator(t *testing.T) {
	restricted := []BinaryWhereClauseType{Regex, NRegex, Like, ILike, NLike, Contains, IContains, NIContains, Lt, Lte, Gt, Gte, Between}
	for _, tok := range restricted {
		if !IsTypeRestrictedOperator(string(tok)) {
			t.Errorf("operator %q accepts a subset of types and must report as type-restricted", tok)
		}
	}
	unrestricted := []BinaryWhereClauseType{Eq, Nq, In, NotIn, HasKey, IsNull}
	for _, tok := range unrestricted {
		if IsTypeRestrictedOperator(string(tok)) {
			t.Errorf("operator %q accepts every type and must not trigger label-type discovery", tok)
		}
	}
	if IsTypeRestrictedOperator("_bogus_token") {
		t.Error("unknown token must not report as type-restricted")
	}
}

func validateCatalogKind(t *testing.T, token string, desc OperatorDescriptor, kind string) {
	t.Helper()
	switch kind {
	case "chip":
		if desc.ChipLabel == "" {
			t.Errorf("descriptor for %q declares kind=chip but has empty ChipLabel", token)
		}
	case "line":
		if desc.LineLabel == "" {
			t.Errorf("descriptor for %q declares kind=line but has empty LineLabel", token)
		}
	default:
		t.Errorf("descriptor for %q has unknown kind %q — allowed: chip, line", token, kind)
	}
}

// TestDescribeOperatorsSkipsUnknown ensures DescribeOperators tolerates
// unknown tokens without panicking — a defensive behavior since the coverage
// test will flag the real problem.
func TestDescribeOperatorsSkipsUnknown(t *testing.T) {
	got := DescribeOperators([]string{string(Eq), "_bogus_token", string(Contains)})
	if len(got) != 2 {
		t.Fatalf("expected 2 descriptors (unknown skipped), got %d", len(got))
	}
	if got[0].Token != string(Eq) || got[1].Token != string(Contains) {
		t.Errorf("unexpected descriptors: %+v", got)
	}
}
