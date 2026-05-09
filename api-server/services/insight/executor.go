package insight

import (
	"context"
	"fmt"
	"log/slog"
	"nudgebee/services/common"
	"nudgebee/services/internal/database"
	"nudgebee/services/query"
	"nudgebee/services/relay"
	"nudgebee/services/security"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/samber/lo"
)

const insightQueryTimeout = 5 * time.Minute

// Pre-compiled — called per row in insight processing loops.
var insightTitlePlaceholderRe = regexp.MustCompile(`\{([a-zA-Z_]+)\}`)

// formatInsightTitle replaces {column_name} placeholders with row values
// and {} with the numeric count value.
// For currency values (when format contains ${}), formats as $X.XX with 2 decimal places.
func formatInsightTitle(format string, value int64, rowValues map[string]any) string {
	result := format
	result = insightTitlePlaceholderRe.ReplaceAllStringFunc(result, func(match string) string {
		colName := match[1 : len(match)-1]
		if v, ok := rowValues[colName]; ok {
			switch val := v.(type) {
			case []byte:
				return string(val)
			case string:
				return val
			default:
				return fmt.Sprintf("%v", val)
			}
		}
		return match
	})

	// Check if this is a currency format (contains ${})
	if strings.Contains(result, "${}") {
		// Format as currency with 2 decimal places
		result = strings.ReplaceAll(result, "${}", fmt.Sprintf("$%.2f", float64(value)))
	} else {
		// Format as integer for non-currency values
		result = strings.ReplaceAll(result, "{}", fmt.Sprintf("%d", value))
	}
	return result
}

func eventTimeParams(rule InsightRule) string {
	if rule.Source != InsightSourceEvent && rule.Source != InsightSourcePrometheus {
		return ""
	}
	rangeDays := rule.Range
	if rangeDays <= 0 {
		rangeDays = 1
	}
	unit := rule.RangeUnit
	if unit == "" {
		unit = InsightRangeUnitDay
	}
	now := time.Now().UTC()
	var start time.Time
	switch unit {
	case InsightRangeUnitHour:
		start = now.Add(-time.Duration(rangeDays) * time.Hour)
	case InsightRangeUnitWeek:
		start = now.AddDate(0, 0, -rangeDays*7)
	case InsightRangeUnitMonth:
		start = now.AddDate(0, -rangeDays, 0)
	default:
		start = now.AddDate(0, 0, -rangeDays)
	}
	return fmt.Sprintf("&start_time=%d&end_time=%d", start.UnixMilli(), now.UnixMilli())
}

func computeRedirectURL(rule InsightRule, accountID string, cloudProvider string) string {
	cp := strings.ToLower(cloudProvider)
	id := accountID

	if cp == "k8s" {
		return computeK8sRedirectURL(rule, id)
	}
	return computeCloudRedirectURL(rule, id)
}

func computeK8sRedirectURL(rule InsightRule, id string) string {
	base := "/kubernetes/details/" + id

	switch rule.Source {
	case InsightSourcePrometheus:
		return computeK8sPrometheusRedirectURL(rule, base)
	case InsightSourceRecommendation:
		return computeK8sRecommendationRedirectURL(rule, base)
	case InsightSourceEvent:
		return computeK8sEventRedirectURL(rule, base)
	}
	return base
}

func computeK8sPrometheusRedirectURL(rule InsightRule, base string) string {
	tp := eventTimeParams(rule)
	switch rule.InsightSubCategory {
	case "Events":
		aggKey := getUIFilterValue(rule, "aggregation_key")
		if aggKey != "" {
			return base + "?eventAggregationKey=" + aggKey + "&eventStatus=FIRING" + tp + "#events/all-events"
		}
		return base + tp + "#events/all-events"
	case "Application":
		aggKey := getUIFilterValue(rule, "aggregation_key")
		if aggKey != "" {
			return base + "?eventAggregationKey=" + aggKey + "&eventStatus=FIRING" + tp + "#events/all-events"
		}
		return base + tp + "#events/all-events"
	case "Trace":
		return base + "#monitoring/traces"
	case "LogGroup":
		return base + "#monitoring/groups"
	case "Storage":
		return base + "#optimize/pv-rightsizing"
	}
	return base
}

func computeK8sRecommendationRedirectURL(rule InsightRule, base string) string {
	ruleName := getFilterValue(rule, "rule_name")
	category := getFilterValue(rule, "category")

	switch ruleName {
	case "pod_right_sizing":
		return base + "#optimize/right-sizing"
	case "unused_pvc":
		return base + "#optimize/unused-volume"
	case "pv_rightsize":
		return base + "#optimize/pv-rightsizing"
	case "abandoned_resource":
		return base + "#optimize/abandoned-resources"
	case "image_scan":
		return base + "#security/image-scan"
	case "eks_cluster_upgrade":
		return base + "#security/cluster-upgrade"
	case "certificate_expiry":
		return base + "#security/ssl-certificate-issues"
	}

	switch category {
	case "Security":
		return base + "#security/image-scan"
	case "InfraUpgrade":
		return base + "#security/cluster-upgrade"
	}

	// Ratio-type rules without rule_name filters — match by unique_id
	switch rule.UniqueID {
	case "17":
		return base + "#security/image-scan"
	case "19":
		return base + "#security/cluster-upgrade"
	}

	return base
}

func computeK8sEventRedirectURL(rule InsightRule, base string) string {
	sources := getFilterValues(rule, "source")
	source := getFilterValue(rule, "source")
	aggKey := getFilterValue(rule, "aggregation_key")
	tp := eventTimeParams(rule)

	switch source {
	case "pagerduty_webhook", "datadog_webhook", "servicenow_webhook", "zenduty_webhook":
		return base + "?source=" + strings.Join(sources, ",") + "&eventStatus=FIRING&sortBy=computed_score" + tp + "#events/all-events"
	case "slo":
		return base + "?eventAggregationKey=SLOViolation&eventStatus=FIRING" + tp + "#events/all-events"
	}
	if aggKey == "report_crash_loop" {
		return base + "?eventAggregationKey=report_crash_loop&eventStatus=FIRING" + tp + "#events/all-events"
	}
	if aggKey == "pod_oom_killer_enricher" {
		return base + "?eventAggregationKey=pod_oom_killer_enricher&eventStatus=FIRING" + tp + "#events/all-events"
	}
	if aggKey == "image_pull_backoff_reporter" {
		return base + "?eventAggregationKey=image_pull_backoff_reporter&eventStatus=FIRING" + tp + "#events/all-events"
	}

	status := getFilterValue(rule, "status")
	if status == "FIRING" {
		return base + "?eventStatus=FIRING&sortBy=computed_score" + tp + "#events/all-events"
	}

	return base + "?" + tp[1:] + "#events/all-events"
}

func computeCloudRedirectURL(rule InsightRule, id string) string {
	base := "/cloud-account/details/" + id + "?accountId=" + id

	if rule.Source == InsightSourceEvent {
		tp := eventTimeParams(rule)
		sources := getFilterValues(rule, "source")
		if len(sources) > 0 {
			return base + "&source=" + strings.Join(sources, ",") + "&eventStatus=FIRING" + tp + "#events/events"
		}
		return base + "&eventStatus=FIRING" + tp + "#events/events"
	}

	ruleNameParam := ""
	if rule.Source == InsightSourceRecommendation {
		ruleNames := getFilterValues(rule, "rule_name")
		if len(ruleNames) > 0 {
			ruleNameParam = "&ruleName=" + strings.Join(ruleNames, ",")
		}
	}

	cat := string(rule.InsightCategory)
	uid := rule.UniqueID

	switch cat {
	case "Security":
		return base + ruleNameParam + "#optimize/security"
	case "Configuration":
		return base + ruleNameParam + "#optimize/configuration"
	case "InfraUpgrade":
		return base + ruleNameParam + "#optimize/infra-upgrade"
	case "Optimization":
		return base + ruleNameParam + "#optimize/right-sizing"
	case "Cost":
		return base + "#summary"
	case "Performance":
		return base + "#services"
	}

	switch uid {
	case "110", "111", "112":
		return base + ruleNameParam + "#optimize/configuration"
	}

	if cat == "Ops" {
		return base + ruleNameParam + "#optimize/configuration"
	}

	return base + "#summary"
}

func getUIFilterValue(rule InsightRule, name string) string {
	for _, f := range rule.InsightUIFilters {
		if f.Name == name {
			return f.Value
		}
	}
	return ""
}

func getFilterValues(rule InsightRule, column string) []string {
	for _, f := range rule.Filters {
		if f.Column == column {
			if s, ok := f.Value.(string); ok {
				return []string{s}
			}
			if arr, ok := f.Value.([]interface{}); ok {
				var result []string
				for _, v := range arr {
					if s, ok := v.(string); ok {
						result = append(result, s)
					}
				}
				return result
			}
		}
	}
	return nil
}

func getFilterValue(rule InsightRule, column string) string {
	for _, f := range rule.Filters {
		if f.Column == column {
			if s, ok := f.Value.(string); ok {
				return s
			}
			// For "in" operator with slice, return the first value
			if arr, ok := f.Value.([]interface{}); ok && len(arr) > 0 {
				if s, ok := arr[0].(string); ok {
					return s
				}
			}
		}
	}
	return ""
}

var (
	allowedRangeUnits = map[InsightRangeUnit]bool{
		InsightRangeUnitHour:  true,
		InsightRangeUnitDay:   true,
		InsightRangeUnitWeek:  true,
		InsightRangeUnitMonth: true,
	}

	allowedFilterOperators = map[string]bool{
		"=": true, "!=": true, "<": true, ">": true,
		"<=": true, ">=": true, "in": true, "like": true,
	}

	// validSQLIdentifier matches safe SQL identifiers: letters, digits, underscores only
	validSQLIdentifier = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

	// validAggregateExpr matches aggregate expressions like max(column), sum(column_name), etc.
	allowedAggregateFunctions = map[string]bool{
		"max": true, "min": true, "sum": true, "avg": true, "count": true,
	}
	validAggregateExpr = regexp.MustCompile(`^([a-zA-Z_]+)\(([a-zA-Z_][a-zA-Z0-9_]*)\)$`)
)

func validateSQLIdentifier(name string) error {
	if !validSQLIdentifier.MatchString(name) {
		return fmt.Errorf("invalid SQL identifier: %q", name)
	}
	return nil
}

func buildJoinCondition(table1, table2 string, groupedBy []string) (string, error) {
	conditions := make([]string, len(groupedBy))
	for i, col := range groupedBy {
		if err := validateSQLIdentifier(col); err != nil {
			return "", fmt.Errorf("invalid group-by column in join: %w", err)
		}
		conditions[i] = fmt.Sprintf("%s.%s = %s.%s", table1, col, table2, col)
	}
	return strings.Join(conditions, " AND "), nil
}

func generateSelectQuery(table string, columns []string) (string, error) {
	for _, c := range columns {
		if err := validateSQLIdentifier(c); err != nil {
			return "", fmt.Errorf("invalid column in select: %w", err)
		}
	}
	updatedColumns := lo.Map(columns, func(c string, i int) string { return fmt.Sprintf("%s.%s", table, c) })
	return strings.Join(updatedColumns, ", "), nil
}

func validateAggregateExpr(expr string) error {
	matches := validAggregateExpr.FindStringSubmatch(expr)
	if matches == nil {
		return fmt.Errorf("invalid aggregate expression: %q", expr)
	}
	if !allowedAggregateFunctions[strings.ToLower(matches[1])] {
		return fmt.Errorf("disallowed aggregate function: %q", matches[1])
	}
	return nil
}

func getAggregateColumn(rule InsightRule) (string, error) {
	if rule.AggregateColumn != "" {
		// Allow both plain column names and aggregate expressions like max(column)
		if validateSQLIdentifier(rule.AggregateColumn) == nil {
			return rule.AggregateColumn, nil
		}
		if err := validateAggregateExpr(rule.AggregateColumn); err != nil {
			return "", fmt.Errorf("invalid aggregate column: %w", err)
		}
		return rule.AggregateColumn, nil
	}
	if rule.Distinct != "" {
		if err := validateSQLIdentifier(rule.Distinct); err != nil {
			return "", fmt.Errorf("invalid distinct column: %w", err)
		}
		return fmt.Sprintf("count(distinct %s) ", rule.Distinct), nil
	}
	return "count(*)", nil
}

func insightFiltersToWhereClause(filters []InsightFilters, accountIdColumnName string, accounIds []string) (string, []any, error) {
	conditions := make([]string, 0)
	args := make([]any, 0)
	conditions = append(conditions, "1 = 1")
	if accountIdColumnName != "" && len(accounIds) > 0 {
		if err := validateSQLIdentifier(accountIdColumnName); err != nil {
			return "", nil, fmt.Errorf("invalid account ID column name: %w", err)
		}
		conditions = append(conditions, fmt.Sprintf("%s in (?)", accountIdColumnName))
		args = append(args, accounIds)
	}
	for _, filter := range filters {
		if err := validateSQLIdentifier(filter.Column); err != nil {
			return "", nil, fmt.Errorf("invalid filter column: %w", err)
		}
		if !allowedFilterOperators[strings.ToLower(filter.Operator)] {
			return "", nil, fmt.Errorf("invalid filter operator: %q", filter.Operator)
		}
		switch filter.Operator {
		case "in":
			conditions = append(conditions, fmt.Sprintf("%s %s (?)", filter.Column, filter.Operator))
			args = append(args, filter.Value)
		default:
			conditions = append(conditions, fmt.Sprintf("%s %s ?", filter.Column, filter.Operator))
			args = append(args, filter.Value)
		}
	}
	return strings.Join(conditions, " AND "), args, nil
}

func newRuleExecutor(ctx *security.RequestContext, rule InsightRule) (*ruleExecutor, error) {
	tableName := ""
	tenantCol := "tenant_id"
	accountCol := "cloud_account_id"
	resourceIDCol := ""

	// Set column names based on source (always runs, even with custom ViewName)
	switch rule.Source {
	case InsightSourceRecommendation:
		tableName = "recommendation"
	case InsightSourceQuery:
		tableName = "dw_queries"
		tenantCol = "tenant_id"
		accountCol = "account_id"
		resourceIDCol = "resource_id"
	case InsightSourceEvent:
		tableName = "events"
		tenantCol = "tenant"
		accountCol = "cloud_account_id"
	case InsightSourceMetric:
		tableName = "metrics"
	case InsightSourceSecurity:
		tableName = "recommendation"
	case InsightSourceSpends:
		tableName = "spends"
		tenantCol = "tenant"
		accountCol = "cloud_account"
	case InsightSourcePrometheus:
		// Prometheus insights are handled separately
	default:
		return nil, fmt.Errorf("unsupported source: %s", rule.Source)
	}

	// ViewName overrides the table name (used for CTE-based rules)
	if rule.ViewName != "" {
		if err := validateSQLIdentifier(rule.ViewName); err != nil {
			return nil, fmt.Errorf("invalid view name: %w", err)
		}
		tableName = rule.ViewName
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, err
	}
	return &ruleExecutor{
		ctx:           ctx,
		db:            dbms,
		tableName:     tableName,
		tenantCol:     tenantCol,
		accountCol:    accountCol,
		resourceIDCol: resourceIDCol,
	}, nil
}

type ruleExecutor struct {
	ctx           *security.RequestContext
	db            *database.DatabaseManager
	tableName     string
	tenantCol     string
	accountCol    string
	resourceIDCol string
}

func (re *ruleExecutor) ExecuteRule(rule InsightRule, accountIds []string) ([]Insight, error) {
	switch rule.Type {
	case InsightTypeDiff:
		return re.processDiffRule(rule, accountIds)
	case InsightTypeAddition:
		return re.processAdditionRule(rule, accountIds)
	case InsightTypeColumnDiff:
		return re.processColumnDiffRule(rule, accountIds)
	case InsightTypeRatio:
		return re.processRatioRule(rule, accountIds)
	case InsightTypePrometheus:
		return processPrometheusRule(rule, accountIds)
	case InsightTypeEventAggregation:
		return re.processEventAggregationRule(rule, accountIds)
	case InsightTypeTraceAggregation:
		return processTraceAggregationRule(re.ctx, rule, accountIds)
	default:
		return nil, fmt.Errorf("unsupported rule type: %s", rule.Type)
	}
}

func (re *ruleExecutor) generateGroupByClause(rule InsightRule) (string, []string, error) {
	groupBy := []string{re.tenantCol, re.accountCol}
	if len(rule.GroupedBy) > 0 {
		for _, col := range rule.GroupedBy {
			if err := validateSQLIdentifier(col); err != nil {
				return "", nil, fmt.Errorf("invalid group-by column: %w", err)
			}
		}
		groupBy = rule.GroupedBy
	}
	return strings.Join(groupBy, ", "), groupBy, nil
}

func (re *ruleExecutor) processColumnDiffRule(rule InsightRule, accountId []string) ([]Insight, error) {
	return re.processAdditionRule(rule, accountId)
}

func (re *ruleExecutor) processAdditionRule(rule InsightRule, accountId []string) ([]Insight, error) {
	additionalFilters, args, err := insightFiltersToWhereClause(rule.Filters, rule.AccountColumnName, accountId)
	if err != nil {
		re.ctx.GetLogger().Error("Failed to generate where filter", "error", err)
		return nil, err
	}
	groupBy, _, err := re.generateGroupByClause(rule)
	if err != nil {
		return nil, err
	}
	aggregateColumn, err := getAggregateColumn(rule)
	if err != nil {
		return nil, err
	}
	filter := "1=1"
	if rule.Range > 0 {
		if !allowedRangeUnits[rule.RangeUnit] {
			return nil, fmt.Errorf("invalid range unit: %q", rule.RangeUnit)
		}
		filter = fmt.Sprintf(" %s AND created_at > now() - interval '%d %s'", filter, rule.Range, rule.RangeUnit)
	}

	query := fmt.Sprintf(`
		SELECT %s AS value, %s
		FROM %s
		WHERE %s AND
			%s
		GROUP BY %s
	`,
		aggregateColumn, groupBy, re.tableName, filter, additionalFilters, groupBy,
	)

	query, args, err = sqlx.In(query, args...)
	if err != nil {
		re.ctx.GetLogger().Error("Failed to prepare query with sqlx.In", "error", err)
		return nil, err
	}
	query = re.db.Db.Rebind(query)

	rows, err := re.db.Db.Queryx(query, args...)
	if err != nil {
		re.ctx.GetLogger().Error("Failed to execute query", "query", query, "error", err)
		return nil, err
	}
	defer func() {
		err := rows.Close()
		if err != nil {
			re.ctx.GetLogger().Error("Failed to close rows", "error", err)
		}
	}()

	insights := []Insight{}
	for rows.Next() {
		values := map[string]any{}
		err = rows.MapScan(values)
		if err != nil {
			re.ctx.GetLogger().Error("Failed to scan row", "error", err)
			return nil, err
		}

		// Handle different value types from database
		var value int64
		switch v := values["value"].(type) {
		case int64:
			value = v
		case float64:
			value = int64(v)
		case []uint8:
			// Convert byte array to string and parse as int
			if len(v) > 0 {
				strVal := string(v)
				parsedVal, err := fmt.Sscanf(strVal, "%d", &value)
				if err != nil || parsedVal != 1 {
					re.ctx.GetLogger().Error("Failed to parse value from bytes", "value", strVal, "error", err)
					value = 0
				}
			}
		default:
			re.ctx.GetLogger().Error("Unexpected value type in processAdditionRule", "value", v, "type", fmt.Sprintf("%T", v))
		}

		if value > int64(rule.Threshold) {
			accountIdRaw := values[re.accountCol]
			tenantIdRaw := values[re.tenantCol]
			if accountIdRaw == nil || tenantIdRaw == nil {
				continue
			}
			accountId := string(accountIdRaw.([]byte))
			tenantId := string(tenantIdRaw.([]byte))
			resourceId := ""
			if re.resourceIDCol != "" && values[re.resourceIDCol] != nil {
				resourceId = values[re.resourceIDCol].(string)
			}
			title := formatInsightTitle(rule.InsightFormat, value, values)
			insights = append(insights, Insight{
				Title:      title,
				Type:       rule.InsightCategory,
				Source:     rule.Source,
				AccountID:  accountId,
				Tenant:     tenantId,
				UniqueID:   rule.UniqueID,
				ResourceID: resourceId,
				Status:     InsightStatusOpen,
				Severity:   rule.Severity,
				Rule:       rule,
			})
		}
	}

	return insights, nil
}

func (re *ruleExecutor) processDiffRule(rule InsightRule, accountIds []string) ([]Insight, error) {
	additionalFilters, args, err := insightFiltersToWhereClause(rule.Filters, rule.AccountColumnName, accountIds)
	if err != nil {
		re.ctx.GetLogger().Error("Failed to generate where filter", "error", err)
		return nil, err
	}
	groupBy, groupByCols, err := re.generateGroupByClause(rule)
	if err != nil {
		return nil, err
	}
	aggregateColumn, err := getAggregateColumn(rule)
	if err != nil {
		return nil, err
	}
	if !allowedRangeUnits[rule.RangeUnit] {
		return nil, fmt.Errorf("invalid range unit: %q", rule.RangeUnit)
	}
	selectQuery, err := generateSelectQuery("current_count", groupByCols)
	if err != nil {
		return nil, err
	}
	joinCondition, err := buildJoinCondition("current_count", "previous_counts", groupByCols)
	if err != nil {
		return nil, err
	}

	currentCountQuery := fmt.Sprintf(`
		WITH current_count AS (
			SELECT %s AS count_this_week, %s
			FROM %s
			WHERE created_at >= current_date - interval '%d %s' AND
				%s
			GROUP BY %s
		), previous_counts AS (
			SELECT %s AS count_last_week, %s
			FROM %s
			WHERE created_at >= current_date - interval '%d %s' AND created_at < current_date - interval '%d %s' AND
				%s
			GROUP BY %s
		)
		SELECT current_count.count_this_week AS current_cnt, previous_counts.count_last_week AS last_cnt, %s
		FROM current_count
			JOIN previous_counts ON %s
	`,
		aggregateColumn, groupBy, re.tableName, rule.Range, rule.RangeUnit, additionalFilters, groupBy,
		aggregateColumn, groupBy, re.tableName, rule.Range*2, rule.RangeUnit, rule.Range, rule.RangeUnit, additionalFilters, groupBy,
		selectQuery, joinCondition,
	)

	// Since additionalFilters is used twice, we need to duplicate arguments
	allArgs := append(args, args...)

	currentCountQuery, allArgs, err = sqlx.In(currentCountQuery, allArgs...)
	if err != nil {
		re.ctx.GetLogger().Error("Failed to prepare query with sqlx.In", "error", err)
		return nil, err
	}
	currentCountQuery = re.db.Db.Rebind(currentCountQuery)

	rows, err := re.db.Db.Queryx(currentCountQuery, allArgs...)
	if err != nil {
		re.ctx.GetLogger().Error("Failed to execute query", "query", currentCountQuery, "error", err)
		return nil, err
	}
	defer func() {
		err := rows.Close()
		if err != nil {
			re.ctx.GetLogger().Error("Failed to close rows", "error", err)
		}
	}()

	insights := []Insight{}
	for rows.Next() {
		values := map[string]any{}
		err = rows.MapScan(values)
		if err != nil {
			re.ctx.GetLogger().Error("Failed to scan row", "error", err)
			return nil, err
		}
		var currentCnt, lastCnt int64
		currentCnt = values["current_cnt"].(int64)
		lastCnt = values["last_cnt"].(int64)
		diff := currentCnt - lastCnt
		if diff > int64(rule.Threshold) {
			accountIdRaw := values[re.accountCol]
			tenantIdRaw := values[re.tenantCol]
			if accountIdRaw == nil || tenantIdRaw == nil {
				continue
			}
			accountId := string(accountIdRaw.([]byte))
			tenantId := string(tenantIdRaw.([]byte))
			resourceId := ""
			if re.resourceIDCol != "" && values[re.resourceIDCol] != nil {
				resourceId = values[re.resourceIDCol].(string)
			}

			insight := Insight{
				Title:      formatInsightTitle(rule.InsightFormat, diff, values),
				Type:       rule.InsightCategory,
				Source:     rule.Source,
				AccountID:  accountId,
				Tenant:     tenantId,
				UniqueID:   rule.UniqueID,
				ResourceID: resourceId,
				Status:     InsightStatusOpen,
				Severity:   rule.Severity,
				Rule:       rule,
			}
			insights = append(insights, insight)
		}
	}

	return insights, nil
}

func (re *ruleExecutor) processRatioRule(rule InsightRule, accountId []string) ([]Insight, error) {
	additionalFilters, args, err := insightFiltersToWhereClause(rule.Filters, rule.AccountColumnName, accountId)
	if err != nil {
		re.ctx.GetLogger().Error("Failed to generate where filter", "error", err)
		return nil, err
	}
	groupBy, _, err := re.generateGroupByClause(rule)
	if err != nil {
		return nil, err
	}
	aggregateColumn, err := getAggregateColumn(rule)
	if err != nil {
		return nil, err
	}

	query := fmt.Sprintf(` SELECT %s AS value, %s FROM %s WHERE %s GROUP BY %s`,
		aggregateColumn, groupBy, re.tableName, additionalFilters, groupBy,
	)
	if rule.With != "" {
		withClause := rule.With
		// Inject account filter into CTE to avoid full table scans.
		// CTE args must be prepended before the outer WHERE args.
		if rule.AccountColumnName != "" && len(accountId) > 0 {
			accountFilter := fmt.Sprintf("%s in (?)", rule.AccountColumnName)
			placeholderCount := strings.Count(rule.With, "{{ACCOUNT_FILTER}}")
			withClause = strings.ReplaceAll(withClause, "{{ACCOUNT_FILTER}}", accountFilter)
			cteArgs := make([]any, 0, placeholderCount)
			for i := 0; i < placeholderCount; i++ {
				cteArgs = append(cteArgs, accountId)
			}
			args = append(cteArgs, args...)
		} else {
			withClause = strings.ReplaceAll(withClause, "{{ACCOUNT_FILTER}}", "1=1")
		}
		query = fmt.Sprintf(`%s %s`, withClause, query)
	}

	query, args, err = sqlx.In(query, args...)
	if err != nil {
		re.ctx.GetLogger().Error("Failed to prepare query with sqlx.In", "error", err)
		return nil, err
	}
	query = re.db.Db.Rebind(query)

	ctx, cancel := context.WithTimeout(context.Background(), insightQueryTimeout)
	defer cancel()
	rows, err := re.db.Db.QueryxContext(ctx, query, args...)
	if err != nil {
		re.ctx.GetLogger().Error("Failed to execute query", "query", query, "error", err)
		return nil, err
	}
	defer func() {
		err := rows.Close()
		if err != nil {
			re.ctx.GetLogger().Error("Failed to close rows", "error", err)
		}
	}()

	insights := []Insight{}
	for rows.Next() {
		values := map[string]any{}
		err = rows.MapScan(values)
		if err != nil {
			re.ctx.GetLogger().Error("Failed to scan row", "error", err)
			return nil, err
		}
		var value float64
		switch v := values["value"].(type) {
		case int64:
			value = float64(v)
		case float64:
			value = v
		case []uint64:
			if len(v) > 0 {
				value = float64(v[0])
			}
		case []uint8:
			// Convert byte array to string and parse as float
			if len(v) > 0 {
				strVal := string(v)
				parsedVal, err := fmt.Sscanf(strVal, "%f", &value)
				if err != nil || parsedVal != 1 {
					re.ctx.GetLogger().Error("Failed to parse value from bytes", "value", strVal, "error", err)
					value = 0
				}
			}
		default:
			re.ctx.GetLogger().Error("Unexpected value type", "value", v, "type", fmt.Sprintf("%T", v))
		}
		if value > float64(rule.Threshold) {
			accountIdRaw := values[re.accountCol]
			tenantIdRaw := values[re.tenantCol]
			if accountIdRaw == nil || tenantIdRaw == nil {
				continue
			}
			accountId := string(accountIdRaw.([]byte))
			tenantId := string(tenantIdRaw.([]byte))
			resourceId := ""
			if re.resourceIDCol != "" && values[re.resourceIDCol] != nil {
				resourceId = values[re.resourceIDCol].(string)
			}
			title := formatInsightTitle(rule.InsightFormat, int64(value), values)
			insights = append(insights, Insight{
				Title:      title,
				Type:       rule.InsightCategory,
				Source:     rule.Source,
				AccountID:  accountId,
				Tenant:     tenantId,
				UniqueID:   rule.UniqueID,
				ResourceID: resourceId,
				Status:     InsightStatusOpen,
				Severity:   rule.Severity,
				Rule:       rule,
			})
		}
	}
	return insights, nil
}

func (re *ruleExecutor) processEventAggregationRule(rule InsightRule, accountIds []string) ([]Insight, error) {
	additionalFilters, args, err := insightFiltersToWhereClause(rule.Filters, re.accountCol, accountIds)
	if err != nil {
		re.ctx.GetLogger().Error("Failed to generate where filter", "error", err)
		return nil, err
	}

	rangeFilter := "1=1"
	if rule.Range > 0 {
		if !allowedRangeUnits[rule.RangeUnit] {
			return nil, fmt.Errorf("invalid range unit: %q", rule.RangeUnit)
		}
		rangeFilter = fmt.Sprintf("created_at > now() - interval '%d %s'", rule.Range, rule.RangeUnit)
	}

	query := fmt.Sprintf(`
		SELECT
			sub.%s AS tenant,
			sub.%s AS cloud_account_id,
			count(*) AS event_count,
			json_agg(
				jsonb_build_object(
					'name', sub.subject_name,
					'namespace', COALESCE(sub.subject_namespace, '')
				)
			) AS applications
		FROM (
			SELECT DISTINCT %s, %s, subject_name, subject_namespace
			FROM %s
			WHERE %s
			  AND %s
			  AND subject_name IS NOT NULL
			  AND subject_name != ''
		) sub
		GROUP BY sub.%s, sub.%s
	`,
		re.tenantCol, re.accountCol,
		re.tenantCol, re.accountCol,
		re.tableName,
		rangeFilter, additionalFilters,
		re.tenantCol, re.accountCol,
	)

	query, args, err = sqlx.In(query, args...)
	if err != nil {
		re.ctx.GetLogger().Error("Failed to prepare query with sqlx.In", "error", err)
		return nil, err
	}
	query = re.db.Db.Rebind(query)

	rows, err := re.db.Db.Queryx(query, args...)
	if err != nil {
		re.ctx.GetLogger().Error("Failed to execute event aggregation query", "query", query, "error", err)
		return nil, err
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil {
			re.ctx.GetLogger().Error("Failed to close rows", "error", cerr)
		}
	}()

	var insights []Insight
	for rows.Next() {
		values := map[string]any{}
		if err := rows.MapScan(values); err != nil {
			re.ctx.GetLogger().Error("Failed to scan row", "error", err)
			return nil, err
		}

		var eventCount int64
		switch v := values["event_count"].(type) {
		case int64:
			eventCount = v
		case float64:
			eventCount = int64(v)
		case []uint8:
			_, _ = fmt.Sscan(string(v), &eventCount)
		}

		if eventCount <= int64(rule.Threshold) {
			continue
		}

		accountIdRaw, ok := values[re.accountCol]
		if !ok || accountIdRaw == nil {
			continue
		}
		accountId := string(accountIdRaw.([]byte))

		tenantIdRaw, ok := values[re.tenantCol]
		if !ok || tenantIdRaw == nil {
			continue
		}
		tenantId := string(tenantIdRaw.([]byte))

		var applications []RelevantApplications
		if rawApps := values["applications"]; rawApps != nil {
			var appsJSON []byte
			switch v := rawApps.(type) {
			case []byte:
				appsJSON = v
			case string:
				appsJSON = []byte(v)
			}
			if appsJSON != nil {
				if err := common.UnmarshalJson(appsJSON, &applications); err != nil {
					re.ctx.GetLogger().Error("Failed to unmarshal applications", "error", err)
					applications = []RelevantApplications{}
				}
			}
		}

		insights = append(insights, Insight{
			Title:        rule.InsightFormat,
			Type:         rule.InsightCategory,
			Source:       rule.Source,
			AccountID:    accountId,
			Tenant:       tenantId,
			UniqueID:     rule.UniqueID,
			Status:       InsightStatusOpen,
			Severity:     rule.Severity,
			Rule:         rule,
			Applications: applications,
		})
	}

	return insights, nil
}

func processTraceAggregationRule(ctx *security.RequestContext, rule InsightRule, accountIds []string) ([]Insight, error) {
	var wg sync.WaitGroup
	insightCh := make(chan Insight, len(accountIds))

	wg.Add(len(accountIds))

	for _, accountId := range accountIds {
		go func(accountId string) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					ctx.GetLogger().Error("Failed to process trace aggregation rule", "rule", rule.UniqueID, "accountId", accountId, "panic", r)
				}
			}()

			insight, err := processTraceAggregationRuleForAccount(ctx, rule, accountId)
			if err != nil {
				ctx.GetLogger().Error("Failed to process trace aggregation rule for account", "error", err, "accountId", accountId)
				return
			}
			if insight.Title != "" {
				insightCh <- insight
			}
		}(accountId)
	}

	wg.Wait()
	close(insightCh)

	var insights []Insight
	for insight := range insightCh {
		insights = append(insights, insight)
	}

	return insights, nil
}

func processTraceAggregationRuleForAccount(ctx *security.RequestContext, rule InsightRule, accountId string) (Insight, error) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("Failed to process trace aggregation rule", "rule", rule.UniqueID, "recover", r, "accountId", accountId)
		}
	}()

	now := time.Now().UTC()
	rangeDays := rule.Range
	if rangeDays <= 0 {
		rangeDays = 1
	}
	startTime := now.AddDate(0, 0, -rangeDays)

	req := query.QueryRequest{
		Table: "traces_groupings_v2",
		Columns: []query.QueryColumn{
			{Name: "workload_name"},
			{Name: "workload_namespace"},
			{Name: "p95_latency"},
			{Name: "error_count"},
			{Name: "count"},
		},
		Where: query.QueryWhereClause{
			Binary: query.BinaryWhereClause{
				"account_id": {query.Eq: accountId},
			},
			And: []query.QueryWhereClause{
				{Binary: query.BinaryWhereClause{
					"timestamp": {
						query.Gte: startTime.Format(time.RFC3339),
						query.Lte: now.Format(time.RFC3339),
					},
				}},
			},
		},
		GroupBy: []string{"workload_name", "workload_namespace"},
		Limit:   1000,
	}

	resp, err := query.ExecuteQuery(ctx, req)
	if err != nil {
		slog.Error("Failed to execute trace aggregation query", "error", err, "accountId", accountId)
		return Insight{}, err
	}
	if len(resp.Errors) > 0 {
		slog.Error("Trace aggregation query returned errors", "errors", resp.Errors, "accountId", accountId)
		return Insight{}, fmt.Errorf("trace query errors: %v", resp.Errors)
	}

	var relevantApplications []RelevantApplications
	for _, row := range resp.Rows {
		name := toStringValue(row["workload_name"])
		namespace := toStringValue(row["workload_namespace"])
		if name == "" {
			continue
		}

		matched := false
		switch rule.AggregateColumn {
		case "p95_latency":
			p95 := toFloat64Value(row["p95_latency"])
			matched = p95 > rule.Threshold
		case "error_rate":
			errorCount := toFloat64Value(row["error_count"])
			count := toFloat64Value(row["count"])
			if count > 0 {
				matched = (errorCount / count) > rule.Threshold
			}
		}

		if matched {
			relevantApplications = append(relevantApplications, RelevantApplications{
				Name:      name,
				Namespace: namespace,
			})
		}
	}

	if len(relevantApplications) == 0 {
		return Insight{}, nil
	}

	return Insight{
		Title:        rule.InsightFormat,
		Type:         rule.InsightCategory,
		Source:       rule.Source,
		AccountID:    accountId,
		Tenant:       "",
		UniqueID:     rule.UniqueID,
		Status:       InsightStatusOpen,
		Severity:     rule.Severity,
		Rule:         rule,
		Applications: relevantApplications,
	}, nil
}

func toStringValue(v any) string {
	if v == nil {
		return ""
	}
	switch val := v.(type) {
	case string:
		return val
	case []byte:
		return string(val)
	default:
		return fmt.Sprintf("%v", val)
	}
}

func toFloat64Value(v any) float64 {
	if v == nil {
		return 0
	}
	switch val := v.(type) {
	case float64:
		return val
	case float32:
		return float64(val)
	case int64:
		return float64(val)
	case int:
		return float64(val)
	case string:
		var f float64
		_, _ = fmt.Sscan(val, &f)
		return f
	case []byte:
		var f float64
		_, _ = fmt.Sscan(string(val), &f)
		return f
	default:
		return 0
	}
}

func processPrometheusRule(rule InsightRule, accountIds []string) ([]Insight, error) {
	var wg sync.WaitGroup
	insightCh := make(chan Insight, len(accountIds))
	errCh := make(chan error, len(accountIds))

	wg.Add(len(accountIds))

	for _, accountId := range accountIds {
		go func(accountId string) {
			defer wg.Done()

			insight, err := processPrometheusRuleForAccount(rule, accountId)
			if err != nil {
				slog.Error("Failed to process prometheus rule for account", "error", err, "accountId", accountId)
				return
			}

			insightCh <- insight
		}(accountId)
	}

	wg.Wait()
	close(insightCh)
	close(errCh)

	var insights []Insight
	for insight := range insightCh {
		insights = append(insights, insight)
	}

	// Handle errors if needed
	if len(errCh) > 0 {
		return insights, <-errCh
	}

	return insights, nil
}

func processPrometheusRuleForAccount(rule InsightRule, accountId string) (Insight, error) {
	if rule.Instant {
		return ProcessPrometheusInstantRule(rule, accountId)
	}
	defer func() {
		if r := recover(); r != nil {
			slog.Error("Failed to process rule", "rule", rule.UniqueID, "recover", r, "accountId", accountId)
		}
	}()
	startAt := time.Now().AddDate(0, 0, rule.Range)
	endsAt := time.Now()

	rStartTime := startAt.Format("2006-01-02T15:04:05.000000Z")
	rEndTime := endsAt.Format("2006-01-02T15:04:05.000000Z")
	request :=
		relay.ActionExecuteBody{
			AccountID:  accountId,
			ActionName: "application_stats",
			ActionParams: map[string]any{
				"r_start_time": rStartTime,
				"r_end_time":   rEndTime,
				"queries": map[string]any{
					"query": rule.Query,
				},
				"applications": "{}",
			},
		}

	resp, err := relay.Execute(relay.RelayExecuteRequest{
		NoSinks: true,
		Cache:   false,
		Body:    request,
	})

	if err != nil {
		slog.Error("Failed to execute relay task", "error", err, "accountId", accountId)
		return Insight{}, err
	}
	if resp["status_code"] == 500 {
		slog.Error("Failed to execute relay task", "error", resp["response"], "accountId", accountId)
		return Insight{}, fmt.Errorf("insight: failed to execute relay task error %s accountId %s ", resp["response"], accountId)
	}

	reports := make([]relay.ApplicationStatsResponse, 0)
	sloReports := resp["data"].(map[string]any)["data"]
	for _, m := range sloReports.([]any) {
		jsonData, err := common.MarshalJson(m)
		if err != nil {
			continue
		}

		var sloReport relay.ApplicationStatsResponse
		if err := common.UnmarshalJson(jsonData, &sloReport); err != nil {
			continue
		}
		reports = append(reports, sloReport)
	}
	relevantApplications := make([]RelevantApplications, 0)

	for _, report := range reports {

		if val, ok := report.OtherMetrics["query"]; ok {
			if val > float64(rule.Threshold) {
				relevantApplications = append(relevantApplications, RelevantApplications{
					Name:      report.Name,
					Namespace: report.Namespace,
				})
			}
		}
	}

	if len(relevantApplications) == 0 {
		return Insight{}, nil
	}
	insight := Insight{
		Title:        rule.InsightFormat,
		Type:         rule.InsightCategory,
		Source:       rule.Source,
		AccountID:    accountId,
		Tenant:       "",
		UniqueID:     rule.UniqueID,
		Status:       InsightStatusOpen,
		Severity:     rule.Severity,
		Rule:         rule,
		Applications: relevantApplications,
	}
	return insight, nil
}

func ProcessPrometheusInstantRule(rule InsightRule, accountId string) (Insight, error) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("Failed to process rule", "rule", rule.UniqueID, "recover", r, "accountId", accountId)
		}
	}()

	relayRequest := relay.RelayExecuteRequest{
		Body: relay.ActionExecuteBody{
			AccountID:  accountId,
			ActionName: "prometheus_enricher",
			ActionParams: map[string]any{
				"promql_query": rule.Query,
				"instant":      true,
			},
		},
	}
	resp, err := relay.Execute(relayRequest)
	if err != nil {
		slog.Error("Failed to execute relay task", "error", err, "accountId", accountId)
		return Insight{}, err
	}
	if resp["status_code"] == 500 {
		slog.Error("Failed to execute relay task", "error", resp["response"], "accountId", accountId)
		return Insight{}, fmt.Errorf("insight: failed to execute relay task error %s accountId %s ", resp["response"], accountId)
	}

	evidence, err := relay.FormatEvidenceResponseFromAgent("Prometheus Metric", resp)
	if err != nil {
		slog.Error("anomaly: error formatting evidence response in anomaly at cpu", "error", err)
	}

	evidenceData, ok := evidence["data"].(map[string]any)
	if !ok || len(evidenceData) == 0 {
		slog.Warn("No data found in evidence for Prometheus rule", "rule", rule.UniqueID, "accountId", accountId)
		return Insight{}, nil
	}
	// check for "vector_result" in evidenceData
	vectorResult, ok := evidenceData["vector_result"].([]any)
	if !ok || len(vectorResult) == 0 {
		slog.Warn("No vector_result found in evidence for Prometheus rule", "rule",
			rule.UniqueID, "accountId", accountId)
		return Insight{}, nil
	}
	relevantApplications := make([]RelevantApplications, 0)
	for _, item := range vectorResult {
		itemMap, ok := item.(map[string]any)
		if !ok {
			slog.Warn("Invalid item in vector_result", "item", item)
			continue
		}
		metrics, ok := itemMap["metric"].(map[string]any)
		if !ok {
			slog.Warn("Invalid metric in vector_result item", "item", item)
			continue
		}

		name, namespace := "", ""
		if len(metrics) >= 2 && len(rule.GroupedBy) >= 2 {
			name = metrics[rule.GroupedBy[0]].(string)
			namespace = metrics[rule.GroupedBy[1]].(string)
		} else if len(metrics) == 1 && len(rule.GroupedBy) == 1 {
			name = metrics[rule.GroupedBy[0]].(string)
		} else {
			slog.Warn("Didnt found required data")
			// Fallback to default labels
			if nameVal, ok := metrics["name"]; ok {
				name = nameVal.(string)
			}
			if namespaceVal, ok := metrics["namespace"]; ok {
				namespace = namespaceVal.(string)
			}
		}
		application := RelevantApplications{
			Name:      name,
			Namespace: namespace,
		}
		relevantApplications = append(relevantApplications, application)
	}

	insight := Insight{
		Title:        rule.InsightFormat,
		Type:         rule.InsightCategory,
		Source:       rule.Source,
		AccountID:    accountId,
		Tenant:       "",
		UniqueID:     rule.UniqueID,
		Status:       InsightStatusOpen,
		Severity:     rule.Severity,
		Rule:         rule,
		Applications: relevantApplications,
	}

	return insight, nil
}
