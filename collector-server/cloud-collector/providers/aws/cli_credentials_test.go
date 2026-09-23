package aws

import (
	"encoding/json"
	"testing"
	"time"
)

// The session must not let a script trade it for another role's access, or mint
// credentials that outlive it.
func TestCliSessionPolicyDeniesEscalation(t *testing.T) {
	raw, err := cliSessionPolicy()
	if err != nil {
		t.Fatalf("building policy: %v", err)
	}

	var policy struct {
		Statement []struct {
			Effect string `json:"effect"`
			Action any    `json:"action"`
		} `json:"Statement"`
	}
	if err := json.Unmarshal([]byte(raw), &policy); err != nil {
		t.Fatalf("policy is not valid JSON: %v", err)
	}

	denied := map[string]bool{}
	for _, st := range policy.Statement {
		if st.Effect != "Deny" {
			continue
		}
		actions, ok := st.Action.([]any)
		if !ok {
			t.Fatalf("deny statement action is %T, want a list", st.Action)
		}
		for _, a := range actions {
			denied[a.(string)] = true
		}
	}

	for _, action := range []string{"sts:AssumeRole", "iam:CreateAccessKey", "iam:CreateUser", "iam:AttachUserPolicy"} {
		if !denied[action] {
			t.Errorf("%s is not denied by the session policy", action)
		}
	}
}

// terraform's AWS provider calls GetCallerIdentity before anything else, and
// scripts use it to prove which account they are in. Denying it would break
// every run.
func TestCliSessionPolicyAllowsGetCallerIdentity(t *testing.T) {
	raw, err := cliSessionPolicy()
	if err != nil {
		t.Fatalf("building policy: %v", err)
	}
	if contains := json.Valid([]byte(raw)); !contains {
		t.Fatal("policy is not valid JSON")
	}

	var policy struct {
		Statement []struct {
			Effect string `json:"Effect"`
			Action any    `json:"Action"`
		} `json:"Statement"`
	}
	if err := json.Unmarshal([]byte(raw), &policy); err != nil {
		t.Fatalf("policy is not valid JSON: %v", err)
	}
	for _, st := range policy.Statement {
		if st.Effect != "Deny" {
			continue
		}
		actions, ok := st.Action.([]any)
		if !ok {
			continue
		}
		for _, a := range actions {
			if a.(string) == "sts:GetCallerIdentity" || a.(string) == "sts:*" {
				t.Fatalf("session policy denies %q, which breaks terraform and identity checks", a)
			}
		}
	}
}

func TestClampCliCredentialsDuration(t *testing.T) {
	cases := []struct {
		name      string
		requested time.Duration
		want      time.Duration
	}{
		{"unset falls back to the ceiling", 0, time.Hour},
		{"longer than allowed is capped", 8 * time.Hour, time.Hour},
		{"shorter than STS accepts is raised", time.Minute, 15 * time.Minute},
		{"a usable value is kept", 30 * time.Minute, 30 * time.Minute},
		{"negative falls back to the ceiling", -time.Minute, time.Hour},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := clampCliCredentialsDuration(tc.requested); got != tc.want {
				t.Fatalf("clamp(%s) = %s, want %s", tc.requested, got, tc.want)
			}
		})
	}
}
