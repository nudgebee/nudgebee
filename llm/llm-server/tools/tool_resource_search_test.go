package tools

import (
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/tmc/langchaingo/llms"
)

func TestResourceSearchTool(t *testing.T) {

	tool := K8sResourceSearchTool{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases :=
		[]struct {
			SessionId  string
			Query      string
			AccountId  string
			UserId     string
			ToolConfig string
		}{
			{
				SessionId:  "ut-tool-chain-1",
				AccountId:  os.Getenv("TEST_ACCOUNT"),
				UserId:     os.Getenv("TEST_USER"),
				Query:      `{"resource_type": "podss", "search_type": "fuzzy"}`,
				ToolConfig: "",
			},
			{
				SessionId:  "ut-tool-chain-2",
				AccountId:  os.Getenv("TEST_ACCOUNT"),
				UserId:     os.Getenv("TEST_USER"),
				Query:      `{"resource_name": "llm-server", "namespace": "default", "search_type": "suggestions"}`,
				ToolConfig: "",
			},
			{
				SessionId:  "ut-tool-chain-3",
				AccountId:  os.Getenv("TEST_ACCOUNT"),
				UserId:     os.Getenv("TEST_USER"),
				Query:      `{"namespace": "nudgebe", "search_type": "namespace"}`,
				ToolConfig: "",
			},
		}
	for _, tc := range testCases {
		ntc := core.NewNbToolContext(sc, &tool, tc.AccountId, tc.UserId, uuid.NewString(), uuid.NewString(), uuid.NewString(), tc.Query, []llms.MessageContent{}, "", core.NBQueryConfig{ToolConfigs: map[string]string{tool.Name(): tc.ToolConfig}}, "")
		resp, err := tool.Call(ntc, core.NBToolCallRequest{
			Command: tc.Query,
		})
		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}
}

func TestResourceSearchTool_MultiWordResourceName(t *testing.T) {

	tool := K8sResourceSearchTool{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases :=
		[]struct {
			SessionId  string
			Query      string
			AccountId  string
			UserId     string
			ToolConfig string
		}{
			{
				SessionId:  "ut-tool-chain-4",
				AccountId:  os.Getenv("TEST_ACCOUNT"),
				UserId:     os.Getenv("TEST_USER"),
				Query:      `{"resource_name": "notifications pods", "search_type": "suggestions"}`,
				ToolConfig: "",
			},
			{
				SessionId:  "ut-tool-chain-5",
				AccountId:  os.Getenv("TEST_ACCOUNT"),
				UserId:     os.Getenv("TEST_USER"),
				Query:      `{"resource_name": "pod notifications", "search_type": "suggestions"}`,
				ToolConfig: "",
			},
			{
				SessionId:  "ut-tool-chain-6",
				AccountId:  os.Getenv("TEST_ACCOUNT"),
				UserId:     os.Getenv("TEST_USER"),
				Query:      `{"resource_name": "llm server", "search_type": "suggestions"}`,
				ToolConfig: "",
			},
			{
				SessionId:  "ut-tool-chain-7",
				AccountId:  os.Getenv("TEST_ACCOUNT"),
				UserId:     os.Getenv("TEST_USER"),
				Query:      `{"resource_name": "coredns", "resource_type": "pod", "namespace": "kube-system", "search_type": "suggestions"}`,
				ToolConfig: "",
			},
		}
	for _, tc := range testCases {
		ntc := core.NewNbToolContext(sc, &tool, tc.AccountId, tc.UserId, uuid.NewString(), uuid.NewString(), uuid.NewString(), tc.Query, []llms.MessageContent{}, "", core.NBQueryConfig{ToolConfigs: map[string]string{tool.Name(): tc.ToolConfig}}, "")
		resp, err := tool.Call(ntc, core.NBToolCallRequest{
			Command: tc.Query,
		})
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		t.Logf("Query: %s, Response: %s", tc.Query, resp.Data)
		assert.Contains(t, resp.Data, "match_quality")
	}
}

func TestGetCurrentPrometheusOtelHosts(t *testing.T) {
	data := GetCurrentOtelHosts(os.Getenv("TEST_ACCOUNT"))
	assert.NotEmpty(t, data)
}

func TestGetCurrentK8sAccountState(t *testing.T) {
	data := GetCurrentK8sAccountState(os.Getenv("TEST_ACCOUNT"), 100)
	assert.NotEmpty(t, data)
}

func TestResourceSearchTool_Service(t *testing.T) {
	tool := K8sResourceSearchTool{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases := []struct {
		SessionId  string
		Query      string
		AccountId  string
		UserId     string
		ToolConfig string
	}{
		{
			SessionId:  "ut-tool-service-1",
			AccountId:  os.Getenv("TEST_ACCOUNT"),
			UserId:     os.Getenv("TEST_USER"),
			Query:      `{"resource_type": "service", "namespace": "default", "search_type": "suggestions"}`,
			ToolConfig: "",
		},
		{
			SessionId:  "ut-tool-service-2",
			AccountId:  os.Getenv("TEST_ACCOUNT"),
			UserId:     os.Getenv("TEST_USER"),
			Query:      `{"resource_type": "services", "search_type": "suggestions"}`,
			ToolConfig: "",
		},
	}
	for _, tc := range testCases {
		ntc := core.NewNbToolContext(sc, &tool, tc.AccountId, tc.UserId, uuid.NewString(), uuid.NewString(), uuid.NewString(), tc.Query, []llms.MessageContent{}, "", core.NBQueryConfig{ToolConfigs: map[string]string{tool.Name(): tc.ToolConfig}}, "")
		resp, err := tool.Call(ntc, core.NBToolCallRequest{
			Command: tc.Query,
		})
		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}
}

func TestResourceSearchTool_ConfigMap(t *testing.T) {
	tool := K8sResourceSearchTool{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases := []struct {
		SessionId  string
		Query      string
		AccountId  string
		UserId     string
		ToolConfig string
	}{
		{
			SessionId:  "ut-tool-configmap-1",
			AccountId:  os.Getenv("TEST_ACCOUNT"),
			UserId:     os.Getenv("TEST_USER"),
			Query:      `{"resource_type": "configmap", "namespace": "default", "search_type": "suggestions"}`,
			ToolConfig: "",
		},
		{
			SessionId:  "ut-tool-configmap-2",
			AccountId:  os.Getenv("TEST_ACCOUNT"),
			UserId:     os.Getenv("TEST_USER"),
			Query:      `{"resource_type": "configmaps", "search_type": "suggestions"}`,
			ToolConfig: "",
		},
	}
	for _, tc := range testCases {
		ntc := core.NewNbToolContext(sc, &tool, tc.AccountId, tc.UserId, uuid.NewString(), uuid.NewString(), uuid.NewString(), tc.Query, []llms.MessageContent{}, "", core.NBQueryConfig{ToolConfigs: map[string]string{tool.Name(): tc.ToolConfig}}, "")
		resp, err := tool.Call(ntc, core.NBToolCallRequest{
			Command: tc.Query,
		})
		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}
}

func TestResourceSearchTool_Secret(t *testing.T) {
	tool := K8sResourceSearchTool{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases := []struct {
		SessionId  string
		Query      string
		AccountId  string
		UserId     string
		ToolConfig string
	}{
		{
			SessionId:  "ut-tool-secret-1",
			AccountId:  os.Getenv("TEST_ACCOUNT"),
			UserId:     os.Getenv("TEST_USER"),
			Query:      `{"resource_type": "secret", "namespace": "default", "search_type": "suggestions"}`,
			ToolConfig: "",
		},
		{
			SessionId:  "ut-tool-secret-2",
			AccountId:  os.Getenv("TEST_ACCOUNT"),
			UserId:     os.Getenv("TEST_USER"),
			Query:      `{"resource_type": "secrets", "search_type": "suggestions"}`,
			ToolConfig: "",
		},
	}
	for _, tc := range testCases {
		ntc := core.NewNbToolContext(sc, &tool, tc.AccountId, tc.UserId, uuid.NewString(), uuid.NewString(), uuid.NewString(), tc.Query, []llms.MessageContent{}, "", core.NBQueryConfig{ToolConfigs: map[string]string{tool.Name(): tc.ToolConfig}}, "")
		resp, err := tool.Call(ntc, core.NBToolCallRequest{
			Command: tc.Query,
		})
		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}
}

func TestResourceSearchTool_Ingress(t *testing.T) {
	tool := K8sResourceSearchTool{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases := []struct {
		SessionId  string
		Query      string
		AccountId  string
		UserId     string
		ToolConfig string
	}{
		{
			SessionId:  "ut-tool-ingress-1",
			AccountId:  os.Getenv("TEST_ACCOUNT"),
			UserId:     os.Getenv("TEST_USER"),
			Query:      `{"resource_type": "ingress", "namespace": "default", "search_type": "suggestions"}`,
			ToolConfig: "",
		},
		{
			SessionId:  "ut-tool-ingress-2",
			AccountId:  os.Getenv("TEST_ACCOUNT"),
			UserId:     os.Getenv("TEST_USER"),
			Query:      `{"resource_type": "ingresses", "search_type": "suggestions"}`,
			ToolConfig: "",
		},
	}
	for _, tc := range testCases {
		ntc := core.NewNbToolContext(sc, &tool, tc.AccountId, tc.UserId, uuid.NewString(), uuid.NewString(), uuid.NewString(), tc.Query, []llms.MessageContent{}, "", core.NBQueryConfig{ToolConfigs: map[string]string{tool.Name(): tc.ToolConfig}}, "")
		resp, err := tool.Call(ntc, core.NBToolCallRequest{
			Command: tc.Query,
		})
		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}
}

func TestResourceSearchTool_StatefulSet(t *testing.T) {
	tool := K8sResourceSearchTool{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases := []struct {
		SessionId  string
		Query      string
		AccountId  string
		UserId     string
		ToolConfig string
	}{
		{
			SessionId:  "ut-tool-statefulset-1",
			AccountId:  os.Getenv("TEST_ACCOUNT"),
			UserId:     os.Getenv("TEST_USER"),
			Query:      `{"resource_type": "statefulset", "namespace": "default", "search_type": "suggestions"}`,
			ToolConfig: "",
		},
		{
			SessionId:  "ut-tool-statefulset-2",
			AccountId:  os.Getenv("TEST_ACCOUNT"),
			UserId:     os.Getenv("TEST_USER"),
			Query:      `{"resource_type": "statefulsets", "search_type": "suggestions"}`,
			ToolConfig: "",
		},
	}
	for _, tc := range testCases {
		ntc := core.NewNbToolContext(sc, &tool, tc.AccountId, tc.UserId, uuid.NewString(), uuid.NewString(), uuid.NewString(), tc.Query, []llms.MessageContent{}, "", core.NBQueryConfig{ToolConfigs: map[string]string{tool.Name(): tc.ToolConfig}}, "")
		resp, err := tool.Call(ntc, core.NBToolCallRequest{
			Command: tc.Query,
		})
		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}
}

func TestResourceSearchTool_DaemonSet(t *testing.T) {
	tool := K8sResourceSearchTool{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases := []struct {
		SessionId  string
		Query      string
		AccountId  string
		UserId     string
		ToolConfig string
	}{
		{
			SessionId:  "ut-tool-daemonset-1",
			AccountId:  os.Getenv("TEST_ACCOUNT"),
			UserId:     os.Getenv("TEST_USER"),
			Query:      `{"resource_type": "daemonset", "namespace": "default", "search_type": "suggestions"}`,
			ToolConfig: "",
		},
		{
			SessionId:  "ut-tool-daemonset-2",
			AccountId:  os.Getenv("TEST_ACCOUNT"),
			UserId:     os.Getenv("TEST_USER"),
			Query:      `{"resource_type": "daemonsets", "search_type": "suggestions"}`,
			ToolConfig: "",
		},
	}
	for _, tc := range testCases {
		ntc := core.NewNbToolContext(sc, &tool, tc.AccountId, tc.UserId, uuid.NewString(), uuid.NewString(), uuid.NewString(), tc.Query, []llms.MessageContent{}, "", core.NBQueryConfig{ToolConfigs: map[string]string{tool.Name(): tc.ToolConfig}}, "")
		resp, err := tool.Call(ntc, core.NBToolCallRequest{
			Command: tc.Query,
		})
		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}
}

func TestResourceSearchTool_Job(t *testing.T) {
	tool := K8sResourceSearchTool{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases := []struct {
		SessionId  string
		Query      string
		AccountId  string
		UserId     string
		ToolConfig string
	}{
		{
			SessionId:  "ut-tool-job-1",
			AccountId:  os.Getenv("TEST_ACCOUNT"),
			UserId:     os.Getenv("TEST_USER"),
			Query:      `{"resource_type": "job", "namespace": "default", "search_type": "suggestions"}`,
			ToolConfig: "",
		},
		{
			SessionId:  "ut-tool-job-2",
			AccountId:  os.Getenv("TEST_ACCOUNT"),
			UserId:     os.Getenv("TEST_USER"),
			Query:      `{"resource_type": "jobs", "search_type": "suggestions"}`,
			ToolConfig: "",
		},
	}
	for _, tc := range testCases {
		ntc := core.NewNbToolContext(sc, &tool, tc.AccountId, tc.UserId, uuid.NewString(), uuid.NewString(), uuid.NewString(), tc.Query, []llms.MessageContent{}, "", core.NBQueryConfig{ToolConfigs: map[string]string{tool.Name(): tc.ToolConfig}}, "")
		resp, err := tool.Call(ntc, core.NBToolCallRequest{
			Command: tc.Query,
		})
		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}
}

func TestResourceSearchTool_CronJob(t *testing.T) {
	tool := K8sResourceSearchTool{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases := []struct {
		SessionId  string
		Query      string
		AccountId  string
		UserId     string
		ToolConfig string
	}{
		{
			SessionId:  "ut-tool-cronjob-1",
			AccountId:  os.Getenv("TEST_ACCOUNT"),
			UserId:     os.Getenv("TEST_USER"),
			Query:      `{"resource_type": "cronjob", "namespace": "default", "search_type": "suggestions"}`,
			ToolConfig: "",
		},
		{
			SessionId:  "ut-tool-cronjob-2",
			AccountId:  os.Getenv("TEST_ACCOUNT"),
			UserId:     os.Getenv("TEST_USER"),
			Query:      `{"resource_type": "cronjobs", "search_type": "suggestions"}`,
			ToolConfig: "",
		},
	}
	for _, tc := range testCases {
		ntc := core.NewNbToolContext(sc, &tool, tc.AccountId, tc.UserId, uuid.NewString(), uuid.NewString(), uuid.NewString(), tc.Query, []llms.MessageContent{}, "", core.NBQueryConfig{ToolConfigs: map[string]string{tool.Name(): tc.ToolConfig}}, "")
		resp, err := tool.Call(ntc, core.NBToolCallRequest{
			Command: tc.Query,
		})
		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}
}

func TestResourceSearchTool_Node(t *testing.T) {
	tool := K8sResourceSearchTool{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases := []struct {
		SessionId  string
		Query      string
		AccountId  string
		UserId     string
		ToolConfig string
	}{
		{
			SessionId:  "ut-tool-node-1",
			AccountId:  os.Getenv("TEST_ACCOUNT"),
			UserId:     os.Getenv("TEST_USER"),
			Query:      `{"resource_type": "node", "search_type": "suggestions"}`,
			ToolConfig: "",
		},
		{
			SessionId:  "ut-tool-node-2",
			AccountId:  os.Getenv("TEST_ACCOUNT"),
			UserId:     os.Getenv("TEST_USER"),
			Query:      `{"resource_type": "nodes", "search_type": "suggestions"}`,
			ToolConfig: "",
		},
	}
	for _, tc := range testCases {
		ntc := core.NewNbToolContext(sc, &tool, tc.AccountId, tc.UserId, uuid.NewString(), uuid.NewString(), uuid.NewString(), tc.Query, []llms.MessageContent{}, "", core.NBQueryConfig{ToolConfigs: map[string]string{tool.Name(): tc.ToolConfig}}, "")
		resp, err := tool.Call(ntc, core.NBToolCallRequest{
			Command: tc.Query,
		})
		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}
}

func TestResourceSearchTool_PersistentVolume(t *testing.T) {
	tool := K8sResourceSearchTool{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases := []struct {
		SessionId  string
		Query      string
		AccountId  string
		UserId     string
		ToolConfig string
	}{
		{
			SessionId:  "ut-tool-pv-1",
			AccountId:  os.Getenv("TEST_ACCOUNT"),
			UserId:     os.Getenv("TEST_USER"),
			Query:      `{"resource_type": "pv", "search_type": "suggestions"}`,
			ToolConfig: "",
		},
		{
			SessionId:  "ut-tool-pv-2",
			AccountId:  os.Getenv("TEST_ACCOUNT"),
			UserId:     os.Getenv("TEST_USER"),
			Query:      `{"resource_type": "persistentvolumes", "search_type": "suggestions"}`,
			ToolConfig: "",
		},
	}
	for _, tc := range testCases {
		ntc := core.NewNbToolContext(sc, &tool, tc.AccountId, tc.UserId, uuid.NewString(), uuid.NewString(), uuid.NewString(), tc.Query, []llms.MessageContent{}, "", core.NBQueryConfig{ToolConfigs: map[string]string{tool.Name(): tc.ToolConfig}}, "")
		resp, err := tool.Call(ntc, core.NBToolCallRequest{
			Command: tc.Query,
		})
		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}
}

func TestResourceSearchTool_PersistentVolumeClaim(t *testing.T) {
	tool := K8sResourceSearchTool{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases := []struct {
		SessionId  string
		Query      string
		AccountId  string
		UserId     string
		ToolConfig string
	}{
		{
			SessionId:  "ut-tool-pvc-1",
			AccountId:  os.Getenv("TEST_ACCOUNT"),
			UserId:     os.Getenv("TEST_USER"),
			Query:      `{"resource_type": "pvc", "namespace": "default", "search_type": "suggestions"}`,
			ToolConfig: "",
		},
		{
			SessionId:  "ut-tool-pvc-2",
			AccountId:  os.Getenv("TEST_ACCOUNT"),
			UserId:     os.Getenv("TEST_USER"),
			Query:      `{"resource_type": "persistentvolumeclaims", "search_type": "suggestions"}`,
			ToolConfig: "",
		},
	}
	for _, tc := range testCases {
		ntc := core.NewNbToolContext(sc, &tool, tc.AccountId, tc.UserId, uuid.NewString(), uuid.NewString(), uuid.NewString(), tc.Query, []llms.MessageContent{}, "", core.NBQueryConfig{ToolConfigs: map[string]string{tool.Name(): tc.ToolConfig}}, "")
		resp, err := tool.Call(ntc, core.NBToolCallRequest{
			Command: tc.Query,
		})
		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}
}

func TestResourceSearchTool_StorageClass(t *testing.T) {
	tool := K8sResourceSearchTool{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases := []struct {
		SessionId  string
		Query      string
		AccountId  string
		UserId     string
		ToolConfig string
	}{
		{
			SessionId:  "ut-tool-storageclass-1",
			AccountId:  os.Getenv("TEST_ACCOUNT"),
			UserId:     os.Getenv("TEST_USER"),
			Query:      `{"resource_type": "storageclass", "search_type": "suggestions"}`,
			ToolConfig: "",
		},
		{
			SessionId:  "ut-tool-storageclass-2",
			AccountId:  os.Getenv("TEST_ACCOUNT"),
			UserId:     os.Getenv("TEST_USER"),
			Query:      `{"resource_type": "storageclasses", "search_type": "suggestions"}`,
			ToolConfig: "",
		},
	}
	for _, tc := range testCases {
		ntc := core.NewNbToolContext(sc, &tool, tc.AccountId, tc.UserId, uuid.NewString(), uuid.NewString(), uuid.NewString(), tc.Query, []llms.MessageContent{}, "", core.NBQueryConfig{ToolConfigs: map[string]string{tool.Name(): tc.ToolConfig}}, "")
		resp, err := tool.Call(ntc, core.NBToolCallRequest{
			Command: tc.Query,
		})
		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}
}

func TestResourceSearchTool_NetworkPolicy(t *testing.T) {
	tool := K8sResourceSearchTool{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases := []struct {
		SessionId  string
		Query      string
		AccountId  string
		UserId     string
		ToolConfig string
	}{
		{
			SessionId:  "ut-tool-networkpolicy-1",
			AccountId:  os.Getenv("TEST_ACCOUNT"),
			UserId:     os.Getenv("TEST_USER"),
			Query:      `{"resource_type": "networkpolicy", "namespace": "default", "search_type": "suggestions"}`,
			ToolConfig: "",
		},
		{
			SessionId:  "ut-tool-networkpolicy-2",
			AccountId:  os.Getenv("TEST_ACCOUNT"),
			UserId:     os.Getenv("TEST_USER"),
			Query:      `{"resource_type": "networkpolicies", "search_type": "suggestions"}`,
			ToolConfig: "",
		},
	}
	for _, tc := range testCases {
		ntc := core.NewNbToolContext(sc, &tool, tc.AccountId, tc.UserId, uuid.NewString(), uuid.NewString(), uuid.NewString(), tc.Query, []llms.MessageContent{}, "", core.NBQueryConfig{ToolConfigs: map[string]string{tool.Name(): tc.ToolConfig}}, "")
		resp, err := tool.Call(ntc, core.NBToolCallRequest{
			Command: tc.Query,
		})
		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}
}

func TestResourceSearchTool_ServiceAccount(t *testing.T) {
	tool := K8sResourceSearchTool{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases := []struct {
		SessionId  string
		Query      string
		AccountId  string
		UserId     string
		ToolConfig string
	}{
		{
			SessionId:  "ut-tool-serviceaccount-1",
			AccountId:  os.Getenv("TEST_ACCOUNT"),
			UserId:     os.Getenv("TEST_USER"),
			Query:      `{"resource_type": "serviceaccount", "namespace": "default", "search_type": "suggestions"}`,
			ToolConfig: "",
		},
		{
			SessionId:  "ut-tool-serviceaccount-2",
			AccountId:  os.Getenv("TEST_ACCOUNT"),
			UserId:     os.Getenv("TEST_USER"),
			Query:      `{"resource_type": "serviceaccounts", "search_type": "suggestions"}`,
			ToolConfig: "",
		},
	}
	for _, tc := range testCases {
		ntc := core.NewNbToolContext(sc, &tool, tc.AccountId, tc.UserId, uuid.NewString(), uuid.NewString(), uuid.NewString(), tc.Query, []llms.MessageContent{}, "", core.NBQueryConfig{ToolConfigs: map[string]string{tool.Name(): tc.ToolConfig}}, "")
		resp, err := tool.Call(ntc, core.NBToolCallRequest{
			Command: tc.Query,
		})
		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}
}

func TestResourceSearchTool_Role(t *testing.T) {
	tool := K8sResourceSearchTool{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases := []struct {
		SessionId  string
		Query      string
		AccountId  string
		UserId     string
		ToolConfig string
	}{
		{
			SessionId:  "ut-tool-role-1",
			AccountId:  os.Getenv("TEST_ACCOUNT"),
			UserId:     os.Getenv("TEST_USER"),
			Query:      `{"resource_type": "role", "namespace": "default", "search_type": "suggestions"}`,
			ToolConfig: "",
		},
		{
			SessionId:  "ut-tool-role-2",
			AccountId:  os.Getenv("TEST_ACCOUNT"),
			UserId:     os.Getenv("TEST_USER"),
			Query:      `{"resource_type": "roles", "search_type": "suggestions"}`,
			ToolConfig: "",
		},
	}
	for _, tc := range testCases {
		ntc := core.NewNbToolContext(sc, &tool, tc.AccountId, tc.UserId, uuid.NewString(), uuid.NewString(), uuid.NewString(), tc.Query, []llms.MessageContent{}, "", core.NBQueryConfig{ToolConfigs: map[string]string{tool.Name(): tc.ToolConfig}}, "")
		resp, err := tool.Call(ntc, core.NBToolCallRequest{
			Command: tc.Query,
		})
		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}
}

func TestResourceSearchTool_RoleBinding(t *testing.T) {
	tool := K8sResourceSearchTool{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases := []struct {
		SessionId  string
		Query      string
		AccountId  string
		UserId     string
		ToolConfig string
	}{
		{
			SessionId:  "ut-tool-rolebinding-1",
			AccountId:  os.Getenv("TEST_ACCOUNT"),
			UserId:     os.Getenv("TEST_USER"),
			Query:      `{"resource_type": "rolebinding", "namespace": "default", "search_type": "suggestions"}`,
			ToolConfig: "",
		},
		{
			SessionId:  "ut-tool-rolebinding-2",
			AccountId:  os.Getenv("TEST_ACCOUNT"),
			UserId:     os.Getenv("TEST_USER"),
			Query:      `{"resource_type": "rolebindings", "search_type": "suggestions"}`,
			ToolConfig: "",
		},
	}
	for _, tc := range testCases {
		ntc := core.NewNbToolContext(sc, &tool, tc.AccountId, tc.UserId, uuid.NewString(), uuid.NewString(), uuid.NewString(), tc.Query, []llms.MessageContent{}, "", core.NBQueryConfig{ToolConfigs: map[string]string{tool.Name(): tc.ToolConfig}}, "")
		resp, err := tool.Call(ntc, core.NBToolCallRequest{
			Command: tc.Query,
		})
		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}
}

func TestResourceSearchTool_ClusterRole(t *testing.T) {
	tool := K8sResourceSearchTool{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases := []struct {
		SessionId  string
		Query      string
		AccountId  string
		UserId     string
		ToolConfig string
	}{
		{
			SessionId:  "ut-tool-clusterrole-1",
			AccountId:  os.Getenv("TEST_ACCOUNT"),
			UserId:     os.Getenv("TEST_USER"),
			Query:      `{"resource_type": "clusterrole", "search_type": "suggestions"}`,
			ToolConfig: "",
		},
		{
			SessionId:  "ut-tool-clusterrole-2",
			AccountId:  os.Getenv("TEST_ACCOUNT"),
			UserId:     os.Getenv("TEST_USER"),
			Query:      `{"resource_type": "clusterroles", "search_type": "suggestions"}`,
			ToolConfig: "",
		},
	}
	for _, tc := range testCases {
		ntc := core.NewNbToolContext(sc, &tool, tc.AccountId, tc.UserId, uuid.NewString(), uuid.NewString(), uuid.NewString(), tc.Query, []llms.MessageContent{}, "", core.NBQueryConfig{ToolConfigs: map[string]string{tool.Name(): tc.ToolConfig}}, "")
		resp, err := tool.Call(ntc, core.NBToolCallRequest{
			Command: tc.Query,
		})
		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}
}

func TestResourceSearchTool_ClusterRoleBinding(t *testing.T) {
	tool := K8sResourceSearchTool{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases := []struct {
		SessionId  string
		Query      string
		AccountId  string
		UserId     string
		ToolConfig string
	}{
		{
			SessionId:  "ut-tool-clusterrolebinding-1",
			AccountId:  os.Getenv("TEST_ACCOUNT"),
			UserId:     os.Getenv("TEST_USER"),
			Query:      `{"resource_type": "clusterrolebinding", "search_type": "suggestions"}`,
			ToolConfig: "",
		},
		{
			SessionId:  "ut-tool-clusterrolebinding-2",
			AccountId:  os.Getenv("TEST_ACCOUNT"),
			UserId:     os.Getenv("TEST_USER"),
			Query:      `{"resource_type": "clusterrolebindings", "search_type": "suggestions"}`,
			ToolConfig: "",
		},
	}
	for _, tc := range testCases {
		ntc := core.NewNbToolContext(sc, &tool, tc.AccountId, tc.UserId, uuid.NewString(), uuid.NewString(), uuid.NewString(), tc.Query, []llms.MessageContent{}, "", core.NBQueryConfig{ToolConfigs: map[string]string{tool.Name(): tc.ToolConfig}}, "")
		resp, err := tool.Call(ntc, core.NBToolCallRequest{
			Command: tc.Query,
		})
		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}
}

func TestResourceSearchTool_CustomResourceDefinition(t *testing.T) {
	tool := K8sResourceSearchTool{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases := []struct {
		SessionId  string
		Query      string
		AccountId  string
		UserId     string
		ToolConfig string
	}{
		{
			SessionId:  "ut-tool-crd-1",
			AccountId:  os.Getenv("TEST_ACCOUNT"),
			UserId:     os.Getenv("TEST_USER"),
			Query:      `{"resource_type": "crd", "search_type": "suggestions"}`,
			ToolConfig: "",
		},
		{
			SessionId:  "ut-tool-crd-2",
			AccountId:  os.Getenv("TEST_ACCOUNT"),
			UserId:     os.Getenv("TEST_USER"),
			Query:      `{"resource_type": "customresourcedefinitions", "search_type": "suggestions"}`,
			ToolConfig: "",
		},
	}
	for _, tc := range testCases {
		ntc := core.NewNbToolContext(sc, &tool, tc.AccountId, tc.UserId, uuid.NewString(), uuid.NewString(), uuid.NewString(), tc.Query, []llms.MessageContent{}, "", core.NBQueryConfig{ToolConfigs: map[string]string{tool.Name(): tc.ToolConfig}}, "")
		resp, err := tool.Call(ntc, core.NBToolCallRequest{
			Command: tc.Query,
		})
		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}
}

func TestResourceSearchTool_LabelSearch(t *testing.T) {

	tool := K8sResourceSearchTool{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases :=
		[]struct {
			SessionId  string
			Query      string
			AccountId  string
			UserId     string
			ToolConfig string
		}{
			{
				SessionId:  "ut-tool-chain-label-1",
				AccountId:  os.Getenv("TEST_ACCOUNT"),
				UserId:     os.Getenv("TEST_USER"),
				Query:      `{"resource_type": "pod", "namespace": "default", "search_type": "label", "label_selector": "app=nginx"}`,
				ToolConfig: "",
			},
			{
				SessionId:  "ut-tool-chain-label-2",
				AccountId:  os.Getenv("TEST_ACCOUNT"),
				UserId:     os.Getenv("TEST_USER"),
				Query:      `{"search_type": "label", "label_selector": "component=web"}`,
				ToolConfig: "",
			},
		}
	for _, tc := range testCases {
		ntc := core.NewNbToolContext(sc, &tool, tc.AccountId, tc.UserId, uuid.NewString(), uuid.NewString(), uuid.NewString(), tc.Query, []llms.MessageContent{}, "", core.NBQueryConfig{ToolConfigs: map[string]string{tool.Name(): tc.ToolConfig}}, "")
		resp, err := tool.Call(ntc, core.NBToolCallRequest{
			Command: tc.Query,
		})
		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}
}

func TestResourceSearchTool_ToolExecutionIssue(t *testing.T) {

	tool := K8sResourceSearchTool{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases :=
		[]struct {
			SessionId  string
			Query      string
			AccountId  string
			UserId     string
			ToolConfig string
		}{
			{
				SessionId:  "ut-tool-chain-label-1",
				AccountId:  os.Getenv("TEST_ACCOUNT"),
				UserId:     os.Getenv("TEST_USER"),
				Query:      `{"resource_name": "cloud-collector", "resource_type": "service workload", "namespace": "nudgebee", "search_type": "suggestions"}`,
				ToolConfig: "",
			},
		}
	for _, tc := range testCases {
		ntc := core.NewNbToolContext(sc, &tool, tc.AccountId, tc.UserId, uuid.NewString(), uuid.NewString(), uuid.NewString(), tc.Query, []llms.MessageContent{}, "", core.NBQueryConfig{ToolConfigs: map[string]string{tool.Name(): tc.ToolConfig}}, "")
		resp, err := tool.Call(ntc, core.NBToolCallRequest{
			Command: tc.Query,
		})
		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}
}

// ---------------------------------------------------------------------------
// Pure-logic unit tests for filterResourcesByRelevance
// These tests do NOT require a live cluster or TEST_ACCOUNT.
// ---------------------------------------------------------------------------

func TestFilterResourcesByRelevance(t *testing.T) {
	tool := K8sResourceSearchTool{}

	// Helper to build a resource info list by name
	makeResources := func(names ...string) []K8sResourceInfo {
		var out []K8sResourceInfo
		for _, n := range names {
			out = append(out, K8sResourceInfo{Name: n, Type: "pods", Namespace: "default"})
		}
		return out
	}

	t.Run("exact match passes", func(t *testing.T) {
		got := tool.filterResourcesByRelevance(makeResources("llm-server"), "llm-server")
		assert.Len(t, got, 1)
		assert.Equal(t, "llm-server", got[0].Name)
	})

	t.Run("prefix match passes", func(t *testing.T) {
		got := tool.filterResourcesByRelevance(makeResources("llm-server-7d9f4b-xk2vn"), "llm-server")
		assert.Len(t, got, 1)
	})

	t.Run("unrelated resource is removed", func(t *testing.T) {
		// The actual bug: resourcequota-controller was returned for "llm-server" query
		got := tool.filterResourcesByRelevance(
			makeResources("system:controller:resourcequota-controller", "system:resource-tracker"),
			"llm-server",
		)
		assert.Empty(t, got, "clusterroles unrelated to 'llm-server' should be filtered out")
	})

	t.Run("component match via hyphen split", func(t *testing.T) {
		// "llm" part of "llm-server" matches "my-llm-worker"
		got := tool.filterResourcesByRelevance(makeResources("my-llm-worker"), "llm-server")
		assert.Len(t, got, 1)
	})

	t.Run("empty query returns all resources unchanged", func(t *testing.T) {
		input := makeResources("foo", "bar", "baz")
		got := tool.filterResourcesByRelevance(input, "")
		assert.Len(t, got, 3)
	})

	t.Run("empty resource list returns empty", func(t *testing.T) {
		got := tool.filterResourcesByRelevance(nil, "llm-server")
		assert.Empty(t, got)
	})

	t.Run("mixed: relevant and irrelevant resources", func(t *testing.T) {
		input := makeResources(
			"llm-server-abc123",       // relevant
			"system:controller:quota", // irrelevant
			"coredns-llm-test",        // relevant (contains "llm")
			"unrelated-workload",      // irrelevant
		)
		got := tool.filterResourcesByRelevance(input, "llm-server")
		names := make([]string, 0, len(got))
		for _, r := range got {
			names = append(names, r.Name)
		}
		assert.Contains(t, names, "llm-server-abc123")
		assert.Contains(t, names, "coredns-llm-test")
		assert.NotContains(t, names, "system:controller:quota")
		assert.NotContains(t, names, "unrelated-workload")
	})

	t.Run("generic terms like 'server' alone do not pollute results", func(t *testing.T) {
		// "server" is in the generic list, so "my-api-server" should NOT match "llm-server"
		// unless "llm" also appears in the name.
		got := tool.filterResourcesByRelevance(makeResources("my-api-server"), "llm-server")
		assert.Empty(t, got, "resource containing only generic term 'server' should be filtered")
	})
}

func TestResourceSearchTool_SearchDbForResources(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("TEST_ACCOUNT not set")
	}

	tool := K8sResourceSearchTool{}
	accountId := os.Getenv("TEST_ACCOUNT")
	sc := security.NewRequestContextForSuperAdmin()
	dummyCtx := core.NbToolContext{Ctx: sc, AccountId: accountId}

	// Test 1: Simple name
	results := tool.searchDbForResources("llm-server", accountId, dummyCtx)
	t.Logf("Search 'llm-server' found %d results", len(results))

	// Test 2: Multi-word name (should trigger variations)
	results2 := tool.searchDbForResources("llm server", accountId, dummyCtx)
	t.Logf("Search 'llm server' found %d results", len(results2))

	// Test 3: Empty name
	results3 := tool.searchDbForResources("", accountId, dummyCtx)
	assert.Equal(t, 0, len(results3))
}
