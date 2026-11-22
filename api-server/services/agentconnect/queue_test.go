package agentconnect

import (
	"context"
	"errors"
	"testing"

	"nudgebee/services/security"
)

// stubWork replaces the seams and records the order the work ran in.
func stubWork(t *testing.T, scansErr error, scansPanic bool) *[]string {
	t.Helper()
	calls := &[]string{}
	origSec, origScans, origOpenCost := newTenantAdminContext, triggerInitialScansFn, runServerSideOpenCost
	t.Cleanup(func() {
		newTenantAdminContext, triggerInitialScansFn, runServerSideOpenCost = origSec, origScans, origOpenCost
	})

	newTenantAdminContext = func(tenantId string) *security.SecurityContext { return &security.SecurityContext{} }

	triggerInitialScansFn = func(ctx *security.RequestContext, accountId, tenantId string) error {
		*calls = append(*calls, "scans:"+accountId+":"+tenantId)
		if _, ok := ctx.GetContext().Deadline(); !ok {
			t.Error("initial scans ran without a deadline")
		}
		if scansPanic {
			panic("boom")
		}
		return scansErr
	}
	runServerSideOpenCost = func(ctx *security.RequestContext, accountId, tenantId string) {
		*calls = append(*calls, "opencost:"+accountId+":"+tenantId)
	}
	return calls
}

func TestProcessAgentConnected_RunsScansThenOpenCost(t *testing.T) {
	calls := stubWork(t, nil, false)

	err := processAgentConnected(context.Background(), []byte(`{"account_id":" acc-1 ","tenant_id":"ten-1"}`))
	if err != nil {
		t.Fatalf("want nil, got %v", err)
	}
	want := []string{"scans:acc-1:ten-1", "opencost:acc-1:ten-1"}
	if len(*calls) != len(want) || (*calls)[0] != want[0] || (*calls)[1] != want[1] {
		t.Fatalf("calls = %v, want %v", *calls, want)
	}
}

// A returned error would be NackRequeued forever, and a failed scan dispatch must
// not stop the OpenCost sync either.
func TestProcessAgentConnected_ScanFailureStillAcksAndRunsOpenCost(t *testing.T) {
	for name, panics := range map[string]bool{"error": false, "panic": true} {
		t.Run(name, func(t *testing.T) {
			calls := stubWork(t, errors.New("db down"), panics)

			if err := processAgentConnected(context.Background(), []byte(`{"account_id":"acc-1","tenant_id":"ten-1"}`)); err != nil {
				t.Fatalf("want nil, got %v", err)
			}
			if len(*calls) != 2 || (*calls)[1] != "opencost:acc-1:ten-1" {
				t.Fatalf("calls = %v, want OpenCost to run after the failed scans", *calls)
			}
		})
	}
}

func TestProcessAgentConnected_DropsUnusableMessages(t *testing.T) {
	for name, body := range map[string]string{
		"malformed":      `{not json`,
		"missing tenant": `{"account_id":"acc-1"}`,
		"blank account":  `{"account_id":"  ","tenant_id":"ten-1"}`,
	} {
		t.Run(name, func(t *testing.T) {
			calls := stubWork(t, nil, false)

			if err := processAgentConnected(context.Background(), []byte(body)); err != nil {
				t.Fatalf("want nil, got %v", err)
			}
			if len(*calls) != 0 {
				t.Fatalf("calls = %v, want none", *calls)
			}
		})
	}
}
