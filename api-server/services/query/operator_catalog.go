package query

import "log/slog"

// OperatorDescriptor is the backend source-of-truth for operator display
// metadata. Every token returned by any *Source.GetSupportedOperators() must
// have an entry in OperatorCatalog below (enforced by
// TestOperatorCatalogCoversAllTokens + the observability package coverage test).
type OperatorDescriptor struct {
	Token     string   `json:"token"`
	ChipLabel string   `json:"chip_label,omitempty"`
	LineLabel string   `json:"line_label,omitempty"`
	Kinds     []string `json:"kinds"`
}

// OperatorCatalog maps BinaryWhereClauseType tokens to their display metadata.
// Keep in sync with the BinaryWhereClauseType constants at the top of
// entity_query.go. Labels mirror app/src/components1/k8s/common/operatorCatalog.ts
// which is retired in the UI follow-up PR.
var OperatorCatalog = map[string]OperatorDescriptor{
	string(Eq):         {Token: string(Eq), ChipLabel: "=", Kinds: []string{"chip"}},
	string(Nq):         {Token: string(Nq), ChipLabel: "!=", Kinds: []string{"chip"}},
	string(Lt):         {Token: string(Lt), ChipLabel: "<", Kinds: []string{"chip"}},
	string(Lte):        {Token: string(Lte), ChipLabel: "<=", Kinds: []string{"chip"}},
	string(Gt):         {Token: string(Gt), ChipLabel: ">", Kinds: []string{"chip"}},
	string(Gte):        {Token: string(Gte), ChipLabel: ">=", Kinds: []string{"chip"}},
	string(In):         {Token: string(In), ChipLabel: "in", Kinds: []string{"chip"}},
	string(NotIn):      {Token: string(NotIn), ChipLabel: "not in", Kinds: []string{"chip"}},
	string(Like):       {Token: string(Like), ChipLabel: "LIKE", LineLabel: "Line matches pattern (LIKE)", Kinds: []string{"chip", "line"}},
	string(ILike):      {Token: string(ILike), ChipLabel: "ILIKE", LineLabel: "Line matches pattern (case-insensitive LIKE)", Kinds: []string{"chip", "line"}},
	string(NLike):      {Token: string(NLike), ChipLabel: "NOT LIKE", LineLabel: "Line does not match pattern", Kinds: []string{"chip", "line"}},
	string(Contains):   {Token: string(Contains), ChipLabel: "contains", LineLabel: "Line contains", Kinds: []string{"chip", "line"}},
	string(IContains):  {Token: string(IContains), ChipLabel: "icontains", LineLabel: "Line contains (case-insensitive)", Kinds: []string{"chip", "line"}},
	string(NIContains): {Token: string(NIContains), ChipLabel: "not icontains", LineLabel: "Line does not contain (case-insensitive)", Kinds: []string{"chip", "line"}},
	string(Regex):      {Token: string(Regex), ChipLabel: "=~", LineLabel: "Line contains regex match", Kinds: []string{"chip", "line"}},
	string(NRegex):     {Token: string(NRegex), ChipLabel: "!~", LineLabel: "Line does not match regex", Kinds: []string{"chip", "line"}},
	string(HasKey):     {Token: string(HasKey), ChipLabel: "exists", Kinds: []string{"chip"}},
	string(IsNull):     {Token: string(IsNull), ChipLabel: "is null", Kinds: []string{"chip"}},
	string(Between):    {Token: string(Between), ChipLabel: "between", Kinds: []string{"chip"}},
	// SolarWinds field-vs-field variants — returned by solarwinds_{logs,traces,metrics}.GetSupportedOperators.
	string(EqF):    {Token: string(EqF), ChipLabel: "= (field)", Kinds: []string{"chip"}},
	string(NqF):    {Token: string(NqF), ChipLabel: "!= (field)", Kinds: []string{"chip"}},
	string(LtF):    {Token: string(LtF), ChipLabel: "< (field)", Kinds: []string{"chip"}},
	string(LteF):   {Token: string(LteF), ChipLabel: "<= (field)", Kinds: []string{"chip"}},
	string(GtF):    {Token: string(GtF), ChipLabel: "> (field)", Kinds: []string{"chip"}},
	string(GteF):   {Token: string(GteF), ChipLabel: ">= (field)", Kinds: []string{"chip"}},
	string(LikeF):  {Token: string(LikeF), ChipLabel: "LIKE (field)", Kinds: []string{"chip"}},
	string(ILikeF): {Token: string(ILikeF), ChipLabel: "ILIKE (field)", Kinds: []string{"chip"}},
}

// DescribeOperators maps a provider's supported_operators []string to their
// descriptors, skipping (with a slog.Warn) any unknown token so we never panic
// at request time on a drift. An unknown token also fails the coverage test in CI.
func DescribeOperators(tokens []string) []OperatorDescriptor {
	out := make([]OperatorDescriptor, 0, len(tokens))
	for _, t := range tokens {
		if d, ok := OperatorCatalog[t]; ok {
			out = append(out, d)
		} else {
			slog.Warn("operator catalog drift: provider returned token with no descriptor", "token", t)
		}
	}
	return out
}
