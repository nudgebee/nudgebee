package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// Matcher is the engine behind the API. Keeping this an interface is what lets
// the Windows (MSRC) path land later without touching handlers or callers.
type Matcher interface {
	Match(os OS, pkgs []Package) ([]Finding, error)
	DBVersion() string
	DBBuiltAt() time.Time
	// SupportedOS reports what the loaded database actually covers. Callers use
	// it as an allowlist so an unsupported distro is refused rather than
	// silently returning no findings.
	SupportedOS() []SupportedOS
}

// Handler serves the matcher API.
type Handler struct {
	m Matcher
	// MaxDBAge marks the service unready once the loaded database is older than
	// this. A stale database still answers, but silently degrades toward
	// reporting nothing, so it must be visible.
	MaxDBAge time.Duration
	// Version identifies the engine build, echoed in responses.
	Version string
}

// NewHandler builds the HTTP handler.
func NewHandler(m Matcher, maxDBAge time.Duration, version string) *Handler {
	return &Handler{m: m, MaxDBAge: maxDBAge, Version: version}
}

// Routes registers all endpoints.
func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/match", h.handleMatch)
	mux.HandleFunc("GET /v1/capabilities", h.handleCapabilities)
	mux.HandleFunc("GET /healthz", h.handleHealthz)
	mux.HandleFunc("GET /readyz", h.handleReadyz)
}

// maxRequestBytes caps the request body. A deduplicated package set is small —
// a 400-host fleet collapses to roughly 15k tuples, a few MB — so this is well
// clear of legitimate traffic while stopping an unbounded body from exhausting
// memory.
const maxRequestBytes = 32 << 20

func (h *Handler) handleMatch(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)

	var req MatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", "malformed JSON body")
		return
	}

	if code, msg := h.validate(req); code != "" {
		writeErr(w, http.StatusUnprocessableEntity, code, msg)
		return
	}

	findings, err := h.m.Match(req.OS, req.Packages)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "match_failed", err.Error())
		return
	}

	resp := MatchResponse{
		DBVersion:      h.m.DBVersion(),
		DBBuiltAt:      h.m.DBBuiltAt().UTC().Format(time.RFC3339),
		MatcherVersion: h.Version,
		Findings:       findings,
		// A non-empty package set that matched nothing is not an error, but it
		// is indistinguishable from a healthy host at a glance. Flag it so the
		// caller alarms instead of recording a clean result.
		SuspectZero: len(findings) == 0 && len(req.Packages) > 0,
	}
	writeJSON(w, http.StatusOK, resp)
}

// validate enforces the input guardrails. Each rejected case is one that would
// otherwise return a confident, plausible, empty result.
func (h *Handler) validate(req MatchRequest) (code, msg string) {
	if strings.TrimSpace(req.OS.Family) == "" {
		return CodeUnknownOSFamily, "os.family is required"
	}
	if len(req.Packages) == 0 {
		return CodeEmptyPackages, "packages must not be empty"
	}

	// The OS must be one the loaded database actually has data for. Without
	// this check an unsupported distro returns zero findings and looks clean.
	family, versioned := h.familyCoverage(req.OS.Family)
	switch {
	case family == nil:
		return CodeUnsupportedOS, "os family " + req.OS.Family +
			" is not covered by the loaded vulnerability database"
	// Rolling releases (wolfi, chainguard, arch) legitimately have no version,
	// so a version is required only where the family is actually versioned.
	// Reporting "unsupported os" for a missing version would point at the
	// wrong problem entirely.
	case versioned && strings.TrimSpace(req.OS.Version) == "":
		return CodeUnknownOSVersion, "os.version is required for " + req.OS.Family
	case versioned && !versionCovered(family, req.OS.Version):
		return CodeUnsupportedOS, "os " + req.OS.Family + " " + req.OS.Version +
			" is not covered by the loaded vulnerability database"
	}

	for _, p := range req.Packages {
		if strings.TrimSpace(p.Key) == "" {
			return CodeInvalidPackage, "every package needs a key"
		}
		if strings.TrimSpace(p.Name) == "" || strings.TrimSpace(p.Version) == "" {
			return CodeInvalidPackage, "package " + p.Key + ": name and version are required"
		}
		switch p.Type {
		case PkgTypeDeb, PkgTypeRPM, PkgTypeAPK:
		default:
			return CodeInvalidPackage, "package " + p.Key + ": unsupported type " + p.Type
		}
		// Advisories are published against the source package. Omitting it does
		// not error inside the matcher, it just quietly matches far less: on a
		// real host, supplying it took findings from 9 to 31. Refusing the input
		// is the only way that stays visible.
		if p.Type != PkgTypeAPK && strings.TrimSpace(p.SourceName) == "" {
			return CodeMissingSource, "package " + p.Key +
				": source_name is required for " + p.Type + " packages"
		}
	}
	return "", ""
}

// familyCoverage finds what the loaded database knows about an OS family. The
// second return reports whether that family is versioned at all: a family with
// no versions is a rolling release, where any version — including none — is
// covered.
func (h *Handler) familyCoverage(name string) (*SupportedOS, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	for i, s := range h.m.SupportedOS() {
		if strings.EqualFold(s.Family, name) {
			return &h.m.SupportedOS()[i], len(s.Versions) > 0
		}
	}
	return nil, false
}

func versionCovered(s *SupportedOS, version string) bool {
	version = strings.TrimSpace(version)
	for _, v := range s.Versions {
		// Match either the full version (22.04) or its major (9 for 9.4),
		// since advisory data is often keyed on the major alone.
		if v == version || v == majorOf(version) {
			return true
		}
	}
	return false
}

func majorOf(version string) string {
	if i := strings.IndexByte(version, '.'); i > 0 {
		return version[:i]
	}
	return version
}

func (h *Handler) handleCapabilities(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, CapabilitiesResponse{
		DBVersion: h.m.DBVersion(),
		DBBuiltAt: h.m.DBBuiltAt().UTC().Format(time.RFC3339),
		Supported: h.m.SupportedOS(),
	})
}

// handleHealthz is liveness only. It deliberately does not consult the database:
// a slow first download would otherwise restart the pod in a loop.
func (h *Handler) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleReadyz gates traffic on a database that is both loaded and fresh.
func (h *Handler) handleReadyz(w http.ResponseWriter, _ *http.Request) {
	built := h.m.DBBuiltAt()
	if built.IsZero() || h.m.DBVersion() == "" {
		writeErr(w, http.StatusServiceUnavailable, "db_not_loaded", "vulnerability database not loaded")
		return
	}
	if h.MaxDBAge > 0 && time.Since(built) > h.MaxDBAge {
		writeErr(w, http.StatusServiceUnavailable, "db_stale",
			"vulnerability database built "+built.UTC().Format(time.RFC3339)+" is older than the allowed age")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"status":      "ready",
		"db_version":  h.m.DBVersion(),
		"db_built_at": built.UTC().Format(time.RFC3339),
	})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, Error{Code: code, Message: msg})
}
