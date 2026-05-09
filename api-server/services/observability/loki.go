package observability

import (
	"encoding/json"
	"fmt"
	"net/url"
	"nudgebee/services/common"
	"nudgebee/services/eventrule/playbooks"
	"nudgebee/services/query"
	"nudgebee/services/relay"
	"nudgebee/services/security"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// LokiSource is a LogSource implementation for Loki.
type LokiSource struct{}

// regexEscapePattern is compiled once for efficiency
var regexEscapePattern = regexp.MustCompile(`([.+*?^$()[\]{}|\\])`)

// Regex validation constants
const (
	maxRegexLength     = 500 // Maximum pattern length in characters
	maxRegexComplexity = 50  // Maximum count of special regex characters
)

// regexDangerousPatterns detects potentially malicious regex patterns (ReDoS attack vectors)
// Patterns like (a+)+, (.*)+, etc. can cause exponential time complexity in some engines
// Note: Go's RE2 engine is safe from ReDoS, but we still validate to prevent resource exhaustion
var regexDangerousPatterns = regexp.MustCompile(`\([^)]*[*+][^)]*\)[*+]|\([^)]*[*+][^)]*\)\{`)

// validateUserRegex validates user-provided regex patterns for safety
// Returns error if pattern is invalid, dangerous, or too complex
func validateUserRegex(pattern string) error {
	// 1. Check for empty pattern
	if pattern == "" {
		return fmt.Errorf("regex pattern cannot be empty")
	}

	// 2. Check length (prevent resource exhaustion)
	if len(pattern) > maxRegexLength {
		return fmt.Errorf("regex pattern too long (max %d characters)", maxRegexLength)
	}

	// 3. Count complexity (special regex chars that affect performance)
	complexity := strings.Count(pattern, "(") +
		strings.Count(pattern, "[") +
		strings.Count(pattern, "{") +
		strings.Count(pattern, "*") +
		strings.Count(pattern, "+")
	if complexity > maxRegexComplexity {
		return fmt.Errorf("regex pattern too complex (max %d special characters)", maxRegexComplexity)
	}

	// 4. Detect dangerous patterns (ReDoS attack vectors)
	// Patterns like (a+)+, (.*)+, etc. can cause exponential time complexity
	if regexDangerousPatterns.MatchString(pattern) {
		return fmt.Errorf("regex pattern contains potentially dangerous nested quantifiers")
	}

	// 5. Validate syntax by compiling
	// Note: Go's regexp package uses RE2, which is ReDoS-safe by design
	// No timeout needed - RE2 guarantees linear time complexity
	_, err := regexp.Compile(pattern)
	if err != nil {
		return fmt.Errorf("invalid regex syntax: %w", err)
	}

	return nil
}

var LogsFilterMap = map[string]string{
	"content": "log",
}

func (s *LokiSource) BuildLokiQuery(req LogsQueryBuilderRequest) (string, error) {
	var sb strings.Builder

	// 1. Labels selector (from Binary Eq/Nq that apply to labels)
	labelSelectors, err := extractLabelSelectors(req.Where)
	if err != nil {
		return "", fmt.Errorf("failed to extract label selectors: %w", err)
	}
	if labelSelectors == "" {
		sb.WriteString("{}")
	} else {
		sb.WriteString("{")
		sb.WriteString(labelSelectors)
		sb.WriteString("}")
	}

	// 2. Filters (from Binary like, contains, etc.)
	filterExpr, err := buildWhere(req.Where)
	if err != nil {
		return "", fmt.Errorf("failed to build where clause: %w", err)
	}
	if filterExpr != "" {
		sb.WriteString(" ")
		sb.WriteString(filterExpr)
	}

	return sb.String(), nil
}

// labelSelector represents a parsed label selector with field, operator, and value
type labelSelector struct {
	field    string
	operator string // "=", "!=", "=~", "!~"
	value    string
}

func extractLabelSelectors(where query.QueryWhereClause) (string, error) {
	selectors, err := extractLabelSelectorsRecursive(where)
	if err != nil {
		return "", err
	}

	// Merge selectors - combine same field with OR into regex alternation
	merged := mergeSelectors(selectors)

	// Loki requires at least one positive matcher (= or =~) whose value is not
	// empty-compatible.  When every selector is negative (!= or !~), pick the
	// first negated field and prepend a positive existence check (field=~".+")
	// so that Loki accepts the query.
	if len(merged) > 0 {
		hasPositive := false
		for _, sel := range merged {
			if sel.operator == "=" || sel.operator == "=~" {
				hasPositive = true
				break
			}
		}
		if !hasPositive {
			merged = append([]labelSelector{
				{field: merged[0].field, operator: "=~", value: ".+"},
			}, merged...)
		}
	}

	// Sort for deterministic output (helps with testing and debugging)
	sort.Slice(merged, func(i, j int) bool {
		if merged[i].field != merged[j].field {
			return merged[i].field < merged[j].field
		}
		return merged[i].operator < merged[j].operator
	})

	// Convert back to strings
	var parts []string
	for _, sel := range merged {
		parts = append(parts, fmt.Sprintf(`%s%s"%s"`, sel.field, sel.operator, sel.value))
	}

	return strings.Join(parts, ", "), nil
}

func extractLabelSelectorsRecursive(where query.QueryWhereClause) ([]labelSelector, error) {
	var selectors []labelSelector

	// Process Binary clauses
	binarySelectors, err := processBinaryLabelSelectors(where.Binary)
	if err != nil {
		return nil, err
	}
	selectors = append(selectors, binarySelectors...)

	// Process AND clauses
	andSelectors, err := processAndLabelSelectors(where.And)
	if err != nil {
		return nil, err
	}
	selectors = append(selectors, andSelectors...)

	// Process OR clauses
	orSelectors, err := processOrLabelSelectors(where.Or)
	if err != nil {
		return nil, err
	}
	selectors = append(selectors, orSelectors...)

	// Process NOT clause
	if where.Not != nil {
		notSelectors, err := processNotLabelSelectors(*where.Not)
		if err != nil {
			return nil, err
		}
		selectors = append(selectors, notSelectors...)
	}

	return selectors, nil
}

func processBinaryLabelSelectors(binary map[string]map[query.BinaryWhereClauseType]any) ([]labelSelector, error) {
	var selectors []labelSelector
	for field, ops := range binary {
		for op, val := range ops {
			selector, err := buildLabelSelector(field, op, val)
			if err != nil {
				return nil, err
			}
			if selector.field != "" {
				selectors = append(selectors, selector)
			}
		}
	}
	return selectors, nil
}

func buildLabelSelector(field string, op query.BinaryWhereClauseType, val any) (labelSelector, error) {
	// Skip "log" field - it should be handled by buildWhere as a line filter, not a label selector
	if field == "log" {
		return labelSelector{}, nil
	}

	switch op {
	case query.Eq:
		escaped := escapeLabelValue(fmt.Sprintf("%v", val))
		return labelSelector{field: field, operator: "=", value: escaped}, nil
	case query.Nq:
		escaped := escapeLabelValue(fmt.Sprintf("%v", val))
		return labelSelector{field: field, operator: "!=", value: escaped}, nil
	case query.In:
		arr, err := toStringArray(val)
		if err != nil {
			return labelSelector{}, fmt.Errorf("expected array for 'in' operator on field %s: %w", field, err)
		}
		// Escape each value for regex
		escaped := make([]string, len(arr))
		for i, v := range arr {
			escaped[i] = escapeRegexValue(v)
		}
		return labelSelector{field: field, operator: "=~", value: strings.Join(escaped, "|")}, nil
	case query.NotIn:
		arr, err := toStringArray(val)
		if err != nil {
			return labelSelector{}, fmt.Errorf("expected array for 'not in' operator on field %s: %w", field, err)
		}
		// Escape each value for regex
		escaped := make([]string, len(arr))
		for i, v := range arr {
			escaped[i] = escapeRegexValue(v)
		}
		return labelSelector{field: field, operator: "!~", value: strings.Join(escaped, "|")}, nil
	case query.Contains:
		// Substring match: wrap escaped value with .* (NO anchors)
		// Example: Contains("api") → =~".*api.*"
		escaped := escapeRegexValue(fmt.Sprintf("%v", val))
		escaped = escapeLabelValue(escaped) // Escape quotes to prevent LogQL injection
		return labelSelector{field: field, operator: "=~", value: fmt.Sprintf(".*%s.*", escaped)}, nil
	case query.Like:
		// SQL LIKE pattern to anchored regex (full string match)
		// Example: Like("api-%") → =~"^api-.*$"
		pattern := convertSQLLikeToRegex(fmt.Sprintf("%v", val))
		return labelSelector{field: field, operator: "=~", value: pattern}, nil
	case query.ILike:
		// Case-insensitive SQL LIKE pattern with anchors
		// Example: ILike("API-%") → =~"^(?i)API-.*$"
		// Note: (?i) must come after ^ for proper regex syntax
		pattern := convertSQLLikeToRegex(fmt.Sprintf("%v", val))
		// Remove anchors, add (?i), re-add anchors
		patternWithoutAnchors := strings.TrimPrefix(strings.TrimSuffix(pattern, "$"), "^")
		return labelSelector{field: field, operator: "=~", value: fmt.Sprintf("^(?i)%s$", patternWithoutAnchors)}, nil
	case query.NLike:
		// Negative SQL LIKE pattern with anchors
		// Example: NLike("test-%") → !~"^test-.*$"
		pattern := convertSQLLikeToRegex(fmt.Sprintf("%v", val))
		return labelSelector{field: field, operator: "!~", value: pattern}, nil
	case query.IContains:
		// Case-insensitive substring match: wrap escaped value with .* and (?i)
		// Example: IContains("api") → =~"(?i).*api.*"
		// Note: (?i) at the start enables case-insensitive mode for the entire pattern
		sval := fmt.Sprintf("%v", val)
		if sval == "" {
			// Empty string: skip filter (return empty selector)
			return labelSelector{}, nil
		}
		escaped := escapeRegexValue(sval)   // Escape regex metacharacters
		escaped = escapeLabelValue(escaped) // Escape quotes to prevent LogQL injection
		return labelSelector{field: field, operator: "=~", value: fmt.Sprintf("(?i).*%s.*", escaped)}, nil
	case query.NIContains:
		// Negation of IContains
		// Example: NIContains("debug") → !~"(?i).*debug.*"
		sval := fmt.Sprintf("%v", val)
		if sval == "" {
			// Empty string: skip filter
			return labelSelector{}, nil
		}
		escaped := escapeRegexValue(sval)
		escaped = escapeLabelValue(escaped)
		return labelSelector{field: field, operator: "!~", value: fmt.Sprintf("(?i).*%s.*", escaped)}, nil
	case query.Regex:
		// User-provided raw regex (NO SQL LIKE conversion, NO anchoring)
		// Security: validate before use to prevent ReDoS attacks
		// Example: Regex("error|warning") → =~"error|warning"
		pattern := fmt.Sprintf("%v", val)
		if err := validateUserRegex(pattern); err != nil {
			return labelSelector{}, fmt.Errorf("invalid regex pattern for field %s: %w", field, err)
		}
		// Escape quotes only (user is responsible for regex syntax)
		escaped := escapeLabelValue(pattern)
		return labelSelector{field: field, operator: "=~", value: escaped}, nil
	case query.NRegex:
		// Negation of Regex
		// Example: NRegex("test-.*") → !~"test-.*"
		pattern := fmt.Sprintf("%v", val)
		if err := validateUserRegex(pattern); err != nil {
			return labelSelector{}, fmt.Errorf("invalid regex pattern for field %s: %w", field, err)
		}
		escaped := escapeLabelValue(pattern)
		return labelSelector{field: field, operator: "!~", value: escaped}, nil
	}
	return labelSelector{}, nil
}

func processAndLabelSelectors(andClauses []query.QueryWhereClause) ([]labelSelector, error) {
	var selectors []labelSelector
	for _, andClause := range andClauses {
		andSelectors, err := extractLabelSelectorsRecursive(andClause)
		if err != nil {
			return nil, err
		}
		selectors = append(selectors, andSelectors...)
	}
	return selectors, nil
}

func processOrLabelSelectors(orClauses []query.QueryWhereClause) ([]labelSelector, error) {
	if len(orClauses) == 0 {
		return nil, nil
	}

	// Collect all selectors from OR clauses
	var allSelectors []labelSelector
	for _, orClause := range orClauses {
		orSelectors, err := extractLabelSelectorsRecursive(orClause)
		if err != nil {
			return nil, err
		}
		allSelectors = append(allSelectors, orSelectors...)
	}

	// Group by field to check if we can merge into regex alternation
	fieldGroups := make(map[string][]labelSelector)
	for _, sel := range allSelectors {
		fieldGroups[sel.field] = append(fieldGroups[sel.field], sel)
	}

	// If multiple fields in OR, we can't represent this in Loki label selectors
	// For now, we'll return all selectors and let them be joined (best effort)
	// Note: This is a limitation of LogQL - true cross-field OR needs line filters
	return allSelectors, nil
}

func processNotLabelSelectors(notClause query.QueryWhereClause) ([]labelSelector, error) {
	notSelectors, err := extractLabelSelectorsRecursive(notClause)
	if err != nil {
		return nil, err
	}

	// Negate each selector
	negated := make([]labelSelector, len(notSelectors))
	for i, sel := range notSelectors {
		negated[i] = negateLabelSelector(sel)
	}
	return negated, nil
}

func negateLabelSelector(sel labelSelector) labelSelector {
	switch sel.operator {
	case "=":
		return labelSelector{field: sel.field, operator: "!=", value: sel.value}
	case "!=":
		return labelSelector{field: sel.field, operator: "=", value: sel.value}
	case "=~":
		return labelSelector{field: sel.field, operator: "!~", value: sel.value}
	case "!~":
		return labelSelector{field: sel.field, operator: "=~", value: sel.value}
	default:
		return sel
	}
}

func mergeSelectors(selectors []labelSelector) []labelSelector {
	// Group by field and operator
	type key struct {
		field    string
		operator string
	}
	groups := make(map[key][]string)

	for _, sel := range selectors {
		k := key{field: sel.field, operator: sel.operator}
		groups[k] = append(groups[k], sel.value)
	}

	// Merge values for same field+operator using regex alternation
	var merged []labelSelector
	for k, values := range groups {
		// Edge case: skip empty value arrays (defensive programming)
		if len(values) == 0 {
			continue
		}

		if len(values) == 1 {
			merged = append(merged, labelSelector{
				field:    k.field,
				operator: k.operator,
				value:    values[0],
			})
		} else {
			merged = append(merged, mergeMultipleValues(k.field, k.operator, values))
		}
	}

	return merged
}

func mergeMultipleValues(field, operator string, values []string) labelSelector {
	// For = and !=, convert to regex and re-escape values
	if operator == "=" {
		return labelSelector{
			field:    field,
			operator: "=~",
			value:    convertLiteralToRegexValues(values),
		}
	}
	if operator == "!=" {
		return labelSelector{
			field:    field,
			operator: "!~",
			value:    convertLiteralToRegexValues(values),
		}
	}
	// Already regex, values are already regex-escaped, just merge
	return labelSelector{
		field:    field,
		operator: operator,
		value:    strings.Join(values, "|"),
	}
}

func convertLiteralToRegexValues(literalValues []string) string {
	// Values are literal-escaped, need to be regex-escaped for regex operator
	regexValues := make([]string, len(literalValues))
	for i, v := range literalValues {
		// Unescape literal quotes first, then escape for regex
		unescaped := strings.ReplaceAll(v, `\"`, `"`)
		regexValues[i] = escapeRegexValue(unescaped)
	}
	return strings.Join(regexValues, "|")
}

// escapeLabelValue escapes quotes in label values
func escapeLabelValue(s string) string {
	return strings.ReplaceAll(s, `"`, `\"`)
}

// escapeRegexValue escapes regex metacharacters in values
func escapeRegexValue(s string) string {
	// Escape regex special characters using pre-compiled pattern
	return regexEscapePattern.ReplaceAllString(s, `\$1`)
}

// convertSQLLikeToRegex converts SQL LIKE pattern to anchored regex pattern
// SQL LIKE: % = zero or more chars, _ = single char, \% = literal %, \_ = literal _, \\ = literal \
// Returns pattern with ^ and $ anchors for full string matching (SQL LIKE semantics)
func convertSQLLikeToRegex(pattern string) string {
	// Step 1: Replace escaped characters with placeholders to protect them
	// Use placeholders that won't be affected by wildcard conversion
	// Order matters: process \\ first, then \%, then \_
	pattern = strings.ReplaceAll(pattern, `\\`, "<<<BACKSLASH>>>")
	pattern = strings.ReplaceAll(pattern, `\%`, "<<<PERCENT>>>")
	pattern = strings.ReplaceAll(pattern, `\_`, "<<<UNDERSCORE>>>")

	// Step 2: Escape regex metacharacters (but not % and _)
	// This escapes: . + * ? ^ $ ( ) [ ] { } | and any remaining \
	pattern = regexEscapePattern.ReplaceAllString(pattern, `\$1`)

	// Step 3: Convert SQL wildcards to regex
	pattern = strings.ReplaceAll(pattern, "%", ".*")
	pattern = strings.ReplaceAll(pattern, "_", ".")

	// Step 4: Restore escaped literals from placeholders
	// For backslash: need to double-escape for regex (SQL \\ → regex \\\\)
	pattern = strings.ReplaceAll(pattern, "<<<PERCENT>>>", "%")
	pattern = strings.ReplaceAll(pattern, "<<<UNDERSCORE>>>", "_")
	pattern = strings.ReplaceAll(pattern, "<<<BACKSLASH>>>", `\\\\`)

	// Step 5: Escape quotes to prevent LogQL injection
	pattern = escapeLabelValue(pattern)

	// Step 6: Add anchors for full string match (SQL LIKE semantics)
	return fmt.Sprintf("^%s$", pattern)
}

// toStringArray converts various array types to []string
func toStringArray(val any) ([]string, error) {
	switch v := val.(type) {
	case []string:
		return v, nil
	case []interface{}:
		result := make([]string, len(v))
		for i, item := range v {
			result[i] = fmt.Sprintf("%v", item)
		}
		return result, nil
	default:
		return nil, fmt.Errorf("expected array, got %T", val)
	}
}

func buildWhere(where query.QueryWhereClause) (string, error) {
	var parts []string

	// Binary filters on log content
	for field, ops := range where.Binary {
		for op, val := range ops {
			sval := fmt.Sprintf("%v", val)

			if field == "log" { // interpret "log" as message content filter
				switch op {
				case query.Contains, query.Eq:
					// Contains: substring match using |= (no regex needed)
					// Escape quotes to prevent LogQL injection
					escaped := escapeLabelValue(sval)
					parts = append(parts, fmt.Sprintf(`|= "%s"`, escaped))
				case query.Nq:
					// Not contains: negative substring match using !=
					escaped := escapeLabelValue(sval)
					parts = append(parts, fmt.Sprintf(`!= "%s"`, escaped))
				case query.Like:
					// SQL LIKE pattern: convert to regex with anchors
					// Use |~ for regex matching
					pattern := convertSQLLikeToRegex(sval)
					parts = append(parts, fmt.Sprintf(`|~ "%s"`, pattern))
				case query.ILike:
					// Case-insensitive SQL LIKE pattern
					pattern := convertSQLLikeToRegex(sval)
					// Remove anchors, add (?i), re-add anchors
					patternWithoutAnchors := strings.TrimPrefix(strings.TrimSuffix(pattern, "$"), "^")
					parts = append(parts, fmt.Sprintf(`|~ "^(?i)%s$"`, patternWithoutAnchors))
				case query.NLike:
					// Negative SQL LIKE pattern: convert to regex with anchors
					// Use !~ for negative regex matching
					pattern := convertSQLLikeToRegex(sval)
					parts = append(parts, fmt.Sprintf(`!~ "%s"`, pattern))
				case query.IContains:
					// Case-insensitive substring match using regex with (?i) flag
					// Example: IContains("error") → |~ "(?i).*error.*"
					// Note: NO anchors, NO SQL LIKE conversion
					if sval == "" {
						// Empty string: skip filter (no-op)
						continue
					}
					escaped := escapeRegexValue(sval)   // Escape regex metacharacters
					escaped = escapeLabelValue(escaped) // Escape quotes to prevent LogQL injection
					parts = append(parts, fmt.Sprintf(`|~ "(?i).*%s.*"`, escaped))
				case query.NIContains:
					// Negation of IContains
					if sval == "" {
						// Empty string: skip filter
						continue
					}
					escaped := escapeRegexValue(sval)
					escaped = escapeLabelValue(escaped)
					parts = append(parts, fmt.Sprintf(`!~ "(?i).*%s.*"`, escaped))
				case query.Regex:
					// User-provided raw regex pattern for line matching
					// Security: validate before use
					if err := validateUserRegex(sval); err != nil {
						return "", fmt.Errorf("invalid regex pattern for log filter: %w", err)
					}
					escaped := escapeLabelValue(sval) // Only escape quotes
					parts = append(parts, fmt.Sprintf(`|~ "%s"`, escaped))
				case query.NRegex:
					// Negation of Regex
					if err := validateUserRegex(sval); err != nil {
						return "", fmt.Errorf("invalid regex pattern for log filter: %w", err)
					}
					escaped := escapeLabelValue(sval)
					parts = append(parts, fmt.Sprintf(`!~ "%s"`, escaped))
				}
			}
		}
	}

	// AND
	for _, andClause := range where.And {
		andPart, err := buildWhere(andClause)
		if err != nil {
			return "", err
		}
		parts = append(parts, andPart)
	}

	// OR
	if len(where.Or) > 0 {
		var orParts []string
		for _, orClause := range where.Or {
			orPart, err := buildWhere(orClause)
			if err != nil {
				return "", err
			}
			orParts = append(orParts, orPart)
		}
		parts = append(parts, "("+strings.Join(orParts, " or ")+")")
	}

	// NOT
	if where.Not != nil {
		notPart, err := buildWhere(*where.Not)
		if err != nil {
			return "", err
		}
		parts = append(parts, "!("+notPart+")")
	}

	return strings.Join(parts, " "), nil
}

func (s *LokiSource) BuildLokiAPIRequest(req FetchLogRequest) (string, error) {
	// Build the core LogQL query string
	logql, err := s.BuildLokiQuery(req.QueryRequest)
	if err != nil {
		return "", fmt.Errorf("failed to build loki query: %w", err)
	}
	params := url.Values{}
	params.Set("query", logql)

	// Limit
	if req.Limit > 0 {
		params.Set("limit", strconv.Itoa(req.Limit))
	}

	// Step interval
	if req.StepInterval > 0 {
		params.Set("step", strconv.FormatInt(int64(req.StepInterval), 10))
	}

	// Start / End
	if req.StartTime > 0 {
		params.Set("start", strconv.FormatInt(req.StartTime*1000000, 10))
	}
	if req.EndTime > 0 {
		params.Set("end", strconv.FormatInt(req.EndTime*1000000, 10))
	}

	// direction (default = backward)
	params.Set("direction", "backward")

	// Build final URL
	fullURL := params.Encode()
	return fullURL, nil
}

// ensureCompleteLokiQuery ensures that the query string in FetchLogRequest is a complete
// Loki API query URL. If the query is empty, it builds one from the request's other attributes.
// If the query is partial, it fills in the missing parameters like start, end, limit, etc.
func (s *LokiSource) ensureCompleteLokiQuery(fetchLogRequest *FetchLogRequest) (string, error) {
	if fetchLogRequest.Query == "" {
		// If the query string is empty, build it entirely from the FetchLogRequest fields.
		return s.BuildLokiAPIRequest(*fetchLogRequest)
	}

	// If fetchLogRequest.Query is provided, ensure it's a well-formed Loki API query.
	// It might be a raw LogQL query or a partial URL query.
	parsedExistingQuery, err := url.ParseQuery(fetchLogRequest.Query)
	if err != nil {
		// If parsing fails, assume the entire fetchLogRequest.Query is the LogQL part.
		parsedExistingQuery = url.Values{}
		parsedExistingQuery.Set("query", fetchLogRequest.Query)
	}

	// Extract the LogQL part from the parsed query.
	logql := parsedExistingQuery.Get("query")
	if logql == "" {
		// If no 'query' parameter was found, assume the original fetchLogRequest.Query was the LogQL.
		logql = fetchLogRequest.Query
	}

	// Create new URL values to construct the final query string.
	finalParams := url.Values{}
	finalParams.Set("query", logql) // Always set the LogQL part

	// Use existing parameters from the query string, but let fetchLogRequest fields fill in if the param is missing.
	if val := parsedExistingQuery.Get("start"); val != "" {
		finalParams.Set("start", val)
	} else if fetchLogRequest.StartTime > 0 {
		finalParams.Set("start", strconv.FormatInt(fetchLogRequest.StartTime*1000000, 10))
	}

	if val := parsedExistingQuery.Get("end"); val != "" {
		finalParams.Set("end", val)
	} else if fetchLogRequest.EndTime > 0 {
		finalParams.Set("end", strconv.FormatInt(fetchLogRequest.EndTime*1000000, 10))
	}

	if val := parsedExistingQuery.Get("limit"); val != "" {
		finalParams.Set("limit", val)
	} else if fetchLogRequest.Limit > 0 {
		finalParams.Set("limit", strconv.Itoa(fetchLogRequest.Limit))
	}

	if val := parsedExistingQuery.Get("step"); val != "" {
		finalParams.Set("step", val)
	} else if fetchLogRequest.StepInterval > 0 {
		finalParams.Set("step", strconv.FormatInt(int64(fetchLogRequest.StepInterval), 10))
	}

	if val := parsedExistingQuery.Get("direction"); val != "" {
		finalParams.Set("direction", val)
	} else {
		finalParams.Set("direction", "backward") // Default direction
	}

	return finalParams.Encode(), nil
}

func (s *LokiSource) QueryLogs(ctx *security.RequestContext, fetchLogRequest FetchLogRequest) ([]OutputLog, error) {
	var err error

	// Default limit before building the query so it gets included in the Loki API request.
	if fetchLogRequest.Limit == 0 {
		fetchLogRequest.Limit = 5000
	} else if fetchLogRequest.Limit > 5000 {
		return nil, fmt.Errorf("loki: limit exceeds maximum of 5000")
	}

	// Check if structured query_request is provided (builder mode)
	if !isEmptyWhereClause(fetchLogRequest.QueryRequest.Where) {
		// Build LogQL query from structured request
		lokiQuery, err := s.BuildLokiQuery(fetchLogRequest.QueryRequest)
		if err != nil {
			return nil, fmt.Errorf("failed to build loki query from query_request: %w", err)
		}
		// Store the built query so ensureCompleteLokiQuery can add time range, limit, etc.
		fetchLogRequest.Query = lokiQuery
	}

	fetchLogRequest.Query, err = s.ensureCompleteLokiQuery(&fetchLogRequest)
	if err != nil {
		return nil, fmt.Errorf("failed to build complete loki query: %w", err)
	}

	lokiRequest := relay.ActionExecuteBody{
		AccountID:  fetchLogRequest.AccountId,
		ActionName: "query_loki",
		ActionParams: map[string]any{
			"query": fetchLogRequest.Query,
		},
		NoSinks: true,
	}

	resp, err := relay.Execute(relay.RelayExecuteRequest{
		NoSinks: true,
		Cache:   false,
		Body:    lokiRequest,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to execute loki query: %w", err)
	}

	if dataStr, ok := resp["data"].(string); ok {
		return nil, fmt.Errorf("loki.QueryLogs received an error response from the service: %s", dataStr)
	}

	data1, ok := resp["data"].(map[string]any)
	if !ok || data1 == nil {
		return nil, fmt.Errorf("loki.QueryLogs data1 field not found or is nil from response: %v", resp["data"])
	}
	data2, ok := data1["data"]
	if !ok || data2 == nil {
		return nil, fmt.Errorf("loki.QueryLogs data2 field not found or is nil from response: %v", data1["data"])
	}
	if v, ok := data2.(string); ok {
		return nil, fmt.Errorf("loki.QueryLogs received an error response from the service: %s", v)
	}
	var data3 map[string]any
	switch v := data2.(type) {
	case string:
		return nil, fmt.Errorf("loki.QueryLogs received an error response from the service: %s", v)
	case map[string]any:
		var ok bool
		data3, ok = v["data"].(map[string]any)
		if !ok || data3 == nil {
			return nil, fmt.Errorf("loki.QueryLogs data3 field not found, is not a map, or is nil from response: %v", resp["data"])
		}
	default:
		return nil, fmt.Errorf("loki.QueryLogs unexpected type for data2 field from response: %T", v)
	}
	data, err := common.MarshalJson(data3)
	if err != nil {
		return nil, fmt.Errorf("loki.QueryLogs failed to marshal json: %w", err)
	}
	outputLog, err := s.convertLokiResponse(data)
	if err != nil {
		return nil, fmt.Errorf("loki.QueryLogs failed to convert loki response in nudgebee log: %w", err)
	}
	return outputLog, nil
}

func (s *LokiSource) ExtractDataSliceFromRelay(resp map[string]any) ([]any, error) {
	data, ok := resp["data"].(map[string]any)
	if !ok || data == nil {
		return nil, fmt.Errorf("'data' field not found or is nil in response")
	}
	nestedData, ok := data["data"].(map[string]any)
	if !ok || nestedData == nil {
		return nil, fmt.Errorf("nested 'data' field not found or is nil in response: %v", data["data"])
	}
	values, ok := nestedData["data"].([]interface{})
	if !ok || values == nil {
		return nil, fmt.Errorf("expected 'data' to be a slice but it was not or was nil")
	}
	return values, nil
}

func (s *LokiSource) QueryLabels(ctx *security.RequestContext, fetchLogRequest FetchLogLabelRequest) ([]OutputLogLabel, error) {

	query := ""
	if fetchLogRequest.Request != nil {
		if queryStr, ok := fetchLogRequest.Request["query"].(string); ok {
			query = queryStr
		}
	}

	if query == "" {
		query = fmt.Sprintf("start=%d&end=%d", fetchLogRequest.StartTime*1_000_000, fetchLogRequest.EndTime*1_000_000)
	}

	lokiRequest := relay.ActionExecuteBody{
		AccountID:  fetchLogRequest.AccountId,
		ActionName: "query_loki_labels",
		ActionParams: map[string]any{
			"query": query,
		},
		NoSinks: true,
	}

	resp, err := relay.Execute(relay.RelayExecuteRequest{
		NoSinks: true,
		Cache:   false,
		Body:    lokiRequest,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to execute loki query: %w", err)
	}

	data3, err := s.ExtractDataSliceFromRelay(resp)
	if err != nil {
		return nil, err
	}

	var output []OutputLogLabel
	for _, v := range data3 {
		if str, ok := v.(string); ok {
			output = append(output, OutputLogLabel{
				Label:      str,
				Attributes: map[string]interface{}{},
			})
		}
	}
	return output, nil
}

func (s *LokiSource) QueryLabelValues(ctx *security.RequestContext, fetchLogRequest FetchLogLabelValuesRequest) ([]OutputLogLabelValue, error) {
	lokiRequest := relay.ActionExecuteBody{
		AccountID:  fetchLogRequest.AccountId,
		ActionName: "query_grafana_loki_label_values",
		ActionParams: map[string]any{
			"query": fetchLogRequest.Request["query"],
			"label": fetchLogRequest.LabelName,
		},
		NoSinks: true,
	}

	resp, err := relay.Execute(relay.RelayExecuteRequest{
		NoSinks: true,
		Cache:   false,
		Body:    lokiRequest,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to execute loki query: %w", err)
	}

	data3, err := s.ExtractDataSliceFromRelay(resp)
	if err != nil {
		return nil, err
	}

	var output []OutputLogLabelValue
	for _, v := range data3 {
		if str, ok := v.(string); ok {
			output = append(output, OutputLogLabelValue{
				Value:      str,
				Attributes: map[string]interface{}{},
			})
		}
	}
	return output, nil
}

func (s *LokiSource) GetQuery(ctx *security.RequestContext, fetchLogRequest FetchLogRequest) (string, error) {
	query, err := s.BuildLokiQuery(fetchLogRequest.QueryRequest)
	if err != nil {
		return "", fmt.Errorf("failed to build loki query: %w", err)
	}
	return query, nil
}

func (s *LokiSource) GetLabelMapping() map[string]string {
	return LogsFilterMap
}

func (s *LokiSource) GetSupportedOperators() []string {
	return []string{"_eq", "_neq", "_in", "_not_in", "_contains", "_like", "_ilike", "_nlike", "_icontains", "_nicontains", "_regex", "_nregex"}
}

func (s *LokiSource) CanGenerateQuery(ctx playbooks.PlaybookActionContext) bool {
	return ctx.GetEvent().SubjectName != "" &&
		getEventNamespace(ctx.GetEvent()) != ""
}

func (s *LokiSource) GenerateQuery(ctx playbooks.PlaybookActionContext) (string, map[string]any, error) {
	workloadName := escapeLokiLabelValue(ctx.GetEvent().SubjectName)
	namespace := escapeLokiLabelValue(getEventNamespace(ctx.GetEvent()))

	// Use regex to match pods belonging to this workload
	kind := ""
	if ctx.GetEvent().Labels != nil {
		kind = ctx.GetEvent().Labels["kind"]
	}
	if kind == "" {
		kind = ctx.GetEvent().SubjectType
	}
	podMatcher := fmt.Sprintf(`pod=~"%s-.*"`, workloadName)
	if strings.EqualFold(kind, "pod") || kind == "" {
		podMatcher = fmt.Sprintf(`pod="%s"`, workloadName)
	}

	logql := fmt.Sprintf(`{%s, namespace="%s"}`, podMatcher, namespace)
	return logql, map[string]any{}, nil
}

// escapeLokiLabelValue escapes special characters in LogQL label values.
func escapeLokiLabelValue(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return s
}

// convertLokiResponse converts Loki response to the desired format
func (s *LokiSource) convertLokiResponse(lokiData []byte) ([]OutputLog, error) {
	var lokiResponse LokiResponse
	if err := json.Unmarshal(lokiData, &lokiResponse); err != nil {
		return nil, fmt.Errorf("failed to unmarshal Loki response: %w", err)
	}

	logEntries := []OutputLog{}

	for _, result := range lokiResponse.Result {
		// Use stream labels for log queries, metric labels for metric queries.
		labels := result.Stream
		if labels == nil {
			labels = result.Metric
		}

		for _, value := range result.Values {
			if len(value) != 2 {
				continue // Skip invalid entries
			}

			// Parse timestamp: can be a JSON string (stream/log queries: nanoseconds)
			// or a JSON number (metric queries: unix seconds as float).
			var timestamp string
			var tsStr string
			if err := json.Unmarshal(value[0], &tsStr); err == nil {
				// Stream query: timestamp is a string of nanoseconds.
				timestampNs, err := strconv.ParseInt(tsStr, 10, 64)
				if err != nil {
					continue
				}
				timestamp = time.Unix(0, timestampNs).UTC().Format(time.RFC3339Nano)
			} else {
				// Metric query: timestamp is a float (unix seconds).
				var tsFloat float64
				if err := json.Unmarshal(value[0], &tsFloat); err != nil {
					continue
				}
				sec := int64(tsFloat)
				nsec := int64((tsFloat - float64(sec)) * 1e9)
				timestamp = time.Unix(sec, nsec).UTC().Format(time.RFC3339Nano)
			}

			// Parse the second element: always a string in both response types.
			var message string
			if err := json.Unmarshal(value[1], &message); err != nil {
				continue
			}

			severity := s.extractSeverity(message)

			logEntry := OutputLog{
				Timestamp: timestamp,
				Message:   message,
				Labels:    labels,
				Severity:  severity,
			}

			logEntries = append(logEntries, logEntry)
		}
	}

	return logEntries, nil
}

// extractSeverity attempts to extract severity level from the log message
func (s *LokiSource) extractSeverity(message string) string {
	messageLower := strings.ToLower(message)
	if strings.Contains(messageLower, "error") || strings.Contains(messageLower, "err") {
		return "error"
	} else if strings.Contains(messageLower, "warn") {
		return "warning"
	} else if strings.Contains(messageLower, "info") {
		return "info"
	} else if strings.Contains(messageLower, "debug") {
		return "debug"
	} else if strings.Contains(messageLower, "fatal") {
		return "fatal"
	} else if strings.Contains(messageLower, "trace") {
		return "trace"
	}

	if strings.Contains(messageLower, "\"levelname\"") {
		if strings.Contains(messageLower, "\"info\"") {
			return "info"
		} else if strings.Contains(messageLower, "\"error\"") {
			return "error"
		} else if strings.Contains(messageLower, "\"warning\"") {
			return "warning"
		} else if strings.Contains(messageLower, "\"debug\"") {
			return "debug"
		}
	}
	return "info"
}

// LokiResponse represents the structure of the Loki logs response
type LokiResponse struct {
	ResultType string `json:"resultType"`
	Result     []struct {
		Stream map[string]interface{} `json:"stream"`
		Metric map[string]interface{} `json:"metric"`
		Values [][]json.RawMessage    `json:"values"`
	} `json:"result"`
}

// LogEntry represents the desired output format
type LogEntry struct {
	Timestamp  string            `json:"timestamp"`
	Message    string            `json:"message"`
	StreamTags map[string]string `json:"streams_tags"`
	Severity   string            `json:"severity"`
}

// QueryLogGroup implements LogGroupSource for Loki.
// Uses LogQL metric queries (count_over_time + sum by) to aggregate error/critical logs
// grouped by namespace, pod, and level.
func (s *LokiSource) QueryLogGroup(ctx *security.RequestContext, req FetchLogGroupRequest) (LogGroupOutput, error) {
	// Fetch raw error log lines from Loki using a stream query, then group by message pattern in-memory.
	logql := s.buildLogGroupStreamQuery(req)

	logRequest := FetchLogRequest{
		AccountId: req.AccountId,
		Query:     logql,
		StartTime: req.StartTime,
		EndTime:   req.EndTime,
		Limit:     1000,
	}

	logs, err := s.QueryLogs(ctx, logRequest)
	if err != nil {
		return LogGroupOutput{}, fmt.Errorf("loki.QueryLogGroup: failed to fetch logs: %w", err)
	}

	return s.groupLogsByPattern(logs, req.EndTime), nil
}

// buildLogGroupStreamQuery builds a LogQL stream query that fetches error/critical/fatal logs.
func (s *LokiSource) buildLogGroupStreamQuery(req FetchLogGroupRequest) string {
	selector := `namespace=~".+"`

	selectedNamespace := common.GetString(req.Request, "selectedNamespace")
	selectedWorkload := common.GetString(req.Request, "selectedWorkload")

	if selectedNamespace != "" {
		selector = fmt.Sprintf(`namespace="%s"`, escapeLokiLabelValue(selectedNamespace))
	}
	if selectedWorkload != "" {
		selector += fmt.Sprintf(`, pod=~"%s-.*"`, escapeLokiLabelValue(selectedWorkload))
	}

	return fmt.Sprintf(`{%s} |~ "(?i)(error|critical|fatal)"`, selector)
}

// groupLogsByPattern groups raw log entries by message pattern hash and returns LogGroupOutput.
func (s *LokiSource) groupLogsByPattern(logs []OutputLog, endTime int64) LogGroupOutput {
	type groupEntry struct {
		sample    string
		namespace string
		workload  string
		container string
		level     string
		count     int64
	}

	grouped := make(map[string]*groupEntry) // keyed by hash|namespace|workload|level

	for _, log := range logs {
		if log.Message == "" {
			continue
		}

		hash := generatePatternHash(log.Message)
		namespace, _ := log.Labels["namespace"].(string)
		pod, _ := log.Labels["pod"].(string)
		container, _ := log.Labels["container"].(string)
		workload := extractWorkloadFromPodName(pod)
		level := log.Severity

		compositeKey := hash + "|" + namespace + "|" + workload + "|" + level

		entry, exists := grouped[compositeKey]
		if !exists {
			// Truncate sample for display
			sample := log.Message
			if runes := []rune(sample); len(runes) > 500 {
				sample = string(runes[:500])
			}

			entry = &groupEntry{
				sample:    sample,
				namespace: namespace,
				workload:  workload,
				container: container,
				level:     level,
			}
			grouped[compositeKey] = entry
		}
		entry.count++
	}

	// Use the end of the query window as the single timestamp.
	var endTimeSec int64
	if endTime <= 0 {
		endTimeSec = time.Now().Unix()
	} else if endTime >= 1e12 {
		endTimeSec = endTime / 1000
	} else {
		endTimeSec = endTime
	}

	groups := make([]LogGroup, 0, len(grouped))
	for _, entry := range grouped {
		containerID := ""
		if entry.namespace != "" && entry.workload != "" {
			containerID = fmt.Sprintf("/k8s/%s/%s", entry.namespace, entry.workload)
		}

		level := entry.level
		if level == "" {
			level = "error"
		}

		groups = append(groups, LogGroup{
			Sample:      entry.sample,
			Namespace:   entry.namespace,
			Workload:    entry.workload,
			Container:   entry.container,
			ContainerID: containerID,
			PatternHash: generatePatternHash(entry.sample),
			Level:       level,
			Count:       entry.count,
			Timestamps:  []int64{endTimeSec},
			Values:      []float64{float64(entry.count)},
		})
	}

	// Sort by count descending
	sort.Slice(groups, func(i, j int) bool {
		return groups[i].Count > groups[j].Count
	})

	// Limit to top 100 groups
	if len(groups) > 100 {
		groups = groups[:100]
	}

	return LogGroupOutput{Groups: groups}
}
