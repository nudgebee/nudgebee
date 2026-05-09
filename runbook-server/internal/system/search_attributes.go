package system

import (
	"context"
	"fmt"
	"log/slog"
	"nudgebee/runbook/internal/model"

	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"

	"go.temporal.io/api/operatorservice/v1"
)

// EnsureSearchAttributes checks if the required search attributes exist, and creates them if not.
func EnsureSearchAttributes(ctx context.Context, c client.Client, logger *slog.Logger, namespace string) error {
	// Use OperatorService to List and Add Search Attributes, consistent with Temporal CLI logic.

	// Define required attributes and their types
	requiredAttributes := map[string]enums.IndexedValueType{
		model.SearchAttrExecutionTags:    enums.INDEXED_VALUE_TYPE_KEYWORD_LIST, // Keyword list
		model.SearchAttrEventType:        enums.INDEXED_VALUE_TYPE_KEYWORD,
		model.SearchAttrTenantID:         enums.INDEXED_VALUE_TYPE_KEYWORD,
		model.SearchAttrAccountID:        enums.INDEXED_VALUE_TYPE_KEYWORD,
		model.SearchAttrWorkflowID:       enums.INDEXED_VALUE_TYPE_KEYWORD,
		model.SearchAttrEventID:          enums.INDEXED_VALUE_TYPE_KEYWORD,
		model.SearchAttrWorkflowTrigger:  enums.INDEXED_VALUE_TYPE_KEYWORD,
		model.SearchAttrTriggeredBy:      enums.INDEXED_VALUE_TYPE_KEYWORD,
		model.SearchAttrParentWorkflowID: enums.INDEXED_VALUE_TYPE_KEYWORD,
	}

	operatorClient := c.OperatorService()

	// 1. List existing search attributes
	listReq := &operatorservice.ListSearchAttributesRequest{
		Namespace: namespace,
	}
	resp, err := operatorClient.ListSearchAttributes(ctx, listReq)
	if err != nil {
		return fmt.Errorf("failed to list search attributes via OperatorService: %w", err)
	}

	missingAttributes := make(map[string]enums.IndexedValueType)
	for name, typ := range requiredAttributes {
		// Check in CustomAttributes
		if existingType, exists := resp.CustomAttributes[name]; !exists {
			// Also check SystemAttributes just in case, though these are custom keys
			if _, sysExists := resp.SystemAttributes[name]; !sysExists {
				missingAttributes[name] = typ
			}
		} else if existingType != typ {
			logger.Warn("Search attribute exists but with different type", "name", name, "expected", typ, "actual", existingType)
		}
	}

	if len(missingAttributes) == 0 {
		logger.Info("All required search attributes are present")
		return nil
	}

	// 2. Add missing search attributes individually
	// Adding individually ensures that if one fails (e.g. type limit reached),
	// others can still be registered.
	for name, typ := range missingAttributes {
		logger.Info("Registering missing search attribute", "name", name, "type", typ)
		addReq := &operatorservice.AddSearchAttributesRequest{
			SearchAttributes: map[string]enums.IndexedValueType{
				name: typ,
			},
			Namespace: namespace,
		}

		_, err = operatorClient.AddSearchAttributes(ctx, addReq)
		if err != nil {
			logger.Error("Failed to add search attribute", "name", name, "error", err)
			// We continue to try others
		} else {
			logger.Info("Successfully registered search attribute", "name", name)
		}
	}

	return nil
}
