package handlers

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// kcatStubDir writes a fake `kcat` onto a tmp dir and returns the dir so it can
// be prepended to PATH. The stub prints each argv element on its own line, which
// lets the tests observe exactly how the shell split the generated command into
// arguments — the only reliable way to catch word-splitting regressions.
func kcatStubDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	stub := "#!/bin/sh\nfor a in \"$@\"; do printf '%s\\n' \"$a\"; done\n"
	path := filepath.Join(dir, "kcat")
	require.NoError(t, os.WriteFile(path, []byte(stub), 0o755))
	return dir
}

// runKafkaCommand builds the kafka workspace action, then executes the generated
// shell command locally with a stubbed kcat and the supplied KAFKA_* env vars.
// It returns the argv that kcat actually received (one element per slice entry).
func runKafkaCommand(t *testing.T, env map[string]string) []string {
	t.Helper()
	_, params, err := buildWorkspaceAction("kafka", "kcat -L", nil, map[string]any{}, "kcat:latest")
	require.NoError(t, err)
	command, ok := params["command"].(string)
	require.True(t, ok, "command must be a string")

	cmd := exec.Command("/bin/sh", "-c", command)
	cmd.Env = []string{"PATH=" + kcatStubDir(t) + ":" + os.Getenv("PATH")}
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "command failed: %s\noutput: %s", command, out)

	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	// Drop the leading "kcat" program name echoed by some shells — here argv
	// excludes argv[0], so every line is a real argument.
	return lines
}

// TestBuildWorkspaceAction_Kafka_PasswordWithSpacesIsSingleArg is the security
// regression test for the kcat flag quoting. A SASL password containing spaces
// must reach kcat as ONE argument (`sasl.password=<value>`), not be split on
// whitespace into broken fragments. This pins the fix for the unquoted
// `${VAR:+ -X key="$VAR"}` word-splitting bug flagged in PR #417.
func TestBuildWorkspaceAction_Kafka_PasswordWithSpacesIsSingleArg(t *testing.T) {
	args := runKafkaCommand(t, map[string]string{
		"KAFKA_BROKERS":           "broker:9092",
		"KAFKA_SECURITY_PROTOCOL": "SASL_SSL",
		"KAFKA_SASL_MECHANISM":    "PLAIN",
		"KAFKA_SASL_USERNAME":     "svc account",
		"KAFKA_SASL_PASSWORD":     "p@ss w0rd with spaces",
	})

	assert.Contains(t, args, "sasl.password=p@ss w0rd with spaces",
		"password with spaces must arrive as a single kcat argument")
	assert.Contains(t, args, "sasl.username=svc account",
		"username with spaces must arrive as a single kcat argument")
	assert.Contains(t, args, "security.protocol=SASL_SSL")
	assert.Contains(t, args, "sasl.mechanism=PLAIN")
	assert.Contains(t, args, "broker:9092")

	// One -X per SASL/TLS property (4), and the value must never leak across
	// argument boundaries (no bare "spaces" fragment from a split).
	xCount := 0
	for _, a := range args {
		if a == "-X" {
			xCount++
		}
		assert.NotEqual(t, "spaces", a, "value was word-split — quoting regressed")
		assert.NotEqual(t, `with`, a, "value was word-split — quoting regressed")
	}
	assert.Equal(t, 4, xCount, "expected exactly four -X flags (one per set property)")
}

// TestBuildWorkspaceAction_Kafka_PlaintextOmitsSaslFlags pins the PLAINTEXT
// path: when the optional SASL/TLS secret keys are absent, the ${VAR:+...}
// expansions must collapse to nothing so kcat is invoked with only -b. A stray
// empty `-X ""` would make kcat reject the connection.
func TestBuildWorkspaceAction_Kafka_PlaintextOmitsSaslFlags(t *testing.T) {
	args := runKafkaCommand(t, map[string]string{
		"KAFKA_BROKERS": "broker:9092",
	})

	assert.Contains(t, args, "broker:9092")
	for _, a := range args {
		assert.NotEqual(t, "-X", a, "no -X flags expected on a PLAINTEXT cluster")
		assert.NotContains(t, a, "sasl.", "no SASL properties expected on a PLAINTEXT cluster")
		assert.NotContains(t, a, "security.protocol", "no security.protocol expected when unset")
	}
}

// TestBuildWorkspaceAction_Kafka_PartialSaslFlags verifies each property is
// independently gated: security.protocol set but no SASL credentials yields the
// protocol flag only (e.g. an SSL-without-SASL cluster).
func TestBuildWorkspaceAction_Kafka_PartialSaslFlags(t *testing.T) {
	args := runKafkaCommand(t, map[string]string{
		"KAFKA_BROKERS":           "broker:9092",
		"KAFKA_SECURITY_PROTOCOL": "SSL",
	})

	assert.Contains(t, args, "security.protocol=SSL")
	xCount := 0
	for _, a := range args {
		if a == "-X" {
			xCount++
		}
		assert.NotContains(t, a, "sasl.", "no SASL properties expected when credentials unset")
	}
	assert.Equal(t, 1, xCount, "expected exactly one -X flag for security.protocol")
}
