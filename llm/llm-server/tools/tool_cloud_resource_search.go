package tools

import (
	"fmt"
	"sort"
	"strings"

	"nudgebee/llm/common"
	"nudgebee/llm/tools/core"
)

const ToolCloudResourceSearch = "cloud_resource_search_execute"

func init() {
	core.RegisterNBToolFactory(ToolCloudResourceSearch, func(accountId string) (core.NBTool, error) {
		return CloudResourceSearchTool{}, nil
	})
}

type CloudResourceSearchTool struct{ core.ReadOnlyTool }

type CloudResourceSearchRequest struct {
	ResourceName string `json:"resource_name,omitempty"`
	Service      string `json:"service,omitempty"`
	ResourceType string `json:"resource_type,omitempty"`
	Region       string `json:"region,omitempty"`
}

type CloudResourceSearchResponse struct {
	Resources []CloudResourceInfo `json:"resources,omitempty"`
	Message   string              `json:"message,omitempty"`
}

type CloudResourceInfo struct {
	Name        string `json:"name" db:"name"`
	ResourceID  string `json:"resource_id,omitempty" db:"resource_id"`
	Type        string `json:"type" db:"type"`
	Service     string `json:"service" db:"service"`
	Region      string `json:"region" db:"region"`
	AccountID   string `json:"account_id" db:"account_id"`
	AccountName string `json:"account_name,omitempty" db:"account_name"`
	// Matches before the limit: overall, and per account name as JSON.
	Total         int    `json:"-" db:"total"`
	AccountCounts string `json:"-" db:"account_counts"`
}

func (r CloudResourceSearchTool) Name() string {
	return ToolCloudResourceSearch
}

func (r CloudResourceSearchTool) GetType() core.NBToolType {
	return core.NBToolTypeTool
}

func (r CloudResourceSearchTool) Description() string {
	return `Searches for cloud resources (AWS, GCP, Azure) indexed in the database.

**Usage:**
* **Resource Discovery:** Find cloud resources by name, service, or type.
* **Service-specific Search:** Search for resources within a specific service (e.g., S3, EC2, CloudSQL).

**Input:** JSON with the following fields:
* resource_name (optional): Name or partial name of the resource, or its full id (e.g. an EC2 instance id).
* service (optional): Cloud service name (e.g., 'EC2', 'S3', 'Lambda').
* resource_type (optional): Type of resource (e.g., 'instance', 'bucket').
* region (optional): Region to search in.

At least one of resource_name/service/resource_type/region is required.

**Output:** JSON with matching resource details.`
}

func (r CloudResourceSearchTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"resource_name": {
				Type:        core.ToolSchemaTypeString,
				Description: "Name or partial name of the resource, or its full id (e.g. an EC2 instance id)",
			},
			"service": {
				Type:        core.ToolSchemaTypeString,
				Description: "Cloud service name",
			},
			"resource_type": {
				Type:        core.ToolSchemaTypeString,
				Description: "Type of resource",
			},
			"region": {
				Type:        core.ToolSchemaTypeString,
				Description: "Region to search in",
			},
		},
		Required: []string{},
	}
}

// missRegionHint builds the empty-result message that bounds a live fallback to the
// regions this account actually uses (from cloud_resourses). It returns "" when there
// are no known regions, so the caller keeps the plain "Found 0" message. Kept pure so
// the message shape + service-scope branch are unit-testable without a DB.
func missRegionHint(resourceName, service string, regions []string) string {
	if len(regions) == 0 {
		return ""
	}
	scope := ""
	if service != "" {
		scope = " " + service
	}
	// resourceName is empty for service/type-only searches (e.g. "list all Lambdas").
	matchTarget := "matching your query"
	if resourceName != "" {
		matchTarget = fmt.Sprintf("matching '%s'", resourceName)
	}
	return fmt.Sprintf(
		"Found 0 resources %s. This account has%s resources in these regions ONLY: %s. "+
			"If it may be new/not-yet-synced, check ONLY these regions — do NOT try other regions or enumerate regions via shell. "+
			"If it is in none of these, it does not exist; report that rather than searching further.",
		matchTarget, scope, strings.Join(regions, ", "))
}

func (r CloudResourceSearchTool) Call(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	// The search is tenant-wide; the target decides whose regions a
	// no-match hint suggests.
	nbRequestContext = nbRequestContext.ForTarget()
	var request CloudResourceSearchRequest
	inputData := input.Command
	if inputData == "" && input.Arguments != nil {
		if b, err := common.MarshalJson(input.Arguments); err == nil {
			inputData = string(b)
		}
	}

	if err := common.UnmarshalJson([]byte(inputData), &request); err != nil {
		return core.NBToolResponse{Data: err.Error(), Status: core.NBToolResponseStatusError}, err
	}

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return core.NBToolResponse{Data: err.Error(), Status: core.NBToolResponseStatusError}, err
	}

	// 1. Get Tenant ID from the current Account
	var tenantId string
	_ = dbms.Db.Get(&tenantId, "SELECT tenant FROM cloud_accounts WHERE id = $1", nbRequestContext.AccountId)

	// 2. Query resources across all accounts in the tenant OR fallback to current account
	// Require at least one search criterion to avoid returning all cloud resources
	if request.ResourceName == "" && request.Service == "" && request.ResourceType == "" && request.Region == "" {
		response := CloudResourceSearchResponse{
			Message: "Please provide at least one search criterion (resource_name, service, resource_type, or region).",
		}
		responseJSON, _ := common.MarshalJson(response)
		return core.NBToolResponse{
			Data:   string(responseJSON),
			Type:   core.NBToolResponseTypeJson,
			Status: core.NBToolResponseStatusSuccess,
		}, nil
	}

	var query string
	var args []interface{}
	if tenantId != "" {
		query = `
			SELECT cr.name, cr.resourse_id AS resource_id, cr.type, cr.service_name as service, cr.region, cr.account as account_id, ca.account_name
			FROM cloud_resourses cr
			JOIN cloud_accounts ca ON cr.account = ca.id
			WHERE ca.tenant = $1 AND cr.is_active = true AND ca.status = 'active'
		`
		args = append(args, tenantId)
	} else {
		query = `
			SELECT cr.name, cr.resourse_id AS resource_id, cr.type, cr.service_name as service, cr.region, cr.account as account_id, ca.account_name
			FROM cloud_resourses cr
			LEFT JOIN cloud_accounts ca ON cr.account = ca.id
			WHERE cr.account = $1 AND cr.is_active = true
		`
		args = append(args, nbRequestContext.AccountId)
	}
	argIdx := 2

	if request.ResourceName != "" {
		// A full resource id (i-0abc…, a bucket name) finds its resource
		// exactly; a substring match on ids would add noise to name searches.
		query += fmt.Sprintf(" AND (cr.name ILIKE $%d OR cr.resourse_id = $%d)", argIdx, argIdx+1)
		args = append(args, "%"+request.ResourceName+"%", request.ResourceName)
		argIdx += 2
	}
	if request.Service != "" {
		query += fmt.Sprintf(" AND cr.service_name ILIKE $%d", argIdx)
		args = append(args, "%"+request.Service+"%")
		argIdx++
	}
	if request.ResourceType != "" {
		query += fmt.Sprintf(" AND cr.type ILIKE $%d", argIdx)
		args = append(args, "%"+request.ResourceType+"%")
		argIdx++
	}
	if request.Region != "" {
		query += fmt.Sprintf(" AND cr.region = $%d", argIdx)
		args = append(args, request.Region)
	}

	query = cloudResourceSearchPage(query)

	var resources []CloudResourceInfo
	rows, err := dbms.Db.Queryx(query, args...)
	if err != nil {
		return core.NBToolResponse{Data: err.Error(), Status: core.NBToolResponseStatusError}, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			nbRequestContext.Ctx.GetLogger().Warn("cloud_resource_search: failed to close rows", "error", err)
		}
	}()

	for rows.Next() {
		var res CloudResourceInfo
		if err := rows.StructScan(&res); err == nil {
			resources = append(resources, res)
		}
	}

	nbRequestContext.Ctx.GetLogger().Info("cloud_resource_search: db lookup result", "name", request.ResourceName, "count", len(resources), "tenant", tenantId)

	response := CloudResourceSearchResponse{
		Resources: resources,
		Message:   fmt.Sprintf("Found %d cloud resources matching your query.", len(resources)) + cloudCutoffNote(resources),
	}

	// On a miss, bound any live fallback to the regions this account actually uses.
	// cloud_resourses is the inventory of record for synced resources, so a name absent
	// here is either not-yet-synced (worth a live check — but ONLY in the account's real
	// regions) or nonexistent. Without this the model scans all ~16 regions and even
	// enumerates them via shell (observed session 9ab533bd). Scoped to the CURRENT account
	// (the live CLI runs against its creds) and to the service when one was given; 'global'
	// and empty are dropped since they aren't real --region values.
	// Fire on ANY miss — a service/type-only search ("list all Lambdas") that returns
	// nothing benefits from the region bound just as much as a named-resource lookup.
	if len(resources) == 0 {
		regionQuery := `SELECT DISTINCT region FROM cloud_resourses
			WHERE account = $1 AND is_active = true
			  AND region IS NOT NULL AND region <> '' AND LOWER(region) <> 'global'`
		regionArgs := []interface{}{nbRequestContext.AccountId}
		if request.Service != "" {
			regionQuery += ` AND service_name ILIKE $2`
			regionArgs = append(regionArgs, "%"+request.Service+"%")
		}
		regionQuery += ` ORDER BY region`

		var regions []string
		usedService := request.Service
		if svcErr := dbms.Db.Select(&regions, regionQuery, regionArgs...); svcErr != nil {
			// Non-fatal: keep the plain "Found 0" message, but don't swallow the error —
			// a silently-skipped hint would look like "account has no regions". Skip the
			// downstream widen/hint entirely on error (empty regions here mean "unknown",
			// not "none").
			nbRequestContext.Ctx.GetLogger().Warn("cloud_resource_search: region-bound query failed; miss hint skipped",
				"error", svcErr, "account", nbRequestContext.AccountId, "service", request.Service)
		} else {
			// Widen to account-wide regions when the service-scoped query SUCCEEDED but
			// returned nothing (first resource of this service, or a service string matching
			// no synced service_name).
			if len(regions) == 0 && request.Service != "" {
				widenQuery := `SELECT DISTINCT region FROM cloud_resourses
					WHERE account = $1 AND is_active = true
					  AND region IS NOT NULL AND region <> '' AND LOWER(region) <> 'global'
					ORDER BY region`
				if err := dbms.Db.Select(&regions, widenQuery, nbRequestContext.AccountId); err != nil {
					nbRequestContext.Ctx.GetLogger().Warn("cloud_resource_search: account-wide region widening failed",
						"error", err, "account", nbRequestContext.AccountId)
				} else {
					usedService = "" // regions are now account-wide, not service-specific
				}
			}
			if hint := missRegionHint(request.ResourceName, usedService, regions); hint != "" {
				response.Message = hint
			}
		}
	}

	responseJSON, _ := common.MarshalJson(response)
	return core.NBToolResponse{
		Data:   string(responseJSON),
		Type:   core.NBToolResponseTypeJson,
		Status: core.NBToolResponseStatusSuccess,
	}, nil
}

// cloudResourceSearchLimit caps one lookup's rows; cloudCutoffNote says when
// more matched.
const cloudResourceSearchLimit = 50

// cloudResourceSearchPage wraps the filtered lookup so the limit is shared
// across accounts (round-robin by name within each account) and the result
// carries per-account match counts. Ordered by account name alone, the first
// account alphabetically filled every row: a Lambda search returned 20
// aws-demo functions and hid dev-aws, where the throttled one was (#39201).
func cloudResourceSearchPage(filtered string) string {
	return fmt.Sprintf(`
		WITH m AS (%s)
		SELECT name, resource_id, type, service, region, account_id, COALESCE(account_name, '') AS account_name,
		       COUNT(*) OVER () AS total,
		       COALESCE((SELECT json_object_agg(acct, n)::text FROM (
		            SELECT COALESCE(account_name, account_id::text, 'unknown') AS acct, COUNT(*) AS n FROM m GROUP BY 1) a), '{}') AS account_counts
		FROM (
			SELECT m.*, ROW_NUMBER() OVER (PARTITION BY m.account_id ORDER BY m.name) AS rn
			FROM m
		) ranked
		ORDER BY rn, COALESCE(account_name, ''), name
		LIMIT %d`, filtered, cloudResourceSearchLimit)
}

// cloudCutoffNote tells the caller when more resources matched than were
// returned, with per-account counts, so it can pick the right account or
// narrow the search. Empty when nothing was cut.
func cloudCutoffNote(resources []CloudResourceInfo) string {
	if len(resources) == 0 || resources[0].Total <= len(resources) {
		return ""
	}
	// Counted over every match, so accounts with nothing on the page still show.
	byAccount := map[string]int{}
	_ = common.UnmarshalJson([]byte(resources[0].AccountCounts), &byAccount)
	names := make([]string, 0, len(byAccount))
	for n := range byAccount {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool {
		if byAccount[names[i]] != byAccount[names[j]] {
			return byAccount[names[i]] > byAccount[names[j]]
		}
		return names[i] < names[j]
	})
	parts := make([]string, 0, len(names))
	for _, n := range names {
		parts = append(parts, fmt.Sprintf("%s %d", n, byAccount[n]))
	}
	return fmt.Sprintf(" Showing %d of %d (by account: %s); narrow with resource_name, service, resource_type or region to see the rest.", len(resources), resources[0].Total, strings.Join(parts, ", "))
}
