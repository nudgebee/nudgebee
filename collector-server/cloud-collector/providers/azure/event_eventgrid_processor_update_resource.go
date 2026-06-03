package azure

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"nudgebee/collector/cloud/common"
	"nudgebee/collector/cloud/providers"
	"strings"
	"time"
)

// deriveAzureResourceNameAndType extracts the resource name and full ARM type
// from an Azure resource ID of the form:
//
//	/subscriptions/{sub}/resourceGroups/{rg}/providers/{provider}/{type1}/{name1}[/{type2}/{name2}/...]
//
// Returns ("", "") if the ID is malformed.
func deriveAzureResourceNameAndType(resourceId string) (name string, fullType string) {
	idx := strings.Index(strings.ToLower(resourceId), "/providers/")
	if idx < 0 {
		return "", ""
	}
	tail := resourceId[idx+len("/providers/"):]
	parts := strings.Split(tail, "/")
	if len(parts) < 2 {
		return "", ""
	}
	provider := parts[0]
	rest := parts[1:]
	if len(rest) == 0 || len(rest)%2 != 0 {
		return "", ""
	}
	typeSegments := []string{provider}
	for i := 0; i < len(rest); i += 2 {
		typeSegments = append(typeSegments, rest[i])
	}
	name = rest[len(rest)-1]
	fullType = strings.Join(typeSegments, "/")
	return name, fullType
}

// executeUpdateCloudResourceAction updates a resource in the cloud_resourses table.
func (p *TemplatedEventGridProcessor) executeUpdateCloudResourceAction(ctx providers.CloudProviderContext, account providers.Account, params map[string]interface{}) (interface{}, error) {
	logger := ctx.GetLogger().With("action", "update_cloud_resource")

	// Extract required parameters
	resourceId, _ := params["resource_id"].(string)
	serviceName, _ := params["service_name"].(string)
	region, _ := params["region"].(string)

	if resourceId == "" || serviceName == "" {
		return nil, fmt.Errorf("update_cloud_resource action requires resource_id and service_name parameters")
	}

	// Azure resource IDs are case-insensitive but the ARM API and Event Grid topics
	// return them with inconsistent casing. We use the lowercased form as the
	// dedup key (external_resource_id) so realtime and bulk-sync upserts collide
	// on the same row instead of creating duplicates.
	externalResourceId := strings.ToLower(resourceId)

	// Extract optional parameters
	resourceType, _ := params["resource_type"].(string)
	newStatus, _ := params["new_status"].(string)
	updateLastSeen, _ := params["update_last_seen"].(bool)
	updateMeta, _ := params["update_meta"].(bool)
	metaUpdates, _ := params["meta_updates"].(map[string]interface{})
	statusMapping, _ := params["status_mapping"].(map[string]interface{})

	logger.Info("update_cloud_resource: updating resource",
		"resourceId", resourceId,
		"serviceName", serviceName,
		"resourceType", resourceType,
		"region", region,
		"newStatus", newStatus,
		"accountNumber", account.AccountNumber)

	// Get account metadata (UUID) from cache
	accountID, tenantID, found := GetAzureAccountMetadata(account.AccountNumber)
	if !found {
		logger.Error("update_cloud_resource: account metadata not found in cache", "accountNumber", account.AccountNumber)
		return nil, fmt.Errorf("update_cloud_resource: account metadata not found in cache for account %s", account.AccountNumber)
	}
	logger.Info("update_cloud_resource: found account metadata", "accountID", accountID, "tenantID", tenantID)

	// Get database manager
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		logger.Error("update_cloud_resource: unable to get database manager", "error", err)
		return nil, fmt.Errorf("update_cloud_resource: failed to get database manager: %w", err)
	}

	// Determine final status - apply status mapping if provided
	finalStatus := newStatus
	if len(statusMapping) > 0 && newStatus != "" {
		if mappedStatusInterface, ok := statusMapping[newStatus]; ok {
			if mappedStatus, ok := mappedStatusInterface.(string); ok {
				finalStatus = mappedStatus
			}
		}
	}

	// Fetch resource details from Azure to get accurate information
	var resourceName string
	var actualResourceType string
	var resourceTags = "{}"
	var resourceMeta = "{}"

	if p.azureProvider != nil {
		resourceResp, err := p.azureProvider.ListResources(ctx, account, providers.ListResourceRequest{
			ServiceName: serviceName,
			Regions:     []string{region},
			ResourceIds: []string{resourceId},
		})
		if err != nil {
			logger.Warn("update_cloud_resource: failed to fetch resource details, will query existing record", "error", err)
			// Query existing record to get the correct type AND meta. The lookup
			// uses (account, external_resource_id) so it matches the same
			// natural key the UPSERT below conflicts on, and benefits from the
			// indexed lowercased external_resource_id column. Reading existing
			// meta lets us merge the realtime param updates on top of the rich
			// bulk-sync meta instead of overwriting it (the prior behavior
			// wiped ~21 KB of resource detail every time a write event landed
			// for a service whose ARM catalog lookup returned "unsupported
			// operation").
			var existingType *string
			var existingMetaStr *string
			queryExisting := `SELECT type, meta::text FROM cloud_resourses WHERE account = $1 AND external_resource_id = $2 LIMIT 1`
			row, queryErr := dbms.QueryRow(queryExisting, accountID, externalResourceId)
			if queryErr == nil {
				switch scanErr := row.Scan(&existingType, &existingMetaStr); scanErr {
				case nil:
					if existingType != nil {
						actualResourceType = *existingType
						logger.Info("update_cloud_resource: found existing resource type from database", "type", actualResourceType)
					} else {
						actualResourceType = resourceType
						logger.Info("update_cloud_resource: existing row had null type, using params type", "type", actualResourceType)
					}
					if existingMetaStr != nil && *existingMetaStr != "" && *existingMetaStr != "null" {
						resourceMeta = *existingMetaStr
					}
				case sql.ErrNoRows:
					// Expected for the first realtime event seen for a brand-new resource.
					actualResourceType = resourceType
					logger.Info("update_cloud_resource: no existing record found, using params type", "type", actualResourceType)
				default:
					actualResourceType = resourceType
					logger.Warn("update_cloud_resource: failed to scan existing record, using params type", "scanError", scanErr, "type", actualResourceType)
				}
			} else {
				actualResourceType = resourceType
				logger.Warn("update_cloud_resource: failed to query existing record, using params type", "queryError", queryErr, "type", actualResourceType)
			}
		} else if len(resourceResp.Items) > 0 {
			resource := resourceResp.Items[0]
			logger.Info("update_cloud_resource: fetched resource details",
				"resourceId", resource.Id,
				"name", resource.Name,
				"type", resource.Type)

			resourceName = resource.Name
			actualResourceType = resource.Type
			if region == "" {
				region = resource.Region
			}

			if len(resource.Tags) > 0 {
				tagsJsonBytes, err := json.Marshal(resource.Tags)
				if err == nil {
					resourceTags = string(tagsJsonBytes)
				} else {
					logger.Error("update_cloud_resource: failed to marshal resource tags", "error", err)
				}
			}

			if len(resource.Meta) > 0 {
				metaJsonBytes, err := json.Marshal(resource.Meta)
				if err == nil {
					resourceMeta = string(metaJsonBytes)
				} else {
					logger.Error("update_cloud_resource: failed to marshal resource meta", "error", err)
				}
			}
		} else {
			logger.Warn("update_cloud_resource: no resource found for resourceId, will use params only", "resourceId", resourceId)
			actualResourceType = resourceType
		}
	} else {
		logger.Warn("update_cloud_resource: Azure provider not available, using params only")
		actualResourceType = resourceType
	}

	// Fallback: derive name and type from the Azure resource ID itself when the
	// ARM list call failed, returned no items, or the catalog has no entry for
	// this service. Without this, rows ended up with empty name/type and were
	// hidden in the UI (filters require non-empty name/region/is_active=true).
	derivedName, derivedType := deriveAzureResourceNameAndType(resourceId)
	if resourceName == "" {
		resourceName = derivedName
	}
	if actualResourceType == "" {
		actualResourceType = derivedType
	}
	if actualResourceType == "" {
		actualResourceType = resourceType
	}

	// If we still have no resourceMeta (ARM list returned 0 items, or
	// p.azureProvider was nil), pull the existing row's meta from DB so the
	// param meta_updates merge below adds keys instead of replacing the rich
	// bulk-sync meta. The error branch above already handled this case. The
	// lookup uses (account, external_resource_id) to match the UPSERT's
	// conflict key and benefit from the indexed column.
	if resourceMeta == "" || resourceMeta == "{}" {
		var existingMetaStr *string
		row, queryErr := dbms.QueryRow(
			`SELECT meta::text FROM cloud_resourses WHERE account = $1 AND external_resource_id = $2 LIMIT 1`,
			accountID, externalResourceId,
		)
		if queryErr == nil {
			if scanErr := row.Scan(&existingMetaStr); scanErr == nil &&
				existingMetaStr != nil && *existingMetaStr != "" && *existingMetaStr != "null" {
				resourceMeta = *existingMetaStr
			}
		}
	}

	// Prepare meta JSON if meta updates are provided
	var paramMetaJson string
	if updateMeta && len(metaUpdates) > 0 {
		metaJsonBytes, err := json.Marshal(metaUpdates)
		if err != nil {
			logger.Error("update_cloud_resource: failed to marshal meta updates", "error", err)
			return nil, fmt.Errorf("update_cloud_resource: failed to marshal meta updates: %w", err)
		}
		paramMetaJson = string(metaJsonBytes)
	}

	// Merge fetched resource meta with param meta updates
	finalMetaJson := resourceMeta
	if paramMetaJson != "" && paramMetaJson != "{}" {
		if resourceMeta != "" && resourceMeta != "{}" {
			// Merge both JSON objects
			var existingMeta map[string]interface{}
			var newMeta map[string]interface{}
			if err := json.Unmarshal([]byte(resourceMeta), &existingMeta); err == nil {
				if err := json.Unmarshal([]byte(paramMetaJson), &newMeta); err == nil {
					for k, v := range newMeta {
						existingMeta[k] = v
					}
					mergedBytes, _ := json.Marshal(existingMeta)
					finalMetaJson = string(mergedBytes)
				}
			}
		} else {
			finalMetaJson = paramMetaJson
		}
	}

	// Build UPSERT query dynamically based on whether status is provided
	now := time.Now().UTC().Format(time.RFC3339)

	// is_active is true unless we explicitly know the resource is deleted.
	isActive := finalStatus != "Deleted"

	// Build INSERT columns and values
	insertColumns := []string{
		"tenant", "account", "cloud_provider", "resourse_id", "external_resource_id",
		"service_name", "region", "type", "name", "tags", "meta",
		"created_at", "updated_at", "first_seen", "last_seen", "is_active",
	}

	args := []interface{}{
		tenantID,           // $1: tenant
		accountID,          // $2: account
		"Azure",            // $3: cloud_provider
		externalResourceId, // $4: resourse_id (lowercased — Azure resource IDs are case-insensitive)
		externalResourceId, // $5: external_resource_id
		serviceName,        // $6: service_name
		region,             // $7: region
		actualResourceType, // $8: type
		resourceName,       // $9: name
		resourceTags,       // $10: tags
		finalMetaJson,      // $11: meta
		now,                // $12: created_at
		now,                // $13: updated_at
		now,                // $14: first_seen
		now,                // $15: last_seen
		isActive,           // $16: is_active
	}
	argIndex := 17

	// Only include status in INSERT if we have a non-empty value (to avoid FK constraint violations)
	if finalStatus != "" {
		insertColumns = append(insertColumns, "status")
		args = append(args, finalStatus)
		argIndex++
	}

	// Build placeholders for INSERT ($1, $2, $3, ...)
	insertPlaceholders := make([]string, len(insertColumns))
	for i := range insertColumns {
		insertPlaceholders[i] = fmt.Sprintf("$%d", i+1)
	}

	// Build UPDATE SET clauses. Each "guard" clause keeps the existing value when
	// the new one is empty so partial events don't blank out fields populated by
	// earlier events or by the bulk-sync path.
	updateSetClauses := []string{
		fmt.Sprintf("updated_at = $%d", argIndex),
		fmt.Sprintf("last_seen = CASE WHEN $%d THEN $15 ELSE cloud_resourses.last_seen END", argIndex+1),
		fmt.Sprintf("meta = CASE WHEN $%d THEN $11::jsonb ELSE cloud_resourses.meta END", argIndex+2),
		fmt.Sprintf("tags = CASE WHEN $%d != '{}' THEN $10::jsonb ELSE cloud_resourses.tags END", argIndex+3),
		fmt.Sprintf("name = CASE WHEN $%d != '' THEN $%d ELSE cloud_resourses.name END", argIndex+4, argIndex+4),
		"type = CASE WHEN $8 != '' THEN $8 ELSE cloud_resourses.type END",
		"service_name = $6",
		"region = CASE WHEN $7 != '' THEN $7 ELSE cloud_resourses.region END",
		"resourse_id = $4",
		"is_active = $16",
	}
	args = append(args,
		now,            // updated_at value for UPDATE
		updateLastSeen, // should update last_seen?
		updateMeta,     // should update meta?
		resourceTags,   // tags value for comparison
		resourceName,   // name value for UPDATE
	)
	argIndex += 5

	// Add status to UPDATE clause only if we have a non-empty value
	if finalStatus != "" {
		updateSetClauses = append(updateSetClauses, fmt.Sprintf("status = $%d", argIndex))
		args = append(args, finalStatus)
	}

	// Pre-reconcile: if a row exists with the same natural key (account, resourse_id, type, region, service_name)
	// but a different external_resource_id, update its external_resource_id so the ON CONFLICT below catches it.
	// Match resourse_id case-insensitively because legacy rows may carry the
	// original ARM/Event Grid casing while we now write a lowercased value.
	reconcileQuery := `UPDATE cloud_resourses SET external_resource_id = $1
		WHERE account = $2 AND LOWER(resourse_id) = LOWER($3) AND type = $4 AND region = $5 AND service_name = $6
		AND (external_resource_id IS NULL OR LOWER(external_resource_id) != $1)`
	_, _ = dbms.Exec(reconcileQuery, externalResourceId, accountID, resourceId, actualResourceType, region, serviceName)

	// Construct the full UPSERT query
	query := fmt.Sprintf(`
		INSERT INTO cloud_resourses (%s)
		VALUES (%s)
		ON CONFLICT (account, external_resource_id)
		DO UPDATE SET %s
		RETURNING id, resourse_id, COALESCE(status, '') as status, updated_at
	`,
		strings.Join(insertColumns, ", "),
		strings.Join(insertPlaceholders, ", "),
		strings.Join(updateSetClauses, ", "),
	)

	// Execute query
	var resultId string
	var resultResourceId string
	var resultStatus string
	var resultUpdatedAt string

	row, err := dbms.QueryRow(query, args...)

	if err != nil {
		logger.Error("update_cloud_resource: failed to execute upsert query", "error", err)
		return nil, fmt.Errorf("update_cloud_resource: failed to execute upsert: %w", err)
	}

	if err := row.Scan(&resultId, &resultResourceId, &resultStatus, &resultUpdatedAt); err != nil {
		logger.Error("update_cloud_resource: failed to scan upsert result", "error", err)
		return nil, fmt.Errorf("update_cloud_resource: failed to scan result: %w", err)
	}

	logger.Info("update_cloud_resource: successfully updated resource",
		"resourceId", resultResourceId,
		"status", resultStatus,
		"dbId", resultId,
		"updated_at", resultUpdatedAt)

	return map[string]interface{}{
		"success":           true,
		"resource_id":       resultResourceId,
		"status":            resultStatus,
		"db_id":             resultId,
		"updated_at":        resultUpdatedAt,
		"update_type":       "upsert",
		"last_seen_updated": updateLastSeen,
		"meta_updated":      updateMeta,
	}, nil
}
