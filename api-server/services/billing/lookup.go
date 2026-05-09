package billing

import (
	"database/sql"
	"fmt"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
)

type BillingListRequest struct {
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

type BillingListItem struct {
	ID               string  `json:"id" db:"id"`
	AmountDue        float32 `json:"amount_due" db:"amount_due"`
	LastBilledAmount float32 `json:"last_billed_amount" db:"last_billed_amount"`
	LastBilledDate   string  `json:"last_billed_date" db:"last_billed_date"`
	Tier             string  `json:"tier" db:"tier"`
	CreatedAt        string  `json:"created_at" db:"created_at"`
	UpdatedAt        string  `json:"updated_at" db:"updated_at"`
}

type BillingListResponse struct {
	Billing    []BillingListItem     `json:"billing"`
	TotalCount BillingAggregateCount `json:"total_count"`
}

type BillingAggregateCount struct {
	Aggregate CountFields `json:"aggregate"`
}

type CountFields struct {
	Count int `json:"count"`
}

type BillingUsageCostListRequest struct {
	StartDate string `json:"start_date"`
	EndDate   string `json:"end_date"`
	Limit     int    `json:"limit"`
	Offset    int    `json:"offset"`
}

type BillingUsageCostItem struct {
	BillingDate  string           `json:"billing_date" db:"billing_date"`
	CostPerUnit  float32          `json:"cost_per_unit" db:"cost_per_unit"`
	CreatedAt    string           `json:"created_at" db:"created_at"`
	ID           string           `json:"id" db:"id"`
	Name         string           `json:"name" db:"name"`
	ServiceName  string           `json:"service_name" db:"service_name"`
	TotalCost    float32          `json:"total_cost" db:"total_cost"`
	Units        int              `json:"units" db:"units"`
	UpdatedAt    string           `json:"updated_at" db:"updated_at"`
	AccountID    string           `json:"account_id" db:"account_id"`
	CloudAccount *CloudAccountRef `json:"cloud_account"`
}

type CloudAccountRef struct {
	AccountName string `json:"account_name"`
}

type BillingUsageCostListResponse struct {
	BillingUsageCost          []BillingUsageCostItem `json:"billing_usage_cost"`
	BillingUsageCostAggregate BillingAggregateCount  `json:"billing_usage_cost_aggregate"`
}

type BillingInfographicsResponse struct {
	TotalAmountDue    BillingAggregateSum `json:"total_amount_due"`
	TotalBilledAmount BillingAggregateSum `json:"total_billed_amount"`
}

type BillingAggregateSum struct {
	Aggregate SumFields `json:"aggregate"`
}

type SumFields struct {
	Sum SumValues `json:"sum"`
}

type SumValues struct {
	AmountDue        *float32 `json:"amount_due,omitempty"`
	LastBilledAmount *float32 `json:"last_billed_amount,omitempty"`
}

// usageCostRow is an internal struct for scanning the JOIN query result
type usageCostRow struct {
	BillingDate string         `db:"billing_date"`
	CostPerUnit float32        `db:"cost_per_unit"`
	CreatedAt   string         `db:"created_at"`
	ID          string         `db:"id"`
	Name        string         `db:"name"`
	ServiceName string         `db:"service_name"`
	TotalCost   float32        `db:"total_cost"`
	Units       int            `db:"units"`
	UpdatedAt   string         `db:"updated_at"`
	AccountID   sql.NullString `db:"account_id"`
	AccountName sql.NullString `db:"account_name"`
}

func ListBillings(ctx *security.RequestContext, request BillingListRequest) (BillingListResponse, error) {
	tenantID := ctx.GetSecurityContext().GetTenantId()
	if tenantID == "" {
		return BillingListResponse{}, fmt.Errorf("unauthorized: missing tenant")
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return BillingListResponse{}, fmt.Errorf("failed to get database: %w", err)
	}

	limit := max(request.Limit, 1)
	offset := max(request.Offset, 0)

	var items []BillingListItem
	err = dbms.Db.Select(&items,
		`SELECT id, amount_due, last_billed_amount, COALESCE(TO_CHAR(last_billed_date, 'YYYY-MM-DD"T"HH24:MI:SS"Z"'), '') as last_billed_date, COALESCE(tier, '') as tier, created_at, updated_at
		 FROM billing
		 WHERE tenant_id = $1
		 ORDER BY last_billed_date DESC NULLS LAST
		 LIMIT $2 OFFSET $3`,
		tenantID, limit, offset)
	if err != nil {
		return BillingListResponse{}, fmt.Errorf("failed to query billing: %w", err)
	}
	if items == nil {
		items = []BillingListItem{}
	}

	var count int
	err = dbms.Db.Get(&count, "SELECT COUNT(*) FROM billing WHERE tenant_id = $1", tenantID)
	if err != nil {
		return BillingListResponse{}, fmt.Errorf("failed to count billing: %w", err)
	}

	return BillingListResponse{
		Billing: items,
		TotalCount: BillingAggregateCount{
			Aggregate: CountFields{Count: count},
		},
	}, nil
}

func ListBillingUsageCosts(ctx *security.RequestContext, request BillingUsageCostListRequest) (BillingUsageCostListResponse, error) {
	tenantID := ctx.GetSecurityContext().GetTenantId()
	if tenantID == "" {
		return BillingUsageCostListResponse{}, fmt.Errorf("unauthorized: missing tenant")
	}

	if request.StartDate == "" || request.EndDate == "" {
		return BillingUsageCostListResponse{}, fmt.Errorf("start_date and end_date are required")
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return BillingUsageCostListResponse{}, fmt.Errorf("failed to get database: %w", err)
	}

	limit := max(request.Limit, 1)
	offset := max(request.Offset, 0)

	var rows []usageCostRow
	err = dbms.Db.Select(&rows,
		`SELECT buc.billing_date, buc.cost_per_unit, buc.created_at, buc.id, buc.name,
		        buc.service_name, buc.total_cost, buc.units, buc.updated_at,
		        buc.account_id, ca.account_name
		 FROM billing_usage_cost buc
		 LEFT JOIN cloud_accounts ca ON buc.account_id = ca.id
		 WHERE buc.tenant_id = $1 AND buc.billing_date >= $2 AND buc.billing_date <= $3
		 ORDER BY buc.billing_date DESC
		 LIMIT $4 OFFSET $5`,
		tenantID, request.StartDate, request.EndDate, limit, offset)
	if err != nil {
		return BillingUsageCostListResponse{}, fmt.Errorf("failed to query billing_usage_cost: %w", err)
	}

	items := make([]BillingUsageCostItem, 0, len(rows))
	for _, r := range rows {
		item := BillingUsageCostItem{
			BillingDate: r.BillingDate,
			CostPerUnit: r.CostPerUnit,
			CreatedAt:   r.CreatedAt,
			ID:          r.ID,
			Name:        r.Name,
			ServiceName: r.ServiceName,
			TotalCost:   r.TotalCost,
			Units:       r.Units,
			UpdatedAt:   r.UpdatedAt,
		}
		if r.AccountID.Valid {
			item.AccountID = r.AccountID.String
		}
		if r.AccountName.Valid {
			item.CloudAccount = &CloudAccountRef{AccountName: r.AccountName.String}
		}
		items = append(items, item)
	}

	var count int
	err = dbms.Db.Get(&count,
		`SELECT COUNT(*) FROM billing_usage_cost
		 WHERE tenant_id = $1 AND billing_date >= $2 AND billing_date <= $3`,
		tenantID, request.StartDate, request.EndDate)
	if err != nil {
		return BillingUsageCostListResponse{}, fmt.Errorf("failed to count billing_usage_cost: %w", err)
	}

	return BillingUsageCostListResponse{
		BillingUsageCost: items,
		BillingUsageCostAggregate: BillingAggregateCount{
			Aggregate: CountFields{Count: count},
		},
	}, nil
}

type infographicsRow struct {
	SumAmountDue        float32 `db:"sum_amount_due"`
	SumLastBilledAmount float32 `db:"sum_last_billed_amount"`
}

func GetBillingInfographics(ctx *security.RequestContext) (BillingInfographicsResponse, error) {
	tenantID := ctx.GetSecurityContext().GetTenantId()
	if tenantID == "" {
		return BillingInfographicsResponse{}, fmt.Errorf("unauthorized: missing tenant")
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return BillingInfographicsResponse{}, fmt.Errorf("failed to get database: %w", err)
	}

	var row infographicsRow
	err = dbms.Db.Get(&row,
		`SELECT COALESCE(SUM(amount_due), 0) as sum_amount_due,
		        COALESCE(SUM(last_billed_amount), 0) as sum_last_billed_amount
		 FROM billing WHERE tenant_id = $1`,
		tenantID)
	if err != nil {
		return BillingInfographicsResponse{}, fmt.Errorf("failed to query billing infographics: %w", err)
	}

	return BillingInfographicsResponse{
		TotalAmountDue: BillingAggregateSum{
			Aggregate: SumFields{
				Sum: SumValues{AmountDue: &row.SumAmountDue},
			},
		},
		TotalBilledAmount: BillingAggregateSum{
			Aggregate: SumFields{
				Sum: SumValues{LastBilledAmount: &row.SumLastBilledAmount},
			},
		},
	}, nil
}
