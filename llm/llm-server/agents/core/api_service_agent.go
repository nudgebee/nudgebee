package core

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"nudgebee/llm/audit"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"

	"github.com/samber/lo"
)

type AgentType string

const (
	AgentTypeSystem AgentType = "system"
	AgentTypeCustom AgentType = "custom"
)

type AgentStatus string

const (
	AgentStatusEnabled  AgentStatus = "enabled"
	AgentStatusDisabled AgentStatus = "disabled"
	AgentStatusDraft    AgentStatus = "draft"
)

type AgentDto struct {
	Id                    string           `json:"id" db:"id"`
	Name                  string           `json:"name" db:"name"`
	Aliases               []string         `json:"aliases" db:"aliases"`
	Description           string           `json:"description" db:"description"`
	Config                map[string]any   `json:"config" db:"config"`
	Type                  AgentType        `json:"type" db:"type"`
	Status                AgentStatus      `json:"status" db:"status"`
	SystemPrompt          string           `json:"system_prompt" db:"system_prompt"`
	SystemPromptVariables []string         `json:"system_prompt_variables" db:"system_prompt_variables"`
	ExecutorType          AgentPlannerType `json:"executor_type" db:"executor_type"`
	Tools                 []string         `json:"tools" db:"tools"`
	CreatedBy             string           `json:"created_by" db:"created_by"`
	CreatedAt             time.Time        `json:"created_at" db:"created_at"`
	UpdatedAt             time.Time        `json:"updated_at" db:"updated_at"`
	UpdatedBy             string           `json:"updated_by" db:"updated_by"`
	Overridden            bool             `json:"overridden,omitempty"`
}

type AgentUpdateDto struct {
	Id                    string            `json:"id" db:"id"`
	Name                  *string           `json:"name" db:"name"`
	Description           *string           `json:"description" db:"description"`
	SystemPrompt          *string           `json:"system_prompt" db:"system_prompt"`
	SystemPromptVariables []string          `json:"system_prompt_variables" db:"system_prompt_variables"`
	Tools                 []string          `json:"tools" db:"tools"`
	Config                map[string]any    `json:"config" db:"config"`
	Status                AgentStatus       `json:"status" db:"status"`
	ExecutorType          *AgentPlannerType `json:"executor_type,omitempty" db:"executor_type"`
	UpdatedBy             string            `json:"updated_by" db:"updated_by"`
	UpdatedAt             time.Time         `json:"updated_at" db:"updated_at"`
}

type AgentRagDto struct {
	Data     string `json:"data"`
	Format   string `json:"format"`
	Filename string `json:"filename"`
}

type AgentExtension struct {
	AgentName string   `json:"agent_name"`
	Prompt    string   `json:"prompt"`
	Tools     []string `json:"tools"`
}

func getLogger(context *security.RequestContext) *slog.Logger {
	if context != nil && context.GetLogger() != nil {
		return context.GetLogger()
	}
	return slog.Default()
}

func GetAgent(context *security.RequestContext, accountId string, name string) (AgentDto, bool) {
	if accountId == "" || name == "" {
		return AgentDto{}, false
	}

	for agent := range nbSystemAgents {
		if !strings.EqualFold(agent, name) {
			continue
		}
		nbAgent, exists1 := GetNBAgent(context, agent, accountId, "")
		if exists1 {
			tools := []string{}
			// skip tools for planner as they check across the tools and also take significant tme to load (fix why)
			if nbAgent.GetPlannerType() != AgentPlannerTypeReWoo {
				tools = lo.Map(nbAgent.GetSupportedTools(context), func(tool toolcore.NBTool, idx int) string { return tool.Name() })
			}
			return AgentDto{
				Name:         agent,
				Aliases:      nbAgent.GetNameAliases(),
				Type:         AgentTypeSystem,
				Status:       AgentStatusEnabled,
				Description:  nbAgent.GetDescription(),
				ExecutorType: nbAgent.GetPlannerType(),
				Tools:        tools,
				Config:       map[string]any{},
			}, true
		}
	}
	return AgentDto{}, false
}

func ListAgents(context *security.RequestContext, accountId string, allowOnlyEnabled bool) []AgentDto {
	if accountId == "" {
		return []AgentDto{}
	}

	agents := make([]AgentDto, 0, len(nbSystemAgents))
	agentMap := make(map[string]AgentDto)
	customAgents := ListCustomAgents(context, accountId, allowOnlyEnabled)

	// Create map of custom agents for lookup
	for _, ca := range customAgents {
		agentMap[strings.ToLower(ca.Name)] = ca
	}

	// First, add system agents
	for agent := range nbSystemAgents {
		//ignor agents which have same name as system agent as system agents already cover those
		if _, ok := agentMap[agent]; ok {
			continue
		}

		nbAgent, err := getSystemAgent(agent, accountId)
		if err == nil {
			tools := []string{}
			// skip tools for planner as they check across the tools and also take significant tme to load (fix why)
			if nbAgent.GetPlannerType() != AgentPlannerTypeReWoo {
				tools = lo.Map(nbAgent.GetSupportedTools(context), func(tool toolcore.NBTool, idx int) string { return tool.Name() })
			}
			agents = append(agents, AgentDto{
				Name:         agent,
				Aliases:      nbAgent.GetNameAliases(),
				Type:         AgentTypeSystem,
				Status:       AgentStatusEnabled,
				Description:  nbAgent.GetDescription(),
				ExecutorType: nbAgent.GetPlannerType(),
				Tools:        tools,
				Config:       map[string]any{},
			})
		} else {
			agents = append(agents, AgentDto{
				Name:   agent,
				Type:   AgentTypeSystem,
				Status: AgentStatusDisabled,
			})
		}
	}

	// Then, add custom agents with overridden flag
	for _, ca := range customAgents {
		// Check if this custom agent has same name as any system agent
		_, isSystemAgent := nbSystemAgents[strings.ToLower(ca.Name)]
		if isSystemAgent {
			ca.Overridden = true
		}
		agents = append(agents, ca)
	}

	return agents
}

func CreateCustomAgent(context *security.RequestContext, accountId string, agent AgentDto, rags []AgentRagDto, overrideAgent bool) (AgentDto, error) {
	if accountId == "" {
		return AgentDto{}, errors.New("accountId is required")
	}

	agent.Name = strings.TrimSpace(agent.Name)
	if agent.Name == "" {
		return AgentDto{}, errors.New("agent.name is required")
	}
	_, ok := GetAgent(context, accountId, agent.Name)
	// check if agent name is already used
	if ok && !overrideAgent {
		return AgentDto{}, fmt.Errorf("agent %s is already used", agent.Name)
	}

	if !common.IsValidName(agent.Name) {
		return AgentDto{}, errors.New("agent name doesnt follow naming guidelines")

	}

	agent.Description = strings.TrimSpace(agent.Description)
	if agent.Description == "" {
		return AgentDto{}, errors.New("agent.description is required")
	}

	tenantId := context.GetSecurityContext().GetTenantId()
	if tenantId == "" {
		return AgentDto{}, errors.New("auth: tenantId is required")
	}

	//validate tool/agent
	agent.ExecutorType = AgentPlannerTypeReAct
	if len(agent.Tools) > 0 {
		for _, tool := range agent.Tools {
			if tool == "" {
				return AgentDto{}, errors.New("agent.tools cannot contain empty tool names")
			}
			if strings.EqualFold(tool, agent.Name) {
				return AgentDto{}, errors.New("agent.tools cannot contain name of agent itself")
			}

			nbTool, exists := toolcore.GetNBTool(accountId, tool)
			if !exists {
				_, existAgent := GetCustomNbAgent(context, accountId, tool, AgentStatusEnabled)
				if !existAgent {
					return AgentDto{}, fmt.Errorf("agent.tools contains tool %s which does not exist", tool)
				}
				agent.ExecutorType = AgentPlannerTypeReWoo
			} else {
				if nbTool.GetType() == toolcore.NBToolTypeAgent {
					agent.ExecutorType = AgentPlannerTypeReWoo
				}
			}
		}

	}

	agent.SystemPrompt = strings.TrimSpace(agent.SystemPrompt)
	if agent.SystemPrompt == "" {
		return AgentDto{}, errors.New("agent.system_prompt is required")
	}

	agent.Id = uuid.NewString()
	agent.Type = AgentTypeCustom
	if agent.Status == "" {
		agent.Status = AgentStatusEnabled
	}
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		getLogger(context).Error("agent: failed to get database manager", "error", err)
		return AgentDto{}, err
	}

	// validate if user has access
	if !context.GetSecurityContext().HasAccountAccess(accountId, "write") {
		getLogger(context).Error("agent: failed to get account access")
		return AgentDto{}, errors.New("auth: unauthorized")
	}

	// check if agent exists
	agentIds, err := getCustomAgentIdByNameAndAccountId(context, accountId, agent.Name)
	if err != nil {
		getLogger(context).Error("agent: failed to get agent", "error", err)
		return AgentDto{}, err
	}
	if len(agentIds) > 0 {
		return AgentDto{}, errors.New("agent with same name already exists")
	}

	agentConfigJson := []byte("{}")
	systemPromptVariablesJson := []byte("[]")
	if len(agent.SystemPromptVariables) > 0 {
		systemPromptVariablesJson, err = common.MarshalJson(agent.SystemPromptVariables)
		if err != nil {
			getLogger(context).Error("agent: failed to marshal system prompt variables", "error", err)
			return AgentDto{}, err
		}
	}
	agentToolsJson := []byte("[]")
	if len(agent.Tools) > 0 {
		agentToolsJson, err = common.MarshalJson(agent.Tools)
		if err != nil {
			getLogger(context).Error("agent: failed to marshal agent tools", "error", err)
			return AgentDto{}, err
		}
	}

	nullableCreatedBy := sql.NullString{String: agent.CreatedBy, Valid: agent.CreatedBy != ""}

	agentAny, err := dbms.DoInTransaction(func(tx *sqlx.Tx) (any, error) {
		_, err = tx.Exec("insert into llm_agents (id, name, description, config, type, status, system_prompt, system_prompt_variables, executor_type, tools, created_by, tenant_id) values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)",
			agent.Id, agent.Name, agent.Description, string(agentConfigJson), agent.Type, agent.Status, agent.SystemPrompt, string(systemPromptVariablesJson), agent.ExecutorType, string(agentToolsJson), nullableCreatedBy, tenantId)
		if err != nil {
			getLogger(context).Error("agent: failed to insert agent", "error", err)
			return AgentDto{}, err
		}

		_, err = tx.Exec("insert into llm_agents_installation (agent_id, account_id, id) values ($1, $2, $3)",
			agent.Id, accountId, uuid.NewString(),
		)
		if err != nil {
			getLogger(context).Error("agent: failed to insert agent installation", "error", err)
			return AgentDto{}, err
		}

		return agent, nil
	})

	if len(rags) > 0 {
		for _, rag := range rags {
			_, err = toolcore.CreateAgentRag(context, accountId, agent.Name, rag.Data, rag.Format, rag.Filename)
			if err != nil {
				getLogger(context).Error("agent: failed to create agent rag", "error", err)
			}
		}
	}

	// Create audit entry for agent creation
	auditReq := &audit.AuditRequest{
		Audits: []audit.Audit{
			{
				AccountId:     accountId,
				EventTime:     time.Now().UTC(),
				EventCategory: "agent",
				EventType:     "custom_agent_creation",
				EventState:    agent,
				EventActor:    audit.EventActor(context.GetSecurityContext().GetUserId()),
				EventTarget:   agent.Id,
				EventAction:   audit.EventActionCreate,
				EventStatus:   audit.EventStatusSuccess,
				TransactionId: context.GetTraceId(),
				EventAttr:     map[string]any{"agent_name": agent.Name},
				TenantId:      context.GetSecurityContext().GetTenantId(),
				UserId:        context.GetSecurityContext().GetUserId(),
			},
		},
	}

	auditErr := audit.CreateAudit(context, auditReq)
	if auditErr != nil {
		getLogger(context).Error("agent: failed to create audit entry", "error", auditErr)
		// Don't return the audit error as it shouldn't affect the main operation
	}

	return agentAny.(AgentDto), err
}

// Define a struct to return both original and updated agent
type agentUpdateResult struct {
	Original AgentDto
	Updated  AgentUpdateDto
}

// Helper function to handle errors consistently
func handleAgentError(context *security.RequestContext, err error, message string, fields ...any) error {
	if err != nil {
		getLogger(context).Error(message, append([]any{"error", err}, fields...)...)
		return fmt.Errorf("%s: %w", message, err)
	}
	return nil
}

// Helper function to fetch agent data from a database row
func fetchAgentFromRow(context *security.RequestContext, row *sqlx.Row) (AgentDto, error) {
	var agent AgentDto

	// Variables for handling special data types
	var sysPromptVarsBytes, toolsBytes, configBytes []byte
	var createdBy, updatedBy sql.NullString

	errScan := row.Scan(
		&agent.Id,
		&agent.Name,
		&agent.Description,
		&agent.Status,
		&agent.SystemPrompt,
		&sysPromptVarsBytes,
		&toolsBytes,
		&configBytes,
		&agent.CreatedAt,
		&createdBy,
		&agent.UpdatedAt,
		&updatedBy,
	)

	if errScan != nil {
		return agent, errScan
	}

	// Set nullable string fields
	if createdBy.Valid {
		agent.CreatedBy = createdBy.String
	}
	if updatedBy.Valid {
		agent.UpdatedBy = updatedBy.String
	}

	// Parse arrays using JSON unmarshaling
	if len(sysPromptVarsBytes) > 0 {
		getLogger(context).Debug("agent: system_prompt_variables raw", "raw", string(sysPromptVarsBytes))
		if err := common.UnmarshalJson(sysPromptVarsBytes, &agent.SystemPromptVariables); err != nil {
			getLogger(context).Error("agent: failed to unmarshal system_prompt_variables", "error", err)
		}
	}

	if len(toolsBytes) > 0 {
		getLogger(context).Debug("agent: tools raw", "raw", string(toolsBytes))
		if err := common.UnmarshalJson(toolsBytes, &agent.Tools); err != nil {
			getLogger(context).Error("agent: failed to unmarshal tools", "error", err)
		}
	}

	// Parse config JSON
	if len(configBytes) > 0 {
		if err := common.UnmarshalJson(configBytes, &agent.Config); err != nil {
			getLogger(context).Error("agent: failed to parse config", "error", err)
		}
	}

	return agent, nil
}

// Helper function to build update query and arguments
func buildAgentUpdateQuery(context *security.RequestContext, agent AgentUpdateDto) (string, []any, int, error) {
	updateFields := []string{"status = $1", "updated_by = $2", "updated_at = $3"}
	args := []any{agent.Status, agent.UpdatedBy, agent.UpdatedAt}
	paramCount := 4

	// Add optional fields if they are provided
	if agent.Name != nil {
		updateFields = append(updateFields, fmt.Sprintf("name = $%d", paramCount))
		args = append(args, *agent.Name)
		paramCount++
	}

	if agent.Description != nil {
		updateFields = append(updateFields, fmt.Sprintf("description = $%d", paramCount))
		args = append(args, *agent.Description)
		paramCount++
	}

	if agent.SystemPrompt != nil {
		updateFields = append(updateFields, fmt.Sprintf("system_prompt = $%d", paramCount))
		args = append(args, *agent.SystemPrompt)
		paramCount++
	}

	if len(agent.SystemPromptVariables) > 0 {
		updateFields = append(updateFields, fmt.Sprintf("system_prompt_variables = $%d", paramCount))
		// Marshal system prompt variables to JSON
		systemPromptVarsJson, jsonErr := common.MarshalJson(agent.SystemPromptVariables)
		if jsonErr != nil {
			return "", nil, 0, handleAgentError(context, jsonErr, "agent: failed to marshal system prompt variables")
		}
		args = append(args, systemPromptVarsJson)
		paramCount++
	}

	if agent.Tools != nil {
		updateFields = append(updateFields, fmt.Sprintf("tools = $%d", paramCount))
		// Marshal tools to JSON
		agentToolsJson, toolsJsonErr := common.MarshalJson(agent.Tools)
		if toolsJsonErr != nil {
			return "", nil, 0, handleAgentError(context, toolsJsonErr, "agent: failed to marshal agent tools")
		}
		args = append(args, agentToolsJson)
		paramCount++
	}

	if len(agent.Config) > 0 {
		updateFields = append(updateFields, fmt.Sprintf("config = $%d", paramCount))
		configJSON, err := common.MarshalJson(agent.Config)
		if err != nil {
			return "", nil, 0, handleAgentError(context, err, "agent: failed to marshal config")
		}
		args = append(args, configJSON)
		paramCount++
	}

	if agent.ExecutorType != nil {
		updateFields = append(updateFields, fmt.Sprintf("executor_type = $%d", paramCount))
		args = append(args, *agent.ExecutorType)
		paramCount++
	}

	// Add the ID as the last parameter
	args = append(args, agent.Id)

	// Construct the final query
	query := fmt.Sprintf("UPDATE llm_agents SET %s WHERE id = $%d", strings.Join(updateFields, ", "), paramCount)

	return query, args, paramCount, nil
}

// Helper function to convert AgentDto to AgentUpdateDto
func convertToAgentUpdateDto(agent AgentDto) AgentUpdateDto {
	return AgentUpdateDto{
		Id:                    agent.Id,
		Name:                  &agent.Name,
		Description:           &agent.Description,
		Status:                agent.Status,
		SystemPrompt:          &agent.SystemPrompt,
		SystemPromptVariables: agent.SystemPromptVariables,
		Tools:                 agent.Tools,
		Config:                agent.Config,
		UpdatedBy:             agent.UpdatedBy,
		UpdatedAt:             agent.UpdatedAt,
	}
}

func UpdateCustomAgent(context *security.RequestContext, accountId string, agent AgentUpdateDto) (AgentUpdateDto, error) {
	// Input validation
	if accountId == "" {
		return AgentUpdateDto{}, errors.New("accountId is required")
	}

	if !common.IsValidName(*agent.Name) {
		return AgentUpdateDto{}, errors.New("agent name doesnt follow naming guidelines")
	}

	if agent.Id == "" {
		return AgentUpdateDto{}, errors.New("agent.id is required")
	}
	if agent.Status == "" {
		return AgentUpdateDto{}, errors.New("agent.status is required")
	}

	// Set default values if not provided
	if agent.UpdatedBy == "" {
		agent.UpdatedBy = context.GetSecurityContext().GetUserId()
	}

	// Authorization check
	if !context.GetSecurityContext().HasAccountAccess(accountId, "write") {
		return AgentUpdateDto{}, handleAgentError(context, errors.New("auth: unauthorized"), "agent: failed to get account access")
	}

	// Validate new tools and recompute executor_type when the tool list is being changed.
	// Mirrors the same logic as CreateCustomAgent so the stored planner type stays correct.
	if agent.Tools != nil {
		executorType := AgentPlannerTypeReAct
		for _, tool := range agent.Tools {
			if tool == "" {
				return AgentUpdateDto{}, errors.New("agent.tools cannot contain empty tool names")
			}
			nbTool, exists := toolcore.GetNBTool(accountId, tool)
			if !exists {
				_, existAgent := GetCustomNbAgent(context, accountId, tool, AgentStatusEnabled)
				if !existAgent {
					return AgentUpdateDto{}, fmt.Errorf("agent.tools contains tool %s which does not exist", tool)
				}
				executorType = AgentPlannerTypeReWoo
			} else if nbTool.GetType() == toolcore.NBToolTypeAgent {
				executorType = AgentPlannerTypeReWoo
			}
		}
		agent.ExecutorType = &executorType
	}

	// Get database connection
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return AgentUpdateDto{}, handleAgentError(context, err, "agent: failed to get database manager")
	}

	// Execute database operations in a transaction
	resultAny, err := dbms.DoInTransaction(func(tx *sqlx.Tx) (any, error) {
		// Fetch the original agent values before updating for auditing purposes
		originalQuery := "SELECT id, name, description, status, system_prompt, system_prompt_variables, tools, config, created_at, created_by, updated_at, updated_by FROM llm_agents WHERE id = $1"
		originalRow := tx.QueryRowx(originalQuery, agent.Id)

		var originalAgent AgentDto
		var errOriginal error
		originalAgent, errOriginal = fetchAgentFromRow(context, originalRow)
		if errOriginal != nil {
			getLogger(context).Error("agent: failed to fetch original agent for audit", "error", errOriginal)
			// Continue despite error - we still want to update the agent
		} else {
			getLogger(context).Info("agent: fetched original agent for audit",
				"agent_id", originalAgent.Id,
				"name", originalAgent.Name,
				"status", originalAgent.Status)
		}

		// Build the update query and arguments
		query, args, _, queryErr := buildAgentUpdateQuery(context, agent)
		if queryErr != nil {
			return AgentUpdateDto{}, queryErr
		}

		// Execute the update query
		_, execErr := tx.Exec(query, args...)
		if execErr != nil {
			return AgentUpdateDto{}, handleAgentError(context, execErr, "agent: failed to update agent")
		}

		// Verify the update by fetching the updated record using a prepared statement
		verifyQuery := "SELECT id, name, description, status, system_prompt, system_prompt_variables, tools, config, created_at, created_by, updated_at, updated_by FROM llm_agents WHERE id = $1"
		verifyStmt, prepErr := tx.Preparex(verifyQuery)
		if prepErr != nil {
			getLogger(context).Error("agent: failed to prepare verification statement", "error", prepErr)
			// Continue despite error - we still want to return the updated agent
		} else {
			defer func() {
				if err := verifyStmt.Close(); err != nil {
					getLogger(context).Error("agent: failed to close verification statement", "error", err)
				}
			}()
		}

		var updatedAgent AgentDto
		var verifyErr error
		row := tx.QueryRowx(verifyQuery, agent.Id)
		updatedAgent, verifyErr = fetchAgentFromRow(context, row)

		if verifyErr != nil {
			getLogger(context).Error("agent: failed to verify agent update", "error", verifyErr)
			// Not returning error here as the update itself was successful
			getLogger(context).Info("agent: continuing despite verification failure")
		} else {
			// Log verification success with key fields
			getLogger(context).Info("agent: update verified",
				"agent_id", updatedAgent.Id,
				"name", updatedAgent.Name,
				"status", updatedAgent.Status,
				"updated_at", updatedAgent.UpdatedAt)
		}

		// Convert AgentDto to AgentUpdateDto for return value
		updateResult := convertToAgentUpdateDto(updatedAgent)

		return agentUpdateResult{
			Original: originalAgent,
			Updated:  updateResult,
		}, nil
	})

	if err != nil {
		return AgentUpdateDto{}, handleAgentError(context, err, "agent: transaction failed")
	}

	// Evict cached tool list only after the transaction commits so concurrent requests
	// never see a warm cache backed by the pre-commit (now-stale) state.
	if agent.Tools != nil {
		InvalidateCustomAgentToolsCache(accountId, "")
	}

	// Type assertion to get the result from the transaction
	result, ok := resultAny.(agentUpdateResult)
	if !ok {
		return AgentUpdateDto{}, handleAgentError(context, errors.New("invalid result type"), "agent: failed to cast transaction result")
	}

	// Create audit entry with both original and updated agent states
	auditRequest := &audit.AuditRequest{
		Audits: []audit.Audit{
			{
				EventType:      audit.EventType("custom_agent_update"),
				EventCategory:  audit.EventCategoryUser,
				EventAction:    audit.EventActionUpdate,
				EventStatus:    audit.EventStatusSuccess,
				EventActor:     audit.EventActorApiService,
				EventTime:      time.Now().UTC(),
				UserId:         agent.UpdatedBy,
				TenantId:       context.GetSecurityContext().GetTenantId(),
				AccountId:      accountId,
				EventTarget:    agent.Id,
				EventState:     result.Updated,
				EventPrevState: result.Original,
				TransactionId:  context.GetTraceId(),
				EventAttr:      map[string]any{"agent_id": agent.Id, "agent_name": result.Original.Name, "status": string(agent.Status)},
			},
		},
	}

	auditErr := audit.CreateAudit(context, auditRequest)

	if auditErr != nil {
		getLogger(context).Error("agent: failed to create audit event", "error", auditErr)
		// Don't fail the update if audit creation fails
	}
	if auditErr != nil {
		getLogger(context).Error("agent: failed to create audit entry", "error", auditErr)
		// Don't return the audit error as it shouldn't affect the main operation
	}

	return result.Updated, nil
}

func getCustomAgentIdByNameAndAccountId(context *security.RequestContext, accountId string, name string) ([]string, error) {
	if accountId == "" {
		return []string{}, errors.New("accountId is required")
	}

	if name == "" {
		return []string{}, errors.New("agent.name is required")
	}
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		getLogger(context).Error("agent: failed to get database manager", "error", err)
		return []string{}, err
	}

	rows, err := dbms.Db.Queryx("select id::text from llm_agents where lower(name) = lower($1) and \"type\" = $2 and id::text in (select agent_id::text from llm_agents_installation where account_id = $3)", name, AgentTypeCustom, accountId)
	if err != nil {
		getLogger(context).Error("agent: failed to get agent", "error", err)
		return []string{}, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			getLogger(context).Error("agent: failed to close rows", "error", err)
		}
	}()

	agentIds := []string{}
	for rows.Next() {
		var id string
		err = rows.Scan(&id)
		if err != nil {
			getLogger(context).Error("agent: failed to scan agent id", "error", err)
			continue
		}
		agentIds = append(agentIds, id)
	}

	return agentIds, nil
}

func DeleteCustomAgent(context *security.RequestContext, accountId string, name string) error {

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		getLogger(context).Error("agent: failed to get database manager", "error", err)
		return err
	}

	// validate if user has access
	if !context.GetSecurityContext().HasAccountAccess(accountId, "write") {
		getLogger(context).Error("agent: failed to get account access", "error", err)
		return errors.New("auth: unauthorized")
	}

	// check if agent exists
	agentIds, err := getCustomAgentIdByNameAndAccountId(context, accountId, name)
	if err != nil {
		getLogger(context).Error("agent: failed to get agent", "error", err)
		return err
	}
	if len(agentIds) == 0 {
		return errors.New("agent: agent not found")
	}

	_, err = dbms.DoInTransaction(func(tx *sqlx.Tx) (any, error) {
		_, err = tx.Exec("delete from llm_agents_installation where agent_id = $1", agentIds[0])
		if err != nil {
			getLogger(context).Error("agent: failed to delete agent", "error", err)
			return nil, err
		}

		_, err = tx.Exec("delete from llm_agents where id = $1", agentIds[0])
		if err != nil {
			getLogger(context).Error("agent: failed to delete agent", "error", err)
			return nil, err
		}
		return nil, err
	})

	// Evict cached tool list for the deleted agent.
	InvalidateCustomAgentToolsCache(accountId, name)

	// Fetch complete agent details for audit before deletion
	var completeAgent AgentDto
	// This query might fetch no rows if the agent was already deleted by the transaction.
	// It's better to fetch before the delete transaction or pass necessary data.
	// However, sticking to minimal changes for now and ensuring rows.Close.
	rows, err := dbms.Db.Queryx("select lg.* from llm_agents lg where lg.id = $1", agentIds[0])
	if err != nil {
		getLogger(context).Error("agent: failed to query agent for audit post-delete-transaction", "error", err, "agent_id", agentIds[0])
	} else {
		defer func() {
			if err := rows.Close(); err != nil {
				getLogger(context).Error("agent: failed to close rows", "error", err)
			}
		}()
		if rows.Next() {
			completeAgent, err = serializeRowToAgent(context, rows)
			if err != nil {
				getLogger(context).Error("agent: failed to serialize agent for audit post-delete-transaction", "error", err, "agent_id", agentIds[0])
			}
		} else {
			getLogger(context).Warn("agent: no agent found for audit post-delete-transaction, likely already deleted", "agent_id", agentIds[0])
		}
	}

	// Create audit entry for agent deletion
	auditReq := &audit.AuditRequest{
		Audits: []audit.Audit{
			{
				AccountId:     accountId,
				EventTime:     time.Now().UTC(),
				EventCategory: "agent",
				EventType:     "custom_agent_deletion",
				EventState:    completeAgent, // Store the full agent data before deletion
				EventActor:    audit.EventActor(context.GetSecurityContext().GetUserId()),
				EventTarget:   agentIds[0],
				EventAction:   audit.EventActionDelete,
				EventStatus:   audit.EventStatusSuccess,
				TransactionId: context.GetTraceId(),
				EventAttr:     map[string]any{"agent_id": agentIds[0], "name": name},
				TenantId:      context.GetSecurityContext().GetTenantId(),
				UserId:        context.GetSecurityContext().GetUserId(),
			},
		},
	}

	auditErr := audit.CreateAudit(context, auditReq)
	if auditErr != nil {
		getLogger(context).Error("agent: failed to create audit entry", "error", auditErr)
		// Don't return the audit error as it shouldn't affect the main operation
	}

	return err

}

func serializeRowToAgent(context *security.RequestContext, rows *sqlx.Rows) (AgentDto, error) {
	agent := AgentDto{}
	agentMap := map[string]any{}
	err := rows.MapScan(agentMap)
	if err != nil {
		getLogger(context).Error("agent: failed to scan agent", "error", err)
		return AgentDto{}, err
	}
	agent.Id = string(agentMap["id"].([]byte))
	agent.Name = agentMap["name"].(string)
	agent.Description = agentMap["description"].(string)
	agent.Type = AgentType(agentMap["type"].(string))
	agent.Status = AgentStatus(agentMap["status"].(string))
	agent.ExecutorType = AgentPlannerType(agentMap["executor_type"].(string))

	if agentMap["created_by"] != nil {
		agent.CreatedBy = string(agentMap["created_by"].([]byte))
	}

	if agentMap["created_at"] != nil {
		agent.CreatedAt = agentMap["created_at"].(time.Time)
	}

	if agentMap["updated_at"] != nil {
		agent.UpdatedAt = agentMap["updated_at"].(time.Time)
	}

	if agentMap["config"] != nil {
		configMap := map[string]any{}
		if agentMap["config"] != nil && len(agentMap["config"].([]byte)) > 0 && string(agentMap["config"].([]byte)) != "null" {
			err = common.UnmarshalJson(agentMap["config"].([]byte), &configMap)
			if err != nil {
				getLogger(context).Error("agent: failed to unmarshal agent config", "error", err, "config", string(agentMap["config"].([]byte)))
				return AgentDto{}, fmt.Errorf("failed to unmarshal agent config: %w", err)
			}
			agent.Config = configMap
		}
	}

	agent.SystemPrompt = agentMap["system_prompt"].(string)
	if agentMap["system_prompt_variables"] != nil && len(agentMap["system_prompt_variables"].([]byte)) > 0 && string(agentMap["system_prompt_variables"].([]byte)) != "null" {
		systemPromptVariables := []string{}
		err = common.UnmarshalJson(agentMap["system_prompt_variables"].([]byte), &systemPromptVariables)
		if err != nil {
			getLogger(context).Error("agent: failed to unmarshal system prompt variables", "error", err, "variables", string(agentMap["system_prompt_variables"].([]byte)))
			return AgentDto{}, fmt.Errorf("failed to unmarshal system prompt variables: %w", err)
		}
		agent.SystemPromptVariables = systemPromptVariables
	}

	if agentMap["tools"] != nil && len(agentMap["tools"].([]byte)) > 0 && string(agentMap["tools"].([]byte)) != "null" {
		tools := []string{}
		err = common.UnmarshalJson(agentMap["tools"].([]byte), &tools)
		if err != nil {
			getLogger(context).Error("agent: failed to unmarshal agent tools", "error", err, "tools", string(agentMap["tools"].([]byte)))
			return AgentDto{}, fmt.Errorf("failed to unmarshal agent tools: %w", err)
		}
		agent.Tools = tools
	}

	return agent, nil
}

func ListCustomAgents(context *security.RequestContext, accountId string, allowOnlyEnabled bool) []AgentDto {
	if accountId == "" {
		return []AgentDto{}
	}

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		getLogger(context).Error("ListCustomAgents: failed to get database manager", "error", err)
		return []AgentDto{}
	}
	agents := make([]AgentDto, 0)

	var rows *sqlx.Rows
	if allowOnlyEnabled {
		rows, err = dbms.Db.Queryx("select lg.* from llm_agents lg join llm_agents_installation lgi on lg.id::text = lgi.agent_id::text where lgi.account_id = $1 and lg.type = $2 and status = $3 ", accountId, AgentTypeCustom, "enabled")
	} else {
		rows, err = dbms.Db.Queryx("select lg.* from llm_agents lg join llm_agents_installation lgi on lg.id::text = lgi.agent_id::text where lgi.account_id = $1 and lg.type = $2 ", accountId, AgentTypeCustom)
	}
	if err != nil {
		getLogger(context).Error("ListCustomAgents: failed to query custom agents", "error", err, "accountId", accountId, "allowOnlyEnabled", allowOnlyEnabled)
		return []AgentDto{}
	}
	defer func() {
		if err := rows.Close(); err != nil {
			getLogger(context).Error("ListCustomAgents: failed to close rows", "error", err)
		}
	}()
	for rows.Next() {
		agent, err := serializeRowToAgent(context, rows)
		if err != nil {
			getLogger(context).Error("ListCustomAgents: failed to serialize agent from row", "error", err)
			continue
		}
		agents = append(agents, agent)
	}
	return agents
}

func GetCustomNbAgent(context *security.RequestContext, accountId string, name string, status AgentStatus) (NBAgent, bool) {
	if accountId == "" || name == "" {
		return nil, false
	}

	if _, err := uuid.Parse(accountId); err != nil {
		getLogger(context).Error("GetCustomNbAgent: accountId is not a valid UUID", "error", err, "accountId", accountId, "name", name)
		return nil, false
	}

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		getLogger(context).Error("GetCustomNbAgent: failed to get database manager", "error", err)
		return nil, false
	}
	agents := make([]AgentDto, 0, 1)
	rows, err := dbms.Db.Queryx("select lg.* from llm_agents lg join llm_agents_installation lgi on lg.id::text = lgi.agent_id::text where lgi.account_id = $1 and lg.type = $2 and lower(lg.name) = $3", accountId, AgentTypeCustom, strings.ToLower(name))
	if err != nil {
		getLogger(context).Error("GetCustomNbAgent: failed to query custom agent by name", "error", err, "accountId", accountId, "agentName", name)
		return nil, false
	}
	defer func() {
		if err := rows.Close(); err != nil {
			getLogger(context).Error("GetCustomNbAgent: failed to close rows", "error", err)
		}
	}()
	for rows.Next() {
		agent, err := serializeRowToAgent(context, rows)
		if err != nil {
			getLogger(context).Error("GetCustomNbAgent: failed to serialize agent from row", "error", err, "agentName", name)
			continue
		}
		agents = append(agents, agent)
	}

	if len(agents) != 1 {
		return nil, false
	}

	if status != "" && agents[0].Status != status {
		return nil, false
	}

	return &nbCustomAgent{
		agent:     agents[0],
		accountId: accountId,
	}, true
}

func ListCustomNbAgent(context *security.RequestContext, accountId string, status AgentStatus) []NBAgent {
	if accountId == "" {
		return nil
	}

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		getLogger(context).Error("ListCustomNbAgent: failed to get database manager", "error", err)
		return nil
	}
	query := "select lg.* from llm_agents lg join llm_agents_installation lgi on lg.id::text = lgi.agent_id::text where lgi.account_id = $1 and lg.type = $2"
	args := []any{accountId, AgentTypeCustom}
	if status != "" {
		query += " and lg.status = $3"
		args = append(args, status)
	}

	rows, err := dbms.Db.Queryx(query, args...)
	if err != nil {
		getLogger(context).Error("ListCustomNbAgent: failed to query custom nb agents", "error", err, "accountId", accountId, "status", status)
		return nil
	}
	defer func() {
		if err := rows.Close(); err != nil {
			getLogger(context).Error("ListCustomNbAgent: failed to close rows", "error", err)
		}
	}()
	agentDtos := make([]AgentDto, 0, 10)
	for rows.Next() {
		agent, err := serializeRowToAgent(context, rows)
		if err != nil {
			getLogger(context).Error("ListCustomNbAgent: failed to serialize agent from row", "error", err)
			continue
		}
		agentDtos = append(agentDtos, agent)
	}

	nbAgents := make([]NBAgent, 0, len(agentDtos))
	for _, agent := range agentDtos {
		nbAgents = append(nbAgents, &nbCustomAgent{
			agent:     agent,
			accountId: accountId,
		})
	}

	return nbAgents
}

type agentConfigCache struct {
	mutex sync.RWMutex
	data  map[string]struct {
		instructions string
		tools        []string
		config       map[string]any
		expiry       time.Time
	}
}

var agentConfigCacheInstance = &agentConfigCache{
	data: make(map[string]struct {
		instructions string
		tools        []string
		config       map[string]any
		expiry       time.Time
	}),
}

func (c *agentConfigCache) get(accountId, agent string) (string, []string, map[string]any, bool) {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	key := accountId + ":" + agent
	item, exists := c.data[key]
	if exists && time.Now().Before(item.expiry) {
		return item.instructions, item.tools, item.config, true
	}
	return "", nil, nil, false
}

func (c *agentConfigCache) set(accountId, agent string, instructions string, tools []string, config map[string]any) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	key := accountId + ":" + agent
	c.data[key] = struct {
		instructions string
		tools        []string
		config       map[string]any
		expiry       time.Time
	}{
		instructions: instructions,
		tools:        tools,
		config:       config,
		expiry:       time.Now().Add(30 * time.Minute),
	}
}

func (c *agentConfigCache) delete(accountId, agent string) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	key := accountId + ":" + agent
	delete(c.data, key)
}

const CacheChannelAgentConfigInvalidation = "llm_agent_config_invalidation"

func init() {
	common.CacheSubscribe(CacheChannelAgentConfigInvalidation, func(message string) {
		parts := strings.Split(message, ":")
		if len(parts) == 2 {
			slog.Info("received global agent config invalidation", "account_id", parts[0], "agent", parts[1])
			agentConfigCacheInstance.delete(parts[0], parts[1])
			InvalidateAllAgentCaches(parts[0], parts[1])
		}
	})
}

func AgentAdditionalInstructionsAndToolsAndConfigs(context *security.RequestContext, accountId string, agent string) (string, []string, map[string]any) {
	if accountId == "" {
		return "", nil, nil
	}

	if instructions, tools, configMap, ok := agentConfigCacheInstance.get(accountId, agent); ok {
		return instructions, tools, configMap
	}

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		getLogger(context).Error("AgentAdditionalInstructionsAndToolsAndConfigs: failed to get database manager", "error", err)
		return "", nil, nil
	}
	query := `
    SELECT
        lgi.config ->> 'additional_instructions' AS config_additional_instructions,
        lgi.additional_instructions,
        lgi.tools::text,
        (lgi.config::jsonb - 'additional_instructions')::text AS agent_config
    FROM
        llm_agents_installation lgi
    WHERE
        lgi.account_id::text = $1
        AND (
            lgi.agent_id = $2
            OR
            lgi.agent_id = ANY(SELECT id::text FROM llm_agents WHERE name = $2)
        );`
	rows, err := dbms.Db.Queryx(query, accountId, agent)
	if err != nil {
		getLogger(context).Error("AgentAdditionalInstructionsAndToolsAndConfigs: failed to query agent extensions", "error", err, "accountId", accountId, "agent", agent)
		return "", nil, nil
	}
	defer func() {
		if err := rows.Close(); err != nil {
			getLogger(context).Error("AgentAdditionalInstructionsAndToolsAndConfigs: failed to close rows", "error", err)
		}
	}()
	var agentConfigAdditionalPrompt, agentAdditionalPrompt, agentToolsJson *string
	var configMapStr string
	for rows.Next() {
		err = rows.Scan(&agentConfigAdditionalPrompt, &agentAdditionalPrompt, &agentToolsJson, &configMapStr)
		if err != nil {
			getLogger(context).Error("AgentAdditionalInstructionsAndToolsAndConfigs: failed to scan row", "error", err, "agent", agent)
			return "", nil, nil
		}
	}
	var agentConfigMap map[string]any
	if configMapStr != "" {
		if err := common.UnmarshalJson([]byte(configMapStr), &agentConfigMap); err != nil {
			getLogger(context).Warn("agent: failed to unmarshal agent config map", "error", err, "config_map", configMapStr)
		}
	}

	additionalPrompt := ""
	if agentAdditionalPrompt != nil {
		additionalPrompt = *agentAdditionalPrompt
	} else if agentConfigAdditionalPrompt != nil {
		additionalPrompt = *agentConfigAdditionalPrompt
	}

	supportedTools := []string{}
	if agentToolsJson != nil && *agentToolsJson != "" {
		_ = common.UnmarshalJson([]byte(*agentToolsJson), &supportedTools)
	}

	agentConfigCacheInstance.set(accountId, agent, additionalPrompt, supportedTools, agentConfigMap)
	return additionalPrompt, supportedTools, agentConfigMap
}

func CreateAgentExtension(context *security.RequestContext, accountId string, agent AgentExtension, createdBy string) (AgentExtension, error) {
	if !context.GetSecurityContext().HasAccountAccess(accountId, "write") {
		return AgentExtension{}, errors.New("auth: unauthorized")
	}

	dbms, err := common.GetDatabaseManager(common.Metastore)

	if err != nil {
		return AgentExtension{}, err
	}

	createdAt := time.Now()

	toolsJson, err := common.MarshalJson(agent.Tools)
	if err != nil {
		return AgentExtension{}, err
	}

	// Check if extension already exists for this agent
	var existingId string
	err = dbms.Db.Get(&existingId, "SELECT id FROM llm_agents_installation WHERE agent_id = $1 AND account_id = $2", agent.AgentName, accountId)

	if err != nil && err != sql.ErrNoRows {
		return AgentExtension{}, err
	}

	if existingId != "" {
		// Update existing extension
		result, err := dbms.Exec("UPDATE llm_agents_installation SET additional_instructions = $1, tools = $2, updated_at = $3, updated_by = $4 WHERE id = $5",
			agent.Prompt, string(toolsJson), createdAt, createdBy, existingId)
		if err != nil {
			return AgentExtension{}, err
		}

		agentConfigCacheInstance.delete(accountId, agent.AgentName)
		InvalidateAllAgentCaches(accountId, agent.AgentName)
		if err := common.CachePublish(CacheChannelAgentConfigInvalidation, accountId+":"+agent.AgentName); err != nil {
			getLogger(context).Error("failed to broadcast agent config invalidation", "error", err)
		}

		rowsAffected, err := result.RowsAffected()
		if err != nil {
			getLogger(context).Error("function: failed to get rows affected", "error", err)
			return AgentExtension{}, err
		}
		if rowsAffected == 0 {
			getLogger(context).Error("function: no rows were updated")
			return AgentExtension{}, errors.New("no rows were updated")
		}
	} else {
		// Insert new extension
		id := common.GenerateUUID()
		result, err := dbms.Exec("INSERT INTO llm_agents_installation (id, agent_id, account_id, created_at, created_by, additional_instructions, tools) VALUES ($1, $2, $3, $4, $5, $6, $7)",
			id, agent.AgentName, accountId, createdAt, createdBy, agent.Prompt, string(toolsJson))
		if err != nil {
			return AgentExtension{}, err
		}

		rowsAffected, err := result.RowsAffected()
		if err != nil {
			getLogger(context).Error("function: failed to get rows affected", "error", err)
			return AgentExtension{}, err
		}
		if rowsAffected == 0 {
			getLogger(context).Error("function: no rows were inserted")
			return AgentExtension{}, errors.New("no rows were inserted")
		}
	}

	return agent, nil
}

func UpdateAgentExtension(context *security.RequestContext, accountId string, agent AgentExtension, updatedBy string) (AgentExtension, error) {
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return AgentExtension{}, err
	}

	updatedAt := time.Now()

	toolsJson, err := common.MarshalJson(agent.Tools)
	if err != nil {
		return AgentExtension{}, err
	}

	// Check if extension exists for this agent
	var existingId string
	err = dbms.Db.Get(&existingId, "SELECT id FROM llm_agents_installation WHERE agent_id = $1 AND account_id = $2", agent.AgentName, accountId)

	if err != nil {
		if err == sql.ErrNoRows {
			return AgentExtension{}, errors.New("agent extension not found")
		}
		return AgentExtension{}, err
	}

	// Update existing extension
	result, err := dbms.Exec("UPDATE llm_agents_installation SET additional_instructions = $1, tools = $2, updated_at = $3, updated_by = $4 WHERE id = $5",
		agent.Prompt, string(toolsJson), updatedAt, updatedBy, existingId)
	if err != nil {
		return AgentExtension{}, err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		getLogger(context).Error("UpdateAgentExtension: failed to get rows affected", "error", err)
		return AgentExtension{}, err
	}
	if rowsAffected == 0 {
		getLogger(context).Error("UpdateAgentExtension: no rows were updated")
		return AgentExtension{}, errors.New("no rows were updated")
	}

	return agent, nil
}

func DeleteAgentExtension(context *security.RequestContext, accountId string, agentName string) error {
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return err
	}

	// Check if extension exists for this agent
	var existingId string
	err = dbms.Db.Get(&existingId, "SELECT id FROM llm_agents_installation WHERE agent_id = $1 AND account_id = $2", agentName, accountId)

	if err != nil {
		if err == sql.ErrNoRows {
			return errors.New("agent extension not found")
		}
		return err
	}

	// Delete the extension
	result, err := dbms.Exec("DELETE FROM llm_agents_installation WHERE id = $1", existingId)
	if err != nil {
		return err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		getLogger(context).Error("DeleteAgentExtension: failed to get rows affected", "error", err)
		return err
	}
	if rowsAffected == 0 {
		getLogger(context).Error("DeleteAgentExtension: no rows were deleted")
		return errors.New("no rows were deleted")
	}

	return nil
}
