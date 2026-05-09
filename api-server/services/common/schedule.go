package common

import (
	"fmt"
	"io"
	"log/slog"
	"nudgebee/services/config"
	"strings"
)

type ScheduledJobs struct {
	CronTriggers []ScheduledJob `json:"cron_triggers"`
}

type ScheduledJob struct {
	Headers           []map[string]any      `json:"headers"`
	IncludeInMetadata bool                  `json:"include_in_metadata"`
	Name              string                `json:"name"`
	Payload           map[string]any        `json:"payload"`
	RetryConf         ScheduledJobRetryConf `json:"retry_conf"`
	Schedule          string                `json:"schedule"`
	Webhook           string                `json:"webhook"`
}

type ScheduledJobRetryConf struct {
	NumRetries           int `json:"num_retries"`
	RetryIntervalSeconds int `json:"retry_interval_seconds"`
	TimeoutSeconds       int `json:"timeout_seconds"`
	ToleranceSeconds     int `json:"tolerance_seconds"`
}

type ScheduledJobEvents struct {
	Events []ScheduledJobEvent `json:"events"`
}

type ScheduledJobEvent struct {
	CreatedAt     string `json:"created_at"`
	Id            string `json:"id"`
	NextRetryAt   string `json:"next_retry_at"`
	ScheduledTime string `json:"scheduled_time"`
	Status        string `json:"status"`
	Tries         int    `json:"tries"`
	TriggerName   string `json:"trigger_name"`
}

func ScheduledJobsList() []ScheduledJobs {
	resp, err := HttpPost(config.Config.HasuraMetadataEndpoint, HttpWithHeaders(map[string]string{
		"Content-Type": "application/json",
		"Accept":       "application/json",
	}), HttpWithJsonBody(map[string]any{
		"type": "get_cron_triggers",
		"args": map[string]any{},
	}))

	if err != nil {
		return []ScheduledJobs{}
	}
	defer func() {
		err := resp.Body.Close()
		if err != nil {
			slog.Error("Error closing response body", "error", err)
		}
	}()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return []ScheduledJobs{}
	}

	if resp.StatusCode != 200 {
		return []ScheduledJobs{}
	}

	var response []ScheduledJobs
	err = UnmarshalJson(body, &response)
	if err != nil {
		return []ScheduledJobs{}
	}

	return response
}

func ScheduleJobCreate(scheduleName string, callbackEndpoint string, cron string, payload map[string]any, callbackEndpointType string) (map[string]any, error) {
	if strings.Index(callbackEndpoint, "/") == 0 {
		callbackEndpoint = callbackEndpoint[:len(callbackEndpoint)-1]
	}
	if callbackEndpointType == "" {
		callbackEndpointType = "service"
	}

	if callbackEndpointType == "action" || callbackEndpointType == "service" {
		callbackEndpoint = fmt.Sprintf("%s/%s", config.Config.ServiceEndpoint, callbackEndpoint)
	}

	query := map[string]any{
		"type": "create_cron_trigger",
		"args": map[string]any{
			"webhook":             callbackEndpoint,
			"schedule":            cron,
			"payload":             payload,
			"include_in_metadata": false,
			"name":                scheduleName,
			"comment":             scheduleName,
			"retry_conf": map[string]any{
				"num_retries":            0,
				"retry_interval_seconds": 10,
				"timeout_seconds":        60,
			},
			"headers": []any{
				map[string]any{
					"name":           config.Config.ServiceApiServerTokenHeader,
					"value_from_env": "ACTION_API_SERVER_TOKEN",
				},
			},
		},
	}

	resp, err := HttpPost(config.Config.HasuraMetadataEndpoint, HttpWithHeaders(map[string]string{
		"Content-Type": "application/json",
		"Accept":       "application/json",
	}), HttpWithJsonBody(query))

	if err != nil {
		return map[string]any{}, err
	}
	defer func() {
		err := resp.Body.Close()
		if err != nil {
			slog.Error("Error closing response body", "error", err)
		}
	}()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return map[string]any{}, err
	}

	if resp.StatusCode != 200 {
		errorResponse := map[string]any{}
		err := UnmarshalJson(body, &errorResponse)
		if err != nil {
			return map[string]any{}, fmt.Errorf("error executing request: %s", resp.Status)
		}
		if errorResponse["message"] != nil {
			return map[string]any{}, fmt.Errorf("error executing request: %s", errorResponse["message"])
		}
		return map[string]any{}, fmt.Errorf("error executing request: %s", resp.Status)
	}

	var response map[string]any
	err = UnmarshalJson(body, &response)
	if err != nil {
		return map[string]any{}, err
	}

	return response, nil
}

func ScheduleJobDelete(scheduleName string) (map[string]any, error) {
	resp, err := HttpPost(config.Config.HasuraMetadataEndpoint, HttpWithHeaders(map[string]string{
		"Content-Type": "application/json",
		"Accept":       "application/json",
	}), HttpWithJsonBody(map[string]any{
		"type": "delete_cron_trigger",
		"args": map[string]any{
			"name": scheduleName,
		},
	}))

	if err != nil {
		return map[string]any{}, err
	}
	defer func() {
		err := resp.Body.Close()
		if err != nil {
			slog.Error("Error closing response body", "error", err)
		}
	}()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return map[string]any{}, err
	}

	if resp.StatusCode != 200 {
		errorResponse := map[string]any{}
		err := UnmarshalJson(body, &errorResponse)
		if err != nil {
			return map[string]any{}, fmt.Errorf("error executing request: %s", resp.Status)
		}
		if errorResponse["message"] != nil {
			return map[string]any{}, fmt.Errorf("error executing request: %s", errorResponse["message"])
		}
		return map[string]any{}, fmt.Errorf("error executing request: %s", resp.Status)
	}

	var response map[string]any
	err = UnmarshalJson(body, &response)
	if err != nil {
		return map[string]any{}, err
	}

	return response, nil
}

func ScheduledJobInstanceList(scheduleName string, status []string, limit int, offset int, rowCount int) (ScheduledJobEvents, error) {

	resp, err := HttpPost(config.Config.HasuraMetadataEndpoint, HttpWithHeaders(map[string]string{
		"Content-Type": "application/json",
		"Accept":       "application/json",
	}), HttpWithJsonBody(map[string]any{
		"type": "get_scheduled_events",
		"args": map[string]any{
			"type":           "cron",
			"trigger_name":   scheduleName,
			"status":         status,
			"limit":          limit,
			"offset":         offset,
			"get_rows_count": rowCount,
		},
	}))

	if err != nil {
		return ScheduledJobEvents{}, err
	}

	defer func() {
		err := resp.Body.Close()
		if err != nil {
			slog.Error("Error closing response body", "error", err)
		}
	}()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return ScheduledJobEvents{}, err
	}

	if resp.StatusCode != 200 {
		errorResponse := map[string]any{}
		err := UnmarshalJson(body, &errorResponse)
		if err != nil {
			return ScheduledJobEvents{}, fmt.Errorf("error executing request: %s", resp.Status)
		}
		if errorResponse["message"] != nil {
			return ScheduledJobEvents{}, fmt.Errorf("error executing request: %s", errorResponse["message"])
		}
		return ScheduledJobEvents{}, fmt.Errorf("error executing request: %s", resp.Status)
	}

	var response ScheduledJobEvents
	err = UnmarshalJson(body, &response)
	if err != nil {
		return ScheduledJobEvents{}, err
	}

	return response, nil
}
