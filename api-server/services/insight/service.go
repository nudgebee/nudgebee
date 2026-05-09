package insight

import (
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"log/slog"
	"nudgebee/services/common"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
	"strings"
	"sync"
	"time"

	"github.com/samber/lo"
)

//go:embed insight_rules.json
var embeddedJSONContent []byte

func readJSONFile() ([]InsightRule, error) {
	var rules []InsightRule
	err := common.UnmarshalJson(embeddedJSONContent, &rules)
	return rules, err
}

func tryProcessRule(ctx *security.RequestContext, rule InsightRule, accountIdAndTenantIds map[string]tenantAndProvider) error {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("Failed to process rule", "rule", rule.UniqueID, "recover", r)
		}
	}()

	if len(rule.CloudProviders) > 0 {
		//execute only applicable rules
		filteredAccounts := make(map[string]tenantAndProvider)
		providers := lo.Map(rule.CloudProviders, func(provider string, i int) string {
			return strings.ToLower(provider)
		})
		for accountId, tenantAndProvider := range accountIdAndTenantIds {
			if lo.Contains(providers, strings.ToLower(tenantAndProvider.cloudProvider)) {
				filteredAccounts[accountId] = tenantAndProvider
			}
		}
		accountIdAndTenantIds = filteredAccounts

		if len(accountIdAndTenantIds) == 0 {
			return nil
		}
	}

	ctx.GetLogger().Info(fmt.Sprintf("Closing existing insights for %s", rule.UniqueID))
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return err
	}

	_, err = dbms.Db.Exec("UPDATE insight SET status = 'CLOSED' WHERE unique_id = $1", rule.UniqueID)
	if err != nil {
		return err
	}

	executor, err := newRuleExecutor(ctx, rule)
	if err != nil {
		return err
	}
	insights, err := executor.ExecuteRule(rule, lo.Keys(accountIdAndTenantIds))
	if err != nil {
		return err
	}
	for i := range insights {
		if insights[i].Title == "" {
			continue
		}

		if insights[i].AccountID == "" {
			continue
		}

		tp := accountIdAndTenantIds[insights[i].AccountID]
		insights[i].Rule.RedirectURL = computeRedirectURL(rule, insights[i].AccountID, tp.cloudProvider)
		insight := insights[i]

		jsonRule, err := common.MarshalJson(insight.Rule)
		if err != nil {
			return err
		}
		applicationsJSON, err := common.MarshalJson(insight.Applications)
		if err != nil {
			return fmt.Errorf("failed to serialize applications: %w", err)
		}
		query := `INSERT INTO insight (tenant, account_id, resource_id, unique_id, title, status, rule, type, source, applications, severity) 
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11) 
			ON CONFLICT (tenant, account_id, unique_id) 
				DO UPDATE SET title = EXCLUDED.title, status = EXCLUDED.status, rule = EXCLUDED.rule, applications = EXCLUDED.applications, updated_at = now(), type = EXCLUDED.type, source = EXCLUDED.source, severity= EXCLUDED.severity`
		_, err = dbms.Db.Exec(query, accountIdAndTenantIds[insight.AccountID].tenant, insight.AccountID, sql.NullString{String: insight.ResourceID, Valid: (insight.ResourceID != "")}, insight.UniqueID, insight.Title, insight.Status, string(jsonRule), insight.Type, insight.Source, applicationsJSON, insight.Severity)
		if err != nil {
			return err
		}
	}

	return nil
}

type tenantAndProvider struct {
	tenant        string
	cloudProvider string
}

func getAccountDetails(ctx *security.RequestContext, accountIds ...string) (map[string]tenantAndProvider, error) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, err
	}
	query := `select ca.id as cloud_account_id , ca.tenant as tenant, ca.cloud_provider as cloud_provider from cloud_accounts ca inner join agent a on ca.id = a.cloud_account_id where ca.status = 'active' and a.status = 'CONNECTED' group by ca.tenant, ca.id, ca.cloud_provider`
	var args []any
	if len(accountIds) > 0 {
		query = `select ca.id as cloud_account_id , ca.tenant as tenant, ca.cloud_provider as cloud_provider from cloud_accounts ca inner join agent a on ca.id = a.cloud_account_id where ca.status = 'active' and a.status = 'CONNECTED' and ca.id in (?) group by ca.tenant, ca.id, ca.cloud_provider`
		args = append(args, accountIds)
	}
	rows, err := dbms.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer func() {
		err := rows.Close()
		if err != nil {
			slog.Error("Failed to close rows", "error", err)
		}
	}()
	// create map of account ids with value tenant
	accounAndTenantIds := make(map[string]tenantAndProvider)
	for rows.Next() {
		var cloudAccountId, tenant, cloudProvider string
		err = rows.Scan(&cloudAccountId, &tenant, &cloudProvider)
		if err != nil {
			return nil, err
		}
		accounAndTenantIds[cloudAccountId] = tenantAndProvider{tenant: tenant, cloudProvider: cloudProvider}
	}
	return accounAndTenantIds, nil
}

func Process(ctx *security.RequestContext, accountIds ...string) error {
	startTime := time.Now()
	rules, err := readJSONFile()
	if err != nil {
		return fmt.Errorf("insight: failed to read rules file: %w", err)
	}

	// fix defaults
	rules = lo.Map(rules, func(rule InsightRule, i int) InsightRule {
		if rule.RangeUnit == "" {
			rule.RangeUnit = InsightRangeUnitDay
		}
		if rule.Range == 0 {
			rule.Range = 7
		}
		return rule
	})

	ctx.GetLogger().Info(fmt.Sprintf("Processing %d insight rules", len(rules)))

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return err
	}

	ruleIds := lo.Map(rules, func(rule InsightRule, i int) string {
		return rule.UniqueID
	})

	query := "UPDATE insight SET status = 'CLOSED' WHERE unique_id NOT IN (?)"
	var args []any
	args = append(args, ruleIds)

	if len(accountIds) > 0 {
		query += " AND account_id IN (?)"
		args = append(args, accountIds)
	}

	_, err = dbms.Exec(query, args...)
	if err != nil {
		return err
	}

	// create map of account ids with value tenant
	accounAndTenantIds, err := getAccountDetails(ctx, accountIds...)
	if err != nil {
		return err
	}

	for _, rule := range rules {
		err = tryProcessRule(ctx, rule, accounAndTenantIds)
		if err != nil {
			ctx.GetLogger().Error("Failed to process rule, continuing with next rule", "ruleId", rule.UniqueID, "error", err)
		}
	}

	endTime := time.Now()
	ctx.GetLogger().Info(fmt.Sprintf("Insight analysis completed in %s", endTime.Sub(startTime)))

	return nil
}

func GetInsights(ctx *security.RequestContext, accountId string) ([]InsightListResponse, error) {
	rules, err := readJSONFile()
	if err != nil {
		return nil, fmt.Errorf("insight: failed to read rules file: %w", err)
	}

	// Fix defaults
	rules = lo.Map(rules, func(rule InsightRule, i int) InsightRule {
		if rule.RangeUnit == "" {
			rule.RangeUnit = InsightRangeUnitDay
		}
		if rule.Range == 0 {
			rule.Range = 7
		}
		return rule
	})
	var allInsights []InsightListResponse
	accountDetails, err := getAccountDetails(ctx, accountId)
	if err != nil {
		return nil, err
	}
	accountDetail := accountDetails[accountId]
	if accountDetail.tenant == "" {
		return allInsights, errors.New("account not found or not connected")
	}

	// Execute rules in parallel
	var wg sync.WaitGroup
	var mu sync.Mutex
	sem := make(chan struct{}, 10) // Limit concurrency to 10

	for _, rule := range rules {
		wg.Add(1)
		go func(rule InsightRule) {
			defer wg.Done()
			sem <- struct{}{}        // Acquire token
			defer func() { <-sem }() // Release token

			if len(rule.CloudProviders) > 0 {
				provieders := lo.Map(rule.CloudProviders, func(provider string, i int) string {
					return strings.ToLower(provider)
				})
				//execute only applicable rules
				if !lo.Contains(provieders, strings.ToLower(accountDetail.cloudProvider)) {
					return
				}
			}

			executor, err := newRuleExecutor(ctx, rule)
			if err != nil {
				slog.Error("Failed to execute rule", "error", err)
				return
			}
			insights, err := executor.ExecuteRule(rule, []string{accountId})
			if err != nil {
				slog.Error("Failed to execute rule", "error", err)
				return
			}

			if len(insights) > 0 {
				mu.Lock()
				for _, ins := range insights {
					ruleWithURL := rule
					ruleWithURL.RedirectURL = computeRedirectURL(rule, accountId, accountDetail.cloudProvider)
					resp := InsightListResponse{Title: ins.Title, Source: string(ins.Source), Rule: ruleWithURL, Applications: ins.Applications, Type: ins.Type}
					if resp.Title == "" {
						continue
					}
					allInsights = append(allInsights, resp)
				}
				mu.Unlock()
			}
		}(rule)
	}

	wg.Wait()
	return allInsights, nil
}
