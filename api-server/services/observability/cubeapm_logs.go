package observability

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	neturl "net/url"
	"nudgebee/services/common"
	"nudgebee/services/integrations"
	"nudgebee/services/query"
	"nudgebee/services/security"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// CubeAPMLogSource implements LogSource (and LogGroupSource) for CubeAPM.
//
// CubeAPM stores logs in a VictoriaLogs engine and queries them with LogsQL, so
// unlike the SQL-backed sources here there is no table or stream name to resolve
// — a query is a stream selector, a set of conditions, and a pipeline. The only
// scoping this source applies is the optional environment tag, which CubeAPM
// guarantees is a stream field on every record.
type CubeAPMLogSource struct{}

// cubeAPMLogsQueryPath is the single documented read endpoint for logs.
const cubeAPMLogsQueryPath = "/api/logs/select/logsql/query"

const (
	cubeAPMLogQueryTimeout     = 30 * time.Second
	cubeAPMLogGroupTimeout     = 60 * time.Second
	cubeAPMDefaultLogLimit     = 100
	cubeAPMMaxLogLimit         = 10000
	cubeAPMLabelSampleSize     = 100
	cubeAPMLabelValueLimit     = 100
	cubeAPMLogGroupLimit       = 100
	cubeAPMDefaultLookbackHour = time.Hour
)

// cubeAPMLogLabelMapping maps canonical field names onto the CubeAPM fields that
// carry them.
//
// CubeAPM flattens nested structures but preserves dots in key names, so an OTel
// resource attribute arrives as `k8s.namespace.name` rather than the underscored
// spelling OpenObserve produces. That is why this table looks like the raw OTel
// semantic conventions: it is not a translation, it is the attribute name itself.
// Deployments whose shipper uses other names remap per-account via the
// label-mapping override rather than editing this table.
var cubeAPMLogLabelMapping = map[string]string{
	"timestamp": "_time",
	"body":      "_msg",
	"message":   "_msg",
	"namespace": "k8s.namespace.name",
	"pod":       "k8s.pod.name",
	"container": "k8s.container.name",
	"node":      "k8s.node.name",
	"workload":  "k8s.deployment.name",
	"app":       "k8s.deployment.name",
	"cluster":   "k8s.cluster.name",
	"host":      "host.name",
	"hostname":  "host.name",
	// `service`, not the OTel `service.name`. CubeAPM's logs-API doc shows a
	// stream selector of {env="UNSET",service.name="order"}, but a live instance
	// running CubeAPM's own demo app indexes the stream field as `service` — the
	// documented spelling matches nothing. Verified against a real deployment;
	// a shipper that does write service.name is remapped per-account.
	"service":  "service",
	"severity": "log.level",
	"level":    "log.level",
	"env":      "env",
}

// cubeAPMLogFieldAliases lists EVERY CubeAPM field a label may be backed by, for
// labels whose real field depends on how the instance is instrumented.
//
// "workload": a CubeAPM fed by the OTel k8s attributes processor carries
// k8s.deployment.name; one fed straight from an instrumented app carries only
// `service` and no k8s.* field at all — CubeAPM's own demo deployment indexes
// exactly `_msg, _time, service, env, endpoint, path, log.level, trace_id`.
//
// "level": CubeAPM's OTel SDK data sets `log.level`, while Kubernetes logs whose
// JSON body was lifted apart at ingestion carry `level` — the same pair the log
// group query coalesces (cubeAPMLogGroupFields.Level). On a live instance `level`
// was on 89k records and `log.level` on 41, so filtering on `log.level` alone
// matched almost nothing.
//
// Resolving to a single field makes the other shape match nothing and return an
// empty result with no error, which reads as "no logs" rather than "wrong field".
//
// FetchLogs rewrites a where clause through GetLabelMapping() before this source
// sees it, so `workload` arrives as `k8s.deployment.name` and `level` as
// `log.level`; an alias keyed only by the canonical name never matched on that
// path. The level pair is therefore also keyed by `log.level` (the two fields mean
// the same thing). The workload pair is not keyed by `k8s.deployment.name` — that
// would widen a filter on the raw field to unrelated `service` values — and is
// handled by cubeAPMLogFieldFallbacks instead. An account override that maps a
// label elsewhere arrives as that other field and is used as-is.
//
// Order matters only for readability; a filter ORs across all of them, so whichever
// field the instance actually populates is the one that matches.
var cubeAPMLogFieldAliases = map[string][]string{
	"workload":  {"k8s.deployment.name", "service"},
	"app":       {"k8s.deployment.name", "service"},
	"level":     {"log.level", "level"},
	"severity":  {"log.level", "level"},
	"log.level": {"log.level", "level"},
}

// cubeAPMLogFieldFallbacks names a field consulted only on records that lack the
// primary one. A workload filter becomes
//
//	(k8s.deployment.name:=X OR (service:=X AND NOT k8s.deployment.name:*))
//
// so an instance shipping Kubernetes attributes is matched exactly on the
// deployment, while one with no k8s.* fields still matches on `service`. It applies
// whether the filter names `workload` or arrives already mapped to
// `k8s.deployment.name`, which is the only form FetchLogs passes on.
var cubeAPMLogFieldFallbacks = map[string]string{
	"k8s.deployment.name": "service",
}

// cubeAPMLogCaseInsensitiveFields are compared without regard to case on equality.
// Severity is written in whatever case the application logs it — one live window
// held both `ERROR` (202 records) and `error` (22) — and LogsQL `:=` is exact, so a
// level filter would otherwise return one spelling and silently drop the other.
var cubeAPMLogCaseInsensitiveFields = map[string]struct{}{
	"log.level": {},
	"level":     {},
}

// cubeAPMFieldsFor returns the CubeAPM fields a label resolves to: the alias list
// when the label (canonical or already mapped) has one, otherwise the single mapped
// field (or the label itself when it is already a raw field name).
func cubeAPMFieldsFor(label string, mapping map[string]string) []string {
	if aliases, ok := cubeAPMLogFieldAliases[label]; ok {
		return aliases
	}
	if mapped, ok := mapping[label]; ok {
		return []string{mapped}
	}
	return []string{label}
}

// cubeAPMLogMessageFields are the keys read as the rendered log line, in priority
// order. `_msg` is CubeAPM's canonical field, but an ingestion pipeline configured
// with a different `_msg_field` leaves the original key in place alongside it.
var cubeAPMLogMessageFields = []string{"_msg", "message", "body", "log"}

// cubeAPMLogSeverityFields are the keys read as the severity, in priority order.
var cubeAPMLogSeverityFields = []string{"log.level", "severity", "level", "severity_text", "SeverityText"}

func (s *CubeAPMLogSource) GetSupportedOperators() []string {
	return []string{"_eq", "_neq", "_contains", "_regex"}
}

func (s *CubeAPMLogSource) GetLabelMapping() map[string]string {
	return cubeAPMLogLabelMapping
}

func (s *CubeAPMLogSource) GetIgnoredQueryRequestKeys() []string {
	return []string{}
}

// cubeAPMQuote renders a value as a LogsQL double-quoted string. Every filter
// value goes through this rather than being interpolated bare: an unquoted value
// containing a space, a colon or a pipe would otherwise be parsed as further query
// syntax, which is both a correctness bug and the injection vector.
func cubeAPMQuote(value string) string {
	var b strings.Builder
	b.Grow(len(value) + 2)
	b.WriteByte('"')
	for _, r := range value {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// cubeAPMFieldNameRe-equivalent check: a LogsQL field name is a dotted identifier.
// Rejecting anything else keeps a hostile label name from closing the filter and
// appending its own pipeline (e.g. `x | delete`).
func isSafeCubeAPMField(field string) bool {
	if field == "" || len(field) > 255 {
		return false
	}
	for _, r := range field {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '_' || r == '.' || r == '-' || r == ':' || r == '/':
		default:
			return false
		}
	}
	return true
}

// renderCubeAPMMatch renders the POSITIVE match of one operator on one field;
// the caller applies negation for _neq.
func renderCubeAPMMatch(col string, op query.BinaryWhereClauseType, value string) (string, error) {
	switch op {
	case query.Eq, query.Nq:
		if _, ci := cubeAPMLogCaseInsensitiveFields[col]; ci {
			return fmt.Sprintf("%s:~%s", col, cubeAPMQuote("(?i)^"+regexp.QuoteMeta(value)+"$")), nil
		}
		return fmt.Sprintf("%s:=%s", col, cubeAPMQuote(value)), nil
	case query.Contains, query.ILike:
		// An escaped, case-insensitive regex. Not field:"*value*": inside quotes
		// LogsQL reads the asterisks literally, so that form matched nothing on
		// every field (verified live: 0 records for _msg:"*error*" against 783 for
		// _msg:~"error").
		return fmt.Sprintf("%s:~%s", col, cubeAPMQuote("(?i)"+regexp.QuoteMeta(value))), nil
	case query.Regex:
		return fmt.Sprintf("%s:~%s", col, cubeAPMQuote(value)), nil
	default:
		return "", fmt.Errorf("unsupported operator for CubeAPM logs: %s", op)
	}
}

// buildCubeAPMBinaryClause renders one binary where-clause into LogsQL conditions.
func buildCubeAPMBinaryClause(binary query.BinaryWhereClause, mapping map[string]string) (string, error) {
	var parts []string
	for field, ops := range binary {
		cols := cubeAPMFieldsFor(field, mapping)
		for _, col := range cols {
			if !isSafeCubeAPMField(col) {
				return "", fmt.Errorf("invalid or unsafe field name: %q", col)
			}
		}
		// A primary field with a fallback renders as primary OR (fallback AND the
		// primary is absent), whether it arrived alone or with its alias pair.
		primary, fallback := "", ""
		if fb, ok := cubeAPMLogFieldFallbacks[cols[0]]; ok && (len(cols) == 1 || (len(cols) == 2 && cols[1] == fb)) {
			primary, fallback = cols[0], fb
		}

		for op, val := range ops {
			strVal := fmt.Sprintf("%v", val)

			var clause string
			if primary != "" {
				p, err := renderCubeAPMMatch(primary, op, strVal)
				if err != nil {
					return "", err
				}
				f, err := renderCubeAPMMatch(fallback, op, strVal)
				if err != nil {
					return "", err
				}
				clause = fmt.Sprintf("(%s OR (%s AND NOT %s:*))", p, f, primary)
			} else {
				// Render the POSITIVE match once per candidate field. A label with a
				// single field keeps the exact expression it always emitted; only a
				// multi-field label grows the OR.
				match := make([]string, 0, len(cols))
				for _, col := range cols {
					m, err := renderCubeAPMMatch(col, op, strVal)
					if err != nil {
						return "", err
					}
					match = append(match, m)
				}
				clause = match[0]
				if len(match) > 1 {
					clause = "(" + strings.Join(match, " OR ") + ")"
				}
			}
			// Negation wraps the whole disjunction. Distributing it instead
			// (NOT a OR NOT b) is always true whenever the two fields differ,
			// which for an alias pair is every record.
			if op == query.Nq {
				clause = "NOT " + clause
			}
			parts = append(parts, clause)
		}
	}
	// Map iteration order is random; sorting keeps a query stable across calls so
	// it can be cached, compared in tests, and read in a log line.
	sort.Strings(parts)
	return strings.Join(parts, " AND "), nil
}

// buildCubeAPMConditions renders a where-clause tree into a LogsQL condition
// expression. Empty sub-clauses are pruned rather than emitted as `()`, which
// LogsQL rejects.
func buildCubeAPMConditions(where query.QueryWhereClause, mapping map[string]string) (string, error) {
	var parts []string

	if len(where.Binary) > 0 {
		clause, err := buildCubeAPMBinaryClause(where.Binary, mapping)
		if err != nil {
			return "", err
		}
		if clause != "" {
			parts = append(parts, clause)
		}
	}

	for _, sub := range where.And {
		clause, err := buildCubeAPMConditions(sub, mapping)
		if err != nil {
			return "", err
		}
		if clause != "" {
			parts = append(parts, "("+clause+")")
		}
	}

	if len(where.Or) > 0 {
		var orParts []string
		for _, sub := range where.Or {
			clause, err := buildCubeAPMConditions(sub, mapping)
			if err != nil {
				return "", err
			}
			if clause != "" {
				orParts = append(orParts, "("+clause+")")
			}
		}
		if len(orParts) > 0 {
			parts = append(parts, "("+strings.Join(orParts, " OR ")+")")
		}
	}

	if where.Not != nil {
		clause, err := buildCubeAPMConditions(*where.Not, mapping)
		if err != nil {
			return "", err
		}
		if clause != "" {
			parts = append(parts, "NOT ("+clause+")")
		}
	}

	return strings.Join(parts, " AND "), nil
}

// cubeAPMBaseQuery assembles the selector-plus-conditions half of a LogsQL query,
// before any pipes. A query with neither becomes `*`, which LogsQL reads as
// "every log in the window" — the same thing a filterless request means here.
func cubeAPMBaseQuery(env, conditions string) string {
	var parts []string
	if env != "" {
		parts = append(parts, fmt.Sprintf("{env=%s}", cubeAPMQuote(env)))
	}
	if conditions != "" {
		parts = append(parts, conditions)
	}
	if len(parts) == 0 {
		return "*"
	}
	return strings.Join(parts, " ")
}

// cubeAPMLogLimit clamps the requested page size. The cap is not decoration: the
// response is streamed NDJSON with no server-side pagination, so an unbounded
// limit is a request to buffer the whole window in memory.
func cubeAPMLogLimit(requested int) int {
	if requested <= 0 {
		return cubeAPMDefaultLogLimit
	}
	if requested > cubeAPMMaxLogLimit {
		return cubeAPMMaxLogLimit
	}
	return requested
}

func (s *CubeAPMLogSource) buildLogsQL(req FetchLogRequest, env string) (string, error) {
	// A raw LogsQL query typed in Code mode is passed through untouched — rewriting
	// it would fight the user, and the env selector may already be in it.
	if strings.TrimSpace(req.Query) != "" {
		return strings.TrimSpace(req.Query), nil
	}

	conditions, err := buildCubeAPMConditions(req.QueryRequest.Where, cubeAPMLogLabelMapping)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf(`%s | sort ("_time" desc) | limit %d`,
		cubeAPMBaseQuery(env, conditions), cubeAPMLogLimit(req.Limit)), nil
}

func (s *CubeAPMLogSource) GetQuery(ctx *security.RequestContext, req FetchLogRequest) (string, error) {
	cfg, err := integrations.GetCubeAPMConfigs(ctx, req.AccountId)
	if err != nil {
		return "", fmt.Errorf("failed to get CubeAPM configs: %w", err)
	}
	return s.buildLogsQL(req, cfg.Env)
}

func (s *CubeAPMLogSource) QueryLogs(ctx *security.RequestContext, req FetchLogRequest) ([]OutputLog, error) {
	cfg, err := integrations.GetCubeAPMConfigs(ctx, req.AccountId)
	if err != nil {
		return nil, fmt.Errorf("failed to get CubeAPM configs: %w", err)
	}

	logsQL, err := s.buildLogsQL(req, cfg.Env)
	if err != nil {
		return nil, err
	}

	rows, err := cubeAPMLogSearch(cfg, logsQL, req.StartTime, req.EndTime, cubeAPMLogLimit(req.Limit), cubeAPMLogQueryTimeout)
	if err != nil {
		return nil, err
	}

	outputs := make([]OutputLog, 0, len(rows))
	for _, row := range rows {
		outputs = append(outputs, cubeAPMRowToOutputLog(row))
	}
	return outputs, nil
}

// cubeAPMRowToOutputLog projects one CubeAPM record onto the shared OutputLog
// shape. Every field is kept in Labels — including the ones promoted to
// Timestamp/Message/Severity — because the log table lets users column on any of
// them, and dropping a field here makes it unselectable there.
func cubeAPMRowToOutputLog(row map[string]any) OutputLog {
	out := OutputLog{Labels: make(map[string]any, len(row))}

	for k, v := range row {
		out.Labels[k] = v
	}

	// `_time` is already ISO 8601 from CubeAPM, which is what the log table
	// renders, so it is passed through rather than reformatted.
	//
	// The canonical `timestamp` label is a different contract and has to be set
	// here. cubeAPMLogLabelMapping maps timestamp → _time, so normalizeOutputLogLabels
	// would otherwise alias the ISO string into `timestamp` — and the log-row
	// dropdown reads that label as epoch nanoseconds (`new Date(value / 1e6)`).
	// An ISO string divides to NaN there, and the resulting Invalid Date throws
	// `RangeError: Invalid time value` out of the row's date formatter, taking the
	// whole log screen down rather than just that one cell. Emitting the
	// nanosecond form pre-empts the alias, which is only added when the canonical
	// key is absent; `_time` stays in Labels as the provider's own field.
	if ts := cubeAPMString(row["_time"]); ts != "" {
		out.Timestamp = ts
		if parsed, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			out.Labels["timestamp"] = parsed.UnixNano()
		} else {
			// A `_time` we cannot parse has no nanosecond form, but leaving the key
			// absent is not an option: normalizeOutputLogLabels tests for the key's
			// EXISTENCE, not its value, so it would alias the unparseable string in
			// and the dropdown would crash on it exactly as before. An explicit nil
			// claims the key and renders as no row at all — the label list drops
			// null values. `_time` itself stays, so the raw value is still visible
			// and filterable.
			out.Labels["timestamp"] = nil
		}
	}
	out.Message = cubeAPMFirstString(row, cubeAPMLogMessageFields)
	out.Severity = cubeAPMFirstString(row, cubeAPMLogSeverityFields)

	// `_stream` arrives as a rendered selector string (`{env="prod",...}`) rather
	// than an object. Expanding it into real labels is what makes the stream
	// fields filterable from the log table instead of being one opaque blob.
	if stream := cubeAPMString(row["_stream"]); stream != "" {
		for k, v := range parseCubeAPMStreamLabels(stream) {
			if _, exists := out.Labels[k]; !exists {
				out.Labels[k] = v
			}
		}
	}

	return out
}

// parseCubeAPMStreamLabels expands a rendered stream selector into its key/value
// pairs. Values are double-quoted and may contain escaped quotes and commas, so
// this walks the string rather than splitting on the separators.
func parseCubeAPMStreamLabels(stream string) map[string]string {
	stream = strings.TrimSpace(stream)
	stream = strings.TrimPrefix(stream, "{")
	stream = strings.TrimSuffix(stream, "}")

	labels := map[string]string{}
	for len(stream) > 0 {
		eq := strings.IndexByte(stream, '=')
		if eq < 0 {
			break
		}
		key := strings.TrimSpace(stream[:eq])
		rest := stream[eq+1:]
		if !strings.HasPrefix(rest, `"`) {
			break
		}

		var value strings.Builder
		i := 1
		for i < len(rest) {
			if rest[i] == '\\' && i+1 < len(rest) {
				value.WriteByte(rest[i+1])
				i += 2
				continue
			}
			if rest[i] == '"' {
				break
			}
			value.WriteByte(rest[i])
			i++
		}
		if key != "" {
			labels[key] = value.String()
		}

		stream = strings.TrimPrefix(strings.TrimSpace(rest[min(i+1, len(rest)):]), ",")
	}
	return labels
}

func cubeAPMString(v any) string {
	switch n := v.(type) {
	case nil:
		return ""
	case string:
		return n
	case json.Number:
		return n.String()
	case bool:
		return strconv.FormatBool(n)
	case float64:
		return strconv.FormatFloat(n, 'f', -1, 64)
	default:
		return fmt.Sprintf("%v", v)
	}
}

func cubeAPMFirstString(row map[string]any, candidates []string) string {
	for _, c := range candidates {
		if s := cubeAPMString(row[c]); s != "" {
			return s
		}
	}
	return ""
}

// cubeAPMLogSearch runs a LogsQL query and decodes the newline-delimited JSON
// response.
//
// The body is streamed line by line rather than decoded whole: CubeAPM returns
// NDJSON with no enclosing array, so there is no single document to unmarshal,
// and buffering a large result before parsing it would defeat the point of the
// streaming format.
func cubeAPMLogSearch(cfg integrations.CubeAPMConfig, logsQL string, startMs, endMs int64, limit int, timeout time.Duration) ([]map[string]any, error) {
	startMs, endMs = cubeAPMTimeRangeMillis(startMs, endMs, time.Now())

	form := neturl.Values{}
	form.Set("query", logsQL)
	form.Set("start", strconv.FormatInt(startMs/1000, 10))
	form.Set("end", strconv.FormatInt(endMs/1000, 10))
	if limit > 0 {
		form.Set("limit", strconv.Itoa(limit))
	}

	body, err := cubeAPMPostForm(cfg, cfg.URL+cubeAPMLogsQueryPath, form, timeout)
	if err != nil {
		return nil, err
	}

	return decodeCubeAPMNDJSON(bytes.NewReader(body))
}

// decodeCubeAPMNDJSON parses a newline-delimited JSON stream. A malformed line is
// skipped rather than failing the whole page: one unparseable record should not
// blank out an otherwise good result set.
func decodeCubeAPMNDJSON(r io.Reader) ([]map[string]any, error) {
	var rows []map[string]any

	scanner := bufio.NewScanner(r)
	// The default 64KB token cap is too small — a single log line carrying a stack
	// trace routinely exceeds it, and the scanner would stop mid-response.
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var row map[string]any
		dec := json.NewDecoder(strings.NewReader(line))
		// Numbers stay as their literal text so large integers (nanosecond
		// timestamps, span ids) do not lose their low digits to float64.
		dec.UseNumber()
		if err := dec.Decode(&row); err != nil {
			continue
		}
		rows = append(rows, row)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to read CubeAPM log stream: %w", err)
	}
	return rows, nil
}

// cubeAPMTimeRangeMillis normalizes a request window, defaulting to the last hour
// when the caller supplied neither bound.
func cubeAPMTimeRangeMillis(startMs, endMs int64, now time.Time) (int64, int64) {
	if endMs <= 0 {
		endMs = now.UnixMilli()
	}
	if startMs <= 0 {
		startMs = now.Add(-cubeAPMDefaultLookbackHour).UnixMilli()
	}
	return startMs, endMs
}

func (s *CubeAPMLogSource) QueryLabels(ctx *security.RequestContext, req FetchLogLabelRequest) ([]OutputLogLabel, error) {
	startTime, endTime := cubeAPMTimeRangeMillis(req.StartTime, req.EndTime, time.Now())

	// CubeAPM exposes no field-name discovery endpoint, so the field set is read
	// off a sample of real records. That is also the more accurate answer: it
	// reports the fields this deployment's shipper actually writes, not the ones a
	// static catalogue expects it to.
	logs, err := s.QueryLogs(ctx, FetchLogRequest{
		AccountId:         req.AccountId,
		LogProvider:       req.LogProvider,
		LogProviderSource: req.LogProviderSource,
		StartTime:         startTime,
		EndTime:           endTime,
		Limit:             cubeAPMLabelSampleSize,
	})
	if err != nil {
		return nil, err
	}

	labelSet := map[string]struct{}{}
	for _, log := range logs {
		for name := range log.Labels {
			labelSet[name] = struct{}{}
		}
	}

	names := make([]string, 0, len(labelSet))
	for name := range labelSet {
		names = append(names, name)
	}
	sort.Strings(names)

	labels := make([]OutputLogLabel, 0, len(names))
	for _, name := range names {
		labels = append(labels, OutputLogLabel{Label: name, Attributes: map[string]any{}})
	}
	return labels, nil
}

// buildCubeAPMLabelValuesQuery emits a LogsQL pipeline that returns one row per
// distinct value of a field.
//
// `uniq by` is the distinct, done server-side: the wire carries each value once
// rather than once per matching record, and no count comes back with it. It
// replaced `stats by (field) count() | sort`, which built a per-value counter for
// every distinct value in the window and only then discarded all but 100 of them,
// plus a sort nothing needed — on a high-cardinality field that is the label-value
// picker holding the whole group set in memory to answer a dropdown.
//
// `limit N` is the memory bound, not a scan bound: LogsQL keeps unique entries in
// memory during execution, and the limit caps how many it accumulates. Reaching it
// returns an arbitrary subset, which is why values no longer arrive most-frequent
// first — `uniq` output is unordered, and re-adding `sort` would reintroduce the
// full-scan-and-rank this change exists to remove.
//
// One field per query, not `uniq by (f1, f2)`: that form returns unique SETS of the
// listed fields, so on an instance populating both alias fields the limit would be
// spent on (deployment, service) pairs and surface only a handful of real values.
func buildCubeAPMLabelValuesQuery(env, field string, limit int) string {
	return fmt.Sprintf(`%s | uniq by (%s) limit %d`,
		cubeAPMBaseQuery(env, field+":*"), field, limit)
}

func (s *CubeAPMLogSource) QueryLabelValues(ctx *security.RequestContext, req FetchLogLabelValuesRequest) ([]OutputLogLabelValue, error) {
	cfg, err := integrations.GetCubeAPMConfigs(ctx, req.AccountId)
	if err != nil {
		return nil, fmt.Errorf("failed to get CubeAPM configs: %w", err)
	}

	fields := cubeAPMFieldsFor(req.LabelName, cubeAPMLogLabelMapping)
	for _, field := range fields {
		if !isSafeCubeAPMField(field) {
			return nil, fmt.Errorf("invalid or unsafe field name: %q", req.LabelName)
		}
	}

	values := make([]OutputLogLabelValue, 0, cubeAPMLabelValueLimit)
	seen := make(map[string]struct{}, cubeAPMLabelValueLimit)

	// An alias label is backed by whichever field this instance populates, so ask
	// each in turn and merge. An unpopulated field returns no rows almost
	// immediately.
	for _, field := range fields {
		logsQL := buildCubeAPMLabelValuesQuery(cfg.Env, field, cubeAPMLabelValueLimit)

		rows, err := cubeAPMLogSearch(cfg, logsQL, req.StartTime, req.EndTime, 0, cubeAPMLogQueryTimeout)
		if err != nil {
			return nil, err
		}

		// `uniq` already deduplicated within this field; the seen set is what keeps a
		// value that both alias fields carry from appearing twice.
		for _, row := range rows {
			v := cubeAPMString(row[field])
			if v == "" {
				continue
			}
			if _, dup := seen[v]; dup {
				continue
			}
			seen[v] = struct{}{}
			values = append(values, OutputLogLabelValue{Value: v, Attributes: map[string]any{}})
			if len(values) >= cubeAPMLabelValueLimit {
				return values, nil
			}
		}
	}
	return values, nil
}

// cubeAPMErrorSeverities are the severity values that make a record an "error log"
// for the log-grouping view. Matched case-insensitively via a regex filter.
var cubeAPMErrorSeverities = []string{"error", "err", "critical", "crit", "fatal", "emergency", "alert", "panic", "severe"}

// cubeAPMExcludedContainers are platform containers whose noise would otherwise
// dominate the grouped view. Mirrors the exclusion list the Prometheus and
// OpenObserve log-group paths already apply.
var cubeAPMExcludedContainers = []string{"istio-proxy", "linkerd-proxy", "envoy", "vault-agent", "config-reloader"}

// cubeAPMLogGroupFieldSet names the CubeAPM field behind each grouping dimension,
// paired with the LogGroup field each populates.
//
// Message and Level are ordered candidate lists, highest priority first, because
// one instance routinely carries both shapes: CubeAPM's own OTel SDK data sets
// `log.level` and `_msg`, while Kubernetes logs whose JSON body was lifted apart at
// ingestion carry `level` and a short `msg` beside the full line in `_msg`. The
// query coalesces each list into one synthetic field, so a record is grouped on the
// first candidate it actually has.
type cubeAPMLogGroupFieldSet struct {
	Message   []string
	Namespace string
	Pod       string
	Workload  string
	Container string
	Level     []string
	Service   string
}

// The synthetic fields the group query coalesces Message and Level into.
const (
	cubeAPMLogGroupMessageField = "cube_message"
	cubeAPMLogGroupLevelField   = "cube_level"
)

// cubeAPMLogGroupFields is the field set an account with no label-mapping override
// groups on.
var cubeAPMLogGroupFields = cubeAPMLogGroupFieldSet{
	// `msg` first: grouping a JSON log on its full line groups on the timestamp
	// embedded in it, one group per record. `_msg` stays the fallback for plain-text
	// lines and for records with no separate message field.
	Message:   []string{"msg", "_msg"},
	Namespace: "k8s.namespace.name",
	Pod:       "k8s.pod.name",
	Workload:  "k8s.deployment.name",
	Container: "k8s.container.name",
	Level:     []string{"log.level", "level"},
	// The OTel-native identity, present whether or not the k8s.* fields are. It is
	// grouped on so a group still has a name to show — and a filter to drill into —
	// on an instance that ships no Kubernetes attributes. See cubeAPMLogFieldAliases.
	Service: "service",
}

// cubeAPMLogGroupFieldsFromMapping resolves the grouping fields through the account's
// merged label mapping, so a remap in Advanced Settings (say `level` -> `severity`)
// changes the Log Groups query the same way it already changes the Logs filters.
//
// Where two canonical names back one dimension (`level`/`severity`,
// `app`/`workload`), the first one mapped away from the provider default wins, so
// an operator editing either name is honoured. For Message and Level the override
// is tried first and the defaults stay behind it, so remapping to a field only some
// records carry does not drop the rest. A mapped value that is not a safe LogsQL
// field name is ignored rather than interpolated into the pipeline.
func cubeAPMLogGroupFieldsFromMapping(mapping map[string]string) cubeAPMLogGroupFieldSet {
	d := cubeAPMLogGroupFields
	override := func(canonical ...string) string {
		for _, name := range canonical {
			v := strings.TrimSpace(mapping[name])
			if v != "" && v != cubeAPMLogLabelMapping[name] && isSafeCubeAPMField(v) {
				return v
			}
		}
		return ""
	}
	pick := func(def string, canonical ...string) string {
		if v := override(canonical...); v != "" {
			return v
		}
		return def
	}
	candidates := func(defaults []string, canonical ...string) []string {
		return cubeAPMDedupeFields(append([]string{override(canonical...)}, defaults...))
	}
	return cubeAPMLogGroupFieldSet{
		Message:   candidates(d.Message, "message", "body"),
		Namespace: pick(d.Namespace, "namespace"),
		Pod:       pick(d.Pod, "pod"),
		Workload:  pick(d.Workload, "workload", "app"),
		Container: pick(d.Container, "container"),
		Level:     candidates(d.Level, "level", "severity"),
		Service:   pick(d.Service, "service"),
	}
}

// cubeAPMDedupeFields drops empty and repeated entries, keeping first-seen order.
func cubeAPMDedupeFields(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, v := range values {
		if _, dup := seen[v]; v == "" || dup {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

// cubeAPMAnyOf renders one condition per field, OR'd, as a single term.
func cubeAPMAnyOf(fields []string, condition func(field string) string) string {
	terms := make([]string, len(fields))
	for i, field := range fields {
		terms[i] = condition(field)
	}
	if len(terms) == 1 {
		return terms[0]
	}
	return "(" + strings.Join(terms, " OR ") + ")"
}

// cubeAPMCoalescePipes copies the first present candidate into target. Pipes run
// lowest priority first, each overwriting the last only when its field exists.
func cubeAPMCoalescePipes(candidates []string, target string) []string {
	pipes := make([]string, 0, len(candidates))
	for i := len(candidates) - 1; i >= 0; i-- {
		if i == len(candidates)-1 {
			pipes = append(pipes, fmt.Sprintf(`format "<%s>" as %s`, candidates[i], target))
			continue
		}
		pipes = append(pipes, fmt.Sprintf(`format if (%s:*) "<%s>" as %s`, candidates[i], candidates[i], target))
	}
	return pipes
}

// buildCubeAPMLogGroupQuery emits a LogsQL pipeline that aggregates error logs
// server-side.
//
// Grouping on the exact message matches every other provider here:
// generatePatternHash is a hash of the raw message bytes, so pulling raw records
// back to group them in Go would cost a full scan for no extra fidelity.
func buildCubeAPMLogGroupQuery(f cubeAPMLogGroupFieldSet, env, selectedNamespace, selectedWorkload string, limit int) string {
	if limit <= 0 {
		limit = cubeAPMLogGroupLimit
	}
	severityRe := cubeAPMQuote("(?i)^(" + strings.Join(cubeAPMErrorSeverities, "|") + ")$")

	conditions := []string{
		// A record with no message has nothing to group on or display.
		cubeAPMAnyOf(f.Message, func(field string) string { return field + ":*" }),
		cubeAPMAnyOf(f.Level, func(field string) string { return field + ":~" + severityRe }),
	}

	for _, c := range cubeAPMExcludedContainers {
		conditions = append(conditions, fmt.Sprintf("NOT %s:=%s", f.Container, cubeAPMQuote(c)))
	}
	if selectedNamespace != "" {
		conditions = append(conditions, fmt.Sprintf("%s:=%s", f.Namespace, cubeAPMQuote(selectedNamespace)))
	}
	if selectedWorkload != "" {
		// Pods are named {workload}-{replica-suffix}, so the workload filter is a
		// prefix match on the pod rather than an equality on a workload field —
		// which also covers StatefulSets and Jobs, whose records carry no
		// deployment name at all. OR'd with the service name so the filter still
		// selects something on an instance that ships no k8s.* fields, where the
		// pod-prefix term matches nothing and silently emptied the view.
		// The asterisk goes OUTSIDE the quotes: `"name-"*` is a prefix match, while
		// `"name-*"` is a literal phrase that matched no pod at all (verified live).
		conditions = append(conditions, fmt.Sprintf("(%s:%s* OR %s:=%s)",
			f.Pod, cubeAPMQuote(selectedWorkload+"-"),
			f.Service, cubeAPMQuote(selectedWorkload)))
	}

	pipes := append(cubeAPMCoalescePipes(f.Message, cubeAPMLogGroupMessageField),
		cubeAPMCoalescePipes(f.Level, cubeAPMLogGroupLevelField)...)

	// A remap can point two dimensions at one field (workload -> service); name it
	// once, since `stats by` rejects a repeated field.
	groupBy := strings.Join(cubeAPMDedupeFields([]string{
		cubeAPMLogGroupMessageField, f.Namespace, f.Pod, f.Workload, f.Container, cubeAPMLogGroupLevelField, f.Service,
	}), ", ")

	return fmt.Sprintf(`%s | %s | stats by (%s) count() as cube_count | sort ("cube_count" desc) | limit %d`,
		cubeAPMBaseQuery(env, strings.Join(conditions, " AND ")), strings.Join(pipes, " | "), groupBy, limit)
}

// QueryLogGroup makes CubeAPMLogSource satisfy LogGroupSource, so the Log Groups
// view resolves through the log provider instead of erroring with an unsupported
// provider/source combination.
func (s *CubeAPMLogSource) QueryLogGroup(ctx *security.RequestContext, req FetchLogGroupRequest) (LogGroupOutput, error) {
	cfg, err := integrations.GetCubeAPMConfigs(ctx, req.AccountId)
	if err != nil {
		return LogGroupOutput{}, fmt.Errorf("failed to get CubeAPM configs: %w", err)
	}

	fields := cubeAPMLogGroupFieldsFromMapping(getMergedLabelMapping(ctx, req.AccountId, s))
	logsQL := buildCubeAPMLogGroupQuery(
		fields,
		cfg.Env,
		common.GetString(req.Request, "selectedNamespace"),
		common.GetString(req.Request, "selectedWorkload"),
		cubeAPMLogGroupLimit,
	)

	ctx.GetLogger().Info("CubeAPM Log Group Query", "query", logsQL)

	startMs, endMs := cubeAPMTimeRangeMillis(req.StartTime, req.EndTime, time.Now())
	rows, err := cubeAPMLogSearch(cfg, logsQL, startMs, endMs, 0, cubeAPMLogGroupTimeout)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "timeout") {
			return LogGroupOutput{}, fmt.Errorf(
				"log group query timed out — the selected time range contains too many logs. " +
					"Please apply more filters: select a specific Namespace or Workload to narrow the scope",
			)
		}
		return LogGroupOutput{}, err
	}

	// The aggregation carries no per-group timestamp (LogsQL's documented stats
	// functions have no max()), so every group is stamped with the end of the
	// query window — the same fallback the OpenObserve path uses for groups whose
	// aggregate timestamp is missing.
	return convertCubeAPMLogGroups(fields, rows, endMs/1000), nil
}

// convertCubeAPMLogGroups maps aggregated rows onto the shared LogGroup contract.
// Timestamps are emitted in epoch seconds — the frontend multiplies by 1000.
func convertCubeAPMLogGroups(f cubeAPMLogGroupFieldSet, rows []map[string]any, timestampSec int64) LogGroupOutput {
	groups := make([]LogGroup, 0, len(rows))

	for _, row := range rows {
		sample := cubeAPMString(row[cubeAPMLogGroupMessageField])
		if sample == "" {
			continue
		}
		// stats counts arrive as strings in the NDJSON body, so this parses rather
		// than type-asserting a number.
		count, err := strconv.ParseInt(strings.TrimSpace(cubeAPMString(row["cube_count"])), 10, 64)
		if err != nil || count <= 0 {
			continue
		}

		pod := cubeAPMString(row[f.Pod])
		workload := cubeAPMString(row[f.Workload])
		if workload == "" {
			workload = extractWorkloadFromPodName(pod)
		}
		// Last resort, and the only one that fires on a CubeAPM with no Kubernetes
		// attributes: the OTel service. Without it the group has no workload at all,
		// so the UI has nothing to build a filter from and refuses to fetch the
		// surrounding logs ("No label filters available"). Only reached when both
		// k8s fields were empty, so a k8s-enriched instance is unaffected.
		if workload == "" {
			workload = cubeAPMString(row[f.Service])
		}

		group := LogGroup{
			Sample:      sample,
			Namespace:   cubeAPMString(row[f.Namespace]),
			Workload:    workload,
			Container:   cubeAPMString(row[f.Container]),
			Level:       cubeAPMString(row[cubeAPMLogGroupLevelField]),
			Count:       count,
			Timestamps:  []int64{timestampSec},
			Values:      []float64{float64(count)},
			PatternHash: generatePatternHash(sample),
		}

		// container_id mirrors the Prometheus format so the UI can parse namespace
		// and workload back out of a single field.
		if group.Namespace != "" && group.Workload != "" {
			if group.Container != "" {
				group.ContainerID = fmt.Sprintf("/k8s/%s/%s/%s", group.Namespace, group.Workload, group.Container)
			} else {
				group.ContainerID = fmt.Sprintf("/k8s/%s/%s", group.Namespace, group.Workload)
			}
		}

		groups = append(groups, group)
	}

	return LogGroupOutput{Groups: groups}
}
