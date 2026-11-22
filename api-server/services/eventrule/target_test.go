package eventrule

import (
	"context"
	"log/slog"
	"regexp"
	"testing"

	"nudgebee/services/common"
	"nudgebee/services/relay"
	"nudgebee/services/security"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testEnv(agent, ruler, git bool) capabilityEnv {
	return capabilityEnv{
		agentConnected:    func(string) bool { return agent },
		agentCapabilities: func(string) AgentCapabilities { return AgentCapabilities{} },
		rulerConfigured:   func(string) (bool, string) { return ruler, "no ruler configured" },
		hasGit:            git,
	}
}

func resolved(source string, mutate func(*RuleTarget)) *RuleTarget {
	t := &RuleTarget{ID: "r1", AccountID: "a1", IsEditable: true, Enabled: true,
		Rule: toggledRule{Alert: "HighCPU", Source: source, AlertType: "metric", Labels: map[string]string{}}}
	if mutate != nil {
		mutate(t)
	}
	t.Kind = targetKindForSource(t.Rule.Source, t.Rule.AlertType)
	t.Locator = buildLocator(t, ResolveOptions{})
	return t
}

// Every control Alert Manager shows is decided here, so each branch that turns a
// control off (or keeps one on) is pinned.
func TestComputeOperations(t *testing.T) {
	awsExpr := `{"AlarmArn":"arn:aws:cloudwatch:us-east-1:111:alarm:HighCPU","AlarmName":"HighCPU"}`
	extID := "12345"

	tests := []struct {
		name   string
		target *RuleTarget
		env    capabilityEnv
		check  func(t *testing.T, ops RuleOperations)
	}{
		{
			name:   "prometheus CR rule with agent: every write, PR with git, mute",
			target: resolved("prometheus", nil),
			env:    testEnv(true, false, true),
			check: func(t *testing.T, ops RuleOperations) {
				for _, c := range []Capability{ops.EditDefinition, ops.UpdateThreshold, ops.UpdateDuration, ops.DisableAtSource, ops.PR, ops.Mute} {
					assert.True(t, c.Available, c.Reason)
				}
			},
		},
		{
			name:   "legacy nudgebee metric row routes like prometheus",
			target: resolved("nudgebee", nil),
			env:    testEnv(true, false, false),
			check: func(t *testing.T, ops RuleOperations) {
				assert.True(t, ops.DisableAtSource.Available)
				assert.True(t, ops.Mute.Available)
				assert.False(t, ops.PR.Available)
			},
		},
		{
			name:   "prometheus rules-API row is read-only but mutable",
			target: resolved("prometheus", func(r *RuleTarget) { r.IsEditable = false }),
			env:    testEnv(true, false, true),
			check: func(t *testing.T, ops RuleOperations) {
				assert.False(t, ops.EditDefinition.Available)
				assert.False(t, ops.DisableAtSource.Available)
				assert.Contains(t, ops.DisableAtSource.Reason, "rules API")
				assert.True(t, ops.Mute.Available)
			},
		},
		{
			name:   "prometheus without agent",
			target: resolved("prometheus", nil),
			env:    testEnv(false, false, false),
			check: func(t *testing.T, ops RuleOperations) {
				assert.False(t, ops.UpdateThreshold.Available)
				assert.Contains(t, ops.UpdateThreshold.Reason, "agent not connected")
			},
		},
		{
			name:   "ambiguous prometheus row",
			target: resolved("prometheus", func(r *RuleTarget) { r.Ambiguous = true }),
			env:    testEnv(true, false, false),
			check: func(t *testing.T, ops RuleOperations) {
				assert.False(t, ops.DisableAtSource.Available)
				assert.True(t, ops.Mute.Available)
			},
		},
		{
			name:   "AWS: threshold edit yes, disable at source never, mute yes",
			target: resolved("AWS_CloudWatch_Alarm", func(r *RuleTarget) { r.Rule.Expr = awsExpr }),
			env:    testEnv(false, false, true),
			check: func(t *testing.T, ops RuleOperations) {
				assert.True(t, ops.UpdateThreshold.Available)
				assert.True(t, ops.UpdateDuration.Available)
				assert.False(t, ops.DisableAtSource.Available)
				assert.Contains(t, ops.DisableAtSource.Reason, "Mute")
				assert.False(t, ops.EditDefinition.Available)
				assert.False(t, ops.PR.Available)
				assert.True(t, ops.Mute.Available)
			},
		},
		{
			name:   "AWS without any locator",
			target: resolved("AWS_CloudWatch_Alarm", nil),
			env:    testEnv(false, false, false),
			check: func(t *testing.T, ops RuleOperations) {
				assert.False(t, ops.UpdateThreshold.Available)
			},
		},
		{
			name:   "GCP with policy label: threshold edit and disable at source",
			target: resolved("GCP_Metric_Alert", func(r *RuleTarget) { r.Rule.Labels["gcp_policy_id"] = "projects/p/alertPolicies/1" }),
			env:    testEnv(false, false, false),
			check: func(t *testing.T, ops RuleOperations) {
				assert.True(t, ops.UpdateThreshold.Available)
				assert.True(t, ops.DisableAtSource.Available)
				assert.False(t, ops.EditDefinition.Available)
			},
		},
		{
			name: "ambiguous GCP row ignores its own policy id",
			target: resolved("GCP_Metric_Alert", func(r *RuleTarget) {
				r.Ambiguous = true
				r.Rule.Labels["gcp_policy_id"] = "projects/p/alertPolicies/1"
			}),
			env: testEnv(false, false, false),
			check: func(t *testing.T, ops RuleOperations) {
				assert.False(t, ops.UpdateThreshold.Available)
				assert.False(t, ops.DisableAtSource.Available)
				assert.Contains(t, ops.DisableAtSource.Reason, "more than one")
			},
		},
		{
			name: "AWS composite alarm has no threshold",
			target: resolved("AWS_CloudWatch_Alarm", func(r *RuleTarget) {
				r.Rule.Expr = `{"AlarmArn":"arn:aws:cloudwatch:us-east-1:1:alarm:c"}`
				r.Rule.Labels["aws_alarm_type"] = "composite"
			}),
			env: testEnv(false, false, false),
			check: func(t *testing.T, ops RuleOperations) {
				assert.False(t, ops.UpdateThreshold.Available)
				assert.Contains(t, ops.UpdateThreshold.Reason, "composite")
				assert.True(t, ops.Mute.Available)
			},
		},
		{
			name: "ambiguous AWS row: the ARN in its definition is not trusted",
			target: resolved("AWS_CloudWatch_Alarm", func(r *RuleTarget) {
				r.Ambiguous = true
				r.Rule.Expr = `{"AlarmArn":"arn:aws:cloudwatch:us-east-1:1:alarm:A"}`
				r.Rule.Labels = map[string]string{"aws_region": "us-east-1", "aws_alarm": "A"}
			}),
			env: testEnv(false, false, false),
			check: func(t *testing.T, ops RuleOperations) {
				assert.False(t, ops.UpdateThreshold.Available)
			},
		},
		{
			name:   "Azure scheduled query alert is not writable",
			target: resolved("Azure_Monitor_Alert", func(r *RuleTarget) { r.Category = azureScheduledQueryCategory }),
			env:    testEnv(false, false, false),
			check: func(t *testing.T, ops RuleOperations) {
				assert.False(t, ops.UpdateThreshold.Available)
				assert.False(t, ops.DisableAtSource.Available)
				assert.True(t, ops.Mute.Available)
			},
		},
		{
			// Verified on dev: turning off "Azure Resource Deleted" (alert_type log)
			// failed in cloud-collector with "metric alert not found".
			name:   "Azure log / activity-log alert is not writable",
			target: resolved("Azure_Monitor_Alert", func(r *RuleTarget) { r.Rule.AlertType = "log" }),
			env:    testEnv(false, false, false),
			check: func(t *testing.T, ops RuleOperations) {
				assert.False(t, ops.DisableAtSource.Available)
				assert.False(t, ops.UpdateThreshold.Available)
				assert.Contains(t, ops.DisableAtSource.Reason, "metric alerts")
				assert.True(t, ops.Mute.Available)
			},
		},
		{
			name:   "Azure found by name warns; disable offered too",
			target: resolved("Azure_Monitor_Alert", nil),
			env:    testEnv(false, false, false),
			check: func(t *testing.T, ops RuleOperations) {
				assert.True(t, ops.UpdateThreshold.Available)
				assert.Contains(t, ops.UpdateThreshold.Warning, "by name")
				assert.True(t, ops.DisableAtSource.Available)
			},
		},
		{
			name: "Azure with a synced ARM id: no warning",
			target: resolved("Azure_Monitor_Alert", func(r *RuleTarget) {
				id := "/subscriptions/s/resourceGroups/rg/providers/microsoft.insights/metricAlerts/HighCPU"
				r.Rule.ExternalRuleID = &id
			}),
			env: testEnv(false, false, false),
			check: func(t *testing.T, ops RuleOperations) {
				assert.True(t, ops.DisableAtSource.Available)
				assert.Empty(t, ops.DisableAtSource.Warning)
			},
		},
		{
			name:   "webhook copy: nothing but a reason",
			target: resolved("datadog_webhook", nil),
			env:    testEnv(true, true, true),
			check: func(t *testing.T, ops RuleOperations) {
				for _, c := range []Capability{ops.EditDefinition, ops.UpdateThreshold, ops.UpdateDuration, ops.DisableAtSource, ops.PR, ops.Mute} {
					assert.False(t, c.Available)
					assert.NotEmpty(t, c.Reason)
				}
			},
		},
		{
			name:   "chronosphere copy is unsupported",
			target: resolved("chronosphere", nil),
			env:    testEnv(true, true, true),
			check: func(t *testing.T, ops RuleOperations) {
				assert.False(t, ops.DisableAtSource.Available)
				assert.False(t, ops.EditDefinition.Available)
			},
		},
		{
			name:   "external rule without provider id",
			target: resolved("datadog", nil),
			env:    testEnv(false, false, false),
			check: func(t *testing.T, ops RuleOperations) {
				assert.False(t, ops.DisableAtSource.Available)
			},
		},
		{
			name:   "external rule with provider id",
			target: resolved("datadog", func(r *RuleTarget) { r.Rule.ExternalRuleID = &extID }),
			env:    testEnv(false, false, false),
			check: func(t *testing.T, ops RuleOperations) {
				assert.True(t, ops.DisableAtSource.Available)
				assert.True(t, ops.EditDefinition.Available)
			},
		},
		{
			name:   "prometheus_user follows the ruler",
			target: resolved("prometheus_user", nil),
			env:    testEnv(false, false, false),
			check: func(t *testing.T, ops RuleOperations) {
				assert.False(t, ops.EditDefinition.Available)
				assert.Equal(t, "no ruler configured", ops.EditDefinition.Reason)
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.check(t, computeOperations(tc.target, tc.env))
		})
	}
}

func TestBuildLocator(t *testing.T) {
	aws := resolved("AWS_CloudWatch_Alarm", func(r *RuleTarget) {
		r.Rule.Expr = `{"AlarmArn":"arn:aws:cloudwatch:eu-west-1:1:alarm:A","AlarmName":"A"}`
		r.Rule.Labels = map[string]string{"aws_region": "eu-west-1"}
	})
	assert.Equal(t, "arn:aws:cloudwatch:eu-west-1:1:alarm:A", aws.Locator.AWSAlarmARN)
	assert.Equal(t, "A", aws.Locator.AWSAlarmName)

	// The event's ARN is more precise than a row that may collapse two regions.
	hinted := buildLocator(aws, ResolveOptions{AlertRuleKey: "arn:aws:cloudwatch:us-east-1:1:alarm:A"})
	assert.Equal(t, "arn:aws:cloudwatch:us-east-1:1:alarm:A", hinted.AWSAlarmARN)

	azure := resolved("Azure_Monitor_Alert", nil)
	byName := buildLocator(azure, ResolveOptions{AlertRuleKey: "HighCPU"})
	assert.Empty(t, byName.AzureID, "an alert name is not an ARM id")
	assert.Equal(t, "HighCPU", byName.AzureName)
	armID := "/subscriptions/s/resourceGroups/rg/providers/Microsoft.Insights/metricAlerts/HighCPU"
	assert.Equal(t, armID, buildLocator(azure, ResolveOptions{AlertRuleKey: armID}).AzureID)

	gcp := resolved("GCP_Metric_Alert", nil)
	assert.Equal(t, "projects/p/alertPolicies/9", buildLocator(gcp, ResolveOptions{AlertRuleKey: "projects/p/alertPolicies/9"}).GCPPolicy)
}

// An ambiguous row only trusts the id the firing event carried.
func TestBuildLocator_Ambiguous(t *testing.T) {
	aws := resolved("AWS_CloudWatch_Alarm", func(r *RuleTarget) {
		r.Ambiguous = true
		r.Rule.Expr = `{"AlarmArn":"arn:aws:cloudwatch:eu-west-1:1:alarm:A"}`
	})
	assert.Empty(t, aws.Locator.AWSAlarmARN)
	assert.Equal(t, "arn:aws:cloudwatch:us-east-1:1:alarm:A",
		buildLocator(aws, ResolveOptions{AlertRuleKey: "arn:aws:cloudwatch:us-east-1:1:alarm:A"}).AWSAlarmARN)

	azure := resolved("Azure_Monitor_Alert", func(r *RuleTarget) { r.Ambiguous = true })
	assert.Empty(t, azure.Locator.AzureName, "a shared name must not be used to find the alert")

	provider, ref := (&RuleTarget{Kind: TargetAzure, Locator: RuleLocator{AzureName: "HighCPU"}}).CloudWriteRef()
	assert.Equal(t, "azure_app_insights", provider)
	assert.Equal(t, "HighCPU", ref)
	provider, _ = (&RuleTarget{Kind: TargetPromCR}).CloudWriteRef()
	assert.Empty(t, provider)
}

func TestTargetKindForSource(t *testing.T) {
	assert.Equal(t, TargetPromCR, targetKindForSource("prometheus", "metric"))
	assert.Equal(t, TargetPromCR, targetKindForSource("nudgebee", ""))
	assert.Equal(t, TargetUnsupported, targetKindForSource("nudgebee", "log"))
	assert.Equal(t, TargetPromRuler, targetKindForSource("prometheus_user", "metric"))
	assert.Equal(t, TargetAzure, targetKindForSource("azure_monitor_webhook", "metric"))
	assert.Equal(t, TargetExternal, targetKindForSource("newrelic", "metric"))
	for _, s := range []string{"chronosphere", "zenduty_webhook", "pagerduty_webhook", "AWS_EventBridge", "kubernetes_api_server"} {
		assert.Equal(t, TargetUnsupported, targetKindForSource(s, "metric"), s)
	}
}

func TestMuteEventSources(t *testing.T) {
	assert.Equal(t, []string{"AWS_CloudWatch_Alarm", "AWS_EventBridge"}, MuteEventSources("AWS_CloudWatch_Alarm"))
	// A mute written on an EventBridge rule covers the pushed alarms now stored
	// as CloudWatch alarms.
	assert.Equal(t, []string{"AWS_EventBridge", "AWS_CloudWatch_Alarm"}, MuteEventSources("AWS_EventBridge"))
	// Prometheus firings also arrive through Alertmanager-fed webhooks (on dev,
	// mostly PagerDuty); a mute that only covered agent delivery let them through.
	promSources := []string{"prometheus", "prometheus_alertmanager_webhook", "pagerduty_webhook", "zenduty_webhook"}
	assert.Equal(t, promSources, MuteEventSources("prometheus"))
	assert.Equal(t, promSources, MuteEventSources("nudgebee"))
	assert.Equal(t, promSources, MuteEventSources("pagerduty_webhook"))
	assert.Equal(t, promSources, MuteEventSources("prometheus_alertmanager_webhook"))
	assert.Nil(t, MuteEventSources("datadog_webhook"))
	assert.Nil(t, MuteEventSources("zenduty_webhook"))

	// Callers get their own slice.
	got := MuteEventSources("prometheus")
	got[0] = "changed"
	assert.Equal(t, promSources, MuteEventSources("pagerduty_webhook"))
}

// AC-10: the lookup is bound to the caller's tenant, and a row in an account the
// caller cannot read is dropped, so either case resolves to "not found" before any
// provider or relay call.
func TestResolveRuleTargets_TenantAndAccountScoped(t *testing.T) {
	const acctA = "11111111-1111-1111-1111-111111111111"
	const acctB = "22222222-2222-2222-2222-222222222222"
	var sc security.SecurityContext
	require.NoError(t, common.UnmarshalJson([]byte(`{
		"TenantId": "tenant-a", "UserId": "u1",
		"AccountIds": ["`+acctA+`"], "Roles": ["account_admin"],
		"ScopedEntityIds": {"account_admin": ["`+acctA+`"]}
	}`), &sc))
	ctx := security.NewRequestContext(context.Background(), &sc, slog.Default(), nil, nil)

	dbms, mock := mockDB(t)
	mock.ExpectQuery(regexp.QuoteMeta(`FROM event_rules WHERE tenant_id = $1 AND id = ANY($2::uuid[])`)).
		WithArgs("tenant-a", sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id", "account_id", "alert", "source"}).
			AddRow("33333333-3333-3333-3333-333333333333", acctB, "HighCPU", "AWS_CloudWatch_Alarm"))
	mock.ExpectQuery(regexp.QuoteMeta(`FROM event_triage_rules`)).
		WithArgs("tenant-a", sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))

	targets, err := resolveRuleTargets(ctx, dbms.Db, []string{"33333333-3333-3333-3333-333333333333", "not-a-uuid"}, ResolveOptions{})
	require.NoError(t, err)
	assert.Empty(t, targets, "a rule in an account the caller cannot read must not resolve")
	require.NoError(t, mock.ExpectationsWereMet())
}

// Only malformed ids: nothing to look up, and no query that would fail the cast.
func TestResolveRuleTargets_SkipsMalformedIDs(t *testing.T) {
	dbms, mock := mockDB(t)
	ctx := security.NewRequestContext(context.Background(), &security.SecurityContext{}, slog.Default(), nil, nil)
	targets, err := resolveRuleTargets(ctx, dbms.Db, []string{"", "r-1", "1; drop"}, ResolveOptions{})
	require.NoError(t, err)
	assert.Empty(t, targets)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestMergeRuleMetadata_KeepsUnshownFields(t *testing.T) {
	stored := toggledRule{
		Labels:      map[string]string{"severity": "warning", "team": "payments"},
		Annotations: map[string]string{"summary": "old", "runbook_url": "https://rb"},
	}
	req := EventConfig{}
	req.Labels.Severity = "critical"
	req.Annotations.Summary = "new"
	req.Annotations.Description = "d"

	labels, annotations := mergeRuleMetadata(stored, req)
	assert.Equal(t, map[string]string{"severity": "critical", "team": "payments"}, labels)
	assert.Equal(t, map[string]string{"summary": "new", "description": "d", "runbook_url": "https://rb"}, annotations)
	assert.Equal(t, "warning", stored.Labels["severity"], "the stored map must not be mutated")
}

func TestToStringMap(t *testing.T) {
	req := EventConfig{}
	req.Labels.Severity = "critical"
	req.Annotations.Summary = "s"
	assert.Equal(t, map[string]string{"severity": "critical"}, toStringMap(req.Labels))
	assert.Equal(t, map[string]string{"summary": "s", "description": ""}, toStringMap(req.Annotations))
	assert.Equal(t, map[string]string{"a": "b"}, toStringMap(map[string]string{"a": "b"}))
}

func TestRequireOperation(t *testing.T) {
	assert.NoError(t, requireOperation(Capability{Available: true}, "edit"))
	err := requireOperation(Capability{Reason: "no"}, "edit")
	assert.ErrorIs(t, err, ErrOperationUnavailable)
	assert.Contains(t, err.Error(), "no")
}

// A write the agent rejected must not read as success.
func TestActionSucceeded(t *testing.T) {
	assert.NoError(t, relay.ActionSucceeded(map[string]any{}))
	assert.NoError(t, relay.ActionSucceeded(map[string]any{"data": map[string]any{"success": true}}))
	assert.NoError(t, relay.ActionSucceeded(map[string]any{"data": map[string]any{}}))
	err := relay.ActionSucceeded(map[string]any{"data": map[string]any{"success": false, "msg": "forbidden"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "forbidden")
}

func TestPromRuleWrite(t *testing.T) {
	newAgent := AgentCapabilities{LocatorWrites: true, ClusterWrite: true, InstallationNamespace: "nb"}
	oldAgent := AgentCapabilities{InstallationNamespace: "nb"}
	readOnlyAgent := AgentCapabilities{LocatorWrites: true, InstallationNamespace: "nb"}
	ref := func(ns, name, managedBy string) PromRuleRef {
		return PromRuleRef{Namespace: ns, Name: name, Group: "g", ManagedBy: managedBy}
	}

	tests := []struct {
		name      string
		ref       PromRuleRef
		agent     AgentCapabilities
		available bool
		warning   string
		reason    string
		locator   bool
	}{
		{"own CR, new agent", ref("nb", nudgebeeRulesCR, "nudgebee"), newAgent, true, "", "", true},
		{"own CR, old agent: legacy path", ref("nb", nudgebeeRulesCR, "nudgebee"), oldAgent, true, "", "", false},
		{"row from before sync recorded the CR, old agent", PromRuleRef{}, oldAgent, true, "", "", false},
		{"row from before sync recorded the CR, new agent", PromRuleRef{}, newAgent, true, "", "", false},
		{"customer CR, old agent", ref("monitoring", "payments", "unmanaged"), oldAgent, false, "", "too old", false},
		{"customer CR, not yet classified, old agent", ref("monitoring", "payments", ""), oldAgent, false, "", "too old", false},
		{"unmanaged customer CR", ref("monitoring", "payments", "unmanaged"), newAgent, true, "", "", true},
		{"helm: direct with warning", ref("monitoring", "kps", "helm"), newAgent, true, "helm upgrade", "", true},
		{"agent chart: direct with warning", ref("nb", "nb-agent.rules", "chart"), newAgent, true, "chart", "", true},
		{"argo / flux: PR only", ref("monitoring", "app", "gitops"), newAgent, false, "", "Argo CD / Flux", false},
		{"unknown manager: PR only", ref("monitoring", "x", "unknown"), newAgent, false, "", "cannot tell", false},
		{"not yet classified", ref("monitoring", "x", ""), newAgent, false, "", "next rule sync", false},
		{"read-only install, other namespace", ref("monitoring", "payments", "unmanaged"), readOnlyAgent, false, "", "own namespace", false},
		{"read-only install, own namespace", ref("nb", "custom", "unmanaged"), readOnlyAgent, true, "", "", true},
		{"another agent's own CR in a shared cluster", ref("other-agent", nudgebeeRulesCR, "nudgebee"), newAgent, true, "", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			target := &RuleTarget{PromRule: tc.ref}
			got := promRuleWrite(target, tc.agent)
			assert.Equal(t, tc.available, got.Available, got.Reason)
			if tc.warning != "" {
				assert.Contains(t, got.Warning, tc.warning)
			} else {
				assert.Empty(t, got.Warning)
			}
			if tc.reason != "" {
				assert.Contains(t, got.Reason, tc.reason)
			}
			assert.Equal(t, tc.locator, target.SendPromLocator)
		})
	}
}

func TestAgentLocatorParams(t *testing.T) {
	target := &RuleTarget{PromRule: PromRuleRef{Namespace: "monitoring", Name: "payments", Group: "pay"}}
	params := target.AgentLocatorParams(map[string]any{"alert": "A", "group": "ui-group", "namespace": "ui-ns"})
	assert.Equal(t, map[string]any{"alert": "A", "group": "ui-group", "namespace": "ui-ns"}, params, "no locator unless the resolver chose it")

	var none *RuleTarget
	assert.Equal(t, map[string]any{"alert": "A"}, none.AgentLocatorParams(map[string]any{"alert": "A"}))

	target.SendPromLocator = true
	params = target.AgentLocatorParams(map[string]any{"alert": "A", "group": "ui-group", "namespace": "ui-ns"})
	assert.Equal(t, map[string]any{"alert": "A", "name": "payments", "namespace": "monitoring", "group": "pay"}, params)
}

// The Prometheus branch of the decision table reads the agent's capabilities.
func TestComputeOperations_PromUsesAgentCapabilities(t *testing.T) {
	env := testEnv(true, false, true)
	env.agentCapabilities = func(string) AgentCapabilities {
		return AgentCapabilities{LocatorWrites: true, ClusterWrite: true, InstallationNamespace: "nb"}
	}
	gitops := resolved("prometheus", func(r *RuleTarget) {
		r.PromRule = PromRuleRef{Namespace: "monitoring", Name: "app", Group: "g", ManagedBy: "gitops"}
	})
	ops := computeOperations(gitops, env)
	assert.False(t, ops.EditDefinition.Available)
	assert.False(t, ops.DisableAtSource.Available)
	assert.True(t, ops.PR.Available)
	assert.True(t, ops.Mute.Available)

	helm := resolved("prometheus", func(r *RuleTarget) {
		r.PromRule = PromRuleRef{Namespace: "monitoring", Name: "kps", Group: "g", ManagedBy: "helm"}
	})
	ops = computeOperations(helm, env)
	assert.True(t, ops.DisableAtSource.Available)
	assert.NotEmpty(t, ops.DisableAtSource.Warning)
	assert.True(t, ops.PR.Available)
	assert.True(t, helm.SendPromLocator)
}

// A synced rule with no recorded location is not in the agent's own
// PrometheusRule once the account's sync records locations: writing it there
// would leave the real rule (e.g. a VMRule) firing while the screen shows it changed.
func TestPromRuleWriteUnknownLocation(t *testing.T) {
	synced := AgentCapabilities{LocatorWrites: true, ClusterWrite: true, InstallationNamespace: "nb", RuleLocationsSynced: true}
	oldSynced := AgentCapabilities{InstallationNamespace: "nb", RuleLocationsSynced: true}
	notSynced := AgentCapabilities{LocatorWrites: true, ClusterWrite: true, InstallationNamespace: "nb"}
	rule := func(source string) *RuleTarget {
		return &RuleTarget{Rule: toggledRule{Source: source}}
	}

	got := promRuleWrite(rule(agentEventSourceName), synced)
	assert.False(t, got.Available)
	assert.Contains(t, got.Reason, "does not know where this rule is defined")

	got = promRuleWrite(rule(agentEventSourceName), oldSynced)
	assert.False(t, got.Available, "an old agent would write the agent's own PrometheusRule too")

	// Before the account's sync records any location, the old assumption stands.
	assert.True(t, promRuleWrite(rule(agentEventSourceName), notSynced).Available)
	// Rules Nudgebee created live in its own PrometheusRule.
	assert.True(t, promRuleWrite(rule("nudgebee"), synced).Available)
	// A rule with a recorded location is unaffected.
	withRef := rule(agentEventSourceName)
	withRef.PromRule = PromRuleRef{Namespace: "monitoring", Name: "payments", Group: "g", ManagedBy: "unmanaged"}
	assert.True(t, promRuleWrite(withRef, synced).Available)
}
