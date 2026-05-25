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

// ShapeEvidenceForFrontend coerces the marshalled evidence map into the
// wire shape the investigate-page React cards expect.
//
// Legacy Python (Robusta) sink emitted table evidence as
// `{type:"table", data:{table_name, rows, headers, column_renderers}}`.
// The Go-side `PlaybookActionResponseTable` flattens Rows / Headers to
// the top level via JSON tags, so `MarshalStructToMap` produces
// `{type:"table", rows, headers}` — no `data`, no `table_name`. Cards
// like TracesCard / ServiceMapCard then crash in
// `evidenceData.filter(i => i.type==='table' && i.data.table_name…)`
// because `i.data` is undefined, killing the whole investigate render.
//
// The function is idempotent (no-op when `data` already exists or when
// `type` is not "table"); safe to call on every persisted evidence map.
func ShapeEvidenceForFrontend(structResponse map[string]any) {
	if structResponse == nil {
		return
	}
	if structResponse["type"] != "table" {
		return
	}
	if _, alreadyWrapped := structResponse["data"]; alreadyWrapped {
		return
	}
	data := map[string]any{}
	if rows, ok := structResponse["rows"]; ok {
		data["rows"] = rows
		delete(structResponse, "rows")
	}
	if headers, ok := structResponse["headers"]; ok {
		data["headers"] = headers
		delete(structResponse, "headers")
	}
	if ai, ok := structResponse["additional_info"].(map[string]any); ok {
		if title, ok := ai["title"].(string); ok {
			data["table_name"] = title
		}
	}
	if _, ok := data["table_name"]; !ok {
		data["table_name"] = ""
	}
	data["column_renderers"] = map[string]any{}
	structResponse["data"] = data
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
