package aws

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestParseBillingPeriodStart(t *testing.T) {
	pathPrefix := "myreport/cost/"

	cases := []struct {
		name       string
		commonPref string
		wantOK     bool
		wantMonth  time.Month
		wantYear   int
	}{
		{
			name:       "valid january 2026",
			commonPref: "myreport/cost/20260101-20260201/",
			wantOK:     true,
			wantMonth:  time.January,
			wantYear:   2026,
		},
		{
			name:       "valid november 2025",
			commonPref: "myreport/cost/20251101-20251201/",
			wantOK:     true,
			wantMonth:  time.November,
			wantYear:   2025,
		},
		{
			name:       "no trailing slash still parses",
			commonPref: "myreport/cost/20260201-20260301",
			wantOK:     true,
			wantMonth:  time.February,
			wantYear:   2026,
		},
		{
			name:       "wrong segment count",
			commonPref: "myreport/cost/some-other-folder/",
			wantOK:     false,
		},
		{
			name:       "non 8-digit parts",
			commonPref: "myreport/cost/2026-2026/",
			wantOK:     false,
		},
		{
			name:       "invalid date digits",
			commonPref: "myreport/cost/20261301-20261401/",
			wantOK:     false,
		},
		{
			name:       "empty segment",
			commonPref: "myreport/cost/",
			wantOK:     false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			start, ok := parseBillingPeriodStart(tc.commonPref, pathPrefix)
			assert.Equal(t, tc.wantOK, ok)
			if tc.wantOK {
				assert.Equal(t, tc.wantMonth, start.Month())
				assert.Equal(t, tc.wantYear, start.Year())
				assert.Equal(t, 1, start.Day())
			}
		})
	}
}
