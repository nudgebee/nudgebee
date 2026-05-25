package system

import (
	"errors"
	"nudgebee/runbook/internal/tasks/types"
	"nudgebee/runbook/services/ml"
)

type VerticalRightsizeGenerateTask struct{}

func (t *VerticalRightsizeGenerateTask) GetName() string {
	return "vertical_rightsize_generate"
}

func (t *VerticalRightsizeGenerateTask) GetDescription() string {
	return "Generate CPU and memory rightsizing recommendations using ML analysis."
}

func (t *VerticalRightsizeGenerateTask) GetDisplayName() string {
	return "Generate Vertical Rightsizing"
}

func (t *VerticalRightsizeGenerateTask) Execute(taskCtx types.TaskContext, params map[string]any) (any, error) {
	taskCtx.GetLogger().Debug("Executing VerticalRightsizeGenerateTask", "params", params)

	accountId := taskCtx.GetAccountID()
	if id, ok := params["account_id"].(string); ok && id != "" {
		accountId = id
	}
	if accountId == "" {
		return nil, errors.New("account_id is required")
	}

	tenantId := taskCtx.GetTenantID()
	if tenantId == "" {
		return nil, errors.New("tenant_id is required")
	}

	namespace, _ := params["namespace"].(string)
	if namespace == "" {
		return nil, errors.New("namespace is required")
	}

	batchByNamespace, _ := params["batch_by_namespace"].(bool)
	persist, _ := params["persist_recommendation"].(bool)

	// workload_names accepts both []any (JSON unmarshal default) and []string
	// so the schema's PropertyTypeArray and direct programmatic callers both
	// land here cleanly.
	var resourceNames []string
	switch v := params["workload_names"].(type) {
	case []string:
		resourceNames = v
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok && s != "" {
				resourceNames = append(resourceNames, s)
			}
		}
	}

	body := ml.VerticalRightsizeBody{
		AccountID:             accountId,
		TenantID:              tenantId,
		Namespace:             namespace,
		ResourceNames:         resourceNames,
		BatchByNamespace:      batchByNamespace,
		PersistRecommendation: persist,
	}

	resp, err := ml.RunVerticalRightsize(body)
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"status":                "success",
		"database_stored":       resp.DatabaseStored,
		"recommendations_count": len(resp.Recommendations),
	}, nil
}

func (t *VerticalRightsizeGenerateTask) InputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"account_id": {
				Type:     types.PropertyTypeAccount,
				Required: true,
				Title:    "Account",
				Order:    1,
			},
			"namespace": {
				Type:        types.PropertyTypeString,
				Required:    true,
				Title:       "Namespace",
				Description: "Namespace to analyse. Required so an accidental run doesn't analyse the whole cluster.",
				Order:       2,
				DependsOn:   []string{"account_id"},
			},
			"workload_names": {
				Type:        types.PropertyTypeArray,
				SubType:     "string",
				Title:       "Workload Names",
				Description: "Optional list of specific workload names to analyse. Leave empty to analyse every workload in the namespace.",
				Order:       3,
				DependsOn:   []string{"account_id", "namespace"},
				OptionsSource: &types.OptionsSource{
					Type:              "k8s_workload_names",
					DependencyMapping: map[string]string{"account_id": "account_id", "namespace": "namespace"},
				},
			},
			"batch_by_namespace": {
				Type:        types.PropertyTypeBoolean,
				Title:       "Batch By Namespace",
				Description: "Group ML inference calls by namespace instead of running per workload. Faster on large clusters.",
				Default:     true,
				Order:       4,
			},
			"persist_recommendation": {
				Type:        types.PropertyTypeBoolean,
				Title:       "Persist Recommendation",
				Description: "Store the generated recommendations in the database so AutoOptimize / the dashboard can pick them up.",
				Default:     false,
				Order:       5,
			},
		},
	}
}

func (t *VerticalRightsizeGenerateTask) OutputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"status":                {Type: types.PropertyTypeString},
			"database_stored":       {Type: types.PropertyTypeBoolean},
			"recommendations_count": {Type: types.PropertyTypeNumber},
		},
	}
}
