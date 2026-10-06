package azure

import (
	"context"
	"fmt"
	"nudgebee/collector/cloud/providers"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeAz puts a stand-in `az` script first on PATH. Instead of talking to Azure
// it keeps its state in AZURE_CONFIG_DIR like the real CLI: `az login` writes a
// token file in a subdirectory (and then fails while the fail_login marker
// exists), `az config set` writes a leak file, and any other command fails when
// the token file is missing, or with the fail_cmd marker's content as its error
// while that marker exists. Every call is recorded as
// "<verb> <dir> <leak file present>".
type fakeAz struct {
	t   *testing.T
	dir string
}

type fakeAzCall struct {
	dir  string
	leak bool
}

func newFakeAz(t *testing.T, tenant string) *fakeAz {
	t.Helper()
	dir := t.TempDir()
	script := fmt.Sprintf(`#!/bin/sh
case "$1 $2" in
  "login "*) verb=login; sleep 0.2; mkdir -p "$AZURE_CONFIG_DIR/cache"; echo token > "$AZURE_CONFIG_DIR/cache/token" ;;
  "account set") verb=account-set ;;
  *) verb=cmd ;;
esac
leak=no
[ -f "$AZURE_CONFIG_DIR/leak" ] && leak=yes
echo "$verb $AZURE_CONFIG_DIR $leak" >> %[1]s/calls.log
if [ "$verb" = login ] && [ -f %[1]s/fail_login ]; then
  echo "ERROR: login failed" >&2
  exit 1
fi
if [ "$verb" = cmd ]; then
  if [ ! -f "$AZURE_CONFIG_DIR/cache/token" ]; then
    echo "ERROR: Please run 'az login' to setup account." >&2
    exit 1
  fi
  if [ -f %[1]s/fail_cmd ]; then
    cat %[1]s/fail_cmd >&2
    exit 1
  fi
  [ "$1 $2" = "config set" ] && echo leaked > "$AZURE_CONFIG_DIR/leak"
fi
echo ok
`, dir)
	if err := os.WriteFile(filepath.Join(dir, "az"), []byte(script), 0o755); err != nil {
		t.Fatalf("failed to write fake az: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	// A tenant unique to the test keeps its session apart from other tests.
	t.Setenv("AZURE_TENANT_ID", tenant)
	t.Setenv("AZURE_CLIENT_ID", "client")
	t.Setenv("AZURE_CLIENT_SECRET", "secret-1")
	t.Setenv("AZURE_SUBSCRIPTION_ID", "sub")
	t.Cleanup(func() {
		azureCliSessions.Range(func(key, _ any) bool {
			if strings.HasPrefix(key.(string), tenant+"/") {
				azureCliSessions.Delete(key)
			}
			return true
		})
	})
	return &fakeAz{t: t, dir: dir}
}

// calls returns the recorded calls for a verb, in order.
func (f *fakeAz) calls(verb string) []fakeAzCall {
	f.t.Helper()
	data, err := os.ReadFile(filepath.Join(f.dir, "calls.log"))
	if err != nil {
		f.t.Fatalf("failed to read fake az log: %v", err)
	}
	var calls []fakeAzCall
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if fields := strings.Fields(line); len(fields) == 3 && fields[0] == verb {
			calls = append(calls, fakeAzCall{dir: fields[1], leak: fields[2] == "yes"})
		}
	}
	return calls
}

func (f *fakeAz) wantCalls(verb string, want int) {
	f.t.Helper()
	if got := len(f.calls(verb)); got != want {
		f.t.Errorf("%s calls = %d, want %d", verb, got, want)
	}
}

func TestExecuteCliCommand_ReusesLogin(t *testing.T) {
	az := newFakeAz(t, "tenant-reuse")
	ctx := providers.NewCloudProviderContext(context.Background())
	p := &azureProvider{}

	for i := 0; i < 3; i++ {
		out, err := p.ExecuteCliCommand(ctx, providers.Account{}, "az group list")
		if err != nil {
			t.Fatalf("call %d: unexpected error: %v", i, err)
		}
		if strings.TrimSpace(out) != "ok" {
			t.Errorf("call %d: output = %q, want %q", i, out, "ok")
		}
	}

	az.wantCalls("login", 1)
	az.wantCalls("account-set", 1)
	az.wantCalls("cmd", 3)

	// The login dir is gone once the login is in memory, and each command runs in
	// its own dir, removed afterwards.
	loginDir := az.calls("login")[0].dir
	if _, err := os.Stat(loginDir); !os.IsNotExist(err) {
		t.Errorf("the login dir %q was left behind", loginDir)
	}
	seen := map[string]bool{loginDir: true}
	for i, call := range az.calls("cmd") {
		if seen[call.dir] {
			t.Errorf("command %d reused dir %q", i, call.dir)
		}
		seen[call.dir] = true
		if _, err := os.Stat(call.dir); !os.IsNotExist(err) {
			t.Errorf("command %d left its dir %q behind", i, call.dir)
		}
	}
}

func TestExecuteCliCommand_ConcurrentCallsLoginOnce(t *testing.T) {
	az := newFakeAz(t, "tenant-concurrent")
	ctx := providers.NewCloudProviderContext(context.Background())
	p := &azureProvider{}

	const n = 5
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = p.ExecuteCliCommand(ctx, providers.Account{}, "az group list")
		}()
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("call %d: unexpected error: %v", i, err)
		}
	}
	az.wantCalls("login", 1)
	az.wantCalls("account-set", 1)
	az.wantCalls("cmd", n)
}

func TestExecuteCliCommand_CommandStateDoesNotLeak(t *testing.T) {
	az := newFakeAz(t, "tenant-leak")
	ctx := providers.NewCloudProviderContext(context.Background())
	p := &azureProvider{}

	for _, command := range []string{"az config set defaults.location=westus", "az group list"} {
		if _, err := p.ExecuteCliCommand(ctx, providers.Account{}, command); err != nil {
			t.Fatalf("%q: unexpected error: %v", command, err)
		}
	}

	az.wantCalls("login", 1)
	if calls := az.calls("cmd"); len(calls) == 2 && calls[1].leak {
		t.Error("state written by one command was visible to the next")
	}
}

func TestExecuteCliCommand_OneLoginPerSecret(t *testing.T) {
	az := newFakeAz(t, "tenant-secrets")
	ctx := providers.NewCloudProviderContext(context.Background())
	p := &azureProvider{}

	// Two accounts on the same app and subscription, each with its own secret.
	for i := 0; i < 6; i++ {
		t.Setenv("AZURE_CLIENT_SECRET", fmt.Sprintf("secret-%d", i%2))
		if _, err := p.ExecuteCliCommand(ctx, providers.Account{}, "az group list"); err != nil {
			t.Fatalf("call %d: unexpected error: %v", i, err)
		}
	}
	az.wantCalls("login", 2)
}

func TestExecuteCliCommand_ReloginAfterBadCredentialError(t *testing.T) {
	az := newFakeAz(t, "tenant-auth-error")
	ctx := providers.NewCloudProviderContext(context.Background())
	p := &azureProvider{}

	if _, err := p.ExecuteCliCommand(ctx, providers.Account{}, "az group list"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	marker := filepath.Join(az.dir, "fail_cmd")
	if err := os.WriteFile(marker, []byte("ERROR: AADSTS7000222: The provided client secret keys for app 'client' are expired.\n"), 0o644); err != nil {
		t.Fatalf("failed to write marker: %v", err)
	}
	if _, err := p.ExecuteCliCommand(ctx, providers.Account{}, "az group list"); err == nil {
		t.Fatal("expected the auth error to be returned")
	}
	az.wantCalls("login", 1)

	if err := os.Remove(marker); err != nil {
		t.Fatalf("failed to remove marker: %v", err)
	}
	if _, err := p.ExecuteCliCommand(ctx, providers.Account{}, "az group list"); err != nil {
		t.Fatalf("unexpected error after re-login: %v", err)
	}
	az.wantCalls("login", 2)
}

func TestExecuteCliCommand_UnrelatedAADSTSErrorKeepsLogin(t *testing.T) {
	az := newFakeAz(t, "tenant-unrelated-error")
	ctx := providers.NewCloudProviderContext(context.Background())
	p := &azureProvider{}

	marker := filepath.Join(az.dir, "fail_cmd")
	if err := os.WriteFile(marker, []byte("ERROR: AADSTS500011: The resource principal named https://example.invalid was not found in the tenant.\n"), 0o644); err != nil {
		t.Fatalf("failed to write marker: %v", err)
	}
	for i := 0; i < 2; i++ {
		if _, err := p.ExecuteCliCommand(ctx, providers.Account{}, "az rest --url https://example.invalid"); err == nil {
			t.Fatalf("call %d: expected the command error to be returned", i)
		}
	}
	if err := os.Remove(marker); err != nil {
		t.Fatalf("failed to remove marker: %v", err)
	}
	if _, err := p.ExecuteCliCommand(ctx, providers.Account{}, "az group list"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	az.wantCalls("login", 1)
}

func TestExecuteCliCommand_FailedLoginLeavesNoDir(t *testing.T) {
	az := newFakeAz(t, "tenant-login-fails")
	ctx := providers.NewCloudProviderContext(context.Background())
	p := &azureProvider{}

	retryAfter := azureCliLoginRetryAfter
	azureCliLoginRetryAfter = 0
	t.Cleanup(func() { azureCliLoginRetryAfter = retryAfter })

	marker := filepath.Join(az.dir, "fail_login")
	if err := os.WriteFile(marker, nil, 0o644); err != nil {
		t.Fatalf("failed to write marker: %v", err)
	}
	if _, err := p.ExecuteCliCommand(ctx, providers.Account{}, "az group list"); err == nil {
		t.Fatal("expected the login error to be returned")
	}
	az.wantCalls("cmd", 0)
	if _, err := os.Stat(az.calls("login")[0].dir); !os.IsNotExist(err) {
		t.Error("the dir of a failed login was left behind")
	}

	if err := os.Remove(marker); err != nil {
		t.Fatalf("failed to remove marker: %v", err)
	}
	if _, err := p.ExecuteCliCommand(ctx, providers.Account{}, "az group list"); err != nil {
		t.Fatalf("unexpected error after the login recovered: %v", err)
	}
	az.wantCalls("login", 2)
	az.wantCalls("cmd", 1)
}

func TestExecuteCliCommand_FailedLoginIsRemembered(t *testing.T) {
	az := newFakeAz(t, "tenant-login-remembered")
	ctx := providers.NewCloudProviderContext(context.Background())
	p := &azureProvider{}

	marker := filepath.Join(az.dir, "fail_login")
	if err := os.WriteFile(marker, nil, 0o644); err != nil {
		t.Fatalf("failed to write marker: %v", err)
	}

	// Callers queued behind a failing login get its error, not a login each.
	const n = 5
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = p.ExecuteCliCommand(ctx, providers.Account{}, "az group list")
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err == nil {
			t.Errorf("call %d: expected the login error to be returned", i)
		}
	}
	az.wantCalls("login", 1)

	// Once the failure is no longer remembered, the login is tried again.
	if err := os.Remove(marker); err != nil {
		t.Fatalf("failed to remove marker: %v", err)
	}
	retryAfter := azureCliLoginRetryAfter
	azureCliLoginRetryAfter = 0
	t.Cleanup(func() { azureCliLoginRetryAfter = retryAfter })
	if _, err := p.ExecuteCliCommand(ctx, providers.Account{}, "az group list"); err != nil {
		t.Fatalf("unexpected error after the login recovered: %v", err)
	}
	az.wantCalls("login", 2)
}

func TestExecuteCliCommand_ReloginAfterTTL(t *testing.T) {
	az := newFakeAz(t, "tenant-ttl")
	ctx := providers.NewCloudProviderContext(context.Background())
	p := &azureProvider{}

	ttl := azureCliLoginTTL
	azureCliLoginTTL = time.Nanosecond
	t.Cleanup(func() { azureCliLoginTTL = ttl })

	for i := 0; i < 2; i++ {
		if _, err := p.ExecuteCliCommand(ctx, providers.Account{}, "az group list"); err != nil {
			t.Fatalf("call %d: unexpected error: %v", i, err)
		}
	}
	az.wantCalls("login", 2)
}
