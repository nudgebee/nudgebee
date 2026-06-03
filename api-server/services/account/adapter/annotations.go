package adapter

import (
	"fmt"
	"time"

	"nudgebee/services/internal/annotations"
)

func getAnnotationDict(containerName string, valuesMap map[string]any) map[string]string {
	finalAnnotation := make(map[string]string)
	for k, value := range valuesMap {
		var key string
		if containerName != "" {
			key = annotations.WorkloadKey(containerName + "." + k)
		} else {
			key = annotations.WorkloadKey(k)
		}
		if value != nil {
			finalAnnotation[key] = fmt.Sprintf("%v", value)
		} else {
			finalAnnotation[key] = ""
		}
	}
	return finalAnnotation
}

func getDefaultAnnotations(recommendationId string, userId string) map[string]any {
	return map[string]any{
		annotations.WorkloadUserID:           userId,
		annotations.WorkloadRecommendationID: recommendationId,
		annotations.WorkloadTime:             time.Now().Format("2006-01-02 15:04"),
	}
}
