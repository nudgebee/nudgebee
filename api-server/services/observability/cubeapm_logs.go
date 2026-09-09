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

// buildCubeAPMBinaryClause renders one binary where-clause into LogsQL conditions.
func buildCubeAPMBinaryClause(binary query.BinaryWhereClause, mapping map[string]string) (string, error) {
	var parts []string
	for field, ops := range binary {
		col := field
		if mapped, ok := mapping[col]; ok {
			col = mapped
		}
		if !isSafeCubeAPMField(col) {
			return "", fmt.Errorf("invalid or unsafe field name: %q", col)
		}
		for op, val := range ops {
			strVal := fmt.Sprintf("%v", val)
			switch op {
			case query.Eq:
				parts = append(parts, fmt.Sprintf("%s:=%s", col, cubeAPMQuote(strVal)))
			case query.Nq:
				parts = append(parts, fmt.Sprintf("NOT %s:=%s", col, cubeAPMQuote(strVal)))
			case query.Contains, query.ILike:
				// LogsQL's substring form is *value* and is already
				// case-insensitive for the word index, which is the closest
				// thing it has to ILIKE.
				parts = append(parts, fmt.Sprintf("%s:%s", col, cubeAPMQuote("*"+strVal+"*")))
			case query.Regex:
				parts = append(parts, fmt.Sprintf("%s:~%s", col, cubeAPMQuote(strVal)))
			default:
				return "", fmt.Errorf("unsupported operator for CubeAPM logs: %s", op)
			}
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
	if ts := cubeAPMString(row["_time"]); ts != "" {
		out.Timestamp = ts
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

func (s *CubeAPMLogSource) QueryLabelValues(ctx *security.RequestContext, req FetchLogLabelValuesRequest) ([]OutputLogLabelValue, error) {
	cfg, err := integrations.GetCubeAPMConfigs(ctx, req.AccountId)
	if err != nil {
		return nil, fmt.Errorf("failed to get CubeAPM configs: %w", err)
	}

	field := req.LabelName
	if mapped, ok := cubeAPMLogLabelMapping[field]; ok {
		field = mapped
	}
	if !isSafeCubeAPMField(field) {
		return nil, fmt.Errorf("invalid or unsafe field name: %q", req.LabelName)
	}

	// `stats by (field)` is an exact distinct-value list, which sampling records
	// would only approximate — a value that appears once in a million-line window
	// is still a legitimate filter choice and a sample would miss it.
	logsQL := fmt.Sprintf(`%s | stats by (%s) count() as cube_count | sort ("cube_count" desc) | limit %d`,
		cubeAPMBaseQuery(cfg.Env, field+":*"), field, cubeAPMLabelValueLimit)

	rows, err := cubeAPMLogSearch(cfg, logsQL, req.StartTime, req.EndTime, 0, cubeAPMLogQueryTimeout)
	if err != nil {
		return nil, err
	}

	values := make([]OutputLogLabelValue, 0, len(rows))
	for _, row := range rows {
		if v := cubeAPMString(row[field]); v != "" {
			values = append(values, OutputLogLabelValue{Value: v, Attributes: map[string]any{}})
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

// cubeAPMLogGroupFields are the grouping dimensions, paired with the LogGroup field
// each populates.
var cubeAPMLogGroupFields = struct {
	Message   string
	Namespace string
	Pod       string
	Workload  string
	Container string
	Level     string
}{
	Message:   "_msg",
	Namespace: "k8s.namespace.name",
	Pod:       "k8s.pod.name",
	Workload:  "k8s.deployment.name",
	Container: "k8s.container.name",
	Level:     "log.level",
}

// buildCubeAPMLogGroupQuery emits a LogsQL pipeline that aggregates error logs
// server-side.
//
// Grouping on the exact message matches every other provider here:
// generatePatternHash is a hash of the raw message bytes, so pulling raw records
// back to group them in Go would cost a full scan for no extra fidelity.
func buildCubeAPMLogGroupQuery(env, selectedNamespace, selectedWorkload string, limit int) string {
	if limit <= 0 {
		limit = cubeAPMLogGroupLimit
	}
	f := cubeAPMLogGroupFields

	conditions := []string{
		// A record with no message has nothing to group on or display.
		fmt.Sprintf("%s:*", f.Message),
		fmt.Sprintf("%s:~%s", f.Level, cubeAPMQuote("(?i)^("+strings.Join(cubeAPMErrorSeverities, "|")+")$")),
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
		// deployment name at all.
		conditions = append(conditions, fmt.Sprintf("%s:%s", f.Pod, cubeAPMQuote(selectedWorkload+"-*")))
	}

	groupBy := strings.Join([]string{f.Message, f.Namespace, f.Pod, f.Workload, f.Container, f.Level}, ", ")

	return fmt.Sprintf(`%s | stats by (%s) count() as cube_count | sort ("cube_count" desc) | limit %d`,
		cubeAPMBaseQuery(env, strings.Join(conditions, " AND ")), groupBy, limit)
}

// QueryLogGroup makes CubeAPMLogSource satisfy LogGroupSource, so the Log Groups
// view resolves through the log provider instead of erroring with an unsupported
// provider/source combination.
func (s *CubeAPMLogSource) QueryLogGroup(ctx *security.RequestContext, req FetchLogGroupRequest) (LogGroupOutput, error) {
	cfg, err := integrations.GetCubeAPMConfigs(ctx, req.AccountId)
	if err != nil {
		return LogGroupOutput{}, fmt.Errorf("failed to get CubeAPM configs: %w", err)
	}

	logsQL := buildCubeAPMLogGroupQuery(
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
	return convertCubeAPMLogGroups(rows, endMs/1000), nil
}

// convertCubeAPMLogGroups maps aggregated rows onto the shared LogGroup contract.
// Timestamps are emitted in epoch seconds — the frontend multiplies by 1000.
func convertCubeAPMLogGroups(rows []map[string]any, timestampSec int64) LogGroupOutput {
	f := cubeAPMLogGroupFields
	groups := make([]LogGroup, 0, len(rows))

	for _, row := range rows {
		sample := cubeAPMString(row[f.Message])
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

		group := LogGroup{
			Sample:      sample,
			Namespace:   cubeAPMString(row[f.Namespace]),
			Workload:    workload,
			Container:   cubeAPMString(row[f.Container]),
			Level:       cubeAPMString(row[f.Level]),
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
