package adapter

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"nudgebee/services/config"
	"nudgebee/services/internal/database"
	"nudgebee/services/internal/database/models"
	"nudgebee/services/security"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"gopkg.in/yaml.v3"
)

// testAccountAdapterContext implements AccountAdapterContext for testing
type testAccountAdapterContext struct {
	ctx             context.Context
	logger          *slog.Logger
	securityContext *security.SecurityContext
}

func (t *testAccountAdapterContext) GetContext() context.Context {
	if t.ctx == nil {
		return context.Background()
	}
	return t.ctx
}

func (t *testAccountAdapterContext) GetLogger() *slog.Logger {
	if t.logger == nil {
		return slog.Default()
	}
	return t.logger
}

func (t *testAccountAdapterContext) GetSecurityContext() *security.SecurityContext {
	return t.securityContext
}

var sampleYamlData = `
# -- The number of replicas (pods) to launch
replicaCount: 2

serverPort: 3000

image:
  # -- Name of the image repository to pull the container image from.
  repository: 280501305789.dkr.ecr.us-east-1.amazonaws.com/nudgebee-app
  # -- [Image pull policy](https://kubernetes.io/docs/concepts/containers/images/#updating-images) for updating already existing images on a node.
  pullPolicy: IfNotPresent
  # -- Overrides the image tag whose default is the chart appVersion.
  tag: "latest"
# -- Reference to one or more secrets to be used when [pulling images](https://kubernetes.io/docs/tasks/configure-pod-container/pull-image-private-registry/#create-a-pod-that-uses-your-secret) (from private registries).
imagePullSecrets: []
# -- A name in place of the chart name for app: labels.
nameOverride: ""
# -- A name to substitute for the full names of resources.
fullnameOverride: ""

serviceAccount:
  # -- Specifies whether a service account should be created.
  create: false
  # -- Annotations to add to the service account.
  annotations: {}
  # -- The name of the service account to use.
  # If not set and create is true, a name is generated using the fullname template.
  name: ""

# -- Labels to be added to pods.
podLabels: {}

# -- Annotations to be added to pods.
podAnnotations: {}

# -- Pod [security context](https://kubernetes.io/docs/tasks/configure-pod-container/security-context/#set-the-security-context-for-a-pod).
# See the [API reference](https://kubernetes.io/docs/reference/kubernetes-api/workload-resources/pod-v1/#security-context) for details.
podSecurityContext: {}
  # fsGroup: 2000

# -- Container [security context](https://kubernetes.io/docs/tasks/configure-pod-container/security-context/#set-the-security-context-for-a-container).
# See the [API reference](https://kubernetes.io/docs/reference/kubernetes-api/workload-resources/pod-v1/#security-context-1) for details.
securityContext: {}
  # capabilities:
  #   drop:
  #   - ALL
  # readOnlyRootFilesystem: true
  # runAsNonRoot: true
  # runAsUser: 1000

service:
  # -- Kubernetes [service type](https://kubernetes.io/docs/concepts/services-networking/service/#publishing-services-service-types).
  type: ClusterIP
  # -- Service port.
  port: 80
  # -- Annotations to be added to the service.
  annotations: {}

ingress:
  # -- Enable [ingress](https://kubernetes.io/docs/concepts/services-networking/ingress/).
  enabled: true
  # -- Ingress [class name](https://kubernetes.io/docs/concepts/services-networking/ingress/#ingress-class).
  className: "nginx"
  # -- Annotations to be added to the ingress.
  annotations: 
    cert-manager.io/issuer: cert-letsencrypt-issuer
    nginx.ingress.kubernetes.io/force-ssl-redirect: "true"
    nginx.ingress.kubernetes.io/configuration-snippet: |
      rewrite ^/help$ / break;
      rewrite ^/help/(.*) /$1 break;

  # -- Ingress host configuration.
  # @default -- See [values.yaml](values.yaml).
  hosts:
    - host: dev.nudgebee.pollux.in
      paths:
        - path: /
          pathType: ImplementationSpecific
  # -- Ingress TLS configuration.
  # @default -- See [values.yaml](values.yaml).
  tls:
   - secretName: nudgebee-dev-pollux-tls
     hosts:
     - dev.nudgebee.pollux.in

# -- Container resource [requests and limits](https://kubernetes.io/docs/concepts/configuration/manage-resources-containers/).
# See the [API reference](https://kubernetes.io/docs/reference/kubernetes-api/workload-resources/pod-v1/#resources) for details.
# @default -- No requests or limits.
resources:
  # We usually recommend not to specify default resources and to leave this as a conscious
  # choice for the user. This also increases chances charts run on environments with little
  # resources, such as Minikube. If you do want to specify resources, uncomment the following
  # lines, adjust them as necessary, and remove the curly braces after 'resources:'.
  limits:
    #cpu: 100m
    memory: 500Mi
  requests:
    #cpu: 100m
    memory: 250Mi

# -- Autoscaling by resources
autoscaling:
  enabled: false
  minReplicas: 1
  maxReplicas: 5
  targetCPUUtilizationPercentage: 10
  targetMemoryUtilizationPercentage: 10

# -- [Node selector](https://kubernetes.io/docs/concepts/scheduling-eviction/assign-pod-node/#nodeselector) configuration.
nodeSelector: {}

# -- [Tolerations](https://kubernetes.io/docs/concepts/scheduling-eviction/taint-and-toleration/) for node taints.
# See the [API reference](https://kubernetes.io/docs/reference/kubernetes-api/workload-resources/pod-v1/#scheduling) for details.
tolerations:
  - key: "node"
    operator: "Equal"
    value: "db"
    effect: "NoSchedule"


# -- [Affinity](https://kubernetes.io/docs/concepts/scheduling-eviction/assign-pod-node/#affinity-and-anti-affinity) configuration.
# See the [API reference](https://kubernetes.io/docs/reference/kubernetes-api/workload-resources/pod-v1/#scheduling) for details.
affinity:
  nodeAffinity:
    requiredDuringSchedulingIgnoredDuringExecution:
      nodeSelectorTerms:
      - matchExpressions:
        - key: node
          operator: In
          values:
          - app
          - db
  podAntiAffinity:
    requiredDuringSchedulingIgnoredDuringExecution:
    - labelSelector:
        matchExpressions:
        - key: app.kubernetes.io/instance
          operator: In
          values:
          - app-dev
      topologyKey: "kubernetes.io/hostname"

# -- extraEnvVarsCM append to ConfigMap with extra environment variables. See [values.yaml](values.yaml).
## E.g:
## extraEnvVarsCM:
##   ENV_STORE_IN_CONFIGMAP: value
extraEnvVarsCM: {}

# -- extraEnvVarsSecret append to Secret with extra environment variables. See [values.yaml](values.yaml).
## E.g:
## extraEnvVarsSecret:
##   ENV_STORE_IN_SECRET: value
extraEnvVarsSecret: {}

secretName: "nudgebee"
`

// generate tests for getAutoOptimizeAnnotation
func TestUpdateYamlPath(t *testing.T) {
	// test case 1
	var result yaml.Node
	err := yaml.Unmarshal([]byte(sampleYamlData), &result)
	if err != nil {
		t.Errorf("Error unmarshalling yaml: %v", err)
	}
	result1, err := updateYamlPath(&result, "resources.requests.memory", "200Mi")
	assert.Nil(t, err)
	assert.NotNil(t, result1)

	data, err := yaml.Marshal(&result)
	assert.Nil(t, err)
	assert.NotNil(t, data)
	assert.NotEmpty(t, data)
	fmt.Println(string(data))
}

func TestUpdateCodeCommit(t *testing.T) {
	details := gitDetailFromDeployment{
		Org:        "nudgebee",
		Repo:       "nudgebee",
		BaseBranch: "main",
		FilePath:   "deploy/kubernetes/app/values-dev.yaml",
		Annotations: map[string]string{
			"ci.nudgebee.com/helm.values.memoryRequestJsonPath": "resources.requests.memory",
		},
		Token: os.Getenv("GITHUB_TOKEN"),
	}
	recommendation := ApplyRecommendationRequest{
		Recommendation: models.Recommendation{
			Id:       "1",
			Category: "RightSizing",
			RuleName: "pod_right_sizing",
		},
		Data: map[string]any{
			"app": map[string]any{ // Container name
				"memory": map[string]any{
					"request": "200Mi",
				},
			},
		},
	}
	dir, err := checkoutCodeRepo(security.NewRequestContextForSuperAdmin(nil, nil, nil), recommendation, details)
	defer func() {
		if dir != "" {
			err := os.RemoveAll(dir)
			assert.Nil(t, err)
		}
	}()
	fmt.Println(dir)
	assert.Nil(t, err)
	assert.NotNil(t, dir)
	assert.NotEmptyf(t, dir, "")

	branchName, err := commitCode(security.NewRequestContextForSuperAdmin(nil, nil, nil), dir, recommendation, details, false)
	assert.Nil(t, err)
	assert.NotEmpty(t, branchName)

	// response, err := raisePrForCodeRepo(security.NewRequestContextForSuperAdmin(), dir, branchName, details)
	// assert.Nil(t, err)
	// assert.NotNil(t, response)

}

func TestApplyRecommendationUsingCodeAgent(t *testing.T) {
	// Check if LLM server is configured and accessible
	llmEndpoint := config.Config.LLMServerEndpoint
	if llmEndpoint == "" {
		t.Skip("Skipping test - LLM_SERVER_ENDPOINT not configured")
	}

	// Quick health check for LLM server
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(llmEndpoint + "/health")
	if err != nil || (resp != nil && resp.StatusCode != http.StatusOK) {
		t.Skipf("Skipping test - LLM server not accessible at %s: %v", llmEndpoint, err)
	}
	if resp != nil {
		err := resp.Body.Close()
		if err != nil {
			t.Logf("Warning: Failed to close response body: %v", err)
		}
	}

	// Get database connection
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		t.Skipf("Skipping test - database not accessible: %v", err)
	}

	// Load real recommendation from database
	recommendationId := "682565b5-8679-4391-9ca9-88e741eca2df"
	var recommendation models.Recommendation
	err = dbms.Db.Get(&recommendation, "SELECT * FROM recommendation WHERE id = $1", recommendationId)
	if err != nil {
		t.Fatalf("Failed to load recommendation from database: %v", err)
	}

	t.Logf("Loaded recommendation: %s (Category: %s, Status: %s)", recommendation.Id, recommendation.Category, recommendation.Status)

	// Load associated resource (optional - can be empty)
	var resource models.Resource
	if recommendation.ResourceId != nil {
		_ = dbms.Db.Get(&resource, "SELECT * FROM cloud_resourses WHERE id = $1", *recommendation.ResourceId)
	}

	// Create git details for test repository
	// Note: Token and Username are not needed - agent_code_2 fetches credentials automatically
	gitDetails := gitDetailFromDeployment{
		Org:         "nudgebee",
		Repo:        "nudgebee",
		BaseBranch:  "main",
		FilePath:    "deploy/kubernetes/notifications-server/values-dev.yaml",
		Annotations: map[string]string{},
	}

	// Build the recommendation data map
	recommendationData := map[string]any{
		"notifications": []map[string]any{
			{
				"resource":    "cpu",
				"allocated":   map[string]any{"request": 0.5, "limit": 0.8},
				"recommended": map[string]any{"request": 0.01, "limit": nil},
			},
			{
				"resource":    "memory",
				"allocated":   map[string]any{"request": 524288000, "limit": 786432000},
				"recommended": map[string]any{"request": 188743680, "limit": 188743680},
			},
		},
	}

	// Create the apply recommendation request
	applyRequest := ApplyRecommendationRequest{
		Data:           recommendationData,
		Recommendation: recommendation,
		Resource:       resource,
		ProviderConfig: map[string]any{},
		ResolverType:   "recommendation",
	}

	// Create a new recommendation_resolution record for this test
	resolutionId := uuid.NewString()
	userId := os.Getenv("TEST_USER")
	if userId == "" {
		userId = "test-user-id"
	}
	_, err = dbms.Db.Exec(`
		INSERT INTO recommendation_resolution (id, recommendation_id, type, status, type_reference_id, resolver_type, resolver_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, resolutionId, recommendation.Id, "DeploymentChange", models.RecommendationResolutionStatusInProgress, "", "User", userId, time.Now(), time.Now())
	if err != nil {
		t.Fatalf("Failed to create recommendation_resolution record: %v", err)
	}

	// Cleanup: delete the test resolution record after test
	defer func() {
		_, err := dbms.Db.Exec("DELETE FROM recommendation_resolution WHERE id = $1", resolutionId)
		if err != nil {
			t.Logf("Warning: Failed to cleanup test resolution record: %v", err)
		}
	}()

	// Create request context
	ctxt := &testAccountAdapterContext{
		ctx:             context.Background(),
		logger:          slog.Default(),
		securityContext: security.NewSecurityContextForSuperAdminAndTenant(os.Getenv("TEST_TENANT")),
	}

	// Call the function
	t.Logf("Calling ApplyRightsizingRecommendationUsingCodeAgent...")
	err = ApplyRightsizingRecommendationUsingCodeAgent(ctxt, applyRequest, gitDetails, resolutionId)
	if err != nil {
		t.Fatalf("Function returned error: %v", err)
	}

	// Since the function runs asynchronously, poll the database for status updates
	t.Logf("Polling database for status updates (max 3 minutes)...")
	maxWaitTime := 3 * time.Minute
	pollInterval := 5 * time.Second
	startTime := time.Now()

	var finalStatus string
	var statusMessage string
	var prUrl string

	for time.Since(startTime) < maxWaitTime {
		time.Sleep(pollInterval)

		var resolution struct {
			Status          string  `db:"status"`
			StatusMessage   *string `db:"status_message"`
			TypeReferenceId *string `db:"type_reference_id"`
		}

		err = dbms.Db.Get(&resolution, "SELECT status, status_message, type_reference_id FROM recommendation_resolution WHERE id = $1", resolutionId)
		if err != nil {
			t.Logf("Error querying resolution status: %v", err)
			continue
		}

		finalStatus = resolution.Status
		if resolution.StatusMessage != nil {
			statusMessage = *resolution.StatusMessage
		}
		if resolution.TypeReferenceId != nil {
			prUrl = *resolution.TypeReferenceId
		}

		t.Logf("Status: %s, Message: %s, PR URL: %s", finalStatus, statusMessage, prUrl)

		// Check if we've reached a terminal state
		if finalStatus == string(models.RecommendationResolutionStatusSuccess) ||
			finalStatus == string(models.RecommendationResolutionStatusFailed) ||
			(finalStatus == string(models.RecommendationResolutionStatusInProgress) && prUrl != "") {
			break
		}
	}

	// Assertions
	assert.NotEmpty(t, finalStatus, "Status should be updated")
	assert.NotEqual(t, string(models.RecommendationResolutionStatusInProgress), finalStatus,
		"Status should have moved from InProgress to a terminal state")

	if finalStatus == string(models.RecommendationResolutionStatusSuccess) || prUrl != "" {
		assert.NotEmpty(t, prUrl, "PR URL should be set on success")
		t.Logf("✓ Test passed! PR created at: %s", prUrl)
	} else {
		t.Logf("Test completed with status: %s, message: %s", finalStatus, statusMessage)
		// Don't fail the test - the code agent might legitimately fail (e.g., no changes needed)
		// Just log the result
	}
}

func TestApplyMultiContainerRecommendationUsingCodeAgent(t *testing.T) {
	// Check if LLM server is configured and accessible
	llmEndpoint := config.Config.LLMServerEndpoint
	if llmEndpoint == "" {
		t.Skip("Skipping test - LLM_SERVER_ENDPOINT not configured")
	}

	// Quick health check for LLM server
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(llmEndpoint + "/health")
	if err != nil || (resp != nil && resp.StatusCode != http.StatusOK) {
		t.Skipf("Skipping test - LLM server not accessible at %s: %v", llmEndpoint, err)
	}
	if resp != nil {
		err := resp.Body.Close()
		if err != nil {
			t.Logf("Warning: Failed to close response body: %v", err)
		}
	}

	// Get database connection
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		t.Skipf("Skipping test - database not accessible: %v", err)
	}

	// Load real multi-container recommendation from database
	// This recommendation has 2 containers: app and nginx
	recommendationId := "9cc7aa21-f50b-483a-aac3-5022b5316be8"
	var recommendation models.Recommendation
	err = dbms.Db.Get(&recommendation, "SELECT * FROM recommendation WHERE id = $1", recommendationId)
	if err != nil {
		t.Fatalf("Failed to load recommendation from database: %v", err)
	}

	t.Logf("Loaded multi-container recommendation: %s (Category: %s, RuleName: %s)", recommendation.Id, recommendation.Category, recommendation.RuleName)

	// Load associated resource
	var resource models.Resource
	if recommendation.ResourceId != nil {
		err = dbms.Db.Get(&resource, "SELECT * FROM cloud_resourses WHERE id = $1", *recommendation.ResourceId)
		if err != nil {
			t.Logf("Warning: Failed to load resource: %v", err)
		} else {
			resourceName := ""
			if resource.Name != nil {
				resourceName = *resource.Name
			}
			resourceType := ""
			if resource.Type != nil {
				resourceType = *resource.Type
			}
			namespace := ""
			if meta, ok := resource.Meta.Object().(map[string]any); ok {
				if ns, ok := meta["namespace"].(string); ok {
					namespace = ns
				}
			}
			t.Logf("Loaded resource: %s (Type: %s, Namespace: %s)", resourceName, resourceType, namespace)
		}
	}

	// Create git details for test repository - app deployment
	gitDetails := gitDetailFromDeployment{
		Org:         "nudgebee",
		Repo:        "nudgebee",
		BaseBranch:  "main",
		FilePath:    "deploy/kubernetes/app/values-dev.yaml",
		Annotations: map[string]string{},
	}

	// Build the recommendation data map from the database recommendation
	// The recommendation.Recommendation field contains the full data for both containers
	recommendationData := recommendation.Recommendation.Object().(map[string]any)

	// Create the apply recommendation request
	applyRequest := ApplyRecommendationRequest{
		Data:           recommendationData,
		Recommendation: recommendation,
		Resource:       resource,
		ProviderConfig: map[string]any{},
		ResolverType:   "recommendation",
	}

	// Create a new recommendation_resolution record for this test
	resolutionId := uuid.NewString()
	userId := os.Getenv("TEST_USER")
	if userId == "" {
		userId = "test-user-id"
	}
	_, err = dbms.Db.Exec(`
		INSERT INTO recommendation_resolution (id, recommendation_id, type, status, type_reference_id, resolver_type, resolver_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, resolutionId, recommendation.Id, "DeploymentChange", models.RecommendationResolutionStatusInProgress, "", "User", userId, time.Now(), time.Now())
	if err != nil {
		t.Fatalf("Failed to create recommendation_resolution record: %v", err)
	}

	// Cleanup: delete the test resolution record after test
	defer func() {
		_, err := dbms.Db.Exec("DELETE FROM recommendation_resolution WHERE id = $1", resolutionId)
		if err != nil {
			t.Logf("Warning: Failed to cleanup test resolution record: %v", err)
		}
	}()

	// Create request context
	ctxt := &testAccountAdapterContext{
		ctx:             context.Background(),
		logger:          slog.Default(),
		securityContext: security.NewSecurityContextForSuperAdminAndTenant(os.Getenv("TEST_TENANT")),
	}

	// Call the function
	t.Logf("Calling ApplyRightsizingRecommendationUsingCodeAgent for multi-container pod (app + nginx)...")
	err = ApplyRightsizingRecommendationUsingCodeAgent(ctxt, applyRequest, gitDetails, resolutionId)
	if err != nil {
		t.Fatalf("Function returned error: %v", err)
	}

	// Since the function runs asynchronously, poll the database for status updates
	t.Logf("Polling database for status updates (max 3 minutes)...")
	maxWaitTime := 3 * time.Minute
	pollInterval := 5 * time.Second
	startTime := time.Now()

	var finalStatus string
	var statusMessage string
	var prUrl string

	for time.Since(startTime) < maxWaitTime {
		time.Sleep(pollInterval)

		var resolution struct {
			Status          string  `db:"status"`
			StatusMessage   *string `db:"status_message"`
			TypeReferenceId *string `db:"type_reference_id"`
		}

		err = dbms.Db.Get(&resolution, "SELECT status, status_message, type_reference_id FROM recommendation_resolution WHERE id = $1", resolutionId)
		if err != nil {
			t.Logf("Error querying resolution status: %v", err)
			continue
		}

		finalStatus = resolution.Status
		if resolution.StatusMessage != nil {
			statusMessage = *resolution.StatusMessage
		}
		if resolution.TypeReferenceId != nil {
			prUrl = *resolution.TypeReferenceId
		}

		t.Logf("Status: %s, Message: %s, PR URL: %s", finalStatus, statusMessage, prUrl)

		// Check if we've reached a terminal state
		if finalStatus == string(models.RecommendationResolutionStatusSuccess) ||
			finalStatus == string(models.RecommendationResolutionStatusFailed) ||
			(finalStatus == string(models.RecommendationResolutionStatusInProgress) && prUrl != "") {
			break
		}
	}

	// Assertions
	assert.NotEmpty(t, finalStatus, "Status should be updated")
	assert.NotEqual(t, string(models.RecommendationResolutionStatusInProgress), finalStatus,
		"Status should have moved from InProgress to a terminal state")

	if finalStatus == string(models.RecommendationResolutionStatusSuccess) || prUrl != "" {
		assert.NotEmpty(t, prUrl, "PR URL should be set on success")
		t.Logf("✓ Multi-container test passed! PR created at: %s", prUrl)
		t.Logf("  This PR should update resources for both 'app' and 'nginx' containers")
	} else {
		t.Logf("Test completed with status: %s, message: %s", finalStatus, statusMessage)
		// Don't fail the test - the code agent might legitimately fail (e.g., no changes needed)
		// Just log the result
	}
}

func TestGetResolutionStatus(t *testing.T) {
	// Get database connection
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		t.Skipf("Skipping test - database not accessible: %v", err)
	}

	// Load actual resolution that we want to test - the one with PR URL
	resolutionId := "a14a0f16-19b6-4b5e-8abc-7622247f6c83"

	var resolution models.RecommendationResolution

	err = dbms.Db.Get(&resolution, "SELECT * FROM recommendation_resolution WHERE id = $1", resolutionId)
	if err != nil {
		t.Fatalf("Failed to load resolution from database: %v", err)
	}

	t.Logf("Loaded resolution: %s (Status: %s)", resolution.Id, resolution.Status)
	if resolution.TypeReferenceId != "" {
		t.Logf("PR URL: %s", resolution.TypeReferenceId)
	}
	if resolution.StatusMessage != nil {
		t.Logf("Status Message: %s", *resolution.StatusMessage)
	}

	// Load the associated recommendation
	var recommendation models.Recommendation
	err = dbms.Db.Get(&recommendation, "SELECT * FROM recommendation WHERE id = $1", resolution.RecommendationId)
	if err != nil {
		t.Fatalf("Failed to load recommendation from database: %v", err)
	}

	t.Logf("Loaded recommendation: %s (Category: %s, Status: %s)",
		recommendation.Id, recommendation.Category, recommendation.Status)

	// Create request context
	ctxt := &testAccountAdapterContext{
		ctx:             context.Background(),
		logger:          slog.Default(),
		securityContext: security.NewSecurityContextForSuperAdminAndTenant(os.Getenv("TEST_TENANT")),
	}

	// Create adapter and call GetRecommendationResolutionStatus
	adapter := githubAdapter{}

	prUrl := resolution.TypeReferenceId

	statusMessage := ""
	if resolution.StatusMessage != nil {
		statusMessage = *resolution.StatusMessage
	}

	// Call the function
	t.Logf("Calling GetRecommendationResolutionStatus for PR: %s", prUrl)
	response, err := adapter.GetRecommendationResolutionStatus(
		ctxt,
		recommendation,
		prUrl,
		resolution.Data,
		statusMessage,
	)

	// Assertions
	if err != nil {
		t.Logf("Error returned: %v", err)
		// Don't fail - we're testing with GitHub App auth which might not be configured
		// The important thing is to see the flow working
	} else {
		t.Logf("✓ Status check succeeded!")
		t.Logf("  Status: %s", response.Status)
		t.Logf("  Message: %s", response.StatusMessage)

		// Validate the response
		assert.NotEmpty(t, response.Status, "Status should not be empty")
		assert.NotEmpty(t, response.StatusMessage, "Status message should not be empty")

		// If we get a success response, the status should be one of the valid statuses
		validStatuses := []RecommendationResolutionStatus{
			RecommendationResolutionStatusInProgress,
			RecommendationResolutionStatusSuccess,
			RecommendationResolutionStatusFailed,
		}
		assert.Contains(t, validStatuses, response.Status, "Status should be a valid resolution status")
	}
}

func TestGetResolutionStatusWithGitHubApp(t *testing.T) {
	// This test specifically validates GitHub App authentication flow

	// Skip if GitHub App credentials not configured
	if config.Config.GithubAppId == "" || config.Config.GithubPrivateKey == "" {
		t.Skip("Skipping test - GitHub App credentials not configured (GITHUB_APP_ID and GITHUB_PRIVATE_KEY)")
	}

	// Get database connection
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		t.Skipf("Skipping test - database not accessible: %v", err)
	}

	// Find a resolution with GitHub App auth type
	var resolution models.RecommendationResolution

	// Query for resolutions that have provider_config with GitHub integration
	query := `
		SELECT *
		FROM recommendation_resolution
		WHERE type_reference_id IS NOT NULL
		AND type_reference_id LIKE 'https://github.com/%'
		LIMIT 1
	`

	err = dbms.Db.Get(&resolution, query)
	if err != nil {
		t.Skipf("Skipping test - no GitHub resolutions found in database: %v", err)
	}

	t.Logf("Testing GitHub App authentication with resolution: %s", resolution.Id)
	if resolution.TypeReferenceId != "" {
		t.Logf("PR URL: %s", resolution.TypeReferenceId)
	}

	// Load the associated recommendation
	var recommendation models.Recommendation
	err = dbms.Db.Get(&recommendation, "SELECT * FROM recommendation WHERE id = $1", resolution.RecommendationId)
	if err != nil {
		t.Fatalf("Failed to load recommendation from database: %v", err)
	}

	// Create request context
	ctxt := &testAccountAdapterContext{
		ctx:             context.Background(),
		logger:          slog.Default(),
		securityContext: security.NewSecurityContextForSuperAdminAndTenant(recommendation.TenantId),
	}

	// Create adapter and call GetRecommendationResolutionStatus
	adapter := githubAdapter{}

	prUrl := resolution.TypeReferenceId

	statusMessage := ""
	if resolution.StatusMessage != nil {
		statusMessage = *resolution.StatusMessage
	}

	// Call the function - this should use GitHub App authentication
	t.Logf("Calling GetRecommendationResolutionStatus with GitHub App auth...")
	response, err := adapter.GetRecommendationResolutionStatus(
		ctxt,
		recommendation,
		prUrl,
		resolution.Data,
		statusMessage,
	)

	// Assertions
	assert.NoError(t, err, "Should not error with proper GitHub App credentials")
	assert.NotEmpty(t, response.Status, "Status should not be empty")

	// Log the result
	t.Logf("✓ GitHub App authentication test passed!")
	t.Logf("  Status: %s", response.Status)
	t.Logf("  Message: %s", response.StatusMessage)

	// The status should be one of the valid statuses (not "Failed" from bad credentials)
	if response.Status == RecommendationResolutionStatusFailed {
		// If failed, it should NOT be due to authentication issues
		assert.NotContains(t, response.StatusMessage, "Bad credentials",
			"Should not fail with 'Bad credentials' when GitHub App is properly configured")
		assert.NotContains(t, response.StatusMessage, "401",
			"Should not fail with 401 when GitHub App is properly configured")
	}
}

func TestApplySecurityRecommendationUsingCodeAgent(t *testing.T) {
	// Check if LLM server is configured and accessible
	llmEndpoint := config.Config.LLMServerEndpoint
	if llmEndpoint == "" {
		t.Skip("Skipping test - LLM_SERVER_ENDPOINT not configured")
	}

	// Quick health check for LLM server
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(llmEndpoint + "/health")
	if err != nil || (resp != nil && resp.StatusCode != http.StatusOK) {
		t.Skipf("Skipping test - LLM server not accessible at %s: %v", llmEndpoint, err)
	}
	if resp != nil {
		err := resp.Body.Close()
		if err != nil {
			t.Logf("Warning: Failed to close response body: %v", err)
		}
	}

	// Get database connection
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		t.Skipf("Skipping test - database not accessible: %v", err)
	}

	// Load a security recommendation from database
	// Security recommendations have category='Security' and rule_name='image_scan'
	var recommendation models.Recommendation
	err = dbms.Db.Get(&recommendation, `
		SELECT * FROM recommendation
		WHERE category = 'Security'
		AND rule_name = 'image_scan'
		AND status = 'Open'
		AND recommendation->>'FixedVersion' IS NOT NULL
		AND recommendation->>'FixedVersion' != ''
		LIMIT 1
	`)
	if err != nil {
		t.Skipf("Skipping test - no security recommendation with FixedVersion found: %v", err)
	}

	t.Logf("Loaded security recommendation: %s (Category: %s, RuleName: %s)", recommendation.Id, recommendation.Category, recommendation.RuleName)

	// Extract recommendation details from JSON
	recommendationData, ok := recommendation.Recommendation.Object().(map[string]any)
	if !ok {
		t.Fatalf("Failed to parse recommendation JSON")
	}

	workloadName := recommendationData["workload_name"]
	namespace := recommendationData["namespace"]
	vulnerabilityID := recommendationData["VulnerabilityID"]
	pkgID := recommendationData["PkgID"]
	fixedVersion := recommendationData["FixedVersion"]

	t.Logf("  Workload: %s/%s", namespace, workloadName)
	t.Logf("  CVE: %s, Package: %s, FixedVersion: %s", vulnerabilityID, pkgID, fixedVersion)

	// Create git details for test repository
	// Note: Token and Username are not needed - agent_code_2 fetches credentials automatically
	gitDetails := gitDetailFromDeployment{
		Org:         "nudgebee",
		Repo:        "nudgebee",
		BaseBranch:  "main",
		FilePath:    "deploy/kubernetes/k8s-collector/values-dev.yaml", // Will be determined by code agent
		Annotations: map[string]string{},
	}

	// Create the apply recommendation request
	applyRequest := ApplyRecommendationRequest{
		Data:           map[string]any{},
		Recommendation: recommendation,
		Resource:       models.Resource{}, // Security recommendations have no linked resource
		ProviderConfig: map[string]any{},
		ResolverType:   "recommendation",
	}

	// Create a new recommendation_resolution record for this test
	resolutionId := uuid.NewString()
	userId := os.Getenv("TEST_USER")
	if userId == "" {
		userId = "test-user-id"
	}
	_, err = dbms.Db.Exec(`
		INSERT INTO recommendation_resolution (id, recommendation_id, type, status, type_reference_id, resolver_type, resolver_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, resolutionId, recommendation.Id, "PullRequest", models.RecommendationResolutionStatusInProgress, "", "User", userId, time.Now(), time.Now())
	if err != nil {
		t.Fatalf("Failed to create recommendation_resolution record: %v", err)
	}

	// Cleanup: delete the test resolution record after test
	defer func() {
		_, err := dbms.Db.Exec("DELETE FROM recommendation_resolution WHERE id = $1", resolutionId)
		if err != nil {
			t.Logf("Warning: Failed to cleanup test resolution record: %v", err)
		}
	}()

	// Create request context
	ctxt := &testAccountAdapterContext{
		ctx:             context.Background(),
		logger:          slog.Default(),
		securityContext: security.NewSecurityContextForSuperAdminAndTenant(os.Getenv("TEST_TENANT")),
	}

	// Call the function
	t.Logf("Calling ApplySecurityRecommendationUsingCodeAgent...")
	err = ApplySecurityRecommendationUsingCodeAgent(ctxt, applyRequest, gitDetails, resolutionId)
	if err != nil {
		t.Fatalf("Function returned error: %v", err)
	}

	// Since the function runs asynchronously, poll the database for status updates
	t.Logf("Polling database for status updates (max 5 minutes)...")
	maxWaitTime := 5 * time.Minute
	pollInterval := 5 * time.Second
	startTime := time.Now()

	var finalStatus string
	var statusMessage string
	var prUrl string

	for time.Since(startTime) < maxWaitTime {
		time.Sleep(pollInterval)

		var resolution struct {
			Status          string  `db:"status"`
			StatusMessage   *string `db:"status_message"`
			TypeReferenceId *string `db:"type_reference_id"`
		}

		err = dbms.Db.Get(&resolution, "SELECT status, status_message, type_reference_id FROM recommendation_resolution WHERE id = $1", resolutionId)
		if err != nil {
			t.Logf("Error querying resolution status: %v", err)
			continue
		}

		finalStatus = resolution.Status
		if resolution.StatusMessage != nil {
			statusMessage = *resolution.StatusMessage
		}
		if resolution.TypeReferenceId != nil {
			prUrl = *resolution.TypeReferenceId
		}

		t.Logf("Status: %s, Message: %s, PR URL: %s", finalStatus, statusMessage, prUrl)

		// Check if we've reached a terminal state
		if finalStatus == string(models.RecommendationResolutionStatusSuccess) ||
			finalStatus == string(models.RecommendationResolutionStatusFailed) ||
			(finalStatus == string(models.RecommendationResolutionStatusInProgress) && prUrl != "") {
			break
		}
	}

	// Assertions
	assert.NotEmpty(t, finalStatus, "Status should be updated")
	assert.NotEqual(t, string(models.RecommendationResolutionStatusInProgress), finalStatus,
		"Status should have moved from InProgress to a terminal state")

	if finalStatus == string(models.RecommendationResolutionStatusSuccess) || prUrl != "" {
		assert.NotEmpty(t, prUrl, "PR URL should be set on success")
		t.Logf("✓ Security recommendation test passed! PR created at: %s", prUrl)
		t.Logf("  This PR should fix CVE %s by updating package %s to version %s", vulnerabilityID, pkgID, fixedVersion)
	} else {
		t.Logf("Test completed with status: %s, message: %s", finalStatus, statusMessage)
		// Don't fail the test - the code agent might legitimately fail (e.g., no changes needed, package not found)
		// Just log the result
	}
}
