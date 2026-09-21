package aws

import (
	"testing"

	"nudgebee/collector/cloud/providers"
)

func accountWithData(data string) providers.Account {
	return providers.Account{Data: &data}
}

func TestIsValidAWSRegion(t *testing.T) {
	valid := []string{"ap-southeast-2", "us-east-1", "eu-central-1", "us-gov-east-1", "ap-southeast-4"}
	for _, r := range valid {
		if !IsValidAWSRegion(r) {
			t.Errorf("expected %q to be a valid region", r)
		}
	}

	// A typo must not pass: an unrecognised region yields an empty crawl that the
	// zero-regions archival safeguard reports as "refusing to archive", which
	// looks healthy while collecting nothing.
	invalid := []string{"ap-southeast2", "apsoutheast-2", "AP-SOUTHEAST-2", "ap-southeast-", "global", "", "  "}
	for _, r := range invalid {
		if IsValidAWSRegion(r) {
			t.Errorf("expected %q to be rejected", r)
		}
	}
}

func TestConfiguredRegions(t *testing.T) {
	tests := []struct {
		name    string
		account providers.Account
		want    []string
	}{
		{
			name:    "nil data means discover",
			account: providers.Account{},
			want:    nil,
		},
		{
			name:    "empty data means discover",
			account: accountWithData(""),
			want:    nil,
		},
		{
			name:    "absent key means discover",
			account: accountWithData(`{"cost_report_name":"nudgebeeReport"}`),
			want:    nil,
		},
		{
			// An empty list must mean "discover regions", never "no regions" —
			// the latter collects nothing while the account looks healthy.
			name:    "empty list means discover",
			account: accountWithData(`{"regions":[]}`),
			want:    nil,
		},
		{
			name:    "single region",
			account: accountWithData(`{"regions":["ap-southeast-2"]}`),
			want:    []string{"ap-southeast-2"},
		},
		{
			name:    "multiple regions preserve order",
			account: accountWithData(`{"regions":["ap-southeast-2","us-east-1"]}`),
			want:    []string{"ap-southeast-2", "us-east-1"},
		},
		{
			name:    "duplicates collapse",
			account: accountWithData(`{"regions":["ap-southeast-2","ap-southeast-2"]}`),
			want:    []string{"ap-southeast-2"},
		},
		{
			name:    "whitespace trimmed",
			account: accountWithData(`{"regions":[" ap-southeast-2 "]}`),
			want:    []string{"ap-southeast-2"},
		},
		{
			name:    "malformed entries dropped",
			account: accountWithData(`{"regions":["ap-southeast-2","ap-southeast2",""]}`),
			want:    []string{"ap-southeast-2"},
		},
		{
			// Every entry invalid is indistinguishable from "no usable list", so
			// fall back to discovery rather than crawling nothing.
			name:    "all entries invalid falls back to discover",
			account: accountWithData(`{"regions":["nonsense"]}`),
			want:    nil,
		},
		{
			name:    "non-string entries ignored",
			account: accountWithData(`{"regions":["ap-southeast-2",7,null]}`),
			want:    []string{"ap-southeast-2"},
		},
		{
			name:    "wrong type means discover",
			account: accountWithData(`{"regions":"ap-southeast-2"}`),
			want:    nil,
		},
		{
			name:    "unparseable data means discover",
			account: accountWithData(`{not json`),
			want:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := configuredRegions(tt.account)
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("got %v, want %v", got, tt.want)
				}
			}
		})
	}
}
