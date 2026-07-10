package aws

import (
	"encoding/json"
	"fmt"
	"net/url"
	"nudgebee/services/cloud"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
	"nudgebee/services/security"
	"strings"
)

// iamRoleSchema / iamUserSchema — concrete schemas for IAM identities
// (NodeTypeServiceIdentity). Set directly by the builders
// below (buildServiceIdentityNodes / createServiceIdentityFromIAMUser) rather than via
// the specific_type map. trust_policy is hoisted for policy-scoped filtering.
var iamRoleSchema = core.SpecificTypeSchema{
	SpecificType: "IAMRole",
	NodeType:     core.NodeTypeServiceIdentity,
	Properties: []core.PropertyDef{
		{Name: "trust_policy", Indexed: true},
		{Name: "role_id"},
		{Name: "path"},
		{Name: "description"},
		{Name: "max_session_duration"},
		{Name: "trust_principals"},
		// Additional provider fields (not yet emitted by our extractor):
		{Name: "create_date"},
		{Name: "create_date_dt"},
	},
}

var iamUserSchema = core.SpecificTypeSchema{
	SpecificType: "IAMUser",
	NodeType:     core.NodeTypeServiceIdentity,
	Properties: []core.PropertyDef{
		// Additional provider fields (not yet emitted by our extractor):
		{Name: "user_id"},
		{Name: "path"},
		{Name: "create_date"},
		{Name: "create_date_dt"},
		{Name: "password_last_used"},
		{Name: "password_last_used_dt"},
	},
}

func init() {
	core.RegisterSpecificTypeSchema(iamRoleSchema)
	core.RegisterSpecificTypeSchema(iamUserSchema)
}

// fetchIAMRolesFromAWS fetches all IAM Roles from the in-memory meta cache.
// AssumeRolePolicyDocument is stored URL-encoded in the DB; it is decoded back
// to a JSON object so extractTrustPolicyPrincipals can work correctly.
func (s *AWSSource) fetchIAMRolesFromAWS(_ *security.RequestContext, _ *core.SourceBuildRequest, _ string) ([]iamRole, error) {
	rows := s.metaByType["Role"]
	roles := make([]iamRole, 0, len(rows))
	for _, row := range rows {
		var role iamRole
		if err := unmarshalMetaInto(row, &role); err != nil {
			s.logger.Warn("Failed to parse IAM role meta, skipping", "resource_id", row.ResourceID, "error", err)
			continue
		}
		// AssumeRolePolicyDocument is URL-encoded in the DB (e.g. %7B%22Version%22%3A...).
		// Decode it back to a JSON object so trust principal extraction works.
		if docStr, ok := role.AssumeRolePolicyDocument.(string); ok && docStr != "" {
			if decoded, err := url.QueryUnescape(docStr); err == nil {
				var docObj interface{}
				if json.Unmarshal([]byte(decoded), &docObj) == nil {
					role.AssumeRolePolicyDocument = docObj
				}
			}
		}
		roles = append(roles, role)
	}
	s.logger.Info("Fetched IAM roles from DB cache", "role_count", len(roles))
	return roles, nil
}

// fetchInstanceProfileRoleMapFromAWS returns a map of instance profile ARN → attached role ARN
// by calling aws iam list-instance-profiles. Used to correctly resolve EC2 RUNS_AS edges when
// the instance profile name differs from the attached role name.
func (s *AWSSource) fetchInstanceProfileRoleMapFromAWS(reqCtx *security.RequestContext, accountID string) (map[string]string, error) {
	cmd := "aws iam list-instance-profiles --output json"

	resp, err := cloud.ExecuteCli(reqCtx, cloud.CloudExecuteCliCommandRequest{
		AccountID: accountID,
		Command:   cmd,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to execute AWS CLI command: %w", err)
	}

	var output string
	if dataStr, ok := resp["data"].(string); ok && dataStr != "" {
		output = dataStr
	} else if outputStr, ok := resp["output"].(string); ok && outputStr != "" {
		output = outputStr
	} else if resultStr, ok := resp["result"].(string); ok && resultStr != "" {
		output = resultStr
	} else {
		return nil, fmt.Errorf("invalid response format from cloud CLI: expected 'data', 'output', or 'result' field")
	}

	var result listIAMInstanceProfilesResponse
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		return nil, fmt.Errorf("failed to parse instance profiles response: %w", err)
	}

	profileRoleMap := make(map[string]string, len(result.InstanceProfiles))
	for _, profile := range result.InstanceProfiles {
		if profile.Arn == "" || len(profile.Roles) == 0 || profile.Roles[0].Arn == "" {
			continue
		}
		profileRoleMap[profile.Arn] = profile.Roles[0].Arn
	}

	s.logger.Info("Fetched instance profile → role mappings",
		"account_id", accountID,
		"count", len(profileRoleMap))

	return profileRoleMap, nil
}

// buildServiceIdentityNodes creates ServiceIdentity knowledge graph nodes from IAM roles.
// IAM is cloud-agnostic in the graph: node_type is always ServiceIdentity,
// with AWS-specific details stored in properties (subtype = "IAMRole").
func (s *AWSSource) buildServiceIdentityNodes(roles []iamRole, req *core.SourceBuildRequest) []*core.DbNode {
	var nodes []*core.DbNode

	for _, role := range roles {
		// Extract AWS account number from the role ARN:
		// arn:aws:iam::123456789012:role/MyRole → parts[4] = "123456789012"
		awsAccountNumber := ""
		if arnParts := strings.Split(role.Arn, ":"); len(arnParts) >= 5 {
			awsAccountNumber = arnParts[4]
		}

		properties := map[string]interface{}{
			"name":                 role.RoleName,
			"arn":                  role.Arn,
			"cloud_provider":       "AWS",
			"region":               "global", // IAM is a global AWS service
			"subtype":              "IAMRole",
			"specific_type":        "IAMRole",
			"service_name":         "AWSIAM",
			"role_id":              role.RoleId,
			"path":                 role.Path,
			"description":          role.Description,
			"max_session_duration": role.MaxSessionDuration,
			"nb_account_id":        req.CloudAccountID,
			"aws_account_number":   awsAccountNumber,
			"is_active":            true,
		}

		// Encode trust policy as JSON string
		if role.AssumeRolePolicyDocument != nil {
			if trustJSON, err := json.Marshal(role.AssumeRolePolicyDocument); err == nil {
				properties["trust_policy"] = string(trustJSON)
			}
		}

		// Extract principal ARNs from the trust policy for edge resolution
		trustPrincipals := extractTrustPolicyPrincipals(role.AssumeRolePolicyDocument)
		if len(trustPrincipals) > 0 {
			if principalsJSON, err := json.Marshal(trustPrincipals); err == nil {
				properties["trust_principals"] = string(principalsJSON)
			}
		}

		// Convert tags to labels map
		if len(role.Tags) > 0 {
			labelsMap := make(map[string]string, len(role.Tags))
			for _, tag := range role.Tags {
				labelsMap[tag.Key] = tag.Value
			}
			properties["labels"] = labelsMap
		}

		// Unique key: aws:{accountID}:global:ServiceIdentity::{roleName}
		uniqueKey := fmt.Sprintf("aws:%s:global:ServiceIdentity::%s", req.CloudAccountID, role.RoleName)

		node := core.NewNode(
			core.NodeTypeServiceIdentity,
			uniqueKey,
			properties,
			req.TenantID,
			req.CloudAccountID,
			"aws",
		)
		nodes = append(nodes, node)
	}

	return nodes
}

// createServiceIdentityFromIAMUser produces a ServiceIdentity DbNode for an
// IAM User row coming from the cloud_resourses table. Same NodeType +
// region="global" + unique-key shape as IAM Role ServiceIdentities, only the
// subtype differs ("IAMUser" vs "IAMRole"). Doesn't need a separate IAM
// API call because cloud_resourses already carries the user's ARN and name.
//
// Mirrors buildServiceIdentityNodes (which works on iamRole structs); this
// helper sits in the createNodeFromResource path because the user data is
// already being iterated there.
func (s *AWSSource) createServiceIdentityFromIAMUser(resource *sources.CloudResourceRow, req *core.SourceBuildRequest) *core.DbNode {
	// Extract AWS account number from the user ARN:
	// arn:aws:iam::123456789012:user/me@example.com → parts[4] = "123456789012"
	awsAccountNumber := ""
	if arnParts := strings.Split(resource.ARN, ":"); len(arnParts) >= 5 {
		awsAccountNumber = arnParts[4]
	}

	properties := map[string]interface{}{
		"name":               resource.Name,
		"arn":                resource.ARN,
		"cloud_provider":     "AWS",
		"region":             "global", // IAM is a global AWS service
		"subtype":            "IAMUser",
		"specific_type":      "IAMUser",
		"service_name":       "AWSIAM",
		"nb_account_id":      req.CloudAccountID,
		"aws_account_number": awsAccountNumber,
		"is_active":          true,
	}
	// Tags arrive as json.RawMessage (bytes). Downstream edge builders
	// (linkLoadBalancerToEKSCluster, createEC2Edges, etc.) assert
	// properties["labels"] as map[string]interface{}, so unmarshal first
	// — assigning the raw bytes would silently break those consumers.
	if len(resource.Tags) > 0 && string(resource.Tags) != "{}" {
		var tagsMap map[string]interface{}
		if err := json.Unmarshal(resource.Tags, &tagsMap); err == nil {
			properties["labels"] = tagsMap
		}
	}

	// Unique key shape: aws:{accountID}:global:ServiceIdentity:IAMUser:{userName}.
	// The 5th segment (hierarchy) carries the subtype "IAMUser" to disambiguate
	// from IAM Roles, which use empty hierarchy under the same NodeType.
	// Although AWS userName and roleName live in separate namespaces, they're
	// NOT guaranteed to be distinct (eg both an IAM User and an IAM Role can
	// be named "admin") — and both would otherwise collide on
	// "aws:{accountID}:global:ServiceIdentity::{name}".
	uniqueKey := fmt.Sprintf("aws:%s:global:ServiceIdentity:IAMUser:%s", req.CloudAccountID, resource.Name)

	return core.NewNode(
		core.NodeTypeServiceIdentity,
		uniqueKey,
		properties,
		req.TenantID,
		req.CloudAccountID,
		"aws",
	)
}

// extractTrustPolicyPrincipals parses an IAM trust policy document and returns all principal ARNs.
func extractTrustPolicyPrincipals(doc interface{}) []string {
	if doc == nil {
		return nil
	}

	docMap, ok := doc.(map[string]interface{})
	if !ok {
		return nil
	}

	var statements []interface{}
	switch v := docMap["Statement"].(type) {
	case []interface{}:
		statements = v
	case map[string]interface{}:
		statements = []interface{}{v}
	default:
		return nil
	}

	var principals []string
	for _, stmt := range statements {
		stmtMap, ok := stmt.(map[string]interface{})
		if !ok {
			continue
		}
		// Only process Allow statements — Deny statements must not create ASSUMES edges
		if effect, ok := stmtMap["Effect"].(string); !ok || effect != "Allow" {
			continue
		}
		principal, ok := stmtMap["Principal"]
		if !ok {
			continue
		}
		switch p := principal.(type) {
		case string:
			if p != "" && p != "*" {
				principals = append(principals, p)
			}
		case map[string]interface{}:
			// Principal can be {"AWS": "arn:...", "Service": "lambda.amazonaws.com"}
			for _, v := range p {
				switch vv := v.(type) {
				case string:
					if vv != "" && vv != "*" {
						principals = append(principals, vv)
					}
				case []interface{}:
					for _, item := range vv {
						if s, ok := item.(string); ok && s != "" && s != "*" {
							principals = append(principals, s)
						}
					}
				}
			}
		}
	}
	return principals
}

// buildServiceIdentityEdges creates edges between ServiceIdentity nodes and compute resources:
//   - Lambda/EC2 → ServiceIdentity (RUNS_AS): compute resources that assume this IAM role
//   - ServiceIdentity → ServiceIdentity (ASSUMES): cross-account / service trust relationships
func (s *AWSSource) buildServiceIdentityEdges(serviceIdentityNodes []*core.DbNode, lookup *sources.NodeLookup, instanceProfileRoleMap map[string]string, req *core.SourceBuildRequest) []*core.DbEdge {
	var edges []*core.DbEdge

	// Build a local ARN index for ServiceIdentity nodes to resolve role lookups
	serviceIdentityByARN := make(map[string]*core.DbNode, len(serviceIdentityNodes))
	for _, n := range serviceIdentityNodes {
		if arn, ok := n.Properties["arn"].(string); ok && arn != "" {
			serviceIdentityByARN[arn] = n
		}
	}

	// 1. Lambda → ServiceIdentity (RUNS_AS)
	for _, lambdaNode := range lookup.ByNodeType[core.NodeTypeServerlessFunction] {
		roleARN, ok := getStringProperty(lambdaNode, "role_arn")
		if !ok || roleARN == "" {
			continue
		}
		if identityNode, exists := serviceIdentityByARN[roleARN]; exists {
			edge := core.NewEdge(
				lambdaNode.ID,
				identityNode.ID,
				core.RelationshipRunsAs,
				map[string]interface{}{"source": "iam_role"},
				req.TenantID,
				req.CloudAccountID,
				"aws",
			)
			edges = append(edges, edge)
		}
	}

	// 2. EC2 → ServiceIdentity (RUNS_AS) via instance profile
	for _, ec2Node := range lookup.ByNodeType[core.NodeTypeComputeInstance] {
		profileARN, ok := getStringProperty(ec2Node, "iam_instance_profile_arn")
		if !ok || profileARN == "" {
			continue
		}
		// Resolve the role ARN via the pre-fetched instance profile map.
		// Fall back to name-based ARN substitution when the map is unavailable.
		roleARN, mapped := instanceProfileRoleMap[profileARN]
		if !mapped {
			roleARN = strings.Replace(profileARN, ":instance-profile/", ":role/", 1)
		}
		if identityNode, exists := serviceIdentityByARN[roleARN]; exists {
			edge := core.NewEdge(
				ec2Node.ID,
				identityNode.ID,
				core.RelationshipRunsAs,
				map[string]interface{}{"source": "iam_instance_profile"},
				req.TenantID,
				req.CloudAccountID,
				"aws",
			)
			edges = append(edges, edge)
		}
	}

	// 3. ServiceIdentity → ServiceIdentity (ASSUMES) via trust policy
	// Only create edges where the principal is another IAM Role ARN (contains ":role/")
	for _, identityNode := range serviceIdentityNodes {
		principalsJSON, ok := getStringProperty(identityNode, "trust_principals")
		if !ok || principalsJSON == "" {
			continue
		}
		var principals []string
		if err := json.Unmarshal([]byte(principalsJSON), &principals); err != nil {
			continue
		}
		for _, principalARN := range principals {
			if !strings.Contains(principalARN, ":role/") {
				continue // Skip non-role principals (services, accounts, etc.)
			}
			if principalNode, exists := serviceIdentityByARN[principalARN]; exists {
				edge := core.NewEdge(
					principalNode.ID,
					identityNode.ID,
					core.RelationshipAssumes,
					map[string]interface{}{"source": "trust_policy"},
					req.TenantID,
					req.CloudAccountID,
					"aws",
				)
				edges = append(edges, edge)
			}
		}
	}

	return edges
}
