package gcloud

import (
	"nudgebee/collector/cloud/providers"
	"testing"

	"github.com/stretchr/testify/assert"
	sqladmin "google.golang.org/api/sqladmin/v1"
)

// TestEffectiveSQLState verifies that a stopped Cloud SQL instance (which GCP
// reports as state=RUNNABLE with activationPolicy=NEVER) is surfaced as STOPPED,
// while running instances are left unchanged.
func TestEffectiveSQLState(t *testing.T) {
	cases := []struct {
		name             string
		state            string
		settings         *sqladmin.Settings
		expectedState    string
		expectedNbStatus providers.ResourceStatus
	}{
		{
			name:             "running instance stays RUNNABLE",
			state:            "RUNNABLE",
			settings:         &sqladmin.Settings{ActivationPolicy: "ALWAYS"},
			expectedState:    "RUNNABLE",
			expectedNbStatus: providers.ResourceStatusActive,
		},
		{
			name:             "stopped instance becomes STOPPED",
			state:            "RUNNABLE",
			settings:         &sqladmin.Settings{ActivationPolicy: "NEVER"},
			expectedState:    "STOPPED",
			expectedNbStatus: providers.ResourceStatusInactive,
		},
		{
			name:             "nil settings falls back to raw state",
			state:            "RUNNABLE",
			settings:         nil,
			expectedState:    "RUNNABLE",
			expectedNbStatus: providers.ResourceStatusActive,
		},
		{
			name:             "explicit failed state preserved",
			state:            "FAILED",
			settings:         &sqladmin.Settings{ActivationPolicy: "ALWAYS"},
			expectedState:    "FAILED",
			expectedNbStatus: providers.ResourceStatusInactive,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			instance := &sqladmin.DatabaseInstance{State: tc.state, Settings: tc.settings}
			assert.Equal(t, tc.expectedState, effectiveSQLState(instance))
			assert.Equal(t, tc.expectedNbStatus, gcpSQLStatusToNbStatus(effectiveSQLState(instance)))
		})
	}
}

// TestInstanceToResource_StoppedStateOverride verifies that instanceToResource
// overrides meta.state and Status for a stopped instance so the UI reflects it.
func TestInstanceToResource_StoppedStateOverride(t *testing.T) {
	s := &cloudSQLService{}
	instance := &sqladmin.DatabaseInstance{
		Name:     "utsav-testing-prod",
		Region:   "us-central1",
		State:    "RUNNABLE",
		Settings: &sqladmin.Settings{ActivationPolicy: "NEVER"},
	}

	res := s.instanceToResource(instance, "example-project")

	assert.Equal(t, providers.ResourceStatusInactive, res.Status)
	assert.Equal(t, "STOPPED", res.Meta["state"])
}
