package adapter

import (
	"fmt"
	"time"
)

var baseKey = "workloads.nudgebee.com"

func getAnnotationDict(containerName string, valuesMap map[string]any) map[string]string {
	finalAnnotation := make(map[string]string)
	for k, value := range valuesMap {
		if containerName != "" {
			key := fmt.Sprintf("%s/%s.%s", baseKey, containerName, k)
			if value != nil {
				finalAnnotation[key] = fmt.Sprintf("%v", value)
			} else {
				finalAnnotation[key] = ""
			}
		} else {
			key := fmt.Sprintf("%s/%s", baseKey, k)
			if value != nil {
				finalAnnotation[key] = fmt.Sprintf("%v", value)
			} else {
				finalAnnotation[key] = ""
			}
		}
	}
	return finalAnnotation
}

func getDefaultAnnotations(recommendationId string, userId string) map[string]any {
	return map[string]any{
		fmt.Sprintf("%s/user.id", baseKey):           userId,
		fmt.Sprintf("%s/recommendation.id", baseKey): recommendationId,
		fmt.Sprintf("%s/time", baseKey):              time.Now().Format("2006-01-02 15:04"),
	}
}
