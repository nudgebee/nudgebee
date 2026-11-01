package observability

import (
	"errors"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
)

func TestGetTracesNoProvider(t *testing.T) {
	lookupFailure := errors.New("provider lookup failed")
	for _, tc := range []struct {
		name      string
		lookupErr error
		wantErr   error
	}{
		{"unconfigured", nil, ErrTraceProviderNotConfigured},
		{"lookup failure", lookupFailure, lookupFailure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			patch := gomonkey.ApplyFunc(getLogsMetricsTracesProviderWithIntegration,
				func(*security.RequestContext, string, string, string, string) (string, string, *core.IntegrationDto, error) {
					return "", "", nil, tc.lookupErr
				})
			defer patch.Reset()
			_, err := GetTraces(newLogLabelCtx(), TracesV3Request{AccountId: "account"})
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("got error %v, want %v", err, tc.wantErr)
			}
		})
	}
}
