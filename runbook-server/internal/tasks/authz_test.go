package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"testing"
	"time"

	"nudgebee/runbook/services/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
)

func TestEveryRegisteredTaskDeclaresAStepRequirement(t *testing.T) {
	// Unlisted task types are refused at run time. This keeps a new task from
	// shipping in that state — declare what a principal needs to run it.
	registered := map[string]bool{}
	var missing []string
	for _, task := range NewInitializedTaskRegistry().ListTasks() {
		registered[task.GetName()] = true
		if _, ok := RequirementFor(task.GetName()); !ok {
			missing = append(missing, task.GetName())
		}
	}
	sort.Strings(missing)
	assert.Empty(t, missing, "add these task types to stepRequirements in authz.go")

	var stale []string
	for name := range stepRequirements {
		if !registered[name] {
			stale = append(stale, name)
		}
	}
	sort.Strings(stale)
	assert.Empty(t, stale, "stepRequirements lists task types that are no longer registered")
}

func TestEveryStepDeclaresATier(t *testing.T) {
	// TierFor answers the highest tier for a type without one, so a step type
	// can never be treated as safe by omission; this keeps omission from
	// happening at all.
	for name, req := range stepRequirements {
		assert.GreaterOrEqualf(t, req.Tier, TierRead, "%s has no tier", name)
		assert.LessOrEqualf(t, req.Tier, TierUnrestricted, "%s has an unknown tier", name)
	}
}

func TestTierFor(t *testing.T) {
	for name, want := range map[string]int{
		"data.transform":       TierRead,
		"tickets.create":       TierRead,
		"observability.logs":   TierRead,
		"k8s.workload_restart": TierChange,
		"tickets.transition":   TierChange,
		"tickets.update":       TierChange,
		"notifications.email":  TierChange,
		"notifications.im":     TierChange,
		"cloud.k8s.cli":        TierUnrestricted,
		"cicd.argocd.cli":      TierUnrestricted,
		"scm.github.cli":       TierUnrestricted,
		"dbms.query":           TierUnrestricted,
		"scripting.run_script": TierUnrestricted,
		"integrations.http":    TierUnrestricted,
		"llm.mcp_call":         TierUnrestricted,
		"core.call-workflow":   TierUnrestricted,
	} {
		got, ok := TierFor(name)
		assert.True(t, ok, name)
		assert.Equal(t, want, got, name)
	}
	got, ok := TierFor("not.a.task")
	assert.False(t, ok)
	assert.Equal(t, TierUnrestricted, got, "an unknown step type is never treated as safe")
}

func TestTiersAgreeWithPermissions(t *testing.T) {
	// A step that needs no permission changes nothing outside the run; a write
	// to a cluster, cloud account or database changes a live system.
	for name, req := range stepRequirements {
		if req.None() {
			assert.Equalf(t, TierRead, req.Tier, "%s needs no permission but is not a read", name)
		}
		if req.Class == classWrite && (req.Module == "k8s" || req.Module == "cloud" || req.Module == "database") {
			assert.GreaterOrEqualf(t, req.Tier, TierChange, "%s writes to a live system", name)
		}
	}
}

func TestDeclaredModulesExistInThePermissionVocabulary(t *testing.T) {
	// Custom roles grant "<module>:<class>" for the modules the platform seeds.
	// A module name here that is not in that set could never be granted, so the
	// step would be admin-only without anyone deciding that.
	seeds, err := filepath.Glob("../../../api-server/migrations/migrations/app/*_V833_*.up.sql")
	require.NoError(t, err)
	require.Len(t, seeds, 1, "V833 seeds the permission modules")
	body, err := os.ReadFile(seeds[0])
	require.NoError(t, err)

	known := map[string]bool{}
	for _, m := range regexp.MustCompile(`\('tenant_admin', '([a-z_0-9]+)'`).FindAllStringSubmatch(string(body), -1) {
		known[m[1]] = true
	}
	require.NotEmpty(t, known)

	for name, req := range stepRequirements {
		if req.None() {
			continue
		}
		assert.Truef(t, known[req.Module], "%s declares unknown module %q", name, req.Module)
		assert.Containsf(t, []string{classRead, classWrite, classExecute}, req.Class, "%s declares unknown class", name)
	}
}

func TestStepAccountPrefersTheStepsOwnAccount(t *testing.T) {
	assert.Equal(t, "acct-b", StepAccount(map[string]any{"account_id": "acct-b"}, "acct-a"))
	assert.Equal(t, "acct-a", StepAccount(map[string]any{"account_id": ""}, "acct-a"))
	assert.Equal(t, "acct-a", StepAccount(map[string]any{}, "acct-a"))
}

// principalContext builds a security context for a principal in tenant "t"
// whose tenant holds accounts acct-a and acct-b.
func principalContext(t *testing.T, roles []string, scoped map[string][]string, grants map[string]map[string]bool) *security.SecurityContext {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"TenantId":                "t",
		"UserId":                  "u",
		"AccountIds":              []string{"acct-a", "acct-b"},
		"Roles":                   roles,
		"ScopedEntityIds":         scoped,
		"ScopedCustomPermissions": grants,
	})
	require.NoError(t, err)
	var sc security.SecurityContext
	require.NoError(t, json.Unmarshal(raw, &sc))
	return &sc
}

func stubAuthorizer(sc *security.SecurityContext, active bool) StepAuthorizer {
	return StepAuthorizer{
		LoadContext: func(context.Context, string, string) (*security.SecurityContext, error) { return sc, nil },
		UserStatus:  func(context.Context, string, string) (UserState, error) { return UserState{Active: active}, nil },
	}
}

func stepInput(task, stepAccount string) StepAuthInput {
	return StepAuthInput{
		TaskName: task, TenantID: "t", Principal: "u",
		RunAccountID: "acct-a", StepAccountID: stepAccount, CheckRunRight: true,
	}
}

func assertDenied(t *testing.T, err error, contains string) {
	t.Helper()
	require.Error(t, err)
	var appErr *temporal.ApplicationError
	require.True(t, errors.As(err, &appErr), "a refusal must be a Temporal application error")
	assert.Equal(t, ErrTypeStepPermissionDenied, appErr.Type())
	assert.True(t, appErr.NonRetryable(), "retrying cannot grant a permission")
	assert.Contains(t, err.Error(), contains)
}

func TestAuthorizeAccountAdminMayRunWriteSteps(t *testing.T) {
	sc := principalContext(t, []string{security.AUTH_ACCOUNT_ADMIN_ROLE}, map[string][]string{security.AUTH_ACCOUNT_ADMIN_ROLE: {"acct-a"}}, nil)
	assert.NoError(t, stubAuthorizer(sc, true).Authorize(context.Background(), stepInput("k8s.pod_delete", "acct-a")))
}

func TestAuthorizeExecuteOnlyCustomRoleIsRefusedAWriteStep(t *testing.T) {
	// The acceptance case: allowed to run automations, not to change the cluster.
	grants := map[string]map[string]bool{"acct-a": {"workflows:Execute": true}}
	sc := principalContext(t, nil, nil, grants)
	authz := stubAuthorizer(sc, true)

	assertDenied(t, authz.Authorize(context.Background(), stepInput("k8s.pod_delete", "acct-a")), "needs k8s:Write on account acct-a")
	// Steps with no outside effect still run.
	assert.NoError(t, authz.Authorize(context.Background(), stepInput("data.transform", "acct-a")))
	// So do steps that need only the right to run automations.
	assert.NoError(t, authz.Authorize(context.Background(), stepInput("network.ping", "acct-a")))
}

func TestAuthorizeCustomGrantForTheModuleAllowsTheStep(t *testing.T) {
	grants := map[string]map[string]bool{"acct-a": {"workflows:Execute": true, "k8s:Write": true}}
	sc := principalContext(t, nil, nil, grants)
	assert.NoError(t, stubAuthorizer(sc, true).Authorize(context.Background(), stepInput("k8s.pod_delete", "acct-a")))
}

func TestAuthorizeReadOnlyAdminMayReadButNotWrite(t *testing.T) {
	sc := principalContext(t, []string{security.AUTH_ACCOUNT_READ_ADMIN_ROLE, security.AUTH_ACCOUNT_ADMIN_ROLE},
		map[string][]string{security.AUTH_ACCOUNT_ADMIN_ROLE: {"acct-a"}, security.AUTH_ACCOUNT_READ_ADMIN_ROLE: {"acct-b"}}, nil)
	authz := stubAuthorizer(sc, true)
	in := stepInput("tickets.get", "acct-b")
	assert.NoError(t, authz.Authorize(context.Background(), in))
	in.TaskName = "tickets.create"
	assertDenied(t, authz.Authorize(context.Background(), in), "needs tickets:Write on account acct-b")
}

func TestAuthorizeChecksTheStepsAccountNotTheRunsAccount(t *testing.T) {
	// A step's account_id may point at another account; the principal must hold
	// the permission there.
	sc := principalContext(t, []string{security.AUTH_ACCOUNT_ADMIN_ROLE}, map[string][]string{security.AUTH_ACCOUNT_ADMIN_ROLE: {"acct-a"}}, nil)
	assertDenied(t, stubAuthorizer(sc, true).Authorize(context.Background(), stepInput("k8s.pod_delete", "acct-b")), "on account acct-b")
}

func TestAuthorizeRefusesAnOwnerWhoMayNotRunAutomations(t *testing.T) {
	// A scheduled run's owner whose role was reduced below "run automations"
	// on the workflow's account.
	grants := map[string]map[string]bool{"acct-a": {"k8s:Write": true}}
	sc := principalContext(t, nil, nil, grants)
	assertDenied(t, stubAuthorizer(sc, true).Authorize(context.Background(), stepInput("k8s.pod_delete", "acct-a")), "may not run automations")
}

func TestAuthorizeRefusesAnInactiveOrMissingPrincipal(t *testing.T) {
	sc := principalContext(t, []string{security.AUTH_TENANT_ADMIN_ROLE}, nil, nil)
	assertDenied(t, stubAuthorizer(sc, false).Authorize(context.Background(), stepInput("k8s.pod_delete", "acct-a")), "not active")

	in := stepInput("k8s.pod_delete", "acct-a")
	in.Principal = ""
	assertDenied(t, stubAuthorizer(sc, true).Authorize(context.Background(), in), "no user to act as")
	in.Principal = security.GetSystemUserId()
	assertDenied(t, stubAuthorizer(sc, true).Authorize(context.Background(), in), "no user to act as")
}

func TestAuthorizeRefusesUndeclaredTaskTypes(t *testing.T) {
	sc := principalContext(t, []string{security.AUTH_TENANT_ADMIN_ROLE}, nil, nil)
	assertDenied(t, stubAuthorizer(sc, true).Authorize(context.Background(), stepInput("made.up", "acct-a")), "declares no permission requirement")
}

func TestAuthorizeLoadFailureIsRetryableNotADenial(t *testing.T) {
	authz := StepAuthorizer{
		LoadContext: func(context.Context, string, string) (*security.SecurityContext, error) {
			return nil, errors.New("services server down")
		},
		UserStatus: func(context.Context, string, string) (UserState, error) { return UserState{Active: true}, nil },
	}
	err := authz.Authorize(context.Background(), stepInput("k8s.pod_delete", "acct-a"))
	require.Error(t, err)
	var appErr *temporal.ApplicationError
	assert.False(t, errors.As(err, &appErr), "an outage must stay retryable")
	assert.Contains(t, err.Error(), "authorization unavailable")
}

func TestContextCacheFetchesOncePerPrincipalWithinTheTTL(t *testing.T) {
	calls := 0
	cache := newContextCache(time.Minute, func(_, userID string) (*security.SecurityContext, error) {
		calls++
		return principalContext(t, nil, nil, nil), nil
	})
	for i := 0; i < 500; i++ { // a foreach over 500 items
		_, err := cache.load(context.Background(), "t", "u")
		require.NoError(t, err)
	}
	assert.Equal(t, 1, calls)
	_, _ = cache.load(context.Background(), "t", "other")
	assert.Equal(t, 2, calls, "each principal is cached separately")
}

func TestContextCacheDoesNotCacheFailures(t *testing.T) {
	calls := 0
	cache := newContextCache(time.Minute, func(string, string) (*security.SecurityContext, error) {
		calls++
		return nil, errors.New("down")
	})
	_, err := cache.load(context.Background(), "t", "u")
	require.Error(t, err)
	_, _ = cache.load(context.Background(), "t", "u")
	assert.Equal(t, 2, calls)
}
