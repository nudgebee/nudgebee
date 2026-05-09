package marketplace

import (
	"database/sql"
	"errors"
	"log/slog"
	"nudgebee/services/internal/database"
	"nudgebee/services/internal/database/models"
	"nudgebee/services/security"
	"nudgebee/services/user"
)

func AddCustomerSubscription(subscription CustomerSubscription) (interface{}, error) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		slog.Error("error getting database manager while creating marketplace customer", "error", err)
		return nil, err
	}
	slog.Info("New marketplace subscription received", "subscription", subscription)

	query := "select id, customer_identifier, marketplace, tenant_id from marketplace_customers where marketplace = $1 and customer_identifier = $2 and provider_account_id = $3 and product_code = $4"

	existingCustomer := CustomerTenant{}
	err = dbms.Db.QueryRowx(query, subscription.Marketplace, subscription.CustomerIdentifier, subscription.ProviderAccountId, subscription.ProductCode).StructScan(&existingCustomer)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		slog.Error("error querying customer data", "error", err)
		return nil, err
	}

	if existingCustomer.CustomerIdentifier != "" {
		return existingCustomer, nil
	}

	insertQuery := `INSERT INTO marketplace_customers (marketplace, customer_identifier, provider_account_id, product_code) 
					VALUES ($1, $2, $3, $4) ON CONFLICT (marketplace, customer_identifier, provider_account_id, product_code) DO NOTHING`
	_, err = dbms.Db.Exec(insertQuery, subscription.Marketplace, subscription.CustomerIdentifier, subscription.ProviderAccountId, subscription.ProductCode)
	if err != nil {
		slog.Error("error inserting new customer subscription", "error", err)
		return nil, err
	}

	err = dbms.Db.QueryRowx(query, subscription.Marketplace, subscription.CustomerIdentifier, subscription.ProviderAccountId, subscription.ProductCode).StructScan(&existingCustomer)
	if err != nil {
		slog.Error("error querying customer after insert", "error", err)
		return nil, err
	}

	if subscription.Marketplace == "aws" {
		go func(subscription CustomerSubscription) {
			var entitlementRequest AwsEntitlementPayload
			entitlementRequest.Action = "entitlement-updated"
			entitlementRequest.CustomerIdentifier = subscription.CustomerIdentifier
			entitlementRequest.ProductCode = subscription.ProductCode
			err := updateEntitlementForCustomer(entitlementRequest)
			if err != nil {
				slog.Error("error updating entitlements", "error", err)
			}
		}(subscription)
	}
	return existingCustomer, nil
}

func CreateUserAndTenant(ctx *security.RequestContext, request NewCustomerTenantRequest) (interface{}, error) {

	response, err := user.CreateUser(ctx, user.UserCreateRequest{
		Username:   request.Username,
		Firstname:  request.Firstname,
		Lastname:   request.Lastname,
		Tenantname: request.Tenantname,
		Role:       request.Role,
	})
	if err != nil {
		return models.Tenant{}, err
	}
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		slog.Error("error getting database manager while creating marketplace customer", "error", err)
		return nil, err
	}
	updateQuery := `UPDATE marketplace_customers SET tenant_id = $1, is_active = true, subscription_status = 'subscribed' WHERE customer_identifier = $2`
	_, err = dbms.Db.Exec(updateQuery, response.TenantId, request.CustomerIdentifier)
	if err != nil {
		slog.Error("error inserting new customer subscription", "error", err)
		return nil, err
	}
	slog.Info("New Tenant Created Successfully", "tenantId", response.TenantId)
	return response, nil
}

func SendUsageReportsToMarketplacesForBilling() error {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		slog.Error("error getting database manager while updating azure subscription", "error", err)
		return err
	}
	rows, err := dbms.Db.Queryx("SELECT id, customer_identifier, product_code, marketplace, tenant_id FROM marketplace_customers WHERE is_active = true")
	if err != nil {
		slog.Error("error querying marketplace customers", "error", err)
		return err
	}
	defer func() {
		err := rows.Close()
		if err != nil {
			slog.Error("error closing rows", "error", err)
		}
	}()

	for rows.Next() {
		var customer Customer
		err := rows.StructScan(&customer)
		if err != nil {
			slog.Error("error scanning customer row", "error", err)
			continue
		}

		switch marketplace := customer.Marketplace; marketplace {
		case "aws":
			err = SendUsageEventToAwsForBilling(customer)
		case "azure":
			err = SendUsageEventToAzureForBilling(customer)
		default:
			slog.Error("unsupported marketplace", "marketplace", marketplace)
		}

		if err != nil {
			slog.Error("error sending metered billing to Azure", "error", err)
			continue
		}
	}
	return nil
}
