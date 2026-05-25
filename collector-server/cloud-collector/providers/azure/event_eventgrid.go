package azure

import (
	"context"
	"encoding/json"
	"fmt"
	"nudgebee/collector/cloud/common"
	"nudgebee/collector/cloud/config"
	"nudgebee/collector/cloud/providers"
	"strings"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/messaging/azservicebus"
)

// EventGridEvent represents the structure of an Azure Event Grid event.
// For more details on the Event Grid event structure, see:
// https://learn.microsoft.com/en-us/azure/event-grid/event-schema
type EventGridEvent struct {
	ID              string          `json:"id"`
	EventType       string          `json:"eventType"`
	Subject         string          `json:"subject"`
	EventTime       time.Time       `json:"eventTime"`
	Data            json.RawMessage `json:"data"`
	DataVersion     string          `json:"dataVersion"`
	MetadataVersion string          `json:"metadataVersion"`
	Topic           string          `json:"topic"`
}

// CloudEvent represents the structure of a CloudEvents v1.0 event.
// Azure Event Grid supports both EventGridEvent and CloudEvent schemas.
// For more details, see: https://learn.microsoft.com/en-us/azure/event-grid/cloud-event-schema
type CloudEvent struct {
	SpecVersion     string          `json:"specversion"`
	Type            string          `json:"type"`
	Source          string          `json:"source"`
	ID              string          `json:"id"`
	Time            time.Time       `json:"time"`
	Subject         string          `json:"subject,omitempty"`
	DataContentType string          `json:"datacontenttype,omitempty"`
	Data            json.RawMessage `json:"data"`
}

// AccountMetadata stores additional metadata about an account that's not in providers.Account
type AccountMetadata struct {
	ID       string // UUID of the account in cloud_accounts table
	TenantID string // UUID of the Nudgebee tenant
}

// Global map to store account metadata by account number (temporary solution)
// Key: account_number (Azure tenant ID), Value: AccountMetadata
var (
	azureAccountMetadataCache      = make(map[string]AccountMetadata)
	azureAccountMetadataCacheMutex sync.RWMutex
)

// eventGridSourceToServiceName maps common Azure Event Grid sources to standardized service names.
// Azure uses resource provider namespaces (Microsoft.Compute, Microsoft.Storage, etc.)
var eventGridSourceToServiceNameMap = map[string]string{
	"microsoft.compute":                 "microsoft.compute/virtualmachines",
	"microsoft.storage":                 "microsoft.storage/storageaccounts",
	"microsoft.sql":                     "microsoft.sql/servers",
	"microsoft.web":                     "microsoft.web/sites",
	"microsoft.containerservice":        "microsoft.containerservice/managedclusters",
	"microsoft.keyvault":                "microsoft.keyvault/vaults",
	"microsoft.network":                 "microsoft.network/virtualnetworks",
	"microsoft.dbformysql":              "microsoft.dbformysql/flexibleservers",
	"microsoft.dbforpostgresql":         "microsoft.dbforpostgresql/flexibleservers",
	"microsoft.dbformariadb":            "microsoft.dbformariadb/servers",
	"microsoft.documentdb":              "microsoft.documentdb/databaseaccounts",
	"microsoft.cache":                   "microsoft.cache/redis",
	"microsoft.eventhub":                "microsoft.eventhub/namespaces",
	"microsoft.servicebus":              "microsoft.servicebus/namespaces",
	"microsoft.eventgrid":               "microsoft.eventgrid/topics",
	"microsoft.machinelearning":         "microsoft.machinelearning/workspaces",
	"microsoft.machinelearningservices": "microsoft.machinelearningservices/workspaces",
	"microsoft.cognitiveservices":       "microsoft.cognitiveservices/accounts",
	"microsoft.logic":                   "microsoft.logic/workflows",
	"microsoft.datafactory":             "microsoft.datafactory/factories",
	"microsoft.app":                     "microsoft.app/containerapps",
	"microsoft.botservice":              "microsoft.botservice/botservices",
	"microsoft.containerregistry":       "microsoft.containerregistry/registries",
	"microsoft.operationalinsights":     "microsoft.operationalinsights/workspaces",
	"microsoft.insights":                "microsoft.insights/metricalerts",
	"microsoft.authorization":           "microsoft.authorization/roleassignments",
	"microsoft.security":                "microsoft.security/pricings",
	"microsoft.securityinsights":        "microsoft.securityinsights",
	"microsoft.hybridcompute":           "microsoft.hybridcompute/machines",
	"microsoft.devops":                  "microsoft.devops/projects",
	"microsoft.cdn":                     "microsoft.cdn/profiles",
	// Add more mappings as required based on the sources you expect
}

// getServiceNameFromEventGridSource attempts to map an Event Grid source to a canonical service name.
// Azure Event Grid events typically use resource URIs as the source, so we extract the provider.
func getServiceNameFromEventGridSource(source string) string {
	// Source is typically a resource URI like:
	// /subscriptions/{sub}/resourceGroups/{rg}/providers/Microsoft.Compute/virtualMachines/{vm}
	parts := strings.Split(strings.ToLower(source), "/")

	// Find "providers" segment and get the namespace after it
	for i, part := range parts {
		if part == "providers" && i+1 < len(parts) {
			provider := parts[i+1]
			if serviceName, ok := eventGridSourceToServiceNameMap[provider]; ok {
				return serviceName
			}
			// Return the provider namespace if not in map
			return provider
		}
	}

	// For unmapped cases, try to extract from eventType
	// eventType format: Microsoft.Resources.ResourceWriteSuccess
	if strings.Contains(source, "microsoft.") {
		provider := strings.Split(source, "/")[0]
		if serviceName, ok := eventGridSourceToServiceNameMap[provider]; ok {
			return serviceName
		}
		return provider
	}

	// Return source as is for custom events
	return source
}

// parseAzureResourceID extracts subscription, resource group, provider, resource type, and resource name from an Azure Resource ID.
// Format: /subscriptions/{subscription-id}/resourceGroups/{resource-group}/providers/{provider}/{resourceType}/{resourceName}
func parseAzureResourceID(resourceID string) (subscription, resourceGroup, provider, resourceType, resourceName string) {
	parts := strings.Split(resourceID, "/")
	if len(parts) < 9 {
		return // Not a valid Azure resource ID
	}

	// Extract components
	for i := 0; i < len(parts); i++ {
		switch strings.ToLower(parts[i]) {
		case "subscriptions":
			if i+1 < len(parts) {
				subscription = parts[i+1]
			}
		case "resourcegroups":
			if i+1 < len(parts) {
				resourceGroup = parts[i+1]
			}
		case "providers":
			if i+1 < len(parts) {
				provider = parts[i+1]
				// Resource type is after provider
				if i+2 < len(parts) {
					resourceType = parts[i+2]
				}
				// Resource name is after resource type
				if i+3 < len(parts) {
					resourceName = parts[i+3]
				}
			}
		}
	}
	return
}

// EventGridEventProcessor defines an interface for processing parsed EventGrid events.
// You would implement this interface to handle the specific logic for different event types.
type EventGridEventProcessor interface {
	Process(ctx providers.CloudProviderContext, event EventGridEvent, account providers.Account) (providers.Event, error)
}

// ProcessEventGridEventFromBytes parses an EventGridEvent or CloudEvent from raw JSON bytes
// and processes it using the given processor. The nudgebeeAccountToken is provided externally
// (e.g., from query params in webhook, or from Service Bus message application properties).
// This function is the core processing logic shared by both the Service Bus consumer and the webhook endpoint.
func ProcessEventGridEventFromBytes(ctx providers.CloudProviderContext, body []byte, nudgebeeAccountToken string, processor EventGridEventProcessor, eventHandler providers.ProcessedEventHandler) (providers.Event, providers.Account, error) {
	// Try to unmarshal as EventGridEvent first (most common for Azure Event Grid)
	var event EventGridEvent
	if err := common.UnmarshalJson(body, &event); err == nil && event.ID != "" && event.EventType != "" {
		ctx.GetLogger().Debug("Parsed Event Grid event", "eventId", event.ID, "eventType", event.EventType)

		account, err := getAccountFromEventGridEvent(ctx, event, nudgebeeAccountToken, eventHandler)
		if err != nil {
			ctx.GetLogger().Error("Error getting account from Event Grid event", "error", err)
			return providers.Event{}, providers.Account{}, err
		}

		operationName := extractOperationName(event.Data)
		logEventProcessing(ctx, "Processing Event Grid message", event, operationName)

		providerEvent, err := processor.Process(ctx, event, account)
		if err == nil && providerEvent.EventId == "" {
			ctx.GetLogger().Warn("Event processor returned empty EventId (event will be skipped)",
				"eventType", event.EventType,
				"subject", event.Subject,
				"eventId", event.ID,
				"reason", "No matching processor rule for this event type")
		}
		return providerEvent, account, err
	}

	// Try to unmarshal as CloudEvent (alternative schema)
	var cloudEvent CloudEvent
	if err := common.UnmarshalJson(body, &cloudEvent); err == nil && cloudEvent.ID != "" && cloudEvent.Type != "" {
		event = EventGridEvent{
			ID:          cloudEvent.ID,
			EventType:   cloudEvent.Type,
			Subject:     cloudEvent.Subject,
			EventTime:   cloudEvent.Time,
			Data:        cloudEvent.Data,
			DataVersion: cloudEvent.SpecVersion,
			Topic:       cloudEvent.Source,
		}

		ctx.GetLogger().Debug("Parsed CloudEvent, converted to EventGrid format", "eventId", event.ID, "eventType", event.EventType)

		account, err := getAccountFromEventGridEvent(ctx, event, nudgebeeAccountToken, eventHandler)
		if err != nil {
			ctx.GetLogger().Error("Error getting account from CloudEvent", "error", err)
			return providers.Event{}, providers.Account{}, err
		}

		operationName := extractOperationName(event.Data)
		logEventProcessing(ctx, "Processing CloudEvent message", event, operationName)

		providerEvent, err := processor.Process(ctx, event, account)
		return providerEvent, account, err
	}

	return providers.Event{}, providers.Account{}, fmt.Errorf("failed to parse message as EventGridEvent or CloudEvent")
}

// processServiceBusMessageForEventGrid is a wrapper around ProcessEventGridEventFromBytes
// that extracts the nudgebeeAccountToken from the Service Bus message application properties.
func processServiceBusMessageForEventGrid(ctx providers.CloudProviderContext, message *azservicebus.ReceivedMessage, processor EventGridEventProcessor, eventHandler providers.ProcessedEventHandler) (providers.Event, providers.Account, error) {
	// Extract nudgebeeAccountToken from Service Bus message application properties
	var token string
	if message.ApplicationProperties != nil {
		if t, ok := message.ApplicationProperties["nudgebeeAccountToken"].(string); ok {
			token = t
		}
	}

	return ProcessEventGridEventFromBytes(ctx, message.Body, token, processor, eventHandler)
}

// extractOperationName extracts the operationName field from event data JSON.
func extractOperationName(data json.RawMessage) string {
	if data == nil {
		return ""
	}
	var eventData map[string]interface{}
	if err := common.UnmarshalJson(data, &eventData); err == nil {
		if op, ok := eventData["operationName"].(string); ok {
			return op
		}
	}
	return ""
}

// logEventProcessing logs event processing details at the appropriate level.
func logEventProcessing(ctx providers.CloudProviderContext, msg string, event EventGridEvent, operationName string) {
	if operationName != "" {
		ctx.GetLogger().Info(msg,
			"source", event.Subject,
			"eventType", event.EventType,
			"eventId", event.ID,
			"topic", event.Topic,
			"operationName", operationName)
	} else {
		ctx.GetLogger().Info(msg,
			"source", event.Subject,
			"eventType", event.EventType,
			"eventId", event.ID,
			"topic", event.Topic)
	}
}

// getAccountFromEventGridEvent extracts account info from Event Grid event using token-based lookup.
// The nudgebeeAccountToken is passed in directly (extracted by the caller from webhook query params
// or Service Bus message application properties).
// Falls back to legacy subscription ID lookup if no token is found (for backward compatibility).
func getAccountFromEventGridEvent(ctx providers.CloudProviderContext, event EventGridEvent, nudgebeeAccountToken string, eventHandler providers.ProcessedEventHandler) (providers.Account, error) {
	// Parse event data to extract Azure identifiers
	var data map[string]interface{}
	if err := common.UnmarshalJson(event.Data, &data); err != nil {
		ctx.GetLogger().Error("Failed to parse event data", "error", err)
		return providers.Account{}, err
	}

	// Use the token passed in by the caller; fall back to event data if empty
	externalId := nudgebeeAccountToken
	hasToken := externalId != ""
	if !hasToken {
		if t, ok := data["nudgebeeAccountToken"].(string); ok && t != "" {
			externalId = t
			hasToken = true
		}
	}

	// Extract Azure tenant ID from event data
	azureTenantId, hasTenantId := data["tenantId"].(string)

	// Extract subscription ID from event data or subject
	subscriptionId, _ := data["subscriptionId"].(string)
	if subscriptionId == "" {
		// Try to parse from subject: /subscriptions/{sub-id}/...
		subscription, _, _, _, _ := parseAzureResourceID(event.Subject)
		subscriptionId = subscription
	}

	if !hasToken || externalId == "" {
		// Fallback: Try Azure-specific subscription lookup
		ctx.GetLogger().Warn("No nudgebeeAccountToken in event, using Azure subscription lookup",
			"subscriptionId", subscriptionId,
			"tenantId", azureTenantId)

		if subscriptionId != "" {
			return getAzureAccountBySubscriptionId(ctx, subscriptionId, azureTenantId)
		}
		return providers.Account{}, fmt.Errorf("no nudgebeeAccountToken and no subscription ID found in event")
	}

	// Lookup by external_id (secure, tenant-aware)
	// Use Azure tenant ID if available, otherwise use subscription ID for backward compatibility
	accountNumber := azureTenantId
	if !hasTenantId || accountNumber == "" {
		accountNumber = subscriptionId
	}

	return getAzureAccountByExternalId(ctx, externalId, accountNumber)
}

// GetAzureAccountMetadata retrieves the account ID and tenant ID for a given Azure account number (tenant ID).
// This is used after account lookup to get metadata needed for resource updates.
func GetAzureAccountMetadata(accountNumber string) (accountId string, tenantId string, found bool) {
	azureAccountMetadataCacheMutex.RLock()
	defer azureAccountMetadataCacheMutex.RUnlock()

	metadata, ok := azureAccountMetadataCache[accountNumber]
	if !ok {
		return "", "", false
	}
	return metadata.ID, metadata.TenantID, true
}

// getAzureAccountByExternalId looks up cloud_account by external_id (token) and Azure account number (tenant ID or subscription ID).
// This provides tenant-safe account resolution even when multiple tenants use the same Azure subscription.
func getAzureAccountByExternalId(ctx providers.CloudProviderContext, externalId string, expectedAccountNumber string) (providers.Account, error) {
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return providers.Account{}, fmt.Errorf("failed to get database manager: %w", err)
	}

	query := `
		SELECT
			id,
			account_number,
			account_name,
			cloud_provider,
			tenant,
			region,
			assume_role,
			access_key,
			access_secret,
			status
		FROM cloud_accounts
		WHERE external_id = $1
		  AND status = 'active'
		  AND lower(cloud_provider) = 'azure'
		  AND account_number = $2
	`

	var accountRow struct {
		Id            string  `db:"id"`
		AccountNumber string  `db:"account_number"`
		AccountName   string  `db:"account_name"`
		CloudProvider string  `db:"cloud_provider"`
		Tenant        string  `db:"tenant"`
		Region        *string `db:"region"`
		AssumeRole    *string `db:"assume_role"` // Subscription ID(s)
		AccessKey     *string `db:"access_key"`  // Service Principal App ID
		AccessSecret  *string `db:"access_secret"`
		Status        string  `db:"status"`
	}

	err = dbms.QueryRowAndScan(&accountRow, query, externalId, expectedAccountNumber)
	if err != nil {
		return providers.Account{}, fmt.Errorf("account not found for azure account_number '%s': %w",
			expectedAccountNumber, err)
	}

	// Store account metadata (ID and TenantID) in cache for later use
	azureAccountMetadataCacheMutex.Lock()
	azureAccountMetadataCache[accountRow.AccountNumber] = AccountMetadata{
		ID:       accountRow.Id,
		TenantID: accountRow.Tenant,
	}
	azureAccountMetadataCacheMutex.Unlock()

	// Build providers.Account with the fields it actually has
	account := providers.Account{
		ID:            accountRow.Id,
		AccountNumber: accountRow.AccountNumber,
		AccountName:   accountRow.AccountName,
		CloudProvider: accountRow.CloudProvider,
		AssumeRole:    accountRow.AssumeRole,
		AccessKey:     accountRow.AccessKey,
		AccessSecret:  accountRow.AccessSecret,
		Region:        accountRow.Region,
	}

	return account, nil
}

// getAzureAccountBySubscriptionId looks up an Azure account by subscription ID.
// This handles the legacy case where events don't include nudgebeeAccountToken.
// It searches for accounts where:
// 1. The subscription ID is in assume_role (comma-separated list of subscriptions)
// 2. OR the subscription ID is the account_number itself (legacy data)
// 3. Optional: filters by tenant ID if provided
func getAzureAccountBySubscriptionId(ctx providers.CloudProviderContext, subscriptionId string, tenantId string) (providers.Account, error) {
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return providers.Account{}, fmt.Errorf("failed to get database manager: %w", err)
	}

	// Build query with optional tenant ID filter
	query := `
		SELECT
			id,
			account_number,
			account_name,
			cloud_provider,
			tenant,
			region,
			assume_role,
			access_key,
			access_secret,
			status
		FROM cloud_accounts
		WHERE lower(cloud_provider) = 'azure'
		  AND status = 'active'
		  AND (
			  assume_role LIKE '%' || $1 || '%'  -- Subscription ID in assume_role (comma-separated)
			  OR account_number = $1              -- Or subscription ID is account_number (legacy)
		  )
	`

	args := []interface{}{subscriptionId}

	// Add tenant ID filter if provided
	if tenantId != "" {
		query += " AND (account_number = $2 OR data::jsonb->>'tenantId' = $2)"
		args = append(args, tenantId)
	}

	// First check for multiple matches to prevent silent cross-tenant data leakage
	countQuery := "SELECT count(*) FROM cloud_accounts WHERE lower(cloud_provider) = 'azure' AND status = 'active' AND (assume_role LIKE '%' || $1 || '%' OR account_number = $1)"
	countArgs := []interface{}{subscriptionId}
	if tenantId != "" {
		countQuery += " AND (account_number = $2 OR data::jsonb->>'tenantId' = $2)"
		countArgs = append(countArgs, tenantId)
	}
	var count int
	if countErr := dbms.QueryRowAndScan(&count, countQuery, countArgs...); countErr == nil && count > 1 {
		ctx.GetLogger().Error("getAzureAccountBySubscriptionId: multiple active accounts found, cannot resolve without nudgebeeAccountToken",
			"count", count, "subscriptionId", subscriptionId, "tenantId", tenantId)
		return providers.Account{}, fmt.Errorf("multiple active Azure accounts (%d) found for subscription '%s' — configure nudgebeeAccountToken for tenant-safe routing", count, subscriptionId)
	}

	query += " LIMIT 1"

	var accountRow struct {
		Id            string  `db:"id"`
		AccountNumber string  `db:"account_number"`
		AccountName   string  `db:"account_name"`
		CloudProvider string  `db:"cloud_provider"`
		Tenant        string  `db:"tenant"`
		Region        *string `db:"region"`
		AssumeRole    *string `db:"assume_role"`
		AccessKey     *string `db:"access_key"`
		AccessSecret  *string `db:"access_secret"`
		Status        string  `db:"status"`
	}

	err = dbms.QueryRowAndScan(&accountRow, query, args...)
	if err != nil {
		ctx.GetLogger().Error("Azure account not found",
			"subscriptionId", subscriptionId,
			"tenantId", tenantId,
			"error", err)
		return providers.Account{}, fmt.Errorf("azure account not found for subscription ID '%s': %w", subscriptionId, err)
	}

	ctx.GetLogger().Info("Found Azure account by subscription ID",
		"subscriptionId", subscriptionId,
		"accountId", accountRow.Id,
		"accountNumber", accountRow.AccountNumber,
		"accountName", accountRow.AccountName)

	// Store account metadata in cache
	azureAccountMetadataCacheMutex.Lock()
	azureAccountMetadataCache[accountRow.AccountNumber] = AccountMetadata{
		ID:       accountRow.Id,
		TenantID: accountRow.Tenant,
	}
	azureAccountMetadataCacheMutex.Unlock()

	// Build providers.Account
	account := providers.Account{
		ID:            accountRow.Id,
		AccountNumber: accountRow.AccountNumber,
		AccountName:   accountRow.AccountName,
		CloudProvider: accountRow.CloudProvider,
		AssumeRole:    accountRow.AssumeRole,
		AccessKey:     accountRow.AccessKey,
		AccessSecret:  accountRow.AccessSecret,
		Region:        accountRow.Region,
	}

	return account, nil
}

// StartAzureServiceBusConsumer continuously polls a Service Bus queue (specified in config)
// for Event Grid events and processes them.
// This function is designed to run as a long-running goroutine.
func StartAzureServiceBusConsumer(pCtx providers.CloudProviderContext, eventHandler providers.ProcessedEventHandler) {
	defer func() {
		if r := recover(); r != nil {
			pCtx.GetLogger().Error("Service Bus consumer panicked", "error", r)
		}
	}()

	connectionString := config.Config.CloudCollectorAzureServiceBusConnectionString
	namespace := config.Config.CloudCollectorAzureServiceBusNamespace
	queueName := config.Config.CloudCollectorAzureServiceBusQueueName
	logger := pCtx.GetLogger()

	if connectionString == "" && namespace == "" {
		logger.Warn("azure: Service Bus connection string or namespace not configured. Consumer will not start.")
		return
	}

	if queueName == "" {
		queueName = "resource-events" // Default queue name
		logger.Info("azure: using default queue name", "queueName", queueName)
	}

	if eventHandler == nil {
		logger.Error("azure: ProcessedEventHandler is nil. Service Bus consumer cannot start as it needs a way to handle processed events.")
		return
	}

	logger.Info("azure: starting Event Grid Service Bus consumer", "queueName", queueName, "component", "ServiceBusConsumer")

	// Create Service Bus client
	var client *azservicebus.Client
	var err error

	if connectionString != "" {
		// Option 1: Connection String (simpler for development)
		client, err = azservicebus.NewClientFromConnectionString(connectionString, nil)
		if err != nil {
			logger.Error("azure: failed to create Service Bus client from connection string", "error", err)
			return
		}
		logger.Info("azure: created Service Bus client using connection string")
	} else {
		// Option 2: Managed Identity / Default Azure Credential (recommended for production)
		cred, err := azidentity.NewDefaultAzureCredential(nil)
		if err != nil {
			logger.Error("azure: failed to create default Azure credential", "error", err)
			return
		}
		client, err = azservicebus.NewClient(namespace, cred, nil)
		if err != nil {
			logger.Error("azure: failed to create Service Bus client with managed identity", "error", err)
			return
		}
		logger.Info("azure: created Service Bus client using managed identity", "namespace", namespace)
	}

	// Create receiver for queue
	receiver, err := client.NewReceiverForQueue(queueName, nil)
	if err != nil {
		logger.Error("azure: failed to create receiver for queue", "queueName", queueName, "error", err)
		return
	}

	// Get event rules
	// Priority: 1. Config value, 2. Environment variable, 3. Default locations
	rulesPath := config.Config.CloudCollectorAzureEventRulesPath
	rules, err := GetAzureEventRules(rulesPath)
	if err != nil {
		logger.Error("azure: failed to get event rules", "error", err, "component", "ServiceBusConsumer")
		return
	}
	logger.Info("azure: loaded event rules", "ruleCount", len(rules), "component", "ServiceBusConsumer")

	// Use the fully-initialized provider built in init(). A bare `&azureProvider{}`
	// has nil services/servicesMap, so ListResources would return ErrUnsupported
	// for every realtime resource lookup.
	processor := NewTemplatedEventGridProcessor(rules, defaultAzureProvider)

	for {
		select {
		case <-pCtx.GetContext().Done():
			logger.Info(
				"azure: Service Bus consumer shutting down.",
				"queueName", queueName,
				"component", "ServiceBusConsumer",
			)

			if err := receiver.Close(context.Background()); err != nil {
				logger.Error("failed to close Service Bus receiver", "error", err)
			}

			if err := client.Close(context.Background()); err != nil {
				logger.Error("failed to close Service Bus client", "error", err)
			}

			return

		default:
			// Receive up to 10 messages with 20 second timeout
			messages, err := receiver.ReceiveMessages(pCtx.GetContext(), 10, &azservicebus.ReceiveMessagesOptions{
				TimeAfterFirstMessage: 20 * time.Second,
			})

			if err != nil {
				// Check if the error is due to context cancellation
				if pCtx.GetContext().Err() == context.Canceled || pCtx.GetContext().Err() == context.DeadlineExceeded {
					logger.Info("azure: Service Bus consumer context cancelled or deadline exceeded during ReceiveMessages.", "queueName", queueName, "error", err, "component", "ServiceBusConsumer")
					return // Exit if context is done
				}
				logger.Error("azure: error receiving messages from Service Bus", "queueName", queueName, "error", err, "component", "ServiceBusConsumer")
				time.Sleep(5 * time.Second) // Wait before retrying
				continue
			}

			if len(messages) == 0 {
				logger.Debug("azure: no messages received from Service Bus, continuing to poll.", "queueName", queueName, "component", "ServiceBusConsumer")
				continue
			}

			logger.Info("azure: received messages from Service Bus", "count", len(messages), "queueName", queueName, "component", "ServiceBusConsumer")

			for _, msg := range messages {
				if len(msg.Body) == 0 {
					logger.Warn("azure: received Service Bus message with nil/empty body", "messageId", msg.MessageID, "queueName", queueName, "component", "ServiceBusConsumer")
					if err := receiver.CompleteMessage(pCtx.GetContext(), msg, nil); err != nil {
						logger.Error("azure: failed to complete Service Bus message with nil/empty body", "messageId", msg.MessageID, "error", err, "component", "ServiceBusConsumer")
					}
					continue
				}

				processedEvent, originatingAccount, err := processServiceBusMessageForEventGrid(pCtx, msg, processor, eventHandler)
				if err != nil {
					logger.Error("azure: failed to process Service Bus message for Event Grid event", "messageId", msg.MessageID, "error", err, "queueName", queueName, "component", "ServiceBusConsumer")

					// Dead letter the message after max delivery attempts
					if msg.DeliveryCount >= 3 {
						errMsg := err.Error()
						deadLetterOptions := &azservicebus.DeadLetterOptions{
							ErrorDescription: &errMsg,
							Reason:           stringPtr("ProcessingFailed"),
						}
						if dlErr := receiver.DeadLetterMessage(pCtx.GetContext(), msg, deadLetterOptions); dlErr != nil {
							logger.Error("azure: failed to dead letter message", "messageId", msg.MessageID, "error", dlErr, "component", "ServiceBusConsumer")
						} else {
							logger.Info("azure: dead lettered message after max delivery attempts", "messageId", msg.MessageID, "deliveryCount", msg.DeliveryCount, "component", "ServiceBusConsumer")
						}
					} else {
						// Abandon to retry
						if abandonErr := receiver.AbandonMessage(pCtx.GetContext(), msg, nil); abandonErr != nil {
							logger.Error("azure: failed to abandon message", "messageId", msg.MessageID, "error", abandonErr, "component", "ServiceBusConsumer")
						} else {
							logger.Info("azure: abandoned message for retry", "messageId", msg.MessageID, "deliveryCount", msg.DeliveryCount, "component", "ServiceBusConsumer")
						}
					}
					continue
				}

				if processedEvent.EventId == "" {
					// Event was intentionally skipped by the processor (no matching rule)
					logger.Info("azure: Event Grid event skipped by processor", "serviceBusMessageId", msg.MessageID, "component", "ServiceBusConsumer")
				} else {
					logger.Info("azure: successfully processed Event Grid event from Service Bus message",
						"processedEventId", processedEvent.EventId, "serviceBusMessageId", msg.MessageID, "eventName", processedEvent.EventName, "component", "ServiceBusConsumer")

					if err := eventHandler.ProcessEvent(pCtx, processedEvent, originatingAccount); err != nil {
						logger.Error("azure: failed to handle processed event", "error", err, "processedEventId", processedEvent.EventId, "serviceBusMessageId", msg.MessageID, "component", "ServiceBusConsumer")
						// Decide if message should be deleted or redriven. For now, we continue to complete.
					}
				}

				// Complete (delete) message
				if err := receiver.CompleteMessage(pCtx.GetContext(), msg, nil); err != nil {
					logger.Error("azure: failed to complete Service Bus message after processing", "messageId", msg.MessageID, "error", err, "component", "ServiceBusConsumer")
				} else {
					logger.Debug("azure: successfully completed Service Bus message", "messageId", msg.MessageID, "component", "ServiceBusConsumer")
				}
			}
		}
	}
}

// stringPtr is a helper function to get a pointer to a string
func stringPtr(s string) *string {
	return &s
}
