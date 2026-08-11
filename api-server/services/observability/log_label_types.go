package observability

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"nudgebee/services/common"
	"nudgebee/services/query"
	"nudgebee/services/security"
)

const logLabelTypesCacheNamespace = "nb_log_label_types"
const logLabelTypesCacheTTL = 10 * time.Minute

func init() {
	common.CacheCreateNamespace(
		logLabelTypesCacheNamespace,
		common.CacheNamespaceWithExpiration(logLabelTypesCacheTTL),
	)
}

// labelDataTypeVocabulary maps every provider's own type name onto the normalized
// set in query.LabelType*. Lowercased keys; parameterized types (varchar(255),
// decimal(10,2), array<string>) are reduced to their base name before lookup.
//
// Deliberately absent: json / map / object / dynamic / nested / struct. Those are
// structured columns where neither pattern nor ordering operators have a
// well-defined meaning, so they resolve to "unknown" and fail open rather than
// being narrowed on a guess.
var labelDataTypeVocabulary = map[string]string{
	// Pinot
	"string": query.LabelTypeString, "int": query.LabelTypeNumber, "long": query.LabelTypeNumber,
	"float": query.LabelTypeNumber, "double": query.LabelTypeNumber, "boolean": query.LabelTypeBool,
	"bytes": query.LabelTypeString, "big_decimal": query.LabelTypeNumber, "timestamp": query.LabelTypeTimestamp,
	// Hive
	"varchar": query.LabelTypeString, "char": query.LabelTypeString, "bigint": query.LabelTypeNumber,
	"smallint": query.LabelTypeNumber, "tinyint": query.LabelTypeNumber, "decimal": query.LabelTypeNumber,
	"numeric": query.LabelTypeNumber, "date": query.LabelTypeTimestamp, "binary": query.LabelTypeString,
	// SigNoz
	"int64": query.LabelTypeNumber, "float64": query.LabelTypeNumber, "int32": query.LabelTypeNumber,
	"float32": query.LabelTypeNumber, "bool": query.LabelTypeBool,
	// Elasticsearch
	"keyword": query.LabelTypeString, "text": query.LabelTypeString, "integer": query.LabelTypeNumber,
	"short": query.LabelTypeNumber, "byte": query.LabelTypeNumber, "half_float": query.LabelTypeNumber,
	"scaled_float": query.LabelTypeNumber, "unsigned_long": query.LabelTypeNumber,
	"date_nanos": query.LabelTypeTimestamp, "ip": query.LabelTypeString,
	// Azure KQL / Observe
	"real": query.LabelTypeNumber, "datetime": query.LabelTypeTimestamp, "guid": query.LabelTypeString,
	"number": query.LabelTypeNumber, "duration": query.LabelTypeNumber, "str": query.LabelTypeString,
}

// labelDataTypeAttributeKeys are the attribute keys providers store a label's type
// under, in precedence order. "dataType" first because SigNoz sets BOTH "dataType"
// (string/int64/…) and "type" (tag/resource) — the latter is a scope, not a type, so
// it must never win.
var labelDataTypeAttributeKeys = []string{"dataType", "datatype", "data_type", "type"}

// lineContentFieldSet is lineContentFields as a set, built once at init rather than
// per call: the log-query validator consults it on every request.
var lineContentFieldSet = func() map[string]struct{} {
	set := make(map[string]struct{}, len(lineContentFields))
	for _, f := range lineContentFields {
		set[f] = struct{}{}
	}
	return set
}()

// normalizeLabelDataType maps a provider's label attributes onto a normalized data
// type. Returns query.LabelTypeUnknown when the type cannot be determined — the
// permissive fallback, so an unrecognized vocabulary never narrows anything.
func normalizeLabelDataType(attrs map[string]any) string {
	if len(attrs) == 0 {
		return query.LabelTypeUnknown
	}

	declared := declaredLabelDataType(attrs)

	// A time column's storage type says nothing about what it holds: Pinot reports an
	// epoch-millis field as {"dataType": "LONG", "fieldType": "dateTime"}, so reading
	// dataType alone would call it a plain number.
	//
	// Not applied to string-backed time columns (Pinot supports those — see tsMode.
	// IsString in buildPinotSQL): there, `LIKE '%2026-08%'` is a legitimate query, and
	// promoting the label to "timestamp" would withhold the pattern operators that
	// actually work on it.
	if role, ok := attrs["fieldType"].(string); ok && strings.EqualFold(role, "dateTime") {
		if declared != query.LabelTypeString {
			return query.LabelTypeTimestamp
		}
	}
	if declared != query.LabelTypeUnknown {
		return declared
	}

	// Elasticsearch _field_caps shape: the type IS the key, and its value is the
	// per-type detail object, e.g. {"text": {"searchable": true}}. Mirrors
	// isTextFieldAttributes in elasticsearch.go, generalized to every type.
	//
	// Only map-valued keys qualify. A flat attribute that merely happens to be named
	// after a type — {"real": "yes"}, {"double": true} — is NOT this shape, and
	// treating it as one would silently mistype the label and withhold operators that
	// are in fact valid. Sorted for determinism when a field reports multiple types.
	keys := make([]string, 0, len(attrs))
	for k, v := range attrs {
		if _, isTypeDetail := v.(map[string]any); isTypeDetail {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		if t := lookupLabelDataType(k); t != query.LabelTypeUnknown {
			return t
		}
	}

	return query.LabelTypeUnknown
}

// declaredLabelDataType reads the type the provider states outright, trying each
// attribute key in precedence order. Returns query.LabelTypeUnknown when no key
// carries a vocabulary we recognize.
func declaredLabelDataType(attrs map[string]any) string {
	for _, key := range labelDataTypeAttributeKeys {
		if raw, ok := attrs[key].(string); ok && raw != "" {
			if t := lookupLabelDataType(raw); t != query.LabelTypeUnknown {
				return t
			}
		}
	}
	return query.LabelTypeUnknown
}

// lookupLabelDataType normalizes one provider type name, stripping the parameters
// and element types that SQL-family providers attach (varchar(255), decimal(10,2),
// array<string>).
func lookupLabelDataType(raw string) string {
	name := strings.ToLower(strings.TrimSpace(raw))
	if i := strings.IndexAny(name, "(<"); i > 0 {
		name = name[:i]
	}
	if t, ok := labelDataTypeVocabulary[strings.TrimSpace(name)]; ok {
		return t
	}
	return query.LabelTypeUnknown
}

// LabelsFromIndexFields converts an index-field listing (Elasticsearch's
// fetch_index path) into the label shape clients consume, normalizing DataType on
// the way. Lives here rather than at the call site so index fields and plain labels
// are typed by the same rule — ES is the provider whose per-field types exist ONLY
// on this path, so a conversion that forgot to normalize would silently exclude it
// from type-aware operators.
func LabelsFromIndexFields(fields []OutputLogLabelFields) []OutputLogLabel {
	labels := make([]OutputLogLabel, len(fields))
	for i, f := range fields {
		labels[i] = OutputLogLabel{
			Label:      f.Field,
			Attributes: f.Attributes,
			DataType:   normalizeLabelDataType(f.Attributes),
		}
	}
	return labels
}

// LogLabelFieldsSource is an optional capability for log sources whose QueryLabels
// does not describe queryable FIELDS. Elasticsearch is the case that matters: its
// QueryLabels returns index names, while the per-field types live behind
// QueryIndexFields. Both ES sources already implement this method (see the type
// switch in FetchLogIndexFields), so declaring the interface costs nothing and keeps
// type resolution generic instead of special-casing ES at the call site.
type LogLabelFieldsSource interface {
	QueryIndexFields(ctx *security.RequestContext, request FetchLogLabelRequest) ([]OutputLogLabelFields, error)
}

// OperatorDataTypeSource is an optional capability for sources whose operator↔type
// validity differs from query.OperatorCatalog's default — e.g. a backend that
// implicitly casts, or one whose label model is uniformly stringly-typed. Returns
// token → applicable data types; tokens absent from the map keep the catalog
// default, so an implementation only states what it disagrees with.
//
// Implementations must be pure (no I/O): this is called on the capabilities path.
type OperatorDataTypeSource interface {
	GetOperatorDataTypes() map[string][]string
}

// effectiveDataTypes resolves the applicable data types for one operator as
// `catalog default ← provider override`. Single resolution point shared by the
// capabilities path and the query validator, so the operator list the UI offers and
// the one the backend enforces cannot drift apart.
func effectiveDataTypes(token string, source any) []string {
	if override, ok := source.(OperatorDataTypeSource); ok {
		if types, found := override.GetOperatorDataTypes()[token]; found {
			return types
		}
	}
	return query.OperatorCatalog[token].ApplicableDataTypes
}

// applyOperatorDataTypeOverrides rewrites ApplicableDataTypes on each descriptor
// using the source's override, if it declares one. Returns descriptors unchanged
// when the source implements no override.
func applyOperatorDataTypeOverrides(descriptors []query.OperatorDescriptor, source any) []query.OperatorDescriptor {
	override, ok := source.(OperatorDataTypeSource)
	if !ok {
		return descriptors
	}
	overrides := override.GetOperatorDataTypes()
	if len(overrides) == 0 {
		return descriptors
	}
	out := make([]query.OperatorDescriptor, len(descriptors))
	copy(out, descriptors)
	for i := range out {
		if types, found := overrides[out[i].Token]; found {
			out[i].ApplicableDataTypes = types
		}
	}
	return out
}

// resolveLabelDataTypes returns the label → normalized data type map for a log
// source, cached per (account, provider, source, index) for 10 minutes. Prefers
// QueryIndexFields when the source implements LogLabelFieldsSource (Elasticsearch);
// otherwise normalizes QueryLabels' attributes.
//
// Returns nil on any failure, which callers treat as "cannot determine" and fail
// open — a label-discovery hiccup must never block a log query.
func resolveLabelDataTypes(ctx *security.RequestContext, source LogSource, request FetchLogLabelRequest) map[string]string {
	cacheKey := logLabelTypesCacheKey(request)

	if cached, ok := common.CacheGet(logLabelTypesCacheNamespace, cacheKey); ok {
		var types map[string]string
		if err := json.Unmarshal(cached, &types); err == nil {
			return types
		}
		_ = common.CacheDelete(logLabelTypesCacheNamespace, cacheKey)
	}

	types := fetchLabelDataTypes(ctx, source, request)
	if types == nil {
		return nil
	}

	if b, err := json.Marshal(types); err == nil {
		if err := common.CacheSet(logLabelTypesCacheNamespace, cacheKey, b); err != nil {
			slog.Warn("resolveLabelDataTypes: failed to cache label types", "account_id", request.AccountId, "error", err)
		}
	}
	return types
}

// labelDiscoveryRequest narrows a log query's free-form Request map to just the keys
// label discovery needs — today only "index", which Elasticsearch's QueryIndexFields
// reads. Passing the log request's map wholesale would let a query-specific key change
// discovery semantics (Loki's QueryLabels, for one, treats Request["query"] as a label
// selector), so this validator must never hand a provider more than it asked for.
func labelDiscoveryRequest(request map[string]any) map[string]any {
	if index := common.GetString(request, "index"); index != "" {
		return map[string]any{"index": index}
	}
	return nil
}

// logLabelTypesCacheKey mirrors the (account, provider, source) triple the query
// source is resolved with, plus the ES index — field types are per-index, so two
// indices under one integration must not share a cache entry.
func logLabelTypesCacheKey(request FetchLogLabelRequest) string {
	index := common.GetString(request.Request, "index")
	return request.AccountId + "|" + request.LogProvider + "|" + request.LogProviderSource + "|" + index
}

// fetchLabelDataTypes performs the uncached discovery. Returns nil when the label
// set cannot be established.
func fetchLabelDataTypes(ctx *security.RequestContext, source LogSource, request FetchLogLabelRequest) map[string]string {
	if fieldsSource, ok := source.(LogLabelFieldsSource); ok {
		fields, err := fieldsSource.QueryIndexFields(ctx, request)
		if err == nil && len(fields) > 0 {
			types := make(map[string]string, len(fields))
			for _, f := range fields {
				types[f.Field] = normalizeLabelDataType(f.Attributes)
			}
			return types
		}
		// Fall through to QueryLabels: an ES account with no index selected still
		// gets whatever the label listing can offer, rather than nothing.
	}

	labels, err := source.QueryLabels(ctx, request)
	if err != nil || len(labels) == 0 {
		return nil
	}
	types := make(map[string]string, len(labels))
	for _, l := range labels {
		types[l.Label] = normalizeLabelDataType(l.Attributes)
	}
	return types
}

// valueCoercibleOperators are the operators whose operand is compared against the
// column rather than matched as text. Only these are coerced: the pattern operators
// are string operations by nature (and are already rejected for non-string columns),
// and _is_null / _has_key carry no comparable value.
var valueCoercibleOperators = map[query.BinaryWhereClauseType]struct{}{
	query.Eq: {}, query.Nq: {},
	query.Lt: {}, query.Lte: {}, query.Gt: {}, query.Gte: {},
	query.In: {}, query.NotIn: {}, query.Between: {},
}

// coerceLabelValue converts a filter value to the Go type the label's data type calls
// for, so SQL renderers emit a bare literal instead of a quoted string. The UI always
// sends chip values as strings, so without this a numeric column gets
// `"timestamp" > '1.78623060714e+12'` — a string compared to a LONG, which Pinot
// rejects outright.
//
// Returns the value unchanged when it is already typed, when the column is not numeric
// or boolean, or when the string does not parse — never guesses.
func coerceLabelValue(val any, dataType string) any {
	raw, isString := val.(string)
	if !isString || raw == "" {
		return val
	}
	switch dataType {
	case query.LabelTypeNumber, query.LabelTypeTimestamp:
		// ParseInt first so large integers (e.g. epoch nanoseconds) keep full
		// precision; float64 only holds integers exactly below 2^53.
		if i, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64); err == nil {
			return i
		}
		// Covers the exponential form the UI produces for epoch millis
		// ("1.78623060714e+12"), which ParseInt cannot read.
		if f, err := strconv.ParseFloat(strings.TrimSpace(raw), 64); err == nil {
			return f
		}
	case query.LabelTypeBool:
		if b, err := strconv.ParseBool(strings.TrimSpace(raw)); err == nil {
			return b
		}
	}
	return val
}

// coerceWhereValuesByDataType returns a copy of the clause with every comparable value
// rendered as its column's native type. Pure — it builds a new clause rather than
// mutating the caller's maps, mirroring convertWhereClauseWithMApping.
func coerceWhereValuesByDataType(where query.QueryWhereClause, labelTypes map[string]string) query.QueryWhereClause {
	converted := query.QueryWhereClause{}

	if len(where.Binary) > 0 {
		converted.Binary = make(query.BinaryWhereClause, len(where.Binary))
		for field, ops := range where.Binary {
			dataType := labelTypes[field]
			newOps := make(map[query.BinaryWhereClauseType]any, len(ops))
			for op, val := range ops {
				if _, coercible := valueCoercibleOperators[op]; !coercible || dataType == "" {
					newOps[op] = val
					continue
				}
				newOps[op] = coerceSingleOrList(val, dataType)
			}
			converted.Binary[field] = newOps
		}
	}
	for _, c := range where.And {
		converted.And = append(converted.And, coerceWhereValuesByDataType(c, labelTypes))
	}
	for _, c := range where.Or {
		converted.Or = append(converted.Or, coerceWhereValuesByDataType(c, labelTypes))
	}
	if where.Not != nil {
		notConverted := coerceWhereValuesByDataType(*where.Not, labelTypes)
		converted.Not = &notConverted
	}
	return converted
}

// coerceSingleOrList applies coerceLabelValue to a scalar or to each element of a list
// value (_in / _not_in / _between).
func coerceSingleOrList(val any, dataType string) any {
	items, isList := val.([]any)
	if !isList {
		return coerceLabelValue(val, dataType)
	}
	out := make([]any, len(items))
	for i, item := range items {
		out[i] = coerceLabelValue(item, dataType)
	}
	return out
}

// hasCoercibleStringValue reports whether the clause contains a string value that a
// numeric or boolean column would need rendered natively. Used to extend the
// label-discovery guard: without it, a plain `pod = "api-xyz"` filter would still pay
// for discovery even though no value could ever be coerced.
func hasCoercibleStringValue(where query.QueryWhereClause) bool {
	for _, ops := range where.Binary {
		for op, val := range ops {
			if _, coercible := valueCoercibleOperators[op]; !coercible {
				continue
			}
			if anyValueLooksTyped(val) {
				return true
			}
		}
	}
	for _, c := range where.And {
		if hasCoercibleStringValue(c) {
			return true
		}
	}
	for _, c := range where.Or {
		if hasCoercibleStringValue(c) {
			return true
		}
	}
	if where.Not != nil {
		return hasCoercibleStringValue(*where.Not)
	}
	return false
}

// anyValueLooksTyped reports whether a value (or any list element) is a string that
// parses as a number or boolean — a cheap syntactic pre-filter, no I/O.
func anyValueLooksTyped(val any) bool {
	if items, isList := val.([]any); isList {
		for _, item := range items {
			if anyValueLooksTyped(item) {
				return true
			}
		}
		return false
	}
	raw, isString := val.(string)
	if !isString || raw == "" {
		return false
	}
	trimmed := strings.TrimSpace(raw)
	if _, err := strconv.ParseFloat(trimmed, 64); err == nil {
		return true
	}
	_, err := strconv.ParseBool(trimmed)
	return err == nil
}

// collectWhereFieldOperators walks a canonical where clause and records every
// (field, operator) pair referenced by a binary condition, across nested
// _and / _or / _not. Mirrors collectWhereFieldValues' recursion.
func collectWhereFieldOperators(where query.QueryWhereClause, out map[string]map[string]struct{}) {
	for field, ops := range where.Binary {
		for op := range ops {
			if out[field] == nil {
				out[field] = map[string]struct{}{}
			}
			out[field][string(op)] = struct{}{}
		}
	}
	for _, c := range where.And {
		collectWhereFieldOperators(c, out)
	}
	for _, c := range where.Or {
		collectWhereFieldOperators(c, out)
	}
	if where.Not != nil {
		collectWhereFieldOperators(*where.Not, out)
	}
}

// applyLabelDataTypes reconciles a where clause with the labels' data types. It does
// two things, both needing the same type lookup so they share one discovery call:
//
//  1. REJECTS an operator the label's type cannot support — the `status_code =~ "5.."`
//     case, where the label is an INT column and the provider answers with a raw error.
//  2. COERCES each value to its column's native type. The UI always sends chip values as
//     strings, so a numeric column would otherwise be compared against a quoted string
//     ("timestamp" > '1.78623060714e+12'), which Pinot rejects outright. — the `status_code =~ "5.."` case, where
//
// the label is an INT column and the provider answers with a raw backend error.
//
// The UI already hides these operators per label, so this guards the callers that
// bypass the builder: code-mode, saved URLs, the LLM agent, and direct API calls.
//
// Best-effort throughout. It fails open — returning nil — when the clause contains no
// type-sensitive operator, when label discovery fails, and for any individual label
// whose type is unknown. It can only reject a combination it positively understands.
//
// Field names must already be in provider space (call after
// convertWhereClauseWithMApping), matching the space QueryLabels reports.
func applyLabelDataTypes(ctx *security.RequestContext, source LogSource, fetchLogRequest *FetchLogRequest) error {
	fieldOperators := map[string]map[string]struct{}{}
	collectWhereFieldOperators(fetchLogRequest.QueryRequest.Where, fieldOperators)
	if len(fieldOperators) == 0 {
		return nil
	}

	// Line-body filters (message/content/…) are not labels and are never type-checked,
	// so drop them before the guard decides — otherwise the very common
	// `message contains "error"` would pay for a label-discovery round-trip only to
	// have its single field skipped below.
	//
	// Sorted so the reported error is deterministic when a clause has several
	// offending fields.
	fields := make([]string, 0, len(fieldOperators))
	for field := range fieldOperators {
		if _, isLineFilter := lineContentFieldSet[field]; isLineFilter {
			continue
		}
		fields = append(fields, field)
	}
	sort.Strings(fields)

	// Hot-path guard: skip discovery entirely unless this clause can actually need it —
	// either an operator that could be invalid for a type, or a string value that a
	// numeric/boolean column would need rendered as a bare literal. A plain
	// `pod = "api-xyz"` filter matches neither and costs nothing.
	if !hasTypeSensitiveOperator(fieldOperators, fields) && !hasCoercibleStringValue(fetchLogRequest.QueryRequest.Where) {
		return nil
	}

	labelTypes := resolveLabelDataTypes(ctx, source, FetchLogLabelRequest{
		AccountId:         fetchLogRequest.AccountId,
		LogProvider:       fetchLogRequest.LogProvider,
		LogProviderSource: fetchLogRequest.LogProviderSource,
		Request:           labelDiscoveryRequest(fetchLogRequest.Request),
		StartTime:         fetchLogRequest.StartTime,
		EndTime:           fetchLogRequest.EndTime,
	})
	if len(labelTypes) == 0 {
		return nil
	}

	for _, field := range fields {
		dataType, known := labelTypes[field]
		if !known || dataType == query.LabelTypeUnknown {
			continue
		}
		if op, ok := firstInvalidOperator(fieldOperators[field], dataType, source); ok {
			return invalidOperatorForTypeError(source, field, op, dataType)
		}
	}

	// Render each value as its column's native type. The UI always sends chip values as
	// strings, so a numeric column would otherwise be compared against a quoted string
	// ("timestamp" > '1.78623060714e+12'), which Pinot rejects.
	fetchLogRequest.QueryRequest.Where = coerceWhereValuesByDataType(fetchLogRequest.QueryRequest.Where, labelTypes)
	return nil
}

// firstInvalidOperator returns the lowest-sorted operator in ops that dataType does
// not support, so the reported error is deterministic for a multi-operator field.
func firstInvalidOperator(ops map[string]struct{}, dataType string, source LogSource) (string, bool) {
	operators := make([]string, 0, len(ops))
	for op := range ops {
		operators = append(operators, op)
	}
	sort.Strings(operators)
	for _, op := range operators {
		if !query.DataTypesApplyToType(effectiveDataTypes(op, source), dataType) {
			return op, true
		}
	}
	return "", false
}

// hasTypeSensitiveOperator reports whether any of the given fields uses an operator
// that is type-restricted, i.e. whether this clause can fail validation at all.
// Only these clauses justify the label-discovery round-trip.
func hasTypeSensitiveOperator(fieldOperators map[string]map[string]struct{}, fields []string) bool {
	for _, field := range fields {
		for op := range fieldOperators[field] {
			if query.IsTypeRestrictedOperator(op) {
				return true
			}
		}
	}
	return false
}

// invalidOperatorForTypeError builds the actionable message for an operator applied
// to an incompatible label type. It names the offending pair and lists the operators
// that ARE valid — intersecting what this provider supports with what the label's
// type accepts, so the suggestion is always directly usable. Mirrors the style of
// unknownLabelError.
func invalidOperatorForTypeError(source LogSource, field, op, dataType string) error {
	var valid []string
	for _, token := range source.GetSupportedOperators() {
		if !query.DataTypesApplyToType(effectiveDataTypes(token, source), dataType) {
			continue
		}
		valid = append(valid, operatorDisplayLabel(token))
	}
	sort.Strings(valid)

	if len(valid) == 0 {
		return fmt.Errorf("operator %q is not valid for label %q (type %s) with this log provider",
			operatorDisplayLabel(op), field, dataType)
	}
	return fmt.Errorf("operator %q is not valid for label %q (type %s); valid operators: %s",
		operatorDisplayLabel(op), field, dataType, strings.Join(valid, ", "))
}

// operatorDisplayLabel renders an operator the way the UI shows it ("=~" rather than
// "_regex"), falling back to the raw token when it has no descriptor.
func operatorDisplayLabel(token string) string {
	if d, ok := query.OperatorCatalog[token]; ok && d.ChipLabel != "" {
		return d.ChipLabel
	}
	return token
}
