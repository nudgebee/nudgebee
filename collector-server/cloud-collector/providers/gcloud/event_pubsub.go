package gcloud

import (
	"context"
	"encoding/json"
	"fmt"
	"nudgebee/collector/cloud/common"
	"nudgebee/collector/cloud/providers"
	"time"

	"cloud.google.com/go/pubsub/v2"
)

// PubSubEvent represents a GCP Pub/Sub message containing cloud events
type PubSubEvent struct {
	// CloudEvents format fields
	ID              string          `json:"id"`
	Source          string          `json:"source"`
	SpecVersion     string          `json:"specversion"`
	Type            string          `json:"type"`
	DataContentType string          `json:"datacontenttype,omitempty"`
	Time            time.Time       `json:"time"`
	Subject         string          `json:"subject,omitempty"`
	Data            json.RawMessage `json:"data"`

	// GCP-specific fields
	ProjectID string       `json:"projectId,omitempty"`
	LogName   string       `json:"logName,omitempty"`
	Resource  *LogResource `json:"resource,omitempty"`
}

// LogResource represents GCP resource metadata in audit logs
type LogResource struct {
	Type   string            `json:"type"`
	Labels map[string]string `json:"labels,omitempty"`
}

// PubSubEventProcessor interface for processing Pub/Sub events
type PubSubEventProcessor interface {
	Process(ctx providers.CloudProviderContext, event PubSubEvent, account providers.Account) (providers.Event, error)
}

// parsePubSubMessage parses a Pub/Sub message into PubSubEvent
func parsePubSubMessage(data []byte) (PubSubEvent, error) {
	var event PubSubEvent
	if err := common.UnmarshalJson(data, &event); err != nil {
		return PubSubEvent{}, err
	}
	return event, nil
}

// getAccountFromPubSubEvent extracts account info using external_id token (preferred) or project ID fallback
func getAccountFromPubSubEvent(ctx providers.CloudProviderContext, event PubSubEvent, msgAttributes map[string]string, eventHandler providers.ProcessedEventHandler) (providers.Account, error) {
	logger := ctx.GetLogger()

	// Try to get account from nudgebeeAccountToken (external_id) first - this is the preferred method
	if accountToken, ok := msgAttributes["nudgebeeAccountToken"]; ok && accountToken != "" {
		logger.Debug("Extracting account from nudgebeeAccountToken attribute", "token", fmt.Sprintf("%.8s...", accountToken))
		account, err := eventHandler.GetAccountFromExternalId(ctx, accountToken, event.ProjectID)
		if err == nil {
			logger.Info("Successfully resolved account from external_id token", "account_id", account.AccountNumber, "project_id", event.ProjectID)
			return account, nil
		}
		logger.Warn("Failed to resolve account from external_id, falling back to project ID lookup", "error", err, "token", fmt.Sprintf("%.8s...", accountToken))

	}

	// Fallback: Try project ID lookup (legacy method for backward compatibility)
	logger.Debug("Looking up account by project ID", "project_id", event.ProjectID)
	account, err := eventHandler.GetAccountFromCloudProviderAccountId(ctx, event.ProjectID)
	if err != nil {
		return providers.Account{}, fmt.Errorf("account not found for project %s: %w", event.ProjectID, err)
	}

	return account, nil
}

// StartPubSubConsumer starts consuming messages from GCP Pub/Sub
func StartPubSubConsumer(ctx providers.CloudProviderContext, projectID, subscriptionID string, processor PubSubEventProcessor, eventHandler providers.ProcessedEventHandler) {
	logger := ctx.GetLogger()

	if subscriptionID == "" {
		logger.Warn("GCP Pub/Sub subscription not configured, skipping consumer startup")
		return
	}

	if projectID == "" {
		logger.Error("GCP project ID not configured for Pub/Sub consumer")
		return
	}

	logger.Info("Starting GCP Pub/Sub consumer", "projectId", projectID, "subscription", subscriptionID)

	go func() {
		for {
			if ctx.GetContext().Err() != nil {
				logger.Info("Pub/Sub consumer stopping due to context cancellation")
				return
			}
			if err := consumePubSubMessages(ctx, projectID, subscriptionID, processor, eventHandler); err != nil {
				logger.Error("Error in Pub/Sub consumer, retrying in 30s", "error", err)
				time.Sleep(30 * time.Second)
			}
		}
	}()
}

// consumePubSubMessages processes messages from Pub/Sub subscription
func consumePubSubMessages(ctx providers.CloudProviderContext, projectID, subscriptionID string, processor PubSubEventProcessor, eventHandler providers.ProcessedEventHandler) error {
	logger := ctx.GetLogger()

	client, err := pubsub.NewClient(ctx.GetContext(), projectID)
	if err != nil {
		return fmt.Errorf("failed to create Pub/Sub client: %w", err)
	}
	defer func() { _ = client.Close() }()

	// Create subscriber with v2 API
	subscriber := client.Subscriber(subscriptionID)

	// Configure receive settings
	subscriber.ReceiveSettings.MaxOutstandingMessages = 10
	subscriber.ReceiveSettings.NumGoroutines = 1

	logger.Info("Pub/Sub consumer started receiving messages")

	// Use v2 Receive API
	err = subscriber.Receive(ctx.GetContext(), func(_ context.Context, msg *pubsub.Message) {
		if err := processPubSubMessage(ctx, msg, processor, eventHandler); err != nil {
			logger.Error("Failed to process Pub/Sub message", "error", err, "messageId", msg.ID)
			msg.Nack()
		} else {
			msg.Ack()
		}
	})

	if err != nil && err != context.Canceled {
		return fmt.Errorf("Pub/Sub receive error: %w", err)
	}

	return nil
}

// processPubSubMessage processes a single Pub/Sub message
func processPubSubMessage(ctx providers.CloudProviderContext, msg *pubsub.Message, processor PubSubEventProcessor, eventHandler providers.ProcessedEventHandler) error {
	logger := ctx.GetLogger()

	// Get message data using v2 API (Data is a field, not a method)
	event, err := parsePubSubMessage(msg.Data)
	if err != nil {
		logger.Error("Failed to parse Pub/Sub event", "error", err)
		return err
	}

	// Set project ID from message attributes if not in payload (Attributes is a field, not a method)
	if event.ProjectID == "" {
		if projectID, ok := msg.Attributes["projectId"]; ok {
			event.ProjectID = projectID
		}
	}

	// Pass message attributes to account lookup (contains nudgebeeAccountToken)
	account, err := getAccountFromPubSubEvent(ctx, event, msg.Attributes, eventHandler)
	if err != nil {
		logger.Error("Failed to get account from event", "error", err)
		return err
	}

	providerEvent, err := processor.Process(ctx, event, account)
	if err != nil {
		logger.Error("Failed to process event", "error", err, "eventId", event.ID)
		return err
	}

	// Skip unmatched events (no rule found)
	if providerEvent.Title == "" {
		logger.Debug("Skipping unmatched event", "eventId", event.ID, "type", event.Type)
		return nil
	}

	if err := eventHandler.ProcessEvent(ctx, providerEvent, account); err != nil {
		logger.Error("Failed to handle processed event", "error", err)
		return err
	}

	logger.Debug("Successfully processed Pub/Sub event", "eventId", event.ID, "type", event.Type)
	return nil
}
