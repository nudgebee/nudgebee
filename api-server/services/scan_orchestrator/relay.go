package scan_orchestrator

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"errors"
	"fmt"
	"io"

	"nudgebee/services/relay"
)

// Three thin wrappers around relay.Execute that match the agent's primitives.
// Each call hits the agent's HMAC-signed action path; auth posture is unchanged
// from the existing `relay.Execute("popeye_scan", ...)` shape we're replacing.

// scheduleJob asks the agent to create a Job from the supplied spec and
// returns (job_name, jobUUID, scheduled bool). When the agent's concurrency
// cap rejects the schedule it returns scheduled=false with no error so the
// caller can back off + retry.
func scheduleJob(accountID string, spec JobSpec) (jobName string, jobUUID string, scheduled bool, err error) {
	resp, err := relay.Execute(relay.RelayExecuteRequest{
		Body: relay.ActionExecuteBody{
			AccountID:    accountID,
			ActionName:   "schedule_k8s_job",
			ActionParams: map[string]any{"spec": spec},
			Origin:       "scan_orchestrator",
		},
		NoSinks:        true,
		Cache:          false,
		TimeoutSeconds: 30,
		AgentType:      "k8s",
	})
	if err != nil {
		return "", "", false, err
	}
	data, ok := resp["data"].(map[string]any)
	if !ok {
		return "", "", false, fmt.Errorf("schedule_k8s_job: relay response missing data: %+v", resp)
	}
	// Agent returns success=false + error="concurrent_job_limit" without an HTTP error.
	if success, ok := data["success"].(bool); ok && !success {
		errCode, _ := data["error"].(string)
		if errCode == "concurrent_job_limit" {
			return "", "", false, nil
		}
		return "", "", false, fmt.Errorf("schedule_k8s_job: agent returned error %q", errCode)
	}
	jobName, _ = data["job_name"].(string)
	jobUUID, _ = data["job_uuid"].(string)
	if jobName == "" {
		return "", "", false, fmt.Errorf("schedule_k8s_job: response missing job_name: %+v", data)
	}
	return jobName, jobUUID, true, nil
}

// jobStatus is a typed view of wait_for_k8s_job's response.
type jobStatus struct {
	Status         string // "Running" | "Complete" | "Failed" | "NotFound"
	FailureReason  string
	StartTime      string
	CompletionTime string
}

func waitForJob(accountID, jobName string) (jobStatus, error) {
	var s jobStatus
	resp, err := relay.Execute(relay.RelayExecuteRequest{
		Body: relay.ActionExecuteBody{
			AccountID:    accountID,
			ActionName:   "wait_for_k8s_job",
			ActionParams: map[string]any{"job_name": jobName},
			Origin:       "scan_orchestrator",
		},
		NoSinks:        true,
		Cache:          false,
		TimeoutSeconds: 30,
		AgentType:      "k8s",
	})
	if err != nil {
		return s, err
	}
	data, ok := resp["data"].(map[string]any)
	if !ok {
		return s, fmt.Errorf("wait_for_k8s_job: relay response missing data: %+v", resp)
	}
	s.Status, _ = data["status"].(string)
	s.FailureReason, _ = data["failure_reason"].(string)
	s.StartTime, _ = data["start_time"].(string)
	s.CompletionTime, _ = data["completion_time"].(string)
	if s.Status == "" {
		return s, errors.New("wait_for_k8s_job: agent response missing status")
	}
	return s, nil
}

// jobLogs is a typed view of get_k8s_job_logs's response.
type jobLogs struct {
	Stdout             string
	Truncated          bool
	StdoutBytesDropped int
}

func getJobLogs(accountID, jobName string) (jobLogs, error) {
	var l jobLogs
	resp, err := relay.Execute(relay.RelayExecuteRequest{
		Body: relay.ActionExecuteBody{
			AccountID:    accountID,
			ActionName:   "get_k8s_job_logs",
			ActionParams: map[string]any{"job_name": jobName},
			Origin:       "scan_orchestrator",
		},
		NoSinks:        true,
		Cache:          false,
		TimeoutSeconds: 60, // logs can be large
		AgentType:      "k8s",
	})
	if err != nil {
		return l, err
	}
	data, ok := resp["data"].(map[string]any)
	if !ok {
		return l, fmt.Errorf("get_k8s_job_logs: relay response missing data: %+v", resp)
	}
	// Newer agents (post-feat/get-job-logs-gzip) return stdout_b64_gzip;
	// older agents return raw stdout. Support both during the rollout window.
	if encoded, ok := data["stdout_b64_gzip"].(string); ok && encoded != "" {
		stdout, decErr := decodeStdoutB64Gzip(encoded)
		if decErr != nil {
			return l, fmt.Errorf("get_k8s_job_logs: inflate stdout: %w", decErr)
		}
		l.Stdout = stdout
	} else {
		l.Stdout, _ = data["stdout"].(string)
	}
	l.Truncated, _ = data["truncated"].(bool)
	if dropped, ok := data["stdout_bytes_dropped"].(float64); ok {
		l.StdoutBytesDropped = int(dropped)
	}
	return l, nil
}

// resolveServiceAccountPlaceholder lets a Scanner's BuildSpec leave
// `{{SCANNER_SA}}` as a placeholder; the orchestrator resolves it from the
// account's preferred SA before sending to the agent. (The agent itself
// accepts any string; this is purely an api-server affordance so the catalog
// doesn't need per-account state.)
func resolveServiceAccountPlaceholder(spec *JobSpec, defaultSA string) {
	if spec.ServiceAccount == "{{SCANNER_SA}}" {
		spec.ServiceAccount = defaultSA
	}
}

// maxInflatedStdoutBytes caps the uncompressed size we'll accept from the
// agent's gzipped stdout. The agent's raw fetchPodLogs cap is 64 MiB, so
// any legit value lands well below this; the cap exists to bound damage
// from a hostile or buggy compressed payload (zip-bomb defence).
const maxInflatedStdoutBytes = 128 << 20 // 128 MiB

// decodeStdoutB64Gzip is the inverse of the agent's encode step in
// nudgebee-agent/pkg/scanners.handleGetJobLogs. The agent gzips + base64s the
// pod stdout so the relay→api-server hop carries an order-of-magnitude less
// JSON payload (Trivy CIS reports compress ~10x). We inflate transparently
// so callers (RunScan, parsers) see plain strings.
func decodeStdoutB64Gzip(s string) (string, error) {
	if s == "" {
		return "", nil
	}
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return "", fmt.Errorf("base64: %w", err)
	}
	gr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return "", fmt.Errorf("gzip reader: %w", err)
	}
	defer func() { _ = gr.Close() }()
	// Read one byte past the cap so we can distinguish "reached cap exactly"
	// from "would have produced more" — the second case is the zip-bomb shape.
	out, err := io.ReadAll(io.LimitReader(gr, maxInflatedStdoutBytes+1))
	if err != nil {
		return "", fmt.Errorf("gzip read: %w", err)
	}
	if len(out) > maxInflatedStdoutBytes {
		return "", fmt.Errorf("inflated stdout exceeds %d MiB cap (zip-bomb guard)", maxInflatedStdoutBytes>>20)
	}
	return string(out), nil
}
