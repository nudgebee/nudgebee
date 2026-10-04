package user

import (
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"nudgebee/services/common"

	"github.com/jmoiron/sqlx"
)

// A platform key opens the app API, a gateway key the AI Gateway, an all key both.
const (
	KindPlatform = "platform"
	KindGateway  = "gateway"
	KindAll      = "all"
)

// carriesGatewayTraffic reports whether quotas mean anything on a key. The gateway's own
// lookup filters on the same two kinds.
func carriesGatewayTraffic(kind string) bool {
	return kind == KindGateway || kind == KindAll
}

// TokenLimit is one quota on a key, and the shape tenant/user ceilings are read back in.
type TokenLimit struct {
	Metric string  `json:"metric" mapstructure:"metric"` // requests | tokens | cost
	Period string  `json:"period" mapstructure:"period"` // minute | hour | day | month
	Value  float64 `json:"value" mapstructure:"value"`
	Model  string  `json:"model" mapstructure:"model"` // "" = any model
}

var validMetrics = map[string]bool{"requests": true, "tokens": true, "cost": true}
var validPeriods = map[string]bool{"minute": true, "hour": true, "day": true, "month": true}

// An empty kind mints an all key: older clients omit the field, and a PAT has always
// opened both doors, so they must keep the access they have.
func normalizeKind(kind string) (string, error) {
	switch kind {
	case "":
		return KindAll, nil
	case KindPlatform, KindGateway, KindAll:
		return kind, nil
	default:
		return "", common.ErrorBadRequest(fmt.Sprintf("unknown key kind %q", kind))
	}
}

// Quotas only mean something on metered LLM traffic, so limits on a platform key are an
// error rather than a silently dropped field (D8).
func validateLimitsForKind(kind string, limits []TokenLimit) error {
	if len(limits) == 0 {
		return nil
	}
	if !carriesGatewayTraffic(kind) {
		return common.ErrorBadRequest("limits can only be set on a key that reaches the AI Gateway (kind=gateway or kind=all); a platform key carries an expiry only")
	}
	// Rows sharing (metric, period, model) share one limiter counter, so a duplicate
	// would count every request twice.
	seen := map[[3]string]bool{}
	for _, l := range limits {
		k := [3]string{l.Metric, l.Period, l.Model}
		if seen[k] {
			return common.ErrorBadRequest(fmt.Sprintf("duplicate limit: %s per %s for model %q appears more than once", l.Metric, l.Period, l.Model))
		}
		seen[k] = true
		if !validMetrics[l.Metric] {
			return common.ErrorBadRequest(fmt.Sprintf("unknown limit metric %q (expected requests, tokens or cost)", l.Metric))
		}
		if !validPeriods[l.Period] {
			return common.ErrorBadRequest(fmt.Sprintf("unknown limit period %q (expected minute, hour, day or month)", l.Period))
		}
		if l.Value <= 0 {
			return common.ErrorBadRequest("limit value must be greater than zero")
		}
	}
	return nil
}

// scopePattern matches a permission-catalog key (app/src/lib/permissionCatalog.ts
// permissionKey). Whether the module exists is the gateway's call: an unknown module
// grants nothing there, so it cannot widen a key.
var scopePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*:(Read|Write|Execute)$`)

// maxScopes bounds the list well above the catalog's size; it only stops a runaway payload.
const maxScopes = 500

// normalizeScopes validates a key's scopes and returns them deduplicated. Omitted (nil)
// means the owner's full access. An explicit empty list is rejected rather than read as
// full access: a caller sending [] most plausibly means "nothing", and widening that
// silently is the wrong way to fail. Only a platform key may carry scopes: the AI Gateway
// never reads them, so on a gateway or all key they would read as protection that is not
// applied (the same reasoning as limits on a platform key, D8).
func normalizeScopes(kind string, scopes []string) ([]string, error) {
	if scopes == nil {
		return nil, nil
	}
	if len(scopes) == 0 {
		return nil, common.ErrorBadRequest("scopes is empty, which would allow nothing; omit scopes for a full-access key")
	}
	if kind != KindPlatform {
		return nil, common.ErrorBadRequest("scopes can only be set on a platform key (kind=platform); the AI Gateway does not enforce them")
	}
	if len(scopes) > maxScopes {
		return nil, common.ErrorBadRequest(fmt.Sprintf("too many scopes (max %d)", maxScopes))
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(scopes))
	for _, s := range scopes {
		if !scopePattern.MatchString(s) {
			return nil, common.ErrorBadRequest(fmt.Sprintf("invalid scope %q (expected <module>:Read, <module>:Write or <module>:Execute)", s))
		}
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out, nil
}

// gatewayEndpoints are the endpoint families the AI Gateway classifies
// (llm/gateway/auth/policy.go). The gateway reads an unknown name as allowing nothing, but
// rejecting it here tells the caller at save time instead of at the first 403.
var gatewayEndpoints = map[string]bool{"chat": true, "responses": true, "embeddings": true, "conversations": true}

var gatewayProviderPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// maxPolicyEntries bounds each list; it only stops a runaway payload.
const maxPolicyEntries = 200

// normalizeGatewayPolicy validates a key's gateway policy and returns it with each list
// deduplicated. nil, or a policy with no lists, means unrestricted. Only a gateway key may
// be restricted: an all key also opens the platform API, where it could mint an
// unrestricted key and step around its own policy.
//
// A key cannot be granted more than its owner here because there is no user or tenant
// allowlist yet; tenant block rules are enforced by the gateway on every routed target.
// Per-user policy (#38963) adds its ceiling check here.
func normalizeGatewayPolicy(kind string, p *GatewayPolicy) (*GatewayPolicy, error) {
	if p == nil || (p.Providers == nil && p.Models == nil && p.Endpoints == nil) {
		return nil, nil
	}
	if kind != KindGateway {
		return nil, common.ErrorBadRequest("model, provider and endpoint restrictions can only be set on an AI Gateway key (kind=gateway)")
	}
	providers, err := normalizePolicyList("providers", p.Providers, func(v string) error {
		if !gatewayProviderPattern.MatchString(v) {
			return fmt.Errorf("invalid provider %q", v)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	models, err := normalizePolicyList("models", p.Models, func(v string) error {
		// Whitespace and control characters (NUL included, which Postgres text rejects)
		// never appear in a model id.
		if len(v) > 200 || strings.IndexFunc(v, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
			return fmt.Errorf("invalid model %q", v)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	endpoints, err := normalizePolicyList("endpoints", p.Endpoints, func(v string) error {
		if !gatewayEndpoints[v] {
			return fmt.Errorf("unknown endpoint %q (expected chat, responses, embeddings or conversations)", v)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &GatewayPolicy{Providers: providers, Models: models, Endpoints: endpoints}, nil
}

// normalizePolicyList keeps nil as nil (unrestricted), rejects an explicit empty list
// rather than reading it as unrestricted, trims and deduplicates entries.
func normalizePolicyList(field string, in []string, valid func(string) error) ([]string, error) {
	if in == nil {
		return nil, nil
	}
	if len(in) == 0 {
		return nil, common.ErrorBadRequest(fmt.Sprintf("gateway_policy.%s is empty, which would allow nothing; omit it to leave %s unrestricted", field, field))
	}
	if len(in) > maxPolicyEntries {
		return nil, common.ErrorBadRequest(fmt.Sprintf("too many gateway_policy.%s entries (max %d)", field, maxPolicyEntries))
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v == "" {
			return nil, common.ErrorBadRequest(fmt.Sprintf("gateway_policy.%s has a blank entry", field))
		}
		if err := valid(v); err != nil {
			return nil, common.ErrorBadRequest(err.Error())
		}
		if key := strings.ToLower(v); !seen[key] {
			seen[key] = true
			out = append(out, v)
		}
	}
	return out, nil
}

// policyEntry is one llm_gateway_access_policies row: one value a key may reach on one
// dimension. A dimension with no rows is unrestricted.
type policyEntry struct {
	APIKeyID  string `db:"api_key_id"`
	Dimension string `db:"dimension"`
	Value     string `db:"value"`
}

// policyEntries flattens a policy into the rows that store it; a nil list writes none.
func policyEntries(p *GatewayPolicy) []policyEntry {
	if p == nil {
		return nil
	}
	var out []policyEntry
	for _, d := range []struct {
		name   string
		values []string
	}{{"provider", p.Providers}, {"model", p.Models}, {"endpoint", p.Endpoints}} {
		for _, v := range d.values {
			out = append(out, policyEntry{Dimension: d.name, Value: v})
		}
	}
	return out
}

// gatewayPoliciesByKey folds stored rows into one policy per key; a key with no rows is
// unrestricted and gets no entry.
func gatewayPoliciesByKey(rows []policyEntry) map[string]*GatewayPolicy {
	out := map[string]*GatewayPolicy{}
	for _, r := range rows {
		p := out[r.APIKeyID]
		if p == nil {
			p = &GatewayPolicy{}
			out[r.APIKeyID] = p
		}
		switch r.Dimension {
		case "provider":
			p.Providers = append(p.Providers, r.Value)
		case "model":
			p.Models = append(p.Models, r.Value)
		case "endpoint":
			p.Endpoints = append(p.Endpoints, r.Value)
		}
	}
	return out
}

// keyScope is a key as user_auths stores it: its id, owner and current tenant, read back as
// text. Policy rows are written from it, never from request input: Postgres matches an
// uppercase or unhyphenated uuid, but the gateway matches rows on the canonical id::text.
type keyScope struct {
	KeyID, UserID, TenantID string
}

// keyScopeColumns reads a keyScope from user_auths, in Scan order.
const keyScopeColumns = `id::text, "user"::text, tenant_id::text`

func (k *keyScope) scanTargets() []any { return []any{&k.KeyID, &k.UserID, &k.TenantID} }

// lockKeyForUpdate locks the caller's own token row for the rest of tx and returns its scope,
// or sql.ErrNoRows. The policy and limits are replaced as delete-then-insert, so two edits of
// one key running at once would otherwise both commit and leave the union of their rows.
func lockKeyForUpdate(tx *sqlx.Tx, id, tenantID, userID string) (keyScope, error) {
	var k keyScope
	err := tx.QueryRowx(`SELECT `+keyScopeColumns+` FROM user_auths
		WHERE id = $1 AND tenant_id = $2 AND "user" = $3 AND provider_type = 'credentials' AND provider = 'token'
		FOR UPDATE`, id, tenantID, userID).Scan(k.scanTargets()...)
	return k, err
}

// sqlExecer is the part of *sqlx.Tx the policy writes need.
type sqlExecer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

// replaceKeyGatewayPolicy makes p the key's stored policy: it deletes the key's rows, then
// writes one row per allowed value; nil leaves the key unrestricted. Run it in the transaction
// that created or deleted the key, or that locked it with lockKeyForUpdate. Rows are matched
// by key and owner, as the gateway reads them, not by tenant: a key's user_auths.tenant_id
// follows its owner's current tenant (UpdateUserAccessed). A blank id is refused, since a row
// with an empty api_key_id applies to every key of the user (#38963).
func replaceKeyGatewayPolicy(tx sqlExecer, k keyScope, p *GatewayPolicy) error {
	if k.KeyID == "" || k.UserID == "" || k.TenantID == "" {
		return fmt.Errorf("gateway policy write needs a key, user and tenant id")
	}
	if _, err := tx.Exec(`DELETE FROM llm_gateway_access_policies WHERE api_key_id = $1 AND user_id = $2`,
		k.KeyID, k.UserID); err != nil {
		return err
	}
	for _, e := range policyEntries(p) {
		if _, err := tx.Exec(
			`INSERT INTO llm_gateway_access_policies (tenant_id, user_id, api_key_id, dimension, value) VALUES ($1, $2, $3, $4, $5)`,
			k.TenantID, k.UserID, k.KeyID, e.Dimension, e.Value); err != nil {
			return err
		}
	}
	return nil
}

// loadKeyGatewayPolicies returns the stored policy of each of the user's keys in the tenant,
// matched the way the gateway matches them; an unrestricted key has no entry.
func loadKeyGatewayPolicies(q sqlx.Queryer, tenantID, userID string) (map[string]*GatewayPolicy, error) {
	var rows []policyEntry
	if err := sqlx.Select(q, &rows,
		`SELECT p.api_key_id, p.dimension, p.value
		   FROM user_auths ua
		   JOIN llm_gateway_access_policies p ON p.api_key_id = ua.id::text AND p.user_id = ua."user"::text
		  WHERE ua.tenant_id = $1 AND ua."user" = $2 AND ua.provider_type = 'credentials' AND ua.provider = 'token'
		  ORDER BY p.value`, tenantID, userID); err != nil {
		return nil, err
	}
	return gatewayPoliciesByKey(rows), nil
}

// validateDeleteRequest accepts either identifier, preferring the id.
func validateDeleteRequest(r UserTokenDeleteRequest) error {
	if r.Id == "" && r.Name == "" {
		return common.ErrorBadRequest("id or name is required")
	}
	return nil
}

// validateLimitsAgainstCeilings rejects any owner limit above the tightest configured
// ceiling for its metric+period. Shared by create and update so the two cannot drift.
func validateLimitsAgainstCeilings(ceilings []TokenLimit, limits []TokenLimit) error {
	for _, l := range limits {
		if ceiling, ok := tightestCeiling(ceilings, l.Metric, l.Period); ok && l.Value > ceiling {
			return common.ErrorBadRequest(
				fmt.Sprintf("%s limit per %s exceeds the configured ceiling of %g", l.Metric, l.Period, ceiling))
		}
	}
	return nil
}

// resolveDeleteID picks the one key id a name-based delete meant. A name may exist once
// per kind, so more than one match is ambiguous and must not be guessed at.
func resolveDeleteID(matchingIDs []string) (string, error) {
	switch len(matchingIDs) {
	case 0:
		return "", common.ErrorBadRequest("token not found")
	case 1:
		return matchingIDs[0], nil
	default:
		return "", common.ErrorBadRequest("name is ambiguous: multiple keys share this name across key kinds; delete by id instead")
	}
}

// tightestCeiling returns the smallest configured ceiling for a metric+period, if any.
func tightestCeiling(ceilings []TokenLimit, metric, period string) (float64, bool) {
	found := false
	var min float64
	for _, c := range ceilings {
		if c.Metric != metric || c.Period != period {
			continue
		}
		if !found || c.Value < min {
			min, found = c.Value, true
		}
	}
	return min, found
}
