package recommendation

import "testing"

func TestClassifyChangeByRuleName(t *testing.T) {
	tests := []struct {
		rule string
		want ChangeClass
	}{
		{"unused_pvc", ChangeClassDestructive},
		{"abandoned_resource", ChangeClassDestructive},
		{"aws_ec2_idle_instance", ChangeClassDestructive},
		{"aws_ec2_orphaned_volume", ChangeClassDestructive},
		{"aws_rds_idle_instance", ChangeClassDestructive},
		{"azure_disk_unattached_volume", ChangeClassDestructive},
		{"gcp_native_compute_instance_idle_resource", ChangeClassDestructive},
		// GCP-native rules always arrive prefixed; the bare recommender id never
		// appears in rule_name, so it must not be a map key anyone relies on.
		{"compute_instance_idle_resource", ChangeClassUnknown},
		{"aws_native_purchase_reserved_instances", ChangeClassAdditive},
		{"aws_native_ce_savings_plan_recommendation", ChangeClassAdditive},
		{"replica-rightsizing", ChangeClassReductive},
		{"pv_rightsize", ChangeClassReductive},
		{"azure_sql_large_dtu_rightsizing", ChangeClassReductive},
		{"azure_sql_database_oversized_compute", ChangeClassReductive},
		{"Spot instance recommendation", ChangeClassReductive},
		{"health_check", ChangeClassUnknown},
		{"image_scan", ChangeClassUnknown},
		{"aws_rds_copy_tags_to_snapshots", ChangeClassUnknown},
		{"", ChangeClassUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.rule, func(t *testing.T) {
			if got := ClassifyChange(tt.rule, nil); got != tt.want {
				t.Errorf("ClassifyChange(%q) = %q, want %q", tt.rule, got, tt.want)
			}
		})
	}
}

func TestClassifyChangePodRightSizingDeltas(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    ChangeClass
	}{
		{
			name:    "any decrease is reductive, even mixed with increases",
			payload: `{"app":[{"resource":"cpu","allocated":{"request":0.5},"recommended":{"request":0.068}},{"resource":"memory","allocated":{"request":268435456},"recommended":{"request":536870912}}]}`,
			want:    ChangeClassReductive,
		},
		{
			name:    "increase-only is additive",
			payload: `{"app":[{"resource":"cpu","allocated":{"request":0.1},"recommended":{"request":0.25}},{"resource":"memory","allocated":{"request":268435456},"recommended":{"request":536870912}}]}`,
			want:    ChangeClassAdditive,
		},
		{
			name:    "setting an unset request is additive",
			payload: `{"metrics":[{"resource":"cpu","allocated":{"request":null},"recommended":{"request":0.01}}]}`,
			want:    ChangeClassAdditive,
		},
		{
			name:    "decrease in one container outweighs increases in another",
			payload: `{"a":[{"resource":"cpu","allocated":{"request":0.1},"recommended":{"request":0.2}}],"b":[{"resource":"memory","allocated":{"request":1073741824},"recommended":{"request":536870912}}]}`,
			want:    ChangeClassReductive,
		},
		{
			name:    "legacy notifications shape parses",
			payload: `{"notifications":[{"resource":"cpu","allocated":{"request":0.25},"recommended":{"request":0.112}}]}`,
			want:    ChangeClassReductive,
		},
		{
			name:    "KRR-null data with no decidable delta stays unknown",
			payload: `{"app":[{"resource":"cpu","allocated":{"request":null},"recommended":{"request":null}}]}`,
			want:    ChangeClassUnknown,
		},
		{
			name:    "non-container keys are tolerated",
			payload: `{"priority":2,"app":[{"resource":"memory","allocated":{"request":1000},"recommended":{"request":500}}]}`,
			want:    ChangeClassReductive,
		},
		{
			name:    "the rightsizer's ? NaN placeholder drops only its own entry, not the container",
			payload: `{"app":[{"resource":"cpu","allocated":{"request":0.5},"recommended":{"request":0.1}},{"resource":"memory","allocated":{"request":"?"},"recommended":{"request":600000000}}]}`,
			want:    ChangeClassReductive,
		},
		{
			name:    "empty payload stays unknown",
			payload: ``,
			want:    ChangeClassUnknown,
		},
		{
			name:    "malformed payload stays unknown",
			payload: `not json`,
			want:    ChangeClassUnknown,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ClassifyChange("pod_right_sizing", []byte(tt.payload)); got != tt.want {
				t.Errorf("ClassifyChange(pod_right_sizing, %s) = %q, want %q", tt.payload, got, tt.want)
			}
		})
	}
}
