package network

import (
	"strings"
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

// Registry fixtures used by the classification and field-extraction tests.
// Each is trimmed to the lines that matter for the assertion below it.
const (
	rawComRegistered = `
   Domain Name: GOOGLE.COM
   Registry Domain ID: 2138514_DOMAIN_COM-VRSN
   Registrar WHOIS Server: whois.markmonitor.com
   Registrar URL: http://www.markmonitor.com
   Updated Date: 2019-09-09T15:39:04Z
   Creation Date: 1997-09-15T04:00:00Z
   Registry Expiry Date: 2028-09-14T04:00:00Z
   Registrar: MarkMonitor Inc.
   Registrar IANA ID: 292
   Registrar Abuse Contact Email: abusecomplaints@markmonitor.com
   Domain Status: clientDeleteProhibited https://icann.org/epp#clientDeleteProhibited
   Domain Status: clientTransferProhibited https://icann.org/epp#clientTransferProhibited
   Name Server: NS1.GOOGLE.COM
   Name Server: NS2.GOOGLE.COM
   Registrant Country: US
`

	rawUKRegistered = `
    Domain name:
        google.co.uk

    Registrar:
        MarkMonitor Inc. [Tag = MARKMONITOR]
        URL: http://www.markmonitor.com

    Relevant dates:
        Registered on: 14-Feb-1999
        Expiry date:  14-Feb-2024
        Last updated:  10-Jan-2023

    Name servers:
        ns1.google.com
        ns2.google.com
`

	rawComNotFound = `No match for "ASDKJHASD.COM".
>>> Last update of whois database: 2026-08-24T09:25:14Z <<<

NOTICE: The expiration date displayed in this record is the date the
registrar's sponsorship of the domain name registration in the registry is
currently set to expire.
`

	rawUKNotFound = `
    No match for "asdkjhasd.co.uk".

    This domain name has not been registered.
`

	rawIONotFound = `Domain not found.
>>> Last update of WHOIS database: 2026-08-24T09:30:00Z <<<
`

	rawOrgNotFound = `
NOT FOUND
>>> Last update of WHOIS database: 2026-08-24T09:30:00Z <<<
`

	rawDEFree = `
Domain: asdkjhasd.de
Status: free
`

	rawRateLimited = `Your connection limit exceeded. Please slow down and try again later.
`
)

func TestClassify(t *testing.T) {
	tests := []struct {
		name           string
		raw            string
		wantRegistered bool
		wantStatus     string
	}{
		{"com registered", rawComRegistered, true, whoisStatusFound},
		{"co.uk registered", rawUKRegistered, true, whoisStatusFound},
		{"com not found", rawComNotFound, false, whoisStatusNotFound},
		{"co.uk not found", rawUKNotFound, false, whoisStatusNotFound},
		{"io not found", rawIONotFound, false, whoisStatusNotFound},
		{"org not found", rawOrgNotFound, false, whoisStatusNotFound},
		{"de free", rawDEFree, false, whoisStatusNotFound},
		{"rate limited", rawRateLimited, false, whoisStatusRateLimited},
		{"unparseable", "some completely unrelated text\n", false, whoisStatusParseError},
		{"empty", "", false, whoisStatusParseError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registered, status := classify(tt.raw, parseWhois(tt.raw))
			assert.Equal(t, tt.wantStatus, status)
			assert.Equal(t, tt.wantRegistered, registered)
		})
	}
}

// Registries use \r\n on the wire; make sure that does not leak into values.
func TestClassifyHandlesCRLF(t *testing.T) {
	raw := strings.ReplaceAll(rawComRegistered, "\n", "\r\n")
	rec := parseWhois(raw)
	registered, status := classify(raw, rec)

	assert.True(t, registered)
	assert.Equal(t, whoisStatusFound, status)
	assert.Equal(t, "MarkMonitor Inc.", rec.Registrar)
}

func TestParseWhois_EppFormat(t *testing.T) {
	rec := parseWhois(rawComRegistered)

	// The "Registrar:" label must not be satisfied by "Registrar URL:",
	// "Registrar WHOIS Server:" or "Registrar IANA ID:", all of which appear
	// earlier in the response.
	assert.Equal(t, "MarkMonitor Inc.", rec.Registrar)
	assert.Equal(t, "GOOGLE.COM", rec.DomainName)
	assert.Equal(t, "1997-09-15T04:00:00Z", rec.Created)
	assert.Equal(t, "2019-09-09T15:39:04Z", rec.Updated)
	assert.Equal(t, []string{"ns1.google.com", "ns2.google.com"}, rec.Nameservers)
	assert.Len(t, rec.DomainStatus, 2)
	assert.Equal(t, "US", rec.RegistrantCountry)
	assert.NotNil(t, rec.Expiry)
}

// Nominet puts values in an indented block under a bare label line.
func TestParseWhois_NominetBlockFormat(t *testing.T) {
	rec := parseWhois(rawUKRegistered)

	assert.Equal(t, "google.co.uk", rec.DomainName)
	assert.Equal(t, "MarkMonitor Inc. [Tag = MARKMONITOR]", rec.Registrar)
	assert.Equal(t, "1999-02-14T00:00:00Z", rec.Created)
	assert.Equal(t, "2023-01-10T00:00:00Z", rec.Updated)
	assert.Equal(t, []string{"ns1.google.com", "ns2.google.com"}, rec.Nameservers)
	assert.NotNil(t, rec.Expiry)
}

func TestParseWhois_NotFoundHasNoRecord(t *testing.T) {
	for name, raw := range map[string]string{
		"com": rawComNotFound,
		"io":  rawIONotFound,
	} {
		t.Run(name, func(t *testing.T) {
			assert.False(t, parseWhois(raw).hasRecord())
		})
	}
}

// Block values are terminated by the first unindented line. Trailing whitespace
// and CRLF carriage returns must not make such a line look indented.
func TestCollectLabeled_BlockEndsOnUnindentedLine(t *testing.T) {
	raw := "Name servers:\r\n    ns1.example.com\r\n    ns2.example.com\r\nnot-a-nameserver   \r\n"
	rec := parseWhois(raw)

	assert.Equal(t, []string{"ns1.example.com", "ns2.example.com"}, rec.Nameservers)
}
