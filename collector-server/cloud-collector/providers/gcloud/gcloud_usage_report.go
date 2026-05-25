package gcloud

import (
	"context"
	"fmt"
	"maps"
	"nudgebee/collector/cloud/common"
	"nudgebee/collector/cloud/providers"
	"nudgebee/collector/cloud/providers/gcloud/models"
	"strings"
	"time"

	"cloud.google.com/go/bigquery"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
)

const incompleteDataThreshold = 0.8

// bigQueryClient defines the interface for BigQuery operations, allowing for mocking.
type bigQueryClient interface {
	Query(q string) *bigquery.Query
	Close() error
}

// newBigQueryClient is a factory function for creating a BigQuery client.
// It's a variable so we can replace it with a mock in tests.
var newBigQueryClient = func(ctx context.Context, projectID string, opts ...option.ClientOption) (bigQueryClient, error) {
	return bigquery.NewClient(ctx, projectID, opts...)
}

func getBillingConfigFromAccount(account providers.Account) (models.BillingConfig, error) {
	config := models.BillingConfig{}

	if account.Data == nil || *account.Data == "" {
		return config, fmt.Errorf("account data is required for GCP billing configuration")
	}

	// Parse the JSON into a generic map
	accountData := map[string]any{}
	err := common.UnmarshalJson([]byte(*account.Data), &accountData)
	if err != nil {
		return config, fmt.Errorf("failed to parse account data: %w", err)
	}

	// Get billing_data sub-object
	billingDataRaw, ok := accountData["billing_data"]
	if !ok {
		return config, fmt.Errorf("missing 'billing_data' field in account data")
	}

	billingData, ok := billingDataRaw.(map[string]any)
	if !ok {
		return config, fmt.Errorf("'billing_data' field is not a valid JSON object")
	}

	// Prefer billing_project_id from config, fall back to account number
	if projectID, ok := billingData["billing_project_id"].(string); ok && projectID != "" {
		config.ProjectID = projectID
	} else {
		config.ProjectID = account.AccountNumber
	}

	// Extract dataset_name → DatasetID
	if datasetName, ok := billingData["dataset_name"].(string); ok {
		config.DatasetID = datasetName
	}

	if tableName, ok := billingData["table_name"].(string); ok {
		config.TableID = tableName
	}

	// Validate
	if config.ProjectID == "" || config.DatasetID == "" || config.TableID == "" {
		return config, fmt.Errorf("billing_data must include 'dataset_name', 'table_name', and 'billing_project_id' (or account number must be set)")
	}

	return config, nil
}

// bigQueryQueryStats captures aggregate metrics gathered while streaming rows.
// Tracking these inline avoids a second pass over the row set.
type bigQueryQueryStats struct {
	RowCount          int
	ServiceMap        map[string]float64
	CurrencyMap       map[string]int
	CreditCount       int
	TotalCreditAmount float64
	FirstDate         time.Time
	LastDate          time.Time
	Variant           string
}

// queryBigQueryAndStream builds and executes the BigQuery query, streaming each
// row to the supplied processor. The full result set is never held in memory.
// accountProjectID is the GCP project ID (account_number) to filter billing data for.
// isParent indicates whether this account is a parent (no parent_account_id) — parent accounts
// also receive rows with NULL project_id (rounding errors, invoice-level adjustments).
func queryBigQueryAndStream(ctx providers.CloudProviderContext, client bigQueryClient, config models.BillingConfig, startDate, endDate time.Time, accountProjectID string, isParent bool, process func(row models.BigQueryBillingRow) error) (bigQueryQueryStats, error) {
	logger := ctx.GetLogger()

	if accountProjectID == "" {
		return bigQueryQueryStats{}, fmt.Errorf("accountProjectID is empty — cannot query billing data without a project filter")
	}

	// Build project filter for per-account billing isolation.
	// Parent accounts get their project's data + NULL project rows (rounding errors, invoice adjustments).
	// Child accounts get only their project's data.
	nestedProjectFilter := "\n        AND project.id = @account_project_id"
	flatProjectFilter := "\n        AND project_id = @account_project_id"
	if isParent {
		nestedProjectFilter = "\n        AND (project.id = @account_project_id OR project.id IS NULL)"
		flatProjectFilter = "\n        AND (project_id = @account_project_id OR project_id IS NULL)"
	}

	// Try nested schema first
	nestedSQL := fmt.Sprintf(`
    SELECT
        service.description AS service_name,
        sku.description AS sku_description,
        usage_start_time,
        usage_end_time,
        project.id AS project_id,
        location.location AS region,
        resource.name AS resource_name,
        cost,
        (SELECT SUM(c.amount) FROM UNNEST(credits) c) AS credit_amount,
        currency,
        cost_type,
        labels,
        system_labels,
        usage.amount AS usage_amount,
        usage.unit AS usage_unit
    FROM `+"`%s.%s.%s`"+`
    WHERE DATE(usage_start_time) >= @start_date
        AND DATE(usage_start_time) < @end_date
        AND (cost != 0 OR ARRAY_LENGTH(credits) > 0)%s
    ORDER BY usage_start_time
    `, config.ProjectID, config.DatasetID, config.TableID, nestedProjectFilter)

	flatSQL := fmt.Sprintf(`
    SELECT
        service_description AS service_name,
        sku_description AS sku_description,
        usage_start_time,
        usage_end_time,
        project_id AS project_id,
        region AS region,
        resource_name AS resource_name,
        cost,
        (SELECT SUM(c.amount) FROM UNNEST(credits) c) AS credit_amount,
        currency,
        cost_type,
        labels,
        system_labels,
        usage_amount AS usage_amount,
        usage_unit AS usage_unit
    FROM `+"`%s.%s.%s`"+`
    WHERE DATE(usage_start_time) >= @start_date
        AND DATE(usage_start_time) < @end_date
        AND (cost != 0 OR ARRAY_LENGTH(credits) > 0)%s
    ORDER BY usage_start_time
    `, config.ProjectID, config.DatasetID, config.TableID, flatProjectFilter)

	// Mixed variant: service present without service_description; sku_description present without sku
	mixedSQL := fmt.Sprintf(`
    SELECT
        service AS service_name,
        sku_description AS sku_description,
        usage_start_time,
        usage_end_time,
        project_id AS project_id,
        COALESCE(region, location) AS region,
        COALESCE(resource_name, resource) AS resource_name,
        cost,
        (SELECT SUM(c.amount) FROM UNNEST(credits) c) AS credit_amount,
        currency,
        cost_type,
        labels,
        system_labels,
        usage_amount AS usage_amount,
        usage_unit AS usage_unit
    FROM `+"`%s.%s.%s`"+`
    WHERE DATE(usage_start_time) >= @start_date
        AND DATE(usage_start_time) < @end_date
        AND (cost != 0 OR ARRAY_LENGTH(credits) > 0)%s
    ORDER BY usage_start_time
    `, config.ProjectID, config.DatasetID, config.TableID, flatProjectFilter)

	// Some exports may use simpler flat names like `service`, `sku`, `location`, `resource`
	flatSimpleSQL := fmt.Sprintf(`
    SELECT
        service AS service_name,
        sku AS sku_description,
        usage_start_time,
        usage_end_time,
        project_id AS project_id,
        COALESCE(region, location) AS region,
        COALESCE(resource_name, resource) AS resource_name,
        cost,
        (SELECT SUM(c.amount) FROM UNNEST(credits) c) AS credit_amount,
        currency,
        cost_type,
        labels,
        system_labels,
        usage_amount AS usage_amount,
        usage_unit AS usage_unit
    FROM `+"`%s.%s.%s`"+`
    WHERE DATE(usage_start_time) >= @start_date
        AND DATE(usage_start_time) < @end_date
        AND (cost != 0 OR ARRAY_LENGTH(credits) > 0)%s
    ORDER BY usage_start_time
    `, config.ProjectID, config.DatasetID, config.TableID, flatProjectFilter)

	// Minimal variant: only select columns guaranteed by provided schema
	minimalSQL := fmt.Sprintf(`
    SELECT
        service AS service_name,
        sku_description AS sku_description,
        usage_start_time,
        usage_end_time,
        project_id AS project_id,
        region AS region,
        cost,
        (SELECT SUM(c.amount) FROM UNNEST(credits) c) AS credit_amount,
        currency,
        cost_type
    FROM `+"`%s.%s.%s`"+`
    WHERE DATE(usage_start_time) >= @start_date
        AND DATE(usage_start_time) < @end_date
        AND (cost != 0 OR ARRAY_LENGTH(credits) > 0)%s
    ORDER BY usage_start_time
    `, config.ProjectID, config.DatasetID, config.TableID, flatProjectFilter)

	queries := []struct {
		name string
		sql  string
	}{
		{"nested", nestedSQL},
		{"flat", flatSQL},
		{"mixed", mixedSQL},
		{"flatSimple", flatSimpleSQL},
		{"minimal", minimalSQL},
	}

	runQuery := func(sql string) (*bigquery.RowIterator, error) {
		q := client.Query(sql)
		q.Parameters = []bigquery.QueryParameter{
			{Name: "start_date", Value: startDate.Format("2006-01-02")},
			{Name: "end_date", Value: endDate.Format("2006-01-02")},
			{Name: "account_project_id", Value: accountProjectID},
		}
		return q.Read(ctx.GetContext())
	}

	var it *bigquery.RowIterator
	var err error
	var successfulQuery string

	for _, query := range queries {
		logger.Info("trying BigQuery schema variant", "variant", query.name)
		it, err = runQuery(query.sql)
		if err == nil {
			// Query succeeded, break the loop
			successfulQuery = query.name
			logger.Info("BigQuery query succeeded", "variant", query.name)
			break
		}

		// If the error is not a schema-related error, fail fast.
		errStr := err.Error()
		logger.Warn("BigQuery query failed", "variant", query.name, "error", errStr)
		if !strings.Contains(errStr, "Unrecognized name") && !strings.Contains(errStr, "Cannot access field") {
			break
		}
	}

	stats := bigQueryQueryStats{
		ServiceMap:  make(map[string]float64),
		CurrencyMap: make(map[string]int),
		Variant:     successfulQuery,
	}
	if err != nil {
		return stats, fmt.Errorf("failed to execute any of the BigQuery query variants: %w", err)
	}

	for {
		var row models.BigQueryBillingRow
		err := it.Next(&row)
		if err == iterator.Done {
			break
		}
		if err != nil {
			return stats, fmt.Errorf("failed to read BigQuery row: %w", err)
		}
		stats.RowCount++
		stats.ServiceMap[row.ServiceName] += row.Cost
		stats.CurrencyMap[row.Currency]++
		if row.CreditAmount.Valid {
			stats.CreditCount++
			stats.TotalCreditAmount += row.CreditAmount.Float64
		}
		// Rows arrive ORDER BY usage_start_time so first/last row dates are min/max.
		if stats.RowCount == 1 {
			stats.FirstDate = row.UsageStartTime
		}
		stats.LastDate = row.UsageStartTime

		if err := process(row); err != nil {
			return stats, fmt.Errorf("failed to process BigQuery row: %w", err)
		}
	}
	logger.Info("credit amount stats", "rowsWithCredits", stats.CreditCount, "totalCreditAmount", stats.TotalCreditAmount)

	logger.Info("BigQuery query completed",
		"variant", stats.Variant,
		"totalRows", stats.RowCount,
		"uniqueServices", len(stats.ServiceMap),
		"currencies", stats.CurrencyMap,
		"serviceBreakdown", stats.ServiceMap)

	// Warn if very few rows returned (likely incomplete billing export)
	if stats.RowCount < 50 && stats.RowCount > 0 {
		logger.Warn("Very few billing records found - billing export may be incomplete or recently enabled",
			"totalRows", stats.RowCount,
			"dateRange", fmt.Sprintf("%s to %s", startDate.Format("2006-01-02"), endDate.Format("2006-01-02")),
			"note", "GCP billing export takes 24-48 hours to populate after enabling. If you just enabled it, check again tomorrow.")
	} else if stats.RowCount == 0 {
		logger.Warn("No billing records found for the specified date range",
			"startDate", startDate.Format("2006-01-02"),
			"endDate", endDate.Format("2006-01-02"),
			"note", "Verify billing export is enabled and has had time to populate (24-48 hours)")
	}

	return stats, nil
}

// streamBigQueryBilling executes a BigQuery query for billing data and invokes
// `process` for each row. The full result set is never materialised in memory —
// callers should aggregate inline if they need a summary.
//
// Declared as a `var` so tests can substitute a mock that calls `process` with
// canned rows.
var streamBigQueryBilling = func(ctx providers.CloudProviderContext, config models.BillingConfig, startDate, endDate time.Time, account providers.Account, process func(row models.BigQueryBillingRow) error) (bigQueryQueryStats, error) {
	// Get GCloud session with credentials (from AccessSecret or GOOGLE_APPLICATION_CREDENTIALS)
	session, err := getGcloudSessionFromAccount(ctx, account)
	if err != nil {
		return bigQueryQueryStats{}, fmt.Errorf("failed to get gcloud session: %w", err)
	}

	// Create BigQuery client using session credentials
	client, err := newBigQueryClient(ctx.GetContext(), config.ProjectID, session.Opts...)
	if err != nil {
		return bigQueryQueryStats{}, fmt.Errorf("failed to create BigQuery client: %w", err)
	}
	defer func() {
		if cerr := client.Close(); cerr != nil {
			ctx.GetLogger().Error("failed to close BigQuery client", "error", cerr)
		}
	}()

	isParent := account.ParentAccountId == nil
	return queryBigQueryAndStream(ctx, client, config, startDate, endDate, account.AccountNumber, isParent, process)
}

// convertToGcpUsageReportItem converts a BigQuery billing row to one or more UsageReportItems.
// If the row has credits, a separate credit item with negative cost is emitted.
func convertToGcpUsageReportItem(row models.BigQueryBillingRow) ([]providers.UsageReportItem, error) {
	item := providers.UsageReportItem{}

	// Map basic fields
	item.ProductCode = row.ServiceName
	item.ProductServiceCode = row.SKUDescription

	// Default region to "global" if empty or not provided by schema
	if row.Region.Valid {
		item.ResourceRegionCode = row.Region.StringVal
	}
	if item.ResourceRegionCode == "" {
		item.ResourceRegionCode = "global"
	}

	// Handle resource name (can be NULL in BigQuery)
	if row.ResourceName.Valid {
		item.ResourceId = row.ResourceName.StringVal
	}

	item.StartDate = row.UsageStartTime
	item.EndDate = row.UsageEndTime
	// Store gross cost; credits are emitted as separate items
	item.Cost = row.Cost
	// Default currency to "USD" if empty or not provided by schema
	item.CostCurrency = row.Currency
	if item.CostCurrency == "" {
		item.CostCurrency = "USD"
	}

	// Handle resource identification
	projectID := ""
	if row.ProjectID.Valid {
		projectID = row.ProjectID.StringVal
	}
	if item.ResourceId == "" {
		item.ResourceId = projectID
	} else if projectID != "" {
		// Combine project and resource for unique identification
		item.ResourceId = fmt.Sprintf("%s/%s", projectID, item.ResourceId)
	}

	// Map cost type to category
	switch strings.ToUpper(row.CostType) {
	case "REGULAR":
		item.CostCategory = providers.UsageReportItemTypeUsage
	case "TAX":
		item.CostCategory = providers.UsageReportItemTypeTax
	case "ADJUSTMENT", "ROUNDING_ERROR":
		item.CostCategory = providers.UsageReportItemTypeUnknown
	default:
		item.CostCategory = providers.UsageReportItemTypeUsage
	}

	// Determine resource type from service name
	item.ResourceType = strings.ToLower(strings.ReplaceAll(row.ServiceName, " ", "-"))

	// Set cost subcategory
	item.CostSubCategory = row.SKUDescription
	if item.CostSubCategory == "" {
		item.CostSubCategory = item.ResourceType
	}

	// Extract labels/tags
	tags := make(map[string][]string)

	// Process regular labels (array of LabelEntry)
	for _, label := range row.Labels {
		if label.Key.Valid && label.Value.Valid && label.Key.StringVal != "" && label.Value.StringVal != "" {
			tags[label.Key.StringVal] = []string{label.Value.StringVal}
		}
	}

	// Process system labels (array of LabelEntry)
	for _, label := range row.SystemLabels {
		if label.Key.Valid && label.Value.Valid && label.Key.StringVal != "" && label.Value.StringVal != "" {
			systemKey := fmt.Sprintf("system:%s", label.Key.StringVal)
			tags[systemKey] = []string{label.Value.StringVal}
		}
	}

	item.ResourceTags = tags

	result := []providers.UsageReportItem{item}

	// Emit a separate credit item if credits exist.
	// ResourceId is cleared so cloud_resource_id becomes NULL in DB,
	// matching how AWS credit line items are stored (avoids unique constraint violation).
	if row.CreditAmount.Valid && row.CreditAmount.Float64 != 0 {
		creditItem := item
		creditItem.Cost = row.CreditAmount.Float64 // negative amount
		creditItem.CostCategory = providers.UsageReportCostCategory("Credit")
		creditItem.CostSubCategory = "Credits"
		// Preserve original resource ID for traceability before clearing it
		creditTags := maps.Clone(item.ResourceTags)
		if creditTags == nil {
			creditTags = make(map[string][]string)
		}
		if item.ResourceId != "" {
			creditTags["nb_credit_source_resource"] = []string{item.ResourceId}
		}
		creditItem.ResourceTags = creditTags
		creditItem.ResourceId = ""
		result = append(result, creditItem)
	}

	return result, nil
}

// dailyAggKey is the composite key used to bucket usage items per day per
// (service, category, region, resource-type, resource-id). Using a struct as
// the map key avoids the per-row string allocation that fmt.Sprintf would
// produce on the streaming hot path — material when processing millions of rows.
// All fields are comparable so the struct works directly as a map key.
type dailyAggKey struct {
	ProductCode  string
	CostCategory providers.UsageReportCostCategory
	Region       string
	ResourceType string
	ResourceId   string
	DayUnix      int64 // unix seconds of the day's UTC midnight
}

// mergeIntoDailyAggregate merges one item into a running daily-aggregation map
// and updates per-service totals. Used by the streaming code path so the raw row
// set is never held in memory; also used by aggregateDailyBilling for batch tests.
func mergeIntoDailyAggregate(item providers.UsageReportItem, agg map[dailyAggKey]providers.UsageReportItem, serviceTotals map[string]float64) {
	serviceTotals[item.ProductCode] += item.Cost

	dayStartDate := item.StartDate.UTC().Truncate(24 * time.Hour)
	dayEndDate := dayStartDate.Add(24*time.Hour - time.Nanosecond) // End of day: 23:59:59.999...

	regionCode := item.ResourceRegionCode
	if regionCode == "" {
		regionCode = "global"
	}
	key := dailyAggKey{
		ProductCode:  item.ProductCode,
		CostCategory: item.CostCategory,
		Region:       regionCode,
		ResourceType: item.ResourceType,
		ResourceId:   item.ResourceId,
		DayUnix:      dayStartDate.Unix(),
	}

	if existing, ok := agg[key]; ok {
		existing.Cost += item.Cost
		agg[key] = existing
		return
	}
	item.StartDate = dayStartDate
	item.EndDate = dayEndDate
	agg[key] = item
}

// aggregateDailyBilling aggregates billing data by day, similar to AWS implementation.
// Retained as a batch helper for unit tests; production code uses
// mergeIntoDailyAggregate inside the streaming loop to avoid holding the raw rows.
func aggregateDailyBilling(items []providers.UsageReportItem) ([]providers.UsageReportItem, map[string]float64) {
	if len(items) == 0 {
		return items, make(map[string]float64)
	}

	aggregatedItemsMap := make(map[dailyAggKey]providers.UsageReportItem)
	serviceTotals := make(map[string]float64)

	for _, item := range items {
		mergeIntoDailyAggregate(item, aggregatedItemsMap, serviceTotals)
	}

	aggregatedItems := make([]providers.UsageReportItem, 0, len(aggregatedItemsMap))
	for _, item := range aggregatedItemsMap {
		aggregatedItems = append(aggregatedItems, item)
	}

	return aggregatedItems, serviceTotals
}

// getGcloudUsageReport is the main function that orchestrates GCP billing data retrieval
func getGcloudUsageReport(ctx providers.CloudProviderContext, account providers.Account, month time.Month, year int) (providers.GetUsageReportResponse, error) {
	logger := ctx.GetLogger()

	// Extract billing configuration
	config, err := getBillingConfigFromAccount(account)
	if err != nil {
		logger.Error("failed to get billing config", "error", err, "accountNumber", account.AccountNumber)
		return providers.GetUsageReportResponse{}, err
	}

	// Calculate date range for the specified month/year
	startDate := time.Date(year, month, 1, 0, 0, 0, 0, time.UTC)
	var endDate time.Time
	if month == 12 {
		endDate = time.Date(year+1, 1, 1, 0, 0, 0, 0, time.UTC)
	} else {
		endDate = time.Date(year, month+1, 1, 0, 0, 0, 0, time.UTC)
	}

	logger.Info("fetching GCP billing data",
		"accountNumber", account.AccountNumber,
		"projectID", config.ProjectID,
		"datasetID", config.DatasetID,
		"tableID", config.TableID,
		"startDate", startDate.Format("2006-01-02"),
		"endDate", endDate.Format("2006-01-02"))

	// Stream BigQuery rows: convert each row, merge into the daily aggregation map.
	// The full row set is never held in memory — only the daily aggregate (orders of
	// magnitude smaller, ~uniqueServices × resources × days entries).
	aggregatedItemsMap := make(map[dailyAggKey]providers.UsageReportItem)
	serviceTotals := make(map[string]float64)
	convertedItemCount := 0
	conversionErrors := 0

	stats, err := streamBigQueryBilling(ctx, config, startDate, endDate, account, func(row models.BigQueryBillingRow) error {
		converted, convertErr := convertToGcpUsageReportItem(row)
		if convertErr != nil {
			conversionErrors++
			logger.Error("failed to convert billing row", "error", convertErr, "accountNumber", account.AccountNumber)
			return nil // skip this row, keep streaming
		}
		for _, item := range converted {
			convertedItemCount++
			mergeIntoDailyAggregate(item, aggregatedItemsMap, serviceTotals)
		}
		return nil
	})
	if err != nil {
		logger.Error("failed to query BigQuery billing data", "error", err, "accountNumber", account.AccountNumber)
		return providers.GetUsageReportResponse{}, err
	}

	aggregatedItems := make([]providers.UsageReportItem, 0, len(aggregatedItemsMap))
	for _, item := range aggregatedItemsMap {
		aggregatedItems = append(aggregatedItems, item)
	}

	// Calculate overall total
	var totalCost float64
	for _, cost := range serviceTotals {
		totalCost += cost
	}

	// Check if data covers full month using firstDate/lastDate captured during streaming
	if stats.RowCount > 0 {
		daysCovered := stats.LastDate.Sub(stats.FirstDate).Hours() / 24
		expectedDays := float64(endDate.Sub(startDate).Hours() / 24)

		if daysCovered < expectedDays*incompleteDataThreshold { // Less than 80% of expected days
			logger.Warn("billing data may be incomplete for this period",
				"firstDate", stats.FirstDate.Format("2006-01-02"),
				"lastDate", stats.LastDate.Format("2006-01-02"),
				"daysCovered", int(daysCovered),
				"expectedDays", int(expectedDays),
				"note", "BigQuery export only captures data from when it was enabled")
		}
	}

	logger.Info("processed billing data",
		"originalRows", stats.RowCount,
		"convertedItems", convertedItemCount,
		"conversionErrors", conversionErrors,
		"aggregatedItems", len(aggregatedItems),
		"uniqueServices", len(serviceTotals),
		"serviceTotals", serviceTotals,
		"totalCost", totalCost,
		"accountNumber", account.AccountNumber)

	return providers.GetUsageReportResponse{
		Items: aggregatedItems,
		Dates: []time.Time{}, // GCP doesn't use dates array like AWS
	}, nil
}
