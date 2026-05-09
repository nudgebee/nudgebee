package playbooks

import (
	"nudgebee/services/common"
)

type PlaybookActionResponseMarkdown struct {
	Text           string                          `json:"text"`
	AdditionalInfo map[string]any                  `json:"additional_info"`
	Insight        []PlaybookActionResponseInsight `json:"insight"`
}

func (m PlaybookActionResponseMarkdown) GetFormatName() string {
	return "markdown"
}

func (m PlaybookActionResponseMarkdown) GetData() any {
	return m.Text
}

func (m PlaybookActionResponseMarkdown) GetAdditionalInfo() map[string]any {
	return m.AdditionalInfo
}

func (m PlaybookActionResponseMarkdown) GetInsights() []PlaybookActionResponseInsight {
	return m.Insight
}

type PlaybookActionResponseJson struct {
	Data           string                          `json:"data"`
	AdditionalInfo map[string]any                  `json:"additional_info"`
	Insight        []PlaybookActionResponseInsight `json:"insight"`
	Metadata       map[string]any                  `json:"metadata"`
	Labels         map[string]any                  `json:"labels"`
	Format         string                          `json:"format"`
}

func (m PlaybookActionResponseJson) GetFormatName() string {
	return m.Format
}

func (m PlaybookActionResponseJson) GetData() any {
	return m.Data
}

func (m PlaybookActionResponseJson) GetAdditionalInfo() map[string]any {
	return m.AdditionalInfo
}

func (m PlaybookActionResponseJson) GetInsights() []PlaybookActionResponseInsight {
	return m.Insight
}

func (m PlaybookActionResponseJson) ExtractLabels() map[string]any {
	return m.Labels
}

func NewPlaybookActionResponseJson(data any, additionalInfo map[string]any, insight []PlaybookActionResponseInsight, metadata map[string]any) PlaybookActionResponseJson {
	response := PlaybookActionResponseJson{
		Data:           "",
		AdditionalInfo: additionalInfo,
		Insight:        insight,
		Metadata:       metadata,
		Format:         "json",
	}
	switch d1 := data.(type) {
	case string:
		response.Data = d1
	default:
		bytesData, _ := common.MarshalJson(data)
		response.Data = string(bytesData)
	}

	return response
}

func NewPlaybookActionResponseJsonWithLabels(data any, additionalInfo map[string]any, insight []PlaybookActionResponseInsight, metadata map[string]any, labels map[string]any) PlaybookActionResponseJson {
	response := PlaybookActionResponseJson{
		Data:           "",
		AdditionalInfo: additionalInfo,
		Insight:        insight,
		Metadata:       metadata,
		Labels:         labels,
		Format:         "json",
	}
	switch d1 := data.(type) {
	case string:
		response.Data = d1
	default:
		bytesData, _ := common.MarshalJson(data)
		response.Data = string(bytesData)
	}

	return response
}

type PlaybookActionResponseTable struct {
	Rows           [][]any                         `json:"rows"`
	Headers        []string                        `json:"headers"`
	AdditionalInfo map[string]any                  `json:"additional_info"`
	Insight        []PlaybookActionResponseInsight `json:"insight"`
	Labels         map[string]any                  `json:"labels"`
}

func (m PlaybookActionResponseTable) ExtractLabels() map[string]any {
	return m.Labels
}

func (m PlaybookActionResponseTable) GetFormatName() string {
	return "table"
}

func (m PlaybookActionResponseTable) GetData() any {
	return map[string]any{
		"rows":    m.Rows,
		"headers": m.Headers,
	}
}

func (m PlaybookActionResponseTable) GetAdditionalInfo() map[string]any {
	return m.AdditionalInfo
}

func (m PlaybookActionResponseTable) GetInsights() []PlaybookActionResponseInsight {
	return m.Insight
}

type PlaybookActionResponseFile struct {
	Type           string                          `json:"type"`
	Filename       string                          `json:"filename"`
	Data           string                          `json:"data"`
	AdditionalInfo map[string]any                  `json:"additional_info"`
	Insight        []PlaybookActionResponseInsight `json:"insight"`
	Labels         map[string]any                  `json:"labels"`
}

func (m PlaybookActionResponseFile) ExtractLabels() map[string]any {
	return m.Labels
}

func (m PlaybookActionResponseFile) GetFormatName() string {
	return "file"
}

func (m PlaybookActionResponseFile) GetData() any {
	return m.Data
}

func (m PlaybookActionResponseFile) GetAdditionalInfo() map[string]any {
	return m.AdditionalInfo
}

func (m PlaybookActionResponseFile) GetInsights() []PlaybookActionResponseInsight {
	return m.Insight
}
