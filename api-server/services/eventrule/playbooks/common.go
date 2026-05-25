package playbooks

func InsightFromRelayResponse(relayResponse map[string]any) []PlaybookActionResponseInsight {
	insight := []PlaybookActionResponseInsight{}

	// Extract insights from relay response first
	if relayResponse["insight"] != nil {
		if insight, ok := relayResponse["insight"].([]any); ok {
			for _, i := range insight {
				if i == nil {
					continue
				}
				if insightMessage, ok := i.(map[string]any); ok {
					if insightMessage == nil || insightMessage["message"] == nil {
						continue
					}
					insight = append(insight, PlaybookActionResponseInsight{
						Message:  insightMessage["message"].(string),
						Severity: insightMessage["severity"].(string),
					})
				}
			}
		}
	}
	return insight
}

type NamedQuery struct {
	Key   string `json:"key"`
	Query string `json:"query"`
}

type PrometheusActionResponse struct {
	Metadata       map[string]any                  `json:"metadata"`
	Data           map[string]any                  `json:"data"`
	AdditionalInfo map[string]any                  `json:"additional_info"`
	Insight        []PlaybookActionResponseInsight `json:"insight"`
}

// Structs for prometheus data parsing
type PrometheusQueryResult struct {
	SeriesListResult []PrometheusSeries `json:"series_list_result,omitempty"`
}

type PrometheusSeries struct {
	Metric     map[string]any `json:"metric"`
	Timestamps []float64      `json:"timestamps"`
	Values     []string       `json:"values"`
}

func (m PrometheusActionResponse) ExtractLabels() map[string]any {
	labels := map[string]any{}
	if len(m.Data) > 0 && m.Data["series_list_result"] != nil {
		seriesList := m.Data["series_list_result"].([]any)
		if len(seriesList) > 0 {
			// Extract all series into an array
			allSeries := make([]map[string]any, 0, len(seriesList))
			for _, seriesItem := range seriesList {
				series := seriesItem.(map[string]any)
				if series["metric"] != nil {
					allSeries = append(allSeries, series["metric"].(map[string]any))
				}
			}

			if len(allSeries) > 0 {
				// Backward compatibility: set first series' labels at top level
				for k, v := range allSeries[0] {
					labels[k] = v
				}

				// Store all series in a special key for multi-series access
				labels["_series"] = allSeries
			}
		}
	}
	return labels
}

func (m PrometheusActionResponse) GetFormatName() string {
	return "prometheus"
}

func (m PrometheusActionResponse) GetData() any {
	return m.Data
}

func (m PrometheusActionResponse) GetAdditionalInfo() map[string]any {
	return m.AdditionalInfo
}

func (m PrometheusActionResponse) GetInsights() []PlaybookActionResponseInsight {
	return m.Insight
}

// LogsActionResponse extends PlaybookActionResponseJson to support label extraction
type LogsActionResponse struct {
	Data            string                          `json:"data"`
	AdditionalInfo  map[string]any                  `json:"additional_info"`
	Insight         []PlaybookActionResponseInsight `json:"insight"`
	Metadata        map[string]any                  `json:"metadata"`
	ExtractedLabels map[string]any                  `json:"-"` // Don't marshal this field
}

// ExtractLabels implements PlaybookActionResponseLabelExtractor interface
func (r *LogsActionResponse) ExtractLabels() map[string]any {
	return r.ExtractedLabels
}

// Implement PlaybookActionResponse interface methods directly
func (r *LogsActionResponse) GetAdditionalInfo() map[string]any {
	return r.AdditionalInfo
}

func (r *LogsActionResponse) GetInsights() []PlaybookActionResponseInsight {
	return r.Insight
}

func (r *LogsActionResponse) GetFormatName() string {
	return "json"
}

func (r *LogsActionResponse) GetData() any {
	return r.Data
}
