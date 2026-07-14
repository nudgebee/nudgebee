package utils

import (
	"context"
	"errors"
	"log/slog"
	"nudgebee/llm/common"
	"nudgebee/llm/relay"
	"strings"
)

var labelData = map[string]string{
	"kubernetes.namespace_name": "Represents the namespace of Kubernetes resources",
	"kubernetes.pod_id":         "Unique identifier of a Kubernetes pod",
	"kubernetes.pod_name":       "Represents name of a Kubernetes pod",
	// "kubernetes.labels.app":                                "Application name assigned to Kubernetes resources",
	"kubernetes.labels.job-name": "Represents the job name label assigned to Kubernetes resources",
	// "kubernetes.labels.k8s-app":                            "Application name assigned to Kubernetes resources",
	"kubernetes.labels.app_kubernetes_io/component":        "Specifies the role of the component within the application (e.g., frontend, backend)",
	"kubernetes.labels.app_kubernetes_io/instance":         "Unique name identifying the instance of the application.",
	"kubernetes.labels.app_kubernetes_io/managed-by":       "Tool or operator managing the application (e.g., helm, operator).",
	"kubernetes.labels.app_kubernetes_io/name":             "The name of the application (e.g., nginx).",
	"kubernetes.labels.app_kubernetes_io/part-of":          "Identifies the larger application of which this component is a part.",
	"kubernetes.labels.app_kubernetes_io/version":          "Specifies the current version of the application.",
	"kubernetes.labels.apps_kubernetes_io/pod-index":       "Index of a pod in a StatefulSet or other ordered list of pods.",
	"kubernetes.labels.batch_kubernetes_io/controller-uid": "Unique identifier of the job's controller.",
	"kubernetes.labels.batch_kubernetes_io/job-name":       "The name of the job this pod is associated with.",
	"@timestamp": "Time the log entry was recorded same as timestamp",
	// "level":      "Indicates the severity level of the log (e.g., 'INFO', 'ERROR')",
	"levelname": "Another representation of log severity level, similar to 'level'",
	"log":       "Contains the main log entry content",
	// "msg":                       "Contains the main log entry content",
	// "message":                   "Contains the main log entry content",
	"time":                              "Time the log entry was recorded",
	"timestamp":                         "Precise timestamp of the log entry same as time",
	"namespace":                         "Kubernetes namespace where the log originated",
	"node":                              "Node in the cluster where the log was generated",
	"pod":                               "Kubernetes pod where the log originated",
	"kubernetes.container_name":         "Container name where the log originated",
	"container_name":                    "Container name where the log originated",
	"app_kubernetes_io/name":            "The name of the application (e.g., nginx).",
	"kubernetes.namespace_name.keyword": "Represents the namespace of Kubernetes resources",
	"kubernetes.pod_id.keyword":         "Unique identifier of a Kubernetes pod",
	"kubernetes.pod_name.keyword":       "Represents name of a Kubernetes pod",
	// "kubernetes.labels.app.keyword":                                "Application name assigned to Kubernetes resources",
	"kubernetes.labels.job-name.keyword": "Represents the job name label assigned to Kubernetes resources",
	// "kubernetes.labels.k8s-app.keyword":                            "Application name assigned to Kubernetes resources",
	"kubernetes.labels.app_kubernetes_io/component.keyword":        "Specifies the role of the component within the application (e.g., frontend, backend)",
	"kubernetes.labels.app_kubernetes_io/instance.keyword":         "Unique name identifying the instance of the application.",
	"kubernetes.labels.app_kubernetes_io/managed-by.keyword":       "Tool or operator managing the application (e.g., helm, operator).",
	"kubernetes.labels.app_kubernetes_io/name.keyword":             "The name of the application (e.g., nginx).",
	"kubernetes.labels.app_kubernetes_io/part-of.keyword":          "Identifies the larger application of which this component is a part.",
	"kubernetes.labels.app_kubernetes_io/version.keyword":          "Specifies the current version of the application.",
	"kubernetes.labels.apps_kubernetes_io/pod-index.keyword":       "Index of a pod in a StatefulSet or other ordered list of pods.",
	"kubernetes.labels.batch_kubernetes_io/controller-uid.keyword": "Unique identifier of the job's controller.",
	"kubernetes.labels.batch_kubernetes_io/job-name.keyword":       "The name of the job this pod is associated with.",
	"@timestamp.keyword": "Time the log entry was recorded same as timestamp",
	// "level.keyword":      "Indicates the severity level of the log (e.g., 'INFO', 'ERROR')",
	"levelname.keyword": "Another representation of log severity level, similar to 'level'",
	"log.keyword":       "Contains the main log entry content",
	// "msg.keyword":                       "Contains the main log entry content",
	// "message.keyword":                   "Contains the main log entry content",
	"time.keyword":                      "Time the log entry was recorded",
	"timestamp.keyword":                 "Precise timestamp of the log entry same as time",
	"namespace.keyword":                 "Kubernetes namespace where the log originated",
	"node.keyword":                      "Node in the cluster where the log was generated",
	"pod.keyword":                       "Kubernetes pod where the log originated",
	"kubernetes.container_name.keyword": "Container name where the log originated",
	"container_name.keyword":            "Container name where the log originated",
	"app_kubernetes_io/name.keyword":    "The name of the application (e.g., nginx).",
}

func GetLabelsList(index string, accountId string) (map[string]map[string]string, error) {
	actionParam := relay.ActionExecuteBody{
		AccountID:  accountId,
		ActionName: "query_es_index_field",
		ActionParams: map[string]any{
			"index": index,
		},
	}
	// GetLabelsList is currently orphaned (no in-repo callers); it has no
	// request-scoped context to thread, so it starts a fresh trace.
	response, err := relay.Execute(context.Background(), actionParam)
	if err != nil {
		return nil, err
	}
	data := response["data"].(map[string]any)
	data1, ok := data["data"]
	success, _ := data["success"].(bool)
	if !ok {
		return nil, errors.New("unable to execute ES query")
	}

	if !success {
		return nil, err
	}
	type FieldType struct {
		Type         string `json:"type"`
		Searchable   bool   `json:"searchable"`
		Aggregatable bool   `json:"aggregatable"`
	}

	type Field struct {
		Text    *FieldType `json:"text,omitempty"`
		Boolean *FieldType `json:"boolean,omitempty"`
		Keyword *FieldType `json:"keyword,omitempty"`
		Long    *FieldType `json:"long,omitempty"`
		Date    *FieldType `json:"date,omitempty"`
		Float   *FieldType `json:"float,omitempty"`
		Object  *FieldType `json:"object,omitempty"`
	}

	type indexData struct {
		Indices []string
		Fields  map[string]Field `json:"fields"`
	}

	if !ok {
		return nil, errors.New("data1 is not a map")
	}
	var indexDataInstance indexData
	err = common.UnmarshalJson([]byte(data1.(string)), &indexDataInstance)
	if err != nil {
		return nil, err
	}

	indexT := make(map[string]map[string]string)
	for fieldName, field := range indexDataInstance.Fields {
		if _, exists := labelData[fieldName]; !exists {
			continue
		}
		help := labelData[fieldName]
		if field.Text != nil {
			indexT[fieldName] = map[string]string{"type": field.Text.Type, "help": help}
		} else if field.Boolean != nil {
			indexT[fieldName] = map[string]string{"type": field.Boolean.Type, "help": help}
		} else if field.Keyword != nil {
			indexT[fieldName] = map[string]string{"type": field.Keyword.Type, "help": help}
		} else if field.Long != nil {
			indexT[fieldName] = map[string]string{"type": field.Long.Type, "help": help}
		} else if field.Date != nil {
			indexT[fieldName] = map[string]string{"type": field.Date.Type, "help": help}
		} else if field.Float != nil {
			indexT[fieldName] = map[string]string{"type": field.Float.Type, "help": help}
		} else if field.Object != nil {
			indexT[fieldName] = map[string]string{"type": field.Object.Type, "help": help}
		}
	}

	return indexT, nil
}

// ESIndexConfig holds the default index and optional named indices for an account.
type ESIndexConfig struct {
	DefaultIndex string            `json:"default_index"`
	Indices      map[string]string `json:"indices,omitempty"`
}

func GetESAccountIndex(accountId string) string {
	cfg := GetESAccountIndexConfig(accountId, "logs")
	return cfg.DefaultIndex
}

// extractIndexFromConfig searches data (which can be a map[string]any or a []any array of {"name": "...", "value": "..."})
// for any matching field name in fieldNames and returns the first non-empty, non-wildcard index pattern found.
func extractIndexFromConfig(data any, fieldNames []string) string {
	if data == nil {
		return ""
	}

	// 1. If data is a map[string]any:
	if m, ok := data.(map[string]any); ok {
		for _, fn := range fieldNames {
			if val, ok := m[fn]; ok && val != nil {
				if s, ok := val.(string); ok && s != "" && s != "*" {
					return s
				}
			}
		}
	}

	// 2. If data is a []any array (e.g. [{"name": "metrics_index", "value": "metricbeat*"}, ...]):
	if arr, ok := data.([]any); ok {
		for _, item := range arr {
			if m, ok := item.(map[string]any); ok {
				name, _ := m["name"].(string)
				value, _ := m["value"].(string)
				for _, fn := range fieldNames {
					if strings.EqualFold(name, fn) && value != "" && value != "*" {
						return value
					}
				}
			}
		}
	}

	return ""
}

func extractIndicesMapFromConfig(data any) map[string]string {
	if data == nil {
		return nil
	}
	result := make(map[string]string)
	if m, ok := data.(map[string]any); ok {
		if indices, ok := m["indices"]; ok && indices != nil {
			if indicesMap, ok := indices.(map[string]any); ok {
				for name, pattern := range indicesMap {
					if s, ok := pattern.(string); ok && s != "" {
						result[name] = s
					}
				}
			}
		}
	}
	if len(result) > 0 {
		return result
	}
	return nil
}

// GetESAccountIndexConfig returns the full index configuration for an account
// for the given providerType ("logs", "metrics", "traces"), checking provider-specific config keys first.
func GetESAccountIndexConfig(accountId string, providerType string) ESIndexConfig {
	cfg := ESIndexConfig{DefaultIndex: "*"}
	if strings.EqualFold(providerType, "metrics") {
		cfg.DefaultIndex = "metrics-*"
	} else if strings.EqualFold(providerType, "traces") {
		cfg.DefaultIndex = "traces-*"
	}

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		slog.Error("GetESAccountIndexConfig: unable to fetch dbms", "error", err)
		return cfg
	}
	rows, err := dbms.Db.Queryx("select connection_status::text from agent where cloud_account_id = $1 order by created_at desc", accountId)
	if err != nil {
		slog.Error("GetESAccountIndexConfig: unable to query agent", "error", err)
		return cfg
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Error("GetESAccountIndexConfig: failed to close rows", "error", err)
		}
	}()

	fieldNames := []string{"log_index", "logs_index", "default_index"}
	configKeys := []string{"log_provider_config"}
	switch strings.ToLower(providerType) {
	case "metrics":
		fieldNames = []string{"metrics_index", "metric_index", "metrics_default_index", "default_index"}
		configKeys = []string{"metrics_provider_config", "metric_provider_config", "log_provider_config"}
	case "traces":
		fieldNames = []string{"trace_index", "traces_index", "trace_default_index", "default_index"}
		configKeys = []string{"traces_provider_config", "trace_provider_config", "log_provider_config"}
	}

	for rows.Next() {
		var connectionStatusString *string
		err := rows.Scan(&connectionStatusString)
		if err != nil {
			slog.Error("GetESAccountIndexConfig: unable to scan rows", "error", err)
			break
		}
		if connectionStatusString == nil || *connectionStatusString == "" {
			continue
		}

		var rawData any
		err = common.UnmarshalJson([]byte(*connectionStatusString), &rawData)
		if err != nil {
			slog.Error("GetESAccountIndexConfig: unable to unmarshal connection_status", "error", err)
			continue
		}

		// 1. Try top-level connection_status (handles both map and array format)
		if idx := extractIndexFromConfig(rawData, fieldNames); idx != "" {
			cfg.DefaultIndex = idx
			if idxMap := extractIndicesMapFromConfig(rawData); idxMap != nil {
				cfg.Indices = idxMap
			}
			return cfg
		}

		// 2. Try nested config keys if rawData is a map
		if connectionStatus, ok := rawData.(map[string]any); ok {
			for _, key := range configKeys {
				if subConfig, ok := connectionStatus[key]; ok && subConfig != nil {
					if idx := extractIndexFromConfig(subConfig, fieldNames); idx != "" {
						cfg.DefaultIndex = idx
						if idxMap := extractIndicesMapFromConfig(subConfig); idxMap != nil {
							cfg.Indices = idxMap
						}
						return cfg
					}
				}
			}
		}
	}
	return cfg
}
