package azure

import (
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
)

// azureAlertEventSource is the source an Azure Monitor alert is stored under.
const azureAlertEventSource = "Azure_Monitor_Alert"

// azureAlertFingerprintLike matches the fingerprints azureAlertRuleIdentity can
// read; it narrows the read, the identity decides.
const azureAlertFingerprintLike = "/subscriptions/%"

// azureAlertRuleIdentity reads a stored Azure alert fingerprint
// ({alert rule id}:{target resource}) as the rule it fired from and that rule's
// subscription, both lowercased: ARM ids are case-insensitive and the same id
// arrives in more than one casing. Only metric alert and log search alert rules
// are read; other fingerprints (activity log alerts, a firing's own id) name
// nothing a rule listing can speak for.
func azureAlertRuleIdentity(fingerprint string) (subscription, rule string, ok bool) {
	rule = strings.ToLower(strings.SplitN(fingerprint, ":", 2)[0])
	if !strings.Contains(rule, "/providers/microsoft.insights/metricalerts/") &&
		!strings.Contains(rule, "/providers/microsoft.insights/scheduledqueryrules/") {
		return "", "", false
	}
	subscription = parseAzureResourceIDSegments(rule)["subscriptions"]
	if subscription == "" {
		return "", "", false
	}
	return subscription, rule, true
}

// metricAlertResolvesItself reports whether a metric alert rule's firings are
// resolved by Azure when the condition clears. A rule that does not evaluate
// resolves nothing.
func metricAlertResolvesItself(props *armmonitor.MetricAlertProperties) bool {
	if props == nil || (props.Enabled != nil && !*props.Enabled) {
		return false
	}
	return props.AutoMitigate == nil || *props.AutoMitigate
}

// logAlertResolvesItself is metricAlertResolvesItself for a log search alert
// rule. Azure resolves its firings only when the rule is set to auto-mitigate,
// which is the default; one that is not leaves every firing fired, so each is
// the moment it fired.
func logAlertResolvesItself(rule *armmonitor.ScheduledQueryRuleResource) bool {
	if rule == nil || rule.Properties == nil || (rule.Kind != nil && *rule.Kind != armmonitor.KindLogAlert) {
		return false
	}
	props := rule.Properties
	if props.Enabled != nil && !*props.Enabled {
		return false
	}
	return props.AutoMitigate == nil || *props.AutoMitigate
}

// listSelfResolvingRules lists one subscription's metric alert and log search
// alert rules and returns, lowercased, the ids of those whose firings Azure
// resolves. Any failure fails the whole subscription: a rule missing from a
// partial listing would look deleted.
func listSelfResolvingRules(ctx providers.CloudProviderContext, cred azcore.TokenCredential, subID string) (map[string]bool, error) {
	rules := map[string]bool{}

	metricClient, err := armmonitor.NewMetricAlertsClient(subID, cred, getAzureMetricAlertsOpts(ctx))
	if err != nil {
		return nil, fmt.Errorf("metric alerts client: %w", err)
	}
	metricPager := metricClient.NewListBySubscriptionPager(nil)
	for metricPager.More() {
		page, err := metricPager.NextPage(ctx.GetContext())
		if err != nil {
			return nil, fmt.Errorf("metric alerts: %w", err)
		}
		for _, rule := range page.Value {
			if rule != nil && rule.ID != nil && metricAlertResolvesItself(rule.Properties) {
				rules[strings.ToLower(*rule.ID)] = true
			}
		}
	}

	logClient, err := armmonitor.NewScheduledQueryRulesClient(subID, cred, getAzureAuditOpts(ctx))
	if err != nil {
		return nil, fmt.Errorf("scheduled query rules client: %w", err)
	}
	logPager := logClient.NewListBySubscriptionPager(nil)
	for logPager.More() {
		page, err := logPager.NextPage(ctx.GetContext())
		if err != nil {
			return nil, fmt.Errorf("scheduled query rules: %w", err)
		}
		for _, rule := range page.Value {
			if rule != nil && rule.ID != nil && logAlertResolvesItself(rule) {
				rules[strings.ToLower(*rule.ID)] = true
			}
		}
	}
	return rules, nil
}

// newAlertRuleSnapshot starts the snapshot of one poll over an account's
// subscriptions. listedAt must be taken before the first rule is listed.
//
// The snapshot speaks for rules, not firings: a condition is on while its rule
// exists and Azure resolves its firings, and since when is not known. A stored
// firing is therefore never ended for a newer firing of its rule (another
// dimension of the rule can be firing beside it); its own resolve ends it, which
// the poll reads from the alerts that changed since the last one. The snapshot
// ends only the firings of a rule that was deleted, disabled or set to stop
// resolving: Azure leaves those fired, and nothing else would end them.
func newAlertRuleSnapshot(listedAt time.Time) *providers.OngoingSnapshot {
	return &providers.OngoingSnapshot{
		Sources:         []string{azureAlertEventSource},
		FingerprintLike: azureAlertFingerprintLike,
		ListedAt:        listedAt,
		Since:           map[string]time.Time{},
		Identify:        azureAlertRuleIdentity,
	}
}

// addSubscriptionRules adds one subscription's listing to the snapshot.
func addSubscriptionRules(snapshot *providers.OngoingSnapshot, subID string, rules map[string]bool) {
	snapshot.Scopes = append(snapshot.Scopes, strings.ToLower(subID))
	for rule := range rules {
		snapshot.Since[rule] = time.Time{}
	}
}

// markOngoingAlerts marks every fired alert of a self-resolving rule in a listed
// subscription as ongoing, so it is stored open until it resolves.
func markOngoingAlerts(events []providers.Event, snapshot *providers.OngoingSnapshot) {
	listed := make(map[string]bool, len(snapshot.Scopes))
	for _, scope := range snapshot.Scopes {
		listed[scope] = true
	}
	for i := range events {
		if events[i].EventStatus != providers.EventStatusFiring {
			continue
		}
		subscription, rule, ok := azureAlertRuleIdentity(events[i].EventId)
		if !ok || !listed[subscription] {
			continue
		}
		if _, on := snapshot.Since[rule]; on {
			events[i].OngoingScope = subscription
		}
	}
}

// alertRuleSnapshot lists the rules of every subscription of the account and
// marks the polled alerts they keep open. A subscription whose rules could not
// be listed is left out, so its stored firings are kept and its alerts are
// stored as before.
func alertRuleSnapshot(ctx providers.CloudProviderContext, account providers.Account, events []providers.Event) *providers.OngoingSnapshot {
	snapshot := newAlertRuleSnapshot(time.Now().UTC())
	cred, session, err := getAzureCredsForAccount(ctx, account)
	if err != nil {
		ctx.GetLogger().Warn("azure: cannot list alert rules, alerts are stored as moments", "error", err, "accountNumber", account.AccountNumber)
		return snapshot
	}
	for _, subID := range strings.Split(session.SubscriptionID, ",") {
		subID = strings.TrimSpace(subID)
		if subID == "" {
			continue
		}
		rules, err := listSelfResolvingRules(ctx, cred, subID)
		if err != nil {
			ctx.GetLogger().Warn("azure: cannot list alert rules, alerts of this subscription are stored as moments", "error", err, "subscriptionID", subID)
			recordProviderPermissionError(ctx, account, "monitor", "ListEvents", err)
			continue
		}
		addSubscriptionRules(snapshot, subID, rules)
	}
	markOngoingAlerts(events, snapshot)
	return snapshot
}
