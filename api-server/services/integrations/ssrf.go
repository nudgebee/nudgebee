package integrations

import (
	"fmt"
	"net/url"
	"strings"
)

// validateEgressURL checks the SHAPE of a tenant-supplied custom endpoint URL at save/test
// time: an http/https scheme and a non-empty host. http is allowed because in-cluster
// endpoints (self-hosted vLLM) are commonly plain http; the actual SSRF boundary (a
// dial-time connect-IP check, gated by the operator's private-endpoints opt-in) lives in
// the gateway, which dials the endpoint at request time and where the probe runs. This
// keeps a clearly-bad URL from ever being saved, without a TOCTOU pre-check.
func validateEgressURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return fmt.Errorf("must be a valid URL (e.g. https://host/v1)")
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return fmt.Errorf("must use http or https")
	}
	if u.Hostname() == "" {
		return fmt.Errorf("must include a host")
	}
	return nil
}
