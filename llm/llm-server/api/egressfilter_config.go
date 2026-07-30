package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/security/egressfilter"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// Tenant-scoped egress-filter config RPC, reached through the egressfilter_*
// actions (registered in app/src/lib/actions.yaml). This is the header-based
// sibling of the path-based admin API in egressfilter_tenant.go: tenant
// identity is derived from the authenticated security context (x-tenant-id),
// never the request body — so the action handler needs no path segment.
//
// Surface:
//   - egressfilter_get            — mode + enabled + custom patterns + env context
//   - egressfilter_update         — set mode / enabled (read-modify-write)
//   - egressfilter_upsert_pattern — create/update one custom detection pattern
//   - egressfilter_delete_pattern — delete one custom pattern by id
//   - egressfilter_clear_override — drop the tenant row → inherit env defaults
//
// Every write preserves the columns it does not touch (allowlist,
// disabled_rules, and — for mode/enabled writes — custom_rules).

// egressConfigRequest is the wire shape for egressfilter_update. TenantId on
// the wire is ignored — tenant is always the authenticated tenant.
//
// PII fields follow the same tri-state pattern as the admin PATCH surface:
// field absent → leave the DB value; field present with a value → replace;
// field present but null → clear back to inherit-env. The custom
// UnmarshalJSON captures the top-level key set into presentKeys so the
// merge step can distinguish absent from null (a plain *bool zeroes both).
type egressConfigRequest struct {
	Mode    *string `json:"mode"`
	Enabled *bool   `json:"enabled"`

	// PII sibling detector (V827 backend + this action).
	PIIEnabled            *bool     `json:"pii_enabled"`
	PIIMode               *string   `json:"pii_mode"`
	PIINerEnabled         *bool     `json:"pii_ner_enabled"`
	PIIDisabledCategories *[]string `json:"pii_disabled_categories"`

	presentKeys map[string]struct{} `json:"-"`
}

// UnmarshalJSON captures the set of top-level keys the caller included in
// the request body. See patchBody.UnmarshalJSON in egressfilter_tenant.go
// for the same trick — needed to tell absent from explicit-null for the
// nullable PII fields.
func (r *egressConfigRequest) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	present := make(map[string]struct{}, len(raw))
	for k := range raw {
		present[k] = struct{}{}
	}
	type alias egressConfigRequest
	var tmp alias
	if err := json.Unmarshal(data, &tmp); err != nil {
		return err
	}
	*r = egressConfigRequest(tmp)
	r.presentKeys = present
	return nil
}

func (r *egressConfigRequest) isPresent(key string) bool {
	_, ok := r.presentKeys[key]
	return ok
}

// piiCategoriesForUI is the closed set the UI's category picker offers.
// Mirrors validPIICategories on the admin surface (egressfilter_tenant.go).
var piiCategoriesForUI = map[string]struct{}{
	"EMAIL":    {},
	"PERSON":   {},
	"PHONE":    {},
	"LOCATION": {},
}

// egressPatternUpsertRequest is the wire shape for egressfilter_upsert_pattern.
// An empty ID creates a new pattern (server assigns a uuid); a known ID updates
// in place. Enabled defaults to true on create.
type egressPatternUpsertRequest struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Regex   string `json:"regex"`
	Enabled *bool  `json:"enabled"`
}

type egressPatternDeleteRequest struct {
	ID string `json:"id"`
}

const (
	errorEgressTenantRequired   = "egressfilter: unable to identify tenant"
	errorEgressInvalidMode      = "egressfilter: mode must be one of: detect, enforce, redact"
	errorEgressInvalidPayload   = "egressfilter: invalid payload, action.name is required"
	errorEgressUnsupported      = "egressfilter: invalid payload, unsupported action"
	errorEgressPatternIDReq     = "egressfilter: pattern id is required"
	errorEgressPatternNotFound  = "egressfilter: pattern not found"
	errorEgressInvalidPIIMode   = "egressfilter: pii_mode must be one of: detect, enforce (or null to inherit env default)"
	errorEgressInvalidPIICatFmt = "egressfilter: pii_disabled_categories contains unknown value: %s (allowed: EMAIL, PERSON, PHONE, LOCATION)"
	errorEgressTooManyPIICats   = "egressfilter: pii_disabled_categories exceeds max entries (8)"
	maxPIIDisabledCategoriesRPC = 8
)

// egressConfigResponse is the read shape returned by every handler. It carries
// the tenant's effective mode/enabled and custom patterns plus read-only
// platform context so the UI can render env defaults and disable controls when
// the subsystem is off at the env level.
func egressConfigResponse(cfg *egressfilter.TenantConfig, mode egressfilter.Mode, enabled, hasOverride bool, patterns []egressfilter.CustomRule) gin.H {
	if patterns == nil {
		patterns = []egressfilter.CustomRule{}
	}
	// Serialize the PII fields off cfg (may be nil for env-defaults response).
	// Nullable bools go out as JSON null when unset, so the UI can distinguish
	// "inherit env" from "explicit false" without a second flag.
	var (
		piiEnabled    any = nil
		piiNerEnabled any = nil
		piiMode       string
		piiCats       []string
	)
	if cfg != nil {
		if cfg.PIIEnabled != nil {
			piiEnabled = *cfg.PIIEnabled
		}
		if cfg.PIINerEnabled != nil {
			piiNerEnabled = *cfg.PIINerEnabled
		}
		piiMode = cfg.PIIMode
		piiCats = cfg.PIIDisabledCategories
	}
	if piiCats == nil {
		piiCats = []string{}
	}
	return gin.H{
		"mode":            string(mode),
		"enabled":         enabled,
		"has_override":    hasOverride,
		"custom_patterns": patterns,
		// PII tenant values (nullable → inherit env when null).
		"pii_enabled":             piiEnabled,
		"pii_mode":                piiMode,
		"pii_ner_enabled":         piiNerEnabled,
		"pii_disabled_categories": piiCats,
		// Read-only platform context (from env), so the UI can explain when a
		// tenant setting has no effect.
		"master_enabled":       config.Config.LlmServerEgressFilterEnabled,
		"secrets_enabled":      config.Config.LlmServerEgressFilterSecretsEnabled,
		"env_default_mode":     string(egressfilter.ParseMode(config.Config.LlmServerEgressFilterSecretsMode)),
		"env_pii_enabled":      config.Config.LlmServerEgressFilterPIIEnabled,
		"env_pii_ner_enabled":  config.Config.LlmServerEgressFilterPIINerEnabled,
		"env_pii_default_mode": envPIIMode(),
	}
}

// envPIIMode normalizes the process-level PII mode string to a UI-friendly
// value ("detect" default when unset or unrecognized).
func envPIIMode() string {
	m := strings.ToLower(strings.TrimSpace(config.Config.LlmServerEgressFilterPIIMode))
	if m == "enforce" {
		return "enforce"
	}
	return "detect"
}

// tenantUUIDFromContext resolves the authenticated tenant as a uuid.UUID.
// The nil guards are defensive: buildContextFromPayload builds the security
// context via NewSecurityContextForTenantAccountAdmin, which returns nil on a
// tenant-lookup DB error, and GetTenantId() has a pointer receiver — so a nil
// security context would panic without this fail-fast.
func tenantUUIDFromContext(context *security.RequestContext) (uuid.UUID, error) {
	if context == nil || context.GetSecurityContext() == nil {
		return uuid.Nil, errors.New(errorEgressTenantRequired)
	}
	raw := context.GetSecurityContext().GetTenantId()
	if raw == "" {
		return uuid.Nil, errors.New(errorEgressTenantRequired)
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, errors.New(errorEgressTenantRequired)
	}
	return id, nil
}

// patternsFromCfg parses the tenant's custom_rules JSONB into []CustomRule.
// A parse error is logged and treated as an empty set — the admin surface must
// stay usable even if the row is somehow corrupted.
func patternsFromCfg(cfg *egressfilter.TenantConfig) []egressfilter.CustomRule {
	if cfg == nil {
		return nil
	}
	rules, err := egressfilter.ParseCustomRules(cfg.CustomRules)
	if err != nil {
		slog.Error("egressfilter config: failed to parse custom_rules", "tenant_id", cfg.TenantID, "error", err)
		return nil
	}
	return rules
}

// defaultTenantConfig returns an env-shaped starting point for a tenant that
// has no override row yet.
func defaultTenantConfig(tenantID uuid.UUID) *egressfilter.TenantConfig {
	return &egressfilter.TenantConfig{
		TenantID: tenantID,
		Mode:     egressfilter.ParseMode(config.Config.LlmServerEgressFilterSecretsMode),
		Enabled:  config.Config.LlmServerEgressFilterSecretsEnabled,
	}
}

// egressConfigGet returns the current tenant's egress-filter config. When no
// per-tenant override row exists, the env-level defaults are returned with
// has_override=false so the UI shows what is actually in effect.
func egressConfigGet(c *gin.Context, context *security.RequestContext) {
	tenantID, err := tenantUUIDFromContext(context)
	if err != nil {
		c.JSON(400, buildApiResponse(nil, []error{err}))
		return
	}

	cfg, err := daoGetTenantConfig(c.Request.Context(), tenantID)
	if err != nil {
		slog.Error("egressfilter config: get failed", "tenant_id", tenantID, "error", err)
		c.JSON(500, buildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
		return
	}

	if cfg == nil {
		envMode := egressfilter.ParseMode(config.Config.LlmServerEgressFilterSecretsMode)
		c.JSON(200, buildApiResponse(egressConfigResponse(nil, envMode, config.Config.LlmServerEgressFilterSecretsEnabled, false, nil), nil))
		return
	}

	c.JSON(200, buildApiResponse(egressConfigResponse(cfg, cfg.Mode, cfg.Enabled, true, patternsFromCfg(cfg)), nil))
}

// egressConfigUpdate sets the tenant's mode and/or enabled flag. It is a
// read-modify-write so allowlist / disabled_rules / custom_rules on an
// existing row are preserved untouched.
func egressConfigUpdate(c *gin.Context, context *security.RequestContext, payload map[string]any) {
	tenantID, err := tenantUUIDFromContext(context)
	if err != nil {
		c.JSON(400, buildApiResponse(nil, []error{err}))
		return
	}

	var request egressConfigRequest
	if err := common.DecodeMapToStruct(payload, &request); err != nil {
		slog.Error("egressfilter config: error binding request", "error", err)
		c.JSON(400, buildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
		return
	}

	cfg, err := loadOrDefault(c, tenantID)
	if err != nil {
		return // response already written
	}

	if err := mergeEgressConfigUpdate(cfg, request); err != nil {
		c.JSON(400, buildApiResponse(nil, []error{err}))
		return
	}

	if err := persistTenantConfig(c, cfg); err != nil {
		return // response already written
	}
	c.JSON(200, buildApiResponse(egressConfigResponse(cfg, cfg.Mode, cfg.Enabled, true, patternsFromCfg(cfg)), nil))
}

// egressPatternUpsert creates or updates one custom detection pattern. The
// full pattern set is validated (regex compiles, caps, unique names) before
// the write, so an invalid pattern is rejected with a 400 rather than
// persisted and silently skipped on the scan path.
func egressPatternUpsert(c *gin.Context, context *security.RequestContext, payload map[string]any) {
	tenantID, err := tenantUUIDFromContext(context)
	if err != nil {
		c.JSON(400, buildApiResponse(nil, []error{err}))
		return
	}

	var request egressPatternUpsertRequest
	if err := common.DecodeMapToStruct(payload, &request); err != nil {
		c.JSON(400, buildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
		return
	}

	cfg, err := loadOrDefault(c, tenantID)
	if err != nil {
		return
	}
	rules := patternsFromCfg(cfg)

	enabled := true
	if request.Enabled != nil {
		enabled = *request.Enabled
	}
	incoming := egressfilter.CustomRule{
		ID:      strings.TrimSpace(request.ID),
		Name:    strings.TrimSpace(request.Name),
		Regex:   request.Regex,
		Enabled: enabled,
	}

	if incoming.ID == "" {
		incoming.ID = uuid.New().String()
		rules = append(rules, incoming)
	} else {
		found := false
		for i := range rules {
			if rules[i].ID == incoming.ID {
				rules[i] = incoming
				found = true
				break
			}
		}
		if !found {
			c.JSON(404, buildApiResponse(nil, []error{errors.New(errorEgressPatternNotFound)}))
			return
		}
	}

	if err := egressfilter.ValidateCustomRules(rules); err != nil {
		c.JSON(400, buildApiResponse(nil, []error{common.Error{Message: "egressfilter: " + err.Error()}}))
		return
	}
	if err := setCustomRules(cfg, rules); err != nil {
		c.JSON(500, buildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
		return
	}
	if err := persistTenantConfig(c, cfg); err != nil {
		return
	}
	c.JSON(200, buildApiResponse(egressConfigResponse(cfg, cfg.Mode, cfg.Enabled, true, rules), nil))
}

// egressPatternDelete removes one custom pattern by id. Deleting an unknown id
// is a no-op success (idempotent).
func egressPatternDelete(c *gin.Context, context *security.RequestContext, payload map[string]any) {
	tenantID, err := tenantUUIDFromContext(context)
	if err != nil {
		c.JSON(400, buildApiResponse(nil, []error{err}))
		return
	}

	var request egressPatternDeleteRequest
	if err := common.DecodeMapToStruct(payload, &request); err != nil {
		c.JSON(400, buildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
		return
	}
	if strings.TrimSpace(request.ID) == "" {
		c.JSON(400, buildApiResponse(nil, []error{errors.New(errorEgressPatternIDReq)}))
		return
	}

	cfg, err := daoGetTenantConfig(c.Request.Context(), tenantID)
	if err != nil {
		slog.Error("egressfilter config: read-before-write failed", "tenant_id", tenantID, "error", err)
		c.JSON(500, buildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
		return
	}
	// Track whether an override row actually existed so the no-op path below
	// reports has_override correctly (a delete against a tenant with no row
	// must not claim an override exists).
	hasOverride := cfg != nil
	if cfg == nil {
		cfg = defaultTenantConfig(tenantID)
	}
	rules := patternsFromCfg(cfg)
	// Fresh slice (not an in-place rules[:0] reuse) — defensive against ever
	// aliasing a shared backing array.
	kept := make([]egressfilter.CustomRule, 0, len(rules))
	for _, r := range rules {
		if r.ID != request.ID {
			kept = append(kept, r)
		}
	}

	// Nothing matched → idempotent no-op: skip the write + cache invalidation
	// (and avoid materializing an override row for a tenant that had none).
	if len(kept) < len(rules) {
		if err := setCustomRules(cfg, kept); err != nil {
			c.JSON(500, buildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
			return
		}
		if err := persistTenantConfig(c, cfg); err != nil {
			return
		}
		hasOverride = true
	}
	c.JSON(200, buildApiResponse(egressConfigResponse(cfg, cfg.Mode, cfg.Enabled, hasOverride, kept), nil))
}

// egressClearOverride drops the tenant's override row entirely, so the next
// resolve falls back to env defaults (mode, enabled, and no custom patterns).
func egressClearOverride(c *gin.Context, context *security.RequestContext) {
	tenantID, err := tenantUUIDFromContext(context)
	if err != nil {
		c.JSON(400, buildApiResponse(nil, []error{err}))
		return
	}
	if err := daoDeleteTenantConfig(c.Request.Context(), tenantID); err != nil {
		slog.Error("egressfilter config: clear override failed", "tenant_id", tenantID, "error", err)
		c.JSON(500, buildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
		return
	}
	egressfilter.InvalidateTenantConfig(tenantID)
	envMode := egressfilter.ParseMode(config.Config.LlmServerEgressFilterSecretsMode)
	c.JSON(200, buildApiResponse(egressConfigResponse(nil, envMode, config.Config.LlmServerEgressFilterSecretsEnabled, false, nil), nil))
}

// --- Shared handler helpers --------------------------------------------------

// loadOrDefault reads the tenant's row, returning an env-shaped default when
// none exists. On DB error it writes the 500 response and returns a non-nil
// error so the caller can bail.
func loadOrDefault(c *gin.Context, tenantID uuid.UUID) (*egressfilter.TenantConfig, error) {
	cfg, err := daoGetTenantConfig(c.Request.Context(), tenantID)
	if err != nil {
		slog.Error("egressfilter config: read-before-write failed", "tenant_id", tenantID, "error", err)
		c.JSON(500, buildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
		return nil, err
	}
	if cfg == nil {
		cfg = defaultTenantConfig(tenantID)
	}
	return cfg, nil
}

// persistTenantConfig upserts cfg and invalidates the resolve cache. On error
// it writes the 500 response and returns a non-nil error.
func persistTenantConfig(c *gin.Context, cfg *egressfilter.TenantConfig) error {
	if err := daoUpsertTenantConfig(c.Request.Context(), cfg); err != nil {
		slog.Error("egressfilter config: upsert failed", "tenant_id", cfg.TenantID, "error", err)
		c.JSON(500, buildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
		return err
	}
	egressfilter.InvalidateTenantConfig(cfg.TenantID)
	return nil
}

// setCustomRules marshals rules onto cfg.CustomRules (empty set → "[]").
func setCustomRules(cfg *egressfilter.TenantConfig, rules []egressfilter.CustomRule) error {
	if len(rules) == 0 {
		cfg.CustomRules = []byte("[]")
		return nil
	}
	raw, err := json.Marshal(rules)
	if err != nil {
		return err
	}
	cfg.CustomRules = raw
	return nil
}

// mergeEgressConfigUpdate applies the supplied mode/enabled + PII overrides
// onto cfg in place. Only fields the caller set (non-nil, and — for the
// nullable PII fields — present in the JSON body) are touched. Returns an
// error (and leaves cfg unmodified for the field that failed) when a mode
// string or category is invalid. Pure and DB-free so it is unit-testable
// without the auth/context stack.
//
// Tri-state semantics for the PII fields:
//   - request key absent           → leave cfg field unchanged
//   - request key present, non-null → replace cfg field with value
//   - request key present, null    → clear cfg field back to "inherit env"
func mergeEgressConfigUpdate(cfg *egressfilter.TenantConfig, request egressConfigRequest) error {
	if request.Mode != nil {
		mode, ok := parseEgressMode(*request.Mode)
		if !ok {
			return errors.New(errorEgressInvalidMode)
		}
		cfg.Mode = mode
	}
	if request.Enabled != nil {
		cfg.Enabled = *request.Enabled
	}
	if request.isPresent("pii_enabled") {
		cfg.PIIEnabled = request.PIIEnabled // nil = clear
	}
	if request.isPresent("pii_ner_enabled") {
		cfg.PIINerEnabled = request.PIINerEnabled // nil = clear
	}
	if request.isPresent("pii_mode") {
		if request.PIIMode == nil {
			cfg.PIIMode = "" // clear
		} else {
			normalized, ok := parseEgressPIIMode(*request.PIIMode)
			if !ok {
				return errors.New(errorEgressInvalidPIIMode)
			}
			cfg.PIIMode = normalized
		}
	}
	if request.isPresent("pii_disabled_categories") {
		if request.PIIDisabledCategories == nil {
			cfg.PIIDisabledCategories = nil // clear (DAO coerces to '{}')
		} else {
			normalized, err := parseEgressPIICategories(*request.PIIDisabledCategories)
			if err != nil {
				return err
			}
			cfg.PIIDisabledCategories = normalized
		}
	}
	return nil
}

// parseEgressPIIMode validates + normalizes the PII mode string. Accepts
// "detect" or "enforce"; empty string clears to "inherit env" (kept as-is
// in cfg.PIIMode = ""). Any other value is rejected loudly, mirroring the
// admin-surface's parsePIIMode.
func parseEgressPIIMode(raw string) (string, bool) {
	trimmed := strings.ToLower(strings.TrimSpace(raw))
	switch trimmed {
	case "", "detect", "enforce":
		return trimmed, true
	default:
		return "", false
	}
}

// parseEgressPIICategories uppercases + dedupes + closed-set-validates the
// input. Empty input → nil (cleared). Unknown category → error with the
// allowed set in the message.
func parseEgressPIICategories(in []string) ([]string, error) {
	if len(in) == 0 {
		return nil, nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, raw := range in {
		v := strings.ToUpper(strings.TrimSpace(raw))
		if v == "" {
			continue
		}
		if _, ok := piiCategoriesForUI[v]; !ok {
			return nil, fmt.Errorf(errorEgressInvalidPIICatFmt, raw)
		}
		if _, dup := seen[v]; dup {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	if len(out) > maxPIIDisabledCategoriesRPC {
		return nil, errors.New(errorEgressTooManyPIICats)
	}
	return out, nil
}

// parseEgressMode validates an admin-supplied mode string. Unlike
// egressfilter.ParseMode (which silently coerces unknowns to detect), the
// admin surface rejects unknown values loudly. Accepts the legacy "audit"
// synonym for detect.
func parseEgressMode(raw string) (egressfilter.Mode, bool) {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	switch normalized {
	case "detect", "audit", "enforce", "redact":
		return egressfilter.ParseMode(normalized), true
	default:
		return "", false
	}
}

// handleEgressfilterConfigApis registers the tenant-scoped egress config RPC.
// Dispatches by action name, mirroring handleGlobalContextApis.
func handleEgressfilterConfigApis(r *gin.Engine, tracer trace.Tracer, meter metric.Meter) {
	group := r.Group("/v1/egressfilter/config")

	group.POST("", func(c *gin.Context) {
		var actionRequest ActionRequest
		if err := c.ShouldBindJSON(&actionRequest); err != nil {
			slog.Error("egressfilter config: error binding rpc request", "error", err)
			c.JSON(400, buildApiResponse(nil, []error{common.Error{Message: "egressfilter: " + err.Error()}}))
			return
		}

		if actionRequest.Action.Name == "" {
			c.JSON(400, buildApiResponse(nil, []error{errors.New(errorEgressInvalidPayload)}))
			return
		}

		context, err := buildContextFromPayload(c.Request.Context(), c, &actionRequest, tracer, meter, slog.With())
		if err != nil {
			c.JSON(401, buildApiResponse(nil, []error{err}))
			return
		}

		payload := actionRequest.Input
		if rawRequest, ok := payload["request"]; ok {
			if castedRequest, castOk := rawRequest.(map[string]any); castOk {
				payload = castedRequest
			}
		}

		switch actionRequest.Action.Name {
		case "egressfilter_get":
			common.MetricsApiRequestsTotal("egressfilter_config_get")
			egressConfigGet(c, context)
		case "egressfilter_update":
			common.MetricsApiRequestsTotal("egressfilter_config_update")
			egressConfigUpdate(c, context, payload)
		case "egressfilter_upsert_pattern":
			common.MetricsApiRequestsTotal("egressfilter_pattern_upsert")
			egressPatternUpsert(c, context, payload)
		case "egressfilter_delete_pattern":
			common.MetricsApiRequestsTotal("egressfilter_pattern_delete")
			egressPatternDelete(c, context, payload)
		case "egressfilter_clear_override":
			common.MetricsApiRequestsTotal("egressfilter_clear_override")
			egressClearOverride(c, context)
		default:
			c.JSON(400, buildApiResponse(nil, []error{errors.New(errorEgressUnsupported)}))
		}
	})
}
