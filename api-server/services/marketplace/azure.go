package marketplace

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"nudgebee/services/billing"
	"nudgebee/services/common"
	"nudgebee/services/config"
	"nudgebee/services/internal/database"
	"time"
)

var azureCustomPricingDimensionMap = map[string]string{
	"active_clusters":    "additional_cluster",
	"active_nodes":       "additional_nodes",
	"auto_runbook_runs":  "automation_runs",
	"auto_optimize_runs": "automation_runs",
}

func UpdateAzureSubscriptionBasedOnAction(payload AzurePayload) (interface{}, error) {

	switch payload.Action {
	case "ChangePlan":
		slog.Info("Plan change request for azure marketplace subscription received", "subscription", payload.SubscriptionID)
		_, err := updateAzureSubscription(payload)
		if err != nil {
			slog.Error("error updating azure subscription", "error", err)
			return nil, err
		}
		return nil, nil
	case "Renew":
		slog.Info("Renewal request for azure marketplace subscription received", "subscription", payload.SubscriptionID)
		_, err := updateAzureSubscription(payload)
		if err != nil {
			slog.Error("error updating azure subscription", "error", err)
			return nil, err
		}
		return nil, nil
	case "Suspend":
		slog.Info("Suspension request for azure marketplace subscription received", "subscription", payload.SubscriptionID)
		_, err := updateAzureSubscription(payload)
		if err != nil {
			slog.Error("error updating azure subscription", "error", err)
			return nil, err
		}
		return nil, nil
	case "Unsubscribe":
		slog.Info("Unsubscribe request for azure marketplace subscription received", "subscription", payload.SubscriptionID)
		_, err := updateAzureSubscription(payload)
		if err != nil {
			slog.Error("error updating azure subscription", "error", err)
			return nil, err
		}
		return nil, nil
	case "Reinstate":
		slog.Info("Reinstate request for azure marketplace subscription received", "subscription", payload.SubscriptionID)
		_, err := updateAzureSubscription(payload)
		if err != nil {
			slog.Error("error updating azure subscription", "error", err)
			return nil, err
		}
		return nil, nil
	default:
		slog.Warn("received unsupported action", "action", payload.Action)
	}
	return nil, nil
}

func updateAzureSubscription(payload AzurePayload) (interface{}, error) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		slog.Error("error getting database manager while updating azure subscription", "error", err)
		return nil, err
	}

	query := "select id, customer_identifier, marketplace, tenant_id from marketplace_customers where marketplace = 'azure' and customer_identifier = $1 and provider_account_id = $2"
	existingCustomer := Customer{}
	err = dbms.Db.QueryRowx(query, payload.SubscriptionID, payload.Subscription.Beneficiary.TenantID).StructScan(&existingCustomer)
	if err != nil {
		slog.Error("error querying customer data", "error", err)
		return nil, err
	}

	if existingCustomer.Action != payload.Action {
		slog.Info("Updating customer subscription action", "customer identifier", payload.SubscriptionID, "action", payload.Action)
		query = "update marketplace_customers set action = $1, subscription_status = $2 where marketplace = 'azure' and customer_identifier = $3 and provider_account_id = $4"
		_, err = dbms.Db.Exec(query, payload.Action, payload.Subscription.SaasSubscriptionStatus, payload.SubscriptionID, payload.Subscription.Beneficiary.TenantID)
		if err != nil {
			slog.Error("error updating customer subscription action", "error", err)
			return nil, err
		}
	}

	return nil, nil
}

func SendUsageEventToAzureForBilling(customer Customer) error {
	slog.Info("Sending metered billing to Azure for customer", "customer identifier", customer.CustomerIdentifier)

	var err error
	var usageBatch []AzureUsageEvent
	if customer.ProductCode == "enterprise" {
		usageBatch = []AzureUsageEvent{}
	} else {
		usageBatch, err = buildBatchUsageEventRequest(customer)
		if err != nil {
			slog.Error("error getting usage cost for tenant", "tenant id", customer.TenantID, "error", err)
			return err
		}
	}

	if len(usageBatch) == 0 {
		return nil
	}

	url := "https://marketplaceapi.microsoft.com/api/batchUsageEvent?api-version=2018-08-31"
	payload, err := common.MarshalJson(AzureUsageEventRequest{UsageEvents: usageBatch})
	if err != nil {
		slog.Error("error marshalling usage batch", "error", err)
		return err
	}

	accessToken, err := getAuthToken()
	if err != nil {
		slog.Error("error getting auth token for Azure API", "error", err)
		return err
	}
	resp, err := common.HttpPost(url,
		common.HttpWithHeaders(map[string]string{"Authorization": fmt.Sprintf("Bearer %s", accessToken)}),
		common.HttpWithJsonBody(payload),
	)
	if err != nil {
		slog.Error("error creating new request", "error", err)
		return err
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {
			slog.Error("error closing response body", "error", err)
		}
	}(resp.Body)

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		slog.Error("received non-OK response from Azure API", "status", resp.StatusCode, "body", string(body))
		return fmt.Errorf("received non-OK response from Azure API: %s", string(body))
	}

	slog.Info("Successfully sent usage batch to Azure API", "customer identifier", customer.CustomerIdentifier)
	return nil
}

func buildBatchUsageEventRequest(customer Customer) ([]AzureUsageEvent, error) {
	billingDate := time.Now().Add(-24 * time.Hour).Truncate(24 * time.Hour)
	usageCosts, err := billing.ListUsageCosts(customer.TenantID, &billingDate, nil)
	if err != nil {
		slog.Error("error getting usage cost for customer", "customer identifier", customer.CustomerIdentifier, "error", err)
		return nil, err
	}

	consumedUnitsByItem := make(map[string]int)
	for _, cost := range usageCosts {
		consumedUnitsByItem[cost.Name] += cost.Units
	}

	var usageBatch []AzureUsageEvent
	for name, units := range consumedUnitsByItem {
		usageBatch = append(usageBatch, AzureUsageEvent{
			ResourceID:         customer.CustomerIdentifier,
			Quantity:           float64(units),
			Dimension:          getCustomAzureDimension(name),
			PlanID:             customer.PricingTier,
			EffectiveStartTime: billingDate,
		})
	}
	return usageBatch, nil
}

func getCustomAzureDimension(key string) string {
	value := azureCustomPricingDimensionMap[key]
	return value
}

func getAuthToken() (string, error) {
	tokenEndpoint := "https://login.microsoftonline.com/common/oauth2/v2.0/token"

	resp, err := common.HttpPost(tokenEndpoint,
		common.HttpWithHeaders(map[string]string{
			"Authorization": fmt.Sprintf("Bearer %s", "Token"),
			"content-type":  "application/x-www-form-urlencoded",
		}),
		common.HttpWithFormUrlEncodedBody(map[string]any{
			"client_id":     config.Config.AwsSellerAccessKey,
			"client_secret": config.Config.AwsSellerSecretKey,
			"scope":         "20e940b3-4c77-4b0b-9a53-9e16a1b010a7/.default",
			"grant_type":    "client_credentials",
		}),
	)
	if err != nil {
		slog.Error("error creating new request", "error", err)
		return "", err
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {
			slog.Error("error closing response body", "error", err)
		}
	}(resp.Body)

	body, _ := io.ReadAll(resp.Body)
	var tokenResponse map[string]interface{}
	err = common.UnmarshalJson(body, &tokenResponse)
	if err != nil {
		return "", err
	}

	accessToken, ok := tokenResponse["access_token"].(string)
	if !ok {
		return "", fmt.Errorf("no access_token found in response")
	}

	return accessToken, nil
}
