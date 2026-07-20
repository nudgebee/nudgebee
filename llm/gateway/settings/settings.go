// Package settings resolves per-tenant data-governance overrides — body capture and
// DLP mode — on the request hot path. It mirrors the routing Store: the OSS build
// carries the seam and an env-only default; the EE build registers a DB-backed
// loader (ee/settings) that hot-reloads llm_gateway_tenant_settings.
//
// Split of responsibility:
//   - Body capture: the env flag gateway_capture_body is the platform CEILING (and
//     metering.BodyLoggingEnabled() also requires a sink). Within that ceiling, a
//     tenant opts IN. OSS has no per-tenant control, so CaptureAllowed defaults to
//     true (env ceiling governs everyone); EE returns the tenant's opt-in (default
//     off — off-until-opt-in).
//   - DLP mode: the env gateway_egress_filter_mode is the DEFAULT; a tenant may
//     override it to anything (including off). OSS returns the env default; EE
//     returns the tenant override when set.
package settings

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"
)

// TenantSettings is one tenant's stored overrides. EgressMode is a pointer so a
// nil (absent) is distinct from an explicit "" (off) — nil inherits the env default.
type TenantSettings struct {
	CaptureBody bool
	EgressMode  *string // nil = inherit env default; "off"|"detect"|"enforce"|"redact" = explicit
}

// LoaderFunc returns the full tenant→settings map. Registered by the EE build.
type LoaderFunc func(ctx context.Context) (map[string]TenantSettings, error)

var registeredLoader atomic.Value // LoaderFunc

// RegisterLoader installs the DB-backed loader (called from ee/settings init()).
func RegisterLoader(f LoaderFunc) { registeredLoader.Store(f) }

// Loader returns the registered loader, or nil (OSS: env-only).
func Loader() LoaderFunc {
	f, _ := registeredLoader.Load().(LoaderFunc)
	return f
}

// Store holds the live per-tenant settings, refreshed on an interval and hot-swapped
// atomically. With no loader (OSS), it holds nothing and every resolver is env-only.
type Store struct {
	load LoaderFunc
	val  atomic.Value // map[string]TenantSettings
	done chan struct{}
}

// NewStore loads once (if a loader is given) then refreshes on interval. A nil loader
// yields an env-only store (no per-tenant overrides).
func NewStore(load LoaderFunc, refresh time.Duration) *Store {
	s := &Store{load: load, done: make(chan struct{})}
	s.val.Store(map[string]TenantSettings{})
	if load != nil {
		s.refresh()
		if refresh <= 0 {
			refresh = 30 * time.Second
		}
		go s.refreshLoop(refresh)
	}
	return s
}

func (s *Store) refreshLoop(every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-s.done:
			return
		case <-t.C:
			s.refresh()
		}
	}
}

func (s *Store) refresh() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	m, err := s.load(ctx)
	if err != nil {
		slog.Error("settings: refresh failed — keeping last-known", "error", err)
		return
	}
	s.val.Store(m)
}

// Close stops the refresh goroutine.
func (s *Store) Close() {
	select {
	case <-s.done:
	default:
		close(s.done)
	}
}

func (s *Store) get(tenantID string) (TenantSettings, bool) {
	m, _ := s.val.Load().(map[string]TenantSettings)
	ts, ok := m[tenantID]
	return ts, ok
}

// CaptureAllowed reports whether this tenant has opted into body capture. Env-only
// (no loader) → always true (the env ceiling governs everyone). With a loader, a
// tenant with no row defaults to false (off-until-opt-in).
func (s *Store) CaptureAllowed(tenantID string) bool {
	if s.load == nil {
		return true
	}
	ts, ok := s.get(tenantID)
	return ok && ts.CaptureBody
}

// EgressMode returns the tenant's DLP mode override, or envDefault when unset.
func (s *Store) EgressMode(tenantID, envDefault string) string {
	if s.load == nil {
		return envDefault
	}
	if ts, ok := s.get(tenantID); ok && ts.EgressMode != nil {
		return *ts.EgressMode
	}
	return envDefault
}

// --- process-wide accessor (set once from main) ---

var active atomic.Value // *Store

// SetActive installs the process store the hot-path accessors read.
func SetActive(s *Store) { active.Store(s) }

func store() *Store {
	s, _ := active.Load().(*Store)
	return s
}

// CaptureAllowed is the hot-path entry point (safe before SetActive: defaults true).
func CaptureAllowed(tenantID string) bool {
	if s := store(); s != nil {
		return s.CaptureAllowed(tenantID)
	}
	return true
}

// EgressMode is the hot-path entry point (safe before SetActive: returns envDefault).
func EgressMode(tenantID, envDefault string) string {
	if s := store(); s != nil {
		return s.EgressMode(tenantID, envDefault)
	}
	return envDefault
}
