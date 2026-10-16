package tasks

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	"nudgebee/runbook/common"
	"nudgebee/runbook/services/security"

	"go.temporal.io/sdk/temporal"
)

// StepRequirement is what a step type needs and how much it can change.
//
// Module and Class are the permission a principal needs, on the account a step
// touches, for that step to run. They use the platform's permission vocabulary
// (`<module>:<Read|Write|Execute>`, the same strings custom roles grant). An
// empty Module means the step has no effect outside the run (control flow,
// data shaping) and needs no permission.
//
// Tier is how much the step can change, whoever runs it: see TierRead,
// TierChange and TierUnrestricted. An automation's tier is the highest tier
// among its steps; it decides how a packaged agent's automation may run
// unattended, and a run that carries a tier ceiling refuses any step above it.
type StepRequirement struct {
	Module string
	Class  string
	Tier   int
}

// None reports whether the step needs no permission.
func (r StepRequirement) None() bool { return r.Module == "" }

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

// none is a step that needs no permission.
func none(tier int) StepRequirement { return StepRequirement{Tier: tier} }

// at sets the requirement's tier.
func (r StepRequirement) at(tier int) StepRequirement {
	r.Tier = tier
	return r
}

// stepRequirements declares, for every registered task type, what a run's
// principal must hold for the step to execute, and the step's tier. Unlisted
// task types are refused (fail closed); TestEveryRegisteredTaskDeclaresAStepRequirement
// keeps this table complete.
//
// Tasks whose effect depends on free-form input (CLIs, scripts, HTTP, SSH, DB
// queries) are Write and TierUnrestricted: a read-only command cannot be told
// apart from a write without parsing it. The source-control CLIs run their
// command as a shell script with the integration's token in its environment;
// core.call-workflow and the agent runs start steps this table never sees.
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

	"k8s.cli":                    write("k8s").at(TierUnrestricted),
	"k8s.continuous_rightsize":   write("k8s").at(TierChange),
	"k8s.horizontal_rightsize":   write("k8s").at(TierChange),
	"k8s.node_graceful_shutdown": write("k8s").at(TierChange),
	"k8s.pod_delete":             write("k8s").at(TierChange),
	"k8s.profile":                write("k8s").at(TierChange),
	"k8s.pv_rightsize":           write("k8s").at(TierChange),
	"k8s.vertical_rightsize":     write("k8s").at(TierChange),
	"k8s.workload_restart":       write("k8s").at(TierChange),
	"cloud.k8s.cli":              write("k8s").at(TierUnrestricted),
	"cicd.argocd.cli":            write("k8s").at(TierUnrestricted),

	"aws.cli":         write("cloud").at(TierUnrestricted),
	"azure.cli":       write("cloud").at(TierUnrestricted),
	"gcp.cli":         write("cloud").at(TierUnrestricted),
	"cloud.aws.cli":   write("cloud").at(TierUnrestricted),
	"cloud.azure.cli": write("cloud").at(TierUnrestricted),
	"cloud.gcp.cli":   write("cloud").at(TierUnrestricted),

	"dbms.query":     write("database").at(TierUnrestricted),
	"dbms.redis.cli": write("database").at(TierUnrestricted),

	"integrations.http":    write("integrations").at(TierUnrestricted),
	"integrations.ssh":     write("integrations").at(TierUnrestricted),
	"mq.rabbitmqadmin.cli": write("integrations").at(TierUnrestricted),
	"scm.github.cli":       write("integrations").at(TierUnrestricted),
	"scm.gitlab.cli":       write("integrations").at(TierUnrestricted),
	"llm.mcp_call":         write("integrations").at(TierUnrestricted),
	"llm.a2a_call":         write("integrations").at(TierUnrestricted),

	// Runs arbitrary code with the account's credentials: only someone who could
	// have written the script may run it.
	"scripting.run_script": write("workflows").at(TierUnrestricted),

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

	"tickets.acknowledge":  write("tickets").at(TierRead),
	"tickets.add_comment":  write("tickets").at(TierRead),
	"tickets.assign":       write("tickets").at(TierRead),
	"tickets.create":       write("tickets").at(TierRead),
	"tickets.escalate":     write("tickets").at(TierRead),
	"tickets.get":          read("tickets").at(TierRead),
	"tickets.get_comments": read("tickets").at(TierRead),
	"tickets.resolve":      write("tickets").at(TierChange),
	"tickets.transition":   write("tickets").at(TierChange),
	// Can set a ticket's status, the same effect as transition and resolve.
	"tickets.update": write("tickets").at(TierChange),

	"vertical_rightsize_generate": write("recommendations").at(TierRead),
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
	// Integrations are the integrations the step will use (IntegrationUses).
	// A bee needs a grant for each one's type.
	Integrations []IntegrationUse
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
	// Bee: the user is a bee account (users.kind = 'bee'), which uses an
	// integration only with a grant for that integration's type.
	Bee bool
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
		   AND EXISTS (SELECT 1 FROM tenant_users tu WHERE tu."user" = u.id AND tu.tenant = $2),
		       u.kind = 'bee'
		FROM users u WHERE u.id = $1`, userID, tenantID).Scan(&state.Active, &state.Bee)
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

// Authorize returns nil when the step may run as in.Principal. A refusal is a
// non-retryable error whose message names the principal, the missing
// permission and the account. A failure to load the principal's permissions is
// a plain (retryable) error.
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

	// The same rule the platform's own routes use: a built-in account role, or
	// a custom grant for the module on that account.
	if !sc.Can(in.StepAccountID, req.Module, req.Class) {
		return denied("permission denied: user %s needs %s:%s on account %s for step type %s",
			in.Principal, req.Module, req.Class, in.StepAccountID, in.TaskName)
	}

	if state.Bee {
		return authorizeIntegrations(ctx, sc, in, req.Class != classRead)
	}
	return nil
}

// authorizeIntegrations refuses a bee's step that uses an integration whose
// type the bee holds no use grant for on the step's account
// (SecurityContext.MayUseIntegration). A step that names an integration this
// organisation does not have is refused too.
func authorizeIntegrations(ctx context.Context, sc *security.SecurityContext, in StepAuthInput, write bool) error {
	class := classRead
	if write {
		class = classExecute
	}
	for _, use := range in.Integrations {
		types, err := integrationTypes(ctx, in.TenantID, use)
		if errors.Is(err, errIntegrationNotFound) && use.Optional {
			continue
		}
		if errors.Is(err, errIntegrationNotFound) {
			return denied("permission denied: step type %s uses integration %q, which this organisation does not have", in.TaskName, use.Ref)
		}
		if err != nil {
			return fmt.Errorf("authorization unavailable: could not read integration %q: %w", use.Ref, err)
		}
		for _, typ := range types {
			if !sc.MayUseIntegration(in.StepAccountID, typ, write) {
				return denied("permission denied: user %s needs %s:%s on account %s to use %s integration %q for step type %s",
					in.Principal, security.IntegrationModule(typ), class, in.StepAccountID, typ, use.Ref, in.TaskName)
			}
		}
	}
	return nil
}
