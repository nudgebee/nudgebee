package clients

import (
	"strings"

	pagerduty "github.com/PagerDuty/go-pagerduty"
)

const defaultPagerDutyAPIEndpoint = "https://api.pagerduty.com"

func CreatePagerdutyClient(authToken string) *pagerduty.Client {
	client := pagerduty.NewClient(authToken)
	return client
}

// CreatePagerdutyClientWithURL builds a client whose API endpoint honors the
// user-configured URL. Passing the URL through here is what causes a typo in
// the form (e.g. "api.pagerduty.com5") to surface as a connection error at
// validation time instead of being silently ignored by the SDK's hardcoded
// default endpoint.
func CreatePagerdutyClientWithURL(authToken, rawURL string) *pagerduty.Client {
	endpoint := NormalizePagerDutyEndpoint(rawURL)
	return pagerduty.NewClient(authToken, pagerduty.WithAPIEndpoint(endpoint))
}

func NormalizePagerDutyEndpoint(rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return defaultPagerDutyAPIEndpoint
	}
	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		rawURL = "https://" + rawURL
	}
	return strings.TrimRight(rawURL, "/")
}
