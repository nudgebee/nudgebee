//go:build e2e

package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

// ============================================================
// flagd feature-flag controller
// ============================================================
//
// Go port of benchmark/llm/agents/rca/utils/feature_flag_controller.py.
// Toggles flags in the OpenTelemetry demo's `flagd-config` configmap so
// RCA fixtures can inject deterministic failure modes (productCatalogFailure,
// adServiceFailure, etc.) without needing to kubectl apply custom manifests.
//
// Uses kubectl via exec.Command exactly like the Python version — no K8s
// client-go dependency for tests.

const (
	flagdNamespace  = "nudgebee-demo"
	flagdConfigMap  = "flagd-config"
	flagdConfigKey  = "demo.flagd.json"
	flagdDeployment = "deployment/flagd"
	flagdSettleWait = 10 * time.Second
)

// We parse the flagd config as a generic map so we preserve any keys we
// don't know about across roundtrips — the demo config evolves and we only
// want to mutate `defaultVariant` for a single flag.

// getFlagdConfig reads the current flagd config from the configmap.
func getFlagdConfig(t *testing.T) (map[string]any, error) {
	t.Helper()
	out, err := kubectl(t, "get", "configmap", flagdConfigMap,
		"-n", flagdNamespace,
		"-o", `jsonpath={.data.demo\.flagd\.json}`,
	)
	if err != nil {
		return nil, fmt.Errorf("get configmap: %w", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(out, &cfg); err != nil {
		return nil, fmt.Errorf("parse flagd config: %w", err)
	}
	return cfg, nil
}

// patchFlagdConfig writes the given config back to the configmap and restarts
// the flagd deployment so the new values take effect.
func patchFlagdConfig(t *testing.T, cfg map[string]any) error {
	t.Helper()
	cfgJSON, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal flagd config: %w", err)
	}
	patch := map[string]any{
		"data": map[string]string{flagdConfigKey: string(cfgJSON)},
	}
	patchJSON, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("marshal patch: %w", err)
	}
	if _, err := kubectl(t, "patch", "configmap", flagdConfigMap,
		"-n", flagdNamespace,
		"--type", "merge",
		"-p", string(patchJSON),
	); err != nil {
		return fmt.Errorf("patch configmap: %w", err)
	}
	if _, err := kubectl(t, "rollout", "restart", flagdDeployment, "-n", flagdNamespace); err != nil {
		return fmt.Errorf("rollout restart: %w", err)
	}
	if _, err := kubectl(t, "rollout", "status", flagdDeployment,
		"-n", flagdNamespace, "--timeout=60s",
	); err != nil {
		return fmt.Errorf("rollout status: %w", err)
	}
	// Give services a moment to pick up new flag values.
	time.Sleep(flagdSettleWait)
	return nil
}

// enableDemoFlag sets the default variant of `flagName` to `variant`.
func enableDemoFlag(t *testing.T, flagName, variant string) error {
	t.Helper()
	cfg, err := getFlagdConfig(t)
	if err != nil {
		return err
	}
	if err := setDefaultVariant(cfg, flagName, variant); err != nil {
		return err
	}
	t.Logf("[flagd] enabling flag %q variant=%q", flagName, variant)
	return patchFlagdConfig(t, cfg)
}

// disableDemoFlag resets the default variant of `flagName` to "off".
func disableDemoFlag(t *testing.T, flagName string) error {
	t.Helper()
	cfg, err := getFlagdConfig(t)
	if err != nil {
		return err
	}
	if err := setDefaultVariant(cfg, flagName, "off"); err != nil {
		return err
	}
	t.Logf("[flagd] disabling flag %q", flagName)
	return patchFlagdConfig(t, cfg)
}

// setDefaultVariant mutates the parsed flagd config to set flagName's default
// variant. Returns an error if the flag doesn't exist (catches typos in
// fixtures early).
func setDefaultVariant(cfg map[string]any, flagName, variant string) error {
	flagsRaw, ok := cfg["flags"]
	if !ok {
		return fmt.Errorf("flagd config has no 'flags' key")
	}
	flags, ok := flagsRaw.(map[string]any)
	if !ok {
		return fmt.Errorf("flagd config 'flags' is not an object")
	}
	flagRaw, ok := flags[flagName]
	if !ok {
		return fmt.Errorf("flag %q not found in configmap", flagName)
	}
	flag, ok := flagRaw.(map[string]any)
	if !ok {
		return fmt.Errorf("flag %q has unexpected shape", flagName)
	}
	flag["defaultVariant"] = variant
	flags[flagName] = flag
	cfg["flags"] = flags
	return nil
}

// kubectlTimeout is the outer timeout for any single kubectl invocation. It
// must comfortably exceed any inner --timeout=<dur> we pass to kubectl itself
// (currently 60s for `rollout status`), otherwise our outer Kill will fire
// before kubectl has a chance to return success on a slow rollout.
const kubectlTimeout = 90 * time.Second

// kubectl runs `kubectl <args>` with a bounded outer timeout and returns
// stdout on success. stderr is surfaced in the error message.
func kubectl(t *testing.T, args ...string) ([]byte, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), kubectlTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "kubectl", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("kubectl %v: timeout after %s", args, kubectlTimeout)
		}
		return nil, fmt.Errorf("kubectl %v: %w; stderr=%s", args, err, stderr.String())
	}
	return stdout.Bytes(), nil
}

// getDeploymentReplicas reads `.spec.replicas` for a deployment. Used by
// mutation-tests to snapshot state so they can restore in t.Cleanup.
func getDeploymentReplicas(t *testing.T, namespace, deployment string) (int, error) {
	t.Helper()
	out, err := kubectl(t, "get", "deployment", deployment, "-n", namespace,
		"-o", "jsonpath={.spec.replicas}")
	if err != nil {
		return 0, err
	}
	s := strings.TrimSpace(string(out))
	if s == "" {
		return 0, fmt.Errorf("deployment %s/%s: empty replicas", namespace, deployment)
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("parse replicas %q: %w", s, err)
	}
	return n, nil
}

// listPodNames returns the names of all pods in a namespace. Used by
// mutation-tests that snapshot the pod set before the agent acts so they
// can identify and delete pods the agent leaked.
func listPodNames(t *testing.T, namespace string) ([]string, error) {
	t.Helper()
	out, err := kubectl(t, "get", "pods", "-n", namespace,
		"-o", "jsonpath={.items[*].metadata.name}")
	if err != nil {
		return nil, err
	}
	s := strings.TrimSpace(string(out))
	if s == "" {
		return nil, nil
	}
	return strings.Fields(s), nil
}
