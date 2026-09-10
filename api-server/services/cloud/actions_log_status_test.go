package cloud

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A log query that died mid-read must be distinguishable from a resource that genuinely
// has no logs. Both used to surface as zero rows and no evidence card, which is how a
// Cloud Logging "Read requests per minute" quota exhaustion was reported to the UI, and to
// the event's own analysis, as "this resource has no logs".
func TestLogQueryIncomplete(t *testing.T) {
	tests := []struct {
		name   string
		status string
		want   bool
		why    string
	}{
		{
			name:   "gcp query aborted mid-read",
			status: "Failed",
			want:   true,
			why:    "the collector sets Failed when the entry iterator errors (quota, permissions)",
		},
		{
			name:   "completed normally",
			status: "Complete",
			want:   false,
		},
		{
			name:   "provider does not populate status",
			status: "",
			want:   false,
			why: "AWS returns a zero-value response for its legitimately-empty cases " +
				"(no direct CloudWatch logs, no log group after detection, ResourceNotFound); " +
				"those must keep reporting 'no logs', not 'failed'",
		},
		{
			name:   "unknown non-complete status is treated as a failure",
			status: "Cancelled",
			want:   true,
			why:    "fail loud rather than silently rendering a partial result as authoritative",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, logQueryIncomplete(tt.status), tt.why)
		})
	}
}
