package common

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/shlex"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestQuoteCommandArgRoundTrip(t *testing.T) {
	value := "client secret with spaces and 'quotes' $dollars"
	stdout, stderr, err := SecureExecute(context.Background(), SecureCommandOptions{Command: "printf '%s' " + QuoteCommandArg(value)})
	if err != nil {
		t.Fatalf("quote round trip failed: %v (stderr: %s)", err, stderr)
	}
	if stdout != value {
		t.Fatalf("quoted argument changed: got %q, want %q", stdout, value)
	}
}

func TestSecureExecute(t *testing.T) {
	t.Run("SimpleSuccess", func(t *testing.T) {
		opts := SecureCommandOptions{
			Command: "echo hello",
		}
		stdout, stderr, err := SecureExecute(context.Background(), opts)
		if err != nil {
			t.Fatalf("expected no error, got %v (stderr: %s)", err, stderr)
		}
		if strings.TrimSpace(stdout) != "hello" {
			t.Errorf("expected stdout 'hello', got %q", stdout)
		}
	})

	t.Run("CommandError", func(t *testing.T) {
		opts := SecureCommandOptions{
			Command: "ls /non-existent-directory-12345",
		}
		_, stderr, err := SecureExecute(context.Background(), opts)
		if err == nil {
			t.Fatal("expected an error, got none")
		}
		if !strings.Contains(stderr, "No such file or directory") {
			t.Errorf("expected stderr to contain 'No such file or directory', got %q", stderr)
		}
	})

	t.Run("Timeout", func(t *testing.T) {
		opts := SecureCommandOptions{
			Command: "sleep 1",
			Timeout: 50 * time.Millisecond,
		}
		_, _, err := SecureExecute(context.Background(), opts)
		if err == nil {
			t.Fatal("expected a timeout error, got none")
		}
		if !strings.Contains(err.Error(), "command timed out") {
			t.Errorf("expected error to be about timeout, got %v", err)
		}
	})

	t.Run("StructuredCommandError", func(t *testing.T) {
		_, _, err := SecureExecute(context.Background(), SecureCommandOptions{
			Command:        "sh -c 'exit 7'",
			MaxOutputBytes: 1024,
		})
		var exitErr *CommandExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode != 7 {
			t.Fatalf("expected typed exit 7, got %T %v", err, err)
		}
	})

	t.Run("StructuredTimeout", func(t *testing.T) {
		_, _, err := SecureExecute(context.Background(), SecureCommandOptions{
			Command:        "sleep 1",
			Timeout:        50 * time.Millisecond,
			MaxOutputBytes: 1024,
		})
		var timeoutErr *CommandTimeoutError
		if !errors.As(err, &timeoutErr) {
			t.Fatalf("expected typed timeout, got %T %v", err, err)
		}
	})

	t.Run("OutputIsCappedAndDrained", func(t *testing.T) {
		truncated := false
		opts := SecureCommandOptions{
			Command:         "sh -c 'printf 1234567890; printf abcdefghij >&2'",
			MaxOutputBytes:  5,
			OutputTruncated: &truncated,
		}
		stdout, stderr, err := SecureExecute(context.Background(), opts)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if stdout != "12345" || stderr != "abcde" {
			t.Fatalf("expected capped streams, got stdout=%q stderr=%q", stdout, stderr)
		}
		if !truncated {
			t.Fatal("expected output truncation to be reported")
		}
	})

	t.Run("StructuredOutputDrainIsBounded", func(t *testing.T) {
		stdoutReader, stdoutWriter, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = stdoutWriter.Close() }()
		stderrReader, stderrWriter, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = stderrWriter.Close() }()

		stdoutDone := make(chan struct{})
		stderrDone := make(chan struct{})
		go func() {
			_, _ = io.Copy(io.Discard, stdoutReader)
			close(stdoutDone)
		}()
		go func() {
			_, _ = io.Copy(io.Discard, stderrReader)
			close(stderrDone)
		}()

		if !waitForCommandOutput(true, stdoutReader, stderrReader, stdoutDone, stderrDone, 10*time.Millisecond) {
			t.Fatal("expected inherited pipe wait to time out")
		}
	})

	// Regression test for #36530: without Setpgid, Kill(-pid, ...) targeted a
	// process group that didn't exist and failed with ESRCH (silently, since the
	// error was discarded), so a timed-out process kept running to completion
	// instead of being terminated. Prove the process is actually dead by having
	// it create a marker file after the timeout window and asserting it never
	// appears.
	//
	// The marker write runs in a backgrounded subshell (`(...) &`) that outlives
	// its parent `sh`, while `sh` itself is kept alive by the trailing `sleep 1`
	// so it's still the process the timeout kills. This only proves the fix if
	// the whole process *group* is killed, not just the direct child — a
	// single-pid kill would leave the backgrounded subshell running to still
	// write the marker, since it isn't `sh`'s direct descendant in the wait
	// sense once backgrounded.
	t.Run("TimeoutActuallyKillsProcess", func(t *testing.T) {
		marker := t.TempDir() + "/ran"
		opts := SecureCommandOptions{
			Command: fmt.Sprintf("sh -c '(sleep 0.3 && touch %s) & sleep 1'", marker),
			Timeout: 50 * time.Millisecond,
		}
		_, _, err := SecureExecute(context.Background(), opts)
		if err == nil || !strings.Contains(err.Error(), "command timed out") {
			t.Fatalf("expected a timeout error, got %v", err)
		}

		time.Sleep(500 * time.Millisecond)
		if _, statErr := os.Stat(marker); statErr == nil {
			t.Fatal("marker file exists: process kept running past its timeout instead of being killed")
		}
	})
}

func TestValidateCliCommand(t *testing.T) {
	t.Run("single token blocked prefixes", func(t *testing.T) {
		blocked := []string{"configure", "sso", "sts"}
		tests := []struct {
			name    string
			command string
			wantErr bool
		}{
			{"allowed command", "aws s3 ls", false},
			{"blocked configure", "aws configure set region us-east-1", true},
			{"blocked sso", "aws sso login", true},
			{"blocked sts", "aws sts get-session-token", true},
			{"allowed with similar prefix", "aws s3api list-buckets", false},
			{"single word command", "aws", false},
			{"empty command", "", false},
			{"blocked with flags before subcommand", "aws --region us-east-1 sso login", true},
			{"blocked with flag=value before subcommand", "aws --output=json configure list", true},
			{"no false positive on substring", "aws authorize-security-group-ingress", false},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				err := ValidateCliCommand(tt.command, blocked)
				if (err != nil) != tt.wantErr {
					t.Errorf("ValidateCliCommand(%q) error = %v, wantErr %v", tt.command, err, tt.wantErr)
				}
			})
		}
	})

	t.Run("multi token blocked prefixes", func(t *testing.T) {
		blocked := []string{"config set", "config unset", "auth", "init"}
		tests := []struct {
			name    string
			command string
			wantErr bool
		}{
			{"blocked config set", "gcloud config set project my-project", true},
			{"blocked config unset", "gcloud config unset project", true},
			{"allowed config list", "gcloud config list", false},
			{"blocked auth", "gcloud auth print-access-token", true},
			{"blocked auth with flags", "gcloud --project=foo auth login", true},
			{"blocked init", "gcloud init", true},
			{"allowed compute", "gcloud compute instances list", false},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				err := ValidateCliCommand(tt.command, blocked)
				if (err != nil) != tt.wantErr {
					t.Errorf("ValidateCliCommand(%q) error = %v, wantErr %v", tt.command, err, tt.wantErr)
				}
			})
		}
	})
}

func TestValidateUntrustedArgs(t *testing.T) {
	workDir := t.TempDir()
	secretDir := t.TempDir()
	secret := filepath.Join(secretDir, "token.json")
	require.NoError(t, os.WriteFile(secret, []byte("{}"), 0600))

	refused := []struct{ name, command string }{
		{"a pipe stage", "aws ec2 describe-regions | grep -r token " + secretDir},
		{"an aws file:// parameter", "aws ec2 run-instances --cli-input-json file://" + secret},
		{"an aws fileb:// parameter after =", "aws lambda invoke --payload=fileb://" + secret + " out"},
		{"an az @ argument", "az rest --method post --url https://example.com --body @" + secret},
		{"an az @ argument naming a folder", "az rest --method post --url https://example.com --body @" + secretDir},
		{"a gcloud --flags-file value", "gcloud compute instances list --flags-file=" + secret},
		{"a --*-file flag value as the next argument", "gcloud secrets versions add s --data-file " + secret},
		{"a key=path list in a --*-file flag", "gcloud compute instances create vm --metadata-from-file a=b,startup-script=" + secret},
		{"a comma-separated path list in a --*-file flag", "gcloud x --metadata-from-file a.txt," + secret},
		{"a short file flag", "az deployment group create -g rg -f " + secret},
		{"a short file flag with =", "az deployment group create -g rg -f=" + secret},
		{"a key flag naming a local file", "az aks create -g rg -n c --ssh-key " + secret},
		{"a path flag value", "az webapp deploy --src-path " + secretDir},
		{"a relative path that leaves the working directory", "az rest --url https://example.com --body @../"},
		{"a home directory reference", "aws ec2 run-instances --cli-input-json file://~/.aws/credentials"},
		{"an environment variable in a file reference", "aws ec2 run-instances --cli-input-json file://$HOME/x"},
	}
	for _, tt := range refused {
		t.Run("refuses "+tt.name, func(t *testing.T) {
			args, err := shlex.Split(tt.command)
			require.NoError(t, err)
			assert.Error(t, ValidateUntrustedArgs(args, workDir))
		})
	}

	allowed := []struct{ name, command string }{
		{"a pipe inside a quoted query", `aws logs start-query --query-string 'fields @timestamp, @message | filter level = "error" | limit 20'`},
		{"a JMESPath query", `aws ec2 describe-instances --query "Reservations[].Instances[] | [0]"`},
		{"a CloudWatch log group prefix of /", "aws logs describe-log-groups --log-group-name-prefix /"},
		{"a CloudWatch log group named like a local path", "aws logs filter-log-events --log-group-name /var/log/messages"},
		{"an Azure resource id", "az monitor metrics list --resource /subscriptions/0000/resourceGroups/rg"},
		{"a KQL case-insensitive match", `az monitor log-analytics query --workspace w --analytics-query "Heartbeat | where Computer =~ 'vm1'"`},
		{"a CloudWatch Insights field as an argument", "aws logs get-query-results --query-id @timestamp"},
		{"a flag ending in file with a plain value", "aws s3 ls --profile default"},
		{"a path flag naming nothing on disk", "az webapp deploy --src-path ./app.zip"},
		{"a short flag with a plain value", "az group show -n rg -g rg"},
		{"a key flag with a secret value", "az storage blob list --account-name a --account-key c2VjcmV0"},
		{"stdin", "az rest --url https://example.com --body @-"},
	}
	for _, tt := range allowed {
		t.Run("allows "+tt.name, func(t *testing.T) {
			args, err := shlex.Split(tt.command)
			require.NoError(t, err)
			assert.NoError(t, ValidateUntrustedArgs(args, workDir))
		})
	}
}

// An untrusted command is checked before it runs and starts in an empty
// directory of its own, so a relative path reaches nothing on the collector.
func TestSecureExecuteUntrusted(t *testing.T) {
	_, _, err := SecureExecute(context.Background(), SecureCommandOptions{Command: `echo "hello world" | grep hello`, Untrusted: true})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "shell pipes are not supported")

	stdout, _, err := SecureExecute(context.Background(), SecureCommandOptions{Command: "ls -A", Untrusted: true})
	require.NoError(t, err)
	assert.Empty(t, stdout)

	cwd, err := os.Getwd()
	require.NoError(t, err)
	stdout, _, err = SecureExecute(context.Background(), SecureCommandOptions{Command: "pwd", Untrusted: true})
	require.NoError(t, err)
	assert.NotEqual(t, cwd, strings.TrimSpace(stdout))
	_, statErr := os.Stat(strings.TrimSpace(stdout))
	assert.True(t, os.IsNotExist(statErr), "the working directory is removed afterwards")

	// A collector-written command keeps the collector's working directory.
	stdout, _, err = SecureExecute(context.Background(), SecureCommandOptions{Command: "pwd"})
	require.NoError(t, err)
	assert.Equal(t, cwd, strings.TrimSpace(stdout))
}
