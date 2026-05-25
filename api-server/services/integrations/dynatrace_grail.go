package integrations

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"nudgebee/services/common"
	"strings"
	"time"
)

// ErrDQLQueryTimeout is returned when a Grail DQL query exceeds the maximum poll duration.
var ErrDQLQueryTimeout = errors.New("DQL query timed out")

// ---------------------------------------------------------------------------
// Shared Dynatrace Grail DQL executor
//
// Both the integrations (dynatrace_webhook.go) and observability packages need
// to run async DQL queries against Dynatrace Grail.  The observability package
// already imports integrations (for GetDynatraceConfigs), so the canonical
// implementation lives here to avoid a circular dependency.
// ---------------------------------------------------------------------------

const (
	grailMaxPollAttempts = 120
	grailPollInterval    = 1 * time.Second
)

// GrailResult holds the records and optional timeseries metadata from a
// completed DQL query.  Metadata is non-nil only for "timeseries" queries.
type GrailResult struct {
	Records  []map[string]any `json:"records"`
	Metadata *GrailMetadata   `json:"metadata"`
}

// GrailMetadata carries per-metric timestamps returned by timeseries DQL queries.
// The observability metrics layer uses this to reconstruct time-series arrays.
type GrailMetadata struct {
	Metrics GrailMetricsMap `json:"metrics"`
}

// GrailMetricMeta holds the ISO 8601 timestamps for a single timeseries alias.
type GrailMetricMeta struct {
	Timestamps []string `json:"timestamps"`
}

// GrailMetricsMap handles both forms of Grail metrics metadata:
//   - object form (docs/mocks): {"val": {"timestamps": [...]}}
//   - array form  (real API):   [{"fieldName": "val", ...}]
type GrailMetricsMap map[string]*GrailMetricMeta

// UnmarshalJSON handles both forms of the Grail metrics metadata field.
func (m *GrailMetricsMap) UnmarshalJSON(data []byte) error {
	var objForm map[string]*GrailMetricMeta
	if err := json.Unmarshal(data, &objForm); err == nil {
		*m = GrailMetricsMap(objForm)
		return nil
	}
	var arrForm []struct {
		FieldName  string   `json:"fieldName"`
		Name       string   `json:"name"`
		Timestamps []string `json:"timestamps"`
	}
	if err := json.Unmarshal(data, &arrForm); err != nil {
		return err
	}
	result := make(GrailMetricsMap, len(arrForm))
	for i := range arrForm {
		key := arrForm[i].FieldName
		if key == "" {
			key = arrForm[i].Name
		}
		if key != "" {
			result[key] = &GrailMetricMeta{Timestamps: arrForm[i].Timestamps}
		}
	}
	*m = result
	return nil
}

// grailExecuteRequest is the JSON body sent to the DQL execute endpoint.
type grailExecuteRequest struct {
	Query string `json:"query"`
}

// grailQueryResponse covers both the execute and poll response envelopes.
type grailQueryResponse struct {
	State        string       `json:"state"`
	RequestToken string       `json:"requestToken"`
	Result       *GrailResult `json:"result"`
	Error        *grailError  `json:"error"`
}

type grailError struct {
	Message string `json:"message"`
	Code    int    `json:"code"`
}

// grailBaseURL normalizes a Dynatrace environment URL for Grail platform API calls.
// The Grail /platform/storage/ API is only available at {id}.apps.dynatrace.com.
// Users often configure the classic {id}.live.dynatrace.com URL; auto-convert it so
// all DQL calls reach the correct endpoint without requiring a config change.
func grailBaseURL(baseURL string) string {
	return strings.ReplaceAll(baseURL, ".live.dynatrace.com", ".apps.dynatrace.com")
}

// ExecuteDQLQuery executes a Dynatrace Grail DQL query using the async
// execute → poll pattern and returns the full result (Records + Metadata).
// Metadata is populated for timeseries queries and nil otherwise.
func ExecuteDQLQuery(baseURL, bearerToken, query string) (*GrailResult, error) {
	headers := map[string]string{
		"Authorization": "Bearer " + bearerToken,
		"Content-Type":  "application/json",
		"Accept":        "application/json",
	}

	base := grailBaseURL(baseURL)
	executeURL := strings.TrimRight(base, "/") + "/platform/storage/query/v1/query:execute"
	res, err := common.HttpPost(executeURL,
		common.HttpWithHeaders(headers),
		common.HttpWithJsonBody(grailExecuteRequest{Query: query}),
	)
	if err != nil {
		return nil, fmt.Errorf("DQL execute request failed: %w", err)
	}
	body, err := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("failed to read DQL execute response: %w", err)
	}
	if res.StatusCode != http.StatusOK && res.StatusCode != http.StatusAccepted {
		return nil, fmt.Errorf("DQL execute returned status %d: %s", res.StatusCode, string(body))
	}

	var execResp grailQueryResponse
	if err := json.Unmarshal(body, &execResp); err != nil {
		return nil, fmt.Errorf("failed to parse DQL execute response: %w", err)
	}

	switch execResp.State {
	case "SUCCEEDED":
		if execResp.Result != nil {
			return execResp.Result, nil
		}
		return &GrailResult{Records: []map[string]any{}}, nil
	case "FAILED":
		return nil, grailQueryFailed(execResp.Error)
	}

	// RUNNING — poll until complete.
	pollHeaders := map[string]string{
		"Authorization": "Bearer " + bearerToken,
		"Accept":        "application/json",
	}
	pollBase := strings.TrimRight(base, "/") + "/platform/storage/query/v1/query:poll"

	for attempt := 0; attempt < grailMaxPollAttempts; attempt++ {
		time.Sleep(grailPollInterval)

		pollURL := pollBase + "?request-token=" + url.QueryEscape(execResp.RequestToken)
		res, err := common.HttpGet(pollURL, common.HttpWithHeaders(pollHeaders))
		if err != nil {
			return nil, fmt.Errorf("DQL poll request failed: %w", err)
		}
		body, err := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("failed to read DQL poll response: %w", err)
		}
		if res.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("DQL poll returned status %d: %s", res.StatusCode, string(body))
		}

		var pollResp grailQueryResponse
		if err := json.Unmarshal(body, &pollResp); err != nil {
			return nil, fmt.Errorf("failed to parse DQL poll response: %w", err)
		}

		switch pollResp.State {
		case "SUCCEEDED":
			if pollResp.Result != nil {
				return pollResp.Result, nil
			}
			return &GrailResult{Records: []map[string]any{}}, nil
		case "FAILED":
			return nil, grailQueryFailed(pollResp.Error)
		}
		// RUNNING — continue polling.
	}

	return nil, fmt.Errorf("DQL query timed out after %d seconds: %w", grailMaxPollAttempts, ErrDQLQueryTimeout)
}

// grailQueryFailed converts a Grail error envelope to a Go error.
func grailQueryFailed(e *grailError) error {
	if e != nil {
		return fmt.Errorf("DQL query failed (code %d): %s", e.Code, e.Message)
	}
	return fmt.Errorf("DQL query failed with unknown error")
}

// EscapeDQLValue escapes backslashes and double-quotes for safe use inside
// DQL string literals (e.g. filter field == "value").
func EscapeDQLValue(v any) string {
	s := fmt.Sprintf("%v", v)
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\t", " ")
	return s
}

// GrailStr safely extracts a string value from a Grail DQL record map.
// Returns "" when the key is absent or the value is not a string.
func GrailStr(record map[string]any, key string) string {
	if v, ok := record[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}
