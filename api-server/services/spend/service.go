package spend

import (
	"fmt"
	"log/slog"
	"nudgebee/services/internal/database"
	"time"
)

func GetSpendByCloudAccount(tenantId string, sevenDaysAgo time.Time, yesterday time.Time) ([]GetSpendByCloudAccountResponse, error) {
	databaseManger, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, err
	}
	rows, err := databaseManger.Db.Queryx("select ca.id, ca.account_name , round(s.amount::numeric, 2)::float as amount , round(estimated_savings::numeric, 2)::float as saving, round(s1.amount::numeric, 2)::float as amount_last, case when s1.amount > 0 then round(((s.amount - s1.amount) / s1.amount * 100)::numeric, 2)::float else 0 end as percentage_change from cloud_accounts ca inner join ( select sum(spends.amount) as amount, spends.cloud_account from spends where spends.date > $2 and spends.date < $3 and tenant = $1 group by spends.cloud_account ) s on ca.id = s.cloud_account left join ( select sum(spends.amount) as amount, spends.cloud_account from spends where spends.date > $2 - interval '7 DAY' and spends.date < $2 and tenant = $1 group by spends.cloud_account ) s1 on ca.id = s1.cloud_account left join ( select recommendation.cloud_account_id , sum(recommendation.estimated_savings) as estimated_savings from recommendation group by recommendation.cloud_account_id ) r on ((ca.id = r.cloud_account_id)) order by s.amount desc limit 5", tenantId, yesterday, sevenDaysAgo)
	if err != nil {
		return nil, err
	}
	defer func() {
		err := rows.Close()
		if err != nil {
			slog.Error("Error closing rows", "error", err)
		}
	}()

	var response []GetSpendByCloudAccountResponse
	for rows.Next() {
		var row GetSpendByCloudAccountResponse
		err = rows.StructScan(&row)
		if err != nil {
			return nil, err
		}
		response = append(response, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating spend by cloud account: %w", err)
	}
	return response, nil
}

func GetSpendByService(tenantId string, sevenDaysAgo time.Time, yesterday time.Time) ([]GetSpendByServiceResponse, error) {
	databaseManger, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, err
	}
	rows, err := databaseManger.Db.Queryx("select cr.tenant, cr.service_name, count(*) as resource_count, round(sum(s.amount)::numeric, 2)::float as spend_amount , case when sum(s1.amount) is not null then round(sum(s1.amount)::numeric, 2)::float else 0 end as spend_amount_last, case when sum(s1.amount) > 0 then round(( ( sum(s.amount) - sum(s1.amount)) / sum(s1.amount) * 100)::numeric, 2)::float else 0 end as percentage_change, round( sum(r.estimated_savings)::numeric, 2)::float as resource_estimated_saving from ( ( ( cloud_resourses cr left join ( select recommendation.resource_id, sum(recommendation.estimated_savings) as estimated_savings from recommendation group by recommendation.resource_id ) r on ((cr.id = r.resource_id)) ) join ( select spends.cloud_resource_id, sum(spends.amount) as amount from spends where spends.date > $2 and spends.date < $3 and tenant = $1 group by spends.cloud_resource_id ) s on ((s.cloud_resource_id = cr.id)) left join ( select spends.cloud_resource_id, sum(spends.amount) as amount from spends where spends.date > $2 - interval '7 DAY' and spends.date < $2 and tenant = $1 group by spends.cloud_resource_id ) s1 on ((s1.cloud_resource_id = cr.id)) ) join cloud_accounts a on ((a.id = cr.account)) ) where s.amount > 0 and cr.service_name is not null group by cr.tenant, cr.service_name order by sum(s.amount) desc limit 20", tenantId, yesterday, sevenDaysAgo)
	if err != nil {
		return nil, err
	}
	defer func() {
		err := rows.Close()
		if err != nil {
			slog.Error("Error closing rows", "error", err)
		}
	}()

	var response []GetSpendByServiceResponse
	for rows.Next() {
		var row GetSpendByServiceResponse
		err = rows.StructScan(&row)
		if err != nil {
			return nil, err
		}
		response = append(response, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating spend by service: %w", err)
	}
	return response, nil

}
