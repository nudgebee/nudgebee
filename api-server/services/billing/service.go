package billing

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"nudgebee/services/account"
	"nudgebee/services/config"
	"nudgebee/services/internal/database"
	"nudgebee/services/tenant"
	"strings"
	"time"
)

func ChargeTenantsForDailyUsage() error {
	tenants, err := tenant.ListTenants(nil)
	if err != nil {
		slog.Error("error listing tenants while generating billing costs", "error", err)
		return err
	}
	for _, tnt := range tenants {
		slog.Info("generating billing usage costs for tenant", "tenantID", tnt.Id)
		billingDate := time.Now().Add(-24 * time.Hour).Truncate(24 * time.Hour)
		var usageCosts []UsageCostPerUnit

		if err := addClusterAndNodeUsageCosts(tnt.Id, billingDate, &usageCosts); err != nil {
			slog.Error("error adding cluster and node usage costs", "error", err, "tenantID", tnt.Id)
		}

		if err := addAutomationUsageCost(tnt.Id, billingDate, &usageCosts); err != nil {
			slog.Error("error adding automation usage costs", "error", err, "tenantID", tnt.Id)
		}

		if err := calculateAndStoreServiceCost(usageCosts); err != nil {
			slog.Error("error calculating billing usage costs", "error", err, "tenantID", tnt.Id)
			continue
		}
		slog.Info("Generated billing usage costs for tenant successfully", "tenantID", tnt.Id)
		err := UpdateBillAmounts(tnt.Id, billingDate, usageCosts)
		if err != nil {
			slog.Error("Alert! Failed to update bill amount for tenant", "tenantID", tnt.Id)
		}
	}
	return nil
}

func calculateAndStoreServiceCost(usageCosts []UsageCostPerUnit) error {
	dbm, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		slog.Error("error getting database manager while storing billing costs", "error", err)
		return err
	}

	var queryBuilderWithAccountID, queryBuilderWithoutAccountID strings.Builder
	queryBuilderWithAccountID.WriteString("INSERT INTO billing_usage_cost (tenant_id, billing_date, service_name, name, account_id, units, cost_per_unit, total_cost) VALUES ")
	queryBuilderWithoutAccountID.WriteString("INSERT INTO billing_usage_cost (tenant_id, billing_date, service_name, name, units, cost_per_unit, total_cost) VALUES ")

	valuesWithAccountID := make([]string, 0)
	valuesWithoutAccountID := make([]string, 0)

	for _, uc := range usageCosts {
		if uc.AccountId != "" {
			valuesWithAccountID = append(valuesWithAccountID, fmt.Sprintf("('%s', '%s', '%s', '%s', '%s', '%d', %.2f, %.2f)",
				uc.TenantID, uc.BillingDate.Format(time.RFC3339), uc.ServiceName, uc.Name, uc.AccountId, uc.Units, uc.CostPerUnit, uc.TotalCost))
		} else {
			valuesWithoutAccountID = append(valuesWithoutAccountID, fmt.Sprintf("('%s', '%s', '%s', '%s', '%d', %.2f, %.2f)",
				uc.TenantID, uc.BillingDate.Format(time.RFC3339), uc.ServiceName, uc.Name, uc.Units, uc.CostPerUnit, uc.TotalCost))
		}
	}

	if len(valuesWithAccountID) > 0 {
		queryBuilderWithAccountID.WriteString(strings.Join(valuesWithAccountID, ","))
		queryBuilderWithAccountID.WriteString(" ON CONFLICT (tenant_id, billing_date, service_name, name, account_id) DO UPDATE SET ")
		queryBuilderWithAccountID.WriteString("units = EXCLUDED.units, cost_per_unit = EXCLUDED.cost_per_unit, total_cost = EXCLUDED.total_cost")

		_, err = dbm.Db.Exec(queryBuilderWithAccountID.String())
		if err != nil {
			slog.Error("error storing billing costs with account_id", "error", err)
			return err
		}
	}

	if len(valuesWithoutAccountID) > 0 {
		queryBuilderWithoutAccountID.WriteString(strings.Join(valuesWithoutAccountID, ","))
		queryBuilderWithoutAccountID.WriteString(" ON CONFLICT (tenant_id, billing_date, service_name, name, account_id) DO UPDATE SET ")
		queryBuilderWithoutAccountID.WriteString("units = EXCLUDED.units, cost_per_unit = EXCLUDED.cost_per_unit, total_cost = EXCLUDED.total_cost")

		_, err = dbm.Db.Exec(queryBuilderWithoutAccountID.String())
		if err != nil {
			slog.Error("error storing billing costs without account_id", "error", err)
			return err
		}
	}

	return nil
}

func addClusterAndNodeUsageCosts(tenantID string, billingDate time.Time, usageCosts *[]UsageCostPerUnit) error {
	services := []string{"troubleshoot", "optimization"}

	accounts, err := account.ListActiveAccountsWithConnectedAgents(nil, tenantID)
	if err != nil {
		slog.Error("error listing accounts while generating billing costs for tenant", "tenantID", tenantID)
		return err
	}
	if len(accounts) == 0 {
		slog.Warn("no active account found for tenant", "tenantID", tenantID)
		return nil
	}

	nodeCountsByAccount, err := account.GetActiveK8sNodeCountForAccounts(tenantID)
	if err != nil {
		slog.Error("error getting active node count for tenant", "tenantID", tenantID)
		return err
	}

	chargeableAccountUnits := len(accounts) - config.Config.BillingFreeCluster
	accountCharges := float32(chargeableAccountUnits) * config.Config.BillingClusterCharges
	if accountCharges < 0 {
		accountCharges = 0
	}

	for _, svc := range services {
		*usageCosts = append(*usageCosts, UsageCostPerUnit{
			TenantID:    tenantID,
			BillingDate: billingDate,
			ServiceName: svc,
			Name:        "active_clusters",
			Units:       len(accounts),
			CostPerUnit: config.Config.BillingClusterCharges,
			TotalCost:   accountCharges,
		})

		for _, nodeCount := range nodeCountsByAccount {
			chargeableNodeUnits := nodeCount.Count - config.Config.BillingFreeNodes
			nodeCharges := float32(chargeableNodeUnits) * config.Config.BillingNodeCharges
			if nodeCharges < 0 {
				nodeCharges = 0
			}
			*usageCosts = append(*usageCosts, UsageCostPerUnit{
				TenantID:    tenantID,
				BillingDate: billingDate,
				ServiceName: svc,
				AccountId:   nodeCount.CloudAccountId,
				Name:        "active_nodes",
				Units:       nodeCount.Count,
				CostPerUnit: config.Config.BillingNodeCharges,
				TotalCost:   nodeCharges,
			})
		}
	}

	return nil
}

func addAutomationUsageCost(tenantID string, billingDate time.Time, usageCosts *[]UsageCostPerUnit) error {
	dbm, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		slog.Error("error getting database manager while storing billing costs", "error", err)
		return err
	}

	serviceName := "automation"

	countAndAppendUsageCost := func(query, name string) error {
		var runs []AccountAutomationRuns
		tillDate := time.Now().Add(24 * time.Hour).Truncate(24 * time.Hour)
		err := dbm.Db.Select(&runs, query, tenantID, billingDate, tillDate)
		if err != nil {
			return err
		}
		if len(runs) == 0 {
			return nil
		}

		for _, run := range runs {
			totalCost, err := chargeForAutomationRuns(dbm, tenantID, name, run)
			if err != nil {
				totalCost = 0.00
			}

			*usageCosts = append(*usageCosts, UsageCostPerUnit{
				TenantID:    tenantID,
				BillingDate: billingDate,
				ServiceName: serviceName,
				Name:        name,
				AccountId:   run.AccountId,
				Units:       run.Count,
				CostPerUnit: 0.00,
				TotalCost:   totalCost,
			})
		}

		return nil
	}

	err = countAndAppendUsageCost("SELECT account_id, COUNT(id) FROM auto_playbook_executions WHERE status = 'COMPLETE' AND tenant_id = $1 AND created_at BETWEEN $2 AND $3 GROUP BY account_id", "auto_runbook_runs")
	if err != nil {
		slog.Error("error getting auto playbook usage costs", "error", err)
		return err
	}

	err = countAndAppendUsageCost("SELECT account_id, COUNT(id) FROM auto_pilot_task WHERE status = 'Complete' AND tenant_id = $1 AND created_at BETWEEN $2 AND $3 GROUP BY account_id", "auto_optimize_runs")
	if err != nil {
		slog.Error("error getting auto optimize usage costs", "error", err)
		return err
	}

	return nil
}

func chargeForAutomationRuns(dbm *database.DatabaseManager, tenantID string, name string, run AccountAutomationRuns) (float32, error) {
	lastBilledDate, err := getLastBilledDate(tenantID, dbm)
	if err != nil {
		return 0, err
	}

	var pastRuns PastAutomationRuns
	query := `
		SELECT COALESCE(SUM(units), 0) AS total_units, COALESCE(SUM(total_cost), 0.0) AS total_cost 
		FROM billing_usage_cost 
		WHERE tenant_id = $1 AND account_id = $2 AND name = $3 AND billing_date > $4
	`
	if err = dbm.Db.Get(&pastRuns, query, tenantID, run.AccountId, name, lastBilledDate); err != nil {
		return 0, err
	}

	totalCost := float32(0.0)
	totalRuns := pastRuns.TotalUnits + run.Count

	if totalRuns > 1000 {
		totalCost = (float32(totalRuns/1000) * config.Config.BillingAutomationCharges) - pastRuns.TotalCost
	}

	return totalCost, nil
}

func UpdateBillAmounts(tenantId string, billingDate time.Time, usageCosts []UsageCostPerUnit) error {
	dbm, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		slog.Error("error getting database manager", "error", err)
		return err
	}
	slog.Info("Updating billing details for tenant", "tenantId", tenantId)

	existingBill := Billing{}
	err = dbm.Db.Get(&existingBill, "SELECT id, tenant_id, last_billed_date, last_billed_amount, amount_due, total_paid FROM billing WHERE tenant_id = $1 AND date_trunc('month', last_billed_date) = date_trunc('month', CAST($2 AS TIMESTAMP))", tenantId, billingDate)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		slog.Error("error checking existing bill for tenant", "tenantId", tenantId, "error", err)
		return err
	}

	totalCost := float32(0)
	for _, cost := range usageCosts {
		totalCost += cost.TotalCost
	}

	if existingBill.TenantID == "" {
		_, err = dbm.Db.Exec(`
        INSERT INTO billing (tenant_id, last_billed_date, last_billed_amount, amount_due)
        VALUES ($1, $2, $3, $4)
    `, tenantId, billingDate, totalCost, totalCost)
	} else if existingBill.LastBilledDate.Truncate(24 * time.Hour).Equal(billingDate.Truncate(24 * time.Hour)) {
		return nil
	} else {
		amountDue := existingBill.AmountDue + totalCost
		billedAmount := totalCost + existingBill.LastBilledAmount
		_, err = dbm.Db.Exec(`
        UPDATE billing
        SET last_billed_date = $2, last_billed_amount = $3, amount_due = $4
        WHERE tenant_id = $1 AND date_trunc('month', last_billed_date) = date_trunc('month', CAST($2 AS TIMESTAMP))
    `, tenantId, billingDate, billedAmount, amountDue)
	}

	if err != nil {
		slog.Error("error storing bill for tenant", "tenantId", tenantId, "error", err)
		return err
	}

	return nil
}

func getLastBilledDate(tenantId string, dbm *database.DatabaseManager) (string, error) {
	var lastBillDate string
	query := "SELECT COALESCE(MAX(last_billed_date), now()::DATE - 30) FROM billing WHERE tenant_id = $1"
	err := dbm.Db.Get(&lastBillDate, query, tenantId)
	if err != nil {
		slog.Error("error getting last billed date", "tenantId", tenantId, "error", err)
		return "", err
	}

	return lastBillDate, nil
}

func ListUsageCosts(tenantID string, startDate, endDate *time.Time) ([]UsageCostPerUnit, error) {
	dbm, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		slog.Error("error getting database manager", "error", err)
		return nil, err
	}

	var usageCosts []UsageCostPerUnit
	var query string
	var args []interface{}

	query = "SELECT id, tenant_id, billing_date, service_name, name, units, cost_per_unit, total_cost FROM billing_usage_cost WHERE tenant_id = $1 AND billing_date >= $2"
	args = append(args, tenantID, startDate)

	if endDate != nil {
		query += " AND billing_date <= $3"
		args = append(args, endDate)
	}

	err = dbm.Db.Select(&usageCosts, query, args...)
	if err != nil {
		slog.Error("error listing usage costs for tenant", "tenantID", tenantID, "error", err)
		return nil, err
	}

	return usageCosts, nil
}

func GenerateBillingDataForTenantForGivenDuration(payload GenerateChargePayload) error {
	tenantId := payload.TenantID
	slog.Info("Generating billing data for tenant for given duration", "tenant id", tenantId)

	startDate := payload.FromDate
	endDate := payload.ToDate

	currentDate := time.Date(startDate.Year(), startDate.Month(), 1, 0, 0, 0, 0, startDate.Location())
	for currentDate.Before(endDate) {
		nextMonth := time.Date(currentDate.Year(), currentDate.Month()+1, 1, 0, 0, 0, 0, currentDate.Location()).Add(-1 * time.Nanosecond).Truncate(24 * time.Hour)
		if nextMonth.After(endDate) {
			nextMonth = endDate
		}

		usageCosts, err := ListUsageCosts(tenantId, &currentDate, &nextMonth)
		if err != nil {
			return fmt.Errorf("error listing usage costs: %w", err)
		}

		if err := UpdateBillAmounts(tenantId, nextMonth, usageCosts); err != nil {
			return fmt.Errorf("error updating bill amounts: %w", err)
		}

		currentDate = nextMonth.AddDate(0, 0, 1)
	}

	slog.Info("Billing data generated successfully for tenant", "tenant id", tenantId)
	return nil
}
