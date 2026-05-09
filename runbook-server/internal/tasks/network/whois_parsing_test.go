package network

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestExtractExpiry(t *testing.T) {
	tests := []struct {
		name        string
		rawResponse string
		expected    *time.Time
	}{
		{
			name: "Verisign COM/NET format (ISO 8601)",
			rawResponse: `
Domain Name: GOOGLE.COM
Registry Domain ID: 2138514_DOMAIN_COM-VRSN
Registrar WHOIS Server: whois.markmonitor.com
Registrar URL: http://www.markmonitor.com
Updated Date: 2019-09-09T15:39:04Z
Creation Date: 1997-09-15T04:00:00Z
Registry Expiry Date: 2028-09-14T04:00:00Z
Registrar: MarkMonitor Inc.
`,
			expected: func() *time.Time {
				t, _ := time.Parse(time.RFC3339, "2028-09-14T04:00:00Z")
				return &t
			}(),
		},
		{
			name: "PIR ORG format (ISO 8601)",
			rawResponse: `
Domain Name: wikipedia.org
Registry Domain ID: D51687756-LROR
Registrar WHOIS Server: whois.markmonitor.com
Registrar URL: http://www.markmonitor.com
Updated Date: 2023-01-14T11:00:00Z
Creation Date: 2001-01-13T00:12:14Z
Registry Expiry Date: 2024-01-13T00:12:14Z
`,
			expected: func() *time.Time {
				t, _ := time.Parse(time.RFC3339, "2024-01-13T00:12:14Z")
				return &t
			}(),
		},
		{
			name: "UK format (DD-Mon-YYYY)",
			rawResponse: `
    Domain name:
        google.co.uk

    Data validation:
        Nominet was able to match the registrant's name and address against a 3rd party data source on 11-Dec-2012

    Registrar:
        MarkMonitor Inc. [Tag = MARKMONITOR]
        URL: http://www.markmonitor.com

    Relevant dates:
        Registered on: 14-Feb-1999
        Expiry date:  14-Feb-2024
        Last updated:  10-Jan-2023
`,
			expected: func() *time.Time {
				t, _ := time.Parse("02-Jan-2006", "14-Feb-2024")
				return &t
			}(),
		},
		{
			name: "Another format (YYYY-MM-DD)",
			rawResponse: `
domain:       example.ru
nserver:      ns1.example.ru.
nserver:      ns2.example.ru.
state:        REGISTERED, DELEGATED, VERIFIED
org:          Example Ltd
registrar:    RU-CENTER-RU
admin-contact: https://www.nic.ru/whois
created:      2020-09-24T12:00:00Z
paid-till:    2024-09-24T12:00:00Z
free-date:    2024-10-25
source:       TCI
`,
			expected: func() *time.Time {
				t, _ := time.Parse(time.RFC3339, "2024-09-24T12:00:00Z")
				return &t
			}(),
		},
		{
			name: "Format with comments",
			rawResponse: `
Domain Name: example.test
Expiration Date: 2025-05-01 (Auto-Renew)
`,
			expected: func() *time.Time {
				t, _ := time.Parse("2006-01-02", "2025-05-01")
				return &t
			}(),
		},
		{
			name: "No expiry date",
			rawResponse: `
Domain Name: example.local
Some Data: 123
`,
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractExpiry(tt.rawResponse)
			if tt.expected == nil {
				assert.Nil(t, got)
			} else {
				assert.NotNil(t, got)
				if got != nil {
					assert.Equal(t, *tt.expected, *got)
				}
			}
		})
	}
}
