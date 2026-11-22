package observability

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"nudgebee/services/query"
)

// SolarWinds log search cannot express every filter exactly (live probes, 2026-10-07):
//   - an unquoted term is a case-insensitive substring match, so _contains (case-sensitive)
//     and _like with an anchor ("canary%", "%-0") have no exact form;
//   - on the Kubernetes attributes (k8s.*, service.name) even a quoted value is a substring
//     match: k8s.namespace.name:"nudgebee" also returns nudgebee-agent-dev pods, and no other
//     spelling (=, regex, ^$) narrows it.
//
// So a filter is executed in two halves. The search string sent to SolarWinds is rendered so
// it returns a SUPERSET of the rows the filter means, and every returned row is then checked
// against the filter's exact meaning on the fields the row carries (host, program, message)
// or that can be looked up for it (namespace, workload). A filter on a field that can neither
// be searched exactly nor checked afterwards is refused rather than answered wrongly.

// swClauseKind tells a rendered search clause apart from the constants true and false, which
// SolarWinds search cannot spell and which simplify away in AND / OR / NOT.
type swClauseKind int8

const (
	swExpr swClauseKind = iota
	swTrue
	swFalse
)

type swClause struct {
	kind swClauseKind
	expr string
}

func swExprClause(expr string) swClause {
	if expr == "" {
		return swClause{kind: swTrue}
	}
	return swClause{expr: expr}
}

// swRenderMode says which side of the exact filter a rendering may err on. A positive term
// needs a superset; under a negation it needs a subset, so that NOT(subset) is a superset.
// A term that has no exact search form renders as false in subset mode: NOT(false) fetches
// everything and leaves the exclusion to the row check, instead of over-excluding.
type swRenderMode bool

const (
	swSuperset swRenderMode = true
	swSubset   swRenderMode = false
)

func swAnd(parts []swClause, paren bool) swClause {
	exprs := make([]string, 0, len(parts))
	for _, p := range parts {
		switch p.kind {
		case swFalse:
			return swClause{kind: swFalse}
		case swExpr:
			exprs = append(exprs, p.expr)
		}
	}
	if len(exprs) == 0 {
		return swClause{kind: swTrue}
	}
	joined := strings.Join(exprs, " AND ")
	if paren {
		joined = "(" + joined + ")"
	}
	return swClause{expr: joined}
}

func swOr(parts []swClause) swClause {
	exprs := make([]string, 0, len(parts))
	for _, p := range parts {
		switch p.kind {
		case swTrue:
			return swClause{kind: swTrue}
		case swExpr:
			exprs = append(exprs, p.expr)
		}
	}
	if len(exprs) == 0 {
		return swClause{kind: swFalse}
	}
	return swClause{expr: "(" + strings.Join(exprs, " OR ") + ")"}
}

func swNot(c swClause) swClause {
	switch c.kind {
	case swTrue:
		return swClause{kind: swFalse}
	case swFalse:
		return swClause{kind: swTrue}
	}
	if swIsGroup(c.expr) {
		return swClause{expr: "-" + c.expr}
	}
	return swClause{expr: swNegate(c.expr)}
}

// swIsGroup reports whether expr is one parenthesised group, "(a OR b)", so negating it
// needs no second pair. Quoted values may contain parentheses, so quotes are skipped.
func swIsGroup(expr string) bool {
	if !strings.HasPrefix(expr, "(") || !strings.HasSuffix(expr, ")") {
		return false
	}
	depth, quoted := 0, false
	for i := 0; i < len(expr); i++ {
		switch c := expr[i]; {
		case quoted && c == '\\':
			i++
		case c == '"':
			quoted = !quoted
		case !quoted && c == '(':
			depth++
		case !quoted && c == ')':
			depth--
			if depth == 0 && i != len(expr)-1 {
				return false
			}
		}
	}
	return depth == 0
}

// renderSWWhere renders a where tree in the given mode. Binary fields and operators are
// visited in sorted order so the same filter always renders the same string.
func renderSWWhere(w query.QueryWhereClause, mode swRenderMode, env *swRenderEnv) (swClause, error) {
	var parts []swClause

	fields := make([]string, 0, len(w.Binary))
	for field := range w.Binary {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	for _, field := range fields {
		ops := make([]string, 0, len(w.Binary[field]))
		for op := range w.Binary[field] {
			ops = append(ops, string(op))
		}
		sort.Strings(ops)
		for _, op := range ops {
			clause, err := renderSWLeaf(field, query.BinaryWhereClauseType(op), w.Binary[field][query.BinaryWhereClauseType(op)], mode, env)
			if err != nil {
				return swClause{}, err
			}
			parts = append(parts, clause)
		}
	}

	if len(w.And) > 0 {
		children := make([]swClause, 0, len(w.And))
		for _, sub := range w.And {
			c, err := renderSWWhere(sub, mode, env)
			if err != nil {
				return swClause{}, err
			}
			children = append(children, c)
		}
		parts = append(parts, swAnd(children, true))
	}
	if len(w.Or) > 0 {
		children := make([]swClause, 0, len(w.Or))
		for _, sub := range w.Or {
			c, err := renderSWWhere(sub, mode, env)
			if err != nil {
				return swClause{}, err
			}
			children = append(children, c)
		}
		parts = append(parts, swOr(children))
	}
	if w.Not != nil {
		c, err := renderSWWhere(*w.Not, !mode, env)
		if err != nil {
			return swClause{}, err
		}
		parts = append(parts, swNot(c))
	}
	return swAnd(parts, false), nil
}

// swPositiveOp splits a negative operator into its positive form.
func swPositiveOp(op query.BinaryWhereClauseType) (query.BinaryWhereClauseType, bool) {
	switch op {
	case query.Nq:
		return query.Eq, true
	case query.NqF:
		return query.EqF, true
	case query.NotIn:
		return query.In, true
	case query.NIContains:
		return query.IContains, true
	case query.NLike:
		return query.Like, true
	}
	return op, false
}

func renderSWLeaf(field string, op query.BinaryWhereClauseType, val any, mode swRenderMode, env *swRenderEnv) (swClause, error) {
	if field == "severity" {
		var err error
		if op, val, err = swSeverityLeaf(op, val); err != nil {
			return swClause{}, err
		}
	}
	pos, negated := swPositiveOp(op)
	if negated {
		inner, err := renderSWPositive(field, pos, val, !mode, env)
		return swNot(inner), err
	}
	return renderSWPositive(field, pos, val, mode, env)
}

// swSeverityLeaf turns any level condition into equality on a level the reader knows, so a
// level filter always gets the token expansion and the row check. "level ilike error" used
// to search severity:ERROR alone, which matches none of the Kubernetes lines (all INFO).
// Pattern operators are accepted when, wildcards removed, the value names a level; any other
// level condition is refused.
func swSeverityLeaf(op query.BinaryWhereClauseType, val any) (query.BinaryWhereClauseType, any, error) {
	pos, negated := swPositiveOp(op)
	check := func(v string) (string, error) {
		if swNormalizeLevel(strings.ToLower(v)) == "" {
			return "", fmt.Errorf("SolarWinds log level filter: %q is not a level (use error, warn, info or debug)", v)
		}
		return v, nil
	}
	switch pos {
	case query.Eq, query.EqF:
		v, err := check(fmt.Sprintf("%v", val))
		return op, v, err
	case query.In:
		values := swListValues(val)
		out := make([]any, 0, len(values))
		for _, v := range values {
			if _, err := check(v); err != nil {
				return op, nil, err
			}
			out = append(out, v)
		}
		return op, out, nil
	case query.Contains, query.IContains, query.Like, query.LikeF, query.ILike, query.ILikeF:
		v, err := check(strings.TrimSpace(strings.Trim(fmt.Sprintf("%v", val), "%*")))
		if err != nil {
			return op, nil, err
		}
		if negated {
			return query.Nq, v, nil
		}
		return query.Eq, v, nil
	}
	return op, val, nil // the operator's own refusal (is_null, ranges, …) applies
}

// swRowFields are the fields a /v1/logs row carries, so a filter on them can be checked
// exactly after the search. severity is carried too, but keeps SolarWinds' own semantics.
var swRowFields = map[string]bool{"hostname": true, "program": true, "message": true}

func renderSWPositive(field string, op query.BinaryWhereClauseType, val any, mode swRenderMode, env *swRenderEnv) (swClause, error) {
	if field == "trace_id" {
		return swClause{}, fmt.Errorf("SolarWinds log search has no confirmed trace id field; filter on the message text instead")
	}
	strVal := fmt.Sprintf("%v", val)
	severity := field == "severity"
	if severity {
		strVal = strings.ToUpper(strVal)
	}

	var term string
	switch op {
	case query.Eq, query.EqF:
		if severity && swLevelOf(strVal) == "info" {
			break // see swLevelOf: no search term finds INFO rows; the row check does
		}
		term = swTerm(field, strVal, true)
	case query.In:
		values := swListValues(val)
		if len(values) == 0 {
			// Rendering "" would drop the filter and return every row.
			return swClause{}, fmt.Errorf("SolarWinds log search: an in / not in filter on %q needs at least one value", field)
		}
		terms := make([]string, 0, len(values))
		for _, v := range values {
			if severity && swLevelOf(v) == "info" {
				terms = nil
				break
			}
			terms = append(terms, swTerm(field, v, true))
		}
		term = strings.Join(terms, " OR ")
		if len(terms) > 1 {
			term = "(" + term + ")"
		}
	case query.Contains, query.IContains, query.Like, query.LikeF, query.ILike, query.ILikeF:
		if err := swCheckPatternField(field, op, strVal); err != nil {
			return swClause{}, err
		}
		term = swLikeClause(field, strVal, false)
	case query.IsNull:
		return swClause{}, fmt.Errorf("SolarWinds log search cannot filter on whether %q is empty", field)
	case query.Gt, query.GtF, query.Gte, query.GteF, query.Lt, query.LtF, query.Lte, query.LteF:
		// SolarWinds answers "time:>…" with HTTP 500; the window belongs in start/end time.
		return swClause{}, fmt.Errorf("SolarWinds log search does not support operator %q on %q; set the time range instead", op, field)
	default:
		return swClause{}, fmt.Errorf("SolarWinds log search does not support operator %q on %q", op, field)
	}

	clause := swExprClause(term)
	// Under a negation, a workload's own pods (when known) name exactly a subset of its
	// lines, so "app != loki" can be excluded by SolarWinds instead of read and dropped.
	if mode == swSubset && (op == query.Eq || op == query.EqF || op == query.In) {
		if pods := env.workloadPodTerms(field, op, val); pods != "" {
			return swExprClause(pods), nil
		}
	}
	// In subset mode an inexact term — including no term at all, as for level=info — must
	// not stand for the rows it would select: render false and let the row check decide.
	if mode == swSubset && !swTermIsExact(field, op, strVal) {
		return swClause{kind: swFalse}, nil
	}
	return clause, nil
}

// swLevelOf folds a level word onto the reader's vocabulary (error / warn / info / debug),
// or returns it lower-cased when it has no place there.
//
// "info" has no search term: SolarWinds matches its Kubernetes rows (shown as INFO) as
// severity:DEBUG, not severity:INFO, so a level=info filter searches without narrowing and the
// row check keeps the rows shown as info.
func swLevelOf(level string) string {
	lower := strings.ToLower(level)
	if normalized := swNormalizeLevel(lower); normalized != "" {
		return normalized
	}
	return lower
}

func swListValues(val any) []string {
	switch v := val.(type) {
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			out = append(out, fmt.Sprintf("%v", item))
		}
		return out
	case []string:
		return v
	default:
		return []string{fmt.Sprintf("%v", val)}
	}
}

// swTermIsExact reports whether the search term for (field, op) selects exactly the rows the
// operator means. Where it does not, the term is only a superset and the row check decides.
func swTermIsExact(field string, op query.BinaryWhereClauseType, val string) bool {
	switch op {
	case query.Eq, query.EqF, query.In:
		switch {
		case field == "hostname", field == "program":
			return true // quoted values are exact on event fields
		case field == "message", field == "severity", field == "k8s.namespace.name", swWorkloadPodPatterns(field) != nil:
			return false
		}
		return true // other attributes: SolarWinds' substring semantics are the meaning
	case query.IContains:
		// swLikeClause splits on % and *, so "100%" searches message:100: a superset of
		// "contains the literal 100%", not the same rows.
		return !strings.ContainsAny(val, "%*")
	case query.ILike, query.ILikeF:
		return swLikeIsContains(val)
	}
	return false
}

// swLikeIsContains reports whether a like pattern is just "%fragment%": one fragment with a
// wildcard at each end, which is what an unquoted SolarWinds term already matches.
func swLikeIsContains(pattern string) bool {
	isWild := func(r rune) bool { return r == '%' || r == '*' }
	fragments := strings.FieldsFunc(pattern, isWild)
	if len(fragments) != 1 {
		return len(fragments) == 0
	}
	runes := []rune(pattern)
	return isWild(runes[0]) && isWild(runes[len(runes)-1])
}

// swCheckPatternField refuses a pattern SolarWinds cannot match exactly on a field the row
// check cannot see. Only case-insensitive unanchored matching is native there.
func swCheckPatternField(field string, op query.BinaryWhereClauseType, val string) error {
	if swRowFields[field] || field == "severity" {
		return nil
	}
	switch op {
	case query.Contains, query.Like, query.LikeF:
		return fmt.Errorf("SolarWinds matches %q only case-insensitively and does not return it on log lines, so a case-sensitive %s cannot be checked; use _icontains", field, op)
	case query.ILike, query.ILikeF:
		if !swLikeIsContains(val) {
			return fmt.Errorf("SolarWinds matches %q only as a substring and does not return it on log lines, so the anchored pattern %q cannot be checked; use _icontains", field, val)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------------------
// Row check
// ---------------------------------------------------------------------------------------

// swTri is a three-valued truth: a leaf the row check cannot evaluate (a field it has no
// value for) is unknown, and the search, which already applied it, decides.
type swTri int8

const (
	swNo swTri = iota
	swMaybe
	swYes
)

func swTriOf(b bool) swTri {
	if b {
		return swYes
	}
	return swNo
}

func (t swTri) not() swTri { return 2 - t }

// swRow is what the row check knows about one returned line.
type swRow struct {
	log       OutputLog
	namespace string // "" when unknown
}

func (r swRow) field(name string) (string, bool) {
	switch name {
	case "hostname":
		v, _ := r.log.Labels["hostname"].(string)
		return v, true
	case "program":
		v, _ := r.log.Labels["program"].(string)
		return v, true
	case "message":
		return r.log.Message, true
	}
	return "", false
}

// evalSWWhere evaluates the exact meaning of a where tree for one row.
func evalSWWhere(w query.QueryWhereClause, row swRow) swTri {
	result := swYes
	and := func(t swTri) {
		if t < result {
			result = t
		}
	}
	for field, ops := range w.Binary {
		for op, val := range ops {
			and(evalSWLeaf(field, op, val, row))
		}
	}
	for _, sub := range w.And {
		and(evalSWWhere(sub, row))
	}
	if len(w.Or) > 0 {
		or := swNo
		for _, sub := range w.Or {
			if t := evalSWWhere(sub, row); t > or {
				or = t
			}
		}
		and(or)
	}
	if w.Not != nil {
		and(evalSWWhere(*w.Not, row).not())
	}
	return result
}

func evalSWLeaf(field string, op query.BinaryWhereClauseType, val any, row swRow) swTri {
	if field == "severity" {
		var err error
		if op, val, err = swSeverityLeaf(op, val); err != nil {
			return swMaybe // refused when rendered; never reached for a row
		}
	}
	pos, negated := swPositiveOp(op)
	t := evalSWPositive(field, pos, val, row)
	if negated {
		return t.not()
	}
	return t
}

func evalSWPositive(field string, op query.BinaryWhereClauseType, val any, row swRow) swTri {
	strVal := fmt.Sprintf("%v", val)
	if field == "severity" {
		// The level a row is shown with (SolarWinds' own, or the line's level token), so a
		// level filter returns exactly the rows the reader labels that level.
		var wants []string
		switch op {
		case query.Eq, query.EqF:
			wants = []string{strVal}
		case query.In:
			wants = swListValues(val)
		default:
			return swMaybe
		}
		have := swLevelOf(row.log.Severity)
		for _, want := range wants {
			if swLevelOf(want) == have {
				return swYes
			}
		}
		return swNo
	}
	if v, ok := row.field(field); ok {
		switch op {
		case query.Eq, query.EqF:
			return swTriOf(v == strVal)
		case query.In:
			for _, want := range swListValues(val) {
				if v == want {
					return swYes
				}
			}
			return swNo
		case query.Contains:
			return swTriOf(strings.Contains(v, strVal))
		case query.IContains:
			return swTriOf(strings.Contains(strings.ToLower(v), strings.ToLower(strVal)))
		case query.Like, query.LikeF:
			return swTriOf(swLikeRegexp(strVal, false).MatchString(v))
		case query.ILike, query.ILikeF:
			return swTriOf(swLikeRegexp(strVal, true).MatchString(v))
		}
		return swMaybe
	}

	if field != "k8s.namespace.name" && swWorkloadPodPatterns(field) == nil {
		return swMaybe // SolarWinds' own semantics; nothing to check on the row
	}
	if field == "k8s.namespace.name" && row.namespace == "" {
		return swMaybe // pod unknown to the entity lookup: the search decides
	}
	var wants []string
	switch op {
	case query.Eq, query.EqF:
		wants = []string{strVal}
	case query.In:
		wants = swListValues(val)
	default:
		return swMaybe // substring operators: SolarWinds' match is the meaning
	}
	for _, want := range wants {
		if swRowHasValue(field, want, row) {
			return swYes
		}
	}
	return swNo
}

func swRowHasValue(field, want string, row swRow) bool {
	if field == "k8s.namespace.name" {
		return row.namespace == want
	}
	pod, _ := row.log.Labels["hostname"].(string)
	return swPodOwnedBy(pod, want, field)
}

var swLikeCache sync.Map // pattern + case flag → *regexp.Regexp

// swLikeRegexp compiles a like pattern (% and * match any run of characters) to an anchored
// regular expression.
func swLikeRegexp(pattern string, fold bool) *regexp.Regexp {
	key := fmt.Sprintf("%v|%s", fold, pattern)
	if re, ok := swLikeCache.Load(key); ok {
		return re.(*regexp.Regexp)
	}
	var b strings.Builder
	b.WriteString("(?s)")
	if fold {
		b.WriteString("(?i)")
	}
	b.WriteString("^")
	for _, r := range pattern {
		if r == '%' || r == '*' {
			b.WriteString(".*")
			continue
		}
		b.WriteString(regexp.QuoteMeta(string(r)))
	}
	b.WriteString("$")
	re := regexp.MustCompile(b.String())
	swLikeCache.Store(key, re)
	return re
}

// ---------------------------------------------------------------------------------------
// Workload ownership from the pod name
// ---------------------------------------------------------------------------------------

// Kubernetes builds generated name suffixes from this alphabet (no vowels, no 0/1/3), so
// "loki-canary-4xwzv" cannot be a pod of a workload named "loki": "canary" is not a suffix.
const k8sNameSuffixAlphabet = "[bcdfghjklmnpqrstvwxz2456789]"

// swWorkloadSuffix matches what follows "<workload>" in a pod name, per workload kind.
var swWorkloadSuffix = map[string]*regexp.Regexp{
	"k8s.deployment.name":  regexp.MustCompile("^-" + k8sNameSuffixAlphabet + "{1,10}-" + k8sNameSuffixAlphabet + "{5}$"),
	"k8s.daemonset.name":   regexp.MustCompile("^-" + k8sNameSuffixAlphabet + "{5}$"),
	"k8s.statefulset.name": regexp.MustCompile("^-[0-9]+$"),
}

// swWorkloadPodPatterns returns the pod-name suffix patterns for a workload field, or nil
// when the field is not one.
func swWorkloadPodPatterns(field string) []*regexp.Regexp {
	if field == swWorkloadField {
		out := make([]*regexp.Regexp, 0, len(swWorkloadKindFields))
		for _, f := range swWorkloadKindFields {
			out = append(out, swWorkloadSuffix[f])
		}
		return out
	}
	if suffix, ok := swWorkloadSuffix[field]; ok {
		return []*regexp.Regexp{suffix}
	}
	return nil
}

// swPodOwnedBy reports whether pod is named as a pod of workload: <workload>-<hash>-<hash>
// for a Deployment, <workload>-<hash> for a DaemonSet, <workload>-<ordinal> for a StatefulSet.
// field picks the kinds (swWorkloadField: any of the three).
func swPodOwnedBy(pod, workload, field string) bool {
	if workload == "" || !strings.HasPrefix(pod, workload+"-") {
		return false
	}
	rest := pod[len(workload):]
	for _, suffix := range swWorkloadPodPatterns(field) {
		if suffix.MatchString(rest) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------------------
// Pod → namespace lookup
// ---------------------------------------------------------------------------------------

// swPodLookupBatch is how many pod names one /v1/entities search names. 100 names
// (about 2.5 KB of query string) resolved in 1.4 s live; 50 keeps the URL well clear of
// proxy limits.
const swPodLookupBatch = 50

// querySWPodNamespacesFor returns pod name → Kubernetes namespace for the given pods, read from
// the KubernetesPod entities (attributes.namespaceName). It searches for exactly these pods
// (`name:"a" OR name:"b"`) rather than listing the org's pods: that list runs to tens of
// thousands of entities, so its first page missed nearly every pod.
func querySWPodNamespacesFor(apiToken, baseURL string, pods []string) (map[string]string, error) {
	m := make(map[string]string, len(pods))
	ambiguous := map[string]bool{}
	for start := 0; start < len(pods); start += swPodLookupBatch {
		batch := pods[start:min(start+swPodLookupBatch, len(pods))]
		terms := make([]string, 0, len(batch))
		for _, pod := range batch {
			terms = append(terms, "name:"+swQuote(pod))
		}
		params := map[string]string{
			"type":     "KubernetesPod",
			"search":   strings.Join(terms, " OR "),
			"pageSize": "1000",
		}
		body, statusCode, err := solarWindsDoGET(apiToken, baseURL, "/v1/entities", params)
		if err != nil {
			return nil, swRequestError("SolarWinds pod namespace lookup failed", err)
		}
		if statusCode != http.StatusOK {
			return nil, fmt.Errorf("SolarWinds entities API returned HTTP %d", statusCode)
		}
		var resp swPodEntityResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("failed to parse SolarWinds pod entity response: %w", err)
		}
		for _, e := range resp.Entities {
			if e.Name == "" || e.Attributes.NamespaceName == "" {
				continue
			}
			if prev, seen := m[e.Name]; seen && prev != e.Attributes.NamespaceName {
				ambiguous[e.Name] = true // e.g. redis-0 in two namespaces: the row cannot say which
			}
			m[e.Name] = e.Attributes.NamespaceName
		}
	}
	for name := range ambiguous {
		delete(m, name) // unknown, so the row check defers to the search
	}
	return m, nil
}

// swPodNamespaceTTL bounds how long a pod's namespace is reused. A pod never changes
// namespace; the bound only keeps the cache from growing for the life of the process.
// A pod SolarWinds does not know yet (created after its last entity sync) is retried
// sooner, after swPodUnknownTTL, since until then its rows fall back to substring matching.
const (
	swPodNamespaceTTL = 30 * time.Minute
	swPodUnknownTTL   = 2 * time.Minute
)

var swPodNamespaceCache = struct {
	sync.Mutex
	entries map[string]swPodNamespaceEntry
}{entries: map[string]swPodNamespaceEntry{}}

type swPodNamespaceEntry struct {
	namespace string
	expires   time.Time
}

// swPodNamespaceCacheKey scopes a pod name to the SolarWinds org (two orgs can both have a
// pod named web-0), without keeping the token itself in memory as a key.
func swPodNamespaceCacheKey(apiToken, baseURL, pod string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(apiToken))
	return fmt.Sprintf("%x|%s|%s", h.Sum64(), baseURL, pod)
}

// swPodNamespacesCached is querySWPodNamespacesFor with a per-org cache. Pods SolarWinds does
// not know (deleted, or a node hostname) are cached as unknown too, so they are not
// searched for again on every page.
func swPodNamespacesCached(apiToken, baseURL string, pods []string) (map[string]string, error) {
	now := time.Now()
	out := make(map[string]string, len(pods))
	var missing []string
	swPodNamespaceCache.Lock()
	for _, pod := range pods {
		if e, ok := swPodNamespaceCache.entries[swPodNamespaceCacheKey(apiToken, baseURL, pod)]; ok && now.Before(e.expires) {
			if e.namespace != "" {
				out[pod] = e.namespace
			}
			continue
		}
		missing = append(missing, pod)
	}
	swPodNamespaceCache.Unlock()
	if len(missing) == 0 {
		return out, nil
	}
	found, err := querySWPodNamespacesFor(apiToken, baseURL, missing)
	if err != nil {
		return nil, err
	}
	swPodNamespaceCache.Lock()
	for _, pod := range missing {
		ttl := swPodNamespaceTTL
		if found[pod] == "" {
			ttl = swPodUnknownTTL
		}
		swPodNamespaceCache.entries[swPodNamespaceCacheKey(apiToken, baseURL, pod)] = swPodNamespaceEntry{namespace: found[pod], expires: now.Add(ttl)}
		if found[pod] != "" {
			out[pod] = found[pod]
		}
	}
	for key, e := range swPodNamespaceCache.entries {
		if now.After(e.expires) {
			delete(swPodNamespaceCache.entries, key)
		}
	}
	swPodNamespaceCache.Unlock()
	return out, nil
}

// swWhereUsesField reports whether any leaf of the tree filters on field.
func swWhereUsesField(w query.QueryWhereClause, field string) bool {
	if _, ok := w.Binary[field]; ok {
		return true
	}
	for _, sub := range append(append([]query.QueryWhereClause{}, w.And...), w.Or...) {
		if swWhereUsesField(sub, field) {
			return true
		}
	}
	return w.Not != nil && swWhereUsesField(*w.Not, field)
}

// filterSWRows keeps the rows that can satisfy the filter's exact meaning, looking up pod
// namespaces when the filter names one.
func filterSWRows(apiToken, baseURL string, w query.QueryWhereClause, logs []OutputLog) ([]OutputLog, error) {
	var namespaces map[string]string
	if swWhereUsesField(w, "k8s.namespace.name") {
		seen := map[string]struct{}{}
		var pods []string
		for _, l := range logs {
			pod, _ := l.Labels["hostname"].(string)
			if _, dup := seen[pod]; pod != "" && !dup {
				seen[pod] = struct{}{}
				pods = append(pods, pod)
			}
		}
		var err error
		if namespaces, err = swPodNamespacesCached(apiToken, baseURL, pods); err != nil {
			return nil, fmt.Errorf("SolarWinds namespace check failed: %w", err)
		}
	}
	kept := logs[:0]
	for _, l := range logs {
		pod, _ := l.Labels["hostname"].(string)
		if evalSWWhere(w, swRow{log: l, namespace: namespaces[pod]}) != swNo {
			kept = append(kept, l)
		}
	}
	return kept, nil
}

// ---------------------------------------------------------------------------------------
// Workload exclusion by pod
// ---------------------------------------------------------------------------------------

// swRenderEnv carries what rendering may use beyond the filter itself. nil renders without it.
type swRenderEnv struct {
	// workloadPods maps swWorkloadPodsKey(field, workload) to the workload's pods, for the
	// workloads whose pods are known completely enough to exclude by.
	workloadPods map[string][]string
}

func swWorkloadPodsKey(field, workload string) string { return field + "\x00" + workload }

// workloadPodTerms renders "these workloads' pods" as exact host terms, or "" when a value's
// pods are not known.
func (e *swRenderEnv) workloadPodTerms(field string, op query.BinaryWhereClauseType, val any) string {
	if e == nil || swWorkloadPodPatterns(field) == nil {
		return ""
	}
	values := []string{fmt.Sprintf("%v", val)}
	if op == query.In {
		values = swListValues(val)
	}
	var terms []string
	for _, v := range values {
		pods, ok := e.workloadPods[swWorkloadPodsKey(field, v)]
		if !ok {
			return ""
		}
		for _, pod := range pods {
			terms = append(terms, swSearchField("hostname")+":"+swQuote(pod))
		}
	}
	if len(terms) == 0 {
		return ""
	}
	if len(terms) == 1 {
		return terms[0]
	}
	return "(" + strings.Join(terms, " OR ") + ")"
}

// swWorkloadExclusionMaxPods caps how many pods an exclusion names; a workload with more
// (a long-lived Deployment's pod history) is excluded by the row check instead.
const swWorkloadExclusionMaxPods = 40

// swWorkloadPodPages bounds the entity pages read per workload.
const swWorkloadPodPages = 2

// swWorkloadLeaves collects the (field, workload) pairs of every workload equality / in leaf.
func swWorkloadLeaves(w query.QueryWhereClause, out map[[2]string]struct{}) {
	for field, ops := range w.Binary {
		if swWorkloadPodPatterns(field) == nil {
			continue
		}
		for op, val := range ops {
			pos, _ := swPositiveOp(op)
			switch pos {
			case query.Eq, query.EqF:
				out[[2]string{field, fmt.Sprintf("%v", val)}] = struct{}{}
			case query.In:
				for _, v := range swListValues(val) {
					out[[2]string{field, v}] = struct{}{}
				}
			}
		}
	}
	for _, sub := range append(append([]query.QueryWhereClause{}, w.And...), w.Or...) {
		swWorkloadLeaves(sub, out)
	}
	if w.Not != nil {
		swWorkloadLeaves(*w.Not, out)
	}
}

// swResolveRenderEnv looks up the pods of every workload the filter names. Lookups that fail
// or find too many pods are left out, which only means the row check does that exclusion.
func swResolveRenderEnv(apiToken, baseURL string, w query.QueryWhereClause) *swRenderEnv {
	leaves := map[[2]string]struct{}{}
	swWorkloadLeaves(w, leaves)
	env := &swRenderEnv{workloadPods: map[string][]string{}}
	for leaf := range leaves {
		field, workload := leaf[0], leaf[1]
		pods, ok := swWorkloadPodsCached(apiToken, baseURL, field, workload)
		if ok {
			env.workloadPods[swWorkloadPodsKey(field, workload)] = pods
		}
	}
	return env
}

// swWorkloadPods lists the pods SolarWinds knows for a workload: an entity name search (a
// substring match), narrowed to names shaped as that workload's pods. ok is false when the
// list may be incomplete or too long to name in a search.
func swWorkloadPods(apiToken, baseURL, field, workload string) (pods []string, ok bool) {
	if workload == "" {
		return nil, false
	}
	params := map[string]string{"type": "KubernetesPod", "name": workload, "pageSize": "1000"}
	for page := 0; page < swWorkloadPodPages; page++ {
		body, statusCode, err := solarWindsDoGET(apiToken, baseURL, "/v1/entities", params)
		if err != nil || statusCode != http.StatusOK {
			return nil, false
		}
		var resp swEntityResponse
		if json.Unmarshal(body, &resp) != nil {
			return nil, false
		}
		for _, e := range resp.Entities {
			if swPodOwnedBy(e.Name, workload, field) {
				pods = append(pods, e.Name)
			}
		}
		if len(pods) > swWorkloadExclusionMaxPods {
			return nil, false
		}
		token := swNextSkipToken(&swPageInfo{NextPage: resp.PageInfo.NextPage})
		if token == "" {
			return pods, true
		}
		params["skipToken"] = token
	}
	return nil, false // more pages than we read: the list may be incomplete
}

// swWorkloadPodsTTL bounds how long a workload's pod list is reused: GetQuery and QueryLogs
// resolve the same filter back to back, and a new pod only costs a row-check exclusion.
const swWorkloadPodsTTL = 2 * time.Minute

var swWorkloadPodsCache = struct {
	sync.Mutex
	entries map[string]swWorkloadPodsEntry
}{entries: map[string]swWorkloadPodsEntry{}}

type swWorkloadPodsEntry struct {
	pods    []string
	ok      bool
	expires time.Time
}

func swWorkloadPodsCached(apiToken, baseURL, field, workload string) ([]string, bool) {
	key := swPodNamespaceCacheKey(apiToken, baseURL, swWorkloadPodsKey(field, workload))
	now := time.Now()
	swWorkloadPodsCache.Lock()
	e, hit := swWorkloadPodsCache.entries[key]
	swWorkloadPodsCache.Unlock()
	if hit && now.Before(e.expires) {
		return e.pods, e.ok
	}
	pods, ok := swWorkloadPods(apiToken, baseURL, field, workload)
	swWorkloadPodsCache.Lock()
	swWorkloadPodsCache.entries[key] = swWorkloadPodsEntry{pods: pods, ok: ok, expires: now.Add(swWorkloadPodsTTL)}
	for k, old := range swWorkloadPodsCache.entries {
		if now.After(old.expires) {
			delete(swWorkloadPodsCache.entries, k)
		}
	}
	swWorkloadPodsCache.Unlock()
	return pods, ok
}
