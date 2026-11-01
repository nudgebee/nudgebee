package signozclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// Alert-rule body generations. SigNoz changed the rule schema underneath an
// unchanged /api/v1/rules route: up to v0.117 a rule's queries live in
// compositeQuery.promQueries / builderQueries ("v4" and older); from v0.118 the
// API validates version == "v5" and reads compositeQuery.queries[], and rejects
// everything else with "alert rule is not valid". Checked in SigNoz's source at
// every release tag from v0.60 to v0.145.
const (
	RuleSchemaV4 = "v4"
	RuleSchemaV5 = "v5"
)

// v5RuleMinVersion is the first release that only accepts v5 rule bodies.
var v5RuleMinVersion = [3]int{0, 118, 0}

// ErrUnknownRuleSchema means the server's release could not be told, so no rule
// body can be built that it is known to accept.
var ErrUnknownRuleSchema = errors.New("could not tell which SigNoz alert-rule format this server accepts")

// RuleSchema reports which alert-rule body the SigNoz behind creds accepts.
//
// The release comes from GET /api/v1/version, which no SigNoz release puts
// behind auth. When that answer is unusable (a proxy in front of SigNoz, a
// trimmed build), a v1 login is the remaining evidence: only releases up to
// ~v0.100 have it, so it proves a v4 server. A v2 login does not settle it —
// v0.100 to v0.117 log in with v2 but still take v4 bodies — so that case is an
// error rather than a guess that would bring back "alert rule is not valid".
func RuleSchema(ctx context.Context, hc *http.Client, creds Credentials) (string, error) {
	if creds.Mode == ModeCloud {
		return RuleSchemaV5, nil
	}
	base := strings.TrimRight(creds.BaseURL, "/")
	version, verr := ServerVersion(ctx, hc, base)
	if v, ok := parseVersion(version); ok {
		if !versionLess(v, v5RuleMinVersion) {
			return RuleSchemaV5, nil
		}
		return RuleSchemaV4, nil
	}
	if !creds.usesAPIKey() {
		if tok, err := Login(ctx, hc, creds); err == nil && tok.Flavor == FlavorV1 {
			return RuleSchemaV4, nil
		}
	}
	if verr == nil {
		verr = fmt.Errorf("unrecognised version %q", version)
	}
	return "", fmt.Errorf("%w: %s/api/v1/version: %v", ErrUnknownRuleSchema, base, verr)
}

// ServerVersion reads the release SigNoz reports at GET /api/v1/version, e.g.
// "v0.145.0".
func ServerVersion(ctx context.Context, hc *http.Client, baseURL string) (string, error) {
	status, body, err := call(ctx, hc, http.MethodGet, strings.TrimRight(baseURL, "/")+"/api/v1/version", nil)
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", status)
	}
	var parsed struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil || parsed.Version == "" {
		return "", errors.New("response is not SigNoz's version JSON")
	}
	return parsed.Version, nil
}

// parseVersion reads "v0.145.0", "0.51.0" or "v0.145.0-rc.1" as major.minor.patch.
func parseVersion(s string) ([3]int, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) < 2 || len(parts) > 3 {
		return [3]int{}, false
	}
	var v [3]int
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return [3]int{}, false
		}
		v[i] = n
	}
	return v, true
}

func versionLess(a, b [3]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}
