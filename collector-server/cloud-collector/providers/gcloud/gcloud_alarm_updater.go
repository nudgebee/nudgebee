package gcloud

import (
	"fmt"
	"time"

	monitoring "cloud.google.com/go/monitoring/apiv3/v2"
	"cloud.google.com/go/monitoring/apiv3/v2/monitoringpb"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"nudgebee/collector/cloud/providers"
)

// UpdateGCPAlertPolicyThreshold patches the threshold (and optional duration) on every
// threshold condition of an existing GCP alert policy and writes it back via the Cloud
// Monitoring API using the account's credentials.
//
// policyName is the fully-qualified policy resource name
// (projects/<project>/alertPolicies/<id>) — it is the alert_rule_key carried from the
// suggestion. The existing policy is fetched first so condition names (and every other
// field) are preserved; only thresholdValue/duration are mutated, keeping the update a
// true patch rather than a replace.
func UpdateGCPAlertPolicyThreshold(ctx providers.CloudProviderContext, account providers.Account, policyName string, newThreshold float64, newDurationMin int) error {
	if policyName == "" {
		return fmt.Errorf("alert policy name is required")
	}

	session, err := getGcloudSessionFromAccount(ctx, account)
	if err != nil {
		return fmt.Errorf("failed to get gcloud session: %w", err)
	}

	client, err := monitoring.NewAlertPolicyClient(ctx.GetContext(), session.Opts...)
	if err != nil {
		return fmt.Errorf("failed to create alert policy client: %w", err)
	}
	defer func() {
		if cerr := client.Close(); cerr != nil {
			ctx.GetLogger().Warn("failed to close alert policy client", "error", cerr)
		}
	}()

	policy, err := client.GetAlertPolicy(ctx.GetContext(), &monitoringpb.GetAlertPolicyRequest{Name: policyName})
	if err != nil {
		return fmt.Errorf("failed to get alert policy %q: %w", policyName, err)
	}

	updated := 0
	for _, cond := range policy.GetConditions() {
		mt := cond.GetConditionThreshold()
		if mt == nil {
			continue
		}
		mt.ThresholdValue = newThreshold
		if newDurationMin > 0 {
			mt.Duration = durationpb.New(time.Duration(newDurationMin) * time.Minute)
		}
		updated++
	}
	if updated == 0 {
		return fmt.Errorf("alert policy %q has no threshold condition to update", policyName)
	}

	if _, err := client.UpdateAlertPolicy(ctx.GetContext(), &monitoringpb.UpdateAlertPolicyRequest{
		AlertPolicy: policy,
		UpdateMask:  &fieldmaskpb.FieldMask{Paths: []string{"conditions"}},
	}); err != nil {
		return fmt.Errorf("failed to update alert policy %q: %w", policyName, err)
	}

	ctx.GetLogger().Info("updated GCP alert policy threshold", "policy", policyName, "threshold", newThreshold, "conditions", updated)
	return nil
}
