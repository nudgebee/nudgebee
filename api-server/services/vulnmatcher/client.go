// Package vulnmatcher is an HTTP client for vuln-matcher-server, which
// matches an OS + package inventory against vulnerability data. That service
// lives in a separate Go module (vuln-matcher-server/), so its wire types are
// mirrored here rather than imported.
package vulnmatcher

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"nudgebee/services/common"
	"nudgebee/services/config"
)

// OS identifies the operating system a package set belongs to. Family is the
// normalized distro id (ubuntu, debian, redhat, amzn, alpine, sles...) — check
// against Capabilities() before calling Match, rather than guessing.
type OS struct {
	Family  string   `json:"family"`
	Version string   `json:"version"`
	IDLike  []string `json:"id_like,omitempty"`
}

// Package type values accepted by vuln-matcher-server.
const (
	PkgTypeDeb = "deb"
	PkgTypeRPM = "rpm"
	PkgTypeAPK = "apk"
)

// Package is one deduplicated package tuple. Key is caller-supplied and
// echoed back on every Finding so results can be mapped back to a host
// without the matcher knowing hosts exist.
type Package struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	Type string `json:"type"`

	// Version is the full version as installed, verbatim. For rpm this is
	// version-release (epoch carried separately); for deb it includes any
	// epoch prefix.
	Version string `json:"version"`
	Arch    string `json:"arch,omitempty"`
	Epoch   *int   `json:"epoch,omitempty"`

	// SourceName is required for deb/rpm packages — advisories are published
	// against the source package, not the binary one. Omitting it silently
	// under-reports findings rather than erroring, which is why the server
	// rejects it with CodeMissingSource instead.
	SourceName    string `json:"source_name,omitempty"`
	SourceVersion string `json:"source_version,omitempty"`

	// ModularityLabel is the RHEL 8/9 AppStream module stream.
	ModularityLabel string `json:"modularity_label,omitempty"`
}

// MatchRequest carries one OS context plus its deduplicated package set.
type MatchRequest struct {
	OS       OS        `json:"os"`
	Packages []Package `json:"packages"`
}

// EPSS is the Exploit Prediction Scoring System entry for a vulnerability.
type EPSS struct {
	Score      float64 `json:"score"`
	Percentile float64 `json:"percentile"`
	Date       string  `json:"date,omitempty"`
}

// Fix state values, mirroring the vendor's own vocabulary.
const (
	FixStateFixed    = "fixed"
	FixStateNotFixed = "not-fixed"
	FixStateWontFix  = "wont-fix"
	FixStateUnknown  = "unknown"
)

// Fix channel values.
const (
	FixChannelStandard = "standard"
	FixChannelESM      = "esm"
	FixChannelVault    = "vault"
)

// Finding is one vulnerability affecting one package tuple.
type Finding struct {
	Key    string `json:"key"`
	VulnID string `json:"vuln_id"`

	FixedVersion string `json:"fixed_version,omitempty"`
	FixState     string `json:"fix_state"`
	FixChannel   string `json:"fix_channel,omitempty"`

	Severity     string   `json:"severity"`
	CVSSv3Score  float64  `json:"cvss_v3_score,omitempty"`
	CVSSv3Vector string   `json:"cvss_v3_vector,omitempty"`
	EPSS         *EPSS    `json:"epss,omitempty"`
	KEV          bool     `json:"kev"`
	Risk         float64  `json:"risk,omitempty"`
	AdvisoryIDs  []string `json:"advisory_ids,omitempty"`
	DataSource   string   `json:"data_source,omitempty"`
	Description  string   `json:"description,omitempty"`
}

// MatchResponse is the result of a match.
type MatchResponse struct {
	DBVersion      string    `json:"db_version"`
	DBBuiltAt      string    `json:"db_built_at"`
	MatcherVersion string    `json:"matcher_version"`
	Findings       []Finding `json:"findings"`

	// SuspectZero marks a non-empty package set that produced no findings at
	// all. Per vuln-matcher-server's contract this is not an error, but
	// callers MUST alarm on it rather than record a clean host — "no
	// vulnerabilities" reads as good news and is exactly the failure mode
	// this service exists to make visible.
	SuspectZero bool `json:"suspect_zero,omitempty"`
}

// SupportedOS is one OS family and the versions the loaded DB actually covers.
type SupportedOS struct {
	Family   string   `json:"family"`
	Versions []string `json:"versions"`
}

// CapabilitiesResponse advertises what the loaded vulnerability database
// actually covers. Use it as an allowlist before calling Match, instead of
// getting back a zero-finding result for a distro the DB has no data on.
type CapabilitiesResponse struct {
	DBVersion string        `json:"db_version"`
	DBBuiltAt string        `json:"db_built_at"`
	Supported []SupportedOS `json:"supported"`
}

// Error codes returned with HTTP 422. Each corresponds to an input defect
// that would otherwise produce a confident, wrong, empty answer.
const (
	CodeUnknownOSFamily  = "unknown_os_family"
	CodeUnknownOSVersion = "unknown_os_version"
	CodeUnsupportedOS    = "unsupported_os"
	CodeMissingSource    = "missing_source_name"
	CodeEmptyPackages    = "empty_packages"
	CodeInvalidPackage   = "invalid_package"
)

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// MatchError wraps a 422 response from vuln-matcher-server so callers can
// branch on Code (e.g. fail fast on CodeUnsupportedOS) instead of parsing
// error strings.
type MatchError struct {
	Code    string
	Message string
}

func (e *MatchError) Error() string {
	return fmt.Sprintf("vuln-matcher: %s: %s", e.Code, e.Message)
}

const defaultTimeout = 60 * time.Second

// Match calls vuln-matcher-server's POST /v1/match. Callers must dedupe
// packages first and populate SourceName for every deb/rpm package, or the
// call fails with a MatchError{Code: CodeMissingSource}.
func Match(req MatchRequest) (MatchResponse, error) {
	var out MatchResponse

	resp, err := common.HttpPost(
		fmt.Sprintf("%s/v1/match", config.Config.VulnMatcherServerEndpoint),
		common.HttpWithHeaders(map[string]string{
			"Content-Type": "application/json",
			"Accept":       "application/json",
		}),
		common.HttpWithJsonBody(req),
		common.HttpWithTimeout(defaultTimeout),
	)
	if err != nil {
		return out, fmt.Errorf("vuln-matcher: match request failed: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			slog.Warn("vuln-matcher: error closing match response body", "error", closeErr)
		}
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return out, fmt.Errorf("vuln-matcher: reading match response: %w", err)
	}

	if resp.StatusCode == http.StatusUnprocessableEntity {
		var apiErr apiError
		if jsonErr := json.Unmarshal(body, &apiErr); jsonErr == nil && apiErr.Code != "" {
			return out, &MatchError{Code: apiErr.Code, Message: apiErr.Message}
		}
		return out, fmt.Errorf("vuln-matcher: match returned 422: %s", string(body))
	}

	if resp.StatusCode != http.StatusOK {
		return out, fmt.Errorf("vuln-matcher: match returned status %d: %s", resp.StatusCode, string(body))
	}

	if err := json.Unmarshal(body, &out); err != nil {
		return out, fmt.Errorf("vuln-matcher: parsing match response: %w", err)
	}
	return out, nil
}

// Capabilities calls GET /v1/capabilities so callers can fail fast on an OS
// the loaded database has no data for, instead of a 422 from Match.
func Capabilities() (CapabilitiesResponse, error) {
	var out CapabilitiesResponse

	resp, err := common.HttpGet(
		fmt.Sprintf("%s/v1/capabilities", config.Config.VulnMatcherServerEndpoint),
		common.HttpWithTimeout(defaultTimeout),
	)
	if err != nil {
		return out, fmt.Errorf("vuln-matcher: capabilities request failed: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			slog.Warn("vuln-matcher: error closing capabilities response body", "error", closeErr)
		}
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return out, fmt.Errorf("vuln-matcher: reading capabilities response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return out, fmt.Errorf("vuln-matcher: capabilities returned status %d: %s", resp.StatusCode, string(body))
	}

	if err := json.Unmarshal(body, &out); err != nil {
		return out, fmt.Errorf("vuln-matcher: parsing capabilities response: %w", err)
	}
	return out, nil
}

// SupportsOS reports whether cap advertises coverage for the given family +
// version. Comparison is case-insensitive on family; version must match
// exactly, per vuln-matcher-server's own reporting.
func (c CapabilitiesResponse) SupportsOS(family, version string) bool {
	for _, s := range c.Supported {
		if !strings.EqualFold(s.Family, family) {
			continue
		}
		for _, v := range s.Versions {
			if v == version || versionsEqual(v, version) {
				return true
			}
		}
	}
	return false
}

// versionsEqual compares dot-separated version strings numeric-segment by
// numeric-segment, so "22.04" (os-release's VERSION_ID, zero-padded month)
// matches "22.4" (how vuln-matcher-server's capabilities list Ubuntu's
// minor version) — verified against a live GET /v1/capabilities response,
// where /v1/match itself already accepts "22.04" directly but the
// capabilities listing does not zero-pad.
func versionsEqual(a, b string) bool {
	as := strings.Split(a, ".")
	bs := strings.Split(b, ".")
	if len(as) != len(bs) {
		return false
	}
	for i := range as {
		an, aErr := strconv.Atoi(as[i])
		bn, bErr := strconv.Atoi(bs[i])
		if aErr != nil || bErr != nil {
			if as[i] != bs[i] {
				return false
			}
			continue
		}
		if an != bn {
			return false
		}
	}
	return true
}
