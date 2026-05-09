package integrations

import (
	"database/sql" // Added for sql.NullString
	"errors"
	"fmt"
	"io"
	"log/slog"
	"nudgebee/runbook/common"
	"nudgebee/runbook/config"
	"nudgebee/runbook/services/security"
	"strings"
)

// IntegrationService handles operations related to integrations, including fetching their configurations.
func GetIntegration(ctx *security.RequestContext, accountId, integrationType, integrationId string) (IntegrationConfig, error) {

	if integrationType == "github" || integrationType == "gitlab" {
		configs, err := ListIntegrationsByType(ctx, accountId, integrationType)
		if err != nil {
			return IntegrationConfig{}, errors.New("integration not found")
		}

		for _, ic := range configs {
			if ic.Id == integrationId {
				return ic, nil
			}
		}
		return IntegrationConfig{}, fmt.Errorf("%s integration not found", integrationType)

	}

	// Existing HTTP call logic for other integration types
	integrationResponse, err := ListIntegrationsByType(ctx, accountId, integrationType)
	if err != nil {
		return IntegrationConfig{}, errors.New("error while fetching integrations")
	}

	for _, ic := range integrationResponse {
		if ic.Id == integrationId {
			if len(ic.Configs) > 0 {
				ic.Values = append(ic.Values, ic.Configs...)
			}
			return ic, nil
		}
	}

	return IntegrationConfig{}, errors.New("integration not found")
}

func ListIntegrationsByType(ctx *security.RequestContext, accountId, integrationType string) ([]IntegrationConfig, error) {

	if integrationType == "github" {
		dbms, err := common.GetDatabaseManager(common.Metastore)
		if err != nil {
			return []IntegrationConfig{}, err
		}

		// GitHub integrations are tenant-scoped (no row in
		// integrations_cloud_accounts). Resolve the tenant directly from the
		// security context so the lookup works even when the request carries no
		// accountId, an empty accountId, or one that doesn't map to a
		// cloud_accounts row — previously the cloud_accounts subquery would
		// silently return zero rows in those cases and we'd surface
		// "integration not found".
		tenantId := ctx.GetSecurityContext().GetTenantId()
		query := `
			SELECT
				i.id,
				i.name,
				MAX(CASE WHEN icv.name = 'username' THEN icv.value END) as username,
				MAX(CASE WHEN icv.name = 'url' THEN icv.value END) as url,
				MAX(CASE WHEN icv.name = 'password' THEN icv.value END) as password,
				MAX(CASE WHEN icv.name = 'auth_type' THEN icv.value END) as auth_type,
				MAX(CASE WHEN icv.name = 'projects' THEN icv.value END) as projects
			FROM integrations i
			JOIN integration_config_values icv ON i.id = icv.integration_id
			WHERE i.tenant_id = $1
			  AND i.type = 'github'
			  AND i.status = 'enabled'
			GROUP BY i.id, i.name
		`

		configRows, err := dbms.Query(query, tenantId)
		if err != nil {
			slog.Error("integrations: failed to get github config from db", "error", err, "tenant_id", tenantId, "integration_type", integrationType)
			return []IntegrationConfig{}, fmt.Errorf("failed to retrieve github configuration: %w", err)
		}
		defer func() {
			if err := configRows.Close(); err != nil {
				slog.Error("integrations: failed to close configRows", "error", err)
			}
		}()

		configs := []IntegrationConfig{}
		for configRows.Next() {
			var configId, configName string
			var username, url, password, authType sql.NullString
			var projects sql.NullString // projects can be null

			err = configRows.Scan(&configId, &configName, &username, &url, &password, &authType, &projects)
			if err != nil {
				slog.Error("integrations: failed to scan github config", "error", err)
				return []IntegrationConfig{}, fmt.Errorf("failed to scan github configuration: %w", err)
			}

			// Skip if required fields are missing
			if !username.Valid || !url.Valid || !password.Valid {
				slog.Warn("integrations: skipping github integration with missing credentials", "integration_id", configId)
				continue
			}

			decryptedPassword := ""
			if password.Valid && password.String != "" {
				decrypted, err := common.Decrypt(password.String)
				if err != nil {
					slog.Error("integrations: failed to decrypt password", "error", err)
					return []IntegrationConfig{}, fmt.Errorf("failed to decrypt password: %w", err)
				}
				decryptedPassword = decrypted
			}

			authTypeValue := "token"
			if authType.Valid {
				authTypeValue = authType.String
			}

			// Construct IntegrationConfig
			ic := IntegrationConfig{
				Id:   configId,
				Name: configName,
				Values: []IntegrationConfigValue{
					{Name: "id", Value: configId},
					{Name: "name", Value: configName},
					{Name: "username", Value: username.String},
					{Name: "url", Value: url.String},
					{Name: "password", Value: decryptedPassword},
					{Name: "auth_type", Value: authTypeValue},
				},
			}
			if projects.Valid {
				ic.Values = append(ic.Values, IntegrationConfigValue{Name: "projects", Value: projects.String})
			}
			configs = append(configs, ic)
		}
		return configs, nil

	}

	if integrationType == "gitlab" {
		dbms, err := common.GetDatabaseManager(common.Metastore)
		if err != nil {
			return []IntegrationConfig{}, err
		}

		// GitLab integrations are tenant-scoped — see the github branch above
		// for the rationale on resolving tenant from the security context.
		tenantId := ctx.GetSecurityContext().GetTenantId()
		query := `
			SELECT
				i.id,
				i.name,
				MAX(CASE WHEN icv.name = 'username' THEN icv.value END) as username,
				MAX(CASE WHEN icv.name = 'url' THEN icv.value END) as url,
				MAX(CASE WHEN icv.name = 'password' THEN icv.value END) as password
			FROM integrations i
			JOIN integration_config_values icv ON i.id = icv.integration_id
			WHERE i.tenant_id = $1
			  AND i.type = 'gitlab'
			  AND i.status = 'enabled'
			GROUP BY i.id, i.name
		`

		configRows, err := dbms.Query(query, tenantId)
		if err != nil {
			slog.Error("integrations: failed to get gitlab config from db", "error", err, "tenant_id", tenantId, "integration_type", integrationType)
			return []IntegrationConfig{}, fmt.Errorf("failed to retrieve gitlab configuration: %w", err)
		}
		defer func() {
			if err := configRows.Close(); err != nil {
				slog.Error("integrations: failed to close configRows", "error", err)
			}
		}()

		configs := []IntegrationConfig{}
		for configRows.Next() {
			var configId, configName string
			var username, url, password sql.NullString

			err = configRows.Scan(&configId, &configName, &username, &url, &password)
			if err != nil {
				slog.Error("integrations: failed to scan gitlab config", "error", err)
				return []IntegrationConfig{}, fmt.Errorf("failed to scan gitlab configuration: %w", err)
			}

			// Skip if token is missing
			if !password.Valid || password.String == "" {
				slog.Warn("integrations: skipping gitlab integration with missing token", "integration_id", configId)
				continue
			}

			decryptedPassword, err := common.Decrypt(password.String)
			if err != nil {
				slog.Error("integrations: failed to decrypt gitlab password", "error", err)
				return []IntegrationConfig{}, fmt.Errorf("failed to decrypt password: %w", err)
			}

			urlValue := "https://gitlab.com"
			if url.Valid && url.String != "" {
				urlValue = url.String
			}

			// Construct IntegrationConfig
			ic := IntegrationConfig{
				Id:   configId,
				Name: configName,
				Values: []IntegrationConfigValue{
					{Name: "id", Value: configId},
					{Name: "name", Value: configName},
					{Name: "username", Value: username.String},
					{Name: "url", Value: urlValue},
					{Name: "password", Value: decryptedPassword},
				},
			}
			configs = append(configs, ic)
		}
		if err := configRows.Err(); err != nil {
			return []IntegrationConfig{}, fmt.Errorf("error iterating gitlab configurations: %w", err)
		}
		return configs, nil

	}

	resp, err := common.HttpPost(fmt.Sprintf("%s/v1/integration/list_integration_config", config.Config.ServiceEndpoint), common.HttpWithHeaders(map[string]string{
		"Content-Type":   "application/json",
		"Accept":         "application/json",
		"X-ACTION-TOKEN": config.Config.ServiceApiServerToken,
		"x-tenant-id":    ctx.GetSecurityContext().GetTenantId(),
		"x-user-id":      ctx.GetSecurityContext().GetUserId(),
	}), common.HttpWithJsonBody(map[string]any{
		"integration_name": integrationType,
		"account_id":       accountId,
	}))

	if err != nil {
		return []IntegrationConfig{}, err
	}

	defer func() {
		if resp.Body != nil {
			if err := resp.Body.Close(); err != nil {
				slog.Info("services_server: failed to close response body", "error", err)
			}
		}
	}()

	jsonBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return []IntegrationConfig{}, err
	}

	if resp.StatusCode == 401 {
		return []IntegrationConfig{}, fmt.Errorf("unauthorized: %v", string(jsonBody))
	}

	if resp.StatusCode == 500 {
		return []IntegrationConfig{}, fmt.Errorf("internal Server Error from Services Server, %v", string(jsonBody))
	}

	integrationResponse := struct {
		Data []IntegrationConfig `json:"data"`
	}{
		Data: []IntegrationConfig{},
	}

	err = common.UnmarshalJson(jsonBody, &integrationResponse)
	if err != nil {
		return []IntegrationConfig{}, err
	}

	return integrationResponse.Data, nil
}

// ValidateWebhookTriggerIntegrationID checks if an integration with the given Name exists.
func ValidateWebhookTriggerIntegrationID(ctx *security.RequestContext, accountId, integrationName string) error {

	configs, err := ListIntegrationsByType(ctx, accountId, "workflow_webhook")
	if err != nil {
		return err
	}

	for _, ic := range configs {
		if strings.EqualFold(ic.Name, integrationName) {
			if len(ic.Configs) > 0 {
				ic.Values = append(ic.Values, ic.Configs...)
			}
			return nil
		}
	}

	return errors.New("integration not found")
}

type CreateWorkflowWebhookTriggerResponse struct {
	IntegrationId string `json:"integration_id"`
	Token         string `json:"token"`
}

func CreateWorkflowWebhookTrigger(ctx *security.RequestContext, accountId, workflowId, integrationName string) (CreateWorkflowWebhookTriggerResponse, error) {

	if accountId == "" || workflowId == "" || integrationName == "" {
		return CreateWorkflowWebhookTriggerResponse{}, common.ErrorBadRequest("accountId, workflowId, integrationName is required")
	}

	resp, err := common.HttpPost(fmt.Sprintf("%s/hasura/integration", config.Config.ServiceEndpoint), common.HttpWithHeaders(map[string]string{
		"Content-Type":   "application/json",
		"Accept":         "application/json",
		"X-ACTION-TOKEN": config.Config.ServiceApiServerToken,
		"x-tenant-id":    ctx.GetSecurityContext().GetTenantId(),
		"x-user-id":      ctx.GetSecurityContext().GetUserId(),
	}), common.HttpWithJsonBody(map[string]any{
		"action": map[string]any{
			"name": "integrations_create_config",
		},
		"input": map[string]any{
			"request": map[string]any{
				"integration_name":        "workflow_webhook",
				"integration_config_name": integrationName,
				"integration_config_values": []map[string]any{
					{
						"name":  "workflow_id",
						"value": workflowId,
					},
					{
						"name":  "token",
						"value": "",
					},
				},
				"account_ids": []string{accountId},
			},
		},
	}))

	if err != nil {
		return CreateWorkflowWebhookTriggerResponse{}, err
	}

	defer func() {
		if resp.Body != nil {
			if err := resp.Body.Close(); err != nil {
				slog.Info("services_server: failed to close response body", "error", err)
			}
		}
	}()

	jsonBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return CreateWorkflowWebhookTriggerResponse{}, err
	}

	if resp.StatusCode != 200 && resp.StatusCode != 201 {
		return CreateWorkflowWebhookTriggerResponse{}, fmt.Errorf("failed to create webhook: %s", string(jsonBody))
	}

	triggerResponse := CreateWorkflowWebhookTriggerResponse{}

	integrationResponse := IntegrationConfig{}
	err = common.UnmarshalJson(jsonBody, &integrationResponse)
	if err != nil {
		return CreateWorkflowWebhookTriggerResponse{}, err
	}

	triggerResponse.IntegrationId = integrationResponse.Id
	triggerResponse.Token = ""

	for _, c := range integrationResponse.Configs {
		if c.Name == "token" {
			triggerResponse.Token = c.Value
		}
	}

	return triggerResponse, nil
}

func DeleteWorkflowWebhookTrigger(ctx *security.RequestContext, accountId, integrationName string) error {

	if accountId == "" || integrationName == "" {
		return errors.New("accountId, integrationName is required")
	}

	resp, err := common.HttpPost(fmt.Sprintf("%s/hasura/integration", config.Config.ServiceEndpoint), common.HttpWithHeaders(map[string]string{
		"Content-Type":   "application/json",
		"Accept":         "application/json",
		"X-ACTION-TOKEN": config.Config.ServiceApiServerToken,
		"x-tenant-id":    ctx.GetSecurityContext().GetTenantId(),
		"x-user-id":      ctx.GetSecurityContext().GetUserId(),
	}), common.HttpWithJsonBody(map[string]any{
		"action": map[string]any{
			"name": "integrations_delete_config",
		},
		"input": map[string]any{
			"request": map[string]any{
				"integration_name":        "workflow_webhook",
				"integration_config_name": integrationName,
				"account_ids":             []string{accountId},
			},
		},
	}))

	if err != nil {
		return err
	}

	defer func() {
		if resp.Body != nil {
			if err := resp.Body.Close(); err != nil {
				slog.Info("services_server: failed to close response body", "error", err)
			}
		}
	}()

	jsonBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if resp.StatusCode != 200 && resp.StatusCode != 204 {
		return fmt.Errorf("failed to delete webhook: %s", string(jsonBody))
	}

	return nil
}
