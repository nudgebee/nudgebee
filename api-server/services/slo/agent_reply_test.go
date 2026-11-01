package slo

import (
	"testing"

	"nudgebee/services/common"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Agent replies arrive as decoded JSON, where numbers are float64 — the reason the
// old `resp["status_code"] == 500` check never fired (#39800).
func decodeReply(t *testing.T, raw string) map[string]any {
	t.Helper()
	var resp map[string]any
	require.NoError(t, common.UnmarshalJson([]byte(raw), &resp))
	return resp
}

func TestSLOAgentReplyError(t *testing.T) {
	for name, tc := range map[string]struct {
		raw     string
		wantErr string
	}{
		"reports":                {raw: `{"status_code":200,"data":{"success":true,"data":[{"name":"latency"}]}}`},
		"no reports":             {raw: `{"status_code":200,"data":{"success":true,"data":[]}}`},
		"action failed":          {raw: `{"status_code":500,"data":{"error":"prometheus not configured"}}`, wantErr: "agent returned an error (status 500): prometheus not configured"},
		"error, no status":       {raw: `{"data":{"error":"relay dropped"}}`, wantErr: "agent returned an error: relay dropped"},
		"slo failed, no message": {raw: `{"status_code":200,"data":{"success":false,"data":[]}}`, wantErr: "agent could not run the SLO: no reason given"},
		"slo run failed":         {raw: `{"status_code":200,"data":{"success":false,"data":[],"msg":"slo_generator: distribution_cut requires expression and threshold_bucket"}}`, wantErr: "agent could not run the SLO: slo_generator: distribution_cut requires expression"},
		"bare error status":      {raw: `{"status_code":504,"data":{}}`, wantErr: "agent returned status 504"},
		"no status, no envelope": {raw: `{"data":{"data":[]}}`},
	} {
		t.Run(name, func(t *testing.T) {
			err := sloAgentReplyError(decodeReply(t, tc.raw))
			if tc.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}
