package query

import "log/slog"

// Normalized label data types. Providers describe a label's type in their own
// vocabulary (Pinot "INT", SigNoz "int64", Hive "bigint", ES "long", …); callers
// normalize onto this set before consulting OperatorDescriptor.ApplicableDataTypes.
//
// LabelTypeUnknown is the fallback for a label whose type could not be determined.
// It is PERMISSIVE — OperatorAppliesToType returns true for it, exactly as it does
// for LabelTypeString — so an untyped label is never narrowed or rejected. It is
// kept distinct from LabelTypeString rather than collapsed into it because the type
// travels on the wire and will gain non-operator consumers (value coercion by column
// type, number-vs-text inputs): for those, "unknown" degrades safely while a
// defaulted "string" would be an active false assertion.
const (
	LabelTypeString    = "string"
	LabelTypeNumber    = "number"
	LabelTypeBool      = "bool"
	LabelTypeTimestamp = "timestamp"
	LabelTypeUnknown   = "unknown"
)

// Reusable ApplicableDataTypes sets. allTypes is the permissive default.
var (
	allTypes        = []string{LabelTypeString, LabelTypeNumber, LabelTypeBool, LabelTypeTimestamp}
	orderableTypes  = []string{LabelTypeString, LabelTypeNumber, LabelTypeTimestamp}
	stringOnlyTypes = []string{LabelTypeString}
)

// OperatorDescriptor is the backend source-of-truth for operator display
// metadata. Every token returned by any *Source.GetSupportedOperators() must
// have an entry in OperatorCatalog below (enforced by
// TestOperatorCatalogCoversAllTokens + the observability package coverage test).
type OperatorDescriptor struct {
	Token     string   `json:"token"`
	ChipLabel string   `json:"chip_label,omitempty"`
	LineLabel string   `json:"line_label,omitempty"`
	Kinds     []string `json:"kinds"`
	// ApplicableDataTypes lists the normalized label data types this operator is
	// meaningful for. This is the TYPE axis and is provider-agnostic; whether a
	// backend implements the operator at all is the separate PROVIDER axis,
	// answered by GetSupportedOperators(). Clients offer the intersection.
	//
	// INVARIANT: every entry must include LabelTypeString. String is both the most
	// permissive type and the behavioural twin of the "unknown" fallback, so this is
	// what makes an undetermined type provably incapable of blocking anything.
	// Enforced by TestOperatorCatalogFallbackIsPermissive.
	ApplicableDataTypes []string `json:"applicable_data_types"`
}

// OperatorCatalog maps BinaryWhereClauseType tokens to their display metadata.
// Keep in sync with the BinaryWhereClauseType constants at the top of
// entity_query.go. Labels mirror app/src/components1/k8s/common/operatorCatalog.ts
// which is retired in the UI follow-up PR.
// Type axis rationale: equality and existence apply to every type; ordering
// comparators are excluded for bool only; pattern operators are string-only —
// that last row is what stops `REGEXP_LIKE(intCol, …)` / `intCol LIKE '%5%'`
// reaching a backend that rejects it.
var OperatorCatalog = map[string]OperatorDescriptor{
	string(Eq):         {Token: string(Eq), ChipLabel: "=", Kinds: []string{"chip"}, ApplicableDataTypes: allTypes},
	string(Nq):         {Token: string(Nq), ChipLabel: "!=", Kinds: []string{"chip"}, ApplicableDataTypes: allTypes},
	string(Lt):         {Token: string(Lt), ChipLabel: "<", Kinds: []string{"chip"}, ApplicableDataTypes: orderableTypes},
	string(Lte):        {Token: string(Lte), ChipLabel: "<=", Kinds: []string{"chip"}, ApplicableDataTypes: orderableTypes},
	string(Gt):         {Token: string(Gt), ChipLabel: ">", Kinds: []string{"chip"}, ApplicableDataTypes: orderableTypes},
	string(Gte):        {Token: string(Gte), ChipLabel: ">=", Kinds: []string{"chip"}, ApplicableDataTypes: orderableTypes},
	string(In):         {Token: string(In), ChipLabel: "in", Kinds: []string{"chip"}, ApplicableDataTypes: allTypes},
	string(NotIn):      {Token: string(NotIn), ChipLabel: "not in", Kinds: []string{"chip"}, ApplicableDataTypes: allTypes},
	string(Like):       {Token: string(Like), ChipLabel: "LIKE", LineLabel: "Line matches pattern (LIKE)", Kinds: []string{"chip", "line"}, ApplicableDataTypes: stringOnlyTypes},
	string(ILike):      {Token: string(ILike), ChipLabel: "ILIKE", LineLabel: "Line matches pattern (case-insensitive LIKE)", Kinds: []string{"chip", "line"}, ApplicableDataTypes: stringOnlyTypes},
	string(NLike):      {Token: string(NLike), ChipLabel: "NOT LIKE", LineLabel: "Line does not match pattern", Kinds: []string{"chip", "line"}, ApplicableDataTypes: stringOnlyTypes},
	string(Contains):   {Token: string(Contains), ChipLabel: "contains", LineLabel: "Line contains", Kinds: []string{"chip", "line"}, ApplicableDataTypes: stringOnlyTypes},
	string(IContains):  {Token: string(IContains), ChipLabel: "icontains", LineLabel: "Line contains (case-insensitive)", Kinds: []string{"chip", "line"}, ApplicableDataTypes: stringOnlyTypes},
	string(NIContains): {Token: string(NIContains), ChipLabel: "not icontains", LineLabel: "Line does not contain (case-insensitive)", Kinds: []string{"chip", "line"}, ApplicableDataTypes: stringOnlyTypes},
	string(Regex):      {Token: string(Regex), ChipLabel: "=~", LineLabel: "Line contains regex match", Kinds: []string{"chip", "line"}, ApplicableDataTypes: stringOnlyTypes},
	string(NRegex):     {Token: string(NRegex), ChipLabel: "!~", LineLabel: "Line does not match regex", Kinds: []string{"chip", "line"}, ApplicableDataTypes: stringOnlyTypes},
	string(HasKey):     {Token: string(HasKey), ChipLabel: "exists", Kinds: []string{"chip"}, ApplicableDataTypes: allTypes},
	string(IsNull):     {Token: string(IsNull), ChipLabel: "is null", Kinds: []string{"chip"}, ApplicableDataTypes: allTypes},
	string(Between):    {Token: string(Between), ChipLabel: "between", Kinds: []string{"chip"}, ApplicableDataTypes: orderableTypes},
	// SolarWinds field-vs-field variants — returned by solarwinds_{logs,traces,metrics}.GetSupportedOperators.
	// Each mirrors its value counterpart on the type axis.
	string(EqF):    {Token: string(EqF), ChipLabel: "= (field)", Kinds: []string{"chip"}, ApplicableDataTypes: allTypes},
	string(NqF):    {Token: string(NqF), ChipLabel: "!= (field)", Kinds: []string{"chip"}, ApplicableDataTypes: allTypes},
	string(LtF):    {Token: string(LtF), ChipLabel: "< (field)", Kinds: []string{"chip"}, ApplicableDataTypes: orderableTypes},
	string(LteF):   {Token: string(LteF), ChipLabel: "<= (field)", Kinds: []string{"chip"}, ApplicableDataTypes: orderableTypes},
	string(GtF):    {Token: string(GtF), ChipLabel: "> (field)", Kinds: []string{"chip"}, ApplicableDataTypes: orderableTypes},
	string(GteF):   {Token: string(GteF), ChipLabel: ">= (field)", Kinds: []string{"chip"}, ApplicableDataTypes: orderableTypes},
	string(LikeF):  {Token: string(LikeF), ChipLabel: "LIKE (field)", Kinds: []string{"chip"}, ApplicableDataTypes: stringOnlyTypes},
	string(ILikeF): {Token: string(ILikeF), ChipLabel: "ILIKE (field)", Kinds: []string{"chip"}, ApplicableDataTypes: stringOnlyTypes},
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

// OperatorAppliesToType reports whether an operator is meaningful for a normalized
// label data type, per the catalog default. Fails OPEN: an unrecognized token, an
// entry with no declared types, an empty type, and LabelTypeUnknown all return true,
// so this can only ever narrow a case we positively understand.
//
// Callers holding a provider override should consult DataTypesApplyToType with the
// effective type list instead.
func OperatorAppliesToType(token, dataType string) bool {
	descriptor, ok := OperatorCatalog[token]
	if !ok {
		return true
	}
	return DataTypesApplyToType(descriptor.ApplicableDataTypes, dataType)
}

// IsTypeRestrictedOperator reports whether an operator accepts only a subset of the
// data types — i.e. whether it can fail type validation at all. Callers use it as a
// cheap guard to skip label-type discovery for clauses that cannot fail (the common
// all-`_eq` query). Unknown tokens are treated as unrestricted.
func IsTypeRestrictedOperator(token string) bool {
	descriptor, ok := OperatorCatalog[token]
	if !ok {
		return false
	}
	return len(descriptor.ApplicableDataTypes) > 0 &&
		len(descriptor.ApplicableDataTypes) < len(allTypes)
}

// DataTypesApplyToType reports whether dataType is in applicable, with the same
// fail-open rules as OperatorAppliesToType. Split out so callers that resolved a
// per-provider override share one definition of "permitted".
func DataTypesApplyToType(applicable []string, dataType string) bool {
	if len(applicable) == 0 || dataType == "" || dataType == LabelTypeUnknown {
		return true
	}
	for _, t := range applicable {
		if t == dataType {
			return true
		}
	}
	return false
}
