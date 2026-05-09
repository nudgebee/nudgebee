package autopilot

import (
	"errors"
	"log/slog"
	"maps"
	"nudgebee/services/common"
	"nudgebee/services/security"
	"strconv"
	"strings"
	"time"
)

func getAutoOptimizeAnnotation(ctx *security.RequestContext, yamlNew map[string]any) map[string]any {
	// convert the string to yaml object
	autoOptimizeAnnotation := map[string]any{}
	if yamlNew["kind"] != "Deployment" {
		return autoOptimizeAnnotation
	}

	if yamlNew["metadata"] == nil || yamlNew["metadata"].(map[string]any)["annotations"] == nil {
		return autoOptimizeAnnotation
	}

	// get annotations
	newAnnotations := yamlNew["metadata"].(map[string]any)["annotations"].(map[string]any)
	for k, v := range newAnnotations {
		if strings.HasPrefix(k, "workloads.nudgebee.com/autopilot.autoOptimize") {
			autoOptimizeAnnotation[k] = v
		}
	}
	return autoOptimizeAnnotation
}

func tryParseBool(value interface{}, defaultValue bool) (bool, error) {
	switch v := value.(type) {
	case bool:
		return v, nil
	case string:
		return strconv.ParseBool(v)
	}
	return defaultValue, nil

}

func generateAutopilotRequestFromAnnotations(ctx *security.RequestContext, autoOptimizeAnnotation map[string]any, yamlDef map[string]any) (AutoOptimizeRequest, error) {
	truncatedAnnotation := map[string]any{}
	for k, v := range autoOptimizeAnnotation {
		k = strings.TrimPrefix(k, "workloads.nudgebee.com/autopilot.autoOptimize.")
		truncatedAnnotation[k] = v
	}

	yamlDefKind := yamlDef["kind"].(string)
	yamlDefName := yamlDef["metadata"].(map[string]any)["name"].(string)
	yamlDefNamespace := yamlDef["metadata"].(map[string]any)["namespace"].(string)

	if truncatedAnnotation["category"] == nil {
		return AutoOptimizeRequest{}, errors.New("autopilot: category is required")
	}

	isDryRun, err := tryParseBool(truncatedAnnotation["dryRun"], false)
	if err != nil {
		return AutoOptimizeRequest{}, err
	}

	isSlackEnabled, err := tryParseBool(truncatedAnnotation["notification.slack.enabled"], true)
	if err != nil {
		return AutoOptimizeRequest{}, err
	}
	isEmailEnabled, err := tryParseBool(truncatedAnnotation["notification.email.enabled"], true)
	if err != nil {
		return AutoOptimizeRequest{}, err
	}

	var frequency *string
	if truncatedAnnotation["schedule.frequency"] != nil {
		frequency2 := truncatedAnnotation["schedule.frequency"].(string)
		frequency = &frequency2
	} else {
		frequency2 := "0 * * * *"
		frequency = &frequency2
	}
	var scheduleStartDate *ISOTime
	if truncatedAnnotation["schedule.start_date"] != nil {
		scheduleStartDate2, err := time.Parse(time.RFC3339, truncatedAnnotation["schedule.start_date"].(string))
		if err != nil {
			return AutoOptimizeRequest{}, err
		}
		isoTimeScheduleStartDate2 := ISOTime(scheduleStartDate2)
		scheduleStartDate = &isoTimeScheduleStartDate2
	} else {
		currentTime := ISOTime(time.Now())
		scheduleStartDate = &currentTime
	}

	var scheduleEndDate *ISOTime
	if truncatedAnnotation["schedule.end_date"] != nil {
		scheduleEndDate2, err := time.Parse(time.RFC3339, truncatedAnnotation["schedule.end_date"].(string))
		if err != nil {
			return AutoOptimizeRequest{}, err
		}
		isoScheduleEndDate := ISOTime(scheduleEndDate2)
		scheduleEndDate = &isoScheduleEndDate
	}

	if scheduleStartDate != nil && scheduleEndDate != nil && time.Time(*scheduleStartDate).After(time.Time(*scheduleEndDate)) {
		return AutoOptimizeRequest{}, errors.New("autopilot: start_date cannot be after end_date")
	}

	var cpuConfig *AutoOptimizeVerticalRightSizeCpuConfig
	var memConfig *AutoOptimizeVerticalRightSizeMemoryConfig

	for k, v := range truncatedAnnotation {
		if strings.HasPrefix(k, "autopilot_config") {
			if strings.HasPrefix(k, "autopilot_config.vertical_rightsize.cpu") {
				switch k {
				case "autopilot_config.vertical_rightsize.cpu.algo":
					if cpuConfig == nil {
						cpuConfig = &AutoOptimizeVerticalRightSizeCpuConfig{}
					}
					cpuConfig.Algo = v.(string)
				case "autopilot_config.vertical_rightsize.cpu.buffer_pct":
					if cpuConfig == nil {
						cpuConfig = &AutoOptimizeVerticalRightSizeCpuConfig{}
					}
					cpuConfig.BufferPct = v.(int)
				case "autopilot_config.vertical_rightsize.cpu.trigger.change_pct":
					if cpuConfig == nil {
						cpuConfig = &AutoOptimizeVerticalRightSizeCpuConfig{}
					}
					cpuConfig.Trigger.ChangePct = v.(int)
				}

			} else if strings.HasPrefix(k, "autopilot_config.vertical_rightsize.memory") {
				switch k {
				case "autopilot_config.vertical_rightsize.memory.algo":
					if memConfig == nil {
						memConfig = &AutoOptimizeVerticalRightSizeMemoryConfig{}
					}
					memConfig.Algo = v.(string)
				case "autopilot_config.vertical_rightsize.memory.buffer_pct":
					if memConfig == nil {
						memConfig = &AutoOptimizeVerticalRightSizeMemoryConfig{}
					}
					memConfig.BufferPct = v.(int)
				case "autopilot_config.vertical_rightsize.memory.unit":
					if memConfig == nil {
						memConfig = &AutoOptimizeVerticalRightSizeMemoryConfig{}
					}
					memConfig.Unit = v.(string)
				case "autopilot_config.vertical_rightsize.memory.trigger.change_pct":
					if memConfig == nil {
						memConfig = &AutoOptimizeVerticalRightSizeMemoryConfig{}
					}
					memConfig.Trigger.ChangePct = v.(int)
				}
			}

			// //TODO: handle horizontal rightsize config
			// else if strings.HasPrefix(k, "autopilot_config.horizontal_rightsize") {
			// }
		}
	}

	var autoOptimizeVerticalRightSizeConfig *AutoOptimizeVerticalRightSizeConfig
	if cpuConfig != nil || memConfig != nil {
		if cpuConfig != nil {
			if cpuConfig.Algo == "" {
				cpuConfig.Algo = "nb"
			}
			if cpuConfig.BufferPct == 0 {
				cpuConfig.BufferPct = 10
			}
			if cpuConfig.Trigger.ChangePct == 0 {
				cpuConfig.Trigger.ChangePct = 10
			}
		}
		if memConfig != nil {
			if memConfig.Algo == "" {
				memConfig.Algo = "nb"
			}
			if memConfig.BufferPct == 0 {
				memConfig.BufferPct = 10
			}
			if memConfig.Unit == "" {
				memConfig.Unit = "MB"
			}
			if memConfig.Trigger.ChangePct == 0 {
				memConfig.Trigger.ChangePct = 10
			}
		}
		autoOptimizeVerticalRightSizeConfig = &AutoOptimizeVerticalRightSizeConfig{
			Cpu:    cpuConfig,
			Memory: memConfig,
		}
	}

	var autoOptimizeHorizontalRightSizeConfig *AutoOptimizeHorizontalRightSizeConfig

	if autoOptimizeVerticalRightSizeConfig == nil && autoOptimizeHorizontalRightSizeConfig == nil {
		return AutoOptimizeRequest{}, errors.New("autopilot: either vertical_rightsize or horizontal_rightsize config is required")
	}

	return AutoOptimizeRequest{
		ResourceFilter: AutoOptimizeRequestResourceFilter{
			Name:      yamlDefName,
			Type:      yamlDefKind,
			Namespace: yamlDefNamespace,
		},
		Category: truncatedAnnotation["category"].(string),
		AutopilotConfig: AutoOptimizeRequestConfig{
			VerticalRightSize:   autoOptimizeVerticalRightSizeConfig,
			HorizontalRightSize: autoOptimizeHorizontalRightSizeConfig,
		},
		Schedule: AutoOptimizeRequestSchedule{
			Frequency: frequency,
			StartDate: scheduleStartDate,
			EndDate:   scheduleEndDate,
		},
		Notification: AutoOptimizeRequestNotification{
			Slack: AutoOptimizeNotificationConfig{
				Enabled: isSlackEnabled,
			},
			Email: AutoOptimizeNotificationConfig{
				Enabled: isEmailEnabled,
			},
		},
		DryRun: isDryRun,
	}, nil
}

func ProcessEvent(ctx *security.RequestContext, event map[string]any) error {
	if event["aggregation_key"] != "ConfigurationChange/KubernetesResource/Change" {
		return nil
	}

	if event["evidences"] == nil || len(event["evidences"].([]any)) == 0 {
		return nil
	}

	if event["cloud_account_id"] == nil {
		ctx.GetLogger().Error("autopilot: cloud_account_id is required")
		return nil
	}

	if event["tenant"] == nil {
		ctx.GetLogger().Error("autopilot: tenant is required")
		return nil
	}

	if event["cloud_resource_id"] == nil {
		ctx.GetLogger().Error("autopilot: cloud_resource_id is required")
		return nil
	}

	evidences := event["evidences"].([]any)
	evidence := evidences[0].(map[string]any)
	if evidence["data"] == nil {
		return nil
	}
	evidenceData := evidence["data"].(map[string]any)
	evidenceDataNewString := ""
	if evidenceData["new"] != nil {
		evidenceDataNewString = evidenceData["new"].(string)
	}
	evidenceDataOldString := ""
	if evidenceData["old"] != nil {
		evidenceDataOldString = evidenceData["old"].(string)
	}

	// convert the string to yaml object
	yamlNew := make(map[string]any)
	yamlOld := make(map[string]any)
	var err error
	if evidenceDataNewString != "" {
		yamlNew, err = common.UnmarshalYamlToMap(evidenceDataNewString)
		if err != nil {
			ctx.GetLogger().Error("autopilot: error decoding evidence new yaml", "error", err)
			return err
		}
	}
	if evidenceDataOldString != "" {
		yamlOld, err = common.UnmarshalYamlToMap(evidenceDataOldString)
		if err != nil {
			ctx.GetLogger().Error("autopilot: error decoding evidence old yaml", "error", err)
			return err
		}
	}

	autoOptimizeAnnotationNew := getAutoOptimizeAnnotation(ctx, yamlNew)
	autoOptimizeAnnotationOld := getAutoOptimizeAnnotation(ctx, yamlOld)

	if len(autoOptimizeAnnotationNew) == 0 && len(autoOptimizeAnnotationOld) == 0 {
		return nil
	} else if len(autoOptimizeAnnotationNew) == 0 && len(autoOptimizeAnnotationOld) > 0 {
		// handle annotation deletion
	} else if len(autoOptimizeAnnotationNew) > 0 && len(autoOptimizeAnnotationOld) == 0 {
		autopilotRequest, err := generateAutopilotRequestFromAnnotations(ctx, autoOptimizeAnnotationNew, yamlNew)
		if err != nil {
			ctx.GetLogger().Error("autopilot: error sending autopilot request", "error", err)
			return err
		}
		autopilotRequest.AccountId = event["cloud_account_id"].(string)
		autopilotRequest.ResourceFilter.Id = event["cloud_resource_id"].(string)
		autopilotRequest.ResourceFilter.Type = ""
		autopilotRequest.ResourceFilter.Namespace = ""
		autopilotRequest.ResourceFilter.Name = ""
		// send autopilot request for creation
		ctx.GetLogger().Info("autopilot: autopilot request sent", "request", slog.AnyValue(autopilotRequest))
		_, err = CreateAutoOptimizeRule(ctx, autopilotRequest, event["tenant"].(string))
		return err

	} else if len(autoOptimizeAnnotationNew) > 0 && len(autoOptimizeAnnotationOld) > 0 && !maps.Equal(autoOptimizeAnnotationNew, autoOptimizeAnnotationOld) {
		if maps.Equal(autoOptimizeAnnotationNew, autoOptimizeAnnotationOld) {
			ctx.GetLogger().Info("autopilot: no change in autopilot annotation")
			return nil
		}
		autopilotRequest, err := generateAutopilotRequestFromAnnotations(ctx, autoOptimizeAnnotationNew, yamlNew)
		if err != nil {
			return err
		}
		autopilotRequest.AccountId = event["cloud_account_id"].(string)
		id, ok := autoOptimizeAnnotationNew["workloads.nudgebee.com/autopilot.id"].(string)
		if ok {
			autopilotRequest.Id = &id
		} else {
			ctx.GetLogger().Error("autopilot: error sending autopilot request", "error", "id not found")
		}
		// send autopilot request for creation
		_, err = UpdateAutoOptimizeRule(ctx, autopilotRequest, event["tenant"].(string))

		return err
	}

	return nil
}
