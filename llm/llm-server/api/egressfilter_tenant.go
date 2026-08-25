package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"nudgebee/llm/security/egressfilter"
)

// Admin API for per-tenant egressfilter overrides. CRUD on a single table
// (public.llm_egressfilter_tenant_config). Auth: global authHandlerMiddleware
// from cmd/main.go — no per-route guard.
//
// Stage 2a scope: mode, enabled, allowlist, disabled_rules. custom_rules
// is a schema-reserved column with no admin-API surface yet (per the
// 2026-06-29 data study — no evidence of need today; adding API surface
// before need risks shipping a foot-gun for ops to misuse).
//
// All write paths invalidate the local process's tenant-config cache so
// the next Resolve sees fresh data. Cross-pod invalidation is Stage 2c
// (RabbitMQ broadcast); in 2a, other pods carry stale data for up to
// SetTenantConfigTTL (default 5m).

const (
	maxAllowlistEntries          = 500
	maxDisabledRulesEntries      = 100
	maxPIIDisabledCategoryValues = 8 // hard cap; the ml-k8s token namespace has ~4 today
)

// validPIICategories is the closed set the ml-k8s /scrub API emits today
// (see ml-k8s-server/server/ee/scrubbing/scrubber.py). Admin API rejects
// anything outside this set so ops can't silently push a value the
// scrubber will never produce. Upper-cased for case-insensitive matching.
var validPIICategories = map[string]struct{}{
	"EMAIL":    {},
	"PERSON":   {},
	"PHONE":    {},
	"LOCATION": {},
}

// DAO function variables — set to the egressfilter package functions at
// init() time; tests overwrite them with stubs so the handlers can be
// exercised without a live DB. Don't rebind these from production code.
var (
	daoGetTenantConfig    = egressfilter.GetTenantConfigByID
	daoUpsertTenantConfig = egressfilter.UpsertTenantConfig
	daoDeleteTenantConfig = egressfilter.DeleteTenantConfig
	daoListTenantConfigs  = egressfilter.ListTenantConfigs
)

// handleEgressfilterTenantApis registers the per-tenant config endpoints.
// Called from cmd/main.go after the gin router is constructed.
func handleEgressfilterTenantApis(r *gin.Engine) {
	g := r.Group("/api/admin/egressfilter/tenant")
	g.GET("/:tenant_id", getTenantConfig)
	g.PUT("/:tenant_id", upsertTenantConfig)
	g.PATCH("/:tenant_id", patchTenantConfig)
	g.DELETE("/:tenant_id", deleteTenantConfig)

	// Debug list — limited surface for ops triage. Not part of the
	// per-tenant CRUD set above.
	r.GET("/api/admin/egressfilter/tenants", listTenantConfigs)
}

// --- Read --------------------------------------------------------------------

func getTenantConfig(c *gin.Context) {
	tenantID, ok := parseTenantID(c)
	if !ok {
		return
	}
	cfg, err := daoGetTenantConfig(c.Request.Context(), tenantID)
	if err != nil {
		slog.Error("egressfilter admin: get failed", "tenant_id", tenantID, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if cfg == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "no per-tenant override exists; env defaults apply"})
		return
	}
	c.JSON(http.StatusOK, configResponse(cfg))
}

func listTenantConfigs(c *gin.Context) {
	cfgs, err := daoListTenantConfigs(c.Request.Context(), 100)
	if err != nil {
		slog.Error("egressfilter admin: list failed", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	out := make([]gin.H, 0, len(cfgs))
	for i := range cfgs {
		out = append(out, configResponse(&cfgs[i]))
	}
	c.JSON(http.StatusOK, gin.H{"items": out, "count": len(out)})
}

// --- Write -------------------------------------------------------------------

type upsertBody struct {
	Mode          *string  `json:"mode,omitempty"`
	Enabled       *bool    `json:"enabled,omitempty"`
	Allowlist     []string `json:"allowlist,omitempty"`
	DisabledRules []string `json:"disabled_rules,omitempty"`
	// PII sibling detector (V827). All fields optional; omitting them
	// leaves the tenant's existing PII posture untouched (nil-ish values
	// mean "inherit env" on the wrapper side).
	PIIEnabled            *bool     `json:"pii_enabled,omitempty"`
	PIIMode               *string   `json:"pii_mode,omitempty"`
	PIINerEnabled         *bool     `json:"pii_ner_enabled,omitempty"`
	PIIDisabledCategories *[]string `json:"pii_disabled_categories,omitempty"`
}

func upsertTenantConfig(c *gin.Context) {
	tenantID, ok := parseTenantID(c)
	if !ok {
		return
	}
	var body upsertBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON: " + err.Error()})
		return
	}

	cfg := &egressfilter.TenantConfig{
		TenantID:      tenantID,
		Mode:          egressfilter.ModeDetect, // default
		Enabled:       true,
		Allowlist:     cleanStrings(body.Allowlist),
		DisabledRules: cleanStrings(body.DisabledRules),
	}
	if body.Mode != nil {
		mode, errResp := parseMode(*body.Mode)
		if errResp != "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": errResp})
			return
		}
		cfg.Mode = mode
	}
	if body.Enabled != nil {
		cfg.Enabled = *body.Enabled
	}
	if errResp := applyPIIToConfig(cfg, body.PIIEnabled, body.PIIMode, body.PIINerEnabled, body.PIIDisabledCategories); errResp != "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": errResp})
		return
	}
	if err := validateBudgets(cfg); err != "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": err})
		return
	}

	if err := daoUpsertTenantConfig(c.Request.Context(), cfg); err != nil {
		slog.Error("egressfilter admin: upsert failed", "tenant_id", tenantID, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	egressfilter.InvalidateTenantConfig(tenantID)
	c.JSON(http.StatusOK, configResponse(cfg))
}

type patchBody struct {
	Mode    *string `json:"mode,omitempty"`
	Enabled *bool   `json:"enabled,omitempty"`
	// Additive operations on the array-shaped columns. *_add and *_remove
	// arrive simultaneously: server applies removes first, then adds, so the
	// caller doesn't have to think about ordering.
	AllowlistAdd        []string `json:"allowlist_add,omitempty"`
	AllowlistRemove     []string `json:"allowlist_remove,omitempty"`
	DisabledRulesAdd    []string `json:"disabled_rules_add,omitempty"`
	DisabledRulesRemove []string `json:"disabled_rules_remove,omitempty"`
	// PII sibling detector (V827). PATCH tri-state semantics via IsPresent:
	//   - field absent            → leave existing DB value
	//   - field present, non-null → replace with value
	//   - field present, null     → clear back to "inherit env"
	// Absent-vs-null cannot be told apart from a nil *bool alone (json.Unmarshal
	// zeroes both), so the custom UnmarshalJSON records the top-level key set
	// and IsPresent(name) reads from it. pii_disabled_categories is a set-
	// replace (not add/remove) — the set is small (max 4) so replace matches
	// how the UI renders it (checkbox group).
	PIIEnabled            *bool     `json:"pii_enabled,omitempty"`
	PIIMode               *string   `json:"pii_mode,omitempty"`
	PIINerEnabled         *bool     `json:"pii_ner_enabled,omitempty"`
	PIIDisabledCategories *[]string `json:"pii_disabled_categories,omitempty"`

	presentKeys map[string]struct{} `json:"-"`
}

// UnmarshalJSON captures the set of top-level keys the caller actually
// included in the request body BEFORE decoding into the typed fields.
// This is the only way to distinguish `{"pii_enabled": null}` (clear)
// from `{}` (leave untouched) — both produce a nil *bool otherwise.
//
// json.Unmarshal into a raw map first, then into the typed alias to avoid
// infinite recursion via UnmarshalJSON. presentKeys is populated on the
// receiver so handler code can query `body.IsPresent("pii_mode")`.
func (p *patchBody) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	present := make(map[string]struct{}, len(raw))
	for k := range raw {
		present[k] = struct{}{}
	}
	type alias patchBody
	var tmp alias
	if err := json.Unmarshal(data, &tmp); err != nil {
		return err
	}
	*p = patchBody(tmp)
	p.presentKeys = present
	return nil
}

// IsPresent reports whether the caller included this top-level key in the
// PATCH body. Used to implement tri-state semantics (present-null vs absent)
// for the nullable PII fields.
func (p *patchBody) IsPresent(key string) bool {
	_, ok := p.presentKeys[key]
	return ok
}

func patchTenantConfig(c *gin.Context) {
	tenantID, ok := parseTenantID(c)
	if !ok {
		return
	}
	var body patchBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON: " + err.Error()})
		return
	}

	// Read-modify-write. Read the current row (nil → start from defaults).
	cfg, err := daoGetTenantConfig(c.Request.Context(), tenantID)
	if err != nil {
		slog.Error("egressfilter admin: patch read failed", "tenant_id", tenantID, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if cfg == nil {
		cfg = &egressfilter.TenantConfig{
			TenantID: tenantID,
			Mode:     egressfilter.ModeDetect,
			Enabled:  true,
		}
	}

	if body.Mode != nil {
		mode, errResp := parseMode(*body.Mode)
		if errResp != "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": errResp})
			return
		}
		cfg.Mode = mode
	}
	if body.Enabled != nil {
		cfg.Enabled = *body.Enabled
	}
	cfg.Allowlist = applyAddRemove(cfg.Allowlist, body.AllowlistAdd, body.AllowlistRemove)
	cfg.DisabledRules = applyAddRemove(cfg.DisabledRules, body.DisabledRulesAdd, body.DisabledRulesRemove)

	// PATCH tri-state semantics: absent = leave; present-value = set;
	// present-null = clear back to "inherit env". applyPIIToConfig can't
	// tell absent from null so we inline the per-field logic here.
	if body.IsPresent("pii_enabled") {
		cfg.PIIEnabled = body.PIIEnabled // nil = clear
	}
	if body.IsPresent("pii_ner_enabled") {
		cfg.PIINerEnabled = body.PIINerEnabled // nil = clear
	}
	if body.IsPresent("pii_mode") {
		if body.PIIMode == nil {
			cfg.PIIMode = "" // clear
		} else {
			normalized, errMsg := parsePIIMode(*body.PIIMode)
			if errMsg != "" {
				c.JSON(http.StatusBadRequest, gin.H{"error": errMsg})
				return
			}
			cfg.PIIMode = normalized
		}
	}
	if body.IsPresent("pii_disabled_categories") {
		if body.PIIDisabledCategories == nil {
			cfg.PIIDisabledCategories = nil // clear (DAO coerces to '{}')
		} else {
			normalized, errMsg := validatePIICategories(*body.PIIDisabledCategories)
			if errMsg != "" {
				c.JSON(http.StatusBadRequest, gin.H{"error": errMsg})
				return
			}
			cfg.PIIDisabledCategories = normalized
		}
	}
	if err := validateBudgets(cfg); err != "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": err})
		return
	}

	if err := daoUpsertTenantConfig(c.Request.Context(), cfg); err != nil {
		slog.Error("egressfilter admin: patch write failed", "tenant_id", tenantID, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	egressfilter.InvalidateTenantConfig(tenantID)
	c.JSON(http.StatusOK, configResponse(cfg))
}

func deleteTenantConfig(c *gin.Context) {
	tenantID, ok := parseTenantID(c)
	if !ok {
		return
	}
	if err := daoDeleteTenantConfig(c.Request.Context(), tenantID); err != nil {
		slog.Error("egressfilter admin: delete failed", "tenant_id", tenantID, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	egressfilter.InvalidateTenantConfig(tenantID)
	c.JSON(http.StatusOK, gin.H{"deleted": true, "tenant_id": tenantID.String()})
}

// --- Helpers -----------------------------------------------------------------

func parseTenantID(c *gin.Context) (uuid.UUID, bool) {
	raw := c.Param("tenant_id")
	id, err := uuid.Parse(raw)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid tenant_id: must be a UUID"})
		return uuid.Nil, false
	}
	return id, true
}

func parseMode(raw string) (egressfilter.Mode, string) {
	m := egressfilter.ParseMode(raw)
	// ParseMode silently maps unknown strings to ModeDetect. For an admin
	// API we want louder validation — explicit list of accepted strings.
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "detect", "audit", "enforce", "redact":
		return m, ""
	default:
		return "", "mode must be one of: detect, enforce, redact (legacy: audit)"
	}
}

// applyAddRemove returns base with removeList values stripped and addList
// values appended, deduped, with whitespace trimmed and empties dropped.
// Removes are applied first so a caller passing the same value in both
// arrays ends up keeping it.
func applyAddRemove(base, addList, removeList []string) []string {
	removeSet := make(map[string]struct{}, len(removeList))
	for _, v := range removeList {
		if v = strings.TrimSpace(v); v != "" {
			removeSet[v] = struct{}{}
		}
	}
	keep := base[:0]
	seen := make(map[string]struct{}, len(base)+len(addList))
	for _, v := range base {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, drop := removeSet[v]; drop {
			continue
		}
		if _, dup := seen[v]; dup {
			continue
		}
		seen[v] = struct{}{}
		keep = append(keep, v)
	}
	for _, v := range addList {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, dup := seen[v]; dup {
			continue
		}
		seen[v] = struct{}{}
		keep = append(keep, v)
	}
	return keep
}

func cleanStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := in[:0]
	seen := make(map[string]struct{}, len(in))
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, dup := seen[v]; dup {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

// validateBudgets enforces per-tenant size caps so a misconfigured tenant
// can't make the resolve path slow or the row absurdly large. Returns ""
// when valid, a human-readable error otherwise.
func validateBudgets(cfg *egressfilter.TenantConfig) string {
	if len(cfg.Allowlist) > maxAllowlistEntries {
		return "allowlist exceeds max entries (500)"
	}
	if len(cfg.DisabledRules) > maxDisabledRulesEntries {
		return "disabled_rules exceeds max entries (100)"
	}
	if len(cfg.PIIDisabledCategories) > maxPIIDisabledCategoryValues {
		return "pii_disabled_categories exceeds max entries (8)"
	}
	return ""
}

// applyPIIToConfig writes the PII sibling fields onto cfg. Only sets fields
// the caller actually provided (nil = leave existing value). Returns "" on
// success, a human-readable error on validation failure.
//
// Semantics parity with the secrets *_add/*_remove pattern is not needed
// here — the PII fields are all scalar / small-set, and admin UIs render
// them as toggles or checkbox groups where a full-value replace matches
// the interaction shape.
//
// The empty-string case for pii_mode is treated as "clear back to inherit
// env" — the CHECK constraint on the DB column rejects arbitrary strings,
// so validate here and translate any allowed string into the canonical
// lowercase form.
func applyPIIToConfig(
	cfg *egressfilter.TenantConfig,
	piiEnabled *bool,
	piiMode *string,
	piiNerEnabled *bool,
	piiDisabledCategories *[]string,
) string {
	if piiEnabled != nil {
		v := *piiEnabled
		cfg.PIIEnabled = &v
	}
	if piiMode != nil {
		normalized, errMsg := parsePIIMode(*piiMode)
		if errMsg != "" {
			return errMsg
		}
		cfg.PIIMode = normalized
	}
	if piiNerEnabled != nil {
		v := *piiNerEnabled
		cfg.PIINerEnabled = &v
	}
	if piiDisabledCategories != nil {
		normalized, errMsg := validatePIICategories(*piiDisabledCategories)
		if errMsg != "" {
			return errMsg
		}
		cfg.PIIDisabledCategories = normalized
	}
	return ""
}

// parsePIIMode validates + normalizes the mode string. Accepts "detect",
// "enforce", or "" (clear back to inherit env). Case-insensitive. Any
// other input is rejected with a caller-friendly message.
func parsePIIMode(raw string) (string, string) {
	trimmed := strings.ToLower(strings.TrimSpace(raw))
	switch trimmed {
	case "", "detect", "enforce":
		return trimmed, ""
	default:
		return "", "pii_mode must be one of: detect, enforce (or empty to inherit env default)"
	}
}

// validatePIICategories uppercases + dedupes the input and rejects any value
// outside validPIICategories (the closed set the ml-k8s /scrub API emits).
func validatePIICategories(in []string) ([]string, string) {
	if len(in) == 0 {
		return nil, ""
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, raw := range in {
		v := strings.ToUpper(strings.TrimSpace(raw))
		if v == "" {
			continue
		}
		if _, ok := validPIICategories[v]; !ok {
			return nil, "pii_disabled_categories contains unknown value: " + raw + " (allowed: EMAIL, PERSON, PHONE, LOCATION)"
		}
		if _, dup := seen[v]; dup {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out, ""
}

func configResponse(cfg *egressfilter.TenantConfig) gin.H {
	resp := gin.H{
		"tenant_id":               cfg.TenantID.String(),
		"mode":                    string(cfg.Mode),
		"enabled":                 cfg.Enabled,
		"allowlist":               cfg.Allowlist,
		"disabled_rules":          cfg.DisabledRules,
		"pii_mode":                cfg.PIIMode, // "" = inherit env
		"pii_disabled_categories": cfg.PIIDisabledCategories,
		"updated_at":              cfg.UpdatedAt,
	}
	// Nullable bools: emit null when unset so the UI can distinguish
	// "inherit env" from "explicit false" without a second flag.
	if cfg.PIIEnabled != nil {
		resp["pii_enabled"] = *cfg.PIIEnabled
	} else {
		resp["pii_enabled"] = nil
	}
	if cfg.PIINerEnabled != nil {
		resp["pii_ner_enabled"] = *cfg.PIINerEnabled
	} else {
		resp["pii_ner_enabled"] = nil
	}
	return resp
}
