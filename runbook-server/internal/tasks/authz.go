package tasks

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"nudgebee/runbook/common"
	"nudgebee/runbook/internal/tasks/tickets"
	"nudgebee/runbook/services/security"

	"go.temporal.io/sdk/temporal"
)

// StepRequirement is what a step type needs and how much it can change.
//
// A step needs exactly the grant for what it acts on, on the account it
// touches, in the platform's permission vocabulary (`<module>:<class>`, the
// same strings custom roles grant):
//   - Module and Class: a step that acts on a platform area (a cluster, a cloud
//     account, notifications) needs <Module>:<Class>.
//   - Through: a step that acts through an integration (a ticket, an SSH host,
//     a database, an MCP server, a source-control CLI) needs the use grant for
//     that integration's stored type, integrations.<type>:<Class>, and nothing
//     else. Read uses it read-only; Execute also makes changes.
//   - needs: a step whose effect depends on its params (a cluster change that
//     GitOps turns into a pull request) works its needs out from them.
//
// A step with none of these has no effect outside the run (control flow, data
// shaping) and needs no permission. People and bees are held to the same
// needs; StepNeeds lists them.
//
// Tier is how much the step can change, whoever runs it: see TierRead,
// TierChange and TierUnrestricted. An automation's tier is the highest tier
// among its steps; it decides how a packaged agent's automation may run
// unattended, and a run that carries a tier ceiling refuses any step above it.
type StepRequirement struct {
	Module  string
	Class   string
	Tier    int
	Through *IntegrationParam
	// needs works the step's needs out from its params; fields lists the
	// integration params it reads (TestEveryIntegrationFieldIsChecked).
	needs  func(params map[string]any) []need
	fields []string
	// also is a platform grant the service a through step calls checks besides
	// the use grant (ticket-server checks tickets). Authorize does not check it,
	// a person's role carries it; ServiceNeeds lists it for a bee's install.
	also *need
}

// IntegrationParam is the param a through step names its integration in (a
// dotted path into the params), and the stored types the task accepts there.
type IntegrationParam struct {
	Param string
	Types []string
}

// None reports whether the step needs no permission.
func (r StepRequirement) None() bool { return r.Module == "" && r.Through == nil && r.needs == nil }

// IntegrationFields are the params naming an integration that the step is
// checked on.
func (r StepRequirement) IntegrationFields() []string {
	if r.Through != nil {
		return []string{r.Through.Param}
	}
	return r.fields
}

// Tiers, lowest first.
const (
	// TierRead steps read, take notes, file and update tickets, or shape data.
	// They run automatically by default.
	TierRead = 1
	// TierChange steps make a bounded change to a running system, or send a
	// message outside the run. They ask first in production by default.
	TierChange = 2
	// TierUnrestricted steps take free-form input — a CLI command, a script, a
	// query, an HTTP or SSH request — or start something whose steps cannot be
	// seen. Their effect cannot be bounded, so they ask first everywhere by
	// default.
	TierUnrestricted = 3
)

// TierFor returns the tier of a task type. The second result is false for a
// type that declares none; the tier returned is then the highest.
func TierFor(taskName string) (int, bool) {
	r, ok := stepRequirements[taskName]
	if !ok || r.Tier == 0 {
		return TierUnrestricted, false
	}
	return r.Tier, true
}

const (
	classRead    = "Read"
	classWrite   = "Write"
	classExecute = "Execute"
)

func read(module string) StepRequirement  { return StepRequirement{Module: module, Class: classRead} }
func write(module string) StepRequirement { return StepRequirement{Module: module, Class: classWrite} }
func execute(module string) StepRequirement {
	return StepRequirement{Module: module, Class: classExecute}
}

// through is a step that acts only through the integration named in param,
// one of types: it needs integrations.<type>:class.
func through(class, param string, types ...string) StepRequirement {
	return StepRequirement{Class: class, Through: &IntegrationParam{Param: param, Types: types}}
}

// dynamic is a step whose needs depend on its params.
func dynamic(needs func(map[string]any) []need, fields ...string) StepRequirement {
	return StepRequirement{needs: needs, fields: fields}
}

// none is a step that needs no permission.
func none(tier int) StepRequirement { return StepRequirement{Tier: tier} }

// alsoNeeds adds <module>:<class> to a through step, for a service that checks
// the caller's module grant as well as the integration.
func (r StepRequirement) alsoNeeds(module, class string) StepRequirement {
	r.also = &need{module: module, class: class}
	return r
}

// at sets the requirement's tier.
func (r StepRequirement) at(tier int) StepRequirement {
	r.Tier = tier
	return r
}

// dbmsTypes are the integration types dbms.query accepts, as it resolves them.
var dbmsTypes = []string{"mysql", "postgresql", "clickhouse", "mssql", "oracle"}

// stepRequirements declares, for every registered task type, what a run's
// principal must hold for the step to execute, and the step's tier. Unlisted
// task types are refused (fail closed); TestEveryRegisteredTaskDeclaresAStepRequirement
// keeps this table complete, and TestEveryIntegrationFieldIsChecked keeps every
// integration a task can be pointed at checked.
//
// A step that changes something needs Execute on its module, the right to run,
// which Write (manage) also covers. Steps on the notifications and events
// modules stay on Write: Execute there is the class the role editor offers for
// narrow actions (a channel watch, an analysis digest), and a role holding it
// for those must not start sending messages. A step that calls an endpoint of
// its own choosing with no integration behind it (HTTP, A2A, an MCP URL) needs
// integrations:Write.
//
// Tasks whose effect depends on free-form input (CLIs, scripts, HTTP, SSH, DB
// queries) need the change class and are TierUnrestricted: a read-only command
// cannot be told apart from a write without parsing it. The source-control CLIs
// run their command as a shell script with the integration's token in its
// environment; core.call-workflow and the agent runs start steps this table
// never sees.
var stepRequirements = map[string]StepRequirement{
	// Control flow and pure data shaping: no effect outside the run.
	"core.foreach":   none(TierRead),
	"core.group":     none(TierRead),
	"core.print":     none(TierRead),
	"core.switch":    none(TierRead),
	"core.wait":      none(TierRead),
	"crypto.decode":  none(TierRead),
	"crypto.decrypt": none(TierRead),
	"crypto.encode":  none(TierRead),
	"crypto.encrypt": none(TierRead),
	"crypto.hash":    none(TierRead),
	"data.filter":    none(TierRead),
	"data.transform": none(TierRead),

	// Posts the approval request to Slack/email.
	"core.approval": write("notifications").at(TierRead),
	// The callee's steps are checked one by one; calling it is running a workflow.
	"core.call-workflow": execute("workflows").at(TierUnrestricted),

	// Outbound network probes from the server. No account, no external change.
	"network.dns":        execute("workflows").at(TierRead),
	"network.ntp":        execute("workflows").at(TierRead),
	"network.ping":       execute("workflows").at(TierRead),
	"network.ssl":        execute("workflows").at(TierRead),
	"network.tcp":        execute("workflows").at(TierRead),
	"network.traceroute": execute("workflows").at(TierRead),
	"network.whois":      execute("workflows").at(TierRead),

	"k8s.cli":                    execute("k8s").at(TierUnrestricted),
	"k8s.continuous_rightsize":   execute("k8s").at(TierChange),
	"k8s.node_graceful_shutdown": execute("k8s").at(TierChange),
	"k8s.pod_delete":             execute("k8s").at(TierChange),
	"k8s.profile":                execute("k8s").at(TierChange),
	"k8s.workload_restart":       execute("k8s").at(TierChange),
	"cloud.k8s.cli":              execute("k8s").at(TierUnrestricted),
	// With GitOps (or a ticket) on, the change becomes a pull request (or a
	// ticket) and the cluster is only read: see rightsizeNeeds.
	"k8s.horizontal_rightsize": dynamic(rightsizeNeeds(true), gitOpsFields...).at(TierChange),
	"k8s.vertical_rightsize":   dynamic(rightsizeNeeds(true), gitOpsFields...).at(TierChange),
	"k8s.pv_rightsize":         dynamic(rightsizeNeeds(false), "gitops_config.integration_id").at(TierChange),

	"aws.cli":         execute("cloud").at(TierUnrestricted),
	"azure.cli":       execute("cloud").at(TierUnrestricted),
	"gcp.cli":         execute("cloud").at(TierUnrestricted),
	"cloud.aws.cli":   execute("cloud").at(TierUnrestricted),
	"cloud.azure.cli": execute("cloud").at(TierUnrestricted),
	"cloud.gcp.cli":   execute("cloud").at(TierUnrestricted),

	// Steps that act through the integration they name.
	"cicd.argocd.cli":      through(classExecute, "integration_id", "argocd").at(TierUnrestricted),
	"dbms.query":           through(classExecute, "integration_id", dbmsTypes...).at(TierUnrestricted),
	"dbms.redis.cli":       through(classExecute, "integration_id", "redis").at(TierUnrestricted),
	"integrations.ssh":     through(classExecute, "integration_id", "ssh").at(TierUnrestricted),
	"mq.rabbitmqadmin.cli": through(classExecute, "integration_id", "rabbitmq").at(TierUnrestricted),
	"scm.github.cli":       through(classExecute, "integration_id", "github").at(TierUnrestricted),
	"scm.gitlab.cli":       through(classExecute, "integration_id", "gitlab").at(TierUnrestricted),
	// Through the MCP integration it names, or straight to a URL.
	"llm.mcp_call": dynamic(mcpCallNeeds, "integration_id").at(TierUnrestricted),

	// Free-form endpoints with no integration behind them.
	"integrations.http": write("integrations").at(TierUnrestricted),
	"llm.a2a_call":      write("integrations").at(TierUnrestricted),

	// Runs arbitrary code with the account's credentials: only someone who could
	// have written the script may run it, and over SSH only through an SSH
	// integration they may use.
	"scripting.run_script": dynamic(runScriptNeeds, "integration_id").at(TierUnrestricted),

	"llm.classify":          execute("llm").at(TierRead),
	"llm.event_investigate": execute("llm").at(TierUnrestricted),
	"llm.investigate":       execute("llm").at(TierUnrestricted),
	"llm.nubi":              execute("llm").at(TierUnrestricted),
	"llm.router":            execute("llm").at(TierRead),
	"llm.summary":           execute("llm").at(TierRead),

	"events.add_evidence": write("events").at(TierRead),
	"events.store":        write("events").at(TierRead),

	"observability.log_groups": read("logs").at(TierRead),
	"observability.logs":       read("logs").at(TierRead),
	"observability.metrics":    read("metrics").at(TierRead),
	"observability.traces":     read("traces").at(TierRead),

	"notifications.add_channel_members": write("notifications").at(TierChange),
	"notifications.add_reaction":        write("notifications").at(TierRead),
	"notifications.create_channel":      write("notifications").at(TierChange),
	"notifications.dm":                  write("notifications").at(TierChange),
	"notifications.email":               write("notifications").at(TierChange),
	"notifications.im":                  write("notifications").at(TierChange),
	"notifications.read_thread":         read("notifications").at(TierRead),
	"slack.join_channel":                write("notifications").at(TierChange),
	"google_chat.join_space":            write("notifications").at(TierChange),

	// ticket-server checks tickets:Read (get, comments, add_comment) or
	// tickets:Write (the rest) on the account besides the integration. create
	// is not checked there.
	"tickets.acknowledge":  through(classExecute, "integration_id", tickets.IncidentPlatforms()...).alsoNeeds("tickets", classWrite).at(TierRead),
	"tickets.add_comment":  through(classExecute, "integration_id", tickets.TicketPlatforms()...).alsoNeeds("tickets", classRead).at(TierRead),
	"tickets.assign":       through(classExecute, "integration_id", tickets.TicketPlatforms()...).alsoNeeds("tickets", classWrite).at(TierRead),
	"tickets.create":       through(classExecute, "integration_id", tickets.TicketPlatforms()...).at(TierRead),
	"tickets.escalate":     through(classExecute, "integration_id", tickets.IncidentPlatforms()...).alsoNeeds("tickets", classWrite).at(TierRead),
	"tickets.get":          through(classRead, "integration_id", tickets.TicketPlatforms()...).alsoNeeds("tickets", classRead).at(TierRead),
	"tickets.get_comments": through(classRead, "integration_id", tickets.TicketPlatforms()...).alsoNeeds("tickets", classRead).at(TierRead),
	"tickets.resolve":      through(classExecute, "integration_id", tickets.IncidentPlatforms()...).alsoNeeds("tickets", classWrite).at(TierChange),
	"tickets.transition":   through(classExecute, "integration_id", tickets.TicketPlatforms()...).alsoNeeds("tickets", classWrite).at(TierChange),
	// Can set a ticket's status, the same effect as transition and resolve.
	"tickets.update": through(classExecute, "integration_id", tickets.TicketPlatforms()...).alsoNeeds("tickets", classWrite).at(TierChange),

	"vertical_rightsize_generate": execute("recommendations").at(TierRead),
}

// need is one grant a step needs: <module>:<class>, or, when use is set, the
// use grant (class Read or Execute) for the integration it names.
type need struct {
	module string
	class  string
	use    *integrationUse
}

// integrationUse is an integration a step acts through.
type integrationUse struct {
	// param is where the step names it, ref what it names there (an id or a
	// name; empty when the step names none).
	param string
	ref   string
	// types are the stored types the task accepts. With no ref and one type,
	// that type is the integration's.
	types []string
	// optional: ref may name a repository rather than an integration; one the
	// tenant does not have counts as naming none.
	optional bool
}

func useOf(class, param, ref string, types []string) need {
	return need{class: class, use: &integrationUse{param: param, ref: ref, types: types}}
}

// gitOpsFields are the GitOps and ticket settings rightsizeNeeds reads.
var gitOpsFields = []string{"gitops_config.integration_id", "gitops_config.name", "gitops_config.repository_name", "ticket_config.configuration_id"}

// rightsizeNeeds is a rightsize step's needs. With GitOps on, the change
// becomes a pull request (vertical, horizontal: HandleGitOpsOrTicket) or an
// issue (pv) through a source-control integration, and the workload's spec is
// read from the cluster into it: Execute on that integration and Read on k8s.
// With a ticket setting on (vertical, horizontal), the same through the ticket
// integration. Otherwise the cluster is patched: Execute on k8s.
func rightsizeNeeds(ticketSetting bool) func(map[string]any) []need {
	return func(params map[string]any) []need {
		if cfg, ok := enabledConfig(params, "gitops_config"); ok {
			return append([]need{{module: "k8s", class: classRead}}, gitOpsUses(cfg, ticketSetting)...)
		}
		if cfg, ok := enabledConfig(params, "ticket_config"); ticketSetting && ok {
			return []need{
				{module: "k8s", class: classRead},
				useOf(classExecute, "ticket_config.configuration_id", stringParam(cfg, "configuration_id"), tickets.TicketPlatforms()),
			}
		}
		return []need{{module: "k8s", class: classExecute}}
	}
}

// gitOpsUses are the integrations a GitOps setting opens its pull request (or
// issue) through. HandleGitOpsOrTicket picks the integration by
// repository_name, name or integration_id, pv_rightsize by integration_id
// only, so each one given is checked. repository_name may name a repository
// instead. With none named, the integration is one of the provider's type
// (GitHub unless set), whose grant is then needed.
func gitOpsUses(cfg map[string]any, byName bool) []need {
	provider := stringParam(cfg, "provider")
	if provider == "" || !byName {
		provider = "github"
	}
	types := []string{strings.ToLower(provider)}
	keys := []string{"integration_id"}
	if byName {
		keys = append(keys, "name")
	}
	var uses []need
	for _, key := range keys {
		if ref := stringParam(cfg, key); ref != "" {
			uses = append(uses, useOf(classExecute, "gitops_config."+key, ref, types))
		}
	}
	if ref := stringParam(cfg, "repository_name"); byName && ref != "" {
		u := useOf(classExecute, "gitops_config.repository_name", ref, types)
		u.use.optional = true
		uses = append(uses, u)
	}
	if len(uses) == 0 {
		uses = append(uses, useOf(classExecute, "gitops_config.provider", "", types))
	}
	return uses
}

// mcpCallNeeds: an MCP call with an integration_id goes through that MCP
// integration (MCPTask.Execute); without one it calls the URL it is given.
func mcpCallNeeds(params map[string]any) []need {
	if ref := stringParam(params, "integration_id"); ref != "" {
		return []need{useOf(classExecute, "integration_id", ref, []string{"mcp"})}
	}
	return []need{{module: "integrations", class: classWrite}}
}

// scriptExecutorsWithoutIntegration are the run_script executors that do not
// use integration_id (only the SSH executor does).
var scriptExecutorsWithoutIntegration = map[string]bool{
	"kubernetes": true, "agent": true, "aws_ssm": true, "azure_run_command": true, "gcp_compute_ssh": true,
}

// runScriptNeeds: Write on workflows, and over SSH, the use grant for the SSH
// integration the script runs on.
func runScriptNeeds(params map[string]any) []need {
	needs := []need{{module: "workflows", class: classWrite}}
	executor := stringParam(params, "executor_type")
	if ref := stringParam(params, "integration_id"); ref != "" && !scriptExecutorsWithoutIntegration[executor] {
		needs = append(needs, useOf(classExecute, "integration_id", ref, []string{"ssh"}))
	}
	return needs
}

// stepNeeds lists what a step with these params needs.
func (r StepRequirement) stepNeeds(params map[string]any) []need {
	switch {
	case r.needs != nil:
		return r.needs(params)
	case r.Through != nil:
		return []need{useOf(r.Class, r.Through.Param, paramAt(params, r.Through.Param), r.Through.Types)}
	case r.Module != "":
		return []need{{module: r.Module, class: r.Class}}
	}
	return nil
}

// paramAt reads a string param by dotted path.
func paramAt(params map[string]any, path string) string {
	keys := strings.Split(path, ".")
	for _, key := range keys[:len(keys)-1] {
		nested, ok := params[key].(map[string]any)
		if !ok {
			return ""
		}
		params = nested
	}
	return stringParam(params, keys[len(keys)-1])
}

// Grant is one `<module>:<class>` a principal holds.
type Grant struct {
	Module string `json:"module"`
	Class  string `json:"class"`
}

// IntegrationResolver returns the stored types of the integration ref names:
// the type of the integration with that id, or of every enabled integration of
// one of accepted with that name. It returns ErrIntegrationNotFound when there
// is none.
type IntegrationResolver func(ctx context.Context, ref string, accepted []string) ([]string, error)

var (
	// ErrIntegrationNotFound: the step names an integration the tenant does not have.
	ErrIntegrationNotFound = errors.New("integration not found")
	// errNoIntegrationNamed: the step names no integration and accepts more than
	// one type, so which grant it needs cannot be told.
	errNoIntegrationNamed = errors.New("names no integration")
)

// grants resolves an integration use to the grants it needs: Read or
// Execute on integrations.<type> for each type it may be.
func (u integrationUse) grants(ctx context.Context, class string, resolve IntegrationResolver) ([]Grant, error) {
	types := u.types
	if u.ref != "" {
		found, err := resolve(ctx, u.ref, u.types)
		switch {
		case errors.Is(err, ErrIntegrationNotFound) && u.optional:
		case err != nil:
			return nil, err
		default:
			types = found
		}
	}
	if u.ref == "" && len(types) != 1 {
		return nil, errNoIntegrationNamed
	}
	if len(types) == 0 {
		return nil, ErrIntegrationNotFound
	}
	grants := make([]Grant, 0, len(types))
	for _, t := range types {
		grants = append(grants, Grant{Module: security.IntegrationModule(t), Class: class})
	}
	return grants, nil
}

// StepNeeds is every grant a principal needs, on the step's account, to run a
// step of taskName with params; resolve answers which integration a param
// names. Authorize checks these, and a bee's install grants them. A type
// with no requirement declared is an error; a step that needs nothing returns
// none.
func StepNeeds(ctx context.Context, taskName string, params map[string]any, resolve IntegrationResolver) ([]Grant, error) {
	req, ok := RequirementFor(taskName)
	if !ok {
		return nil, fmt.Errorf("step type %q declares no permission requirement", taskName)
	}
	var grants []Grant
	for _, n := range req.stepNeeds(params) {
		if n.use == nil {
			grants = append(grants, Grant{Module: n.module, Class: n.class})
			continue
		}
		g, err := n.use.grants(ctx, n.class, resolve)
		if err != nil {
			return nil, fmt.Errorf("step type %s, %s %q: %w", taskName, n.use.param, n.use.ref, err)
		}
		grants = append(grants, g...)
	}
	return grants, nil
}

// ServiceNeeds is the grant the service a step calls checks on the caller
// besides the step's own needs, if any. A bee's install grants it, as the bee
// has no role to carry it.
func ServiceNeeds(taskName string) []Grant {
	if r, ok := stepRequirements[taskName]; ok && r.also != nil {
		return []Grant{{Module: r.also.module, Class: r.also.class}}
	}
	return nil
}

// RequirementFor returns the declared requirement for a task type.
func RequirementFor(taskName string) (StepRequirement, bool) {
	r, ok := stepRequirements[taskName]
	return r, ok
}

// StepAccount is the account a step acts on: its `account_id` param when set,
// else the run's account. Every task that can target another account takes it
// as `account_id` (an account-typed property) and falls back to the run's
// account the same way.
func StepAccount(params map[string]any, runAccountID string) string {
	if v, ok := params["account_id"].(string); ok && v != "" {
		return v
	}
	return runAccountID
}

// StepAuthInput is one step about to run.
type StepAuthInput struct {
	TaskName  string
	TenantID  string
	Principal string
	// RunAccountID is the workflow's account. When CheckRunRight is set the
	// principal must also be allowed to run workflows there — the right a
	// scheduled or event run's owner needs, and which a manual run's requester
	// already proved at the route.
	RunAccountID  string
	StepAccountID string
	CheckRunRight bool
	// Params are the step's params, rendered: they name the integrations a
	// step acts through and turn some changes into pull requests.
	Params map[string]any
}

// StepAuthorizer checks a step against its run's principal. The loaders are
// fields so tests can stub them.
type StepAuthorizer struct {
	// LoadContext returns the principal's permissions (roles + custom grants).
	LoadContext func(ctx context.Context, tenantID, userID string) (*security.SecurityContext, error)
	// UserStatus reads the user's state. Read directly rather than from the
	// cached security context, so a suspension takes effect on the next step.
	UserStatus func(ctx context.Context, tenantID, userID string) (UserState, error)
}

// UserState is what Authorize reads about a principal from the metastore.
type UserState struct {
	// Active: the user is active and still a member of the tenant.
	Active bool
}

// DefaultStepAuthorizer loads contexts from the services server (through a
// short-lived cache, so a loop over many items does not make one call per item)
// and status from the metastore.
var DefaultStepAuthorizer = StepAuthorizer{
	LoadContext: newContextCache(contextCacheTTL, func(tenantID, userID string) (*security.SecurityContext, error) {
		return security.NewSecurityContext(tenantID, userID)
	}).load,
	UserStatus: activeTenantMember,
}

// contextCacheTTL bounds how stale a principal's permissions can be within this
// worker. The services server already caches them for 30 minutes, so this adds
// no staleness in practice; it only saves the round trip. User status is not
// cached (see UserStatus).
const contextCacheTTL = time.Minute

type cachedContext struct {
	sc      *security.SecurityContext
	expires time.Time
}

type contextCache struct {
	ttl   time.Duration
	fetch func(tenantID, userID string) (*security.SecurityContext, error)
	mu    sync.Mutex
	items map[string]cachedContext
}

func newContextCache(ttl time.Duration, fetch func(tenantID, userID string) (*security.SecurityContext, error)) *contextCache {
	return &contextCache{ttl: ttl, fetch: fetch, items: map[string]cachedContext{}}
}

func (c *contextCache) load(_ context.Context, tenantID, userID string) (*security.SecurityContext, error) {
	key := tenantID + ":" + userID
	now := time.Now()
	c.mu.Lock()
	if it, ok := c.items[key]; ok && now.Before(it.expires) {
		c.mu.Unlock()
		return it.sc, nil
	}
	c.mu.Unlock()

	sc, err := c.fetch(tenantID, userID)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	for k, it := range c.items { // keep the map bounded by live entries
		if now.After(it.expires) {
			delete(c.items, k)
		}
	}
	c.items[key] = cachedContext{sc: sc, expires: now.Add(c.ttl)}
	c.mu.Unlock()
	return sc, nil
}

func activeTenantMember(ctx context.Context, tenantID, userID string) (UserState, error) {
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return UserState{}, err
	}
	var state UserState
	err = dbManager.Db.QueryRowContext(ctx, `
		SELECT u.status = 'active'
		   AND EXISTS (SELECT 1 FROM tenant_users tu WHERE tu."user" = u.id AND tu.tenant = $2)
		FROM users u WHERE u.id = $1`, userID, tenantID).Scan(&state.Active)
	if errors.Is(err, sql.ErrNoRows) {
		return UserState{}, nil
	}
	return state, err
}

// ErrTypeStepPermissionDenied is the Temporal error type of a refused step. It
// is non-retryable: retrying cannot grant the permission.
const ErrTypeStepPermissionDenied = "StepPermissionDenied"

func denied(format string, args ...any) error {
	return temporal.NewNonRetryableApplicationError(fmt.Sprintf(format, args...), ErrTypeStepPermissionDenied, nil)
}

// builtInAccess is the built-in role access a class needs, as SecurityContext.Can maps it.
var builtInAccess = map[string]security.SecurityAccessType{
	classRead:    security.SecurityAccessTypeRead,
	classExecute: security.SecurityAccessTypeCreate,
	classWrite:   security.SecurityAccessTypeUpdate,
}

// Authorize returns nil when the step may run as in.Principal. A refusal is a
// non-retryable error whose message names the principal, the missing
// permission and the account. A failure to load the principal's permissions,
// or to read an integration, is a plain (retryable) error.
func (a StepAuthorizer) Authorize(ctx context.Context, in StepAuthInput) error {
	req, ok := RequirementFor(in.TaskName)
	if !ok {
		return denied("permission denied: step type %q declares no permission requirement, so it cannot run", in.TaskName)
	}
	if req.None() {
		return nil
	}
	if in.Principal == "" || in.Principal == security.GetSystemUserId() {
		return denied("permission denied: this run has no user to act as (the automation's owner may have been deleted); make a version live again to set an owner")
	}

	state, err := a.UserStatus(ctx, in.TenantID, in.Principal)
	if err != nil {
		return fmt.Errorf("authorization unavailable: could not read user %s: %w", in.Principal, err)
	}
	if !state.Active {
		return denied("permission denied: this run acts as user %s, who is not active in this organisation", in.Principal)
	}

	sc, err := a.LoadContext(ctx, in.TenantID, in.Principal)
	if err != nil {
		return fmt.Errorf("authorization unavailable: could not load permissions for user %s: %w", in.Principal, err)
	}

	if in.CheckRunRight &&
		!sc.HasAccountAccess(in.RunAccountID, security.SecurityAccessTypeCreate) &&
		!sc.HasScopedPermission(in.RunAccountID, "workflows", classExecute) &&
		!sc.HasScopedPermission(in.RunAccountID, "workflows", classWrite) {
		return denied("permission denied: user %s may not run automations on account %s", in.Principal, in.RunAccountID)
	}

	for _, n := range req.stepNeeds(in.Params) {
		if n.use == nil {
			// The same rule the platform's own routes use: a built-in account
			// role, or a custom grant for the module on that account.
			if !sc.Can(in.StepAccountID, n.module, n.class) {
				return denied("permission denied: user %s needs %s:%s on account %s for step type %s",
					in.Principal, n.module, n.class, in.StepAccountID, in.TaskName)
			}
			continue
		}
		if err := a.authorizeUse(ctx, sc, in, n); err != nil {
			return err
		}
	}
	return nil
}

// authorizeUse checks a step's use of an integration: a built-in account role
// allows any integration, as it allowed the step's module before; otherwise
// the principal needs the use grant for each type the integration may be.
func (a StepAuthorizer) authorizeUse(ctx context.Context, sc *security.SecurityContext, in StepAuthInput, n need) error {
	if sc.HasAccountAccess(in.StepAccountID, builtInAccess[n.class]) {
		return nil
	}
	if strings.Contains(n.use.ref, "{{") {
		return denied("permission denied: step type %s names its integration in %s by a template (%s), so which integration it uses cannot be checked yet",
			in.TaskName, n.use.param, n.use.ref)
	}
	resolve := func(ctx context.Context, ref string, accepted []string) ([]string, error) {
		return integrationTypes(ctx, in.TenantID, ref, accepted)
	}
	grants, err := n.use.grants(ctx, n.class, resolve)
	switch {
	case errors.Is(err, ErrIntegrationNotFound):
		return denied("permission denied: step type %s uses integration %q, which this organisation does not have", in.TaskName, n.use.ref)
	case errors.Is(err, errNoIntegrationNamed):
		return denied("permission denied: step type %s names no integration in %s, so which one it uses cannot be checked", in.TaskName, n.use.param)
	case err != nil:
		return fmt.Errorf("authorization unavailable: could not read integration %q: %w", n.use.ref, err)
	}
	for _, g := range grants {
		typ := strings.TrimPrefix(g.Module, "integrations.")
		if !sc.MayUseIntegration(in.StepAccountID, typ, n.class != classRead) {
			named := ""
			if n.use.ref != "" {
				named = fmt.Sprintf(" %q", n.use.ref)
			}
			return denied("permission denied: user %s needs %s:%s on account %s to use %s integration%s for step type %s",
				in.Principal, g.Module, g.Class, in.StepAccountID, typ, named, in.TaskName)
		}
	}
	return nil
}
