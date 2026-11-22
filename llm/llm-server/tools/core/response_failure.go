package core

import (
	"encoding/json"
	"strings"
)

// FailureReason says why a reply that came back OK is still a failure.
type FailureReason string

const (
	FailureReasonMalformedResponse FailureReason = "malformed_response"
	FailureReasonReportedFailed    FailureReason = "reported_failed"
	FailureReasonBadInput          FailureReason = "bad_input"
	FailureReasonInfra             FailureReason = "infra"
)

// FailureRules is what one tool declares about its own replies, on top of the
// default rules in StructuredResponseFailed.
type FailureRules struct {
	// ExpectJSON makes a non-JSON body a malformed_response failure.
	ExpectJSON bool
	// StatusPath is a dotted path to a status string ("resolution.status");
	// FailingStatuses are the values at that path that mean failure, matched
	// case-insensitively. Empty StatusPath disables the check.
	StatusPath      string
	FailingStatuses []string
	// ClosedStatuses is the documented set of values at StatusPath. A value
	// outside it fails closed as a malformed_response, so a status the tool
	// has never heard of is not read as success. Empty means no closed set.
	ClosedStatuses []string
}

// StructuredResponseFailed reports whether response is a JSON object and, if
// so, whether it says it failed: a non-empty top-level "error" or "errors", or
// a top-level "status" of failed. Nested keys are ignored on purpose — complete
// code-analysis answers carry empty "error" fields inside build steps.
func StructuredResponseFailed(response string) (structured, failed bool) {
	obj, ok := parseJSONObject(response)
	if !ok {
		return false, false
	}
	return true, objectReportsFailure(obj)
}

func parseJSONObject(response string) (map[string]json.RawMessage, bool) {
	response = strings.TrimSpace(response)
	if !strings.HasPrefix(response, "{") {
		return nil, false
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(response), &obj); err != nil {
		return nil, false
	}
	return obj, true
}

func objectReportsFailure(obj map[string]json.RawMessage) bool {
	for _, key := range []string{"error", "errors"} {
		if v, ok := obj[key]; ok && !isEmptyJSONValue(v) {
			return true
		}
	}
	var status string
	if v, ok := obj["status"]; ok && json.Unmarshal(v, &status) == nil {
		switch strings.TrimSpace(status) {
		case "fail", "failed", "failure", "error":
			return true
		}
	}
	return false
}

func isEmptyJSONValue(v json.RawMessage) bool {
	switch strings.TrimSpace(string(v)) {
	case "null", `""`, "false", "[]", "{}":
		return true
	}
	return false
}

// ClassifyResponse judges a tool body by its fields, never by its prose. An
// empty body is not judged here: the executor already records it as an empty
// result. The body is parsed once.
func ClassifyResponse(body string, rules FailureRules) (failed bool, reason FailureReason) {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return false, ""
	}
	obj, structured := parseJSONObject(trimmed)
	if !structured {
		if rules.ExpectJSON && !json.Valid([]byte(trimmed)) {
			return true, FailureReasonMalformedResponse
		}
		return false, ""
	}
	if objectReportsFailure(obj) {
		return true, FailureReasonReportedFailed
	}
	if rules.StatusPath == "" {
		return false, ""
	}
	status, found := statusAtPath(obj, rules.StatusPath)
	if !found {
		return false, ""
	}
	if containsFold(rules.FailingStatuses, status) {
		return true, FailureReasonReportedFailed
	}
	if len(rules.ClosedStatuses) > 0 && !containsFold(rules.ClosedStatuses, status) {
		return true, FailureReasonMalformedResponse
	}
	return false, ""
}

// statusAtPath returns the string at a dotted path, starting from the object
// already parsed; only the sub-documents on the path are parsed again. found
// is false when any step is missing or the value is not a string.
func statusAtPath(obj map[string]json.RawMessage, path string) (status string, found bool) {
	keys := strings.Split(path, ".")
	for _, key := range keys[:len(keys)-1] {
		raw, ok := obj[key]
		if !ok {
			return "", false
		}
		obj = nil
		if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
			return "", false
		}
	}
	raw, ok := obj[keys[len(keys)-1]]
	if !ok || json.Unmarshal(raw, &status) != nil {
		return "", false
	}
	return status, true
}

func containsFold(set []string, value string) bool {
	value = strings.TrimSpace(value)
	for _, candidate := range set {
		if strings.EqualFold(value, candidate) {
			return true
		}
	}
	return false
}
