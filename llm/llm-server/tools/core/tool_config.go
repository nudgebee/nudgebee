package core

import (
	"errors"
	"fmt"
	"log/slog"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"strings"
	"sync"
	"time"

	"github.com/samber/lo"
	"golang.org/x/sync/errgroup"
)

const CacheNamespaceLlmToolConfig = "llm_tool_config"

func init() {
	common.CacheCreateNamespace(CacheNamespaceLlmToolConfig, common.CacheNamespaceWithExpiration(30*time.Minute))
}

type AccountConfigSummary struct {
	IntegrationTypes map[string]bool
	CloudProviders   map[string]bool
	HasAgent         bool
}

type accountConfigSummaryCache struct {
	mutex sync.RWMutex
	data  map[string]struct {
		summary AccountConfigSummary
		expiry  time.Time
	}
}

var accountConfigSummaryCacheInstance = &accountConfigSummaryCache{
	data: make(map[string]struct {
		summary AccountConfigSummary
		expiry  time.Time
	}),
}

func (c *accountConfigSummaryCache) get(accountId string) (AccountConfigSummary, bool) {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	item, exists := c.data[accountId]
	if exists && time.Now().Before(item.expiry) {
		return item.summary, true
	}
	return AccountConfigSummary{}, false
}

func (c *accountConfigSummaryCache) set(accountId string, summary AccountConfigSummary) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.data[accountId] = struct {
		summary AccountConfigSummary
		expiry  time.Time
	}{
		summary: summary,
		expiry:  time.Now().Add(30 * time.Minute),
	}
}

func (c *accountConfigSummaryCache) delete(accountId string) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	delete(c.data, accountId)
}

func GetAccountConfigSummary(ctx *security.RequestContext, accountId string) (AccountConfigSummary, error) {
	if accountId == "" {
		return AccountConfigSummary{}, errors.New("tools: accountId is required")
	}

	// Level 1: In-memory cache (unmarshaled)
	if summary, ok := accountConfigSummaryCacheInstance.get(accountId); ok {
		return summary, nil
	}

	summary := AccountConfigSummary{
		IntegrationTypes: make(map[string]bool),
		CloudProviders:   make(map[string]bool),
	}

	// Level 2: Shared cache (marshaled JSON)
	cacheKey := "account_config_summary:" + accountId
	if cachedData, found := common.CacheGet(CacheNamespaceLlmToolConfig, cacheKey); found {
		if err := common.UnmarshalJson(cachedData, &summary); err == nil {
			accountConfigSummaryCacheInstance.set(accountId, summary)
			return summary, nil
		}
		slog.Warn("tools: failed to unmarshal cached account summary", "error", "unmarshal error")
	}

	if ctx == nil {
		tenantId, err := security.GetTenantIdFromAccountId(accountId)
		if err != nil {
			return summary, err
		}
		ctx = security.NewRequestContextForTenantAdmin(tenantId)
	}

	// Verify that the account belongs to the tenant
	tenantId := ctx.GetSecurityContext().GetTenantId()
	if !security.IsAccountInTenant(accountId, tenantId) {
		slog.Error("tools: account does not belong to tenant", "account_id", accountId, "tenant_id", tenantId)
		return summary, errors.New("auth: unauthorized account access")
	}

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return summary, err
	}

	var g errgroup.Group
	var mu sync.Mutex

	// Fetch active integrations
	g.Go(func() error {
		integrationRows, err := dbms.Query(`
		SELECT DISTINCT i.type
		FROM integrations i
		JOIN integrations_cloud_accounts ia ON i.id = ia.integration_id
		WHERE ia.cloud_account_id = $1 AND i.status = 'enabled'`, accountId)
		if err != nil {
			slog.Error("tools: failed to query integrations", "error", err, "account_id", accountId)
			return err
		}
		defer func() {
			if err := integrationRows.Close(); err != nil {
				slog.Error("tools: failed to close integration rows", "error", err)
			}
		}()
		for integrationRows.Next() {
			var t string
			if err := integrationRows.Scan(&t); err == nil {
				mu.Lock()
				summary.IntegrationTypes[strings.ToLower(t)] = true
				mu.Unlock()
			}
		}
		return nil
	})

	// Fetch cloud accounts for the tenant to see which providers are active
	g.Go(func() error {
		tenantId := ctx.GetSecurityContext().GetTenantId()
		cloudProviderRows, err := dbms.Query("SELECT DISTINCT lower(cloud_provider) FROM cloud_accounts WHERE tenant = $1 AND status = 'active'", tenantId)
		if err != nil {
			slog.Error("tools: failed to query cloud accounts", "error", err, "tenant_id", tenantId)
			return err
		}
		defer func() {
			if err := cloudProviderRows.Close(); err != nil {
				slog.Error("tools: failed to close cloud provider rows", "error", err)
			}
		}()
		for cloudProviderRows.Next() {
			var p string
			if err := cloudProviderRows.Scan(&p); err == nil {
				mu.Lock()
				summary.CloudProviders[p] = true
				mu.Unlock()
			}
		}
		return nil
	})

	// Check if agent is connected
	g.Go(func() error {
		var agentStatus string
		err := dbms.Db.Get(&agentStatus, "select status from agent where status = 'CONNECTED' and cloud_account_id = $1 limit 1", accountId)
		if err == nil && agentStatus == "CONNECTED" {
			mu.Lock()
			summary.HasAgent = true
			mu.Unlock()
		}
		return nil
	})

	if err := g.Wait(); err != nil {
		return summary, err
	}

	accountConfigSummaryCacheInstance.set(accountId, summary)
	if cachedBytes, err := common.MarshalJson(summary); err == nil {
		_ = common.CacheSet(CacheNamespaceLlmToolConfig, cacheKey, cachedBytes, common.CacheSetWithExpiration(time.Duration(config.Config.CacheToolConfigExpirationMin)*time.Minute))
	}

	return summary, nil
}

func IsToolConfigured(ctx *security.RequestContext, accountId string, tool NBTool, summary AccountConfigSummary) bool {
	return isToolConfigured(ctx, accountId, tool, summary, nil)
}

func isToolConfigured(ctx *security.RequestContext, accountId string, tool NBTool, summary AccountConfigSummary, visited map[string]bool) bool {
	// 1. Core capabilities should always be enabled.
	// LLM (summarizer) and clarification (followup/user prompts) must never be gated.
	if strings.EqualFold(tool.Name(), "LLM") || strings.EqualFold(tool.Name(), "clarification") {
		return true
	}

	// 2. If the tool doesn't implement NBToolConfig, it requires no external config.
	configTool, ok := tool.(NBToolConfig)
	if !ok {
		return true
	}

	schema := configTool.ConfigSchema(ctx)
	configType := strings.ToLower(schema.ConfigType)

	switch schema.ConfigSource {
	case ToolConfigSourceAccount:
		return summary.CloudProviders[configType]
	case ToolConfigSourceAccountAgent:
		return summary.HasAgent
	case ToolConfigSourceAccountAgentAll:
		return summary.HasAgent
	case ToolConfigSourceLLMAgent:
		// For agent-wrapped tools, check if any of the agent's leaf tools are configured.
		if toolsProp, ok := schema.Properties["tools"]; ok && toolsProp.Type == ToolSchemaTypeArray && len(toolsProp.Items) > 0 {
			if visited == nil {
				visited = map[string]bool{}
			}
			visited[strings.ToLower(tool.Name())] = true
			for leafToolName := range toolsProp.Items {
				if visited[strings.ToLower(leafToolName)] {
					continue // prevent cycles
				}
				if leafTool, found := GetNBTool(accountId, leafToolName); found {
					if isToolConfigured(ctx, accountId, leafTool, summary, visited) {
						return true
					}
				}
			}
			return false
		}
		// Fallback: agent without declared leaf tools, allow if any config exists
		return len(summary.IntegrationTypes) > 0 || len(summary.CloudProviders) > 0 || summary.HasAgent
	case ToolConfigSourceTicketAll:
		// Check if any ticket-type integration is configured
		ticketTypes := []string{"jira", "github", "gitlab", "servicenow", "pagerduty", "zenduty"}
		for _, t := range ticketTypes {
			if summary.IntegrationTypes[t] {
				return true
			}
		}
		return false
	default:
		return summary.IntegrationTypes[configType]
	}
}

func GetToolConfigByName(context *security.RequestContext, accountId string, tool NBTool, name string) (ToolConfig, error) {
	configs, err := ListToolConfigs(context, accountId, tool)
	if err != nil {
		return ToolConfig{}, err
	}

	for _, config := range configs {
		if strings.EqualFold(config.Name, name) {
			return config, nil
		}
	}

	return ToolConfig{}, errors.New("tools: tool config not found")
}

func ListToolConfigs(context *security.RequestContext, accountId string, tool NBTool) ([]ToolConfig, error) {
	configs := []ToolConfig{}

	if accountId == "" {
		return configs, errors.New("tools: accountId is required")
	}

	if context == nil {
		tenantId, err := security.GetTenantIdFromAccountId(accountId)
		if err != nil {
			return configs, err
		}
		context = security.NewRequestContextForTenantAdmin(tenantId)
	}

	if tool == nil {
		return configs, errors.New("tools: is required")
	}

	cacheKey := fmt.Sprintf("list_tool_configs:%s:%s", accountId, tool.Name())
	if cachedData, found := common.CacheGet(CacheNamespaceLlmToolConfig, cacheKey); found {
		if err := common.UnmarshalJson(cachedData, &configs); err == nil {
			return configs, nil
		}
		slog.Warn("tools: failed to unmarshal cached tool configs", "error", "unmarshal error", "tool", tool.Name())
	}

	toolConfigProvider, ok := tool.(NBToolConfig)
	if !ok {
		return configs, nil
	}

	toolConfigSchema := toolConfigProvider.ConfigSchema(context)

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		slog.Error("tools: failed to get database manager", "error", err)
		return configs, err
	}

	switch toolConfigSchema.ConfigSource {
	case ToolConfigSourceLLMAgent:
		// check for connected tools which may be using configurations
		// if found && not configureations available, then we can skip returning config
		supportedTools := []string{}
		schema, ok := toolConfigSchema.Properties["tools"]
		if ok && schema.Type == ToolSchemaTypeArray {
			for toolName := range schema.Items {
				supportedTools = append(supportedTools, toolName)
			}
		}
		foundToolWithConfig := false
		isConfigAvailable := false
		toolConfigNames := []string{}

		// Optimization: Fetch summary to skip unconfigured tools
		summary, _ := GetAccountConfigSummary(context, accountId)

		for _, toolName := range supportedTools {
			if nbTool, found := GetNBTool(accountId, toolName); found && nbTool.GetType() == NBToolTypeTool {
				if _, ok := nbTool.(NBToolConfig); ok {
					// Optimization: Skip unconfigured tools
					if !IsToolConfigured(context, accountId, nbTool, summary) {
						continue
					}

					foundToolWithConfig = true
					toolConfigs, err := ListToolConfigs(context, accountId, nbTool)
					if err != nil {
						slog.Error("tools: failed to get tool configs", "tool", toolName, "accountId", accountId, "error", err)
						continue
					}
					isConfigAvailable = isConfigAvailable || len(toolConfigs) > 0
					for _, tc := range toolConfigs {
						toolConfigNames = append(toolConfigNames, tc.Name)
					}
				}
			}
		}

		if !foundToolWithConfig {
			configs = append(configs, ToolConfig{
				Values: []ToolConfigValue{},
				Tags:   map[string]string{},
				Schema: toolConfigSchema,
				Name:   "NoConfigToolsFound",
			})
		} else if foundToolWithConfig && isConfigAvailable {
			toolConfigNames = lo.Uniq(toolConfigNames)
			configs = append(configs, ToolConfig{
				Values: []ToolConfigValue{
					{
						Name:  "config_names",
						Value: strings.Join(toolConfigNames, ","),
					},
				},
				Tags:   map[string]string{},
				Schema: toolConfigSchema,
				Name:   "NoConfigToolsFound",
			})
		}

	case ToolConfigSourceAccountAgent:
		configRows, err := dbms.Query("select status from agent where status = 'CONNECTED' and cloud_account_id = $1", accountId)
		if err != nil {
			slog.Error("tools: failed to get tool", "error", err)
			return configs, err
		}
		defer func() {
			if err := configRows.Close(); err != nil {
				slog.Error("tools: failed to close configRows", "error", err)
			}
		}()

		for configRows.Next() {
			configs = []ToolConfig{
				{
					Values: []ToolConfigValue{},
					Tags:   map[string]string{},
					Schema: toolConfigSchema,
					Name:   "Agent",
				},
			}
		}

	case ToolConfigSourceAccountAgentAll:
		// List ALL accounts with connected agents for the tenant (multi-cluster support).
		// Unlike ToolConfigSourceAccount, we intentionally do NOT narrow by the request's accountId
		// because the whole point of this source is cross-account selection (e.g., picking an EKS
		// cluster from a different account). The standard selection flow (IdentifyConfig / LLM /
		// user followup) handles choosing when there are multiple configs.
		configRows, err := dbms.Query(`
			SELECT DISTINCT ca.id::text, ca.account_name, ca.account_number, ca.cloud_provider
			FROM cloud_accounts ca
			JOIN agent a ON a.cloud_account_id = ca.id
			WHERE ca.tenant = $1 AND a.status = 'CONNECTED' AND ca.status = 'active'
		`, context.GetSecurityContext().GetTenantId())
		if err != nil {
			slog.Error("tools: failed to get agent-connected accounts", "error", err)
			return configs, err
		}
		defer func() {
			if err := configRows.Close(); err != nil {
				slog.Error("tools: failed to close configRows", "error", err)
			}
		}()

		for configRows.Next() {
			var configId, configName, configNumber, cloudProvider *string
			err = configRows.Scan(&configId, &configName, &configNumber, &cloudProvider)
			if err != nil {
				slog.Error("tools: failed to scan agent-connected account", "error", err)
				return configs, err
			}

			if configId == nil {
				continue
			}

			safeName := ""
			if configName != nil && *configName != "" {
				safeName = *configName
			} else if configNumber != nil && *configNumber != "" {
				safeName = *configNumber
			} else {
				safeName = *configId
			}
			safeNumber := ""
			if configNumber != nil {
				safeNumber = *configNumber
			}
			safeProvider := ""
			if cloudProvider != nil {
				safeProvider = *cloudProvider
			}

			tc := ToolConfig{
				Name: safeName,
				Values: []ToolConfigValue{
					{Name: "id", Value: *configId},
					{Name: "account_name", Value: safeName},
					{Name: "account_number", Value: safeNumber},
					{Name: "cloud_provider", Value: safeProvider},
				},
				Tags:   map[string]string{},
				Schema: toolConfigSchema,
			}
			configs = append(configs, tc)
		}

	case ToolConfigSourceAccount:
		configRows, err := dbms.Query("select id::text, account_name, account_number  from cloud_accounts where tenant = $1 and lower(cloud_provider) = lower($2) and status = 'active'", context.GetSecurityContext().GetTenantId(), toolConfigSchema.ConfigType)
		if err != nil {
			slog.Error("tools: failed to get tool", "error", err)
			return configs, err
		}
		defer func() {
			if err := configRows.Close(); err != nil {
				slog.Error("tools: failed to close configRows", "error", err)
			}
		}()

		foundSameAccountId := false
		var sameAccountConfigs ToolConfig

		for configRows.Next() {
			var configId, configName, configNumber *string
			err = configRows.Scan(&configId, &configName, &configNumber)
			if err != nil {
				slog.Error("tools: failed to scan tool", "error", err)
				return configs, err
			}

			if configId == nil {
				continue
			}

			safeName := ""
			if configName != nil {
				safeName = *configName
			}
			safeNumber := ""
			if configNumber != nil {
				safeNumber = *configNumber
			}

			labels := map[string]string{}
			configs = append(configs, ToolConfig{
				Name: safeName,
				Values: []ToolConfigValue{
					{
						Name:  "id",
						Value: *configId,
					},
					{
						Name:  "account_number",
						Value: safeNumber,
					},
				},
				Tags:   labels,
				Schema: toolConfigSchema,
			})

			if *configId == accountId {
				foundSameAccountId = true
				sameAccountConfigs = configs[len(configs)-1]
			}
		}

		if foundSameAccountId {
			configs = []ToolConfig{sameAccountConfigs}
		}

	case ToolConfigSourceTicketAll:
		configRows, err := dbms.Query(`
			SELECT
				i.id,
				i.name,
				i.type,
				MAX(CASE WHEN icv.name = 'username' THEN icv.value END) as username,
				MAX(CASE WHEN icv.name = 'url' THEN icv.value END) as url,
				MAX(CASE WHEN icv.name = 'password' THEN icv.value END) as password,
				MAX(CASE WHEN icv.name = 'auth_type' THEN icv.value END) as auth_type,
				MAX(CASE WHEN icv.name = 'projects' THEN icv.value END) as projects
			FROM integrations i
			JOIN integration_config_values icv ON i.id = icv.integration_id
			WHERE i.tenant_id IN (SELECT tenant FROM cloud_accounts WHERE id = $1)
			  AND i.status = 'enabled'
			  AND i.type IN ('jira', 'github', 'gitlab', 'servicenow', 'pagerduty', 'zenduty')
			GROUP BY i.id, i.name, i.type
		`, accountId)
		if err != nil {
			slog.Error("tools: failed to get ticket configs (all)", "error", err)
			return configs, err
		}
		defer func() {
			if err := configRows.Close(); err != nil {
				slog.Error("tools: failed to close configRows", "error", err)
			}
		}()

		for configRows.Next() {
			var configId, configName, configType, username, url, password, authType, projects *string
			err = configRows.Scan(&configId, &configName, &configType, &username, &url, &password, &authType, &projects)
			if err != nil {
				slog.Error("tools: failed to scan ticket config (all)", "error", err)
				return configs, err
			}

			safeConfigId := ""
			if configId != nil {
				safeConfigId = *configId
			}
			safeConfigName := ""
			if configName != nil {
				safeConfigName = *configName
			}
			safeConfigType := ""
			if configType != nil {
				safeConfigType = *configType
			}
			safeUsername := ""
			if username != nil {
				safeUsername = *username
			}
			safeUrl := ""
			if url != nil {
				safeUrl = *url
			}
			safePassword := ""
			if password != nil {
				decrypted, err := common.Decrypt(*password)
				if err != nil {
					slog.Error("tools: failed to decrypt password", "error", err)
					return configs, err
				}
				safePassword = decrypted
			}
			safeAuthType := ""
			if authType != nil {
				safeAuthType = *authType
			}
			safeProjects := ""
			if projects != nil {
				safeProjects = *projects
			}

			configs = append(configs, ToolConfig{
				Name: safeConfigName,
				Values: []ToolConfigValue{
					{Name: "id", Value: safeConfigId},
					{Name: "name", Value: safeConfigName},
					{Name: "type", Value: safeConfigType},
					{Name: "username", Value: safeUsername},
					{Name: "url", Value: safeUrl},
					{Name: "password", Value: safePassword},
					{Name: "auth_type", Value: safeAuthType},
					{Name: "projects", Value: safeProjects},
				},
				Tags:   map[string]string{},
				Schema: toolConfigSchema,
			})
		}

	case ToolConfigSourceTicket:
		configRows, err := dbms.Query(`
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
			WHERE i.tenant_id IN (SELECT tenant FROM cloud_accounts WHERE id = $1)
			  AND i.status = 'enabled'
			  AND i.type = $2
			GROUP BY i.id, i.name
		`, accountId, toolConfigSchema.ConfigType)
		if err != nil {
			slog.Error("tools: failed to get tool", "error", err)
			return configs, err
		}
		defer func() {
			if err := configRows.Close(); err != nil {
				slog.Error("tools: failed to close configRows", "error", err)
			}
		}()

		for configRows.Next() {
			var configId, configName, username, url, password, authType, projects *string
			err = configRows.Scan(&configId, &configName, &username, &url, &password, &authType, &projects)
			if err != nil {
				slog.Error("tools: failed to scan tool", "error", err)
				return configs, err
			}

			// Handle nil pointers by providing default empty strings
			safeConfigId := ""
			if configId != nil {
				safeConfigId = *configId
			}
			safeConfigName := ""
			if configName != nil {
				safeConfigName = *configName
			}
			safeUsername := ""
			if username != nil {
				safeUsername = *username
			}
			safeUrl := ""
			if url != nil {
				safeUrl = *url
			}
			safePassword := ""
			if password != nil {
				decrypted, err := common.Decrypt(*password)
				if err != nil {
					slog.Error("tools: failed to decrypt password", "error", err)
					return configs, err
				}
				safePassword = decrypted
			}
			safeAuthType := ""
			if authType != nil {
				safeAuthType = *authType
			}
			safeProjects := ""
			if projects != nil {
				safeProjects = *projects
			}

			labels := map[string]string{}
			configs = append(configs, ToolConfig{
				Name: safeConfigName,
				Values: []ToolConfigValue{
					{
						Name:  "id",
						Value: safeConfigId,
					},
					{
						Name:  "name",
						Value: safeConfigName,
					},
					{
						Name:  "username",
						Value: safeUsername,
					},
					{
						Name:  "url",
						Value: safeUrl,
					},
					{
						Name:  "password",
						Value: safePassword,
					},
					{
						Name:  "auth_type",
						Value: safeAuthType,
					},
					{
						Name:  "projects",
						Value: safeProjects,
					},
				},
				Tags:   labels,
				Schema: toolConfigSchema,
			})
		}

	default:
		configRows, err := dbms.Query(`
			SELECT
				i.id::text,
				i.name::text,
				i.labels::text,
				icv.name as config_name,
				icv.value as config_value,
				icv.is_encrypted as config_encrypted
			FROM integrations i
			JOIN integrations_cloud_accounts ia ON i.id = ia.integration_id
			LEFT JOIN integration_config_values icv ON i.id = icv.integration_id
			WHERE ia.cloud_account_id = $1
			AND i.type = $2 AND i.status = 'enabled'
		`, accountId, toolConfigSchema.ConfigType)
		if err != nil {
			slog.Error("tools: failed to get tool configs", "error", err, "account_id", accountId, "type", toolConfigSchema.ConfigType)
			return configs, err
		}
		defer func() {
			if err := configRows.Close(); err != nil {
				slog.Error("tools: failed to close configRows", "error", err)
			}
		}()

		configMap := make(map[string]*ToolConfig)
		configOrder := []string{}

		for configRows.Next() {
			var configId, configName, configLabels *string
			var configKey, configValue *string
			var configEncrypted *bool

			err = configRows.Scan(&configId, &configName, &configLabels, &configKey, &configValue, &configEncrypted)
			if err != nil {
				slog.Error("tools: failed to scan tool", "error", err)
				return configs, err
			}

			if configId == nil {
				continue
			}

			if _, exists := configMap[*configId]; !exists {
				labels := unmarshalLabelsToStringMap(configLabels)

				configMap[*configId] = &ToolConfig{
					Id:     *configId,
					Name:   *configName,
					Values: []ToolConfigValue{},
					Tags:   labels,
					Schema: toolConfigSchema,
				}
				configOrder = append(configOrder, *configId)
			}

			if configKey != nil && configValue != nil {
				val := ToolConfigValue{
					Name:  *configKey,
					Value: *configValue,
				}
				if configEncrypted != nil && *configEncrypted {
					decrypted, err := common.Decrypt(*configValue)
					if err != nil {
						slog.Error("tools: failed to decrypt tool config value", "error", err, "config_id", *configId, "key", *configKey)
						// proceed with encrypted value if decryption fails, but log it
					} else {
						val.Value = decrypted
						val.IsEncrypted = false
					}
				} else if configEncrypted != nil {
					val.IsEncrypted = *configEncrypted
				}
				configMap[*configId].Values = append(configMap[*configId].Values, val)
			}
		}

		for _, id := range configOrder {
			configs = append(configs, *configMap[id])
		}
	}

	// Cache the result
	if cachedBytes, err := common.MarshalJson(configs); err == nil {
		_ = common.CacheSet(CacheNamespaceLlmToolConfig, cacheKey, cachedBytes, common.CacheSetWithExpiration(time.Duration(config.Config.CacheToolConfigExpirationMin)*time.Minute))
	}

	return configs, nil
}

func ListAllToolConfigs(context *security.RequestContext, accountId string) ([]ToolConfig, error) {
	configs := []ToolConfig{}

	if accountId == "" {
		return configs, errors.New("tools: accountId is required")
	}

	cacheKey := fmt.Sprintf("list_all_tool_configs:%s", accountId)
	if cachedData, found := common.CacheGet(CacheNamespaceLlmToolConfig, cacheKey); found {
		if err := common.UnmarshalJson(cachedData, &configs); err == nil {
			return configs, nil
		}
		slog.Warn("tools: failed to unmarshal cached all tool configs", "error", "unmarshal error")
	}

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		slog.Error("tools: failed to get database manager", "error", err)
		return configs, err
	}

	// Pre-fetch tenant_id to avoid subquery
	var tenantId string
	err = dbms.Db.Get(&tenantId, `SELECT tenant FROM cloud_accounts WHERE id = $1`, accountId)
	if err != nil {
		slog.Error("tools: failed to get tenant for account", "error", err, "account_id", accountId)
		return configs, err
	}

	// 1. Query cloud accounts (AWS, GCP, Azure, K8s)
	cloudAccountRows, err := dbms.Query(`
		SELECT id::text, account_name, account_number, cloud_provider
		FROM cloud_accounts
		WHERE tenant = $1 AND status = 'active'
	`, tenantId)
	if err != nil {
		slog.Error("tools: failed to get cloud accounts", "error", err, "tenant_id", tenantId)
	} else {
		defer func() {
			if err := cloudAccountRows.Close(); err != nil {
				slog.Error("tools: failed to close cloudAccountRows", "error", err)
			}
		}()

		for cloudAccountRows.Next() {
			var configId, configName, configNumber, cloudProvider *string
			err = cloudAccountRows.Scan(&configId, &configName, &configNumber, &cloudProvider)
			if err != nil {
				slog.Error("tools: failed to scan cloud account", "error", err)
				continue
			}

			safeConfigId := ""
			if configId != nil {
				safeConfigId = *configId
			}
			safeConfigName := ""
			if configName != nil {
				safeConfigName = *configName
			}
			safeConfigNumber := ""
			if configNumber != nil {
				safeConfigNumber = *configNumber
			}
			safeCloudProvider := ""
			if cloudProvider != nil {
				safeCloudProvider = *cloudProvider
			}

			values := []ToolConfigValue{
				{Name: "id", Value: safeConfigId},
			}
			if safeConfigNumber != "" {
				values = append(values, ToolConfigValue{Name: "account_number", Value: safeConfigNumber})
			}

			configs = append(configs, ToolConfig{
				Name:   safeConfigName,
				Values: values,
				Tags:   map[string]string{},
				Schema: ToolConfigSchema{ConfigType: safeCloudProvider},
			})
		}
	}

	// 2. Query integrations (postgres, mysql, redis, etc.)
	// Only account-specific configs (no tenant-wide configs)
	configRows, err := dbms.Query(`
		SELECT
			i.id::text,
			i.name::text,
			i.type::text,
			i.labels::text,
			icv.name as config_name,
			icv.value as config_value,
			icv.is_encrypted as config_encrypted
		FROM integrations i
		INNER JOIN integrations_cloud_accounts ia ON i.id = ia.integration_id
		LEFT JOIN integration_config_values icv ON i.id = icv.integration_id
		WHERE ia.cloud_account_id = $1
		AND i.status = 'enabled'
	`, accountId)
	if err != nil {
		slog.Error("tools: failed to get all tool configs", "error", err, "account_id", accountId)
		return configs, err
	}
	defer func() {
		if err := configRows.Close(); err != nil {
			slog.Error("tools: failed to close configRows", "error", err)
		}
	}()

	configMap := make(map[string]*ToolConfig)
	configOrder := []string{}

	for configRows.Next() {
		var configId, configName, configType, configLabels *string
		var configKey, configValue *string
		var configEncrypted *bool

		err = configRows.Scan(&configId, &configName, &configType, &configLabels, &configKey, &configValue, &configEncrypted)
		if err != nil {
			slog.Error("tools: failed to scan tool config", "error", err)
			return configs, err
		}

		if configId == nil {
			continue
		}

		if _, exists := configMap[*configId]; !exists {
			labels := unmarshalLabelsToStringMap(configLabels)

			schema := ToolConfigSchema{ConfigType: ""}
			if configType != nil {
				schema.ConfigType = *configType
			}

			safeConfigName := ""
			if configName != nil {
				safeConfigName = *configName
			}

			configMap[*configId] = &ToolConfig{
				Id:     *configId,
				Name:   safeConfigName,
				Values: []ToolConfigValue{},
				Tags:   labels,
				Schema: schema,
			}
			configOrder = append(configOrder, *configId)
		}

		if configKey != nil && configValue != nil {
			val := ToolConfigValue{
				Name:  *configKey,
				Value: *configValue,
			}
			if configEncrypted != nil && *configEncrypted {
				decrypted, err := common.Decrypt(*configValue)
				if err != nil {
					slog.Error("tools: failed to decrypt tool config value", "error", err, "config_id", *configId, "key", *configKey)
					// proceed with encrypted value if decryption fails, but log it
				} else {
					val.Value = decrypted
					val.IsEncrypted = false
				}
			} else if configEncrypted != nil {
				val.IsEncrypted = *configEncrypted
			}
			configMap[*configId].Values = append(configMap[*configId].Values, val)
		}
	}

	for _, id := range configOrder {
		configs = append(configs, *configMap[id])
	}

	// Cache the result
	if cachedBytes, err := common.MarshalJson(configs); err == nil {
		_ = common.CacheSet(CacheNamespaceLlmToolConfig, cacheKey, cachedBytes, common.CacheSetWithExpiration(time.Duration(config.Config.CacheToolConfigExpirationMin)*time.Minute))
	}

	return configs, nil
}

func DeleteToolConfig(ctx *security.RequestContext, accountId string, toolName string, toolConfigName string) error {
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		slog.Error("tools: failed to get database manager", "error", err)
		return err
	}
	tool, found := GetNBTool(accountId, toolName)
	if !found {
		return errors.New("tools: tool not found - " + toolName)
	}

	toolConfigProvider, ok := tool.(NBToolConfig)
	if !ok {
		return nil
	}

	_, err = dbms.Exec(`
        DELETE FROM integration_config_values
        WHERE integration_id IN (
            SELECT i.id
            FROM integrations i
            JOIN integrations_cloud_accounts ia ON i.id = ia.integration_id
            WHERE ia.cloud_account_id = $1
              AND i.type = $2
              AND LOWER(i.name) = LOWER($3)
        )
    `, accountId, toolConfigProvider.ConfigSchema(ctx).ConfigType, toolConfigName)
	if err != nil {
		slog.Error("unable to delete values for tool config",
			"error", err,
			"config_type", toolConfigProvider.ConfigSchema(ctx).ConfigType,
			"config_name", toolConfigName)
		return errors.New("tools: unable to delete values for tool config")
	}

	_, err = dbms.Exec(`
        DELETE FROM integrations
        WHERE id IN (
            SELECT i.id
            FROM integrations i
            JOIN integrations_cloud_accounts ia ON i.id = ia.integration_id
            WHERE ia.cloud_account_id = $1
              AND i.type = $2
              AND LOWER(i.name) = LOWER($3)
        )
    `, accountId, toolConfigProvider.ConfigSchema(ctx).ConfigType, toolConfigName)

	return err
}

// unmarshalLabelsToStringMap parses a JSON labels string into map[string]string,
// converting any non-string values (e.g. numeric ports) to their string representation.
func unmarshalLabelsToStringMap(configLabels *string) map[string]string {
	labels := map[string]string{}
	if configLabels == nil || *configLabels == "" {
		return labels
	}

	var raw map[string]interface{}
	err := common.UnmarshalJson([]byte(*configLabels), &raw)
	if err != nil {
		slog.Error("tools: failed to unmarshal tool config labels", "error", err)
		return labels
	}

	for k, v := range raw {
		labels[k] = fmt.Sprintf("%v", v)
	}
	return labels
}
