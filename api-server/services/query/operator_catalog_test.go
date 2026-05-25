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
