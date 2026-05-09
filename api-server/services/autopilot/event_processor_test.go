package autopilot

import (
	"fmt"
	"nudgebee/services/common"
	"nudgebee/services/security"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

var sampleData = `
---
apiVersion: apps/v1
kind: Deployment
metadata:
  annotations: {deployment.kubernetes.io/revision: '57', meta.helm.sh/release-name: app-dev,
    meta.helm.sh/release-namespace: nudgebee, workloads.nudgebee.com/app.cpu_request: '0.1',
    workloads.nudgebee.com/app.memory_limit: 362Mi, workloads.nudgebee.com/app.memory_request: 362Mi,
    workloads.nudgebee.com/app.prev_cpu_request: '0.1', workloads.nudgebee.com/app.prev_memory_limit: '524288000.0',
    workloads.nudgebee.com/app.prev_memory_request: '262144000.0', workloads.nudgebee.com/autopilot.id: 427aff9c-0266-4002-bb04-319106391f68,
    workloads.nudgebee.com/autopilot.service: 'AutoOptimise', workloads.nudgebee.com/autopilot.autoOptimize.category: 'vertical_rightsize',
	workloads.nudgebee.com/autopilot.autoOptimize.dryRun: 'false', 
	workloads.nudgebee.com/autopilot.autoOptimize.schedule.frequency: '0 * * * *',
	workloads.nudgebee.com/autopilot.autoOptimize.autopilot_config.vertical_rightsize.cpu.algo: 'max',
	workloads.nudgebee.com/autopilot.autoOptimize.autopilot_config.vertical_rightsize.memory.algo: 'max',
	}
  creationTimestamp: '2024-02-17T17:07:17Z'
  labels: {app.kubernetes.io/instance: app-dev, app.kubernetes.io/managed-by: Helm,
    app.kubernetes.io/name: app, app.kubernetes.io/version: V0.1, helm.sh/chart: app-0.1}
  name: app-dev
  namespace: nudgebee
  uid: 8eef06fc-dc0e-42d8-89c9-abe7bada64e3
spec:
  progressDeadlineSeconds: 600
  revisionHistoryLimit: 10
  selector:
    matchLabels: {app.kubernetes.io/instance: app-dev, app.kubernetes.io/name: app}
  strategy:
    rollingUpdate: {maxSurge: 25%, maxUnavailable: 25%}
    type: RollingUpdate
  template:
    metadata:
      labels: {app.kubernetes.io/instance: app-dev, app.kubernetes.io/name: app}
    spec:
      affinity:
        nodeAffinity:
          requiredDuringSchedulingIgnoredDuringExecution:
            nodeSelectorTerms:
            - matchExpressions:
              - key: node
                operator: In
                values: [app, db]
        podAntiAffinity:
          requiredDuringSchedulingIgnoredDuringExecution:
          - labelSelector:
              matchExpressions:
              - key: app.kubernetes.io/instance
                operator: In
                values: [app-dev]
            topologyKey: kubernetes.io/hostname
      containers:
      - envFrom:
        - secretRef: {name: nudgebee}
        image: 280501305789.dkr.ecr.us-east-1.amazonaws.com/nudgebee-app:2024-02-24T07-24-07_df4bb35538c1c950dec01f9bcd6cb2e9502d7eb5
        imagePullPolicy: IfNotPresent
        name: app
        ports:
        - {containerPort: 3000, name: http, protocol: TCP}
        resources:
          limits: {memory: 500Mi}
          requests: {cpu: 100m, memory: 250Mi}
        securityContext: {}
        terminationMessagePath: /dev/termination-log
        terminationMessagePolicy: File
      dnsPolicy: ClusterFirst
      restartPolicy: Always
      schedulerName: default-scheduler
      securityContext: {}
      serviceAccount: default
      serviceAccountName: default
      terminationGracePeriodSeconds: 30
      tolerations:
      - {effect: NoSchedule, key: node, operator: Equal, value: db}
`

// generate tests for getAutoOptimizeAnnotation
func TestGetAutoOptimizeAnnotation(t *testing.T) {
	// test case 1
	newYaml, err := common.UnmarshalYamlToMap(sampleData)
	if err != nil {
		t.Errorf("error decoding yaml: %v", err)
	}

	ctx := security.RequestContext{}

	autoOptimizeAnnotation := getAutoOptimizeAnnotation(&ctx, newYaml)
	assert.Equal(t, 5, len(autoOptimizeAnnotation))
	request, err := generateAutopilotRequestFromAnnotations(&ctx, autoOptimizeAnnotation, newYaml)
	assert.Nil(t, err)
	assert.NotNil(t, request)
	assert.NotNil(t, request.Category)
	assert.NotNil(t, request.Notification)
	assert.Equal(t, request.ResourceFilter.Namespace, "nudgebee")
	assert.Equal(t, request.ResourceFilter.Name, "app-dev")
	assert.NotNil(t, request.AutopilotConfig)
	assert.NotNil(t, request.AutopilotConfig.VerticalRightSize)
	assert.NotNil(t, request.AutopilotConfig.VerticalRightSize.Cpu)
	assert.Equal(t, request.AutopilotConfig.VerticalRightSize.Cpu.Algo, "max")
	assert.Equal(t, request.AutopilotConfig.VerticalRightSize.Cpu.BufferPct, 10)
	assert.NotNil(t, request.AutopilotConfig.VerticalRightSize.Memory.Algo, "max")
	assert.Equal(t, request.AutopilotConfig.VerticalRightSize.Memory.BufferPct, 10)
	assert.Nil(t, request.AutopilotConfig.HorizontalRightSize)
	assert.NotNil(t, request.Notification)
	assert.Equal(t, request.Notification.Slack.Enabled, true)
	assert.Equal(t, request.Notification.Email.Enabled, true)
	assert.Equal(t, *(request.Schedule.Frequency), "0 * * * *")
	assert.Equal(t, request.DryRun, false)
}

func TestUUID(t *testing.T) {
	uuid1 := uuid.MustParse("00000000-0000-0000-0000-000000000000")
	uuid2 := uuid.MustParse("FFFFFFFF-FFFF-FFFF-FFFF-FFFFFFFFFFFF")

	fmt.Println(uuid1, uuid2)
}
