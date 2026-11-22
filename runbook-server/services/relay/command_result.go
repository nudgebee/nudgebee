package relay

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"nudgebee/runbook/common"
)

// InterpretCommandResponse turns a CLI job's {stdout, stderr, exit_code} into a task result:
// a non-zero exit not in okExit fails; with no exit_code any stderr fails (#41249).
func InterpretCommandResponse(tool, respStr string, okExit ...int) (map[string]any, error) {
	resp := map[string]any{}
	if err := common.UnmarshalJson([]byte(respStr), &resp); err != nil {
		return nil, errors.New(respStr)
	}
	if msg, _ := resp["error"].(string); msg != "" {
		return nil, errors.New(msg)
	}
	stdout, _ := resp["stdout"].(string)
	stderr, _ := resp["stderr"].(string)
	code, hasExit := ExitCode(resp["exit_code"])
	if hasExit && code != 0 && !slices.Contains(okExit, code) {
		return nil, fmt.Errorf("%s exited %d: %s", tool, code, failureDetail(stderr, stdout))
	}
	if !hasExit && stderr != "" {
		return nil, errors.New(stderr)
	}
	out := map[string]any{"data": resp["stdout"]}
	if stderr != "" {
		out["stderr"] = stderr
	}
	if hasExit {
		out["exit_code"] = code
	}
	return out, nil
}

func failureDetail(stderr, stdout string) string {
	if d := strings.TrimSpace(stderr); d != "" {
		return d
	}
	if d := strings.TrimSpace(stdout); d != "" {
		return d
	}
	return "no output"
}

// ExitCode reads an agent's exit_code, whatever numeric type the decoder
// produced. Returns (code, true) only when one is present.
func ExitCode(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case float32:
		return int(n), true
	case int:
		return n, true
	case int32:
		return int(n), true
	case int64:
		return int(n), true
	}
	return 0, false
}

// commandOutputJSON reshapes a script job's {response, exit_code} into the
// {stdout, exit_code} JSON the CLI tasks read.
func commandOutputJSON(responseAny any, responseParsed map[string]any) (string, error) {
	out := map[string]any{"stdout": responseAny}
	if code, ok := ExitCode(responseParsed["exit_code"]); ok {
		out["exit_code"] = code
	}
	b, err := common.MarshalJson(out)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
