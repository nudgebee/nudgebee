package observability

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"nudgebee/services/common"
	"nudgebee/services/integrations"
	"nudgebee/services/security"
	"strconv"
	"time"
)

// OpenObserveMetricSource implements MetricSource interface for OpenObserve
type OpenObserveMetricSource struct{}

func openObserveMetricQueryRangeParams(req FetchMetricsRequest) (start, end string, step int) {
	durationSecs := (req.EndTime - req.StartTime) / 1000
	step = req.StepInterval
	if step <= 0 {
		step = int(durationSecs / 100)
		if step < 1 {
			step = 1
		}
	}

	return strconv.FormatInt(req.StartTime/1000, 10), strconv.FormatInt(req.EndTime/1000, 10), step
}

func (s *OpenObserveMetricSource) GetSupportedOperators() []string {
	return []string{"_eq", "_neq", "_regex"}
}

func (s *OpenObserveMetricSource) GetQuery(_ *security.RequestContext, req FetchMetricsRequest) (string, error) {
	for _, rawQuery := range req.Queries {
		return injectPromQLMatchers(rawQuery, req.LabelMatchers, req.Labels)
	}
	return "", nil
}

func (s *OpenObserveMetricSource) FetchMetricsQuery(ctx *security.RequestContext, req FetchMetricsRequest) (OutputMetricQuery, error) {
	cfg, err := integrations.GetOpenObserveConfigs(ctx, req.AccountId)
	if err != nil {
		return OutputMetricQuery{}, fmt.Errorf("failed to get OpenObserve configs: %w", err)
	}

	results := OutputMetricQuery{Results: []QueryResult{}}

	start, end, step := openObserveMetricQueryRangeParams(req)

	for queryKey, rawQuery := range req.Queries {
		query, err := injectPromQLMatchers(rawQuery, req.LabelMatchers, req.Labels)
		if err != nil {
			errorMsg := err.Error()
			results.Results = append(results.Results, QueryResult{QueryKey: queryKey, Error: &errorMsg})
			continue
		}

		if item, ok := req.QueryItems[queryKey]; ok && item.AggregateOperator != "" {
			query, err = wrapPromQLAggregator(query, item.AggregateOperator)
			if err != nil {
				errorMsg := err.Error()
				results.Results = append(results.Results, QueryResult{QueryKey: queryKey, Error: &errorMsg})
				continue
			}
		}

		endpoint := fmt.Sprintf("%s/api/%s/prometheus/api/v1/query_range", cfg.URL, cfg.OrgID)

		form := neturl.Values{}
		form.Add("query", query)
		form.Add("start", start)
		form.Add("end", end)
		form.Add("step", strconv.Itoa(step))

		authHeader := openObserveAuthHeader(cfg.Username, cfg.Password)

		resp, err := common.HttpPost(endpoint,
			common.HttpWithHeaders(map[string]string{
				"Authorization": authHeader,
				"Content-Type":  "application/x-www-form-urlencoded",
			}),
			common.HttpWithBody(io.NopCloser(bytes.NewReader([]byte(form.Encode())))),
			common.HttpWithTimeout(30*time.Second),
		)

		if err != nil {
			errorMsg := err.Error()
			results.Results = append(results.Results, QueryResult{QueryKey: queryKey, Error: &errorMsg})
			continue
		}

		bodyBytes, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			errorMsg := fmt.Sprintf("OpenObserve query failed with status %d: %s", resp.StatusCode, string(bodyBytes))
			results.Results = append(results.Results, QueryResult{QueryKey: queryKey, Error: &errorMsg})
			continue
		}

		var payload struct {
			Data struct {
				Result []struct {
					Metric map[string]string `json:"metric"`
					Values [][]any           `json:"values"`
				} `json:"result"`
			} `json:"data"`
		}

		if err := json.Unmarshal(bodyBytes, &payload); err != nil {
			errorMsg := fmt.Sprintf("failed to parse response: %v", err)
			results.Results = append(results.Results, QueryResult{QueryKey: queryKey, Error: &errorMsg})
			continue
		}

		var payloadResults []Result
		for _, r := range payload.Data.Result {
			timestamps := make([]int64, 0, len(r.Values))
			values := make([]float64, 0, len(r.Values))
			for _, v := range r.Values {
				if len(v) != 2 {
					continue
				}
				ts, ok := v[0].(float64)
				if !ok {
					continue
				}
				valStr, ok := v[1].(string)
				if !ok {
					continue
				}
				valFloat, err := strconv.ParseFloat(valStr, 64)
				if err != nil {
					continue
				}
				timestamps = append(timestamps, int64(ts)*1000)
				values = append(values, valFloat)
			}
			payloadResults = append(payloadResults, Result{
				Metric:     r.Metric,
				Timestamps: timestamps,
				Values:     values,
			})
		}

		results.Results = append(results.Results, QueryResult{
			QueryKey: queryKey,
			Query:    query,
			Payload:  payloadResults,
		})
	}

	return results, nil
}

func (s *OpenObserveMetricSource) FetchMetricList(ctx *security.RequestContext, req FetchMetricsListRequest) ([]OutputMetrics, error) {
	cfg, err := integrations.GetOpenObserveConfigs(ctx, req.AccountId)
	if err != nil {
		return nil, fmt.Errorf("failed to get OpenObserve configs: %w", err)
	}

	endpoint := fmt.Sprintf("%s/api/%s/prometheus/api/v1/label/__name__/values", cfg.URL, cfg.OrgID)

	if req.Metric != "" {
		endpoint += "?match[]=" + neturl.QueryEscape(fmt.Sprintf("{__name__=~.*%s.*}", req.Metric))
	}

	authHeader := openObserveAuthHeader(cfg.Username, cfg.Password)

	resp, err := common.HttpGet(endpoint,
		common.HttpWithHeaders(map[string]string{
			"Authorization": authHeader,
		}),
		common.HttpWithTimeout(15*time.Second),
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OpenObserve API returned HTTP %d", resp.StatusCode)
	}

	var payload struct {
		Data []string `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}

	var output []OutputMetrics
	for _, name := range payload.Data {
		output = append(output, OutputMetrics{Metric: name, Attributes: map[string]any{}})
	}

	return output, nil
}

func (s *OpenObserveMetricSource) FetchMetricLabelValues(ctx *security.RequestContext, req FetchMetricsLabelValueRequest) ([]OutputMetricsLabelValues, error) {
	cfg, err := integrations.GetOpenObserveConfigs(ctx, req.AccountId)
	if err != nil {
		return nil, fmt.Errorf("failed to get OpenObserve configs: %w", err)
	}

	endpoint := fmt.Sprintf("%s/api/%s/prometheus/api/v1/label/%s/values", cfg.URL, cfg.OrgID, req.Label)

	authHeader := openObserveAuthHeader(cfg.Username, cfg.Password)

	resp, err := common.HttpGet(endpoint,
		common.HttpWithHeaders(map[string]string{
			"Authorization": authHeader,
		}),
		common.HttpWithTimeout(15*time.Second),
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OpenObserve API returned HTTP %d", resp.StatusCode)
	}

	var payload struct {
		Data []string `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}

	var output []OutputMetricsLabelValues
	for _, name := range payload.Data {
		output = append(output, OutputMetricsLabelValues{Value: name, Attributes: map[string]any{}})
	}

	return output, nil
}

func (s *OpenObserveMetricSource) FetchMetricsLabels(ctx *security.RequestContext, req FetchMetricLabelsRequest) ([]OutputMetricLabels, error) {
	cfg, err := integrations.GetOpenObserveConfigs(ctx, req.AccountId)
	if err != nil {
		return nil, fmt.Errorf("failed to get OpenObserve configs: %w", err)
	}

	endpoint := fmt.Sprintf("%s/api/%s/prometheus/api/v1/labels", cfg.URL, cfg.OrgID)
	if req.MetricName != "" {
		endpoint += "?match[]=" + neturl.QueryEscape(fmt.Sprintf("{__name__=\"%s\"}", req.MetricName))
	}

	authHeader := openObserveAuthHeader(cfg.Username, cfg.Password)

	resp, err := common.HttpGet(endpoint,
		common.HttpWithHeaders(map[string]string{
			"Authorization": authHeader,
		}),
		common.HttpWithTimeout(15*time.Second),
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OpenObserve API returned HTTP %d", resp.StatusCode)
	}

	var payload struct {
		Data []string `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}

	var output []OutputMetricLabels
	for _, name := range payload.Data {
		output = append(output, OutputMetricLabels{Label: name, Attributes: map[string]any{}})
	}

	return output, nil
}
