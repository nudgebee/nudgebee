package event

import (
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"nudgebee/services/account"
	"nudgebee/services/account/adapter"
	"nudgebee/services/common"
	"nudgebee/services/config"
	"nudgebee/services/eventrule"
	"nudgebee/services/eventrule/playbooks"
	integrationcore "nudgebee/services/integrations/core"
	"nudgebee/services/internal/database"
	"nudgebee/services/internal/database/models"
	"nudgebee/services/llm"
	"nudgebee/services/notification"
	"nudgebee/services/relay"
	"nudgebee/services/security"
	"nudgebee/services/triage"
	"nudgebee/services/workflow"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"gopkg.in/yaml.v2"
)

// Compiled once at package init instead of on every extractDiffInfo call.
var (
	diffLineNumberRegex = regexp.MustCompile(`@@ -(\d+),`)
	diffOldChangeRegex  = regexp.MustCompile(`(?m)^-(.*)`)
	diffNewChangeRegex  = regexp.MustCompile(`(?m)^\+(.*)`)
)

func init() {
	integrationcore.InvestigateEventFn = InvestigateEvent
}

func GetEvent(context *security.RequestContext, id string) (models.Event, error) {
	databaseManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return models.Event{}, err
	}
	r := databaseManager.Db.QueryRowx("SELECT * FROM events WHERE id = $1", id)
	if r.Err() != nil {
		return models.Event{}, r.Err()
	}
	recommendation := models.Event{}
	err = r.StructScan(&recommendation)
	return recommendation, err
}

func ListEventResolutions(context *security.RequestContext, rescommendationId string) ([]models.EventResolution, error) {
	databaseManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return []models.EventResolution{}, err
	}
	r, err := databaseManager.Db.Queryx("SELECT * FROM event_resolution WHERE event_id = $1", rescommendationId)
	if err != nil {
		return []models.EventResolution{}, err
	}

	resolutions := []models.EventResolution{}
	for r.Next() {
		resolution := models.EventResolution{}
		err = r.StructScan(&resolution)
		if err != nil {
			return []models.EventResolution{}, err
		}
		resolutions = append(resolutions, resolution)
	}
	return resolutions, nil
}

func updateContainerImage(pod map[any]any, containerName string, newImage string) (map[any]any, error) {
	spec, ok := pod["spec"].(map[any]any)
	if !ok {
		return pod, fmt.Errorf("spec not found in pod")
	}

	containers, ok := spec["template"].(map[any]any)["spec"].(map[any]any)["containers"].([]any)
	if !ok {
		return pod, fmt.Errorf("containers not found in spec")
	}
	for _, c := range containers {
		container, ok := c.(map[any]any)
		if !ok {
			return pod, fmt.Errorf("invalid container structure")
		}

		if container["name"] == containerName {
			container["image"] = newImage
			return pod, nil
		}
	}
	return pod, fmt.Errorf("container %s not found", containerName)
}

func extractDiffInfo(diff string) (lineNumber int, oldChange, newChange string, err error) {
	diff = replaceTabs(diff)
	lineNumberMatches := diffLineNumberRegex.FindStringSubmatch(diff)
	if len(lineNumberMatches) < 2 {
		return 0, "", "", fmt.Errorf("could not find line number in diff")
	}

	if _, err1 := fmt.Sscanf(lineNumberMatches[1], "%d", &lineNumber); err1 != nil {
		return 0, "", "", fmt.Errorf("error parsing line number in diff: %w", err1)
	}

	oldChangeMatches := diffOldChangeRegex.FindAllStringSubmatch(diff, -1)
	newChangeMatches := diffNewChangeRegex.FindAllStringSubmatch(diff, -1)

	filterOutFilenames := func(matches [][]string) string {
		for _, match := range matches {
			if !strings.HasPrefix(strings.TrimSpace(match[1]), "-- a/") &&
				!strings.HasPrefix(strings.TrimSpace(match[1]), "++ b/") &&
				!strings.HasPrefix(strings.TrimSpace(match[0]), "--- a/") &&
				!strings.HasPrefix(strings.TrimSpace(match[0]), "+++ b/") {
				return strings.TrimSpace(match[1])
			}
		}
		return ""
	}

	oldChange = filterOutFilenames(oldChangeMatches)
	newChange = filterOutFilenames(newChangeMatches)
	if oldChange == "" && newChange == "" {
		lines := strings.Split(diff, "\n")
		inChanges := false

		for _, line := range lines {
			if strings.HasPrefix(line, "@@") {
				inChanges = true
				continue
			}

			if inChanges {
				if strings.HasPrefix(line, "-") {
					oldChange = strings.TrimPrefix(line, "-")
				} else if strings.HasPrefix(line, "+") {
					newChange = strings.TrimPrefix(line, "+")
				}
			}
		}
	}

	return lineNumber, oldChange, newChange, nil
}

func replaceTabs(str string) string {
	return strings.ReplaceAll(str, "\t", "    ") // Replacing tab '\t' with 4 spaces
}

func ApplyEventResolution(ctx *security.RequestContext, query EventRecommendationApplyRequest) (EventRecommendationApplyResponse, error) {
	if !ctx.GetSecurityContext().HasAccountAccess(query.AccountId, security.SecurityAccessTypeCreate) {
		return EventRecommendationApplyResponse{}, common.ErrorUnauthorized("error: account access not found")
	}

	queryData, ok := query.Data.(map[string]any)
	if !ok {
		return EventRecommendationApplyResponse{}, fmt.Errorf("resolution: invalid data format, expected map")
	}
	a, err := account.GetAccount(ctx, query.AccountId)
	if err != nil {
		ctx.GetLogger().Error("error getting account", "error", err)
		return EventRecommendationApplyResponse{}, err
	}
	if a.Id == "" {
		return EventRecommendationApplyResponse{}, fmt.Errorf("recommendation: account not found - %s", query.AccountId)
	}

	if a.AccountType != "kubernetes" {
		return EventRecommendationApplyResponse{}, fmt.Errorf("recommendation: account type not supported - %s", a.AccountType)
	}

	r, err := GetEvent(ctx, query.EventId)
	if err != nil {
		ctx.GetLogger().Error("error getting events", "error", err)
		return EventRecommendationApplyResponse{}, err
	}
	if r.Id == "" {
		return EventRecommendationApplyResponse{}, fmt.Errorf("recommendation: event not found - %s", query.EventId)
	}

	var cr models.Resource
	if r.CloudResourceId != nil {
		cr, err = account.GetResource(ctx, *r.CloudResourceId)
		if err != nil {
			ctx.GetLogger().Error("error getting resource", "error", err)
			return EventRecommendationApplyResponse{}, err
		}
	} else {
		cr = models.Resource{}
	}

	// Determine the provider
	switch query.Provider {
	case "git":
		// "git" means: raise a PR, auto-detect whether GitHub or GitLab from annotation
		if cr.Id == "" {
			return EventRecommendationApplyResponse{}, fmt.Errorf("event resolution: resource not found, cannot detect git provider")
		}
		if r.CloudAccountId == nil {
			return EventRecommendationApplyResponse{}, fmt.Errorf("event resolution: cloud account id not found")
		}
		gitRepoURL, gitErr := adapter.GetGitRepoURLFromWorkload(ctx, cr, *r.CloudAccountId)
		if gitErr != nil {
			ctx.GetLogger().Error("error getting git repo URL from workload", "error", gitErr)
			return EventRecommendationApplyResponse{}, fmt.Errorf("event resolution: failed to get git repo annotation - %w", gitErr)
		}
		if gitRepoURL == "" {
			return EventRecommendationApplyResponse{}, fmt.Errorf("event resolution: ci.nudgebee.com/git.repo annotation not found on workload")
		}
		detected := adapter.DetectGitProviderFromURL(gitRepoURL)
		if detected == "" {
			return EventRecommendationApplyResponse{}, fmt.Errorf("event resolution: unable to detect git provider from URL - %s", gitRepoURL)
		}
		query.Provider = detected
		ctx.GetLogger().Info("detected git provider from annotation", "provider", detected, "repo_url", gitRepoURL)
	case "":
		query.Provider = "kubernetes"
	}

	adptr := adapter.GetAdapter(query.Provider)
	existingRecommendations, err := ListEventResolutions(ctx, r.Id)

	if err != nil {
		ctx.GetLogger().Error("failed to fetch existing recommendations", "error", err)
		return EventRecommendationApplyResponse{}, err
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return EventRecommendationApplyResponse{}, err
	}

	// insert resolution
	statusMessage := "Configuring"
	found, recommendationResolutionId := hasInProgressRecommendation(existingRecommendations)
	resolutionCreatedAt := time.Now()
	resolution := models.EventResolution{
		Id:              recommendationResolutionId,
		CreatedAt:       &resolutionCreatedAt,
		UpdatedAt:       &resolutionCreatedAt,
		EventId:         r.Id,
		Type:            models.RecommendationResolutionTypeDeploymentChange,
		Data:            models.NewJsonObject(query),
		Status:          models.RecommendationResolutionStatusInProgress,
		TypeReferenceId: "",
		ResolverType:    models.RecommendationResolutionResolverTypeUser,
		ResolverId:      ctx.GetSecurityContext().GetUserId(),
		StatusMessage:   &statusMessage,
	}
	convertedRecommendationResolution := []models.RecommendationResolution{}
	recommendationRequest := adapter.ApplyRecommendationRequest{}

	revert, okRevert := queryData["revert"].(bool)
	raisePR, okRaisePR := queryData["raisePR"].(bool)
	if *r.AggregationKey == "KubePersistentVolumeFillingUp" || *r.AggregationKey == "KubernetesVolumeOutOfDiskSpace" {
		if queryData["size"] == "" || queryData["size"] == nil {
			return EventRecommendationApplyResponse{}, fmt.Errorf("recommendation: to increase persistent volume size is required")
		}
		var persistentVolumeClaimName = ""
		if r.Evidences.IsArray() {
			for _, item := range r.Evidences.Array() {
				switch v := item.(type) {
				case map[string]any:
					if evidenceType, ok := v["type"].(string); ok {
						if evidenceType == "table" {
							if dataMap, ok := v["data"].(map[string]any); ok {
								if tableName, ok := dataMap["table_name"].(string); ok && tableName == "*Alert labels*" {
									if rows, ok := dataMap["rows"].([]any); ok {
										for _, row := range rows {
											if rowSlice, ok := row.([]any); ok && len(rowSlice) == 2 {
												if label, ok := rowSlice[0].(string); ok && label == "persistentvolumeclaim" {
													if value, ok := rowSlice[1].(string); ok {
														persistentVolumeClaimName = value
													}
												}
											}
										}
									}
								}
							}
						}
					}
				default:
					slog.Warn("Unknown type for evidence item", "type", fmt.Sprintf("%T", v))
				}
			}
		}

		if persistentVolumeClaimName == "" {
			return EventRecommendationApplyResponse{}, fmt.Errorf("recommendation: persistent volume claim not found")
		}
		recommendationRequest = adapter.ApplyRecommendationRequest{
			Data: query.Data.(map[string]any),
			Recommendation: models.Recommendation{
				Category:       "EventResolution",
				RuleName:       *r.AggregationKey,
				Id:             r.Id,
				CloudAccountId: *r.CloudAccountId,
				TenantId:       *r.Tenant,
				Recommendation: models.NewJsonObject(map[string]any{
					"account_id":  *r.CloudAccountId,
					"action_name": "rightsize_pvc",
					"action_params": map[string]any{
						"name":      persistentVolumeClaimName,
						"namespace": r.SubjectNamespace,
						"size":      queryData["size"].(string) + "Gi",
					},
				}),
				AccountObjectId: &query.EventId,
			},
			Resource:       cr,
			ProviderConfig: query.ProviderConfig,
		}
	} else if *r.AggregationKey == "CPUThrottlingHigh" {
		if common.IsEmptyStringPointer(r.SubjectOwnerKind) || common.IsEmptyStringPointer(r.SubjectOwner) {
			return EventRecommendationApplyResponse{}, fmt.Errorf("resolution: kind or owner is not correct")
		}
		if *r.SubjectOwnerKind != "Pod" {
			size, sizeOk := queryData["size"].(string)
			restart, restartOk := queryData["restart"].(bool)
			increaseReplicas, increaseReplicasOk := queryData["increase_replicas"].(string)
			if !sizeOk || !restartOk || !increaseReplicasOk {
				return EventRecommendationApplyResponse{}, fmt.Errorf("resolution: invalid or missing data for 'size', 'restart', or 'increase_replicas'")
			}
			if size != "" {
				containerName := ""
				if r.Evidences.IsArray() {
					for _, item := range r.Evidences.Array() {
						switch v := item.(type) {
						case map[string]any:
							if evidenceType, ok := v["type"].(string); ok {
								if evidenceType == "table" {
									if dataMap, ok := v["data"].(map[string]any); ok {
										if tableName, ok := dataMap["table_name"].(string); ok && tableName == "*Alert labels*" {
											if rows, ok := dataMap["rows"].([]any); ok {
												for _, row := range rows {
													if rowSlice, ok := row.([]any); ok && len(rowSlice) == 2 {
														if label, ok := rowSlice[0].(string); ok && label == "container" {
															if value, ok := rowSlice[1].(string); ok {
																containerName = value
															}
														}
													}
												}
											}
										}
									}
								}
							}
						default:
							slog.Warn("Unknown type for evidence item", "type", fmt.Sprintf("%T", v))
						}
					}
				}
				if containerName == "" {
					return EventRecommendationApplyResponse{}, fmt.Errorf("resolution: container name cannot be empty")
				}
				recommendationRequest = adapter.ApplyRecommendationRequest{
					Data: query.Data.(map[string]any),
					Recommendation: models.Recommendation{
						Category:       "EventResolution",
						RuleName:       "CPUThrottlingHigh",
						Id:             r.Id,
						CloudAccountId: *r.CloudAccountId,
						TenantId:       *r.Tenant,
						Recommendation: models.NewJsonObject(map[string]any{
							"account_id":  *r.CloudAccountId,
							"action_name": "rightsizing_resource",
							"action_params": map[string]any{
								"name":       r.SubjectOwner,
								"namespace":  r.SubjectNamespace,
								"kind":       r.SubjectOwnerKind,
								"containers": []map[string]any{{"cpu_limit": size, "container_name": containerName}},
							},
						}),
						AccountObjectId: &query.EventId,
					},
					Resource:       cr,
					ProviderConfig: query.ProviderConfig,
				}
			} else if increaseReplicas != "" {
				recommendationRequest = adapter.ApplyRecommendationRequest{
					Data: query.Data.(map[string]any),
					Recommendation: models.Recommendation{
						Category:       "EventResolution",
						RuleName:       "CPUThrottlingHigh",
						Id:             r.Id,
						CloudAccountId: *r.CloudAccountId,
						TenantId:       *r.Tenant,
						Recommendation: models.NewJsonObject(map[string]any{
							"account_id":  *r.CloudAccountId,
							"action_name": "replica_rightsizing",
							"action_params": map[string]any{
								"name":          r.SubjectOwner,
								"namespace":     r.SubjectNamespace,
								"kind":          r.SubjectOwnerKind,
								"replica_count": increaseReplicas,
							},
						}),
						AccountObjectId: &query.EventId,
					},
					Resource:       cr,
					ProviderConfig: query.ProviderConfig,
				}
			} else if restart {
				recommendationRequest = adapter.ApplyRecommendationRequest{
					Data: query.Data.(map[string]any),
					Recommendation: models.Recommendation{
						Category:       "EventResolution",
						RuleName:       "CPUThrottlingHigh",
						Id:             r.Id,
						CloudAccountId: *r.CloudAccountId,
						TenantId:       *r.Tenant,
						Recommendation: models.NewJsonObject(map[string]any{
							"account_id":  *r.CloudAccountId,
							"action_name": "delete_pod",
							"action_params": map[string]any{
								"name":      r.SubjectName,
								"namespace": r.SubjectNamespace,
								"previous":  false,
							},
						}),
						AccountObjectId: &query.EventId,
					},
					Resource:       cr,
					ProviderConfig: query.ProviderConfig,
				}
			}
		} else {
			return EventRecommendationApplyResponse{}, fmt.Errorf("resolution: unsupported service type- %s", *r.SubjectOwnerKind)
		}
	} else if *r.AggregationKey == "PodMemoryReachingLimit" {
		if common.IsEmptyStringPointer(r.SubjectOwnerKind) || common.IsEmptyStringPointer(r.SubjectOwner) {
			return EventRecommendationApplyResponse{}, fmt.Errorf("resolution: kind or owner is not correct")
		}
		size, sizeOk := queryData["size"].(string)
		restart, restartOk := queryData["restart"].(bool)
		increaseReplicas, increaseReplicasOk := queryData["increase_replicas"].(string)
		if !sizeOk || !restartOk || !increaseReplicasOk {
			return EventRecommendationApplyResponse{}, fmt.Errorf("resolution: invalid or missing data for 'size', 'restart', or 'increase_replicas'")
		}
		if *r.SubjectOwnerKind != "Pod" {
			if size != "" {
				containerName, _ := queryData["containerName"].(string)
				if r.Evidences.IsArray() {
					for _, item := range r.Evidences.Array() {
						switch v := item.(type) {
						case map[string]any:
							if evidenceType, ok := v["type"].(string); ok {
								if evidenceType == "table" {
									if dataMap, ok := v["data"].(map[string]any); ok {
										if tableName, ok := dataMap["table_name"].(string); ok && tableName == "*Alert labels*" {
											if rows, ok := dataMap["rows"].([]any); ok {
												for _, row := range rows {
													if rowSlice, ok := row.([]any); ok && len(rowSlice) == 2 {
														if label, ok := rowSlice[0].(string); ok && label == "container" {
															if value, ok := rowSlice[1].(string); ok {
																containerName = value
															}
														}
													}
												}
											}
										}
									}
								}
							}
						default:
							slog.Warn("Unknown type for evidence item", "type", fmt.Sprintf("%T", v))
						}
					}
				}
				if containerName == "" {
					return EventRecommendationApplyResponse{}, fmt.Errorf("resolution: container name cannot be empty")
				}
				recommendationRequest = adapter.ApplyRecommendationRequest{
					Data: query.Data.(map[string]any),
					Recommendation: models.Recommendation{
						Category:       "EventResolution",
						RuleName:       "PodMemoryReachingLimit",
						Id:             r.Id,
						CloudAccountId: *r.CloudAccountId,
						TenantId:       *r.Tenant,
						Recommendation: models.NewJsonObject(map[string]any{
							"account_id":  *r.CloudAccountId,
							"action_name": "rightsizing_resource",
							"action_params": map[string]any{
								"name":       r.SubjectOwner,
								"namespace":  r.SubjectNamespace,
								"kind":       r.SubjectOwnerKind,
								"containers": []map[string]any{{"memory_limit": size, "container_name": containerName}},
							},
						}),
						AccountObjectId: &query.EventId,
					},
					Resource:       cr,
					ProviderConfig: query.ProviderConfig,
				}
			} else if increaseReplicas != "" {
				recommendationRequest = adapter.ApplyRecommendationRequest{
					Data: query.Data.(map[string]any),
					Recommendation: models.Recommendation{
						Category:       "EventResolution",
						RuleName:       "PodMemoryReachingLimit",
						Id:             r.Id,
						CloudAccountId: *r.CloudAccountId,
						TenantId:       *r.Tenant,
						Recommendation: models.NewJsonObject(map[string]any{
							"account_id":  *r.CloudAccountId,
							"action_name": "replica_rightsizing",
							"action_params": map[string]any{
								"name":          r.SubjectOwner,
								"namespace":     r.SubjectNamespace,
								"kind":          r.SubjectOwnerKind,
								"replica_count": increaseReplicas,
							},
						}),
						AccountObjectId: &query.EventId,
					},
					Resource:       cr,
					ProviderConfig: query.ProviderConfig,
				}
			} else if restart {
				recommendationRequest = adapter.ApplyRecommendationRequest{
					Data: query.Data.(map[string]any),
					Recommendation: models.Recommendation{
						Category:       "EventResolution",
						RuleName:       "PodMemoryReachingLimit",
						Id:             r.Id,
						CloudAccountId: *r.CloudAccountId,
						TenantId:       *r.Tenant,
						Recommendation: models.NewJsonObject(map[string]any{
							"account_id":  *r.CloudAccountId,
							"action_name": "delete_pod",
							"action_params": map[string]any{
								"name":      r.SubjectName,
								"namespace": r.SubjectNamespace,
								"previous":  false,
							},
						}),
						AccountObjectId: &query.EventId,
					},
					Resource:       cr,
					ProviderConfig: query.ProviderConfig,
				}
			}
		} else {
			return EventRecommendationApplyResponse{}, fmt.Errorf("resolution: unsupported service type- %s", *r.SubjectOwnerKind)
		}
	} else if *r.AggregationKey == "image_pull_backoff_reporter" {
		imageChangeContainerName, _ := queryData["imageChangeContainerName"].(string)
		imageNameWithTag, _ := queryData["imageNameWithTag"].(string)
		if imageChangeContainerName == "" || imageNameWithTag == "" {
			return EventRecommendationApplyResponse{}, fmt.Errorf("resolution: imageChangeContainerName and imageNameWithTag are required")
		}

		resp, err1 := relay.Execute(relay.RelayExecuteRequest{
			NoSinks: true,
			Cache:   false,
			Body: relay.ActionExecuteBody{
				ActionName: "get_resource_yaml",
				AccountID:  *r.CloudAccountId,
				ActionParams: map[string]any{
					"name":      r.SubjectOwner,
					"namespace": r.SubjectNamespace,
					"kind":      r.SubjectOwnerKind,
				},
			},
		})
		if _, ok := resp["data"]; !ok {
			return EventRecommendationApplyResponse{}, err1
		}
		var result map[any]any
		if v, ok := resp["data"].(map[string]any)["findings"]; ok {
			findings := v.([]any)
			if len(findings) > 0 {
				if evidenceRaw, ok := findings[0].(map[string]any)["evidence"]; ok {
					evidence := evidenceRaw.([]any)
					if len(evidence) > 0 {
						data := []map[string]any{}
						evidenceDataRaw := evidence[0].(map[string]any)["data"]
						evidenceDataRawBytes := []byte(evidenceDataRaw.(string))
						err := common.UnmarshalJson(evidenceDataRawBytes, &data)
						if err != nil {
							return EventRecommendationApplyResponse{}, err1
						}
						for _, d := range data {
							if d["type"] == "yaml" {
								if encodedData, ok := d["data"].(string); ok {
									encodedData = strings.TrimPrefix(encodedData, "b'")
									encodedData = strings.TrimSuffix(encodedData, "'")
									decodedBytes, err2 := base64.StdEncoding.DecodeString(encodedData)
									if err2 != nil {
										return EventRecommendationApplyResponse{}, err2
									}
									err3 := yaml.Unmarshal(decodedBytes, &result)
									if err3 != nil {
										return EventRecommendationApplyResponse{}, err3
									}
								} else {
									return EventRecommendationApplyResponse{}, fmt.Errorf("data field is not a string")
								}
							}
						}
					}
				}
			}
		}
		updatedResult, err4 := updateContainerImage(result, imageChangeContainerName, imageNameWithTag)
		if err4 != nil {
			return EventRecommendationApplyResponse{}, err4
		}
		recommendationRequest = adapter.ApplyRecommendationRequest{
			Data: query.Data.(map[string]any),
			Recommendation: models.Recommendation{
				Category:       "EventResolution",
				RuleName:       *r.AggregationKey,
				Id:             r.Id,
				CloudAccountId: *r.CloudAccountId,
				TenantId:       *r.Tenant,
				Recommendation: models.NewJsonObject(map[string]any{
					"account_id":    *r.CloudAccountId,
					"action_name":   "replace_workload",
					"action_params": updatedResult,
				}),
				AccountObjectId: &query.EventId,
			},
			Resource:       cr,
			ProviderConfig: query.ProviderConfig,
		}
	} else if okRevert {
		if revert {
			request, err := getRevertRecommendationRequest(cr, r)
			if err != nil {
				return EventRecommendationApplyResponse{}, err
			}
			recommendationRequest = adapter.ApplyRecommendationRequest{
				Data: query.Data.(map[string]any),
				Recommendation: models.Recommendation{
					Category:       "EventResolution",
					RuleName:       *r.AggregationKey,
					Id:             r.Id,
					CloudAccountId: *r.CloudAccountId,
					TenantId:       *r.Tenant,
					Recommendation: models.NewJsonObject(map[string]any{
						"account_id":    *r.CloudAccountId,
						"action_name":   "replace_workload",
						"action_params": request,
					}),
					AccountObjectId: &query.EventId,
				},
				Resource:       cr,
				ProviderConfig: query.ProviderConfig,
			}
		} else {
			ctx.GetLogger().Error("error applying at revert resolution", "error", "select atleast one resolution")
			return EventRecommendationApplyResponse{}, fmt.Errorf("select atleast one resolution")
		}
	} else if *r.AggregationKey == "KubeNodeNotReady" {
		cordon, _ := queryData["cordon"].(bool)
		drain, _ := queryData["drain"].(bool)
		actionName := ""
		if cordon {
			actionName = "cordon"
		} else if drain {
			actionName = "drain"
		} else {
			ctx.GetLogger().Error("error applying resolution", "error", "cordon or drain action is required")
			return EventRecommendationApplyResponse{}, common.ErrorBadRequest("cordon or drain action is required")
		}
		if cordon || drain {
			recommendationRequest = adapter.ApplyRecommendationRequest{
				Data: query.Data.(map[string]any),
				Recommendation: models.Recommendation{
					Category:       "EventResolution",
					RuleName:       *r.AggregationKey,
					Id:             r.Id,
					CloudAccountId: *r.CloudAccountId,
					TenantId:       *r.Tenant,
					Recommendation: models.NewJsonObject(map[string]any{
						"account_id":  *r.CloudAccountId,
						"action_name": actionName,
						"action_params": map[string]any{
							"name": r.SubjectName,
						},
					}),
					AccountObjectId: &query.EventId,
				},
				Resource:       cr,
				ProviderConfig: query.ProviderConfig,
			}
		}
	} else if okRaisePR {
		if raisePR {
			resolution.Type = models.RecommendationResolutionTypePullRequest
			eventLogAnalysisRow := dbms.Db.QueryRowx("SELECT analysis from event_log_analysis WHERE analysis_type = 'log_analysis' and cloud_account_id = $1 AND event_id = $2 AND status = 'COMPLETED'", query.AccountId, query.EventId)
			if eventLogAnalysisRow.Err() != nil {
				return EventRecommendationApplyResponse{}, fmt.Errorf("invalid event_log_analysis - %s", query.EventId)
			}
			eventLogAnalysisDetail := map[string]any{}
			err = eventLogAnalysisRow.MapScan(eventLogAnalysisDetail)
			if err != nil {
				return EventRecommendationApplyResponse{}, err
			}
			var result map[string]any
			err := common.UnmarshalJson([]byte(eventLogAnalysisDetail["analysis"].(string)), &result)
			if err != nil {
				return EventRecommendationApplyResponse{}, err
			}
			sourceUpdates, ok := result["source_updates"].(map[string]any)
			if !ok {
				return EventRecommendationApplyResponse{}, fmt.Errorf("failed to parse 'source_updates' from result")
			}
			filepath, ok := sourceUpdates["file_path"].(string)
			if !ok {
				return EventRecommendationApplyResponse{}, fmt.Errorf("'file_path' not found or not a string in 'source_updates'")
			}
			gitDiff, ok := sourceUpdates["gitDiff"].(string)
			if !ok {
				return EventRecommendationApplyResponse{}, fmt.Errorf("'gitDiff' not found or not a string in 'source_updates'")
			}
			lineNumber, oldLine, newLine, err1 := extractDiffInfo(gitDiff)
			if err1 != nil {
				return EventRecommendationApplyResponse{}, fmt.Errorf("failed to extract diff info from 'gitDiff': %w", err1)
			}
			sourceDetails, ok := result["source_details"].(map[string]any)
			if !ok {
				return EventRecommendationApplyResponse{}, fmt.Errorf("failed to parse 'source_details' from result")
			}
			sha1, ok := sourceDetails["workloads.nudgebee.com/git.hash"].(string)
			if !ok {
				return EventRecommendationApplyResponse{}, fmt.Errorf("'workloads.nudgebee.com/git.hash' not found or not a string in 'source_details'")
			}
			recommendationRequest = adapter.ApplyRecommendationRequest{
				Data: query.Data.(map[string]any),
				Recommendation: models.Recommendation{
					Category:       "EventResolutionRaisePr",
					RuleName:       *r.AggregationKey,
					Id:             r.Id,
					CloudAccountId: *r.CloudAccountId,
					TenantId:       *r.Tenant,
					Recommendation: models.NewJsonObject(map[string]any{
						"account_id": *r.CloudAccountId,
						"old_change": gitDiff,
						"fileName":   filepath,
						"lineNumber": lineNumber + 1,
						"oldLine":    oldLine,
						"newLine":    newLine,
						"sha1":       sha1,
					}),
					AccountObjectId: &query.EventId,
				},
				Resource:       cr,
				ProviderConfig: query.ProviderConfig,
			}
		} else {
			return EventRecommendationApplyResponse{}, fmt.Errorf("resolution: raisePR must be true to create a pull request")
		}
	} else {
		containerName := ""
		if queryData["container_name"] != nil {
			containerName = queryData["container_name"].(string)
		}

		if containerName == "" {
			ctx.GetLogger().Error("error applying recommendation", "error", "container name is required")
			return EventRecommendationApplyResponse{}, common.ErrorBadRequest("container_name not found")
		}

		containerValues := []any{}
		resourceMeta := cr.Meta.Object().(map[string]any)
		resourceConfigs := resourceMeta["config"].(map[string]any)
		resourceContainers := resourceConfigs["containers"].([]any)
		for _, containerAny := range resourceContainers {
			container := containerAny.(map[string]any)
			if container["name"] == containerName {
				containerResources := container["resources"].(map[string]any)
				containerLimits := map[string]any{}
				containerRequests := map[string]any{}
				if containerResources["limits"] != nil {
					containerLimits = containerResources["limits"].(map[string]any)
				}
				if containerResources["requests"] != nil {
					containerRequests = containerResources["requests"].(map[string]any)
				}
				cpuResource := map[string]any{
					"resource":  "cpu",
					"allocated": map[string]any{},
				}
				memResource := map[string]any{
					"resource":  "memory",
					"allocated": map[string]any{},
				}
				if containerLimits["cpu"] != nil {
					cpuResource["allocated"].(map[string]any)["limits"] = containerLimits["cpu"]
				}
				if containerLimits["memory"] != nil {
					memResource["allocated"].(map[string]any)["limits"] = containerLimits["memory"]
				}
				if containerRequests["cpu"] != nil {
					cpuResource["allocated"].(map[string]any)["requests"] = containerRequests["cpu"]
				}
				if containerRequests["memory"] != nil {
					memResource["allocated"].(map[string]any)["requests"] = containerRequests["memory"]
				}
				containerValues = append(containerValues, cpuResource, memResource)
			}
		}

		if query.ProviderConfig == nil {
			query.ProviderConfig = map[string]any{}
		}
		query.ProviderConfig["recommendation_source"] = "event"

		recommendationRequest = adapter.ApplyRecommendationRequest{
			Data: query.Data.(map[string]any),
			Recommendation: models.Recommendation{
				Category:       "RightSizing",
				RuleName:       "pod_right_sizing",
				Id:             r.Id,
				CloudAccountId: *r.CloudAccountId,
				TenantId:       *r.Tenant,
				Recommendation: models.NewJsonObject(map[string]any{
					containerName: containerValues,
				}),
			},
			Resource:       cr,
			ProviderConfig: query.ProviderConfig,
		}

		for _, eventResolution := range existingRecommendations {
			convertedRecommendationResolution = append(convertedRecommendationResolution, models.RecommendationResolution{
				Id:               eventResolution.Id,
				CreatedAt:        eventResolution.CreatedAt,
				UpdatedAt:        eventResolution.UpdatedAt,
				RecommendationId: eventResolution.EventId,
				Type:             eventResolution.Type,
				Data:             eventResolution.Data,
				Status:           eventResolution.Status,
				TypeReferenceId:  eventResolution.TypeReferenceId,
				ResolverType:     eventResolution.ResolverType,
				ResolverId:       eventResolution.ResolverId,
				StatusMessage:    eventResolution.StatusMessage,
			})

		}
	}

	if !found {
		_, err = dbms.Db.Exec(`INSERT INTO event_resolution (id, created_at, updated_at, event_id, type, data, status, type_reference_id, resolver_type, resolver_id, status_message) values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
			resolution.Id, resolutionCreatedAt.Format(time.RFC3339), resolutionCreatedAt.Format(time.RFC3339), resolution.EventId, resolution.Type, resolution.Data, resolution.Status, resolution.TypeReferenceId, resolution.ResolverType, resolution.ResolverId, resolution.StatusMessage)
		if err != nil {
			ctx.GetLogger().Error("error inserting recommendation resolution", "error", err, "event_id", resolution.EventId, "resolution_id", resolution.Id)
			return EventRecommendationApplyResponse{}, common.ErrorInternal(fmt.Sprintf("error inserting recommendation resolution: %s", err.Error()))
		}
	}

	resp, err := adptr.ApplyRecommendation(ctx, recommendationRequest, convertedRecommendationResolution, recommendationResolutionId)

	if err != nil {
		ctx.GetLogger().Error("error applying recommendation", "error", err)
		_, err = dbms.Db.Exec("UPDATE event_resolution SET status = $2, updated_at = $3, status_message = $4 WHERE id = $1", resolution.Id, models.RecommendationResolutionStatusFailed, time.Now().Format(time.RFC3339), err.Error())
		if err != nil {
			ctx.GetLogger().Error("error updating status of event resolution", "error", err)
		}
		return EventRecommendationApplyResponse{}, err
	}

	ctx.GetLogger().Info("Recommendation applied", "response", slog.AnyValue(resp.Data))

	recommendationStatus := models.RecommendationStatusInProgress
	switch resp.Status {
	case adapter.RecommendationResolutionStatusSuccess:
		recommendationStatus = models.RecommendationStatusClosed
	case adapter.RecommendationResolutionStatusFailed:
		recommendationStatus = models.RecommendationStatusDismissed
	case adapter.RecommendationResolutionStatusInProgress:
		recommendationStatus = models.RecommendationStatusInProgress
	}

	if recommendationStatus == models.RecommendationStatusClosed {
		_, err = dbms.Db.Exec("UPDATE events SET status = $3, updated_at = $2 WHERE id = $1", r.Id, time.Now().Format(time.RFC3339), "RESOLVED")
		if err != nil {
			ctx.GetLogger().Error("error closing recommendation", "error", err, "event_id", r.Id, "status", recommendationStatus)
			return EventRecommendationApplyResponse{}, common.ErrorInternal(fmt.Sprintf("error closing recommendation: %s", err.Error()))
		}
	}

	_, err = dbms.Db.Exec("UPDATE event_resolution SET type_reference_id = $2, updated_at = $3 WHERE id = $1", resolution.Id, resp.ResolutionTypeRefrenceId, time.Now().Format(time.RFC3339))
	if err != nil {
		ctx.GetLogger().Error("error updating type reference event resolution", "error", err)
	}

	// trigger notification
	err = notification.TriggerNotificationForEventResolution(ctx, r, resolution)
	if err != nil {
		ctx.GetLogger().Error("error triggering notification", "error", err)
	}
	return EventRecommendationApplyResponse{
		Data:       []any{resp.Data},
		Resolution: resolution,
		Status:     models.RecommendationStatus(string(recommendationStatus)),
	}, nil

}

func getRevertRecommendationRequest(cr models.Resource, r models.Event) (map[string]any, error) {
	oldYaml := ""
	resourceKeys := strings.Split(*cr.ResourceId, "/")
	if len(resourceKeys) != 3 {
		return map[string]any{}, fmt.Errorf("resolution: service key is not correct")
	}
	if r.Evidences.IsArray() {
		for _, item := range r.Evidences.Array() {
			switch v := item.(type) {
			case map[string]any:
				if evidenceType, ok := v["type"].(string); ok {
					if evidenceType == "diff" {
						if dataMap, ok := v["data"].(map[string]any); ok {
							if oldVal, ok := dataMap["old"]; ok {
								switch old := oldVal.(type) {
								case string:
									oldYaml = old
								default:
									slog.Warn("Unexpected type for 'old'", "type", fmt.Sprintf("%T", old))
								}
							} else {
								slog.Warn("'old' key not found in dataMap")
							}
						}
					}
				}
			default:
				slog.Warn("Unknown type for evidence item", "type", fmt.Sprintf("%T", v))
			}
		}
	}
	if oldYaml != "" {
		var yamlMap map[string]any
		err := yaml.Unmarshal([]byte(oldYaml), &yamlMap)
		if err != nil {
			return map[string]any{}, common.ErrorBadRequest("container_name not found")
		}

		return map[string]any{
			"name":                           resourceKeys[2],
			"namespace":                      r.SubjectNamespace,
			"kind":                           resourceKeys[1],
			strings.ToLower(resourceKeys[1]): yamlMap}, nil
	}
	return map[string]any{}, nil
}

func hasInProgressRecommendation(existingRecommendations []models.EventResolution) (bool, string) {
	recommendationResolutionId := common.GenerateUUID()
	for _, resolution := range existingRecommendations {
		if resolution.Status == models.RecommendationResolutionStatusInProgress {
			return true, resolution.Id
		}
	}
	return false, recommendationResolutionId
}

func UpdateResolutionStatus(ctx *security.RequestContext) error {
	t0 := time.Now()
	defer func() {
		ctx.GetLogger().Info("UpdateResolutionStatus", "time", time.Since(t0))
	}()
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		ctx.GetLogger().Error("error getting database manager", "error", err)
		return err
	}

	// only check resolutions that are in progress and are user resolutions
	rows, err := dbms.Db.Queryx("select * from event_resolution where status = $1 and resolver_type = $2", models.RecommendationResolutionStatusInProgress, models.RecommendationResolutionResolverTypeUser)
	if err != nil {
		ctx.GetLogger().Error("error getting event resolutions", "error", err)
		return err
	}
	defer func() {
		err := rows.Close()
		if err != nil {
			ctx.GetLogger().Error("error closing rows", "error", err)
		}

	}()

	eventResolutionsToCheck := make([]models.EventResolution, 0)
	events := make(map[string]models.Event)
	eventIds := make([]any, 0)

	for rows.Next() {
		resolution := models.EventResolution{}
		err = rows.StructScan(&resolution)
		if err != nil {
			ctx.GetLogger().Error("error scanning event resolutions", "error", err)
			return err
		}
		eventResolutionsToCheck = append(eventResolutionsToCheck, resolution)
		eventIds = append(eventIds, resolution.EventId)
	}

	if len(eventResolutionsToCheck) == 0 {
		return nil
	}

	rows1, err := dbms.Query("select * from events where id in (?)", eventIds)
	if err != nil {
		ctx.GetLogger().Error("error getting events", "error", err)
		return err
	}
	defer func() {
		err := rows1.Close()
		if err != nil {
			ctx.GetLogger().Error("error closing rows", "error", err)
		}
	}()

	for rows1.Next() {
		event := models.Event{}
		err = rows1.StructScan(&event)
		if err != nil {
			ctx.GetLogger().Error("error scanning events", "error", err)
			return err
		}
		events[event.Id] = event
	}

	ctx.GetLogger().Info("Checking resolutions", "resolutions", len(eventResolutionsToCheck))

	for _, resolution := range eventResolutionsToCheck {
		event := events[resolution.EventId]
		if event.Id == "" {
			ctx.GetLogger().Error("error getting event", "error", fmt.Errorf("event not found - %s", resolution.EventId))
			continue
		}
		adptr := adapter.GetAdapterFromResolutionProvider(resolution.Type)
		if adptr == nil {
			ctx.GetLogger().Error("error getting adapter", "error", fmt.Errorf("adapter not found - %s", resolution.Type))
			continue
		}

		adapterContext := security.NewRequestContext(ctx.GetContext(), security.NewSecurityContextForSuperAdminAndTenant(*event.Tenant), ctx.GetLogger(), ctx.GetTracer(), ctx.GetMeter())
		resp, err := adptr.GetRecommendationResolutionStatus(adapterContext, models.Recommendation{
			Category:       "RightSizing",
			RuleName:       "pod_right_sizing",
			Id:             event.Id,
			CloudAccountId: *event.CloudAccountId,
			TenantId:       *event.Tenant,
			Recommendation: models.NewJsonObject(map[string]any{}),
		}, resolution.TypeReferenceId, resolution.Data, *resolution.StatusMessage)
		var status models.RecommendationResolutionStatus
		var statusMsg string
		if err != nil {
			ctx.GetLogger().Error("error getting event resolution status", "error", err)
			statusMsg = fmt.Sprintf("error getting event resolution status - %s", err.Error())
			status = models.RecommendationResolutionStatusFailed
		} else {
			status = models.RecommendationResolutionStatus(string(resp.Status))
			statusMsg = resp.StatusMessage
		}

		_, err = dbms.Db.Exec("UPDATE event_resolution SET status = $2, updated_at = $3, status_message = $4 WHERE id = $1", resolution.Id, status, time.Now().Format(time.RFC3339), statusMsg)
		if err != nil {
			ctx.GetLogger().Error("error updating event resolution", "error", err)
			return err
		}

		if status == models.RecommendationResolutionStatusSuccess {
			_, err = dbms.Db.Exec("UPDATE events SET status = $3, updated_at = $2 WHERE id = $1", event.Id, time.Now().Format(time.RFC3339), "RESOLVED")
			if err != nil {
				ctx.GetLogger().Error("error closing recommendation", "error", err, "event_id", event.Id, "status", status)
				return common.ErrorInternal(fmt.Sprintf("error closing recommendation: %s", err.Error()))
			}
		}
	}

	return nil
}

// maxSubjectNameBytes is the maximum byte length for subject_name.
// PostgreSQL B-tree index entries are limited to ~2730 bytes (8KB page / 3).
// We cap at 2048 bytes to leave comfortable headroom.
const maxSubjectNameBytes = 2048

// truncateStringToMaxBytes truncates s so that its UTF-8 byte length does not
// exceed maxBytes, cutting on a valid UTF-8 character boundary.
func truncateStringToMaxBytes(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	// Walk backwards from maxBytes to find the leading byte of the last rune
	// that may have been split by the cut.
	i := maxBytes
	for i > 0 && s[i-1]&0xC0 == 0x80 {
		i--
	}
	if i == 0 {
		return ""
	}
	// s[i-1] is either ASCII or a multi-byte leading byte.
	b := s[i-1]
	if b&0x80 == 0 {
		// ASCII byte — the rune is complete, everything up to maxBytes is valid.
		return s[:maxBytes]
	}
	// Multi-byte leading byte: determine the expected rune length.
	var charLen int
	switch {
	case b&0xE0 == 0xC0:
		charLen = 2
	case b&0xF0 == 0xE0:
		charLen = 3
	case b&0xF8 == 0xF0:
		charLen = 4
	}
	// Check if the full rune fits within maxBytes.
	if (i-1)+charLen <= maxBytes {
		return s[:maxBytes]
	}
	// The rune is split — exclude it entirely.
	return s[:i-1]
}

func InsertEvent(event Event, id string) (string, error) {
	// Validate required UUID fields to prevent DB errors from invalid values
	if event.Tenant == "" {
		return "", fmt.Errorf("event: tenant is required")
	}
	if _, err := uuid.Parse(event.Tenant); err != nil {
		return "", fmt.Errorf("event: tenant is not a valid UUID: %s", event.Tenant)
	}
	if event.AccountId == "" {
		return "", fmt.Errorf("event: account_id is required")
	}
	if _, err := uuid.Parse(event.AccountId); err != nil {
		return "", fmt.Errorf("event: account_id is not a valid UUID: %s", event.AccountId)
	}

	event.SubjectType = strings.ToLower(event.SubjectType)
	event.SubjectName = truncateStringToMaxBytes(event.SubjectName, maxSubjectNameBytes)

	query := `INSERT INTO events ("id", "finding_id", "title", "description", "source", "aggregation_key", "priority", "subject_type", "subject_name", "subject_namespace", "subject_node", "service_key", "fingerprint", "evidences", "tenant", "cloud_account_id", "status", "finding_type","cluster", "starts_at", "labels", "updated_at", "ends_at", "cloud_resource_id", "category", "created_at", "urgency", "subject_owner", "subject_owner_kind")
			 VALUES($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25, $26, $27, $28, $29)
			 ON CONFLICT (tenant, cloud_account_id, finding_id)
			 DO UPDATE SET
			 	updated_at = EXCLUDED.updated_at
				, title = EXCLUDED.title
				, subject_type = EXCLUDED.subject_type
				, subject_node = EXCLUDED.subject_node
				, subject_namespace = EXCLUDED.subject_namespace
				, subject_name = EXCLUDED.subject_name
				, status = EXCLUDED.status
				, starts_at = EXCLUDED.starts_at
				, source = EXCLUDED.source
				, service_key = EXCLUDED.service_key
				, priority = EXCLUDED.priority
				, fingerprint = EXCLUDED.fingerprint
				, finding_type = EXCLUDED.finding_type
				, evidences = EXCLUDED.evidences
				, ends_at = EXCLUDED.ends_at
				, description = EXCLUDED.description
				, cluster = EXCLUDED.cluster
				, cloud_resource_id = EXCLUDED.cloud_resource_id
				, category = EXCLUDED.category
				, aggregation_key = EXCLUDED.aggregation_key
				, principal = EXCLUDED.principal
				, urgency = EXCLUDED.urgency
				, subject_owner = EXCLUDED.subject_owner
				, subject_owner_kind = EXCLUDED.subject_owner_kind
			 RETURNING (xmax = 0) AS is_insert`

	if id == "" {
		id = common.GenerateUUID()
	}
	// convert evidence to json string
	evidences, err := common.MarshalJson(event.Evidences)
	if err != nil {
		slog.Error("event: failed to marshal evidences", "error", err)
		return "", fmt.Errorf("event: failed to marshal evidences: %w", err)
	}
	// convert evidence to json string
	labels, err := common.MarshalJson(event.Labels)
	if err != nil {
		slog.Error("event: failed to marshal labels", "error", err)
		return "", fmt.Errorf("event: failed to marshal labels: %w", err)
	}
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		slog.Error("integrations: unable to process request", "error", err)
		return "", err
	}

	nullableCloudResourceId := sql.NullString{
		String: event.CloudResourceId,
		Valid:  event.CloudResourceId != "",
	}

	if event.CreatedAt == nil {
		t0 := time.Now().UTC()
		event.CreatedAt = &t0
	}

	// Default urgency to LOW if not specified
	if event.Urgency == "" {
		event.Urgency = "LOW"
	}

	var isInsert bool
	err = dbms.Db.QueryRow(query, id, event.FindingId, event.Title, event.Description, event.Source, event.AggregationKey, event.Priority, event.SubjectType, event.SubjectName, event.SubjectNamespace, event.SubjectNode, event.ServiceKey, event.Fingerprint, evidences, event.Tenant, event.AccountId, event.Status, event.FindingType, event.Cluster, event.StartsAt, labels, time.Now().UTC(), event.EndsAt, nullableCloudResourceId, event.Category, event.CreatedAt, event.Urgency, event.SubjectOwner, event.SubjectOwnerKind).Scan(&isInsert)
	if err != nil {
		slog.Error("event: failed to insert event", "error", err)
		return id, err
	}

	// Publish new events for post-processing (replaces Hasura new_event_notification trigger)
	if isInsert {
		pubErr := common.MqPublish(
			config.Config.RabbitMqEventPostProcessExchange,
			config.Config.RabbitMqEventPostProcessQueue,
			map[string]string{"event_id": id},
			common.MqPublishWithExpiration(1*time.Hour),
		)
		if pubErr != nil {
			slog.Error("event: failed to publish event for post-processing", "error", pubErr, "event_id", id)
		}
	}

	return id, nil
}

var namespaceKeys = []string{"namespace", "subject_namespace", "k8s_namespace"}
var subjectKeys = []string{"subject", "subject_name", "pod", "pod_name", "aws_event_instance", "service_name", "service.name", "dimension_QueueName", "job", "job_name"}
var clusterKeys = []string{"cluster", "k8s_cluster"}
var endTimeKeys = []string{"end", "ends_at", "ended_at"}
var startTimeKeys = []string{"start", "starts_at", "started_at"}

func getPlaybookStartEndTime(e Event) (time.Time, time.Time) {
	var startTime, endTime time.Time
	if e.StartsAt != nil {
		startTime = *e.StartsAt
	} else {
		for _, s := range startTimeKeys {
			if val, ok := e.Labels[s]; ok && val != "" {
				startsAt1, err := common.ParseTimeValue(val)
				if err == nil {
					startTime = startsAt1
					break
				}
			}
		}
	}

	if e.EndsAt != nil {
		endTime = *e.EndsAt
	} else {
		for _, s := range endTimeKeys {
			if val, ok := e.Labels[s]; ok && val != "" {
				endsAt1, err := common.ParseTimeValue(val)
				if err == nil {
					endTime = endsAt1
					break
				}
			}
		}
	}

	if endTime.IsZero() && !startTime.IsZero() {
		endTime = startTime.Add(time.Hour)
	} else if endTime.IsZero() {
		endTime = time.Now()
	}

	if startTime.IsZero() {
		startTime = endTime.Add(-time.Hour)
	}
	// set start time as -10 minutes
	startTime = startTime.Add(-10 * time.Minute)

	return startTime.UTC(), endTime.UTC()
}

func InvestigateEvent(sc *security.RequestContext, webhookEvent Event, id string) (string, error) {
	start := time.Now().UTC()
	tenantId := sc.GetSecurityContext().GetTenantId()
	accountId := webhookEvent.AccountId
	eventSource := webhookEvent.Source
	aggregationKey := webhookEvent.AggregationKey

	// Debug logging for Azure Event Grid events
	sc.GetLogger().Info("InvestigateEvent: ENTRY",
		"id", id,
		"finding_id", webhookEvent.FindingId,
		"source", eventSource,
		"title", webhookEvent.Title,
		"tenant", tenantId,
		"account_id", accountId,
		"aggregation_key", aggregationKey)

	defer func() {
		duration := time.Since(start).Seconds()
		common.MetricsEventProcessingDuration(sc.GetContext(), duration, eventSource, tenantId, accountId, aggregationKey)
	}()

	if accountId == "" {
		sc.GetLogger().Error("InvestigateEvent: account ID is empty", "source", eventSource, "finding_id", webhookEvent.FindingId)
		common.MetricsEventProcessingFailed(sc.GetContext(), eventSource, tenantId, accountId, common.MetricReasonMissingAccountID)
		return "", errors.New("account ID is required")
	}

	if tenantId == "" {
		sc.GetLogger().Error("InvestigateEvent: tenant ID is empty", "source", eventSource, "finding_id", webhookEvent.FindingId, "account_id", accountId)
		common.MetricsEventProcessingFailed(sc.GetContext(), eventSource, tenantId, accountId, common.MetricReasonMissingTenantID)
		return "", errors.New("tenant ID is required")
	}

	if webhookEvent.EndsAt == nil {
		for _, s := range endTimeKeys {
			if webhookEvent.Labels[s] != "" {
				endsAt1, err := common.ParseTimeValue(webhookEvent.Labels[s])
				if err == nil {
					webhookEvent.EndsAt = &endsAt1
				}
			}
		}
	}

	if webhookEvent.StartsAt == nil {
		for _, s := range startTimeKeys {
			if webhookEvent.Labels[s] != "" {
				startsAt1, err := common.ParseTimeValue(webhookEvent.Labels[s])
				if err == nil {
					webhookEvent.StartsAt = &startsAt1
				}
			}
		}
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		sc.GetLogger().Error("event: unable to get database manager", "error", err)
		return "", err
	}

	var existingEventId string
	var existingSubjectName sql.NullString
	err = dbms.Db.QueryRowx("SELECT id, subject_name FROM events WHERE tenant = $1 AND cloud_account_id = $2 AND finding_id = $3", tenantId, accountId, webhookEvent.FindingId).Scan(&existingEventId, &existingSubjectName)

	if err == nil { // event exists
		sc.GetLogger().Info("event: already processed, updating event details", "event_id", existingEventId, "finding_id", webhookEvent.FindingId)
		labels, err1 := common.MarshalJson(webhookEvent.Labels)
		if err1 != nil {
			slog.Error("event: failed to marshal labels", "error", err1)
			return "", fmt.Errorf("event: failed to marshal labels: %w", err1)
		}
		nullableCloudResourceId := sql.NullString{
			String: webhookEvent.CloudResourceId,
			Valid:  webhookEvent.CloudResourceId != "",
		}
		// Do NOT overwrite evidences — playbook-collected evidences would be lost.
		// Duplicate webhooks carry the same inline evidences, so nothing new to store.
		_, err := dbms.Db.Exec("UPDATE events SET updated_at = $1, labels = $3, priority=$4, subject_name=$5, subject_namespace=$6, subject_type=$7, cloud_resource_id=$8, subject_owner=$9, subject_owner_kind=$10 WHERE id = $2", time.Now().UTC(), existingEventId, labels, webhookEvent.Priority, webhookEvent.SubjectName, webhookEvent.SubjectNamespace, webhookEvent.SubjectType, nullableCloudResourceId, webhookEvent.SubjectOwner, webhookEvent.SubjectOwnerKind)
		if err != nil {
			sc.GetLogger().Error("event: failed to update existing event", "error", err)
			return "", err
		}

		oldSubjectEmpty := !existingSubjectName.Valid || existingSubjectName.String == ""
		newSubjectResolved := webhookEvent.SubjectName != ""
		if oldSubjectEmpty && newSubjectResolved {
			sc.GetLogger().Info("event: subject_name resolved, triggering playbook re-run",
				"event_id", existingEventId, "new_subject_name", webhookEvent.SubjectName)
			go func() {
				if err := RefreshInvestigation(sc, existingEventId); err != nil {
					sc.GetLogger().Error("event: failed to refresh investigation after subject resolution",
						"event_id", existingEventId, "error", err)
				}
			}()
		}

		return existingEventId, nil
	} else if err == sql.ErrNoRows { // event does not exist, so process it
		var evidenceResponse []eventrule.PlaybookActionExecutionResponse

		// Snapshot original label keys so the subject/namespace/cluster fallback
		// only considers labels from the webhook payload, not labels injected by
		// playbook actions (e.g. prometheus_enricher metric labels like "pod", "job").
		// Action-extracted labels still flow into webhookEvent.Labels for persistence.
		originalLabelKeys := make(map[string]bool, len(webhookEvent.Labels))
		for k := range webhookEvent.Labels {
			originalLabelKeys[k] = true
		}

		// Skip playbook execution for configuration change events
		if webhookEvent.FindingType == "configuration_change" {
			sc.GetLogger().Info("event: skipping playbook execution for configuration change event", "event_id", id, "aggregation_key", webhookEvent.AggregationKey)
		} else {
			eventruleStart, eventruleEnd := getPlaybookStartEndTime(webhookEvent)

			// Use the actual alert name for playbook lookup instead of AggregationKey (which is the PagerDuty service name).
			// nb_alert_name is set during webhook enrichment and contains the correct alert name.
			playbookName := webhookEvent.AggregationKey
			if alertName, ok := webhookEvent.Labels["nb_alert_name"]; ok && alertName != "" {
				playbookName = alertName
			} else if alertName, ok := webhookEvent.Labels["alertname"]; ok && alertName != "" {
				playbookName = alertName
			}

			existingEvidenceActions := extractEvidenceActionNames(webhookEvent.Evidences)

			evidenceResponse, err = eventrule.ExecutePlaybook(sc, webhookEvent.AccountId, playbooks.PlaybookEvent{
				EventId:                 id,
				Name:                    playbookName,
				Labels:                  webhookEvent.Labels,
				Annotations:             map[string]string{},
				StartedAt:               &eventruleStart,
				EndedAt:                 &eventruleEnd,
				Source:                  webhookEvent.Source,
				SubjectName:             webhookEvent.SubjectName,
				SubjectType:             webhookEvent.SubjectType,
				SubjectOwner:            webhookEvent.SubjectOwner,
				SubjectNamespace:        webhookEvent.SubjectNamespace,
				AggregationKey:          webhookEvent.AggregationKey,
				ExistingEvidenceActions: existingEvidenceActions,
			})
			if err != nil {
				sc.GetLogger().Error("event: unable to process playbook for event", "error", err, "aggregation_key", webhookEvent.AggregationKey, "account_id", webhookEvent.AccountId, "event_id", webhookEvent.Fingerprint)
			}

			for _, e := range evidenceResponse {
				if e.Error != nil {
					common.MetricsPlaybookActionFailed(sc.GetContext(), e.ActionName, eventSource, tenantId, accountId, common.MetricReasonActionExecutionError)
				} else {
					common.MetricsPlaybookActionTotal(sc.GetContext(), e.ActionName, eventSource, tenantId, accountId, common.MetricStatusCompleted)
				}
				// Record individual action duration
				common.MetricsPlaybookActionDuration(sc.GetContext(), e.DurationSeconds, e.ActionName, eventSource, tenantId, accountId)

				if e.Response == nil {
					continue
				}
				structResponse, err := common.MarshalStructToMap(e.Response)
				if err != nil {
					common.MetricsPlaybookActionFailed(sc.GetContext(), e.ActionName, eventSource, tenantId, accountId, common.MetricReasonEvidenceMarshalFailed)
					webhookEvent.Evidences = append(webhookEvent.Evidences, e.Response)
					continue
				}
				structResponse["type"] = e.Response.GetFormatName()
				additionalInfo, ok := structResponse["additional_info"].(map[string]any)
				if !ok {
					additionalInfo = map[string]any{}
					structResponse["additional_info"] = additionalInfo
				}
				if e.ActionTitle != "" {
					additionalInfo["title"] = e.ActionTitle
				}
				// Only set action_name if not already set by the action response
				if _, exists := additionalInfo["action_name"]; !exists {
					additionalInfo["action_name"] = e.ActionName
				}

				webhookEvent.Evidences = append(webhookEvent.Evidences, structResponse)
			}
		}

		mergeAggregatedAlertLabels(webhookEvent.Labels, aggregationKey, evidenceResponse)
		linkCloudResourceId(&webhookEvent, evidenceResponse)

		if webhookEvent.Labels == nil {
			webhookEvent.Labels = map[string]string{}
		}

		if webhookEvent.Cluster == "" {
			for _, key := range clusterKeys {
				if cluster, ok := webhookEvent.Labels[key]; ok && originalLabelKeys[key] {
					webhookEvent.Cluster = cluster
					break
				}
			}
		}

		if webhookEvent.SubjectNamespace == "" {
			for _, key := range namespaceKeys {
				if namespace, ok := webhookEvent.Labels[key]; ok && originalLabelKeys[key] {
					webhookEvent.SubjectNamespace = namespace
					break
				}
			}
		}

		if webhookEvent.SubjectName == "" {
			for _, key := range subjectKeys {
				if subject, ok := webhookEvent.Labels[key]; ok && originalLabelKeys[key] {
					webhookEvent.SubjectName = subject
					break
				}
			}
		}

		if webhookEvent.CreatedAt == nil {
			webhookEvent.CreatedAt = &start
		}

		sc.GetLogger().Info("InvestigateEvent: calling InsertEvent",
			"id", id,
			"finding_id", webhookEvent.FindingId,
			"source", eventSource,
			"tenant", tenantId,
			"account_id", accountId)

		id, err := InsertEvent(webhookEvent, id)

		if err != nil {
			sc.GetLogger().Error("InvestigateEvent: InsertEvent failed",
				"error", err,
				"finding_id", webhookEvent.FindingId,
				"source", eventSource,
				"tenant", tenantId,
				"account_id", accountId)
			common.MetricsEventProcessingFailed(sc.GetContext(), eventSource, tenantId, accountId, common.MetricReasonEventInsertionFailed)
			return "", fmt.Errorf("event: failed to insert event: %w", err)
		}

		sc.GetLogger().Info("InvestigateEvent: successfully inserted event",
			"event_id", id,
			"finding_id", webhookEvent.FindingId,
			"source", eventSource,
			"tenant", tenantId,
			"account_id", accountId)
		common.MetricsEventProcessingTotal(sc.GetContext(), eventSource, tenantId, accountId, common.MetricStatusCompleted)
		return id, err
	} else { // some other db error
		sc.GetLogger().Error("event: failed to check for existing event", "error", err, "source", eventSource, "finding_id", webhookEvent.FindingId)
		return "", err
	}
}

func extractEvidenceActionNames(evidences []any) map[string]bool {
	actions := map[string]bool{}
	for _, e := range evidences {
		m, ok := e.(map[string]any)
		if !ok {
			continue
		}
		ai, ok := m["additional_info"].(map[string]any)
		if !ok {
			continue
		}
		if name, ok := ai["action_name"].(string); ok && name != "" {
			actions[name] = true
		}
		if name, ok := ai["actual_action_name"].(string); ok && name != "" {
			actions[name] = true
		}
	}
	return actions
}

func RefreshInvestigation(sc *security.RequestContext, eventId string) error {

	event, err := GetEvent(sc, eventId)
	if err != nil {
		return fmt.Errorf("failed to get event: %w", err)
	}
	if event.Id == "" {
		return fmt.Errorf("event not found: %s", eventId)
	}

	accountId := ""
	if event.CloudAccountId != nil {
		accountId = *event.CloudAccountId
	}

	if accountId == "" {
		return errors.New("account ID is not available in the event")
	}

	tenantId := ""
	if event.Tenant != nil {
		tenantId = *event.Tenant
	}

	if tenantId == "" {
		return errors.New("tenant ID is not available in the event")
	}

	// Skip playbook execution for configuration change events
	if event.FindingType != nil && *event.FindingType == "configuration_change" {
		sc.GetLogger().Info("event: skipping playbook refresh for configuration change event", "event_id", eventId)
		return nil
	}

	eventSc := security.NewRequestContext(sc.GetContext(), security.NewSecurityContextForSuperAdminAndTenant(tenantId), sc.GetLogger(), sc.GetTracer(), sc.GetMeter())

	labels := map[string]string{}
	if event.Labels.IsObject() {
		for k, v := range event.Labels.Object().(map[string]any) {
			labels[k] = v.(string)
		}
	}

	eventruleStart, eventruleEnd := getPlaybookStartEndTime(Event{
		StartsAt: event.StartsAt,
		EndsAt:   event.EndsAt,
		Labels:   labels,
	})

	playbookName := *event.AggregationKey
	if alertName, ok := labels["nb_alert_name"]; ok && alertName != "" {
		playbookName = alertName
	} else if alertName, ok := labels["alertname"]; ok && alertName != "" {
		playbookName = alertName
	}

	subjectOwner := ""
	if event.SubjectOwner != nil {
		subjectOwner = *event.SubjectOwner
	}

	subjectNamespace := ""
	if event.SubjectNamespace != nil {
		subjectNamespace = *event.SubjectNamespace
	}

	newEvidenceResponse, err := eventrule.ExecutePlaybook(eventSc, accountId, playbooks.PlaybookEvent{
		EventId:          eventId,
		Name:             playbookName,
		Labels:           labels,
		Annotations:      map[string]string{},
		StartedAt:        &eventruleStart,
		EndedAt:          &eventruleEnd,
		Source:           *event.Source,
		SubjectName:      *event.SubjectName,
		SubjectType:      *event.SubjectType,
		SubjectOwner:     subjectOwner,
		SubjectNamespace: subjectNamespace,
		AggregationKey:   *event.AggregationKey,
	})
	if err != nil {
		sc.GetLogger().Error("event: unable to process playbook for event", "error", err, "aggregation_key", *event.AggregationKey, "account_id", accountId, "event_id", event.Id)
	}

	labelsUpdated := mergeAggregatedAlertLabels(labels, *event.AggregationKey, newEvidenceResponse)

	newEvidences := []any{}
	for _, e := range newEvidenceResponse {
		structResponse := map[string]any{}
		if e.Response == nil {
			if e.Error != nil {
				structResponse["error"] = e.Error.Error()
			}
		} else {
			structResponse, err = common.MarshalStructToMap(e.Response)
			if err != nil {
				newEvidences = append(newEvidences, e.Response)
				continue
			}
			structResponse["type"] = e.Response.GetFormatName()
		}
		additionalInfo, ok := structResponse["additional_info"].(map[string]any)
		if !ok {
			additionalInfo = map[string]any{}
			structResponse["additional_info"] = additionalInfo
		}
		if e.ActionTitle != "" {
			additionalInfo["title"] = e.ActionTitle
		}
		if _, hasActionName := additionalInfo["action_name"]; !hasActionName {
			additionalInfo["action_name"] = e.ActionName
		}

		newEvidences = append(newEvidences, structResponse)
	}

	var existingEvidences []map[string]any
	if event.Evidences.IsArray() {
		for _, e := range event.Evidences.Array() {
			if evidence, ok := e.(map[string]any); ok {
				existingEvidences = append(existingEvidences, evidence)
			}
		}
	}

	sourceEvidenceActions := map[string]bool{
		"datadog_event":                true,
		"datadog_incident":             true,
		"datadog_event_details":        true,
		"datadog_traces":               true,
		"datadog_logs":                 true,
		"datadog_metrics":              true,
		"datadog_error_tracking_issue": true,
		"cloud_logs":                   true,
		"pagerduty_alert":              true,
		"newrelic_alert_details":       true,
		"newrelic_entity_details":      true,
	}

	retainedEvidences := []any{}
	for _, evidence := range existingEvidences {
		if additionalInfo, ok := evidence["additional_info"].(map[string]any); ok {
			if actionName, ok := additionalInfo["action_name"].(string); ok && (actionName == "webhook_event" || sourceEvidenceActions[actionName]) {
				retainedEvidences = append(retainedEvidences, evidence)
			} else if actionType, ok := additionalInfo["action_type"].(string); ok && (actionType == "event_detail") {
				retainedEvidences = append(retainedEvidences, evidence)
			}
		}
	}

	finalEvidences := append(retainedEvidences, newEvidences...)

	evidencesJson, err := common.MarshalJson(finalEvidences)
	if err != nil {
		return fmt.Errorf("failed to marshal final evidences: %w", err)
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return err
	}

	if labelsUpdated {
		labelsJson, err := common.MarshalJson(labels)
		if err != nil {
			return fmt.Errorf("failed to marshal updated labels: %w", err)
		}
		_, err = dbms.Db.Exec("UPDATE events SET evidences = $1, labels = $2, updated_at = $3 WHERE id = $4", evidencesJson, labelsJson, time.Now(), eventId)
		if err != nil {
			return fmt.Errorf("failed to update event: %w", err)
		}
	} else {
		_, err = dbms.Db.Exec("UPDATE events SET evidences = $1, updated_at = $2 WHERE id = $3", evidencesJson, time.Now(), eventId)
		if err != nil {
			return fmt.Errorf("failed to update event evidences: %w", err)
		}
	}

	return nil
}

// aggregatedAlertLabelDefs maps aggregation keys to the labels that should be
// extracted and the metric key used to match the event's series.
var aggregatedAlertLabelDefs = map[string]struct {
	labelsToExtract []string
	matchKey        string
}{
	"ApplicationAPIFailures": {
		labelsToExtract: []string{"path", "method", "status"},
		matchKey:        "destination_workload_name",
	},
	"HighErrorCriticalLogs": {
		labelsToExtract: []string{"container_id", "sample"},
		matchKey:        "app_id",
	},
}

// mergeAggregatedAlertLabels extracts per-series labels for aggregated alerts.
// It first checks for a dedicated aggregated_alert_label_enricher result;
// if that didn't run (e.g. instant query returned no data), it falls back to
// parsing the prometheus_enricher evidence and picking the matching series.
func mergeAggregatedAlertLabels(labels map[string]string, aggregationKey string, evidenceResponse []eventrule.PlaybookActionExecutionResponse) bool {
	// Try dedicated enricher first
	updated := false
	for _, e := range evidenceResponse {
		if e.Response == nil || e.ActionName != "aggregated_alert_label_enricher" {
			continue
		}
		if labelExtractor, ok := e.Response.(playbooks.PlaybookActionResponseLabelExtractor); ok {
			for k, v := range labelExtractor.ExtractLabels() {
				if vStr, ok := v.(string); ok {
					labels[k] = vStr
					updated = true
				}
			}
		}
	}
	if updated {
		return true
	}

	// Fallback: extract from prometheus_enricher evidence
	def, ok := aggregatedAlertLabelDefs[aggregationKey]
	if !ok {
		return false
	}
	matchValue := labels[def.matchKey]
	if matchValue == "" {
		return false
	}

	for _, e := range evidenceResponse {
		if e.Response == nil || e.ActionName != "prometheus_enricher" {
			continue
		}
		promResp, ok := e.Response.(playbooks.PrometheusActionResponse)
		if !ok {
			continue
		}
		topMetric := pickTopMatchingSeries(promResp.Data, def.matchKey, matchValue)
		if topMetric == nil {
			continue
		}
		for _, key := range def.labelsToExtract {
			if v, ok := topMetric[key].(string); ok {
				labels[key] = v
				updated = true
			}
		}
	}
	return updated
}

// linkCloudResourceId resolves the cloud_resource_id for an event by extracting
// resource_id/resource_arn from cloud_resource evidence and looking up the UUID
// in the cloud_resourses table.
func linkCloudResourceId(webhookEvent *Event, evidenceResponse []eventrule.PlaybookActionExecutionResponse) {
	if webhookEvent.CloudResourceId != "" || webhookEvent.AccountId == "" {
		return
	}
	for _, e := range evidenceResponse {
		if e.Response == nil {
			continue
		}
		if e.ActionName != "cloud_resource" && e.ActionName != "cloud_resources" {
			continue
		}
		labelExtractor, ok := e.Response.(playbooks.PlaybookActionResponseLabelExtractor)
		if !ok {
			continue
		}
		labels := labelExtractor.ExtractLabels()
		resourceId, _ := labels["resource_id"].(string)
		resourceArn, _ := labels["resource_arn"].(string)
		if resourceId == "" && resourceArn == "" {
			continue
		}
		dbms, err := database.GetDatabaseManager(database.Metastore)
		if err != nil {
			return
		}
		var cloudResourceUUID string
		err = dbms.Db.QueryRowx(
			"SELECT id FROM cloud_resourses WHERE account = $1 AND (resourse_id = $2 OR arn = $3) LIMIT 1",
			webhookEvent.AccountId, resourceId, resourceArn,
		).Scan(&cloudResourceUUID)
		if err == nil && cloudResourceUUID != "" {
			webhookEvent.CloudResourceId = cloudResourceUUID
		}
		return
	}
}

// pickTopMatchingSeries finds the series whose metric[matchKey]==matchValue
// and has the highest value, returning its metric labels.
func pickTopMatchingSeries(data map[string]any, matchKey, matchValue string) map[string]any {
	var seriesList []any

	// Direct series_list_result / vector_result (extractSeriesResponse mode)
	seriesList, _ = data["series_list_result"].([]any)
	if len(seriesList) == 0 {
		seriesList, _ = data["vector_result"].([]any)
	}
	// Wrapped under key "A"
	if len(seriesList) == 0 {
		if qr, ok := data["A"]; ok {
			switch q := qr.(type) {
			case []any:
				seriesList = q
			case map[string]any:
				seriesList, _ = q["series_list_result"].([]any)
				if len(seriesList) == 0 {
					seriesList, _ = q["vector_result"].([]any)
				}
			}
		}
	}
	if len(seriesList) == 0 {
		return nil
	}

	var topMetric map[string]any
	topValue := -1.0

	for _, item := range seriesList {
		series, ok := item.(map[string]any)
		if !ok {
			continue
		}
		metric, ok := series["metric"].(map[string]any)
		if !ok {
			continue
		}
		if mv, _ := metric[matchKey].(string); mv != matchValue {
			continue
		}

		// Instant query: value is [timestamp, "value_string"]
		if value, ok := series["value"].([]any); ok && len(value) >= 2 {
			if vStr, ok := value[1].(string); ok {
				if v, err := strconv.ParseFloat(vStr, 64); err == nil && v > topValue {
					topValue = v
					topMetric = metric
				}
			}
		}
		// Range query: values can be flat strings or nested [timestamp, value] pairs
		if values, ok := series["values"].([]any); ok && len(values) > 0 {
			lastVal := values[len(values)-1]
			var vStr string
			switch lv := lastVal.(type) {
			case string:
				vStr = lv
			case []any:
				if len(lv) >= 2 {
					vStr, _ = lv[1].(string)
				}
			}
			if vStr != "" {
				if v, err := strconv.ParseFloat(vStr, 64); err == nil && v > topValue {
					topValue = v
					topMetric = metric
				}
			}
		}
	}
	return topMetric
}

func processTriage(ctx *security.RequestContext, newEvent map[string]any) error {
	eventId, ok := newEvent["id"].(string)
	if !ok || eventId == "" {
		ctx.GetLogger().Error("triage: event ID not found")
		return nil
	}

	// Fetch the full event from database
	event, err := GetEvent(ctx, eventId)
	if err != nil {
		ctx.GetLogger().Error("triage: failed to fetch event", "error", err, "event_id", eventId)
		return nil // Don't fail the entire pipeline
	}

	// Get database connection
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		ctx.GetLogger().Error("triage: failed to get database manager", "error", err)
		return nil
	}

	// Process triage
	err = triage.ProcessEvent(ctx.GetContext(), dbms.Db, &event)
	if err != nil {
		ctx.GetLogger().Error("triage: processing failed", "error", err, "event_id", eventId)
		return nil // Don't fail the entire pipeline
	}

	// Propagate post-triage nb_status back into newEvent so downstream
	// processors (notably llm.ProcessEvent's suppressed-skip gate) see the
	// classification triage just applied. triage.ProcessEvent issues a SQL
	// UPDATE for nb_status without mutating the in-memory event struct, so
	// we re-read here. Combined with PostProcessEvent running triage before
	// other processors, this closes the same-batch race where llm could
	// publish before triage flipped nb_status to SUPPRESSED/DROPPED.
	var nbStatus sql.NullString
	if err := dbms.Db.GetContext(ctx.GetContext(), &nbStatus,
		`SELECT nb_status FROM events WHERE id = $1`, eventId); err != nil {
		ctx.GetLogger().Warn("triage: failed to refresh post-triage nb_status", "error", err, "event_id", eventId)
	} else if nbStatus.Valid {
		newEvent["nb_status"] = nbStatus.String
	}

	return nil
}

func processPagerDutyComment(ctx *security.RequestContext, newEvent map[string]any) error {
	tenantId, ok := newEvent["tenant"].(string)
	if !ok || tenantId == "" {
		ctx.GetLogger().Error("pagerduty_comment: tenant ID not found")
		return nil
	}

	source, ok := newEvent["source"].(string)
	if !ok || source != "pagerduty_webhook" {
		return nil
	}

	eventId, ok := newEvent["id"].(string)
	if !ok {
		ctx.GetLogger().Error("pagerduty_comment: event ID not found")
		return nil
	}

	findingId, ok := newEvent["finding_id"].(string)
	if !ok {
		ctx.GetLogger().Error("pagerduty_comment: finding ID not found")
		return nil
	}

	accountId, ok := newEvent["cloud_account_id"].(string)
	if !ok || accountId == "" {
		ctx.GetLogger().Error("pagerduty_comment: account ID not found")
		return nil
	}

	// Ensure security context has the tenant ID for downstream queries
	tenantCtx := security.NewRequestContext(
		ctx.GetContext(),
		security.NewSecurityContextForSuperAdminAndTenant(tenantId),
		ctx.GetLogger(), nil, nil,
	)

	// Check if allow_comments is enabled in PagerDuty integration config
	configs, err := integrationcore.ListIntegrationConfigs(tenantCtx, accountId, "pagerduty")
	if err != nil {
		ctx.GetLogger().Error("pagerduty_comment: failed to get integration configs", "error", err)
		return nil
	}

	allowComments := false
	for _, cfg := range configs {
		for _, v := range cfg.Configs {
			if v.Name == "allow_comments" && v.Value == "true" {
				allowComments = true
				break
			}
		}
		if allowComments {
			break
		}
	}
	if !allowComments {
		return nil
	}

	comment := fmt.Sprintf("To accelerate your investigation, Nudgebee has prepared an initial evidence report for you: %s/investigate?id=%s", config.Config.BaseUrl, eventId)

	ctx.GetLogger().Info("pagerduty_comment: adding comment to PagerDuty incident",
		"event_id", eventId,
		"incident_id", findingId,
		"account_id", accountId,
		"tenant_id", tenantId)

	// Call ticket-server directly instead of going through Hasura Action
	ticketUrl := config.Config.TicketServiceUrl + "/tickets/hasura/add-comment"
	resp, err := common.HttpPost(ticketUrl,
		common.HttpWithJsonBody(map[string]any{
			"action": map[string]any{"name": "ticket_add_comment"},
			"input": map[string]any{
				"object": map[string]any{
					"tenant":     tenantId,
					"account_id": accountId,
					"source":     "pagerduty",
					"ticket_id":  findingId,
					"comment":    comment,
				},
			},
			"session_variables": map[string]any{
				"x-hasura-role": "admin",
			},
		}),
		common.HttpWithHeaders(map[string]string{
			"Content-Type": "application/json",
		}),
	)
	if err != nil {
		ctx.GetLogger().Error("pagerduty_comment: failed to add comment", "error", err)
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		ctx.GetLogger().Error("pagerduty_comment: ticket-server returned non-200", "status", resp.StatusCode)
		return fmt.Errorf("ticket-server returned status %d", resp.StatusCode)
	}

	ctx.GetLogger().Info("pagerduty_comment: successfully added comment to PagerDuty incident")
	return nil
}

var eventProcessors = map[string]func(ctx *security.RequestContext, newEvent map[string]any) (err error){
	"triage":            processTriage,
	"notification":      notification.ProcessEvent,
	"llm":               llm.ProcessEvent,
	"pagerduty_comment": processPagerDutyComment,
	"workflow":          workflow.ProcessEvent,
	// "autopilot":    autopilot.ProcessEvent,
}

// RegisterEventProcessor allows dynamic registration of event processors
func RegisterEventProcessor(name string, processor func(ctx *security.RequestContext, newEvent map[string]any) (err error)) {
	eventProcessors[name] = processor
}

func PostProcessEvent(ctx *security.RequestContext, newEvent map[string]any) {

	if newEvent["priority"] == nil {
		newEvent["priority"] = EventPriortiyInfo
	}

	// use string as rest of the system is assuming this to be string
	if priorityE, ok := newEvent["priority"].(EventPriortiy); ok {
		newEvent["priority"] = string(priorityE)
	}

	// Triage must run before other processors so its classification
	// (nb_status etc.) is visible in newEvent to downstream gates such as
	// llm.ProcessEvent's suppressed-skip check. Map iteration order is
	// non-deterministic, so without this, llm/notification/workflow may
	// run before triage updates the DB and observe stale nb_status.
	if triageFn, ok := eventProcessors["triage"]; ok {
		if err := triageFn(ctx, newEvent); err != nil {
			ctx.GetLogger().Error("Error processing event by", "processor", "triage", "error", err)
		}
	}

	for name, processor := range eventProcessors {
		if name == "triage" {
			continue
		}
		p1 := processor
		err := p1(ctx, newEvent)
		if err != nil {
			ctx.GetLogger().Error("Error processing event by", "processor", name, "error", err)
		}
	}
}

func UpdateEvent(ctx *security.RequestContext, request models.UpdateEventRequest) (models.Event, error) {
	r, err := GetEvent(ctx, request.EventId)
	if err != nil {
		ctx.GetLogger().Error("services.UpdateEvent error getting events", "error", err)
		return models.Event{}, err
	}
	if request.Urgency != "" {
		r.Urgency = &request.Urgency
	}

	oldSubjectName := common.StrVal(r.SubjectName)
	subjectName := common.StrVal(r.SubjectName)
	subjectNamespace := common.StrVal(r.SubjectNamespace)
	subjectType := common.StrVal(r.SubjectType)

	if request.SubjectName != "" {
		subjectName = request.SubjectName
	}
	if request.SubjectNamespace != "" {
		subjectNamespace = request.SubjectNamespace
	}
	if request.SubjectType != "" {
		subjectType = request.SubjectType
	}

	now := time.Now().UTC()
	_, err1 := InsertEvent(Event{
		Urgency:          *r.Urgency,
		AccountId:        *r.CloudAccountId,
		FindingId:        *r.FindingId,
		Title:            r.Title,
		Description:      *r.Description,
		Source:           *r.Source,
		AggregationKey:   *r.AggregationKey,
		Priority:         EventPriortiy(*r.Priority),
		SubjectType:      subjectType,
		SubjectName:      subjectName,
		SubjectNamespace: subjectNamespace,
		SubjectNode:      common.StrVal(r.SubjectNode),
		ServiceKey:       *r.ServiceKey,
		Fingerprint:      *r.Fingerprint,
		Evidences:        r.Evidences.Array(),
		Tenant:           *r.Tenant,
		Status:           EventStatus(*r.Status),
		FindingType:      *r.FindingType,
		Cluster:          *r.Cluster,
		StartsAt:         r.StartsAt,
		EndsAt:           r.EndsAt,
		Category:         common.StrVal(r.Category),
		CreatedAt:        r.CreatedAt,
		CloudResourceId:  common.StrVal(r.CloudResourceId),
		Labels:           common.JsonToStringMap(r.Labels),
		UpdatedAt:        &now,
		SubjectOwner:     common.StrVal(r.SubjectOwner),
		SubjectOwnerKind: common.StrVal(r.SubjectOwnerKind),
	}, request.EventId)

	if err1 != nil {
		return models.Event{}, fmt.Errorf("event: failed to insert event: %w", err1)
	}

	if oldSubjectName == "" && subjectName != "" {
		ctx.GetLogger().Info("event: subject_name resolved via update, triggering playbook re-run",
			"event_id", request.EventId, "new_subject_name", subjectName)
		go func() {
			if err := RefreshInvestigation(ctx, request.EventId); err != nil {
				ctx.GetLogger().Error("event: failed to refresh investigation after subject resolution",
					"event_id", request.EventId, "error", err)
			}
		}()
	}

	return r, nil
}
