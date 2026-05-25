// Package hiveclient is a thin shared HiveServer2 (Thrift) wrapper used by
// both the integrations registry (for config autogen) and the observability
// query path. It has no observability-package coupling so it can be imported
// from either side without creating a cycle.
package hiveclient

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/beltran/gohive"
)

// ColumnSpec describes one column from a Hive DESCRIBE response.
// IsPartition is true for columns Hive uses as partition keys.
type ColumnSpec struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	IsPartition bool   `json:"is_partition,omitempty"`
}

// ConnectConfig configures a HiveServer2 connection. Auth is "none" or "ldap"
// (case-insensitive); other values default to NONE so the call doesn't fail.
//
// UseTLS opts into TLS on the Thrift socket; most internal HS2 deployments
// run plaintext, so the default (false) matches the common case.
//
// TLSSkipVerify is an explicit opt-in to bypass certificate-chain validation.
// Required for self-signed deployments, but a footgun in production — LDAP
// credentials travel plaintext inside the TLS tunnel, so a MITM with skip-
// verify on can capture them. Default false: TLS is strict unless the
// integration owner explicitly chose otherwise via the form.
type ConnectConfig struct {
	Host          string
	Port          int
	Auth          string
	Username      string
	Password      string
	Database      string
	UseTLS        bool
	TLSSkipVerify bool

	ConnectTimeout time.Duration
	SocketTimeout  time.Duration
}

// ParseHostPort splits a "host:port" string. Strips an optional scheme
// (hive://, thrift://, http://, https://) and trailing path, and reports
// whether the scheme implies TLS (https://, hive+ssl://). Empty input is an
// error.
func ParseHostPort(raw string) (host string, port int, useTLS bool, err error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", 0, false, fmt.Errorf("hive_url is required")
	}
	if i := strings.Index(s, "://"); i >= 0 {
		switch strings.ToLower(s[:i]) {
		case "https", "hive+ssl", "thrift+ssl":
			useTLS = true
		}
		s = s[i+3:]
	}
	if i := strings.IndexAny(s, "/?"); i >= 0 {
		s = s[:i]
	}
	h, portStr, ok := strings.Cut(s, ":")
	if !ok || h == "" || portStr == "" {
		return "", 0, false, fmt.Errorf("hive_url must be host:port (got %q)", raw)
	}
	p, convErr := strconv.Atoi(portStr)
	if convErr != nil || p <= 0 {
		return "", 0, false, fmt.Errorf("hive_url has invalid port %q", portStr)
	}
	return h, p, useTLS, nil
}

// Connect opens a HiveServer2 Thrift connection per cfg. Caller is responsible
// for closing the returned *gohive.Connection.
func Connect(cfg ConnectConfig) (*gohive.Connection, error) {
	gc := gohive.NewConnectConfiguration()
	gc.Database = cfg.Database
	if cfg.Database == "" {
		gc.Database = "default"
	}
	gc.ConnectTimeout = cfg.ConnectTimeout
	if gc.ConnectTimeout == 0 {
		gc.ConnectTimeout = 30 * time.Second
	}
	gc.SocketTimeout = cfg.SocketTimeout
	if gc.SocketTimeout == 0 {
		gc.SocketTimeout = 60 * time.Second
	}
	if cfg.UseTLS {
		gc.TLSConfig = &tls.Config{ServerName: cfg.Host}
		if cfg.TLSSkipVerify {
			gc.TLSConfig.InsecureSkipVerify = true //nolint:gosec // explicit per-integration opt-in for self-signed Hive deployments
		}
	}

	auth := "NONE"
	switch strings.ToLower(strings.TrimSpace(cfg.Auth)) {
	case "ldap":
		auth = "LDAP"
		gc.Username = cfg.Username
		gc.Password = cfg.Password
	default:
		// HiveServer2 with NONE auth still requires a non-empty username on the wire.
		if cfg.Username != "" {
			gc.Username = cfg.Username
		} else {
			gc.Username = "anonymous"
		}
	}

	c, err := gohive.Connect(cfg.Host, cfg.Port, auth, gc)
	if err != nil {
		return nil, fmt.Errorf("hive connect %s:%d: %w", cfg.Host, cfg.Port, err)
	}
	return c, nil
}

// RawResult is the column-and-rows shape of a Hive query result.
type RawResult struct {
	Columns []string
	Rows    [][]any
}

// sessionDefaults are SET statements applied to every cursor before the
// user's query runs. fetch.task.conversion=more + threshold=-1 ask Hive to
// satisfy simple SELECT … WHERE … LIMIT … (no aggregation, no sort) via the
// fetch task instead of MapReduce. This keeps log queries working on
// HiveServer2 deployments where local-mode MR is broken or slow.
var sessionDefaults = []string{
	"SET hive.fetch.task.conversion=more",
	"SET hive.fetch.task.conversion.threshold=-1",
}

// RunQuery executes a single HiveQL statement and returns the result set.
// Caller supplies the timeout via ctx. Session defaults that bias Hive
// toward the fetch task are applied to the cursor before the user query
// runs.
func RunQuery(ctx context.Context, conn *gohive.Connection, sqlQuery string) (*RawResult, error) {
	cursor := conn.Cursor()
	defer cursor.Close()

	for _, stmt := range sessionDefaults {
		cursor.Exec(ctx, stmt)
		if cErr := cursor.Error(); cErr != nil {
			// SET failures are non-fatal — these are pure optimisation hints
			// (push simple SELECTs through the fetch task); the user query
			// still works even if the hint didn't apply. Some clusters
			// (Sentry / Ranger / restricted Hive 1.x) gate SET privileges,
			// and we don't want session setup to block the integration on
			// those. gohive's Cursor.Exec resets the error state on the next
			// call, so we just log and proceed.
			slog.Warn("hive session SET failed; continuing without the hint",
				"statement", stmt, "error", cErr)
		}
	}

	cursor.Exec(ctx, sqlQuery)
	if cErr := cursor.Error(); cErr != nil {
		return nil, fmt.Errorf("hive query failed: %w", cErr)
	}

	desc := cursor.Description() // [][]string of [columnName, columnType, ...]
	columns := make([]string, len(desc))
	for i, d := range desc {
		if len(d) > 0 {
			columns[i] = d[0]
		}
	}

	rows := make([][]any, 0)
	for cursor.HasMore(ctx) {
		row := cursor.RowMap(ctx)
		if rErr := cursor.Error(); rErr != nil {
			return nil, fmt.Errorf("hive fetch failed: %w", rErr)
		}
		flat := make([]any, len(columns))
		for i, col := range columns {
			if v, ok := row[col]; ok {
				flat[i] = v
				continue
			}
			// gohive sometimes keys RowMap by "table.column".
			if dot := strings.LastIndex(col, "."); dot >= 0 && dot+1 < len(col) {
				if v, ok := row[col[dot+1:]]; ok {
					flat[i] = v
				}
			}
		}
		rows = append(rows, flat)
	}

	return &RawResult{Columns: columns, Rows: rows}, nil
}

// SampleColumn runs `SELECT col FROM db.table WHERE col IS NOT NULL LIMIT 1`
// and returns the value as a string. Returns ("", nil) when the table has no
// matching rows (caller decides whether that's an error). Used by strict
// ValidateConfig probes that need a representative value — e.g. to detect
// the layout of a STRING-typed timestamp column.
func SampleColumn(ctx context.Context, conn *gohive.Connection, db, table, col string) (string, error) {
	sql := fmt.Sprintf("SELECT %s FROM %s WHERE %s IS NOT NULL LIMIT 1",
		QuoteIdent(col), QualifiedTable(db, table), QuoteIdent(col))
	r, err := RunQuery(ctx, conn, sql)
	if err != nil {
		return "", fmt.Errorf("sample %s: %w", col, err)
	}
	if len(r.Rows) == 0 || len(r.Rows[0]) == 0 {
		return "", nil
	}
	switch v := r.Rows[0][0].(type) {
	case string:
		return v, nil
	case nil:
		return "", nil
	default:
		return fmt.Sprintf("%v", v), nil
	}
}

// KnownTimestampLayouts is the ordered list of Go time layouts we attempt
// against a sample value when auto-detecting a STRING-typed timestamp
// column's format. First match wins. Used by both the integrations layer
// (strict ValidateConfig) and the observability query path.
var KnownTimestampLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02T15:04:05.000000Z",
	"2006-01-02T15:04:05.000Z",
	time.RFC3339,
	"2006-01-02T15:04:05Z",
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05.999999",
	"2006-01-02 15:04:05.000",
	"2006-01-02 15:04:05",
	"2006-01-02",
	"01/02/2006 15:04:05",
	"02/01/2006 15:04:05",
	"02-Jan-2006 15:04:05",
	"02-Jan-2006",
	time.RFC1123,
	time.RFC1123Z,
	time.RFC822,
	time.RFC822Z,
}

// DetectTimestampFormat tries each layout in KnownTimestampLayouts against
// the trimmed sample and returns the first one that parses, or "" when no
// layout matches.
func DetectTimestampFormat(sample string) string {
	s := strings.TrimSpace(sample)
	if s == "" {
		return ""
	}
	for _, layout := range KnownTimestampLayouts {
		if _, err := time.Parse(layout, s); err == nil {
			return layout
		}
	}
	return ""
}

// QuoteIdent wraps each segment of a Hive column reference in backticks.
// "kubernetes.namespace_name" → "`kubernetes`.`namespace_name`".
// Embedded backticks are doubled per HiveQL escaping rules.
func QuoteIdent(s string) string {
	parts := strings.Split(s, ".")
	for i, p := range parts {
		parts[i] = "`" + strings.ReplaceAll(p, "`", "``") + "`"
	}
	return strings.Join(parts, ".")
}

// QualifiedTable returns `db`.`table` (or just `table` if db is empty).
func QualifiedTable(db, table string) string {
	if db == "" {
		return QuoteIdent(table)
	}
	return QuoteIdent(db) + "." + QuoteIdent(table)
}

// DescribeColumns runs `DESCRIBE <db>.<table>` and parses the result into a
// slice of ColumnSpec. Partition columns are marked IsPartition=true. Does
// NOT recurse into struct types — use FlattenColumns on the result for that.
//
// DESCRIBE output layout:
//
//	<regular cols...>
//	<blank>
//	# Partition Information
//	# col_name    data_type    comment
//	<partition cols...>
func DescribeColumns(ctx context.Context, conn *gohive.Connection, db, table string) ([]ColumnSpec, error) {
	r, err := RunQuery(ctx, conn, fmt.Sprintf("DESCRIBE %s", QualifiedTable(db, table)))
	if err != nil {
		return nil, fmt.Errorf("describe %s.%s: %w", db, table, err)
	}
	return parseDescribeResult(r), nil
}

// parseDescribeResult converts a DESCRIBE result-set into a deduplicated list
// of ColumnSpec. Extracted from DescribeColumns so the parser is testable
// without a live HiveServer2 connection.
//
// Hive emits partition columns TWICE in DESCRIBE output — first in the main
// column section, then again under "# Partition Information". We track each
// name emitted in the regular pass so the partition pass flips IsPartition
// on the existing entry instead of producing a duplicate.
func parseDescribeResult(r *RawResult) []ColumnSpec {
	if r == nil {
		return nil
	}
	nameIdx, typeIdx := -1, -1
	for i, c := range r.Columns {
		switch strings.ToLower(c) {
		case "col_name", "name":
			nameIdx = i
		case "data_type", "type":
			typeIdx = i
		}
	}
	if nameIdx < 0 {
		nameIdx = 0
	}
	if typeIdx < 0 && len(r.Columns) > 1 {
		typeIdx = 1
	}

	cols := make([]ColumnSpec, 0, len(r.Rows))
	idxByName := make(map[string]int)
	inPartitionSection := false
	for _, row := range r.Rows {
		if nameIdx >= len(row) {
			continue
		}
		name := strings.TrimSpace(fmt.Sprintf("%v", row[nameIdx]))
		if name == "" {
			continue
		}
		if strings.HasPrefix(name, "#") {
			if strings.Contains(strings.ToLower(name), "partition information") {
				inPartitionSection = true
			}
			continue
		}
		if inPartitionSection {
			if i, ok := idxByName[name]; ok {
				cols[i].IsPartition = true
				continue
			}
			// Partition column we didn't see in the main section — emit it.
		}
		dataType := ""
		if typeIdx >= 0 && typeIdx < len(row) {
			dataType = strings.TrimSpace(fmt.Sprintf("%v", row[typeIdx]))
		}
		cols = append(cols, ColumnSpec{
			Name:        name,
			Type:        dataType,
			IsPartition: inPartitionSection,
		})
		idxByName[name] = len(cols) - 1
	}
	return cols
}
