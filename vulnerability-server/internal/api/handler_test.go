package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeMatcher stands in for the engine so the guardrails can be tested without
// a vulnerability database.
type fakeMatcher struct {
	findings  []Finding
	err       error
	supported []SupportedOS
	built     time.Time
	version   string
}

func (f *fakeMatcher) Match(OS, []Package) ([]Finding, error) { return f.findings, f.err }
func (f *fakeMatcher) DBVersion() string {
	if f.version == "" {
		return "v6.1.9"
	}
	return f.version
}
func (f *fakeMatcher) DBBuiltAt() time.Time       { return f.built }
func (f *fakeMatcher) SupportedOS() []SupportedOS { return f.supported }

func newTestHandler(m *fakeMatcher) *Handler {
	if m.supported == nil {
		m.supported = []SupportedOS{{Family: "ubuntu", Versions: []string{"22.04"}}}
	}
	if m.built.IsZero() {
		m.built = time.Now()
	}
	return NewHandler(m, 7*24*time.Hour, "test")
}

func post(t *testing.T, h *Handler, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/match", bytes.NewReader(b))
	rec := httptest.NewRecorder()
	mux := http.NewServeMux()
	h.Routes(mux)
	mux.ServeHTTP(rec, req)
	return rec
}

func validPackage() Package {
	return Package{
		Key:        "t1",
		Name:       "libssl3",
		Type:       PkgTypeDeb,
		Version:    "3.0.2-0ubuntu1.6",
		Arch:       "amd64",
		SourceName: "openssl",
	}
}

func validRequest() MatchRequest {
	return MatchRequest{
		OS:       OS{Family: "ubuntu", Version: "22.04"},
		Packages: []Package{validPackage()},
	}
}

// The guardrails below all defend the same failure: input defects that make the
// matcher return nothing, which reads as "this host is clean".

func TestMatch_RejectsMissingSourceName(t *testing.T) {
	h := newTestHandler(&fakeMatcher{})
	req := validRequest()
	req.Packages[0].SourceName = ""

	rec := post(t, h, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %d (%s)", rec.Code, rec.Body.String())
	}
	var e Error
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if e.Code != CodeMissingSource {
		t.Errorf("want code %q, got %q", CodeMissingSource, e.Code)
	}
}

func TestMatch_AllowsMissingSourceNameForApk(t *testing.T) {
	// apk carries its origin inside the package database, so there is no
	// separate source field to require.
	h := newTestHandler(&fakeMatcher{supported: []SupportedOS{{Family: "alpine", Versions: []string{"3.20"}}}})
	req := MatchRequest{
		OS: OS{Family: "alpine", Version: "3.20"},
		Packages: []Package{{
			Key: "t1", Name: "busybox", Type: PkgTypeAPK, Version: "1.36.1-r29",
		}},
	}

	if rec := post(t, h, req); rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestMatch_RejectsUnknownOSFamily(t *testing.T) {
	h := newTestHandler(&fakeMatcher{})
	req := validRequest()
	req.OS.Family = ""

	rec := post(t, h, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %d", rec.Code)
	}
}

func TestMatch_RejectsOSOutsideDatabaseCoverage(t *testing.T) {
	// A distro the database has no data for returns zero findings and looks
	// clean. It must be refused instead.
	h := newTestHandler(&fakeMatcher{
		supported: []SupportedOS{{Family: "ubuntu", Versions: []string{"22.04"}}},
	})
	req := validRequest()
	req.OS = OS{Family: "opensuse-leap", Version: "15.5"}

	rec := post(t, h, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422 for uncovered OS, got %d (%s)", rec.Code, rec.Body.String())
	}
	var e Error
	_ = json.Unmarshal(rec.Body.Bytes(), &e)
	if e.Code != CodeUnsupportedOS {
		t.Errorf("want %q, got %q", CodeUnsupportedOS, e.Code)
	}
}

func TestMatch_AcceptsMajorVersionOnlyCoverage(t *testing.T) {
	// Advisory data is often keyed on the major alone (redhat 9 covers 9.4).
	h := newTestHandler(&fakeMatcher{
		supported: []SupportedOS{{Family: "redhat", Versions: []string{"9"}}},
	})
	req := MatchRequest{
		OS: OS{Family: "redhat", Version: "9.4"},
		Packages: []Package{{
			Key: "t1", Name: "openssl-libs", Type: PkgTypeRPM,
			Version: "3.0.7-24.el9", SourceName: "openssl",
		}},
	}

	if rec := post(t, h, req); rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// Rolling releases carry no version at all, and the database holds real data
// for them — chainguard alone has hundreds of thousands of advisory rows. A
// family reported with an empty version set means "any version is covered", so
// a request without a version must be answered, not refused.
func TestMatch_AcceptsVersionlessRollingDistro(t *testing.T) {
	h := newTestHandler(&fakeMatcher{
		supported: []SupportedOS{{Family: "wolfi"}},
	})
	req := MatchRequest{
		OS: OS{Family: "wolfi"},
		Packages: []Package{{
			Key: "t1", Name: "openssl", Type: PkgTypeAPK, Version: "3.3.2-r0",
		}},
	}

	if rec := post(t, h, req); rec.Code != http.StatusOK {
		t.Fatalf("want 200 for a versionless rolling distro, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// A versioned family with no version supplied is a different problem from an
// uncovered OS, and saying "unsupported os" would send the caller looking in
// the wrong place.
func TestMatch_MissingVersionOnVersionedFamilyIsItsOwnError(t *testing.T) {
	h := newTestHandler(&fakeMatcher{
		supported: []SupportedOS{{Family: "ubuntu", Versions: []string{"22.04"}}},
	})
	req := validRequest()
	req.OS.Version = ""

	rec := post(t, h, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %d", rec.Code)
	}
	var e Error
	_ = json.Unmarshal(rec.Body.Bytes(), &e)
	if e.Code != CodeUnknownOSVersion {
		t.Errorf("want %q, got %q (%s)", CodeUnknownOSVersion, e.Code, e.Message)
	}
}

// An unknown family and a known family at an uncovered version are distinct
// failures and should not collapse into the same message.
func TestMatch_UnknownFamilyReportsFamilyNotVersion(t *testing.T) {
	h := newTestHandler(&fakeMatcher{
		supported: []SupportedOS{{Family: "ubuntu", Versions: []string{"22.04"}}},
	})
	req := validRequest()
	req.OS = OS{Family: "plan9", Version: "4"}

	rec := post(t, h, req)

	var e Error
	_ = json.Unmarshal(rec.Body.Bytes(), &e)
	if rec.Code != http.StatusUnprocessableEntity || e.Code != CodeUnsupportedOS {
		t.Fatalf("want 422/%s, got %d/%s", CodeUnsupportedOS, rec.Code, e.Code)
	}
	if !strings.Contains(e.Message, "family") {
		t.Errorf("message should name the family as the problem, got: %s", e.Message)
	}
}

// An unbounded request body would let one caller exhaust the service's memory.
func TestMatch_RejectsOversizedBody(t *testing.T) {
	h := newTestHandler(&fakeMatcher{})

	huge := bytes.Repeat([]byte("a"), maxRequestBytes+1024)
	req := httptest.NewRequest(http.MethodPost, "/v1/match", bytes.NewReader(huge))
	rec := httptest.NewRecorder()
	mux := http.NewServeMux()
	h.Routes(mux)
	mux.ServeHTTP(rec, req)

	if rec.Code == http.StatusOK {
		t.Fatal("an oversized body must not be accepted")
	}
}

func TestMatch_FlagsSuspectZero(t *testing.T) {
	// Zero findings for a real package set is not an error, but it must be
	// visible so the caller alarms rather than recording a clean host.
	h := newTestHandler(&fakeMatcher{findings: nil})

	rec := post(t, h, validRequest())

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	var resp MatchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.SuspectZero {
		t.Error("want suspect_zero=true when a non-empty package set matched nothing")
	}
}

func TestMatch_DoesNotFlagSuspectZeroWhenFindingsExist(t *testing.T) {
	h := newTestHandler(&fakeMatcher{
		findings: []Finding{{Key: "t1", VulnID: "CVE-2024-6119", FixState: FixStateFixed}},
	})

	rec := post(t, h, validRequest())

	var resp MatchResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.SuspectZero {
		t.Error("suspect_zero must be false when findings were returned")
	}
	if resp.DBVersion == "" {
		t.Error("response must pin the db_version that produced it")
	}
}

func TestMatch_RejectsEmptyPackageSet(t *testing.T) {
	h := newTestHandler(&fakeMatcher{})
	req := validRequest()
	req.Packages = nil

	if rec := post(t, h, req); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %d", rec.Code)
	}
}

func TestReadyz_UnreadyWhenDatabaseStale(t *testing.T) {
	// A stale database still answers, but drifts toward reporting nothing. An
	// air-gapped install that stopped receiving updates must not look healthy.
	m := &fakeMatcher{built: time.Now().Add(-30 * 24 * time.Hour)}
	m.supported = []SupportedOS{{Family: "ubuntu", Versions: []string{"22.04"}}}
	h := NewHandler(m, 7*24*time.Hour, "test")

	mux := http.NewServeMux()
	h.Routes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503 for a stale database, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestHealthz_IgnoresDatabaseState(t *testing.T) {
	// Liveness must not depend on the database, or a slow first download
	// restarts the pod in a loop.
	m := &fakeMatcher{built: time.Time{}, version: ""}
	m.supported = []SupportedOS{}
	h := NewHandler(m, time.Hour, "test")

	mux := http.NewServeMux()
	h.Routes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("healthz must stay 200 while the database is unloaded, got %d", rec.Code)
	}
}

func TestCapabilities_ReportsDatabaseCoverage(t *testing.T) {
	h := newTestHandler(&fakeMatcher{
		supported: []SupportedOS{
			{Family: "ubuntu", Versions: []string{"22.04", "24.04"}},
			{Family: "redhat", Versions: []string{"9", "10"}},
		},
	})

	mux := http.NewServeMux()
	h.Routes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/capabilities", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	var resp CapabilitiesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Supported) != 2 {
		t.Fatalf("want 2 families, got %d", len(resp.Supported))
	}
	if resp.DBVersion == "" {
		t.Error("capabilities must report the database version backing them")
	}
}
