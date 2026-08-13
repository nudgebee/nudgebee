package workspace

import (
	"testing"
)

func TestParseExtraEnv(t *testing.T) {
	envs := ParseExtraEnv("AGENT_HARNESS_VERIFY=true, AGENT_INLOOP_VERIFY=true ,NB_CLI_STAGE_EXITS=false")
	if len(envs) != 3 {
		t.Fatalf("want 3 env vars, got %d: %+v", len(envs), envs)
	}
	if envs[0].Name != "AGENT_HARNESS_VERIFY" || envs[0].Value != "true" {
		t.Errorf("unexpected first env: %+v", envs[0])
	}
	if envs[1].Name != "AGENT_INLOOP_VERIFY" {
		t.Errorf("whitespace around entries must be tolerated, got %+v", envs[1])
	}

	// Values may contain '=' — split on the first only.
	envs = ParseExtraEnv("GOFLAGS=-mod=mod")
	if len(envs) != 1 || envs[0].Value != "-mod=mod" {
		t.Errorf("value with '=' must survive, got %+v", envs)
	}

	// Malformed and invalid-name entries are skipped, never fail pod creation:
	// an invalid env name would 422 the whole pod at the API server.
	envs = ParseExtraEnv("NOVALUE,=orphan, ,VALID=1, 1INVALID=2, INVALID-KEY=3, SPACES IN KEY=4, KEY_WITH_SPACES = VALUE ")
	if len(envs) != 2 || envs[0].Name != "VALID" || envs[1].Name != "KEY_WITH_SPACES" || envs[1].Value != "VALUE" {
		t.Errorf("malformed entries must be skipped and values trimmed, got %+v", envs)
	}

	if got := ParseExtraEnv(""); len(got) != 0 {
		t.Errorf("empty input must yield no envs, got %+v", got)
	}
}
