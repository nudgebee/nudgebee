package common

import (
	"context"
	"strings"
	"testing"
	"time"
)

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

func TestSecureExecutePipeline(t *testing.T) {
	t.Run("SimplePipelineSuccess", func(t *testing.T) {
		opts := SecureCommandOptions{
			Command: `echo "hello world" | grep hello`,
		}
		stdout, stderr, err := SecureExecutePipeline(context.Background(), opts)
		if err != nil {
			t.Fatalf("expected no error, got %v (stderr: %s)", err, stderr)
		}
		if strings.TrimSpace(stdout) != "hello world" {
			t.Errorf("expected stdout 'hello world', got %q", stdout)
		}
	})

	t.Run("JqPipelineSuccess", func(t *testing.T) {
		opts := SecureCommandOptions{
			Command: `echo '{"foo": "bar"}' | jq .foo`,
		}
		stdout, _, err := SecureExecutePipeline(context.Background(), opts)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if strings.TrimSpace(stdout) != `"bar"` {
			t.Errorf(`expected stdout '"bar"', got %q`, stdout)
		}
	})

	t.Run("DisallowedTool", func(t *testing.T) {
		opts := SecureCommandOptions{
			Command: `echo "hello" | cat`,
		}
		_, _, err := SecureExecutePipeline(context.Background(), opts)
		if err == nil {
			t.Fatal("expected an error for disallowed tool, got none")
		}
		if !strings.Contains(err.Error(), "tool 'cat' is not allowed") {
			t.Errorf("expected error about disallowed tool, got %v", err)
		}
	})

	t.Run("PipelineStageFailure", func(t *testing.T) {
		opts := SecureCommandOptions{
			Command: `echo "hello" | grep non-existent-string`,
		}
		stdout, _, err := SecureExecutePipeline(context.Background(), opts)
		if err == nil {
			t.Fatal("expected an error from grep, got none")
		}
		if stdout != "" {
			t.Errorf("expected stdout to be empty, got %q", stdout)
		}
	})
}
