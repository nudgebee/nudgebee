package marketplace

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"nudgebee/services/billing"
	"nudgebee/services/common"
	"nudgebee/services/config"
	"nudgebee/services/internal/database"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	entitlementclient "github.com/aws/aws-sdk-go-v2/service/marketplaceentitlementservice"
	entitlementtypes "github.com/aws/aws-sdk-go-v2/service/marketplaceentitlementservice/types"
	marketplacemeteringservice "github.com/aws/aws-sdk-go-v2/service/marketplacemetering"
	marketplacemeteringservicetypes "github.com/aws/aws-sdk-go-v2/service/marketplacemetering/types"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

var awsSubscriptionStatusMap = map[string]string{
	"subscribe-success":   "subscribed",
	"subscribe-fail":      "unsubscribed",
	"unsubscribe-pending": "unsubscribed",
	"unsubscribe-success": "unsubscribed",
}

var awsCustomPricingDimensionMap = map[string]string{
	"active_clusters":    "additional_cluster",
	"active_nodes":       "additional_nodes",
	"auto_runbook_runs":  "automation_runs",
	"auto_optimize_runs": "automation_runs",
}

func GetPurchaseDetailsAndUpdateEntitlements(message *sqstypes.Message) error {
	if message == nil {
		slog.Error("received nil sqs message for purchase details change")
		return nil
	}

	var snsMessage SNSMessage
	err := common.UnmarshalJson([]byte(*message.Body), &snsMessage)
	if err != nil {
		slog.Error("error unmarshalling SNS message", "error", err)
		return err
	}

	var payload AwsEntitlementPayload

	err = common.UnmarshalJson([]byte(snsMessage.Message), &payload)
	if err != nil {
		slog.Error("error unmarshalling SQS message", "error", err)
		return err
	}
	if payload.Action == "entitlement-updated" {
		slog.Info("Updating entitlement for customer", "customer identifier", payload.CustomerIdentifier, "entitlement ", payload)
		err = updateEntitlementForCustomer(payload)
		if err != nil {
			slog.Error("error updating AWS marketplace entitlements for customer ", "customer", payload.CustomerIdentifier, "error", err)
			return err
		}
	} else {
		slog.Warn("received unsupported action", "action", payload.Action)
	}
	return nil
}

func updateEntitlementForCustomer(payload AwsEntitlementPayload) error {
	creds := credentials.NewStaticCredentialsProvider(config.Config.AwsSellerAccessKey, config.Config.AwsSellerSecretKey, "")
	cfg, err := awsconfig.LoadDefaultConfig(context.TODO(), awsconfig.WithRegion("us-east-1"), awsconfig.WithCredentialsProvider(creds))
	if err != nil {
		slog.Error("Error getting aws config:", "error", err)
		return err
	}
	entitlementClient := entitlementclient.NewFromConfig(cfg)

	request := &entitlementclient.GetEntitlementsInput{
		ProductCode: aws.String(payload.ProductCode),
		Filter: map[string][]string{
			"CUSTOMER_IDENTIFIER": {payload.CustomerIdentifier},
		},
	}

	response, err := entitlementClient.GetEntitlements(context.TODO(), request)
	slog.Info("Entitlement update response for customer", "customer identifier", payload.CustomerIdentifier, "entitlement ", payload)
	if err != nil {
		slog.Error("error getting entitlements", "error", err)
		return err
	}
	newEntitlements := response.Entitlements
	slog.Info("Subscription updated for customer account", "customer identifier", payload.CustomerIdentifier, "entitlements ", newEntitlements)

	subscription := CustomerSubscription{
		CustomerIdentifier: payload.CustomerIdentifier,
		ProductCode:        payload.ProductCode,
	}
	_, err = UpdateCustomerTierAndEntitlements(subscription, newEntitlements)
	if err != nil {
		slog.Error("error updating customer subscription", "error", err)
	} else {
		slog.Info("customer subscription updated successfully", "subscription", subscription)
	}
	return nil
}

func UpdateCustomerTierAndEntitlements(subscription CustomerSubscription, entitlements []entitlementtypes.Entitlement) (interface{}, error) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		slog.Error("error getting database manager while updating marketplace customer subscription", "error", err)
		return nil, err
	}

	query := "select id, customer_identifier, tenant_id from marketplace_customers where customer_identifier = $1 and product_code = $2"

	existingCustomer := CustomerTenant{}
	err = dbms.Db.QueryRowx(query, subscription.CustomerIdentifier, subscription.ProductCode).StructScan(&existingCustomer)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		slog.Error("error querying customer data", "error", err)
		return nil, err
	}

	if existingCustomer.CustomerIdentifier == "" {
		return nil, errors.New("customer not found")
	}

	entitlementJson, err := common.MarshalJson(entitlements)
	if err != nil {
		slog.Error("error marshalling entitlements", "error", err)
		return nil, err
	}
	if len(entitlements) == 0 {
		slog.Error("no entitlements found for customer", "customer", subscription.CustomerIdentifier)
		return nil, nil
	}
	pricingTier := *entitlements[0].Dimension
	expiry := *entitlements[0].ExpirationDate
	updateQuery := `UPDATE marketplace_customers SET entitlement_details = $1, pricing_tier = $2, subscription_expiry = $3 WHERE customer_identifier = $4 and product_code = $5`
	_, err = dbms.Db.Exec(updateQuery, entitlementJson, pricingTier, expiry.Format(time.RFC3339), subscription.CustomerIdentifier, subscription.ProductCode)
	if err != nil {
		slog.Error("error updating customer subscription", "error", err)
		return nil, err
	}
	return existingCustomer, nil
}

func UpdateSubscriptionActions(message *sqstypes.Message) error {

	if message == nil {

		slog.Error("received nil sqs message for customer billing change")

		return nil

	}

	var snsMessage SNSMessage

	err := common.UnmarshalJson([]byte(*message.Body), &snsMessage)

	if err != nil {

		slog.Error("error unmarshalling SNS message", "error", err)

		return err

	}

	var payload AwsSubscriptionPayload

	err = common.UnmarshalJson([]byte(*message.Body), &payload)

	if err != nil {

		slog.Error("error unmarshalling SQS message", "error", err)

		return err

	}

	slog.Info("Subscription updated for customer", "customer identifier", payload.CustomerIdentifier, "subscription ", payload)

	err = UpdateCustomerSubscriptionDimension(payload)

	if err != nil {

		slog.Error("error updating customer subscription", "error", err)

	} else {

		slog.Info("customer subscription updated successfully", "subscription", payload)

	}

	return nil

}

func UpdateCustomerSubscriptionDimension(subscription AwsSubscriptionPayload) error {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		slog.Error("error getting database manager while updating marketplace customer subscription", "error", err)
		return err
	}

	query := "select id, customer_identifier, tenant_id from marketplace_customers where customer_identifier = $1 and product_code = $2"

	existingCustomer := CustomerTenant{}
	err = dbms.Db.QueryRowx(query, subscription.CustomerIdentifier, subscription.ProductCode).StructScan(&existingCustomer)
	if err != nil && errors.Is(err, sql.ErrNoRows) {
		slog.Error("error querying customer data", "error", err)
		return err
	}

	if existingCustomer.CustomerIdentifier == "" {
		return errors.New("customer not found")
	}

	action := subscription.Action
	subscriptionStatus := getSubscriptionStatus(subscription.Action)
	updateQuery := `UPDATE marketplace_customers SET action = $1, subscription_status = $2 WHERE customer_identifier = $4 and product_code = $5`
	_, err = dbms.Db.Exec(updateQuery, action, subscriptionStatus, subscription.CustomerIdentifier, subscription.ProductCode)
	if err != nil {
		slog.Error("error updating customer subscription", "error", err)
		return err
	}
	return nil
}

func getSubscriptionStatus(action string) string {
	value := awsSubscriptionStatusMap[action]
	return value
}

func SendUsageEventToAwsForBilling(customer Customer) error {
	creds := credentials.NewStaticCredentialsProvider(config.Config.AwsSellerAccessKey, config.Config.AwsSellerSecretKey, "")
	cfg, err := awsconfig.LoadDefaultConfig(context.TODO(), awsconfig.WithRegion("us-east-1"), awsconfig.WithCredentialsProvider(creds))
	if err != nil {
		slog.Error("Error getting aws config:", "error", err)
		return err
	}
	marketplaceClient := marketplacemeteringservice.NewFromConfig(cfg)
	request := buildUsageCostRequest(customer)
	if request == nil {
		slog.Info("No usage costs found for customer", "customer", customer.CustomerIdentifier)
		return nil
	}
	response, err := marketplaceClient.BatchMeterUsage(context.TODO(), request)
	if err != nil {
		slog.Error("Error in batch meter usage:", "error", err)
	} else {
		slog.Info("Batch for metered usage, ", "Response:", fmt.Sprintf("%+v", response))
	}
	return nil
}

func buildUsageCostRequest(customer Customer) *marketplacemeteringservice.BatchMeterUsageInput {
	billingDate := time.Now().Add(-24 * time.Hour).Truncate(24 * time.Hour)
	usageCosts, err := billing.ListUsageCosts(customer.TenantID, &billingDate, nil)
	if err != nil {
		slog.Error("Error listing usage costs:", "error", err)
		return nil
	}

	consumedUnitsByItem := make(map[string]int)
	for _, cost := range usageCosts {
		consumedUnitsByItem[cost.Name] += cost.Units
	}

	var records []marketplacemeteringservicetypes.UsageRecord
	for name, units := range consumedUnitsByItem {
		remainingUnits := int64(units)
		for remainingUnits > 0 {
			quantity := remainingUnits
			if quantity > math.MaxInt32 {
				quantity = math.MaxInt32
			}
			records = append(records, marketplacemeteringservicetypes.UsageRecord{
				CustomerIdentifier: aws.String(customer.CustomerIdentifier),
				Dimension:          aws.String(getAwsCustomPricingDimension(name)),
				Timestamp:          aws.Time(billingDate),
				Quantity:           aws.Int32(int32(quantity)),
			})
			remainingUnits -= quantity
		}
	}

	if len(records) == 0 {
		return nil
	}

	return &marketplacemeteringservice.BatchMeterUsageInput{
		ProductCode:  aws.String(customer.ProductCode),
		UsageRecords: records,
	}
}

func getAwsCustomPricingDimension(action string) string {
	value := awsCustomPricingDimensionMap[action]
	return value
}

// Valid AWS metered dimensions for test usage
var validAwsMeteredDimensions = map[string]bool{
	"ai_troubleshoot":           true,
	"ai_workflow":               true,
	"ai_workflow_function_call": true,
}

func SendTestMeteredUsageToAws(req TestMeteredUsageRequest) error {
	if !validAwsMeteredDimensions[req.Dimension] {
		return fmt.Errorf("invalid dimension: %s. Valid dimensions: ai_troubleshoot, ai_workflow, ai_workflow_function_call", req.Dimension)
	}

	creds := credentials.NewStaticCredentialsProvider(config.Config.AwsSellerAccessKey, config.Config.AwsSellerSecretKey, "")
	cfg, err := awsconfig.LoadDefaultConfig(context.TODO(), awsconfig.WithRegion("us-east-1"), awsconfig.WithCredentialsProvider(creds))
	if err != nil {
		slog.Error("Error getting aws config:", "error", err)
		return err
	}

	marketplaceClient := marketplacemeteringservice.NewFromConfig(cfg)

	// AWS requires timestamp to be within the past hour
	timestamp := time.Now().Add(-1 * time.Minute).Truncate(time.Hour)

	request := &marketplacemeteringservice.BatchMeterUsageInput{
		ProductCode: aws.String(req.ProductCode),
		UsageRecords: []marketplacemeteringservicetypes.UsageRecord{
			{
				CustomerIdentifier: aws.String(req.CustomerIdentifier),
				Dimension:          aws.String(req.Dimension),
				Timestamp:          aws.Time(timestamp),
				Quantity:           aws.Int32(req.Quantity),
			},
		},
	}

	response, err := marketplaceClient.BatchMeterUsage(context.TODO(), request)
	if err != nil {
		slog.Error("Error sending test metered usage to AWS:", "error", err)
		return err
	}

	slog.Info("Test metered usage sent to AWS", "response", fmt.Sprintf("%+v", response), "request", req)
	return nil
}
