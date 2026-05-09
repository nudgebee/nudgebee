package api

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"nudgebee/collector/otel/config"
	"nudgebee/collector/otel/metrics"
	"nudgebee/collector/otel/metrics/prometheus"
	"nudgebee/collector/otel/security"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPrometheusReader_Methods(t *testing.T) {
	logger := slog.Default()
	testCases := []struct {
		name               string
		requestType        string
		urlParams          url.Values
		formParams         url.Values
		path               string // Used for validateRequest and extracting label_name
		mockServerResponse *http.Response
		expectedStatus     int
		expectedBody       string
		expectedError      string
		validateRequest    func(t *testing.T, req *http.Request, endpoint string)
	}{
		{
			name:        "ValidQuery",
			requestType: "query",
			urlParams: url.Values{
				"query": {"up"},
			},
			formParams:         url.Values{},
			path:               "/api/v1/query",
			mockServerResponse: &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"status": "success", "data": {"resultType": "vector", "result": []}}`))},
			expectedStatus:     http.StatusOK, // This will be the status from the mock server
			expectedBody:       `{"status": "success", "data": {"resultType": "vector", "result": []}}`,
			expectedError:      "",
			validateRequest: func(t *testing.T, req *http.Request, endpoint string) {
				assert.Equal(t, "/api/v1/query", req.URL.Path)
				assert.Contains(t, req.URL.Query().Get("query"), "account_id=\"test-account\"")
				assert.Contains(t, req.URL.Query().Get("query"), "tenant_id=\"test-tenant\"")
				assert.Contains(t, req.URL.Query().Get("query"), "up")
			},
		},
		{
			name:        "ValidQueryRange",
			requestType: "query_range",
			urlParams:   url.Values{},
			formParams: url.Values{
				"query": {"up"},
				"start": {"1672531200"},
				"end":   {"1672534800"},
				"step":  {"60"},
			},
			path:               "/api/v1/query_range",
			mockServerResponse: &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"status": "success", "data": {"resultType": "matrix", "result": []}}`))},
			expectedStatus:     http.StatusOK,
			expectedBody:       `{"status": "success", "data": {"resultType": "matrix", "result": []}}`,
			expectedError:      "",
			validateRequest: func(t *testing.T, req *http.Request, endpoint string) {
				assert.Equal(t, "/api/v1/query_range", req.URL.Path)
				assert.Contains(t, req.URL.Query().Get("query"), "account_id=\"test-account\"")
				assert.Contains(t, req.URL.Query().Get("query"), "tenant_id=\"test-tenant\"")
				assert.Contains(t, req.URL.Query().Get("query"), "up")
				assert.Equal(t, "1672531200", req.URL.Query().Get("start"))
				assert.Equal(t, "1672534800", req.URL.Query().Get("end"))
				assert.Equal(t, "60", req.URL.Query().Get("step"))
			},
		},
		{
			name:        "ValidSeries",
			requestType: "series",
			urlParams: url.Values{
				"match[]": {"up"},
				"start":   {"1672531200"},
				"end":     {"1672534800"},
			},
			formParams:         url.Values{},
			path:               "/api/v1/series",
			mockServerResponse: &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"status": "success", "data": []}`))},
			expectedStatus:     http.StatusOK,
			expectedBody:       `{"status": "success", "data": []}`,
			expectedError:      "",
			validateRequest: func(t *testing.T, req *http.Request, endpoint string) {
				assert.Equal(t, "/api/v1/series", req.URL.Path)
				assert.Contains(t, req.URL.Query()["match[]"][0], "account_id=\"test-account\"")
				assert.Contains(t, req.URL.Query()["match[]"][0], "tenant_id=\"test-tenant\"")
				assert.Contains(t, req.URL.Query()["match[]"][0], "up")
				assert.Equal(t, "1672531200", req.URL.Query().Get("start"))
				assert.Equal(t, "1672534800", req.URL.Query().Get("end"))
			},
		},
		{
			name:        "ValidLabels",
			requestType: "labels",
			urlParams:   url.Values{},
			formParams: url.Values{
				"match[]": {"up"},
				"start":   {"1672531200"},
				"end":     {"1672534800"},
			},
			path:               "/api/v1/labels",
			mockServerResponse: &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"status": "success", "data": []}`))},
			expectedStatus:     http.StatusOK,
			expectedBody:       `{"status": "success", "data": []}`,
			expectedError:      "",
			validateRequest: func(t *testing.T, req *http.Request, endpoint string) {
				assert.Equal(t, "/api/v1/labels", req.URL.Path)
				assert.Contains(t, req.URL.Query()["match[]"][0], "account_id=\"test-account\"")
				assert.Contains(t, req.URL.Query()["match[]"][0], "tenant_id=\"test-tenant\"")
				assert.Contains(t, req.URL.Query()["match[]"][0], "up")
				assert.Equal(t, "1672531200", req.URL.Query().Get("start"))
				assert.Equal(t, "1672534800", req.URL.Query().Get("end"))
			},
		},
		{
			name:        "ValidLabelValues",
			requestType: "label_values",
			urlParams: url.Values{
				"match[]": {"up"},
				"start":   {"1672531200"},
				"end":     {"1672534800"},
			},
			formParams:         url.Values{},
			path:               "/api/v1/label/job/values",
			mockServerResponse: &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"status": "success", "data": []}`))},
			expectedStatus:     http.StatusOK,
			expectedBody:       `{"status": "success", "data": []}`,
			expectedError:      "",
			validateRequest: func(t *testing.T, req *http.Request, endpoint string) {
				assert.Equal(t, "/api/v1/label/job/values", req.URL.Path)
				assert.Contains(t, req.URL.Query()["match[]"][0], "account_id=\"test-account\"")
				assert.Contains(t, req.URL.Query()["match[]"][0], "tenant_id=\"test-tenant\"")
				assert.Contains(t, req.URL.Query()["match[]"][0], "up")
				assert.Equal(t, "1672531200", req.URL.Query().Get("start"))
				assert.Equal(t, "1672534800", req.URL.Query().Get("end"))
			},
		},
		{
			name:        "InvalidQuery",
			requestType: "query",
			urlParams: url.Values{
				"query": {"up{invalid}"},
			},
			formParams:         url.Values{},
			path:               "/api/v1/query",
			mockServerResponse: &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"status": "error", "errorType": "bad_data", "error": "parse error at char 3: invalid character inside braces"}`))},
			expectedStatus:     http.StatusBadRequest, // This is the status returned by our reader due to bad input
			expectedBody:       "",                    // No body when our reader returns an error before proxying
			expectedError:      "metrics: invalid query: 1:3: parse error: invalid character inside braces",
			validateRequest: func(t *testing.T, req *http.Request, endpoint string) {
				assert.Equal(t, "/api/v1/query", req.URL.Path)
				assert.Contains(t, req.URL.Query().Get("query"), "account_id=\"test-account\"")
				assert.Contains(t, req.URL.Query().Get("query"), "tenant_id=\"test-tenant\"")
				assert.Contains(t, req.URL.Query().Get("query"), "up{invalid}")
			},
		},
		{
			name:        "NoQuery",
			requestType: "query",
			urlParams:   url.Values{},
			formParams:  url.Values{},
			path:        "/api/v1/query", // Not strictly needed for this error case by reader, but good for consistency
			// mockServerResponse not needed as the error occurs before calling the backend
			expectedStatus: http.StatusBadRequest,
			expectedBody:   "",
			expectedError:  "metrics: query parameter is missing",
		},
		{
			name:        "ValidFormatQuery",
			requestType: "format_query",
			urlParams: url.Values{
				"query": {"up"},
			},
			formParams:         url.Values{},
			path:               "/api/v1/format_query",
			mockServerResponse: &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"status": "success", "data": "up"}`))},
			expectedStatus:     http.StatusOK,
			expectedBody:       `{"status": "success", "data": "up"}`,
			expectedError:      "",
			validateRequest: func(t *testing.T, req *http.Request, endpoint string) {
				assert.Equal(t, "/api/v1/format_query", req.URL.Path)
				assert.Equal(t, "up", req.URL.Query().Get("query"))
			},
		},
		{
			name:        "InvalidSeriesMatcher",
			requestType: "series",
			urlParams: url.Values{
				"match[]": {"up{invalid}"},
				"start":   {"1672531200"},
				"end":     {"1672534800"},
			},
			formParams:         url.Values{},
			path:               "/api/v1/series",
			mockServerResponse: &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"status": "success", "data": []}`))},
			expectedStatus:     http.StatusBadRequest,
			expectedBody:       "",
			expectedError:      "metrics: invalid 'match[]' parameter 'up{invalid}': 1:3: parse error: invalid character inside braces",
			validateRequest: func(t *testing.T, req *http.Request, endpoint string) {
				assert.Equal(t, "/api/v1/series", req.URL.Path)
				assert.Contains(t, req.URL.Query()["match[]"][0], "up{invalid}")
				assert.Equal(t, "1672531200", req.URL.Query().Get("start"))
				assert.Equal(t, "1672534800", req.URL.Query().Get("end"))
			},
		},
		{
			name:        "InvalidLabelMatcher",
			requestType: "labels",
			urlParams:   url.Values{},
			formParams: url.Values{
				"match[]": {"up{invalid}"},
				"start":   {"1672531200"},
				"end":     {"1672534800"},
			},
			path:               "/api/v1/labels",
			mockServerResponse: &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"status": "success", "data": []}`))},
			expectedStatus:     http.StatusBadRequest,
			expectedBody:       "",
			expectedError:      "metrics: invalid 'match[]' parameter 'up{invalid}': 1:3: parse error: invalid character inside braces",
			validateRequest: func(t *testing.T, req *http.Request, endpoint string) {
				assert.Equal(t, "/api/v1/labels", req.URL.Path)
				assert.Contains(t, req.URL.Query()["match[]"][0], "up{invalid}")
				assert.Equal(t, "1672531200", req.URL.Query().Get("start"))
				assert.Equal(t, "1672534800", req.URL.Query().Get("end"))
			},
		},
		{
			name:        "InvalidLabelValueMatcher",
			requestType: "label_values",
			urlParams: url.Values{
				"match[]": {"up{invalid}"},
				"start":   {"1672531200"},
				"end":     {"1672534800"},
			},
			formParams:         url.Values{},
			path:               "/api/v1/label/job/values",
			mockServerResponse: &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"status": "success", "data": []}`))},
			expectedStatus:     http.StatusBadRequest,
			expectedBody:       "",
			expectedError:      "metrics: invalid 'match[]' parameter 'up{invalid}': 1:3: parse error: invalid character inside braces",
			validateRequest: func(t *testing.T, req *http.Request, endpoint string) {
				assert.Equal(t, "/api/v1/label/job/values", req.URL.Path)
				assert.Contains(t, req.URL.Query()["match[]"][0], "up{invalid}")
				assert.Equal(t, "1672531200", req.URL.Query().Get("start"))
				assert.Equal(t, "1672534800", req.URL.Query().Get("end"))
			},
		},
		{
			name:        "SeriesWithNoMatcher",
			requestType: "series",
			urlParams: url.Values{
				"start": {"1672531200"},
				"end":   {"1672534800"},
			},
			formParams:         url.Values{},
			path:               "/api/v1/series",
			mockServerResponse: &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"status": "success", "data": []}`))},
			expectedStatus:     http.StatusOK,
			expectedBody:       `{"status": "success", "data": []}`,
			expectedError:      "",
			validateRequest: func(t *testing.T, req *http.Request, endpoint string) {
				assert.Equal(t, "/api/v1/series", req.URL.Path)
				assert.Contains(t, req.URL.Query()["match[]"][0], "account_id=\"test-account\"")
				assert.Contains(t, req.URL.Query()["match[]"][0], "tenant_id=\"test-tenant\"")
				assert.Equal(t, "1672531200", req.URL.Query().Get("start"))
				assert.Equal(t, "1672534800", req.URL.Query().Get("end"))
			},
		},
		{
			name:        "LabelsWithNoMatcher",
			requestType: "labels",
			urlParams:   url.Values{},
			formParams: url.Values{
				"start": {"1672531200"},
				"end":   {"1672534800"},
			},
			path:               "/api/v1/labels",
			mockServerResponse: &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"status": "success", "data": []}`))},
			expectedStatus:     http.StatusOK,
			expectedBody:       `{"status": "success", "data": []}`,
			expectedError:      "",
			validateRequest: func(t *testing.T, req *http.Request, endpoint string) {
				assert.Equal(t, "/api/v1/labels", req.URL.Path)
				assert.Contains(t, req.URL.Query()["match[]"][0], "account_id=\"test-account\"")
				assert.Contains(t, req.URL.Query()["match[]"][0], "tenant_id=\"test-tenant\"")
				assert.Equal(t, "1672531200", req.URL.Query().Get("start"))
				assert.Equal(t, "1672534800", req.URL.Query().Get("end"))
			},
		},
		{
			name:        "LabelValuesWithNoMatcher",
			requestType: "label_values",
			urlParams: url.Values{
				"start": {"1672531200"},
				"end":   {"1672534800"},
			},
			formParams:         url.Values{},
			path:               "/api/v1/label/job/values",
			mockServerResponse: &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"status": "success", "data": []}`))},
			expectedStatus:     http.StatusOK,
			expectedBody:       `{"status": "success", "data": []}`,
			expectedError:      "",
			validateRequest: func(t *testing.T, req *http.Request, endpoint string) {
				assert.Equal(t, "/api/v1/label/job/values", req.URL.Path)
				assert.Contains(t, req.URL.Query()["match[]"][0], "account_id=\"test-account\"")
				assert.Contains(t, req.URL.Query()["match[]"][0], "tenant_id=\"test-tenant\"")
				assert.Equal(t, "1672531200", req.URL.Query().Get("start"))
				assert.Equal(t, "1672534800", req.URL.Query().Get("end"))
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Combine URL and Form params, Form params take precedence
			requestParams := make(url.Values)
			for k, v := range tc.urlParams {
				requestParams[k] = v
			}
			for k, v := range tc.formParams {
				requestParams[k] = v
			}

			var testServer *httptest.Server
			if tc.mockServerResponse != nil { // Only setup server if we expect a call to it
				testServer = httptest.NewServer(http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
					if tc.validateRequest != nil {
						tc.validateRequest(t, req, testServer.URL) // Pass original testServer.URL for consistency if needed
					}
					// Ensure mockServerResponse.Body can be read multiple times if necessary, or clone it.
					// For this test structure, it's read once.
					res.Header().Set("Content-Type", "application/json") // Assuming JSON for Prometheus
					res.WriteHeader(tc.mockServerResponse.StatusCode)
					bodyBytes, _ := io.ReadAll(tc.mockServerResponse.Body)
					_, err := res.Write(bodyBytes)
					if err != nil {
						slog.Error("test mock server: unable to write response", "error", err)
					}
					// Reset Body for potential next read if the test case was designed for it (not typical here)
					tc.mockServerResponse.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
				}))
				config.Config.OtelMetricsQueryEndpoint = testServer.URL
			} else {
				// If no mockServerResponse, it means we expect an error before hitting the server.
				// Set a dummy endpoint or ensure it's not used.
				config.Config.OtelMetricsQueryEndpoint = "http://dummy-endpoint-not-called"
			}
			defer func() { testServer.Close() }()

			mockAgentDetail := security.Account{
				TenantId:  "test-tenant",
				AccountId: "test-account",
			}

			reader := prometheus.NewReader(config.Config.OtelMetricsQueryEndpoint)
			var resp metrics.MetricsResponse

			switch tc.requestType {
			case "query":
				params := metrics.QueryParams{Query: requestParams.Get("query"), Time: requestParams.Get("time"), Timeout: requestParams.Get("timeout")}
				resp = reader.Query(mockAgentDetail, logger, params)
			case "query_range":
				params := metrics.QueryRangeParams{Query: requestParams.Get("query"), Start: requestParams.Get("start"), End: requestParams.Get("end"), Step: requestParams.Get("step"), Timeout: requestParams.Get("timeout")}
				resp = reader.QueryRange(mockAgentDetail, logger, params)
			case "series":
				params := metrics.SeriesParams{Matchers: requestParams["match[]"], Start: requestParams.Get("start"), End: requestParams.Get("end"), Limit: requestParams.Get("limit")}
				resp = reader.Series(mockAgentDetail, logger, params)
			case "labels":
				params := metrics.LabelsParams{Matchers: requestParams["match[]"], Start: requestParams.Get("start"), End: requestParams.Get("end"), Limit: requestParams.Get("limit")}
				resp = reader.Labels(mockAgentDetail, logger, params)
			case "label_values":
				// Extract label name from path, e.g., /api/v1/label/job/values -> job
				parts := strings.Split(tc.path, "/")
				var labelName string
				if len(parts) >= 5 && parts[3] == "label" { // Ensure path structure
					labelName = parts[4]
				} else {
					t.Fatalf("Invalid path for label_values test case %s: %s", tc.name, tc.path)
				}
				params := metrics.LabelValuesParams{Matchers: requestParams["match[]"], Start: requestParams.Get("start"), End: requestParams.Get("end"), Limit: requestParams.Get("limit")}
				resp = reader.LabelValues(mockAgentDetail, logger, labelName, params)
			case "format_query":
				params := metrics.FormatQueryParams{Query: requestParams.Get("query")}
				resp = reader.FormatQuery(mockAgentDetail, logger, params)
			default:
				t.Fatalf("Unknown request type in test case: %s", tc.requestType)
			}

			if tc.expectedError != "" {
				assert.NotNil(t, resp.Error, "Expected an error but got none for test: %s", tc.name)
				if resp.Error != nil {
					assert.Equal(t, tc.expectedError, resp.Error.Error())
				}
			} else {
				assert.NoError(t, resp.Error, "Expected no error but got one for test: %s, error: %v", tc.name, resp.Error)
			}

			if tc.expectedStatus != -1 {
				assert.Equal(t, tc.expectedStatus, resp.StatusCode)
			}

			if tc.expectedBody != "" {
				assert.JSONEq(t, tc.expectedBody, string(resp.Body))
			}
			if tc.expectedStatus == http.StatusOK {
				assert.Equal(t, "application/json", resp.ContentType) // Assuming Prometheus always returns JSON
			}
		})
	}
}

type MockHTTPClient struct {
	DoFunc func(*http.Request) (*http.Response, error)
}

func (m *MockHTTPClient) RoundTrip(req *http.Request) (*http.Response, error) {
	if m.DoFunc != nil {
		return m.DoFunc(req)
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader("")),
	}, nil
}
