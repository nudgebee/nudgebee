package tools

import (
	"nudgebee/llm/tools/core"
	"strings"
)

func init() {
	core.RegisterNBToolFactory(ToolSecurityExecute, func(accountId string) (core.NBTool, error) {
		return SecurityExecuteTool{}, nil
	})
}

const SecurityView = `
			with pod_container as (
				select
					cr.workload_name as workload_name ,
					cr.workload_type as workload_type ,
					cr."namespace" as "namespace" ,
					cr.cloud_account_id as cloud_account_id ,
					container ->>'image'::text as image,
					cr.tenant_id as tenant_id
				from
								k8s_pods cr ,
					lateral jsonb_array_elements(cr.meta->'config'->'containers') as container
				where cr.is_active is not false and cr.cloud_account_id is not null 
				and cr.workload_name is not null 
				and cr.workload_type is not null 
				and cr."namespace" is not null 
				group by 	
					cr.workload_name,
					cr.workload_type,
					cr."namespace",
					cr.cloud_account_id ,
					cr.tenant_id ,
					container ->>'image'::text 
			) select
					r.id::text as id,
					r.cloud_account_id::text as cloud_account_id,
					r.severity,
					r.status,
					r.recommendation->>'image_name'::text as image,
					r.recommendation ->>'VulnerabilityID'::text as vulnerability_id,
					r.recommendation ->>'PkgID'::text as package_id,
					r.created_at as created_at,
					r.updated_at as updated_at,
					cr.workload_name,
					cr.workload_type,
					cr.namespace,
					r.recommendation::varchar as recommendation,
					r.rule_name as category
			from
					pod_container cr
			right outer join recommendation r on  
					r.category = 'Security'
					and r.account_object_id is not null
					and r.cloud_account_id = cr.cloud_account_id
					and r.tenant_id = cr.tenant_id
					and r.recommendation->>'image_name'::text = cr.image
			where cr.image is not null 

		`
const cisScanView = `
		SELECT
			CAST(r.cloud_account_id AS TEXT) AS cloud_account_id,
			CAST(r.tenant_id AS TEXT) AS tenant_id,
			r.severity AS severity,
			r.severity_weight AS severity_weight,
			r.recommendation ->> 'Id' AS rule_id,
			r.recommendation ->> 'Name' AS rule_name,
			r.recommendation ->> 'Description' AS rule_description,
			COUNT(*) AS count,
			'cis_scan' AS category, 
			r.status AS status,
			MAX(r.updated_at) AS updated_at
		FROM (
			SELECT
				r.*,
				CASE
					WHEN r.severity = 'Critical' THEN 10
					WHEN r.severity = 'High' THEN 8
					WHEN r.severity = 'Medium' THEN 5
					WHEN r.severity = 'Low' THEN 2
					WHEN r.severity = 'Info' THEN 1
					ELSE 0
				END AS severity_weight
			FROM
				recommendation r
			WHERE
				r.rule_name like 'k8s-cis%'
		) AS r
		JOIN cloud_accounts ca ON r.cloud_account_id = ca.id
		JOIN tenant t ON r.tenant_id = t.id
		WHERE
			ca.account_name IS NOT NULL
		GROUP BY
			r.cloud_account_id,
			r.tenant_id,
			r.severity,
			r.severity_weight,
			r.recommendation ->> 'Id',
			r.recommendation ->> 'Name',
			r.recommendation ->> 'Description',
			r.updated_at,
			r.status
		ORDER BY
			r.severity_weight DESC
		
	`
const ToolSecurityExecute = "security_execute"

type SecurityExecuteTool struct {
}

func (m SecurityExecuteTool) Name() string {
	return ToolSecurityExecute
}

func (m SecurityExecuteTool) GetType() core.NBToolType {
	return core.NBToolTypeTool
}

func (m SecurityExecuteTool) Description() string {
	return "Executes a SQL query to retrieve security issues."
}

func (m SecurityExecuteTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"command": {
				Type:        core.ToolSchemaTypeString,
				Description: "security_view SQL Query to execute",
			},
		},
		Required: []string{"command"},
	}
}

func (m SecurityExecuteTool) Call(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	// check if input has security_view or cis_scan
	if strings.Contains(input.Command, "cis_scan") {
		resp, _, err := sqlToolCall(nbRequestContext, input.Command, "security_view", cisScanView, 10, nil)
		if err == nil {
			resp.References = []core.NBToolResponseReference{
				core.GetNudgebeeUIReferenceForClusterDetails(nbRequestContext, []string{"security", "cis-scan"}, "CIS Scan Details", nil, ""),
			}
		}
		return resp, err
	}
	resp, _, err := sqlToolCall(nbRequestContext, input.Command, "security_view", SecurityView, 10, nil)
	if err == nil {
		resp.References = []core.NBToolResponseReference{
			core.GetNudgebeeUIReferenceForClusterDetails(nbRequestContext, []string{"security", "image-scan"}, "Image Scan Details", nil, ""),
		}
	}
	return resp, err
}
