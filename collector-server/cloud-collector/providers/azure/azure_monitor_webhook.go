package azure

import (
	"encoding/json"
	"errors"
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/alertsmanagement/armalertsmanagement"
	"github.com/google/uuid"
)

// Azure Monitor pushes a fired or resolved alert to an action group webhook in the
// Common Alert Schema (https://learn.microsoft.com/azure/azure-monitor/alerts/alerts-common-schema),
// not the Alerts Management shape polling reads. ConvertMonitorWebhookAlert turns it
// into the Event polling stores for the same firing, so the two arrivals of one
// alert are one row: the same finding_id, fingerprint, subject and title.
//
// api-server used to name webhook alerts itself — fingerprint = the per-firing
// alertId, subject = the target's short name — and every alert on an account that
// was also polled was stored twice, about 2.5 minutes apart. Keep identity logic in
// convertAzureAlertToEvent only.

type monitorWebhookPayload struct {
	SchemaID string `json:"schemaId"`
	Data     struct {
		Essentials monitorWebhookEssentials `json:"essentials"`
	} `json:"data"`
}

type monitorWebhookEssentials struct {
	AlertID             string   `json:"alertId"`
	AlertRule           string   `json:"alertRule"` // the rule's name, not its resource id
	AlertRuleID         string   `json:"alertRuleID"`
	Severity            string   `json:"severity"`
	SignalType          string   `json:"signalType"`
	MonitorCondition    string   `json:"monitorCondition"`
	MonitoringService   string   `json:"monitoringService"`
	AlertTargetIDs      []string `json:"alertTargetIDs"`
	FiredDateTime       string   `json:"firedDateTime"`
	Description         string   `json:"description"`
	TargetResourceType  string   `json:"targetResourceType"`
	TargetResourceGroup string   `json:"targetResourceGroup"`
}

// errNoAlertReadAccess means the account has no credentials to read alerts with.
var errNoAlertReadAccess = errors.New("account has no credentials to read alerts")

// ConvertMonitorWebhookAlert converts an Azure Monitor webhook body into the Event
// polling would produce for the same firing.
func ConvertMonitorWebhookAlert(ctx providers.CloudProviderContext, account providers.Account, body []byte) (providers.Event, error) {
	var payload monitorWebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return providers.Event{}, fmt.Errorf("invalid Azure Monitor webhook payload: %w", err)
	}
	if payload.SchemaID != "azureMonitorCommonAlertSchema" {
		return providers.Event{}, fmt.Errorf("invalid Azure Monitor webhook payload: unsupported schema %q", payload.SchemaID)
	}
	ess := payload.Data.Essentials
	if strings.TrimSpace(ess.AlertID) == "" {
		return providers.Event{}, fmt.Errorf("invalid Azure Monitor webhook payload: alertId is missing")
	}
	subID := parseAzureResourceIDSegments(ess.AlertID)["subscriptions"]

	// Polling builds the fingerprint from the alert rule's resource id and the target
	// as the Alerts Management API spells them. The webhook carries the rule only by
	// name (alertRuleID is optional) and spells ids with its own casing, so read the
	// firing from that API when the account's credentials allow it.
	alert, err := fetchAzureAlert(ctx, account, subID, ess.AlertID)
	if err != nil {
		// A webhook-only account has no credentials to read with, and the payload is
		// its expected source; only a lookup that could have worked is worth a warning.
		if errors.Is(err, errNoAlertReadAccess) {
			ctx.GetLogger().Debug("azure webhook: account cannot read alerts, naming it from the payload",
				"error", err, "alertId", ess.AlertID)
		} else {
			ctx.GetLogger().Warn("azure webhook: alert lookup failed, naming it from the payload",
				"error", err, "alertId", ess.AlertID)
		}
		alert = webhookEssentialsToAlert(ess)
	}

	event := convertAzureAlertToEvent(subID, alert, alert.Properties.Essentials)
	// The delivery says whether this is the firing or its resolution; the API's
	// current state may already be past it.
	event.EventStatus = providers.EventStatusFiring
	if strings.EqualFold(ess.MonitorCondition, string(armalertsmanagement.MonitorConditionResolved)) {
		event.EventStatus = providers.EventStatusResolved
	}
	return event, nil
}

// fetchAzureAlert reads one firing from the Alerts Management API by its guid,
// the same call polling makes when the list omits a firing's rule or target.
func fetchAzureAlert(ctx providers.CloudProviderContext, account providers.Account, subID, alertID string) (*armalertsmanagement.Alert, error) {
	guid := alertID[strings.LastIndex(alertID, "/")+1:]
	if _, err := uuid.Parse(guid); err != nil {
		return nil, fmt.Errorf("alert id %q does not end in a guid", alertID)
	}
	if subID == "" {
		return nil, fmt.Errorf("alert id %q names no subscription", alertID)
	}
	cred, _, err := getAzureCredsForAccount(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errNoAlertReadAccess, err)
	}
	client, err := armalertsmanagement.NewAlertsClient("subscriptions/"+subID, cred, getAzureAuditOpts(ctx))
	if err != nil {
		return nil, err
	}
	resp, err := client.GetByID(ctx.GetContext(), guid, nil)
	if err != nil {
		return nil, err
	}
	if resp.ID == nil || resp.Name == nil || resp.Properties == nil || resp.Properties.Essentials == nil {
		return nil, fmt.Errorf("alert %s came back without essentials", guid)
	}
	return &resp.Alert, nil
}

// webhookEssentialsToAlert maps the payload onto the Alerts Management fields
// convertAzureAlertToEvent reads. It is the fallback for accounts whose
// credentials cannot read alerts: the finding_id still matches polling, but the
// fingerprint does only when the payload carries alertRuleID in polling's casing.
func webhookEssentialsToAlert(ess monitorWebhookEssentials) *armalertsmanagement.Alert {
	essentials := &armalertsmanagement.Essentials{
		Severity:         to.Ptr(armalertsmanagement.Severity(ess.Severity)),
		MonitorCondition: to.Ptr(armalertsmanagement.MonitorCondition(ess.MonitorCondition)),
		SignalType:       to.Ptr(armalertsmanagement.SignalType(ess.SignalType)),
		MonitorService:   to.Ptr(armalertsmanagement.MonitorService(ess.MonitoringService)),
	}
	if ess.AlertRuleID != "" {
		essentials.AlertRule = to.Ptr(ess.AlertRuleID)
	}
	if len(ess.AlertTargetIDs) > 0 {
		essentials.TargetResource = to.Ptr(ess.AlertTargetIDs[0])
	}
	if ess.TargetResourceType != "" {
		essentials.TargetResourceType = to.Ptr(ess.TargetResourceType)
	}
	resourceGroup := ess.TargetResourceGroup
	if resourceGroup == "" && len(ess.AlertTargetIDs) > 0 {
		resourceGroup = parseAzureResourceIDSegments(ess.AlertTargetIDs[0])["resourcegroups"]
	}
	if resourceGroup != "" {
		essentials.TargetResourceGroup = to.Ptr(resourceGroup)
	}
	if ess.Description != "" {
		essentials.Description = to.Ptr(ess.Description)
	}
	if fired, err := time.Parse(time.RFC3339, ess.FiredDateTime); err == nil {
		essentials.StartDateTime = to.Ptr(fired.UTC())
	}
	return &armalertsmanagement.Alert{
		ID:         to.Ptr(ess.AlertID),
		Name:       to.Ptr(ess.AlertRule),
		Properties: &armalertsmanagement.AlertProperties{Essentials: essentials},
	}
}
