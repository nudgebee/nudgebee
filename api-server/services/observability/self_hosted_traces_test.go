package observability

import (
	"testing"

	"nudgebee/services/account"
)

func TestIsSelfHostedProvider(t *testing.T) {
	for in, want := range map[string]bool{
		account.AccountProviderSelfHosted: true,
		"selfhosted":                      true,
		"AWS":                             false,
		"GCP":                             false,
		"":                                false,
	} {
		if got := isSelfHostedProvider(in); got != want {
			t.Errorf("isSelfHostedProvider(%q) = %v, want %v", in, got, want)
		}
	}
}
