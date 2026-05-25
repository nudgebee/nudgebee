package core

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"nudgebee/llm/audit"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

const CacheChannelToolInvalidation = "llm_tool_invalidation"

type toolCache struct {
	mutex sync.RWMutex
	data  map[string]map[string]NBTool // accountId -> toolName -> tool
}

var toolCacheInstance *toolCache

type enabledToolsCache struct {
	mutex sync.RWMutex
	data  map[string]struct {
		tools  []NBTool
		expiry time.Time
	}
}

var enabledToolsCacheInstance *enabledToolsCache

type toolDtoCache struct {
	mutex sync.RWMutex
	data  map[string]struct {
		tools  []ToolDto
		expiry time.Time
	}
}

var toolDtoCacheInstance *toolDtoCache

type customToolDtoCache struct {
	mutex sync.RWMutex
	data  map[string]struct {
		tools  []ToolDto
		expiry time.Time
	}
}

var customToolDtoCacheInstance *customToolDtoCache

func init() {
	toolCacheInstance = &toolCache{
		data: make(map[string]map[string]NBTool),
	}
	enabledToolsCacheInstance = &enabledToolsCache{
		data: make(map[string]struct {
			tools  []NBTool
			expiry time.Time
		}),
	}
	toolDtoCacheInstance = &toolDtoCache{
		data: make(map[string]struct {
			tools  []ToolDto
			expiry time.Time
		}),
	}
	customToolDtoCacheInstance = &customToolDtoCache{
		data: make(map[string]struct {
			tools  []ToolDto
			expiry time.Time
		}),
	}

	common.CacheSubscribe(CacheChannelToolInvalidation, func(accountId string) {
		slog.Info("received global tool invalidation", "account_id", accountId)
		// Internal invalidation only (don't broadcast again)
		invalidateLocalCaches(accountId)
	})
}

func (c *customToolDtoCache) get(accountId string) ([]ToolDto, bool) {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	item, exists := c.data[accountId]
	if exists && time.Now().Before(item.expiry) {
		return item.tools, true
	}
	return nil, false
}

func (c *customToolDtoCache) set(accountId string, tools []ToolDto) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.data[accountId] = struct {
		tools  []ToolDto
		expiry time.Time
	}{
		tools:  tools,
		expiry: time.Now().Add(defaultCacheTTL),
	}
}

func (c *customToolDtoCache) delete(accountId string) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	delete(c.data, accountId)
}

const defaultCacheTTL = 30 * time.Minute

func (c *enabledToolsCache) get(accountId string) ([]NBTool, bool) {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	item, exists := c.data[accountId]
	if exists && time.Now().Before(item.expiry) {
		return item.tools, true
	}
	return nil, false
}

func (c *enabledToolsCache) set(accountId string, tools []NBTool) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.data[accountId] = struct {
		tools  []NBTool
		expiry time.Time
	}{
		tools:  tools,
		expiry: time.Now().Add(defaultCacheTTL),
	}
}

func (c *enabledToolsCache) delete(accountId string) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	delete(c.data, accountId)
}

func (c *toolDtoCache) get(accountId string) ([]ToolDto, bool) {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	item, exists := c.data[accountId]
	if exists && time.Now().Before(item.expiry) {
		return item.tools, true
	}
	return nil, false
}

func (c *toolDtoCache) set(accountId string, tools []ToolDto) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.data[accountId] = struct {
		tools  []ToolDto
		expiry time.Time
	}{
		tools:  tools,
		expiry: time.Now().Add(defaultCacheTTL),
	}
}

func (c *toolDtoCache) delete(accountId string) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	delete(c.data, accountId)
}

func (c *toolCache) get(accountId string) ([]NBTool, bool) {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	toolMap, exists := c.data[accountId]
	if exists {
		slog.Info(logCacheHit, "account_id", accountId)
		// Convert map to slice
		tools := make([]NBTool, 0, len(toolMap))
		for _, tool := range toolMap {
			tools = append(tools, tool)
		}
		return tools, true
	} else {
		slog.Info(logCacheMiss, "account_id", accountId)
	}
	return nil, false
}

func (c *toolCache) getFromCache(accountId string, toolName string) (NBTool, bool) {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	if toolMap, ok := c.data[accountId]; ok {
		if tool, ok := toolMap[strings.ToLower(toolName)]; ok {
			return tool, true
		}
	}
	return nil, false
}

func (c *toolCache) set(accountId string, tools []NBTool) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	toolMap := make(map[string]NBTool, len(tools))
	for _, tool := range tools {
		toolMap[strings.ToLower(tool.Name())] = tool
	}
	c.data[accountId] = toolMap
	slog.Info(logCacheSet, "account_id", accountId, "tools_count", len(tools))
}

func invalidateLocalCaches(accountId string) {
	enabledToolsCacheInstance.delete(accountId)
	toolDtoCacheInstance.delete(accountId)
	customToolDtoCacheInstance.delete(accountId)
	accountConfigSummaryCacheInstance.delete(accountId)
	InvalidateAllCaches(accountId)
}

func updateToolCache(accountId string) {
	// Load fresh data from DB and update cache
	tools := loadCustomNbToolsFromDB(accountId)
	toolCacheInstance.set(accountId, tools)
	invalidateLocalCaches(accountId)
	// Broadcast to other replicas
	if err := common.CachePublish(CacheChannelToolInvalidation, accountId); err != nil {
		slog.Error("tools: failed to broadcast tool invalidation", "error", err, "account_id", accountId)
	}
}

func InvalidateToolCache(accountId string) {
	updateToolCache(accountId)
}

type ToolType string

// Error messages
const (
	errAccountIDRequired  = "accountId is required"
	errToolNameRequired   = "tool.name is required"
	errToolIDRequired     = "tool.id is required"
	errToolStatusRequired = "tool.status is required"
	errUserIDRequired     = "userId is required"
	errUnauthorized       = "auth: unauthorized"
	errToolNameNotValid   = "tool.name doesnt follow naming guidelines"
)

// Log messages
const (
	logFailedDBManager = "tools: failed to get database manager"
	logFailedCloseRows = "tools: failed to close rows"
	logFailedScanTool  = "tools: failed to scan tool"
	logFailedGetTools  = "tools: failed to get tools"

	// Cache log messages
	logCacheHit    = "tools: cache hit for account"
	logCacheMiss   = "tools: cache miss for account"
	logCacheSet    = "tools: cache updated for account"
	logCacheDelete = "tools: cache deleted for account"
)

const (
	ToolTypeSystem ToolType = "system"
	ToolTypeCustom ToolType = "custom"
)

type ToolStatus string

const (
	ToolStatusEnabled  ToolStatus = "enabled"
	ToolStatusDisabled ToolStatus = "disabled"
	ToolStatusDraft    ToolStatus = "draft"
	ToolStatusError    ToolStatus = "error"
)

type ToolExecutorType string

const (
	ToolExecutorTypeSystem ToolExecutorType = "system"
	// ToolExecutorTypeWorkflow retains the "runbook" string value for backward compatibility
	// with existing DB rows. New tool creation with this executor type is deprecated —
	// use workflow tools instead. Do not compare against the literal "workflow" string.
	ToolExecutorTypeWorkflow  ToolExecutorType = "runbook"
	ToolExecutorTypeMCP       ToolExecutorType = "mcp"
	ToolExecutorTypeContainer ToolExecutorType = "container"
)

type ToolDto struct {
	Id           string            `json:"id" db:"id"`
	Name         string            `json:"name" db:"name"`
	Description  string            `json:"description" db:"description"`
	Config       map[string]any    `json:"config" db:"config"`
	Type         ToolType          `json:"type" db:"type"`
	NBToolType   NBToolType        `json:"nb_tool_type" db:"nb_tool_type"`
	InputSchema  ToolSchema        `json:"schema" db:"input_schema"`
	Status       ToolStatus        `json:"status" db:"status"`
	ExecutorType ToolExecutorType  `json:"executor_type" db:"executor_type"`
	CreatedBy    string            `json:"created_by" db:"created_by"`
	CreatedAt    time.Time         `json:"created_at" db:"created_at"`
	UpdatedAt    time.Time         `json:"updated_at" db:"updated_at"`
	NeedsConfig  bool              `json:"needs_config"`
	IsConfigured bool              `json:"is_configured"`
	ConfigSchema *ToolConfigSchema `json:"config_schema,omitempty"`
}

func getSystemToolDto(context *security.RequestContext, accountId string, toolName string, summary AccountConfigSummary) ToolDto {
	nbTool, exists := GetNBTool(accountId, toolName)
	if !exists {
		return ToolDto{
			Id:     toolName,
			Name:   toolName,
			Type:   ToolTypeSystem,
			Status: ToolStatusDisabled,
		}
	}

	dto := ToolDto{
		Id:           toolName,
		Name:         nbTool.Name(),
		Type:         ToolTypeSystem,
		Status:       ToolStatusEnabled,
		Description:  nbTool.Description(),
		ExecutorType: ToolExecutorTypeSystem,
		Config:       map[string]any{},
		InputSchema:  nbTool.InputSchema(),
		NBToolType:   nbTool.GetType(),
	}

	if configTool, ok := nbTool.(NBToolConfig); ok {
		dto.NeedsConfig = true
		schema := configTool.ConfigSchema(context)
		dto.ConfigSchema = &schema
		dto.IsConfigured = IsToolConfigured(context, accountId, nbTool, summary)
	}

	return dto
}

func ListTools(context *security.RequestContext, accountId string) []ToolDto {
	if !context.GetSecurityContext().HasAccountAccess(accountId, "read") {
		slog.Error("tools: unauthorized access to list tools", "account_id", accountId)
		return []ToolDto{}
	}

	if dtos, ok := toolDtoCacheInstance.get(accountId); ok {
		return dtos
	}

	summary, err := GetAccountConfigSummary(context, accountId)
	if err != nil {
		slog.Error("tools: failed to get account config summary", "error", err)
	}

	tools := make([]ToolDto, len(nbSystemTools))
	var wg sync.WaitGroup

	// Use a semaphore to limit concurrency to 20 to prevent resource exhaustion
	sem := make(chan struct{}, 20)

	i := 0
	for toolName := range nbSystemTools {
		wg.Add(1)
		go func(idx int, name string) {
			defer wg.Done()
			sem <- struct{}{}        // Acquire
			defer func() { <-sem }() // Release

			dto := getSystemToolDto(context, accountId, name, summary)
			tools[idx] = dto
		}(i, toolName)
		i++
	}
	wg.Wait()

	finalTools := make([]ToolDto, 0, len(tools)+10)
	for _, t := range tools {
		if t.Name != "" {
			finalTools = append(finalTools, t)
		}
	}

	for _, t := range ListCustomTools(accountId) {
		if t.ExecutorType == ToolExecutorTypeMCP {
			continue
		}
		finalTools = append(finalTools, t)
	}
	finalTools = append(finalTools, ListMCPIntegrationToolDtos(accountId)...)
	toolDtoCacheInstance.set(accountId, finalTools)
	return finalTools
}

func GetEnabledNBTools(context *security.RequestContext, accountId string) []NBTool {
	if !context.GetSecurityContext().HasAccountAccess(accountId, "read") {
		slog.Error("tools: unauthorized access to get enabled tools", "account_id", accountId)
		return []NBTool{}
	}

	if tools, ok := enabledToolsCacheInstance.get(accountId); ok {
		return tools
	}

	summary, err := GetAccountConfigSummary(context, accountId)
	if err != nil {
		slog.Error("tools: failed to get account config summary", "error", err)
	}

	// Iterate `nbSystemTools` in a deterministic, sorted order. Map iteration in Go
	// is randomized; without sorting first, the resulting tool slice ends up in a
	// different order on every cache refill, which mutates the system prompt's
	// tool_descriptions block and busts the account-scope LLM cache.
	toolNames := make([]string, 0, len(nbSystemTools))
	for toolName := range nbSystemTools {
		toolNames = append(toolNames, toolName)
	}
	sort.Strings(toolNames)

	systemTools := make([]NBTool, len(toolNames))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 20)

	for idx, toolName := range toolNames {
		wg.Add(1)
		go func(idx int, name string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			if tool, ok := GetNBTool(accountId, name); ok {
				if !IsToolConfigured(context, accountId, tool, summary) {
					return
				}

				// For leaf tools, we must verify at least one config exists.
				// For agents, IsToolConfigured is already sufficient and avoids recursion.
				if tool.GetType() == NBToolTypeTool {
					if _, ok := tool.(NBToolConfig); ok {
						configs, err := ListToolConfigs(context, accountId, tool)
						if err != nil || len(configs) == 0 {
							return
						}
					}
				}
				systemTools[idx] = tool
			}
		}(idx, toolName)
	}
	wg.Wait()

	finalTools := make([]NBTool, 0, len(systemTools)+10)
	for _, t := range systemTools {
		if t != nil {
			finalTools = append(finalTools, t)
		}
	}

	finalTools = append(finalTools, ListCustomNbTool(accountId, ToolStatusEnabled)...)
	finalTools = append(finalTools, ListMCPIntegrationTools(accountId)...)
	enabledToolsCacheInstance.set(accountId, finalTools)
	return finalTools
}

func getToolStatus(tool NBTool) ToolStatus {
	switch t := tool.(type) {
	case nbCustomMCPTool:
		return t.tool.Status
	case nbCustomContainerTool:
		return t.tool.Status
	case mcpIntegrationTool:
		return ToolStatusEnabled
	default:
		return ""
	}
}

func CreateCustomTool(context *security.RequestContext, accountId string, tool ToolDto) (ToolDto, error) {
	if accountId == "" {
		return ToolDto{}, errors.New(errAccountIDRequired)
	}

	tool.Name = strings.TrimSpace(tool.Name)
	if tool.Name == "" {
		return ToolDto{}, errors.New(errToolNameRequired)
	}

	if !common.IsValidName(tool.Name) {
		return ToolDto{}, errors.New(errToolNameNotValid)

	}

	tool.Description = strings.TrimSpace(tool.Description)
	if tool.Description == "" {
		return ToolDto{}, errors.New("tool.description is required")
	}

	tool.Id = uuid.NewString()
	tool.Type = ToolTypeCustom
	if tool.Status == "" {
		tool.Status = ToolStatusEnabled
	}

	tenantId := context.GetSecurityContext().GetTenantId()
	if tenantId == "" {
		return ToolDto{}, errors.New("auth: tenantId is required")
	}

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		slog.Error(logFailedDBManager, "error", err)
		return ToolDto{}, err
	}

	// validate if user has access
	if !context.GetSecurityContext().HasAccountAccess(accountId, "write") {
		slog.Error("tools: failed to get account access")
		return ToolDto{}, errors.New(errUnauthorized)
	}

	// check if tool exists with same name
	toolIds, err := getCustomToolIdByNameAndAccountId(accountId, tool.Name)
	if err != nil {
		slog.Error("tools: failed to get custom tool", "error", err, "tool", tool.Name, "account", accountId)
		return ToolDto{}, err
	}
	if len(toolIds) > 0 {
		return ToolDto{}, errors.New("tools: tool with same name already exists")
	}
	toolConfigJson := []byte("{}")
	if len(tool.Config) > 0 {
		toolConfigJson, err = common.MarshalJson(tool.Config)
		if err != nil {
			slog.Error("tools: failed to marshal tool config", "error", err)
			return ToolDto{}, err
		}
	}

	switch tool.ExecutorType {
	case "":
		return ToolDto{}, errors.New("tools: executor_type is required for custom tool")
	case ToolExecutorTypeSystem:
		return ToolDto{}, errors.New("tools: system executor type is not allowed for custom tool")
	case ToolExecutorTypeWorkflow:
		return ToolDto{}, errors.New("tools: workflow executor type is deprecated, use workflow tools instead")
	}

	//executor type validation
	switch tool.ExecutorType {
	case ToolExecutorTypeMCP:
		return ToolDto{}, errors.New("MCP tools are now managed via integrations, create an MCP integration instead")
	case ToolExecutorTypeContainer:
		// Assuming a similar validation function for container type
		schema, err := customToolContainerValidateConfigAndReturnSchema(tool.Config, accountId)
		if err != nil {
			return ToolDto{}, err
		}
		tool.InputSchema = schema
	default:
		return ToolDto{}, errors.New("tools: unsupported executor_type for custom tool: " + string(tool.ExecutorType))
	}

	toolSchemaJson, err := common.MarshalJson(tool.InputSchema)
	if err != nil {
		slog.Error("tools: failed to marshal tool schema", "error", err)
		return ToolDto{}, err
	}

	if tool.NBToolType == "" { // Set default only if not provided
		tool.NBToolType = NBToolTypeTool
	}

	nullableCreatedBy := sql.NullString{String: tool.CreatedBy, Valid: tool.CreatedBy != ""}

	toolAny, err := dbms.DoInTransaction(func(tx *sqlx.Tx) (any, error) {
		_, err = tx.Exec("insert into llm_tools (id, name, description, config, type, status, executor_type, created_by, input_schema, nb_tool_type, tenant_id) values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)",
			tool.Id, tool.Name, tool.Description, string(toolConfigJson), tool.Type, tool.Status, tool.ExecutorType, nullableCreatedBy, string(toolSchemaJson), tool.NBToolType, tenantId)
		if err != nil {
			slog.Error("tools: failed to insert tool", "error", err)
			return ToolDto{}, err
		}

		_, err = tx.Exec("insert into llm_tools_installation (tool_id, account_id, id) values ($1, $2, $3)",
			tool.Id, accountId, uuid.NewString(),
		)
		if err != nil {
			slog.Error("tools: failed to insert tool installation", "error", err)
			return ToolDto{}, err
		}

		return tool, nil
	})

	if err == nil {
		toolInstance := toolAny.(ToolDto)
		// Create audit entry for tool creation
		auditReq := &audit.AuditRequest{
			Audits: []audit.Audit{
				{
					AccountId:     accountId,
					EventTime:     time.Now().UTC(),
					EventCategory: "tool",
					EventType:     "CUSTOM_TOOL_CREATE",
					EventState:    toolInstance,
					EventActor:    audit.EventActor(context.GetSecurityContext().GetUserId()),
					EventTarget:   toolInstance.Id,
					EventAction:   audit.EventActionCreate,
					EventStatus:   audit.EventStatusSuccess,
					TransactionId: context.GetTraceId(),
					EventAttr:     map[string]any{"tool_id": toolInstance.Id, "tool_name": toolInstance.Name},
					TenantId:      context.GetSecurityContext().GetTenantId(),
					UserId:        context.GetSecurityContext().GetUserId(),
				},
			},
		}
		auditErr := audit.CreateAudit(context, auditReq)
		if auditErr != nil {
			slog.Error("tools: failed to create audit entry for custom tool creation", "error", auditErr, "tool_id", toolInstance.Id)
			// Do not return auditErr, as the main operation (tool creation) was successful.
		}
	}

	toolDto := toolAny.(ToolDto)
	// Update cache if operation succeeded
	if err == nil {
		updateToolCache(accountId)
	}
	return toolDto, err
}

func getCustomToolIdByNameAndAccountId(accountId string, name string) ([]string, error) {
	if accountId == "" {
		return []string{}, errors.New(errAccountIDRequired)
	}

	if name == "" {
		return []string{}, errors.New(errToolNameRequired)
	}
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		slog.Error(logFailedDBManager, "error", err)
		return []string{}, err
	}

	rows, err := dbms.Query("select id::text from llm_tools where lower(name) = lower($1) and \"type\" = $2 and id in (select tool_id from llm_tools_installation where account_id = $3)", name, ToolTypeCustom, accountId)
	if err != nil {
		slog.Error("tools: failed to get tool", "error", err, "name", name, "accountId", accountId)
		return []string{}, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Error(logFailedCloseRows, "error", err)
		}
	}()

	toolsIds := []string{}
	for rows.Next() {
		var id string
		err = rows.Scan(&id)
		if err != nil {
			slog.Error("tools: failed to scan tool id", "error", err)
			continue
		}
		toolsIds = append(toolsIds, id)
	}

	return toolsIds, nil
}

func DeleteCustomTool(context *security.RequestContext, accountId, name string) error {

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		slog.Error(logFailedDBManager, "error", err)
		return err
	}

	// validate if user has access
	if !context.GetSecurityContext().HasAccountAccess(accountId, "write") {
		slog.Error("tools: failed to get account access", "error", err)
		return errors.New(errUnauthorized)
	}

	// check if tool exists
	toolIds, err := getCustomToolIdByNameAndAccountId(accountId, name)
	if err != nil {
		slog.Error("tools: failed to get custom tool", "error", err, "name", name, "accountId", accountId)
		return err
	}

	if len(toolIds) == 0 {
		return errors.New("tool: tool not found - " + name)
	}

	toolIdToDelete := toolIds[0]
	var completeToolForAudit ToolDto

	// Fetch complete tool details for audit before deletion
	rows, err := dbms.Db.Queryx("SELECT * FROM llm_tools WHERE id = $1", toolIdToDelete)
	if err != nil {
		slog.Error("tools: failed to query tool for audit before deletion", "error", err, "toolId", toolIdToDelete)
		// Continue with deletion even if audit fetch fails, but log the error.
	} else {
		defer func() {
			if err := rows.Close(); err != nil {
				slog.Error(logFailedCloseRows, "error", err)
			}
		}()
		if rows.Next() {
			completeToolForAudit, err = serializeRowToTool(rows)
			if err != nil {
				slog.Error("tools: failed to serialize tool for audit before deletion", "error", err, "toolId", toolIdToDelete)
				// Continue with deletion, audit record might be incomplete or missing.
			}
		} else {
			slog.Warn("tools: no tool found for audit before deletion", "toolId", toolIdToDelete)
			// Tool might have been deleted by another process, or this is an unexpected state.
			// If completeToolForAudit remains empty, audit record will reflect that.
		}
	}

	_, err = dbms.DoInTransaction(func(tx *sqlx.Tx) (any, error) {
		_, err = tx.Exec("delete from llm_tools_installation where tool_id = $1", toolIdToDelete)
		if err != nil {
			slog.Error("tools: failed to delete tool installation", "error", err, "toolId", toolIdToDelete)
			return nil, err
		}

		_, err = tx.Exec("delete from llm_tools where id = $1", toolIdToDelete)
		if err != nil {
			slog.Error("tools: failed to delete tool main entry", "error", err, "toolId", toolIdToDelete)
			return nil, err
		}

		return nil, nil
	})

	if err == nil {
		// Delete all cache entries for this account
		updateToolCache(accountId)

		// Create audit entry for tool deletion
		auditReq := &audit.AuditRequest{
			Audits: []audit.Audit{
				{
					AccountId:     accountId,
					EventTime:     time.Now().UTC(),
					EventCategory: "tool",
					EventType:     "CUSTOM_TOOL_DELETE",
					EventState:    completeToolForAudit, // This is the state before deletion
					EventActor:    audit.EventActor(context.GetSecurityContext().GetUserId()),
					EventTarget:   toolIdToDelete,
					EventAction:   audit.EventActionDelete,
					EventStatus:   audit.EventStatusSuccess,
					TransactionId: context.GetTraceId(),
					EventAttr:     map[string]any{"tool_id": toolIdToDelete, "tool_name": name},
					TenantId:      context.GetSecurityContext().GetTenantId(),
					UserId:        context.GetSecurityContext().GetUserId(),
				},
			},
		}
		auditErr := audit.CreateAudit(context, auditReq)
		if auditErr != nil {
			slog.Error("tools: failed to create audit entry for custom tool deletion", "error", auditErr, "tool_id", toolIdToDelete)
			// Do not return auditErr, as the main operation (tool deletion) was successful.
		}
	}

	return err

}

func UpdateCustomTool(context *security.RequestContext, accountId, name string, tool ToolDto) error {
	if tool.ExecutorType == ToolExecutorTypeMCP {
		return errors.New("MCP tools are now managed via integrations, create an MCP integration instead")
	}

	if accountId == "" {
		return errors.New(errAccountIDRequired)
	}

	if tool.Id == "" {
		return errors.New(errToolIDRequired)
	}

	if !(common.IsValidName(name)) {
		return errors.New(errToolNameNotValid)
	}

	if tool.Name == "" {
		return errors.New(errToolNameRequired)
	}
	if tool.Status == "" {
		return errors.New(errToolStatusRequired)
	}

	userId := context.GetSecurityContext().GetUserId()
	if userId == "" {
		return errors.New(errUserIDRequired)
	}

	if !context.GetSecurityContext().HasAccountAccess(accountId, "update") {
		slog.Error("tool: failed to get account access")
		return errors.New(errUnauthorized)
	}

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		slog.Error("tool: failed to get database manager", "error", err)
		return err
	}

	toolId := tool.Id

	// Verify that the tool exists and is owned by the correct account and name using a transaction
	_, err = dbms.DoInTransaction(func(tx *sqlx.Tx) (any, error) {
		var exists bool
		err := tx.Get(&exists, `
		select exists(
			select 1 from llm_tools_installation where account_id = $1 and tool_id = $2
		)
		`, accountId, toolId)
		if err != nil {
			slog.Error("tool: failed to verify tool ownership", "error", err, "toolId", toolId, "account", accountId)
			return nil, err
		}
		if !exists {
			return nil, errors.New("tool not found or not owned by account - " + toolId)
		}
		return nil, nil
	})
	if err != nil {
		return err
	}

	// Transactional update
	_, err = dbms.DoInTransaction(func(tx *sqlx.Tx) (any, error) {
		configJSON, err := common.MarshalJson(tool.Config)
		if err != nil {
			slog.Error("tool: failed to marshal config", "error", err)
			return nil, err
		}
		schemaJSON, err := common.MarshalJson(tool.InputSchema)
		if err != nil {
			slog.Error("tool: failed to marshal schema", "error", err)
			return nil, err
		}
		_, err = tx.Exec(`
			UPDATE llm_tools
			SET name = $1, description = $2, config = $3, status = $4, input_schema = $5, executor_type = $6, nb_tool_type = $7, updated_at = NOW()
			WHERE id = $8`,
			tool.Name,
			tool.Description,
			string(configJSON),
			tool.Status,
			string(schemaJSON),
			tool.ExecutorType,
			tool.NBToolType,
			toolId,
		)
		if err != nil {
			slog.Error("tool: failed to update tool", "error", err, "tool", name)
			return nil, err
		}
		return nil, nil
	})
	if err != nil {
		return err
	}

	// Fetch complete tool details for audit
	var completeTool ToolDto
	rows, err := dbms.Db.Queryx("SELECT * FROM llm_tools WHERE id = $1", toolId)
	if err != nil {
		slog.Error("tool: failed to query tool for audit", "error", err, "toolId", toolId)
	} else {
		defer func() {
			if err := rows.Close(); err != nil {
				slog.Error(logFailedCloseRows, "error", err)
			}
		}()
		if rows.Next() {
			completeTool, err = serializeRowToTool(rows)
			if err != nil {
				slog.Error("tool: failed to serialize tool for audit", "error", err, "toolId", toolId)
			}
		} else {
			slog.Error("tool: no tool found for audit", "toolId", toolId)
		}
	}

	// Create audit entry for tool update
	auditReq := &audit.AuditRequest{
		Audits: []audit.Audit{
			{
				AccountId:     accountId,
				EventTime:     time.Now().UTC(),
				EventCategory: "tool",
				EventType:     "CUSTOM_TOOL_UPDATE",
				EventState:    completeTool,
				EventActor:    audit.EventActor(userId),
				EventTarget:   toolId,
				EventAction:   audit.EventActionUpdate,
				EventStatus:   audit.EventStatusSuccess,
				TransactionId: context.GetTraceId(),
				EventAttr:     map[string]any{"tool_id": toolId, "tool_name": completeTool.Name, "status": string(tool.Status)},
				TenantId:      context.GetSecurityContext().GetTenantId(),
				UserId:        userId,
			},
		},
	}
	auditErr := audit.CreateAudit(context, auditReq)
	if auditErr != nil {
		slog.Error("tool: failed to create audit entry", "error", auditErr)
	}

	// Update all caches and broadcast invalidation to other replicas
	updateToolCache(accountId)
	return nil
}

func serializeRowToTool(rows *sqlx.Rows) (ToolDto, error) {
	tool := ToolDto{}
	toolMap := map[string]any{}
	err := rows.MapScan(toolMap)
	if err != nil {
		slog.Error(logFailedScanTool, "error", err)
		return ToolDto{}, err
	}
	tool.Id = string(toolMap["id"].([]byte))
	tool.Name = toolMap["name"].(string)
	tool.Description = toolMap["description"].(string)
	tool.Type = ToolType(toolMap["type"].(string))
	tool.Status = ToolStatus(toolMap["status"].(string))
	tool.ExecutorType = ToolExecutorType(toolMap["executor_type"].(string))
	tool.NBToolType = NBToolType(toolMap["nb_tool_type"].(string))

	if toolMap["created_by"] != nil {
		tool.CreatedBy = string(toolMap["created_by"].([]byte))
	}

	if toolMap["created_at"] != nil {
		tool.CreatedAt = toolMap["created_at"].(time.Time)
	}

	if toolMap["updated_at"] != nil {
		tool.UpdatedAt = toolMap["updated_at"].(time.Time)
	}

	if toolMap["config"] != nil && len(toolMap["config"].([]byte)) > 0 && string(toolMap["config"].([]byte)) != "null" {
		configMap := map[string]any{}
		err = common.UnmarshalJson(toolMap["config"].([]byte), &configMap)
		if err != nil {
			slog.Error("tools: failed to unmarshal tool config", "error", err, "config", string(toolMap["config"].([]byte)))
			return ToolDto{}, fmt.Errorf("failed to unmarshal tool config: %w", err)
		}
		tool.Config = configMap
	}

	if toolMap["input_schema"] != nil && len(toolMap["input_schema"].([]byte)) > 0 && string(toolMap["input_schema"].([]byte)) != "null" {
		schemaMap := ToolSchema{}
		err = common.UnmarshalJson(toolMap["input_schema"].([]byte), &schemaMap)
		if err != nil {
			slog.Error("tools: failed to unmarshal tool schema", "error", err, "schema", string(toolMap["input_schema"].([]byte)))
			return ToolDto{}, fmt.Errorf("failed to unmarshal tool input_schema: %w", err)
		}
		tool.InputSchema = schemaMap
	}

	tool.IsConfigured = true
	tool.NeedsConfig = false

	return tool, nil
}

func ListCustomTools(accountId string) []ToolDto {
	if dtos, ok := customToolDtoCacheInstance.get(accountId); ok {
		return dtos
	}

	if accountId == "" {
		return []ToolDto{}
	}

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		slog.Error(logFailedDBManager, "error", err)
		return []ToolDto{}
	}
	tools := make([]ToolDto, 0)
	rows, err := dbms.Query("select lg.* from llm_tools lg join llm_tools_installation lgi on lg.id = lgi.tool_id where lgi.account_id = $1 and lg.type = $2", accountId, ToolTypeCustom)
	if err != nil {
		slog.Error(logFailedGetTools, "error", err)
		return []ToolDto{}
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Error(logFailedCloseRows, "error", err)
		}
	}()
	for rows.Next() {
		tool, err := serializeRowToTool(rows)
		if err != nil {
			slog.Error(logFailedScanTool, "error", err)
			continue
		}
		tools = append(tools, tool)
	}
	customToolDtoCacheInstance.set(accountId, tools)
	return tools
}

func GetCustomNbTool(accountId string, name string) (NBTool, bool) {
	if accountId == "" || name == "" {
		return nil, false
	}

	// Fast path: Check cache directly
	if tool, found := toolCacheInstance.getFromCache(accountId, name); found {
		return tool, true
	}

	// Slow path: Refresh cache and try again
	// ListCustomNbTool triggers a DB load if cache is missing
	_ = ListCustomNbTool(accountId, "")

	return toolCacheInstance.getFromCache(accountId, name)
}

func createNBToolFromDto(accountId string, tool ToolDto) (NBTool, error) {
	switch tool.ExecutorType {
	case ToolExecutorTypeWorkflow:
		// Workflow executor type is deprecated - use workflow tools instead
		slog.Warn("tools: workflow executor type is deprecated", "tool_id", tool.Id)
		return nil, errors.New("workflow executor type is deprecated, use workflow tools instead")
	case ToolExecutorTypeMCP:
		// MCP tools from llm_tools are deprecated — MCP tools now come from integrations.
		// Existing llm_tools MCP entries are ignored; use MCP integrations instead.
		slog.Warn("tools: MCP executor type in llm_tools is deprecated, use MCP integrations instead", "tool_id", tool.Id)
		return nil, errors.New("MCP tools are now managed via integrations")
	case ToolExecutorTypeContainer:
		return nbCustomContainerTool{
			tool: tool,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported executor_type: %s", tool.ExecutorType)
	}
}

func loadCustomNbToolsFromDB(accountId string) []NBTool {
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		slog.Error(logFailedDBManager, "error", err)
		return nil
	}

	rows, err := dbms.Query("select lg.* from llm_tools lg join llm_tools_installation lgi on lg.id = lgi.tool_id where lgi.account_id = $1 and lg.type = $2", accountId, ToolTypeCustom)
	if err != nil {
		slog.Error(logFailedGetTools, "error", err)
		return nil
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Error(logFailedCloseRows, "error", err)
		}
	}()

	toolDtos := []ToolDto{}
	for rows.Next() {
		toolDto, err := serializeRowToTool(rows)
		if err != nil {
			slog.Error(logFailedScanTool, "error", err)
			continue
		}
		toolDtos = append(toolDtos, toolDto)
	}

	tools := make([]NBTool, 0)
	for _, toolDto := range toolDtos {
		nbTool, err := createNBToolFromDto(accountId, toolDto)
		if err != nil {
			continue
		}
		tools = append(tools, nbTool)
	}

	// Sort by name so the resulting tool_descriptions block is stable across
	// cache refills. The DB query has no ORDER BY, so without this Postgres
	// row ordering can drift after VACUUM/REINDEX.
	sort.SliceStable(tools, func(i, j int) bool {
		return tools[i].Name() < tools[j].Name()
	})

	return tools
}

func filterToolsByStatus(tools []NBTool, status ToolStatus) []NBTool {
	if status == "" {
		return tools
	}
	filtered := make([]NBTool, 0, len(tools))
	for _, tool := range tools {
		if getToolStatus(tool) == status {
			filtered = append(filtered, tool)
		}
	}
	return filtered
}

func ListCustomNbTool(accountId string, status ToolStatus) []NBTool {
	if accountId == "" {
		return nil
	}

	// Check cache first
	if tools, exists := toolCacheInstance.get(accountId); exists {
		return filterToolsByStatus(tools, status)
	}

	// Cache miss - load from DB and update cache
	tools := loadCustomNbToolsFromDB(accountId)
	toolCacheInstance.set(accountId, tools)
	return filterToolsByStatus(tools, status)
}
