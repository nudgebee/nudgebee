package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"

	"nudgebee/services/common"
	"nudgebee/services/config"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
)

// LLM default provider rules (spec: llm/llm-server/docs/llm-default-provider-spec.md).
// An account uses its own default, otherwise the tenant default, otherwise the
// platform LLM (llm-server ENV), otherwise nothing. chooseLLMForAccount mirrors
// llm-server's chooseAccountLLMIntegration; both run the cases in
// llm/llm-server/agents/core/testdata/llm_default_cases.json.

const llmIntegrationType = "llm"

// LLMDefaultsChange describes a proposed change to an LLM integration.
type LLMDefaultsChange struct {
	Op            string   `json:"op"`             // save | delete | disable | enable
	IntegrationId string   `json:"integration_id"` // empty for a new integration
	AccountIds    []string `json:"account_ids"`    // save: the selected accounts; none means tenant-wide
	// TenantDefault and AccountDefault are the two save-time ticks; nil leaves
	// that default as it is.
	TenantDefault  *bool `json:"llm_tenant_default"`
	AccountDefault *bool `json:"llm_account_default"`
}

// LLMRef names an integration in a report.
type LLMRef struct {
	Id   string `json:"id"`
	Name string `json:"name"`
}

// LLMAccountImpact is what one account uses before and after a change.
// Source is account_default, tenant_default, platform or none.
type LLMAccountImpact struct {
	AccountId    string  `json:"account_id"`
	AccountName  string  `json:"account_name"`
	Before       *LLMRef `json:"before,omitempty"`
	BeforeSource string  `json:"before_source"`
	After        *LLMRef `json:"after,omitempty"`
	AfterSource  string  `json:"after_source"`
}

// LLMDefaultsReport is what a change would do. The UI builds its popups from it;
// api-server refuses the change when Blocked is non-empty.
type LLMDefaultsReport struct {
	EnvConfigured           bool               `json:"env_configured"`
	TenantDefault           *LLMRef            `json:"tenant_default,omitempty"`
	ReplacesTenantDefault   *LLMRef            `json:"replaces_tenant_default,omitempty"`
	BecomesTenantWide       bool               `json:"becomes_tenant_wide"`
	Affected                []LLMAccountImpact `json:"affected"`
	ReplacedAccountDefaults []LLMAccountImpact `json:"replaced_account_defaults"`
	Blocked                 []LLMAccountImpact `json:"blocked"`
}

type llmConfigState struct {
	Id              string
	Name            string
	Enabled         bool
	Accounts        []string
	AccountDefaults []string
	TenantDefault   bool
}

type llmTenantState struct {
	Configs  []llmConfigState
	Accounts []LLMRef // active accounts of the tenant
}

// chooseLLMForAccount returns the integration an account uses, and why.
func chooseLLMForAccount(configs []llmConfigState, account string) (string, string) {
	var own, tenant []string
	for _, c := range configs {
		if !c.Enabled {
			continue
		}
		if slices.Contains(c.Accounts, account) && slices.Contains(c.AccountDefaults, account) {
			own = append(own, c.Id)
		}
		if c.TenantDefault {
			tenant = append(tenant, c.Id)
		}
	}
	if len(own) == 1 {
		return own[0], "account_default"
	}
	if len(tenant) == 1 {
		return tenant[0], "tenant_default"
	}
	return "", ""
}

// llmPickableForAccount mirrors llm-server's rule for the model picker.
func llmPickableForAccount(c llmConfigState, account string) bool {
	return c.Enabled && (slices.Contains(c.Accounts, account) || len(c.Accounts) == 0 || c.TenantDefault)
}

func loadLLMTenantState(tx sqlx.Queryer, tenantId string) (llmTenantState, error) {
	var state llmTenantState
	rows := []struct {
		Id              string         `db:"id"`
		Name            string         `db:"name"`
		Status          string         `db:"status"`
		Accounts        pq.StringArray `db:"accounts"`
		AccountDefaults pq.StringArray `db:"account_defaults"`
		TenantDefault   bool           `db:"tenant_default"`
	}{}
	if err := sqlx.Select(tx, &rows, `
		SELECT i.id::text AS id, i.name, i.status,
		       COALESCE(array_agg(DISTINCT ia.cloud_account_id::text) FILTER (WHERE ia.cloud_account_id IS NOT NULL), '{}') AS accounts,
		       COALESCE(array_agg(DISTINCT ia.cloud_account_id::text) FILTER (WHERE ia.default_llm_provider), '{}') AS account_defaults,
		       EXISTS (SELECT 1 FROM integration_config_values d
		               WHERE d.integration_id = i.id AND d.name = $2 AND d.value = 'true') AS tenant_default
		FROM integrations i
		LEFT JOIN integrations_cloud_accounts ia ON ia.integration_id = i.id
		WHERE i.tenant_id = $1 AND i.type = 'llm'
		GROUP BY i.id`, tenantId, DefaultLLMProvider); err != nil {
		return state, fmt.Errorf("integrations: failed to load LLM integrations: %w", err)
	}
	for _, r := range rows {
		state.Configs = append(state.Configs, llmConfigState{
			Id: r.Id, Name: r.Name, Enabled: r.Status == string(IntegrationStatusEnabled),
			Accounts: r.Accounts, AccountDefaults: r.AccountDefaults, TenantDefault: r.TenantDefault,
		})
	}
	if err := sqlx.Select(tx, &state.Accounts, `
		SELECT id::text AS id, COALESCE(account_name, '') AS name
		FROM cloud_accounts WHERE tenant = $1 AND status = 'active' ORDER BY account_name`, tenantId); err != nil {
		return state, fmt.Errorf("integrations: failed to load accounts: %w", err)
	}
	return state, nil
}

// applyLLMChange returns the integrations as they would be after the change.
// A new integration gets the id "new".
func applyLLMChange(configs []llmConfigState, ch LLMDefaultsChange) []llmConfigState {
	out := make([]llmConfigState, 0, len(configs)+1)
	for _, c := range configs {
		c.Accounts = slices.Clone(c.Accounts)
		c.AccountDefaults = slices.Clone(c.AccountDefaults)
		out = append(out, c)
	}
	id := ch.IntegrationId
	if id == "" {
		id = "new"
	}
	idx := slices.IndexFunc(out, func(c llmConfigState) bool { return c.Id == id })
	switch ch.Op {
	case "delete":
		if idx >= 0 {
			out = slices.Delete(out, idx, idx+1)
		}
		return out
	case "disable", "enable":
		if idx >= 0 {
			out[idx].Enabled = ch.Op == "enable"
		}
		return out
	}
	if idx < 0 {
		out = append(out, llmConfigState{Id: id, Name: "", Enabled: true})
		idx = len(out) - 1
	}
	c := &out[idx]
	c.Accounts = slices.Clone(ch.AccountIds)
	switch {
	case ch.AccountDefault == nil:
		c.AccountDefaults = slices.DeleteFunc(c.AccountDefaults, func(a string) bool { return !slices.Contains(c.Accounts, a) })
	case *ch.AccountDefault:
		c.AccountDefaults = slices.Clone(c.Accounts)
		for i := range out {
			if i != idx {
				out[i].AccountDefaults = slices.DeleteFunc(out[i].AccountDefaults, func(a string) bool { return slices.Contains(c.Accounts, a) })
			}
		}
	default:
		c.AccountDefaults = nil
	}
	if ch.TenantDefault != nil {
		if *ch.TenantDefault {
			for i := range out {
				out[i].TenantDefault = i == idx
			}
		} else {
			c.TenantDefault = false
		}
	}
	return out
}

// checkLLMChange reports what a change does to every active account in the tenant.
func checkLLMChange(state llmTenantState, ch LLMDefaultsChange, envConfigured bool) LLMDefaultsReport {
	after := applyLLMChange(state.Configs, ch)
	names := map[string]string{"new": ""}
	for _, c := range state.Configs {
		names[c.Id] = c.Name
	}
	ref := func(id string) *LLMRef {
		if id == "" {
			return nil
		}
		return &LLMRef{Id: id, Name: names[id]}
	}
	source := func(id, why string) string {
		if id != "" {
			return why
		}
		if envConfigured {
			return "platform"
		}
		return "none"
	}

	report := LLMDefaultsReport{EnvConfigured: envConfigured, Affected: []LLMAccountImpact{}, ReplacedAccountDefaults: []LLMAccountImpact{}, Blocked: []LLMAccountImpact{}}
	var beforeTenant, afterTenant string
	for _, c := range state.Configs {
		if c.Enabled && c.TenantDefault {
			beforeTenant = c.Id
		}
	}
	for _, c := range after {
		if c.Enabled && c.TenantDefault {
			afterTenant = c.Id
		}
	}
	report.TenantDefault = ref(beforeTenant)
	if beforeTenant != "" && afterTenant != "" && beforeTenant != afterTenant {
		report.ReplacesTenantDefault = ref(beforeTenant)
	}

	id := ch.IntegrationId
	if id == "" {
		id = "new"
	}
	wasWide, nowWide := false, false
	for _, c := range state.Configs {
		if c.Id == id {
			wasWide = c.Enabled && len(c.Accounts) == 0
		}
	}
	for _, c := range after {
		if c.Id == id {
			nowWide = c.Enabled && len(c.Accounts) == 0
		}
	}
	report.BecomesTenantWide = nowWide && !wasWide && ch.IntegrationId != ""

	for _, acct := range state.Accounts {
		b, bWhy := chooseLLMForAccount(state.Configs, acct.Id)
		a, aWhy := chooseLLMForAccount(after, acct.Id)
		impact := LLMAccountImpact{
			AccountId: acct.Id, AccountName: acct.Name,
			Before: ref(b), BeforeSource: source(b, bWhy), After: ref(a), AfterSource: source(a, aWhy),
		}
		if bWhy == "account_default" && aWhy == "account_default" && b != a {
			report.ReplacedAccountDefaults = append(report.ReplacedAccountDefaults, impact)
		}
		if b == a {
			continue
		}
		report.Affected = append(report.Affected, impact)
		if a == "" && !envConfigured && b != "" {
			report.Blocked = append(report.Blocked, impact)
		}
	}
	return report
}

// ErrLLMChangeBlocked is returned when a change would leave accounts with no LLM.
var ErrLLMChangeBlocked = errors.New("integrations: this change would leave accounts with no LLM")

// enforceLLMChange runs the check inside the caller's transaction and refuses a
// blocked change or one made by a user who is not a tenant admin. The advisory
// lock serialises LLM changes per tenant so two saves cannot both become the
// tenant default.
func enforceLLMChange(ctx *security.RequestContext, tx *sqlx.Tx, ch LLMDefaultsChange, envConfigured bool) error {
	sc := ctx.GetSecurityContext()
	if !sc.IsTenantAdmin() && !sc.IsSuperAdmin() {
		return errors.New("integrations: only a tenant admin can change LLM integrations")
	}
	tenantId := sc.GetTenantId()
	if _, err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtextextended('llm_defaults:' || $1, 0))`, tenantId); err != nil {
		return fmt.Errorf("integrations: failed to lock LLM defaults: %w", err)
	}
	state, err := loadLLMTenantState(tx, tenantId)
	if err != nil {
		return err
	}
	report := checkLLMChange(state, ch, envConfigured)
	if len(report.Blocked) == 0 {
		return nil
	}
	names := make([]string, 0, len(report.Blocked))
	for _, b := range report.Blocked {
		names = append(names, b.AccountName)
	}
	sort.Strings(names)
	return fmt.Errorf("%w: %s. Set another tenant default first, or keep a default for these accounts",
		ErrLLMChangeBlocked, strings.Join(names, ", "))
}

// CheckLLMChange is the read-only check behind integrations_check_llm_defaults.
func CheckLLMChange(ctx *security.RequestContext, ch LLMDefaultsChange) (LLMDefaultsReport, error) {
	sc := ctx.GetSecurityContext()
	if !sc.IsTenantAdmin() && !sc.IsSuperAdmin() {
		return LLMDefaultsReport{}, errors.New("integrations: only a tenant admin can change LLM integrations")
	}
	envConfigured := LLMEnvConfigured(ctx.GetContext())
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return LLMDefaultsReport{}, err
	}
	state, err := loadLLMTenantState(dbms.Db, sc.GetTenantId())
	if err != nil {
		return LLMDefaultsReport{}, err
	}
	return checkLLMChange(state, ch, envConfigured), nil
}

// LLMEnvConfigured asks llm-server whether it has a platform LLM. Any failure
// counts as "no", the strict case: changes that would leave accounts with no
// LLM stay blocked, and everything else still goes through.
func LLMEnvConfigured(c context.Context) bool {
	if config.Config.LLMServerEndpoint == "" {
		return false
	}
	if c == nil {
		c = context.Background()
	}
	headers := map[string]string{}
	if config.Config.LLMServerTokenHeader != "" && config.Config.LLMServerToken != "" {
		headers[config.Config.LLMServerTokenHeader] = config.Config.LLMServerToken
	}
	resp, err := common.HttpGet(strings.TrimRight(config.Config.LLMServerEndpoint, "/")+"/v1/llm-config/env-status",
		common.HttpWithContext(c), common.HttpWithHeaders(headers), common.HttpWithTimeout(5*time.Second))
	if err != nil {
		slog.Warn("integrations: could not read llm-server ENV status; treating as not configured", "error", err)
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		slog.Warn("integrations: llm-server ENV status failed; treating as not configured", "status", resp.StatusCode)
		return false
	}
	var parsed struct {
		Configured bool `json:"configured"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return false
	}
	return parsed.Configured
}

// LLMSaveOptions are the two save-time ticks of an LLM integration; nil leaves
// that default as it is.
type LLMSaveOptions struct {
	TenantDefault  *bool
	AccountDefault *bool
}

// llmDefaultsMigratedKey marks an LLM integration llm-server's hook-link
// cleanup must skip. Written on every LLM save. Remove with that job.
const llmDefaultsMigratedKey = "defaults_migrated"

// The two save-time ticks travel to CreateIntegrationConfig as config values
// under these names, so the generic save keeps one signature for every
// integration type. resolveLLMSaveOptions takes them out before anything is
// stored.
const (
	llmTenantDefaultKey  = "llm_save_tenant_default"
	llmAccountDefaultKey = "llm_save_account_default"
)

// WithLLMSaveOptions adds the ticks the caller set to an LLM save's values.
func WithLLMSaveOptions(values []IntegrationConfigValue, opts LLMSaveOptions) []IntegrationConfigValue {
	if opts.TenantDefault != nil {
		values = append(values, IntegrationConfigValue{Name: llmTenantDefaultKey, Value: strconv.FormatBool(*opts.TenantDefault)})
	}
	if opts.AccountDefault != nil {
		values = append(values, IntegrationConfigValue{Name: llmAccountDefaultKey, Value: strconv.FormatBool(*opts.AccountDefault)})
	}
	return values
}

// resolveLLMSaveOptions returns the ticks a save sets, whether they came
// explicitly (the new modal), and the values to store with the tick entries
// removed. Explicit ticks also drop the old default_llm_provider value, which
// they replace. Without them it reads that value the old way: one checkbox for
// both defaults, still written by the existing save loop.
func resolveLLMSaveOptions(values []IntegrationConfigValue) (LLMSaveOptions, bool, []IntegrationConfigValue) {
	var opts LLMSaveOptions
	explicit := false
	kept := make([]IntegrationConfigValue, 0, len(values))
	for _, v := range values {
		b := v.Value == "true"
		switch v.Name {
		case llmTenantDefaultKey:
			opts.TenantDefault, explicit = &b, true
		case llmAccountDefaultKey:
			opts.AccountDefault, explicit = &b, true
		default:
			kept = append(kept, v)
		}
	}
	if explicit {
		return opts, true, slices.DeleteFunc(kept, func(v IntegrationConfigValue) bool { return v.Name == DefaultLLMProvider })
	}
	for _, v := range kept {
		if v.Name != DefaultLLMProvider || strings.HasPrefix(strings.TrimSpace(v.Value), "{") {
			continue
		}
		on := v.Value == "true"
		opts.TenantDefault = &on
		if on {
			opts.AccountDefault = &on
		}
		break
	}
	return opts, false, kept
}

// applyLLMSaveDefaults writes the two ticks of an explicit save.
func applyLLMSaveDefaults(ctx *security.RequestContext, tx *sqlx.Tx, integrationId string, accountIds []string, opts LLMSaveOptions) error {
	sc := ctx.GetSecurityContext()
	if opts.TenantDefault != nil {
		value := "false"
		if *opts.TenantDefault {
			value = "true"
		}
		if _, err := applyLLMTenantDefault(tx, sc.GetTenantId(), sc.GetUserId(), integrationId, value); err != nil {
			return err
		}
	}
	if opts.AccountDefault == nil || len(accountIds) == 0 {
		return nil
	}
	// The per-tenant advisory lock serialises LLM saves, so batch updates cannot deadlock.
	if *opts.AccountDefault {
		if _, err := tx.Exec(`
			UPDATE integrations_cloud_accounts SET default_llm_provider = false
			WHERE cloud_account_id = ANY($1::uuid[]) AND integration_id != $2
			  AND integration_id IN (SELECT id FROM integrations WHERE type = 'llm')`, pq.Array(accountIds), integrationId); err != nil {
			return fmt.Errorf("integrations: failed to clear the previous account defaults: %w", err)
		}
	}
	if _, err := tx.Exec(`
		UPDATE integrations_cloud_accounts SET default_llm_provider = $3
		WHERE integration_id = $2 AND cloud_account_id = ANY($1::uuid[])`, pq.Array(accountIds), integrationId, *opts.AccountDefault); err != nil {
		return fmt.Errorf("integrations: failed to set the account defaults: %w", err)
	}
	return nil
}

func markLLMDefaultsMigrated(tx *sqlx.Tx, integrationId string) error {
	_, err := tx.Exec(`
		INSERT INTO integration_config_values (integration_id, name, value, is_encrypted)
		VALUES ($1, $2, 'v1', false)
		ON CONFLICT (integration_id, name) DO NOTHING`, integrationId, llmDefaultsMigratedKey)
	if err != nil {
		return fmt.Errorf("integrations: failed to mark LLM integration: %w", err)
	}
	return nil
}
