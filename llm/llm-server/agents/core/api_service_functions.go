package core

import (
	"database/sql"
	"errors"
	"log/slog"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

type LLMFunctionDto struct {
	Id               string         `json:"id" db:"id"`
	Name             string         `json:"name" db:"name" validate:"required,max=50"`
	Description      string         `json:"description" db:"description" validate:"required,max=1000"`
	Prompt           string         `json:"prompt" db:"prompt" validate:"required"`
	Variables        []string       `json:"variables" db:"variables"`
	VariableDefaults map[string]any `json:"variable_defaults" db:"variable_defaults"`
	Status           string         `json:"status" db:"status"`
	Version          int            `json:"version" db:"version"`
	AccountId        string         `json:"account_id" db:"account_id"`
	TenantId         string         `json:"tenant_id" db:"tenant_id"`
	CreatedBy        string         `json:"created_by" db:"created_by"`
	CreatedAt        time.Time      `json:"created_at" db:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at" db:"updated_at"`
	UpdatedBy        string         `json:"updated_by" db:"updated_by"`
}

// DeleteLLMFunction deletes an LLM function from the database
func DeleteLLMFunction(agentContext *security.RequestContext, accountId string, functionId string) (LLMFunctionDto, error) {
	if accountId == "" {
		return LLMFunctionDto{}, errors.New("accountId is required")
	}

	if functionId == "" {
		return LLMFunctionDto{}, errors.New("functionId is required")
	}

	// Get database manager
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		slog.Error("function: failed to get database manager", "error", err)
		return LLMFunctionDto{}, err
	}
	slog.Info("function: successfully got database manager")

	// Validate if user has access
	hasAccess := agentContext.GetSecurityContext().HasAccountAccess(accountId, security.SecurityAccessTypeDelete)
	if !hasAccess {
		slog.Error("function: user does not have account access",
			"user_id", agentContext.GetSecurityContext().GetUserId(),
			"account_id", accountId,
			"tenant_id", agentContext.GetSecurityContext().GetTenantId())
		return LLMFunctionDto{}, errors.New("auth: unauthorized")
	}
	slog.Info("function: user has account access", "user_id", agentContext.GetSecurityContext().GetUserId())

	// Delete from database within transaction
	deletedFunctionAny, err := dbms.DoInTransaction(func(tx *sqlx.Tx) (any, error) {
		// First, get the function details for audit logging
		var deletedFunction LLMFunctionDto
		err := tx.Get(&deletedFunction,
			"SELECT id, name, description, account_id, tenant_id, created_by FROM public.llm_functions WHERE id = $1 AND account_id = $2 AND tenant_id = $3",
			functionId, accountId, agentContext.GetSecurityContext().GetTenantId())
		if err != nil {
			slog.Error("function: failed to find function for deletion", "error", err, "function_id", functionId)
			return nil, errors.New("function not found or access denied")
		}

		// Now delete the function
		result, err := tx.Exec(
			"DELETE FROM public.llm_functions WHERE id = $1 AND account_id = $2 AND tenant_id = $3",
			functionId, accountId, agentContext.GetSecurityContext().GetTenantId())
		if err != nil {
			slog.Error("function: failed to delete function", "error", err, "function_id", functionId)
			return nil, err
		}

		// Check if any rows were affected
		rowsAffected, err := result.RowsAffected()
		if err != nil {
			slog.Error("function: failed to get rows affected", "error", err)
			return nil, err
		}
		if rowsAffected == 0 {
			slog.Error("function: no rows were deleted", "function_id", functionId)
			return nil, errors.New("function not found or access denied")
		}

		slog.Info("function: successfully deleted function", "rows_affected", rowsAffected, "function_id", functionId)
		return deletedFunction, nil
	})

	if err != nil {
		slog.Error("function: failed to delete function in transaction", "error", err)
		return LLMFunctionDto{}, err
	}

	// Cast the result back to FunctionDto for audit logging
	deletedFunction := deletedFunctionAny.(LLMFunctionDto)

	slog.Info("LLM Function deleted successfully",
		"function_id", deletedFunction.Id,
		"function_name", deletedFunction.Name,
		"account_id", accountId,
		"deleted_by", agentContext.GetSecurityContext().GetUserId())

	return deletedFunction, nil
}

// CreateLLMFunction creates a new LLM function in the database
func CreateLLMFunction(agentContext *security.RequestContext, accountId string, function LLMFunctionDto) (LLMFunctionDto, error) {
	if accountId == "" {
		return LLMFunctionDto{}, errors.New("accountId is required")
	}

	if function.Prompt == "" {
		return LLMFunctionDto{}, errors.New("function.prompt is required")
	}

	if function.Status != "" && function.Status != "draft" && function.Status != "active" {
		return LLMFunctionDto{}, errors.New("function.status must be either 'draft' or 'active'")
	}
	// Trim and validate function name
	function.Name = strings.TrimSpace(function.Name)
	if function.Name == "" {
		return LLMFunctionDto{}, errors.New("function.name is required")
	}

	if !common.IsValidName(function.Name) {
		return LLMFunctionDto{}, errors.New("function name doesn't follow naming guidelines")
	}

	// Trim and validate description
	function.Description = strings.TrimSpace(function.Description)
	if function.Description == "" {
		return LLMFunctionDto{}, errors.New("function.description is required")
	}

	// Generate ID if not provided
	if function.Id == "" {
		function.Id = uuid.NewString()
	}

	// Set default status if not provided
	if function.Status == "" {
		function.Status = "draft"
	}

	// Set timestamps
	now := time.Now().UTC()
	function.CreatedAt = now

	// Set tenant and account info
	function.TenantId = agentContext.GetSecurityContext().GetTenantId()
	function.AccountId = accountId

	// Get database manager
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		slog.Error("function: failed to get database manager", "error", err)
		return LLMFunctionDto{}, err
	}
	slog.Info("function: successfully got database manager")

	// Validate if user has access
	hasAccess := agentContext.GetSecurityContext().HasAccountAccess(accountId, security.SecurityAccessTypeCreate)
	if !hasAccess {
		slog.Error("function: user does not have account access",
			"user_id", agentContext.GetSecurityContext().GetUserId(),
			"account_id", accountId,
			"tenant_id", agentContext.GetSecurityContext().GetTenantId())
		return LLMFunctionDto{}, errors.New("auth: unauthorized")
	}
	slog.Info("function: user has account access", "user_id", agentContext.GetSecurityContext().GetUserId())

	// Marshal variables and variable_defaults to JSON
	variablesJson := []byte("[]")
	if len(function.Variables) > 0 {
		variablesJson, err = common.MarshalJson(function.Variables)
		if err != nil {
			slog.Error("function: failed to marshal variables", "error", err)
			return LLMFunctionDto{}, err
		}
	}

	variableDefaultsJson := []byte("{}")
	if function.VariableDefaults != nil {
		variableDefaultsJson, err = common.MarshalJson(function.VariableDefaults)
		if err != nil {
			slog.Error("function: failed to marshal variable defaults", "error", err)
			return LLMFunctionDto{}, err
		}
	}

	// Validate UUID fields are not empty (already validated in the handler)
	if function.CreatedBy == "" {
		return LLMFunctionDto{}, errors.New("created_by is required")
	}

	// Log the data being inserted for debugging
	slog.Info("function: attempting to insert function",
		"id", function.Id,
		"name", function.Name,
		"description", function.Description,
		"prompt_length", len(function.Prompt),
		"variables", string(variablesJson),
		"variable_defaults", string(variableDefaultsJson),
		"status", function.Status,
		"version", function.Version,
		"account_id", function.AccountId,
		"tenant_id", function.TenantId,
		"created_by", function.CreatedBy)

	// Insert into database within transaction
	functionAny, err := dbms.DoInTransaction(func(tx *sqlx.Tx) (any, error) {

		// Try the insertion with better error handling
		result, err := tx.Exec(`INSERT INTO public.llm_functions 
			(id, name, description, prompt, variables, variable_defaults, status, version, account_id, tenant_id, created_by, created_at) 
			VALUES ($1, $2, $3, $4, $5::jsonb, $6::jsonb, $7, $8, $9, $10, $11, $12)`,
			function.Id, function.Name, function.Description, function.Prompt,
			string(variablesJson), string(variableDefaultsJson), function.Status, function.Version,
			function.AccountId, function.TenantId, function.CreatedBy,
			function.CreatedAt)
		if err != nil {
			slog.Error("function: failed to insert function", "error", err, "sql_error", err.Error())
			return LLMFunctionDto{}, err
		}

		// Check if any rows were affected
		rowsAffected, err := result.RowsAffected()
		if err != nil {
			slog.Error("function: failed to get rows affected", "error", err)
			return LLMFunctionDto{}, err
		}
		if rowsAffected == 0 {
			slog.Error("function: no rows were inserted")
			return LLMFunctionDto{}, errors.New("no rows were inserted")
		}

		slog.Info("function: successfully inserted function", "rows_affected", rowsAffected)
		return function, nil
	})

	if err != nil {
		slog.Error("function: failed to create function in transaction", "error", err)
		return LLMFunctionDto{}, err
	}

	// Cast the result back to FunctionDto
	createdFunction := functionAny.(LLMFunctionDto)

	slog.Info("LLM Function created successfully",
		"function_id", createdFunction.Id,
		"function_name", createdFunction.Name,
		"account_id", accountId,
		"created_by", createdFunction.CreatedBy)

	return createdFunction, nil
}

// Get function by name
func GetLLMFunctionByName(agentContext *security.RequestContext, accountId string, functionName string) (LLMFunctionDto, error) {
	if accountId == "" {
		return LLMFunctionDto{}, errors.New("accountId is required")
	}

	if functionName == "" {
		return LLMFunctionDto{}, errors.New("functionName is required")
	}

	// Get database manager
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		slog.Error("function: failed to get database manager", "error", err)
		return LLMFunctionDto{}, err
	}
	slog.Debug("function: successfully got database manager")

	// Validate if user has access
	hasAccess := agentContext.GetSecurityContext().HasAccountAccess(accountId, security.SecurityAccessTypeRead)
	if !hasAccess {
		slog.Error("function: user does not have account access",
			"user_id", agentContext.GetSecurityContext().GetUserId(),
			"account_id", accountId,
			"tenant_id", agentContext.GetSecurityContext().GetTenantId())
		return LLMFunctionDto{}, errors.New("auth: unauthorized")
	}
	slog.Debug("function: user has account access", "user_id", agentContext.GetSecurityContext().GetUserId())

	// Query the function and manually unmarshal JSON fields (variables, variable_defaults)
	var function LLMFunctionDto

	// We'll query into local vars so we can unmarshal JSON fields safely
	var id, name, description, prompt, status, accountIdRow, tenantIdRow string
	var createdBy, updatedBy sql.NullString
	var version int
	var createdAt, updatedAt time.Time
	var variablesJSON, variableDefaultsJSON sql.NullString

	row, err := dbms.QueryRow(
		"SELECT id, name, description, prompt, variables::text, variable_defaults::text, status, version, account_id, tenant_id, created_by, created_at, updated_by, updated_at "+
			"FROM public.llm_functions WHERE lower(name) = lower($1) AND account_id = $2 AND tenant_id = $3",
		functionName, accountId, agentContext.GetSecurityContext().GetTenantId())
	if err != nil {
		slog.Error("function: failed to execute query to get function by name", "error", err, "function_name", functionName)
		return LLMFunctionDto{}, errors.New("function not found or access denied")
	}

	if err := row.Scan(&id, &name, &description, &prompt, &variablesJSON, &variableDefaultsJSON, &status, &version, &accountIdRow, &tenantIdRow, &createdBy, &createdAt, &updatedBy, &updatedAt); err != nil {
		slog.Error("function: failed to scan row for function", "error", err, "function_name", functionName)
		return LLMFunctionDto{}, errors.New("function not found or access denied")
	}

	// Populate struct fields
	function.Id = id
	function.Name = name
	function.Description = description
	function.Prompt = prompt
	function.Status = status
	function.Version = version
	function.AccountId = accountIdRow
	function.TenantId = tenantIdRow
	function.CreatedBy = createdBy.String
	function.CreatedAt = createdAt
	function.UpdatedBy = updatedBy.String
	function.UpdatedAt = updatedAt

	// Unmarshal variables JSON
	if variablesJSON.Valid {
		var vars []string
		if err := common.UnmarshalJson([]byte(variablesJSON.String), &vars); err != nil {
			slog.Error("function: failed to unmarshal variables JSON", "error", err)
			return LLMFunctionDto{}, err
		}
		function.Variables = vars
	} else {
		function.Variables = []string{}
	}

	// Unmarshal variable_defaults JSON
	if variableDefaultsJSON.Valid {
		var vdefs map[string]any
		if err := common.UnmarshalJson([]byte(variableDefaultsJSON.String), &vdefs); err != nil {
			slog.Error("function: failed to unmarshal variable_defaults JSON", "error", err)
			return LLMFunctionDto{}, err
		}
		function.VariableDefaults = vdefs
	} else {
		function.VariableDefaults = map[string]any{}
	}

	slog.Info("function: successfully retrieved function by name", "function_id", function.Id, "function_name", function.Name)
	return function, nil
}

// UpdateLLMFunction updates an existing LLM function in the database
func UpdateLLMFunction(agentContext *security.RequestContext, accountId string, functionId string, function LLMFunctionDto) (LLMFunctionDto, error) {
	if accountId == "" {
		return LLMFunctionDto{}, errors.New("accountId is required")
	}

	if functionId == "" {
		return LLMFunctionDto{}, errors.New("functionId is required")
	}

	if function.Prompt == "" {
		return LLMFunctionDto{}, errors.New("function.prompt is required")
	}

	if function.Status != "" && function.Status != "draft" && function.Status != "active" {
		return LLMFunctionDto{}, errors.New("function.status must be either 'draft' or 'active'")
	}

	// Trim and validate function name
	function.Name = strings.TrimSpace(function.Name)
	if function.Name == "" {
		return LLMFunctionDto{}, errors.New("function.name is required")
	}
	if !common.IsValidName(function.Name) {
		return LLMFunctionDto{}, errors.New("function name doesn't follow naming guidelines")
	}

	// Trim and validate description
	function.Description = strings.TrimSpace(function.Description)
	if function.Description == "" {
		return LLMFunctionDto{}, errors.New("function.description is required")
	}

	// Set update timestamp
	function.UpdatedAt = time.Now().UTC()

	// Get database manager
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		slog.Error("function: failed to get database manager", "error", err)
		return LLMFunctionDto{}, err
	}
	slog.Info("function: successfully got database manager")

	// Validate if user has access
	hasAccess := agentContext.GetSecurityContext().HasAccountAccess(accountId, security.SecurityAccessTypeUpdate)
	if !hasAccess {
		slog.Error("function: user does not have account access",
			"user_id", agentContext.GetSecurityContext().GetUserId(),
			"account_id", accountId,
			"tenant_id", agentContext.GetSecurityContext().GetTenantId())
		return LLMFunctionDto{}, errors.New("auth: unauthorized")
	}
	slog.Info("function: user has account access", "user_id", agentContext.GetSecurityContext().GetUserId())

	// Marshal variables and variable_defaults to JSON
	variablesJson := []byte("[]")
	if len(function.Variables) > 0 {
		variablesJson, err = common.MarshalJson(function.Variables)
		if err != nil {
			slog.Error("function: failed to marshal variables", "error", err)
			return LLMFunctionDto{}, err
		}
	}

	variableDefaultsJson := []byte("{}")
	if function.VariableDefaults != nil {
		variableDefaultsJson, err = common.MarshalJson(function.VariableDefaults)
		if err != nil {
			slog.Error("function: failed to marshal variable defaults", "error", err)
			return LLMFunctionDto{}, err
		}
	}

	// Validate UpdatedBy is not empty
	if function.UpdatedBy == "" {
		return LLMFunctionDto{}, errors.New("updated_by is required")
	}

	// Log the data being updated for debugging
	slog.Info("function: attempting to update function",
		"id", functionId,
		"name", function.Name,
		"description", function.Description,
		"prompt_length", len(function.Prompt),
		"variables", string(variablesJson),
		"variable_defaults", string(variableDefaultsJson),
		"status", function.Status,
		"account_id", accountId,
		"tenant_id", agentContext.GetSecurityContext().GetTenantId(),
		"updated_by", function.UpdatedBy)

	// Update in database within transaction
	functionAny, err := dbms.DoInTransaction(func(tx *sqlx.Tx) (any, error) {
		// First check if function exists and user has access
		var existingFunction LLMFunctionDto
		err := tx.Get(&existingFunction,
			"SELECT id, name, description, version FROM public.llm_functions WHERE id = $1 AND account_id = $2 AND tenant_id = $3",
			functionId, accountId, agentContext.GetSecurityContext().GetTenantId())
		if err != nil {
			slog.Error("function: failed to find function for update", "error", err, "function_id", functionId)
			return nil, errors.New("function not found or access denied")
		}

		// Increment version
		newVersion := existingFunction.Version + 1

		// Update the function
		result, err := tx.Exec(`UPDATE public.llm_functions 
			SET name = $1, description = $2, prompt = $3, variables = $4::jsonb, variable_defaults = $5::jsonb, 
			    status = $6, version = $7, updated_by = $8, updated_at = $9
			WHERE id = $10 AND account_id = $11 AND tenant_id = $12`,
			function.Name, function.Description, function.Prompt,
			string(variablesJson), string(variableDefaultsJson), function.Status, newVersion,
			function.UpdatedBy, function.UpdatedAt,
			functionId, accountId, agentContext.GetSecurityContext().GetTenantId())
		if err != nil {
			slog.Error("function: failed to update function", "error", err, "function_id", functionId)
			return LLMFunctionDto{}, err
		}

		// Check if any rows were affected
		rowsAffected, err := result.RowsAffected()
		if err != nil {
			slog.Error("function: failed to get rows affected", "error", err)
			return LLMFunctionDto{}, err
		}
		if rowsAffected == 0 {
			slog.Error("function: no rows were updated", "function_id", functionId)
			return LLMFunctionDto{}, errors.New("function not found or access denied")
		}

		// Set the updated version
		function.Version = newVersion
		function.Id = functionId

		slog.Info("function: successfully updated function", "rows_affected", rowsAffected, "function_id", functionId)
		return function, nil
	})

	if err != nil {
		slog.Error("function: failed to update function in transaction", "error", err)
		return LLMFunctionDto{}, err
	}

	// Cast the result back to FunctionDto
	updatedFunction := functionAny.(LLMFunctionDto)

	slog.Info("LLM Function updated successfully",
		"function_id", updatedFunction.Id,
		"function_name", updatedFunction.Name,
		"account_id", accountId,
		"updated_by", updatedFunction.UpdatedBy,
		"new_version", updatedFunction.Version)

	return updatedFunction, nil
}
