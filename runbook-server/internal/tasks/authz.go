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

// StepRequirement is the permission a principal needs, on the account a step
// touches, for that step to run. Module and Class use the platform's permission
// vocabulary (`<module>:<Read|Write|Execute>`, the same strings custom roles
// grant). A zero StepRequirement means the step has no effect outside the run
// (control flow, data shaping) and needs no check.
type StepRequirement struct {
	Module string
	Class  string
}

// None reports whether the step needs no permission.
func (r StepRequirement) None() bool { return r.Module == "" }

const (
	classRead    = "Read"
	classWrite   = "Write"
	classExecute = "Execute"
)

func read(module string) StepRequirement    { return StepRequirement{module, classRead} }
func write(module string) StepRequirement   { return StepRequirement{module, classWrite} }
func execute(module string) StepRequirement { return StepRequirement{module, classExecute} }

// stepRequirements declares, for every registered task type, what a run's
// principal must hold for the step to execute. Unlisted task types are refused
// (fail closed); TestEveryRegisteredTaskDeclaresAStepRequirement keeps this
// table complete.
//
// Tasks whose effect depends on free-form input (CLIs, scripts, HTTP, SSH, DB
// queries) are Write: a read-only command cannot be told apart from a write
// without parsing it.
var stepRequirements = map[string]StepRequirement{
	// Control flow and pure data shaping: no effect outside the run.
	"core.foreach":   {},
	"core.group":     {},
	"core.print":     {},
	"core.switch":    {},
	"core.wait":      {},
	"crypto.decode":  {},
	"crypto.decrypt": {},
	"crypto.encode":  {},
	"crypto.encrypt": {},
	"crypto.hash":    {},
	"data.filter":    {},
	"data.transform": {},

	// Posts the approval request to Slack/email.
	"core.approval": write("notifications"),
	// The callee's steps are checked one by one; calling it is running a workflow.
	"core.call-workflow": execute("workflows"),

	// Outbound network probes from the server. No account, no external change.
	"network.dns":        execute("workflows"),
	"network.ntp":        execute("workflows"),
	"network.ping":       execute("workflows"),
	"network.ssl":        execute("workflows"),
	"network.tcp":        execute("workflows"),
	"network.traceroute": execute("workflows"),
	"network.whois":      execute("workflows"),

	"k8s.cli":                    write("k8s"),
	"k8s.continuous_rightsize":   write("k8s"),
	"k8s.horizontal_rightsize":   write("k8s"),
	"k8s.node_graceful_shutdown": write("k8s"),
	"k8s.pod_delete":             write("k8s"),
	"k8s.profile":                write("k8s"),
	"k8s.pv_rightsize":           write("k8s"),
	"k8s.vertical_rightsize":     write("k8s"),
	"k8s.workload_restart":       write("k8s"),
	"cloud.k8s.cli":              write("k8s"),
	"cicd.argocd.cli":            write("k8s"),

	"aws.cli":         write("cloud"),
	"azure.cli":       write("cloud"),
	"gcp.cli":         write("cloud"),
	"cloud.aws.cli":   write("cloud"),
	"cloud.azure.cli": write("cloud"),
	"cloud.gcp.cli":   write("cloud"),

	"dbms.query":     write("database"),
	"dbms.redis.cli": write("database"),

	"integrations.http":    write("integrations"),
	"integrations.ssh":     write("integrations"),
	"mq.rabbitmqadmin.cli": write("integrations"),
	"scm.github.cli":       write("integrations"),
	"scm.gitlab.cli":       write("integrations"),
	"llm.mcp_call":         write("integrations"),
	"llm.a2a_call":         write("integrations"),

	// Runs arbitrary code with the account's credentials: only someone who could
	// have written the script may run it.
	"scripting.run_script": write("workflows"),

	"llm.classify":          execute("llm"),
	"llm.event_investigate": execute("llm"),
	"llm.investigate":       execute("llm"),
	"llm.nubi":              execute("llm"),
	"llm.router":            execute("llm"),
	"llm.summary":           execute("llm"),

	"events.add_evidence": write("events"),
	"events.store":        write("events"),

	"observability.log_groups": read("logs"),
	"observability.logs":       read("logs"),
	"observability.metrics":    read("metrics"),
	"observability.traces":     read("traces"),

	"notifications.add_channel_members": write("notifications"),
	"notifications.add_reaction":        write("notifications"),
	"notifications.create_channel":      write("notifications"),
	"notifications.dm":                  write("notifications"),
	"notifications.email":               write("notifications"),
	"notifications.im":                  write("notifications"),
	"notifications.read_thread":         read("notifications"),
	"slack.join_channel":                write("notifications"),
	"google_chat.join_space":            write("notifications"),

	"tickets.acknowledge":  write("tickets"),
	"tickets.add_comment":  write("tickets"),
	"tickets.assign":       write("tickets"),
	"tickets.create":       write("tickets"),
	"tickets.escalate":     write("tickets"),
	"tickets.get":          read("tickets"),
	"tickets.get_comments": read("tickets"),
	"tickets.resolve":      write("tickets"),
	"tickets.transition":   write("tickets"),
	"tickets.update":       write("tickets"),

	"vertical_rightsize_generate": write("recommendations"),
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
}

// StepAuthorizer checks a step against its run's principal. The loaders are
// fields so tests can stub them.
type StepAuthorizer struct {
	// LoadContext returns the principal's permissions (roles + custom grants).
	LoadContext func(ctx context.Context, tenantID, userID string) (*security.SecurityContext, error)
	// UserStatus reports whether the user is active and still a member of the
	// tenant. Read directly rather than from the cached security context, so a
	// suspension takes effect on the next step.
	UserStatus func(ctx context.Context, tenantID, userID string) (active bool, err error)
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

func activeTenantMember(ctx context.Context, tenantID, userID string) (bool, error) {
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return false, err
	}
	var active bool
	err = dbManager.Db.QueryRowContext(ctx, `
		SELECT u.status = 'active'
		   AND EXISTS (SELECT 1 FROM tenant_users tu WHERE tu."user" = u.id AND tu.tenant = $2)
		FROM users u WHERE u.id = $1`, userID, tenantID).Scan(&active)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return active, err
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

	active, err := a.UserStatus(ctx, in.TenantID, in.Principal)
	if err != nil {
		return fmt.Errorf("authorization unavailable: could not read user %s: %w", in.Principal, err)
	}
	if !active {
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

	if !holds(sc, in.StepAccountID, req) {
		return denied("permission denied: user %s needs %s:%s on account %s for step type %s",
			in.Principal, req.Module, req.Class, in.StepAccountID, in.TaskName)
	}
	return nil
}

// holds applies the same rule the platform's own routes use: a built-in
// account role, or a custom grant for the module on that account. Write grants
// imply Read and Execute.
func holds(sc *security.SecurityContext, accountID string, req StepRequirement) bool {
	switch req.Class {
	case classRead:
		return sc.CanReadAccountData(accountID, req.Module)
	case classExecute:
		return sc.HasAccountAccess(accountID, security.SecurityAccessTypeCreate) ||
			sc.HasScopedPermission(accountID, req.Module, classExecute) ||
			sc.HasScopedPermission(accountID, req.Module, classWrite)
	default:
		return sc.HasAccountAccess(accountID, security.SecurityAccessTypeUpdate) ||
			sc.HasScopedPermission(accountID, req.Module, classWrite)
	}
}
