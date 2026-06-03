//go:build e2e

package agents

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"nudgebee/llm/agents/core"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// ============================================================
// Benchmark fixture loader
// ============================================================
//
// Reads `test_case.yaml` files from `llm/benchmark/llm/agents/*/fixtures/*/`
// and converts them into k8sTestCase values so the Go integration tests can
// share the single source of truth maintained by the Python benchmark harness.
//
// Reuses:
//   - fixture YAML schema (agent, user_prompt, expected_output, before_test,
//     after_test, wait_time_seconds, tags, skip)
//   - the self-bootstrapping before_test/after_test pattern: each fixture
//     creates and tears down its own k8s namespace + manifests, so no
//     shared demo cluster is required (only a reachable kubectl).
//
// Does NOT reuse:
//   - RAGAS scoring (that path stays in the Python nightly runner)
//   - pytest parametrization / dashboard
//
// History: an earlier version of this loader supported a `feature_flag` /
// `flag_variant` pair that toggled OpenTelemetry-demo flagd variants. The
// Python benchmark refactor (early 2026) replaced that mechanism with
// self-contained shell hooks in every fixture, so this loader no longer
// reads those fields. The flagd helpers in `flagd_controller_test.go` are
// kept as a standalone utility in case a future fixture needs them.

// fixtureYAML mirrors the subset of test_case.yaml fields the Go runner needs.
// Fields we don't consume yet (scenario_id, type, include_files, ...) are
// deliberately omitted — yaml.v3 will ignore unknown keys.
type fixtureYAML struct {
	Agent             string            `yaml:"agent"`
	UserPrompt        string            `yaml:"user_prompt"`
	ExpectedOutput    StringOrList      `yaml:"expected_output"`
	ExpectedRootCause expectedRootCause `yaml:"expected_root_cause"`
	Tags              []string          `yaml:"tags"`
	BeforeTest        string            `yaml:"before_test"`
	AfterTest         string            `yaml:"after_test"`
	SetupTimeout      int               `yaml:"setup_timeout"`     // seconds, default 300
	TeardownTimeout   int               `yaml:"teardown_timeout"`  // seconds, default 120
	WaitSeconds       int               `yaml:"wait_time_seconds"` // seconds to wait after setup
	PortForwards      []PortForward     `yaml:"port_forwards"`
	Skip              bool              `yaml:"skip"`
	SkipReason        string            `yaml:"skip_reason"`
}

// StringOrList is a YAML field that accepts either a single scalar string
// or a list of strings, normalizing both to []string. Fixtures mix the
// two forms for `expected_output`: some use a list of independent claims,
// some use a single multi-line block. Callers that want LLM-judged
// semantic checks pass this directly into k8sTestCase.WantLLMClaims.
type StringOrList []string

// UnmarshalYAML accepts both `expected_output: "..."` (scalar) and
// `expected_output: [...]` (list). Any other shape is an error.
func (s *StringOrList) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		var single string
		if err := value.Decode(&single); err != nil {
			return fmt.Errorf("expected_output scalar: %w", err)
		}
		single = strings.TrimSpace(single)
		if single == "" {
			*s = nil
			return nil
		}
		*s = []string{single}
		return nil
	case yaml.SequenceNode:
		var list []string
		if err := value.Decode(&list); err != nil {
			return fmt.Errorf("expected_output list: %w", err)
		}
		*s = list
		return nil
	default:
		return fmt.Errorf("expected_output: expected scalar or sequence, got node kind %d", value.Kind)
	}
}

// PortForward declares a kubectl port-forward the fixture needs while the
// test runs. Tools that talk to fixture-deployed datasources (per-namespace
// Tempo, Loki, Prometheus, ...) connect via the forwarded local port.
//
// Only `svc/<name>` targets are observed in the benchmark corpus (all 12
// uses match this shape); pod/deployment targets are not supported yet —
// add a `target_type` field when a real fixture needs them.
type PortForward struct {
	Namespace  string `yaml:"namespace"`
	Service    string `yaml:"service"`
	LocalPort  int    `yaml:"local_port"`
	RemotePort int    `yaml:"remote_port"`
}

// expectedRootCause is the structured ground truth for an RCA fixture. The
// Python harness scores answers semantically via RAGAS; the Go fast-path
// tier uses this for narrow keyword presence checks (affected_service is
// usually a single high-signal token like "productcatalogservice").
type expectedRootCause struct {
	AffectedService string   `yaml:"affected_service"`
	IssueType       string   `yaml:"issue_type"`
	Symptoms        []string `yaml:"symptoms"`
}

// Fixture wraps a loaded benchmark test case with its lifecycle helpers.
type Fixture struct {
	// Path is the absolute or repo-relative path to test_case.yaml.
	Path string
	// ID is the parent directory name (e.g. "001_users_are_reporting...").
	ID string
	// Dir is the absolute path to the fixture directory (cwd for hooks).
	Dir string
	// YAML is the parsed document.
	YAML fixtureYAML
	// TestCase is the fixture converted into the integration-test schema.
	// Callers can further customise it (WantAnyToolMatching, WantMinToolCalls,
	// etc.) before passing to runTest.
	TestCase k8sTestCase
}

const (
	defaultSetupTimeout    = 300 * time.Second
	defaultTeardownTimeout = 120 * time.Second
)

// LoadFixture reads test_case.yaml at the given path and returns a Fixture.
// Honours the `skip:` field by calling t.Skip() with the reason.
func LoadFixture(t *testing.T, path string) *Fixture {
	t.Helper()

	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatalf("LoadFixture: resolve path %q: %v", path, err)
	}
	raw, err := os.ReadFile(abs)
	if err != nil {
		t.Fatalf("LoadFixture: read %q: %v", abs, err)
	}

	var y fixtureYAML
	if err := yaml.Unmarshal(raw, &y); err != nil {
		t.Fatalf("LoadFixture: parse %q: %v", abs, err)
	}
	if y.UserPrompt == "" {
		t.Fatalf("LoadFixture: %q: user_prompt is required", abs)
	}

	if y.Skip {
		reason := y.SkipReason
		if reason == "" {
			reason = "fixture marked skip"
		}
		t.Skipf("benchmark fixture skipped: %s", reason)
	}

	id := filepath.Base(filepath.Dir(abs))

	// Derive a stable session ID per fixture so the LLM cache / dedup works
	// across repeated runs. Truncate to keep the value short.
	sessionID := fmt.Sprintf("ut-bench-%s", clipString(sanitizeID(id), 48))

	tc := k8sTestCase{
		Name:      id,
		SessionId: sessionID,
		Query:     y.UserPrompt,
		AccountId: os.Getenv("TEST_ACCOUNT"),
		UserId:    os.Getenv("TEST_USER"),
	}

	// Default content check: a single high-signal token (the affected
	// service name) when the fixture provides expected_root_cause. This
	// matches reliably on free-form LLM answers — unlike `expected_output`,
	// which is a full ground-truth sentence intended for RAGAS-style
	// semantic scoring in the Python harness, not verbatim substring checks.
	// Tests can override or extend tc.WantContainsAny after LoadFixture.
	if svc := strings.TrimSpace(y.ExpectedRootCause.AffectedService); svc != "" {
		tc.WantContainsAny = []string{svc}
	}

	return &Fixture{
		Path:     abs,
		ID:       id,
		Dir:      filepath.Dir(abs),
		YAML:     y,
		TestCase: tc,
	}
}

// Setup runs before_test, waits for the requested telemetry-settling
// period, and starts any declared port-forwards. Registers teardown via
// t.Cleanup BEFORE running before_test so a Fatalf mid-setup still triggers
// cleanup of whatever state was created.
//
// Cleanup ordering (LIFO across t.Cleanup registrations):
//  1. teardown (after_test) — registered first, runs LAST
//  2. each port-forward kill — registered later, runs FIRST
//
// This is intentional: after_test typically deletes the namespace the
// port-forward is targeting; killing the port-forward first prevents a
// spam of "connection refused" / "pod no longer exists" log lines.
func (f *Fixture) Setup(t *testing.T) {
	t.Helper()

	// Register teardown FIRST so partial before_test still gets cleaned up.
	t.Cleanup(func() { f.teardown(t) })

	if f.YAML.BeforeTest != "" {
		timeout := time.Duration(f.YAML.SetupTimeout) * time.Second
		if timeout <= 0 {
			timeout = defaultSetupTimeout
		}
		if err := runShellHook(t, "before_test", f.YAML.BeforeTest, f.Dir, timeout); err != nil {
			t.Fatalf("[%s] before_test failed: %v", f.ID, err)
		}
	}

	if f.YAML.WaitSeconds > 0 {
		t.Logf("[%s] waiting %ds for telemetry to accumulate", f.ID, f.YAML.WaitSeconds)
		time.Sleep(time.Duration(f.YAML.WaitSeconds) * time.Second)
	}

	// Port-forwards started AFTER before_test + settle: the target services
	// usually don't exist until before_test has applied its manifests.
	for _, pf := range f.YAML.PortForwards {
		startPortForward(t, f.ID, pf)
	}
}

// teardown runs after_test. Failures are logged but do not fail the test —
// we don't want cleanup errors to mask real test failures.
func (f *Fixture) teardown(t *testing.T) {
	t.Helper()

	if f.YAML.AfterTest != "" {
		timeout := time.Duration(f.YAML.TeardownTimeout) * time.Second
		if timeout <= 0 {
			timeout = defaultTeardownTimeout
		}
		if err := runShellHook(t, "after_test", f.YAML.AfterTest, f.Dir, timeout); err != nil {
			t.Logf("[%s] warning: after_test failed: %v", f.ID, err)
		}
	}
}

// Agent resolves the NBAgent referenced by the fixture (or the given default
// if the fixture doesn't specify one).
func (f *Fixture) Agent(t *testing.T, fallback core.NBAgent) core.NBAgent {
	t.Helper()
	name := f.YAML.Agent
	if name == "" {
		return fallback
	}
	sc := newSC(f.TestCase)
	a, ok := core.GetNBAgent(sc, name, f.TestCase.AccountId, core.AgentStatusEnabled)
	if !ok {
		t.Fatalf("[%s] agent %q not registered / not enabled for account", f.ID, name)
	}
	return a
}

// runShellHook executes a multi-line shell script with a timeout and captures
// stdout/stderr into the test log.
func runShellHook(t *testing.T, label, script, cwd string, timeout time.Duration) error {
	t.Helper()
	t.Logf("[hook:%s] running (timeout=%s, cwd=%s)", label, timeout, cwd)

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", "-c", script)
	cmd.Dir = cwd
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	err := cmd.Run()
	elapsed := time.Since(start)

	if stdout.Len() > 0 {
		t.Logf("[hook:%s] stdout: %s", label, trimForLog(stdout.String()))
	}
	if stderr.Len() > 0 {
		t.Logf("[hook:%s] stderr: %s", label, trimForLog(stderr.String()))
	}

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("%s: timed out after %s", label, timeout)
		}
		return fmt.Errorf("%s: %w (elapsed=%s)", label, err, elapsed)
	}
	t.Logf("[hook:%s] ok in %s", label, elapsed)
	return nil
}

// startPortForward spawns `kubectl port-forward -n <ns> svc/<svc> L:R` as
// a long-lived background process, waits for the local port to start
// accepting TCP connections, and registers a t.Cleanup that kills the
// whole process group (kubectl + its children) on test end. Fatals on
// either spawn failure or readiness timeout.
//
// The process is put in its own group via Setpgid so the eventual SIGKILL
// signals every child kubectl spawned. Without that, kubectl-port-forward
// can outlive the test and hold the local port hostage for the next run.
//
// Caveat: parallel tests that declare overlapping LocalPort values will
// race — kubectl will fail to bind for the second one. We don't run
// fixture tests with t.Parallel() today; if that changes, the fixtures
// themselves need to pick non-overlapping local ports (the benchmark
// corpus already does this — every observed local_port is unique).
func startPortForward(t *testing.T, fixtureID string, pf PortForward) {
	t.Helper()

	target := "svc/" + pf.Service
	portSpec := fmt.Sprintf("%d:%d", pf.LocalPort, pf.RemotePort)
	args := []string{"port-forward", "-n", pf.Namespace, target, portSpec}
	label := fmt.Sprintf("%s/%s %d→%d", pf.Namespace, pf.Service, pf.LocalPort, pf.RemotePort)

	cmd := exec.Command("kubectl", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// Thread-safe stderr buffer: kubectl writes from its own goroutine while the
	// main goroutine reads via String()/Len() during the readiness wait and on
	// failure. A plain bytes.Buffer would race.
	var stderr safeBuffer
	cmd.Stdout = nil // discard; kubectl prints "Forwarding from ..." but we don't need it
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		t.Fatalf("[%s] port-forward %s: start kubectl: %v", fixtureID, label, err)
	}

	// Register cleanup BEFORE blocking on readiness so a flaky port-forward
	// that comes up briefly then dies still gets reaped.
	t.Cleanup(func() {
		if cmd.Process == nil {
			return
		}
		// Signal the whole process group so kubectl's children exit too.
		pgid := cmd.Process.Pid
		_ = syscall.Kill(-pgid, syscall.SIGTERM)

		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
			<-done
		}
		if stderr.Len() > 0 {
			t.Logf("[%s] port-forward %s stderr: %s", fixtureID, label, trimForLog(stderr.String()))
		}
	})

	// Wait for the local port to accept TCP connections. Bounded loop so a
	// permanently-stuck port-forward fails the test in seconds, not minutes.
	addr := fmt.Sprintf("127.0.0.1:%d", pf.LocalPort)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 250*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			t.Logf("[%s] port-forward ready: %s", fixtureID, label)
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("[%s] port-forward %s: never accepted connections on %s; kubectl stderr: %s",
		fixtureID, label, addr, trimForLog(stderr.String()))
}

// trimForLog clips long hook output so test logs remain readable.
func trimForLog(s string) string { return clipString(s, 2048) }

func clipString(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}

// sanitizeID replaces filesystem/DB-unfriendly chars in a fixture id so it
// can be embedded in a session id.
func sanitizeID(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z',
			c >= 'A' && c <= 'Z',
			c >= '0' && c <= '9',
			c == '-', c == '_':
			out = append(out, c)
		default:
			out = append(out, '-')
		}
	}
	return string(out)
}

// skipIfNoKubectl skips the test when no kubectl is on PATH or the configured
// kubeconfig cannot reach the cluster. nubi-style fixtures self-bootstrap
// their own namespaces via before_test, so the only thing we actually require
// from the host is a working kubectl + reachable API server. Uses a short
// timeout so a hung/unreachable cluster doesn't block the test suite.
func skipIfNoKubectl(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, "kubectl", "version", "--request-timeout=3s").Run(); err != nil {
		t.Skipf("skipping: kubectl cannot reach a cluster: %v", err)
	}
}

// skipIfNoFixtureEnv skips the test when the integration env vars that
// every fixture-driven test relies on are absent. Lives next to
// LoadFixture because it's a fixture-runner concern, not a K8s-specific
// one — AWS/GCP/Azure fixture tests need exactly the same env.
func skipIfNoFixtureEnv(t *testing.T) {
	t.Helper()
	if os.Getenv("TEST_ACCOUNT") == "" || os.Getenv("TEST_USER") == "" || os.Getenv("TEST_TENANT") == "" {
		t.Skip("skipping integration test: TEST_ACCOUNT, TEST_USER, TEST_TENANT not set")
	}
}

// fixturePath returns the path to a benchmark fixture given its sub-tree
// location relative to `llm/benchmark/llm/agents/<subtree>/fixtures/`.
//
// Examples:
//
//	fixturePath("nubi", "17_oom_kill")
//	  → ../../benchmark/llm/agents/nubi/fixtures/17_oom_kill/test_case.yaml
//
//	fixturePath("cloud/aws", "022_some_lambda_scenario")
//	  → ../../benchmark/llm/agents/cloud/aws/fixtures/022_some_lambda_scenario/test_case.yaml
//
// All Go-driven fixtures currently live under `nubi/` and route through the
// k8s_debug agent (the agent itself delegates into AWS/GCP/Azure tooling as
// needed — see e.g. nubi/fixtures/22_high_latency_dbi_down which exercises
// the RDS path through a K8s framing). When the benchmark tree gains
// cloud-rooted scenarios that want a non-K8s entry agent, set `agent: <name>`
// in the fixture YAML and the loader's f.Agent(t, fallback) will route to
// it; the fallback passed by the test author is only used when the YAML
// leaves the agent unspecified.
func fixturePath(subtree, id string) string {
	return "../../benchmark/llm/agents/" + subtree + "/fixtures/" + id + "/test_case.yaml"
}

// safeBuffer is a mutex-guarded bytes.Buffer used to capture stderr of a
// background `exec.Cmd`. The cmd's stderr-pump goroutine writes here while the
// test goroutine reads via String()/Len() during the readiness wait and on
// failure paths, so the underlying buffer must be synchronized.
type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *safeBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *safeBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

func (s *safeBuffer) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Len()
}
