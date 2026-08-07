package integrations

import (
	"fmt"
	"net/url"
	"strings"
)

// validateEgressURL checks the SHAPE of a tenant-supplied custom endpoint URL at
// save/test time: an https scheme and a non-empty host. It deliberately does NOT resolve
// the IP — the actual SSRF boundary (a dial-time connect-IP check) lives in the gateway,
// which is what dials the endpoint at request time and where the connectivity probe now
// runs. This keeps a clearly-bad URL from ever being saved, without a TOCTOU pre-check.
func validateEgressURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return fmt.Errorf("must be a valid URL (e.g. https://host/v1)")
	}
	if u.Scheme != "https" {
		return fmt.Errorf("must use https")
	}
	if u.Hostname() == "" {
		return fmt.Errorf("must include a host")
	}
	return nil
}
